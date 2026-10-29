package usage

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func near(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %v, want %v (+- %v)", what, got, want, tol)
	}
}

// ---- /proc parsing ----

func TestParseProcStatHandlesOddNames(t *testing.T) {
	// The name holds a space and a closing parenthesis.
	line := "4242 (my (odd) prog) S 17 4242 4242 0 -1 4194560 100 0 0 0 150 50 30 20 20 0 3 0 99999 1000000 250 18446744073709551615"
	p, ok := parseProcStat([]byte(line), 4096)
	if !ok {
		t.Fatal("not parsed")
	}
	if p.PID != 4242 || p.PPID != 17 || p.Comm != "my (odd) prog" {
		t.Errorf("got %+v", p)
	}
	near(t, "cpu seconds", p.CPU, (150+50+30+20)/100.0, 1e-9)
	if p.RSS != 250*4096 {
		t.Errorf("rss = %d", p.RSS)
	}
	if p.Start != 99999 {
		t.Errorf("start = %d", p.Start)
	}
}

func TestParseProcStatRejectsShortLines(t *testing.T) {
	if _, ok := parseProcStat([]byte("1 (init) S 0"), 4096); ok {
		t.Error("short line accepted")
	}
	if _, ok := parseProcStat([]byte("garbage"), 4096); ok {
		t.Error("garbage accepted")
	}
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProcFSReadsAFixture(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "stat"), "cpu  100 0 100 700 100 0 0 0 0 0\ncpu0 50 0 50 350 50 0 0 0 0 0\ncpu1 50 0 50 350 50 0 0 0 0 0\nintr 1\n")
	writeFile(t, filepath.Join(root, "meminfo"), "MemTotal:       8000000 kB\nMemFree:         500000 kB\nMemAvailable:   2000000 kB\n")
	writeFile(t, filepath.Join(root, "loadavg"), "1.50 0.75 0.25 2/300 999\n")
	writeFile(t, filepath.Join(root, "10", "stat"), "10 (a) S 1 10 10 0 -1 0 0 0 0 0 100 0 0 0 20 0 1 0 5 0 10 0\n")
	writeFile(t, filepath.Join(root, "11", "stat"), "11 (b) S 10 10 10 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 6 0 20 0\n")
	writeFile(t, filepath.Join(root, "11", "cmdline"), "node\x00server.js\x00--port\x003000\x00")
	writeFile(t, filepath.Join(root, "self", "stat"), "not a pid dir")
	p := newProcFS(root)
	p.pageSize = 4096
	snap, err := p.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.M.Cores != 2 {
		t.Errorf("cores = %d", snap.M.Cores)
	}
	near(t, "busy", snap.M.CPUBusy, 200, 1e-9) // 1000 total - 700 idle - 100 iowait
	near(t, "total", snap.M.CPUTotal, 1000, 1e-9)
	if snap.M.MemTotal != 8000000*1024 || snap.M.MemUsed != 6000000*1024 {
		t.Errorf("mem = %d / %d", snap.M.MemUsed, snap.M.MemTotal)
	}
	if snap.M.Load != [3]float64{1.5, 0.75, 0.25} {
		t.Errorf("load = %v", snap.M.Load)
	}
	if len(snap.Procs) != 2 || snap.Procs[11].PPID != 10 || snap.Procs[11].RSS != 20*4096 {
		t.Errorf("procs = %+v", snap.Procs)
	}
	args := p.Args(context.Background(), []int{11, 99})
	if args[11] != "node server.js --port 3000" || len(args) != 1 {
		t.Errorf("args = %v", args)
	}
}

// ---- macOS parsing ----

func TestParsePS(t *testing.T) {
	out := []byte(`    1     0  12000   0:12.34 /sbin/launchd
  501     1   4096 1:02:03.45 /Applications/Visual Studio Code.app/Contents/MacOS/Electron
  777   501 204800   3-04:05:06 /usr/local/bin/node
bad line
`)
	p := parsePS(out)
	if len(p) != 3 {
		t.Fatalf("procs = %+v", p)
	}
	if p[1].Comm != "launchd" || p[1].RSS != 12000*1024 {
		t.Errorf("launchd = %+v", p[1])
	}
	near(t, "launchd cpu", p[1].CPU, 12.34, 1e-9)
	if p[501].Comm != "Electron" {
		t.Errorf("name with spaces = %q", p[501].Comm)
	}
	near(t, "electron cpu", p[501].CPU, 3600+2*60+3.45, 1e-9)
	near(t, "node cpu", p[777].CPU, 3*86400+4*3600+5*60+6, 1e-9)
	if p[777].PPID != 501 {
		t.Errorf("ppid = %d", p[777].PPID)
	}
}

func TestParseCPUTimeRejectsJunk(t *testing.T) {
	for _, s := range []string{"", "12", "a:b", "1:2:3:4", "x-1:00"} {
		if _, ok := parseCPUTime(s); ok {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestParseLoadavgAndVMStat(t *testing.T) {
	if l := parseLoadavg("{ 2.45 2.30 2.20 }\n"); l != [3]float64{2.45, 2.30, 2.20} {
		t.Errorf("load = %v", l)
	}
	vm := `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                               3331.
Pages active:                           240000.
Pages inactive:                         230000.
Pages speculative:                        2000.
Pages wired down:                        90000.
Pages purgeable:                          5000.
"Translation faults":                  1234567.
File-backed pages:                      150000.
Anonymous pages:                        320000.
Pages stored in compressor:             200000.
Pages occupied by compressor:            80000.
`
	// (320000 - 5000 + 90000 + 80000) pages of 16 KiB.
	want := uint64(485000) * 16384
	if got := parseVMStat(vm); got != want {
		t.Errorf("used = %d, want %d", got, want)
	}
	if parseVMStat("nothing useful") != 0 {
		t.Error("junk should give 0")
	}
}

func TestPSReaderWithFakeCommands(t *testing.T) {
	r := &psReader{run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "ps":
			if args[0] == "-A" {
				return []byte("  1  0  1000  0:01.00 /sbin/launchd\n 20  1  2000  0:02.00 /bin/zsh\n"), nil
			}
			return []byte("  20 /bin/zsh -l --token abc\n"), nil
		case "sysctl":
			switch args[1] {
			case "hw.ncpu":
				return []byte("8\n"), nil
			case "hw.memsize":
				return []byte("17179869184\n"), nil
			case "vm.loadavg":
				return []byte("{ 1.00 2.00 3.00 }\n"), nil
			}
		case "vm_stat":
			return []byte("Mach Virtual Memory Statistics: (page size of 4096 bytes)\nAnonymous pages: 100.\nPages wired down: 50.\nPages occupied by compressor: 10.\nPages purgeable: 0.\n"), nil
		}
		t.Fatalf("unexpected command %s %v", name, args)
		return nil, nil
	}}
	snap, err := r.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.M.Cores != 8 || snap.M.MemTotal != 17179869184 || snap.M.MemUsed != 160*4096 || snap.M.Load[2] != 3 {
		t.Errorf("machine = %+v", snap.M)
	}
	if len(snap.Procs) != 2 {
		t.Errorf("procs = %+v", snap.Procs)
	}
	if a := r.Args(context.Background(), []int{20}); a[20] != "/bin/zsh -l --token abc" {
		t.Errorf("args = %v", a)
	}
}

// ---- the sampler, with a fake machine ----

type fakeReader struct {
	snaps []Snapshot
	i     int
	args  map[int]string
}

func (f *fakeReader) Read(ctx context.Context) (Snapshot, error) {
	s := f.snaps[min(f.i, len(f.snaps)-1)]
	f.i++
	return s, nil
}

func (f *fakeReader) Args(ctx context.Context, pids []int) map[int]string {
	out := map[int]string{}
	for _, p := range pids {
		if a, ok := f.args[p]; ok {
			out[p] = a
		}
	}
	return out
}

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// snapAt builds a snapshot taken sec seconds after t0. cpu is the CPU seconds
// of pids 100 (root), 101 (child), 102 (grandchild) and 200 (outside the tree).
func snapAt(sec int, busy, total float64, cpu [4]float64, rss [4]uint64) Snapshot {
	procs := map[int]Proc{
		1:   {PID: 1, PPID: 0, Start: 1, Comm: "init"},
		100: {PID: 100, PPID: 1, Start: 10, CPU: cpu[0], RSS: rss[0], Comm: "claude"},
		101: {PID: 101, PPID: 100, Start: 11, CPU: cpu[1], RSS: rss[1], Comm: "node"},
		102: {PID: 102, PPID: 101, Start: 12, CPU: cpu[2], RSS: rss[2], Comm: "vite"},
		200: {PID: 200, PPID: 1, Start: 20, CPU: cpu[3], RSS: rss[3], Comm: "other"},
	}
	return Snapshot{At: t0.Add(time.Duration(sec) * time.Second), Procs: procs,
		M: Machine{Cores: 4, CPUBusy: busy, CPUTotal: total, MemUsed: 4 << 30, MemTotal: 8 << 30, Load: [3]float64{1, 2, 3}}}
}

func newFake(snaps ...Snapshot) (*Sampler, *fakeReader) {
	fr := &fakeReader{snaps: snaps, args: map[int]string{
		100: "claude --dangerously --token sk-abcdefghijklmnopqrstuvwxyz0123",
		101: "node /srv/mcp.js",
		102: "vite --port 5173",
		200: "other",
	}}
	return NewWith(fr, 5*time.Second, 5*time.Minute, func() []int { return []int{100} }), fr
}

func TestSamplerMachineAndTreeFigures(t *testing.T) {
	s, _ := newFake(
		snapAt(0, 100, 400, [4]float64{10, 1, 1, 5}, [4]uint64{100, 50, 25, 999}),
		snapAt(5, 250, 800, [4]float64{12, 2, 1.5, 9}, [4]uint64{100, 60, 40, 999}),
		snapAt(10, 350, 1200, [4]float64{12, 2, 1.5, 9}, [4]uint64{100, 60, 20, 999}),
	)
	ctx := context.Background()
	s.Sample(ctx)
	if _, ok := s.Machine(); !ok {
		t.Fatal("no machine data after the first sample")
	}
	s.Sample(ctx)
	m, _ := s.Machine()
	// The first sample gives no figure, the second gives 150 busy of 400.
	near(t, "machine cpu", m.CPU.Now, 37.5, 1e-9)
	if m.Cores != 4 || m.Procs != 5 || m.MemTotal != 8<<30 || m.Load[1] != 2 {
		t.Errorf("machine = %+v", m)
	}
	tr := s.Trees()[100]
	if !tr.Alive || tr.Procs != 3 {
		t.Fatalf("tree = %+v", tr)
	}
	// CPU of the tree grew from 12 to 15.5 seconds in 5 s: 70% of one core.
	near(t, "tree cpu", tr.CPU.Now, 70, 1e-9)
	if tr.RSS.Now != 200 {
		t.Errorf("tree rss = %v", tr.RSS.Now)
	}
	s.Sample(ctx)
	m, _ = s.Machine()
	near(t, "machine cpu now", m.CPU.Now, 25, 1e-9)
	near(t, "machine cpu avg", m.CPU.Avg, (37.5+25)/2, 1e-9)
	tr = s.Trees()[100]
	near(t, "tree cpu now", tr.CPU.Now, 0, 1e-9)
	near(t, "tree cpu avg", tr.CPU.Avg, 35, 1e-9)
	near(t, "tree rss avg", tr.RSS.Avg, (175+200+180)/3.0, 1e-9)
	if tr.Samples != 2 {
		t.Errorf("samples = %d", tr.Samples)
	}
}

func TestSamplerWithoutMachineCounterSumsProcesses(t *testing.T) {
	a := snapAt(0, 0, 0, [4]float64{0, 0, 0, 0}, [4]uint64{})
	b := snapAt(5, 0, 0, [4]float64{5, 0, 0, 5}, [4]uint64{}) // 10 CPU seconds on 4 cores in 5 s = 50%
	s, _ := newFake(a, b)
	s.Sample(context.Background())
	s.Sample(context.Background())
	m, _ := s.Machine()
	near(t, "machine cpu", m.CPU.Now, 50, 1e-9)
}

func TestSamplerWindowDropsOldSamples(t *testing.T) {
	var snaps []Snapshot
	for i := 0; i <= 80; i++ {
		// 20 busy ticks per 100 for the first 40 samples, then idle.
		busy := float64(min(i, 40) * 20)
		snaps = append(snaps, snapAt(i*5, busy, float64(i*100), [4]float64{}, [4]uint64{}))
	}
	s, _ := newFake(snaps...)
	for range snaps {
		s.Sample(context.Background())
	}
	m, _ := s.Machine()
	// 400 s have passed. The window is 300 s, so the busy period (samples 1-40, up to 200 s) has partly left.
	if m.Samples > 61 || m.Samples < 59 {
		t.Errorf("window holds %d samples, want about 60", m.Samples)
	}
	near(t, "now", m.CPU.Now, 0, 1e-9)
	if m.CPU.Avg > 20*(float64(m.Samples)-20)/float64(m.Samples)+0.01 || m.CPU.Avg < 1 {
		t.Errorf("avg = %v with %d samples", m.CPU.Avg, m.Samples)
	}
}

func TestSamplerDropsTreesThatAreNotWatched(t *testing.T) {
	s, _ := newFake(snapAt(0, 0, 0, [4]float64{}, [4]uint64{}), snapAt(5, 0, 0, [4]float64{}, [4]uint64{}))
	roots := []int{100, 200}
	s.Watch = func() []int { return roots }
	s.Sample(context.Background())
	if len(s.Trees()) != 2 {
		t.Fatalf("trees = %v", s.Trees())
	}
	roots = []int{200}
	s.Sample(context.Background())
	if _, ok := s.Trees()[100]; ok || len(s.Trees()) != 1 {
		t.Errorf("trees = %v", s.Trees())
	}
}

func TestSamplerNoticesAReusedPID(t *testing.T) {
	a := snapAt(0, 0, 0, [4]float64{100, 0, 0, 0}, [4]uint64{})
	b := snapAt(5, 0, 0, [4]float64{1, 0, 0, 0}, [4]uint64{})
	p := b.Procs[100]
	p.Start = 999 // a new process got PID 100, and its CPU time is lower
	b.Procs[100] = p
	s, _ := newFake(a, b)
	s.Sample(context.Background())
	s.Sample(context.Background())
	if tr := s.Trees()[100]; tr.Samples != 0 {
		t.Errorf("figures carried over to a new process: %+v", tr)
	}
}

func TestTopListsHeaviestProcessesAndMasksArgs(t *testing.T) {
	s, _ := newFake(
		snapAt(0, 0, 0, [4]float64{0, 0, 0, 0}, [4]uint64{100, 5000, 300, 99999}),
		snapAt(5, 0, 0, [4]float64{0.5, 4, 0, 50}, [4]uint64{100, 5000, 300, 99999}),
	)
	s.Sample(context.Background())
	s.Sample(context.Background())
	top, ok := s.Top(context.Background(), 100, 2)
	if !ok {
		t.Fatal("no top")
	}
	if top.Procs != 3 || len(top.ByCPU) != 2 || len(top.ByRSS) != 2 {
		t.Fatalf("top = %+v", top)
	}
	if top.ByCPU[0].PID != 101 || top.ByCPU[1].PID != 100 {
		t.Errorf("by cpu = %+v", top.ByCPU)
	}
	near(t, "node cpu", top.ByCPU[0].CPU, 80, 1e-9)
	if top.ByRSS[0].PID != 101 || top.ByRSS[1].PID != 102 {
		t.Errorf("by rss = %+v", top.ByRSS)
	}
	for _, l := range [][]ProcStat{top.ByCPU, top.ByRSS} {
		for _, p := range l {
			if strings.Contains(p.Args, "sk-abc") || len([]rune(p.Args)) > 80 {
				t.Errorf("unsafe args %q", p.Args)
			}
		}
	}
	if top.ByCPU[1].Args == "" || !strings.Contains(top.ByCPU[1].Args, "[REDACTED]") {
		t.Errorf("args of claude = %q", top.ByCPU[1].Args)
	}
	if _, ok := s.Top(context.Background(), 4242, 5); ok {
		t.Error("top for an unwatched tree")
	}
}

// BenchmarkSampleProc is the cost of one sample of the real /proc with one
// followed tree: go test -bench Sample ./internal/usage
func BenchmarkSampleProc(b *testing.B) {
	if runtime.GOOS != "linux" {
		b.Skip("reads /proc")
	}
	s := NewWith(newProcFS("/proc"), time.Second, time.Minute, func() []int { return []int{os.Getpid()} })
	ctx := context.Background()
	s.Sample(ctx)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Sample(ctx)
	}
}

// BenchmarkSamplePS is the cost of the macOS reader's ps call, run here on
// Linux as a rough guide (sysctl and vm_stat do not exist here, so the machine
// totals are skipped; on a Mac they add two small commands).
func BenchmarkSamplePS(b *testing.B) {
	if _, err := exec.LookPath("ps"); err != nil {
		b.Skip("no ps")
	}
	s := NewWith(newPSReader(), time.Second, time.Minute, func() []int { return []int{os.Getpid()} })
	ctx := context.Background()
	s.Sample(ctx)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Sample(ctx)
	}
}

// ---- the real thing, on Linux ----

func TestRealProcFSSeesATreeAndItsCPU(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	// A shell that burns one core and has a background child.
	cmd := exec.Command("sh", "-c", "sleep 30 & while :; do :; done")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	pid := cmd.Process.Pid
	s := NewWith(newProcFS("/proc"), time.Second, time.Minute, func() []int { return []int{pid} })
	ctx := context.Background()
	s.Sample(ctx)
	time.Sleep(1200 * time.Millisecond)
	s.Sample(ctx)
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	tr := s.Trees()[pid]
	if !tr.Alive || tr.Procs < 2 {
		t.Fatalf("tree = %+v", tr)
	}
	if tr.CPU.Now < 50 || tr.CPU.Now > 130 {
		t.Errorf("busy loop shows %v%% CPU, want about 100", tr.CPU.Now)
	}
	if tr.RSS.Now == 0 {
		t.Error("no memory")
	}
	m, ok := s.Machine()
	if !ok || m.Cores < 1 || m.MemTotal == 0 || m.Procs < 2 || m.CPU.Now < 0 || m.CPU.Now > 100 {
		t.Errorf("machine = %+v", m)
	}
	top, ok := s.Top(ctx, pid, 5)
	if !ok || len(top.ByCPU) == 0 || top.ByCPU[0].Args == "" {
		t.Errorf("top = %+v", top)
	}
}
