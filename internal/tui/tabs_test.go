package tui

import (
	"strings"
	"testing"
)

func TestTabsMoveFeedToMessages(t *testing.T) {
	m := newModel(80, 24)
	if !strings.Contains(m.View(), "1 Sessions") {
		t.Fatal("sessions tab missing")
	}
	m = keys(m, "2")
	if !strings.Contains(m.View(), "2 Messages") || !strings.Contains(m.View(), "Messages") {
		t.Fatal("messages tab missing")
	}
	m = keys(m, "3")
	if !strings.Contains(m.View(), "3 Quotas") {
		t.Fatal("quotas tab missing")
	}
}
