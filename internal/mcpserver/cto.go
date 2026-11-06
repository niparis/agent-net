package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolcatalog"
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

var ctoEdgeKinds = []string{"depends", "governs", "deploys", "accepts", "reviews", "tests", "tests_after_merge", "approves", "approves_deploy", "merges", "releases", "delivery_deploys", "verifies"}

func settingsChangesField() ctoField {
	return ctoField{typ: "array", desc: "Setting changes with key, level, scope, value or delete, and expect.", items: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"key", "level", "scope"}, "properties": map[string]any{"key": map[string]any{"type": "string"}, "level": map[string]any{"type": "string"}, "scope": map[string]any{"type": "string"}, "value": map[string]any{}, "delete": map[string]any{"type": "boolean"}, "expect": map[string]any{}}}}
}

var ctoNodeSettings = []string{"tests_before_merge", "merge_authority", "worker_sandbox", "judge_consumers", "deploy_rule", "autonomy"}

// ctoRoutingField is the field that selects the concrete operation of a merged
// tool. The daemon keeps the authoritative action table.
var ctoRoutingField = map[string]string{
	"message":         "action",
	"graph_change":    "action",
	"session_change":  "action",
	"worker_change":   "action",
	"answer":          "kind",
	"tools_change":    "action",
	"mcp_change":      "action",
	"settings_change": "area",
	"project_change":  "action",
}

// ctoTools holds the argument schemas of the merged CTO change tools and of
// the stored as-owner message call. The listed CTO tools come from the catalog;
// these schemas validate stored proposals.
var ctoTools = map[string]ctoToolDef{
	// message is not listed. It is the stored call of an as-owner send or reply
	// that agent_message_send or agent_message_reply proposes; this schema
	// validates that call.
	"message": {desc: "Send or reply; as_owner proposes.", fields: map[string]ctoField{"action": {typ: "string", enum: []string{"send", "reply"}}, "to": {typ: "string"}, "message_id": {typ: "string"}, "body": {typ: "string"}, "as_owner": {typ: "boolean"}}},

	"graph_change": {
		desc: "Change the graph: charters, edges and typed repo or agent lifecycle. Repo and agent create, archive, tombstone and their undo always require owner approval.",
		fields: map[string]ctoField{
			"action":  {typ: "string", enum: []string{"charter_set", "node_add", "node_remove", "edge_set", "edge_remove", "settings_set", "undo", "repo_create", "repo_archive", "agent_create", "agent_tombstone"}},
			"ref":     {typ: "graph_ref", desc: "Typed target."},
			"node":    {typ: "string", desc: "Node."},
			"attr":    {typ: "string", desc: "Attribute.", enum: ctoCharterAttrs},
			"value":   {typ: "string", desc: "Value; empty clears."},
			"name":    {typ: "string", desc: "Display name."},
			"role":    {typ: "string", desc: "Agent role; optional."},
			"from":    {typ: "graph_endpoint", desc: "Source."},
			"to":      {typ: "graph_endpoint", desc: "Target."},
			"kind":    {typ: "string", desc: "Dependency, governance or delivery owner edge.", enum: ctoEdgeKinds},
			"mode":    {typ: "string", desc: "Advise, arbitrate, decide.", enum: []string{"advisor", "arbiter", "owner"}},
			"rule":    {typ: "string", desc: "Deliver, own, hold, refuse.", enum: []string{"normal", "auto", "ask", "never"}},
			"id":      {typ: "integer", desc: "History ID; omit for latest."},
			"setting": {typ: "string", desc: "Node setting.", enum: ctoNodeSettings},
		},
	},
	"session_change": {
		desc: "Change a session: rename, trust, policy, session_start, merger_start or merger_stop.",
		fields: map[string]ctoField{
			"action":     {typ: "string", enum: []string{"rename", "trust", "policy", "session_start", "merger_start", "merger_stop"}},
			"session_id": {typ: "string", desc: "Session ID."},
			"agent_id":   {typ: "string", desc: "Lasting Agent; omit for Repo owner."},
			"project":    {typ: "string"}, "path": {typ: "string"}, "node_id": {typ: "string"},
			"harness": {typ: "string"}, "model": {typ: "string"}, "folder": {typ: "string"},
			"branch": {typ: "string"}, "first_message": {typ: "string"}, "create": {typ: "boolean"},
			"name":    {typ: "string", desc: "Name; empty resets."},
			"on":      {typ: "boolean", desc: "Enable trust."},
			"scope":   {typ: "string", desc: "Default session.", enum: []string{"session", "project"}},
			"policy":  {typ: "object", desc: "Policy."},
			"dry_run": {typ: "boolean", desc: "Preview."},
		},
	},
	"worker_change": {
		desc: "Change a worker: start or stop.",
		fields: map[string]ctoField{
			"action":        {typ: "string", enum: []string{"start", "stop"}},
			"project":       {typ: "string", desc: "Project."},
			"harness":       {typ: "string", desc: "Harness; empty is auto.", enum: []string{"opencode", "claude", "pi", "codex"}},
			"model":         {typ: "string", desc: "Model; empty is auto."},
			"difficulty":    {typ: "string", desc: "Deprecated and ignored: the level comes from the difficulty judge.", enum: []string{"trivial", "simple", "normal", "hard", "very_complex"}},
			"touches":       {typ: "array", desc: "Deprecated and ignored: the level comes from the difficulty judge.", items: map[string]any{"type": "string", "enum": []string{"db", "hot-path", "ui", "docs", "tests", "config", "other"}}},
			"base":          {typ: "string", desc: "Branch."},
			"continue":      {typ: "string", desc: "Worker ID."},
			"task":          {typ: "string", desc: "Brief and limits."},
			"machine":       {typ: "string", desc: "Machine."},
			"isolation":     {typ: "string", desc: "None reads only; default worktree.", enum: []string{"worktree", "none"}},
			"session":       {typ: "string", desc: "Worker or queue ID."},
			"auto_continue": {typ: "boolean", desc: "Default true: continue a worker that stops early."},
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
		desc: "Change tools: toggle, new_projects, plan, apply, undo, import or native_remove.",
		fields: map[string]ctoField{
			"action":       {typ: "string", enum: []string{"plan", "apply", "undo", "import", "native_remove", "toggle", "new_projects"}},
			"item":         {typ: "string", desc: "Item ID."},
			"on":           {typ: "boolean", desc: "On or off."},
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
	"mcp_call":     {desc: "Call a tool on a shared MCP server.", fields: map[string]ctoField{"server": {typ: "string", desc: "Server name."}, "tool": {typ: "string", desc: "Tool."}, "arguments": {typ: "object", desc: "Arguments."}}},
	"proposal_ask": {desc: "Open the approval dialog for a waiting proposal.", fields: map[string]ctoField{"id": {typ: "string", desc: "Open proposal ID."}}},
	"cto_new":      {desc: "Start a new conversation for the active harness."},
	"settings_change": {
		desc: "Change owner settings: worker profiles, providers incl. protected providers, scores, integrations, notifications, CTO, playbook, machine name, machine removal, token rotation, invite or onboarding.",
		fields: map[string]ctoField{
			"changes":             settingsChangesField(),
			"revision":            {typ: "integer", desc: "Revision to undo."},
			"area":                {typ: "string", enum: []string{"values_write", "values_undo", "worker_profile_add", "worker_profile_remove", "worker_providers", "protected_providers", "score", "integrations", "notifications", "cto", "playbook", "machine_name", "machine_remove", "machine_rotate", "invite", "onboarding"}},
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
		desc: "Change projects: add, remove, display or create.",
		fields: map[string]ctoField{
			"action":  {typ: "string", enum: []string{"add", "remove", "display", "create"}},
			"paths":   {typ: "array", desc: "Paths."},
			"node_id": {typ: "string", desc: "Machine for create or remove; omit for this machine."}, "harness": {typ: "string"}, "model": {typ: "string"},
			"folder": {typ: "string"}, "branch": {typ: "string"}, "first_message": {typ: "string"},
			"dir":     {typ: "string", desc: "Directory."},
			"scan":    {typ: "boolean", desc: "Scan the directory."},
			"depth":   {typ: "integer", desc: "3; max 8."},
			"dry_run": {typ: "boolean", desc: "Preview."},
			"name":    {typ: "string", desc: "Project name; empty clears."},
			"path":    {typ: "string", desc: "Path."},
			"remote":  {typ: "string", desc: "Optional clone remote."},
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
		if f.typ == "graph_ref" || f.typ == "graph_endpoint" {
			ref := map[string]any{"type": "object", "properties": map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"repo", "agent", "special"}}, "id": map[string]any{"type": "string", "minLength": 1}}, "required": []string{"kind", "id"}, "additionalProperties": false}
			if name == "agent_graph_read" {
				ref["properties"].(map[string]any)["kind"].(map[string]any)["enum"] = []string{"repo", "agent", "team", "special"}
			}
			prop = ref
			if f.typ == "graph_endpoint" {
				prop = map[string]any{"anyOf": []any{map[string]any{"type": "string"}, ref}}
			}
		}
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

// NewCTO is deliberately separate from the network MCP registry. It lists the
// catalog tools of the CTO role.
func NewCTO(call Caller, version string, note ...func(context.Context) string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "agent-net-cto", Version: version}, nil)
	s.AddReceivingMiddleware(renamedTools())
	if len(note) > 0 && note[0] != nil {
		s.AddReceivingMiddleware(staleToolsNote(note[0]))
	}
	originalCall := call
	call = func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		raw, err := originalCall(ctx, method, params)
		if err != nil {
			return raw, err
		}
		var proposal ctoDialogProposal
		if json.Unmarshal(raw, &proposal) == nil && proposal.Kind == "result" && proposal.Value != nil {
			return proposal.Value, nil
		}
		if proposal.Kind == "proposal" && proposal.ID != "" && proposal.Message != "" {
			if slot, ok := ctx.Value(ctoDialogKey{}).(*ctoDialogProposal); ok {
				*slot = proposal
				return json.Marshal(proposal.Text)
			}
		}
		return raw, nil
	}
	addTool := ctoDialogTools(s, originalCall)
	addCTOV2Tools(s, call, addTool)
	addCTOSharedTools(s, call, addTool)
	filterCatalogTools(s, toolcatalog.RoleCTO)
	return s
}

type ctoDialogKey struct{}
type ctoDialogProposal struct {
	Kind    string          `json:"kind"`
	Value   json.RawMessage `json:"value"`
	ID      string          `json:"proposal_id"`
	Message string          `json:"message"`
	Text    string          `json:"text"`
}

type pendingCTODialog struct {
	proposal ctoDialogProposal
	session  *mcp.ServerSession
	tool     string
}

type ctoToolAdder func(*mcp.Tool, mcp.ToolHandler)

func ctoDialogTools(s *mcp.Server, call Caller) ctoToolAdder {
	var mu sync.Mutex
	pending := make(map[string]pendingCTODialog)
	finish := func(ctx context.Context, proposal ctoDialogProposal, action string) *mcp.CallToolResult {
		text := proposal.Text
		if text == "" {
			text = "Waiting in the Inbox."
		}
		if action == "accept" || action == "decline" {
			approve := action == "accept"
			outcome, err := call(ctx, "cto_dialog_answer", map[string]any{"id": proposal.ID, "approve": approve})
			if err == nil {
				if approve {
					text = "Approved by the owner in the dialog."
					var note string
					if json.Unmarshal(outcome, &note) == nil && note != "" {
						text += " " + note
					}
				} else {
					text = "Refused by the owner in the dialog."
				}
			}
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
	}
	return func(tool *mcp.Tool, next mcp.ToolHandler) {
		s.AddTool(tool, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if request.Params != nil && (request.Params.RequestState != "" || request.Params.InputResponses != nil) {
				mu.Lock()
				item, ok := pending[request.Params.RequestState]
				if ok {
					delete(pending, request.Params.RequestState)
				}
				mu.Unlock()
				if !ok || item.session != request.Session || item.tool != request.Params.Name {
					return finish(ctx, ctoDialogProposal{}, ""), nil
				}
				action := ""
				if response, ok := request.Params.InputResponses["approval"].(*mcp.ElicitResult); ok && response != nil {
					action = response.Action
				}
				return finish(ctx, item.proposal, action), nil
			}
			var proposal ctoDialogProposal
			res, err := next(context.WithValue(ctx, ctoDialogKey{}, &proposal), request)
			if err != nil || proposal.ID == "" {
				return res, err
			}
			if res == nil || res.IsError {
				return res, err
			}
			init := request.Session.InitializeParams()
			if init == nil || init.Capabilities == nil || init.Capabilities.Elicitation == nil {
				return finish(ctx, proposal, ""), nil
			}
			params := &mcp.ElicitParams{Message: proposal.Message, RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}}}
			if init.ProtocolVersion >= "2026-07-28" {
				var bytes [32]byte
				if _, err := rand.Read(bytes[:]); err != nil {
					return finish(ctx, proposal, ""), nil
				}
				token := hex.EncodeToString(bytes[:])
				mu.Lock()
				pending[token] = pendingCTODialog{proposal: proposal, session: request.Session, tool: request.Params.Name}
				mu.Unlock()
				return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{"approval": params}, RequestState: token}, nil
			}
			answer, err := request.Session.Elicit(ctx, params)
			if err != nil || answer == nil {
				return finish(ctx, proposal, ""), nil
			}
			return finish(ctx, proposal, answer.Action), nil
		})
	}
}

// CTOToolNames returns the listed CTO tool names, sorted: the catalog tools of
// the CTO role.
func CTOToolNames() []string {
	tools := toolcatalog.ForRole(toolcatalog.RoleCTO)
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func RunCTO(ctx context.Context, socket, version string) error {
	logf := func(format string, a ...any) { fmt.Fprintf(os.Stderr, "agent-net cto: "+format+"\n", a...) }
	conn := newStdioConn(0, os.Stdout, takeHandover(logf), logf)
	go conn.readLoop()
	exe, _ := os.Executable()
	opts := Options{Version: version, Exe: exe, NoAutoUpgrade: os.Getenv("AGENT_NET_NO_AUTO_UPGRADE") == "1"}
	sw := &switcher{opts: opts, conn: conn, logf: logf}
	var note staleNote
	sw.dropNote = note.dropNote
	ch, err := dialCTOChannel(ctx, socket, func(v string) {
		note.observeVersion(version, v)
		go sw.consider(ctx, v)
	})
	if err != nil {
		return err
	}
	defer ch.close()
	s := NewCTO(func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		if method == "cto_dialog_answer" {
			return ch.call(ctx, method, params)
		}
		return ch.call(ctx, "cto_tool", map[string]any{"name": method, "arguments": params})
	}, version, func(context.Context) string { return note.takeNote() })
	return s.Run(ctx, stdioTransport{conn})
}

// ctoReconnectWait bounds how long the CTO channel waits for the daemon, at
// start and after its connection drops. The daemon restarts on every
// automatic upgrade, and the CTO's Claude job, with this process, outlives it.
const ctoReconnectWait = 30 * time.Second

// ctoChannel is the CTO's connection to the daemon. It dials again and says
// hello again when the connection drops, so a daemon restart does not leave
// the CTO without its tools.
type ctoChannel struct {
	socket  string
	onHello func(string)
	mu      sync.Mutex
	c       *client.Client
}

func dialCTOChannel(ctx context.Context, socket string, onHello ...func(string)) (*ctoChannel, error) {
	ch := &ctoChannel{socket: socket}
	if len(onHello) > 0 {
		ch.onHello = onHello[0]
	}
	if err := ch.connect(ctx); err != nil {
		return nil, err
	}
	return ch, nil
}

// connect dials and says hello, retrying while the daemon is unreachable. A
// refusal from the daemon (not the CTO's process tree) is final.
func (ch *ctoChannel) connect(ctx context.Context) error {
	deadline := time.Now().Add(ctoReconnectWait)
	for {
		c, err := client.Dial(ch.socket)
		if err == nil {
			var raw json.RawMessage
			raw, err = c.Call(ctx, "hello", map[string]string{"client": "cto-mcp"})
			if err == nil {
				var hello struct {
					Version string `json:"version"`
				}
				_ = json.Unmarshal(raw, &hello)
				ch.mu.Lock()
				ch.c = c
				ch.mu.Unlock()
				if ch.onHello != nil && hello.Version != "" {
					ch.onHello(hello.Version)
				}
				return nil
			}
			c.Close()
			if !client.IsConnError(err) {
				return fmt.Errorf("CTO channel: %w", err)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("CTO channel: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// call sends one request, and on a dropped connection reconnects and sends it
// once more.
func (ch *ctoChannel) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ch.mu.Lock()
	c := ch.c
	ch.mu.Unlock()
	if c != nil {
		res, err := c.Call(ctx, method, params)
		if err == nil || !client.IsConnError(err) {
			return res, err
		}
		c.Close()
	}
	if err := ch.connect(ctx); err != nil {
		return nil, err
	}
	ch.mu.Lock()
	c = ch.c
	ch.mu.Unlock()
	return c.Call(ctx, method, params)
}

func (ch *ctoChannel) close() {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.c != nil {
		ch.c.Close()
		ch.c = nil
	}
}
