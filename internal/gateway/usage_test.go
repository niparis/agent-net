package gateway

import (
	"math"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSessionCountsSplitOnlineRecentAndOld(t *testing.T) {
	s, clk := newTestStore(t)
	n, _, err := s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, name := range []string{"old", "recent", "live", "live2"} {
		id, err := s.UpsertSession(n, protocol.SessionInfo{Harness: "claude", NativeID: name, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	// "old" went offline 3 days ago, "recent" 2 hours ago.
	clk.t = clk.t.Add(-72 * time.Hour)
	if err := s.SetSessionOffline(n, ids["old"]); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(70 * time.Hour)
	if err := s.SetSessionOffline(n, ids["recent"]); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(2 * time.Hour)
	c, err := s.SessionCounts(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if c.Online != 2 || c.Offline24h != 1 || c.OfflineTotal != 2 {
		t.Errorf("counts = %+v", c)
	}
}

func TestSessionCountsOfAnEmptyGateway(t *testing.T) {
	s, _ := newTestStore(t)
	c, err := s.SessionCounts(24 * time.Hour)
	if err != nil || c != (protocol.SessionCounts{}) {
		t.Errorf("counts = %+v, %v", c, err)
	}
}

func TestCleanUsageBoundsWhatANodeSends(t *testing.T) {
	rep := protocol.UsageReport{Machine: protocol.MachineUsage{CPU: 250, CPUAvg: -3, Load: [3]float64{math.NaN(), 1, -1}}}
	for i := 0; i < maxUsageTrees+10; i++ {
		rep.Trees = append(rep.Trees, protocol.TreeUsage{PID: i, CPU: -5, Harness: "a-very-long-harness-name-that-goes-on", Sessions: make([]string, maxUsageSessions+5)})
	}
	got := cleanUsage(rep)
	if got.Machine.CPU != 100 || got.Machine.CPUAvg != 0 || got.Machine.Load[0] != 0 || got.Machine.Load[2] != 0 {
		t.Errorf("machine = %+v", got.Machine)
	}
	if len(got.Trees) != maxUsageTrees || len(got.Trees[0].Sessions) != maxUsageSessions || got.Trees[0].CPU != 0 || len(got.Trees[0].Harness) > 20 {
		t.Errorf("trees were not bounded: %d, %d sessions", len(got.Trees), len(got.Trees[0].Sessions))
	}
	if empty := cleanUsage(protocol.UsageReport{}); empty.Trees == nil {
		t.Error("trees should be an empty list, not null")
	}
}
