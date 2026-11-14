package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The session list carries each session's Agent scopes and the Agent's pinned
// lead session, so the TUI can show the authority and a gone lead without a
// second read (decision 0166, decision 0160).
func TestAgentsCarryScopesAndLeadSession(t *testing.T) {
	s := teamStore(t)
	leads, _ := graphSessions(t, s, "demo", "other")
	if _, err := s.db.Exec(`UPDATE agents SET lead_session=? WHERE id=?`, leads["demo"], "demo"); err != nil {
		t.Fatal(err)
	}
	// The read is cached until one of its tables changes, so a read before the
	// scope is written must not hide the scope afterwards.
	if _, err := s.Agents(protocol.QueryAgents{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetScope(protocol.ScopeSetRequest{Node: "demo", Kind: protocol.ScopeMerge, Over: "repo:demo"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	agents, err := s.Agents(protocol.QueryAgents{})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]protocol.AgentView{}
	for _, a := range agents {
		found[a.AgentID] = a
	}
	demo, ok := found["demo"]
	if !ok {
		t.Fatalf("no demo session in %d agents", len(agents))
	}
	if len(demo.Scopes) != 1 || demo.Scopes[0].Kind != protocol.ScopeMerge || demo.Scopes[0].Over != "repo:demo" || demo.Scopes[0].Node != "demo" {
		t.Errorf("demo scopes %+v", demo.Scopes)
	}
	if demo.AgentLeadSession != leads["demo"] {
		t.Errorf("demo lead session %q, want %q", demo.AgentLeadSession, leads["demo"])
	}
	if other := found["other"]; len(other.Scopes) != 0 || other.AgentLeadSession != "" {
		t.Errorf("other agent has scopes %+v or lead %q", other.Scopes, other.AgentLeadSession)
	}
}

// BenchmarkAgentsListOrgTree times the session list read on the org tree
// fixture: 600 sessions, with scopes on every fifth Agent.
func BenchmarkAgentsListOrgTree(b *testing.B) {
	s := orgReadFixture(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.agentsRead(protocol.QueryAgents{}); err != nil {
			b.Fatal(err)
		}
	}
}
