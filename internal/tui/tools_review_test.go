package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestToolsReviewDescribesSelectedAgent(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	m = pressKey(m, "right")
	m = pressKey(m, " ")
	m = pressKey(m, "esc")
	m = pressKey(m, "down")
	m = pressKey(m, "down")
	m = pressKey(m, " ")
	for _, want := range []string{"Switch off archify in homelab.", "archify moves from every project to 12 projects", "Switch on found-skill in homelab."} {
		if !strings.Contains(plain(m), want) {
			t.Errorf("missing %q: %s", want, plain(m))
		}
	}
	if len(s.applied) != 0 || len(s.imports) != 0 {
		t.Fatal("review changed files")
	}
	m = pressKey(m, "d")
	if !strings.Contains(plain(m), "/home/n/.agents/skills/x") {
		t.Fatal(plain(m))
	}
}

func TestToolsSingularCounts(t *testing.T) {
	m, _ := agentToolsFixture(120, 34)
	if !strings.Contains(plain(m), "1 skill is installed outside agent-net") {
		t.Fatal(plain(m))
	}
	m = pressKey(m, "i")
	if !strings.Contains(plain(m), "Import all · 1 skill─") {
		t.Fatal(plain(m))
	}
	m = pressKey(m, "esc")
	m = pressKey(m, "right")
	m = pressKey(m, " ")
	if !strings.Contains(plain(m), "Review 1 change for homelab") || !strings.Contains(plain(m), "Switch off archify in homelab.") {
		t.Fatal(plain(m))
	}
}

func TestToolsCompactActionKeys(t *testing.T) {
	m, _ := agentToolsFixture(120, 34)
	for _, focus := range []string{"agents", "skills"} {
		if focus == "skills" {
			m = pressKey(m, "right")
		}
		lines := m.toolsKeyLines(120)
		if len(lines) != 1 {
			t.Fatalf("%s must fit one line: %v", focus, lines)
		}
		text := ansi.Strip(lines[0])
		for _, bad := range []string{"j/down", "enter/y", "right/enter", "and apply"} {
			if strings.Contains(text, bad) {
				t.Errorf("alias-heavy key bar: %s", text)
			}
		}
		if !strings.Contains(text, "↑↓ move") {
			t.Fatal(text)
		}
		if focus == "agents" && !strings.Contains(text, "→ skills") {
			t.Fatal(text)
		}
		if focus == "skills" && (!strings.Contains(text, "← projects") || !strings.Contains(text, "enter on/off")) {
			t.Fatal(text)
		}
	}
}

func TestToolsSingleReviewKeepsExactFolders(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	m = pressKey(m, "right")
	m = pressKey(m, " ")
	if !strings.Contains(plain(m), "Switch off archify in homelab.") || len(s.toggles) != 1 || len(s.applied) != 0 {
		t.Fatal(plain(m))
	}
	m = pressKey(m, "d")
	if !m.tl.modal.showPaths || len(m.tl.modal.paths) == 0 || !strings.Contains(plain(m), "/home/n/") {
		t.Fatal("exact destination folders missing: " + plain(m))
	}
}

func TestToolsOneItemAndTokenCounts(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	s.items = s.items[:1]
	s.items[0].StartTokens = 1
	m = pressKey(m, "5")
	if !strings.Contains(plain(m), "1 skill on · ~1 token per session") {
		t.Fatal(plain(m))
	}
	m = pressKey(m, "tab")
	if !strings.Contains(plain(m), "1 tool") || strings.Contains(plain(m), "1 tools") {
		t.Fatal(plain(m))
	}
}

func TestToolsModalKeepsSlashFilterKey(t *testing.T) {
	md := &toolsModal{foot: []string{"/", "filter", "j/k", "move", "enter/y", "apply"}}
	parts := modalKeys(md)
	if parts[0] != "/" || parts[2] != "↑↓" || parts[4] != "enter" {
		t.Fatalf("display keys: %v", parts)
	}
}
