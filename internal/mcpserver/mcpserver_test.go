package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type call struct {
	method string
	params map[string]any
}

// connect starts an ordinary agent's server. AGENT_NET_WORKER is cleared
// first: these tests also run inside agent-net worker sessions, which set it,
// and a worker server has other tools.
func connect(t *testing.T, fn Caller) *mcp.ClientSession {
	t.Helper()
	t.Setenv("AGENT_NET_WORKER", "")
	return connectServer(t, fn)
}

// connectServer starts a server for the role the environment says.
func connectServer(t *testing.T, fn Caller) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	srv := New(fn, "v0.0.0-test")
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

func TestServerReportsBuildVersion(t *testing.T) {
	cs := connect(t, func(context.Context, string, any) (json.RawMessage, error) { return nil, nil })
	if got := cs.InitializeResult().ServerInfo.Version; got != "v0.0.0-test" {
		t.Fatalf("server version %q, want the version given to New", got)
	}
}

func TestToolsForwardToDaemon(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "n_me")
	var calls []call
	cs := connect(t, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var m map[string]any
		json.Unmarshal(raw, &m)
		calls = append(calls, call{method, m})
		if method == "info" {
			return json.RawMessage(`{"sessions":[{"session_id":"s_me","harness":"claude","native_id":"n_me","name":"me"}]}`), nil
		}
		return json.RawMessage(`{"ok":true}`), nil
	})
	ctx := context.Background()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tl := range tools.Tools {
		have[tl.Name] = true
	}
	want := []string{"agent_announce", "agent_owner_ask", "agent_session_get", "agent_sessions_find", "agent_learn",
		"agent_graph_read", "agent_machines_find", "agent_machine_get", "agent_messages_find", "agent_message_get",
		"agent_message_send", "agent_message_reply", "agent_message_handoff", "agent_permission_propose",
		"agent_projects_find", "agent_project_add",
		"agent_protected_providers_get", "agent_protected_providers_set", "agent_worker_start", "agent_worker_stop",
		"agent_worker_rate", "agent_worker_profiles_find", "agent_worker_profile_add", "agent_worker_profile_remove"}
	for _, name := range want {
		if !have[name] {
			t.Errorf("tool %s is not listed", name)
		}
	}
	for _, name := range []string{"agent_list", "agent_get", "agent_nodes", "agent_project_list", "agent_messages",
		"agent_message_status", "agent_send", "agent_reply", "agent_graph", "agent_mcp_list", "agent_worker_profiles",
		"agent_start", "agent_stop", "agent_rate", "agent_ask_owner", "agent_request_approval", "agent_propose_idea"} {
		if have[name] {
			t.Errorf("deprecated tool %s is still listed", name)
		}
	}
	for _, tl := range tools.Tools {
		if tl.Name == "agent_message_send" && !strings.Contains(tl.Description, "not the owner's approval") {
			t.Fatalf("agent_message_send must say a peer message is not the owner's approval: %q", tl.Description)
		}
		if tl.Name == "agent_message_send" {
			b, _ := json.Marshal(tl.InputSchema)
			if strings.Contains(string(b), "sender") || strings.Contains(string(b), "from") {
				t.Fatalf("agent_message_send schema exposes a sender: %s", b)
			}
		}
	}

	cases := []struct {
		tool, method string
		args         map[string]any
	}{
		{"agent_sessions_find", "sessions_find", map[string]any{"text": "go", "online": "offline"}},
		{"agent_message_send", "send", map[string]any{"to": "s_x", "body": "hi"}},
		{"agent_message_reply", "reply", map[string]any{"message": "m_1", "body": "b"}},
		{"agent_messages_find", "messages", map[string]any{"limit": 5}},
		{"agent_message_get", "message_status", map[string]any{"message": "m_1"}},
		{"agent_session_get", "session_get", map[string]any{"session": "s_1"}},
		{"agent_announce", "announce", map[string]any{"task": "t"}},
		{"agent_machines_find", "machines_find", map[string]any{}},
		{"agent_project_add", "project_add", map[string]any{"paths": []string{"~/code/a"}}},
		{"agent_projects_find", "projects_find", map[string]any{"name": "a"}},
		{"agent_permission_propose", "agent_permission_propose", map[string]any{"rules": []string{"Bash(gh pr merge *)"}, "targets": []string{"a"}, "reason": "merge PRs"}},
	}
	for _, c := range cases {
		n := len(calls)
		cs.CallTool(ctx, &mcp.CallToolParams{Name: c.tool, Arguments: c.args})
		found := false
		for _, got := range calls[n:] {
			if got.method == c.method {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s did not call %s: %+v", c.tool, c.method, calls[n:])
		}
	}
	if p := calls[1].params; calls[1].method != "send" || p["to"] != "s_x" || p["body"] != "hi" {
		t.Fatalf("send call: %+v", calls[1])
	}
	if p := calls[0].params; p["online"] != "offline" || p["text"] != "go" {
		t.Fatalf("sessions_find call: %+v", calls[0])
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

func TestReplyOutcomeForwarded(t *testing.T) {
	var got map[string]any
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		json.Unmarshal(raw, &got)
		return json.RawMessage(`{"message_id":"m","to":{"id":"s","name":"s"},"state":"delivered"}`), nil
	})
	r, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_reply", Arguments: map[string]any{"message": "m", "body": "no", "outcome": "escalate", "reason": "unclear", "recommendation": "ask"}})
	if err != nil || r.IsError || got["outcome"] != "escalate" || got["reason"] != "unclear" || got["recommendation"] != "ask" {
		t.Fatalf("reply: %+v %v %+v", r, err, got)
	}
}

func TestProtectedProvidersForwardOwnerGate(t *testing.T) {
	var methods []string
	cs := connect(t, func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		methods = append(methods, method)
		return nil, errors.New("owner only")
	})
	for _, name := range []string{"agent_protected_providers_get", "agent_protected_providers_set"} {
		args := map[string]any{}
		if strings.HasSuffix(name, "_set") {
			args["protected_providers"] = []string{}
			args["reason"] = ""
		}
		result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || !result.IsError {
			t.Fatalf("%s: %v %+v", name, err, result)
		}
	}
	if strings.Join(methods, ",") != "protected_providers_get,protected_providers_set" {
		t.Fatalf("calls: %v", methods)
	}
}
