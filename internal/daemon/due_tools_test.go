package daemon

import (
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestDueForwardsTheCallerSession(t *testing.T) {
	t.Parallel()
	n, f := startDeliveryNode(t)
	f.answer(protocol.FrameDue, func(raw json.RawMessage) (any, error) {
		return protocol.DueResult{Items: []protocol.DueItem{{ID: "d_1", What: "flip", Status: "pending", Blocker: "none", CreatedAt: "2026-11-21T07:00:00Z"}}}, nil
	})
	_, wt := deliveryWorktree(t)
	lc, sid := helloDelivery(t, n, "np-chief", wt, "gpt-6.1-sol")
	res, err := lc.call(protocol.OpDue, map[string]any{"action": "add", "what": "flip", "due": "45m"})
	if err != nil {
		t.Fatal(err)
	}
	var out protocol.DueResult
	if err := json.Unmarshal(res, &out); err != nil || len(out.Items) != 1 || out.Items[0].ID != "d_1" {
		t.Fatalf("result = %s (%v)", res, err)
	}
	sent := f.frames(protocol.FrameDue)
	if len(sent) != 1 {
		t.Fatalf("frames sent = %d, want 1", len(sent))
	}
	var q protocol.DueRequest
	if err := json.Unmarshal(sent[0], &q); err != nil {
		t.Fatal(err)
	}
	if q.SessionID != sid || q.Action != protocol.DueActionAdd || q.What != "flip" || q.Due != "45m" {
		t.Fatalf("frame = %+v, caller session %s", q, sid)
	}
}

func TestDueRefusesBadArgsBeforeTheGateway(t *testing.T) {
	t.Parallel()
	n, f := startDeliveryNode(t)
	_, wt := deliveryWorktree(t)
	lc, _ := helloDelivery(t, n, "np-chief", wt, "gpt-6.1-sol")
	if _, err := lc.call(protocol.OpDue, map[string]any{"action": "add", "what": "flip"}); err == nil {
		t.Fatal("an add with neither due nor after was accepted")
	}
	if sent := f.frames(protocol.FrameDue); len(sent) != 0 {
		t.Fatalf("a refused call reached the gateway: %d frames", len(sent))
	}
}
