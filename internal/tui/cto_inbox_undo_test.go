package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
	"time"
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

func logEntry(id int64, text string, options ...string) protocol.DeskLogEntry {
	return protocol.DeskLogEntry{ID: id, At: time.Now(), Source: "cto", Text: text, Options: options}
}

func logModel(src Source, e protocol.DeskLogEntry) Model {
	m := newModel(80, 24)
	m.src = src
	m.tab = inboxTab
	m.deskLogOpen = true
	m.deskLog = []protocol.DeskLogEntry{e}
	return m
}

// The undo of a CTO change sits on its line of the Desk log. Running it also
// marks the line as undone, so it cannot run twice.
func TestCTOLogUndo(t *testing.T) {
	src := &ctoUndoSource{}
	m := logModel(src, logEntry(5, "CTO applied team create", "graph_undo", `{"id":42}`))
	if !strings.Contains(m.testView(), "undo") {
		t.Fatal(m.testView())
	}
	_, cmd := m.Update(tea.KeyPressMsg{Text: "u", Code: 'u'})
	if cmd == nil {
		t.Fatal("missing undo command")
	}
	msg := cmd()
	if _, ok := msg.(deskUndoneMsg); !ok {
		t.Fatalf("undo result %#v", msg)
	}
	if len(src.methods) != 2 || src.methods[0] != "graph_undo" || src.methods[1] != protocol.OpDeskSettle {
		t.Fatalf("methods %v", src.methods)
	}
	b, _ := json.Marshal(src.params[1])
	if !strings.Contains(string(b), `"id":5`) {
		t.Fatalf("settle args: %s", b)
	}
}

func TestCTOLogUndoMachineRename(t *testing.T) {
	method, args, ok := logUndo(logEntry(1, "x", "node_rename", `{"node_id":"n_test","name":"old-name"}`))
	if !ok || method != protocol.OpNodeRename || !strings.Contains(string(args), `"name":"old-name"`) {
		t.Fatalf("undo: %q %s %v", method, args, ok)
	}
}

func TestCTOLogUndoTools(t *testing.T) {
	method, args, ok := logUndo(logEntry(1, "x", "tools_undo", `{"item":"tool_test"}`))
	if !ok || method != "tools_undo" || !strings.Contains(string(args), `"item":"tool_test"`) {
		t.Fatalf("undo: %q %s %v", method, args, ok)
	}
}

func TestCTOLogUndoMCPNativeRemove(t *testing.T) {
	method, args, ok := logUndo(logEntry(1, "x", "mcp_native_remove", `{"undo_plan_id":"plan_test"}`))
	if !ok || method != "mcp_native_remove" || !strings.Contains(string(args), `"undo_plan_id":"plan_test"`) {
		t.Fatalf("undo: %q %s %v", method, args, ok)
	}
}

func TestCTOLogUndoNativeRefusalRemains(t *testing.T) {
	src := &ctoUndoRefusedSource{}
	m := logModel(src, logEntry(3, "CTO removed native MCP", "mcp_native_remove", `{"undo_plan_id":"plan_test"}`))
	_, cmd := m.Update(tea.KeyPressMsg{Text: "u", Code: 'u'})
	if cmd == nil {
		t.Fatal("missing undo command")
	}
	msg, ok := cmd().(actionMsg)
	if !ok || msg.err == nil || !strings.Contains(msg.err.Error(), "changed since apply") || len(src.methods) != 1 {
		t.Fatalf("a refused undo was marked done: %+v %v", msg, src.methods)
	}
}

func TestCTOLogUndoSettings(t *testing.T) {
	for _, method := range []string{protocol.OpScoresSet, protocol.OpIntegrationsSet, protocol.OpPlaybookSet, "tools_new_projects"} {
		got, _, ok := logUndo(logEntry(1, "x", method, `{}`))
		if !ok || got != method {
			t.Fatalf("%s undo not available", method)
		}
	}
	if _, _, ok := logUndo(protocol.DeskLogEntry{Source: "apps", Options: []string{"graph_undo", `{}`}}); ok {
		t.Fatal("a line that is not the CTO's offered an undo")
	}
}
