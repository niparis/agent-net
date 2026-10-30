package tui

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestDetailPaneWarnsAboutOldTools(t *testing.T) {
	m, src := workerModel(140, 40)
	const target = "s_0000000000000001"
	set := func(v string, old bool) Model {
		for i := range src.snap.Agents {
			if src.snap.Agents[i].SessionID == target {
				src.snap.Agents[i].ToolsVersion, src.snap.Agents[i].ToolsOld = v, old
			}
		}
		return selectID(refresh(m, src), target)
	}
	screen := func(m Model) string { return detailText(m) }

	if s := screen(set("", false)); strings.Contains(s, "reconnect agent-net") {
		t.Errorf("a session with no tools process must show no warning:\n%s", s)
	}
	if s := screen(set("v0.5.0", false)); strings.Contains(s, "reconnect agent-net") {
		t.Errorf("current tools must show no warning:\n%s", s)
	}
	if s := screen(set("v0.4.5", true)); !strings.Contains(s, "v0.4.5 (old: reconnect agent-net with /mcp)") {
		t.Errorf("old tools not shown:\n%s", s)
	}
	if s := screen(set(protocol.ToolsUnknown, true)); !strings.Contains(s, "unknown (from before v0.4.7; reconnect agent-net with /mcp)") {
		t.Errorf("unknown tools not shown:\n%s", s)
	}
}
