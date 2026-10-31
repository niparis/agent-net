package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func learnedProjects(t *testing.T) (*Store, *clock, string, string) {
	t.Helper()
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project=CASE id WHEN ? THEN 'app' ELSE 'ops' END WHERE id IN (?,?)`, a, a, b); err != nil {
		t.Fatal(err)
	}
	return s, c, a, b
}
func learnedRead(t *testing.T, s *Store, ready bool) []LearnedSuggestion {
	t.Helper()
	v, err := s.LearnedSuggestions(ready)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func learnedFind(v []LearnedSuggestion, node, field, phrase string) *LearnedSuggestion {
	for i := range v {
		if v[i].Node == node && v[i].Field == field && v[i].Phrase == phrase {
			return &v[i]
		}
	}
	return nil
}
func TestLearnedOwnerMergeAndQuiet(t *testing.T) {
	s, c, a, b := learnedProjects(t)
	for i := 0; i < 2; i++ {
		if err := s.RememberOwnerPrompt(a, "ops: backups are his job"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := send(s, a, b, "", ""); err != nil {
			t.Fatal(err)
		}
		c.t = c.t.Add(time.Minute)
	}
	all := learnedRead(t, s, false)
	for _, x := range [][3]string{{"ops", "accepts", "backups"}, {"app", "needs", "ops for backups"}} {
		v := learnedFind(all, x[0], x[1], x[2])
		if v == nil || len(v.Evidence) != 2 || v.Status != "pending" {
			t.Fatalf("suggestions: %+v", all)
		}
	}
	if v := learnedRead(t, s, true); len(v) != 0 {
		t.Fatalf("ready early: %+v", v)
	}
	c.t = c.t.Add(time.Minute)
	if v := learnedRead(t, s, true); len(v) != 2 {
		t.Fatalf("not ready: %+v", v)
	}
}
func learnedOutcome(t *testing.T, s *Store, a, b, outcome, reason, redirect string) {
	t.Helper()
	m, _, err := send(s, a, b, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: m.ID, Body: "reply", ReplyOutcome: protocol.ReplyOutcome{Outcome: outcome, Reason: reason, RedirectTo: redirect}})
	if err != nil {
		t.Fatal(err)
	}
}
func TestLearnedRedirectAndWindow(t *testing.T) {
	s, c, a, b := learnedProjects(t)
	if err := s.AddArchitect("infra", "owner"); err != nil {
		t.Fatal(err)
	}
	learnedOutcome(t, s, a, b, "redirect", "backups belongs to infra", "infra")
	c.t = c.t.Add(8 * 24 * time.Hour)
	learnedOutcome(t, s, a, b, "redirect", "backups belongs to infra", "infra")
	if v := learnedRead(t, s, false); len(v) != 0 {
		t.Fatalf("old redirect counted: %+v", v)
	}
	learnedOutcome(t, s, a, b, "redirect", "backups belongs to infra", "infra")
	v := learnedRead(t, s, false)
	for _, x := range [][3]string{{"ops", "needs", "infra for backups"}, {"ops", "edge", "infra"}} {
		p := learnedFind(v, x[0], x[1], x[2])
		if p == nil || len(p.Evidence) != 2 {
			t.Fatalf("redirects: %+v", v)
		}
	}
}
func TestLearnedRefusal(t *testing.T) {
	s, _, a, b := learnedProjects(t)
	for i := 0; i < 2; i++ {
		learnedOutcome(t, s, a, b, "reject", "deployments", "")
	}
	if v := learnedRead(t, s, false); len(v) != 0 {
		t.Fatalf("too early: %+v", v)
	}
	learnedOutcome(t, s, a, b, "reject", "deployments", "")
	v := learnedRead(t, s, false)
	p := learnedFind(v, "ops", "refuses", "deployments")
	if p == nil || len(p.Evidence) != 3 || p.Severity != "major" {
		t.Fatalf("refusal: %+v", v)
	}
}
func TestLearnedMessageFlow(t *testing.T) {
	s, c, a, b := learnedProjects(t)
	deliver := func(from, to string) {
		t.Helper()
		m, _, err := send(s, from, to, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if err = s.ApplyDelivery(m.ID, protocol.StateDelivered, ""); err != nil {
			t.Fatal(err)
		}
	}
	deliver(a, b)
	c.t = c.t.Add(8 * 24 * time.Hour)
	for i := 0; i < 4; i++ {
		deliver(a, b)
	}
	if v := learnedRead(t, s, false); len(v) != 0 {
		t.Fatalf("old message counted: %+v", v)
	}
	deliver(b, a)
	v := learnedRead(t, s, false)
	p := learnedFind(v, "ops", "edge", "app")
	if p == nil || len(p.Evidence) != 5 {
		t.Fatalf("flow: %+v", v)
	}
}

func TestLearnedNamedOwnerAndOtherRecipient(t *testing.T) {
	s, _, a, b := learnedProjects(t)
	if err := s.AddArchitect("infra", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberOwnerPrompt(a, "backups belongs to infra"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := send(s, a, b, "", ""); err != nil {
		t.Fatal(err)
	}
	v := learnedRead(t, s, false)
	if learnedFind(v, "infra", "accepts", "backups") == nil || learnedFind(v, "app", "needs", "infra for backups") == nil || learnedFind(v, "ops", "accepts", "backups") != nil {
		t.Fatalf("named target: %+v", v)
	}
}
func TestLearnedFlowMergesBothDirections(t *testing.T) {
	s, _, a, b := learnedProjects(t)
	for i := 0; i < 6; i++ {
		from, to := a, b
		if i == 5 {
			from, to = b, a
		}
		m, _, err := send(s, from, to, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if err = s.ApplyDelivery(m.ID, protocol.StateDelivered, ""); err != nil {
			t.Fatal(err)
		}
	}
	v := learnedRead(t, s, false)
	if len(v) != 1 || len(v[0].Evidence) != 6 {
		t.Fatalf("bidirectional flow: %+v", v)
	}
}
func TestLearnedFlowExistingEdge(t *testing.T) {
	s, _, a, b := learnedProjects(t)
	if err := s.SetEdge(protocol.Edge{From: "ops", To: "app", Kind: "depends"}, "owner"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		m, _, err := send(s, a, b, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if err = s.ApplyDelivery(m.ID, protocol.StateDelivered, ""); err != nil {
			t.Fatal(err)
		}
	}
	if v := learnedRead(t, s, false); len(v) != 0 {
		t.Fatalf("existing edge: %+v", v)
	}
}
func TestLearnedRefusalWindowAndDistinctRequest(t *testing.T) {
	s, c, a, b := learnedProjects(t)
	learnedOutcome(t, s, a, b, "reject", "deployments", "")
	c.t = c.t.Add(8 * 24 * time.Hour)
	learnedOutcome(t, s, a, b, "reject", "deployments", "")
	m, _, err := send(s, a, b, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		_, _, err = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: m.ID, Body: "reply", ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "deployments"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if v := learnedRead(t, s, false); len(v) != 0 {
		t.Fatalf("old or duplicate refusal counted: %+v", v)
	}
	learnedOutcome(t, s, a, b, "reject", "deployments", "")
	if v := learnedRead(t, s, false); len(v) != 1 || len(v[0].Evidence) != 3 {
		t.Fatalf("recent refusals: %+v", v)
	}
}

func TestLearnedOutcomeUsesProfileProjects(t *testing.T) {
	s, _, a, b := learnedProjects(t)
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project='' WHERE id IN (?,?)`, a, b); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app", "ops"} {
		if err := s.AddArchitect(name, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO profiles(session_id,project,updated_at) VALUES(?, 'app', 0),(?, 'ops', 0)`, a, b); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		learnedOutcome(t, s, a, b, "reject", "deployments", "")
	}
	if v := learnedRead(t, s, false); learnedFind(v, "ops", "refuses", "deployments") == nil {
		t.Fatalf("profile projects: %+v", v)
	}
}

func TestLearnedPrunesOldFlowEvidence(t *testing.T) {
	s, c, a, b := learnedProjects(t)
	m, _, err := send(s, a, b, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyDelivery(m.ID, protocol.StateDelivered, ""); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(8 * 24 * time.Hour)
	if _, err = s.Prune(Retention{}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM learned_message_flow`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("old flow rows=%d err=%v", n, err)
	}
}

func TestLearnedOwnerCanAssignSendingProject(t *testing.T) {
	s, _, a, b := learnedProjects(t)
	if err := s.RememberOwnerPrompt(a, "backups belongs to app"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := send(s, a, b, "", ""); err != nil {
		t.Fatal(err)
	}
	v := learnedRead(t, s, false)
	if len(v) != 1 || learnedFind(v, "app", "accepts", "backups") == nil {
		t.Fatalf("self assignment: %+v", v)
	}
}
