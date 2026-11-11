package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestNudgeCatalogCallsDaemon(t *testing.T) {
	var called string
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		called = method
		return json.Marshal(protocol.NudgeResult{ID: "np_test", Sessions: []protocol.NudgeSession{{ID: "s_test", Name: "test"}}, Messages24h: 3, Pauses: []protocol.NudgePause{{ID: "np_test", Kind: "idle_check", Agent: "test", CreatedAt: 1}}})
	})
	for _, tc := range []struct{ name, op, args string }{
		{"agent_nudge_pause", protocol.OpNudgePause, `{"kind":"idle_check","agent":"test","mode":"preview"}`},
		{"agent_nudge_resume", protocol.OpNudgeResume, `{"id":"np_test"}`},
		{"agent_nudge_pauses_find", protocol.OpNudgePausesFind, `{}`},
	} {
		res := deliveryCall(t, cs, tc.name, tc.args)
		if res.IsError || called != tc.op {
			t.Fatalf("%s method=%s result=%v", tc.name, called, res)
		}
	}
}
