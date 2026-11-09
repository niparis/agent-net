package gateway

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// sevenFieldDrafts submits the seven fields of a full charter draft for the
// node of one session: three on the repo, four on the agent.
func sevenFieldDrafts(t *testing.T, s *Store, session string) {
	t.Helper()
	for _, q := range []protocol.CharterDraftRequest{
		{Level: "repo", Attr: "purpose", Value: "Demo service"},
		{Level: "repo", Attr: "maturity", Value: "early"},
		{Level: "repo", Attr: "refuses", Value: "live trading"},
		{Level: "agent", Attr: "accepts", Value: "bug fixes"},
		{Level: "agent", Attr: "refuses", Value: "deploys"},
		{Level: "agent", Attr: "escalate", Value: "the owner"},
		{Level: "agent", Attr: "feature_requests", Value: "ideas"},
	} {
		if _, err := s.SubmitCharterDraft(session, q, "h1"); err != nil {
			t.Fatalf("submit %s %s: %v", q.Level, q.Attr, err)
		}
	}
}

// later moves the store clock on, as the minute tick would over time.
func later(s *Store, d time.Duration) {
	old := s.now
	s.now = func() time.Time { return old().Add(d) }
}

func draftAsks(t *testing.T, s *Store) []protocol.Ask {
	t.Helper()
	asks, err := s.ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	return askByKind(asks, protocol.AskKindDraft)
}

func TestSevenFieldDraftsOpenOneAsk(t *testing.T) {
	s := teamStore(t)
	sevenFieldDrafts(t, s, draftSession(t, s, "demo"))
	asks := draftAsks(t, s)
	if len(asks) != 1 {
		t.Fatalf("want one draft ask, got %d: %+v", len(asks), asks)
	}
	a := asks[0]
	if a.Question != "Charter draft for demo: 7 fields" || a.Next != protocol.AskNextOwner || a.AboutID != "demo" {
		t.Fatalf("ask: %+v", a)
	}
	if len(a.Options) != 2 || a.Options[0] != "accept" || a.Options[1] != "reject" {
		t.Fatalf("options: %v", a.Options)
	}
	for _, want := range []string{"repo purpose", "Demo service", "repo maturity", "early", "agent accepts", "bug fixes", "agent feature_requests", "ideas"} {
		if !strings.Contains(a.Detail, want) {
			t.Fatalf("detail lacks %q:\n%s", want, a.Detail)
		}
	}
	// Reconcile again: still one ask, same row.
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	again := draftAsks(t, s)
	if len(again) != 1 || again[0].ID != a.ID {
		t.Fatalf("second reconcile: %+v", again)
	}
}

func TestMemberNodeDraftAskGoesToTheLead(t *testing.T) {
	s, asker, lead := forgeRig(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToLead})
	sevenFieldDrafts(t, s, asker)
	// The fields are still coming in: the lead hears nothing yet, and the owner
	// does not either.
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE sender=? AND recipient=?`, asker, lead).Scan(&n); err != nil || n != 0 {
		t.Fatalf("lead got %d requests while drafts were arriving, %v", n, err)
	}
	if asks := draftAsks(t, s); len(asks) != 0 {
		t.Fatalf("ask before the fields settled: %+v", asks)
	}
	later(s, 3*time.Minute)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	asks := draftAsks(t, s)
	if len(asks) != 1 {
		t.Fatalf("want one draft ask, got %+v", asks)
	}
	a := asks[0]
	if a.Next != lead || a.State != protocol.AskWithSomeone || !strings.HasPrefix(a.Ref, leadAskRefPrefix) {
		t.Fatalf("ask is not with the lead: %+v", a)
	}
	if inbox, err := s.ListInbox(); err != nil || len(inbox) != 0 {
		t.Fatalf("the owner's inbox has it: %+v %v", inbox, err)
	}
	// The lead has one request from the member, with every field in it.
	var body string
	if err := s.db.QueryRow(`SELECT COUNT(*),COALESCE(MAX(body),'') FROM messages WHERE sender=? AND recipient=?`, asker, lead).Scan(&n, &body); err != nil {
		t.Fatal(err)
	}
	if n != 1 || !strings.Contains(body, "Charter draft for demo: 7 fields") || !strings.Contains(body, "live trading") {
		t.Fatalf("lead message (%d): %s", n, body)
	}
	// Reconciling again sends nothing more, even after the lead has answered.
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	var msgID string
	if err := s.db.QueryRow(`SELECT id FROM messages WHERE sender=? AND recipient=?`, asker, lead).Scan(&msgID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: lead, Recipient: asker, ParentID: msgID, Body: "looks right"}); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE sender=? AND recipient=?`, asker, lead).Scan(&n); err != nil || n != 1 {
		t.Fatalf("lead got %d requests, %v", n, err)
	}
	if asks := draftAsks(t, s); len(asks) != 0 {
		t.Fatalf("answered ask came back: %+v", asks)
	}
	// A new draft after the answer asks the lead again.
	if _, err := s.SubmitCharterDraft(asker, protocol.CharterDraftRequest{Level: "agent", Attr: "rules", Value: "be brief"}, "h2"); err != nil {
		t.Fatal(err)
	}
	later(s, 3*time.Minute)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE sender=? AND recipient=?`, asker, lead).Scan(&n); err != nil || n != 2 {
		t.Fatalf("lead got %d requests after a new draft, %v", n, err)
	}
}

func TestLeadNodeDraftAskGoesToTheOwner(t *testing.T) {
	s, _, lead := forgeRig(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToLead})
	sevenFieldDrafts(t, s, lead)
	asks := draftAsks(t, s)
	if len(asks) != 1 || asks[0].Next != protocol.AskNextOwner || asks[0].Question != "Charter draft for other: 7 fields" {
		t.Fatalf("lead node ask: %+v", asks)
	}
}

func TestMemberDraftAskStaysWithOwnerWhenTeamAsksOwner(t *testing.T) {
	s, asker, _ := forgeRig(t)
	sevenFieldDrafts(t, s, asker)
	asks := draftAsks(t, s)
	if len(asks) != 1 || asks[0].Next != protocol.AskNextOwner {
		t.Fatalf("asks: %+v", asks)
	}
}

func TestPerFieldDraftAsksAreGroupedOnce(t *testing.T) {
	s := teamStore(t)
	sid := draftSession(t, s, "demo")
	now := s.now().UnixNano()
	var ids []int64
	for i, f := range [][2]string{{"repo", "purpose"}, {"repo", "maturity"}, {"agent", "accepts"}} {
		res, err := s.db.Exec(`INSERT INTO charter_drafts(level,scope,attr,value,source,source_hash,drafted_by,created_at) VALUES(?,?,?,?,'AGENTS.md','h',?,?)`,
			f[0], "demo", f[1], fmt.Sprintf("value %d", i), sid, now)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		ids = append(ids, id)
		if _, err := s.createInboxItem(s.db, protocol.InboxItem{
			Kind: protocol.AskKindDraft, SessionID: gatewayAsker, FromKind: protocol.AskFromGateway, Node: "demo", AskerName: "charter draft",
			SourceRef: fmt.Sprintf("draft:%d", id), AboutKind: protocol.AskAboutProject, AboutID: "demo",
			Text: "old per-field ask", Options: []string{"accept", "reject"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A per-field ask whose draft is gone is closed as resolved, not grouped.
	if _, err := s.createInboxItem(s.db, protocol.InboxItem{
		Kind: protocol.AskKindDraft, SessionID: gatewayAsker, FromKind: protocol.AskFromGateway, Node: "demo",
		SourceRef: "draft:9999", AboutKind: protocol.AskAboutProject, AboutID: "demo", Text: "stale", Options: []string{"accept", "reject"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	asks := draftAsks(t, s)
	if len(asks) != 1 || asks[0].Question != "Charter draft for demo: 3 fields" || strings.HasPrefix(asks[0].Ref, "draft:") {
		t.Fatalf("after migration: %+v", asks)
	}
	for _, id := range ids {
		status, answer, _ := askRow(t, s, `source_ref=?`, fmt.Sprintf("draft:%d", id))
		if status != "closed" || answer != protocol.AskReasonGrouped {
			t.Fatalf("draft:%d: %q %q", id, status, answer)
		}
	}
	if _, answer, _ := askRow(t, s, `source_ref='draft:9999'`); answer != protocol.AskReasonResolved {
		t.Fatalf("stale ask closed as %q", answer)
	}
	// The drafts themselves are untouched: nothing is lost.
	drafts, err := s.PendingCharterDrafts()
	if err != nil || len(drafts) != 3 {
		t.Fatalf("drafts: %+v %v", drafts, err)
	}
	// A second run changes nothing.
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if again := draftAsks(t, s); len(again) != 1 || again[0].ID != asks[0].ID {
		t.Fatalf("second run: %+v", again)
	}
}

func TestAcceptingTheGroupedAskAppliesEveryField(t *testing.T) {
	s := teamStore(t)
	sevenFieldDrafts(t, s, draftSession(t, s, "demo"))
	a := draftAsks(t, s)[0]
	if _, err := s.AnswerInboxAs(a.ID, "accept", "owner"); err != nil {
		t.Fatal(err)
	}
	c, err := s.EffectiveCharter("demo", "demo", "")
	if err != nil || c.Purpose != "Demo service" || c.Maturity != "early" {
		t.Fatalf("charter: %+v %v", c, err)
	}
	var accepted int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM charter_drafts WHERE status='accepted'`).Scan(&accepted); err != nil || accepted != 7 {
		t.Fatalf("accepted %d: %v", accepted, err)
	}
	if asks := draftAsks(t, s); len(asks) != 0 {
		t.Fatalf("ask still open: %+v", asks)
	}
}

func TestRejectingTheGroupedAskRejectsEveryField(t *testing.T) {
	s := teamStore(t)
	sevenFieldDrafts(t, s, draftSession(t, s, "demo"))
	a := draftAsks(t, s)[0]
	if _, err := s.AnswerInboxAs(a.ID, "reject", "cto"); err != nil {
		t.Fatal(err)
	}
	var rejected int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM charter_drafts WHERE status='rejected'`).Scan(&rejected); err != nil || rejected != 7 {
		t.Fatalf("rejected %d: %v", rejected, err)
	}
	if c, _ := s.EffectiveCharter("demo", "demo", ""); c.Purpose != "" {
		t.Fatalf("a rejected draft took effect: %+v", c)
	}
	if asks := draftAsks(t, s); len(asks) != 0 {
		t.Fatalf("ask still open: %+v", asks)
	}
}

func TestOneFieldOfTheGroupedAskCanBeDecidedAlone(t *testing.T) {
	s := teamStore(t)
	sevenFieldDrafts(t, s, draftSession(t, s, "demo"))
	a := draftAsks(t, s)[0]
	if !strings.Contains(a.Detail, "accept repo:purpose") {
		t.Fatalf("detail does not say how to decide one field:\n%s", a.Detail)
	}
	if _, err := s.AnswerInboxAs(a.ID, "accept repo:purpose", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInboxAs(a.ID, "reject agent:accepts", "owner"); err != nil {
		t.Fatal(err)
	}
	c, _ := s.EffectiveCharter("demo", "demo", "")
	if c.Purpose != "Demo service" || c.Maturity != "" {
		t.Fatalf("charter: %+v", c)
	}
	asks := draftAsks(t, s)
	if len(asks) != 1 || asks[0].ID != a.ID || asks[0].Question != "Charter draft for demo: 5 fields" || strings.Contains(asks[0].Detail, "bug fixes") {
		t.Fatalf("ask after two single decisions: %+v", asks)
	}
	if _, err := s.AnswerInboxAs(a.ID, "accept repo:nonsense", "owner"); err == nil {
		t.Fatal("unknown field accepted")
	}
	// The rest in one answer.
	if _, err := s.AnswerInboxAs(a.ID, "accept", "owner"); err != nil {
		t.Fatal(err)
	}
	if asks := draftAsks(t, s); len(asks) != 0 {
		t.Fatalf("ask still open: %+v", asks)
	}
}

func TestLearnedEdgeAskForAMemberGoesToTheLead(t *testing.T) {
	s, asker, lead := forgeRig(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToLead})
	now := s.now().UnixNano()
	for _, node := range []string{"demo", "other"} {
		if _, err := s.db.Exec(`INSERT INTO learned_suggestions(node,field,edge_kind,kind,phrase,evidence,status,severity,created_at,ready_at) VALUES(?,'edge','depends','add','x-'||?,'[]','pending','major',?,?)`,
			node, node, now, now-int64(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	asks, _ := s.ListAsks()
	learned := askByKind(asks, protocol.AskKindLearned)
	if len(learned) != 2 {
		t.Fatalf("learned asks: %+v", asks)
	}
	for _, a := range learned {
		switch a.AboutID {
		case "demo":
			if a.Next != lead || a.State != protocol.AskWithSomeone {
				t.Fatalf("member edge ask: %+v", a)
			}
		case "other":
			if a.Next != protocol.AskNextOwner {
				t.Fatalf("lead edge ask: %+v", a)
			}
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE sender=? AND recipient=?`, asker, lead).Scan(&n); err != nil || n != 1 {
		t.Fatalf("lead requests: %d %v", n, err)
	}
	// No duplicate on the next run.
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE sender=? AND recipient=?`, asker, lead).Scan(&n); err != nil || n != 1 {
		t.Fatalf("lead requests after second run: %d %v", n, err)
	}
	if asks, _ = s.ListAsks(); len(askByKind(asks, protocol.AskKindLearned)) != 2 {
		t.Fatalf("second run changed the asks: %+v", asks)
	}
}

func TestLearnedFieldAskForAMemberStaysWithTheOwner(t *testing.T) {
	s, _, _ := forgeRig(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToLead})
	now := s.now().UnixNano()
	if _, err := s.db.Exec(`INSERT INTO learned_suggestions(node,field,edge_kind,kind,phrase,evidence,status,severity,created_at,ready_at) VALUES('demo','accepts','','add','billing','[]','pending','major',?,?)`, now, now-int64(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	asks, _ := s.ListAsks()
	l := askByKind(asks, protocol.AskKindLearned)
	if len(l) != 1 || l[0].Next != protocol.AskNextOwner {
		t.Fatalf("learned field ask: %+v", asks)
	}
}
