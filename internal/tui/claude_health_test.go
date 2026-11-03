package tui

import (
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestClaudeDeliveryWarningViews(t *testing.T) {
	const warning = "Claude delivery broken by Claude Code test"
	h := []protocol.HarnessInfo{{Name: "claude", DeliveryWarning: warning}}
	m := Model{}
	row := machineRow{nodeID: "machine", harnesses: h}
	if got := m.machineHarnessNote(row, []machineRow{row}); got != warning {
		t.Fatal(got)
	}
	m.snap.Nodes = []protocol.NodeView{{NodeID: "machine", Hostname: "box", Harnesses: h}}
	lines := m.mapDetailLines(protocol.Node{Name: "machine", Kind: "machine"}, true, 120, 50)
	if !strings.Contains(ansi.Strip(strings.Join(lines, "\n")), warning) {
		t.Fatal(lines)
	}
}
