package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func toolFilter(m Model, q string) Model {
	m = pressKey(m, "esc")
	m = pressKey(m, "/")
	m = typeText(m, q)
	return pressKey(m, "enter")
}
func toolChoose(t *testing.T, m Model, label string) Model {
	t.Helper()
	if m.tl.modal == nil || m.tl.modal.menu == nil {
		t.Fatalf("no menu for %s:\n%s", label, plain(m))
	}
	for i, a := range m.tl.modal.menu.actions {
		if a.label == label {
			for j := 0; j < i; j++ {
				m = pressKey(m, "j")
			}
			return pressKey(m, "enter")
		}
	}
	t.Fatalf("menu lacks %s", label)
	return m
}
func toolPick(t *testing.T, m Model, label string) Model {
	t.Helper()
	p := m.tl.modal.picker
	for i, s := range p.shown() {
		if s == label {
			for j := 0; j < i; j++ {
				m = pressKey(m, "j")
			}
			return pressKey(m, " ")
		}
	}
	t.Fatalf("no picker target %s", label)
	return m
}
func TestToolsOwnerTasks(t *testing.T) {
	t.Run("turn on for one project", func(t *testing.T) {
		m, src := newToolsModel(120, 34)
		m = openTools(m)
		if !strings.Contains(plain(m), "PROJECTS") {
			t.Fatal("Tools did not open the All tools grid")
		}
		m = toolFilter(m, "using-git-worktrees")
		m = pressKey(m, "enter")
		m = toolChoose(t, m, "Turn on for...")
		m = pressKey(m, "enter")
		if !strings.Contains(plain(m), "Tick at least one") {
			t.Fatal("empty picker must explain itself")
		}
		m = toolPick(t, m, "Project: homelab")
		m = pressKey(m, "enter")
		if len(src.applied) != 1 {
			t.Fatal("Enter in the picker must apply the selected scope")
		}
		if len(src.plans) != 1 || src.plans[0].Target.Scope != "projects" || strings.Join(src.plans[0].Target.Projects, ",") != "homelab" || len(src.applied) != 1 {
			t.Fatalf("plans %+v applied %v", src.plans, src.applied)
		}
	})
	t.Run("turn off on one machine", func(t *testing.T) {
		m, src := newToolsModel(120, 34)
		m = toolFilter(openTools(m), "research")
		m = toolFilter(m, "using-git-worktrees")
		m = pressKey(m, "enter")
		m = toolChoose(t, m, "Turn off for...")
		m = toolPick(t, m, "Machine: research")
		m = pressKey(m, "enter")
		if len(src.plans) != 1 || src.plans[0].Target.Scope != "uninstalled" || strings.Join(src.plans[0].Target.Machines, ",") != toolNodeR || len(src.applied) != 1 {
			t.Fatalf("plans %+v applied %v", src.plans, src.applied)
		}
	})
	t.Run("read SKILL.md", func(t *testing.T) {
		m, _ := newToolsModel(80, 24)
		m = toolFilter(openTools(m), "using-git-worktrees")
		m = pressKey(m, "enter")
		m = toolChoose(t, m, "Read it")
		if !strings.Contains(plain(m), "new text") || !strings.Contains(plain(m), "scroll") {
			t.Fatal(plain(m))
		}
		m = pressKey(m, "j")
		if m.tl.modal.scroll != 1 {
			t.Fatal("read view did not scroll")
		}
	})
	t.Run("remove everywhere after confirm", func(t *testing.T) {
		m, src := newToolsModel(120, 34)
		m = toolFilter(openTools(m), "found-skill-10")
		m = pressKey(m, "enter")
		m = toolChoose(t, m, "Remove everywhere")
		if len(src.plans) != 0 || len(src.applied) != 0 {
			t.Fatal("removed before confirmation")
		}
		m = pressKey(m, "enter")
		if len(src.imports) != 1 || len(src.plans) != 1 || src.plans[0].Target.Scope != "uninstalled" || len(src.plans[0].Target.Machines) != 0 || len(src.applied) != 1 {
			t.Fatalf("imports %v plans %+v applied %v", src.imports, src.plans, src.applied)
		}
	})
	t.Run("add MCP", func(t *testing.T) {
		m, src := newToolsModel(120, 34)
		m = openTools(m)
		m = pressKey(m, "n")
		m = pressKey(m, "enter")
		m = typeText(m, "fixture")
		m = pressKey(m, "tab")
		m = typeText(m, "/bin/true")
		m = pressKey(m, "tab")
		m = typeText(m, `["--demo"]`)
		m = pressKey(m, "tab")
		m = typeText(m, "FIXTURE_TOKEN")
		m = pressKey(m, "enter")
		if len(src.addedMCP) != 1 || src.addedMCP[0].Server.Name != "fixture" || src.addedMCP[0].Server.Command != "/bin/true" || strings.Join(src.addedMCP[0].Server.Env, ",") != "FIXTURE_TOKEN" {
			t.Fatalf("added %+v", src.addedMCP)
		}
	})
	t.Run("keep one copy", func(t *testing.T) {
		m, src := newToolsModel(120, 34)
		m = toolFilter(openTools(m), "design-taste-frontend")
		m = pressKey(m, "enter")
		m = toolChoose(t, m, "Compare the copies / Keep this one")
		m = pressKey(m, "a")
		if strings.Contains(m.tl.modal.title, "bbbbbb") {
			t.Fatal("raw hash in plan title")
		}
		m = pressKey(m, "enter")
		if len(src.imports) != 1 || len(src.plans) != 1 || len(src.applied) != 1 {
			t.Fatalf("imports %v plans %v applied %v", src.imports, src.plans, src.applied)
		}
	})
	t.Run("node scope and graph shortcut", func(t *testing.T) {
		m, src := newToolsModel(120, 34)
		m = toolFilter(openTools(m), "node:homelab using-git-worktrees")
		m = pressKey(m, "enter")
		m = toolChoose(t, m, "Turn on for...")
		if m.tl.modal.picker != nil || len(src.plans) != 1 || strings.Join(src.plans[0].Target.Projects, ",") != "homelab" {
			t.Fatalf("scope plan %+v", src.plans)
		}
		m = pressKey(m, "enter")
		if len(src.applied) != 1 {
			t.Fatal("node scope not applied")
		}
		m = pressKey(m, "enter")
		m = pressKey(m, "2")
		m.mp.selected = "homelab"
		m.mapOn = true
		m = pressKey(m, "t")
		if m.tab != toolsTab || m.mapOn || m.tl.search != "node:homelab" {
			t.Fatalf("tab %d filter %s", m.tab, m.tl.search)
		}
	})
}

func TestToolsOwnerGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		fresh := func() Model { m, _ := newToolsModel(size[0], size[1]); return openTools(m) }
		snap := func(name string, m Model) {
			golden(t, fmt.Sprintf("tools_owner_%s_%dx%d", name, size[0], size[1]), m.View())
			for _, l := range strings.Split(plain(m), "\n") {
				if ansi.StringWidth(l) > size[0] {
					t.Fatalf("line too wide: %s", l)
				}
			}
		}
		m := fresh()
		snap("list", m)
		m = toolFilter(m, "design-taste-frontend")
		m = pressKey(m, "enter")
		snap("menu", m)
		m = toolChoose(t, m, "Turn on for...")
		snap("picker", m)
		m = fresh()
		m = toolFilter(m, "using-git-worktrees")
		m = pressKey(m, "enter")
		m = toolChoose(t, m, "Read it")
		snap("read", m)
		m = fresh()
		m = pressKey(m, "n")
		m = pressKey(m, "enter")
		snap("add", m)
		m = fresh()
		m = toolFilter(m, "design-taste-frontend")
		m = pressKey(m, "enter")
		m = toolChoose(t, m, "Compare the copies / Keep this one")
		snap("compare", m)
		m = toolFilter(fresh(), "node:homelab machine:research")
		snap("filtered", m)
	}
}
func TestToolsListOnlyDocumentedKeys(t *testing.T) {
	m, _ := newToolsModel(120, 34)
	m = openTools(m)
	before := plain(m)
	for _, key := range []string{"h", "l", "x", ".", "m", "r"} {
		m = pressKey(m, key)
		if plain(m) != before {
			t.Fatalf("obsolete list key %q changed the screen", key)
		}
	}
	m = toolFilter(m, "project:homelab machine:research using-git-worktrees")
	if len(m.shownRows()) != 1 {
		t.Fatalf("combined filters: %+v", m.shownRows())
	}
	m = pressKey(m, "esc")
	if m.tl.search != "" {
		t.Fatal("esc did not clear filter")
	}
}
func (f *fakeTools) ToolsMCPAdd(_ context.Context, p protocol.MCPAdd) error {
	f.addedMCP = append(f.addedMCP, p)
	return nil
}
func (f *fakeTools) ToolsMCPRemove(_ context.Context, name string) error {
	f.removedMCP = append(f.removedMCP, name)
	return nil
}
func (f *fakeTools) ToolsMCPTest(_ context.Context, name string) error {
	f.testedMCP = append(f.testedMCP, name)
	return nil
}
func (f *fakeTools) ToolsMCPList(context.Context) ([]protocol.MCPServer, error) {
	return []protocol.MCPServer{{Name: "tenebrae-library", Command: "fixture", Env: []string{"FIXTURE_TOKEN"}}}, nil
}
func (f *fakeTools) ToolsImportFolder(_ context.Context, p protocol.ToolImportLocation) error {
	f.folders = append(f.folders, p)
	return nil
}

func (f *fakeTools) ToolsKeepCopy(ctx context.Context, p protocol.ToolCopyChoice) error {
	return f.ToolsImport(ctx, p.Chosen)
}

func TestToolsAddFolderAndMCPMenu(t *testing.T) {
	m, src := newToolsModel(120, 34)
	m = openTools(m)
	m = pressKey(m, "n")
	m = pressKey(m, "j")
	m = pressKey(m, "enter")
	m = typeText(m, "/tmp/fixture-skill")
	m = pressKey(m, "tab")
	m = pressKey(m, "right")
	m = pressKey(m, "enter")
	if len(src.folders) != 1 || src.folders[0].NodeID != toolNodeR || src.folders[0].Location != "/tmp/fixture-skill" {
		t.Fatalf("folder imports %+v", src.folders)
	}
	m = pressKey(m, "enter")
	m = toolFilter(m, "tenebrae-library")
	m = pressKey(m, "enter")
	m = toolChoose(t, m, "Read it")
	if !strings.Contains(plain(m), "FIXTURE_TOKEN") {
		t.Fatal("MCP config preview missing")
	}
	m = pressKey(m, "esc")
	m = pressKey(m, "enter")
	m = toolChoose(t, m, "Test it")
	if strings.Join(src.testedMCP, ",") != "tenebrae-library" {
		t.Fatalf("tests %v", src.testedMCP)
	}
	m = pressKey(m, "enter")
	m = pressKey(m, "enter")
	m = toolChoose(t, m, "Remove everywhere")
	if len(src.removedMCP) != 0 {
		t.Fatal("MCP removed before confirmation")
	}
	m = pressKey(m, "enter")
	if strings.Join(src.removedMCP, ",") != "tenebrae-library" {
		t.Fatalf("removals %v", src.removedMCP)
	}
}

func TestToolsColumnsAndOverlay(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, _ := newToolsModel(size[0], size[1])
		m = openTools(m)
		kindColumn := -1
		for _, line := range strings.Split(plain(m), "\n") {
			if strings.Contains(line, "KIND") {
				kindColumn = ansi.StringWidth(line[:strings.Index(line, "KIND")])
			}
			if at := strings.Index(line, "skill  "); at >= 0 && ansi.StringWidth(line[:at]) != kindColumn {
				t.Fatalf("misaligned kind column: %q", line)
			}
		}
		m = pressKey(m, "enter")
		if strings.Contains(plain(m), "╭─Tools─") || strings.Contains(plain(m), "STATUS") {
			t.Fatal("list box remains under the menu")
		}
		if strings.Count(plain(m), "╭─") != 2 {
			t.Fatalf("expected one action box and the CTO box:\n%s", plain(m))
		}
	}
}

func TestToolsUnassignedStoredSkillIsOff(t *testing.T) {
	m, src := newToolsModel(120, 34)
	src.items = []protocol.ToolItem{{ID: "skill:stored", Kind: "skill", Name: "stored-skill", Hash: "test-hash"}}
	m = openTools(m)
	if rows := m.shownRows(); len(rows) != 1 || !strings.Contains(m.toolStatus(rows[0]), "off everywhere") {
		t.Fatalf("imported folders must remain off: %+v", rows)
	}
}

func TestToolsVersionDriftOffersCompare(t *testing.T) {
	m, _ := newToolsModel(120, 34)
	m = toolFilter(openTools(m), "code-review")
	m = pressKey(m, "enter")
	m = toolChoose(t, m, "Compare the copies / Keep this one")
	if !strings.Contains(plain(m), "Nicolas-MBP") || !strings.Contains(plain(m), "research") {
		t.Fatal("version comparison must name both machines")
	}
}

func TestToolsFilteredAttentionReasons(t *testing.T) {
	m, _ := newToolsModel(120, 34)
	m = openTools(m)
	for _, filter := range []string{"node:homelab", "project:homelab", "node:homelab machine:research", "node:homelab code-review"} {
		t.Run(filter, func(t *testing.T) {
			m.tl.search = filter
			for _, r := range m.shownRows() {
				if strings.Contains(m.toolStatus(r), "available to turn on") {
					t.Fatalf("repetitive off status: %s", m.toolStatus(r))
				}
				switch r.Name {
				case "code-review":
					status := m.toolStatus(r)
					for _, want := range []string{"on for homelab", "v1", "v3", "Nicolas-MBP", "research"} {
						if !strings.Contains(status, want) {
							t.Errorf("flagged drift lacks %q: %s", want, status)
						}
					}
				case "imnplementing-clean-architecture":
					if !strings.Contains(m.toolStatus(r), "off for homelab · looks like a copy of clean-architecture-python") {
						t.Errorf("flagged duplicate lacks its reason: %s", m.toolStatus(r))
					}
				}
			}
		})
	}
}

func TestToolsUnfilteredAttentionReasons(t *testing.T) {
	m, _ := newToolsModel(120, 34)
	m = openTools(m)
	for _, r := range m.shownRows() {
		switch r.Name {
		case "diagnosing-bugs":
			if m.toolAttention(r) && !strings.Contains(m.toolStatus(r), "scripts folder") {
				t.Errorf("harness limitation needs a concrete reason or no flag: %s", m.toolStatus(r))
			}
		case "web-design-guidelines":
			if !m.toolAttention(r) || !strings.Contains(m.toolStatus(r), "repo copy in sovl-clone hides yours") {
				t.Errorf("shadowed skill needs its reason: %s", m.toolStatus(r))
			}
		}
	}
}
