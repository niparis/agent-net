package tui

import "testing"

func TestSessionTypeColumn(t *testing.T) {
	for _, width := range []int{80, 120} {
		cols := columns(width, 18)
		if len(cols) < 2 || cols[0].name != "NAME" || cols[1].name != "MACHINE" {
			t.Fatalf("width %d: %#v", width, cols)
		}
	}
}
