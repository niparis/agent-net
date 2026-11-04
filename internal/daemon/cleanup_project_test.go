package daemon

import (
	"context"
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
