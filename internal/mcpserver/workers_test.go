package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

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
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, `"recorded":true`) || !strings.Contains(text, `"id":"7"`) || !strings.Contains(text, `"left_today":2`) {
		t.Errorf("learn JSON output: %q", text)
	}
	accepted = false
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_learn",
		Arguments: map[string]any{"subject_kind": "model", "subject": "terra", "finding": "again"}})
	if err != nil || res.IsError || len(res.Content) == 0 {
		t.Fatalf("refused agent_learn: %v %+v", err, res)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, `"recorded":false`) || !strings.Contains(text, `"left_today":0`) {
		t.Errorf("learn refusal JSON output: %q", text)
	}
}

func TestWorkerToolsForwardToDaemon(t *testing.T) {
	var calls []call
	worker := protocol.AgentView{SessionID: "s_w1", Hostname: "mac", Harness: "opencode", Model: "a/b",
		Online: true, Profile: protocol.Profile{Name: "worker-one"},
		Worker: &protocol.WorkerInfo{ID: "w_1", Owner: "s_me", Project: "shop", Task: "do it", StartedAt: 1700000000}}
	list, _ := json.Marshal(protocol.AgentsResult{Agents: []protocol.AgentView{worker}})
	cs := connect(t, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var m map[string]any
		json.Unmarshal(raw, &m)
		calls = append(calls, call{method, m})
		switch method {
		case "worker_start":
			return json.RawMessage(`{"status":"started","session_id":"s_w1","harness":"opencode","model":"a/b","project":"shop","hostname":"mac"}`), nil
		case "worker_stop":
			return json.RawMessage(`{"session":"s_w1","stopped":true}`), nil
		case "agent_rate":
			return json.RawMessage(`{"accepted":true,"worker":"s_w1","score":4,"model":"a/b","difficulty":"hard"}`), nil
		case "list":
			return list, nil
		}
		return json.RawMessage(`{"ok":true,"accepted":true,"id":1,"remaining":2}`), nil
	})
	ctx := context.Background()
	for _, c := range []struct {
		tool, method string
		args         map[string]any
		check        func(map[string]any) bool
	}{
		{"agent_worker_start", "worker_start", map[string]any{"project": "shop", "task": "do it", "model": "a/b", "difficulty": "hard", "touches": []string{"db", "tests"}, "isolation": "worktree"},
			func(p map[string]any) bool {
				return len(p["touches"].([]any)) == 2 && p["project"] == "shop" && p["harness"] == "opencode" && p["model"] == "a/b" && p["difficulty"] == "hard" && p["isolation"] == "worktree" && p["task"] == "do it"
			}},
		{"agent_worker_stop", "worker_stop", map[string]any{"worker": "s_w1"}, func(p map[string]any) bool { return p["session"] == "s_w1" }},
		{"agent_learn", "agent_learn", map[string]any{"subject_kind": "model_harness", "subject": "terra@pi",
			"finding": "Hands back partial work.", "evidence": []any{"s_w1", "s_w2"}},
			func(p map[string]any) bool {
				return p["subject_kind"] == "model_harness" && p["subject"] == "terra@pi" &&
					p["finding"] == "Hands back partial work." && len(p["evidence"].([]any)) == 2
			}},
		{"agent_worker_rate", "agent_rate", map[string]any{"worker": "s_w1", "kind": "rating", "score": 4, "note": "the diff was right"},
			func(p map[string]any) bool {
				return p["worker"] == "s_w1" && p["score"] == float64(4) && p["note"] == "the diff was right"
			}},
	} {
		n := len(calls)
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: c.tool, Arguments: c.args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", c.tool, err, res)
		}
		found := false
		for _, got := range calls[n:] {
			if got.method == c.method && c.check(got.params) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: %+v", c.tool, calls[n:])
		}
	}
	// None of the tools can name a sender, an owner or a recipient: the daemon
	// takes the caller from the connection.
	tools, _ := cs.ListTools(ctx, nil)
	for _, tl := range tools.Tools {
		if !strings.HasPrefix(tl.Name, "agent_worker_start") && tl.Name != "agent_handback" && tl.Name != "agent_worker_stop" {
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

func TestWorkerProfilesListToolReturnsRoster(t *testing.T) {
	cs := connect(t, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		if method == "worker_profiles" {
			return json.RawMessage(`[{"name":"demo","harness":"codex","model":"gpt","score":42.5,"score_source":"owner"}]`), nil
		}
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_worker_profiles_find"})
	if err != nil || res.IsError || len(res.Content) == 0 || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "demo") {
		t.Fatalf("profiles: %+v %v", res, err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	for _, want := range []string{`"score":42.5`, `"score_source":"owner"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("profile row lost %s: %s", want, text)
		}
	}
}

func TestStartWithoutTouches(t *testing.T) {
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		if method == "worker_start" {
			p := params.(protocol.WorkerStartRequest)
			if p.Provider != "vendor" || p.Effort != "high" {
				t.Fatalf("fields dropped: %+v", p)
			}
			if len(p.Touches) != 0 {
				t.Fatalf("touches was sent: %+v", p)
			}
			return json.RawMessage(`{"status":"starting","session_id":"s_w","harness":"opencode","model":"a/x","project":"p"}`), nil
		}
		if method == "list" {
			return json.RawMessage(`{"agents":[]}`), nil
		}
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_worker_start", Arguments: map[string]any{"task": "x", "provider": "vendor", "model": "x", "effort": "high"}})
	if err != nil || res.IsError {
		t.Fatalf("start without touches: %+v %v", res, err)
	}
}
