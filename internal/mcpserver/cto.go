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
	"graph_get": {
		desc:   "Read the component graph with charters and edges. Read it at the start of a conversation and before any graph change.",
		fields: map[string]ctoField{"all": {typ: "boolean", desc: "Include inactive empty projects."}},
	},
	"charter_set": {
		desc: "Set one charter attribute of a node. An empty value clears the attribute. Show the change as a short diff and offer undo.",
		fields: map[string]ctoField{
			"node":  {typ: "string", desc: "Name of the node, as graph_get returns it."},
			"attr":  {typ: "string", desc: "Charter attribute to set. maturity holds a level and a short sentence; the other attributes hold free text.", enum: ctoCharterAttrs},
			"value": {typ: "string", desc: "New text for the attribute (at most 2 KiB). An empty string clears it. For maturity, start with early, growing, mature or critical and then add a sentence."},
		},
	},
	"charter_history": {
		desc: "Read the change history of one node's charter, newest first. Each row has the old and new value, who changed it and when.",
		fields: map[string]ctoField{
			"node":  {typ: "string", desc: "Node whose charter history to read."},
			"limit": {typ: "integer", desc: "Maximum number of changes, newest first. Default 1000, at most 1000."},
		},
	},
	"node_add": {
		desc: "Add an architect node: a node with no code of its own that can govern components. Show the change as a short diff and offer undo.",
		fields: map[string]ctoField{
			"name": {typ: "string", desc: "Name of the new architect node."},
		},
	},
	"node_remove": {
		desc: "Remove an architect node. It must have no edges; its settings are removed with it. Show the change as a short diff and offer undo.",
		fields: map[string]ctoField{
			"name": {typ: "string", desc: "Name of the architect node to remove."},
		},
	},
	"edge_set": {
		desc: "Add or replace a directed edge between two nodes. The kind decides the direction and meaning, and the rule decides what happens to a request from the from-node to the to-node. Show the change as a short diff and offer undo.",
		fields: map[string]ctoField{
			"from": {typ: "string", desc: "Source node: the consumer for depends, the architect for governs, the deployer for deploys."},
			"to":   {typ: "string", desc: "Target node: the provider for depends, the component for governs, the app it deploys for deploys."},
			"kind": {typ: "string", desc: "Edge kind. depends runs from a consumer to its provider. governs runs from an architect to a component, with a mode. deploys runs from a deployer to each app it deploys.", enum: ctoEdgeKinds},
			"mode": {typ: "string", desc: "Only for governs. advisor: the architect's opinion goes with the request and the component decides. arbiter: it settles a dispute between the nodes it governs. owner: its decision stands and the component follows it.", enum: []string{"advisor", "arbiter", "owner"}},
			"rule": {typ: "string", desc: "Request rule for requests from the from-node to the to-node. normal: deliver them and the receiver judges by its charter. auto: act on them as if the owner asked, with the owner's limits. ask: hold them until the owner decides. never: refuse them.", enum: []string{"normal", "auto", "ask", "never"}},
		},
	},
	"edge_remove": {
		desc: "Remove an edge between two nodes. Show the change as a short diff and offer undo.",
		fields: map[string]ctoField{
			"from": {typ: "string", desc: "Source node of the edge: the consumer, the architect or the deployer."},
			"to":   {typ: "string", desc: "Target node of the edge: the provider, the component or the deployed app."},
			"kind": {typ: "string", desc: "Edge kind: depends (consumer to provider), governs (architect to component) or deploys (deployer to app).", enum: ctoEdgeKinds},
		},
	},
	"edge_history": {
		desc: "Read the change history of edges, newest first. Give a node to see only the changes that involve it, or leave the node empty for every edge.",
		fields: map[string]ctoField{
			"node":  {typ: "string", desc: "Node whose edge changes to read. An empty string returns all edges."},
			"limit": {typ: "integer", desc: "Maximum number of changes, newest first. Default 1000, at most 1000."},
		},
	},
	"graph_undo": {
		desc: "Undo a graph change by its history row, or undo the latest change when id is omitted. Undo is recorded as another change and refuses a stale value.",
		fields: map[string]ctoField{
			"id": {typ: "integer", desc: "History row to undo. Omit it to undo the latest change."},
		},
	},
	"agents": {
		desc: "List compact agent session summaries with project, machine, folder, harness, model, status and task.",
		fields: map[string]ctoField{
			"text":           {typ: "string", desc: "Only agents whose name, function or task matches this text."},
			"harness":        {typ: "string", desc: "Only agents on this harness.", enum: []string{"opencode", "claude", "pi", "codex"}},
			"capability":     {typ: "string", desc: "Only agents with this announced capability."},
			"online_only":    {typ: "boolean", desc: "Only online agents when true."},
			"include_owners": {typ: "boolean", desc: "Include owner sessions."},
			"project":        {typ: "string", desc: "Only agents in this project."},
		},
	},
	"workers": {
		desc:   "List workers, newest first. Use next_before for the next page.",
		fields: map[string]ctoField{"limit": {typ: "integer", desc: "Page size, default 20, maximum 50."}, "before": {typ: "string", desc: "Cursor from next_before."}},
	},
	"needs_you": {
		desc: "List the agent sessions that wait for you: questions, escalations, approvals and requests held by an ask rule.",
	},
	"feed": {
		desc: "Read the network feed: recent events such as messages, worker starts, graph changes and approvals, newest first.",
		fields: map[string]ctoField{
			"limit":  {typ: "integer", desc: "Page size, default 20, maximum 50."},
			"before": {typ: "integer", desc: "Cursor from next_before."},
		},
	},
	"send": {
		desc: "Send a message to an agent session as the owner's CTO. Receivers treat it as the owner's word within their own charter's limits, and it carries a note only agent-net can write.",
		fields: map[string]ctoField{
			"to":   {typ: "string", desc: "Session ID, or an exact agent name that is unique."},
			"body": {typ: "string", desc: "Message text, sent as a request from the owner's CTO."},
		},
	},
	"worker_profile_list": {desc: "List all registered worker profiles, with their harness, model, effort, creator and time."},
	"worker_profile_add": {desc: "Register a worker profile only when the owner asks; model names resolve against installed harness model lists.", fields: map[string]ctoField{
		"name": {typ: "string", desc: "Optional profile name; defaults to the harness and resolved model."}, "harness": {typ: "string", desc: "Installed worker harness.", enum: []string{"opencode", "claude", "pi", "codex"}}, "model": {typ: "string", desc: "Model ID or loose model name."}, "effort": {typ: "string", desc: "Optional reasoning or thinking effort."},
	}},
	"worker_profile_remove": {desc: "Remove a registered worker profile by name only when the owner asks for that removal.", fields: map[string]ctoField{"name": {typ: "string", desc: "Name of the registered profile to remove."}}},
	"worker_providers_set": {
		desc:   "Set the network worker provider allowlist only when the owner asks. Pass provider IDs as an array; omit providers to restore the defaults.",
		fields: map[string]ctoField{"providers": {typ: "array", desc: "Provider IDs to allow. Omit this field to restore the subscription defaults."}},
	},
	"score_set": {
		desc: "Set or clear an owner model capability score when the owner asks. Scores range from 0 to 100 and override the bundled table.",
		fields: map[string]ctoField{
			"model": {typ: "string", desc: "Model ID or loose model pattern to score."},
			"score": {typ: "number", desc: "Capability score from 0 to 100. Omit to clear the owner override."},
		},
	},
	"worker_start": {
		desc: "Start a worker in a project on one of the owner's machines. The worker gets its own git worktree by default, and its report comes back as a message. Only do this when the owner asks.",
		fields: map[string]ctoField{
			"project":    {typ: "string", desc: "Known project name or absolute path. Empty uses the CTO's own project."},
			"harness":    {typ: "string", desc: "Harness for the worker. Empty lets automatic model selection choose one.", enum: []string{"opencode", "claude", "pi", "codex"}},
			"model":      {typ: "string", desc: "Model as the harness names it, or a loose name such as deepseek 4.1. Empty or auto selects by difficulty and quota."},
			"difficulty": {typ: "string", desc: "Task difficulty for automatic model selection; defaults to normal.", enum: []string{"trivial", "simple", "normal", "hard", "very_complex"}},
			"base":       {typ: "string", desc: "Existing branch to base the worker worktree on."},
			"continue":   {typ: "string", desc: "Session or worker ID of an ended worker to continue."},
			"task":       {typ: "string", desc: "The worker's task: a brief for an agent that knows nothing about this conversation, with the goal, files, limits and what to report."},
			"machine":    {typ: "string", desc: "Machine that holds the project, when more than one does."},
			"isolation":  {typ: "string", desc: "worktree gives the worker its own git worktree (the default in a git repository). none works in the project folder, for read-only tasks.", enum: []string{"worktree", "none"}},
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
			if name == "charter_set" && (k == "attr" || k == "value") {
				description += "\nPer-attribute charter guide:\n" + protocol.CharterGuideText() +
					"\nKeep each value under about 200 characters. Never include secrets, IPs, tokens or paths; put detail in AGENTS.md."
			}
			prop := map[string]any{"type": f.typ, "description": description}
			if f.properties != nil {
				prop["properties"] = f.properties
				prop["additionalProperties"] = false
			}
			if f.typ == "array" {
				prop["items"] = map[string]any{"type": "string"}
			}
			if len(f.enum) > 0 {
				prop["enum"] = f.enum
			}
			props[k] = prop
		}
		toolDescription := spec.desc
		if name == "charter_set" {
			toolDescription += "\nCharters are short rules for other agents, read on every turn. Keep each value under about 200 characters; never include secrets, IPs, tokens or paths.\nPer-attribute charter guide:\n" + protocol.CharterGuideText()
		}
		tool := &mcp.Tool{Name: name, Description: toolDescription, InputSchema: map[string]any{"type": "object", "properties": props, "additionalProperties": false}}
		method := name
		s.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			raw, err := call(ctx, method, req.Params.Arguments)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
			}
			text := string(raw)
			if method == "charter_set" {
				var set protocol.CharterSetRequest
				if b, marshalErr := json.Marshal(req.Params.Arguments); marshalErr == nil && json.Unmarshal(b, &set) == nil {
					if warning := protocol.CharterValueWarning(set.Attr, set.Value); warning != "" {
						text += "\n" + warning
					}
				}
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
