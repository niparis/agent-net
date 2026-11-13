package daemon

import (
	"sync/atomic"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A reconnect sends the gateway the sessions the daemon holds when the
// handshake starts. A session whose process ends before the gateway answers
// must read offline, not stay online until the next reconnect.
func TestSessionGoneDuringReconnectReadsOffline(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	var leave atomic.Pointer[func()]
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) {
		c.testAfterSyncSnapshot = func() {
			if f := leave.Swap(nil); f != nil {
				(*f)()
			}
		}
	}})
	n.d.SetSessions("codex", []protocol.SessionInfo{{NativeID: "t-gone", Cwd: "/work/api", Status: "idle"}})
	var id string
	eventually(t, "the session is registered", func() bool {
		id = n.d.SessionID("codex", "t-gone")
		return id != ""
	})

	// Discovery drops the session inside the handshake window: the daemon has
	// no link yet, so its remove cannot reach the gateway then.
	gone := func() { n.d.SetSessions("codex", nil) }
	leave.Store(&gone)
	first := currentLink(n.d)
	first.ws.CloseNow()
	eventually(t, "the daemon reconnects", func() bool {
		l := currentLink(n.d)
		return l != nil && l != first
	})
	if leave.Load() != nil {
		t.Fatal("the handshake did not reach the snapshot hook")
	}
	eventually(t, "the gone session reads offline", func() bool {
		a, ok, err := g.gw.Store().Agent(id)
		return err == nil && ok && !a.Online
	})
}
