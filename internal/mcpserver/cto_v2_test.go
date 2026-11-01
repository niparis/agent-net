package mcpserver

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

func TestValidateCTOActionSchema(t *testing.T) {
	tests := []struct{ name, raw, want string }{
		{"graph_change", `{"action":"node_add","name":"x"}`, ""},
		{"graph_change", `{"action":"node_add","name":"x","attr":"purpose"}`, "attr"},
		{"session_change", `{"action":"session_start"}`, "project or path"},
		{"worker_change", `{"action":"start","project":"p"}`, "task"},
		{"worker_change", `{"action":"start","continue":"w1","machine":"other"}`, "machine"},
	}
	for _, tt := range tests {
		t.Run(tt.name+tt.raw, func(t *testing.T) {
			err := ValidateCTOActionSchema(tt.name, json.RawMessage(tt.raw))
			if tt.want == "" && err != nil {
				t.Fatal(err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestCTOV2CatalogSchemasAndStructuredResults(t *testing.T) {
	cs := ctoTestSession(t)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range toolcatalog.ForRole(toolcatalog.RoleCTO) {
		if !ctoV2Tools[entry.Name] {
			continue
		}
		var tool *mcp.Tool
		for _, got := range list.Tools {
			if got.Name == entry.Name {
				tool = got
				break
			}
		}
		if tool == nil {
			t.Fatalf("missing %s", entry.Name)
		}
		in, _ := json.Marshal(tool.InputSchema)
		out, _ := json.Marshal(tool.OutputSchema)
		var wantIn, wantOut any
		_ = json.Unmarshal(entry.InputSchema, &wantIn)
		_ = json.Unmarshal(entry.OutputSchema, &wantOut)
		wantInBytes, _ := json.Marshal(wantIn)
		wantOutBytes, _ := json.Marshal(wantOut)
		if !reflect.DeepEqual(in, wantInBytes) || !reflect.DeepEqual(out, wantOutBytes) {
			t.Errorf("%s schema differs from catalog", entry.Name)
		}
	}
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "graph_change", Arguments: map[string]any{"action": "node_add", "name": "example"}})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("structured graph result: %+v, %v", result, err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content = %#v", result.Content)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, result.StructuredContent) {
		t.Fatalf("text result %v differs from structured %v", got, result.StructuredContent)
	}
}

func TestCTOV2OutputsMatchCatalogSchemas(t *testing.T) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	srv := NewCTO(func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		switch method {
		case "ui_show":
			return json.RawMessage(`"shown"`), nil
		case "proposals":
			return json.RawMessage(`{"items":[],"total":0}`), nil
		default:
			return json.RawMessage(`{"state":"proposed"}`), nil
		}
	}, "test")
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	args := map[string]any{
		"graph_change":       map[string]any{"action": "node_add", "name": "x"},
		"session_change":     map[string]any{"action": "rename"},
		"worker_change":      map[string]any{"action": "stop"},
		"cto_ui_show":        map[string]any{"tab": "graph"},
		"cto_proposals_find": map[string]any{},
	}
	for name, input := range args {
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: input})
		if err != nil || result.IsError {
			t.Fatalf("%s: %+v %v", name, result, err)
		}
		var structured json.RawMessage
		structured, err = json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var schema jsonschema.Schema
		for _, entry := range toolcatalog.ForRole(toolcatalog.RoleCTO) {
			if entry.Name == name {
				if err := json.Unmarshal(entry.OutputSchema, &schema); err != nil {
					t.Fatal(err)
				}
				break
			}
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(structured, &value); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(value); err != nil {
			t.Errorf("%s output %s: %v", name, structured, err)
		}
		if len(result.Content) != 1 || result.Content[0].(*mcp.TextContent).Text != string(structured) {
			t.Errorf("%s text and structured output differ", name)
		}
	}
}

func TestCTOV2EveryActionValidates(t *testing.T) {
	cases := []struct{ tool, args string }{
		{"graph_change", `{"action":"charter_set"}`}, {"graph_change", `{"action":"node_add"}`}, {"graph_change", `{"action":"node_remove"}`}, {"graph_change", `{"action":"edge_set"}`}, {"graph_change", `{"action":"edge_remove"}`}, {"graph_change", `{"action":"settings_set"}`}, {"graph_change", `{"action":"undo"}`},
		{"session_change", `{"action":"rename"}`}, {"session_change", `{"action":"trust"}`}, {"session_change", `{"action":"policy"}`}, {"session_change", `{"action":"session_start","project":"example"}`},
		{"worker_change", `{"action":"start","project":"example","task":"work","touches":["docs"]}`}, {"worker_change", `{"action":"stop"}`},
	}
	for _, tc := range cases {
		if err := ValidateCTOActionSchema(tc.tool, json.RawMessage(tc.args)); err != nil {
			t.Errorf("%s %s: %v", tc.tool, tc.args, err)
		}
	}
}

func TestCTOV2ActionArgumentsReachExistingHandler(t *testing.T) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	type observed struct {
		method string
		args   map[string]any
	}
	var calls []observed
	srv := NewCTO(func(_ context.Context, method string, params any) (json.RawMessage, error) {
		args, _ := params.(json.RawMessage)
		var m map[string]any
		_ = json.Unmarshal(args, &m)
		calls = append(calls, observed{method, m})
		return json.RawMessage(`{"state":"proposed"}`), nil
	}, "test")
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"graph_change", map[string]any{"action": "charter_set", "node": "x", "attr": "purpose", "value": "v"}},
		{"graph_change", map[string]any{"action": "node_add", "name": "x"}}, {"graph_change", map[string]any{"action": "node_remove", "name": "x"}},
		{"graph_change", map[string]any{"action": "edge_set", "from": "a", "to": "b", "kind": "depends", "mode": "advisor", "rule": "normal"}},
		{"graph_change", map[string]any{"action": "edge_remove", "from": "a", "to": "b", "kind": "depends"}},
		{"graph_change", map[string]any{"action": "settings_set", "node": "a", "setting": "autonomy", "value": "owner"}}, {"graph_change", map[string]any{"action": "undo"}},
		{"session_change", map[string]any{"action": "rename", "session_id": "s", "name": "x"}}, {"session_change", map[string]any{"action": "trust", "session_id": "s", "on": true}},
		{"session_change", map[string]any{"action": "policy", "session_id": "s", "scope": "session", "dry_run": true}}, {"session_change", map[string]any{"action": "session_start", "project": "p"}},
		{"worker_change", map[string]any{"action": "start", "project": "p", "task": "t", "touches": []string{"docs"}}}, {"worker_change", map[string]any{"action": "stop", "session": "w"}},
	}
	for _, tc := range cases {
		out, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
		if err != nil || out.IsError {
			t.Fatalf("%s %v: %+v %v", tc.tool, tc.args, out, err)
		}
	}
	if len(calls) != len(cases) {
		t.Fatalf("got %d calls for %d actions", len(calls), len(cases))
	}
	for i, tc := range cases {
		if calls[i].method != tc.tool {
			t.Errorf("route %d: method %q", i, calls[i].method)
		}
		for k, v := range tc.args {
			if !reflect.DeepEqual(calls[i].args[k], v) { // JSON normalizes integer/slice values.
				if k == "touches" {
					continue
				}
				t.Errorf("route %d argument %s = %v, want %v", i, k, calls[i].args[k], v)
			}
		}
	}
}
