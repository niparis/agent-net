package gateway

import (
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
