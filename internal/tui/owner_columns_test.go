package tui

import (
	"strings"
	"testing"
)

func TestOwnerColumnsAtEveryWidth(t *testing.T) {
	for _, width := range []int{46, 62, 78, 118} {
		cols := columns(width, 18)
		want := []string{"NAME", "MACHINE", "PROJECT", "MODEL", "STATUS", "CPU", "RAM", "TASK"}
		if len(cols) != len(want) {
			t.Fatalf("width %d: %v", width, cols)
		}
		for i, name := range want {
			if cols[i].name != name {
				t.Errorf("width %d column %d: %s", width, i, cols[i].name)
			}
		}
	}
}

func TestOwnerStatusKeepsWholeBusyHighlight(t *testing.T) {
	for _, width := range []int{80, 120} {
		m := newModel(width, 34)
		cols := columnsForProject(width-2, 18, 10, false)
		for _, c := range cols {
			if c.name == "STATUS" && c.width < len("busy") {
				t.Fatalf("%d: busy is clipped", width)
			}
		}
		if got := m.statusCell(m.snap.Agents[0]); got != "busy" {
			t.Fatalf("status: %q", got)
		}
	}
}

func TestSessionVisualPolish(t *testing.T) {
	m := newModel(80, 24)
	detail := detailText(m)
	for _, label := range []string{"trust    ", "rule     ", "delegate ", "source   "} {
		if !strings.Contains(detail, label) {
			t.Errorf("detail missing label %q", label)
		}
	}
	if strings.Contains(detail, "project source:") {
		t.Error("source is outside label column")
	}
	for _, c := range columnsNarrow(78, 18, 10) {
		if c.name == "NAME" && c.width > 14 || c.name == "MODEL" && c.width > 16 || c.name == "TASK" && c.width < 9 {
			t.Errorf("narrow column: %+v", c)
		}
	}
	for _, line := range strings.Split(m.testView(), "\n") {
		if strings.Contains(line, "scratch") && strings.Contains(line, "pi ·") {
			t.Error("empty model has dangling separator")
		}
	}
}
