package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

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
	desc   string
	fields map[string]ctoField
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
	"notification_settings": {desc: "Read or set notifications.", fields: map[string]ctoField{"disabled": {typ: "boolean", desc: "Off if true; omit to read."}}},
	"charter_suggestions":   {desc: "Read charter suggestions.", fields: map[string]ctoField{}},
	"graph_get": {
		desc:   "Read graph and charters.",
		fields: map[string]ctoField{"all": {typ: "boolean", desc: "Include empty projects."}},
	},
	"charter_set": {
		desc: "Set a charter attribute.",
		fields: map[string]ctoField{
			"node":  {typ: "string", desc: "Node."},
			"attr":  {typ: "string", desc: "Attribute.", enum: ctoCharterAttrs},
			"value": {typ: "string", desc: "2 KiB max; empty clears; budget 1200 characters per charter, at most 6 items in accepts, refuses and escalate, and 40 characters of accepts and refuses visible to neighbors."},
		},
	},
	"charter_history": {
		desc: "Read charter history.",
		fields: map[string]ctoField{
			"node":  {typ: "string", desc: "Node."},
			"limit": {typ: "integer", desc: "Limit; default/max 1000."},
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
			"kind": {typ: "string", desc: "Consumer to provider, architect to component, or deployer to app.", enum: ctoEdgeKinds},
			"mode": {typ: "string", desc: "Governs: advise, arbitrate, or decide.", enum: []string{"advisor", "arbiter", "owner"}},
			"rule": {typ: "string", desc: "Deliver, act as owner, hold, or refuse.", enum: []string{"normal", "auto", "ask", "never"}},
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
	"edge_history": {
		desc: "Read edge history.",
		fields: map[string]ctoField{
			"node":  {typ: "string", desc: "Node; empty means all."},
			"limit": {typ: "integer", desc: "Limit; default/max 1000."},
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
	"workers": {
		desc:   "List workers, newest first.",
		fields: map[string]ctoField{"limit": {typ: "integer", desc: "Default 20; max 50."}, "before": {typ: "string", desc: "Cursor."}},
	},
	"needs_you": {
		desc: "List waiting sessions.",
	},
	"feed": {
		desc: "Read network feed.",
		fields: map[string]ctoField{
			"limit":  {typ: "integer", desc: "Default 20; max 50."},
			"before": {typ: "integer", desc: "Cursor."},
		},
	},
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
		"name": {typ: "string", desc: "An existing name replaces it."}, "harness": {typ: "string", desc: "Harness."}, "model": {typ: "string", desc: "Model ID or name."}, "effort": {typ: "string", desc: "Effort."},
	}},
	"worker_profile_remove":   {desc: "Remove a worker profile.", fields: map[string]ctoField{"name": {typ: "string", desc: "Name."}}},
	"protected_providers_get": {desc: "Read protected providers."},
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
			"project":    {typ: "string", desc: "Project; empty uses CTO project."},
			"harness":    {typ: "string", desc: "Harness; empty is auto.", enum: []string{"opencode", "claude", "pi", "codex"}},
			"model":      {typ: "string", desc: "Model; empty is auto."},
			"difficulty": {typ: "string", desc: "Default normal.", enum: []string{"trivial", "simple", "normal", "hard", "very_complex"}},
			"base":       {typ: "string", desc: "Branch."},
			"continue":   {typ: "string", desc: "Worker ID."},
			"task":       {typ: "string", desc: "Task brief and limits."},
			"machine":    {typ: "string", desc: "Machine."},
			"isolation":  {typ: "string", desc: "None is read-only; default worktree.", enum: []string{"worktree", "none"}},
		},
	},
}

// NewCTO is deliberately separate from the network MCP registry. ctoTools is
// the single place to add a CTO tool and its argument schema.
func NewCTO(call Caller, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "agent-net-cto", Version: version}, nil)
	for name, spec := range ctoTools {
		props := map[string]any{}
		for k, f := range spec.fields {
			description := f.desc

			prop := map[string]any{"type": f.typ, "description": description}
			if f.properties != nil {
				prop["properties"] = f.properties
				prop["additionalProperties"] = false
			}
			if f.typ == "array" {
				if f.items != nil {
					prop["items"] = f.items
				} else {
					prop["items"] = map[string]any{"type": "string"}
				}
			}
			if len(f.enum) > 0 {
				prop["enum"] = f.enum
			}
			props[k] = prop
		}
		toolDescription := spec.desc
		tool := &mcp.Tool{Name: name, Description: toolDescription, InputSchema: map[string]any{"type": "object", "properties": props, "additionalProperties": false}}
		method := name
		s.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			raw, err := call(ctx, method, req.Params.Arguments)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
			}
			text := string(raw)
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
