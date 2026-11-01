package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// reworkRow waits for the daemon's record of a worker and returns it.
func reworkRow(t *testing.T, w *workerNode, session string) workerRow {
	t.Helper()
	var got workerRow
	eventually(t, "the worker record", func() bool {
		rows, err := w.d.st.workers()
		if err != nil {
			return false
		}
		for _, r := range rows {
			if r.SessionID == session {
				got = r
				return true
			}
		}
		return false
	})
	return got
}

// A follow-up from the owner after a hand-back with complete:no counts one
// continue, and the facts reach the notice, the queries and the track record.
func TestWorkerFollowUpAfterPartialHandbackCountsContinue(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback FAKE:partial do half the task", AutoContinue: autoContinueOff()})
	w.waitNotice(t, 1)
	eventually(t, "the partial worker ends", func() bool { return w.d.workerCount() == 0 })
	if row := reworkRow(t, w, res.SessionID); row.Continue != 0 || row.Fix != 0 {
		t.Fatalf("counters before the follow-up: %+v", row)
	}

	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:ignore keep quiet"}), &sent)
	if sent.Message == nil {
		t.Fatal("owner_send did not send the follow-up")
	}

	eventually(t, "one continue", func() bool {
		row := reworkRow(t, w, res.SessionID)
		return row.Continue == 1 && row.ContinueRun == 1 && row.Fix == 0
	})

	// The workers query carries the counters while the worker runs again.
	eventually(t, "the workers query shows the counter", func() bool {
		for _, v := range w.workers(t) {
			if v.SessionID == res.SessionID {
				return v.Continue == 1 && v.Fix == 0
			}
		}
		return false
	})
	// `list` carries them in the worker part of the session.
	var listed protocol.AgentsResult
	if err := json.Unmarshal(w.cli.mustCall("list", protocol.QueryAgents{IncludeOwners: true}), &listed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range listed.Agents {
		if a.SessionID == res.SessionID && a.Worker != nil {
			found = true
			if a.Worker.Continue != 1 || a.Worker.Fix != 0 {
				t.Errorf("list worker counters: %+v", a.Worker)
			}
			if a.Worker.DifficultyUsed != "normal" {
				t.Errorf("difficulty used: %q", a.Worker.DifficultyUsed)
			}
		}
	}
	if !found {
		t.Fatal("the worker is not in the list")
	}

	// The end notice carries the counter.
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	notices := w.waitNotice(t, 2)
	last := notices[len(notices)-1]
	if !strings.Contains(last.Body, "continue: 1") || !strings.Contains(last.Body, "fix: 0") {
		t.Errorf("the notice does not carry the counters:\n%s", last.Body)
	}

	// The gateway keeps the counters on the worker record and counts the run
	// in the model track record.
	eventually(t, "the gateway worker record has the counter", func() bool {
		a, ok, err := g.gw.Store().Agent(res.SessionID)
		return err == nil && ok && a.Worker != nil && a.Worker.Continue == 1
	})
	tr, ok, err := g.gw.Store().TrackRecord(w.d.NodeID(), "opencode", "fake/m1", protocol.TrackRecordDays*24*time.Hour)
	if err != nil || !ok {
		t.Fatalf("track record: %v %v", ok, err)
	}
	if tr.Continue != 1 || tr.Fix != 0 {
		t.Errorf("track record rework: %+v", tr)
	}
}

// A follow-up after a hand-back with complete:yes counts one fix.
func TestWorkerFollowUpAfterCompleteHandbackCountsFix(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback do the task"})
	w.waitNotice(t, 1)
	eventually(t, "the worker ends", func() bool { return w.d.workerCount() == 0 })

	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:ignore keep quiet"}), &sent)

	eventually(t, "one fix", func() bool {
		row := reworkRow(t, w, res.SessionID)
		return row.Fix == 1 && row.FixRun == 1 && row.Continue == 0
	})
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	notices := w.waitNotice(t, 2)
	last := notices[len(notices)-1]
	if !strings.Contains(last.Body, "fix: 1") || !strings.Contains(last.Body, "continue: 0") {
		t.Errorf("the notice does not carry the counters:\n%s", last.Body)
	}
}

// Only a message from the work's owner counts: another session and a notice
// are ignored.
func TestWorkerReworkIgnoresOtherSessionsAndNotices(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	d := w.d
	wk := &worker{id: "w_rework", owner: "s_owner", sessionID: "s_rework", project: "shop",
		startedAt: time.Now(), pid: 1, taskText: "a task", pendingHandback: true, pendingPartial: true}
	d.wmu.Lock()
	d.workers[wk.id] = wk
	d.wmu.Unlock()
	t.Cleanup(func() {
		d.wmu.Lock()
		delete(d.workers, wk.id)
		d.wmu.Unlock()
	})

	d.noteAsk(protocol.Message{ID: "m1", From: "s_other", To: wk.sessionID, Type: protocol.TypeRequest, Body: "from another session"})
	d.noteAsk(protocol.Message{ID: "m2", From: wk.owner, To: wk.sessionID, Type: protocol.TypeNotification, Body: "a notice"})
	d.wmu.Lock()
	cont, fix := wk.continueCount, wk.fixCount
	d.wmu.Unlock()
	if cont != 0 || fix != 0 {
		t.Fatalf("a message that is not the owner's request counted: continue %d fix %d", cont, fix)
	}

	d.noteAsk(protocol.Message{ID: "m3", From: wk.owner, To: wk.sessionID, Type: protocol.TypeRequest, Body: "do it again"})
	d.noteAsk(protocol.Message{ID: "m4", From: wk.owner, To: wk.sessionID, Type: protocol.TypeRequest, Body: "and again"})
	d.wmu.Lock()
	cont, fix, pending := wk.continueCount, wk.fixCount, wk.pendingHandback
	d.wmu.Unlock()
	if cont != 2 || fix != 0 || !pending {
		t.Fatalf("the owner's follow-ups: continue %d fix %d pending %v", cont, fix, pending)
	}
	// A new hand-back with complete:yes counts fixes from then on.
	d.wmu.Lock()
	wk.pendingPartial = false
	d.wmu.Unlock()
	d.noteAsk(protocol.Message{ID: "m5", From: wk.owner, To: wk.sessionID, Type: protocol.TypeRequest, Body: "fix it"})
	d.wmu.Lock()
	cont, fix = wk.continueCount, wk.fixCount
	d.wmu.Unlock()
	if cont != 2 || fix != 1 {
		t.Fatalf("after a complete hand-back: continue %d fix %d", cont, fix)
	}
}

// The counters and the pending hand-back survive a daemon restart.
func TestWorkerReworkCountersSurviveDaemonRestart(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback FAKE:partial do half", AutoContinue: autoContinueOff()})
	w.waitNotice(t, 1)
	eventually(t, "the worker ends", func() bool { return w.d.workerCount() == 0 })
	if row := reworkRow(t, w, res.SessionID); !row.PendingHandback || !row.PendingPartial {
		t.Fatalf("the partial hand-back is not pending: %+v", row)
	}

	// The pending hand-back survives: the first follow-up after the restart
	// still counts one continue.
	w.d.Stop()
	w.node.restart()
	w.cli = cliClient(t, w.node)
	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:ignore keep quiet"}), &sent)
	eventually(t, "one continue after the restart", func() bool {
		row := reworkRow(t, w, res.SessionID)
		return row.Continue == 1 && row.ContinueRun == 1
	})

	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	w.d.Stop()
	w.node.restart()
	w.cli = cliClient(t, w.node)
	row := reworkRow(t, w, res.SessionID)
	if row.Continue != 1 || row.ContinueRun != 1 {
		t.Errorf("after the second restart: %+v", row)
	}
}

// An idle worker that did not hand back is continued by the owner's next
// message. That message counts as a continue: the worker stopped early
// without saying so.
func TestWorkerFollowUpAfterIdleNoticeCountsContinue(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	d := w.d
	wk := &worker{id: "w_idle_rework", owner: "s_owner", sessionID: "s_idle_rework", project: "shop",
		startedAt: time.Now(), pid: 1, taskText: "a task", idleNoticeSent: true}
	d.wmu.Lock()
	d.workers[wk.id] = wk
	d.wmu.Unlock()
	t.Cleanup(func() {
		d.wmu.Lock()
		delete(d.workers, wk.id)
		d.wmu.Unlock()
	})

	d.noteAsk(protocol.Message{ID: "m1", From: wk.owner, To: wk.sessionID, Type: protocol.TypeRequest,
		Body: "Continue now: finish the task"})
	d.wmu.Lock()
	cont, fix := wk.continueCount, wk.fixCount
	d.wmu.Unlock()
	if cont != 1 || fix != 0 {
		t.Fatalf("a follow-up to an idle worker: continue %d fix %d, want 1 and 0", cont, fix)
	}
}

// The routed path does the same: an owner message after the idle notice of an
// OpenCode worker that never handed back counts one continue.
func TestWorkerIdleFollowUpCountsContinue(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.IdleNotice = 200 * time.Millisecond
	w := startWorkerNode(t, g, set, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode",
		Task: "FAKE:edit", Isolation: protocol.IsolationWorktree, AutoContinue: autoContinueOff()})
	w.waitNotice(t, 1)
	eventually(t, "the idle notice is recorded", func() bool {
		wk := w.d.workerBySession(res.SessionID)
		if wk == nil {
			return false
		}
		w.d.wmu.Lock()
		sent := wk.idleNoticeSent
		w.d.wmu.Unlock()
		return sent
	})

	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID,
		Body: "Continue now: FAKE:ignore keep quiet"}), &sent)
	eventually(t, "the idle follow-up counts a continue", func() bool {
		row := reworkRow(t, w, res.SessionID)
		return row.Continue == 1 && row.ContinueRun == 1 && row.Fix == 0
	})
}
