package tui

import (
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestDeliveryDeskWaitingReason(t *testing.T) {
	note := "waiting on owner opens console (until 2026-11-12T08:00:00Z)"
	ref := protocol.StepRef{Step: protocol.StepVerify, AfterMerge: true}
	v := protocol.ChangeView{Change: protocol.Change{ID: "ch_wait", State: protocol.ChangeMerged, MergedCommit: strings.Repeat("a", 40)}, Steps: []protocol.StepStatus{{StepRef: ref, State: protocol.StepStateWaiting, Note: note}}}
	ch := deliveryChangeFromView(v, time.Now())
	text, _ := (Model{}).deliveryStatusText(ch)
	if text != note {
		t.Fatalf("Desk/delivery status=%q want %q", text, note)
	}
	if len(ch.Steps) != 1 || ch.Steps[0].Note != note {
		t.Fatalf("step detail lost reason: %+v", ch.Steps)
	}
}
