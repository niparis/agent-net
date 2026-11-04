package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

// The CTO lists only catalog tools: no tool from before the redesign.
func TestCTOListHasOnlyCatalogTools(t *testing.T) {
	list, err := ctoTestSession(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if toolcatalog.RenamedError(tool.Name) != nil {
			t.Errorf("CTO still lists the old name %s", tool.Name)
		}
		if !toolcatalog.Allows(toolcatalog.RoleCTO, tool.Name) {
			t.Errorf("CTO lists %s, which is not a CTO catalog tool", tool.Name)
		}
	}
	if len(list.Tools) != len(toolcatalog.ForRole(toolcatalog.RoleCTO)) {
		t.Errorf("CTO lists %d tools, the catalog has %d CTO tools", len(list.Tools), len(toolcatalog.ForRole(toolcatalog.RoleCTO)))
	}
}

// A call with an old name gets one line that names the new tool, and never
// reaches the daemon.
func TestOldToolNameCallSaysRenamed(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	caller := func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		mu.Lock()
		methods = append(methods, method)
		mu.Unlock()
		return json.RawMessage(`{"ok":true}`), nil
	}
	cto := func(t *testing.T) *mcp.ClientSession {
		st, ct := mcp.NewInMemoryTransports()
		ss, err := NewCTO(caller, "test").Connect(context.Background(), st, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ss.Close() })
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(context.Background(), ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cs.Close() })
		return cs
	}
	agent := func(t *testing.T) *mcp.ClientSession {
		t.Setenv("AGENT_NET_WORKER", "")
		return connect(t, caller)
	}
	for _, tc := range []struct {
		server  string
		session func(*testing.T) *mcp.ClientSession
		old     string
		want    string
	}{
		{"cto", cto, "ui", "ui was renamed to cto_ui_show."},
		{"cto", cto, "message", "message was renamed to agent_message_send or agent_message_reply."},
		{"cto", cto, "sessions", "sessions was renamed to agent_sessions_find."},
		{"cto", cto, "graph", "graph was renamed to agent_graph_read."},
		{"cto", cto, "learn", "learn was renamed to agent_learn."},
		{"agent", agent, "agent_list", "agent_list was renamed to agent_sessions_find."},
		{"agent", agent, "agent_send", "agent_send was renamed to agent_message_send."},
		{"agent", agent, "agent_ask_owner", "agent_ask_owner was renamed to agent_owner_ask."},
	} {
		cs := tc.session(t)
		mu.Lock()
		methods = nil
		mu.Unlock()
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.old, Arguments: map[string]any{}})
		if err != nil {
			t.Fatalf("%s %s: protocol error %v, want a tool error", tc.server, tc.old, err)
		}
		if !res.IsError || len(res.Content) == 0 {
			t.Fatalf("%s %s: %+v, want a tool error", tc.server, tc.old, res)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if text != tc.want {
			t.Errorf("%s %s: %q, want %q", tc.server, tc.old, text, tc.want)
		}
		mu.Lock()
		if len(methods) != 0 {
			t.Errorf("%s %s reached the daemon: %v", tc.server, tc.old, methods)
		}
		mu.Unlock()
	}
}

// The CTO's charter guide is a view of agent_graph_read now; ordinary agents
// keep the plain schema.
func TestCTOGraphReadHasGuideView(t *testing.T) {
	var got map[string]any
	st, ct := mcp.NewInMemoryTransports()
	srv := NewCTO(func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		_ = json.Unmarshal(raw, &got)
		if method != "agent_graph_read" {
			t.Errorf("method %s", method)
		}
		return json.RawMessage(`{"nodes":[],"edges":[],"note":"guide text"}`), nil
	}, "test")
	ss, err := srv.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_graph_read", Arguments: map[string]any{"view": "guide"}})
	if err != nil || res.IsError {
		t.Fatalf("guide: %+v %v", res, err)
	}
	if got["view"] != "guide" {
		t.Fatalf("daemon got %v", got)
	}
	if !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "guide text") {
		t.Fatalf("result %+v", res.Content[0])
	}
	list, err := connectAgent(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "agent_graph_read" {
			raw, _ := json.Marshal(tool.InputSchema)
			if strings.Contains(string(raw), `"view"`) {
				t.Fatalf("agent schema has the CTO view: %s", raw)
			}
		}
	}
}

// cto_ui_show and cto_proposals_find reach the daemon under their own names.
func TestCTOToolsCallDaemonByCatalogName(t *testing.T) {
	type call struct {
		method string
		params map[string]any
	}
	var calls []call
	st, ct := mcp.NewInMemoryTransports()
	srv := NewCTO(func(_ context.Context, method string, params any) (json.RawMessage, error) {
		var p map[string]any
		raw, _ := json.Marshal(params)
		_ = json.Unmarshal(raw, &p)
		calls = append(calls, call{method, p})
		if method == "cto_proposals_find" {
			return json.RawMessage(`{"items":[],"total":0}`), nil
		}
		return json.RawMessage(`"shown"`), nil
	}, "test")
	ss, err := srv.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	for _, args := range []map[string]any{{"tab": "graph", "select": "shop"}, {"notice": "hello"}} {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "cto_ui_show", Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("cto_ui_show %v: %+v %v", args, res, err)
		}
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "cto_proposals_find", Arguments: map[string]any{"cursor": "25"}})
	if err != nil || res.IsError {
		t.Fatalf("cto_proposals_find: %+v %v", res, err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls %+v", calls)
	}
	if calls[0].method != "cto_ui_show" || calls[0].params["tab"] != "graph" || calls[0].params["select"] != "shop" {
		t.Errorf("show call %+v", calls[0])
	}
	if calls[1].method != "cto_ui_show" || calls[1].params["notice"] != "hello" {
		t.Errorf("notice call %+v", calls[1])
	}
	if calls[2].method != "cto_proposals_find" || calls[2].params["offset"] != float64(25) {
		t.Errorf("proposals call %+v", calls[2])
	}
}

// With the old message tool gone, agent_message_send is the CTO's only send. A
// default inside to_set must not turn a send to one session into a group send.
func TestCTOSendToOneSessionHasNoGroup(t *testing.T) {
	var got map[string]any
	st, ct := mcp.NewInMemoryTransports()
	srv := NewCTO(func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		_ = json.Unmarshal(raw, &got)
		return json.RawMessage(`{"sent":[{"message_id":"m1","to":{"id":"s1","name":"s1"},"state":"delivered"}]}`), nil
	}, "test")
	ss, err := srv.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_send", Arguments: map[string]any{"to": "s1", "body": "hello"}})
	if err != nil || res.IsError {
		t.Fatalf("send: %+v %v", res, err)
	}
	if _, ok := got["to_set"]; ok {
		t.Fatalf("send to one session carries a group: %v", got)
	}
	if got["to"] != "s1" || got["type"] != "request" {
		t.Fatalf("send args: %v", got)
	}
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_send", Arguments: map[string]any{"to_set": map[string]any{"harness": "claude"}, "body": "hello"}})
	if err != nil || res.IsError {
		t.Fatalf("group send: %+v %v", res, err)
	}
	set, _ := got["to_set"].(map[string]any)
	if set["harness"] != "claude" || set["online_only"] != true {
		t.Fatalf("group send lost its defaults: %v", got)
	}
}
