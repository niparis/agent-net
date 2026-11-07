package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestCleanupWorktreeMergedRunningAndUnique(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	state := t.TempDir()
	dir := filepath.Join(state, "worktrees", "w_old")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_old", dir)
	d := &Daemon{cfg: Config{StateDir: state}, workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: "w_old", Project: project, Path: dir, Branch: "agent-net/w_old", EndedAt: time.Now()}
	d.workers["w_old"] = &worker{id: "w_old"}
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("running removed: %v", err)
	}
	delete(d.workers, "w_old")
	if err := os.WriteFile(filepath.Join(dir, "unique"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "unique")
	gitIn(t, dir, "commit", "-q", "-m", "unique")
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("unique removed: %v", err)
	}
	gitIn(t, project, "merge", "-q", "--ff-only", "agent-net/w_old")
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("merged remains: %v", err)
	}
	if _, err := gitOut(context.Background(), project, "show-ref", "--verify", "refs/heads/agent-net/w_old"); err == nil {
		t.Fatal("merged branch remains")
	}
}

func TestCleanupWorktreeEndTimePersists(t *testing.T) {
	t.Parallel()
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	row := workerRow{ID: "w_example", Harness: "test", Project: "test", Path: "/project", MainPath: "/project", Worktree: "/state/worktrees/w_example", Branch: "agent-net/w_example", EndedReason: "stopped", StartedAt: time.Now(), LastActive: time.Now()}
	if err := st.saveWorker(row); err != nil {
		t.Fatal(err)
	}
	if err := st.deleteWorker(row.ID); err != nil {
		t.Fatal(err)
	}
	entries, err := st.cleanupWorktrees("/project")
	if err != nil || len(entries) != 1 || entries[0].EndedAt.IsZero() {
		t.Fatalf("ended worktree not preserved: %+v %v", entries, err)
	}
}

func TestCleanupWorktreeObservesLegacy(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "worktrees", "w_legacy")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_legacy", dir)
	st, err := openState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d := &Daemon{cfg: Config{StateDir: stateDir}, st: st, workers: map[string]*worker{}}
	now := time.Now()
	if err := d.observeLegacyWorktrees(context.Background(), project, now); err != nil {
		t.Fatal(err)
	}
	entries, err := st.cleanupWorktrees(project)
	if err != nil || len(entries) != 1 || entries[0].ID != "w_legacy" || entries[0].EndedAt.Before(now) {
		t.Fatalf("legacy: %+v %v", entries, err)
	}
	if err := d.cleanupWorktree(context.Background(), entries[0], now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("merged legacy worktree remains: %v", err)
	}
}

func TestCleanupWorktreeKeepsQueuedBranch(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "worktrees", "w_old")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_old", dir)
	st, err := openState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	if err := st.addQueued(queuedStart{ID: "q_test", Owner: "owner", Project: "project", Path: project, Harness: "test", Base: "agent-net/w_old", QueuedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{StateDir: stateDir}, st: st, workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: "w_old", Project: project, Path: dir, Branch: "agent-net/w_old", EndedAt: time.Now().Add(-25 * time.Hour)}
	if _, err := st.db.Exec(`INSERT INTO cleanup_worktrees(worker_id,project,path,branch,ended_at) VALUES(?,?,?,?,?)`, candidate.ID, candidate.Project, candidate.Path, candidate.Branch, candidate.EndedAt.UnixNano()); err != nil {
		t.Fatal(err)
	}
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("queued worktree removed: %v", err)
	}
}

func TestCleanupWorktreeKeepsPersistedRunning(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "worktrees", "w_running")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_running", dir)
	st, err := openState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	row := workerRow{ID: "w_running", Harness: "test", Project: "project", Path: dir, MainPath: project, Worktree: dir, Branch: "agent-net/w_running", StartedAt: time.Now(), LastActive: time.Now()}
	if err := st.saveWorker(row); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{StateDir: stateDir}, st: st, workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: row.ID, Project: project, Path: dir, Branch: row.Branch, EndedAt: time.Now().Add(-25 * time.Hour)}
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("running worktree removed: %v", err)
	}
}

func TestCleanupWorktreeRemovesLedger(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "worktrees", "w_done")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_done", dir)
	st, err := openState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	row := workerRow{ID: "w_done", Harness: "test", Project: "project", Path: dir, MainPath: project, Worktree: dir, Branch: "agent-net/w_done", StartedAt: time.Now(), LastActive: time.Now(), EndedReason: "stopped"}
	if err := st.saveWorker(row); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-25 * time.Hour)
	if _, err := st.db.Exec(`UPDATE cleanup_worktrees SET ended_at=? WHERE worker_id=?`, old.UnixNano(), row.ID); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{StateDir: stateDir}, st: st, workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: row.ID, Project: project, Path: dir, Branch: row.Branch, EndedAt: old}
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	entries, err := st.cleanupWorktrees(project)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cleanup ledger remains: %+v %v", entries, err)
	}
}

func TestCleanupWorktreeKeepsCommitOnlyOnAnotherBranch(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "worktrees", "w_other")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_other", dir)
	if err := os.WriteFile(filepath.Join(dir, "change"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "change")
	gitIn(t, dir, "commit", "-q", "-m", "change")
	gitIn(t, project, "branch", "keep-this", "agent-net/w_other")
	d := &Daemon{cfg: Config{StateDir: stateDir}, workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: "w_other", Project: project, Path: dir, Branch: "agent-net/w_other", EndedAt: time.Now().Add(-25 * time.Hour)}
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("unmerged worktree removed: %v", err)
	}
	if _, err := gitOut(context.Background(), project, "show-ref", "--verify", "refs/heads/keep-this"); err != nil {
		t.Fatalf("kept branch missing: %v", err)
	}
}

func TestCleanupWorktreeKeepsDifferentCheckedOutBranch(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "worktrees", "w_changed")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_changed", dir)
	gitIn(t, dir, "checkout", "-q", "-b", "different")
	d := &Daemon{cfg: Config{StateDir: stateDir}, workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: "w_changed", Project: project, Path: dir, Branch: "agent-net/w_changed", EndedAt: time.Now().Add(-25 * time.Hour)}
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("worktree on different branch removed: %v", err)
	}
}

func TestCleanupWorktreeBranchWhenWorktreeAlreadyGone(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "worktrees", "w_gone")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_gone", dir)
	gitIn(t, project, "worktree", "remove", dir)
	d := &Daemon{cfg: Config{StateDir: stateDir}, workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: "w_gone", Project: project, Path: dir, Branch: "agent-net/w_gone", EndedAt: time.Now().Add(-25 * time.Hour)}
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOut(context.Background(), project, "show-ref", "--verify", "refs/heads/agent-net/w_gone"); err == nil {
		t.Fatal("merged branch remains")
	}
}

func TestCleanupWorktreeForgetsAlreadyRemoved(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "worktrees", "w_removed")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_removed", dir)
	st, err := openState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	row := workerRow{ID: "w_removed", Harness: "test", Project: "project", Path: dir, MainPath: project, Worktree: dir, Branch: "agent-net/w_removed", StartedAt: time.Now(), LastActive: time.Now(), EndedReason: "stopped"}
	if err := st.saveWorker(row); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-25 * time.Hour)
	if _, err := st.db.Exec(`UPDATE cleanup_worktrees SET ended_at=? WHERE worker_id=?`, old.UnixNano(), row.ID); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "remove", dir)
	gitIn(t, project, "branch", "-D", "agent-net/w_removed")
	d := &Daemon{cfg: Config{StateDir: stateDir}, st: st, workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: row.ID, Project: project, Path: dir, Branch: row.Branch, EndedAt: old}
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	entries, err := st.cleanupWorktrees(project)
	if err != nil || len(entries) != 0 {
		t.Fatalf("ledger: %+v %v", entries, err)
	}
}

// A cleanup line in a project's worker.md is no longer honoured: the daemon
// only removes its own worker worktrees.
func TestCleanupIgnoresDeclaredFolders(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	project := newRepo(t, "project")
	w.register(t, project)
	if err := os.MkdirAll(filepath.Join(project, ".agent-net"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".agent-net", "worker.md"), []byte("cleanup: build 2h\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(project, "build")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "old")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	w.d.cleanupProjects(context.Background())
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("declared folder trimmed: %v", err)
	}
}

func TestCleanupWorktreeKeepsRecentlyRestarted(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "worktrees", "w_recent")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_recent", dir)
	st, err := openState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	row := workerRow{ID: "w_recent", Harness: "test", Project: "project", Path: dir, MainPath: project, Worktree: dir, Branch: "agent-net/w_recent", StartedAt: time.Now(), LastActive: time.Now(), EndedReason: "stopped"}
	if err := st.saveWorker(row); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{StateDir: stateDir}, st: st, workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: row.ID, Project: project, Path: dir, Branch: row.Branch, EndedAt: time.Now().Add(-25 * time.Hour)}
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("recently ended worktree removed: %v", err)
	}
}

func TestCleanupWorktreeRemovesIgnoredBuildFiles(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "worktrees", "w_build")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_build", dir)
	if err := os.WriteFile(filepath.Join(dir, "fake-opencode.json"), []byte("build data"), 0600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{StateDir: stateDir}, workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: "w_build", Project: project, Path: dir, Branch: "agent-net/w_build", EndedAt: time.Now().Add(-25 * time.Hour)}
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("worktree with ignored build data remains: %v", err)
	}
}

func TestCleanupWorktreeKeepsStartingBase(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "worktrees", "w_base")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_base", dir)
	d := &Daemon{cfg: Config{StateDir: stateDir}, workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: "w_base", Project: project, Path: dir, Branch: "agent-net/w_base", EndedAt: time.Now().Add(-25 * time.Hour)}
	d.reserveWorkerBase(candidate.Branch)
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("starting base removed: %v", err)
	}
	d.releaseWorkerBase(candidate.Branch)
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("released base remains: %v", err)
	}
}

func TestCleanupStartingBranchReservedByAdmission(t *testing.T) {
	t.Parallel()
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d := &Daemon{st: st, cfg: Config{Workers: WorkerSettings{StartGap: -1}, LoadProbe: func() MachineLoad { return roomyLoad() }}, workers: map[string]*worker{}}
	branch := "agent-net/w_base"
	if result := d.admitOrQueue(protocol.WorkerLaunch{Base: branch}); result != nil {
		t.Fatalf("start did not admit: %+v", result)
	}
	if d.startingBases[branch] != 1 {
		t.Fatal("admitted base not reserved")
	}
	d.releaseWorkerBase(branch)
	if d.startingBases[branch] != 0 {
		t.Fatal("base reservation not released")
	}
}

func TestCleanupWorktreeKeepsBranchCheckedOutElsewhere(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "worktrees", "w_shared")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_shared", dir)
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, project, "worktree", "add", "-q", "--force", other, "agent-net/w_shared")
	d := &Daemon{cfg: Config{StateDir: stateDir}, workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: "w_shared", Project: project, Path: dir, Branch: "agent-net/w_shared", EndedAt: time.Now().Add(-25 * time.Hour)}
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("shared branch worktree removed: %v", err)
	}
}

func TestCleanupWorktreeRemovesClaudeTrust(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	project := newRepo(t, "project")
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "worktrees", "w_claude")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_claude", dir)
	if err := claudetrust.Set(dir); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{StateDir: stateDir}, workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: "w_claude", Project: project, Path: dir, Branch: "agent-net/w_claude", Harness: "claude", EndedAt: time.Now().Add(-25 * time.Hour)}
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	trusted, err := claudetrust.Trusted(dir)
	if err != nil || trusted {
		t.Fatalf("trust remains: %v %v", trusted, err)
	}
}

func TestCleanupRegisteredProjectRemovesEndedWorktree(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	project := newRepo(t, "project")
	w.register(t, project)
	dir := filepath.Join(w.cfg.StateDir, "worktrees", "w_ended")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_ended", dir)
	row := workerRow{ID: "w_ended", Harness: "test", Project: "project", Path: dir, MainPath: project, Worktree: dir, Branch: "agent-net/w_ended", StartedAt: time.Now(), LastActive: time.Now(), EndedReason: "stopped"}
	if err := w.d.st.saveWorker(row); err != nil {
		t.Fatal(err)
	}
	w.d.cleanupProjects(context.Background())
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("ended worktree remains: %v", err)
	}
	if _, err := gitOut(context.Background(), project, "show-ref", "--verify", "refs/heads/agent-net/w_ended"); err == nil {
		t.Fatal("merged branch remains")
	}
}

func TestCleanupWorktreeUsesConfiguredDefaultBranch(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "worktrees", "w_default")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "branch", "trunk")
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_default", dir)
	if err := os.WriteFile(filepath.Join(dir, "change"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "change")
	gitIn(t, dir, "commit", "-q", "-m", "change")
	gitIn(t, project, "merge", "-q", "--ff-only", "agent-net/w_default")
	d := &Daemon{cfg: Config{StateDir: stateDir}, workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: "w_default", Project: project, Path: dir, Branch: "agent-net/w_default", DefaultBranch: "trunk", EndedAt: time.Now()}
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("worktree not merged into default removed: %v", err)
	}
	gitIn(t, project, "branch", "-f", "trunk", "agent-net/w_default")
	if err := d.cleanupWorktree(context.Background(), candidate, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("worktree merged into default remains: %v", err)
	}
}

// A project that agent-net only saw (a session works there, nobody registered
// it) still gets its ended worker worktrees cleaned: they are the daemon's own.
func TestCleanupSeenProjectRemovesEndedWorktree(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	project := newRepo(t, "project")
	helloPi(t, w.node, "p1", project, false)
	eventually(t, "project seen", func() bool {
		raw, err := w.d.call(context.Background(), "query.projects", struct{}{})
		return err == nil && strings.Contains(string(raw), `"source":"seen"`)
	})
	dir := filepath.Join(w.cfg.StateDir, "worktrees", "w_seen")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_seen", dir)
	row := workerRow{ID: "w_seen", Harness: "test", Project: "project", Path: dir, MainPath: project, Worktree: dir, Branch: "agent-net/w_seen", StartedAt: time.Now(), LastActive: time.Now(), EndedReason: "stopped"}
	if err := w.d.st.saveWorker(row); err != nil {
		t.Fatal(err)
	}
	w.d.cleanupProjects(context.Background())
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("ended worktree in a seen project remains: %v", err)
	}
}

func TestCleanupWorktreeFindsPreviousCustomPrefix(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	state := t.TempDir()
	dir := filepath.Join(state, "worktrees", "w_old")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "old-prefix/w_old", dir)
	st, err := openState(state)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d := &Daemon{cfg: Config{StateDir: state}, st: st, workers: map[string]*worker{}}
	if err = d.observeLegacyWorktrees(context.Background(), project, time.Now()); err != nil {
		t.Fatal(err)
	}
	entries, err := st.cleanupWorktrees(project)
	if err != nil || len(entries) != 1 || entries[0].Branch != "old-prefix/w_old" {
		t.Fatalf("recorded: %+v %v", entries, err)
	}
	if err = d.cleanupWorktree(context.Background(), entries[0], time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("merged worktree remains: %v", err)
	}
}

func TestCleanupBranchValidatesRecordedPrefix(t *testing.T) {
	for _, tc := range []struct {
		branch string
		want   bool
	}{
		{"agent-net/w_1", true}, {"old/team/w_1", true}, {"-invalid/w_1", false}, {"old/../w_1", false}, {"old/w_2", false}, {"old/w_1/extra", false},
	} {
		if got := cleanupBranch(tc.branch, "w_1"); got != tc.want {
			t.Errorf("%q: %v", tc.branch, got)
		}
	}
}

func TestCleanupWorktreeDeliveryRetention(t *testing.T) {
	project := newRepo(t, "project")
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "worktrees", "w_delivery")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_delivery", dir)
	if err := os.WriteFile(filepath.Join(dir, "unique"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "unique")
	gitIn(t, dir, "commit", "-q", "-m", "unique")
	st, err := openState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	now := time.Now()
	w := cleanupWorktree{ID: "w_delivery", Project: project, Path: dir, Branch: "agent-net/w_delivery", EndedAt: now.Add(-4 * 24 * time.Hour), ChangeState: protocol.ChangeStopped, ChangeEndedAt: now.Add(-4 * 24 * time.Hour)}
	if _, err = st.db.Exec(`INSERT INTO cleanup_worktrees(worker_id,project,path,branch,ended_at) VALUES(?,?,?,?,?)`, w.ID, w.Project, w.Path, w.Branch, w.EndedAt.UnixNano()); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{StateDir: stateDir}, st: st, workers: map[string]*worker{}}
	if err := d.cleanupWorktree(context.Background(), w, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("closed worktree remains: %v", err)
	}
	if _, err := gitOut(context.Background(), project, "show-ref", "--verify", "refs/heads/"+w.Branch); err != nil {
		t.Fatalf("branch not retained: %v", err)
	}
	rows, err := st.cleanupWorktrees(project)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ledger: %+v %v", rows, err)
	}
	rows[0].ChangeState, rows[0].ChangeEndedAt = w.ChangeState, w.ChangeEndedAt
	if err := d.cleanupWorktree(context.Background(), rows[0], now.Add(26*24*time.Hour-time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOut(context.Background(), project, "show-ref", "--verify", "refs/heads/"+w.Branch); err != nil {
		t.Fatalf("branch deleted before deadline: %v", err)
	}
	if err := d.cleanupWorktree(context.Background(), rows[0], now.Add(26*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOut(context.Background(), project, "show-ref", "--verify", "refs/heads/"+w.Branch); err == nil {
		t.Fatal("expired branch remains")
	}
}

func TestCleanupWorktreeDeliveryRules(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, tc := range []struct {
		name, state                        string
		age                                time.Duration
		checked, dirty, merged, wantRemove bool
	}{
		{"closed before three days", protocol.ChangeStopped, 3 * 24 * time.Hour, true, false, false, false},
		{"closed after three days", protocol.ChangeStopped, 3*24*time.Hour + time.Second, true, false, false, true},
		{"orphan before fourteen days", "", 14 * 24 * time.Hour, true, false, false, false},
		{"orphan after fourteen days", "", 14*24*time.Hour + time.Second, true, false, false, true},
		{"unknown gateway", "", 20 * 24 * time.Hour, false, false, false, false},
		{"merged commit on default", protocol.ChangeMerged, time.Hour, true, false, true, true},
		{"merged commit absent", protocol.ChangeDone, time.Hour, true, false, false, false},
		{"dirty closed", protocol.ChangeStopped, 4 * 24 * time.Hour, true, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := newRepo(t, "project")
			stateDir := t.TempDir()
			dir := filepath.Join(stateDir, "worktrees", "w_rules")
			if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
				t.Fatal(err)
			}
			gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_rules", dir)
			defaultCommit, err := gitOut(context.Background(), project, "rev-parse", "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "unique"), []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
			gitIn(t, dir, "add", "unique")
			gitIn(t, dir, "commit", "-q", "-m", "unique")
			if tc.dirty {
				if err := os.WriteFile(filepath.Join(dir, "dirty"), []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			w := cleanupWorktree{ID: "w_rules", Project: project, Path: dir, Branch: "agent-net/w_rules", EndedAt: now.Add(-tc.age), ChangeState: tc.state, ChangeEndedAt: now.Add(-tc.age), ChangeChecked: tc.checked}
			if tc.merged {
				w.MergedCommit = defaultCommit
			} else if tc.state == protocol.ChangeDone {
				w.MergedCommit, err = gitOut(context.Background(), dir, "rev-parse", "HEAD")
				if err != nil {
					t.Fatal(err)
				}
			}
			d := &Daemon{cfg: Config{StateDir: stateDir}, workers: map[string]*worker{}}
			ctx := context.Background()
			w.DefaultBranch = cleanupDefaultBranch(ctx, project, w.DefaultBranch)
			if cleanupNeedsRemoteProof(ctx, &w) {
				w.RemoteDefaultRef = cleanupFetchDefault(ctx, project, w.DefaultBranch)
			}
			if err := cleanupPrepareProof(ctx, &w); err != nil {
				t.Fatal(err)
			}
			if err := d.cleanupWorktree(ctx, w, now); err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(dir)
			if removed := os.IsNotExist(err); removed != tc.wantRemove {
				t.Fatalf("removed=%v, error=%v", removed, err)
			}
		})
	}
}

func TestCleanupChangeRejectsOldGateway(t *testing.T) {
	d := &Daemon{cfg: Config{testGatewayCall: func(context.Context, string, any) (json.RawMessage, bool, error) {
		return json.RawMessage(`{"changes":[]}`), true, nil
	}}}
	w := cleanupWorktree{Branch: "agent-net/w_old"}
	if err := d.cleanupChange(context.Background(), "project", &w); err == nil {
		t.Fatal("old gateway accepted as branch lookup")
	}
}

func TestCleanupWorktreeSquash(t *testing.T) {
	project := newRepo(t, "project")
	state := t.TempDir()
	dir := filepath.Join(state, "worktrees", "w_squash")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_squash", dir)
	if err := os.WriteFile(filepath.Join(dir, "change"), []byte("change"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "change")
	gitIn(t, dir, "commit", "-q", "-m", "change")
	gitIn(t, project, "merge", "--squash", "agent-net/w_squash")
	gitIn(t, project, "commit", "-q", "-m", "squash")
	merged := gitHead(t, project, "HEAD")
	now := time.Now()
	d := &Daemon{cfg: Config{StateDir: state}, workers: map[string]*worker{}}
	row := cleanupWorktree{ID: "w_squash", Project: project, Path: dir, Branch: "agent-net/w_squash", DefaultBranch: "main",
		EndedAt: now.Add(-4 * 24 * time.Hour), ChangeState: protocol.ChangeDone, ChangeEndedAt: now.Add(-31 * 24 * time.Hour), MergedCommit: merged}
	if err := d.cleanupWorktree(context.Background(), row, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("squash worktree remains: %v", err)
	}
	if _, err := gitOut(context.Background(), project, "show-ref", "--verify", "refs/heads/agent-net/w_squash"); err == nil {
		t.Fatal("squash branch remains")
	}
}

func TestCleanupWorktreeStaleInitializingLockRemoved(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	state := t.TempDir()
	dir := filepath.Join(state, "worktrees", "w_stale_init")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_stale_init", dir)
	gitIn(t, project, "worktree", "lock", "--reason", "initializing", dir)
	lockFile := filepath.Join(project, ".git", "worktrees", "w_stale_init", "locked")
	if _, err := os.Stat(lockFile); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
	now := time.Now()
	stale := now.Add(-2 * time.Hour)
	if err := os.Chtimes(lockFile, stale, stale); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "merge", "-q", "--ff-only", "agent-net/w_stale_init")

	var logs bytes.Buffer
	d := &Daemon{cfg: Config{StateDir: state}, log: log.New(&logs, "", 0), workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: "w_stale_init", Project: project, Path: dir, Branch: "agent-net/w_stale_init", EndedAt: now.Add(-25 * time.Hour)}
	if err := d.cleanupWorktree(context.Background(), candidate, now); err != nil {
		t.Fatalf("stale initializing lock cleanup failed: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("worktree directory remains: %v", err)
	}
	if _, err := gitOut(context.Background(), project, "show-ref", "--verify", "refs/heads/agent-net/w_stale_init"); err == nil {
		t.Fatal("merged branch remains")
	}
	if !strings.Contains(logs.String(), "unlocked_stale=initializing") {
		t.Fatalf("log does not report unlocked_stale=initializing: %s", logs.String())
	}
}

func TestCleanupWorktreeFreshInitializingLockKept(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	state := t.TempDir()
	dir := filepath.Join(state, "worktrees", "w_fresh_init")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_fresh_init", dir)
	gitIn(t, project, "worktree", "lock", "--reason", "initializing", dir)
	lockFile := filepath.Join(project, ".git", "worktrees", "w_fresh_init", "locked")
	if _, err := os.Stat(lockFile); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
	now := time.Now()
	fresh := now.Add(-10 * time.Minute)
	if err := os.Chtimes(lockFile, fresh, fresh); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "merge", "-q", "--ff-only", "agent-net/w_fresh_init")

	var logs bytes.Buffer
	d := &Daemon{cfg: Config{StateDir: state}, log: log.New(&logs, "", 0), workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: "w_fresh_init", Project: project, Path: dir, Branch: "agent-net/w_fresh_init", EndedAt: now.Add(-25 * time.Hour)}
	if err := d.cleanupWorktree(context.Background(), candidate, now); err == nil {
		t.Fatal("expected error on fresh locked worktree, got nil")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("fresh locked worktree removed: %v", err)
	}
	if strings.Contains(logs.String(), "unlocked_stale=initializing") {
		t.Fatalf("fresh lock should not be unlocked: %s", logs.String())
	}
}

func TestCleanupWorktreeOtherLockReasonKept(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	state := t.TempDir()
	dir := filepath.Join(state, "worktrees", "w_other_lock")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_other_lock", dir)
	gitIn(t, project, "worktree", "lock", "--reason", "owner hold", dir)
	lockFile := filepath.Join(project, ".git", "worktrees", "w_other_lock", "locked")
	if _, err := os.Stat(lockFile); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
	now := time.Now()
	stale := now.Add(-2 * time.Hour)
	if err := os.Chtimes(lockFile, stale, stale); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "merge", "-q", "--ff-only", "agent-net/w_other_lock")

	var logs bytes.Buffer
	d := &Daemon{cfg: Config{StateDir: state}, log: log.New(&logs, "", 0), workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: "w_other_lock", Project: project, Path: dir, Branch: "agent-net/w_other_lock", EndedAt: now.Add(-25 * time.Hour)}
	if err := d.cleanupWorktree(context.Background(), candidate, now); err == nil {
		t.Fatal("expected error on worktree locked with other reason, got nil")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("worktree with other lock reason removed: %v", err)
	}
	if strings.Contains(logs.String(), "unlocked_stale=initializing") {
		t.Fatalf("other lock reason should not be unlocked: %s", logs.String())
	}
}

func TestCleanupWorktreeDirtyLockedKept(t *testing.T) {
	t.Parallel()
	project := newRepo(t, "project")
	state := t.TempDir()
	dir := filepath.Join(state, "worktrees", "w_dirty_locked")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_dirty_locked", dir)
	gitIn(t, project, "worktree", "lock", "--reason", "initializing", dir)
	lockFile := filepath.Join(project, ".git", "worktrees", "w_dirty_locked", "locked")
	if _, err := os.Stat(lockFile); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
	now := time.Now()
	stale := now.Add(-2 * time.Hour)
	if err := os.Chtimes(lockFile, stale, stale); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "merge", "-q", "--ff-only", "agent-net/w_dirty_locked")
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("dirty content"), 0600); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	d := &Daemon{cfg: Config{StateDir: state}, log: log.New(&logs, "", 0), workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: "w_dirty_locked", Project: project, Path: dir, Branch: "agent-net/w_dirty_locked", EndedAt: now.Add(-25 * time.Hour)}
	if err := d.cleanupWorktree(context.Background(), candidate, now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("dirty locked worktree removed: %v", err)
	}
	if strings.Contains(logs.String(), "unlocked_stale=initializing") {
		t.Fatalf("dirty worktree must not be unlocked: %s", logs.String())
	}
}

func TestCleanupProjectsSkipsMissingAndNonGitAndRateLimits(t *testing.T) {
	tmp := t.TempDir()
	missingPath := filepath.Join(tmp, "missing_proj")
	nonGitPath := filepath.Join(tmp, "nongit_proj")
	if err := os.MkdirAll(nonGitPath, 0700); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	clock := time.Now()
	nodeID := "node-1"

	projectsResult := protocol.ProjectsResult{
		Projects: []protocol.ProjectView{
			{NodeID: nodeID, Name: "missing", Path: missingPath},
			{NodeID: nodeID, Name: "nongit", Path: nonGitPath},
		},
	}
	rawProjects, _ := json.Marshal(projectsResult)

	d := &Daemon{
		cfg: Config{
			NodeID:   nodeID,
			StateDir: tmp,
			testGatewayCall: func(_ context.Context, typ string, _ any) (json.RawMessage, bool, error) {
				if typ == "query.projects" {
					return rawProjects, true, nil
				}
				return nil, false, nil
			},
		},
		log:     log.New(&logs, "", 0),
		workers: map[string]*worker{},
		testCleanupNow: func() time.Time {
			return clock
		},
	}

	// Pass 1: should log once for missing and once for nongit.
	d.cleanupProjects(context.Background())
	out1 := logs.String()
	if !strings.Contains(out1, `project="`+missingPath+`"`) {
		t.Fatalf("pass 1 missingPath not logged: %s", out1)
	}
	if !strings.Contains(out1, `project="`+nonGitPath+`"`) {
		t.Fatalf("pass 1 nonGitPath not logged: %s", out1)
	}

	// Pass 2: 1 hour later (within 24h), should NOT log again.
	logs.Reset()
	clock = clock.Add(1 * time.Hour)
	d.cleanupProjects(context.Background())
	out2 := logs.String()
	if strings.Contains(out2, `project="`+missingPath+`"`) {
		t.Fatalf("pass 2 unexpectedly logged missingPath again within 24h: %s", out2)
	}
	if strings.Contains(out2, `project="`+nonGitPath+`"`) {
		t.Fatalf("pass 2 unexpectedly logged nonGitPath again within 24h: %s", out2)
	}

	// Pass 3: 25 hours after pass 1 (exceeds 24h), should log again.
	logs.Reset()
	clock = clock.Add(24 * time.Hour)
	d.cleanupProjects(context.Background())
	out3 := logs.String()
	if !strings.Contains(out3, `project="`+missingPath+`"`) {
		t.Fatalf("pass 3 missingPath should log after 24h: %s", out3)
	}
	if !strings.Contains(out3, `project="`+nonGitPath+`"`) {
		t.Fatalf("pass 3 nonGitPath should log after 24h: %s", out3)
	}
}

func TestCleanupWorktreeErrorRateLimited(t *testing.T) {
	project := newRepo(t, "project")
	state := t.TempDir()
	dir := filepath.Join(state, "worktrees", "w_err_ratelimit")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_err_ratelimit", dir)
	gitIn(t, project, "worktree", "lock", "--reason", "owner hold", dir)
	gitIn(t, project, "merge", "-q", "--ff-only", "agent-net/w_err_ratelimit")

	st, err := openState(state)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()

	clock := time.Now()
	ended := clock.Add(-25 * time.Hour)
	if _, err := st.db.Exec(`INSERT INTO cleanup_worktrees(worker_id,project,path,branch,ended_at) VALUES(?,?,?,?,?)`, "w_err_ratelimit", project, dir, "agent-net/w_err_ratelimit", ended.UnixNano()); err != nil {
		t.Fatal(err)
	}

	nodeID := "node-1"
	projectsResult := protocol.ProjectsResult{
		Projects: []protocol.ProjectView{
			{NodeID: nodeID, Name: "project", Path: project},
		},
	}
	rawProjects, _ := json.Marshal(projectsResult)

	var logs bytes.Buffer
	d := &Daemon{
		cfg: Config{
			NodeID:   nodeID,
			StateDir: state,
			testGatewayCall: func(_ context.Context, typ string, _ any) (json.RawMessage, bool, error) {
				if typ == "query.projects" {
					return rawProjects, true, nil
				}
				if typ == protocol.FrameChangeList {
					return json.RawMessage(`{"branch":"agent-net/w_err_ratelimit","changes":[]}`), true, nil
				}
				return nil, false, nil
			},
		},
		st:      st,
		log:     log.New(&logs, "", 0),
		workers: map[string]*worker{},
		testCleanupNow: func() time.Time {
			return clock
		},
	}

	// Pass 1: should log error=
	d.cleanupProjects(context.Background())
	out1 := logs.String()
	if !strings.Contains(out1, `worker.worktree_cleanup path="`+dir+`" error=`) {
		t.Fatalf("pass 1 expected error log for worktree: %s", out1)
	}

	// Pass 2: 2 minutes later (same error, within 24h), should NOT log error=
	logs.Reset()
	clock = clock.Add(2 * time.Minute)
	d.cleanupProjects(context.Background())
	out2 := logs.String()
	if strings.Contains(out2, `worker.worktree_cleanup path="`+dir+`" error=`) {
		t.Fatalf("pass 2 unexpectedly logged worktree error within 24h: %s", out2)
	}

	// Pass 3: 25 hours later, should log error= again
	logs.Reset()
	clock = clock.Add(25 * time.Hour)
	d.cleanupProjects(context.Background())
	out3 := logs.String()
	if !strings.Contains(out3, `worker.worktree_cleanup path="`+dir+`" error=`) {
		t.Fatalf("pass 3 expected error log for worktree after 24h: %s", out3)
	}
}
