package daemon

import (
	"context"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

// agentToolMethods are the daemon methods behind agent-net's tools. A request
// for one of them from a bound session is a tool call for the nudge log. The
// message tools are left out on purpose: a message that is not an ack is its
// own work signal, and an ack must not count. Both are read from the message
// table instead.
var agentToolMethods = func() map[string]bool {
	out := toolcatalog.ToolMethods()
	for _, method := range []string{"send", "reply", "withdraw", "handoff"} {
		delete(out, method)
	}
	return out
}()

// touchToolWork reports one agent-net tool call to the gateway, so a nudge
// whose follow window still contains the call is marked followed. A tool call
// is not a message: an ack does not reach here. Frames are limited to one per
// session per second, which is far below the 30 minute follow window.
func (d *Daemon) touchToolWork(session string) {
	if session == "" {
		return
	}
	now := time.Now()
	d.mu.Lock()
	if d.toolTouches == nil {
		d.toolTouches = map[string]time.Time{}
	}
	if last := d.toolTouches[session]; !last.IsZero() && now.Sub(last) < time.Second {
		d.mu.Unlock()
		return
	}
	d.toolTouches[session] = now
	d.mu.Unlock()
	d.goBackground(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = d.call(ctx, protocol.FrameNudgeTouch, protocol.NudgeTouchRequest{SessionID: session, At: now.UnixNano()})
	})
}
