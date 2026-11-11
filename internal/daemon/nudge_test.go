package daemon

import (
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestNudgeForwardsCallerSession(t *testing.T) {
	n, f := startDeliveryNode(t)
	_, wt := deliveryWorktree(t)
	lc, sid := helloDelivery(t, n, "np-chief-nudge", wt, "gpt-6.1-sol")
	for _, tc := range []struct {
		op, frame string
		args      any
	}{
		{protocol.OpNudgePause, protocol.FrameNudgePause, protocol.NudgePauseArgs{Kind: "idle_check", Agent: "test", Mode: "preview"}},
		{protocol.OpNudgeResume, protocol.FrameNudgeResume, protocol.NudgeResumeArgs{ID: "np_test"}},
		{protocol.OpNudgePausesFind, protocol.FrameNudgePausesFind, protocol.NudgePausesFindArgs{}},
	} {
		f.answer(tc.frame, func(raw json.RawMessage) (any, error) { return protocol.NudgeResult{ID: "np_test"}, nil })
		if _, err := lc.call(tc.op, tc.args); err != nil {
			t.Fatal(err)
		}
		sent := f.frames(tc.frame)
		var q struct {
			SessionID string `json:"session_id"`
		}
		if len(sent) != 1 || json.Unmarshal(sent[0], &q) != nil || q.SessionID != sid {
			t.Fatalf("%s frames=%s want session=%s", tc.op, sent, sid)
		}
	}
}
