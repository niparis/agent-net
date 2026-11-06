package daemon

import (
	"encoding/json"
	"github.com/niparis/agent-net/internal/mcpserver"
	"testing"
)

func TestCTOGraphTypedProposalCompatibility(t *testing.T) {
	for _, args := range []string{
		`{"action":"charter_set","ref":{"kind":"agent","id":"demo"},"attr":"purpose","value":"Actor purpose"}`,
		`{"action":"edge_set","from":{"kind":"agent","id":"reviewer"},"to":{"kind":"repo","id":"demo"},"kind":"reviews"}`,
		`{"action":"charter_set","node":"demo","attr":"purpose","value":"Legacy purpose"}`,
	} {
		name, raw, _, err := ctoConcreteCall("graph_change", json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		if err = mcpserver.CTOArguments(name, raw); err != nil {
			t.Fatalf("%s: %v", args, err)
		}
	}
}
