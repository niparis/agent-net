package daemon

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// The machine's test slot (decision: merger suites obey worker admission).
// A merger runs every local suite through `agent-net test-run`, which asks the
// daemon for this slot. One suite runs at a time per machine, so a second
// waits in a queue and is told its place. A running suite is load like a
// worker: worker admission counts it and its granted cores. The slot lives on
// the client's connection, so a suite that exits, crashes or is killed frees
// it when the connection closes.

const (
	// testTimeoutDefault is the wall-clock limit when merger.test_timeout has
	// no value.
	testTimeoutDefault = 90 * time.Minute
	// testAdmissionRAM is used when the recipe has no default worker RAM.
	testAdmissionRAM = 600 << 20
)

// testSlotState is the suite that holds the machine's slot.
type testSlotState struct {
	conn      *localConn
	startedAt time.Time
	parallel  int
	// cost is what worker admission counts while the suite runs.
	cost WorkerCost
}

// runningTestSlot is the suite that holds the slot, or nil. The copy is made
// under the lock so admissions can read it without holding tmu.
func (d *Daemon) runningTestSlot() *testSlotState {
	d.tmu.Lock()
	defer d.tmu.Unlock()
	if d.testSlot == nil {
		return nil
	}
	copy := *d.testSlot
	return &copy
}

// testAdmissionCost is the expected cost of one suite at admission: one core
// and the default worker RAM. Its real parallelism is granted afterwards.
func (d *Daemon) testAdmissionCost(snap settings.Snapshot) WorkerCost {
	ram := uint64(snap.Int("workers.cost_default_ram", settings.Where{}))
	if ram == 0 {
		ram = testAdmissionRAM
	}
	return WorkerCost{RAM: ram, Cores: 1}
}

// testParallel is how many packages a suite may build or test at once: the
// free cores under the CPU target, after the workers that started in the last
// five minutes, at least 1 and with no fixed cap.
//
// The machine figure is its 5-minute average, the same samples the usage view
// and worker admission use. A suite that just ended stays in that average for
// minutes, and the tree that ran it belongs to the calling session, so those
// averaged cores are added back. Only one suite runs per machine and this one
// holds the free slot, so no subtraction for a running suite is needed.
func testParallel(load MachineLoad, target, pendingCores, ownCores float64) int {
	if load.Cores <= 0 {
		return 1
	}
	if target <= 0 {
		target = 75
	}
	free := float64(load.Cores)*(target-load.CPU5)/100 - pendingCores + ownCores
	n := int(math.Floor(free))
	if n < 1 {
		return 1
	}
	return n
}

// callerTreeCores is the averaged CPU, in cores, of the calling session's own
// process tree: the tree that holds the suite it ran before. Crediting that
// share keeps the caller's own finished work from sizing its next suite down.
// Workers it started are still running and still use their CPU, so they are
// not credited. A caller that maps to no session credits nothing.
func (d *Daemon) callerTreeCores(lc *localConn) float64 {
	s := d.cfg.UsageSampler
	if s == nil {
		return 0
	}
	lc.mu.Lock()
	pid := lc.peerPID
	lc.mu.Unlock()
	root := d.callerSession(pid)
	if root <= 0 {
		return 0
	}
	if t, ok := s.Trees()[root]; ok {
		return t.CPU.Avg / 100
	}
	return 0
}

// callerSession walks pid's parents to the first known session or worker root,
// as insideSession does for the owner gate. A pid that maps to no session
// returns 0.
func (d *Daemon) callerSession(pid int) int {
	if pid <= 0 {
		return 0
	}
	roots := map[int]bool{}
	add := func(root int) {
		if root > 1 {
			roots[root] = true
		}
	}
	d.mu.Lock()
	for _, ls := range d.sessions {
		add(ls.info.PID)
	}
	d.mu.Unlock()
	// A worker's process counts from its start, before its plugin says hello.
	d.wmu.Lock()
	for _, w := range d.workers {
		add(w.pid)
	}
	d.wmu.Unlock()
	for p, depth := pid, 0; p > 1 && depth < 64; depth++ {
		if roots[p] {
			return p
		}
		parent, err := parentPID(p)
		if err != nil {
			return 0
		}
		p = parent
	}
	return 0
}

// testTimeout is the merger.test_timeout value for a repo, or its network
// value, or the default.
func (d *Daemon) testTimeout(repo string) time.Duration {
	snap := d.SettingsSnapshot()
	e, err := snap.Resolve("merger.test_timeout", settings.Where{Repo: repo})
	if err == nil {
		var text string
		if json.Unmarshal(e.Value, &text) == nil {
			if dur, err := time.ParseDuration(text); err == nil && dur > 0 {
				return dur
			}
		}
	}
	return testTimeoutDefault
}

// acquireTestSlot asks for the machine's test slot. When it is held, the
// caller is queued and told its place. The caller keeps its local connection
// open while the suite runs; closing it frees the slot.
func (d *Daemon) acquireTestSlot(lc *localConn, cwd string) protocol.TestSlotResult {
	host := d.hostName()
	repo := ""
	if cwd != "" {
		repo = d.repo.Lookup(cwd).Project
	}
	timeout := d.testTimeout(repo)
	seconds := int64(timeout / time.Second)

	d.tmu.Lock()
	if d.testSlot != nil && d.testSlot.conn == lc {
		parallel := d.testSlot.parallel
		d.tmu.Unlock()
		return protocol.TestSlotResult{Admitted: true, Parallel: parallel, TimeoutSeconds: seconds, Hostname: host}
	}
	pos := d.testQueuePositionLocked(lc)
	head := d.testWait[0] == lc
	running := d.testSlot != nil
	d.tmu.Unlock()
	if !head || running {
		return protocol.TestSlotResult{Position: pos, TimeoutSeconds: seconds, Hostname: host,
			Reason: testQueueReason(host, running, pos)}
	}

	// The head of the queue asks for room. Admission is first come, first
	// served, so a later request does not go ahead while the head waits.
	d.qmu.Lock()
	dec := d.admissionForTest()
	d.qmu.Unlock()
	if !dec.OK {
		return protocol.TestSlotResult{Position: pos, TimeoutSeconds: seconds, Hostname: host, Reason: dec.Reason}
	}

	snap := d.SettingsSnapshot()
	load := d.load()
	set := d.workerSettingsWith(snap, "")
	parallel := testParallel(load, set.CPUTarget, d.pendingWorkerCores(load.Total, snap), d.callerTreeCores(lc))
	cost := d.testAdmissionCost(snap)
	cost.Cores = float64(parallel)

	d.tmu.Lock()
	if d.testSlot != nil {
		pos = d.testQueuePositionLocked(lc)
		running = true
		d.tmu.Unlock()
		return protocol.TestSlotResult{Position: pos, TimeoutSeconds: seconds, Hostname: host,
			Reason: testQueueReason(host, running, pos)}
	}
	d.testSlot = &testSlotState{conn: lc, startedAt: time.Now(), parallel: parallel, cost: cost}
	d.removeTestWaiterLocked(lc)
	d.tmu.Unlock()

	// The suite is a start like a worker start, so the start gap holds.
	d.qmu.Lock()
	d.lastStart = time.Now()
	d.qmu.Unlock()

	d.Logf("test.slot admitted host=%s parallel=%d timeout=%s", host, parallel, timeout)
	return protocol.TestSlotResult{Admitted: true, Parallel: parallel, TimeoutSeconds: seconds, Hostname: host}
}

// testQueueReason says why a suite waits.
func testQueueReason(host string, running bool, pos int) string {
	if running {
		return fmt.Sprintf("a test suite runs on %s", host)
	}
	return fmt.Sprintf("%d test request(s) are ahead on %s", max(pos-1, 0), host)
}

// releaseTestSlot frees the slot of a connection that ended, or takes a
// waiting request out of the queue.
func (d *Daemon) releaseTestSlot(lc *localConn) {
	d.tmu.Lock()
	defer d.tmu.Unlock()
	d.removeTestWaiterLocked(lc)
	if d.testSlot != nil && d.testSlot.conn == lc {
		d.testSlot = nil
		d.Logf("test.slot released host=%s", d.hostName())
	}
}

// testQueuePositionLocked returns the caller's place in the test queue, 1
// first, and remembers it. The caller holds tmu.
func (d *Daemon) testQueuePositionLocked(lc *localConn) int {
	for i, c := range d.testWait {
		if c == lc {
			return i + 1
		}
	}
	d.testWait = append(d.testWait, lc)
	return len(d.testWait)
}

// removeTestWaiterLocked drops a connection from the test queue. The caller
// holds tmu.
func (d *Daemon) removeTestWaiterLocked(lc *localConn) {
	for i, c := range d.testWait {
		if c == lc {
			d.testWait = append(d.testWait[:i], d.testWait[i+1:]...)
			return
		}
	}
}

// pendingWorkerCores is the CPU cost of the workers that started in the last
// five minutes: the averages cannot show them yet.
func (d *Daemon) pendingWorkerCores(total uint64, snap settings.Snapshot) float64 {
	now := time.Now()
	type pendingWorker struct{ harness, model string }
	d.wmu.Lock()
	var recent []pendingWorker
	for _, w := range d.workers {
		if age := now.Sub(w.startedAt); age >= 0 && age < pendingLoadWindow {
			recent = append(recent, pendingWorker{w.harness, w.model})
		}
	}
	d.wmu.Unlock()
	var cores float64
	for _, r := range recent {
		cores += d.expectedCostWithSnapshot(r.harness, r.model, total, snap).Cores
	}
	return cores
}
