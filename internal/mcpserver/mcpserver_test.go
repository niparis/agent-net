package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type call struct {
	method string
	params map[string]any
}

func connect(t *testing.T, fn Caller) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	srv := New(fn)
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestToolsForwardToDaemon(t *testing.T) {
	var calls []call
	cs := connect(t, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var m map[string]any
		json.Unmarshal(raw, &m)
		calls = append(calls, call{method, m})
		if method == "send" && m["body"] == "boom" {
			return nil, errors.New("gateway says no")
		}
		return json.RawMessage(`{"ok":true}`), nil
	})
	ctx := context.Background()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	sort.Strings(names)
	want := "agent_announce agent_get agent_handback agent_list agent_mcp_add agent_mcp_list agent_mcp_remove agent_mcp_test agent_message_status agent_messages agent_nodes agent_project_add agent_project_list agent_reply agent_send agent_start agent_stop"
	if strings.Join(names, " ") != want {
		t.Fatalf("tools: %v", names)
	}
	for _, tl := range tools.Tools {
		if tl.Name == "agent_send" && !strings.Contains(tl.Description, "requests from peers") {
			t.Fatalf("agent_send must say peer messages are requests from peers: %q", tl.Description)
		}
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_list", Arguments: map[string]any{"text": "go"}})
	if err != nil || res.IsError {
		t.Fatalf("agent_list: %v %+v", err, res)
	}
	if calls[0].method != "list" || calls[0].params["online_only"] != true || calls[0].params["text"] != "go" {
		t.Fatalf("list call: %+v", calls[0])
	}
	cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_list", Arguments: map[string]any{"online_only": false}})
	if calls[1].params["online_only"] != false {
		t.Fatalf("online_only=false not passed: %+v", calls[1])
	}

	cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_send", Arguments: map[string]any{"to": "s_x", "body": "hi"}})
	if calls[2].method != "send" || calls[2].params["to"] != "s_x" {
		t.Fatalf("send call: %+v", calls[2])
	}
	// The tool has no way to name a sender.
	for _, tl := range tools.Tools {
		if tl.Name == "agent_send" {
			b, _ := json.Marshal(tl.InputSchema)
			if strings.Contains(string(b), "sender") || strings.Contains(string(b), "from") {
				t.Fatalf("agent_send schema exposes a sender: %s", b)
			}
		}
	}

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_send", Arguments: map[string]any{"to": "s_x", "body": "boom"}})
	if err != nil || !res.IsError {
		t.Fatalf("daemon error should be a tool error: %v %+v", err, res)
	}
	cases := []struct {
		tool, method string
		args         map[string]any
	}{
		{"agent_reply", "reply", map[string]any{"message_id": "m_1", "body": "b"}},
		{"agent_messages", "messages", map[string]any{"limit": 5}},
		{"agent_message_status", "message_status", map[string]any{"message_id": "m_1"}},
		{"agent_get", "get", map[string]any{"session_id": "s_1"}},
		{"agent_announce", "announce", map[string]any{"name": "n", "function": "f"}},
		{"agent_nodes", "nodes", map[string]any{}},
		{"agent_project_add", "project_add", map[string]any{"paths": []string{"~/code/a"}}},
		{"agent_project_list", "projects", map[string]any{"name": "a"}},
	}
	for _, c := range cases {
		n := len(calls)
		cs.CallTool(ctx, &mcp.CallToolParams{Name: c.tool, Arguments: c.args})
		if len(calls) != n+1 || calls[n].method != c.method {
			t.Fatalf("%s did not call %s: %+v", c.tool, c.method, calls[n:])
		}
	}
	// A scan and a dry run reach the daemon as given, and the agent cannot name a machine.
	n := len(calls)
	cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_project_add", Arguments: map[string]any{"dir": "~/code", "scan": true, "dry_run": true, "depth": 2}})
	if p := calls[n].params; calls[n].method != "project_add" || p["dir"] != "~/code" || p["scan"] != true || p["dry_run"] != true || p["depth"] != float64(2) {
		t.Fatalf("scan call: %+v", calls[n])
	}
	for _, tl := range tools.Tools {
		if tl.Name == "agent_project_add" {
			b, _ := json.Marshal(tl.InputSchema)
			var sch struct {
				Properties map[string]any `json:"properties"`
			}
			json.Unmarshal(b, &sch)
			for _, bad := range []string{"on", "machine", "node", "node_id", "host"} {
				if _, ok := sch.Properties[bad]; ok {
					t.Fatalf("agent_project_add lets the agent pick a machine (%s): %s", bad, b)
				}
			}
		}
	}
}
