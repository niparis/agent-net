package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

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

// The Desk shows each signal with its evidence where it shows the change.
func TestDeskChangeShowsQualitySignals(t *testing.T) {
	ch := deliveryChange{ID: "ch_sig", Title: "Fix the deploy gate", Repo: "agent-net",
		State: protocol.ChangeOpen, Step: "accept", StepOwner: "merger", OwnerKind: protocol.OwnerNode,
		StepState: protocol.StepStateReady, Signals: uiSignals}
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: []deliveryChange{ch}}, 120, 40))
	m.inboxIndex = 0
	if sec, _ := m.deskPosition(); sec != deskSecChange {
		t.Fatalf("cursor is in section %d", sec)
	}
	view := ansi.Strip(m.testView())
	for _, want := range []string{"quality signals:", "strong: repeated line ×3:", "weak: heavy tests:"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Desk lacks %q:\n%s", want, view)
		}
	}
}
