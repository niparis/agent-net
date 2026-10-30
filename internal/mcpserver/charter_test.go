package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAgentToolsCarryCharter(t *testing.T) {
	cs := connect(t, func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		agent := `{"session_id":"s_test","charter":{"purpose":"fallback","purpose_fallback":true,"maturity":"mature","accepts":"bugs","refuses":"special","feature_requests":"ask"}}`
		if method == "list" {
			return json.RawMessage(`{"agents":[` + agent + `]}`), nil
		}
		return json.RawMessage(`{"agent":` + agent + `}`), nil
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
		for _, want := range []string{"fallback", "purpose_fallback", "mature", "bugs", "special", "feature_requests"} {
			if !strings.Contains(string(raw), want) {
				t.Fatalf("%s missing %s: %s", name, want, raw)
			}
		}
	}
}
