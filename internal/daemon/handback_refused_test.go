package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The text of the one message a worker gets about a refused hand-back.
const refusalToWorker = "agent-net could not take your hand-back"

// agyRefusalTurns counts the turns, over every agy process of the test, that
// carried the refusal message.
func agyRefusalTurns(dir string) int {
	n := 0
	for _, r := range agyRecords(dir) {
		for _, turn := range r.Turns {
			if strings.Contains(turn, refusalToWorker) {
				n++
			}
		}
	}
	return n
}

// refuseAgyHandbackAtRestart starts an agy worker that hands back with a file,
// then restarts the daemon after the tool call was acknowledged and before the
// turn's result was read. The file is gone when the new daemon prepares it,
// which the worker can correct. nextTurn holds the FAKE markers of the
// worker's answer to the refusal message.
func refuseAgyHandbackAtRestart(t *testing.T, w *workerNode, dir, nextTurn string) protocol.WorkerStartResult {
	t.Helper()
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low",
		Task: "FAKE:handback FAKE:filehandback FAKE:result-gate FAKE:exit", Isolation: protocol.IsolationWorktree})
	wk := w.d.workerBySession(res.SessionID)
	eventually(t, "Antigravity acknowledged handback", func() bool {
		wk.agy.mu.Lock()
		defer wk.agy.mu.Unlock()
		return wk.agy.handback != nil
	})
	pid := agyRecords(dir)[0].PID
	w.node.restartWith(func() {
		if err := os.Remove(filepath.Join(res.Worktree, "agy-result.txt")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "next-turn"), []byte(nextTurn), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "result-release"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		// The refusal message must start a new process, not reach this one
		// after its last read.
		eventually(t, "Antigravity result written during downtime", func() bool { return !alive(pid) })
	}, true)
	w.cli = cliClient(t, w.node)
	return res
}

// A cause the worker can correct goes back to the worker once, in its own
// session, and its next hand-back reaches the caller as usual.
func TestAgyRefusedHandbackGoesBackToTheWorker(t *testing.T) {
	t.Parallel()
	set := shortSet
	set.Idle, set.IdleNotice = time.Hour, time.Hour
	w, dir, _ := startAgyNode(t, set)
	res := refuseAgyHandbackAtRestart(t, w, dir, "FAKE:handback")
	n := w.waitNotice(t, 1)[0]
	t.Logf("notice frame:\n%s", n.Body)
	if n.From != res.SessionID || !strings.Contains(n.Body, "status: done") || !strings.Contains(n.Body, "finished agy task") || len(n.Files) != 0 {
		t.Fatalf("the second hand-back did not reach the caller: %+v", n)
	}
	if got := agyRefusalTurns(dir); got != 1 {
		t.Fatalf("the worker got %d refusal messages, want 1", got)
	}
	for _, r := range agyRecords(dir) {
		for _, turn := range r.Turns {
			if strings.Contains(turn, refusalToWorker) {
				t.Logf("worker turn frame:\n%s", turn)
				if !strings.Contains(turn, "agy-result.txt") {
					t.Errorf("the refusal message does not name the missing file:\n%s", turn)
				}
			}
		}
	}
	eventually(t, "agy ends after its second hand-back", func() bool { return w.d.workerCount() == 0 })
	if got := len(w.notices(t)); got != 1 {
		t.Fatalf("the caller got %d notices, want only the hand-back", got)
	}
}

// A cause the worker cannot correct goes to the caller with the worker's
// report, and the worker can still take a message and hand back again.
func TestAgyRefusedHandbackTellsTheCaller(t *testing.T) {
	t.Parallel()
	var refused atomic.Bool
	set := shortSet
	set.Idle, set.IdleNotice = time.Hour, time.Hour
	w, dir, _ := startAgyNode(t, set, func(c *Config) {
		c.testGatewayCall = func(_ context.Context, typ string, p any) (json.RawMessage, bool, error) {
			m, ok := p.(protocol.MsgSend)
			if typ == "msg.send" && ok && m.WorkerNotice && strings.Contains(m.Body, "\nstatus: done\n") && refused.CompareAndSwap(false, true) {
				return nil, true, RemoteError("the test gateway refused this notice")
			}
			return nil, false, nil
		}
	})
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:handback", Isolation: protocol.IsolationWorktree})
	n := w.waitNotice(t, 1)[0]
	t.Logf("notice frame:\n%s", n.Body)
	for _, want := range []string{"status: hand-back refused", "the test gateway refused this notice", "finished agy task"} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("the caller's notice lacks %q:\n%s", want, n.Body)
		}
	}
	if got := agyRefusalTurns(dir); got != 0 {
		t.Fatalf("the worker got %d refusal messages for a cause it cannot correct", got)
	}
	// The refused hand-back ended the turn: a follow-up is not held behind it.
	sendAgy(t, w, res.SessionID, "FAKE:handback again")
	again := w.waitNotice(t, 2)[1]
	if !strings.Contains(again.Body, "status: done") {
		t.Fatalf("the follow-up hand-back: %+v", again)
	}
}

// The worker had its one message, the daemon restarted, and the worker ended
// its turn without a new hand-back: the caller gets the kept report and the
// reason, and the worker gets no second message.
func TestAgyRefusedHandbackReachesTheCallerAfterARestart(t *testing.T) {
	t.Parallel()
	set := shortSet
	set.Idle, set.IdleNotice = time.Hour, time.Hour
	w, dir, _ := startAgyNode(t, set)
	res := refuseAgyHandbackAtRestart(t, w, dir, "FAKE:gate")
	eventually(t, "the worker got the refusal message", func() bool { return agyRefusalTurns(dir) == 1 })
	w.node.restart()
	w.cli = cliClient(t, w.node)
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	n := w.waitNotice(t, 1)[0]
	t.Logf("notice frame:\n%s", n.Body)
	if n.From != res.SessionID {
		t.Fatalf("notice from %s, want the worker %s", n.From, res.SessionID)
	}
	for _, want := range []string{"status: hand-back refused", "agy-result.txt", "finished agy task"} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("the caller's notice lacks %q:\n%s", want, n.Body)
		}
	}
	if got := agyRefusalTurns(dir); got != 1 {
		t.Fatalf("the worker got %d refusal messages, want 1", got)
	}
}
