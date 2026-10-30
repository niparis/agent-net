package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/usage"
)

// A project on two machines goes to the one with more room, and the result
// says why. The machines tell the gateway their room with their usage report.
func TestStartPicksTheMachineWithMoreRoom(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	node := func(l MachineLoad) *workerNode {
		fl := &fakeLoad{l: l}
		fr := &procReader{}
		fr.tick(0)
		sampler := usage.NewWith(fr, time.Hour, 5*time.Minute, nil)
		return startWorkerNode(t, g, queueSet, func(c *Config) {
			c.UsageSampler, c.LoadProbe, c.UsageReportInterval = sampler, fl.get, 50*time.Millisecond
		})
	}
	busy := roomyLoad()
	busy.CPU5 = 70 // under the target, but little room
	a := node(busy)
	b := node(roomyLoad())
	repoA, repoB := newRepo(t, "shop"), newRepo(t, "shop")
	a.register(t, repoA)
	b.register(t, repoB)
	// Wait until the gateway knows both machines' room.
	eventually(t, "both machines reported their room", func() bool {
		var u protocol.UsageResult
		json.Unmarshal(a.cli.mustCall("usage", nil), &u)
		n := 0
		if u.Gateway != nil {
			for _, x := range u.Gateway.Nodes {
				if x.Usage.Workers != nil && x.Usage.Workers.Headroom > 0 {
					n++
				}
			}
		}
		return n == 2
	})
	res := a.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	if res.NodeID != b.d.NodeID() || !strings.Contains(res.MachineNote, "picked "+b.cfg.Hostname) || !strings.Contains(res.MachineNote, a.cfg.Hostname) {
		t.Errorf("result: %+v", res)
	}
}
