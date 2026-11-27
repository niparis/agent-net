package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

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
		{"agent_worker_start", "worker_start", map[string]any{"project": "shop", "task": "do it", "isolation": "worktree"},
			func(p map[string]any) bool {
				return p["project"] == "shop" && (p["harness"] == "" || p["harness"] == nil) && (p["model"] == "" || p["model"] == nil) && p["isolation"] == "worktree" && p["task"] == "do it"
			}},
		{"agent_worker_start_override", "worker_start", map[string]any{"project": "shop", "task": "do it", "model": "a/b", "owner_words": "use a/b for this"},
			func(p map[string]any) bool {
				return p["project"] == "shop" && p["model"] == "a/b" && (p["harness"] == "" || p["harness"] == nil) && p["owner_words"] == "use a/b for this"
			}},
		{"agent_worker_stop", "worker_stop", map[string]any{"worker": "s_w1"}, func(p map[string]any) bool { return p["session"] == "s_w1" }},
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

func TestStartSendsProviderAndEffort(t *testing.T) {
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		if method == "worker_start" {
			p := params.(protocol.WorkerStartRequest)
			if p.Provider != "vendor" || p.Effort != "high" {
				t.Fatalf("fields dropped: %+v", p)
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

func TestStartAcceptedAnswerTellsTheCallerToWait(t *testing.T) {
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		if method == "worker_start" {
			return json.RawMessage(`{"status":"accepted","start_id":"a_1","note":"the start is accepted","project":"p"}`), nil
		}
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_worker_start", Arguments: map[string]any{"task": "x", "model": "x"}})
	text := ""
	if res != nil {
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text += tc.Text
			}
		}
	}
	if err != nil || res.IsError {
		t.Fatalf("accepted start: %s %v", text, err)
	}
	if !strings.Contains(text, `"accepted"`) || !strings.Contains(text, "a_1") || strings.Contains(text, `"worker"`) {
		t.Fatalf("accepted start answer: %s", text)
	}
}
