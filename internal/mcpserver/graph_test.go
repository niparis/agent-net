package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAgentGraphToolReadsThePublicGraph(t *testing.T) {
	ctx := context.Background()
	called := 0
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		if method != "agent_graph" {
			t.Fatalf("method = %q", method)
		}
		called++
		raw, _ := json.Marshal(params)
		if !strings.Contains(string(raw), `"node":"shop"`) {
			t.Fatalf("params = %s", raw)
		}
		return json.RawMessage(`{"nodes":[{"name":"shop","kind":"component","maturity":"mature","line":"shop (component) mature Sells things","charter":{"purpose":"Sells things"},"rules":[{"name":"deploy_rule","value":"owner"},{"name":"worker_sandbox","value":"no-network"}]}],"edges":[{"from":"shop","to":"billing","kind":"depends","rule":"ask"}],"total":1,"edge_total":1}`), nil
	})
	defer cs.Close()
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var tool *mcp.Tool
	for _, candidate := range list.Tools {
		if candidate.Name == "agent_graph" {
			tool = candidate
		}
	}
	if tool == nil {
		t.Fatal("agent_graph is not registered")
	}
	for _, want := range []string{"charter", "edge", "node", "offset", "total", "rule", "read-only"} {
		if !strings.Contains(strings.ToLower(tool.Description), want) {
			t.Errorf("description does not mention %q: %s", want, tool.Description)
		}
	}
	out, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_graph", Arguments: map[string]any{"node": "shop", "offset": 0, "limit": 5}})
	if err != nil || out.IsError {
		t.Fatalf("call: %+v %v", out, err)
	}
	if called != 1 {
		t.Fatalf("daemon called %d times", called)
	}
	text := ""
	for _, content := range out.Content {
		if item, ok := content.(*mcp.TextContent); ok {
			text += item.Text
		}
	}
	for _, want := range []string{"Sells things", "depends", "ask", "deploy_rule", "owner", "worker_sandbox", "no-network"} {
		if !strings.Contains(text, want) {
			t.Errorf("result misses %q: %s", want, text)
		}
	}
}

func TestCTOGraphHasAPublicReadSource(t *testing.T) {
	def, ok := ctoTools["graph"]
	if !ok {
		t.Fatal("the CTO has no graph tool")
	}
	for _, field := range []string{"node", "offset", "limit"} {
		if _, ok := def.fields[field]; !ok {
			t.Errorf("graph has no %s field", field)
		}
	}
	cs := ctoTestSession(t)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "graph" {
			if !strings.Contains(strings.ToLower(tool.Description), "charter") {
				t.Errorf("CTO graph description: %s", tool.Description)
			}
			return
		}
	}
	t.Fatal("the CTO does not offer graph")
}
