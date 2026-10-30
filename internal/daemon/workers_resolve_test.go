package daemon

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Two folders named "shop" on one machine: r1 is registered, r2 is only seen
// because a session ran there.
func twoShops(t *testing.T) (w *workerNode, r1, r2 string, pi *localClient, piID string) {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	w = startWorkerNode(t, g, shortSet, nil)
	r1, r2 = newRepo(t, "shop"), newRepo(t, "shop")
	w.register(t, r1)
	pi, piID = helloPi(t, w.node, "caller-pi", r2, true)
	return
}

func TestWorkerRegisteredFolderWinsOverSeenOnly(t *testing.T) {
	w, r1, _, _, _ := twoShops(t)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	if res.Path != r1 {
		t.Errorf("the registered folder should win: %+v", res)
	}
}

func TestWorkerAmbiguityListsPathsAndHints(t *testing.T) {
	w, r1, r2, _, _ := twoShops(t)
	w.register(t, r2)
	_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), r1) || !strings.Contains(err.Error(), r2) ||
		!strings.Contains(err.Error(), "pass one of these paths as project") {
		t.Errorf("two registered folders: %v", err)
	}
	// The hint works: the path picks one.
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: r2, Harness: "opencode", Task: "x"})
	if res.Path != r2 {
		t.Errorf("path as project: %+v", res)
	}
}

func TestWorkerSeenOnlyFoldersStayAmbiguous(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	r1, r2 := newRepo(t, "shop"), newRepo(t, "shop")
	helloPi(t, w.node, "pi-1", r1, true)
	helloPi(t, w.node, "pi-2", r2, true)
	_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "2 folders named") || !strings.Contains(err.Error(), r1) {
		t.Errorf("two seen-only folders: %v", err)
	}
}

// An empty project means the caller's own folder, found by path, even when a
// registered folder with the same name exists.
func TestWorkerEmptyProjectUsesTheCallersFolderExactly(t *testing.T) {
	_, _, r2, pi, piID := twoShops(t)
	res := mustWorker(t, pi.mustCall("worker_start", protocol.WorkerStartRequest{Harness: "opencode", Task: "x", Isolation: protocol.IsolationNone}))
	if res.Path != r2 || res.Task == nil || res.Task.From != piID {
		t.Errorf("worker for the caller's folder: %+v", res)
	}
}

func TestWorkerProjectAsPath(t *testing.T) {
	w, _, r2, _, _ := twoShops(t)
	// A seen-only folder by path.
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: r2, Harness: "opencode", Task: "x"})
	if res.Path != r2 || res.Project != "shop" {
		t.Errorf("seen folder by path: %+v", res)
	}
	// A linked worktree of a known project matches too.
	wt := filepath.Join(filepath.Dir(r2), "shop-wt")
	gitIn(t, r2, "worktree", "add", "-q", "-b", "wt", wt)
	helloPi(t, w.node, "pi-wt", wt, true)
	res = w.mustStart(t, protocol.WorkerStartRequest{Project: wt, Harness: "opencode", Task: "x"})
	if res.Path != r2 {
		t.Errorf("worktree path: %+v", res)
	}
	// An unknown path says so.
	_, err := w.start(protocol.WorkerStartRequest{Project: "/no/such/folder", Harness: "opencode", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "no known project at the path") {
		t.Errorf("unknown path: %v", err)
	}
}
