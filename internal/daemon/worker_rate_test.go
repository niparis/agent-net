package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The 5th finished worker of a caller gets a one-line rating ask in its end
// notice. The first three of a little-rated model ask too; the 4th does not.
func TestWorkerRateAskEveryFifth(t *testing.T) {
	t.Parallel()
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
		got := strings.Contains(last.Body, "with agent_worker_rate: 1 to 5")
		if got != ask {
			t.Fatalf("worker %d (%s): rating ask = %v, want %v:\n%s", i+1, res.SessionID, got, ask, last.Body)
		}
		if ask && !strings.Contains(last.Body, res.SessionID) {
			t.Errorf("worker %d: the ask does not name its session:\n%s", i+1, last.Body)
		}
		eventually(t, "the worker ends", func() bool { return w.d.workerCount() == 0 })
	}
}

// The owner at the keyboard can report a defect found later from the TUI's
// owner path. It is one operator call and reaches the gateway as the owner
// session, not as a score.
func TestWorkerDefectOwnerPath(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	owner := w.ownerID()
	node := w.d.NodeID()
	wid, err := g.gw.Store().UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "w",
		Model: "fake/m1", Worker: &protocol.WorkerInfo{ID: "w_1", Owner: owner, DifficultyAsked: "hard", DifficultyUsed: "hard"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: wid, Harness: "opencode",
		Model: "fake/m1", Outcome: protocol.OutcomeComplete, EndedAt: time.Now().Unix(), Caller: owner, Difficulty: "hard"}); err != nil {
		t.Fatal(err)
	}
	raw, err := w.cli.call(protocol.OpWorkerDefect, protocol.DefectRequest{Worker: wid, Severity: 3, Note: "the old path stayed in the merge"})
	if err != nil {
		t.Fatal(err)
	}
	var out protocol.RateResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Accepted || out.Kind != protocol.RateKindDefectLater || out.Worker != wid || out.Severity != 3 {
		t.Fatalf("owner defect: %+v", out)
	}
	defects, err := g.gw.Store().DefectCountsBySession(0)
	if err != nil {
		t.Fatal(err)
	}
	if defects[wid] != 1 {
		t.Fatalf("defects: %v", defects)
	}
	rework, err := g.gw.Store().DefectReworkBySession(0)
	if err != nil {
		t.Fatal(err)
	}
	if rework[wid] != 3 {
		t.Fatalf("defect severity points: %v", rework)
	}
	// The agent tool is still closed to the command line: the owner has its
	// own call, so a score cannot arrive this way.
	if _, err := w.cli.call("agent_rate", protocol.RateRequest{Worker: wid, Score: 4}); err == nil {
		t.Fatal("cli agent_rate was accepted")
	}
}

// A rating comes from an agent session, never the command line.
func TestWorkerRateIsForAgentSessions(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	if _, err := w.cli.call("agent_rate", protocol.RateRequest{Worker: "s_w", Score: 4}); err == nil ||
		!strings.Contains(err.Error(), "not available to cli clients") {
		t.Fatalf("cli agent_rate: %v", err)
	}
}
