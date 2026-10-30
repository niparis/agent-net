package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestRequestRulePrecedence(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project=CASE id WHEN ? THEN 'consumer' ELSE 'provider' END WHERE id IN (?,?)`, a, a, b); err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		got, err := s.RequestRule(a, b)
		if err != nil || got != want {
			t.Fatalf("rule = %q, %v; want %q", got, err, want)
		}
	}
	check("normal")
	if err := s.SetEdge(protocol.Edge{From: "*", To: "provider", Kind: "depends", Rule: "ask"}, "owner"); err != nil {
		t.Fatal(err)
	}
	check("ask")
	if err := s.SetEdge(protocol.Edge{From: "consumer", To: "provider", Kind: "depends", Rule: "auto"}, "owner"); err != nil {
		t.Fatal(err)
	}
	check("auto")
}

func TestRequestRuleHoldsAtStorage(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project=CASE id WHEN ? THEN 'consumer' ELSE 'provider' END WHERE id IN (?,?)`, a, a, b); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEdge(protocol.Edge{From: "*", To: "provider", Kind: "depends", Rule: "ask"}, "owner"); err != nil {
		t.Fatal(err)
	}
	m, _, err := send(s, a, b, "held", "")
	if err != nil || m.State != "held" {
		t.Fatalf("message = %#v, %v", m, err)
	}
	n := mustNode(t, s, b)
	pending, err := s.Pending(n, true)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %#v, %v", pending, err)
	}
}

func TestHeldApprovalAndRejection(t *testing.T) {
	for _, action := range []string{"a", "A", "r", "R"} {
		t.Run(action, func(t *testing.T) {
			s, _ := newTestStore(t)
			a, b := twoSessions(t, s)
			if _, err := s.db.Exec(`UPDATE sessions SET repo_project=CASE id WHEN ? THEN 'consumer' ELSE 'provider' END WHERE id IN (?,?)`, a, a, b); err != nil {
				t.Fatal(err)
			}
			if err := s.SetEdge(protocol.Edge{From: "*", To: "provider", Kind: "depends", Rule: "ask"}, "owner"); err != nil {
				t.Fatal(err)
			}
			m, _, err := send(s, a, b, "request", "")
			if err != nil {
				t.Fatal(err)
			}
			list, err := s.HeldRequests()
			if err != nil || len(list) != 1 || list[0].ID != m.ID {
				t.Fatalf("held: %#v %v", list, err)
			}
			got, err := s.AnswerHeld(m.ID, action)
			if err != nil {
				t.Fatal(err)
			}
			if action == "a" || action == "A" {
				if got.State != protocol.StateStored || !got.OwnerApproved {
					t.Fatalf("approved: %#v", got)
				}
			} else if got.State != protocol.StateFailed {
				t.Fatalf("rejected: %#v", got)
			}
			if _, err := s.AnswerHeld(m.ID, action); err == nil {
				t.Fatal("answered twice")
			}
			if action == "A" || action == "R" {
				rule, err := s.RequestRule(a, b)
				want := "auto"
				if action == "R" {
					want = "never"
				}
				if err != nil || rule != want {
					t.Fatalf("rule %q %v, want %q", rule, err, want)
				}
			}
		})
	}
}

func TestHeldRequestExpires(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project='provider' WHERE id=?`, b); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEdge(protocol.Edge{From: "*", To: "provider", Kind: "depends", Rule: "ask"}, "owner"); err != nil {
		t.Fatal(err)
	}
	m, _, err := send(s, a, b, "expiry", "")
	if err != nil {
		t.Fatal(err)
	}
	c.t = m.ExpiresAt.Add(time.Second)
	if _, err = s.Expire(); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Message(m.ID)
	if err != nil || got.State != protocol.StateExpired {
		t.Fatalf("expired: %#v %v", got, err)
	}
	messages, err := s.Messages(mustNode(t, s, a), protocol.QueryMessages{SessionID: a})
	if err != nil {
		t.Fatal(err)
	}
	for _, reply := range messages {
		if reply.ParentID == m.ID && strings.Contains(reply.Body, "expired") {
			return
		}
	}
	t.Fatalf("sender not notified: %#v", messages)
}

func TestDeclineIsRecordedWithReply(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project='provider' WHERE id=?`, b); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEdge(protocol.Edge{From: "*", To: "provider", Kind: "depends", Rule: "ask"}, "owner"); err != nil {
		t.Fatal(err)
	}
	m, _, err := send(s, a, b, "held", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AnswerHeld(m.ID, "r"); err != nil {
		t.Fatal(err)
	}
	messages, err := s.Messages(mustNode(t, s, a), protocol.QueryMessages{SessionID: a})
	if err != nil {
		t.Fatal(err)
	}
	for _, reply := range messages {
		if reply.ParentID == m.ID && reply.Body == "your user declined this request" {
			return
		}
	}
	t.Fatal("missing decline reply")
}
