package gateway

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoForTest registers the one CTO session of a store and returns it.
func ctoForTest(t *testing.T, s *Store, node string) string {
	t.Helper()
	cto, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	return cto
}

// secondSessionOf makes another session of the Agent that owns the session.
func secondSessionOf(t *testing.T, s *Store, lead, native string) string {
	t.Helper()
	var agent, repo, node string
	if err := s.db.QueryRow(`SELECT COALESCE(agent_id,''),COALESCE(repo_id,''),node_id FROM sessions WHERE id=?`, lead).Scan(&agent, &repo, &node); err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: native, Name: native,
		Repo: protocol.RepoInfo{Root: "/w/" + repo, Project: repo}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET agent_id=? WHERE id=?`, agent, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// The live tenebrae case: two sessions of one lead Agent, one pinned. Only the
// pinned session is told it leads, and only it receives a redirect.
func TestOnlyPinnedLeadSessionLeads(t *testing.T) {
	r := newPlaceRig(t)
	second := secondSessionOf(t, r.s, r.lead, "homelab-agent 2")
	if _, err := r.s.db.Exec(`UPDATE agents SET lead_session=? WHERE id='other'`, r.lead); err != nil {
		t.Fatal(err)
	}
	if got, err := r.s.placeText(r.lead); err != nil || !strings.Contains(got, "You lead team Alpha") {
		t.Fatalf("pinned lead place: %q %v", got, err)
	}
	got, err := r.s.placeText(second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "You lead") {
		t.Fatalf("the second session was told it leads: %q", got)
	}
	if !strings.Contains(got, "You are a member of team Alpha; its lead session is forge-lead.") {
		t.Fatalf("the second session is not told its lead session: %q", got)
	}
	m, _, note, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outside, Recipient: second, Body: "deploy it"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != r.lead {
		t.Fatalf("message went to %s, want the pinned session %s (note %q)", m.To, r.lead, note)
	}
	if !strings.Contains(m.Body, "You lead ") {
		t.Fatalf("the pinned session was not told it leads: %q", m.Body)
	}
}

// With no pin nothing is the lead: the request is delivered as addressed and
// the receipt says the lead has no lead session.
func TestLeadWithoutPinDeliversAsAddressed(t *testing.T) {
	r := newPlaceRig(t)
	second := secondSessionOf(t, r.s, r.lead, "homelab-agent 2")
	if _, err := r.s.db.Exec(`UPDATE agents SET lead_session=NULL WHERE id='other'`); err != nil {
		t.Fatal(err)
	}
	if got, err := r.s.placeText(second); err != nil || strings.Contains(got, "You lead") {
		t.Fatalf("unpinned session place: %q %v", got, err)
	}
	m, _, note, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outside, Recipient: second, Body: "deploy it"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != second || !strings.Contains(note, "no lead session") || strings.Contains(note, "Redirected") {
		t.Fatalf("message went to %s with note %q", m.To, note)
	}
}

// The session list's star follows the pin: only the pinned session of a lead
// Agent carries repo_lead and team_lead.
func TestOnlyPinnedLeadSessionCarriesTheLeadFlags(t *testing.T) {
	s := teamStore(t)
	lead := sessionID(t, s, "n-demo")
	second := secondSessionOf(t, s, lead, "demo-second")
	agent := protocol.GraphRef{Kind: "agent", ID: "demo"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Owner: &agent})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})
	if _, err := s.db.Exec(`UPDATE agents SET lead_session=? WHERE id='demo'`, lead); err != nil {
		t.Fatal(err)
	}
	pinned, _, err := s.Agent(lead)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := s.Agent(second)
	if err != nil {
		t.Fatal(err)
	}
	if !pinned.RepoLead || !pinned.TeamLead {
		t.Fatalf("pinned session flags: repo_lead=%v team_lead=%v", pinned.RepoLead, pinned.TeamLead)
	}
	if other.RepoLead || other.TeamLead {
		t.Fatalf("the second session carries lead flags: repo_lead=%v team_lead=%v", other.RepoLead, other.TeamLead)
	}
}

// A second session on a lead Agent with no pin does not become the lead: the
// CTO is asked which session leads.
func TestSecondSessionWithoutPinAsksCTO(t *testing.T) {
	r := newPlaceRig(t)
	var node string
	if err := r.s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, r.lead).Scan(&node); err != nil {
		t.Fatal(err)
	}
	cto := ctoForTest(t, r.s, node)
	if _, err := r.s.db.Exec(`UPDATE agents SET lead_session=NULL WHERE id='other'`); err != nil {
		t.Fatal(err)
	}
	secondSessionOf(t, r.s, r.lead, "homelab-agent 2")
	if got := agentLeadSession(t, r.s, "other"); got != "" {
		t.Fatalf("the new session became the lead: %q", got)
	}
	var next string
	if err := r.s.db.QueryRow(`SELECT next_who FROM inbox_items WHERE source_ref=? AND status='open'`, "lead_session:other").Scan(&next); err != nil {
		t.Fatalf("no CTO question for the second session: %v", err)
	}
	if next != cto {
		t.Fatalf("question next_who=%q, want cto %q", next, cto)
	}
}

// A lead_set that names an Agent with exactly one session pins that session.
func TestLeadSetAgentPinsItsOnlySession(t *testing.T) {
	s := teamStore(t)
	solo := agentSession(t, s, "demo", "solo")
	if _, err := s.db.Exec(`UPDATE agents SET lead_session=NULL WHERE id='solo'`); err != nil {
		t.Fatal(err)
	}
	agent := protocol.GraphRef{Kind: "agent", ID: "solo"}
	if err := s.SetLead(protocol.LeadSetRequest{Ref: protocol.GraphRef{Kind: "repo", ID: "demo"}, Lead: &agent}, "owner", false); err != nil {
		t.Fatal(err)
	}
	if got := agentLeadSession(t, s, "solo"); got != solo {
		t.Fatalf("solo lead_session=%q, want %q", got, solo)
	}
}

// A lead_set that names an Agent with several sessions pins none and asks the
// CTO, not the owner, which session leads it.
func TestLeadSetAgentWithSeveralSessionsAsksCTO(t *testing.T) {
	s := teamStore(t)
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions LIMIT 1`).Scan(&node); err != nil {
		t.Fatal(err)
	}
	cto := ctoForTest(t, s, node)
	first := agentSession(t, s, "demo", "multi")
	second := secondSessionOf(t, s, first, "multi-two")
	if _, err := s.db.Exec(`UPDATE sessions SET name='multi-one' WHERE id=?`, first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET name='multi-two' WHERE id=?`, second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE agents SET lead_session=NULL WHERE id='multi'`); err != nil {
		t.Fatal(err)
	}
	agent := protocol.GraphRef{Kind: "agent", ID: "multi"}
	if err := s.SetLead(protocol.LeadSetRequest{Ref: protocol.GraphRef{Kind: "repo", ID: "demo"}, Lead: &agent}, "owner", false); err != nil {
		t.Fatal(err)
	}
	if got := agentLeadSession(t, s, "multi"); got != "" {
		t.Fatalf("multi was pinned to %q with two sessions", got)
	}
	var text, next, source string
	if err := s.db.QueryRow(`SELECT text,next_who,source_ref FROM inbox_items WHERE source_ref=? AND status='open'`,
		"lead_session:multi").Scan(&text, &next, &source); err != nil {
		t.Fatalf("no CTO question: %v", err)
	}
	if next != cto || !strings.Contains(text, "Which session leads multi?") ||
		!strings.Contains(text, "multi-one") || !strings.Contains(text, "multi-two") {
		t.Fatalf("question next=%q text=%q", next, text)
	}
	// The CTO answers with the session it picks.
	answer := protocol.GraphRef{Kind: "session", ID: second}
	if err := s.SetLead(protocol.LeadSetRequest{Ref: agent, Lead: &answer}, "cto", false); err != nil {
		t.Fatal(err)
	}
	if got := agentLeadSession(t, s, "multi"); got != second {
		t.Fatalf("after the CTO answer lead_session=%q, want %q", got, second)
	}
}

// A new session of a lead Agent leaves an existing pin alone.
func TestNewSessionKeepsTheLeadPin(t *testing.T) {
	r := newPlaceRig(t)
	if _, err := r.s.db.Exec(`UPDATE agents SET lead_session=? WHERE id='other'`, r.lead); err != nil {
		t.Fatal(err)
	}
	secondSessionOf(t, r.s, r.lead, "homelab-agent 2")
	if got := agentLeadSession(t, r.s, "other"); got != r.lead {
		t.Fatalf("pin moved to %q, want %q", got, r.lead)
	}
}

// The first session of a lead Agent with no pin is pinned when it appears.
func TestFirstSessionPinsTheLeadAgent(t *testing.T) {
	s := teamStore(t)
	if err := s.GraphLifecycle("agent.create", protocol.GraphLifecycleRequest{Name: "solo"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES('solo','solo',0,0,'solo')`); err != nil {
		t.Fatal(err)
	}
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions LIMIT 1`).Scan(&node); err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "solo-main", Repo: protocol.RepoInfo{Root: "/w/solo", Project: "solo"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := agentLeadSession(t, s, "solo"); got != id {
		t.Fatalf("solo lead_session=%q, want the new session %q", got, id)
	}
}

// Archiving the pinned session clears the pin. Other sessions left make the
// same CTO question.
func TestArchivingThePinnedLeadSessionAsksCTO(t *testing.T) {
	r := newPlaceRig(t)
	var node string
	if err := r.s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, r.lead).Scan(&node); err != nil {
		t.Fatal(err)
	}
	cto := ctoForTest(t, r.s, node)
	secondSessionOf(t, r.s, r.lead, "homelab-agent 2")
	if _, err := r.s.db.Exec(`UPDATE agents SET lead_session=? WHERE id='other'`, r.lead); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetSessionOffline(node, r.lead); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.ArchiveSessions(protocol.SessionsArchiveRequest{IDs: []string{r.lead}}); err != nil {
		t.Fatal(err)
	}
	if got := agentLeadSession(t, r.s, "other"); got != "" {
		t.Fatalf("archived session still pinned: %q", got)
	}
	var next string
	if err := r.s.db.QueryRow(`SELECT next_who FROM inbox_items WHERE source_ref=? AND status='open'`, "lead_session:other").Scan(&next); err != nil {
		t.Fatalf("no CTO question after the archive: %v", err)
	}
	if next != cto {
		t.Fatalf("question next_who=%q, want cto %q", next, cto)
	}
}

// Archiving the pinned session with no other session clears the pin and asks
// nothing.
func TestArchivingTheOnlyLeadSessionAsksNothing(t *testing.T) {
	r := newPlaceRig(t)
	var node string
	if err := r.s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, r.lead).Scan(&node); err != nil {
		t.Fatal(err)
	}
	ctoForTest(t, r.s, node)
	if _, err := r.s.db.Exec(`UPDATE agents SET lead_session=? WHERE id='other'`, r.lead); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetSessionOffline(node, r.lead); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.ArchiveSessions(protocol.SessionsArchiveRequest{IDs: []string{r.lead}}); err != nil {
		t.Fatal(err)
	}
	if got := agentLeadSession(t, r.s, "other"); got != "" {
		t.Fatalf("archived session still pinned: %q", got)
	}
	var count int
	if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE source_ref LIKE 'lead_session:%' AND status='open'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("%d CTO questions after the last session went", count)
	}
}

// repairFixture seeds the live shape: 38 lead Agents, 2 already pinned, 20
// with one session, 3 with two, 13 with none.
func repairFixture(t *testing.T, s *Store) (cto string) {
	t.Helper()
	node, _, err := s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	cto, _, err = s.upsertSession(s.db, node, protocol.SessionInfo{Harness: "cto", NativeID: "cto", Name: "CTO"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('cto_session',?,0) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, cto); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixNano()
	addAgent := func(name string) {
		if _, err := s.db.Exec(`INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(?,?,'',?,?,0)`, name, name, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES(?,?,?,?,?)`, name, name, now, now, name); err != nil {
			t.Fatal(err)
		}
	}
	addSession := func(agent, id string, online int) {
		if _, err := s.db.Exec(`INSERT INTO sessions(id,node_id,harness,native_id,name,status,last_seen,online,repo_id,agent_id)
			VALUES(?,?,?,?,?,'idle',?,?,?,?)`, id, node, "claude", id, id, now, online, agent, agent); err != nil {
			t.Fatal(err)
		}
	}
	// Two Agents with a pin already.
	for i := 0; i < 2; i++ {
		name := fmt.Sprintf("pinned-%02d", i)
		addAgent(name)
		session := fmt.Sprintf("s_1%015d", i)
		addSession(name, session, 1)
		if _, err := s.db.Exec(`UPDATE agents SET lead_session=? WHERE id=?`, session, name); err != nil {
			t.Fatal(err)
		}
	}
	// Twenty Agents with exactly one session.
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("one-%02d", i)
		addAgent(name)
		addSession(name, fmt.Sprintf("s_2%015d", i), i%2)
	}
	// Three Agents with two sessions. tenebrae has exactly one online.
	for i, name := range []string{"tenebrae", "ai-arch", "wbg-infra-game"} {
		addAgent(name)
		firstOnline, secondOnline := 0, 0
		switch name {
		case "tenebrae":
			firstOnline = 1
		case "wbg-infra-game":
			firstOnline, secondOnline = 1, 1
		}
		addSession(name, fmt.Sprintf("s_3%015d", i*2), firstOnline)
		addSession(name, fmt.Sprintf("s_3%015d", i*2+1), secondOnline)
	}
	// Thirteen lead Agents with no session.
	for i := 0; i < 13; i++ {
		addAgent(fmt.Sprintf("empty-%02d", i))
	}
	return cto
}

// The one-time repair pins a lone session, or the one online session, and asks
// the CTO about the rest.
func TestLeadSessionRepairMigration(t *testing.T) {
	s, _ := newTestStore(t)
	cto := repairFixture(t, s)
	version := migrationVersion(t, "Lead session repair")
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, version); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	pinned := 0
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agents WHERE COALESCE(lead_session,'')<>''`).Scan(&pinned); err != nil {
		t.Fatal(err)
	}
	if pinned != 23 {
		t.Fatalf("%d Agents have a lead session, want 23", pinned)
	}
	var tenebrae, aiArch string
	if err := s.db.QueryRow(`SELECT COALESCE(lead_session,'') FROM agents WHERE id='tenebrae'`).Scan(&tenebrae); err != nil {
		t.Fatal(err)
	}
	if tenebrae != "s_3000000000000000" {
		t.Fatalf("tenebrae lead_session=%q, want its one online session s_3000000000000000", tenebrae)
	}
	if err := s.db.QueryRow(`SELECT COALESCE(lead_session,'') FROM agents WHERE id='ai-arch'`).Scan(&aiArch); err != nil {
		t.Fatal(err)
	}
	if aiArch != "" {
		t.Fatalf("ai-arch was pinned to %q with no online session", aiArch)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE source_ref IN ('lead_session:ai-arch','lead_session:wbg-infra-game') AND next_who=? AND status='open'`, cto).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("%d CTO questions, want one for each of ai-arch and wbg-infra-game", count)
	}
	for _, name := range []string{"empty-00", "one-00"} {
		var pin string
		if err := s.db.QueryRow(`SELECT COALESCE(lead_session,'') FROM agents WHERE id=?`, name).Scan(&pin); err != nil {
			t.Fatal(err)
		}
		if name == "empty-00" && pin != "" {
			t.Fatalf("an Agent with no session was pinned to %q", pin)
		}
		if name == "one-00" && pin == "" {
			t.Fatal("the lone session of one-00 was not pinned")
		}
	}
}
