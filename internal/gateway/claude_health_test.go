package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestClaudeDeliveryWarningReachesNodeInfo(t *testing.T) {
	s, _, _ := newLoggedStore(t)
	id, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeHello(id, "box", "test"); err != nil {
		t.Fatal(err)
	}
	const warning = "Claude delivery broken by Claude Code test"
	for _, want := range []string{warning, ""} {
		if _, err := s.SetNodeHarnesses(id, []protocol.HarnessInfo{{Name: "claude", Version: "test", DeliveryWarning: want}}); err != nil {
			t.Fatal(err)
		}
		nodes, err := s.Nodes()
		if err != nil {
			t.Fatal(err)
		}
		if len(nodes) != 1 || len(nodes[0].Harnesses) != 1 || nodes[0].Harnesses[0].DeliveryWarning != want {
			t.Fatalf("node info: %+v", nodes)
		}
	}
}
