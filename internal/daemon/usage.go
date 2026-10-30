package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/usage"
)

// This file reports CPU and memory: of the machine, and of the process tree of
// each local session. The sampler takes a reading every few seconds; this file
// sends the summary to the gateway every UsageReportSeconds and answers the
// owner's requests for it. See decision 0007.

// defaultUsageReportInterval is how often the summary goes to the gateway.
const defaultUsageReportInterval = protocol.UsageReportSeconds * time.Second

// usageState is the daemon's side of usage reporting.
type usageState struct {
	mu sync.Mutex
	// off is the link that answered "unknown frame type": a gateway from
	// before this feature. No more reports go over that link; a new link
	// (a new gateway) is tried again.
	off *link
}

func (u *usageState) disabledFor(l *link) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.off == l
}

func (u *usageState) disable(l *link) {
	u.mu.Lock()
	u.off = l
	u.mu.Unlock()
}

// startUsage runs the sampler and the reporter when a sampler was configured,
// and the quota reader when quota-axi is available or can be installed
// (decision 0022).
func (d *Daemon) startUsage(ctx context.Context) {
	s := d.cfg.UsageSampler
	if s != nil {
		s.Watch = d.watchedPIDs
		d.wg.Add(1)
		go func() { defer d.wg.Done(); s.Run(ctx) }()
	}
	d.startQuota(ctx)
	if s == nil {
		// A machine with only quotas (no usage sampler) still reports, because
		// startQuota may install quota-axi and begin reading.
		if !quotaAllowed(ConfigDir()) {
			if _, ok := FindQuotaAXI(d.cfg.QuotaAXIPath); !ok {
				return
			}
		}
	}
	d.wg.Add(1)
	go func() { defer d.wg.Done(); d.usageLoop(ctx) }()
}

// watchedPIDs lists the process of every local session.
func (d *Daemon) watchedPIDs() []int {
	seen := map[int]bool{}
	var out []int
	d.mu.Lock()
	for _, ls := range d.sessions {
		if pid := ls.info.PID; pid > 0 && !seen[pid] {
			seen[pid] = true
			out = append(out, pid)
		}
	}
	d.mu.Unlock()
	// A worker's process is followed even before its session is known, so
	// its whole life is measured (see cost.go).
	d.wmu.Lock()
	for _, w := range d.workers {
		if w.pid > 0 && !seen[w.pid] {
			seen[w.pid] = true
			out = append(out, w.pid)
		}
	}
	d.wmu.Unlock()
	return out
}

func (d *Daemon) usageLoop(ctx context.Context) {
	every := d.cfg.UsageReportInterval
	if every <= 0 {
		every = defaultUsageReportInterval
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		d.mu.Lock()
		l := d.lk
		d.mu.Unlock()
		if l == nil || d.usage.disabledFor(l) {
			continue
		}
		rep, ok := d.usageReport()
		if !ok {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := l.call(cctx, "node.usage", rep)
		cancel()
		var re RemoteError
		if errors.As(err, &re) && strings.Contains(string(re), "unknown frame type") {
			d.Logf("the gateway is too old for CPU and memory reports, not sending them on this connection")
			d.usage.disable(l)
		}
	}
}

// usageReport builds the summary for the gateway. ok is false before the first sample.
func (d *Daemon) usageReport() (protocol.UsageReport, bool) {
	quotas, quotaError := d.quota.report()
	rep := protocol.UsageReport{Trees: []protocol.TreeUsage{}, Workers: d.workerLoad(), Quotas: quotas, QuotaError: quotaError, QuotaNote: d.quota.noteText()}
	s := d.cfg.UsageSampler
	if s != nil {
		m, ok := s.Machine()
		if ok {
			rep.Machine = machineUsage(m)
		} else if rep.Quotas == nil && rep.QuotaError == "" && rep.QuotaNote == "" {
			return protocol.UsageReport{}, false
		}
	} else if rep.Quotas == nil && rep.QuotaError == "" && rep.QuotaNote == "" {
		return protocol.UsageReport{}, false
	}
	// Group the sessions by process: OpenCode runs many in one.
	type group struct {
		harness string
		ids     []string
		count   int
	}
	groups := map[int]*group{}
	d.mu.Lock()
	for _, ls := range d.sessions {
		pid := ls.info.PID
		if pid <= 0 {
			continue
		}
		g := groups[pid]
		if g == nil {
			g = &group{harness: ls.info.Harness}
			groups[pid] = g
		}
		g.count++
		if ls.sessionID != "" {
			g.ids = append(g.ids, ls.sessionID)
		}
	}
	d.mu.Unlock()
	trees := map[int]usage.TreeStat{}
	if s != nil {
		trees = s.Trees()
	}
	for pid, g := range groups {
		ts, ok := trees[pid]
		if !ok || !ts.Alive || len(g.ids) == 0 {
			continue
		}
		sort.Strings(g.ids)
		tu := treeUsage(ts)
		tu.Harness, tu.Sessions = g.harness, g.ids
		tu.Shared = g.harness == "opencode" || g.count > 1
		rep.Trees = append(rep.Trees, tu)
	}
	sort.Slice(rep.Trees, func(i, j int) bool { return rep.Trees[i].PID < rep.Trees[j].PID })
	return rep, true
}

func machineUsage(m usage.MachineStat) protocol.MachineUsage {
	return protocol.MachineUsage{Cores: m.Cores, CPU: round1(m.CPU.Now), CPUAvg: round1(m.CPU.Avg),
		MemUsed: uint64(m.MemUsed.Now), MemUsedAvg: uint64(m.MemUsed.Avg), MemTotal: m.MemTotal,
		Load: m.Load, Procs: m.Procs, Samples: m.Samples}
}

func treeUsage(t usage.TreeStat) protocol.TreeUsage {
	return protocol.TreeUsage{PID: t.PID, CPU: round1(t.CPU.Now), CPUAvg: round1(t.CPU.Avg),
		RSS: uint64(t.RSS.Now), RSSAvg: uint64(t.RSS.Avg), Procs: t.Procs, Samples: t.Samples}
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

// ---- the methods ----

// localUsage answers the "usage" method: this machine's own report, read now,
// and what the gateway holds for every machine. The gateway part is left out,
// with the reason, when it cannot be asked.
func (d *Daemon) localUsage(ctx context.Context) (any, error) {
	out := map[string]any{"node_id": d.cfg.NodeID, "hostname": d.hostName()}
	if rep, ok := d.usageReport(); ok {
		out["local"] = rep
	}
	data, err := d.call(ctx, "query.usage", struct{}{})
	switch {
	case err == nil:
		out["gateway"] = json.RawMessage(data)
	case strings.Contains(err.Error(), "unknown frame type"):
		out["gateway_error"] = "the gateway is too old to collect CPU and memory reports"
	default:
		out["gateway_error"] = err.Error()
	}
	return out, nil
}

// localUsageTop answers for a session of this machine.
func (d *Daemon) localUsageTop(ctx context.Context, sessionID string) (protocol.UsageTop, error) {
	s := d.cfg.UsageSampler
	if s == nil {
		return protocol.UsageTop{}, errors.New("this daemon does not measure CPU and memory")
	}
	ls := d.sessionByID(sessionID)
	if ls == nil {
		return protocol.UsageTop{}, errors.New("no such session on this machine")
	}
	info := d.infoOf(ls)
	if info.PID <= 0 {
		return protocol.UsageTop{}, errors.New("the process of this session is not known")
	}
	top, ok := s.Top(ctx, info.PID, usage.TopN)
	if !ok {
		return protocol.UsageTop{}, errors.New("no figures yet for this session (the sampler needs two readings, about 10 seconds)")
	}
	tu := treeUsage(top.Tree)
	tu.Harness = info.Harness
	d.mu.Lock()
	n := 0
	for _, o := range d.sessions {
		if o.info.PID == info.PID {
			tu.Sessions = append(tu.Sessions, o.sessionID)
			n++
		}
	}
	d.mu.Unlock()
	sort.Strings(tu.Sessions)
	tu.Shared = info.Harness == "opencode" || n > 1
	conv := func(in []usage.ProcStat) []protocol.ProcUsage {
		out := make([]protocol.ProcUsage, 0, len(in))
		for _, p := range in {
			out = append(out, protocol.ProcUsage{PID: p.PID, Cmd: p.Cmd, Args: p.Args, CPU: round1(p.CPU), RSS: p.RSS})
		}
		return out
	}
	return protocol.UsageTop{SessionID: sessionID, Tree: tu, ByCPU: conv(top.ByCPU), ByRSS: conv(top.ByRSS)}, nil
}
