package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

const testWorkersWarning = "worker window off: run agent-net setup --claude-workers-mod"

// TestWorkerWindowWarningViews shows the warning on the machine row and on the
// machine's map card.
func TestWorkerWindowWarningViews(t *testing.T) {
	h := []protocol.HarnessInfo{{Name: "claude", WorkersWarning: testWorkersWarning}}
	m := Model{}
	row := machineRow{nodeID: "machine", harnesses: h}
	if got := m.machineHarnessNote(row, []machineRow{row}); got != testWorkersWarning {
		t.Fatal(got)
	}
	m.snap.Nodes = []protocol.NodeView{{NodeID: "machine", Hostname: "box", Harnesses: h}}
	lines := m.mapDetailLines(protocol.Node{Name: "machine", Kind: "machine"}, true, 120, 50)
	if !strings.Contains(ansi.Strip(strings.Join(lines, "\n")), testWorkersWarning) {
		t.Fatal(lines)
	}
}

// TestWorkerWindowWarningScreens renders the machine row at the two sizes the
// owner uses.
func TestWorkerWindowWarningScreens(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"80x24", 80, 24}, {"120x34", 120, 34}} {
		m := quietFixtures(newModel(size.w, size.h))
		nodes := append([]protocol.NodeView(nil), m.snap.Nodes...)
		harnesses := append([]protocol.HarnessInfo(nil), nodes[0].Harnesses...)
		for i := range harnesses {
			if harnesses[i].Name == "claude" {
				harnesses[i].WorkersWarning = testWorkersWarning
			}
		}
		nodes[0].Harnesses = harnesses
		m.snap.Nodes = nodes
		golden(t, "worker_window_warning_"+size.name, m.testView())
	}
}
