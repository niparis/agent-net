package daemon

import (
	"testing"
	"time"
)

// A restart of an exited session keeps its ID when Claude resumes the same
// conversation under a new native ID (a background copy). The gateway record
// follows the new handle, so the workers, changes and inbox of the session
// stay with it.
func TestRestartKeepsTheSessionIDWhenTheConversationIsResumed(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_NEW_RESUME_ID", "1")
	f := newCTOControlFixture(t)
	result, err := ctoControl(f.cto.node, "restart", f.old)
	if err != nil {
		t.Fatal(err)
	}
	assertCTOImmediate(t, result)
	rec := readClaude(t, f.repo)
	if rec.Session == f.native {
		t.Fatalf("the fake resumed the old conversation instead of a copy: %+v", rec)
	}
	live := f.online(t)
	if live != f.old {
		t.Fatalf("the restart gave the session the new ID %q, want %q", live, f.old)
	}
	a, ok, err := f.g.gw.Store().Agent(f.old)
	if err != nil || !ok {
		t.Fatalf("read the restarted session: %+v %v", a, err)
	}
	if !a.Online || a.NativeID != rec.Session || a.ReplacedBy != "" {
		t.Fatalf("the restarted record is not the same live session: %+v", a)
	}
	if a.Profile.Name != "tenebrae" {
		t.Fatalf("the restart renamed the session to %q", a.Profile.Name)
	}
}

// A worker's hand-back and step tasks are addressed with the caller ID the
// daemon holds, so the daemon moves its own worker records when a restart
// gives the caller a new session.
func TestRestartMovesTheCallerOfTheDaemonsWorkers(t *testing.T) {
	const (
		old   = "s_oldcaller"
		fresh = "s_newcaller"
	)
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, nil)
	n.d.wmu.Lock()
	n.d.workers["w_run"] = &worker{id: "w_run", owner: old, harness: "claude", project: "shop"}
	n.d.wmu.Unlock()
	if err := n.d.st.saveWorker(workerRow{ID: "w_run", Owner: old, Harness: "claude", Project: "shop", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := n.d.st.addQueued(queuedStart{ID: "w_queued", Owner: old, Project: "shop", Path: t.TempDir(), Harness: "claude", Task: "waiting work"}); err != nil {
		t.Fatal(err)
	}
	n.d.reassignWorkerOwner(old, fresh)

	n.d.wmu.Lock()
	running := n.d.workers["w_run"].owner
	n.d.wmu.Unlock()
	if running != fresh {
		t.Fatalf("the running worker still names the old caller: %q", running)
	}
	rows, err := n.d.st.workers()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == "w_run" && row.Owner != fresh {
			t.Fatalf("the stored worker still names the old caller: %+v", row)
		}
	}
	queued, err := n.d.st.queue()
	if err != nil || len(queued) != 1 || queued[0].Owner != fresh {
		t.Fatalf("the queued worker still names the old caller: %+v %v", queued, err)
	}
}
