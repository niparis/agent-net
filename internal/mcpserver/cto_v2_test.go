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
		var wantIn any
		_ = json.Unmarshal(entry.InputSchema, &wantIn)
		wantInBytes, _ := json.Marshal(wantIn)
		if !reflect.DeepEqual(in, wantInBytes) {
			t.Errorf("%s input schema differs from catalog", entry.Name)
		}
		if tool.OutputSchema != nil {
			t.Errorf("%s lists an output schema", entry.Name)
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
		"proposal_ask":       map[string]any{"id": "p_15"},
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
		{"graph_change", `{"action":"team_create","name":"team"}`}, {"graph_change", `{"action":"team_rename","team":"team","name":"new"}`}, {"graph_change", `{"action":"team_purpose","team":"team","purpose":"new purpose"}`}, {"graph_change", `{"action":"team_archive","team":"team"}`}, {"graph_change", `{"action":"team_member_add","team":"team","member":{"kind":"agent","id":"a"}}`}, {"graph_change", `{"action":"team_member_remove","team":"team","member":{"kind":"agent","id":"a"}}`},
		{"session_change", `{"action":"rename"}`}, {"session_change", `{"action":"trust"}`}, {"session_change", `{"action":"policy"}`}, {"session_change", `{"action":"session_start","project":"example"}`},
		{"worker_change", `{"action":"start","project":"example","task":"work","touches":["docs"]}`}, {"worker_change", `{"action":"stop"}`},
	}
	for _, tc := range cases {
		if err := ValidateCTOActionSchema(tc.tool, json.RawMessage(tc.args)); err != nil {
			t.Errorf("%s %s: %v", tc.tool, tc.args, err)
		}
	}
}

func TestCTOV2GraphTeamActionsAcceptProposalReason(t *testing.T) {
	for _, args := range []string{
		`{"action":"team_create","name":"team","reason":"group related work"}`,
		`{"action":"team_rename","team":"team","name":"new","reason":"clearer name"}`,
		`{"action":"team_purpose","team":"team","purpose":"new purpose","reason":"scope changed"}`,
		`{"action":"team_archive","team":"team","reason":"team is done"}`,
		`{"action":"team_member_add","team":"team","member":{"kind":"agent","id":"a"},"reason":"assign ownership"}`,
		`{"action":"team_member_remove","team":"team","member":{"kind":"agent","id":"a"},"reason":"assignment ended"}`,
	} {
		if err := ValidateCTOActionSchema("graph_change", json.RawMessage(args)); err != nil {
			t.Errorf("%s: %v", args, err)
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
		{"graph_change", map[string]any{"action": "edge_set", "from": "a", "to": "b", "kind": "depends", "rule": "normal"}},
		{"graph_change", map[string]any{"action": "edge_remove", "from": "a", "to": "b", "kind": "depends"}},
		{"graph_change", map[string]any{"action": "settings_set", "node": "a", "setting": "autonomy", "value": "owner"}}, {"graph_change", map[string]any{"action": "undo"}},
		{"graph_change", map[string]any{"action": "team_create", "name": "t", "purpose": "p"}},
		{"graph_change", map[string]any{"action": "team_rename", "team": "t", "name": "new"}},
		{"graph_change", map[string]any{"action": "team_purpose", "team": "t", "purpose": "new purpose"}},
		{"graph_change", map[string]any{"action": "team_archive", "team": "t"}},
		{"graph_change", map[string]any{"action": "team_member_add", "team": "t", "member": map[string]any{"kind": "agent", "id": "a"}}},
		{"graph_change", map[string]any{"action": "team_member_remove", "team": "t", "member": map[string]any{"kind": "agent", "id": "a"}}},
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

func TestCTOChangePart2CatalogAndActionValidation(t *testing.T) {
	cs := ctoTestSession(t)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range toolcatalog.ForRole(toolcatalog.RoleCTO) {
		if !ctoV2Tools[entry.Name] {
			continue
		}
		var got *mcp.Tool
		for _, tool := range list.Tools {
			if tool.Name == entry.Name {
				got = tool
				break
			}
		}
		if got == nil {
			t.Errorf("missing catalog tool %s", entry.Name)
			continue
		}
		actual, _ := json.Marshal(got.InputSchema)
		var want any
		_ = json.Unmarshal(entry.InputSchema, &want)
		wantBytes, _ := json.Marshal(want)
		if !reflect.DeepEqual(actual, wantBytes) {
			t.Errorf("%s input differs from catalog", entry.Name)
		}
	}
	cases := []struct{ name, raw, want string }{
		{"answer", `{"kind":"held","message_id":"m1","action":"a","id":"other"}`, `id`},
		{"answer", `{"kind":"held","message_id":"m1"}`, `action`},
		{"tools_change", `{"action":"apply"}`, `plan_id`},
		{"tools_change", `{"action":"import"}`, `candidate`},
		{"mcp_change", `{"action":"add"}`, `server`},
		{"settings_change", `{"area":"notifications"}`, `disabled`},
		{"project_change", `{"action":"create"}`, `path`},
	}
	for _, tc := range cases {
		t.Run(tc.name+tc.want, func(t *testing.T) {
			if err := ValidateCTOActionSchema(tc.name, json.RawMessage(tc.raw)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCTOChangePart2RoutesAndOutputs(t *testing.T) {
	ctx := context.Background()
	calls := []struct {
		name string
		args json.RawMessage
	}{}
	st, ct := mcp.NewInMemoryTransports()
	srv := NewCTO(func(_ context.Context, method string, params any) (json.RawMessage, error) {
		b, _ := json.Marshal(params)
		calls = append(calls, struct {
			name string
			args json.RawMessage
		}{method, b})
		return json.RawMessage(`"proposal test-id is in the owner's Inbox (tab 7 of the TUI); the owner decides there"`), nil
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
	cases := []struct{ name, raw string }{
		{"answer", `{"kind":"held","message_id":"m","action":"a"}`}, {"answer", `{"kind":"inbox","id":"i","answer":"yes"}`}, {"answer", `{"kind":"outcome","reply_id":"m","action":"overrule"}`},
		{"tools_change", `{"action":"plan","item":"i","target":{"scope":"global"}}`}, {"tools_change", `{"action":"apply","plan_id":"p"}`}, {"tools_change", `{"action":"undo","item":"i"}`}, {"tools_change", `{"action":"import","candidate":"c"}`}, {"tools_change", `{"action":"native_remove","undo_plan_id":"p"}`},
		{"mcp_change", `{"action":"add","server":{"name":"s","transport":"stdio","command":"/bin/true"}}`}, {"mcp_change", `{"action":"remove","name":"s"}`}, {"mcp_change", `{"action":"test","name":"s"}`}, {"mcp_change", `{"action":"enable","name":"s","enabled":true}`},
		{"settings_change", `{"area":"worker_profile_add","name":"p","harness":"anything","model":"m"}`}, {"settings_change", `{"area":"worker_profile_remove","name":"p"}`}, {"settings_change", `{"area":"worker_providers","providers":["p"]}`}, {"settings_change", `{"area":"protected_providers","protected_providers":["p"]}`}, {"settings_change", `{"area":"score","model":"m","score":50}`}, {"settings_change", `{"area":"integrations","peer_messages":true}`}, {"settings_change", `{"area":"notifications","disabled":true}`}, {"settings_change", `{"area":"cto","title":"CTO"}`}, {"settings_change", `{"area":"playbook","note":"hello"}`}, {"settings_change", `{"area":"machine_name","name":"desk"}`}, {"settings_change", `{"area":"invite"}`}, {"settings_change", `{"area":"onboarding"}`},
		{"project_change", `{"action":"add","paths":["/tmp/project"]}`}, {"project_change", `{"action":"remove","name":"p"}`}, {"project_change", `{"action":"display","project":"p","name":"P"}`}, {"project_change", `{"action":"create","path":"~/project"}`},
		{"mcp_call", `{"server":"s","tool":"list","arguments":{}}`}, {"proposal_ask", `{"id":"p_15"}`}, {"cto_new", `{}`},
	}
	for _, tc := range cases {
		var args map[string]any
		_ = json.Unmarshal([]byte(tc.raw), &args)
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s %s: result=%+v err=%v", tc.name, tc.raw, res, err)
		}
		var entry *toolcatalog.Tool
		for i := range toolcatalog.Tools {
			if toolcatalog.Tools[i].Name == tc.name {
				entry = &toolcatalog.Tools[i]
				break
			}
		}
		if entry == nil {
			t.Fatalf("missing catalog entry %s", tc.name)
		}
		var schema jsonschema.Schema
		if err = json.Unmarshal(entry.OutputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(res.StructuredContent)
		var value any
		_ = json.Unmarshal(encoded, &value)
		if err = resolved.Validate(value); err != nil {
			t.Errorf("%s output %s: %v", tc.name, encoded, err)
		}
		if len(res.Content) != 1 || res.Content[0].(*mcp.TextContent).Text != string(encoded) {
			t.Errorf("%s text and structured content differ", tc.name)
		}
		if tc.name == "mcp_change" && strings.Contains(tc.raw, `"action":"add"`) {
			got := res.StructuredContent.(map[string]any)
			if got["state"] != "proposed" || got["proposal_id"] != "test-id" {
				t.Errorf("mcp add was not returned as a proposal: %#v", got)
			}
		}
	}
	if len(calls) != len(cases) {
		t.Fatalf("calls=%d want %d", len(calls), len(cases))
	}
	// mcp_change add must go through the proposal tool route and preserve args.
	var result map[string]any
	_ = json.Unmarshal([]byte(calls[8].args), &result)
	if calls[8].name != "mcp_change" || result["action"] != "add" {
		t.Fatalf("mcp add route: %s %s", calls[8].name, calls[8].args)
	}
}

func TestCTOProjectCreateRemoteSchema(t *testing.T) {
	if err := ValidateCTOActionSchema("project_change", json.RawMessage(`{"action":"create","path":"~/repo","remote":"git@git.example:team/repo.git"}`)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCTOActionSchema("project_change", json.RawMessage(`{"action":"add","paths":["/tmp/repo"],"remote":"git@git.example:team/repo.git"}`)); err == nil {
		t.Fatal("remote accepted for add")
	}
	if err := ValidateCTOActionSchema("session_change", json.RawMessage(`{"action":"session_start","path":"~/repo","remote":"git@git.example:team/repo.git"}`)); err == nil {
		t.Fatal("remote accepted for session start")
	}
}
