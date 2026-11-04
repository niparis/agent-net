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
	m, s := agentToolsFixture(120, 34)
	if !strings.Contains(plain(m), "1 skill is installed outside agent-net") {
		t.Fatal("missing import-all banner:\n" + plain(m))
	}
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
	if len(s.imports) != 1 || len(s.plans) != 0 || len(s.applied) != 0 {
		t.Fatalf("import must preserve native copies without reassignment: imports %v plans %v applied %v", s.imports, s.plans, s.applied)
	}
}

func TestToolsOutsideWording(t *testing.T) {
	m, _ := agentToolsFixture(120, 34)
	m = pressKey(m, "tab")
	m.tl.search = "found-skill"
	text := plain(m)
	if strings.Contains(text, "managed") || !strings.Contains(text, "outside agent-net") {
		t.Fatal(text)
	}
	r, _ := m.selectedRow()
	if got := m.toolBaseStatus(r); got != "installed on laptop, outside agent-net (on for every session there)" {
		t.Fatal(got)
	}
}

func TestToolsKeyBarListsLocalKeys(t *testing.T) {
	m, _ := agentToolsFixture(80, 24)
	m = pressKey(m, "tab")
	for _, key := range []string{"t", "R", "tab", "n", "?", "esc", "enter", "/"} {
		if !strings.Contains(ansi.Strip(m.helpLine(80)), key) {
			t.Errorf("key bar hides %s: %s", key, m.helpLine(80))
		}
	}
}

func TestToolsImportAllGoldens(t *testing.T) {
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		m, s := agentToolsFixture(size[0], size[1])
		s.items = append(s.items, toolSkill("outside-helper", "helper", toolLoc(toolNodeR, "research", "/tmp/skills/outside-helper", "user", "", "codex")))
		m = pressKey(m, "5")
		m = pressKey(m, "i")
		golden(t, fmt.Sprintf("tools_import_all_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestToolsImportAllSavesEveryOutsideVersion(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	extra := toolSkill("found-skill", "other", toolLoc(toolNodeR, "research", "/tmp/other", "user", "", "codex"))
	s.items = append(s.items, extra, toolSkill("local-skill", "local", toolLocal(toolNodeR, "research", "/tmp/local", "homelab", "codex")))
	m = pressKey(m, "5")
	before := append([]protocol.ToolItem(nil), s.items...)
	m = pressKey(m, "i")
	if !strings.Contains(plain(m), "2 skills") {
		t.Fatal(plain(m))
	}
	m = pressKey(m, "enter")
	if len(s.imports) != 3 || len(s.plans) != 0 || len(s.applied) != 0 {
		t.Fatalf("wrong calls: %v %v %v", s.imports, s.plans, s.applied)
	}
	if !reflect.DeepEqual(before, s.items) {
		t.Fatal("import changed observed items or assignments")
	}
}

func TestToolsCompleteKeyBars(t *testing.T) {
	for _, width := range []int{80, 120} {
		fresh := func() Model { m, _ := agentToolsFixture(width, 34); return m }
		check := func(name string, m Model, keys ...string) {
			t.Helper()
			lines := m.toolsKeyLines(width)
			if width == 120 && len(lines) != 1 {
				t.Errorf("%s must fit one line at 120 columns: %v", name, lines)
			}
			if len(lines) > 2 {
				t.Errorf("%s needs %d key lines: %v", name, len(lines), lines)
			}
			text := ansi.Strip(strings.Join(lines, "\n"))
			for _, key := range keys {
				if !strings.Contains(text, key) {
					t.Errorf("%s hides %s: %s", name, key, text)
				}
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > width {
					t.Errorf("%s overflows: %s", name, line)
				}
			}
			if len(strings.Split(m.testView(), "\n")) != m.height {
				t.Errorf("%s changed screen height", name)
			}
		}
		m := fresh()
		check("agents", m, "i", "↑↓", "→", "tab", "/", "esc", "?")
		m = pressKey(m, "right")
		check("skills", m, "space", "enter", "←", "i", "?")
		m = pressKey(m, "i")
		check("plan", m, "enter", "esc", "d", "↑↓", "?")
		m = pressKey(fresh(), "tab")
		check("list", m, "t", "R", "n", "enter", "?")
		m = pressKey(m, "enter")
		check("menu", m, "enter", "esc", "↑↓", "?")
		m = toolChoose(t, m, "Turn on for...")
		check("picker", m, "space", "/", "enter", "esc", "?")
		m = pressKey(fresh(), "tab")
		m = pressKey(m, "n")
		m = pressKey(m, "enter")
		check("form", m, "tab", "shift+tab", "←→", "backspace", "enter", "esc", "text")
	}
}

func TestToolsImportedOutsideSkillOffKeepsOtherAgents(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	// Import saves the version but deliberately leaves its native user copy.
	s.items[2].VersionNo = 1
	s.items[2].Locations[0].NodeID = toolNodeR
	s.items[2].Locations[0].Machine = "research"
	m = pressKey(m, "5")
	m = pressKey(m, "right")
	m = pressKey(m, "down")
	m = pressKey(m, "down")
	m = pressKey(m, " ")
	if len(s.toggles) != 1 || s.toggles[0].On || s.toggles[0].Project != "homelab" || len(s.plans) != 0 {
		t.Fatalf("outside skill must use gateway project toggle: %v", s.toggles)
	}
}
