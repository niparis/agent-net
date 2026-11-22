package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

// The four reads that filter, page or fetch run in the daemon. The MCP handler
// makes one call to the tool's catalog method with the input it was given, and
// nothing else: no whoami, no list, no paging of its own.
func TestReadToolsForwardOneCallToTheCatalogMethod(t *testing.T) {
	plugin, err := toolcatalog.PluginTools()
	if err != nil {
		t.Fatal(err)
	}
	methods := map[string]string{}
	for _, p := range plugin {
		methods[p.Name] = p.Method
	}
	cases := []struct {
		tool, field, marker string
		answer              string
	}{
		{"agent_messages_find", "with", "bob", `{"items":[],"total":0}`},
		{"agent_workers_find", "project", "alpha", `{"items":[],"total":0}`},
		{"agent_mcp_servers_find", "limit", "7", `{"items":[],"total":0}`},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			var sent []string
			var got map[string]any
			cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
				sent = append(sent, method)
				raw, _ := json.Marshal(params)
				_ = json.Unmarshal(raw, &got)
				return json.RawMessage(tc.answer), nil
			})
			var value any = tc.marker
			if tc.field == "limit" {
				value = 7
			}
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.tool, Arguments: map[string]any{tc.field: value}})
			if err != nil || res.IsError {
				t.Fatalf("%s: %v %+v", tc.tool, err, res)
			}
			if len(sent) != 1 || sent[0] != methods[tc.tool] {
				t.Fatalf("daemon calls %v, want one call to %s", sent, methods[tc.tool])
			}
			if got[tc.field] == nil || got[tc.field] == "" {
				t.Fatalf("the input %s did not reach the daemon: %v", tc.field, got)
			}
		})
	}
}
