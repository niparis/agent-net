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
			return json.RawMessage(`{"status":"started","session_id":"s_w","harness":"opencode","model":"a/x","project":"p","hostname":"mac","permissions":"sandboxed","track":{"harness":"opencode","model":"a/x","runs":3,"complete":1,"partial":2,"median_run_seconds":60,"resumable":true},"playbooks":[{"subject_kind":"model","subject":"a/x","note":"ask for the diff"}]}`), nil
		case "nodes":
			return json.RawMessage(`{"nodes":[{"node_id":"n_1","hostname":"mac","online":true,"version":"v1","harnesses":[],"tracks":[{"harness":"opencode","model":"a/x","runs":14,"complete":13,"median_run_seconds":1560,"resumable":true}]}],"playbook":[{"subject_kind":"model","subject":"a/x","note":"ask for the diff","support":3,"source":"auto"}]}`), nil
		}
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_start",
		Arguments: map[string]any{"project": "p", "task": "t"}})
	if err != nil || res.IsError || len(res.Content) == 0 {
		t.Fatalf("agent_start: %v %+v", err, res)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	for _, want := range []string{"a/x · opencode: 3 runs: 1 complete, 2 partial (median 1 min) · resumes", "playbook [a/x]: ask for the diff"} {
		if !strings.Contains(text, want) {
			t.Errorf("agent_start output lacks %q:\n%s", want, text)
		}
	}
	nres, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_nodes"})
	if err != nil || nres.IsError || len(nres.Content) == 0 {
		t.Fatalf("agent_nodes: %v %+v", err, nres)
	}
	text = nres.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "mac: a/x · opencode: 14 runs: 13 complete (median 26 min) · resumes") ||
		!strings.Contains(text, "playbook [a/x] 3 findings: ask for the diff") {
		t.Errorf("agent_nodes output lacks the track or playbook line:\n%s", text)
	}
}

func TestAgentLearnShowsTheResultAndTheDailyLimit(t *testing.T) {
	accepted := true
	cs := connect(t, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		if accepted {
			return json.RawMessage(`{"accepted":true,"id":7,"status":"new","remaining":2}`), nil
		}
		return json.RawMessage(`{"accepted":false,"reason":"at most 3 findings per session per day","remaining":0}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_learn",
		Arguments: map[string]any{"subject_kind": "model", "subject": "terra", "finding": "Hands back partial work."}})
	if err != nil || res.IsError || len(res.Content) == 0 {
		t.Fatalf("agent_learn: %v %+v", err, res)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "Recorded finding 7") || !strings.Contains(text, "2 left today") {
		t.Errorf("learn text: %q", text)
	}
	accepted = false
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_learn",
		Arguments: map[string]any{"subject_kind": "model", "subject": "terra", "finding": "again"}})
	if err != nil || res.IsError || len(res.Content) == 0 {
		t.Fatalf("refused agent_learn: %v %+v", err, res)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "Not recorded: at most 3 findings") {
		t.Errorf("refusal text: %q", text)
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
		{"agent_learn", "agent_learn", map[string]any{"subject_kind": "model_harness", "subject": "terra@pi",
			"finding": "Hands back partial work.", "evidence": []any{"s_w1", "s_w2"}},
			func(p map[string]any) bool {
				return p["subject_kind"] == "model_harness" && p["subject"] == "terra@pi" &&
					p["finding"] == "Hands back partial work." && len(p["evidence"].([]any)) == 2
			}},
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
