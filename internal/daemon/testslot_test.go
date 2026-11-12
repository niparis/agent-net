package daemon

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/usage"
)

func acquireTestSlot(t *testing.T, lc *localClient, cwd string) protocol.TestSlotResult {
	t.Helper()
	var r protocol.TestSlotResult
	if err := json.Unmarshal(lc.mustCall(protocol.OpTestSlot, protocol.TestSlotRequest{Cwd: cwd}), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func testSlotHeld(d *Daemon) bool {
	d.tmu.Lock()
	defer d.tmu.Unlock()
	return d.testSlot != nil
}

// One suite at a time per machine: the second asks, is told its place, and
// gets the slot after the first lets go.
func TestTestSlotOnePerMachineAndQueue(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, queueSet, withLoad(&fakeLoad{l: roomyLoad()}))
	a := cliClient(t, w.node)
	b := cliClient(t, w.node)

	first := acquireTestSlot(t, a, "")
	if !first.Admitted || first.Parallel != 5 {
		t.Fatalf("first suite: %+v, want admitted with 5 parallel", first)
	}
	if first.TimeoutSeconds != int64((90 * time.Minute).Seconds()) {
		t.Fatalf("timeout %d s, want the merger.test_timeout default of 90m", first.TimeoutSeconds)
	}
	second := acquireTestSlot(t, b, "")
	if second.Admitted || second.Position != 1 {
		t.Fatalf("second suite: %+v, want queued at position 1", second)
	}
	again := acquireTestSlot(t, b, "")
	if again.Admitted || again.Position != 1 {
		t.Fatalf("second suite asked again: %+v, want the same place", again)
	}
	a.c.Close()
	eventually(t, "the test slot frees when its client disconnects", func() bool { return !testSlotHeld(w.d) })
	third := acquireTestSlot(t, b, "")
	if !third.Admitted || third.Parallel != 5 {
		t.Fatalf("queued suite after the holder left: %+v", third)
	}
}

// The parallelism is the free cores under workers.cpu_target, at least one,
// with no fixed cap.
func TestTestSlotParallelFollowsFreeCores(t *testing.T) {
	cases := []struct {
		name    string
		load    MachineLoad
		target  float64
		pending float64
		own     float64
		want    int
	}{
		{"roomy eight cores", roomyLoad(), 75, 0, 0, 5},
		{"sixteen idle cores", MachineLoad{Cores: 16, CPU5: 0}, 75, 0, 0, 12},
		{"two idle cores", MachineLoad{Cores: 2, CPU5: 0}, 75, 0, 0, 1},
		{"near the target", MachineLoad{Cores: 8, CPU5: 74}, 75, 0, 0, 1},
		{"pending workers", roomyLoad(), 75, 5, 0, 1},
		{"unknown core count", MachineLoad{}, 75, 0, 0, 1},
		{"higher target", roomyLoad(), 100, 0, 0, 7},
	}
	for _, c := range cases {
		if got := testParallel(c.load, c.target, c.pending, c.own); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}

// A suite is sized from the machine's 5-minute average, but the caller's own
// tree is credited back: the previous suite it ran stays in that average for
// minutes. The same load from another tree counts in full.
func TestTestSlotParallelCreditsTheCallersOwnTree(t *testing.T) {
	// 16 cores at target 75: a 12-core budget. The average shows 90% busy
	// (14.4 cores), 12.8 of them the caller's own work.
	own := MachineLoad{Cores: 16, CPU5: 90}
	if got := testParallel(own, 75, 0, 12.8); got != 10 {
		t.Fatalf("own load credited: %d, want 10 of the 12-core budget", got)
	}
	if got := testParallel(own, 75, 0, 0); got >= 10 {
		t.Fatalf("same load from another tree: %d, want fewer than 10", got)
	}
}

// treeReader feeds fixed snapshots to a usage sampler.
type treeReader struct {
	snaps []usage.Snapshot
	i     int
}

func (r *treeReader) Read(context.Context) (usage.Snapshot, error) {
	s := r.snaps[len(r.snaps)-1]
	if r.i < len(r.snaps) {
		s = r.snaps[r.i]
		r.i++
	}
	return s, nil
}

func (r *treeReader) Args(context.Context, []int) map[int]string { return nil }

// The caller's own session tree is credited; a worker the caller started is
// not, because that worker is still running and still uses its CPU. A caller
// that maps to no session credits nothing.
func TestCallerTreeCoresCreditsTheCallersTrees(t *testing.T) {
	self := os.Getpid()
	workerPID := self + 1
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snap := func(sec int, selfCPU, workerCPU float64) usage.Snapshot {
		return usage.Snapshot{At: at.Add(time.Duration(sec) * time.Second),
			Procs: map[int]usage.Proc{
				self:      {PID: self, PPID: 1, Start: 1, CPU: selfCPU},
				workerPID: {PID: workerPID, PPID: 1, Start: 2, CPU: workerCPU},
			},
			M: usage.Machine{Cores: 16, MemUsed: 1 << 30, MemTotal: 16 << 30}}
	}
	r := &treeReader{snaps: []usage.Snapshot{snap(0, 0, 0), snap(10, 10, 5)}}
	s := usage.NewWith(r, time.Hour, 5*time.Minute, func() []int { return []int{self, workerPID} })
	ctx := context.Background()
	s.Sample(ctx)
	s.Sample(ctx)
	d := &Daemon{cfg: Config{UsageSampler: s},
		sessions: map[string]*localSession{"claude/x": {sessionID: "s_own", info: protocol.SessionInfo{PID: self}}},
		workers:  map[string]*worker{"w_x": {id: "w_x", pid: workerPID, owner: "s_own", sessionID: "s_worker"}}}
	lc := &localConn{d: d, peerPID: self}
	// The session's tree ran at 100% of a core. Its worker ran at 50%, which
	// is not credited.
	if got := d.callerTreeCores(lc); math.Abs(got-1) > 1e-9 {
		t.Fatalf("own tree: %v cores, want 1", got)
	}
	if got := (&Daemon{cfg: d.cfg}).callerTreeCores(lc); got != 0 {
		t.Fatalf("a caller with no session: %v cores, want 0", got)
	}
}

// A slot whose client connection died is free again, and the daemon frees it.
func TestTestSlotFreedWhenClientDisconnects(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, queueSet, withLoad(&fakeLoad{l: roomyLoad()}))
	a := cliClient(t, w.node)
	if r := acquireTestSlot(t, a, ""); !r.Admitted {
		t.Fatalf("first suite: %+v", r)
	}
	if !testSlotHeld(w.d) {
		t.Fatal("the daemon does not hold the slot")
	}
	a.c.Close()
	eventually(t, "the dead client's slot is released", func() bool { return !testSlotHeld(w.d) })
}

// A running suite is load for workers: it counts against the machine cap and
// takes its place in the load figures.
func TestTestSlotCountsForWorkerAdmission(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := queueSet
	set.Max = 1
	w := startWorkerNode(t, g, set, withLoad(&fakeLoad{l: roomyLoad()}))
	a := cliClient(t, w.node)
	if r := acquireTestSlot(t, a, ""); !r.Admitted {
		t.Fatalf("acquire: %+v", r)
	}
	w.d.qmu.Lock()
	dec := w.d.admission("opencode", "fake/m1")
	w.d.qmu.Unlock()
	if dec.OK || dec.Room != protocol.RoomCap {
		t.Fatalf("a worker beside a suite: %+v, want the machine cap", dec)
	}
}

// The timeout setting is readable with the repo context and falls back to the
// network value.
func TestTestSlotTimeoutFromSettings(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, queueSet, withLoad(&fakeLoad{l: roomyLoad()}))
	if got := w.d.testTimeout(""); got != 90*time.Minute {
		t.Fatalf("timeout %s, want 90m", got)
	}
}
