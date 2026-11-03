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
	m = pressKey(m, "down")
	m = pressKey(m, "down")
	m = pressKey(m, " ")
	m = pressKey(m, "enter")
	for _, want := range []string{"- archify off for homelab (stays on for the 12 other projects)", "+ found-skill on for homelab"} {
		if !strings.Contains(plain(m), want) {
			t.Errorf("missing %q:\n%s", want, plain(m))
		}
	}
	if strings.Contains(plain(m), "+ archify for") || len(s.imports) != 0 || len(s.applied) != 0 {
		t.Fatal("review must describe pending changes without applying")
	}
	jobs, why := m.agentJobs("homelab")
	if why != "" || len(jobs[0].target.Projects) != 12 || hasStr(jobs[0].target.Projects, "homelab") {
		t.Fatalf("exact targets changed: %+v %s", jobs, why)
	}
	m = pressKey(m, "d")
	if !strings.Contains(plain(m), "archify target projects:") || !strings.Contains(plain(m), "found-skill target projects: homelab") {
		t.Fatal("target details missing: " + plain(m))
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
	m = pressKey(m, "enter")
	if !strings.Contains(plain(m), "Review 1 change for homelab") || !strings.Contains(plain(m), "- archify off for homelab") {
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
		if focus == "skills" && (!strings.Contains(text, "← agents") || !strings.Contains(text, "enter review (0 changes)")) {
			t.Fatal(text)
		}
	}
}

func TestToolsSingleReviewKeepsExactFolders(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	m = pressKey(m, "right")
	m = pressKey(m, " ")
	m = pressKey(m, "enter")
	if !strings.Contains(plain(m), "- archify off for homelab") || len(s.plans) != 1 || len(s.applied) != 0 {
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
