package daemon

import (
	"context"
	"fmt"
	"os/user"
	"sort"
	"strconv"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// machineProcesses uses the machine sampler and the stop path's refusal rules.
// Refusals are advisory: process_stop checks identity again before signaling.
func (d *Daemon) machineProcesses(ctx context.Context, order string, read func(int) (procView, error)) ([]protocol.MachineProcessRow, error) {
	if order == "" {
		order = "rss"
	}
	if order != "rss" && order != "cpu" {
		return nil, fmt.Errorf("sort must be rss or cpu")
	}
	s := d.cfg.UsageSampler
	if s == nil {
		return nil, fmt.Errorf("this daemon does not measure processes")
	}
	if age, ok := s.SnapshotAge(); !ok || age > protocol.MachineUsageMaxAgeSeconds*time.Second {
		s.Sample(ctx)
	}
	procs, _, ok := s.TopProcs(ctx, int(^uint(0)>>1))
	if !ok {
		return nil, fmt.Errorf("process sampler needs two readings")
	}
	owners := d.procOwners()
	if roster := d.readClaudeRoster(time.Now()); roster != nil {
		for id, w := range roster.Workers {
			for _, pid := range []int{w.PID, w.ReplPID} {
				if pid > 1 {
					owners[pid] = procOwner{protocol.ProcOwnerSession, "Claude background session", id}
				}
			}
		}
	}
	rows := make([]protocol.MachineProcessRow, 0, len(procs))
	for _, p := range procs {
		v, err := read(p.PID)
		if err != nil {
			continue
		}
		o := classifyProc(p, owners)
		name := strconv.Itoa(v.UID)
		if u, err := user.LookupId(name); err == nil {
			name = u.Username
		}
		cmd := p.Args
		if cmd == "" {
			cmd = "[" + p.Cmd + "]"
		}
		rows = append(rows, protocol.MachineProcessRow{MachineProcRow: protocol.MachineProcRow{PID: p.PID, PPID: p.PPID, CPU: round1(p.CPU), RSS: p.RSS, ElapsedSeconds: int64(p.Elapsed / time.Second), Cmd: cmd, OwnerKind: o.kind, Owner: o.name, OwnerID: o.id}, User: name, Refusal: d.processRefusal(v)})
	}
	sort.Slice(rows, func(i, j int) bool {
		if order == "rss" && rows[i].RSS != rows[j].RSS {
			return rows[i].RSS > rows[j].RSS
		}
		if order == "cpu" && rows[i].CPU != rows[j].CPU {
			return rows[i].CPU > rows[j].CPU
		}
		return rows[i].PID < rows[j].PID
	})
	return rows, nil
}
