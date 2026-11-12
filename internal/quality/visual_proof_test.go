package quality

import (
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
)

func TestVisualProofSignals(t *testing.T) {
	for _, tc := range []struct {
		visible, diff, strength string
		proof                   bool
	}{
		{"yes", "", protocol.SignalStrong, false},
		{"no", "+++ b/internal/tui/messages_view.go\n+x", protocol.SignalWeak, false},
		{"no", "+++ b/web/theme.css\n+x", protocol.SignalWeak, false},
		{"no", "+++ b/web/types.ts\n+x", protocol.SignalWeak, false},
		{"no", "+++ b/internal/store.go\n+x", "", false},
		{"yes", "", "", true},
	} {
		v := protocol.VisualEvidence{UserVisible: tc.visible}
		if tc.proof {
			v.VisualProof = []protocol.VisualProof{{File: "frame.txt", Caption: "open"}}
		}
		signals := VisualSignals(tc.diff, v)
		if tc.strength == "" {
			if len(signals) != 0 {
				t.Fatal(signals)
			}
		} else if len(signals) != 1 || signals[0].Strength != tc.strength {
			t.Fatalf("%+v: %+v", tc, signals)
		}
	}
}
