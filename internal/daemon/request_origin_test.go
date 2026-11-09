package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The gateway counts requests by the origin the daemon puts on the frame. A
// call from the owner's TUI, one from an agent session and the daemon's own
// loops must each arrive under their own label.
func TestGatewayFramesCarryTheCallerOrigin(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})

	tui := dialLocal(t, n.sock)
	tui.mustCall("hello", map[string]any{"client": "cli", "tui": true})
	for i := 0; i < 3; i++ {
		tui.mustCall("list", map[string]any{})
	}
	agent, _ := helloPi(t, n, "origin-fixture", "/origin-fixture", true)
	agent.mustCall("list", map[string]any{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got protocol.GatewayRequests
	waitDaemonLink(t, "counters of all three origins", func() bool {
		out, err := g.gw.MachineGet(ctx, protocol.MachineGetRequest{Machine: n.d.hostName()})
		if err != nil || out.Requests == nil {
			return false
		}
		got = *out.Requests
		seen := map[string]int64{}
		for _, s := range got.Since.Top {
			if s.Method == "query.agents" {
				seen[s.Origin] += s.Calls
			}
		}
		return seen[protocol.ViaTUI] >= 3 && seen[protocol.ViaMCP] >= 1
	})
	byOrigin := map[string]int64{}
	for _, o := range got.Since.ByOrigin {
		byOrigin[o.Origin] = o.Calls
	}
	if byOrigin[protocol.ViaDaemon] == 0 {
		t.Fatalf("the daemon's own frames (hello, session sync) are not counted as daemon: %+v", got.Since.ByOrigin)
	}
	if byOrigin[protocol.ViaUnknown] != 0 {
		t.Fatalf("a frame from this daemon came without an origin: %+v", got.Since.ByOrigin)
	}
}

func TestLocalConnOriginLabels(t *testing.T) {
	d := &Daemon{}
	worker := &localSession{info: protocol.SessionInfo{Worker: &protocol.WorkerInfo{}}}
	for _, c := range []struct {
		name string
		lc   *localConn
		want string
	}{
		{"before hello", &localConn{d: d}, protocol.ViaUnknown},
		{"tui", &localConn{d: d, client: "cli", tui: true}, protocol.ViaTUI},
		{"cli", &localConn{d: d, client: "cli"}, protocol.ViaCLI},
		{"cto", &localConn{d: d, client: "cto-mcp"}, protocol.ViaCTO},
		{"agent", &localConn{d: d, client: "mcp", ls: &localSession{}}, protocol.ViaMCP},
		{"worker", &localConn{d: d, client: "mcp", ls: worker}, protocol.ViaWorker},
	} {
		if got := c.lc.via(); got != c.want {
			t.Errorf("%s: via = %q, want %q", c.name, got, c.want)
		}
	}
	if got := viaFrom(context.Background()); got != protocol.ViaDaemon {
		t.Errorf("a call with no label = %q, want daemon", got)
	}
}
