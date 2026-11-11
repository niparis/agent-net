package tui

import (
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
)

func TestNudgeDeskUndoRoute(t *testing.T) {
	e := protocol.DeskLogEntry{Source: "chief", Options: []string{protocol.OpNudgeUndo, `{"id":"np_test","receipt":"nu_test"}`}}
	method, _, ok := logUndo(e)
	if !ok || method != protocol.OpNudgeUndo {
		t.Fatalf("chief undo=%s %v", method, ok)
	}
	e.Source = "apps"
	if _, _, ok := logUndo(e); ok {
		t.Fatal("untrusted source supplied chief Undo")
	}
}
