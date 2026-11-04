package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// Decision 8 of the delivery pipeline: at a complete hand-back the daemon
// commits the worker's leftover changes, for every harness, so the hand-back
// names a real commit. These tests use the Claude Code harness.

func handbackCommitNode(t *testing.T) (*claudeNode, string) {
	t.Helper()
	c, repo, _ := handbackCommitNodeGateway(t)
	return c, repo
}

func handbackCommitNodeGateway(t *testing.T) (*claudeNode, string, *testGateway) {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.Idle = time.Hour
	c := startClaudeNode(t, g, set)
	repo := newRepo(t, "shop")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".gitignore")
	gitIn(t, repo, "commit", "-q", "-m", "ignore file")
	c.register(t, repo)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("notices: %+v", c.notices(t))
			t.Logf("daemon log: %v", c.logs.lines())
		}
	})
	return c, repo, g
}

func TestCompleteHandbackCommitsLeftoverChanges(t *testing.T) {
	c, repo := handbackCommitNode(t)
	base := gitOutT(t, repo, "rev-parse", "HEAD")
	res := c.mustStart(t, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude", Title: "Add the dirty file",
		Task: "FAKE:dirty FAKE:handback", Isolation: protocol.IsolationWorktree})
	n := c.waitNotice(t, 1)[0]
	if got := gitOutT(t, repo, "rev-list", "--count", base+".."+res.Branch); got != "1" {
		t.Fatalf("commits on the worker branch: %s", got)
	}
	msg := gitOutT(t, repo, "log", "-1", "--format=%B", res.Branch)
	for _, want := range []string{"Add the dirty file\n", "Agent-Net-Worker: " + res.SessionID, "Agent-Net-Harness: claude", "Agent-Net-Model: sonnet", "Agent-Net-Caller:"} {
		if !strings.Contains(msg, want) {
			t.Errorf("commit message lacks %q:\n%s", want, msg)
		}
	}
	if !strings.HasPrefix(msg, "Add the dirty file\n") || strings.Contains(strings.ToLower(msg), "co-authored-by:") {
		t.Errorf("commit message:\n%s", msg)
	}
	if files := gitOutT(t, repo, "diff-tree", "--no-commit-id", "--name-only", "-r", res.Branch); files != "dirty.txt" {
		t.Errorf("committed files: %q", files)
	}
	if _, err := os.Stat(filepath.Join(res.Worktree, "ignored.txt")); err != nil {
		t.Errorf("the ignored file is gone: %v", err)
	}
	head := gitOutT(t, repo, "rev-parse", "--short", res.Branch)
	for _, want := range []string{"head: " + head, "the daemon committed your leftover changes as " + head, "dirty.txt"} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, n.Body)
		}
	}
	if strings.Contains(n.Body, "unfinished changes were saved") {
		t.Errorf("a complete hand-back is called unfinished:\n%s", n.Body)
	}
	if got := gitOutT(t, repo, "rev-parse", "HEAD"); got != base {
		t.Error("the owner's branch moved")
	}
}

func TestCompleteHandbackWithNothingToCommit(t *testing.T) {
	c, repo := handbackCommitNode(t)
	base := gitOutT(t, repo, "rev-parse", "HEAD")
	res := c.mustStart(t, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude", Title: "Nothing to do",
		Task: "FAKE:handback", Isolation: protocol.IsolationWorktree})
	n := c.waitNotice(t, 1)[0]
	if got := gitOutT(t, repo, "rev-parse", res.Branch); got != base {
		t.Errorf("an empty commit was made: %s", gitOutT(t, repo, "log", "-1", "--format=%B", res.Branch))
	}
	if strings.Contains(n.Body, "the daemon committed") {
		t.Errorf("notice:\n%s", n.Body)
	}
	if want := "head: " + gitOutT(t, repo, "rev-parse", "--short", base); !strings.Contains(n.Body, want) {
		t.Errorf("notice lacks %q:\n%s", want, n.Body)
	}
}

func TestPartialHandbackDoesNotCommit(t *testing.T) {
	c, repo := handbackCommitNode(t)
	base := gitOutT(t, repo, "rev-parse", "HEAD")
	res := c.mustStart(t, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude", Title: "Half done",
		Task: "FAKE:dirty FAKE:partial FAKE:handback", Isolation: protocol.IsolationWorktree, AutoContinue: autoContinueOff()})
	n := c.waitNotice(t, 1)[0]
	if got := gitOutT(t, repo, "rev-parse", res.Branch); got != base {
		t.Errorf("a partial hand-back was committed: %s", gitOutT(t, repo, "log", "-1", "--format=%B", res.Branch))
	}
	if st := gitOutT(t, res.Worktree, "status", "--porcelain"); !strings.Contains(st, "dirty.txt") {
		t.Errorf("the uncommitted work is gone: %q", st)
	}
	if strings.Contains(n.Body, "the daemon committed") || strings.Contains(n.Body, "unfinished changes were saved") {
		t.Errorf("notice:\n%s", n.Body)
	}
}

func TestCompleteHandbackInAMergeIsLeftAsIs(t *testing.T) {
	c, repo := handbackCommitNode(t)
	res := c.mustStart(t, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude", Title: "Merge main",
		Task: "FAKE:ignore merge main", Isolation: protocol.IsolationWorktree})
	eventually(t, "the worker runs", func() bool {
		_, err := os.Stat(filepath.Join(res.Worktree, "fake-claude.json"))
		return err == nil
	})
	if err := os.WriteFile(filepath.Join(repo, "main-only.txt"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "main-only.txt")
	gitIn(t, repo, "commit", "-q", "-m", "main moves on")
	gitIn(t, res.Worktree, "-c", "user.name=t", "-c", "user.email=t@example.com", "merge", "--no-commit", "--no-ff", "main")
	before := gitOutT(t, repo, "rev-parse", res.Branch)
	c.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:handback merged"})
	n := c.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "A git merge was in progress; the worktree was left as is.") {
		t.Errorf("notice lacks the merge line:\n%s", n.Body)
	}
	if strings.Contains(n.Body, "the daemon committed") {
		t.Errorf("notice:\n%s", n.Body)
	}
	if after := gitOutT(t, repo, "rev-parse", res.Branch); after != before {
		t.Errorf("the branch moved from %s to %s", before, after)
	}
	if gitOutT(t, res.Worktree, "rev-parse", "--verify", "MERGE_HEAD") == "" {
		t.Error("MERGE_HEAD is gone")
	}
}

// continue keeps the leftover work of a partial hand-back on the new branch,
// even with delivery.save_unfinished off.
func TestContinueKeepsWorkWithSaveUnfinishedOff(t *testing.T) {
	c, repo, g := handbackCommitNodeGateway(t)
	// Written at the gateway, as the owner would: a snapshot stored in the
	// daemon directly is replaced by the next push from the gateway.
	if _, err := g.gw.Store().WriteSettings([]gateway.SettingsChange{{Key: "delivery.save_unfinished", Level: "network", Scope: "network", Value: json.RawMessage(`false`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the daemon has save_unfinished off", func() bool {
		return !c.d.SettingsSnapshot().Bool("delivery.save_unfinished", settings.Where{})
	})
	old := c.mustStart(t, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude", Title: "Half done",
		Task: "FAKE:dirty FAKE:partial FAKE:handback", Isolation: protocol.IsolationWorktree, AutoContinue: autoContinueOff()})
	c.waitNotice(t, 1)
	eventually(t, "the worker ended and its row was saved", func() bool {
		row, ok, err := c.d.st.workerBySession(old.SessionID)
		return c.d.workerCount() == 0 && err == nil && ok && row.EndedReason != ""
	})
	if c.d.SettingsSnapshot().Bool("delivery.save_unfinished", settings.Where{}) {
		t.Fatal("save_unfinished is on")
	}
	res, err := c.startDefault(protocol.WorkerStartRequest{Continue: old.SessionID, Task: "FAKE:ignore"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(res.Worktree, "dirty.txt")); err != nil || string(got) != "dirty\n" {
		t.Errorf("the uncommitted work is not in the new worktree: %q %v", got, err)
	}
	want := "agent-net: unfinished work of worker " + workerIDOf(old) + " (handback)"
	if subject := gitOutT(t, repo, "log", "-1", "--format=%s", old.Branch); subject != want {
		t.Errorf("subject %q, want %q", subject, want)
	}
}

// The merge line belongs to one hand-back. A Codex worker keeps its record
// across turns, so a merge at one turn must not mark the next one.
func TestMergeLineIsOnlyForItsHandback(t *testing.T) {
	t.Parallel()
	d := commitTestDaemon(t)
	w, dir := commitTestWorker(t, d)
	w.leftAsIs = true // from an earlier turn, in a merge that is now finished
	if err := os.WriteFile(filepath.Join(dir, "next.txt"), []byte("next\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := true
	if d.commitAtHandback(w, protocol.WorkerHandbackRequest{Report: "next turn", Status: protocol.StatusDone, Complete: &done}) == "" {
		t.Fatal("the next turn was not committed")
	}
	if w.leftAsIs {
		t.Error("the merge mark of an earlier turn is kept")
	}
}
