package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/protocol"
)

// Paths and new folders in the New session form
// (docs/design/start-sessions.md, build plan part 10).

// pathTestHome points HOME at a temporary folder, so ~ paths and the
// home-folder rule are tested without touching the real home folder.
func pathTestHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	return home
}

// newHomeRepo makes a git repository under the test home folder.
func newHomeRepo(t *testing.T, home, rel string) string {
	t.Helper()
	repo := filepath.Join(home, filepath.FromSlash(rel))
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# garden\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "README.md")
	gitIn(t, repo, "commit", "-q", "-m", "first")
	return repo
}

func sessionStartPathCall(t *testing.T, n *workerNode, req protocol.SessionStartRequest) protocol.StartedSession {
	t.Helper()
	raw, err := n.cli.call("session_start", req)
	if err != nil {
		t.Fatalf("session_start: %v", err)
	}
	var r protocol.StartedSession
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func waitSessionStartPath(t *testing.T, n *workerNode, r protocol.StartedSession) protocol.StartedSession {
	t.Helper()
	eventually(t, "the path start finishes", func() bool {
		mustJSON(t, n.cli.mustCall("session_start_get", protocol.SessionStartQuery{ID: r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("path start failed: %+v", r)
		}
		return r.Status == "started" && r.SessionID != ""
	})
	return r
}

func TestSessionStartPathRegistersAnExistingFolderAndStartsThere(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	repo := newHomeRepo(t, home, "code/garden")

	r := sessionStartPathCall(t, c.workerNode, protocol.SessionStartRequest{
		Path: repo, Harness: "claude", TrustClaude: true,
	})
	r = waitSessionStartPath(t, c.workerNode, r)
	t.Cleanup(func() { killFakeClaude(t, repo) })
	if r.Path != repo || r.Project != "garden" {
		t.Fatalf("started %+v, want %q as garden", r, repo)
	}
	if rec := readClaude(t, repo); rec.Cwd != repo {
		t.Fatalf("the session started in %q, want %q", rec.Cwd, repo)
	}
	var projects protocol.ProjectsResult
	if err := json.Unmarshal(c.cli.mustCall("projects", protocol.QueryProjects{Name: "garden"}), &projects); err != nil {
		t.Fatal(err)
	}
	if len(projects.Projects) != 1 || projects.Projects[0].Path != repo {
		t.Fatalf("the folder was not registered: %+v", projects.Projects)
	}
	if src := projects.Projects[0].Source; src != protocol.SourceRegistered && src != protocol.SourceBoth {
		t.Errorf("the project source is %q, want a registered project", src)
	}
}

// An existing folder is not trusted for Claude Code by agent-net on its own.
// The owner answers the trust question, and the start is sent again.
func TestSessionStartPathAsksBeforeTrustingClaude(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	repo := newHomeRepo(t, home, "code/garden")

	r := sessionStartPathCall(t, c.workerNode, protocol.SessionStartRequest{Path: repo, Harness: "claude"})
	if r.Status != "needs_trust" || r.Path != repo {
		t.Fatalf("untrusted folder answered %+v, want needs_trust", r)
	}
	if _, err := os.Stat(filepath.Join(repo, "fake-claude.json")); !os.IsNotExist(err) {
		t.Fatalf("the session started before the owner trusted the folder: %v", err)
	}
	if trusted, err := claudetrust.Trusted(repo); err != nil || trusted {
		t.Fatalf("the folder was trusted without the owner: trusted=%v err=%v", trusted, err)
	}

	raw, err := c.cli.call("session_start", protocol.SessionStartRequest{
		Path: repo, Harness: "claude", TrustClaude: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var again protocol.StartedSession
	mustJSON(t, raw, &again)
	again = waitSessionStartPath(t, c.workerNode, again)
	t.Cleanup(func() { killFakeClaude(t, repo) })
	if again.Status != "started" || again.Path != repo {
		t.Fatalf("trusted start %+v", again)
	}
	if trusted, err := claudetrust.Trusted(repo); err != nil || !trusted {
		t.Fatalf("the owner's trust was not saved: trusted=%v err=%v", trusted, err)
	}
}

func TestSessionStartMissingPathNeedsTheCreateFlag(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	target := filepath.Join(home, "code", "garden-planner")

	r := sessionStartPathCall(t, c.workerNode, protocol.SessionStartRequest{Path: target, Harness: "claude"})
	if r.Status != "needs_create" || r.Path != target || r.Project != "garden-planner" {
		t.Fatalf("missing folder answered %+v, want needs_create for %q", r, target)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("the folder exists without the owner's yes: %v", err)
	}
	var projects protocol.ProjectsResult
	if err := json.Unmarshal(c.cli.mustCall("projects", protocol.QueryProjects{Name: "garden-planner"}), &projects); err != nil {
		t.Fatal(err)
	}
	if len(projects.Projects) != 0 {
		t.Fatalf("a missing folder was registered: %+v", projects.Projects)
	}

	yes := sessionStartPathCall(t, c.workerNode, protocol.SessionStartRequest{Path: target, Harness: "claude", Create: true})
	yes = waitSessionStartPath(t, c.workerNode, yes)
	t.Cleanup(func() { killFakeClaude(t, target) })
	if yes.Path != target || yes.Project != "garden-planner" {
		t.Fatalf("created start %+v", yes)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("the folder was not created: %v", err)
	}
	if info, err := os.Stat(filepath.Join(target, ".git")); err != nil || !info.IsDir() {
		t.Fatalf("git init did not run: %v", err)
	}
	if rec := readClaude(t, target); rec.Cwd != target {
		t.Fatalf("the session started in %q, want %q", rec.Cwd, target)
	}
	if trusted, err := claudetrust.Trusted(target); err != nil || !trusted {
		t.Fatalf("a folder agent-net created is not trusted: trusted=%v err=%v", trusted, err)
	}
	if err := json.Unmarshal(c.cli.mustCall("projects", protocol.QueryProjects{Name: "garden-planner"}), &projects); err != nil {
		t.Fatal(err)
	}
	if len(projects.Projects) != 1 || projects.Projects[0].Path != target {
		t.Fatalf("the created folder was not registered: %+v", projects.Projects)
	}
}

func TestSessionStartPathHomeRule(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, "escape")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	for _, tc := range []struct {
		name   string
		path   string
		create bool
		want   string
	}{
		{"outside", "/etc/agent-net-garden", false, "outside your home folder"},
		{"outside create", filepath.Join(home, "..", "agent-net-garden"), true, "outside your home folder"},
		{"relative", "code/garden", false, "absolute or start with ~"},
		{"control", "/tmp/garden\x01planner", false, "control character"},
		{"link", filepath.Join(home, "escape"), false, "outside your home folder"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.cli.call("session_start", protocol.SessionStartRequest{
				Path: tc.path, Harness: "claude", Create: tc.create,
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("path %q: error %v, want %q", tc.path, err, tc.want)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(home, "..", "agent-net-garden")); !os.IsNotExist(err) {
		t.Fatalf("a folder outside home was created: %v", err)
	}
}

// The path and the create flag travel with a start on another machine, and
// that machine applies the same checks.
func TestSessionStartRelayCarriesAPathAndTheCreateFlag(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	a := startWorkerNode(t, g, shortSet, nil)
	b := startClaudeNode(t, g, shortSet)
	target := filepath.Join(home, "code", "relay-garden")

	r := sessionStartPathCall(t, a, protocol.SessionStartRequest{
		Path: target, Harness: "claude", NodeID: b.d.NodeID(),
	})
	if r.Status != "needs_create" || r.NodeID != b.d.NodeID() {
		t.Fatalf("relayed missing path answered %+v", r)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("the other machine created the folder without the owner's yes: %v", err)
	}

	yes := sessionStartPathCall(t, a, protocol.SessionStartRequest{
		Path: target, Harness: "claude", NodeID: b.d.NodeID(), Create: true,
	})
	if yes.NodeID != b.d.NodeID() {
		t.Fatalf("relayed start answered %+v", yes)
	}
	eventually(t, "the relayed path start finishes", func() bool {
		mustJSON(t, a.cli.mustCall("session_start_get", protocol.SessionStartQuery{ID: yes.ID}), &yes)
		if yes.Status == "failed" {
			t.Fatalf("relayed start failed: %+v", yes)
		}
		return yes.Status == "started" && yes.SessionID != ""
	})
	t.Cleanup(func() { killFakeClaude(t, target) })
	if yes.Path != target {
		t.Fatalf("relayed start path %q, want %q", yes.Path, target)
	}
	if info, err := os.Stat(filepath.Join(target, ".git")); err != nil || !info.IsDir() {
		t.Fatalf("the other machine did not create the folder with git init: %v", err)
	}
	if rec := readClaude(t, target); rec.Cwd != target {
		t.Fatalf("the relayed session started in %q, want %q", rec.Cwd, target)
	}

	_, err := a.cli.call("session_start", protocol.SessionStartRequest{
		Path: "/etc/agent-net-relay", Harness: "claude", NodeID: b.d.NodeID(), Create: true,
	})
	if err == nil || !strings.Contains(err.Error(), "outside your home folder") {
		t.Fatalf("the other machine accepted a bad path: %v", err)
	}
}
