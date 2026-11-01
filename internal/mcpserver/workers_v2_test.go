package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

func TestRedesignedWorkerAndOwnerTools(t *testing.T) {
	var calls []call
	cs := connect(t, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		calls = append(calls, call{method, p})
		return json.RawMessage(`{"accepted":true,"id":7,"status":"new","remaining":2,"item_id":"i_1","kind":"question","state":"open","recorded":true,"worker":"s_w","score":4,"severity":2,"delivered":true,"projects":[],"dry_run":true,"ok":true,"tools":[],"detail":"ok","profile":{"name":"n","project":"p","task":"t","expertise":"x","capabilities":[]}}`), nil
	})
	ctx := context.Background()
	cases := []struct {
		name, method string
		args         map[string]any
	}{
		{"agent_worker_start", "worker_start", map[string]any{"task": "do it", "touches": []string{"ui"}}},
		{"agent_worker_stop", "worker_stop", map[string]any{"worker": "s_w"}},
		{"agent_worker_rate", "agent_rate", map[string]any{"worker": "s_w", "kind": "rating", "score": 4}},
		{"agent_announce", "announce", map[string]any{"task": "working", "expertise": "Go"}},
		{"agent_learn", "agent_learn", map[string]any{"subject_kind": "model", "subject": "terra", "finding": "works"}},
		{"agent_project_add", "project_add", map[string]any{"paths": []string{"/tmp/demo"}}},
		{"agent_mcp_test", "mcp_test", map[string]any{"server": "demo"}},
		{"agent_owner_ask", "waiting", map[string]any{"kind": "question", "text": "Merge?", "recommendation": "yes"}},
		{"agent_owner_ask", "request_approval", map[string]any{"kind": "approval", "text": "deploy", "recommendation": "yes"}},
		{"agent_owner_ask", "propose_idea", map[string]any{"kind": "idea", "text": "feature", "recommendation": "yes"}},
	}
	for _, tc := range cases {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", tc.name, err, res)
		}
		if calls[len(calls)-1].method != tc.method {
			t.Errorf("%s called %s", tc.name, calls[len(calls)-1].method)
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
			if calls[len(calls)-1].params["task"] != "do it" || calls[len(calls)-1].params["touches"].([]any)[0] != "ui" {
				t.Errorf("worker_start args: %#v", calls[len(calls)-1].params)
			}
		case "agent_worker_stop":
			if calls[len(calls)-1].params["session"] != "s_w" {
				t.Errorf("worker_stop args: %#v", calls[len(calls)-1].params)
			}
		case "agent_worker_rate":
			if calls[len(calls)-1].params["kind"] != "rating" || calls[len(calls)-1].params["score"] != float64(4) {
				t.Errorf("rate args: %#v", calls[len(calls)-1].params)
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
	cs := connect(t, func(context.Context, string, any) (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
	listed, _ := cs.ListTools(context.Background(), nil)
	for _, tool := range listed.Tools {
		if tool.Name == "agent_worker_start" || tool.Name == "agent_worker_stop" || tool.Name == "agent_worker_rate" || tool.Name == "agent_owner_ask" || tool.Name == "agent_project_add" || tool.Name == "agent_announce" {
			t.Errorf("worker sees %s", tool.Name)
		}
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
