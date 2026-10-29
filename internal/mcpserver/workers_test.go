package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWorkerToolsForwardToDaemon(t *testing.T) {
	var calls []call
	cs := connect(t, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var m map[string]any
		json.Unmarshal(raw, &m)
		calls = append(calls, call{method, m})
		return json.RawMessage(`{"ok":true}`), nil
	})
	ctx := context.Background()
	for _, c := range []struct {
		tool, method string
		args         map[string]any
		check        func(map[string]any) bool
	}{
		{"agent_start", "worker_start", map[string]any{"project": "shop", "task": "do it", "model": "a/b", "isolation": "worktree"},
			func(p map[string]any) bool {
				return p["project"] == "shop" && p["harness"] == "opencode" && p["model"] == "a/b" && p["isolation"] == "worktree" && p["task"] == "do it"
			}},
		{"agent_stop", "worker_stop", map[string]any{"session": "s_w"}, func(p map[string]any) bool { return p["session"] == "s_w" }},
		{"agent_handback", "worker_handback", map[string]any{"report": "done", "status": "blocked"},
			func(p map[string]any) bool { return p["report"] == "done" && p["status"] == "blocked" }},
	} {
		n := len(calls)
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: c.tool, Arguments: c.args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", c.tool, err, res)
		}
		if len(calls) != n+1 || calls[n].method != c.method || !c.check(calls[n].params) {
			t.Errorf("%s: %+v", c.tool, calls[n:])
		}
	}
	// None of the tools can name a sender, an owner or a recipient: the daemon
	// takes the caller from the connection.
	tools, _ := cs.ListTools(ctx, nil)
	for _, tl := range tools.Tools {
		if !strings.HasPrefix(tl.Name, "agent_start") && tl.Name != "agent_handback" && tl.Name != "agent_stop" {
			continue
		}
		b, _ := json.Marshal(tl.InputSchema)
		var sch struct {
			Properties map[string]any `json:"properties"`
		}
		json.Unmarshal(b, &sch)
		for _, bad := range []string{"owner", "sender", "from", "to", "recipient", "owner_session"} {
			if _, ok := sch.Properties[bad]; ok {
				t.Errorf("%s exposes %q: %s", tl.Name, bad, b)
			}
		}
	}
}
