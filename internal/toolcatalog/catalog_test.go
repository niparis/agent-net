package toolcatalog

import (
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

func effectiveInput(tool Tool, role Role) json.RawMessage {
	if role == RoleCTO && len(tool.CTOInputSchema) > 0 {
		return tool.CTOInputSchema
	}
	return tool.InputSchema
}

func decodeObject(t *testing.T, name string, raw json.RawMessage) map[string]any {
	t.Helper()
	if !json.Valid(raw) {
		t.Fatalf("%s: schema is not valid JSON: %s", name, raw)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("%s: schema root is not an object: %v", name, err)
	}
	return obj
}

func checkInputRoot(t *testing.T, name string, raw json.RawMessage) {
	t.Helper()
	obj := decodeObject(t, name, raw)
	if got := obj["type"]; got != "object" {
		t.Errorf("%s: input schema root type is %v, want object", name, got)
	}
	props, ok := obj["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		t.Errorf("%s: input schema root has no properties", name)
	}
	if got, ok := obj["additionalProperties"]; !ok || got != false {
		t.Errorf("%s: input schema additionalProperties is %v, want false", name, got)
	}
	for _, keyword := range []string{"oneOf", "anyOf", "allOf"} {
		if _, ok := obj[keyword]; ok {
			t.Errorf("%s: input schema root has top-level %s, which Claude Code skips", name, keyword)
		}
	}
}

func TestInputSchemaShape(t *testing.T) {
	if len(Tools) != 46 {
		t.Errorf("catalog has %d tools, want 46", len(Tools))
	}
	for _, tool := range Tools {
		checkInputRoot(t, tool.Name, tool.InputSchema)
		if len(tool.CTOInputSchema) > 0 {
			checkInputRoot(t, tool.Name+" (CTO input)", tool.CTOInputSchema)
		}
	}
}

func checkRefs(t *testing.T, name string, root map[string]any) {
	t.Helper()
	defs, _ := root["$defs"].(map[string]any)
	var walk func(prefix string, value any)
	walk = func(prefix string, value any) {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				if key == "$ref" {
					ref, ok := child.(string)
					if !ok || !strings.HasPrefix(ref, "#/$defs/") {
						t.Errorf("%s: %s$ref %v is not a local $defs reference", name, prefix, child)
						continue
					}
					target := strings.TrimPrefix(ref, "#/$defs/")
					if _, ok := defs[target]; !ok {
						t.Errorf("%s: %s$ref %q does not resolve in its own $defs", name, prefix, ref)
					}
					continue
				}
				walk(prefix+key+".", child)
			}
		case []any:
			for i, child := range v {
				walk(prefix+string(rune('0'+i))+"[]", child)
			}
		}
	}
	walk("", root)
}

func TestSchemasValidJSONAndRefs(t *testing.T) {
	for _, tool := range Tools {
		for suffix, raw := range map[string]json.RawMessage{
			"input":  tool.InputSchema,
			"output": tool.OutputSchema,
		} {
			name := tool.Name + " " + suffix
			root := decodeObject(t, name, raw)
			checkRefs(t, name, root)
		}
		if len(tool.CTOInputSchema) > 0 {
			name := tool.Name + " cto_input"
			root := decodeObject(t, name, tool.CTOInputSchema)
			checkRefs(t, name, root)
		}
	}
}

var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func TestNamesUniqueAndValid(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range Tools {
		if !toolNamePattern.MatchString(tool.Name) {
			t.Errorf("tool name %q does not match %s", tool.Name, toolNamePattern)
		}
		if seen[tool.Name] {
			t.Errorf("tool name %q appears more than once", tool.Name)
		}
		seen[tool.Name] = true
		if len(tool.Roles) == 0 {
			t.Errorf("tool %q has no roles", tool.Name)
		}
		for _, role := range tool.Roles {
			switch role {
			case RoleAgent, RoleWorker, RoleCTO, RoleBot:
			default:
				t.Errorf("tool %q has unknown role %q", tool.Name, role)
			}
		}
	}
}

func TestDescriptions(t *testing.T) {
	for _, tool := range Tools {
		if tool.Description == "" {
			t.Errorf("tool %q has no description", tool.Name)
		}
		if n := utf8.RuneCountInString(tool.Description); n > 300 {
			t.Errorf("tool %q description is %d characters, want at most 300", tool.Name, n)
		}
		if strings.ContainsRune(tool.Description, '\u2014') {
			t.Errorf("tool %q description contains an em dash", tool.Name)
		}
	}
}

func names(tools []Tool) []string {
	out := make([]string, len(tools))
	for i, tool := range tools {
		out[i] = tool.Name
	}
	return out
}

func contains(tools []Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

var ctoOnlyTools = []string{
	"cto_ui_show", "cto_proposals_find", "graph_change", "session_change",
	"worker_change", "answer", "tools_change", "mcp_change",
	"settings_change", "project_change", "sessions_archive", "proposal_ask", "mcp_call", "cto_new",
}

func TestForRole(t *testing.T) {
	worker := ForRole(RoleWorker)
	agent := ForRole(RoleAgent)
	cto := ForRole(RoleCTO)

	for _, name := range []string{"graph_change", "mcp_change", "agent_worker_start", "agent_project_add"} {
		if contains(worker, name) {
			t.Errorf("worker sees %q", name)
		}
	}
	for _, name := range ctoOnlyTools {
		if contains(agent, name) {
			t.Errorf("agent sees CTO-only tool %q", name)
		}
	}
	if !contains(cto, "cto_new") {
		t.Error("CTO does not see cto_new")
	}
	if !contains(cto, "proposal_ask") {
		t.Error("CTO does not see proposal_ask")
	}
	if !contains(cto, "agent_sessions_find") {
		t.Error("CTO does not see shared tool agent_sessions_find")
	}

	for _, role := range []Role{RoleAgent, RoleWorker, RoleCTO} {
		given := ForRole(role)
		if !sort.SliceIsSorted(given, func(i, j int) bool { return given[i].Name < given[j].Name }) {
			t.Errorf("%s tools are not sorted by name: %v", role, names(given))
		}
		for _, tool := range given {
			found := false
			for _, r := range tool.Roles {
				if r == role {
					found = true
				}
			}
			if !found {
				t.Errorf("%s list contains %q, which is not marked for that role", role, tool.Name)
			}
		}
	}
}

func TestAlwaysLoad(t *testing.T) {
	want := map[string]bool{
		"agent_announce":      true,
		"agent_sessions_find": true,
		"agent_message_send":  true,
	}
	for _, tool := range Tools {
		if tool.AlwaysLoad != want[tool.Name] {
			t.Errorf("tool %q AlwaysLoad is %v, want %v", tool.Name, tool.AlwaysLoad, want[tool.Name])
		}
	}
}

func TestWorkersFindCTOInput(t *testing.T) {
	for _, tool := range Tools {
		if tool.Name != "agent_workers_find" {
			continue
		}
		if len(tool.CTOInputSchema) == 0 {
			t.Fatal("agent_workers_find has no CTO input schema")
		}
		var schema struct {
			Properties map[string]map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(tool.CTOInputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		if got := schema.Properties["started_by"]["default"]; got != "anyone" {
			t.Errorf("CTO started_by default is %q, want anyone", got)
		}
		return
	}
	t.Fatal("agent_workers_find is not in the catalog")
}

func TestSizeReport(t *testing.T) {
	for _, role := range []Role{RoleAgent, RoleWorker, RoleCTO} {
		total := 0
		for _, tool := range ForRole(role) {
			total += len(tool.Name) + len(tool.Description) + len(effectiveInput(tool, role))
		}
		t.Logf("%s tools: %d tools, %d bytes (name+description+inputSchema)", role, len(ForRole(role)), total)
	}
}

func TestBotRole(t *testing.T) {
	want := []string{"agent_graph_read", "agent_machine_get", "agent_machines_find", "agent_message_get", "agent_message_reply", "agent_message_send", "agent_messages_find", "agent_projects_find", "agent_session_get", "agent_sessions_find", "agent_settings_read"}
	if got := names(ForRole(RoleBot)); !reflect.DeepEqual(got, want) {
		t.Fatalf("bot tools = %v, want %v", got, want)
	}
	for _, tool := range ForRole(RoleBot) {
		if !Allows(RoleBot, tool.Name) {
			t.Errorf("Allows disagrees for %s", tool.Name)
		}
		if tool.Name == "agent_message_send" {
			if strings.Contains(tool.Description, "to_set") || strings.Contains(tool.Description, "as_owner") {
				t.Error("bot description offers forbidden arguments")
			}
			obj := decodeObject(t, tool.Name, tool.InputSchema)
			checkInputRoot(t, tool.Name+" (bot)", tool.InputSchema)
			if required := obj["required"]; !reflect.DeepEqual(required, []any{"body", "to"}) {
				t.Errorf("bot required = %v", required)
			}
			props := obj["properties"].(map[string]any)
			for _, name := range []string{"to_set", "as_owner"} {
				if _, ok := props[name]; ok {
					t.Errorf("bot schema offers %s", name)
				}
			}
			if _, ok := props["to"]; !ok {
				t.Error("bot schema has no to")
			}
		}
	}
}

func TestExistingRoleListsUnchanged(t *testing.T) {
	if got, want := names(ForRole(RoleAgent)), []string{"agent_announce", "agent_change_submit", "agent_changes_find", "agent_file_send", "agent_graph_read", "agent_learn", "agent_machine_get", "agent_machine_log", "agent_machines_find", "agent_mcp_servers_find", "agent_mcp_test", "agent_message_get", "agent_message_handoff", "agent_message_reply", "agent_message_send", "agent_messages_find", "agent_owner_ask", "agent_project_add", "agent_projects_find", "agent_session_get", "agent_sessions_find", "agent_settings_read", "agent_step_claim", "agent_step_report", "agent_worker_profiles_find", "agent_worker_rate", "agent_worker_start", "agent_worker_stop", "agent_workers_find"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RoleAgent tools changed: %v", got)
	}
	if got, want := names(ForRole(RoleWorker)), []string{"agent_changes_find", "agent_file_send", "agent_graph_read", "agent_handback", "agent_learn", "agent_machine_get", "agent_machine_log", "agent_machines_find", "agent_mcp_servers_find", "agent_message_get", "agent_message_reply", "agent_message_send", "agent_messages_find", "agent_projects_find", "agent_session_get", "agent_sessions_find", "agent_settings_read", "agent_workers_find"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RoleWorker tools changed: %v", got)
	}
	if got, want := names(ForRole(RoleCTO)), []string{"agent_graph_read", "agent_learn", "agent_machine_get", "agent_machine_log", "agent_machines_find", "agent_mcp_servers_find", "agent_message_get", "agent_message_reply", "agent_message_send", "agent_messages_find", "agent_owner_ask", "agent_projects_find", "agent_session_get", "agent_sessions_find", "agent_settings_read", "agent_worker_profiles_find", "agent_workers_find", "answer", "cto_new", "cto_placement_memory", "cto_proposals_find", "cto_ui_show", "graph_change", "mcp_call", "mcp_change", "project_change", "projects_layout", "proposal_ask", "session_change", "sessions_archive", "settings_change", "tools_change", "worker_change"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RoleCTO tools changed: %v", got)
	}
	for _, role := range []Role{RoleAgent, RoleWorker, RoleCTO} {
		for _, tool := range ForRole(role) {
			if tool.Name == "agent_message_send" {
				props := decodeObject(t, tool.Name, tool.InputSchema)["properties"].(map[string]any)
				for _, name := range []string{"to_set", "as_owner"} {
					if _, ok := props[name]; !ok {
						t.Errorf("%s lost %s", role, name)
					}
				}
			}
		}
	}
}

func TestWorkerModelFieldsParity(t *testing.T) {
	var schemas []map[string]any
	for _, name := range []string{"agent_worker_start", "worker_change"} {
		for _, tool := range Tools {
			if tool.Name == name {
				obj := decodeObject(t, name, tool.InputSchema)
				schemas = append(schemas, obj["properties"].(map[string]any))
			}
		}
	}
	if len(schemas) != 2 {
		t.Fatal("missing worker schemas")
	}
	for _, field := range []string{"provider", "model", "effort"} {
		if schemas[0][field] == nil || !reflect.DeepEqual(schemas[0][field], schemas[1][field]) {
			t.Errorf("%s differs: %v / %v", field, schemas[0][field], schemas[1][field])
		}
	}
}

// The delivery tools: any agent submits and reports steps; workers only read
// changes. The CTO gets agent_changes_find once its tool list has room under
// its schema budget (TestCTOSchemaBudgetAndCharterGuide).
func TestDeliveryToolRoles(t *testing.T) {
	want := map[string][]Role{
		"agent_change_submit": {RoleAgent},
		"agent_step_claim":    {RoleAgent},
		"agent_step_report":   {RoleAgent},
		"agent_changes_find":  {RoleAgent, RoleWorker},
	}
	for name, roles := range want {
		found := false
		for _, tool := range Tools {
			if tool.Name != name {
				continue
			}
			found = true
			if !reflect.DeepEqual(tool.Roles, roles) {
				t.Errorf("%s roles = %v, want %v", name, tool.Roles, roles)
			}
			if name == "agent_changes_find" && !(tool.Hints.ReadOnly && tool.Hints.Idempotent) {
				t.Errorf("%s hints = %+v", name, tool.Hints)
			}
			props := decodeObject(t, name, tool.InputSchema)["properties"].(map[string]any)
			for _, field := range map[string][]string{"agent_change_submit": {"branch", "commit", "change"}, "agent_step_claim": {"change", "step", "for", "after_merge", "commit"}, "agent_step_report": {"change", "step", "for", "after_merge", "result", "commit", "fields", "done_by", "note"}, "agent_changes_find": {"change", "project", "state", "caller", "limit", "cursor"}}[name] {
				if props[field] == nil {
					t.Errorf("%s has no %s", name, field)
				}
			}
		}
		if !found {
			t.Errorf("%s is not in the catalog", name)
		}
	}
}

func TestFileSendCatalog(t *testing.T) {
	var found *Tool
	for i := range Tools {
		if Tools[i].Name == "agent_file_send" {
			found = &Tools[i]
		}
	}
	if found == nil {
		t.Fatal("agent_file_send missing")
	}
	for _, role := range []Role{RoleAgent, RoleWorker} {
		if !Allows(role, found.Name) {
			t.Errorf("%s cannot call file send", role)
		}
	}
	input := decodeObject(t, found.Name, found.InputSchema)
	if !reflect.DeepEqual(input["required"], []any{"to", "paths", "note"}) {
		t.Errorf("required = %v", input["required"])
	}
	output := decodeObject(t, found.Name, found.OutputSchema)
	if !reflect.DeepEqual(output["required"], []any{"message_id", "files"}) {
		t.Errorf("output required = %v", output["required"])
	}
}
