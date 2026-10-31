package mcpserver

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func ctoTestSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	srv := NewCTO(func(_ context.Context, method string, params any) (json.RawMessage, error) {
		if method == "charter_set" {
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
	wantTools := []string{"graph_get", "charter_set", "charter_history", "node_add", "node_remove", "edge_set", "edge_remove", "edge_history", "graph_undo", "agents", "workers", "needs_you", "feed", "send", "worker_start", "score_set", "worker_providers_set", "worker_profile_add", "worker_profile_remove", "worker_profile_list"}
	have := map[string]bool{}
	for _, name := range tools {
		have[name.Name] = true
	}
	for _, name := range wantTools {
		if !have[name] {
			t.Fatalf("missing tool %s in %v", name, tools)
		}
	}
	for _, name := range wantTools {
		tl, ok := tools[name]
		if !ok {
			t.Errorf("missing tool %s", name)
			continue
		}
		if strings.HasPrefix(tl.Description, "CTO: ") || len(tl.Description) < 60 {
			t.Errorf("%s has a generic or short description: %q", name, tl.Description)
		}
		props, names := ctoToolSchema(t, tl)
		for _, field := range names {
			if d, _ := props[field]["description"].(string); strings.TrimSpace(d) == "" {
				t.Errorf("%s.%s has no description", name, field)
			}
		}
		if name == "worker_profile_add" {
			if d, _ := props["name"]["description"].(string); !strings.Contains(d, "existing name") {
				t.Errorf("worker_profile_add.name does not explain replacement: %q", d)
			}
		}
	}
	wantEnums := map[string]map[string][]string{
		"edge_set": {
			"kind": {"depends", "governs", "deploys"},
			"mode": {"advisor", "arbiter", "owner"},
			"rule": {"normal", "auto", "ask", "never"},
		},
		"edge_remove":  {"kind": {"depends", "governs", "deploys"}},
		"charter_set":  {"attr": {"purpose", "maturity", "accepts", "refuses", "feature_requests", "consumers", "escalate", "needs", "notes"}},
		"worker_start": {"harness": {"opencode", "claude", "pi", "codex"}, "isolation": {"worktree", "none"}},
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

func TestCTOCharterGuideAppearsInSchemaAndWarnsWithoutRefusing(t *testing.T) {
	cs := ctoTestSession(t)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var charter *mcp.Tool
	for _, tool := range list.Tools {
		if tool.Name == "charter_set" {
			charter = tool
			break
		}
	}
	if charter == nil {
		t.Fatal("charter_set missing")
	}
	props, _ := ctoToolSchema(t, charter)
	for name, description := range map[string]string{
		"tool":  charter.Description,
		"attr":  props["attr"]["description"].(string),
		"value": props["value"]["description"].(string),
	} {
		for _, want := range []string{"accepts", "other agents ask", "Bug fixes in retrieval", "Bearer-authenticated", "1200 characters", "AGENTS.md"} {
			if !strings.Contains(description, want) {
				t.Errorf("%s description misses %q: %s", name, want, description)
			}
		}
	}
	out, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "charter_set", Arguments: map[string]any{
		"node": "cogforge", "attr": "needs", "value": "Read /home/project/private/config",
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
		t.Fatalf("successful charter_set did not include the warning: %q", text)
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
	for _, name := range []string{"graph_get", "charter_set", "charter_history", "node_add", "node_remove", "edge_set", "edge_remove", "edge_history", "graph_undo", "agents", "workers", "needs_you", "feed", "send", "worker_start", "score_set", "worker_providers_set", "worker_profile_add", "worker_profile_remove", "worker_profile_list"} {
		out, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
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
	for _, name := range []string{"info", "nodes", "projects", "project_add", "project_remove", "get", "messages", "message_status", "session_text", "usage", "usage_top", "counts", "graph_view", "session_enter", "session_trust", "session_policy", "node_rename", "integrations_get", "integrations_set", "worker_stop", "held_list", "held_answer", "inbox_list", "inbox_answer", "outcome_inbox", "outcome_overrule", "node_settings_get", "node_settings_set", "playbook_get", "playbook_set", "learn", "mcp_list", "mcp_add", "mcp_remove", "mcp_test", "mcp_enable", "cto_settings", "cto_enter", "cto_send", "cto_new", "cto_events", "cto_graph_history"} {
		if !got[name] {
			t.Errorf("missing CTO tool %s", name)
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
		case "session_policy", "mcp_add", "cto_settings":
			props, _ := ctoToolSchema(t, tool)
			field := map[string]string{"session_policy": "policy", "mcp_add": "server", "cto_settings": "compaction"}[tool.Name]
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
		if tool.Name == "session_policy" {
			if strings.Contains(strings.ToLower(tool.Description), "read or set") || !strings.Contains(tool.Description, "get") {
				t.Fatalf("misleading policy description: %s", tool.Description)
			}
			return
		}
	}
	t.Fatal("session_policy missing")
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
	for _, name := range []string{"tools_list", "tools_plan", "tools_apply", "tools_history", "tools_differences", "mcp_import_candidates", "mcp_import", "mcp_native_remove"} {
		if found[name] == nil || len(found[name].Description) < 60 {
			t.Fatalf("missing description: %s", name)
		}
	}
	if !strings.Contains(found["tools_apply"].Description, "explicit") || !strings.Contains(found["mcp_import"].Description, "explicit") || !strings.Contains(found["mcp_native_remove"].Description, "explicit") {
		t.Fatal("apply description lacks owner instruction requirement")
	}
	planProps, _ := ctoToolSchema(t, found["tools_plan"])
	if !strings.Contains(found["tools_plan"].Description, "local branch") || !strings.Contains(strings.Join(enumStrings(planProps["operation"]["enum"]), ","), "repo_remove") {
		t.Fatalf("repo removal missing from tools_plan: %s", found["tools_plan"].Description)
	}
	targets := planProps["repo_targets"]
	items, _ := targets["items"].(map[string]any)
	if items["type"] != "object" {
		t.Fatalf("repo_targets must contain checkout objects: %+v", targets)
	}
}
