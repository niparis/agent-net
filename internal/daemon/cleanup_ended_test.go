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

	"github.com/niparis/agent-net/internal/protocol"
)

// endedFixture is a project with a bare origin, a daemon with a real state
// database and a fake gateway that answers query.projects and change.list.
// It runs the whole cleanup pass (cleanupProjects) the way the daemon does.
type endedFixture struct {
	t       *testing.T
	d       *Daemon
	repo    string
	peer    string
	state   string
	logs    *bytes.Buffer
	changes map[string][]protocol.ChangeView
}

func newEndedFixture(t *testing.T) *endedFixture {
	t.Helper()
	repo, _, peer := cleanupRemoteRepo(t, "project")
	state := seededStateDir(t)
	st, err := openState(state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	f := &endedFixture{t: t, repo: repo, peer: peer, state: state, logs: &bytes.Buffer{}, changes: map[string][]protocol.ChangeView{}}
	f.d = &Daemon{cfg: Config{StateDir: state, NodeID: "n_test"}, st: st, workers: map[string]*worker{}, log: log.New(f.logs, "", 0)}
	f.d.cfg.testGatewayCall = func(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
		switch typ {
		case "query.projects":
			raw, err := json.Marshal(protocol.ProjectsResult{Projects: []protocol.ProjectView{{Name: "project", NodeID: "n_test", Path: repo}}})
			return raw, true, err
		case protocol.FrameChangeList:
			q := params.(protocol.ChangeListRequest)
			raw, err := json.Marshal(protocol.ChangeListResult{Branch: q.Branch, Changes: f.changes[q.Branch]})
			return raw, true, err
		}
		return nil, false, nil
	}
	return f
}

// endWorker makes a worker worktree and records that its worker ended, through
// the same store call the daemon uses when a worker ends.
func (f *endedFixture) endWorker(id, harness string) string {
	f.t.Helper()
	dir := cleanupAddWorktree(f.t, f.repo, f.state, id)
	row := workerRow{ID: id, Harness: harness, Project: "project", Path: dir, MainPath: f.repo, Worktree: dir,
		Branch: "agent-net/" + id, StartedAt: time.Now(), LastActive: time.Now(), EndedReason: protocol.StopHandback}
	if err := f.d.st.saveWorker(row); err != nil {
		f.t.Fatal(err)
	}
	return dir
}

// pullRequestMerge lands the pushed branch on origin's default branch the way
// GitHub does, with a merge commit or a squash commit. The owner's local
// default branch is not updated, as on a machine nobody pulls on.
func (f *endedFixture) pullRequestMerge(branch string, squash bool) string {
	f.t.Helper()
	gitIn(f.t, f.repo, "push", "-q", "origin", branch)
	gitIn(f.t, f.peer, "fetch", "-q", "origin")
	if squash {
		gitIn(f.t, f.peer, "merge", "-q", "--squash", "origin/"+branch)
		gitIn(f.t, f.peer, "commit", "-q", "-m", "squash "+branch)
	} else {
		gitIn(f.t, f.peer, "merge", "-q", "--no-ff", "-m", "merge "+branch, "origin/"+branch)
	}
	gitIn(f.t, f.peer, "push", "-q", "origin", "main")
	return gitHead(f.t, f.peer, "HEAD")
}

func endedChange(id, state, merged, head string, updated time.Time) protocol.ChangeView {
	return protocol.ChangeView{Change: protocol.Change{ID: id, State: state, MergedCommit: merged, HeadCommit: head, UpdatedAt: updated.Unix()}}
}

func (f *endedFixture) clean() { f.d.cleanupProjects(context.Background()) }

func worktreeListed(t *testing.T, repo, dir string) bool {
	t.Helper()
	out, err := gitOut(context.Background(), repo, "worktree", "list", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(out, "worktree "+dir+"\n")
}

// A change that GitHub merged is done, but its merged commit is on origin's
// default branch, not on the local one. The folder must still go.
func TestCleanupChangeMergedOnRemoteOnly(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		squash      bool
	}{
		{"merge commit, merged", protocol.ChangeMerged, false},
		{"merge commit, done", protocol.ChangeDone, false},
		{"squash, done", protocol.ChangeDone, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEndedFixture(t)
			dir := f.endWorker("w_pr", "claude")
			head := cleanupCommit(t, dir, "change", "change")
			merged := f.pullRequestMerge("agent-net/w_pr", tc.squash)
			f.changes["agent-net/w_pr"] = []protocol.ChangeView{endedChange("ch_1", tc.state, merged, head, time.Now())}
			if _, err := gitOut(context.Background(), f.repo, "merge-base", "--is-ancestor", merged, "refs/heads/main"); err == nil {
				t.Fatal("local main already holds the merged commit")
			}
			f.clean()
			cleanupGone(t, dir, "agent-net/w_pr", f.repo)
		})
	}
}

// A merged change says nothing about commits the worker added after the merge.
func TestCleanupMergedChangeKeepsLaterCommits(t *testing.T) {
	f := newEndedFixture(t)
	dir := f.endWorker("w_later", "claude")
	head := cleanupCommit(t, dir, "change", "change")
	merged := f.pullRequestMerge("agent-net/w_later", true)
	cleanupCommit(t, dir, "after", "added after the merge")
	f.changes["agent-net/w_later"] = []protocol.ChangeView{endedChange("ch_1", protocol.ChangeDone, merged, head, time.Now())}
	f.clean()
	cleanupKept(t, dir, "agent-net/w_later", f.repo)
}

// A worker whose branch carried several changes (a closed one, then the one
// that merged) is not an unknown case: the newest closed change decides.
func TestCleanupSeveralClosedChangesOnOneBranch(t *testing.T) {
	now := time.Now()
	t.Run("closed then merged", func(t *testing.T) {
		f := newEndedFixture(t)
		dir := f.endWorker("w_multi", "codex")
		head := cleanupCommit(t, dir, "change", "change")
		merged := f.pullRequestMerge("agent-net/w_multi", false)
		f.changes["agent-net/w_multi"] = []protocol.ChangeView{
			endedChange("ch_old", protocol.ChangeStopped, "", head, now.Add(-48*time.Hour)),
			endedChange("ch_new", protocol.ChangeDone, merged, head, now),
		}
		f.clean()
		cleanupGone(t, dir, "agent-net/w_multi", f.repo)
	})
	t.Run("an open change keeps it", func(t *testing.T) {
		f := newEndedFixture(t)
		dir := f.endWorker("w_multi_open", "codex")
		head := cleanupCommit(t, dir, "change", "change")
		merged := f.pullRequestMerge("agent-net/w_multi_open", false)
		f.changes["agent-net/w_multi_open"] = []protocol.ChangeView{
			endedChange("ch_old", protocol.ChangeOpen, "", head, now.Add(-48*time.Hour)),
			endedChange("ch_new", protocol.ChangeDone, merged, head, now),
		}
		f.clean()
		cleanupKept(t, dir, "agent-net/w_multi_open", f.repo)
	})
}

// Files the worker-end commit never saves (a placeholder, a compiled binary)
// are not work. A text file it would have saved, or a tracked change, is.
func TestCleanupDirtyCheckIgnoresUnsavedLeftovers(t *testing.T) {
	elf := append([]byte("\x7fELF"), make([]byte, 64)...)
	for _, tc := range []struct {
		name  string
		write func(t *testing.T, dir string)
		want  bool // folder removed
	}{
		{"binary build output", func(t *testing.T, dir string) { writeFile(t, dir, "tool", elf) }, true},
		{"sandbox placeholder", func(t *testing.T, dir string) { writeFile(t, dir, ".bashrc", nil) }, true},
		{"sandbox placeholder in a subfolder", func(t *testing.T, dir string) {
			if err := os.MkdirAll(filepath.Join(dir, "sub", ".claude"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, "sub/.claude/agents", nil)
		}, true},
		{"untracked source file", func(t *testing.T, dir string) { writeFile(t, dir, "notes.go", []byte("package x\n")) }, false},
		{"tracked change", func(t *testing.T, dir string) { writeFile(t, dir, "README.md", []byte("edited\n")) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _, _ := cleanupRemoteRepo(t, "project")
			state := t.TempDir()
			dir := cleanupAddWorktree(t, repo, state, "w_dirty")
			tc.write(t, dir)
			cleanupRun(t, state, cleanupCandidate("w_dirty", repo, dir))
			_, err := os.Stat(dir)
			if removed := os.IsNotExist(err); removed != tc.want {
				t.Fatalf("removed=%v want %v (%v)", removed, tc.want, err)
			}
			if tc.want {
				cleanupGoneBranch(t, repo, "agent-net/w_dirty")
			}
		})
	}
}

func writeFile(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func cleanupGoneBranch(t *testing.T, repo, branch string) {
	t.Helper()
	if _, err := gitOut(context.Background(), repo, "show-ref", "--verify", "refs/heads/"+branch); err == nil {
		t.Fatalf("branch remains: %s", branch)
	}
}

// A worker with no change whose branch is on a remote or another branch keeps
// nothing in its folder that the branch does not keep. The folder goes after
// the three days a closed change also waits; the branch stays. Commits on no
// other ref wait 14 days, and then only the folder goes: the branch is the
// only copy.
func TestCleanupNoChangeFreesFolderKeepsBranch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pushed     bool
		age        time.Duration
		wantFolder bool // folder removed
	}{
		{"pushed, ended an hour ago", true, time.Hour, false},
		{"pushed, ended four days ago", true, 4 * 24 * time.Hour, true},
		{"unpushed, ended four days ago", false, 4 * 24 * time.Hour, false},
		{"unpushed, ended fifteen days ago", false, 15 * 24 * time.Hour, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _, _ := cleanupRemoteRepo(t, "project")
			state := t.TempDir()
			dir := cleanupAddWorktree(t, repo, state, "w_nochange")
			cleanupCommit(t, dir, "change", "change")
			if tc.pushed {
				gitIn(t, repo, "push", "-q", "origin", "agent-net/w_nochange")
			}
			w := cleanupCandidate("w_nochange", repo, dir)
			w.EndedAt = time.Now().Add(-tc.age)
			cleanupRun(t, state, w)
			_, err := os.Stat(dir)
			if removed := os.IsNotExist(err); removed != tc.wantFolder {
				t.Fatalf("folder removed=%v want %v (%v)", removed, tc.wantFolder, err)
			}
			if _, err := gitOut(context.Background(), repo, "show-ref", "--verify", "refs/heads/agent-net/w_nochange"); err != nil {
				t.Fatalf("branch with unique commits removed: %v", err)
			}
		})
	}
}

// For every harness: a worker ends, its change reaches done, and the next
// cleanup pass removes its worktree folder. A worker that ended with no change
// and no commits loses its folder on the first pass.
func TestCleanupEndedWorkerEveryHarness(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "opencode", "pi", "agy"} {
		t.Run(harness+" merged on GitHub", func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			f := newEndedFixture(t)
			id := "w_" + harness + "_done"
			dir := f.endWorker(id, harness)
			head := cleanupCommit(t, dir, "change", "change")
			merged := f.pullRequestMerge("agent-net/"+id, true)
			f.changes["agent-net/"+id] = []protocol.ChangeView{endedChange("ch_1", protocol.ChangeDone, merged, head, time.Now())}
			if !worktreeListed(t, f.repo, dir) {
				t.Fatal("setup: worktree not listed")
			}
			f.clean()
			cleanupGone(t, dir, "agent-net/"+id, f.repo)
			if worktreeListed(t, f.repo, dir) {
				t.Fatal("git worktree list still shows the folder")
			}
		})
		t.Run(harness+" no change, no commits", func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			f := newEndedFixture(t)
			id := "w_" + harness + "_empty"
			dir := f.endWorker(id, harness)
			f.clean()
			cleanupGone(t, dir, "agent-net/"+id, f.repo)
			if worktreeListed(t, f.repo, dir) {
				t.Fatal("git worktree list still shows the folder")
			}
		})
	}
}
