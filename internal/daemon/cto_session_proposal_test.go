package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

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
