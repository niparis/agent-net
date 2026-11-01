package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The alias inventory of docs/design/tool-redesign.md: old names keep their
// arguments and result shapes for one release while running the new operation.
var aliasNewTools = map[string]string{
	"agent_list":             "agent_sessions_find",
	"agent_get":              "agent_session_get",
	"agent_nodes":            "agent_machines_find",
	"agent_project_list":     "agent_projects_find",
	"agent_messages":         "agent_messages_find",
	"agent_message_status":   "agent_message_get",
	"agent_send":             "agent_message_send",
	"agent_reply":            "agent_message_reply",
	"agent_graph":            "agent_graph_read",
	"agent_mcp_list":         "agent_mcp_servers_find",
	"agent_worker_profiles":  "agent_worker_profiles_find",
	"agent_start":            "agent_worker_start",
	"agent_stop":             "agent_worker_stop",
	"agent_rate":             "agent_worker_rate",
	"agent_ask_owner":        "agent_owner_ask",
	"agent_request_approval": "agent_owner_ask",
	"agent_propose_idea":     "agent_owner_ask",
}

const aliasMessageJSON = `{"message_id":"m_1","conversation_id":"c_1","sender_session_id":"s_1","recipient_session_id":"s_2","message_type":"request","body":"hello","hop":1,"idempotency_key":"k_1","created_at":"2026-01-01T12:00:00Z","expires_at":"2026-01-02T12:00:00Z","state":"delivered","sender_name":"s01"}`

const aliasSessionJSON = `{"id":"s_1","name":"s01","kind":"agent","machine":"research","project":"housing","harness":"claude","model":"standard","online":true,"status":"idle","task":"work on s01","last_seen":"2023-11-14T22:13:20Z","task_started":null,"last_seen_ago":"2h ago","folder":"/work/housing","branch":"main","remote":"https://example.com/r.git","function":"housing work","expertise":"Go","capabilities":["go"],"purpose":"Sells things","rules":{"deploy_rule":"owner"}}`

// aliasFixture answers the new tools' daemon methods with the same data the
// legacy shapes were recorded from.
func aliasFixture(method string, params map[string]any) string {
	switch method {
	case "sessions_find":
		return `{"items":[` + aliasSessionJSON + `],"total":1}`
	case "session_get":
		return `{"session":` + aliasSessionJSON + `}`
	case "machines_find":
		return `{"items":[{"id":"n_1","name":"research","online":true,"version":"v0.3.2","harnesses":[{"name":"claude","version":"1.0"}],"sessions_online":3,"workers_running":1,"os":"linux","last_seen":"2023-11-14T22:13:20Z"}],"total":1}`
	case "machine_get":
		switch firstParam(params, "include") {
		case "models":
			return `{"machine":{"id":"n_1","name":"research","online":true,"version":"v0.3.2","harnesses":[{"name":"claude","version":"1.0"}],"sessions_online":3,"workers_running":1},"models":{"items":[{"harness":"claude","id":"claude-a"},{"harness":"claude","id":"claude-b"}],"total":2}}`
		case "quotas":
			return `{"machine":{"id":"n_1","name":"research","online":true,"version":"v0.3.2","harnesses":[],"sessions_online":0,"workers_running":0},"quotas":{"items":[{"provider":"claude","window":"weekly","percent_used":20}],"total":1}}`
		case "track_record":
			return `{"machine":{"id":"n_1","name":"research","online":true,"version":"v0.3.2","harnesses":[],"sessions_online":0,"workers_running":0},"track_record":{"items":[],"total":0}}`
		case "playbook":
			return `{"machine":{"id":"n_1","name":"research","online":true,"version":"v0.3.2","harnesses":[],"sessions_online":0,"workers_running":0},"playbook":{"items":[{"subject":"claude-a","note":"fine"}],"total":1}}`
		}
		return `{}`
	case "projects_find":
		return `{"items":[{"name":"housing","display_name":"","machine":"research","folder":"/work/housing","sessions_online":1,"sessions_total":2,"source":"both","remote":"https://example.com/r.git"}],"total":1}`
	case "messages":
		return `{"messages":[` + aliasMessageJSON + `],"total":1}`
	case "message_status":
		return `{"message":` + aliasMessageJSON + `}`
	case "send", "reply":
		return `{"message":` + aliasMessageJSON + `,"idempotency_key":"k_2"}`
	case "agent_graph":
		return `{"nodes":[{"name":"shop","kind":"component","maturity":"mature","charter":{"purpose":"Sells things"},"rules":[{"name":"deploy_rule","value":"owner"}]}],"edges":[{"from":"shop","to":"billing","kind":"depends","rule":"ask"}],"total":1,"edge_total":1}`
	case "mcp_list":
		return `{"servers":[{"name":"demo","node_id":"n_1","hostname":"research","transport":"stdio","command":"/bin/demo","enabled":true,"health":"ok","host_online":true,"added_by_node":"n_1","added_by_session":"s_1","added_by_harness":"claude","tools":[{"name":"search"}]}]}`
	case "worker_profiles":
		return `[{"name":"flash","harness":"opencode","model":"deepseek","added_by":"cli","added_at":1700000000,"available":true,"machines_checked":["research"]}]`
	case "worker_start":
		return `{"session_id":"s_w","node_id":"n_1","hostname":"research","project":"housing","path":"/work/housing","harness":"opencode","model":"deepseek","permissions":"sandboxed","status":"started"}`
	case "worker_stop":
		return `{"session":"s_w","stopped":true}`
	case "agent_rate":
		return `{"accepted":true,"worker":"s_w","score":4,"model":"deepseek","difficulty":"normal"}`
	case "waiting":
		return `null`
	case "request_approval", "propose_idea":
		kind := "approval"
		if method == "propose_idea" {
			kind = "feature_idea"
		}
		return `{"id":"i_1","kind":"` + kind + `","state":"open"}`
	case "worker_handback":
		return `{"status":"done","complete":true,"sent":{"message_id":"m_2"}}`
	case "agent_learn":
		return `{"accepted":true,"id":7,"status":"new","remaining":2}`
	case "project_add":
		return `{"added":[{"name":"housing","path":"/w","remote":"r","default_branch":"main"}],"existing":[],"machine":"research"}`
	case "announce":
		return `{"profile":{"name":"s01","project":"housing","task":"t","expertise":"x","capabilities":["go"]}}`
	}
	return `{}`
}

func firstParam(params map[string]any, key string) string {
	switch v := params[key].(type) {
	case string:
		return v
	case []any:
		if len(v) > 0 {
			s, _ := v[0].(string)
			return s
		}
	}
	return ""
}

func aliasFake(t *testing.T, calls *[]call) Caller {
	t.Helper()
	return func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if calls != nil {
			*calls = append(*calls, call{method, m})
		}
		return json.RawMessage(aliasFixture(method, m)), nil
	}
}

// Every old name is still listed and says how to replace it.
func TestAliasNamesAndDeprecation(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "")
	cs := connect(t, aliasFake(t, nil))
	listed, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	for _, tool := range listed.Tools {
		tools[tool.Name] = tool
	}
	for old, newTool := range aliasNewTools {
		tool := tools[old]
		if tool == nil {
			t.Errorf("old tool %s is no longer listed", old)
			continue
		}
		want := "Deprecated: use " + newTool + ". This name is removed in the next release."
		if !strings.HasPrefix(tool.Description, want) {
			t.Errorf("%s description starts %q, want %q", old, tool.Description, want)
		}
	}
}

// Each alias reaches the new tool's daemon method, not the old one.
func TestAliasCallsTheNewPath(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "")
	cases := []struct {
		old    string
		args   map[string]any
		method string
	}{
		{"agent_list", map[string]any{"text": "go"}, "sessions_find"},
		{"agent_get", map[string]any{"session_id": "s_1"}, "session_get"},
		{"agent_nodes", map[string]any{}, "machines_find"},
		{"agent_project_list", map[string]any{"name": "housing"}, "projects_find"},
		{"agent_messages", map[string]any{"limit": 5}, "messages"},
		{"agent_message_status", map[string]any{"message_id": "m_1"}, "message_status"},
		{"agent_send", map[string]any{"to": "s_2", "body": "hi"}, "send"},
		{"agent_reply", map[string]any{"message_id": "m_1", "body": "ok"}, "reply"},
		{"agent_graph", map[string]any{"node": "shop"}, "agent_graph"},
		{"agent_mcp_list", map[string]any{}, "mcp_list"},
		{"agent_worker_profiles", map[string]any{}, "worker_profiles"},
		{"agent_start", map[string]any{"task": "do", "touches": []string{"ui"}}, "worker_start"},
		{"agent_stop", map[string]any{"session": "s_w"}, "worker_stop"},
		{"agent_rate", map[string]any{"worker": "s_w", "score": 4}, "agent_rate"},
		{"agent_ask_owner", map[string]any{"question": "merge?"}, "waiting"},
		{"agent_request_approval", map[string]any{"action": "deploy", "recommendation": "yes"}, "request_approval"},
		{"agent_propose_idea", map[string]any{"text": "feature", "recommendation": "yes"}, "propose_idea"},
	}
	for _, tc := range cases {
		var calls []call
		cs := connect(t, aliasFake(t, &calls))
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.old, Arguments: tc.args})
		if err != nil || res.IsError {
			t.Errorf("%s: %v %+v", tc.old, err, res)
			continue
		}
		if len(calls) == 0 || calls[0].method != tc.method {
			t.Errorf("%s called %v, want %s first", tc.old, methods(calls), tc.method)
		}
	}
}

// agent_nodes reads every category the old answer carried, so models and
// quotas are not dropped.
func TestAliasNodesKeepsModelsAndQuotas(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "")
	var calls []call
	cs := connect(t, aliasFake(t, &calls))
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_nodes"})
	if err != nil || res.IsError {
		t.Fatalf("agent_nodes: %v %+v", err, res)
	}
	got := map[string]any{}
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	nodes, _ := got["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("nodes: %#v", got["nodes"])
	}
	node, _ := nodes[0].(map[string]any)
	harnesses, _ := node["harnesses"].([]any)
	if len(harnesses) != 1 {
		t.Fatalf("harnesses: %#v", node["harnesses"])
	}
	harness, _ := harnesses[0].(map[string]any)
	if models, _ := harness["models"].([]any); len(models) != 2 {
		t.Fatalf("models dropped: %#v", harness)
	}
	if node["quotas"] == nil {
		t.Fatalf("quotas dropped: %#v", node)
	}
	included := false
	for _, c := range calls {
		if c.method == "machine_get" {
			included = true
		}
	}
	if !included {
		t.Fatalf("agent_nodes did not read the machine categories: %v", methods(calls))
	}
}

// The old result shapes, recorded from the old implementation in
// testdata/legacy_shapes.json, survive the alias.
func TestAliasKeepsOldResultShapes(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "")
	raw, err := os.ReadFile("testdata/legacy_shapes.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]any
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args map[string]any
	}{
		{"agent_list", map[string]any{"text": "go"}},
		{"agent_get", map[string]any{"session_id": "s_1"}},
		{"agent_nodes", map[string]any{}},
		{"agent_project_list", map[string]any{"name": "housing"}},
		{"agent_messages", map[string]any{"limit": 5}},
		{"agent_message_status", map[string]any{"message_id": "m_1"}},
		{"agent_send", map[string]any{"to": "s_2", "body": "hi"}},
		{"agent_reply", map[string]any{"message_id": "m_1", "body": "ok"}},
		{"agent_graph", map[string]any{"node": "shop", "offset": 0, "limit": 5}},
		{"agent_mcp_list", map[string]any{}},
		{"agent_worker_profiles", map[string]any{}},
		{"agent_start", map[string]any{"task": "do", "touches": []string{"ui"}}},
		{"agent_stop", map[string]any{"session": "s_w"}},
		{"agent_rate", map[string]any{"worker": "s_w", "score": 4}},
		{"agent_ask_owner", map[string]any{"question": "merge?"}},
		{"agent_request_approval", map[string]any{"action": "deploy", "recommendation": "yes"}},
		{"agent_propose_idea", map[string]any{"text": "feature", "recommendation": "yes"}},
		{"agent_handback", map[string]any{"report": "done"}},
		{"agent_learn", map[string]any{"subject_kind": "model", "subject": "deepseek", "finding": "works"}},
		{"agent_project_add", map[string]any{"paths": []string{"/w"}}},
		{"agent_announce", map[string]any{"task": "t"}},
	}
	cs := connect(t, aliasFake(t, nil))
	for _, tc := range cases {
		want, ok := golden[tc.name]
		if !ok {
			t.Errorf("no recorded shape for %s", tc.name)
			continue
		}
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		if err != nil || res.IsError {
			t.Errorf("%s: %v %+v", tc.name, err, res)
			continue
		}
		got := resultShape(res.StructuredContent)
		if !reflect.DeepEqual(got, want) {
			gotJSON, _ := json.MarshalIndent(got, "", "  ")
			wantJSON, _ := json.MarshalIndent(want, "", "  ")
			t.Errorf("%s result shape changed:\n got %s\nwant %s", tc.name, gotJSON, wantJSON)
		}
	}
}

// The old message tools never asked for a receipt; the new ones do.
func TestAliasSendAndReplyDoNotAskForAReceipt(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "")
	var calls []call
	cs := connect(t, aliasFake(t, &calls))
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_send",
		Arguments: map[string]any{"to": "s_2", "body": "hi"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_reply",
		Arguments: map[string]any{"message_id": "m_1", "body": "ok"}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if c.method != "send" && c.method != "reply" {
			continue
		}
		if c.params["receipt"] == true {
			t.Errorf("%s asked for a receipt, which changes the old result shape", c.method)
		}
	}
}

// A worker still cannot start workers through the alias: the name is not
// listed and a call is refused.
func TestAliasWorkerCannotStartWorkers(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "w_test")
	cs := connect(t, aliasFake(t, nil))
	ctx := context.Background()
	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "agent_start" || tool.Name == "agent_worker_start" {
			t.Errorf("a worker sees %s", tool.Name)
		}
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_start",
		Arguments: map[string]any{"task": "do", "touches": []string{"ui"}}})
	if err != nil {
		// A client may refuse the call itself; that is still a refusal.
		return
	}
	if !res.IsError {
		t.Fatalf("a worker's agent_start was not refused: %+v", res)
	}
}

func methods(calls []call) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.method)
	}
	return out
}

// resultShape is the key and type tree of a JSON value: the result shape,
// without the values.
func resultShape(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := map[string]any{}
		for k, item := range x {
			m[k] = resultShape(item)
		}
		return m
	case []any:
		if len(x) == 0 {
			return []any{}
		}
		return []any{resultShape(x[0])}
	case string:
		return "string"
	case bool:
		return "bool"
	case float64:
		return "number"
	case nil:
		return "null"
	}
	return "?"
}
