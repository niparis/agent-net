package gateway

import (
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func draftSession(t *testing.T, s *Store, project string) string {
	t.Helper()
	var id string
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_project=?`, project).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func mustAsk(t *testing.T, s *Store, sid, hash string) string {
	t.Helper()
	text, err := s.CharterDraftAsk(sid, hash)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestCharterDraftAskedOncePerAgentsMD(t *testing.T) {
	s := draftFolderStore(t)
	sid := draftSession(t, s, "demo")
	text := mustAsk(t, s, sid, "h1")
	for _, want := range []string{"AGENTS.md", "agent_charter_draft", "purpose", "maturity", "accepts", "escalate", "feature_requests"} {
		if !strings.Contains(text, want) {
			t.Fatalf("request lacks %q:\n%s", want, text)
		}
	}
	if again := mustAsk(t, s, sid, "h1"); again != "" {
		t.Fatalf("asked twice for the same AGENTS.md:\n%s", again)
	}
	// Another session of the same repo is not asked either.
	if again := mustAsk(t, s, draftSession(t, s, "demo"), "h1"); again != "" {
		t.Fatalf("asked again: %s", again)
	}
	if next := mustAsk(t, s, sid, "h2"); next == "" {
		t.Fatal("not asked after AGENTS.md changed")
	}
	if again := mustAsk(t, s, sid, "h2"); again != "" {
		t.Fatalf("asked twice for the changed file:\n%s", again)
	}
	// No AGENTS.md, no request.
	other := draftSession(t, s, "other")
	if text := mustAsk(t, s, other, ""); text != "" {
		t.Fatalf("asked without AGENTS.md:\n%s", text)
	}
}

func TestCharterDraftAskNamesOnlyMissingFields(t *testing.T) {
	s := draftFolderStore(t)
	for _, q := range []protocol.CharterSetRequest{
		{Level: "repo", Node: "demo", Attr: "purpose", Value: "Demo repo"},
		{Level: "repo", Node: "demo", Attr: "maturity", Value: "early"},
		{Level: "repo", Node: "demo", Attr: "refuses", Value: "secrets"},
		{Level: "agent", Node: "demo", Attr: "accepts", Value: "fixes"},
		{Level: "agent", Node: "demo", Attr: "refuses", Value: "deploys"},
		{Level: "agent", Node: "demo", Attr: "escalate", Value: "the owner"},
		{Level: "agent", Node: "demo", Attr: "feature_requests", Value: "ideas"},
		{Level: "agent", Node: "demo", Attr: "rules", Value: "be brief"},
	} {
		if err := s.SetLevelCharter(q, "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	// The charter is full, so only the project rules request may remain.
	if text := mustAsk(t, s, draftSession(t, s, "demo"), "h1"); strings.Contains(text, "Charter draft request") {
		t.Fatalf("asked for a full charter:\n%s", text)
	}
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "agent", Node: "demo", Attr: "rules", Value: ""}, "owner", false); err != nil {
		t.Fatal(err)
	}
	text := mustAsk(t, s, draftSession(t, s, "demo"), "h1")
	if !strings.Contains(text, "- rules:") || strings.Contains(text, "- accepts:") || strings.Contains(text, "- maturity:") || strings.Contains(text, "- purpose:") {
		t.Fatalf("request names the wrong fields:\n%s", text)
	}
}

func TestCharterDraftStaysSuggestionUntilAccepted(t *testing.T) {
	s := teamStore(t)
	sid := draftSession(t, s, "demo")
	res, err := s.SubmitCharterDraft(sid, protocol.CharterDraftRequest{Level: "repo", Attr: "purpose", Value: "Shared demo repo"}, "h1")
	if err != nil || res.ID == 0 || res.Status != "pending" {
		t.Fatalf("submit: %+v %v", res, err)
	}
	if c, _ := s.EffectiveCharter("demo", "demo", ""); c.Purpose != "" {
		t.Fatalf("draft took effect: %+v", c)
	}
	drafts, err := s.PendingCharterDrafts()
	if err != nil || len(drafts) != 1 || drafts[0].Source != "AGENTS.md" || drafts[0].Value != "Shared demo repo" {
		t.Fatalf("pending: %+v %v", drafts, err)
	}
	// The owner sees it in the learned list, as a draft with a negative ID.
	learned, err := s.OwnerLearned()
	if err != nil || len(learned) != 1 || learned[0].Origin != "draft" || learned[0].ID != -res.ID || learned[0].Level != "repo" || learned[0].Scope != "demo" || learned[0].NewValue != "Shared demo repo" || learned[0].Status != "pending" || learned[0].Severity != "major" {
		t.Fatalf("owner list: %+v %v", learned, err)
	}
}

func TestCharterDraftSubmitValidation(t *testing.T) {
	s := teamStore(t)
	sid := draftSession(t, s, "demo")
	for name, q := range map[string]protocol.CharterDraftRequest{
		"unknown level":      {Level: "global", Attr: "rules", Value: "x"},
		"agent attr on repo": {Level: "repo", Attr: "accepts", Value: "x"},
		"bad maturity":       {Level: "repo", Attr: "maturity", Value: "unknown"},
		"too long":           {Level: "repo", Attr: "purpose", Value: strings.Repeat("x", protocol.CharterValueMaxBytes+1)},
		"empty":              {Level: "repo", Attr: "purpose", Value: "  "},
		"not a team lead":    {Level: "team", Attr: "purpose", Value: "x"},
	} {
		if _, err := s.SubmitCharterDraft(sid, q, "h1"); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	// A field that already has a value is not drafted over.
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "repo", Node: "demo", Attr: "purpose", Value: "Owner text"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitCharterDraft(sid, protocol.CharterDraftRequest{Level: "repo", Attr: "purpose", Value: "Draft"}, "h1"); err == nil {
		t.Fatal("drafted over an owner value")
	}
}

func TestCharterDraftAcceptWritesAtItsLevelWithSource(t *testing.T) {
	s := teamStore(t)
	sid := draftSession(t, s, "demo")
	submit := func(level, attr, value string) int64 {
		res, err := s.SubmitCharterDraft(sid, protocol.CharterDraftRequest{Level: level, Attr: attr, Value: value}, "h1")
		if err != nil {
			t.Fatal(err)
		}
		return res.ID
	}
	repoID := submit("repo", "purpose", "Shared demo repo")
	maturityID := submit("repo", "maturity", "growing")
	agentID := submit("agent", "accepts", "Bug fixes in demo")
	if err := s.DecideCharterDraft(repoID, "accept", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideCharterDraft(maturityID, "accept", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideLearned(-agentID, "accept"); err != nil {
		t.Fatal(err)
	}
	c, err := s.EffectiveCharter("demo", "demo", "")
	if err != nil || c.Purpose != "Shared demo repo" || c.Maturity != "growing" || c.Accepts != "Bug fixes in demo" {
		t.Fatalf("effective: %+v %v", c, err)
	}
	src := *c.Sources
	if src["purpose"][0].Level != "repo" || src["purpose"][0].Scope != "demo" || src["accepts"][0].Level != "agent" {
		t.Fatalf("sources: %+v", src)
	}
	var status, source, by string
	if err := s.db.QueryRow(`SELECT status,source,decided_by FROM charter_drafts WHERE id=?`, repoID).Scan(&status, &source, &by); err != nil || status != "accepted" || source != "AGENTS.md" || by != "owner" {
		t.Fatalf("draft row: %s %s %s %v", status, source, by, err)
	}
	if left, _ := s.PendingCharterDrafts(); len(left) != 0 {
		t.Fatalf("still pending: %+v", left)
	}
	if err := s.DecideCharterDraft(repoID, "accept", "owner"); err == nil {
		t.Fatal("accepted twice")
	}
}

func TestCharterDraftRejectKeepsFieldEmptyAndIsNotResubmitted(t *testing.T) {
	s := teamStore(t)
	sid := draftSession(t, s, "demo")
	q := protocol.CharterDraftRequest{Level: "agent", Attr: "refuses", Value: "Never deploy"}
	res, err := s.SubmitCharterDraft(sid, q, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DecideLearned(-res.ID, "reject"); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.EffectiveCharter("demo", "demo", ""); c.Refuses != "" {
		t.Fatalf("rejected draft took effect: %+v", c)
	}
	again, err := s.SubmitCharterDraft(sid, q, "h2")
	if err != nil || again.Status != "rejected" {
		t.Fatalf("resubmit: %+v %v", again, err)
	}
	if left, _ := s.PendingCharterDrafts(); len(left) != 0 {
		t.Fatalf("rejected value came back: %+v", left)
	}
}

func TestCharterDraftSupersededWhenFieldGetsAValue(t *testing.T) {
	s := teamStore(t)
	sid := draftSession(t, s, "demo")
	if _, err := s.SubmitCharterDraft(sid, protocol.CharterDraftRequest{Level: "repo", Attr: "purpose", Value: "Draft"}, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "repo", Node: "demo", Attr: "purpose", Value: "Owner wrote this"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	if left, err := s.PendingCharterDrafts(); err != nil || len(left) != 0 {
		t.Fatalf("pending after owner write: %+v %v", left, err)
	}
	if c, _ := s.EffectiveCharter("demo", "demo", ""); c.Purpose != "Owner wrote this" {
		t.Fatalf("owner value lost: %+v", c)
	}
}

func TestTeamLeadDraftsTeamCharterFromMembers(t *testing.T) {
	s := teamStore(t)
	owner := protocol.GraphRef{Kind: "agent", ID: "demo"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Owner: &owner})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &protocol.GraphRef{Kind: "repo", ID: "other"}})
	lead, member := draftSession(t, s, "demo"), draftSession(t, s, "other")
	// Members have no charter yet, so there is nothing to draft from.
	if text := mustAsk(t, s, lead, "h1"); strings.Contains(text, "team") {
		t.Fatalf("team asked with empty members:\n%s", text)
	}
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "repo", Node: "other", Attr: "purpose", Value: "Other repo"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "repo", Node: "other", Attr: "refuses", Value: "No prod writes"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	text := mustAsk(t, s, lead, "h1")
	for _, want := range []string{"team Alpha", "agent_charter_draft", "purpose", "refuses", "other"} {
		if !strings.Contains(text, want) {
			t.Fatalf("team request lacks %q:\n%s", want, text)
		}
	}
	if again := mustAsk(t, s, lead, "h1"); strings.Contains(again, "team") {
		t.Fatalf("team asked twice:\n%s", again)
	}
	// A member session is never asked about the team.
	if text := mustAsk(t, s, member, ""); text != "" {
		t.Fatalf("member asked: %s", text)
	}
	if _, err := s.SubmitCharterDraft(member, protocol.CharterDraftRequest{Level: "team", Attr: "purpose", Value: "x"}, ""); err == nil {
		t.Fatal("a member drafted for the team")
	}
	res, err := s.SubmitCharterDraft(lead, protocol.CharterDraftRequest{Level: "team", Attr: "purpose", Value: "Keep the demo repos working"}, "")
	if err != nil {
		t.Fatal(err)
	}
	refuses, err := s.SubmitCharterDraft(lead, protocol.CharterDraftRequest{Level: "team", Attr: "refuses", Value: "No prod writes"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := s.EffectiveCharter("", "", "alpha"); c.Purpose != "" {
		t.Fatalf("team draft took effect: %+v", c)
	}
	if err = s.DecideCharterDraft(res.ID, "accept", "owner"); err != nil {
		t.Fatal(err)
	}
	if err = s.DecideCharterDraft(refuses.ID, "accept", "cto"); err != nil {
		t.Fatal(err)
	}
	c, err := s.EffectiveCharter("", "", "alpha")
	if err != nil || c.Purpose != "Keep the demo repos working" || !strings.Contains(c.Refuses, "No prod writes") {
		t.Fatalf("team charter: %+v %v", c, err)
	}
	if (*c.Sources)["purpose"][0].Level != "team" || (*c.Sources)["purpose"][0].Scope != "alpha" {
		t.Fatalf("sources: %+v", *c.Sources)
	}
}

// A team's charter request says "You lead team". The merger of the lead's
// project shares the lead's agent, so it must not get that request.
func TestTeamRequestNeverGoesToTheMerger(t *testing.T) {
	s := teamStore(t)
	owner := protocol.GraphRef{Kind: "agent", ID: "demo"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Owner: &owner})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &protocol.GraphRef{Kind: "repo", ID: "other"}})
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "repo", Node: "other", Attr: "purpose", Value: "Other repo"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "repo", Node: "other", Attr: "refuses", Value: "No prod writes"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	lead := draftSession(t, s, "demo")
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, lead).Scan(&node); err != nil {
		t.Fatal(err)
	}
	merger := mergerOfLead(t, s, node, lead, "merger")
	if got := mustAsk(t, s, merger, "h1"); strings.Contains(got, "You lead team") {
		t.Fatalf("the merger was asked for the team charter:\n%s", got)
	}
	if got := mustAsk(t, s, lead, "h1"); !strings.Contains(got, "You lead team Alpha") {
		t.Fatalf("the lead was not asked for the team charter:\n%s", got)
	}
}

// draftFolderStore is teamStore with each repo's folder recorded on the node,
// as `agent-net projects add` does. The repo's sessions work in that folder.
func draftFolderStore(t *testing.T) *Store {
	t.Helper()
	s := teamStore(t)
	if _, err := s.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) SELECT node_id,cwd,repo_project,0,0 FROM sessions WHERE repo_project<>'' AND cwd<>''`); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCharterDraftIsAskedOnlyInTheRepoFolder(t *testing.T) {
	s := draftFolderStore(t)
	var node, repo string
	if err := s.db.QueryRow(`SELECT node_id,id FROM sessions WHERE cwd='/w/demo'`).Scan(&node, &repo); err != nil {
		t.Fatal(err)
	}
	// An umbrella session in /w names the repo too, but its AGENTS.md is the
	// umbrella's. A draft from it would describe the umbrella, not the repo.
	umbrella, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "umbrella", Cwd: "/w",
		Repo: protocol.RepoInfo{Root: "/w", Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	// A draft asked before this fix left a row for the umbrella. It must not
	// block the ask to the repo session.
	if _, err := s.db.Exec(`INSERT INTO charter_draft_asks(level,scope,source_hash,asked_at,session_id) VALUES('repo','demo','h-umbrella',0,?)`, umbrella); err != nil {
		t.Fatal(err)
	}
	if text := mustAsk(t, s, umbrella, "h-umbrella-2"); text != "" {
		t.Fatalf("umbrella session was asked to draft the repo:\n%s", text)
	}
	text := mustAsk(t, s, repo, "h-repo")
	if !strings.Contains(text, "Read /w/demo/AGENTS.md") || strings.Contains(text, "Read AGENTS.md") {
		t.Fatalf("request does not name the repo's AGENTS.md:\n%s", text)
	}
	if again := mustAsk(t, s, repo, "h-repo"); again != "" {
		t.Fatalf("asked twice for the same AGENTS.md:\n%s", again)
	}
}

func TestCharterDraftWaitsForASessionInTheRepoFolder(t *testing.T) {
	s := draftFolderStore(t)
	if _, err := s.db.Exec(`UPDATE sessions SET cwd='/w' WHERE cwd='/w/demo'`); err != nil {
		t.Fatal(err)
	}
	var reasons []string
	s.logf = func(event string, kv ...any) {
		reasons = append(reasons, event+fmt.Sprint(kv...))
	}
	sid := draftSession(t, s, "demo")
	if text := mustAsk(t, s, sid, "h1"); text != "" {
		t.Fatalf("asked from outside the repo folder:\n%s", text)
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "outside the repo folder") {
		t.Fatalf("reason not logged once: %q", reasons)
	}
	if again := mustAsk(t, s, sid, "h1"); again != "" || len(reasons) != 1 {
		t.Fatalf("same skip asked or logged again: %q %q", again, reasons)
	}
}

func TestCharterDraftNeedsARecordedRepoFolder(t *testing.T) {
	s := draftFolderStore(t)
	if _, err := s.db.Exec(`DELETE FROM projects WHERE name='demo'`); err != nil {
		t.Fatal(err)
	}
	if text := mustAsk(t, s, draftSession(t, s, "demo"), "h1"); text != "" {
		t.Fatalf("asked for a repo with no recorded folder:\n%s", text)
	}
}
