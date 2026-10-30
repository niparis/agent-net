package daemon

import (
	"fmt"
	"sort"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/usage"
)

// Admission by machine load (decision 0018). A new worker starts only when
// the machine has room for it. The load is the moving average of the sampler
// the TUI already uses; the cost of a worker is learned from the workers that
// ended before it.

// MachineLoad is what admission needs to know about the machine. Known is
// false when the daemon cannot measure (no sampler): then only the hard cap
// applies.
type MachineLoad struct {
	Known bool
	Cores int
	// CPU1 and CPU5 are the whole-machine CPU busy percent (0-100), averaged
	// over the last 1 and 5 minutes. They are not the load average.
	CPU1, CPU5 float64
	// Avail5 is the memory available for a new process, averaged over 5
	// minutes, in bytes. Total is the machine's RAM.
	Avail5 float64
	Total  uint64
	// Samples is how many CPU samples the 5-minute figure covers.
	Samples int
}

// minLoadSamples is how many CPU samples (5 seconds each) the daemon needs
// before it trusts its averages.
const minLoadSamples = 2

// workerGapHoldMinAge is how long a worker must have run for its start to hold
// the start gap. A start that failed, or a worker that ended sooner, did not
// add load the averages could see, so the next start need not wait.
const workerGapHoldMinAge = 30 * time.Second

// loadFromSampler reads the averages of a sampler. The sampler reports memory
// in use: on Linux that is MemTotal minus MemAvailable. On macOS it is app +
// wired + compressed memory (the way Activity Monitor counts it), so the file
// cache counts as available. Available memory here is total minus that.
func loadFromSampler(s *usage.Sampler) MachineLoad {
	if s == nil {
		return MachineLoad{}
	}
	a5 := s.Averages(5 * time.Minute)
	if a5.MemSamples == 0 || a5.MemTotal == 0 {
		return MachineLoad{Known: true}
	}
	a1 := s.Averages(time.Minute)
	l := MachineLoad{Known: true, Cores: a5.Cores, CPU5: a5.CPU, CPU1: a1.CPU, Total: a5.MemTotal, Samples: a5.Samples}
	if a1.Samples == 0 {
		l.CPU1 = a5.CPU
	}
	if a5.MemUsed < float64(a5.MemTotal) {
		l.Avail5 = float64(a5.MemTotal) - a5.MemUsed
	}
	return l
}

// WorkerCost is the expected cost of one more worker.
type WorkerCost struct {
	RAM   uint64  // bytes
	Cores float64 // CPU cores, 1 = one full core
	// Learned is true when the figures come from ended workers.
	Learned bool
}

// defaultWorkerCost is the cost of a worker with no history.
func defaultWorkerCost() WorkerCost {
	return WorkerCost{RAM: protocol.DefaultWorkerRAM, Cores: protocol.DefaultWorkerCores}
}

// memReserve is the memory that stays free: the setting, or the larger of
// 1.5 GB and 10 percent of RAM.
func memReserve(set WorkerSettings, total uint64) float64 {
	if set.MemReserve > 0 {
		return float64(set.MemReserve)
	}
	return max(float64(protocol.DefaultMemReserveBytes), protocol.DefaultMemReserveShare*float64(total))
}

// Decision is the answer of admission.
type Decision struct {
	OK bool
	// Room is protocol.RoomOK, or the first rule that said no.
	Room string
	// Reason is a sentence for the rule that said no.
	Reason string
	// Headroom ranks machines: the smaller of the free memory share and the
	// free CPU share, -1 when unknown.
	Headroom float64
}

// admit applies the rules in order: the hard cap, memory, CPU, and the gap
// since the last start. The first rule that says no gives the reason.
func admit(set WorkerSettings, load MachineLoad, cost WorkerCost, running int, lastStart, now time.Time) Decision {
	d := Decision{OK: true, Room: protocol.RoomOK, Headroom: -1}
	if load.Known && load.Total > 0 {
		reserve := memReserve(set, load.Total)
		memFree := load.Avail5 - float64(cost.RAM) - reserve
		cpuFree := set.CPUTarget - load.CPU5
		d.Headroom = min(memFree/float64(load.Total), cpuFree/100)
	}
	no := func(room, format string, a ...any) Decision {
		d.OK, d.Room, d.Reason = false, room, fmt.Sprintf(format, a...)
		return d
	}
	if running >= set.Max {
		d.Headroom = min(d.Headroom, 0)
		return no(protocol.RoomCap, "this machine runs %d workers, which is its cap", running)
	}
	if !load.Known {
		return d // no way to measure: the cap is the only rule
	}
	if load.Samples < minLoadSamples || load.Total == 0 {
		return no(protocol.RoomMeasuring, "the machine load is still being measured")
	}
	reserve := memReserve(set, load.Total)
	if load.Avail5-float64(cost.RAM)-reserve <= 0 {
		return no(protocol.RoomMem, "memory 5-min average available %s, a worker needs about %s and %s stay free",
			fmtBytes(load.Avail5), fmtBytes(float64(cost.RAM)), fmtBytes(reserve))
	}
	if load.CPU5 >= set.CPUTarget {
		return no(protocol.RoomCPU, "CPU 5-min average %.0f%% (target %.0f%%)", load.CPU5, set.CPUTarget)
	}
	if load.CPU1 >= protocol.WorkerCPUHot {
		return no(protocol.RoomCPU, "CPU 1-min average %.0f%% (limit %.0f%%)", load.CPU1, protocol.WorkerCPUHot)
	}
	if gap := set.StartGap; gap > 0 && !lastStart.IsZero() && now.Sub(lastStart) < gap {
		return no(protocol.RoomGap, "a worker started %s ago; the next may start after %s so the averages can show it",
			now.Sub(lastStart).Round(time.Second), gap)
	}
	return d
}

// fmtBytes writes a byte count in MB or GB.
func fmtBytes(b float64) string {
	if b >= 1<<30 {
		return fmt.Sprintf("%.1f GB", b/(1<<30))
	}
	return fmt.Sprintf("%.0f MB", b/(1<<20))
}

// median is the middle of the values (the mean of the two middle ones when
// the count is even).
func median(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	v := append([]float64(nil), vals...)
	sort.Float64s(v)
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}

// load returns the machine load: the configured probe, or the sampler.
func (d *Daemon) load() MachineLoad {
	if d.cfg.LoadProbe != nil {
		return d.cfg.LoadProbe()
	}
	return loadFromSampler(d.cfg.UsageSampler)
}

// lastWorkerStart is when a worker started last on this machine: the newest
// start of a worker that runs, or the last reserved start.
func (d *Daemon) lastWorkerStart() time.Time {
	last := d.lastStart
	d.wmu.Lock()
	defer d.wmu.Unlock()
	for _, w := range d.workers {
		if w.startedAt.After(last) {
			last = w.startedAt
		}
	}
	return last
}

// clearStartReservation drops the start time an admission just set, because
// the start failed before a worker ran. A worker that started since (under a
// disabled gap) keeps its own start time.
func (d *Daemon) clearStartReservation() {
	d.qmu.Lock()
	defer d.qmu.Unlock()
	d.wmu.Lock()
	later := false
	for _, w := range d.workers {
		if !w.startedAt.Before(d.lastStart) {
			later = true
			break
		}
	}
	d.wmu.Unlock()
	if !later {
		d.lastStart = time.Time{}
	}
}

// clearGapAfterShortWorker drops the start time of a worker that ended too soon
// to hold the gap. A start that came after it (which the gap keeps at least a
// minute away) is left alone.
func (d *Daemon) clearGapAfterShortWorker(w *worker) {
	d.qmu.Lock()
	defer d.qmu.Unlock()
	if !d.lastStart.IsZero() && w.startedAt.Sub(d.lastStart) < 5*time.Second && w.startedAt.Sub(d.lastStart) > -5*time.Second {
		d.lastStart = time.Time{}
	}
}

// admission decides whether a worker of this harness and model may start
// now. The caller holds d.qmu.
func (d *Daemon) admission(harness, model string) Decision {
	d.wmu.Lock()
	running := len(d.workers)
	d.wmu.Unlock()
	return admit(d.cfg.Workers.filled(), d.load(), d.expectedCost(harness, model), running, d.lastWorkerStart(), time.Now())
}

// workerLoad is the worker count and room of this machine, for the report to
// the gateway and for the owner's views.
func (d *Daemon) workerLoad() *protocol.WorkerLoad {
	d.qmu.Lock()
	dec := d.admission("", "")
	d.qmu.Unlock()
	d.wmu.Lock()
	running := len(d.workers)
	d.wmu.Unlock()
	queued, _ := d.st.queueLen()
	wl := &protocol.WorkerLoad{Running: running, Queued: queued, Room: dec.Room, Headroom: dec.Headroom, Reason: dec.Reason}
	if entries, err := d.st.queue(); err == nil {
		for i, e := range entries {
			wl.Queue = append(wl.Queue, protocol.QueuedWorker{QueueID: e.ID, Position: i + 1, Hostname: d.hostName(), Harness: e.Harness,
				Model: e.Model, Owner: e.Owner, Project: e.Project, QueuedAt: e.QueuedAt.Unix(), Reason: dec.Reason})
		}
	}
	return wl
}
