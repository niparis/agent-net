package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolsImportAllKeepsInstallations(t *testing.T) {
	m, s := newToolsModel(120, 34)
	m = openTable(m)
	m = pressKey(m, "i")
	if m.tl.modal == nil || !strings.Contains(plain(m), "Import all") {
		t.Fatal("i must open one import plan")
	}
	if len(s.imports) != 0 || len(s.plans) != 0 || len(s.applied) != 0 {
		t.Fatal("import happened before confirmation")
	}
	m = pressKey(m, "esc")
	if len(s.imports) != 0 {
		t.Fatal("cancel imported files")
	}
	m = pressKey(m, "i")
	m = pressKey(m, "enter")
	if len(s.imports) == 0 || len(s.plans) != 0 || len(s.applied) != 0 {
		t.Fatalf("import must preserve native copies without reassignment: imports %v plans %v applied %v", s.imports, s.plans, s.applied)
	}
}

func TestTableKeyBarListsEveryKey(t *testing.T) {
	m, _ := newToolsModel(120, 34)
	m = openTable(m)
	for _, key := range []string{"↑↓←→", "space", "enter", "/", "i", "d", "m", "R", "?"} {
		if !strings.Contains(ansi.Strip(m.helpLine(120)), key) {
			t.Errorf("key bar hides %s: %s", key, m.helpLine(120))
		}
	}
	m.tl.col = len(m.tl.projectCols)
	if !strings.Contains(ansi.Strip(m.helpLine(120)), "new projects") {
		t.Errorf("the new projects column has no key hint: %s", m.helpLine(120))
	}
}

func TestToolsImportAllGoldens(t *testing.T) {
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		m, _ := newToolsModel(size[0], size[1])
		m = openTable(m)
		m = pressKey(m, "i")
		golden(t, fmt.Sprintf("tools_table_import_%dx%d", size[0], size[1]), m.View())
	}
}

func TestToolsImportAllSavesEveryOutsideVersion(t *testing.T) {
	m, s := newToolsModel(120, 34)
	m = openTable(m)
	before := append([]protocol.ToolItem(nil), s.items...)
	m = pressKey(m, "i")
	if !strings.Contains(plain(m), "skills") {
		t.Fatal(plain(m))
	}
	m = pressKey(m, "enter")
	if len(s.imports) == 0 || len(s.plans) != 0 || len(s.applied) != 0 {
		t.Fatalf("wrong calls: %v %v %v", s.imports, s.plans, s.applied)
	}
	if !reflect.DeepEqual(before, s.items) {
		t.Fatal("import changed observed items or assignments")
	}
}

func TestTableKeyBarFits(t *testing.T) {
	for _, width := range []int{80, 120} {
		m, _ := newToolsModel(width, 34)
		m = openTable(m)
		lines := m.toolsKeyLines(width)
		if len(lines) > 3 {
			t.Errorf("%d: table needs %d key lines: %v", width, len(lines), lines)
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > width {
				t.Errorf("%d: key bar overflows: %s", width, line)
			}
		}
		if len(strings.Split(m.View(), "\n")) != m.height {
			t.Errorf("%d: changed screen height", width)
		}
	}
}
