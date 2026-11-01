package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

// ctoField is one input field of a CTO tool. A fixed set of values carries an
// enum, so the model sees the allowed values in the schema as well as the text.
type ctoField struct {
	typ        string
	desc       string
	enum       []string
	properties map[string]any
	items      map[string]any
}

// ctoToolDef is one CTO tool: its description and its input fields.
type ctoToolDef struct {
	compact bool // Internal concrete tools omit redundant short scalar labels.
	desc    string
	fields  map[string]ctoField
}

var ctoCharterAttrs = func() []string {
	guide := protocol.CharterGuide()
	attrs := make([]string, 0, len(guide))
	for _, attr := range guide {
		attrs = append(attrs, attr.Name)
	}
	return attrs
}()

var ctoEdgeKinds = []string{"depends", "governs", "deploys"}

var ctoNodeSettings = []string{"tests_before_merge", "merge_authority", "worker_sandbox", "judge_consumers", "deploy_rule"}

// ctoRoutingField is the field that selects the concrete operation of a merged
// tool. The daemon keeps the authoritative action table.
var ctoRoutingField = map[string]string{
	"message":         "action",
	"ui":              "action",
	"graph_change":    "action",
	"session_change":  "action",
	"worker_change":   "action",
	"answer":          "kind",
	"tools_change":    "action",
	"mcp_change":      "action",
	"settings_change": "area",
	"project_change":  "action",
}

// ctoTools is the single place to add a CTO tool and its argument schema. Every
// change is one action tool, so the whole list stays small.
var ctoTools = map[string]ctoToolDef{
	"workers":   {desc: "List workers or read one.", fields: map[string]ctoField{"id": {typ: "string"}, "limit": {typ: "integer"}, "offset": {typ: "integer"}}},
	"tools":     {desc: "List shared MCP servers.", fields: map[string]ctoField{"view": {typ: "string", enum: []string{"mcp"}}, "limit": {typ: "integer"}, "offset": {typ: "integer"}}},
	"settings":  {desc: "List public worker profiles.", fields: map[string]ctoField{"area": {typ: "string", enum: []string{"worker_profiles"}}, "limit": {typ: "integer"}, "offset": {typ: "integer"}}},
	"graph":     {desc: "Read the public graph: charters, maturity, rules, edges; one node's full charter and rules; view guide.", fields: map[string]ctoField{"view": {typ: "string"}, "node": {typ: "string"}, "limit": {typ: "integer"}, "offset": {typ: "integer"}}},
	"machines":  {desc: "List machines or a harness's models.", fields: map[string]ctoField{"machine": {typ: "string"}, "harness": {typ: "string"}, "limit": {typ: "integer"}, "offset": {typ: "integer"}}},
	"proposals": {desc: "List own proposals or read one.", fields: map[string]ctoField{"id": {typ: "string"}, "limit": {typ: "integer"}, "offset": {typ: "integer"}}},
	"feed":      {desc: "List own conversation metadata.", fields: map[string]ctoField{"limit": {typ: "integer"}, "offset": {typ: "integer"}}},
	"overview":  {desc: "Public network counts."},
	"sessions":  {desc: "List sessions.", fields: map[string]ctoField{"limit": {typ: "integer"}, "offset": {typ: "integer"}, "text": {typ: "string"}, "project": {typ: "string"}, "harness": {typ: "string"}, "capability": {typ: "string"}, "online_only": {typ: "boolean"}, "include_owners": {typ: "boolean"}}},
	"session":   {desc: "Read a session profile.", fields: map[string]ctoField{"session_id": {typ: "string"}}},
	"projects":  {desc: "List projects.", fields: map[string]ctoField{"name": {typ: "string", desc: "Filter."}, "limit": {typ: "integer"}, "offset": {typ: "integer"}}},
	"messages":  {desc: "List own messages or read one.", fields: map[string]ctoField{"message_id": {typ: "string"}, "conversation_id": {typ: "string"}, "limit": {typ: "integer"}, "offset": {typ: "integer"}}},

	"message":   {desc: "Send or reply; as_owner proposes.", fields: map[string]ctoField{"action": {typ: "string", enum: []string{"send", "reply"}}, "to": {typ: "string"}, "message_id": {typ: "string"}, "body": {typ: "string"}, "as_owner": {typ: "boolean"}}},
	"ask_owner": {desc: "Ask the owner a question with a recommendation.", fields: map[string]ctoField{"question": {typ: "string", desc: "Question, one line."}, "options": {typ: "array", desc: "Up to four short answers."}, "recommendation": {typ: "string", desc: "Your recommendation."}, "urgency": {typ: "string", desc: "Normal or high.", enum: []string{"normal", "high"}}}},
	"learn":     {desc: "Record a finding.", fields: map[string]ctoField{"subject_kind": {typ: "string", desc: "Kind.", enum: []string{"model", "harness", "model_harness", "project", "mcp", "machine"}}, "subject": {typ: "string", desc: "Subject."}, "finding": {typ: "string", desc: "Finding."}, "evidence": {typ: "array", desc: "Evidence."}}},
	"ui":        {desc: "Show a TUI view or a notice.", fields: map[string]ctoField{"action": {typ: "string", enum: []string{"show", "notice"}}, "tab": {typ: "string", enum: []string{"sessions", "messages", "quotas", "graph", "tools"}}, "select": {typ: "string", desc: "Selection."}, "filter": {typ: "string", desc: "Filter."}, "why": {typ: "string", desc: "Reason."}, "text": {typ: "string", desc: "Notice."}}},

	"graph_change": {
		desc: "Change the graph: charter_set, node_add, node_remove, edge_set, edge_remove, settings_set or undo. Minor changes apply; major ones are proposals.",
		fields: map[string]ctoField{
			"action":  {typ: "string", enum: []string{"charter_set", "node_add", "node_remove", "edge_set", "edge_remove", "settings_set", "undo"}},
			"node":    {typ: "string", desc: "Node."},
			"attr":    {typ: "string", desc: "Attribute.", enum: ctoCharterAttrs},
			"value":   {typ: "string", desc: "Value; empty clears."},
			"name":    {typ: "string", desc: "Node name."},
			"from":    {typ: "string", desc: "Source."},
			"to":      {typ: "string", desc: "Target."},
			"kind":    {typ: "string", desc: "Consumer to provider, architect to component, deployer to app.", enum: ctoEdgeKinds},
			"mode":    {typ: "string", desc: "Advise, arbitrate, decide.", enum: []string{"advisor", "arbiter", "owner"}},
			"rule":    {typ: "string", desc: "Deliver, own, hold, refuse.", enum: []string{"normal", "auto", "ask", "never"}},
			"id":      {typ: "integer", desc: "History ID; omit for latest."},
			"setting": {typ: "string", desc: "Node setting.", enum: ctoNodeSettings},
		},
	},
	"session_change": {
		desc: "Change a session: rename, trust or policy.",
		fields: map[string]ctoField{
			"action":     {typ: "string", enum: []string{"rename", "trust", "policy"}},
			"session_id": {typ: "string", desc: "Session ID."},
			"name":       {typ: "string", desc: "Name; empty resets."},
			"on":         {typ: "boolean", desc: "Enable trust."},
			"scope":      {typ: "string", desc: "Default session.", enum: []string{"session", "project"}},
			"policy":     {typ: "object", desc: "Policy."},
			"dry_run":    {typ: "boolean", desc: "Preview."},
		},
	},
	"worker_change": {
		desc: "Change a worker: start or stop.",
		fields: map[string]ctoField{
			"action":     {typ: "string", enum: []string{"start", "stop"}},
			"project":    {typ: "string", desc: "Project."},
			"harness":    {typ: "string", desc: "Harness; empty is auto.", enum: []string{"opencode", "claude", "pi", "codex"}},
			"model":      {typ: "string", desc: "Model; empty is auto."},
			"difficulty": {typ: "string", desc: "Default normal.", enum: []string{"trivial", "simple", "normal", "hard", "very_complex"}},
			"base":       {typ: "string", desc: "Branch."},
			"continue":   {typ: "string", desc: "Worker ID."},
			"task":       {typ: "string", desc: "Brief and limits."},
			"machine":    {typ: "string", desc: "Machine."},
			"isolation":  {typ: "string", desc: "None reads only; default worktree.", enum: []string{"worktree", "none"}},
			"session":    {typ: "string", desc: "Worker or queue ID."},
		},
	},
	"answer": {
		desc: "Answer a held request, an inbox item or an outcome.",
		fields: map[string]ctoField{
			"kind":       {typ: "string", enum: []string{"held", "inbox", "outcome"}},
			"message_id": {typ: "string", desc: "Held request."},
			"id":         {typ: "string", desc: "Inbox item."},
			"answer":     {typ: "string", desc: "Answer."},
			"reply_id":   {typ: "string", desc: "Outcome reply."},
			"action":     {typ: "string", desc: "Held: a, A, r, R. Outcome: let_stand, overrule.", enum: []string{"a", "A", "r", "R", "let_stand", "overrule"}},
		},
	},
	"tools_change": {
		desc: "Change tools: plan, apply, undo, import or native_remove.",
		fields: map[string]ctoField{
			"action":       {typ: "string", enum: []string{"plan", "apply", "undo", "import", "native_remove"}},
			"item":         {typ: "string", desc: "Item ID."},
			"target":       {typ: "object", desc: "Assignment."},
			"operation":    {typ: "string", desc: "Operation.", enum: []string{"repo_remove"}},
			"project":      {typ: "string", desc: "Project."},
			"repo_targets": {typ: "array", desc: "Checkouts."},
			"plan_id":      {typ: "string", desc: "Plan ID."},
			"candidate":    {typ: "string", desc: "Candidate."},
			"undo_plan_id": {typ: "string", desc: "Removal ID."},
		},
	},
	"mcp_change": {
		desc: "Change shared MCP servers: add, remove, test or enable.",
		fields: map[string]ctoField{
			"action":  {typ: "string", enum: []string{"add", "remove", "test", "enable"}},
			"server":  {typ: "object", desc: "Configuration."},
			"name":    {typ: "string", desc: "Server name."},
			"enabled": {typ: "boolean", desc: "Enable."},
			"on":      {typ: "string", desc: "Host; local default."},
		},
	},
	"mcp_call": {desc: "Call a tool on a shared MCP server.", fields: map[string]ctoField{"server": {typ: "string", desc: "Server name."}, "tool": {typ: "string", desc: "Tool."}, "arguments": {typ: "object", desc: "Arguments."}}},
	"cto_new":  {desc: "Start a new conversation for the active harness."},
	"settings_change": {
		desc: "Change owner settings: worker profiles, providers incl. protected providers, scores, integrations, notifications, CTO, playbook, machine name, invite or onboarding.",
		fields: map[string]ctoField{
			"area":                {typ: "string", enum: []string{"worker_profile_add", "worker_profile_remove", "worker_providers", "protected_providers", "score", "integrations", "notifications", "cto", "playbook", "machine_name", "invite", "onboarding"}},
			"name":                {typ: "string", desc: "Profile or machine name; empty clears."},
			"harness":             {typ: "string", desc: "Harness.", enum: []string{"opencode", "claude", "pi", "codex"}},
			"model":               {typ: "string", desc: "Model ID or name."},
			"effort":              {typ: "string", desc: "Effort."},
			"providers":           {typ: "array", desc: "Provider IDs; omit for defaults."},
			"protected_providers": {typ: "array", desc: "Provider IDs."},
			"score":               {typ: "number", desc: "0-100; omit to clear."},
			"disabled":            {typ: "boolean", desc: "Notifications off if true."},
			"peer_messages":       {typ: "boolean", desc: "Messages."},
			"codex_tools":         {typ: "boolean", desc: "Codex."},
			"hint_hook":           {typ: "boolean", desc: "Hints."},
			"policy_hook":         {typ: "boolean", desc: "Policy."},
			"needs_hook":          {typ: "boolean", desc: "Needs you."},
			"workers_mod":         {typ: "boolean", desc: "Workers."},
			"quota_axi":           {typ: "boolean", desc: "Quotas."},
			"title":               {typ: "string", desc: "Name on screens."},
			"compaction":          {typ: "object", desc: "Compaction."},
			"note":                {typ: "string", desc: "Playbook note; empty clears."},
			"pin":                 {typ: "boolean", desc: "Pin."},
			"node_id":             {typ: "string", desc: "Machine."},
		},
	},
	"project_change": {
		desc: "Change projects: add, remove or display.",
		fields: map[string]ctoField{
			"action":  {typ: "string", enum: []string{"add", "remove", "display"}},
			"paths":   {typ: "array", desc: "Paths."},
			"dir":     {typ: "string", desc: "Directory."},
			"scan":    {typ: "boolean", desc: "Scan the directory."},
			"depth":   {typ: "integer", desc: "3; max 8."},
			"dry_run": {typ: "boolean", desc: "Preview."},
			"name":    {typ: "string", desc: "Project name; empty clears."},
			"path":    {typ: "string", desc: "Path."},
			"project": {typ: "string", desc: "Project."},
		},
	},
}

// Nested schemas make the valid settings and credential sources visible to the
// model, while the daemon remains the final validator.
func init() {
	property := func(typ, desc string, values ...string) map[string]any {
		p := map[string]any{"type": typ, "description": desc}
		if len(values) > 0 {
			p["enum"] = values
		}
		return p
	}
	stringsArray := func(desc string) map[string]any {
		return map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": "string"}}
	}
	setProperties := func(tool, field string, props map[string]any) {
		spec := ctoTools[tool]
		f := spec.fields[field]
		f.properties = props
		spec.fields[field] = f
		ctoTools[tool] = spec
	}
	setProperties("session_change", "policy", map[string]any{
		"delegate":    property("string", "Mode.", "", "off", "native", "workers"),
		"coder_model": property("string", "Coder."), "main_model": property("string", "Main."),
		"workers": property("string", "Sandbox.", "", "sandboxed", "no-network", "read-only"),
	})
	setProperties("mcp_change", "server", map[string]any{
		"name": property("string", "Server name."), "transport": property("string", "Transport.", "stdio", "http", "sse"),
		"command": property("string", "Command."), "args": stringsArray("No secrets."), "env": stringsArray("Names, not values."),
		"url": property("string", "Endpoint."), "timeout_sec": property("integer", "Seconds."),
		"headers": map[string]any{"type": "array", "description": "Headers.", "items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"name": property("string", "Name."), "env": property("string", "Env."), "file": property("string", "Path.")}}},
	})
	setProperties("settings_change", "compaction", map[string]any{
		"threshold_percent":    property("integer", "Percent."),
		"cache_expiry_enabled": property("boolean", "Default true."),
		"cache_window_seconds": map[string]any{"type": "object", "description": "Seconds per provider.", "additionalProperties": map[string]any{"type": "integer"}},
	})
}

// Schema metadata only. The daemon owns the authoritative class table.
var ctoProposalReasons = func() map[string]bool {
	out := map[string]bool{}
	for _, name := range strings.Fields("message graph_change session_change worker_change answer tools_change mcp_change mcp_call settings_change project_change cto_new") {
		out[name] = true
	}
	return out
}()

// CTOArguments validates the same schema advertised by the CTO MCP server, or
// the internal concrete schema of a stored call. Old stored proposals keep
// working after a rename.
func CTOArguments(name string, raw json.RawMessage) error {
	spec, ok := ctoTools[name]
	if !ok {
		spec, ok = ctoConcreteTools[name]
	}
	if !ok {
		return fmt.Errorf("unknown CTO tool %q", name)
	}
	schemaRaw, err := json.Marshal(ctoSchema(name, spec))
	if err != nil {
		return err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		return err
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return err
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	return resolved.Validate(value)
}

// IsCTOConcreteTool reports whether name is an internal concrete change tool.
// The names are not exposed in the MCP tool list; they serve stored proposals
// and validation only.
func IsCTOConcreteTool(name string) bool {
	_, ok := ctoConcreteTools[name]
	return ok
}

// Nested field names and enums carry their meaning; keep descriptions on the
// top-level fields and tools while avoiding repeated labels in nested objects.
func compactCTONested(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			if k != "description" {
				out[k] = compactCTONested(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = compactCTONested(item)
		}
		return out
	default:
		return value
	}
}

func ctoSchema(name string, spec ctoToolDef) map[string]any {
	props := map[string]any{}
	if ctoProposalReasons[name] {
		props["reason"] = map[string]any{"type": "string", "description": "Why"}
	}
	for k, f := range spec.fields {
		description := strings.TrimSuffix(f.desc, ".")
		if description == "" {
			description = k
		}

		prop := map[string]any{"type": f.typ}
		if description != "" && !(spec.compact && len(description) <= 8) {
			prop["description"] = description
		}
		if f.properties != nil {
			prop["properties"] = compactCTONested(f.properties)
			prop["additionalProperties"] = false
		}
		if f.typ == "array" {
			if f.items != nil {
				prop["items"] = compactCTONested(f.items)
			} else {
				prop["items"] = map[string]any{"type": "string"}
			}
		}
		if len(f.enum) > 0 {
			prop["enum"] = f.enum
		}
		props[k] = prop
	}
	schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if field := ctoRoutingField[name]; field != "" {
		schema["required"] = []string{field}
	}
	return schema
}

// NewCTO is deliberately separate from the network MCP registry. ctoTools is
// the single place to add a CTO tool and its argument schema.
func NewCTO(call Caller, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "agent-net-cto", Version: version}, nil)
	for name, spec := range ctoTools {
		tool := &mcp.Tool{Name: name, Description: strings.TrimSuffix(spec.desc, "."), InputSchema: ctoSchema(name, spec)}
		method := name
		s.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			raw, err := call(ctx, method, req.Params.Arguments)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
			}
			text := string(raw)
			var message string
			if json.Unmarshal(raw, &message) == nil {
				text = message
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
		})
	}
	return s
}

// CTOToolNames returns the exposed CTO tool names, sorted.
func CTOToolNames() []string {
	names := make([]string, 0, len(ctoTools))
	for name := range ctoTools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func RunCTO(ctx context.Context, socket, version string) error {
	c, err := client.Dial(socket)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err = c.Call(ctx, "hello", map[string]string{"client": "cto-mcp"}); err != nil {
		return fmt.Errorf("CTO channel: %w", err)
	}
	s := NewCTO(func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		return c.Call(ctx, "cto_tool", map[string]any{"name": method, "arguments": params})
	}, version)
	return s.Run(ctx, &mcp.StdioTransport{})
}
