package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// This independent allowlist pins every data view to its ordinary tool. Static
// guide text and the CTO's own timeline are the only non-agent sources.
var wantCTOReads = map[string]string{
	"agents": "list", "nodes": "nodes", "projects": "projects", "get": "get",
	"messages": "messages", "message_status": "message_status", "mcp_list": "mcp_list",
	"worker_profile_list": "worker_profiles", "cto_events": "own timeline", "charter_guide": "static guide",
}

func TestCTOReadParitySources(t *testing.T) {
	for name, class := range ctoToolClasses {
		if class == ctoRead && wantCTOReads[name] == "" {
			t.Errorf("read %s has no ordinary-agent source", name)
		}
	}
	for name := range wantCTOReads {
		if ctoToolClasses[name] != ctoRead {
			t.Errorf("missing read %s", name)
		}
	}
	for _, name := range []string{"cto_settings", "notification_settings"} {
		class, err := ctoClass(name, json.RawMessage(`{}`))
		if err == nil && class != ctoProposal {
			t.Errorf("%s empty call still reads owner settings", name)
		}
	}
}

func TestCTOReadCanary(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
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
	// Approved calls retain their complete results for the owner only. Use the
	// real runner for both sensitive result types and check every delivered note.
	runner := proposalRunner{state: w.d.st, now: time.Now, validate: func(context.Context, proposalCall) error { return nil }, run: func(_ context.Context, call proposalCall, _ string) (json.RawMessage, error) {
		if call.Tool == "tools_plan" {
			return json.RawMessage(`{"files":["/private/TOOL_PLAN_PATH_CANARY"]}`), nil
		}
		return json.RawMessage(`{"body":"MCP_RESULT_CANARY"}`), nil
	}}
	for _, tool := range []string{"tools_plan", "mcp_call"} {
		pid, err := runner.propose(context.Background(), proposalCall{Tool: tool, Args: json.RawMessage(`{}`)}, "")
		if err != nil {
			t.Fatal(err)
		}
		if err = runner.decide(context.Background(), pid, true); err != nil {
			t.Fatal(err)
		}
		if err = runner.deliverNote(context.Background(), func(_ context.Context, note string) error {
			for _, secret := range []string{"TOOL_PLAN_PATH_CANARY", "MCP_RESULT_CANARY"} {
				if strings.Contains(note, secret) {
					t.Errorf("outcome leaked %s", secret)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for name, class := range ctoToolClasses {
		if class != ctoRead {
			continue
		}
		args, _ := json.Marshal(map[string]any{"session_id": id, "node": "missing", "model": "fake/m1", "message_id": held.ID, "conversation_id": held.ConversationID})
		value, err := w.d.runCTOToolRaw(context.Background(), name, args)
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
	}
}

func TestCTOAgentReadCanary(t *testing.T) {
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
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	_, id := helloPi(t, w.node, "parity", "/fixture", true)
	args, _ := json.Marshal(map[string]any{"session_id": id, "message_id": "missing"})
	for view, source := range ctoReadSources {
		if wantCTOReads[view] != source {
			t.Errorf("%s source %q, want %q", view, source, wantCTOReads[view])
		}
		t.Run(view, func(t *testing.T) {
			got, ge := w.d.runCTOToolRaw(context.Background(), view, args)
			want, we := w.d.ctoReadConn().dispatch(context.Background(), wantCTOReads[view], args)
			gb, _ := json.Marshal(got)
			wb, _ := json.Marshal(want)
			if string(gb) != string(wb) || fmt.Sprint(ge) != fmt.Sprint(we) {
				t.Fatalf("CTO %s %v; ordinary agent %s %v", gb, ge, wb, we)
			}
		})
	}
	for view, source := range wantCTOReads {
		if source != "own timeline" && source != "static guide" && ctoReadSources[view] != source {
			t.Errorf("%s lost its source", view)
		}
	}
	if _, err := w.d.readCTOTool(context.Background(), "unsourced_read", nil); err == nil {
		t.Fatal("read without source ran")
	}
	caller := w.d.ctoReadConn()
	id, err := caller.boundID()
	if err != nil || id == "" || id == w.ownerID() || caller.client == "cli" {
		t.Fatalf("CTO borrowed owner authority: %q %v", id, err)
	}
}

func TestCTOOutcomeCanary(t *testing.T) {
	for _, status := range []string{"applied", "failed", "declined", "expired"} {
		t.Run(status, func(t *testing.T) {
			state, err := openState(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer state.close()
			now := time.Now()
			runner := proposalRunner{state: state, now: func() time.Time { return now }, validate: func(context.Context, proposalCall) error { return nil }, run: func(context.Context, proposalCall, string) (json.RawMessage, error) {
				result := json.RawMessage(`{"files":["/private/TOOL_PLAN_PATH_CANARY"],"body":"MCP_RESULT_CANARY"}`)
				if status == "failed" {
					return result, fmt.Errorf("fixture failure")
				}
				return result, nil
			}}
			id, err := runner.propose(context.Background(), proposalCall{Tool: "mcp_call", Args: json.RawMessage(`{}`)}, "")
			if err != nil {
				t.Fatal(err)
			}
			switch status {
			case "expired":
				now = now.Add(25 * time.Hour)
			case "declined":
				err = runner.decide(context.Background(), id, false)
			default:
				err = runner.decide(context.Background(), id, true)
			}
			if status == "failed" && err == nil || status != "failed" && err != nil {
				t.Fatalf("decide: %v", err)
			}
			notes := 0
			if err = runner.deliverNote(context.Background(), func(_ context.Context, note string) error {
				notes++
				if !strings.Contains(note, map[string]string{"applied": "approved and applied", "failed": "approved, failed", "declined": "declined", "expired": "expired"}[status]) {
					t.Errorf("wrong note %q", note)
				}
				for _, marker := range []string{"TOOL_PLAN_PATH_CANARY", "MCP_RESULT_CANARY"} {
					if strings.Contains(note, marker) {
						t.Errorf("outcome leaked %s", marker)
					}
				}
				return nil
			}); err != nil || notes != 1 {
				t.Fatalf("notes=%d err=%v", notes, err)
			}
		})
	}
}
