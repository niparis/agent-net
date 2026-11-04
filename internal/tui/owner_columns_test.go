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
