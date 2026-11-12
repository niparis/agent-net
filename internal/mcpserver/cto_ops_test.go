package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func ctoOpTestSession(t *testing.T, result json.RawMessage, calls *[]map[string]any, mu *sync.Mutex) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	srv := NewCTO(func(_ context.Context, method string, params any) (json.RawMessage, error) {
		if mu != nil {
			mu.Lock()
			raw, _ := json.Marshal(params)
			var args map[string]any
			_ = json.Unmarshal(raw, &args)
			*calls = append(*calls, map[string]any{"method": method, "args": args})
			mu.Unlock()
		}
		return result, nil
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

func TestCTOOpToolsAreListed(t *testing.T) {
	list, err := ctoTestSession(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]*mcp.Tool{}
	for _, tool := range list.Tools {
		found[tool.Name] = tool
	}
	for _, name := range []string{"cto_ops_list", "cto_op"} {
		tool := found[name]
		if tool == nil {
			t.Fatalf("missing %s", name)
		}
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("%s has no description", name)
		}
		props, _ := ctoToolSchema(t, tool)
		if name == "cto_op" {
			if props["method"] == nil || props["args"] == nil {
				t.Errorf("cto_op schema: %v", props)
			}
		}
		if name == "cto_ops_list" && props["filter"] == nil {
			t.Errorf("cto_ops_list schema: %v", props)
		}
	}
}

func TestCTOOpBadArgumentsReturnSchemaAndRunNothing(t *testing.T) {
	var mu sync.Mutex
	var calls []map[string]any
	cs := ctoOpTestSession(t, json.RawMessage(`{"ok":true}`), &calls, &mu)
	for _, tc := range []struct {
		name, args string
		want       []string
	}{
		{"process_stop", `{"method":"process_stop","args":{"node_id":"n_1"}}`, []string{"pids", "Schema:", "Example:"}},
		{"node_remove", `{"method":"node_remove","args":{}}`, []string{"name", "Schema:", "Example:"}},
		{"bar_edit without a project", `{"method":"bar_edit","args":{"add":["Performance first."]}}`, []string{"project", "Schema:", "Example:"}},
		{"unknown argument", `{"method":"app_grant","args":{"id":"app-1","grant":"mcp server x","bogus":true}}`, []string{"bogus", "Schema:", "Example:"}},
		{"unknown op", `{"method":"no_such_op","args":{}}`, []string{"no_such_op", "cto_ops_list"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "cto_op", Arguments: json.RawMessage(tc.args)})
			if err != nil {
				t.Fatal(err)
			}
			if !res.IsError || len(res.Content) == 0 {
				t.Fatalf("expected a tool error: %+v", res)
			}
			text := res.Content[0].(*mcp.TextContent).Text
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("error %q lacks %q", text, want)
				}
			}
		})
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("a bad cto_op reached the daemon: %+v", calls)
	}
}

func TestCTOOpForwardsOneCallToTheDaemon(t *testing.T) {
	var mu sync.Mutex
	var calls []map[string]any
	cs := ctoOpTestSession(t, json.RawMessage(`{"state":"completed"}`), &calls, &mu)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "cto_op", Arguments: map[string]any{"method": "apps_list", "args": map[string]any{}}})
	if err != nil || res.IsError {
		t.Fatalf("cto_op: %+v %v", res, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0]["method"] != "cto_op" {
		t.Fatalf("daemon calls: %+v", calls)
	}
	args, _ := calls[0]["args"].(map[string]any)
	if args["method"] != "apps_list" {
		t.Fatalf("cto_op args: %+v", args)
	}
}

func TestCTOOpsListForwardsFilter(t *testing.T) {
	var mu sync.Mutex
	var calls []map[string]any
	cs := ctoOpTestSession(t, json.RawMessage(`{"count":1,"groups":[]}`), &calls, &mu)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "cto_ops_list", Arguments: map[string]any{"filter": "apps"}})
	if err != nil || res.IsError {
		t.Fatalf("cto_ops_list: %+v %v", res, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0]["method"] != "cto_ops_list" {
		t.Fatalf("daemon calls: %+v", calls)
	}
	args, _ := calls[0]["args"].(map[string]any)
	if args["filter"] != "apps" {
		t.Fatalf("filter: %+v", args)
	}
}
