package daemon

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A start asked for by a session on machine A and queued on machine B passes
// the gate's recheck when it leaves B's queue and starts. The recheck comes
// from B, where the owner session does not live (decision 0162).
func TestQueuedCrossMachineStartPassesRecheck(t *testing.T) {
	setupShadowJudge(t, shadowReply, false)
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	starter := startWorkerNode(t, g, queueSet, withDifficulty(withLoad(fl)))
	starter.register(t, newRepo(t, "shop"))
	setJudgeScores(t, starter, map[string]float64{"fake/m1": 12})
	ownerNode := startWorkerNode(t, g, shortSet, withDifficulty(nil))
	pi, _ := helloPi(t, ownerNode.node, "caller", "/work/caller", true)

	queued := startFrom(t, pi, protocol.WorkerStartRequest{Model: "auto", Project: "shop", Harness: "opencode",
		Difficulty: "very_complex", Touches: []string{"db"}, Task: "queued across machines"})
	if queued.Status != "queued" || queued.NodeID != starter.cfg.NodeID {
		t.Fatalf("the start did not wait in the other machine's queue: %+v", queued)
	}

	fl.set(roomyLoad())
	var notice protocol.Message
	deadline := time.Now().Add(scaled(30 * time.Second))
	for notice.ID == "" {
		select {
		case m := <-pi.Pushes:
			if strings.Contains(m.Body, "status: started") {
				notice = m
			}
		default:
		}
		if lines := starter.logs.lines("worker.not_started"); len(lines) > 0 {
			t.Fatalf("the queued start was refused: %s", lines[len(lines)-1])
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the queued cross-machine start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(notice.Body, "on "+starter.d.hostName()) {
		t.Fatalf("the start notice does not name the queueing machine:\n%s", notice.Body)
	}
	session := regexp.MustCompile(`worker: (s_[a-z0-9]+) `).FindStringSubmatch(notice.Body)
	if session == nil {
		t.Fatalf("no worker session in the start notice:\n%s", notice.Body)
	}
	if starter.d.workerBySession(session[1]) == nil {
		t.Fatalf("worker %s did not start on the queueing machine", session[1])
	}
	if ownerNode.d.workerBySession(session[1]) != nil {
		t.Fatalf("worker %s started on the owner's machine", session[1])
	}
}
