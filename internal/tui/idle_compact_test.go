package tui

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestIdleCompactionRowNote(t *testing.T) {
	m := Model{loc: time.UTC}
	a := protocol.AgentView{IdleCompactedAt: 1700000000, Status: "idle"}
	if got := m.sessionTaskText(a); got != "compacted while idle 22:13" {
		t.Fatalf("note=%q", got)
	}
}
