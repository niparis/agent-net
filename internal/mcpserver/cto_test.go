package mcpserver

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

func ctoTestSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	srv := NewCTO(func(_ context.Context, method string, params any) (json.RawMessage, error) {
		if method == "graph" {
			return json.Marshal(protocol.CharterGuideText())
		}
		if method == "graph_change" {
			return json.RawMessage(`{"ok":true,"warnings":["needs: too long/too detailed for a charter: keep it to names and pointers; put detail in AGENTS.md"]}`), nil
		}
		return json.RawMessage(`{"ok":true}`), nil
	}, "test")
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func ctoToolSchema(t *testing.T, tool *mcp.Tool) (map[string]map[string]any, []string) {
	t.Helper()
	b, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return schema.Properties, names
}

func TestCTOToolDescriptionsAndEnums(t *testing.T) {
	cs := ctoTestSession(t)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	for _, tl := range list.Tools {
		tools[tl.Name] = tl
	}
	wantTools := []string{"graph_change", "session_change", "worker_change", "answer", "tools_change", "mcp_change", "mcp_call", "settings_change", "project_change", "cto_new", "message", "ask_owner", "learn", "ui", "sessions"}
	have := map[string]bool{}
	for _, name := range wantTools {
		if tools[name] == nil {
			t.Fatalf("missing tool %s in %v", name, tools)
		}
		have[name] = true
	}
	for _, name := range wantTools {
		tl := tools[name]
		if strings.HasPrefix(tl.Description, "CTO: ") || strings.TrimSpace(tl.Description) == "" {
			t.Errorf("%s has a generic or short description: %q", name, tl.Description)
		}
		props, names := ctoToolSchema(t, tl)
		for _, field := range names {
			if d, _ := props[field]["description"].(string); strings.TrimSpace(d) == "" {
				t.Errorf("%s.%s has no description", name, field)
			}
		}
	}
	wantEnums := map[string]map[string][]string{
		"graph_change": {
			"action": {"charter_set", "node_add", "node_remove", "edge_set", "edge_remove", "settings_set", "undo"},
			"mode":   {"advisor", "arbiter", "owner"},
			"rule":   {"normal", "auto", "ask", "never"},
		},
		"session_change": {"action": {"rename", "trust", "policy", "session_start"}, "folder": {"project", "worktree"}, "harness": {"opencode", "claude", "pi", "codex"}},
		"worker_change":  {"action": {"start", "stop"}, "harness": {"opencode", "claude", "pi", "codex"}, "isolation": {"worktree", "none"}},
		"answer":         {"kind": {"held", "inbox", "outcome"}},
		"tools_change":   {"action": {"plan", "apply", "undo", "import", "native_remove", "toggle", "new_projects"}},
		"settings_change": {
			"area": {"worker_profile_add", "worker_profile_remove", "worker_providers", "protected_providers", "score", "integrations", "notifications", "cto", "playbook", "machine_name", "invite", "onboarding"},
		},
		"project_change": {"action": {"add", "remove", "display", "create"}},
		"message":        {"action": {"send", "reply"}},
		"ui":             {"action": {"show", "notice"}},
	}
	for tool, fields := range wantEnums {
		tl, ok := tools[tool]
		if !ok {
			continue
		}
		props, _ := ctoToolSchema(t, tl)
		for field, want := range fields {
			got := enumStrings(props[field]["enum"])
			if strings.Join(got, " ") != strings.Join(want, " ") {
				t.Errorf("%s.%s enum = %v, want %v", tool, field, got, want)
			}
		}
	}
}

func TestCTOCharterSetWarnsWithoutRefusing(t *testing.T) {
	cs := ctoTestSession(t)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var charter *mcp.Tool
	for _, tool := range list.Tools {
		if tool.Name == "graph_change" {
			charter = tool
			break
		}
	}
	if charter == nil {
		t.Fatal("graph_change missing")
	}
	props, _ := ctoToolSchema(t, charter)
	value := props["value"]["description"].(string)
	if strings.Contains(value, "1200") {
		t.Error("guide text remains in schema")
	}

	out, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "graph_change", Arguments: map[string]any{
		"action": "charter_set", "node": "cogforge", "attr": "needs", "value": "Read /home/project/private/config",
	}})
	if err != nil || out.IsError {
		t.Fatalf("detailed value was refused: result=%+v err=%v", out, err)
	}
	text := ""
	for _, content := range out.Content {
		if item, ok := content.(*mcp.TextContent); ok {
			text += item.Text
		}
	}
	if !strings.Contains(text, "too long/too detailed for a charter: keep it to names and pointers; put detail in AGENTS.md") {
		t.Fatalf("successful graph change did not include the warning: %q", text)
	}
}

func enumStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}

func TestCTOToolsForward(t *testing.T) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	seen := map[string]bool{}
	srv := NewCTO(func(_ context.Context, method string, params any) (json.RawMessage, error) {
		seen[method] = true
		return json.RawMessage(`{"ok":true}`), nil
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
	for _, name := range []string{"graph_change", "session_change", "worker_change", "answer", "tools_change", "mcp_change", "mcp_call", "settings_change", "project_change", "cto_new", "message", "ask_owner", "learn", "ui", "sessions"} {
		args := map[string]any{}
		if field := ctoRoutingField[name]; field != "" {
			args[field] = map[string]string{"message": "send", "ui": "show", "graph_change": "charter_set", "session_change": "rename", "worker_change": "start", "answer": "held", "tools_change": "plan", "mcp_change": "test", "settings_change": "onboarding", "project_change": "add"}[name]
		}
		switch name {
		case "answer":
			args["message_id"], args["action"] = "m1", "a"
		case "mcp_change":
			args["name"] = "test"
		case "project_change", "tools_change":
		case "mcp_call":
			args["server"], args["tool"], args["arguments"] = "test", "list", map[string]any{}
		}
		if name == "worker_change" {
			args["project"] = "example"
			args["task"] = "Do the task"
		}
		out, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || out.IsError || !seen[name] {
			t.Errorf("%s: %+v %v", name, out, err)
		}
	}
}

func TestCTOParityTools(t *testing.T) {
	cs := ctoTestSession(t)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range list.Tools {
		got[tool.Name] = true
	}
	for _, name := range []string{"overview", "sessions", "session", "proposals", "feed", "messages", "graph", "machines", "projects", "workers", "tools", "settings", "message", "ask_owner", "learn", "ui", "graph_change", "session_change", "worker_change", "answer", "tools_change", "mcp_change", "mcp_call", "settings_change", "project_change", "cto_new",
		"agent_sessions_find", "agent_session_get", "agent_machines_find", "agent_machine_get", "agent_projects_find", "agent_workers_find", "agent_messages_find", "agent_message_get", "agent_graph_read", "agent_mcp_servers_find", "agent_worker_profiles_find", "agent_message_send", "agent_message_reply", "agent_owner_ask", "agent_learn"} {
		if !got[name] {
			t.Errorf("missing CTO tool %s", name)
		}
	}
	if len(got) != 43 {
		t.Errorf("CTO exposes %d tools, want 43: %v", len(got), got)
	}
	for _, name := range []string{"charter_set", "node_add", "node_remove", "edge_set", "edge_remove", "graph_undo", "session_rename", "session_trust", "session_policy", "worker_start", "worker_stop", "held_answer", "inbox_answer", "outcome_overrule", "tools_plan", "tools_apply", "tools_undo", "mcp_import", "mcp_native_remove", "mcp_add", "mcp_remove", "mcp_test", "mcp_enable", "send", "reply", "ui_show", "ui_notice", "cto_send"} {
		if got[name] {
			t.Errorf("old name %s is still a CTO tool", name)
		}
	}
}

func TestCTOSettingsSchemasDescribeNestedValues(t *testing.T) {
	cs := ctoTestSession(t)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		switch tool.Name {
		case "session_change", "mcp_change", "settings_change":
			props, _ := ctoToolSchema(t, tool)
			field := map[string]string{"session_change": "policy", "mcp_change": "server", "settings_change": "compaction"}[tool.Name]
			if _, ok := props[field]["properties"].(map[string]any); !ok {
				t.Errorf("%s.%s lacks nested properties", tool.Name, field)
			}
		}
	}
}

func TestCTOSessionPolicyToolDescribesWrite(t *testing.T) {
	list, err := ctoTestSession(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "session_change" {
			if strings.Contains(strings.ToLower(tool.Description), "read or set") || !strings.Contains(tool.Description, "policy") {
				t.Fatalf("misleading policy description: %s", tool.Description)
			}
			return
		}
	}
	t.Fatal("session_change missing")
}

func TestCTOToolManagementDescriptions(t *testing.T) {
	list, err := ctoTestSession(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]*mcp.Tool{}
	for _, v := range list.Tools {
		found[v.Name] = v
	}
	if found["tools_change"] == nil || found["tools_change"].Description == "" {
		t.Fatal("missing tools_change")
	}
	props, _ := ctoToolSchema(t, found["tools_change"])
	if !strings.Contains(found["tools_change"].Description, "plan") || !strings.Contains(strings.Join(enumStrings(props["action"]["enum"]), ","), "native_remove") {
		t.Fatalf("tool actions missing: %s", found["tools_change"].Description)
	}
	targets := props["repo_targets"]
	items, _ := targets["items"].(map[string]any)
	if items["type"] != "object" {
		t.Fatalf("repo_targets must contain checkout objects: %+v", targets)
	}
}

func TestProtectedProvidersTools(t *testing.T) {
	def, ok := ctoTools["settings_change"]
	if !ok {
		t.Fatal("missing settings_change")
	}
	if !strings.Contains(strings.ToLower(def.desc), "protected") {
		t.Fatal("settings_change does not mention protected providers")
	}
	if _, ok := ctoConcreteTools["protected_providers_set"]; !ok {
		t.Fatal("missing concrete protected_providers_set")
	}
}

// The owner renames a session through the CTO (decision 0033). An empty name
// returns to the automatic project name.
func TestCTOSessionRenameTool(t *testing.T) {
	cs := ctoTestSession(t)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "session_change" {
			props, _ := ctoToolSchema(t, tool)
			if props["session_id"] == nil || props["name"] == nil {
				t.Fatalf("properties %v", props)
			}
			return
		}
	}
	t.Fatal("session_change missing")
}

func TestCTOProjectDisplayTool(t *testing.T) {
	cs := ctoTestSession(t)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "project_change" {
			props, _ := ctoToolSchema(t, tool)
			if props["project"] == nil || props["name"] == nil || props["action"] == nil {
				t.Fatalf("properties %v", props)
			}
			return
		}
	}
	t.Fatal("project_change missing")
}

func TestCTOSchemaBudgetAndCharterGuide(t *testing.T) {
	list, err := ctoTestSession(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := json.Marshal(list.Tools)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("total schema: %d bytes", len(schema))
	if len(schema) >= 32*1024 {
		t.Errorf("CTO schema is %d bytes, budget %d", len(schema), 32*1024)
	}
	for _, tool := range list.Tools {
		data, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "1200") {
			t.Errorf("%s schema carries the charter guide", tool.Name)
		}
	}
}

func TestCTOCharterGuideView(t *testing.T) {
	cs := ctoTestSession(t)
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "graph", Arguments: map[string]any{"view": "guide"}})
	if err != nil || result.IsError {
		t.Fatalf("guide: %v %v", result, err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if text != protocol.CharterGuideText() {
		t.Fatalf("guide missing: %s", text)
	}
	list, _ := cs.ListTools(context.Background(), nil)
	for _, tool := range list.Tools {
		raw, _ := json.Marshal(tool)
		if strings.Contains(string(raw), "1200") {
			t.Errorf("guide text in %s schema", tool.Name)
		}
	}
}

func TestCTOWorkerStartWithoutTouches(t *testing.T) {
	for _, tc := range []struct{ name, args string }{
		{"worker_start", `{"task":"x","project":"p"}`}, {"worker_change", `{"action":"start","task":"x","project":"p"}`},
	} {
		if err := CTOArguments(tc.name, json.RawMessage(tc.args)); err != nil {
			t.Errorf("%s refused a start without touches: %v", tc.name, err)
		}
	}
	for _, tc := range []struct{ name, args string }{
		{"worker_start", `{"continue":"old"}`}, {"worker_change", `{"action":"start","continue":"old"}`}, {"worker_change", `{"action":"stop"}`},
	} {
		if err := CTOArguments(tc.name, json.RawMessage(tc.args)); err != nil {
			t.Errorf("%s %s: %v", tc.name, tc.args, err)
		}
	}
}
