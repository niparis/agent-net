package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSessionStallDiscoverySignals(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{adapter: &fakeAdapter{harness: "pi"}})
	for _, h := range []string{"claude", "codex"} {
		old := time.Now().Add(-time.Hour).Truncate(time.Second)
		in := protocol.SessionInfo{NativeID: h + "-stall", Status: "busy", HarnessActivity: old.Unix()}
		n.d.SetSessions(h, []protocol.SessionInfo{in})
		ls := testSessionOf(t, n, h, in.NativeID)
		n.d.SetSessions(h, []protocol.SessionInfo{in})
		if got := n.d.infoOf(ls).HarnessActivity; got != old.Unix() {
			t.Fatalf("%s poll moved activity: %d", h, got)
		}
		in.HarnessActivity = old.Add(time.Minute).Unix()
		n.d.SetSessions(h, []protocol.SessionInfo{in})
		if got := n.d.infoOf(ls).HarnessActivity; got != in.HarnessActivity {
			t.Fatalf("%s output lost: %d", h, got)
		}
	}
	a := n.d.adapters["codex"].(*codexAdapter)
	ls := testSessionOf(t, n, "codex", "codex-stall")
	a.onNote("item/completed", json.RawMessage(`{"threadId":"codex-stall"}`))
	if n.d.infoOf(ls).HarnessActivity < time.Now().Add(-time.Minute).Unix() {
		t.Fatal("Codex event did not record progress")
	}
}

func TestSessionStallPluginSignals(t *testing.T) {
	for _, tc := range []struct{ harness, exe string }{{"pi", "/usr/bin/pi"}, {"opencode", "/opt/x/node_modules/@opencode/cli/bin/opencode.exe"}} {
		t.Run(tc.harness, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			n := startNode(t, g, nodeOpts{cmdline: "pi", exe: tc.exe})
			lc := dialLocal(t, n.sock)
			lc.mustCall("hello", map[string]any{"client": tc.harness, "harness": tc.harness, "native_id": "test-stall", "cwd": "/work/demo", "status": "busy"})
			ls := testSessionOf(t, n, tc.harness, "test-stall")
			if got := n.d.infoOf(ls).HarnessActivity; got != 0 {
				t.Fatalf("hello counted as progress: %d", got)
			}
			lc.mustCall("harness_activity", map[string]any{})
			if got := n.d.infoOf(ls).HarnessActivity; got < time.Now().Add(-time.Minute).Unix() {
				t.Fatalf("missing progress: %d", got)
			}
		})
	}
}
