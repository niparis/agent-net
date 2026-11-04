package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// autoContinueOff is the caller's explicit "tell me at once" setting.
func autoContinueOff() *bool {
	off := false
	return &off
}

// A partial hand-back gets one automatic continue, then another, and only the
// third stop reaches the caller, with the count. The automatic continues are
// kept apart from the caller's rework counters and show in the track record.
func TestAutoContinuePartialHandbackStopsAtTwo(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.IdleNotice = time.Hour // this test is about hand-backs, not idleness
	w := startWorkerNode(t, g, set, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback FAKE:partial do half the task"})

	// The first partial hand-back: one automatic continue, no caller notice.
	eventually(t, "the first automatic continue", func() bool {
		row := reworkRow(t, w, res.SessionID)
		return row.AutoContinueCount == 1 && row.AutoContinueRun == 1 && fakeAutoContinueCount(repo) == 1
	})
	if got := len(w.notices(t)); got != 0 {
		t.Fatalf("the caller got %d notices after the first automatic continue, want none", got)
	}

	// The second partial hand-back: one more automatic continue, still no notice.
	eventually(t, "the second automatic continue", func() bool {
		row := reworkRow(t, w, res.SessionID)
		return row.AutoContinueCount == 2 && row.AutoContinueRun == 2 && fakeAutoContinueCount(repo) == 2
	})
	if got := len(w.notices(t)); got != 0 {
		t.Fatalf("the caller got %d notices after the second automatic continue, want none", got)
	}

	// The third partial hand-back is not continued: the caller is told.
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "status: done") || !strings.Contains(n.Body, "2 automatic continues") {
		t.Errorf("the notice does not say how many automatic continues were sent:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "auto-continues: 2") {
		t.Errorf("the notice facts do not carry the automatic continues:\n%s", n.Body)
	}
	eventually(t, "the worker ends after the third partial hand-back", func() bool { return w.d.workerCount() == 0 })
	row := reworkRow(t, w, res.SessionID)
	if row.AutoContinueCount != 2 || row.AutoContinueRun != 2 {
		t.Errorf("the worker record: auto continues %d/%d", row.AutoContinueCount, row.AutoContinueRun)
	}
	// They are not the caller's continues: the caller sent no message.
	if row.Continue != 0 || row.Fix != 0 || row.ContinueRun != 0 || row.FixRun != 0 {
		t.Errorf("automatic continues counted as caller rework: %+v", row)
	}

	// The gateway's track record shows them apart from continue and fix.
	eventually(t, "the automatic continues reach the track record", func() bool {
		tr, ok, err := g.gw.Store().TrackRecord(w.d.NodeID(), "opencode", "fake/m1", 30*24*time.Hour)
		return err == nil && ok && tr.AutoContinue == 2
	})
	tr, ok, err := g.gw.Store().TrackRecord(w.d.NodeID(), "opencode", "fake/m1", 30*24*time.Hour)
	if err != nil || !ok {
		t.Fatalf("track record: %v %v", ok, err)
	}
	if tr.Continue != 0 || tr.Fix != 0 {
		t.Errorf("automatic continues leaked into the caller counters: %+v", tr)
	}
	if line := tr.Line(); !strings.Contains(line, "2 auto-continues") {
		t.Errorf("track record line: %q", line)
	}
}

// auto_continue false keeps the old behavior: the first partial hand-back is
// reported to the caller at once.
func TestAutoContinueOffSendsNoContinue(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback FAKE:partial do half", AutoContinue: autoContinueOff()})

	n := w.waitNotice(t, 1)[0]
	if strings.Contains(n.Body, "automatic continue") || strings.Contains(n.Body, "auto-continues:") {
		t.Errorf("a notice for a worker with auto_continue false mentions automatic continues:\n%s", n.Body)
	}
	eventually(t, "the worker ends", func() bool { return w.d.workerCount() == 0 })
	row := reworkRow(t, w, res.SessionID)
	if row.AutoContinue {
		t.Error("the worker record lost auto_continue false")
	}
	if row.AutoContinueCount != 0 || row.AutoContinueRun != 0 {
		t.Errorf("auto continues were sent while they were off: %+v", row)
	}
	if got := fakeAutoContinueCount(repo); got != 0 {
		t.Fatalf("the worker got %d automatic continues, want none", got)
	}
}

// A worker that goes idle without a hand-back also gets automatic continues,
// and only after the second one is the caller told.
func TestAutoContinueIdleWithoutHandback(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.IdleNotice = 150 * time.Millisecond
	w := startWorkerNode(t, g, set, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:ignore keep quiet"})

	eventually(t, "the first automatic continue from idleness", func() bool {
		row := reworkRow(t, w, res.SessionID)
		return row.AutoContinueCount == 1 && fakeAutoContinueCount(repo) == 1
	})
	if got := len(w.notices(t)); got != 0 {
		t.Fatalf("the caller got %d notices after the first automatic continue, want none", got)
	}
	eventually(t, "the second automatic continue from idleness", func() bool {
		row := reworkRow(t, w, res.SessionID)
		return row.AutoContinueCount == 2 && fakeAutoContinueCount(repo) == 2
	})
	if got := fakeAutoContinueCount(repo); got != 2 {
		t.Fatalf("the worker got %d automatic continues, want 2", got)
	}
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "status: idle without hand-back") || !strings.Contains(n.Body, "2 automatic continues") {
		t.Errorf("the idle notice does not say how many automatic continues were sent:\n%s", n.Body)
	}
}

// A worker whose model call failed is never continued: it did not stop early,
// it failed.
func TestAutoContinueFailedWorkerGetsNone(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.Idle = time.Hour
	w := startWorkerNode(t, g, set, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:modelfail do it"})
	w.workerStopped(t, g, protocol.StopModelFails)
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "status: failed") {
		t.Errorf("notice:\n%s", n.Body)
	}
	row := reworkRow(t, w, res.SessionID)
	if row.AutoContinueCount != 0 || row.AutoContinueRun != 0 {
		t.Errorf("a failed worker got an automatic continue: %+v", row)
	}
	if got := fakeAutoContinueCount(repo); got != 0 {
		t.Fatalf("a failed worker got %d automatic continues, want none", got)
	}
}

// A Codex worker reports "complete: no", gets an automatic continue, and the
// report of that next turn reaches the caller as a hand-back. The continue
// comes from the reserved agent-net sender; the report must still go to the
// caller, not back to that sender (v0.4.93 and earlier: "unknown recipient
// session", and the caller was told "idle without hand-back").
func TestAutoContinueCodexReportReachesTheCaller(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.IdleNotice = time.Hour
	w := startCodexNode(t, g, set)
	repo := newRepo(t, "shop")
	w.register(t, repo)

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Model: "gpt-fake", Task: "FAKE:partial do half the task"})
	n := w.waitNotice(t, 1)[0]
	if strings.Contains(n.Body, "idle without hand-back") || !strings.Contains(n.Body, "status: done") {
		t.Fatalf("the caller did not get the hand-back after the automatic continue:\n%s", n.Body)
	}
	if row := reworkRow(t, w, res.SessionID); row.AutoContinueCount != 1 {
		t.Errorf("automatic continues: %d, want 1", row.AutoContinueCount)
	}
}
