package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// This independent allowlist pins every data view to its ordinary tool. Static
// guide text and the CTO's own timeline are the only non-agent sources.
var wantCTOReads = map[string]string{
	"cto_owner_items_find": "own proposals,asks.list,permission.get,permission.pending",
	"cto_intake_find":      protocol.FrameIntakeFind,
	"agent_sessions_find":  "sessions_find",
	"agent_machines_find":  "machines_find", "agent_machine_log": "machine_log",
	"agent_projects_find": "projects_find", "agent_workers_find": "list,get; detail full adds pick_quota",
	"agent_messages_find": "messages",
	"agent_graph_read":    "agent_graph", "agent_settings_read": "settings_read", "agent_mcp_servers_find": "mcp_list",
	"agent_worker_profiles_find": "worker_profiles", "agent_owner_judgments_find": "judgments_find", "projects_layout": "projects_layout",
	"agent_protected_providers_get": protocol.OpProtectedProvidersGet,
}

func TestCTOReadParitySources(t *testing.T) {
	for name, class := range ctoToolClasses {
		if class == ctoRead && name != "cto_ops_list" && wantCTOReads[name] == "" {
			t.Errorf("read %s has no ordinary-agent source", name)
		}
	}
	for name := range wantCTOReads {
		if ctoToolClasses[name] != ctoRead {
			t.Errorf("missing read %s", name)
		}
	}
	for _, name := range []string{"settings_change"} {
		class, err := ctoClass(name, json.RawMessage(`{"area":"cto"}`))
		if err != nil || class != ctoMinor {
			t.Errorf("%s did not apply owner settings: %v", name, err)
		}
	}
}

func TestCTOReadCanary(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.testLogRead = func(ctx context.Context, q protocol.MachineLogRequest) (protocol.MachineLogResult, error) {
			return protocol.MachineLogResult{Machine: q.Machine, Log: q.Log, Since: q.Since, Lines: []string{}, Truncated: false}, nil
		}
	})
	if err := w.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	peer, id := helloPi(t, w.node, "private-fixture", "/fixture", true)
	marker := "OWNER_ONLY_QUESTION_CANARY"
	peer.mustCall("announce", map[string]any{"task": "fixture", "waiting": map[string]any{"kind": "question", "text": marker}})
	_, _, err := g.gw.Store().StoreMessage(protocol.MsgSend{Sender: id, Recipient: w.ownerID(), Body: "OWNER_ONLY_INBOX_CANARY", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}

	_, textID := dialFakePlugin(t, w.sock, "pi", "private-text", func(map[string]any) map[string]any {
		return map[string]any{"ok": true, "page": map[string]any{"entries": []map[string]any{ent("1", "user", "OTHER_SESSION_TEXT_CANARY")}}}
	})
	text := w.cli.mustCall("session_text", map[string]any{"session_id": textID})
	if !strings.Contains(string(text), "OTHER_SESSION_TEXT_CANARY") {
		t.Fatalf("text fixture missing: %s", text)
	}
	st := g.gw.Store()
	if _, err := st.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: id, Text: "PRIVATE_INBOX_ITEM_CANARY"}); err != nil {
		t.Fatal(err)
	}
	settings, err := st.CTOSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Title = "PRIVATE_SETTINGS_CANARY"
	if err = st.SetCTOSettings(settings); err != nil {
		t.Fatal(err)
	}
	// A held request between two sessions is private even on the CTO's machine.
	target, err := st.UpsertSession(w.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "held-target", Repo: protocol.RepoInfo{Project: "held-project"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetEdge(protocol.Edge{From: "*", To: "held-project", Kind: "depends", Rule: "ask"}, "owner"); err != nil {
		t.Fatal(err)
	}
	held, _, err := st.StoreMessage(protocol.MsgSend{Sender: id, Recipient: target, Body: "HELD_REQUEST_CANARY", Type: protocol.TypeRequest})
	if err != nil || held.State != "held" {
		t.Fatalf("held fixture: %+v %v", held, err)
	}
	for name, class := range ctoToolClasses {
		if class != ctoRead {
			continue
		}
		cases := []map[string]any{{}}
		switch name {
		case "agent_sessions_find":
			cases = []map[string]any{{}, {"session": id}}
		case "agent_machine_log":
			cases = []map[string]any{{"machine": w.cfg.Hostname, "log": "daemon"}}
		case "agent_messages_find":
			cases = append(cases, map[string]any{"conversation": held.ConversationID})
		case "agent_graph_read":
			cases = append(cases, map[string]any{"view": "guide"})
		}
		for _, args := range cases {
			encoded, _ := json.Marshal(args)
			value, err := w.d.runCTOToolRaw(context.Background(), name, encoded)
			raw, _ := json.Marshal(value)
			output := string(raw)
			if err != nil {
				output += err.Error()
			}
			for _, secret := range []string{marker, "OWNER_ONLY_INBOX_CANARY", "PRIVATE_INBOX_ITEM_CANARY", "PRIVATE_SETTINGS_CANARY", "HELD_REQUEST_CANARY", "TOOL_PLAN_PATH_CANARY", "MCP_RESULT_CANARY", "OTHER_SESSION_TEXT_CANARY"} {
				if strings.Contains(output, secret) {
					t.Errorf("%s leaked %s", name, secret)
				}
			}
			if err != nil && name != "agent_message_get" {
				t.Errorf("%s %v: %v", name, args, err)
			}
		}
	}
}

func TestCTOAgentReadCanary(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi"})
	caller, _ := helloPi(t, n, "reader", "/fixture", true)
	_, other := helloPi(t, n, "other", "/fixture", true)
	eventually(t, "owner", func() bool { return n.d.ownerSessionID() != "" })
	msg, _, err := g.gw.Store().StoreMessage(protocol.MsgSend{Sender: other, Recipient: n.d.ownerSessionID(), Body: "OTHER_CONVERSATION_CANARY", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := caller.call("message_status", map[string]string{"message_id": msg.ID})
	if err == nil || strings.Contains(string(raw), "OTHER_CONVERSATION_CANARY") {
		t.Fatalf("agent read another conversation: %s %v", raw, err)
	}
}

func TestCTOReadParity(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	_, id := helloPi(t, w.node, "parity", "/fixture", true)
	args, _ := json.Marshal(map[string]any{"session": id})
	for view, source := range wantCTOReads {
		if ctoReadSources[view] != source {
			t.Errorf("%s source %q, want %q", view, ctoReadSources[view], source)
		}
	}
	for view := range ctoReadSources {
		if wantCTOReads[view] == "" {
			t.Errorf("%s has a source the allowlist does not pin", view)
		}
	}
	got, ge := w.d.runCTOToolRaw(context.Background(), "agent_sessions_find", args)
	want, we := w.d.ctoReadConn().dispatch(context.Background(), "sessions_find", args)
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	// last_seen_ago is derived from the wall clock at each read, so two reads
	// a second apart differ only there. The field must be present in both, and
	// every other field is still compared exactly.
	gb, wb = withoutLastSeenAgo(t, gb), withoutLastSeenAgo(t, wb)
	if string(gb) != string(wb) || fmt.Sprint(ge) != fmt.Sprint(we) {
		t.Fatalf("session %s %v; agent %s %v", gb, ge, wb, we)
	}

	ctoWorkersFindParity(t, g, w)

	if _, err := w.d.readCTOTool(context.Background(), "unsourced_read", nil); err == nil {
		t.Fatal("read without source ran")
	}
	caller := w.d.ctoReadConn()
	id, err := caller.boundID()
	if err != nil || id == "" || id == w.ownerID() || caller.client == "cli" {
		t.Fatalf("CTO borrowed owner authority: %q %v", id, err)
	}
}

// ctoWorkersFindParity checks agent_workers_find against its sources: the
// concise read shows no quota read, and the full read's quota_read for a worker
// is exactly what the ordinary agent's pick_quota read gives for it.
func ctoWorkersFindParity(t *testing.T, g *testGateway, w *workerNode) {
	t.Helper()
	st := g.gw.Store()
	worker, err := st.UpsertSession(w.d.NodeID(), protocol.SessionInfo{Harness: "codex", NativeID: "parity-worker",
		Worker: &protocol.WorkerInfo{ID: "w_parity", Owner: w.ownerID(), Project: "alpha", StartedAt: 1, RunGeneration: 1}})
	if err != nil {
		t.Fatal(err)
	}
	quota := &protocol.PickQuota{At: 100, ReadAt: 100,
		Providers:  []protocol.PickQuotaProvider{{Provider: "codex", ReadAt: 100, Windows: []protocol.PickQuotaWindow{{ID: "5h", Kind: "five_hour", Used: 40, ResetsAt: 3700}}}},
		Candidates: []protocol.PickQuotaCandidate{{Pos: 0, Provider: "codex", Windows: []string{"5h"}, Decided: "5h"}}}
	if err := st.RecordWorkerAssessment(protocol.WorkerAssessment{RunID: "r_parity", SessionID: worker, StartedAt: 1, AssessedAt: 1, Label: "normal", Confidence: "high",
		PickRows: []protocol.PickCandidate{{Name: "codex gpt-6.1-terra", Harness: "codex", Model: "gpt-6.1-terra"}}, PickQuota: quota}); err != nil {
		t.Fatal(err)
	}
	read := func(detail string) map[string]any {
		t.Helper()
		value, err := w.d.runCTOToolRaw(context.Background(), "agent_workers_find", json.RawMessage(`{"state":"any","detail":"`+detail+`"}`))
		if err != nil {
			t.Fatalf("agent_workers_find %s: %v", detail, err)
		}
		raw, _ := json.Marshal(value)
		var out struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		for _, item := range out.Items {
			if item["id"] == worker {
				return item
			}
		}
		t.Fatalf("agent_workers_find %s has no worker %s: %s", detail, worker, raw)
		return nil
	}
	if row := read("concise"); row["quota_read"] != nil {
		t.Fatalf("the concise read shows a quota read: %v", row)
	}
	value, err := w.d.ctoReadConn().dispatch(context.Background(), "pick_quota", json.RawMessage(`{"sessions":["`+worker+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var ordinary protocol.PickQuotaResult
	if err := ctoDecode(value, &ordinary); err != nil || len(ordinary.Picks[worker].Lines) == 0 {
		t.Fatalf("the ordinary pick_quota read: %+v %v", ordinary, err)
	}
	got, _ := json.Marshal(read("full")["quota_read"])
	want, _ := json.Marshal(ordinary.Picks[worker].Lines)
	if string(got) != string(want) {
		t.Fatalf("full quota_read %s; the ordinary read %s", got, want)
	}
}

// withoutLastSeenAgo blanks the one field a read derives from the wall clock,
// so two reads taken at different moments still compare on every other field.
// It does the session and every item, because a read carries the same row in
// both places.
func withoutLastSeenAgo(t *testing.T, raw []byte) []byte {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode read: %v", err)
	}
	session, ok := out["session"].(map[string]any)
	if !ok {
		t.Fatalf("read has no session object: %s", raw)
	}
	blankLastSeenAgo(t, session, "session", raw)
	items, ok := out["items"].([]any)
	if !ok {
		t.Fatalf("read has no items array: %s", raw)
	}
	for i, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("read item %d is not an object: %s", i, raw)
		}
		blankLastSeenAgo(t, row, fmt.Sprintf("item %d", i), raw)
	}
	norm, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("encode read: %v", err)
	}
	return norm
}

func blankLastSeenAgo(t *testing.T, row map[string]any, where string, raw []byte) {
	t.Helper()
	if ago, ok := row["last_seen_ago"].(string); !ok || ago == "" {
		t.Fatalf("read has no last_seen_ago in %s: %s", where, raw)
	}
	row["last_seen_ago"] = "derived"
}

// TestWithoutLastSeenAgo pins the comparison helper itself: two reads that
// differ only in a derived last_seen_ago must compare equal, and no other
// field may be hidden.
func TestWithoutLastSeenAgo(t *testing.T) {
	read := func(itemAgos []string, name string) []byte {
		items := make([]string, len(itemAgos))
		for i, ago := range itemAgos {
			items[i] = fmt.Sprintf(`{"id":"s%d","name":%q,"last_seen_ago":%q}`, i, name, ago)
		}
		return []byte(fmt.Sprintf(`{"items":[%s],"total":%d,"session":{"id":"s0","name":%q,"last_seen_ago":%q}}`,
			strings.Join(items, ","), len(items), name, itemAgos[0]))
	}
	first := withoutLastSeenAgo(t, read([]string{"0s ago", "1s ago"}, "parity"))
	second := withoutLastSeenAgo(t, read([]string{"1s ago", "2s ago"}, "parity"))
	if string(first) != string(second) {
		t.Fatalf("a read that differs only in last_seen_ago did not normalise:\n%s\n%s", first, second)
	}
	renamed := withoutLastSeenAgo(t, read([]string{"0s ago", "1s ago"}, "other"))
	if string(first) == string(renamed) {
		t.Fatal("normalisation hid a field other than last_seen_ago")
	}
}
