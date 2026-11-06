package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/usage"
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

// The process tree belongs only to this Claude session. CPU progress in a
// long-running child is visible even when no transcript or status is written.
func TestSessionStallClaudeCPUProgress(t *testing.T) {
	base := time.Now().Add(-30 * time.Minute).Truncate(time.Second)
	r := &stallCPUReader{at: base}
	sampler := usage.NewWith(r, 5*time.Second, 5*time.Minute, nil)
	ls := &localSession{info: protocol.SessionInfo{Harness: "claude", NativeID: "cpu", PID: 10, Status: "busy"}, harnessActivity: base}
	worker := &localSession{info: protocol.SessionInfo{Harness: "claude", NativeID: "worker", PID: 20, Status: "busy", Worker: &protocol.WorkerInfo{ID: "w_test"}}, harnessActivity: base}
	d := &Daemon{cfg: Config{UsageSampler: sampler}, sessions: map[string]*localSession{key("claude", "cpu"): ls, key("claude", "worker"): worker}}
	sampler.Watch = d.watchedPIDs
	sampler.Sample(context.Background())
	r.at = base.Add(15 * time.Minute)
	r.cpu = 2
	sampler.Sample(context.Background())
	d.observeClaudeCPU()
	if got := d.infoOf(ls).HarnessActivity; got != r.at.Unix() {
		t.Fatalf("CPU progress after long run = %d, want %d", got, r.at.Unix())
	}
	if got := d.infoOf(worker).HarnessActivity; got != base.Unix() {
		t.Fatalf("worker changed: %d", got)
	}
	// A repeated sample and a later zero-CPU sample are not new activity.
	d.observeClaudeCPU()
	r.at = base.Add(25 * time.Minute)
	sampler.Sample(context.Background())
	d.observeClaudeCPU()
	if got := d.infoOf(ls).HarnessActivity; got != base.Add(15*time.Minute).Unix() {
		t.Fatalf("unchanged CPU moved activity: %d", got)
	}
	d.bg.Wait()
}

type stallCPUReader struct {
	at  time.Time
	cpu float64
}

func (r *stallCPUReader) Read(context.Context) (usage.Snapshot, error) {
	return usage.Snapshot{At: r.at, Procs: map[int]usage.Proc{
		10: {PID: 10, PPID: 1, Start: 1}, 11: {PID: 11, PPID: 10, Start: 2, CPU: r.cpu},
		20: {PID: 20, PPID: 1, Start: 3}, 21: {PID: 21, PPID: 20, Start: 4, CPU: r.cpu},
	}}, nil
}
func (r *stallCPUReader) Args(context.Context, []int) map[int]string { return nil }
