package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The old list and get tools now read the tool redesign's session row (full
// detail), so they keep the charter fields that row carries.
func TestAgentToolsCarryCharter(t *testing.T) {
	row := `{"id":"s_test","name":"t","kind":"agent","machine":"mac","project":"agent-net","harness":"claude","model":"m","online":true,"status":"idle","task":"work","last_seen":"2023-11-14T22:13:20Z","task_started":null,"function":"fn","expertise":"exp","capabilities":["go"],"purpose":"fallback","rules":{"deploy_rule":"owner","worker_sandbox":"network"}}`
	cs := connect(t, func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		if method == "sessions_find" {
			return json.RawMessage(`{"items":[` + row + `],"total":1}`), nil
		}
		return json.RawMessage(`{"session":` + row + `}`), nil
	})
	for _, name := range []string{"agent_list", "agent_get"} {
		args := map[string]any{}
		if name == "agent_get" {
			args["session_id"] = "s_test"
		}
		r, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(r)
		for _, want := range []string{"fallback", "deploy_rule", "owner", "exp", `\"go\"`} {
			if !strings.Contains(string(raw), want) {
				t.Fatalf("%s missing %s: %s", name, want, raw)
			}
		}
	}
}

func TestAgentToolsCarryProject(t *testing.T) {
	row := `{"id":"s_test","name":"t","kind":"agent","machine":"mac","project":"agent-net","harness":"claude","model":"m","online":true,"status":"idle","task":"work","last_seen":"2023-11-14T22:13:20Z","task_started":null}`
	cs := connect(t, func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		if method == "sessions_find" {
			return json.RawMessage(`{"items":[` + row + `],"total":1}`), nil
		}
		return json.RawMessage(`{"session":` + row + `}`), nil
	})
	for _, name := range []string{"agent_list", "agent_get"} {
		args := map[string]any{}
		if name == "agent_get" {
			args["session_id"] = "s_test"
		}
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(res)
		if !strings.Contains(string(raw), "agent-net") {
			t.Fatalf("%s: %s", name, raw)
		}
	}
}
