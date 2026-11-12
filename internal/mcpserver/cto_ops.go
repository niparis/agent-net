package mcpserver

// The CTO lists a small core of everyday tools. Every rare operation lives in
// the op tier behind cto_ops_list and cto_op: the list names each op, its
// purpose and its owner gate, and cto_op runs one through the same daemon path
// as the typed tool it replaced. A bad call returns the op's full schema and
// an example, and nothing runs.

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// CTOOp is one operation in the CTO's op tier.
type CTOOp struct {
	Name    string
	Group   string
	Purpose string
	Example string
	Schema  map[string]any
	// Tool is the merged daemon tool the op belongs to, or empty when the op
	// is an owner operation in Method.
	Tool string
	// Field is the routing field of Tool ("action", or "area" for settings).
	Field string
	// Action is the value of Field.
	Action string
	// Method is an owner operation method, run through settings_change area
	// operator.
	Method string
}

var ctoOpGroupOrder = []string{"Connected apps", "Machines", "Sessions", "Graph", "Tools", "Owner reads", "Owner changes"}

// ctoOwnerOpTypes is the argument schema of owner operations that moved to the
// op tier with a strict contract. Every other owner operation keeps the free
// form arguments object it had inside settings_change area operator; the
// gateway still refuses an unknown argument where it decodes the request.
var ctoOwnerOpTypes = map[string]reflect.Type{
	"charter_history": reflect.TypeOf(protocol.GraphHistoryRequest{}),
	"chief_set":       reflect.TypeOf(protocol.ChiefSetRequest{}),
	"gateway_backup": reflect.TypeOf(struct {
		Path string `json:"path"`
	}{}),
	"tools_export":   reflect.TypeOf(protocol.ToolVersionRequest{}),
	"tools_import":   reflect.TypeOf(protocol.ToolImport{}),
	"tools_versions": reflect.TypeOf(protocol.ToolVersionRequest{}),
	"bar_edit":       reflect.TypeOf(protocol.BarEditRequest{}),
	"tools_items":    reflect.TypeOf(protocol.ToolsFilter{}),
}

var ctoOwnerOpExamples = map[string]string{
	"charter_history": `{"node":"architect"}`,
	"chief_set":       `{"harness":"claude","model":"claude-opus-5-5","effort":"medium"}`,
	"gateway_backup":  `{"path":"/tmp/agent-net-backup"}`,
	"tools_export":    `{"item":"skill-id","version":"version-hash"}`,
	"tools_import":    `{"item":"skill-id","location":{"node_id":"node-id","location":"/home/owner/skill"}}`,
	"tools_versions":  `{"item":"skill-id"}`,
	"bar_edit":        `{"project":"demo","add":["Performance first."]}`,
	"tools_items":     `{"compact":true,"name":"wiki","limit":50}`,
}

// ctoOwnerOpPurposes words the ops whose name alone does not say what they do.
var ctoOwnerOpPurposes = map[string]string{
	"bar_edit":    "Add, edit or remove the short rules that workers and reviewers check a project's diffs against. Name the project.",
	"tools_items": "List skills and MCP servers. With compact true, one short row per item: id, name, kind, machines, file_count, trimmed and a one-line description, paged by limit and cursor. Read one item in full with tools_item and its id.",
}

func ownerOpSchema(method string) map[string]any {
	if typ, ok := ctoOwnerOpTypes[method]; ok {
		schema, err := jsonschema.ForType(typ, nil)
		if err == nil {
			raw, err := json.Marshal(schema)
			if err == nil {
				var out map[string]any
				if json.Unmarshal(raw, &out) == nil {
					if method == "tools_export" {
						out["required"] = []string{"item", "version"}
					}
					if method == "bar_edit" {
						out["required"] = []string{"project"}
					}
					return out
				}
			}
		}
	}
	return map[string]any{"type": "object", "additionalProperties": true, "description": "Arguments of the owner operation."}
}

func ownerOpPurpose(method string, read bool) string {
	if purpose, ok := ctoOwnerOpPurposes[method]; ok {
		return purpose
	}
	words := strings.ReplaceAll(strings.ReplaceAll(method, "_", " "), ".", " ")
	if read {
		return "Read " + words + " through the owner operation."
	}
	return "Run the owner operation " + method + "."
}

func ownerOpExample(method string) string {
	if example, ok := ctoOwnerOpExamples[method]; ok {
		return example
	}
	return "{}"
}

// concreteOpSchema builds the strict schema of a moved merged action from the
// concrete tool schema. reason is allowed only for a proposal.
func concreteOpSchema(concrete string, required []string, proposal bool) map[string]any {
	spec, ok := ctoConcreteTools[concrete]
	if !ok {
		panic("mcpserver: no concrete CTO schema for op " + concrete)
	}
	schema := ctoSchema(concrete, spec)
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		schema["properties"] = props
	}
	if proposal {
		props["reason"] = map[string]any{"type": "string", "description": "Why"}
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func mergedOp(name, group, purpose, tool, action, concrete string, required []string, proposal bool, example string) CTOOp {
	field := "action"
	if tool == "settings_change" {
		field = "area"
	}
	return CTOOp{Name: name, Group: group, Purpose: purpose, Example: example, Tool: tool, Field: field, Action: action, Schema: concreteOpSchema(concrete, required, proposal)}
}

// ctoMovedOps are the operations removed from the everyday tools.
var ctoMovedOps = []CTOOp{
	mergedOp("apps_list", "Connected apps", "List connected apps and their IDs.", "mcp_change", "apps_list", "apps.list", nil, false, `{}`),
	mergedOp("app_grant", "Connected apps", "Grant a shared MCP server to a bot.", "mcp_change", "app_grant", "apps.grant", []string{"id", "grant"}, false, `{"id":"app-1","grant":"mcp server browser"}`),
	mergedOp("app_ungrant", "Connected apps", "Remove a bot's grant of a shared MCP server.", "mcp_change", "app_ungrant", "apps.ungrant", []string{"id", "grant"}, false, `{"id":"app-1","grant":"mcp server browser"}`),
	mergedOp("app_rename", "Connected apps", "Rename an active app.", "mcp_change", "app_rename", "apps.rename", []string{"id", "name"}, false, `{"id":"app-1","name":"Builder"}`),
	mergedOp("app_revoke", "Connected apps", "Revoke an app and its credentials; cannot be undone.", "mcp_change", "app_revoke", "apps.revoke", []string{"id"}, false, `{"id":"app-1"}`),
	mergedOp("public_status", "Connected apps", "Read internet access for connected apps.", "mcp_change", "public_status", "apps.public.status", nil, false, `{}`),
	mergedOp("public_on", "Connected apps", "Turn internet access on for connected apps.", "mcp_change", "public_on", "apps.public.on", nil, false, `{}`),
	mergedOp("public_off", "Connected apps", "Turn internet access off for connected apps.", "mcp_change", "public_off", "apps.public.off", nil, false, `{}`),

	mergedOp("machine_remove", "Machines", "Remove a machine and revoke its token; owner approval.", "settings_change", "machine_remove", "machine_remove", []string{"node_id"}, true, `{"node_id":"node-id","reason":"retired"}`),
	mergedOp("machine_rotate", "Machines", "Rotate an online machine token; owner approval.", "settings_change", "machine_rotate", "machine_rotate", []string{"node_id"}, true, `{"node_id":"node-id"}`),
	mergedOp("process_stop", "Machines", "Stop processes on a machine: TERM then KILL; no undo.", "settings_change", "process_stop", "process_stop", []string{"node_id", "pids"}, false, `{"node_id":"node-id","pids":[1234,5678]}`),

	mergedOp("session_replace", "Sessions", "Move an old session's work to the session that replaced it.", "session_change", "replace", "session_replace", []string{"old_session", "new_session"}, false, `{"old_session":"old-session-id","new_session":"new-session-id"}`),

	mergedOp("node_remove", "Graph", "Remove an edgeless architect; owner approval.", "graph_change", "node_remove", "node_remove", []string{"name"}, true, `{"name":"old-node","reason":"retired"}`),
	mergedOp("repo_archive", "Graph", "Deactivate a typed repo identity; owner approval.", "graph_change", "repo_archive", "repo_archive", []string{"ref"}, true, `{"ref":{"kind":"repo","id":"old-repo"},"reason":"retired"}`),
	mergedOp("agent_tombstone", "Graph", "Deactivate a typed agent identity; owner approval.", "graph_change", "agent_tombstone", "agent_tombstone", []string{"ref"}, true, `{"ref":{"kind":"agent","id":"old-agent"},"reason":"retired"}`),

	mergedOp("mcp_import", "Tools", "Import a native MCP server, by candidate or by plan.", "tools_change", "import", "mcp_import", nil, false, `{"candidate":"mcp-candidate"}`),
}

var ctoOps = func() []CTOOp {
	ops := append([]CTOOp(nil), ctoMovedOps...)
	seen := map[string]bool{}
	for _, op := range ops {
		seen[op.Name] = true
	}
	addOwner := func(method, group string, read bool) {
		if seen[method] {
			return
		}
		seen[method] = true
		ops = append(ops, CTOOp{
			Name:    method,
			Group:   group,
			Purpose: ownerOpPurpose(method, read),
			Example: ownerOpExample(method),
			Schema:  ownerOpSchema(method),
			Method:  method,
		})
	}
	for _, method := range strings.Fields(protocol.CTOOperatorReads) {
		addOwner(method, "Owner reads", true)
	}
	for _, method := range strings.Fields(protocol.CTOOperatorChanges) {
		addOwner(method, "Owner changes", false)
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Name < ops[j].Name })
	return ops
}()

// CTOOps returns every op tier entry, sorted by name.
func CTOOps() []CTOOp {
	return append([]CTOOp(nil), ctoOps...)
}

// CTOOpByName finds one op tier entry.
func CTOOpByName(name string) (CTOOp, bool) {
	for _, op := range ctoOps {
		if op.Name == name {
			return op, true
		}
	}
	return CTOOp{}, false
}

// CTOOpGroups returns the group order of the op list.
func CTOOpGroups() []string {
	return append([]string(nil), ctoOpGroupOrder...)
}

func ctoOpSchemaError(op CTOOp, err error) error {
	schema, _ := json.MarshalIndent(op.Schema, "", "  ")
	return fmt.Errorf("cto_op %s arguments do not match its schema: %v\nSchema: %s\nExample: %s", op.Name, err, schema, op.Example)
}

// ValidateCTOOpArgs checks cto_op arguments against the op's full schema. A
// wrong, missing or unknown argument returns the schema and example, and no
// call is made. Proposal and irreversible ops never run with defaults: the
// same check holds for them.
func ValidateCTOOpArgs(op CTOOp, raw json.RawMessage) error {
	schemaRaw, err := json.Marshal(op.Schema)
	if err != nil {
		return ctoOpSchemaError(op, err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		return ctoOpSchemaError(op, err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return ctoOpSchemaError(op, err)
	}
	var value any
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &value); err != nil {
			return ctoOpSchemaError(op, fmt.Errorf("arguments must be a JSON object"))
		}
	}
	if value == nil {
		value = map[string]any{}
	}
	if err := resolved.Validate(value); err != nil {
		return ctoOpSchemaError(op, err)
	}
	fields, _ := value.(map[string]any)
	stringField := func(key string) string {
		text, _ := fields[key].(string)
		return strings.TrimSpace(text)
	}
	switch op.Name {
	case "mcp_import":
		selected := 0
		for _, key := range []string{"candidate", "plan_id"} {
			if stringField(key) != "" {
				selected++
			}
		}
		if selected != 1 {
			return ctoOpSchemaError(op, fmt.Errorf("needs exactly one of candidate or plan_id"))
		}
	case "process_stop":
		pids, _ := fields["pids"].([]any)
		if stringField("node_id") == "" || len(pids) == 0 {
			return ctoOpSchemaError(op, fmt.Errorf("needs node_id and a nonempty pids list"))
		}
	case "machine_remove", "machine_rotate":
		if stringField("node_id") == "" {
			return ctoOpSchemaError(op, fmt.Errorf("needs node_id"))
		}
	case "node_remove":
		if stringField("name") == "" {
			return ctoOpSchemaError(op, fmt.Errorf("needs name"))
		}
	case "app_grant", "app_ungrant":
		if stringField("id") == "" || stringField("grant") == "" {
			return ctoOpSchemaError(op, fmt.Errorf("needs id and grant"))
		}
	case "app_rename":
		if stringField("id") == "" || stringField("name") == "" {
			return ctoOpSchemaError(op, fmt.Errorf("needs id and name"))
		}
	case "app_revoke":
		if stringField("id") == "" {
			return ctoOpSchemaError(op, fmt.Errorf("needs id"))
		}
	}
	return nil
}

// CallArgs turns op arguments into the daemon tool and its full arguments.
func (op CTOOp) CallArgs(raw json.RawMessage) (string, json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &fields); err != nil {
			return "", nil, fmt.Errorf("arguments must be a JSON object")
		}
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	if op.Method != "" {
		area, _ := json.Marshal("operator")
		method, _ := json.Marshal(op.Method)
		arguments, err := json.Marshal(fields)
		if err != nil {
			return "", nil, err
		}
		full, err := json.Marshal(map[string]json.RawMessage{"area": area, "method": method, "arguments": arguments})
		if err != nil {
			return "", nil, err
		}
		return "settings_change", full, nil
	}
	if op.Field != "" {
		value, _ := json.Marshal(op.Action)
		fields[op.Field] = value
	}
	full, err := json.Marshal(fields)
	if err != nil {
		return "", nil, err
	}
	return op.Tool, full, nil
}

func ctoOpTools() []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:        "cto_ops_list",
			Description: "List the rare CTO operations, grouped, each with its purpose and owner gate: direct (runs at once), proposal (owner approval) or irreversible (runs at once, no undo). Use filter to narrow. Run one with cto_op.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"filter": map[string]any{"type": "string", "description": "Text matched against name, purpose and group."}},
			},
		},
		{
			Name:        "cto_op",
			Description: "Run one rare CTO operation named by cto_ops_list, with its arguments. A wrong, missing or unknown argument returns that operation's full schema and an example, and runs nothing. Proposal and irreversible operations never fall back to defaults.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"method": map[string]any{"type": "string", "description": "Op name from cto_ops_list."},
					"args":   map[string]any{"type": "object", "description": "Arguments of that operation; the schema shows the fields."},
				},
				"required": []string{"method"},
			},
		},
	}
}

func addCTOOpTools(s *mcp.Server, call Caller, addTool ctoToolAdder) {
	for _, tool := range ctoOpTools() {
		name := tool.Name
		addTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := json.RawMessage(nil)
			if req != nil && req.Params != nil {
				args = req.Params.Arguments
			}
			if name == "cto_ops_list" {
				var list struct {
					Filter string `json:"filter"`
				}
				if len(args) > 0 && string(args) != "null" && json.Unmarshal(args, &list) != nil {
					return v2Error(fmt.Errorf("cto_ops_list arguments must be an object with an optional filter")), nil
				}
				raw, err := call(ctx, name, args)
				if err != nil {
					return v2Error(err), nil
				}
				var value any
				if json.Unmarshal(raw, &value) != nil {
					value = string(raw)
				}
				return ctoPlainResult(value)
			}
			var request struct {
				Method string          `json:"method"`
				Args   json.RawMessage `json:"args"`
			}
			if len(args) > 0 && json.Unmarshal(args, &request) != nil {
				return v2Error(fmt.Errorf("cto_op arguments must be an object with method and optional args")), nil
			}
			request.Method = strings.TrimSpace(request.Method)
			op, ok := CTOOpByName(request.Method)
			if !ok {
				return v2Error(fmt.Errorf("unknown cto_op %q; call cto_ops_list for the names", request.Method)), nil
			}
			if err := ValidateCTOOpArgs(op, request.Args); err != nil {
				return v2Error(err), nil
			}
			raw, err := call(ctx, name, args)
			if err != nil {
				return v2Error(err), nil
			}
			var value any
			if json.Unmarshal(raw, &value) != nil {
				value = string(raw)
			}
			if ctoOpNormalizesResult(op) {
				value = normalizeCTOProposalResult(value)
			}
			return ctoPlainResult(value)
		})
	}
}

// ctoOpNormalizesResult reports whether the blocked op returns through a change
// tool, whose results get the same envelope as the typed tool today.
func ctoOpNormalizesResult(op CTOOp) bool {
	if op.Method != "" {
		return true
	}
	switch op.Tool {
	case "graph_change", "session_change", "worker_change", "answer", "tools_change", "mcp_change", "settings_change", "project_change", "mcp_call", "proposal_ask", "cto_new", "sessions_archive":
		return true
	}
	return false
}

// ctoPlainResult returns a value as one text block and structured content,
// without a catalog output schema.
func ctoPlainResult(value any) (*mcp.CallToolResult, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return toolError(err)
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return toolError(err)
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		StructuredContent: normalized,
	}, nil
}
