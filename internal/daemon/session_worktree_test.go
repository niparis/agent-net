package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Folder switch of the New session form on the daemon: a new git worktree
// next to the project, the lowest free branch number, and owner worktrees that
// worker cleanup never removes (docs/design/start-sessions.md, part 9).

func ownerWorktreePath(repo, branch string) string {
	return filepath.Join(filepath.Dir(repo), filepath.Base(repo)+".worktrees", branch)
}

// startOwnerWorktree asks for an owner session in a new worktree and waits for
// the start to finish.
func startOwnerWorktree(t *testing.T, n *workerNode, req protocol.SessionStartRequest) protocol.StartedSession {
	t.Helper()
	if req.Folder == "" {
		req.Folder = protocol.FolderWorktree
	}
	var r protocol.StartedSession
	mustJSON(t, n.cli.mustCall("session_start", req), &r)
	eventually(t, "the owner worktree start finishes", func() bool {
		mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("worktree start failed: %+v", r)
		}
		return r.Status == "started" && r.SessionID != ""
	})
	return r
}

func killFakeClaude(t *testing.T, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "fake-claude.json"))
	if err != nil {
		return
	}
	var rec fakeClaudeRecord
	if json.Unmarshal(raw, &rec) == nil && rec.Pgid > 1 {
		syscall.Kill(-rec.Pgid, syscall.SIGKILL)
	}
}

func TestSessionStartCreatesAnOwnerWorktree(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)

	r := startOwnerWorktree(t, c.workerNode, protocol.SessionStartRequest{Project: "shop", Harness: "claude"})
	want := ownerWorktreePath(repo, "agent-net/shop-1")
	if r.Path != want {
		t.Fatalf("worktree path %q, want %q", r.Path, want)
	}
	if r.Branch != "agent-net/shop-1" {
		t.Fatalf("branch %q, want agent-net/shop-1", r.Branch)
	}
	t.Cleanup(func() { killFakeClaude(t, want) })
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("the worktree folder was not created: %v", err)
	}
	if got := gitOutT(t, repo, "branch", "--list", "agent-net/shop-1"); !strings.Contains(got, "agent-net/shop-1") {
		t.Fatalf("the branch was not created: %q", got)
	}
	if got := gitOutT(t, repo, "worktree", "list", "--porcelain"); !strings.Contains(got, want) {
		t.Fatalf("git does not know the worktree: %s", got)
	}
	if rec := readClaude(t, want); rec.Cwd != want {
		t.Fatalf("the session started in %q, want the worktree %q", rec.Cwd, want)
	}
	if _, err := os.Stat(filepath.Join(repo, "fake-claude.json")); !os.IsNotExist(err) {
		t.Errorf("the session started in the project folder: %v", err)
	}
	if a := c.agentOf(t, r.SessionID); a == nil || a.Cwd != want || a.Repo.Worktree != want {
		t.Fatalf("registered session %+v, want the worktree", a)
	}
}

func TestSessionStartOwnerWorktreeUsesTheLowestFreeBranch(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)
	gitIn(t, repo, "branch", "agent-net/shop-1")
	gitIn(t, repo, "branch", "agent-net/shop-3")

	r := startOwnerWorktree(t, c.workerNode, protocol.SessionStartRequest{Project: "shop", Harness: "claude"})
	if r.Branch != "agent-net/shop-2" {
		t.Fatalf("branch %q, want agent-net/shop-2", r.Branch)
	}
	if want := ownerWorktreePath(repo, r.Branch); r.Path != want {
		t.Fatalf("worktree %q, want %q", r.Path, want)
	}
	t.Cleanup(func() { killFakeClaude(t, r.Path) })
}

func TestSessionStartOwnerWorktreeHonoursTheBranchField(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)

	r := startOwnerWorktree(t, c.workerNode, protocol.SessionStartRequest{Project: "shop", Harness: "claude", Branch: "feature/garden"})
	if r.Branch != "feature/garden" {
		t.Fatalf("branch %q, want feature/garden", r.Branch)
	}
	if want := ownerWorktreePath(repo, "feature/garden"); r.Path != want {
		t.Fatalf("worktree %q, want %q", r.Path, want)
	}
	t.Cleanup(func() { killFakeClaude(t, r.Path) })
}

func TestSessionStartOwnerWorktreeRefusesANonRepository(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{}
	_, _, _, err := d.createOwnerWorktree(context.Background(), folder, "plain", "")
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("non-repository worktree start: %v", err)
	}
}

// An owner worktree is the owner's. Worker cleanup removes the worktrees it
// made under the daemon state folder; it must never remove this one.
func TestOwnerWorktreeIsNeverRemovedByWorkerCleanup(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)

	d := c.d
	dir, branch, base, err := d.createOwnerWorktree(context.Background(), repo, "shop", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := ownerWorktreePath(repo, branch); dir != want {
		t.Fatalf("worktree %q, want %q", dir, want)
	}
	if base != "main" {
		t.Errorf("base branch %q, want main", base)
	}
	if d.inWorktreesDir(dir) {
		t.Fatal("the owner worktree is inside the worker worktrees folder")
	}

	// A worker record that somehow points at the owner's worktree must not
	// lose it in the cleanup.
	w := &worker{id: "w_owner_guard", path: dir, mainPath: repo, worktree: dir, branch: branch, harness: "claude"}
	d.finishWorktree(w)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("worker cleanup removed the owner worktree: %v", err)
	}
	if got := gitOutT(t, repo, "branch", "--list", branch); !strings.Contains(got, branch) {
		t.Fatalf("worker cleanup removed the owner branch: %q", got)
	}
}

// The folder choice and the branch travel with a start on another machine.
func TestSessionStartRelayCarriesTheWorktreeChoice(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	a := startWorkerNode(t, g, shortSet, nil)
	b := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "homelab")
	b.register(t, repo)

	r := startOwnerWorktree(t, a, protocol.SessionStartRequest{
		Project: "homelab", Harness: "claude", NodeID: b.d.NodeID(),
		Folder: protocol.FolderWorktree, Branch: "agent-net/homelab-7",
	})
	want := ownerWorktreePath(repo, "agent-net/homelab-7")
	if r.Path != want || r.Branch != "agent-net/homelab-7" || r.NodeID != b.d.NodeID() {
		t.Fatalf("relayed worktree start: %+v, want %q", r, want)
	}
	t.Cleanup(func() { killFakeClaude(t, want) })
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("the other machine did not create the worktree: %v", err)
	}
	if rec := readClaude(t, want); rec.Cwd != want {
		t.Fatalf("the session started in %q, want %q", rec.Cwd, want)
	}
}
