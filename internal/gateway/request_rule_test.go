package gateway

import (
	"testing"

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
