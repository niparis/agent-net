package daemon

import (
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A worker that hands back ends after its notice, and the gateway keeps the
// ended state. A late session sync must not turn the row back into a busy one.
func TestHandbackEndLeavesTheGatewayRowEnded(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode",
		Task: "FAKE:handback done", Isolation: protocol.IsolationNone})
	w.waitNotice(t, 1)
	eventually(t, "the worker ends after its hand-back", func() bool { return w.d.workerCount() == 0 })
	eventually(t, "the ended worker record is reported", func() bool {
		rows, _ := w.d.st.workers()
		return len(rows) == 1 && rows[0].Reported
	})
	eventually(t, "the gateway row is ended", func() bool {
		a, ok, err := g.gw.Store().Agent(res.SessionID)
		return err == nil && ok && !a.Online && a.Status == "ended"
	})
}

// A worker whose process exits without a hand-back still ends in the gateway,
// with status ended and online false.
func TestWorkerProcessExitLeavesTheGatewayRowEnded(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode",
		Task: "FAKE:ignore hold the worker", Isolation: protocol.IsolationNone})
	rec := readFake(t, repo)
	eventually(t, "the worker is listed", func() bool { return w.d.workerCount() == 1 })
	if err := syscall.Kill(-rec.Pgid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill worker process group: %v", err)
	}
	eventually(t, "the worker ends after its process exit", func() bool { return w.d.workerCount() == 0 })
	eventually(t, "the ended worker record is reported", func() bool {
		rows, _ := w.d.st.workers()
		return len(rows) == 1 && rows[0].Reported
	})
	eventually(t, "the gateway row is ended", func() bool {
		a, ok, err := g.gw.Store().Agent(res.SessionID)
		return err == nil && ok && !a.Online && a.Status == "ended"
	})
	time.Sleep(100 * time.Millisecond) // give a late session sync the chance to change it
	if a, _, _ := g.gw.Store().Agent(res.SessionID); a.Online || a.Status != "ended" {
		t.Fatalf("after a late session sync: online=%v status=%q", a.Online, a.Status)
	}
}

// A worker that ends after its hand-back, whose harness session is still
// listed busy for a moment, stays ended in the gateway.
func TestLateSessionSyncAfterHandbackLeavesTheRowEnded(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode",
		Task: "FAKE:handback done", Isolation: protocol.IsolationNone})
	rec := readFake(t, repo)
	w.waitNotice(t, 1)
	eventually(t, "the worker ends after its hand-back", func() bool { return w.d.workerCount() == 0 })
	eventually(t, "the ended worker record is reported", func() bool {
		rows, _ := w.d.st.workers()
		return len(rows) == 1 && rows[0].Reported
	})
	// What the daemon's session sync can still send for a moment: the harness
	// session it keeps for a resume, with the busy status it had before.
	info := protocol.SessionInfo{Harness: "opencode", NativeID: rec.SessionID, Status: "busy",
		Worker: &protocol.WorkerInfo{ID: "w_late"}}
	if _, err := g.gw.Store().UpsertSession(w.d.NodeID(), info); err != nil {
		t.Fatal(err)
	}
	a, _, _ := g.gw.Store().Agent(res.SessionID)
	if a.Online || a.Status != "ended" {
		t.Fatalf("after a late session sync: online=%v status=%q", a.Online, a.Status)
	}
}
