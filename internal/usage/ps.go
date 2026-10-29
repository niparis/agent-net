package usage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// cmdFunc runs a program and returns its output. Tests replace it.
type cmdFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

func runCmd(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// psReader reads processes with ps and the machine totals with sysctl and
// vm_stat. It is the macOS reader (the program is built without cgo, so it
// cannot call the Mach API that Activity Monitor uses).
//
// CPU time comes from the "time" column of ps, which has a resolution of
// 10 ms. It counts the time of processes that are alive, not of those that
// ended between two samples, and not of children their parent has waited for.
// So the CPU % of a tree misses short-lived processes, and the machine CPU %
// is the sum over live processes divided by the cores. See docs/reference.md.
type psReader struct {
	run   cmdFunc
	cores int
	total uint64
	// Load and memory change slowly and each costs a process start, so they
	// are read on every slowEvery-th sample and kept in between.
	n    int
	load [3]float64
	used uint64
}

const slowEvery = 4

func newPSReader() *psReader { return &psReader{run: runCmd} }

func (r *psReader) Read(ctx context.Context) (Snapshot, error) {
	snap := Snapshot{At: time.Now(), Procs: map[int]Proc{}}
	out, err := r.run(ctx, "ps", "-A", "-o", "pid=,ppid=,rss=,time=,comm=")
	if err != nil {
		return snap, fmt.Errorf("ps: %w", err)
	}
	snap.Procs = parsePS(out)
	if len(snap.Procs) == 0 {
		return snap, errors.New("ps listed no process")
	}
	// The core count and the memory size do not change.
	if r.cores == 0 {
		r.cores = r.sysctlInt(ctx, "hw.ncpu")
		if r.cores <= 0 {
			r.cores = runtime.NumCPU()
		}
	}
	if r.total == 0 {
		r.total = uint64(r.sysctlInt(ctx, "hw.memsize"))
	}
	snap.M.Cores, snap.M.MemTotal = r.cores, r.total
	if r.n%slowEvery == 0 {
		if out, err := r.run(ctx, "sysctl", "-n", "vm.loadavg"); err == nil {
			r.load = parseLoadavg(string(out))
		}
		if out, err := r.run(ctx, "vm_stat"); err == nil {
			r.used = parseVMStat(string(out))
		}
	}
	r.n++
	snap.M.Load, snap.M.MemUsed = r.load, r.used
	return snap, nil
}

func (r *psReader) sysctlInt(ctx context.Context, name string) int {
	out, err := r.run(ctx, "sysctl", "-n", name)
	if err != nil {
		return 0
	}
	v, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	return int(v)
}

// parsePS reads the output of: ps -A -o pid=,ppid=,rss=,time=,comm=
// The program name is last because on macOS it is a path that may hold spaces.
func parsePS(out []byte) map[int]Proc {
	procs := map[int]Proc{}
	for _, line := range bytes.Split(out, []byte("\n")) {
		f := strings.Fields(string(line))
		if len(f) < 5 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		rssKB, err3 := strconv.ParseUint(f[2], 10, 64)
		cpu, ok := parseCPUTime(f[3])
		if err1 != nil || err2 != nil || err3 != nil || !ok {
			continue
		}
		// Everything after the fourth field is the program path. Fields()
		// collapsed its spaces, which is fine for a short name.
		comm := strings.Join(f[4:], " ")
		procs[pid] = Proc{PID: pid, PPID: ppid, CPU: cpu, RSS: rssKB * 1024, Comm: filepath.Base(comm)}
	}
	return procs
}

// parseCPUTime reads the CPU time column of ps: [[d-]hh:]mm:ss[.ff].
func parseCPUTime(s string) (float64, bool) {
	days := 0.0
	if i := strings.IndexByte(s, '-'); i >= 0 {
		d, err := strconv.Atoi(s[:i])
		if err != nil {
			return 0, false
		}
		days, s = float64(d), s[i+1:]
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	total := 0.0
	for _, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil || v < 0 {
			return 0, false
		}
		total = total*60 + v
	}
	return days*86400 + total, true
}

// parseLoadavg reads the output of: sysctl -n vm.loadavg  ("{ 1.52 1.60 1.70 }").
func parseLoadavg(s string) [3]float64 {
	var l [3]float64
	f := strings.Fields(strings.Trim(strings.TrimSpace(s), "{}"))
	for i := 0; i < 3 && i < len(f); i++ {
		l[i], _ = strconv.ParseFloat(f[i], 64)
	}
	return l
}

// parseVMStat returns the memory in use in bytes, the way Activity Monitor
// counts it: app memory (anonymous pages minus purgeable ones) plus wired
// memory plus the memory the compressor occupies. File cache is not counted.
// It returns 0 when the output has none of these lines.
func parseVMStat(s string) uint64 {
	page := uint64(4096)
	vals := map[string]uint64{}
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "Mach Virtual Memory Statistics") {
			if i := strings.Index(line, "page size of "); i >= 0 {
				if f := strings.Fields(line[i+len("page size of "):]); len(f) > 0 {
					if v, err := strconv.ParseUint(f[0], 10, 64); err == nil && v > 0 {
						page = v
					}
				}
			}
			continue
		}
		i := strings.LastIndexByte(line, ':')
		if i < 0 {
			continue
		}
		v, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(line[i+1:]), "."), 10, 64)
		if err != nil {
			continue
		}
		vals[strings.Trim(strings.TrimSpace(line[:i]), `"`)] = v
	}
	anon, hasAnon := vals["Anonymous pages"]
	wired := vals["Pages wired down"]
	comp := vals["Pages occupied by compressor"]
	var pages uint64
	if hasAnon {
		app := anon
		if p := vals["Pages purgeable"]; p < app {
			app -= p
		}
		pages = app + wired + comp
	} else if a, ok := vals["Pages active"]; ok {
		// Older macOS has no anonymous page count.
		pages = a + wired + comp
	} else {
		return 0
	}
	return pages * page
}

// Args runs ps once for the wanted processes.
func (r *psReader) Args(ctx context.Context, pids []int) map[int]string {
	out := map[int]string{}
	if len(pids) == 0 {
		return out
	}
	ids := make([]string, len(pids))
	for i, p := range pids {
		ids[i] = strconv.Itoa(p)
	}
	b, err := r.run(ctx, "ps", "-ww", "-p", strings.Join(ids, ","), "-o", "pid=,args=")
	if err != nil && len(b) == 0 {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		i := strings.IndexByte(line, ' ')
		if i < 0 {
			continue
		}
		if pid, err := strconv.Atoi(line[:i]); err == nil {
			out[pid] = strings.TrimSpace(line[i+1:])
		}
	}
	return out
}
