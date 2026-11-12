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
	s := usage.NewWith(ownerFixture(time.Now()), time.Hour, 5*time.Minute, nil)
	d := &Daemon{cfg: Config{UsageSampler: s, claudeRosterPath: os.DevNull}, sessions: map[string]*localSession{}, workers: map[string]*worker{}}
	d.workers["w_test"] = &worker{id: "w_test", pid: 100}
	d.sessions["s_test"] = &localSession{sessionID: "s_test", info: protocol.SessionInfo{PID: 200}}
	s.Sample(context.Background())
	s.Sample(context.Background())
	rows, err := d.machineProcesses(context.Background(), "rss", func(pid int) (procView, error) { return procView{PID: pid, UID: os.Getuid(), EUID: os.Getuid()}, nil })
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
	if out.Processes.Items[0].PID != os.Getpid() || out.Processes.Items[0].Refusal == "" || out.Processes.Items[0].OwnerKind != protocol.ProcOwnerDaemon {
		t.Fatalf("missing daemon protection: %+v", out.Processes)
	}
}
