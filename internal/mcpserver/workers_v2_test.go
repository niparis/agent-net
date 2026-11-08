package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

func TestRedesignedWorkerAndOwnerTools(t *testing.T) {
	var calls []call
	cs := connect(t, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		calls = append(calls, call{method, p})
		return json.RawMessage(`{"accepted":true,"id":7,"status":"new","remaining":2,"item_id":"i_1","kind":"question","state":"open","recorded":true,"worker":"s_w","score":4,"severity":2,"delivered":true,"projects":[],"dry_run":true,"ok":true,"tools":[],"detail":"ok","profile":{"name":"n","function":"f","project":"p","capabilities":[],"task":"t","expertise":"x"}}`), nil
	})
	ctx := context.Background()
	cases := []struct {
		name, method string
		args         map[string]any
	}{
		{"agent_worker_start", "worker_start", map[string]any{"task": "do it", "title": "Parser fix", "touches": []string{"ui"}, "not_family": []string{"openai"}, "fixes": "ch_shipped", "fixes_severity": 3}},
		{"agent_worker_stop", "worker_stop", map[string]any{"worker": "s_w"}},
		{"agent_worker_rate", "agent_rate", map[string]any{"worker": "s_w", "kind": "rating", "score": 4}},
		{"agent_announce", "announce", map[string]any{"task": "working", "expertise": "Go"}},
		{"agent_learn", "agent_learn", map[string]any{"subject_kind": "model", "subject": "terra", "finding": "works"}},
		{"agent_project_add", "project_add", map[string]any{"paths": []string{"/tmp/demo"}}},
		{"agent_mcp_test", "mcp_test", map[string]any{"server": "demo"}},
		{"agent_owner_ask", "ask_question", map[string]any{"kind": "question", "text": "Merge?", "recommendation": "yes"}},
		{"agent_owner_ask", "request_approval", map[string]any{"kind": "approval", "text": "deploy", "recommendation": "yes"}},
		{"agent_owner_ask", "propose_idea", map[string]any{"kind": "idea", "text": "feature", "recommendation": "yes"}},
	}
	for _, tc := range cases {
		first := len(calls)
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", tc.name, err, res)
		}
		if len(calls) == first || calls[first].method != tc.method {
			t.Errorf("%s called %v, want %s first", tc.name, fmt.Sprint(calls[first:]), tc.method)
		}
		output, _ := res.StructuredContent.(map[string]any)
		if output == nil {
			t.Errorf("%s has no structured output", tc.name)
		}
		if len(res.Content) == 0 || res.Content[0].(*mcp.TextContent).Text != mustJSON(output) {
			t.Errorf("%s text is not the structured JSON", tc.name)
		}
		if entry := findCatalogTool(tc.name); entry != nil && len(entry.OutputSchema) > 0 {
			var schema struct {
				Required []string `json:"required"`
			}
			_ = json.Unmarshal(entry.OutputSchema, &schema)
			for _, field := range schema.Required {
				if _, ok := output[field]; !ok {
					t.Errorf("%s output misses required %q: %#v", tc.name, field, output)
				}
			}
		}
		switch tc.name {
		case "agent_worker_start":
			if calls[first].params["task"] != "do it" || calls[first].params["title"] != "Parser fix" || calls[first].params["touches"].([]any)[0] != "ui" || calls[first].params["not_family"].([]any)[0] != "openai" || calls[first].params["fixes"] != "ch_shipped" || calls[first].params["fixes_severity"] != float64(3) {
				t.Errorf("worker_start args: %#v", calls[first].params)
			}
		case "agent_worker_stop":
			if calls[first].params["session"] != "s_w" {
				t.Errorf("worker_stop args: %#v", calls[first].params)
			}
		case "agent_worker_rate":
			if calls[first].params["kind"] != "rating" || calls[first].params["score"] != float64(4) {
				t.Errorf("rate args: %#v", calls[first].params)
			}
		case "agent_owner_ask":
			if tc.args["kind"] == "question" && (calls[len(calls)-1].params["kind"] != "question" || calls[len(calls)-1].params["text"] != "Merge?") {
				t.Errorf("owner question args: %#v", calls[len(calls)-1].params)
			}
		}

		entry := findCatalogTool(tc.name)
		if entry == nil {
			t.Fatalf("missing catalog entry %s", tc.name)
		}
		listed, _ := cs.ListTools(ctx, nil)
		for _, tool := range listed.Tools {
			if tool.Name == tc.name {
				in, _ := json.Marshal(tool.InputSchema)
				var got, want any
				_ = json.Unmarshal(in, &got)
				_ = json.Unmarshal(entry.InputSchema, &want)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s input differs from catalog", tc.name)
				}
				out, _ := json.Marshal(tool.OutputSchema)
				if tool.OutputSchema != nil {
					t.Errorf("%s lists an output schema: %s", tc.name, out)
				}
				break
			}
		}
	}
}

func TestRedesignedToolValidations(t *testing.T) {
	cs := connect(t, func(context.Context, string, any) (json.RawMessage, error) {
		t.Fatal("invalid call forwarded")
		return nil, nil
	})
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"agent_worker_start", map[string]any{"task": "x", "continue": "s", "touches": []string{"ui"}}},
		{"agent_worker_rate", map[string]any{"worker": "s", "kind": "rating"}},
		{"agent_worker_rate", map[string]any{"worker": "s", "kind": "defect_found_later", "note": "bug"}},
	} {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		if err != nil || !res.IsError {
			t.Errorf("%s args=%v should be refused: %v %+v", tc.name, tc.args, err, res)
		}
	}
}

func TestWorkerRoleDoesNotSeeDelegationTools(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "w_test")
	cs := connectServer(t, func(context.Context, string, any) (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
	listed, _ := cs.ListTools(context.Background(), nil)
	for _, tool := range listed.Tools {
		if tool.Name == "agent_worker_start" || tool.Name == "agent_worker_stop" || tool.Name == "agent_worker_rate" || tool.Name == "agent_owner_ask" || tool.Name == "agent_project_add" || tool.Name == "agent_announce" {
			t.Errorf("worker sees %s", tool.Name)
		}
	}
}

// When the team lead takes the question, the tool result says so and tells the
// asker to carry on.
func TestOwnerAskSentToTheTeamLeadSaysSo(t *testing.T) {
	note := "Your team (forge) sends questions to its lead, tenebrae. Sent there; carry on with work that does not need the answer."
	cs := connect(t, func(context.Context, string, any) (json.RawMessage, error) {
		return json.RawMessage(`{"item":{"id":"i_9","kind":"question","status":"open"},"redirected":{"team":"forge","lead":"tenebrae","item_id":"i_9","message_id":"m_1","note":"` + note + `"}}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_owner_ask", Arguments: map[string]any{"kind": "question", "text": "Can I?", "recommendation": "yes"}})
	if err != nil || res.IsError {
		t.Fatalf("%v %+v", err, res)
	}
	out, _ := res.StructuredContent.(map[string]any)
	if out["state"] != "sent_to_lead" || out["note"] != note || out["item_id"] != "i_9" || out["kind"] != "question" {
		t.Fatalf("result: %#v", out)
	}
}

func TestRedesignedOwnerKindsAndTextJSON(t *testing.T) {
	cs := connect(t, func(context.Context, string, any) (json.RawMessage, error) {
		return json.RawMessage(`{"item_id":"i_1","kind":"question","state":"open"}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_owner_ask", Arguments: map[string]any{"kind": "question", "text": "Can I?", "recommendation": "yes"}})
	if err != nil || res.IsError {
		t.Fatalf("%v %+v", err, res)
	}
	if res.StructuredContent == nil {
		t.Fatal("missing structured output")
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, `"item_id":"i_1"`) {
		t.Fatalf("missing JSON text: %+v", res.Content)
	}
}

// agent_worker_start returns the worker record with the caller that started
// it, as agent_workers_find shows that worker.
func TestAgentWorkerStartFillsTheCaller(t *testing.T) {
	worker := protocol.AgentView{
		SessionID: "s_w1", Hostname: "research", Harness: "opencode", Model: "deepseek-v4.1-flash",
		Online: true, Profile: protocol.Profile{Name: "worker-one"},
		Worker: &protocol.WorkerInfo{ID: "w_1", Owner: "s_me", Project: "shop", Task: "do it", Title: "Parser fix",
			StartedAt: 1700000000, DifficultyUsed: "normal"},
	}
	list, _ := json.Marshal(protocol.AgentsResult{Agents: []protocol.AgentView{worker}})
	cs := connect(t, func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		switch method {
		case "worker_start":
			return json.RawMessage(`{"status":"started","session_id":"s_w1","harness":"opencode","model":"deepseek-v4.1-flash","project":"shop","hostname":"research","permissions":"sandboxed","owner_line":"Tell your user: worker on deepseek-v4.1-flash, difficulty normal (D 2.60; R3 S2 C3 U2 V2 K2).","reminder":"You named the model."}`), nil
		case "list":
			return list, nil
		}
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_worker_start",
		Arguments: map[string]any{"task": "do it", "title": "Parser fix", "touches": []string{"ui"}}})
	if err != nil || res.IsError {
		t.Fatalf("agent_worker_start: %v %+v", err, res)
	}
	out, _ := res.StructuredContent.(map[string]any)
	got, _ := out["worker"].(map[string]any)
	caller, _ := got["caller"].(map[string]any)
	if caller["id"] != "s_me" || caller["name"] != "s_me" {
		t.Fatalf("caller: %+v", caller)
	}
	if got["id"] != "s_w1" || got["project"] != "shop" || got["state"] != "running" || got["title"] != "Parser fix" {
		t.Fatalf("worker: %+v", got)
	}
	if line, _ := out["owner_line"].(string); !strings.HasPrefix(line, "Tell your user: worker on deepseek-v4.1-flash, difficulty normal") {
		t.Fatalf("owner line: %q", line)
	}
	if r, _ := out["reminder"].(string); r != "You named the model." {
		t.Fatalf("reminder: %q", r)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return fmt.Sprint(string(b)) }

func findCatalogTool(name string) *toolcatalog.Tool {
	for i := range toolcatalog.Tools {
		if toolcatalog.Tools[i].Name == name {
			return &toolcatalog.Tools[i]
		}
	}
	return nil
}

// A question returns no inbox item from the daemon. The tool must still answer
// with a valid result, or agents ask again.
func TestOwnerAskQuestionWithoutItemIsValid(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "")
	cs := connect(t, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_owner_ask", Arguments: map[string]any{"kind": "question", "text": "merge?", "recommendation": "yes"}})
	if err != nil || res.IsError {
		t.Fatalf("owner_ask question: %v %+v", err, res)
	}
}

// A worker started since the difficulty judge carries an assessment. Rating it
// returns the short worker record, which declares no assessment field, so the
// tool must not pass it through (v0.4.95: "unexpected additional properties").
func TestAgentWorkerRateLeavesOutTheAssessment(t *testing.T) {
	worker := protocol.AgentView{
		SessionID: "s_w1", Hostname: "research", Harness: "codex", Model: "gpt-6.1-sol:low",
		Profile: protocol.Profile{Name: "worker-one"},
		Worker: &protocol.WorkerInfo{ID: "w_1", Owner: "s_me", Project: "shop", Task: "do it", Title: "Skills",
			StartedAt: 1700000000, DifficultyUsed: "normal", Assessment: &protocol.WorkerAssessment{Label: "normal", D: 2.7}},
	}
	list, _ := json.Marshal(protocol.AgentsResult{Agents: []protocol.AgentView{worker}})
	cs := connect(t, func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		switch method {
		case protocol.OpRate:
			return json.RawMessage(`{"accepted":true,"worker":"s_w1","model":"gpt-6.1-sol:low","harness":"codex","difficulty":"normal"}`), nil
		case "list":
			return list, nil
		}
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_worker_rate",
		Arguments: map[string]any{"worker": "s_w1", "kind": "rating", "score": 4}})
	if err != nil || res.IsError {
		t.Fatalf("agent_worker_rate: %v %+v", err, res)
	}
}
