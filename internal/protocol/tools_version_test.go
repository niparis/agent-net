package protocol

import (
	"strings"
	"testing"
)

func TestToolsStaleNote(t *testing.T) {
	c := ToolsStaleNote("claude", "v0.4.5", "v0.5.0")
	want := "agent-net: your agent-net tools are v0.4.5 (this machine runs v0.5.0). " +
		"Ask your user to reconnect agent-net: in Claude Code type /mcp, pick agent-net, reconnect. New tools appear after that."
	if c != want {
		t.Errorf("claude note:\n got %q\nwant %q", c, want)
	}
	x := ToolsStaleNote("codex", "v0.4.5", "v0.5.0")
	if strings.Contains(x, "Claude Code") || !strings.Contains(x, "Codex") || !strings.HasPrefix(x, "agent-net:") {
		t.Errorf("codex note: %q", x)
	}
	if strings.Contains(c+x, "\n") {
		t.Error("one line expected")
	}
}
