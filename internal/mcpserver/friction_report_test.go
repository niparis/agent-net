package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// A worker can file friction with no step. The tool sends the same entries to
// the daemon as a step report's friction field.
func TestFrictionReportToolCallsTheDaemon(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "1")
	var method string
	var input protocol.FrictionReportArgs
	cs := connectServer(t, func(_ context.Context, m string, p any) (json.RawMessage, error) {
		method = m
		b, _ := json.Marshal(p)
		_ = json.Unmarshal(b, &input)
		return json.RawMessage(`{"stored":1}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_friction_report",
		Arguments: map[string]any{
			"friction": []any{map[string]any{"area": "environment", "text": "the sandbox cannot bind a local port"}},
			"change":   "ch_1234",
		}})
	if err != nil || res.IsError {
		t.Fatalf("agent_friction_report: %v %+v", err, res)
	}
	if method != protocol.OpFrictionReport {
		t.Fatalf("agent_friction_report called %q", method)
	}
	if len(input.Friction) != 1 || input.Friction[0].Area != protocol.FrictionEnvironment || input.Change != "ch_1234" {
		t.Fatalf("friction lost: %+v", input)
	}
}
