package usage

import (
	"context"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/redact"
)

// Defaults: a sample every 5 seconds, a window of 5 minutes.
const (
	DefaultInterval = 5 * time.Second
	DefaultWindow   = 5 * time.Minute
	// TopN is how many processes the top lists hold.
	TopN = 5
)

// Value is the last value of a series and its average over the window.
type Value struct {
	Now, Avg float64
}

// MachineStat is the machine side of a sample.
type MachineStat struct {
	Cores    int
	CPU      Value // percent of all cores, 0-100
	MemUsed  Value // bytes
	MemTotal uint64
	Load     [3]float64
	Procs    int
	// Samples is how many samples the averages cover.
	Samples int
}

// TreeStat is one process tree: a root process and all its descendants.
type TreeStat struct {
	PID int
	CPU Value // percent of one core (100 = one full core; it can be more)
	RSS Value // bytes, the sum over the tree
	// Life covers the whole time the tree was followed.
	Life  Life
	Procs int
	// Samples is how many CPU samples the averages cover. 0 means the tree
	// has only been seen once, so there is no CPU figure yet.
	Samples int
	// Alive is false when the root process is gone.
	Alive bool
}

// ProcStat is one process of a tree, for the top lists.
type ProcStat struct {
	PID  int
	Cmd  string  // short program name
	Args string  // first 80 characters of the command line, secrets masked
	CPU  float64 // percent of one core over the last interval
	RSS  uint64
}

// Top is the heaviest processes of one tree.
type Top struct {
	Tree   TreeStat
	ByCPU  []ProcStat
	ByRSS  []ProcStat
	Procs  int
	Cutoff time.Time // time of the snapshot
}

type point struct {
	at time.Time
	v  float64
}

// series is a rolling window of values.
type series struct{ pts []point }

func (s *series) add(at time.Time, v float64, window time.Duration) {
	s.pts = append(s.pts, point{at, v})
	cut := 0
	for cut < len(s.pts)-1 && at.Sub(s.pts[cut].at) > window {
		cut++
	}
	if cut > 0 {
		s.pts = append(s.pts[:0], s.pts[cut:]...)
	}
}

func (s *series) value() Value {
	if len(s.pts) == 0 {
		return Value{}
	}
	sum := 0.0
	for _, p := range s.pts {
		sum += p.v
	}
	return Value{Now: s.pts[len(s.pts)-1].v, Avg: sum / float64(len(s.pts))}
}

// avgSince is the average of the values taken at or after cut, and how many
// there are.
func (s *series) avgSince(cut time.Time) (float64, int) {
	sum, n := 0.0, 0
	for _, p := range s.pts {
		if !p.at.Before(cut) {
			sum += p.v
			n++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return sum / float64(n), n
}

// Life is the figures of a tree over everything the sampler saw of it, not
// only the last window. CPU is a percent of one core, RSS is in bytes.
type Life struct {
	CPUAvg, CPUPeak float64
	RSSAvg, RSSPeak float64
}

// lifeState sums a tree's samples over its whole life.
type lifeState struct {
	cpuSum, cpuPeak float64
	cpuN            int
	rssSum, rssPeak float64
	rssN            int
}

func (l *lifeState) value() Life {
	var out Life
	if l.cpuN > 0 {
		out.CPUAvg = l.cpuSum / float64(l.cpuN)
	}
	if l.rssN > 0 {
		out.RSSAvg = l.rssSum / float64(l.rssN)
	}
	out.CPUPeak, out.RSSPeak = l.cpuPeak, l.rssPeak
	return out
}

type treeState struct {
	life     lifeState
	start    uint64 // Start of the root, to notice a reused PID
	lastCPU  float64
	lastAt   time.Time
	haveCPU  bool
	cpu, rss series
	procs    int
	alive    bool
}

// Sampler samples a Reader on a timer and keeps the rolling window.
type Sampler struct {
	r        Reader
	interval time.Duration
	window   time.Duration
	// Watch returns the root PIDs of the trees to follow. It is called at
	// every sample.
	Watch func() []int

	mu        sync.Mutex
	prev, cur *Snapshot
	cpu, mem  series
	trees     map[int]*treeState
	machine   MachineStat
	lastErr   error
	took      time.Duration // total time spent sampling, for the cost figure
	n         int
}

// New makes a Sampler for this platform. It returns nil on a platform with no
// reader (anything but Linux and macOS).
func New(watch func() []int) *Sampler {
	var r Reader
	switch runtime.GOOS {
	case "linux":
		r = newProcFS("/proc")
	case "darwin":
		r = newPSReader()
	default:
		return nil
	}
	return NewWith(r, DefaultInterval, DefaultWindow, watch)
}

// NewWith makes a Sampler with a given reader, interval and window.
func NewWith(r Reader, interval, window time.Duration, watch func() []int) *Sampler {
	return &Sampler{r: r, interval: interval, window: window, Watch: watch, trees: map[int]*treeState{}}
}

// Run samples until ctx ends. The first sample is taken at once.
func (s *Sampler) Run(ctx context.Context) {
	s.Sample(ctx)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Sample(ctx)
		}
	}
}

// Sample takes one snapshot and updates the window.
func (s *Sampler) Sample(ctx context.Context) {
	began := time.Now()
	snap, err := s.r.Read(ctx)
	var roots []int
	if err == nil && s.Watch != nil {
		roots = s.Watch()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErr = err
	if err != nil {
		return
	}
	s.prev, s.cur = s.cur, &snap
	s.update(roots)
	s.n++
	s.took += time.Since(began)
}

// Cost returns the average time one sample takes and how many were taken.
func (s *Sampler) Cost() (perSample time.Duration, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.n == 0 {
		return 0, 0
	}
	return s.took / time.Duration(s.n), s.n
}

// Err returns the error of the last sample, or nil.
func (s *Sampler) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

func children(procs map[int]Proc) map[int][]int {
	kids := make(map[int][]int, len(procs))
	for pid, p := range procs {
		if p.PPID != pid {
			kids[p.PPID] = append(kids[p.PPID], pid)
		}
	}
	return kids
}

// tree returns root and all its descendants. A process whose parent is itself
// or that appears twice (it cannot) is visited once.
func tree(root int, procs map[int]Proc, kids map[int][]int) []int {
	if _, ok := procs[root]; !ok {
		return nil
	}
	out := []int{root}
	seen := map[int]bool{root: true}
	for i := 0; i < len(out); i++ {
		for _, c := range kids[out[i]] {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// update folds the newest snapshot into the window. s.mu is held.
func (s *Sampler) update(roots []int) {
	cur := s.cur
	m := cur.M
	st := &s.machine
	st.Cores = m.Cores
	if st.Cores == 0 {
		st.Cores = runtime.NumCPU()
	}
	st.MemTotal = m.MemTotal
	st.Load = m.Load
	st.Procs = len(cur.Procs)
	s.mem.add(cur.At, float64(m.MemUsed), s.window)
	st.MemUsed = Value{Now: s.mem.value().Now, Avg: s.mem.value().Avg}

	if s.prev != nil {
		dt := cur.At.Sub(s.prev.At).Seconds()
		if dt > 0 {
			pct, ok := 0.0, false
			if m.CPUTotal > 0 && s.prev.M.CPUTotal > 0 {
				if dTotal := m.CPUTotal - s.prev.M.CPUTotal; dTotal > 0 {
					pct, ok = (m.CPUBusy-s.prev.M.CPUBusy)/dTotal*100, true
				}
			} else {
				// No machine counter: add up what the processes used.
				used := 0.0
				for pid, p := range cur.Procs {
					if o, was := s.prev.Procs[pid]; was && o.Start == p.Start && p.CPU >= o.CPU {
						used += p.CPU - o.CPU
					}
				}
				pct, ok = used/(dt*float64(st.Cores))*100, true
			}
			if ok {
				s.cpu.add(cur.At, clamp(pct, 0, 100), s.window)
			}
		}
	}
	st.CPU = s.cpu.value()
	st.Samples = len(s.cpu.pts)

	kids := children(cur.Procs)
	live := map[int]bool{}
	for _, root := range roots {
		live[root] = true
		ts := s.trees[root]
		members := tree(root, cur.Procs, kids)
		if ts == nil {
			ts = &treeState{}
			s.trees[root] = ts
		}
		if len(members) == 0 {
			ts.alive, ts.procs = false, 0
			continue
		}
		rp := cur.Procs[root]
		if ts.haveCPU && ts.start != rp.Start {
			*ts = treeState{} // the PID now belongs to another process
		}
		ts.start, ts.alive, ts.procs = rp.Start, true, len(members)
		var cpu float64
		var rss uint64
		for _, pid := range members {
			p := cur.Procs[pid]
			cpu += p.CPU
			rss += p.RSS
		}
		s.addTree(ts, cur.At, cpu, rss)
	}
	for pid := range s.trees {
		if !live[pid] {
			delete(s.trees, pid)
		}
	}
}

func (s *Sampler) addTree(ts *treeState, at time.Time, cpu float64, rss uint64) {
	ts.rss.add(at, float64(rss), s.window)
	ts.life.rssSum += float64(rss)
	ts.life.rssN++
	ts.life.rssPeak = max(ts.life.rssPeak, float64(rss))
	if ts.haveCPU {
		if dt := at.Sub(ts.lastAt).Seconds(); dt > 0 {
			pct := max(cpu-ts.lastCPU, 0) / dt * 100
			ts.cpu.add(at, pct, s.window)
			ts.life.cpuSum += pct
			ts.life.cpuN++
			ts.life.cpuPeak = max(ts.life.cpuPeak, pct)
		}
	}
	ts.lastCPU, ts.lastAt, ts.haveCPU = cpu, at, true
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Machine returns the machine figures. The bool is false until the first sample.
func (s *Sampler) Machine() (MachineStat, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.machine, s.cur != nil
}

// Averages are the machine figures over the last d, taken from the same
// samples as Machine. CPU is whole-machine busy percent (0-100), MemUsed is in
// bytes. Samples counts the CPU samples inside d; MemSamples the memory ones.
type Averages struct {
	CPU        float64
	MemUsed    float64
	MemTotal   uint64
	Cores      int
	Samples    int
	MemSamples int
}

// Averages returns the averages over the last d (at most the sampler window).
// It is all zero before the first sample.
func (s *Sampler) Averages(d time.Duration) Averages {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == nil {
		return Averages{}
	}
	cut := s.cur.At.Add(-d)
	var a Averages
	a.CPU, a.Samples = s.cpu.avgSince(cut)
	a.MemUsed, a.MemSamples = s.mem.avgSince(cut)
	a.MemTotal, a.Cores = s.machine.MemTotal, s.machine.Cores
	return a
}

func (s *Sampler) treeStat(pid int, ts *treeState) TreeStat {
	return TreeStat{PID: pid, CPU: ts.cpu.value(), RSS: ts.rss.value(), Life: ts.life.value(), Procs: ts.procs,
		Samples: len(ts.cpu.pts), Alive: ts.alive}
}

// Trees returns the figures of every followed tree, by root PID.
func (s *Sampler) Trees() map[int]TreeStat {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int]TreeStat, len(s.trees))
	for pid, ts := range s.trees {
		out[pid] = s.treeStat(pid, ts)
	}
	return out
}

// Top returns the heaviest processes of the tree under root, by CPU over the
// last interval and by resident memory. ok is false when root is not a
// followed tree or is gone.
func (s *Sampler) Top(ctx context.Context, root, n int) (Top, bool) {
	s.mu.Lock()
	ts := s.trees[root]
	cur, prev := s.cur, s.prev
	if ts == nil || cur == nil || !ts.alive {
		s.mu.Unlock()
		return Top{}, false
	}
	members := tree(root, cur.Procs, children(cur.Procs))
	stat := s.treeStat(root, ts)
	rows := make([]ProcStat, 0, len(members))
	for _, pid := range members {
		p := cur.Procs[pid]
		row := ProcStat{PID: pid, Cmd: p.Comm, RSS: p.RSS}
		if prev != nil {
			if o, ok := prev.Procs[pid]; ok && o.Start == p.Start && p.CPU >= o.CPU {
				if dt := cur.At.Sub(prev.At).Seconds(); dt > 0 {
					row.CPU = (p.CPU - o.CPU) / dt * 100
				}
			}
		}
		rows = append(rows, row)
	}
	at := cur.At
	s.mu.Unlock()

	byCPU := append([]ProcStat(nil), rows...)
	sort.Slice(byCPU, func(i, j int) bool {
		if byCPU[i].CPU != byCPU[j].CPU {
			return byCPU[i].CPU > byCPU[j].CPU
		}
		return byCPU[i].PID < byCPU[j].PID
	})
	byRSS := append([]ProcStat(nil), rows...)
	sort.Slice(byRSS, func(i, j int) bool {
		if byRSS[i].RSS != byRSS[j].RSS {
			return byRSS[i].RSS > byRSS[j].RSS
		}
		return byRSS[i].PID < byRSS[j].PID
	})
	if n <= 0 {
		n = TopN
	}
	byCPU, byRSS = byCPU[:min(n, len(byCPU))], byRSS[:min(n, len(byRSS))]

	// Command lines are read only for the processes that are listed.
	want := map[int]bool{}
	for _, l := range [][]ProcStat{byCPU, byRSS} {
		for _, p := range l {
			want[p.PID] = true
		}
	}
	pids := make([]int, 0, len(want))
	for pid := range want {
		pids = append(pids, pid)
	}
	args := s.r.Args(ctx, pids)
	for _, l := range [][]ProcStat{byCPU, byRSS} {
		for i := range l {
			l[i].Args = redact.Args(args[l[i].PID])
		}
	}
	return Top{Tree: stat, ByCPU: byCPU, ByRSS: byRSS, Procs: len(members), Cutoff: at}, true
}
