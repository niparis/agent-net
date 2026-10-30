package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/usage"
)

// ---- the rules (pure) ----

// A machine with room: 16 GB, 8 GB available, 10 percent CPU.
func roomyLoad() MachineLoad {
	return MachineLoad{Known: true, Cores: 8, CPU1: 10, CPU5: 10, Avail5: 8 << 30, Total: 16 << 30, Samples: 60}
}

func TestAdmitRules(t *testing.T) {
	set := WorkerSettings{}.filled()
	now := time.Now()
	long := now.Add(-time.Hour)
	cost := defaultWorkerCost()
	cases := []struct {
		name    string
		mut     func(*MachineLoad)
		running int
		last    time.Time
		set     func(*WorkerSettings)
		ok      bool
		room    string
		reason  string
	}{
		{name: "room", ok: true, room: protocol.RoomOK, last: long},
		{name: "hard cap", running: 12, ok: false, room: protocol.RoomCap, reason: "12 workers", last: long},
		{name: "cap is configurable", running: 2, set: func(s *WorkerSettings) { s.Max = 2 }, ok: false, room: protocol.RoomCap, last: long},
		{name: "memory short", mut: func(l *MachineLoad) { l.Avail5 = 2 << 30 }, ok: false, room: protocol.RoomMem, reason: "memory 5-min average available", last: long},
		// 16 GB: the reserve is 10 percent = 1.6 GB (above 1.5 GB); 600 MB + 1.6 GB = 2.2 GB needed.
		{name: "memory just enough", mut: func(l *MachineLoad) { l.Avail5 = 2.3 * (1 << 30) }, ok: true, room: protocol.RoomOK, last: long},
		{name: "memory just short", mut: func(l *MachineLoad) { l.Avail5 = 2.1 * (1 << 30) }, ok: false, room: protocol.RoomMem, last: long},
		{name: "small machine reserve is 1.5 GB", mut: func(l *MachineLoad) { l.Total, l.Avail5 = 8<<30, 2.0*(1<<30)+1 }, ok: false, room: protocol.RoomMem, last: long},
		{name: "reserve is configurable", set: func(s *WorkerSettings) { s.MemReserve = 7.5 * (1 << 30) }, ok: false, room: protocol.RoomMem, last: long},
		{name: "cpu 5-min over target", mut: func(l *MachineLoad) { l.CPU5 = 82 }, ok: false, room: protocol.RoomCPU, reason: "CPU 5-min average 82% (target 75%)", last: long},
		{name: "cpu target is configurable", mut: func(l *MachineLoad) { l.CPU5 = 82 }, set: func(s *WorkerSettings) { s.CPUTarget = 90 }, ok: true, room: protocol.RoomOK, last: long},
		{name: "cpu 1-min hot", mut: func(l *MachineLoad) { l.CPU1 = 96 }, ok: false, room: protocol.RoomCPU, reason: "CPU 1-min average 96% (limit 95%)", last: long},
		{name: "cpu 1-min busy but under the limit", mut: func(l *MachineLoad) { l.CPU1 = 90 }, ok: true, room: protocol.RoomOK, last: long},
		{name: "a worker started 20 s ago", ok: false, room: protocol.RoomGap, reason: "started 20s ago", last: now.Add(-20 * time.Second)},
		{name: "a worker started 61 s ago", ok: true, room: protocol.RoomOK, last: now.Add(-61 * time.Second)},
		{name: "gap can be switched off", set: func(s *WorkerSettings) { s.StartGap = -1 }, ok: true, room: protocol.RoomOK, last: now},
		{name: "not measured yet", mut: func(l *MachineLoad) { l.Samples = 1 }, ok: false, room: protocol.RoomMeasuring, last: long},
		{name: "unknown load: only the cap applies", mut: func(l *MachineLoad) { *l = MachineLoad{} }, ok: true, room: protocol.RoomOK, last: now},
	}
	for _, c := range cases {
		load := roomyLoad()
		if c.mut != nil {
			c.mut(&load)
		}
		s := set
		if c.set != nil {
			c.set(&s)
		}
		got := admit(s, load, cost, c.running, c.last, now)
		if got.OK != c.ok || got.Room != c.room || !strings.Contains(got.Reason, c.reason) {
			t.Errorf("%s: got %+v, want ok=%v room=%s reason~%q", c.name, got, c.ok, c.room, c.reason)
		}
	}
	// A bigger expected worker needs more memory.
	big := WorkerCost{RAM: 7 << 30, Cores: 2, Learned: true}
	if got := admit(set, roomyLoad(), big, 0, long, now); got.OK || got.Room != protocol.RoomMem {
		t.Errorf("a 7 GB worker on 8 GB available: %+v", got)
	}
	// Headroom ranks machines: more free memory and CPU means more.
	a := admit(set, roomyLoad(), cost, 0, long, now)
	busy := roomyLoad()
	busy.CPU5 = 60
	b := admit(set, busy, cost, 0, long, now)
	if !(a.Headroom > b.Headroom && b.Headroom > 0) {
		t.Errorf("headroom: idle %v, busy %v", a.Headroom, b.Headroom)
	}
}

func TestDefaultsOfWorkerSettings(t *testing.T) {
	f := WorkerSettings{}.filled()
	if f.Max != 12 || f.CPUTarget != 75 || f.QueueTimeout != 2*time.Hour || f.QueueTick != 15*time.Second || f.StartGap != time.Minute {
		t.Errorf("defaults: %+v", f)
	}
	if got := defaultWorkerCost(); got.RAM != 600<<20 || got.Cores != 1 || got.Learned {
		t.Errorf("default cost: %+v", got)
	}
}

func TestMedian(t *testing.T) {
	if median(nil) != 0 || median([]float64{7}) != 7 {
		t.Error("small sets")
	}
	if got := median([]float64{4, 1, 3, 2}); got != 2.5 {
		t.Errorf("median of 1..4 = %v, want 2.5", got)
	}
	if got := median([]float64{9, 1, 5}); got != 5 {
		t.Errorf("median of 1,5,9 = %v, want 5", got)
	}
}

// The real history of a 15 GB machine: one go test -race burst peaked at 9.3 GB.
func TestLearnedCostIgnoresPeaks(t *testing.T) {
	const gb = 1 << 30

	avg := []float64{0.27, 1.25, 1.05, 0.95, 1.58, 1.39}
	peak := []float64{0.28, 3.6, 3.5, 3.5, 9.3, 4.2}
	var cs []workerCost
	for i := range avg {
		cs = append(cs, workerCost{AvgRSS: avg[i] * gb, PeakRSS: peak[i] * gb, AvgCPU: 30})
	}
	c := learnedCost(cs, 15*gb)
	// Median of the six averages: (1.05 + 1.25) / 2 = 1.15 GB.
	if !c.Learned || float64(c.RAM)/gb < 1.14 || float64(c.RAM)/gb > 1.16 || c.Cores != 0.3 {
		t.Errorf("cost %+v, want about 1.15 GB and 0.3 cores", c)
	}
	// A huge history is capped at 25% of the machine.
	big := []workerCost{{AvgRSS: 20 * gb}, {AvgRSS: 30 * gb}, {AvgRSS: 40 * gb}}
	if c := learnedCost(big, 16*gb); c.RAM != 4*gb {
		t.Errorf("capped cost %v, want 4 GB", c.RAM)
	}
	// Unknown total: no cap.
	if c := learnedCost(big, 0); c.RAM != 30*gb {
		t.Errorf("uncapped cost %v, want 30 GB", c.RAM)
	}
	// Fewer than two samples: the default.
	if c := learnedCost(cs[:1], 15*gb); c.Learned || c.RAM != 600<<20 {
		t.Errorf("one sample: %+v", c)
	}
	if c := learnedCost(nil, 15*gb); c.Learned {
		t.Errorf("no sample: %+v", c)
	}
}

// ---- cost learning ----

func TestWorkerCostLearning(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	d := w.d
	// No history: 600 MB and one core.
	if c := d.expectedCost("opencode", "fake/m1"); c.RAM != 600<<20 || c.Cores != 1 || c.Learned {
		t.Fatalf("no history: %+v", c)
	}
	// 25 workers: only the last 20 are kept, and the expected cost is the median of the averages.
	for i := 1; i <= 25; i++ {
		d.recordWorkerCost("opencode", "fake/m1", usage.Life{CPUAvg: float64(i * 10), CPUPeak: float64(i * 20), RSSAvg: float64(i) * 1e8, RSSPeak: float64(i) * 1e8}, time.Minute)
	}
	cs, _ := d.st.costs("opencode", "fake/m1")
	if len(cs) != protocol.WorkerCostKeep {
		t.Fatalf("kept %d, want %d", len(cs), protocol.WorkerCostKeep)
	}
	// Kept: 6..25. The median of 20 values is the mean of the 10th and 11th: 15.5.
	c := d.expectedCost("opencode", "fake/m1")
	if !c.Learned || c.RAM != uint64(15.5e8) || c.Cores < 1.549 || c.Cores > 1.551 {
		t.Errorf("expected cost %+v, want 1.55e9 bytes and 1.55 cores", c)
	}
	// Another model has its own history, so it still gets the default.
	if c := d.expectedCost("opencode", "fake/m2"); c.Learned {
		t.Errorf("m2 should have no history: %+v", c)
	}
	// A run too short to measure, or with no figure, is not stored.
	d.recordWorkerCost("claude", "", usage.Life{RSSPeak: 1e9}, 2*time.Second)
	d.recordWorkerCost("claude", "", usage.Life{}, time.Minute)
	if cs, _ := d.st.costs("claude", ""); len(cs) != 0 {
		t.Errorf("stored an unmeasured run: %+v", cs)
	}
}

// A worker that ends leaves its measured cost: the sampler follows its tree.
func TestEndedWorkerLeavesItsCost(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fr := &procReader{}
	sampler := usage.NewWith(fr, time.Hour, 5*time.Minute, nil)
	w := startWorkerNode(t, g, shortSet, func(c *Config) { c.UsageSampler = sampler; c.LoadProbe = (&fakeLoad{l: roomyLoad()}).get })
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	rec := readFake(t, repo)
	// The sampler follows the worker's process (watchedPIDs). Feed it three readings.
	fr.setPID(rec.Pid)
	for i := 0; i < 3; i++ {
		fr.tick(i)
		sampler.Sample(context.Background())
	}
	// The run time must pass the minimum: move the start back.
	w.d.wmu.Lock()
	for _, wk := range w.d.workers {
		wk.startedAt = wk.startedAt.Add(-time.Minute)
	}
	w.d.wmu.Unlock()
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	eventually(t, "the cost is stored", func() bool {
		cs, _ := w.d.st.costs("opencode", "")
		return len(cs) == 1
	})
	cs, _ := w.d.st.costs("opencode", "")
	if cs[0].PeakRSS != 300 || cs[0].AvgRSS != 200 || cs[0].PeakCPU != 100 {
		t.Errorf("stored cost: %+v", cs[0])
	}
}

// procReader is a fake usage reader with one process, the worker's.
type procReader struct {
	mu   sync.Mutex
	pid  int
	snap usage.Snapshot
}

func (p *procReader) setPID(pid int) { p.mu.Lock(); p.pid = pid; p.mu.Unlock() }

func (p *procReader) tick(i int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rss := []uint64{100, 300, 200}[i]
	cpu := []float64{0, 5, 5}[i] // CPU seconds: 5 s in the second interval = 100 percent of a core
	at := time.Date(2026, 1, 1, 0, 0, i*5, 0, time.UTC)
	p.snap = usage.Snapshot{At: at, Procs: map[int]usage.Proc{p.pid: {PID: p.pid, PPID: 1, Start: 1, CPU: cpu, RSS: rss}},
		M: usage.Machine{Cores: 4, CPUBusy: float64(i) * 10, CPUTotal: float64(i) * 100, MemUsed: 1 << 30, MemTotal: 8 << 30}}
}

func (p *procReader) Read(ctx context.Context) (usage.Snapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snap, nil
}

func (p *procReader) Args(ctx context.Context, pids []int) map[int]string {
	return map[int]string{}
}

// ---- the queue, end to end ----

// fakeLoad is a machine load a test can change.
type fakeLoad struct {
	mu sync.Mutex
	l  MachineLoad
}

func (f *fakeLoad) get() MachineLoad { f.mu.Lock(); defer f.mu.Unlock(); return f.l }
func (f *fakeLoad) set(l MachineLoad) {
	f.mu.Lock()
	f.l = l
	f.mu.Unlock()
}

func cpuBusy() MachineLoad {
	l := roomyLoad()
	l.CPU5 = 82
	return l
}

var queueSet = func() WorkerSettings {
	s := shortSet
	s.QueueTick = 30 * time.Millisecond
	s.StartGap = -1
	return s
}()

func withLoad(f *fakeLoad) func(*Config) {
	return func(c *Config) { c.LoadProbe = f.get }
}

// startFrom starts a worker as the agent session pi and returns the result.
func startFrom(t *testing.T, pi *localClient, req protocol.WorkerStartRequest) protocol.WorkerStartResult {
	t.Helper()
	if req.Isolation == "" {
		req.Isolation = protocol.IsolationNone
	}
	var res protocol.WorkerStartResult
	if err := json.Unmarshal(pi.mustCall("worker_start", req), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestStartIsQueuedWhenTheMachineIsBusyAndStartsInOrder(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	set := queueSet
	set.StartGap = 300 * time.Millisecond // first come first served, one at a time
	w := startWorkerNode(t, g, set, withLoad(fl))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, piID := helloPi(t, w.node, "caller", "/work/caller", true)
	req := protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "first task"}

	r1 := startFrom(t, pi, req)
	req.Task = "second task"
	r2 := startFrom(t, pi, req)
	for i, r := range []protocol.WorkerStartResult{r1, r2} {
		if r.Status != "queued" || r.SessionID != "" || !protocol.IsQueueID(r.QueueID) || r.Position != i+1 || r.Hostname == "" {
			t.Fatalf("start %d: %+v", i+1, r)
		}
	}
	want := fmt.Sprintf("queued #2 on %s: CPU 5-min average 82%% (target 75%%)", w.cfg.Hostname)
	if r2.QueueReason != want {
		t.Errorf("reason %q, want %q", r2.QueueReason, want)
	}
	if w.d.workerCount() != 0 {
		t.Fatal("a worker started with no room")
	}
	// The queue shows in the machine's load, for the owner views.
	if wl := w.d.workerLoad(); wl.Queued != 2 || wl.Running != 0 || wl.Room != protocol.RoomCPU {
		t.Errorf("worker load %+v", wl)
	}

	// Room appears. The first start goes first, the second after the gap.
	fl.set(roomyLoad())
	eventually(t, "both queued starts became workers", func() bool { return len(w.logs.lines("worker.dequeued")) == 2 })
	lines := w.logs.lines("worker.dequeued")
	if len(lines) != 2 || !strings.Contains(lines[0], "queue="+r1.QueueID) || !strings.Contains(lines[1], "queue="+r2.QueueID) {
		t.Fatalf("start order: %v", lines)
	}
	// The caller gets a "worker started" notice for each, with the session and the wait.
	var notices []protocol.Message
	for len(notices) < 2 {
		m := pi.nextPush()
		if strings.Contains(m.Body, "status: started") {
			notices = append(notices, m)
		}
	}
	for _, m := range notices {
		if m.Type != protocol.TypeNotification || m.To != piID || !strings.Contains(m.Body, "queue wait: ") || !strings.Contains(m.Body, "worker: s_") {
			t.Errorf("started notice: %+v", m)
		}
	}
	// The task reached each worker as a message from the caller.
	for _, n := range notices {
		sid := n.From
		eventually(t, "the task reached "+sid, func() bool {
			for _, m := range w.messagesOf(t, sid) {
				if m.From == piID && m.Type == protocol.TypeRequest && strings.Contains(m.Body, "Task:\n") {
					return true
				}
			}
			return false
		})
	}
	if left, _ := w.d.st.queueLen(); left != 0 {
		t.Errorf("%d starts still queued", left)
	}
}

func TestQueuedStartCanBeCancelled(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	w := startWorkerNode(t, g, queueSet, withLoad(fl))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, _ := helloPi(t, w.node, "caller", "/work/caller", true)
	other, _ := helloPi(t, w.node, "other", "/work/other", true)
	r := startFrom(t, pi, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	if r.Status != "queued" {
		t.Fatalf("%+v", r)
	}
	// Another agent may not cancel it.
	if _, err := other.call("worker_stop", protocol.WorkerStopRequest{Session: r.QueueID}); err == nil {
		t.Fatal("another session cancelled a queued start")
	}
	if n, _ := w.d.st.queueLen(); n != 1 {
		t.Fatalf("queue length %d", n)
	}
	// Its owner can.
	if _, err := pi.call("worker_stop", protocol.WorkerStopRequest{Session: r.QueueID}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if n, _ := w.d.st.queueLen(); n != 0 {
		t.Fatalf("queue length %d after cancel", n)
	}
	// Room appears: nothing starts.
	fl.set(roomyLoad())
	time.Sleep(200 * time.Millisecond)
	if w.d.workerCount() != 0 {
		t.Fatal("a cancelled start became a worker")
	}
	// Cancelling again, or an unknown ID, is an error.
	if _, err := pi.call("worker_stop", protocol.WorkerStopRequest{Session: r.QueueID}); err == nil {
		t.Error("cancelled twice")
	}
	// The person at the command line may cancel any queued start.
	fl.set(cpuBusy())
	r2 := startFrom(t, pi, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "y"})
	if _, err := w.cli.call("worker_stop", protocol.WorkerStopRequest{Session: r2.QueueID}); err != nil {
		t.Errorf("owner cancel: %v", err)
	}
}

func TestQueuedStartTimesOutWithANotice(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	set := queueSet
	set.QueueTimeout = 200 * time.Millisecond
	w := startWorkerNode(t, g, set, withLoad(fl))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, piID := helloPi(t, w.node, "caller", "/work/caller", true)
	r := startFrom(t, pi, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	if r.Status != "queued" {
		t.Fatalf("%+v", r)
	}
	var m protocol.Message
	for {
		m = pi.nextPush()
		if strings.Contains(m.Body, "status: not started") {
			break
		}
	}
	if m.To != piID || !strings.Contains(m.Body, "waited") || !strings.Contains(m.Body, "CPU 5-min average 82%") || !strings.Contains(m.Body, "project shop") {
		t.Errorf("not-started notice: %+v", m)
	}
	if n, _ := w.d.st.queueLen(); n != 0 {
		t.Errorf("%d starts still queued", n)
	}
	if w.d.workerCount() != 0 {
		t.Error("a worker started")
	}
}

func TestQueueSurvivesADaemonRestart(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	w := startWorkerNode(t, g, queueSet, withLoad(fl))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	// The person at the command line is the owner: it survives a restart too.
	r, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "after the restart"})
	if err != nil || r.Status != "queued" {
		t.Fatalf("%+v %v", r, err)
	}
	w.d.Stop()
	w.node.restart()
	w.cli = cliClient(t, w.node)
	if n, _ := w.d.st.queueLen(); n != 1 {
		t.Fatalf("queue length %d after the restart", n)
	}
	if w.d.workerCount() != 0 {
		t.Fatal("started with no room")
	}
	fl.set(roomyLoad())
	eventually(t, "the queued start began after the restart", func() bool {
		return len(w.logs.lines("worker.dequeued", "queue="+r.QueueID)) == 1
	})
	// The task reached the worker, sent as the owner.
	sid := ""
	eventually(t, "the worker is listed", func() bool {
		for _, v := range w.workers(t) {
			sid = v.SessionID
		}
		return sid != ""
	})
	eventually(t, "the task reached the worker", func() bool {
		for _, m := range w.messagesOf(t, sid) {
			if m.From == w.ownerID() && strings.Contains(m.Body, "after the restart") {
				return true
			}
		}
		return false
	})
}

func TestQueuedStartIsDroppedWhenItsOwnerLeaves(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	set := queueSet
	set.OwnerGrace = 150 * time.Millisecond
	w := startWorkerNode(t, g, set, withLoad(fl))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, _ := helloPi(t, w.node, "caller", "/work/caller", true)
	r := startFrom(t, pi, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	if r.Status != "queued" {
		t.Fatalf("%+v", r)
	}
	pi.c.Close() // the caller leaves
	eventually(t, "the queued start was dropped", func() bool {
		n, _ := w.d.st.queueLen()
		return n == 0
	})
	eventually(t, "the drop is logged", func() bool { return len(w.logs.lines("worker.queue_dropped")) == 1 })
	fl.set(roomyLoad())
	time.Sleep(150 * time.Millisecond)
	if w.d.workerCount() != 0 {
		t.Error("a dropped start became a worker")
	}
}

// With room and an empty queue, a start goes straight through.
func TestStartWithRoomIsNotQueued(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: roomyLoad()}
	w := startWorkerNode(t, g, queueSet, withLoad(fl))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	if res.Status != "started" || res.SessionID == "" || res.Task == nil {
		t.Errorf("%+v", res)
	}
}

// The gap: a start within the gap of the last one waits, then goes.
func TestStartGapQueuesTheSecondStart(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: roomyLoad()}
	set := queueSet
	set.StartGap = 700 * time.Millisecond
	w := startWorkerNode(t, g, set, withLoad(fl))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, _ := helloPi(t, w.node, "caller", "/work/caller", true)
	req := protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"}
	// A worker was let through just now.
	w.d.qmu.Lock()
	w.d.lastStart = time.Now()
	w.d.qmu.Unlock()
	r := startFrom(t, pi, req)
	if r.Status != "queued" || !strings.Contains(r.QueueReason, "a worker started") {
		t.Fatalf("second: %+v", r)
	}
	eventually(t, "the start went after the gap", func() bool { return w.d.workerCount() == 1 })
}

// At the hard cap a start is queued, not refused, and goes when a worker ends.
func TestHardCapQueuesInsteadOfRefusing(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := queueSet
	set.Max = 1
	w := startWorkerNode(t, g, set, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	first := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	r, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "y"})
	if err != nil || r.Status != "queued" || !strings.Contains(r.QueueReason, "1 workers, which is its cap") {
		t.Fatalf("%+v %v", r, err)
	}
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: first.SessionID})
	eventually(t, "the queued start went after the stop", func() bool {
		return len(w.logs.lines("worker.dequeued")) == 1
	})
}
