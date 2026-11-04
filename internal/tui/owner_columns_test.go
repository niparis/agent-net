package tui

import "testing"

func TestOwnerColumnsAtEveryWidth(t *testing.T) {
	for _, width := range []int{46, 62, 78, 118} {
		cols := columns(width, 18)
		want := []string{"NAME", "TYPE", "PROJECT", "MODEL", "STATUS", "CPU", "RAM", "TASK"}
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
