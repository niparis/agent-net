package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// An adopted worker keeps its session ID across a daemon restart, even when
// its gateway record is old-style (no start time) and offline at the first
// cleanup, and even when the gateway lost the record.
func TestAdoptedWorkerKeepsItsSessionID(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.MaxAge = time.Hour
	w := startWorkerNode(t, g, set, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	st := g.gw.Store()
	ag, ok, _ := st.Agent(res.SessionID)
	if !ok {
		t.Fatal("no record for the worker")
	}
	rows, _ := w.d.st.workers()
	if len(rows) != 1 {
		t.Fatalf("%d worker rows", len(rows))
	}
	// An older daemon wrote the record without a start time.
	info := protocol.SessionInfo{Harness: "opencode", NativeID: rows[0].NativeID, Cwd: ag.Cwd, Status: "idle",
		Worker: &protocol.WorkerInfo{ID: rows[0].ID, Owner: w.ownerID(), Project: "shop"}}
	if _, err := st.UpsertSession(w.d.NodeID(), info); err != nil {
		t.Fatal(err)
	}
	w.d.Stop()
	// What the broken cleanup did: the offline record is gone.
	if err := st.SetSessionOffline(w.d.NodeID(), res.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CleanWorkers(w.d.NodeID(), time.Nanosecond, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.Agent(res.SessionID); ok {
		t.Fatal("setup: the record is still there")
	}
	w.node.restart()
	w.cli = cliClient(t, w.node)
	eventually(t, "the adopted worker is back under its session ID", func() bool {
		for _, v := range w.workers(t) {
			if v.SessionID == res.SessionID && v.Online {
				return true
			}
		}
		return false
	})
	if n := len(w.workers(t)); n != 1 {
		t.Fatalf("%d workers listed, want 1", n)
	}
	// A cleanup after the restart keeps it.
	w.d.cleanupWorkers(context.Background())
	if _, ok, _ := st.Agent(res.SessionID); !ok {
		t.Fatal("cleanup deleted the record of a running worker")
	}
}

// Re-registering a known session must ask the gateway for its existing ID,
// including when a status update races gateway record cleanup.
func TestSessionReportsKnownID(t *testing.T) {
	d := &Daemon{sessions: make(map[string]*localSession)}
	ls := &localSession{sessionID: "s_existing", info: protocol.SessionInfo{Harness: "opencode", NativeID: "native"}}
	d.sessions[key("opencode", "native")] = ls
	if got := d.infoOf(ls).SessionID; got != ls.sessionID {
		t.Errorf("upsert ID = %q, want %q", got, ls.sessionID)
	}
	if got := d.snapshot()[0].SessionID; got != ls.sessionID {
		t.Errorf("sync ID = %q, want %q", got, ls.sessionID)
	}
}

func TestAdoptedCodexKeepsItsSessionIDAfterRecordCleanup(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Task: "FAKE:slow"})
	readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.Turns) == 1 })
	w.d.Stop()
	st := g.gw.Store()
	if err := st.SetSessionOffline(w.d.NodeID(), res.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CleanWorkers(w.d.NodeID(), time.Nanosecond, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.Agent(res.SessionID); err != nil || ok {
		t.Fatalf("worker record remains: exists=%v err=%v", ok, err)
	}
	w.node.restart()
	w.cli = cliClient(t, w.node)
	eventually(t, "adopted Codex keeps its session ID", func() bool {
		for _, v := range w.workers(t) {
			if v.SessionID == res.SessionID && v.Online {
				return true
			}
		}
		return false
	})
}
