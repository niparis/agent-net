package tui

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCompactionUnsupportedRowNote(t *testing.T) {
	m := Model{loc: time.UTC}
	a := protocol.AgentView{Harness: "codex", Status: "idle"}
	if got := m.sessionTaskText(a); got != "compaction not supported for codex" {
		t.Fatalf("note=%q", got)
	}
	a.Harness = "claude"
	if got := m.sessionTaskText(a); got != "" {
		t.Fatalf("supported harness note=%q", got)
	}
}

func TestIdleCompactionRowNote(t *testing.T) {
	m := Model{loc: time.UTC}
	for _, tc := range []struct{ reason, want string }{
		{"fill 82%", "compacted 22:13 (fill 82%)"},
		{"idle", "compacted 22:13 (idle)"},
		{"", "compacted 22:13 (idle)"},
	} {
		a := protocol.AgentView{LastCompactedAt: 1700000000, LastCompactedReason: tc.reason, Status: "idle"}
		if got := m.sessionTaskText(a); got != tc.want {
			t.Fatalf("reason %q note=%q, want %q", tc.reason, got, tc.want)
		}
	}
}
