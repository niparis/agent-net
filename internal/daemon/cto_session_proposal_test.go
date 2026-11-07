package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestCTOSessionStartIsProposal(t *testing.T) {
	for _, tc := range []struct{ name, args string }{
		{"session_change", `{"action":"session_start","project":"garden","harness":"claude"}`},
		{"project_change", `{"action":"create","path":"/outside/project"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proposed, ran := 0, 0
			_, err := routeCTOTool(context.Background(), tc.name, json.RawMessage(tc.args), func(context.Context, string, string, json.RawMessage, string) (string, error) {
				proposed++
				return "p_1", nil
			}, func(context.Context, string, json.RawMessage) (any, error) { ran++; return nil, nil })
			if err != nil || proposed != 0 || ran != 1 {
				t.Fatalf("proposal=%d runs=%d err=%v", proposed, ran, err)
			}
		})
	}
}

func TestCTOSessionStartRejectsPathOutsideHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := &Daemon{}
	for _, call := range []proposalCall{
		{Tool: "session_change", Action: "session_start", Args: json.RawMessage(`{"action":"session_start","path":"/outside/project","harness":"claude"}`)},
		{Tool: "project_change", Action: "create", Args: json.RawMessage(`{"action":"create","path":"/outside/project"}`)},
	} {
		if err := d.validateProposal(context.Background(), call); err == nil || !strings.Contains(err.Error(), "outside your home") {
			t.Errorf("outside path for %s: %v", call.Tool, err)
		}
	}
}

func TestCTOSessionProposalApprovalAndTrust(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	repo := newHomeRepo(t, home, "code/garden")
	ctx := context.Background()
	args, _ := json.Marshal(map[string]any{"action": "session_start", "path": repo, "harness": "claude", "first_message": "hello"})
	if _, err := legacyProposalFixture(ctx, c.d, "session_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := c.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "fake-claude.json")); !os.IsNotExist(err) {
		t.Fatalf("started before approval: %v", err)
	}
	if err := c.d.proposalRunner().decide(ctx, items[0].ID, true); err != nil {
		t.Fatal(err)
	}
	items, err = c.d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(items[0].Note, "trust needs the owner") {
		t.Fatalf("trust note: %s", items[0].Note)
	}
	var result protocol.StartedSession
	if err := json.Unmarshal(items[0].Result, &result); err != nil || result.Status != "needs_trust" {
		t.Fatalf("result=%s err=%v", items[0].Result, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "fake-claude.json")); !os.IsNotExist(err) {
		t.Fatalf("started without trust: %v", err)
	}
	if err := c.d.proposalRunner().decide(ctx, items[0].ID, true); err == nil {
		t.Fatal("approved twice")
	}
}

func TestCTOProjectCreateRunsAfterApproval(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	path := filepath.Join(home, "code", "new-project")
	ctx := context.Background()
	args, _ := json.Marshal(map[string]any{"action": "create", "path": path, "harness": "claude", "first_message": "hello"})
	if _, err := legacyProposalFixture(ctx, c.d, "project_change", args); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("created before approval: %v", err)
	}
	items, err := c.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if err := c.d.proposalRunner().decide(ctx, items[0].ID, true); err != nil {
		t.Fatal(err)
	}
	items, err = c.d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	var result protocol.StartedSession
	if err := json.Unmarshal(items[0].Result, &result); err != nil {
		t.Fatal(err)
	}
	result = waitSessionStartPath(t, c.workerNode, result)
	t.Cleanup(func() { killFakeClaude(t, path) })
	if result.Status != "started" {
		t.Fatalf("result=%+v", result)
	}
	if err := c.d.proposalRunner().decide(ctx, items[0].ID, true); err == nil {
		t.Fatal("approved twice")
	}
}

func TestCTOSessionProposalRemotePathRefusedAfterApproval(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	pathTestHome(t)
	a := startWorkerNode(t, g, shortSet, nil)
	b := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"claude": os.Args[0]} })
	args, _ := json.Marshal(map[string]any{"action": "session_start", "path": "/outside/project", "harness": "claude", "node_id": b.d.NodeID(), "create": true})
	ctx := context.Background()
	if _, err := legacyProposalFixture(ctx, a.d, "session_change", args); err != nil {
		t.Fatalf("proposal: %v", err)
	}
	items, err := a.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if err := a.d.proposalRunner().decide(ctx, items[0].ID, true); err == nil || !strings.Contains(err.Error(), "outside your home") {
		t.Fatalf("approval error: %v", err)
	}
	items, err = a.d.proposalRunner().list()
	if err != nil || items[0].Status != "failed" || !strings.Contains(items[0].Note, "outside your home") {
		t.Fatalf("result=%v err=%v", items, err)
	}
}

func TestCTOProposalCannotSetTrust(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"session_start", `{"path":"~/garden","trust_claude":true}`},
		{"project_create", `{"path":"~/garden","trust_claude":true}`},
	} {
		if err := mcpserver.CTOArguments(tc.name, json.RawMessage(tc.raw)); err == nil {
			t.Errorf("%s accepted trust flag", tc.name)
		}
	}
}

func TestCTOSessionProposalRejectsWorktreeOutsideHome(t *testing.T) {
	home := pathTestHome(t)
	repo := newHomeRepo(t, home, "code/garden")
	root := ownerWorktreeRoot(repo)
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{}
	_, _, _, err := d.createOwnerWorktreeChecked(context.Background(), repo, "garden", "escape/branch", true)
	if err == nil || !strings.Contains(err.Error(), "outside your home") {
		t.Fatalf("worktree error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "branch")); !os.IsNotExist(err) {
		t.Fatalf("created outside: %v", err)
	}
}

func TestCTOSessionProposalRemoteStartOnce(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	a := startWorkerNode(t, g, shortSet, nil)
	b := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} })
	repo := newHomeRepo(t, home, "code/garden")
	var creates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/session":
			creates.Add(1)
			helloOpenCode(t, b.node, "ses_proposal", repo)
			fmt.Fprint(w, `{"data":{"id":"ses_proposal"}}`)
		case "POST /api/session/ses_proposal/prompt":
			fmt.Fprint(w, `{"data":{}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	writeOwnerOpenCodeService(t, os.Getenv("XDG_STATE_HOME"), server.URL)
	args, _ := json.Marshal(map[string]any{"action": "session_start", "path": repo, "node_id": b.d.NodeID(), "harness": "opencode", "first_message": "hello"})
	ctx := context.Background()
	if _, err := legacyProposalFixture(ctx, a.d, "session_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := a.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if creates.Load() != 0 {
		t.Fatal("started before approval")
	}
	if err := a.d.proposalRunner().decide(ctx, items[0].ID, true); err != nil {
		t.Fatal(err)
	}
	items, err = a.d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	var r protocol.StartedSession
	if err := json.Unmarshal(items[0].Result, &r); err != nil {
		t.Fatal(err)
	}
	eventually(t, "remote proposal start", func() bool {
		var err error
		r, err = a.d.sessionStartGet(ctx, r.ID)
		return err == nil && r.Status == "started"
	})
	if err := a.d.proposalRunner().decide(ctx, items[0].ID, true); err == nil {
		t.Fatal("approved twice")
	}
	if creates.Load() != 1 {
		t.Fatalf("started %d times", creates.Load())
	}
}

func TestCTOProjectCreateRejectsExistingPath(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	path := filepath.Join(home, "code", "already")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"action": "create", "path": path, "harness": "claude"})
	ctx := context.Background()
	if _, err := legacyProposalFixture(ctx, c.d, "project_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := c.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	err = c.d.proposalRunner().decide(ctx, items[0].ID, true)
	if err == nil || !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "register it") {
		t.Fatalf("approval error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); !os.IsNotExist(err) {
		t.Fatalf("unexpected git repository: %v", err)
	}
}

func TestCTOProjectCloneRemoteValidation(t *testing.T) {
	for _, tc := range []struct {
		remote string
		valid  bool
	}{
		{"git@git.example:team/repo.git", true}, {"https://git.example/team/repo.git", true},
		{"file:///tmp/repo.git", false}, {"/tmp/repo.git", false}, {"../repo.git", false},
		{"http://git.example/team/repo.git", false}, {"ssh://git@git.example/team/repo.git", false},
		{"https://user:pass@git.example/team/repo.git", false}, {"https://git.example/team/../repo.git", false},
		{"git@git.example:team/repo.git?x=y", false}, {"https://git.example/team/repo.git#fragment", false},
	} {
		err := validateProjectRemote(tc.remote)
		if (err == nil) != tc.valid {
			t.Errorf("remote %q: %v", tc.remote, err)
		}
	}
}

func TestCTOProjectCloneRegistersAndStarts(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	source := newHomeRepo(t, home, "source")
	bare := filepath.Join(home, "source.git")
	gitIn(t, home, "clone", "--bare", source, bare)
	path := filepath.Join(home, "code", "company-rag")
	c.d.cloneProject = func(ctx context.Context, remote, dest string) error {
		if remote != "git@git.example:team/repo.git" {
			return fmt.Errorf("unexpected remote %q", remote)
		}
		if _, err := gitOut(ctx, home, "-c", "protocol.file.allow=always", "clone", "--", bare, dest); err != nil {
			return err
		}
		_, err := gitOut(ctx, dest, "remote", "set-url", "origin", remote)
		return err
	}
	ctx := context.Background()
	args, _ := json.Marshal(map[string]any{"action": "create", "path": path, "remote": "git@git.example:team/repo.git", "harness": "claude"})
	if _, err := legacyProposalFixture(ctx, c.d, "project_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := c.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cloned before approval: %v", err)
	}
	if err := c.d.proposalRunner().decide(ctx, items[0].ID, true); err != nil {
		t.Fatal(err)
	}
	items, err = c.d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	var started protocol.StartedSession
	if err := json.Unmarshal(items[0].Result, &started); err != nil {
		t.Fatal(err)
	}
	started = waitSessionStartPath(t, c.workerNode, started)
	t.Cleanup(func() { killFakeClaude(t, path) })
	if started.Status != "started" {
		t.Fatalf("start: %+v", started)
	}
	if _, err := os.Stat(filepath.Join(path, "README.md")); err != nil {
		t.Fatal(err)
	}
	if trusted, err := claudetrust.Trusted(path); err != nil || !trusted {
		t.Fatalf("trust=%v err=%v", trusted, err)
	}
	if started.EnterPlan != nil {
		t.Fatalf("clone opened a view: %+v", started.EnterPlan)
	}
	var projects protocol.ProjectsResult
	if err := json.Unmarshal(c.cli.mustCall("projects", protocol.QueryProjects{Name: "repo"}), &projects); err != nil || len(projects.Projects) != 1 || projects.Projects[0].Path != path {
		t.Fatalf("projects=%+v err=%v", projects, err)
	}
}

func TestCTOProjectCloneRejectsUnsafeRemotesBeforeHook(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	called := false
	c.d.cloneProject = func(context.Context, string, string) error { called = true; return nil }
	for _, remote := range []string{"file:///tmp/repo.git", "/tmp/repo.git", "https://u:p@git.example/team/repo.git"} {
		args, _ := json.Marshal(map[string]any{"action": "create", "path": filepath.Join(home, "code", "target"), "remote": remote, "harness": "claude"})
		if _, err := legacyProposalFixture(context.Background(), c.d, "project_change", args); err == nil {
			t.Errorf("accepted %q", remote)
		}
	}
	if called {
		t.Fatal("clone hook ran for an unsafe remote")
	}
}

func TestCTOProjectCloneRejectsExistingPath(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	path := filepath.Join(home, "code", "target")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	called := false
	c.d.cloneProject = func(context.Context, string, string) error { called = true; return nil }
	args, _ := json.Marshal(map[string]any{"action": "create", "path": path, "remote": "https://git.example/team/repo.git", "harness": "claude"})
	if _, err := legacyProposalFixture(context.Background(), c.d, "project_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := c.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	err = c.d.proposalRunner().decide(context.Background(), items[0].ID, true)
	if err == nil || !strings.Contains(err.Error(), "already exists") || called {
		t.Fatalf("err=%v cloned=%v", err, called)
	}
}

func TestCTOProjectCloneRemoteRelay(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	a := startWorkerNode(t, g, shortSet, nil)
	b := startClaudeNode(t, g, shortSet)
	source := newHomeRepo(t, home, "source")
	bare := filepath.Join(home, "source.git")
	gitIn(t, home, "clone", "--bare", source, bare)
	path := filepath.Join(home, "code", "company-rag")
	remote := "git@git.example:team/repo.git"
	b.d.cloneProject = func(ctx context.Context, got, dest string) error {
		if got != remote {
			return fmt.Errorf("remote lost in relay: %q", got)
		}
		_, err := gitOut(ctx, home, "-c", "protocol.file.allow=always", "clone", "--", bare, dest)
		return err
	}
	args, _ := json.Marshal(map[string]any{"action": "create", "path": path, "node_id": b.d.NodeID(), "remote": remote, "harness": "claude"})
	if _, err := legacyProposalFixture(context.Background(), a.d, "project_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := a.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if err := a.d.proposalRunner().decide(context.Background(), items[0].ID, true); err != nil {
		t.Fatal(err)
	}
	items, _ = a.d.proposalRunner().list()
	var result protocol.StartedSession
	if err := json.Unmarshal(items[0].Result, &result); err != nil {
		t.Fatal(err)
	}
	eventually(t, "remote clone starts", func() bool {
		var err error
		result, err = a.d.sessionStartGet(context.Background(), result.ID)
		return err == nil && result.Status == "started"
	})
	t.Cleanup(func() { killFakeClaude(t, path) })
	if _, err := os.Stat(filepath.Join(path, "README.md")); err != nil {
		t.Fatal(err)
	}
}

func TestCTOProjectCloneRejectsLegacyRelay(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	a := startWorkerNode(t, g, shortSet, nil)
	b := startClaudeNode(t, g, shortSet)
	path := filepath.Join(home, "code", "company-rag")
	p := protocol.SessionOwnerRequest{Owner: a.ownerID(), FromNode: a.d.NodeID(), SessionStartRequest: protocol.SessionStartRequest{Path: path, Harness: "claude", Create: true, Remote: "git@git.example:team/repo.git"}}
	raw, _ := json.Marshal(p)
	_, err := b.d.sessionOwnerRelay(context.Background(), protocol.FrameSessionOwnerProposalStart, raw)
	if err == nil || !strings.Contains(err.Error(), "clone relay") {
		t.Fatalf("legacy relay accepted clone: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("legacy relay created path: %v", err)
	}
}

func TestCTOProjectCloneDestinationRejectsUnsafeRemote(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	called := false
	c.d.cloneProject = func(context.Context, string, string) error { called = true; return nil }
	_, err := c.d.sessionStartLocal(context.Background(), protocol.SessionStartRequest{Path: filepath.Join(home, "code", "target"), Harness: "claude", Create: true, CTOProposal: true, Remote: "file:///tmp/repo.git"})
	if err == nil || called {
		t.Fatalf("unsafe remote accepted: err=%v cloned=%v", err, called)
	}
}

func TestCTOProjectCloneOnlyForApprovedCreate(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	_, err := c.d.sessionStart(context.Background(), protocol.SessionStartRequest{Path: filepath.Join(home, "target"), NodeID: "n_other", Harness: "claude", Create: true, Remote: "git@git.example:team/repo.git"})
	if err == nil || !strings.Contains(err.Error(), "approved project create") {
		t.Fatalf("unapproved clone: %v", err)
	}
}
