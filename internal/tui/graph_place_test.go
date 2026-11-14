package tui

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// An older gateway sends no Place, so the card falls back to the typed graph.
// It names the parent the tree stores instead of guessing one from the repo or
// team lead.
func TestDerivedPlaceNamesTheTreeParent(t *testing.T) {
	m := Model{snap: Snapshot{Agents: []protocol.AgentView{
		{SessionID: "s_lead", AgentID: "lead", Profile: protocol.Profile{Name: "strategy lead"}},
		{SessionID: "s_merger", AgentID: "merger", Profile: protocol.Profile{Name: "merger"}},
	}}}
	n := protocol.Node{Name: "agent:merger", Ref: &protocol.GraphRef{Kind: "agent", ID: "merger"}, ReportsTo: "lead"}
	if p := m.derivedPlace(n); p.ReportsTo != "strategy lead" {
		t.Fatalf("reports to %q, want the tree parent", p.ReportsTo)
	}
}
