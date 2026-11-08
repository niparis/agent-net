package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

type ctoUndoSource struct {
	fakeSource
	methods []string
	params  []any
}

type ctoUndoRefusedSource struct{ ctoUndoSource }

func (s *ctoUndoRefusedSource) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	s.methods = append(s.methods, method)
	return json.RawMessage(`{"state":"refused","reason":"changed since apply"}`), nil
}

func (s *ctoUndoSource) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	s.methods = append(s.methods, method)
	s.params = append(s.params, params)
	return json.RawMessage(`{}`), nil
}

func TestCTOInboxUndo(t *testing.T) {
	src := &ctoUndoSource{}
	m := newModel(80, 24)
	m.src = src
	m.tab = inboxTab
	m.inboxItems = []protocol.InboxItem{{ID: "i_test", Kind: "check", Node: "cto", Text: "CTO applied team create", Status: "open", Options: []string{"graph_undo", `{"id":42}`}}}
	if !strings.Contains(m.testView(), "undo") {
		t.Fatal(m.testView())
	}
	_, cmd := m.Update(tea.KeyPressMsg{Text: "u", Code: 'u'})
	if cmd == nil {
		t.Fatal("missing undo command")
	}
	cmd()
	if len(src.methods) != 2 || src.methods[0] != "graph_undo" || src.methods[1] != "inbox_answer" {
		t.Fatalf("methods %v", src.methods)
	}
	b, _ := json.Marshal(src.params[1])
	if !strings.Contains(string(b), `"id":"i_test"`) {
		t.Fatalf("dismiss args: %s", b)
	}
}

func TestCTOInboxUndoMachineRename(t *testing.T) {
	item := protocol.InboxItem{Node: "cto", Options: []string{"node_rename", `{"node_id":"n_test","name":"old-name"}`}}
	method, args, ok := ctoInboxUndo(item)
	if !ok || method != protocol.OpNodeRename || !strings.Contains(string(args), `"name":"old-name"`) {
		t.Fatalf("undo: %q %s %v", method, args, ok)
	}
}

func TestCTOInboxUndoTools(t *testing.T) {
	item := protocol.InboxItem{Node: "cto", Options: []string{"tools_undo", `{"item":"tool_test"}`}}
	method, args, ok := ctoInboxUndo(item)
	if !ok || method != "tools_undo" || !strings.Contains(string(args), `"item":"tool_test"`) {
		t.Fatalf("undo: %q %s %v", method, args, ok)
	}
}

func TestCTOInboxUndoMCPNativeRemove(t *testing.T) {
	item := protocol.InboxItem{Node: "cto", Options: []string{"mcp_native_remove", `{"undo_plan_id":"plan_test"}`}}
	method, args, ok := ctoInboxUndo(item)
	if !ok || method != "mcp_native_remove" || !strings.Contains(string(args), `"undo_plan_id":"plan_test"`) {
		t.Fatalf("undo: %q %s %v", method, args, ok)
	}
}

func TestCTOInboxUndoNativeRefusalRemains(t *testing.T) {
	src := &ctoUndoRefusedSource{}
	m := newModel(80, 24)
	m.src = src
	m.tab = inboxTab
	m.inboxItems = []protocol.InboxItem{{ID: "i_test", Kind: "check", Node: "cto", Text: "CTO removed native MCP", Status: "open", Options: []string{"mcp_native_remove", `{"undo_plan_id":"plan_test"}`}}}
	_, cmd := m.Update(tea.KeyPressMsg{Text: "u", Code: 'u'})
	if cmd == nil {
		t.Fatal("missing undo command")
	}
	msg, ok := cmd().(actionMsg)
	if !ok || msg.err == nil || !strings.Contains(msg.err.Error(), "changed since apply") || len(src.methods) != 1 {
		t.Fatalf("refused undo dismissed its Inbox item: %+v %v", msg, src.methods)
	}
}

func TestCTOInboxUndoSettings(t *testing.T) {
	for _, method := range []string{protocol.OpScoresSet, protocol.OpIntegrationsSet, protocol.OpPlaybookSet, "tools_new_projects"} {
		item := protocol.InboxItem{Node: "cto", Options: []string{method, `{}`}}
		got, _, ok := ctoInboxUndo(item)
		if !ok || got != method {
			t.Fatalf("%s undo not available", method)
		}
	}
}

func TestCTOInboxUndoApps(t *testing.T) {
	for _, method := range []string{"apps.rename", "apps.grant", "apps.ungrant", "apps.public.on", "apps.public.off"} {
		item := protocol.InboxItem{Node: "cto", Options: []string{method, `{"id":"app_fixture"}`}}
		got, _, ok := ctoInboxUndo(item)
		if !ok || got != method {
			t.Fatalf("%s undo not available", method)
		}
	}
}
