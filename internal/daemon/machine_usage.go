package daemon

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/usage"
)

// This file answers node.machine_usage: the load and the busiest processes of
// this machine, for agent_machine_get. It uses the readings the sampler
// already holds (usage.go). It starts no loop. When the newest reading is older
// than protocol.MachineUsageMaxAgeSeconds, it takes one reading first.

// procOwner says who a process belongs to.
type procOwner struct{ kind, name, id string }

// machineUsageNow builds the usage block of this machine.
func (d *Daemon) machineUsageNow(ctx context.Context) protocol.MachineUsageNow {
	out := protocol.MachineUsageNow{Status: protocol.MachineUsageUnavailable}
	s := d.cfg.UsageSampler
	if s == nil {
		out.Note = "this daemon does not measure CPU and memory"
		return out
	}
	maxAge := protocol.MachineUsageMaxAgeSeconds * time.Second
	if age, ok := s.SnapshotAge(); !ok || age > maxAge {
		s.Sample(ctx)
	}
	m, ok := s.Machine()
	if !ok {
		out.Note = "no reading yet: " + usageErrText(s.Err(), "the sampler has not run")
		return out
	}
	if age, ok := s.SnapshotAge(); ok {
		out.AgeSeconds = int(age.Seconds())
	}
	out.Cores = m.Cores
	out.Load = []float64{round2(m.Load[0]), round2(m.Load[1]), round2(m.Load[2])}
	out.CPUPct = round1(m.CPU.Now)
	out.MemUsed, out.MemTotal = uint64(m.MemUsed.Now), m.MemTotal
	out.SwapUsed, out.SwapTotal = m.SwapUsed, m.SwapTotal
	if m.HaveSplit {
		out.Split = &protocol.MachineCPUSplit{User: round1(m.Split.User), Sys: round1(m.Split.Sys), Nice: round1(m.Split.Nice),
			Idle: round1(m.Split.Idle), IOWait: round1(m.Split.IOWait), Steal: round1(m.Split.Steal)}
	}
	procs, _, ok := s.TopProcs(ctx, protocol.MachineUsageTopN)
	if !ok {
		out.Note = "no per-process figures yet: the sampler needs two readings, about 10 seconds"
		out.Status = protocol.MachineUsageOK
		return out
	}
	owners := d.procOwners()
	out.Status = protocol.MachineUsageOK
	out.Top = make([]protocol.MachineProcRow, 0, len(procs))
	for _, p := range procs {
		cmd := p.Args
		if cmd == "" {
			cmd = "[" + p.Cmd + "]"
		}
		o := classifyProc(p, owners)
		out.Top = append(out.Top, protocol.MachineProcRow{PID: p.PID, PPID: p.PPID, CPU: round1(p.CPU), RSS: p.RSS,
			ElapsedSeconds: int64(p.Elapsed / time.Second), Cmd: cmd, OwnerKind: o.kind, Owner: o.name, OwnerID: o.id})
	}
	return out
}

func usageErrText(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

// procOwners maps the root process of every known tree to its owner: sessions
// and workers, hosted MCP servers and this daemon. A worker's own record wins
// over its session, so the id shown is the worker id.
func (d *Daemon) procOwners() map[int]procOwner {
	owners := map[int]procOwner{os.Getpid(): {protocol.ProcOwnerDaemon, "agent-net daemon", ""}}
	d.mu.Lock()
	shared := map[int][]string{}
	for _, ls := range d.sessions {
		pid := ls.info.PID
		if pid <= 0 {
			continue
		}
		name := ls.info.Name
		if name == "" {
			name = ls.info.Harness
		}
		kind, id := protocol.ProcOwnerSession, ls.sessionID
		if w := ls.info.Worker; w != nil && w.ID != "" {
			kind, id = protocol.ProcOwnerWorker, w.ID
		}
		if old, dup := owners[pid]; dup && old.kind != protocol.ProcOwnerDaemon {
			// Several sessions in one process (OpenCode): one shared owner.
			shared[pid] = append(shared[pid], id)
			continue
		}
		owners[pid] = procOwner{kind, name, id}
		shared[pid] = []string{id}
	}
	d.mu.Unlock()
	for pid, ids := range shared {
		if len(ids) > 1 {
			sort.Strings(ids)
			o := owners[pid]
			owners[pid] = procOwner{o.kind, strings.TrimSpace(o.name + " (shared by " + strconv.Itoa(len(ids)) + " sessions)"), ids[0]}
		}
	}
	d.wmu.Lock()
	for _, w := range d.workers {
		if w.pid <= 0 {
			continue
		}
		name := w.title
		if name == "" {
			name = w.project
		}
		owners[w.pid] = procOwner{protocol.ProcOwnerWorker, name, w.id}
	}
	d.wmu.Unlock()
	if d.mcp != nil {
		d.mcp.mu.Lock()
		runners := make([]*mcpRunner, 0, len(d.mcp.runners))
		for _, r := range d.mcp.runners {
			runners = append(runners, r)
		}
		d.mcp.mu.Unlock()
		for _, r := range runners {
			r.mu.Lock()
			pgid := r.pgid
			r.mu.Unlock()
			if pgid > 1 {
				owners[pgid] = procOwner{protocol.ProcOwnerMCP, "mcp server " + r.spec.Name, ""}
			}
		}
	}
	return owners
}

// classifyProc finds the nearest known ancestor of a process, starting with
// the process itself. A process with none is "agent-net gateway" when its
// command line says so, else "other".
func classifyProc(p usage.MachineProc, owners map[int]procOwner) procOwner {
	if o, ok := owners[p.PID]; ok {
		return o
	}
	for _, pid := range p.Lineage {
		if o, ok := owners[pid]; ok {
			return o
		}
	}
	if f := strings.Fields(p.Args); len(f) >= 2 && filepath.Base(f[0]) == "agent-net" && f[1] == "gateway" {
		return procOwner{protocol.ProcOwnerGateway, "agent-net gateway", ""}
	}
	return procOwner{protocol.ProcOwnerOther, "other", ""}
}
