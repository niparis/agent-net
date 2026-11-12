package daemon

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/usage"
)

// scriptReader is a fake process table: it returns its snapshots in turn and
// repeats the last one.
type scriptReader struct {
	mu    sync.Mutex
	snaps []usage.Snapshot
	args  map[int]string
	reads int
}

func (r *scriptReader) Read(ctx context.Context) (usage.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.snaps[min(r.reads, len(r.snaps)-1)]
	r.reads++
	return s, nil
}

func (r *scriptReader) Args(ctx context.Context, pids []int) map[int]string {
	out := map[int]string{}
	for _, p := range pids {
		if a, ok := r.args[p]; ok {
			out[p] = a
		}
	}
	return out
}

func (r *scriptReader) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads
}

// ownerFixture builds two snapshots 5 s apart, ending at end. The busy
// processes sit in the trees of a worker, a session, the daemon, a hosted MCP
// server, a gateway, and nobody.
func ownerFixture(end time.Time) *scriptReader {
	self := os.Getpid()
	mk := func(secs float64) usage.Snapshot {
		p := func(pid, ppid int, cpu float64, comm string) usage.Proc {
			return usage.Proc{PID: pid, PPID: ppid, Start: uint64(pid), CPU: cpu * secs, RSS: uint64(pid) << 20, Comm: comm, Elapsed: time.Duration(pid) * time.Minute}
		}
		at := end
		if secs == 1 {
			at = end.Add(-5 * time.Second)
		}
		return usage.Snapshot{At: at,
			Procs: map[int]usage.Proc{
				1:        p(1, 0, 0, "init"),
				self:     p(self, 1, 0.1, "agent-net"),
				self + 1: p(self+1, self, 0.2, "git"), // a child of the daemon
				100:      p(100, self, 0.5, "claude"), // the worker (a child of the daemon too)
				101:      p(101, 100, 3.0, "go"),      // busiest: inside the worker
				200:      p(200, 1, 0.3, "claude"),    // a session
				201:      p(201, 200, 1.0, "node"),
				300:      p(300, self, 0.1, "npx"), // hosted MCP server group leader
				301:      p(301, 300, 0.7, "node"),
				400:      p(400, 1, 0.9, "agent-net"), // the gateway
				500:      p(500, 1, 2.0, "stress"),    // nobody's
			},
			M: usage.Machine{Cores: 12, CPUBusy: 1000 * secs, CPUTotal: 1200 * secs, MemUsed: 6 << 30, MemTotal: 16 << 30,
				Load: [3]float64{20.7, 15.25, 9.5}, SwapUsed: 1 << 30, SwapTotal: 2 << 30, HaveParts: true,
				Parts: usage.CPUParts{User: 600 * secs, Nice: 10 * secs, Sys: 300 * secs, Idle: 200 * secs, IOWait: 50 * secs, Steal: 40 * secs}},
		}
	}
	return &scriptReader{snaps: []usage.Snapshot{mk(1), mk(6)}, args: map[int]string{
		101: "go test ./... " + strings.Repeat("-run=Slow ", 40),
		500: "stress --cpu 4 --token sk-abcdefghijklmnopqrstuvwxyz0123",
		400: "/usr/local/bin/agent-net gateway --listen :9",
	}}
}

func TestMachineUsageNowTopProcessesWithOwners(t *testing.T) {
	self := os.Getpid()
	r := ownerFixture(time.Now())
	s := usage.NewWith(r, time.Hour, 5*time.Minute, nil)
	d := &Daemon{cfg: Config{UsageSampler: s}, sessions: map[string]*localSession{}, workers: map[string]*worker{}}
	d.sessions["s_alice"] = &localSession{sessionID: "s_alice", info: protocol.SessionInfo{Harness: "claude", Name: "alice", PID: 200}}
	d.workers["w_fix"] = &worker{id: "w_fix", pid: 100, title: "Fix the thing", project: "agent-net"}
	d.mcp = &mcpHost{runners: map[string]*mcpRunner{"files": {spec: protocol.MCPServer{Name: "files"}, pgid: 300}}}
	ctx := context.Background()
	s.Sample(ctx)
	s.Sample(ctx)

	got := d.machineUsageNow(ctx)
	if got.Status != protocol.MachineUsageOK || got.Cores != 12 || got.AgeSeconds > 2 {
		t.Fatalf("block = %+v", got)
	}
	if len(got.Load) != 3 || got.Load[0] != 20.7 || got.Load[1] != 15.25 || got.Load[2] != 9.5 {
		t.Errorf("load = %v", got.Load)
	}
	if got.CPUPct < 83 || got.CPUPct > 84 { // 5000 of 6000
		t.Errorf("cpu = %v", got.CPUPct)
	}
	// Deltas: user 600, nice 10, sys 300, idle 200, iowait 50, steal 40 = 1200.
	if sp := got.Split; sp == nil || sp.User != 50 || sp.Sys != 25 || sp.Nice != 0.8 || sp.Idle != 16.7 || sp.IOWait != 4.2 || sp.Steal != 3.3 {
		t.Errorf("split = %+v", got.Split)
	}
	if got.MemUsed != 6<<30 || got.MemTotal != 16<<30 || got.SwapUsed != 1<<30 || got.SwapTotal != 2<<30 {
		t.Errorf("memory = %+v", got)
	}
	if len(got.Top) != 10 {
		t.Fatalf("top = %d rows, want 10", len(got.Top))
	}
	want := []struct {
		pid        int
		kind, name string
		id         string
	}{
		{101, protocol.ProcOwnerWorker, "Fix the thing", "w_fix"},
		{500, protocol.ProcOwnerOther, "other", ""},
		{201, protocol.ProcOwnerSession, "alice", "s_alice"},
		{400, protocol.ProcOwnerGateway, "agent-net gateway", ""},
		{301, protocol.ProcOwnerMCP, "mcp server files", ""},
		{100, protocol.ProcOwnerWorker, "Fix the thing", "w_fix"},
		{200, protocol.ProcOwnerSession, "alice", "s_alice"},
		{self + 1, protocol.ProcOwnerDaemon, "agent-net daemon", ""},
		{300, protocol.ProcOwnerMCP, "mcp server files", ""},
		{self, protocol.ProcOwnerDaemon, "agent-net daemon", ""},
	}
	for i, w := range want {
		row := got.Top[i]
		if row.PID != w.pid || row.OwnerKind != w.kind || row.Owner != w.name || row.OwnerID != w.id {
			t.Errorf("row %d = %+v, want pid %d owner %s/%s/%s", i, row, w.pid, w.kind, w.name, w.id)
		}
	}
	if b, err := json.MarshalIndent(got, "", " "); err == nil {
		t.Logf("sample usage block:\n%s", b)
	}
	first := got.Top[0]
	if first.CPU != 300 || first.PPID != 100 || first.RSS != 101<<20 || first.ElapsedSeconds != 101*60 {
		t.Errorf("first = %+v", first)
	}
	if n := len([]rune(first.Cmd)); n != 120 || !strings.HasPrefix(first.Cmd, "go test ./...") {
		t.Errorf("cmd length %d: %q", n, first.Cmd)
	}
	if strings.Contains(got.Top[1].Cmd, "abcdefghij") {
		t.Errorf("secret in cmd: %q", got.Top[1].Cmd)
	}
	if got.Top[2].Cmd != "[node]" {
		t.Errorf("a process with no command line is shown as %q", got.Top[2].Cmd)
	}
}

func TestMachineUsageNowReusesFreshReadingAndRefreshesStale(t *testing.T) {
	ctx := context.Background()
	fresh := ownerFixture(time.Now())
	s := usage.NewWith(fresh, time.Hour, 5*time.Minute, nil)
	d := &Daemon{cfg: Config{UsageSampler: s}}
	s.Sample(ctx)
	s.Sample(ctx)
	d.machineUsageNow(ctx)
	d.machineUsageNow(ctx)
	if n := fresh.count(); n != 2 {
		t.Errorf("a reading under 10 s old was not reused: %d reads", n)
	}

	stale := ownerFixture(time.Now().Add(-time.Hour))
	s2 := usage.NewWith(stale, time.Hour, 5*time.Minute, nil)
	d2 := &Daemon{cfg: Config{UsageSampler: s2}}
	s2.Sample(ctx)
	got := d2.machineUsageNow(ctx)
	if n := stale.count(); n != 2 {
		t.Errorf("a stale reading was not refreshed once: %d reads", n)
	}
	if got.Status != protocol.MachineUsageOK || len(got.Top) == 0 {
		t.Errorf("block = %+v", got)
	}
}

func TestMachineUsageNowWithoutSampler(t *testing.T) {
	got := (&Daemon{}).machineUsageNow(context.Background())
	if got.Status != protocol.MachineUsageUnavailable || !strings.Contains(got.Note, "does not measure") {
		t.Errorf("block = %+v", got)
	}
}

// The whole path: the gateway asks the machine's daemon and the answer reaches
// agent_machine_get. A machine that is gone gives an offline note instead.
func TestMachineGetUsageThroughGateway(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{detect: fakeInventory}, withSampler(os.Getpid()))
	ctx := context.Background()
	var out protocol.MachineGetResult
	eventually(t, "usage block", func() bool {
		var err error
		out, err = g.gw.MachineGet(ctx, protocol.MachineGetRequest{Machine: n.d.NodeID()})
		return err == nil && out.Usage != nil && out.Usage.Status == protocol.MachineUsageOK && len(out.Usage.Top) > 0
	})
	u := out.Usage
	if u.Cores != 4 || len(u.Load) != 3 || u.Load[0] != 0.5 {
		t.Errorf("usage = %+v", u)
	}
	kinds := map[int]string{}
	for _, r := range u.Top {
		kinds[r.PID] = r.OwnerKind
	}
	self := os.Getpid()
	if kinds[self+1000001] != protocol.ProcOwnerOther || kinds[self+1000000] != protocol.ProcOwnerDaemon || kinds[self] != protocol.ProcOwnerDaemon {
		t.Errorf("owners = %v", kinds)
	}
	cat, err := g.gw.MachineGet(ctx, protocol.MachineGetRequest{Machine: n.d.NodeID(), Include: []string{"models"}})
	if err != nil || cat.Usage != nil {
		t.Errorf("a category call must not carry the usage block: err=%v usage=%+v", err, cat.Usage)
	}
	if _, err := os.Stat("fake-codex.json"); !os.IsNotExist(err) {
		t.Fatalf("test left fake-codex.json in package folder: %v", err)
	}
}
