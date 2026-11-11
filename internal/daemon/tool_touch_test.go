package daemon

import "testing"

// An ack must not count as work, so the message tools are not tool calls for
// the nudge firing log. The message table decides a non-ack message instead.
func TestToolTouchExcludesMessageTools(t *testing.T) {
	for _, method := range []string{"send", "reply", "withdraw", "handoff"} {
		if agentToolMethods[method] {
			t.Errorf("%s is counted as a tool call", method)
		}
	}
	for _, method := range []string{"sessions_find", "step_report", "worker_start", "announce"} {
		if !agentToolMethods[method] {
			t.Errorf("%s is not counted as a tool call", method)
		}
	}
}
