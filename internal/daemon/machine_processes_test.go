package daemon

import (
	"context"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/usage"
	"os"
	"testing"
	"time"
)

func TestMachineProcessesRSS(t *testing.T) {
	now := time.Now()
	fixture := ownerFixture(now)
	s := usage.NewWith(fixture, time.Hour, 5*time.Minute, nil)
	d := &Daemon{cfg: Config{UsageSampler: s, claudeRosterPath: os.DevNull}, sessions: map[string]*localSession{}, workers: map[string]*worker{}}
	d.workers["w_test"] = &worker{id: "w_test", pid: 100}
	d.sessions["s_test"] = &localSession{sessionID: "s_test", info: protocol.SessionInfo{PID: 200}}
	s.Sample(context.Background())
	s.Sample(context.Background())
	rows, _, err := d.machineProcesses(context.Background(), "rss", fixtureProcessTable(now, fixture))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for i, r := range rows {
		if i > 0 && rows[i-1].RSS < r.RSS {
			t.Fatal("not sorted by rss")
		}
		found[r.OwnerKind] = true
		if r.PID == 100 && (r.OwnerID != "w_test" || r.Refusal == "") {
			t.Fatalf("worker protection missing: %+v", r)
		}
	}
	for _, kind := range []string{protocol.ProcOwnerWorker, protocol.ProcOwnerSession, protocol.ProcOwnerOther} {
		if !found[kind] {
			t.Fatalf("missing %s", kind)
		}
	}
}

func TestMachineProcessesThroughGateway(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{detect: fakeInventory}, func(c *Config) { withSampler(os.Getpid())(c); c.claudeRosterPath = os.DevNull })
	ctx := context.Background()
	eventually(t, "process read", func() bool {
		out, err := g.gw.MachineGet(ctx, protocol.MachineGetRequest{Machine: n.d.NodeID(), Include: []string{"processes"}, Sort: "rss", Limit: 1})
		return err == nil && out.Processes != nil && len(out.Processes.Items) == 1
	})
	out, err := g.gw.MachineGet(ctx, protocol.MachineGetRequest{Machine: n.d.NodeID(), Include: []string{"processes"}, Sort: "rss", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if r := out.Processes.Roster; r == nil || r.State != protocol.ClaudeRosterMissing {
		t.Errorf("the process page must carry the roster status: %+v", r)
	}
	if out.Processes.Items[0].PID != os.Getpid() || out.Processes.Items[0].Refusal == "" || out.Processes.Items[0].OwnerKind != protocol.ProcOwnerDaemon {
		t.Fatalf("missing daemon protection: %+v", out.Processes)
	}
}

func TestMachineProcessesReadsTableAndRosterOnce(t *testing.T) {
	now := time.Now()
	fixture := ownerFixture(now)
	s := usage.NewWith(fixture, time.Hour, 5*time.Minute, nil)
	ctx := context.Background()
	s.Sample(ctx)
	s.Sample(ctx)
	tableReads, rosterReads := 0, 0
	d := &Daemon{cfg: Config{
		UsageSampler:     s,
		claudeRosterPath: writeClaudeRoster(t, 200, now.Add(-time.Hour), map[string]any{"live": map[string]any{"pid": 101, "replPid": 102}}),
		readClaudeRosterFile: func(path string) ([]byte, error) {
			rosterReads++
			return os.ReadFile(path)
		},
	}, sessions: map[string]*localSession{}, workers: map[string]*worker{}}
	table := func() (map[int]procView, error) {
		tableReads++
		return fixtureProcessTable(now, fixture)()
	}
	rows, _, err := d.machineProcesses(ctx, "rss", table)
	if err != nil {
		t.Fatal(err)
	}
	if want := len(fixture.snaps[len(fixture.snaps)-1].Procs); len(rows) != want {
		t.Fatalf("got %d rows, want %d", len(rows), want)
	}
	if tableReads != 1 || rosterReads != 1 {
		t.Fatalf("process table read %d times and roster %d times for %d processes; want 1 and 1", tableReads, rosterReads, len(rows))
	}
}

// fixtureProcessTable is the process table of a fixture: each of its processes
// belongs to the test user and started two hours ago.
func fixtureProcessTable(now time.Time, fixture *scriptReader) func() (map[int]procView, error) {
	return func() (map[int]procView, error) {
		out := map[int]procView{}
		for pid := range fixture.snaps[len(fixture.snaps)-1].Procs {
			out[pid] = procView{PID: pid, UID: os.Getuid(), EUID: os.Getuid(), Started: now.Add(-2 * time.Hour)}
		}
		return out, nil
	}
}
