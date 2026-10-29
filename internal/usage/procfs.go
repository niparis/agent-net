package usage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// clkTck is the unit of the CPU times in /proc/<pid>/stat. It is 100 on every
// Linux the program supports (USER_HZ).
const clkTck = 100.0

// procFS reads a Linux /proc.
type procFS struct {
	root     string // /proc, or a fixture
	pageSize uint64
	buf      []byte
}

func newProcFS(root string) *procFS {
	return &procFS{root: root, pageSize: uint64(os.Getpagesize()), buf: make([]byte, 4096)}
}

func (p *procFS) Read(ctx context.Context) (Snapshot, error) {
	snap := Snapshot{At: time.Now(), Procs: map[int]Proc{}}
	if err := p.readStat(&snap.M); err != nil {
		return snap, err
	}
	p.readMem(&snap.M)
	p.readLoad(&snap.M)
	ents, err := os.ReadDir(p.root)
	if err != nil {
		return snap, err
	}
	for _, e := range ents {
		name := e.Name()
		if name[0] < '0' || name[0] > '9' {
			continue
		}
		pid, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		// A process may end while we look: skip it.
		if pr, ok := p.readProc(pid); ok {
			snap.Procs[pid] = pr
		}
	}
	return snap, nil
}

// readFile reads a small file into the shared buffer.
func (p *procFS) readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	n := 0
	for {
		if n == len(p.buf) {
			p.buf = append(p.buf, make([]byte, len(p.buf))...)
		}
		m, err := f.Read(p.buf[n:])
		n += m
		if err != nil {
			break
		}
	}
	return p.buf[:n], nil
}

func (p *procFS) readProc(pid int) (Proc, bool) {
	b, err := p.readFile(filepath.Join(p.root, strconv.Itoa(pid), "stat"))
	if err != nil {
		return Proc{}, false
	}
	return parseProcStat(b, p.pageSize)
}

// parseProcStat reads /proc/<pid>/stat. The name is in parentheses and may
// hold spaces and parentheses, so the fields are counted after the last ")".
func parseProcStat(b []byte, pageSize uint64) (Proc, bool) {
	open := bytes.IndexByte(b, '(')
	closing := bytes.LastIndexByte(b, ')')
	if open < 0 || closing < open {
		return Proc{}, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b[:open])))
	if err != nil {
		return Proc{}, false
	}
	f := strings.Fields(string(b[closing+1:]))
	// f[0] is field 3 (state); field N is f[N-3].
	if len(f) < 22 {
		return Proc{}, false
	}
	num := func(i int) uint64 { v, _ := strconv.ParseUint(f[i], 10, 64); return v }
	ppid, _ := strconv.Atoi(f[1])
	ticks := num(11) + num(12) + num(13) + num(14) // utime stime cutime cstime
	rss, _ := strconv.ParseInt(f[21], 10, 64)
	if rss < 0 {
		rss = 0
	}
	return Proc{PID: pid, PPID: ppid, Start: num(19), CPU: float64(ticks) / clkTck,
		RSS: uint64(rss) * pageSize, Comm: string(b[open+1 : closing])}, true
}

func (p *procFS) readStat(m *Machine) error {
	b, err := os.ReadFile(filepath.Join(p.root, "stat"))
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "cpu") {
			break
		}
		f := strings.Fields(line)
		if f[0] != "cpu" {
			m.Cores++
			continue
		}
		var v [8]float64
		for i := 0; i < 8 && i+1 < len(f); i++ {
			v[i], _ = strconv.ParseFloat(f[i+1], 64)
		}
		// user nice system idle iowait irq softirq steal
		idle := v[3] + v[4]
		for _, x := range v {
			m.CPUTotal += x
		}
		m.CPUBusy = m.CPUTotal - idle
	}
	if m.CPUTotal == 0 {
		return errors.New("no cpu line in /proc/stat")
	}
	return nil
}

func (p *procFS) readMem(m *Machine) {
	b, err := os.ReadFile(filepath.Join(p.root, "meminfo"))
	if err != nil {
		return
	}
	var total, avail uint64
	var haveAvail bool
	var free, buffers, cached uint64
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(f[1], 10, 64)
		v *= 1024 // kB
		switch f[0] {
		case "MemTotal:":
			total = v
		case "MemAvailable:":
			avail, haveAvail = v, true
		case "MemFree:":
			free = v
		case "Buffers:":
			buffers = v
		case "Cached:":
			cached = v
		}
	}
	if !haveAvail { // kernels before 3.14
		avail = free + buffers + cached
	}
	m.MemTotal = total
	if total > avail {
		m.MemUsed = total - avail
	}
}

func (p *procFS) readLoad(m *Machine) {
	b, err := os.ReadFile(filepath.Join(p.root, "loadavg"))
	if err != nil {
		return
	}
	f := strings.Fields(string(b))
	for i := 0; i < 3 && i < len(f); i++ {
		m.Load[i], _ = strconv.ParseFloat(f[i], 64)
	}
}

func (p *procFS) Args(ctx context.Context, pids []int) map[int]string {
	out := map[int]string{}
	for _, pid := range pids {
		b, err := os.ReadFile(filepath.Join(p.root, strconv.Itoa(pid), "cmdline"))
		if err != nil {
			continue
		}
		s := strings.TrimRight(string(b), "\x00")
		out[pid] = strings.ReplaceAll(s, "\x00", " ")
	}
	return out
}
