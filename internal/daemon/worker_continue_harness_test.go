package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A continue can move a worker to another harness (decision 0189): the
// picker chooses again when the old model no longer qualifies. The new worker
// is a fresh session of the new harness, so these tests check what carries
// over: the work on the branch, the delivery change, the task ID and the full
// first task.

// startMixedNode starts a node that runs the fake OpenCode, Claude, Codex and
// agy, and registers the project "shop".
func startMixedNode(t *testing.T) (*workerNode, *testGateway, string) {
	t.Helper()
	dir := t.TempDir()
	agy := filepath.Join(dir, "agy")
	script := "#!/bin/sh\nFAKE_AGY_RECORDS=" + shellQuote(dir) + " FAKE_AGY_PROCESS=1 exec " + shellQuote(os.Args[0]) + " -test.run '^TestAgyProcess$' -- \"$@\"\n"
	if err := os.WriteFile(agy, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	sessions, _ := useFakeClaude(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	set := shortSet
	// The fake agy and Claude are whole test binaries: give them the scaled
	// start budget of the other harness tests.
	set.StartTimeout = scaled(30 * time.Second)
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNodeWithDetect(t, g, set, agyInventory, func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, &fsClaudeAdapter{dir: sessions})
		c.HarnessPaths = map[string]string{"claude": os.Args[0], "codex": os.Args[0], "agy": agy}
		c.WorkerGoTmp = filepath.Join(dir, "gotmp")
	})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	return w, g, repo
}

// harnessModel is one fake harness and a model it runs.
type harnessModel struct{ harness, model string }

var (
	fakeOpenCode = harnessModel{"opencode", "fake/m1"}
	fakeClaude   = harnessModel{"claude", "sonnet"}
	fakeCodexHM  = harnessModel{"codex", "gpt-fake"}
	fakeAgy      = harnessModel{"agy", "gemini-3.8-flash-low"}
)

// keepWorking is a task every fake keeps working on until it is stopped:
// FAKE:slow holds the Codex turn open and FAKE:ignore keeps Claude quiet.
const keepWorking = "write half.txt and keep going FAKE:slow FAKE:ignore"

// endedWorkerWithWork starts a worker on from, leaves an uncommitted file in
// its worktree and stops it. It returns the start and the ended record.
func endedWorkerWithWork(t *testing.T, w *workerNode, from harnessModel) (protocol.WorkerStartResult, workerRow) {
	t.Helper()
	old := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: from.harness, Model: from.model, Task: keepWorking,
		Isolation: protocol.IsolationWorktree, AutoContinue: autoContinueOff()})
	if err := os.WriteFile(filepath.Join(old.Worktree, "half.txt"), []byte("half\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: old.SessionID})
	var row workerRow
	eventually(t, "the "+from.harness+" worker ended", func() bool {
		r, ok, err := w.d.st.workerBySession(old.SessionID)
		row = r
		return err == nil && ok && r.EndedReason != "" && w.d.workerCount() == 0
	})
	return old, row
}

// stopWorker stops a worker and waits until it has ended.
func stopWorker(t *testing.T, w *workerNode, session string) {
	t.Helper()
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: session})
	eventually(t, "the worker ended", func() bool { return w.d.workerCount() == 0 })
}

// A continue onto another harness keeps the worktree's work, the branch line,
// the delivery change, the task ID and the full first task, for each pair of
// harnesses. Each harness is once the old one and once the new one.
func TestContinueMovesAcrossHarnesses(t *testing.T) {
	w, _, _ := startMixedNode(t)
	for _, pair := range [][2]harnessModel{{fakeOpenCode, fakeCodexHM}, {fakeCodexHM, fakeClaude}, {fakeClaude, fakeAgy}, {fakeAgy, fakeOpenCode}} {
		from, to := pair[0], pair[1]
		t.Run(from.harness+"-to-"+to.harness, func(t *testing.T) {
			old, oldRow := endedWorkerWithWork(t, w, from)
			res, err := w.startDefault(protocol.WorkerStartRequest{Continue: old.SessionID, Harness: to.harness, Model: to.model})
			if err != nil {
				t.Fatalf("continue on %s: %v\ndaemon log:\n%s", to.harness, err, strings.Join(w.logs.lines(), "\n"))
			}
			if res.Harness != to.harness || res.SessionID == old.SessionID || res.Branch == old.Branch || res.Worktree == old.Worktree {
				t.Fatalf("continue on %s: %+v", to.harness, res)
			}
			if got := gitOutT(t, res.Worktree, "show", "HEAD:half.txt"); got != "half" {
				t.Errorf("the old worker's work is not in the new worktree: %q", got)
			}
			if res.Task == nil || !strings.Contains(res.Task.Body, keepWorking) {
				t.Errorf("the new worker lacks the first task: %+v", res.Task)
			}
			row, ok, err := w.d.st.workerBySession(res.SessionID)
			if err != nil || !ok {
				t.Fatalf("the new worker's record: %v %v", ok, err)
			}
			if row.Harness != to.harness || row.ChangeID != oldRow.ChangeID || row.TaskID != first(oldRow.TaskID, oldRow.SessionID) || row.TaskFull != oldRow.TaskFull {
				t.Errorf("record of the new worker: harness %q change %q task %q; old: change %q task %q",
					row.Harness, row.ChangeID, row.TaskID, oldRow.ChangeID, first(oldRow.TaskID, oldRow.SessionID))
			}
			stopWorker(t, w, res.SessionID)
		})
	}
}

// A new harness that fails to start does not lose the worker: the old record,
// its worktree and its work stay, and the next continue picks them up.
func TestFailedContinueOnAnotherHarnessKeepsTheWorker(t *testing.T) {
	w, _, repo := startMixedNode(t)
	old, _ := endedWorkerWithWork(t, w, fakeOpenCode)

	// The fake Claude, Codex and Pi exit at once; the fake OpenCode ran
	// before this was set.
	t.Setenv(envFakeStartFails, "1")
	if _, err := w.startDefault(protocol.WorkerStartRequest{Continue: old.SessionID, Harness: fakeClaude.harness, Model: fakeClaude.model}); err == nil || !strings.Contains(err.Error(), "Workspace not trusted") {
		t.Fatalf("continue on a Claude that cannot start: %v", err)
	}
	if r, ok, err := w.d.st.workerBySession(old.SessionID); err != nil || !ok || r.EndedReason == "" {
		t.Fatalf("the ended worker's record: %+v %v %v", r, ok, err)
	}
	if got := gitOutT(t, repo, "show", old.Branch+":half.txt"); got != "half" {
		t.Fatalf("the work is not on the old branch: %q", got)
	}

	t.Setenv(envFakeStartFails, "")
	res, err := w.startDefault(protocol.WorkerStartRequest{Continue: old.SessionID, Harness: fakeClaude.harness, Model: fakeClaude.model})
	if err != nil {
		t.Fatalf("the next continue: %v", err)
	}
	if res.Harness != fakeClaude.harness {
		t.Fatalf("the next continue: %+v", res)
	}
	if got := gitOutT(t, res.Worktree, "show", "HEAD:half.txt"); got != "half" {
		t.Errorf("the work is not in the new worktree: %q", got)
	}
	stopWorker(t, w, res.SessionID)
}
