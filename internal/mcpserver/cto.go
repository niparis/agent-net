package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
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
	compact bool // Legacy parity tools omit redundant short scalar labels.
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

// ctoTools is the single place to add a CTO tool and its argument schema. The
// descriptions are written from docs/reference.md: what each tool does, the
// allowed values, and what an edge rule does to a request.
var ctoTools = map[string]ctoToolDef{
	"workers":               {desc: "List workers or read one.", fields: map[string]ctoField{"id": {typ: "string"}, "limit": {typ: "integer"}, "offset": {typ: "integer"}}},
	"tools":                 {desc: "List shared MCP servers.", fields: map[string]ctoField{"view": {typ: "string", enum: []string{"mcp"}}, "limit": {typ: "integer"}, "offset": {typ: "integer"}}},
	"settings":              {desc: "List public worker profiles.", fields: map[string]ctoField{"area": {typ: "string", enum: []string{"worker_profiles"}}, "limit": {typ: "integer"}, "offset": {typ: "integer"}}},
	"graph":                 {desc: "List public project nodes or read the guide.", fields: map[string]ctoField{"view": {typ: "string"}, "node": {typ: "string"}, "limit": {typ: "integer"}, "offset": {typ: "integer"}}},
	"machines":              {desc: "List machines or a harness's models.", fields: map[string]ctoField{"machine": {typ: "string"}, "harness": {typ: "string"}, "limit": {typ: "integer"}, "offset": {typ: "integer"}}},
	"proposals":             {desc: "List own proposals or read one.", fields: map[string]ctoField{"id": {typ: "string"}, "limit": {typ: "integer"}, "offset": {typ: "integer"}}},
	"feed":                  {desc: "List own conversation metadata.", fields: map[string]ctoField{"limit": {typ: "integer"}, "offset": {typ: "integer"}}},
	"overview":              {desc: "Public network counts."},
	"sessions":              {desc: "List sessions.", fields: map[string]ctoField{"limit": {typ: "integer"}, "offset": {typ: "integer"}, "text": {typ: "string"}, "project": {typ: "string"}, "harness": {typ: "string"}, "capability": {typ: "string"}, "online_only": {typ: "boolean"}, "include_owners": {typ: "boolean"}}},
	"session":               {desc: "Read a session profile.", fields: map[string]ctoField{"session_id": {typ: "string"}}},
	"charter_guide":         {desc: "Read charter guide.", fields: map[string]ctoField{}},
	"onboarding_restart":    {desc: "Restart the getting-started steps.", fields: map[string]ctoField{}},
	"notification_settings": {desc: "Propose notifications.", fields: map[string]ctoField{"disabled": {typ: "boolean", desc: "Off if true."}}},
	"charter_set": {
		desc: "Set a charter attribute.",
		fields: map[string]ctoField{
			"node":  {typ: "string", desc: "Node."},
			"attr":  {typ: "string", desc: "Attribute.", enum: ctoCharterAttrs},
			"value": {typ: "string", desc: "2 KiB max; empty clears."},
		},
	},
	"node_add": {
		desc: "Add an architect node.",
		fields: map[string]ctoField{
			"name": {typ: "string", desc: "Name."},
		},
	},
	"node_remove": {
		desc: "Remove an edgeless architect.",
		fields: map[string]ctoField{
			"name": {typ: "string", desc: "Name."},
		},
	},
	"edge_set": {
		desc: "Set a directed edge.",
		fields: map[string]ctoField{
			"from": {typ: "string", desc: "Source."},
			"to":   {typ: "string", desc: "Target."},
			"kind": {typ: "string", desc: "Consumer to provider, architect to component, deployer to app.", enum: ctoEdgeKinds},
			"mode": {typ: "string", desc: "Advise, arbitrate, decide.", enum: []string{"advisor", "arbiter", "owner"}},
			"rule": {typ: "string", desc: "Deliver, own, hold, refuse.", enum: []string{"normal", "auto", "ask", "never"}},
		},
	},
	"edge_remove": {
		desc: "Remove a graph edge.",
		fields: map[string]ctoField{
			"from": {typ: "string", desc: "Source."},
			"to":   {typ: "string", desc: "Target."},
			"kind": {typ: "string", desc: "Kind and direction.", enum: ctoEdgeKinds},
		},
	},
	"graph_undo": {
		desc: "Undo a graph change.",
		fields: map[string]ctoField{
			"id": {typ: "integer", desc: "ID; omit for latest."},
		},
	},
	"agents": {
		desc: "List agent sessions.",
		fields: map[string]ctoField{
			"text":           {typ: "string", desc: "Name or task."},
			"harness":        {typ: "string", desc: "Harness.", enum: []string{"opencode", "claude", "pi", "codex"}},
			"capability":     {typ: "string", desc: "Capability."},
			"online_only":    {typ: "boolean", desc: "Online only."},
			"include_owners": {typ: "boolean", desc: "Include owners."},
			"project":        {typ: "string", desc: "Project."},
		},
	},
	"message": {desc: "Send or reply; as_owner proposes.", fields: map[string]ctoField{"action": {typ: "string", enum: []string{"send", "reply"}}, "to": {typ: "string"}, "message_id": {typ: "string"}, "body": {typ: "string"}, "as_owner": {typ: "boolean"}}},

	"send": {
		desc: "Send a CTO request.",
		fields: map[string]ctoField{
			"to":   {typ: "string", desc: "Session ID or unique name."},
			"body": {typ: "string", desc: "Request."},
		},
	},
	"session_rename": {
		desc: "Rename a session for the owner.",
		fields: map[string]ctoField{
			"session_id": {typ: "string", desc: "Session ID."},
			"name":       {typ: "string", desc: "Name; empty resets."},
		},
	},
	"worker_profile_list": {desc: "List worker profiles."},
	"worker_profile_add": {desc: "Set a worker profile.", fields: map[string]ctoField{
		"name": {typ: "string", desc: "Replaces an existing name."}, "harness": {typ: "string", desc: "Harness."}, "model": {typ: "string", desc: "Model ID or name."}, "effort": {typ: "string", desc: "Effort."},
	}},
	"worker_profile_remove":   {desc: "Remove a worker profile.", fields: map[string]ctoField{"name": {typ: "string", desc: "Name."}}},
	"protected_providers_set": {desc: "Set providers; ask the owner.", fields: map[string]ctoField{"protected_providers": {typ: "array", desc: "Provider IDs."}, "reason": {typ: "string", desc: "Reason."}}},
	"worker_providers_set": {
		desc:   "Set worker providers.",
		fields: map[string]ctoField{"providers": {typ: "array", desc: "IDs; omit for defaults."}},
	},
	"score_set": {
		desc: "Set a model score.",
		fields: map[string]ctoField{
			"model": {typ: "string", desc: "Model or pattern."},
			"score": {typ: "number", desc: "0-100; omit to clear."},
		},
	},
	"worker_start": {
		desc: "Start a worker.",
		fields: map[string]ctoField{
			"project":    {typ: "string", desc: "Default CTO project."},
			"harness":    {typ: "string", desc: "Harness; empty is auto.", enum: []string{"opencode", "claude", "pi", "codex"}},
			"model":      {typ: "string", desc: "Model; empty is auto."},
			"difficulty": {typ: "string", desc: "Default normal.", enum: []string{"trivial", "simple", "normal", "hard", "very_complex"}},
			"base":       {typ: "string", desc: "Branch."},
			"continue":   {typ: "string", desc: "Worker ID."},
			"task":       {typ: "string", desc: "Brief and limits."},
			"machine":    {typ: "string", desc: "Machine."},
			"isolation":  {typ: "string", desc: "None reads only; default worktree.", enum: []string{"worktree", "none"}},
		},
	},
}

// Schema metadata only. The daemon owns the authoritative class table.
var ctoProposalReasons = func() map[string]bool {
	out := map[string]bool{}
	for _, name := range strings.Fields("message project_display_set charter_set node_add node_remove edge_set edge_remove graph_undo worker_profile_add worker_profile_remove worker_providers_set protected_providers_set score_set worker_start project_add project_remove mcp_call session_trust session_policy node_rename session_rename integrations_set worker_stop held_answer inbox_answer outcome_overrule node_settings_set playbook_set mcp_add mcp_remove mcp_test mcp_enable cto_send cto_new tools_plan tools_apply tools_undo mcp_import mcp_native_remove invite onboarding_restart notification_settings cto_settings") {
		out[name] = true
	}
	return out
}()

// CTOArguments validates the same schema advertised by the CTO MCP server.
func CTOArguments(name string, raw json.RawMessage) error {
	spec, ok := ctoTools[name]
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
	if name == "message" {
		schema["required"] = []string{"action"}
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
			if method == "charter_guide" {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: protocol.CharterGuideText()}}}, nil
			}
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
