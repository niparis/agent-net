package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

func catalogToolT(t *testing.T, name string) toolcatalog.Tool {
	t.Helper()
	for _, tool := range toolcatalog.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("%s is not in the tool catalog", name)
	return toolcatalog.Tool{}
}

func resolvedSchema(t *testing.T, raw json.RawMessage) *jsonschema.Resolved {
	t.Helper()
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	res, err := schema.Resolve(&jsonschema.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func sameJSON(t *testing.T, what string, a, b any) bool {
	t.Helper()
	ra, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var va, vb any
	json.Unmarshal(ra, &va)
	json.Unmarshal(rb, &vb)
	ca, _ := json.Marshal(va)
	cb, _ := json.Marshal(vb)
	if string(ca) != string(cb) {
		t.Errorf("%s differs:\n got %s\nwant %s", what, ca, cb)
		return false
	}
	return true
}

var readToolOutputs = map[string]string{
	"sessions_find": `{"items":[{"id":"s_1","name":"s01","kind":"agent","machine":"research","project":"housing","harness":"claude","model":"standard","online":true,"status":"idle","task":"work on s01","last_seen":"2026-01-01T12:00:00Z","task_started":"2026-01-01T11:00:00Z"}],"total":1}`,
	"session_get":   `{"session":{"id":"s_1","name":"s01","kind":"agent","machine":"research","project":"housing","harness":"claude","model":"standard","online":true,"status":"idle","task":"work on s01","last_seen":"2026-01-01T12:00:00Z","task_started":null}}`,
	"machines_find": `{"items":[{"id":"n_1","name":"research","online":true,"version":"v0.3.2","harnesses":[{"name":"claude","version":"1.0"}],"sessions_online":3,"workers_running":1}],"total":1}`,
	"machine_get":   `{"machine":{"id":"n_1","name":"research","online":true,"version":"v0.3.2","harnesses":[{"name":"claude","version":"1.0"}],"sessions_online":3,"workers_running":1},"models":{"items":[{"harness":"claude","id":"claude-a"}],"total":1}}`,
	"projects_find": `{"items":[{"name":"housing","display_name":"","machine":"research","folder":"/work/housing","sessions_online":1,"sessions_total":1,"source":"seen"}],"total":1}`,
}

func readToolCaller(t *testing.T, calls *[]call) Caller {
	return func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var m map[string]any
		json.Unmarshal(raw, &m)
		if calls != nil {
			*calls = append(*calls, call{method, m})
		}
		out, ok := readToolOutputs[method]
		if !ok {
			t.Fatalf("unexpected daemon method %q", method)
		}
		return json.RawMessage(out), nil
	}
}

func TestReadToolsRegisteredFromCatalog(t *testing.T) {
	ctx := context.Background()
	cs := connect(t, readToolCaller(t, nil))
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool
	}
	for _, name := range []string{"agent_sessions_find", "agent_session_get", "agent_machines_find", "agent_machine_get", "agent_projects_find"} {
		entry := catalogToolT(t, name)
		got, ok := byName[name]
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		if got.Name != entry.Name || got.Description != entry.Description {
			t.Errorf("%s name or description differs from the catalog", name)
		}
		sameJSON(t, name+" input schema", got.InputSchema, entry.InputSchema)
		if got.OutputSchema != nil {
			t.Errorf("%s lists an output schema", name)
		}
		if entry.AlwaysLoad {
			if meta, _ := got.Meta["anthropic/alwaysLoad"].(bool); !meta {
				t.Errorf("%s is missing anthropic/alwaysLoad", name)
			}
		}
	}
}

func TestReadToolResultsMatchCatalogSchema(t *testing.T) {
	ctx := context.Background()
	cs := connect(t, readToolCaller(t, nil))
	cases := []struct {
		tool, method string
		args         map[string]any
	}{
		{"agent_sessions_find", "sessions_find", map[string]any{"machine": "research"}},
		{"agent_session_get", "session_get", map[string]any{"session": "s01"}},
		{"agent_machines_find", "machines_find", map[string]any{"online": "any"}},
		{"agent_machine_get", "machine_get", map[string]any{"machine": "research", "include": []string{"models"}}},
		{"agent_projects_find", "projects_find", map[string]any{"name": "housing"}},
	}
	for _, c := range cases {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: c.tool, Arguments: c.args})
		if err != nil {
			t.Fatalf("%s: %v", c.tool, err)
		}
		if res.IsError {
			t.Fatalf("%s returned an error: %+v", c.tool, res.Content)
		}
		if res.StructuredContent == nil {
			t.Fatalf("%s has no structuredContent", c.tool)
		}
		entry := catalogToolT(t, c.tool)
		if err := resolvedSchema(t, entry.OutputSchema).Validate(res.StructuredContent); err != nil {
			t.Fatalf("%s structuredContent does not match the catalog output schema: %v", c.tool, err)
		}
		var text string
		for _, content := range res.Content {
			if tc, ok := content.(*mcp.TextContent); ok && strings.HasPrefix(strings.TrimSpace(tc.Text), "{") {
				text = tc.Text
			}
		}
		if text == "" {
			t.Fatalf("%s has no JSON text block", c.tool)
		}
		var fromText any
		if err := json.Unmarshal([]byte(text), &fromText); err != nil {
			t.Fatalf("%s text block is not JSON: %v", c.tool, err)
		}
		sameJSON(t, c.tool+" text and structuredContent", fromText, res.StructuredContent)
	}
}

func TestReadToolsForwardFilters(t *testing.T) {
	var calls []call
	cs := connect(t, readToolCaller(t, &calls))
	ctx := context.Background()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_sessions_find", Arguments: map[string]any{
		"machine": "research", "project": "housing", "harness": "claude", "model": "terra",
		"kind": []string{"agent", "worker"}, "online": "offline", "detail": "full",
		"limit": 10, "cursor": "c1",
		"match_any": []any{map[string]any{"harness": "opencode"}, map[string]any{"model": "terra"}},
	}})
	if err != nil || res.IsError {
		t.Fatalf("agent_sessions_find: %v %+v", err, res)
	}
	p := calls[0].params
	if calls[0].method != "sessions_find" || p["machine"] != "research" || p["online"] != "offline" ||
		p["detail"] != "full" || p["limit"] != float64(10) || p["cursor"] != "c1" {
		t.Fatalf("sessions_find params: %+v", calls[0])
	}
	if len(p["match_any"].([]any)) != 2 {
		t.Fatalf("match_any not forwarded: %+v", p)
	}

	// The schema defaults are applied before the daemon sees the arguments.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_sessions_find", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("agent_sessions_find defaults: %v %+v", err, res)
	}
	p = calls[1].params
	if p["online"] != "online" || p["detail"] != "concise" || p["limit"] != float64(25) {
		t.Fatalf("schema defaults not applied: %+v", p)
	}
	kind, _ := p["kind"].([]any)
	if len(kind) != 2 || kind[0] != "agent" || kind[1] != "worker" {
		t.Fatalf("kind default: %+v", p["kind"])
	}

	calls = nil
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_machine_get", Arguments: map[string]any{
		"machine": "research", "include": []string{"track_record"}, "harness": "claude", "model_filter": "terra", "limit": 5,
	}})
	if err != nil || res.IsError {
		t.Fatalf("agent_machine_get: %v %+v", err, res)
	}
	p = calls[0].params
	if calls[0].method != "machine_get" || p["machine"] != "research" || p["model_filter"] != "terra" {
		t.Fatalf("machine_get params: %+v", calls[0])
	}
}

func TestMachineGetTwoCategoriesRefused(t *testing.T) {
	var calls []call
	cs := connect(t, readToolCaller(t, &calls))
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_machine_get", Arguments: map[string]any{
		"machine": "research", "include": []string{"models", "quotas"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("two include categories must be refused: %+v", res)
	}
	if len(calls) != 0 {
		t.Fatalf("the daemon was called anyway: %+v", calls)
	}
}

func TestCTOAvailabilityMatchesSessionOutputSchemas(t *testing.T) {
	session := map[string]any{
		"id": "s_cto", "name": "CTO", "kind": "agent", "machine": "test", "project": "", "harness": "cto", "model": "", "online": true,
		"status": "dormant", "availability": "wakes on message", "task": "", "last_seen": "", "task_started": nil,
	}
	for _, tc := range []struct {
		name   string
		output any
	}{
		{"agent_sessions_find", map[string]any{"items": []any{session}, "total": 1}},
		{"agent_session_get", map[string]any{"session": session}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := resolvedSchema(t, catalogToolT(t, tc.name).OutputSchema).Validate(tc.output); err != nil {
				t.Fatal(err)
			}
		})
	}
}
