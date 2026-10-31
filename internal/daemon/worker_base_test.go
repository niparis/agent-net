package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// repoWithFeature makes a repository whose checked out branch is main and
// whose branch "agent-net/w_c2435587709e6f13" has a file that main lacks.
func repoWithFeature(t *testing.T) string {
	t.Helper()
	repo := newRepo(t, "shop")
	gitIn(t, repo, "checkout", "-q", "-b", "agent-net/w_c2435587709e6f13")
	if err := os.WriteFile(filepath.Join(repo, "feature"), []byte("feature"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "feature")
	gitIn(t, repo, "commit", "-q", "-m", "feature")
	gitIn(t, repo, "checkout", "-q", "main")
	return repo
}

// A Codex worker starts its worktree from the base branch.
func TestCodexWorkerStartsFromTheBaseBranch(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	t.Setenv(envFakeCodex, "1")
	t.Setenv("CODEX_HOME", t.TempDir())
	w := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = map[string]string{"codex": os.Args[0]}
	})
	repo := repoWithFeature(t)
	w.register(t, repo)
	res, err := w.startDefault(protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Task: "say ok", Base: "agent-net/w_c2435587709e6f13"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(res.Worktree, "feature")); err != nil {
		t.Fatalf("the worktree is not made from the base branch: %v", err)
	}
	rec := readCodex(t, res.Worktree, func(r fakeCodexRecord) bool { return r.Cwd != "" })
	if _, err := os.Stat(filepath.Join(rec.Cwd, "feature")); err != nil {
		t.Fatalf("codex ran in %s, which lacks the base branch: %v", rec.Cwd, err)
	}
	w.waitNotice(t, 1)
	eventually(t, "the worker ends", func() bool { return w.d.workerCount() == 0 })
}

// A start that waits in the queue keeps its base branch.
func TestQueuedStartKeepsItsBaseBranch(t *testing.T) {
	for _, harness := range []string{"codex", "opencode"} {
		t.Run(harness, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			t.Setenv(envFakeCodex, "1")
			t.Setenv("CODEX_HOME", t.TempDir())
			fl := &fakeLoad{l: cpuBusy()}
			w := startWorkerNode(t, g, queueSet, func(c *Config) {
				c.HarnessPaths = map[string]string{"codex": os.Args[0]}
				c.LoadProbe = fl.get
			})
			repo := repoWithFeature(t)
			w.register(t, repo)
			pi, _ := helloPi(t, w.node, "caller", "/work/caller", true)
			r := startFrom(t, pi, protocol.WorkerStartRequest{Project: "shop", Harness: harness, Task: "say ok",
				Isolation: protocol.IsolationWorktree, Base: "agent-net/w_c2435587709e6f13"})
			if r.Status != "queued" {
				t.Fatalf("start: %+v", r)
			}
			fl.set(roomyLoad())
			eventually(t, "the queued worker has a worktree from the base", func() bool {
				for _, wt := range gitWorktrees(t, repo) {
					if wt == repo {
						continue
					}
					if _, err := os.Stat(filepath.Join(wt, "feature")); err == nil {
						return true
					}
				}
				return false
			})
		})
	}
}

func gitWorktrees(t *testing.T, repo string) []string {
	t.Helper()
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repo
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			out = append(out, p)
		}
	}
	return out
}

func TestWorkerStopsBeforeRepoCleanup(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	t.Setenv(envFakeCodex, "1")
	t.Setenv("CODEX_HOME", t.TempDir())
	w := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = map[string]string{"codex": os.Args[0]}
	})
	repo := newRepo(t, "shop")
	var exited <-chan struct{}
	t.Cleanup(func() {
		if exited == nil {
			return
		}
		select {
		case <-exited:
		default:
			t.Error("worker was not reaped before repository cleanup")
		}
	})
	w.register(t, repo)
	_, err := w.startDefault(protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Task: "say ok"})
	if err != nil {
		t.Fatal(err)
	}
	w.d.wmu.Lock()
	for _, worker := range w.d.workers {
		exited = worker.exited
	}
	w.d.wmu.Unlock()
	if exited == nil {
		t.Fatal("worker has no exit channel")
	}
}
