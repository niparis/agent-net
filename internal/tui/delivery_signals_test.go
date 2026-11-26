package tui

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

var uiSignals = []protocol.QualitySignal{
	{Code: protocol.SignalRepeatedLine, Strength: protocol.SignalStrong, Evidence: "repeated line ×3: p.Ack || p.Type == protocol.TypeAck in server.go, workers.go"},
	{Code: protocol.SignalHeavyTests, Strength: protocol.SignalWeak, Evidence: "heavy tests: 300 test lines added for 12 code lines"},
}

func TestDeliveryChangeKeepsQualitySignals(t *testing.T) {
	c := protocol.Change{ID: "ch_1", Project: "agent-net", Branch: "agent-net/w_1", HeadCommit: "abc", Revision: 1, State: protocol.ChangeOpen, CreatedAt: 1}
	ch := deliveryChangeFromView(protocol.ChangeView{Change: c, Signals: uiSignals}, t0)
	if len(ch.Signals) != 2 || ch.Signals[0].Code != protocol.SignalRepeatedLine {
		t.Fatalf("signals = %+v", ch.Signals)
	}
}

// The change detail shows each signal with its evidence.
func TestChangeDetailShowsQualitySignals(t *testing.T) {
	ch := deliveryChange{ID: "ch_sig", Title: "Fix the deploy gate", Repo: "agent-net",
		State: protocol.ChangeOpen, Step: "accept", StepOwner: "merger", OwnerKind: protocol.OwnerNode,
		StepState: protocol.StepStateReady, Signals: uiSignals}
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: []deliveryChange{ch}}, 120, 40))
	openChangeDetail(t, &m, ch.ID)
	view := deliveryText(m, 120, 40)
	for _, want := range []string{"Quality signals", "strong: repeated line ×3:", "weak: heavy tests:"} {
		if !strings.Contains(view, want) {
			t.Fatalf("detail lacks %q:\n%s", want, view)
		}
	}
}
