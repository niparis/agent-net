package tui

import "testing"

func TestExitedStatusIsVisible(t *testing.T) {
	if got := statusShort("exited"); got != "exited" {
		t.Fatalf("status %q, want exited", got)
	}
}
