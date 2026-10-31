package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// fakeRegistry answers the daemon's mcp_* methods from memory.
type fakeRegistry struct {
	mu      sync.Mutex
	servers []protocol.MCPServer
	calls   []protocol.MCPCall
	added   []protocol.MCPAdd
	failAdd string
}

func (f *fakeRegistry) set(s ...protocol.MCPServer) {
	f.mu.Lock()
	f.servers = s
	f.mu.Unlock()
}

func (f *fakeRegistry) call(_ context.Context, method string, params any) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := json.Marshal(params)
	switch method {
	case "mcp_tools", "mcp_list":
		return json.Marshal(map[string]any{"servers": f.servers})
	case "mcp_call":
		var c protocol.MCPCall
		json.Unmarshal(raw, &c)
		f.calls = append(f.calls, c)
		if c.Tool == "boom" {
			return nil, errors.New("homelab on nas: tool timed out")
		}
		return json.RawMessage(`{"content":[{"type":"text","text":"result of ` + c.Tool + `"}]}`), nil
	case "mcp_add":
		var a protocol.MCPAdd
		json.Unmarshal(raw, &a)
		if f.failAdd != "" {
			return nil, errors.New(f.failAdd)
		}
		f.added = append(f.added, a)
		s := a.Server
		s.Hostname, s.Enabled, s.Health = "nas", true, "ok"
		s.Tools = []protocol.MCPTool{{Name: "echo"}}
		return json.Marshal(map[string]any{"server": s})
	case "mcp_remove":
		return json.RawMessage(`{}`), nil
	}
	return nil, errors.New("unexpected " + method)
}

func tool(name string) protocol.MCPTool {
	return protocol.MCPTool{Name: name, Description: "does " + name,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`)}
}

func listNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tl := range res.Tools {
		if strings.Contains(tl.Name, "__") {
			out = append(out, tl.Name)
		}
	}
	return out
}

// Registered tools appear as <server>__<tool> with a prefixed description,
// the client is told with tools/list_changed, calls pass through, and the
// tools go away when the server does.
func TestDynamicToolsFollowRegistry(t *testing.T) {
	reg := &fakeRegistry{}
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	srv := New(reg.call, "")
	dyn := NewDynamic(srv, reg.call)
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	changed := make(chan struct{}, 8)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) { changed <- struct{}{} },
	}).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	waitChanged := func() {
		t.Helper()
		select {
		case <-changed:
		case <-time.After(5 * time.Second):
			t.Fatal("no notifications/tools/list_changed")
		}
	}

	// The server notifies only sessions it has seen initialized. Connect does
	// not wait for that, so one round trip makes sure of it.
	listNames(t, cs)
	reg.set(protocol.MCPServer{Name: "homelab", Hostname: "nas", Enabled: true, Tools: []protocol.MCPTool{tool("echo"), tool("boom")}})
	if err := dyn.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	waitChanged()
	if got := strings.Join(listNames(t, cs), ","); got != "homelab__boom,homelab__echo" {
		t.Fatalf("tools = %s", got)
	}
	res, _ := cs.ListTools(ctx, nil)
	for _, tl := range res.Tools {
		if tl.Name == "homelab__echo" && tl.Description != "[homelab on nas] does echo" {
			t.Fatalf("description = %q", tl.Description)
		}
	}

	out, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "homelab__echo", Arguments: map[string]any{"text": "x"}})
	if err != nil || out.IsError {
		t.Fatalf("call: %v %+v", err, out)
	}
	if txt := out.Content[0].(*mcp.TextContent).Text; txt != "result of echo" {
		t.Fatalf("text = %q", txt)
	}
	if len(reg.calls) != 1 || reg.calls[0].Server != "homelab" || reg.calls[0].Tool != "echo" || !strings.Contains(string(reg.calls[0].Arguments), `"x"`) {
		t.Fatalf("daemon call = %+v", reg.calls)
	}
	// An error from the host reaches the model as a tool error with its text.
	out, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "homelab__boom", Arguments: map[string]any{}})
	if err != nil || !out.IsError || !strings.Contains(out.Content[0].(*mcp.TextContent).Text, "timed out") {
		t.Fatalf("boom: %v %+v", err, out)
	}

	// A tool disappears from the server: list_changed again, one tool left.
	reg.set(protocol.MCPServer{Name: "homelab", Hostname: "nas", Enabled: true, Tools: []protocol.MCPTool{tool("echo")}})
	dyn.Refresh(ctx)
	waitChanged()
	if got := strings.Join(listNames(t, cs), ","); got != "homelab__echo" {
		t.Fatalf("tools = %s", got)
	}
	// The registry is unreachable: nothing changes.
	failing := NewDynamic(srv, func(context.Context, string, any) (json.RawMessage, error) { return nil, ErrUnreachable })
	failing.have = dyn.have
	if err := failing.Refresh(ctx); err == nil {
		t.Fatal("want an error")
	}
	if got := strings.Join(listNames(t, cs), ","); got != "homelab__echo" {
		t.Fatalf("tools changed on error: %s", got)
	}
	// The server is removed: all its tools go.
	reg.set()
	dyn.Refresh(ctx)
	waitChanged()
	if got := listNames(t, cs); len(got) != 0 {
		t.Fatalf("tools = %v", got)
	}
}

func TestAgentMCPAddTool(t *testing.T) {
	reg := &fakeRegistry{}
	cs := connect(t, reg.call)
	ctx := context.Background()

	// There is no way to choose a host.
	tools, _ := cs.ListTools(ctx, nil)
	for _, tl := range tools.Tools {
		if tl.Name == "agent_mcp_add" {
			b, _ := json.Marshal(tl.InputSchema)
			if strings.Contains(string(b), `"on"`) || strings.Contains(string(b), `"host"`) {
				t.Fatalf("agent_mcp_add exposes a host: %s", b)
			}
			if !strings.Contains(tl.Description, "Ask your user") || !strings.Contains(tl.Description, "Never put a secret") {
				t.Fatalf("description lacks the rules: %q", tl.Description)
			}
		}
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_mcp_add", Arguments: map[string]any{
		"name": "homelab", "url": "https://nas.lan/mcp",
		"headers": []map[string]any{{"name": "Authorization", "env": "HOMELAB_TOKEN"}}}})
	if err != nil || res.IsError {
		t.Fatalf("add: %v %+v", err, res)
	}
	if len(reg.added) != 1 || reg.added[0].On != "" || reg.added[0].Server.Transport != "http" ||
		reg.added[0].Server.Headers[0].Env != "HOMELAB_TOKEN" {
		t.Fatalf("daemon got %+v", reg.added)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), "every agent on every machine") || !strings.Contains(string(b), "homelab__echo") {
		t.Fatalf("result lacks the global note or tool names: %s", b)
	}

	// A secret value in place of a name is refused before anything is sent.
	reg.added = nil
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_mcp_add", Arguments: map[string]any{
		"name": "bad", "command": "x", "env": []string{"TOKEN=hunter2hunter2"}}})
	if err != nil || !res.IsError || len(reg.added) != 0 {
		t.Fatalf("secret env accepted: %v %+v", err, res)
	}
	if txt := res.Content[0].(*mcp.TextContent).Text; strings.Contains(txt, "hunter2") {
		t.Fatalf("error echoes the secret: %s", txt)
	}

	res, _ = cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_mcp_remove", Arguments: map[string]any{"name": "homelab"}})
	b, _ = json.Marshal(res.StructuredContent)
	if res.IsError || !strings.Contains(string(b), "every agent on every machine") {
		t.Fatalf("remove: %s", b)
	}
}
