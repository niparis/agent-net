package tui

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestDeliveryReconciledStatus(t *testing.T) {
	for _, tc := range []struct{ state, kind, want string }{
		{protocol.ChangeDone, "external", "merged outside agent-net"},
		{protocol.ChangeStopped, "abandoned", "abandoned: branch gone"},
	} {
		ch := deliveryChange{State: tc.state, Evidence: []deliveryEvidence{{Fields: map[string]string{"reconcile_kind": tc.kind}}}}
		got, _ := (Model{}).deliveryStatusText(ch)
		if got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func TestDeliveryReconciledListStatus(t *testing.T) {
	v := protocol.ChangeView{Change: protocol.Change{State: protocol.ChangeDone}, Evidence: []protocol.Evidence{{Fields: map[string]string{"reconcile_kind": "external"}}}}
	ch := deliveryChangeFromView(v, time.Now())
	got, _ := (Model{}).deliveryStatusText(ch)
	if got != "merged outside agent-net" {
		t.Fatalf("got %q", got)
	}
}
