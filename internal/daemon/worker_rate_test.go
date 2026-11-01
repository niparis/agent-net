package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The 5th finished worker of a caller gets a one-line rating ask in its end
// notice. The first three of a little-rated model ask too; the 4th does not.
func TestWorkerRateAskEveryFifth(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.StartGap = -time.Second // five starts in a row: the gap is not under test
	w := startWorkerNode(t, g, set, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)

	want := []bool{true, true, true, false, true}
	for i, ask := range want {
		res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
			Task: "FAKE:handback do the task"})
		notices := w.waitNotice(t, i+1)
		last := notices[len(notices)-1]
		got := strings.Contains(last.Body, "with agent_rate: 1 to 5")
		if got != ask {
			t.Fatalf("worker %d (%s): rating ask = %v, want %v:\n%s", i+1, res.SessionID, got, ask, last.Body)
		}
		if ask && !strings.Contains(last.Body, res.SessionID) {
			t.Errorf("worker %d: the ask does not name its session:\n%s", i+1, last.Body)
		}
		eventually(t, "the worker ends", func() bool { return w.d.workerCount() == 0 })
	}
}

// A rating comes from an agent session, never the command line.
func TestWorkerRateIsForAgentSessions(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	if _, err := w.cli.call("agent_rate", protocol.RateRequest{Worker: "s_w", Score: 4}); err == nil ||
		!strings.Contains(err.Error(), "not available to cli clients") {
		t.Fatalf("cli agent_rate: %v", err)
	}
}
