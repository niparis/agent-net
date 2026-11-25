package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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

// stopAtStep stops the first daemon that reaches step of a refused hand-back
// as if its process ended there: the goroutine exits before the next step.
// reached is closed then. A daemon started later passes the step.
func stopAtStep(step string) (func(*Config), <-chan struct{}) {
	reached := make(chan struct{})
	var once sync.Once
	return func(c *Config) {
		c.testHandbackRefusedStep = func(at string) {
			if at != step {
				return
			}
			stop := false
			once.Do(func() { stop = true; close(reached) })
			if stop {
				runtime.Goexit()
			}
		}
	}, reached
}

func waitReached(t *testing.T, reached <-chan struct{}) {
	t.Helper()
	eventually(t, "the daemon stopped at its step", func() bool {
		select {
		case <-reached:
			return true
		default:
			return false
		}
	})
}

// refusalGateway keeps the notices a daemon without a link sends.
type refusalGateway struct {
	mu    sync.Mutex
	sends []protocol.MsgSend
}

func (g *refusalGateway) call(_ context.Context, typ string, p any) (json.RawMessage, bool, error) {
	if m, ok := p.(protocol.MsgSend); ok && typ == "msg.send" {
		g.mu.Lock()
		g.sends = append(g.sends, m)
		g.mu.Unlock()
	}
	return json.RawMessage(`{}`), true, nil
}

func (g *refusalGateway) all() []protocol.MsgSend {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]protocol.MsgSend(nil), g.sends...)
}

// refusalDaemon is a daemon with a state folder and a fake gateway, without a
// socket or a link, and one running worker of harness.
func refusalDaemon(t *testing.T, harness string) (*Daemon, *worker, *refusalGateway) {
	t.Helper()
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	g := &refusalGateway{}
	d := &Daemon{cfg: Config{StateDir: t.TempDir(), testGatewayCall: g.call}, st: st, log: log.New(io.Discard, "", 0)}
	w := &worker{id: "w_test", harness: harness, sessionID: "s_worker", owner: "s_owner", taskMsg: "m_task", startedAt: time.Now()}
	w.dir = filepath.Join(d.cfg.StateDir, "workers", w.id)
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	d.workers = map[string]*worker{w.id: w}
	return d, w, g
}

// A refused Codex hand-back is on disk before the caller is told: first the
// record, which adoption reads as "tell the caller, do not hand back again",
// then the idle period, which keeps the turn handled once the record is gone.
// The notice carries the record's key, so a notice sent again after a restart
// is the same message at the gateway.
func TestCodexRefusalIsSavedBeforeTheCallerIsTold(t *testing.T) {
	t.Parallel()
	d, w, g := refusalDaemon(t, "codex")
	var got []string
	key := ""
	state := func(step string) {
		r, saved := d.loadRefusedHandback(w.id)
		if saved {
			key = r.NoticeKey
		}
		since, _, _, sent, err := d.st.loadWorkerIdleNotice(w.id)
		if err != nil {
			t.Error(err)
		}
		got = append(got, fmt.Sprintf("%s: saved=%t idle=%t notices=%d", step, saved, !since.IsZero() && sent, len(g.all())))
	}
	d.cfg.testHandbackRefusedStep = state
	d.codexRefused(w, protocol.WorkerHandbackRequest{Report: "codex report", Status: protocol.StatusDone}, "codex report", RemoteError("unknown recipient s_gone"))
	state("end")
	want := []string{
		"refused: saved=false idle=false notices=0",
		"saved: saved=true idle=false notices=0",
		"idle: saved=true idle=true notices=0",
		"told: saved=true idle=true notices=1",
		"end: saved=false idle=true notices=1",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("steps:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	n := g.all()[0]
	t.Logf("notice frame:\n%s", n.Body)
	if n.IdempotencyKey != key || n.Recipient != "s_owner" || n.ParentID != "m_task" {
		t.Errorf("the notice is not the saved one to the caller: key %s (saved %s), to %s, reply to %s", n.IdempotencyKey, key, n.Recipient, n.ParentID)
	}
	for _, want := range []string{"status: hand-back refused", "unknown recipient s_gone", "codex report"} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("the notice lacks %q", want)
		}
	}
}

// A refused agy hand-back is saved, naming its pending turn, before the
// stream state lets the pending hand-back go, and both are on disk before the
// caller is told.
func TestAgyRefusalIsSavedBeforeTheCallerIsTold(t *testing.T) {
	t.Parallel()
	d, w, g := refusalDaemon(t, "agy")
	req := protocol.WorkerHandbackRequest{Report: "agy report", Status: protocol.StatusDone}
	c := &agyConn{d: d, w: w, streamID: "s_stream", offset: 42, completed: true}
	c.pending, c.pendingKey = &req, c.turnKeyLocked()
	if err := c.saveStreamLocked(); err != nil {
		t.Fatal(err)
	}
	var got []string
	key := ""
	state := func(step string) {
		r, saved := d.loadRefusedHandback(w.id)
		if saved {
			key = r.NoticeKey
			if r.Turn != "agy-w_test-s_stream-42" {
				t.Errorf("the record names turn %q", r.Turn)
			}
		}
		var s agyStreamState
		b, err := os.ReadFile(filepath.Join(w.dir, "agy-stream.json"))
		if err != nil || json.Unmarshal(b, &s) != nil {
			t.Errorf("read the stream state: %v", err)
		}
		got = append(got, fmt.Sprintf("%s: saved=%t pending=%t notices=%d", step, saved, s.Pending != nil, len(g.all())))
	}
	d.cfg.testHandbackRefusedStep = state
	c.refused(context.Background(), req, c.pendingKey, RemoteError("the gateway refused the notice"))
	state("end")
	want := []string{
		"refused: saved=false pending=true notices=0",
		"saved: saved=true pending=true notices=0",
		"cleared: saved=true pending=false notices=0",
		"told: saved=true pending=false notices=1",
		"end: saved=false pending=false notices=1",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("steps:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if n := g.all()[0]; n.IdempotencyKey != key || !strings.Contains(n.Body, "status: hand-back refused") || !strings.Contains(n.Body, "agy report") {
		t.Errorf("the notice is not the saved one: key %s (saved %s)\n%s", n.IdempotencyKey, key, n.Body)
	}
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

// agyHandsBackAfterTheMessage checks that the worker got the one refusal
// message, naming the missing file, and that its next hand-back reached the
// caller as the only notice.
func agyHandsBackAfterTheMessage(t *testing.T, w *workerNode, dir string, res protocol.WorkerStartResult) {
	t.Helper()
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

// A cause the worker can correct goes back to the worker once, in its own
// session, and its next hand-back reaches the caller as usual.
func TestAgyRefusedHandbackGoesBackToTheWorker(t *testing.T) {
	t.Parallel()
	set := shortSet
	set.Idle, set.IdleNotice = time.Hour, time.Hour
	w, dir, _ := startAgyNode(t, set)
	res := refuseAgyHandbackAtRestart(t, w, dir, "FAKE:handback")
	agyHandsBackAfterTheMessage(t, w, dir, res)
}

// The daemon stops at each step before the worker has its message. Before
// the refusal is saved, the next daemon publishes the hand-back again and is
// refused again; after, it goes on from the saved refusal. Either way the
// worker gets one message.
func TestAgyRefusedHandbackStopBeforeTheWorkerIsTold(t *testing.T) {
	t.Parallel()
	for _, step := range []string{"refused", "saved", "cleared"} {
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			set := shortSet
			set.Idle, set.IdleNotice = time.Hour, time.Hour
			stop, reached := stopAtStep(step)
			w, dir, _ := startAgyNode(t, set, stop)
			res := refuseAgyHandbackAtRestart(t, w, dir, "FAKE:handback")
			waitReached(t, reached)
			w.node.restart()
			w.cli = cliClient(t, w.node)
			agyHandsBackAfterTheMessage(t, w, dir, res)
		})
	}
}

// refuseAgyNotice makes the gateway refuse the worker's first "done" notice,
// a cause the worker cannot correct.
func refuseAgyNotice(c *Config) {
	var refused atomic.Bool
	c.testGatewayCall = func(_ context.Context, typ string, p any) (json.RawMessage, bool, error) {
		m, ok := p.(protocol.MsgSend)
		if typ == "msg.send" && ok && m.WorkerNotice && strings.Contains(m.Body, "\nstatus: done\n") && refused.CompareAndSwap(false, true) {
			return nil, true, RemoteError("the test gateway refused this notice")
		}
		return nil, false, nil
	}
}

// agyCallerHasTheRefusal checks the caller's one notice about the refusal,
// then that a follow-up message still reaches the worker and its hand-back.
func agyCallerHasTheRefusal(t *testing.T, w *workerNode, dir string, res protocol.WorkerStartResult) {
	t.Helper()
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
	if got := len(w.notices(t)); got != 2 {
		t.Fatalf("the caller got %d notices, want the refusal and the follow-up hand-back", got)
	}
}

// A cause the worker cannot correct goes to the caller with the worker's
// report, and the worker can still take a message and hand back again.
func TestAgyRefusedHandbackTellsTheCaller(t *testing.T) {
	t.Parallel()
	set := shortSet
	set.Idle, set.IdleNotice = time.Hour, time.Hour
	w, dir, _ := startAgyNode(t, set, refuseAgyNotice)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:handback", Isolation: protocol.IsolationWorktree})
	agyCallerHasTheRefusal(t, w, dir, res)
}

// The daemon stops after the caller's notice is sent and before the saved
// refusal is removed. The next daemon sends the notice again with the saved
// key, and the gateway keeps the first: the caller has it once.
func TestAgyRefusedHandbackStopAfterTheCallerIsTold(t *testing.T) {
	t.Parallel()
	set := shortSet
	set.Idle, set.IdleNotice = time.Hour, time.Hour
	stop, reached := stopAtStep("told")
	w, dir, _ := startAgyNode(t, set, refuseAgyNotice, stop)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:handback", Isolation: protocol.IsolationWorktree})
	waitReached(t, reached)
	path := w.d.refusedHandbackPath(w.d.workerBySession(res.SessionID).id)
	w.node.restart()
	w.cli = cliClient(t, w.node)
	eventually(t, "the next daemon sent the saved refusal", func() bool { _, err := os.Stat(path); return errors.Is(err, os.ErrNotExist) })
	agyCallerHasTheRefusal(t, w, dir, res)
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

// refuseCodexHandback starts a Codex worker whose turn ends while its sender
// is unknown to the gateway, so the gateway refuses the hand-back notice.
func refuseCodexHandback(t *testing.T, opt func(*Config)) (*workerNode, protocol.WorkerStartResult) {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.IdleNotice = time.Second
	w := startWorkerNode(t, g, set, func(c *Config) {
		c.HarnessPaths = map[string]string{"codex": os.Args[0]}
		if opt != nil {
			opt(c)
		}
	})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "gpt-fake", Project: "shop", Harness: "codex", Task: "FAKE:edit FAKE:hold-codex", Isolation: protocol.IsolationWorktree, AutoContinue: autoContinueOff()})
	eventually(t, "codex turn held", func() bool { _, err := os.Stat(filepath.Join(res.Worktree, "fake-codex-held")); return err == nil })
	worker := w.d.workerBySession(res.SessionID)
	w.d.wmu.Lock()
	cc := worker.codex
	w.d.wmu.Unlock()
	cc.mu.Lock()
	cc.cur.from = "s_unknown"
	cc.mu.Unlock()
	if err := os.WriteFile(filepath.Join(res.Worktree, "fake-codex-release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	return w, res
}

// codexRefusalNotice checks the caller's notice about the refused turn.
func codexRefusalNotice(t *testing.T, n protocol.Message, res protocol.WorkerStartResult) {
	t.Helper()
	t.Logf("notice frame:\n%s", n.Body)
	for _, want := range []string{"status: hand-back refused", "could not take the worker's hand-back", "s_unknown", "codex done in", "work.txt", protocol.ChecklistMarker} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice missing %q: %s", want, n.Body)
		}
	}
	if n.ParentID != res.Task.ID {
		t.Errorf("the notice replies to %s, want the task %s", n.ParentID, res.Task.ID)
	}
}

// codexFollowUpAfterRefusal checks that the daemon after a restart did not
// hand the refused turn back again: the caller's next notice answers its
// follow-up, and the caller has two notices in all.
func codexFollowUpAfterRefusal(t *testing.T, w *workerNode, res protocol.WorkerStartResult) {
	t.Helper()
	eventually(t, "codex worker adopted", func() bool { return w.d.workerBySession(res.SessionID) != nil && w.d.sessionByID(res.SessionID) != nil })
	var next protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "continue"}), &next)
	handback := w.waitNotice(t, 2)[1]
	if !strings.Contains(handback.Body, "status: done") || handback.ParentID != next.Message.ID {
		t.Fatalf("follow-up handback: %+v", handback)
	}
	eventually(t, "codex worker ends after follow-up", func() bool { return w.d.workerCount() == 0 })
	if got := len(w.notices(t)); got != 2 {
		t.Fatalf("the caller got %d notices, want the refusal and the follow-up hand-back", got)
	}
}

// The daemon stops at each step of a refused Codex hand-back. Before the
// refusal is saved, the next daemon hands the turn back again, and nobody was
// told before. At any later step the refusal is saved: the next daemon tells
// the caller once and does not hand the turn back again.
func TestCodexRefusedHandbackStopAtEachStep(t *testing.T) {
	t.Parallel()
	for _, step := range []string{"refused", "saved", "idle", "told"} {
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			stop, reached := stopAtStep(step)
			w, res := refuseCodexHandback(t, stop)
			waitReached(t, reached)
			path := w.d.refusedHandbackPath(w.d.workerBySession(res.SessionID).id)
			w.node.restart()
			w.cli = cliClient(t, w.node)
			if step == "refused" {
				n := w.waitNotice(t, 1)[0]
				if !strings.Contains(n.Body, "status: done") || n.ParentID != res.Task.ID {
					t.Fatalf("the turn was not handed back again: %+v", n)
				}
				eventually(t, "codex worker ends after its hand-back", func() bool { return w.d.workerCount() == 0 })
				if got := len(w.notices(t)); got != 1 {
					t.Fatalf("the caller got %d notices, want the hand-back only", got)
				}
				return
			}
			codexRefusalNotice(t, w.waitNotice(t, 1)[0], res)
			eventually(t, "the next daemon sent the saved refusal", func() bool { _, err := os.Stat(path); return errors.Is(err, os.ErrNotExist) })
			codexFollowUpAfterRefusal(t, w, res)
		})
	}
}

// The Codex app server ended while the daemon was down, after the refusal was
// saved and before the caller was told. The worker is lost, and its lost
// notice carries the saved report and the reason.
func TestCodexRefusedHandbackOfALostWorker(t *testing.T) {
	t.Parallel()
	stop, reached := stopAtStep("saved")
	w, res := refuseCodexHandback(t, stop)
	waitReached(t, reached)
	worker := w.d.workerBySession(res.SessionID)
	w.d.wmu.Lock()
	pid, pgid := worker.pid, worker.pgid
	w.d.wmu.Unlock()
	w.node.restartWith(func() {
		syscall.Kill(-pgid, syscall.SIGKILL)
		eventually(t, "the Codex app server ended", func() bool { return !alive(pid) })
	}, true)
	w.cli = cliClient(t, w.node)
	n := w.waitNotice(t, 1)[0]
	t.Logf("notice frame:\n%s", n.Body)
	for _, want := range []string{"status: lost after daemon restart", "its last hand-back was refused", "s_unknown", "codex done in"} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("the lost notice lacks %q:\n%s", want, n.Body)
		}
	}
	if got := len(w.notices(t)); got != 1 {
		t.Fatalf("the caller got %d notices, want the lost notice only", got)
	}
}
