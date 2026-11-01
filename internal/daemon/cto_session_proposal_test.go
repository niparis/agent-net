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
			if err != nil || proposed != 1 || ran != 0 {
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
	if _, err := c.d.runCTOToolRaw(ctx, "session_change", args); err != nil {
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
	if _, err := c.d.runCTOToolRaw(ctx, "project_change", args); err != nil {
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
	if _, err := a.d.runCTOToolRaw(ctx, "session_change", args); err != nil {
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
	if _, err := a.d.runCTOToolRaw(ctx, "session_change", args); err != nil {
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
