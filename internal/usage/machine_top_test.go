package usage

import (
	"context"
	"strings"
	"testing"
	"time"
)

type elapsedReader struct {
	*fakeReader
	asked []int
}

func (e *elapsedReader) Elapsed(ctx context.Context, pids []int) map[int]time.Duration {
	e.asked = append(e.asked, pids...)
	out := map[int]time.Duration{}
	for _, p := range pids {
		out[p] = time.Duration(p) * time.Second
	}
	return out
}

func TestTopProcsWholeMachineWithLineage(t *testing.T) {
	long := "node " + strings.Repeat("x", 300)
	fr := &fakeReader{snaps: []Snapshot{
		snapAt(0, 100, 400, [4]float64{10, 1, 1, 5}, [4]uint64{100, 50, 25, 999}),
		snapAt(5, 250, 800, [4]float64{12, 2, 1.5, 9}, [4]uint64{100, 60, 40, 999}),
	}, args: map[int]string{101: long, 200: "other --token sk-abcdefghijklmnopqrstuvwxyz0123"}}
	er := &elapsedReader{fakeReader: fr}
	s := NewWith(er, 5*time.Second, 5*time.Minute, nil)
	ctx := context.Background()
	if _, _, ok := s.TopProcs(ctx, 3); ok {
		t.Fatal("top before any sample")
	}
	s.Sample(ctx)
	if _, _, ok := s.TopProcs(ctx, 3); ok {
		t.Fatal("top after one sample: there is no interval yet")
	}
	s.Sample(ctx)
	rows, at, ok := s.TopProcs(ctx, 3)
	if !ok || !at.Equal(t0.Add(5*time.Second)) || len(rows) != 3 {
		t.Fatalf("rows = %+v at=%v ok=%v", rows, at, ok)
	}
	// Over 5 s: pid 200 used 4 s (80%), 100 used 2 s (40%), 101 used 1 s (20%).
	if rows[0].PID != 200 || rows[1].PID != 100 || rows[2].PID != 101 {
		t.Fatalf("order = %d %d %d", rows[0].PID, rows[1].PID, rows[2].PID)
	}
	near(t, "cpu 200", rows[0].CPU, 80, 1e-6)
	if rows[0].PPID != 1 || len(rows[0].Lineage) != 1 || rows[0].Lineage[0] != 1 {
		t.Errorf("200 lineage = %v ppid %d", rows[0].Lineage, rows[0].PPID)
	}
	if got := rows[2].Lineage; len(got) != 2 || got[0] != 100 || got[1] != 1 {
		t.Errorf("101 lineage = %v", got)
	}
	if n := len([]rune(rows[2].Args)); n != ArgsLenTop {
		t.Errorf("args length = %d, want %d", n, ArgsLenTop)
	}
	if strings.Contains(rows[0].Args, "abcdefghij") {
		t.Errorf("secret not masked: %q", rows[0].Args)
	}
	// The reader gave no run time, so it was asked for exactly the listed pids.
	if len(er.asked) != 3 || rows[0].Elapsed != 200*time.Second {
		t.Errorf("asked = %v, elapsed = %v", er.asked, rows[0].Elapsed)
	}
}

func TestSnapshotAgeAndSplit(t *testing.T) {
	a := snapAt(0, 100, 400, [4]float64{}, [4]uint64{})
	b := snapAt(5, 250, 800, [4]float64{}, [4]uint64{})
	a.M.HaveParts, b.M.HaveParts = true, true
	a.M.Parts = CPUParts{User: 100, Sys: 50, Idle: 250}
	b.M.Parts = CPUParts{User: 200, Sys: 100, Idle: 500, IOWait: 50, Steal: 50}
	b.M.SwapUsed, b.M.SwapTotal = 1<<20, 4<<20
	s, _ := newFake(a, b)
	if _, ok := s.SnapshotAge(); ok {
		t.Fatal("age before any sample")
	}
	ctx := context.Background()
	s.Sample(ctx)
	if m, _ := s.Machine(); m.HaveSplit {
		t.Error("split after one sample")
	}
	s.Sample(ctx)
	m, _ := s.Machine()
	if !m.HaveSplit {
		t.Fatal("no split")
	}
	// Deltas: user 100, sys 50, idle 250, iowait 50, steal 50 = 500.
	near(t, "user", m.Split.User, 20, 1e-6)
	near(t, "sys", m.Split.Sys, 10, 1e-6)
	near(t, "idle", m.Split.Idle, 50, 1e-6)
	near(t, "iowait", m.Split.IOWait, 10, 1e-6)
	near(t, "steal", m.Split.Steal, 10, 1e-6)
	if m.SwapUsed != 1<<20 || m.SwapTotal != 4<<20 {
		t.Errorf("swap = %d / %d", m.SwapUsed, m.SwapTotal)
	}
	if age, ok := s.SnapshotAge(); !ok || age <= 0 {
		t.Errorf("age = %v ok=%v", age, ok)
	}
}

func TestProcFSBootTimeSwapAndSplit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root+"/stat", "cpu  100 10 50 700 100 5 5 30 0 0\ncpu0 100 10 50 700 100 5 5 30 0 0\nbtime 1000\n")
	writeFile(t, root+"/meminfo", "MemTotal: 8000000 kB\nMemAvailable: 2000000 kB\nSwapTotal: 4000 kB\nSwapFree: 1000 kB\n")
	// started 500 ticks (5 s) after boot
	writeFile(t, root+"/10/stat", "10 (a) S 1 10 10 0 -1 0 0 0 0 0 100 0 0 0 20 0 1 0 500 0 10 0\n")
	p := newProcFS(root)
	p.pageSize = 4096
	snap, err := p.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !snap.M.HaveParts || snap.M.Parts.User != 100 || snap.M.Parts.Nice != 10 || snap.M.Parts.Sys != 60 || snap.M.Parts.Steal != 30 {
		t.Errorf("parts = %+v", snap.M.Parts)
	}
	if snap.M.SwapTotal != 4000*1024 || snap.M.SwapUsed != 3000*1024 {
		t.Errorf("swap = %d / %d", snap.M.SwapUsed, snap.M.SwapTotal)
	}
	want := snap.At.Sub(time.Unix(1005, 0))
	if got := snap.Procs[10].Elapsed; got < want-time.Second || got > want+time.Second {
		t.Errorf("elapsed = %v, want about %v", got, want)
	}
}

func TestParseSwapUsageAndElapsed(t *testing.T) {
	used, total := parseSwapUsage("total = 2048.00M  used = 1024.50M  free = 1023.50M  (encrypted)\n")
	if total != 2048<<20 || used != uint64(1024.5*float64(1<<20)) {
		t.Errorf("used %d total %d", used, total)
	}
	if u, tt := parseSwapUsage("garbage"); u != 0 || tt != 0 {
		t.Errorf("junk = %d %d", u, tt)
	}
	r := &psReader{run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("  20 1-02:03:04\n  21 05:06\nbad\n"), nil
	}}
	e := r.Elapsed(context.Background(), []int{20, 21})
	if e[20] != 26*time.Hour+3*time.Minute+4*time.Second || e[21] != 5*time.Minute+6*time.Second || len(e) != 2 {
		t.Errorf("elapsed = %v", e)
	}
}
