package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

// This file has the fixture and the helpers of the read-tool tests (tool
// redesign task 2b). The fake daemon answers the read methods from memory and
// never touches a real setup.

type roFakeDaemon struct {
	caller        string
	callerNative  string
	agents        []protocol.AgentView
	messages      []protocol.Message
	message       protocol.Message
	graph         protocol.PublicGraphPage
	graphRoots    []string
	servers       []protocol.MCPServer
	profiles      []protocol.WorkerProfile
	methods       []string
	mcpListDetail bool
	// files serves message_file: attachment name to text.
	files map[string]string
}

func (f *roFakeDaemon) call(_ context.Context, method string, params any) (json.RawMessage, error) {
	raw, _ := json.Marshal(params)
	f.methods = append(f.methods, method)
	switch method {
	case "info":
		return json.Marshal(protocol.OperatorInfo{Sessions: []protocol.SessionInfo{{
			SessionID: f.caller, Harness: "claude", NativeID: f.callerNative, Name: "me",
			Cwd: "/tmp/work", Repo: protocol.RepoInfo{Project: "proj"},
		}}})
	case "list":
		return json.Marshal(protocol.AgentsResult{Agents: f.agents})
	case "messages":
		var q protocol.QueryMessages
		json.Unmarshal(raw, &q)
		total := len(f.messages)
		offset := q.Offset
		if offset < 0 {
			offset = 0
		}
		if offset > total {
			offset = total
		}
		limit := q.Limit
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		end := offset + limit
		if end > total {
			end = total
		}
		return json.Marshal(map[string]any{"messages": f.messages[offset:end], "total": total})
	case "message_status":
		return json.Marshal(protocol.MessageResult{Message: f.message})
	case "message_file":
		var q protocol.MessageFileRequest
		json.Unmarshal(raw, &q)
		text, ok := f.files[q.File]
		if !ok {
			return nil, fmt.Errorf("the message has no file named %q", q.File)
		}
		end := min(int(q.Offset)+q.MaxBytes, len(text))
		page := protocol.MessageFileResult{Name: q.File, Size: int64(len(text)), Type: "text", Text: text[q.Offset:end]}
		if end < len(text) {
			page.NextOffset = int64(end)
		}
		return json.Marshal(page)
	case "agent_graph":
		var q protocol.PublicGraphQuery
		json.Unmarshal(raw, &q)
		f.graphRoots = append(f.graphRoots, q.Root)
		page := f.graph
		if q.Node != "" {
			page.Nodes = nil
			for _, node := range f.graph.Nodes {
				if node.Name == q.Node {
					page.Nodes = append(page.Nodes, node)
				}
			}
			page.Edges = nil
			for _, edge := range f.graph.Edges {
				if edge.From == q.Node || edge.To == q.Node {
					page.Edges = append(page.Edges, edge)
				}
			}
			page.Total = len(page.Nodes)
			page.EdgeTotal = len(page.Edges)
		}
		if q.Node == "" {
			offset, limit := q.Offset, q.Limit
			if limit <= 0 {
				limit = protocol.PublicGraphDefaultLimit
			}
			if limit > protocol.PublicGraphMaxLimit {
				limit = protocol.PublicGraphMaxLimit
			}
			if offset < 0 {
				offset = 0
			}
			if offset > len(page.Nodes) {
				offset = len(page.Nodes)
			}
			end := offset + limit
			if end > len(page.Nodes) {
				end = len(page.Nodes)
			}
			page.Nodes = page.Nodes[offset:end]
			page.Total = len(f.graph.Nodes)
			page.Next = 0
			if end < len(f.graph.Nodes) {
				page.Next = end
			}
		}
		return json.Marshal(page)
	case "mcp_list":
		return json.Marshal(map[string]any{"servers": f.servers})
	case "worker_profiles":
		return json.Marshal(f.profiles)
	}
	return nil, fmt.Errorf("unexpected daemon method %q", method)
}

func roMessageAt(id string, at time.Time, from, to, body string) protocol.Message {
	return protocol.Message{
		ID: id, ConversationID: "c_" + from + "_" + to, From: from, To: to,
		FromName: from, Type: "request", State: "stored", Body: body, CreatedAt: at,
	}
}

func roAgent(id, name, project string, online bool, worker *protocol.WorkerInfo) protocol.AgentView {
	return protocol.AgentView{
		SessionID: id, NodeID: "n_" + project, Hostname: "host-" + project,
		Harness: "claude", Model: "standard", Cwd: "/tmp/" + project,
		Status: "idle", Online: online, Profile: protocol.Profile{Name: name},
		Worker: worker,
	}
}

func roSchema(t *testing.T, raw json.RawMessage) *jsonschema.Resolved {
	t.Helper()
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{})
	if err != nil {
		t.Fatalf("resolve schema: %v", err)
	}
	return resolved
}

func roCatalogTool(t *testing.T, name string) toolcatalog.Tool {
	t.Helper()
	for _, candidate := range toolcatalog.Tools {
		if candidate.Name == name {
			return candidate
		}
	}
	t.Fatalf("%s is not in the catalog", name)
	return toolcatalog.Tool{}
}

func roToolCall(name string, args map[string]any) *mcp.CallToolParams {
	return &mcp.CallToolParams{Name: name, Arguments: args}
}

// roCall calls one read tool and checks the two rules every result follows: the
// structured value validates against the catalog output schema, and one text
// block holds the same JSON.
func roCall(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s: %+v", name, res.Content)
	}
	spec := roCatalogTool(t, name)
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("%s: structured content: %v", name, err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("%s: structured content: %v", name, err)
	}
	if err := roSchema(t, spec.OutputSchema).Validate(value); err != nil {
		t.Fatalf("%s: structuredContent does not match the catalog output schema: %v\n%s", name, err, raw)
	}
	var text string
	for _, content := range res.Content {
		if item, ok := content.(*mcp.TextContent); ok {
			text = item.Text
			break
		}
	}
	var textValue any
	if err := json.Unmarshal([]byte(text), &textValue); err != nil {
		t.Fatalf("%s: the text block is not the same JSON: %v\n%s", name, err, text)
	}
	textRaw, _ := json.Marshal(textValue)
	var structuredValue any
	json.Unmarshal(raw, &structuredValue)
	structuredRaw, _ := json.Marshal(structuredValue)
	if string(textRaw) != string(structuredRaw) {
		t.Fatalf("%s: text and structuredContent differ\ntext: %s\nstructured: %s", name, textRaw, structuredRaw)
	}
	return value
}

func roPageItems(t *testing.T, page map[string]any) []map[string]any {
	t.Helper()
	items, ok := page["items"].([]any)
	if !ok {
		t.Fatalf("page has no items: %+v", page)
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, item.(map[string]any))
	}
	return out
}

func roPageIDs(t *testing.T, page map[string]any) []string {
	t.Helper()
	var ids []string
	for _, item := range roPageItems(t, page) {
		ids = append(ids, item["id"].(string))
	}
	return ids
}

func roReadFixture() *roFakeDaemon {
	start := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	f := &roFakeDaemon{caller: "s_me", callerNative: "n_me"}
	f.agents = []protocol.AgentView{
		roAgent("s_me", "me", "proj", true, nil),
		roAgent("s_peer", "peer", "proj", true, nil),
		roAgent("s_w_mine", "mine", "proj", true, &protocol.WorkerInfo{ID: "w1", Owner: "s_me", Project: "proj", StartedAt: start.Unix(), Task: "my task", DifficultyUsed: "normal"}),
		roAgent("s_w_other", "other", "proj", true, &protocol.WorkerInfo{ID: "w2", Owner: "s_peer", Project: "proj", StartedAt: start.Unix(), Task: "their task", DifficultyUsed: "hard"}),
		roAgent("s_w_ended", "ended", "proj", false, &protocol.WorkerInfo{ID: "w3", Owner: "s_me", Project: "proj", StartedAt: start.Unix(), Task: "done", DifficultyUsed: "simple"}),
	}
	for i := 0; i < 60; i++ {
		at := start.Add(time.Duration(i) * time.Minute)
		from, to := "s_me", "s_peer"
		if i%2 == 1 {
			from, to = "s_peer", "s_me"
		}
		f.messages = append(f.messages, roMessageAt(fmt.Sprintf("m_%02d", i), at, from, to, fmt.Sprintf("message %02d\nbody", i)))
	}
	f.message = f.messages[59]
	f.graph = protocol.PublicGraphPage{
		Nodes: []protocol.PublicGraphNode{
			{Name: "alpha", Kind: "component", Maturity: "mature", Rules: []protocol.PublicRule{{Name: "merge_authority", Value: "owner"}}, Charter: &protocol.Charter{Purpose: "Own the alpha"}},
			{Name: "beta", Kind: "component", Maturity: "growing", Rules: []protocol.PublicRule{}, Charter: &protocol.Charter{Purpose: "Own the beta"}},
		},
		Edges:     []protocol.PublicGraphEdge{{From: "alpha", To: "beta", Kind: "depends", Mode: "advisor", Rule: "normal"}},
		Total:     2,
		EdgeTotal: 1,
	}
	f.servers = []protocol.MCPServer{
		{Name: "files", Hostname: "nas", Enabled: true, Health: "ok", HostOnline: true, Scope: "global", Tools: []protocol.MCPTool{{Name: "read"}, {Name: "write"}}},
		{Name: "off", Hostname: "nas", Enabled: false, Health: "off", HostOnline: false, Tools: []protocol.MCPTool{{Name: "x"}}},
	}
	available := true
	f.profiles = []protocol.WorkerProfile{
		{Name: "alpha", Harness: "opencode", Model: "a/x", Effort: "high", Available: &available,
			MachineAvailability: []protocol.WorkerProfileMachineAvailability{{Machine: "build", Available: true}}},
		{Name: "beta", Harness: "claude", Model: "b/y", Available: &available,
			MachineAvailability: []protocol.WorkerProfileMachineAvailability{{Machine: "desk", Available: true}}},
	}
	return f
}
