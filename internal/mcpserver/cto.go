package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/client"
)

// ctoField is one input field of a CTO tool. A fixed set of values carries an
// enum, so the model sees the allowed values in the schema as well as the text.
type ctoField struct {
	typ  string
	desc string
	enum []string
}

// ctoToolDef is one CTO tool: its description and its input fields.
type ctoToolDef struct {
	desc   string
	fields map[string]ctoField
}

var ctoCharterAttrs = []string{"purpose", "maturity", "accepts", "refuses", "feature_requests", "consumers", "escalate", "needs", "notes"}

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
		desc: "List the agent sessions on the network, with their project, task, expertise and whether they wait for the owner.",
		fields: map[string]ctoField{
			"text":    {typ: "string", desc: "Only agents whose name, function or task matches this text."},
			"project": {typ: "string", desc: "Only agents in this project."},
		},
	},
	"workers": {
		desc: "List the workers on the network, with machine, project, harness, model, owner, state and start time.",
	},
	"needs_you": {
		desc: "List the agent sessions that wait for you: questions, escalations, approvals and requests held by an ask rule.",
	},
	"feed": {
		desc: "Read the network feed: recent events such as messages, worker starts, graph changes and approvals, newest first.",
		fields: map[string]ctoField{
			"limit": {typ: "integer", desc: "Maximum number of events, newest first."},
		},
	},
	"send": {
		desc: "Send a message to an agent session as the owner's CTO. Receivers treat it as the owner's word within their own charter's limits, and it carries a note only agent-net can write.",
		fields: map[string]ctoField{
			"to":   {typ: "string", desc: "Session ID, or an exact agent name that is unique."},
			"body": {typ: "string", desc: "Message text, sent as a request from the owner's CTO."},
		},
	},
	"worker_start": {
		desc: "Start a worker in a project on one of the owner's machines. The worker gets its own git worktree by default, and its report comes back as a message. Only do this when the owner asks.",
		fields: map[string]ctoField{
			"project":   {typ: "string", desc: "Known project name or absolute path. Empty uses the CTO's own project."},
			"harness":   {typ: "string", desc: "Harness for the worker. Empty uses the default, opencode.", enum: []string{"opencode", "claude", "pi", "codex"}},
			"model":     {typ: "string", desc: "Model as the harness names it, or a loose name such as deepseek 4.1. Empty uses the harness default."},
			"task":      {typ: "string", desc: "The worker's task: a brief for an agent that knows nothing about this conversation, with the goal, files, limits and what to report."},
			"machine":   {typ: "string", desc: "Machine that holds the project, when more than one does."},
			"isolation": {typ: "string", desc: "worktree gives the worker its own git worktree (the default in a git repository). none works in the project folder, for read-only tasks.", enum: []string{"worktree", "none"}},
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
			prop := map[string]any{"type": f.typ, "description": f.desc}
			if len(f.enum) > 0 {
				prop["enum"] = f.enum
			}
			props[k] = prop
		}
		tool := &mcp.Tool{Name: name, Description: spec.desc, InputSchema: map[string]any{"type": "object", "properties": props, "additionalProperties": false}}
		method := name
		s.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			raw, err := call(ctx, method, req.Params.Arguments)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil
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
