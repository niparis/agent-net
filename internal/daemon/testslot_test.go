package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
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
		want    int
	}{
		{"roomy eight cores", roomyLoad(), 75, 0, 5},
		{"sixteen idle cores", MachineLoad{Cores: 16, CPU5: 0}, 75, 0, 12},
		{"two idle cores", MachineLoad{Cores: 2, CPU5: 0}, 75, 0, 1},
		{"near the target", MachineLoad{Cores: 8, CPU5: 74}, 75, 0, 1},
		{"pending workers", roomyLoad(), 75, 5, 1},
		{"unknown core count", MachineLoad{}, 75, 0, 1},
		{"higher target", roomyLoad(), 100, 0, 7},
	}
	for _, c := range cases {
		if got := testParallel(c.load, c.target, c.pending); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
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
