package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A worker that hands back complete ends at once, as done, while its check
// waits for the machine's one test slot. Before, it stayed alive and idle
// until the check ended: ten finished workers sat idle for over an hour, the
// Claude workers panel showed no end, and they filled the worker cap.

// The worker ends right after its hand-back, before its check ends. Its record
// says it ended after a hand-back, so a message from its owner starts it
// again. The notice still waits for the check and carries its result.
func TestCompleteHandbackEndsWorkerBeforeCheck(t *testing.T) {
	a := startAsyncCheck(t)
	wk := a.w.d.workerBySession(a.res.SessionID)
	a.w.d.wmu.Lock()
	pid := wk.pid
	a.w.d.wmu.Unlock()
	a.handback(t)
	a.waitStarts(t, 1)
	a.waitEnded(t)
	if alive(pid) {
		t.Error("the worker process still runs after its hand-back")
	}
	row, ok, err := a.w.d.st.workerBySession(a.res.SessionID)
	if err != nil || !ok || row.EndedReason != protocol.StopHandback || row.DoneStatus != protocol.StatusDone || !row.Resumable {
		t.Fatalf("worker row: ok=%t err=%v %+v", ok, err, row)
	}
	if n := len(a.w.notices(t)); n != 0 {
		t.Fatalf("the notice went out before the check ended (%d)", n)
	}
	a.open(t)
	n := a.w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "hand-back check: passed in ") || !strings.Contains(n.Body, "ended after its hand-back") {
		t.Fatalf("notice:\n%s", n.Body)
	}
}

// A worker whose check still runs does not count toward the machine's worker
// cap: the next start runs at once instead of waiting in the queue. The
// running check itself counts once, as any suite in the test slot does, so a
// cap of two leaves room for one start.
func TestCheckingWorkerDoesNotCountTowardCap(t *testing.T) {
	set := queueSet
	set.Max = 2
	a := startAsyncCheckWith(t, autoContinueOff(), set)
	a.handback(t)
	a.waitStarts(t, 1)
	eventually(t, "the cap leaves room while the check runs", func() bool {
		return a.w.d.admissionWithLastStart(a.w.d.SettingsSnapshot(), WorkerCost{}, time.Time{}).Room != protocol.RoomCap
	})
	next, err := a.w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "FAKE:ignore"})
	if err != nil || next.Status != "started" {
		t.Fatalf("the next start: %+v %v", next, err)
	}
}

// The workers list shows the ended worker as done with the state of its
// check, however long the check waits, and without it once the notice is out.
// The Claude workers panel draws its ✓ from that row.
func TestWorkersListShowsCheckOfEndedWorker(t *testing.T) {
	a := startAsyncCheck(t)
	a.handback(t)
	a.waitStarts(t, 1)
	row := func() (protocol.WorkerView, string) {
		raw := a.w.cli.mustCall(protocol.OpWorkers, protocol.WorkersQuery{Recent: true})
		var r protocol.WorkersResult
		mustJSON(t, raw, &r)
		for _, v := range r.Workers {
			if v.SessionID == a.res.SessionID {
				b, _ := json.Marshal(v)
				return v, string(b)
			}
		}
		return protocol.WorkerView{}, ""
	}
	eventually(t, "the list shows the worker done, with its check running", func() bool {
		v, raw := row()
		return v.EndStatus == protocol.StatusDone && v.Check == protocol.HandbackCheckRunning && strings.Contains(raw, `"check":"running"`)
	})
	// The two-minute window of ended workers does not drop it while it checks.
	kept := false
	for _, v := range a.w.d.recentWorkers(time.Now().Add(time.Hour)) {
		kept = kept || (v.SessionID == a.res.SessionID && v.Check == protocol.HandbackCheckRunning)
	}
	if !kept {
		t.Error("a worker whose check runs left the list after the recent window")
	}
	a.open(t)
	a.w.waitNotice(t, 1)
	eventually(t, "the list shows the worker done, with no check", func() bool {
		v, _ := row()
		return v.EndStatus == protocol.StatusDone && v.Check == ""
	})
}
