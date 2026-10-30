package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestTabSwitching(t *testing.T) {
	m := newModel(80, 24)
	if !strings.HasPrefix(ansi.Strip(m.titleLine(80)), "1 Sessions   2 Quotas") {
		t.Fatal(m.titleLine(80))
	}
	m = keys(m, "2")
	if m.tab != 1 {
		t.Fatal("2 did not select quotas")
	}
	m = keys(m, "1")
	if m.tab != 0 {
		t.Fatal("1 did not select sessions")
	}
	m = keys(m, "/", "2")
	if m.tab != 0 || m.filter != "2" {
		t.Fatal("filter intercepted tab key")
	}
}
