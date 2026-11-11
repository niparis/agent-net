package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// agent_changes_find returns the signals the gateway stored, with their
// evidence, so a caller reads them where it reads the change.
func TestChangesFindShowsQualitySignals(t *testing.T) {
	view := deliveryView()
	view.Signals = []protocol.QualitySignal{
		{Code: protocol.SignalRepeatedLine, Strength: protocol.SignalStrong, Evidence: "repeated line ×3: p.Ack || p.Type == protocol.TypeAck in server.go, workers.go"},
		{Code: protocol.SignalHeavyTests, Strength: protocol.SignalWeak, Evidence: "heavy tests: 300 test lines added for 12 code lines"},
	}
	cs := connect(t, func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		if method != protocol.OpChangesFind {
			return json.RawMessage(`{}`), nil
		}
		return json.Marshal(protocol.ChangesFindResult{Items: []protocol.ChangeView{view}, Total: 1})
	})
	text := deliveryText(deliveryCall(t, cs, "agent_changes_find", `{"change":"ch_1"}`))
	for _, want := range []string{"repeated_line", "p.Ack || p.Type == protocol.TypeAck", "heavy_tests", "300 test lines"} {
		if !strings.Contains(text, want) {
			t.Fatalf("agent_changes_find lacks %q:\n%s", want, text)
		}
	}
}
