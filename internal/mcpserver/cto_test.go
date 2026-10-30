package mcpserver

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func ctoTestSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	srv := NewCTO(func(_ context.Context, method string, params any) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), nil
	}, "test")
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func ctoToolSchema(t *testing.T, tool *mcp.Tool) (map[string]map[string]any, []string) {
	t.Helper()
	b, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return schema.Properties, names
}

func TestCTOToolDescriptionsAndEnums(t *testing.T) {
	cs := ctoTestSession(t)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	for _, tl := range list.Tools {
		tools[tl.Name] = tl
	}
	wantTools := []string{"graph_get", "charter_set", "charter_history", "node_add", "node_remove", "edge_set", "edge_remove", "edge_history", "graph_undo", "agents", "workers", "needs_you", "feed", "send", "worker_start", "score_set", "worker_providers_set"}
	if len(tools) != len(wantTools) {
		t.Fatalf("got %d tools, want %d: %v", len(tools), len(wantTools), tools)
	}
	for _, name := range wantTools {
		tl, ok := tools[name]
		if !ok {
			t.Errorf("missing tool %s", name)
			continue
		}
		if strings.HasPrefix(tl.Description, "CTO: ") || len(tl.Description) < 60 {
			t.Errorf("%s has a generic or short description: %q", name, tl.Description)
		}
		props, names := ctoToolSchema(t, tl)
		for _, field := range names {
			if d, _ := props[field]["description"].(string); strings.TrimSpace(d) == "" {
				t.Errorf("%s.%s has no description", name, field)
			}
		}
	}
	wantEnums := map[string]map[string][]string{
		"edge_set": {
			"kind": {"depends", "governs", "deploys"},
			"mode": {"advisor", "arbiter", "owner"},
			"rule": {"normal", "auto", "ask", "never"},
		},
		"edge_remove":  {"kind": {"depends", "governs", "deploys"}},
		"charter_set":  {"attr": {"purpose", "maturity", "accepts", "refuses", "feature_requests", "consumers", "escalate", "needs", "notes"}},
		"worker_start": {"harness": {"opencode", "claude", "pi", "codex"}, "isolation": {"worktree", "none"}},
	}
	for tool, fields := range wantEnums {
		tl, ok := tools[tool]
		if !ok {
			continue
		}
		props, _ := ctoToolSchema(t, tl)
		for field, want := range fields {
			got := enumStrings(props[field]["enum"])
			if strings.Join(got, " ") != strings.Join(want, " ") {
				t.Errorf("%s.%s enum = %v, want %v", tool, field, got, want)
			}
		}
	}
}

func enumStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}

func TestCTOToolsForward(t *testing.T) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	seen := map[string]bool{}
	srv := NewCTO(func(_ context.Context, method string, params any) (json.RawMessage, error) {
		seen[method] = true
		return json.RawMessage(`{"ok":true}`), nil
	}, "test")
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	for _, name := range []string{"graph_get", "charter_set", "charter_history", "node_add", "node_remove", "edge_set", "edge_remove", "edge_history", "graph_undo", "agents", "workers", "needs_you", "feed", "send", "worker_start", "score_set", "worker_providers_set"} {
		out, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err != nil || out.IsError || !seen[name] {
			t.Errorf("%s: %+v %v", name, out, err)
		}
	}
}
