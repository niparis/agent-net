package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWorkerStartAndNodesShowTrackAndPlaybook(t *testing.T) {
	cs := connect(t, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		switch method {
		case "worker_start":
			return json.RawMessage(`{"status":"started","session_id":"s_w","harness":"opencode","model":"a/x","project":"p","hostname":"mac","permissions":"sandboxed","track":{"harness":"opencode","model":"a/x","runs":3,"complete":1,"partial":2,"median_run_seconds":60,"resumable":true},"playbook":{"model":"a/x","note":"ask for the diff"}}`), nil
		case "nodes":
			return json.RawMessage(`{"nodes":[{"node_id":"n_1","hostname":"mac","online":true,"version":"v1","harnesses":[],"tracks":[{"harness":"opencode","model":"a/x","runs":14,"complete":13,"median_run_seconds":1560,"resumable":true}]}]}`), nil
		}
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_start",
		Arguments: map[string]any{"project": "p", "task": "t"}})
	if err != nil || res.IsError || len(res.Content) == 0 {
		t.Fatalf("agent_start: %v %+v", err, res)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	for _, want := range []string{"a/x · opencode: 3 runs: 1 complete, 2 partial (median 1 min) · resumes", "playbook: ask for the diff"} {
		if !strings.Contains(text, want) {
			t.Errorf("agent_start output lacks %q:\n%s", want, text)
		}
	}
	nres, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_nodes"})
	if err != nil || nres.IsError || len(nres.Content) == 0 {
		t.Fatalf("agent_nodes: %v %+v", err, nres)
	}
	text = nres.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "mac: a/x · opencode: 14 runs: 13 complete (median 26 min) · resumes") {
		t.Errorf("agent_nodes output lacks the track line:\n%s", text)
	}
}

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
		{"agent_handback", "worker_handback", map[string]any{"report": "done", "status": "blocked", "complete": false},
			func(p map[string]any) bool {
				return p["report"] == "done" && p["status"] == "blocked" && p["complete"] == false
			}},
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
