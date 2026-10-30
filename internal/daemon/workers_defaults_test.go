package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The defaults of a short request (decision 0002, W17): a repository gets a
// worktree, "none" opts out, and the notice ends with agent-net's checklist.
func TestWorkerDefaultsToAWorktreeInARepository(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)

	res, err := w.startDefault(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "FAKE:edit FAKE:handback add work.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Worktree == "" || res.Branch == "" || res.Worktree == repo {
		t.Fatalf("no worktree by default: %+v", res)
	}
	if rec := readFake(t, res.Worktree); rec.Cwd != res.Worktree {
		t.Errorf("the worker runs in %s, not in the worktree %s", rec.Cwd, res.Worktree)
	}
	n := w.waitNotice(t, 1)[0]
	last := n.Body[strings.LastIndex(n.Body, "\n")+1:]
	if !strings.HasPrefix(last, "If you learned something") || !strings.Contains(last, "agent_learn") {
		t.Errorf("the notice does not end with the learning line:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, protocol.ChecklistMarker) || !strings.Contains(n.Body, "read the diff in "+res.Worktree) ||
		!strings.Contains(n.Body, "run the project's tests there") {
		t.Errorf("the notice has no checklist:\n%s", n.Body)
	}
}

func TestWorkerSandboxFollowsTheNodesMaturity(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	st := g.gw.Store()
	if err := st.SetCharter("shop", "maturity", "critical - money", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeSetting("shop", protocol.SettingWorkerSandbox, protocol.WorkersReadOnly, "owner"); err != nil {
		t.Fatal(err)
	}
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	if res.Permissions != protocol.WorkersReadOnly {
		t.Fatalf("the worker got %q, want the node's override %q", res.Permissions, protocol.WorkersReadOnly)
	}
}

func TestWorkerIsolationNoneWorksInTheProjectFolder(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x", Isolation: protocol.IsolationNone})
	if res.Worktree != "" || res.Branch != "" {
		t.Errorf("a worktree although isolation is none: %+v", res)
	}
	if rec := readFake(t, repo); rec.Cwd != repo {
		t.Errorf("the worker runs in %s, not in the project %s", rec.Cwd, repo)
	}
	// Pi has no permission prompts: it still needs a worktree.
	_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "pi", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "needs isolation") {
		t.Errorf("pi with isolation none: %v", err)
	}
	_, err = w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x", Isolation: "docker"})
	if err == nil || !strings.Contains(err.Error(), `"none"`) {
		t.Errorf("bad isolation: %v", err)
	}
}

// A folder that is not a repository gets no worktree, and no error.
func TestIsGitRepo(t *testing.T) {
	ctx := context.Background()
	if isGitRepo(ctx, t.TempDir()) {
		t.Error("an empty folder is not a repository")
	}
	if !isGitRepo(ctx, newRepo(t, "shop")) {
		t.Error("a repository with a commit is one")
	}
	if isGitRepo(ctx, filepath.Join(os.TempDir(), "agent-net-no-such-folder")) {
		t.Error("a missing folder is not a repository")
	}
}

// A loose model name is resolved on the gateway against the machine's list,
// and the result says how.
func TestWorkerModelIsResolved(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil) // the list is fake/m1 and fake/m2
	repo := newRepo(t, "shop")
	w.register(t, repo)

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "M2", Task: "x"})
	if res.Model != "fake/m2" || res.ModelNote != "M2 -> fake/m2" {
		t.Errorf("resolved model: %q, note %q", res.Model, res.ModelNote)
	}
	rec := readFake(t, repo)
	if m, _ := rec.SessionBody["model"].(map[string]any); m["providerID"] != "fake" || m["id"] != "m2" {
		t.Errorf("the harness got model %v", rec.SessionBody["model"])
	}
	// An exact name needs no note.
	res = w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "x"})
	if res.Model != "fake/m1" || res.ModelNote != "" {
		t.Errorf("exact model: %q, note %q", res.Model, res.ModelNote)
	}
	// Two models match equally well: an error that lists them.
	_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "fake/m1, fake/m2") {
		t.Errorf("tie: %v", err)
	}
	// Claude has no list: the alias passes through.
	if _, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Model: "sonnet", Task: "x"}); err != nil &&
		strings.Contains(err.Error(), "model") {
		t.Errorf("claude alias: %v", err)
	}
}

// With no project, the worker starts in the project of the calling session. The
// command line has none and must name one.
func TestWorkerProjectDefaultsToTheCallers(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)

	_, err := w.start(protocol.WorkerStartRequest{Harness: "opencode", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "no project") {
		t.Errorf("command line without a project: %v", err)
	}
	// An agent session that runs in the repository.
	pi, piID := helloPi(t, w.node, "caller-pi", repo, true)
	res := mustWorker(t, pi.mustCall("worker_start", protocol.WorkerStartRequest{Harness: "opencode", Task: "x"}))
	if res.Project != "shop" || res.Path != repo || res.Task == nil || res.Task.From != piID || res.Worktree == "" {
		t.Errorf("worker for the caller's project: %+v", res)
	}
	// A caller in a folder that is not a repository: its folder is a seen
	// project, and the worker gets no worktree there.
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	other, _ := helloPi(t, w.node, "plain-pi", dir, true)
	res = mustWorker(t, other.mustCall("worker_start", protocol.WorkerStartRequest{Harness: "opencode", Task: "x"}))
	if res.Path != dir || res.Worktree != "" {
		t.Errorf("worker in a plain folder: %+v", res)
	}
}

// When the caller's project is on two machines, the caller's own machine wins,
// and else the one that has the harness. A named project is still ambiguous.
func TestWorkerCallerProjectPicksTheMachine(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	a := startWorkerNode(t, g, shortSet, nil)
	b := startWorkerNode(t, g, shortSet, nil)
	ra, rb := newRepo(t, "shop"), newRepo(t, "shop")
	a.register(t, ra)
	b.register(t, rb)
	owner := a.ownerID()
	call := func(from *workerNode, callerProject bool) (res protocol.WorkerStartAnswer, err error) {
		data, err := from.d.call(context.Background(), "worker.start", protocol.WorkerStartFrame{
			WorkerStartRequest: protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x", Isolation: protocol.IsolationNone},
			Owner:              owner, CallerProject: callerProject})
		if err == nil {
			err = json.Unmarshal(data, &res)
		}
		return res, err
	}
	res, err := call(a, true)
	if err != nil || res.NodeID != a.d.NodeID() {
		t.Fatalf("the caller's machine should win: %+v, %v", res, err)
	}
	if _, err := call(a, false); err == nil || !strings.Contains(err.Error(), "is on several machines") {
		t.Errorf("a named project on two machines: %v", err)
	}
}
