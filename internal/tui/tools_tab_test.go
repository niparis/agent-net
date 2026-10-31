package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

func plain(m Model) string { return ansi.Strip(m.View()) }

func toolsAtScreen(w, h, screen int) (Model, *fakeTools) {
	m, src := newToolsModel(w, h)
	m = openTools(m)
	for i := 0; i < screen; i++ {
		m = pressKey(m, "]")
	}
	return m, src
}

func TestToolsTabGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		for i, name := range []string{"diffs", "all", "project"} {
			m, _ := toolsAtScreen(size[0], size[1], i)
			if i == 2 {
				m.tl.projSel = "homelab"
			}
			golden(t, fmt.Sprintf("tools_%s_%dx%d", name, size[0], size[1]), m.View())
		}
	}
}

func TestToolsPlanGoldens(t *testing.T) {
	// A fix from the differences screen: the plan is in machines, d shows folders.
	m, _ := toolsAtScreen(120, 34, 0)
	m = pressKey(m, "enter")
	golden(t, "tools_plan_120x34", m.View())
	m = pressKey(m, "d")
	golden(t, "tools_plan_folders_120x34", m.View())
	// The same plan at 80x24.
	m, _ = toolsAtScreen(80, 24, 0)
	m = pressKey(m, "enter")
	golden(t, "tools_plan_80x24", m.View())
	// A project picker.
	m, _ = toolsAtScreen(120, 34, 1)
	m = rowByName(m, "found-skill-10")
	m = pressKey(m, "l")
	m = pressKey(m, "l")
	m = pressKey(m, "a")
	golden(t, "tools_picker_120x34", m.View())
}

func rowByName(m Model, name string) Model {
	for i, r := range m.shownRows() {
		if r.Name == name && r.Origin != toolsdiff.OriginRepo {
			m.tl.row = i
		}
	}
	return m
}

func rowByNameFrom(m Model, name, from string) Model {
	for i, r := range m.shownRows() {
		if r.Name == name && strings.HasPrefix(r.From, from) {
			m.tl.row = i
		}
	}
	return m
}

func TestToolsTabOpensWithKey5AndDifferencesFirst(t *testing.T) {
	m, _ := newToolsModel(120, 34)
	m = openTools(m)
	if m.tab != toolsTab || m.tl.screen != toolsDiffs || !m.tl.loaded {
		t.Fatalf("tab %d screen %d loaded %v", m.tab, m.tl.screen, m.tl.loaded)
	}
	v := plain(m)
	for _, want := range []string{"1 Sessions  2 Messages  3 Quotas  4 Map  5 Tools", "What differs", "code-review differs", "design-taste-frontend has 2 different copies on research", "fix: "} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q in\n%s", want, v)
		}
	}
	if !strings.Contains(v, "CTO") {
		t.Errorf("the CTO box is missing:\n%s", v)
	}
}

func TestToolsScreensSwitchWithBracketsNotDigits(t *testing.T) {
	m, _ := toolsAtScreen(120, 34, 0)
	m = pressKey(m, "]")
	if m.tl.screen != toolsAll || m.tab != toolsTab {
		t.Fatalf("screen %d tab %d", m.tl.screen, m.tab)
	}
	m = pressKey(m, "]")
	m = pressKey(m, "]")
	if m.tl.screen != toolsDiffs {
		t.Fatalf("wraps to the first screen, got %d", m.tl.screen)
	}
	m = pressKey(m, "[")
	if m.tl.screen != toolsProject {
		t.Fatalf("[ goes back, got %d", m.tl.screen)
	}
	// digits stay the global tabs
	m = pressKey(m, "1")
	if m.tab != 0 {
		t.Fatalf("1 must open Sessions, tab %d", m.tab)
	}
	m = openTools(m)
	if !strings.Contains(ansi.Strip(m.helpLine(120)), "[ ]") {
		t.Errorf("the hint line must say how to switch screens: %q", ansi.Strip(m.helpLine(120)))
	}
}

func TestToolsDifferencesAreOrderedAndAcceptedLeaveTheList(t *testing.T) {
	m, src := toolsAtScreen(120, 34, 0)
	ds := m.shownDiffs()
	if len(ds) < 5 || ds[0].Kind != toolsdiff.KindTwoCopies {
		t.Fatalf("diffs %+v", ds)
	}
	first := ds[0]
	m = pressKey(m, "x")
	if len(src.acceptCall) != 1 || src.acceptCall[0].Key != first.Key || !src.acceptCall[0].Accepted {
		t.Fatalf("accept calls %+v", src.acceptCall)
	}
	if len(m.shownDiffs()) != len(ds)-1 {
		t.Fatalf("accepted difference must leave the list")
	}
	if strings.Contains(plain(m), "design-taste-frontend has") {
		t.Errorf("accepted line still shown:\n%s", plain(m))
	}
	m = pressKey(m, ".")
	if v := plain(m); !strings.Contains(v, "(accepted)") || !strings.Contains(v, "design-taste-frontend has") {
		t.Errorf(". must show accepted ones:\n%s", v)
	}
	// x again takes the mark off
	for i, d := range m.shownDiffs() {
		if d.Key == first.Key {
			m.tl.row = i
		}
	}
	m = pressKey(m, "x")
	if len(src.acceptCall) != 2 || src.acceptCall[1].Accepted {
		t.Fatalf("accept calls %+v", src.acceptCall)
	}
}

func TestToolsAcceptedMarksComeFromTheGatewayAtLoad(t *testing.T) {
	m, src := newToolsModel(120, 34)
	src.accepted = []string{"two-copies:design-taste-frontend:research"}
	m = openTools(m)
	for _, d := range m.shownDiffs() {
		if d.Kind == toolsdiff.KindTwoCopies {
			t.Fatalf("an accepted difference came back after a restart: %+v", d)
		}
	}
}

func TestToolsAcceptFailureRevertsTheScreen(t *testing.T) {
	m, src := toolsAtScreen(120, 34, 0)
	src.acceptErr = fmt.Errorf("gateway said no")
	n := len(m.shownDiffs())
	m = pressKey(m, "x")
	if len(m.shownDiffs()) != n {
		t.Fatalf("the mark was not stored, the list must be as before")
	}
	if !strings.Contains(m.tl.toast, "could not store") {
		t.Errorf("toast %q", m.tl.toast)
	}
}

func TestToolsEnterOpensPlanFirstAndEscCancels(t *testing.T) {
	m, src := toolsAtScreen(120, 34, 0)
	// the drift line: the plan comes from the gateway and nothing is applied yet
	for i, d := range m.shownDiffs() {
		if d.Kind == toolsdiff.KindDrift {
			m.tl.row = i
		}
	}
	m = pressKey(m, "enter")
	if m.tl.modal == nil {
		t.Fatal("enter must open a plan")
	}
	v := plain(m)
	for _, want := range []string{"Make every machine use v3", "+ research: code-review", "+ Nicolas-MBP: code-review", "enter apply", "d exact folders", "esc cancel"} {
		if !strings.Contains(v, want) {
			t.Errorf("plan lacks %q:\n%s", want, v)
		}
	}
	if len(src.applied) != 0 {
		t.Fatal("nothing may run before enter on the plan")
	}
	if strings.Contains(v, "/home/n/.claude/skills/x") {
		t.Errorf("folders are shown only after d:\n%s", v)
	}
	m = pressKey(m, "d")
	if !strings.Contains(plain(m), "/home/n/.claude/skills/x") {
		t.Errorf("d must show the exact folders:\n%s", plain(m))
	}
	m = pressKey(m, "esc")
	if m.tl.modal != nil || len(src.applied) != 0 {
		t.Fatalf("esc cancels: modal %v applied %v", m.tl.modal, src.applied)
	}
	// enter then enter applies the plan that was shown
	m = pressKey(m, "enter")
	m = pressKey(m, "enter")
	if len(src.applied) != 1 || src.applied[0] != "plan_2" {
		t.Fatalf("applied %v plans %+v", src.applied, src.plans)
	}
	if v := plain(m); !strings.Contains(v, "pending") || !strings.Contains(v, "machine offline") {
		t.Errorf("the result must report the pending target:\n%s", v)
	}
}

func TestToolsAllToolsGridHasNoHarnessColumnsOrNames(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, _ := toolsAtScreen(size[0], size[1], 1)
		v := plain(m)
		header := ""
		for _, l := range strings.Split(v, "\n") {
			if strings.Contains(l, "NAME") && strings.Contains(l, "FROM") {
				header = l
			}
		}
		for _, want := range []string{"NAME", "FROM", "research", "Nicolas-MBP", "PROJECTS"} {
			if !strings.Contains(header, want) {
				t.Errorf("%dx%d header %q lacks %q", size[0], size[1], header, want)
			}
		}
		for _, bad := range []string{"claude", "codex", "opencode", "Claude", "Codex", "OpenCode", " pi "} {
			if strings.Contains(v, bad) {
				t.Errorf("%dx%d: harness name %q in the All tools screen:\n%s", size[0], size[1], bad, v)
			}
		}
	}
}

func TestToolsAllToolsOriginsAndCells(t *testing.T) {
	m, _ := toolsAtScreen(120, 34, 1)
	rows := m.shownRows()
	byName := map[string]toolsdiff.Row{}
	for _, r := range rows {
		byName[r.Name+"/"+r.Origin] = r
	}
	if r := byName["deploy-test-server/repo"]; r.From != "repo sovl-clone" {
		t.Errorf("repo row %+v", r)
	}
	ri, mi := -1, -1
	for i, mc := range m.tl.cat.Machines {
		if mc.Name == "research" {
			ri = i
		} else {
			mi = i
		}
	}
	if r := byName["found-skill-10/machines"]; r.From != "your machines" || r.Cells[ri].State != toolsdiff.CellFound || r.Cells[mi].State != toolsdiff.CellOff {
		t.Errorf("found row %+v", r)
	}
	if r := byName["tenebrae-library/gateway"]; r.Kind != "mcp" {
		t.Errorf("mcp row %+v", r)
	}
	for name, wants := range map[string][]string{
		"tenebrae-library":    {"◆ tenebrae-library", "gateway on research"},
		"deploy-test-server":  {"⌂ deploy-test-server", "repo sovl-clone"},
		"using-git-worktrees": {"agent-net v2"},
	} {
		v := plain(rowByNameFrom(m, name, ""))
		for _, want := range wants {
			if !strings.Contains(v, want) {
				t.Errorf("screen lacks %q:\n%s", want, v)
			}
		}
	}
}

func TestToolsTurnOffOnAMachineCellPlansAnUninstall(t *testing.T) {
	m, src := toolsAtScreen(120, 34, 1)
	m = rowByNameFrom(m, "research", "agent-net")
	m = pressKey(m, "l") // the second machine column is research
	m = pressKey(m, "u")
	if m.tl.modal == nil {
		t.Fatalf("u must open a plan: %q", m.tl.toast)
	}
	if len(src.plans) != 1 || src.plans[0].Target.Scope != "uninstalled" {
		t.Fatalf("plans %+v", src.plans)
	}
	v := plain(m)
	if !strings.Contains(v, "Turn off research on research") || !strings.Contains(v, "only on research") {
		t.Errorf("plan text:\n%s", v)
	}
	m = pressKey(m, "esc")
	if len(src.applied) != 0 {
		t.Fatal("esc must not apply")
	}
}

func TestToolsTurningOnAnUnmanagedSkillImportsThenPlansThenApplies(t *testing.T) {
	m, src := toolsAtScreen(120, 34, 1)
	m = rowByName(m, "found-skill-10")
	m = pressKey(m, "a")
	if m.tl.modal == nil || len(src.imports) != 0 || len(src.plans) != 0 {
		t.Fatalf("the plan must come first: imports %v plans %v", src.imports, src.plans)
	}
	if v := plain(m); !strings.Contains(v, "agent-net store") {
		t.Errorf("the plan must say the skill goes into the store:\n%s", v)
	}
	m = pressKey(m, "enter")
	if len(src.imports) != 1 || len(src.plans) != 1 || len(src.applied) != 1 {
		t.Fatalf("imports %v plans %v applied %v", src.imports, src.plans, src.applied)
	}
	if src.imports[0].Location.NodeID != toolNodeR {
		t.Errorf("import %+v", src.imports[0])
	}
}

func TestToolsRepoSkillRemovalOffersBranchAndAsksTheAgent(t *testing.T) {
	m, src := toolsAtScreen(120, 34, 1)
	m = rowByNameFrom(m, "deploy-test-server", "repo")
	m = pressKey(m, "u")
	v := plain(m)
	for _, want := range []string{"Remove deploy-test-server from the sovl-clone repo", "agent-net/remove-deploy-test-server", "ask sovl-clone's agent", "uncommitted changes in this skill folder", ".agents/skills/deploy-test-server/SKILL.md", ".claude/skills/deploy-test-server/SKILL.md"} {
		if !strings.Contains(v, want) {
			t.Errorf("repo plan lacks %q:\n%s", want, v)
		}
	}
	m = pressKey(m, "a")
	if m.tl.modal == nil || len(src.applied) != 1 || len(src.plans) != 1 || src.plans[0].Operation != "repo_remove" {
		t.Fatalf("a must create the planned branch: modal %+v plans %+v applied %v", m.tl.modal, src.plans, src.applied)
	}
	if got := plain(m); !strings.Contains(got, "branch agent-net/remove-deploy-test-server created in sovl-clone: merge it when ready") || !strings.Contains(got, "commit "+strings.Repeat("a", 40)) {
		t.Fatalf("branch result:\n%s", got)
	}
	m = pressKey(m, "esc")
	m = rowByNameFrom(m, "deploy-test-server", "repo")
	m = pressKey(m, "u")
	m = pressKey(m, "b")
	if len(src.sent) != 1 || !strings.HasPrefix(src.sent[0], "s_sovl: ") || !strings.Contains(src.sent[0], "deploy-test-server") || !strings.Contains(src.sent[0], "Do not commit to the default branch") {
		t.Fatalf("sent %v", src.sent)
	}
	if len(src.plans) != 2 || src.plans[1].Operation != "repo_remove" || len(src.applied) != 1 {
		t.Errorf("repo removal should use the repo operation without changing the assignment: plans=%+v applied=%v", src.plans, src.applied)
	}
}

func TestToolsRepoCopyHidesManagedCopyOnProjectScreen(t *testing.T) {
	m, _ := toolsAtScreen(120, 34, 2)
	m.tl.projSel = "sovl-clone"
	v := plain(m)
	for _, want := range []string{"FROM THE REPO", "⌂ deploy-test-server", "ON FOR THIS PROJECT", "hidden by the repo's own copy"} {
		if !strings.Contains(v, want) {
			t.Errorf("project screen lacks %q:\n%s", want, v)
		}
	}
	m.tl.projSel = "homelab"
	if v := plain(m); !strings.Contains(v, "- estate-runbook") {
		t.Errorf("the charter's need must say what covers it:\n%s", v)
	}
	m = pressKey(m, "m")
	if v := plain(m); !strings.Contains(v, "gets on research") {
		t.Errorf("m must change the machine:\n%s", v)
	}
}

func TestToolsProjectPickerFilterScalesAndPlansTheTickedProjects(t *testing.T) {
	m, src := newToolsModel(120, 34)
	for i := 0; i < 60; i++ {
		src.snap.Projects = append(src.snap.Projects, protocol.ProjectView{Name: fmt.Sprintf("proj-%02d", i), NodeID: toolNodeR, Source: protocol.SourceRegistered})
	}
	m = refresh(m, src)
	m = openTools(m)
	m = pressKey(m, "]")
	m = rowByName(m, "found-skill-10")
	m = pressKey(m, "l")
	m = pressKey(m, "l")
	m = pressKey(m, "a")
	p := m.tl.modal.picker
	if p == nil || len(p.shown()) != 73 {
		t.Fatalf("picker %+v", p)
	}
	m = pressKey(m, "/")
	for _, k := range "proj-4" {
		m = pressKey(m, string(k))
	}
	if got := p.shown(); len(got) != 10 {
		t.Fatalf("filter proj-4 shows %v", got)
	}
	m = pressKey(m, "enter")
	m = pressKey(m, " ")
	m = pressKey(m, "j")
	m = pressKey(m, " ")
	if !p.sel["proj-40"] || !p.sel["proj-41"] {
		t.Fatalf("ticks %v", p.sel)
	}
	v := plain(m)
	if !strings.Contains(v, "[x] proj-40") || !strings.Contains(v, "2 ticked") {
		t.Errorf("picker view:\n%s", v)
	}
	m = pressKey(m, "enter")
	if m.tl.modal == nil || m.tl.modal.picker != nil {
		t.Fatalf("enter on the picker must open the plan")
	}
	if v := plain(m); !strings.Contains(v, "for proj-40, proj-41") {
		t.Errorf("plan must name the projects:\n%s", v)
	}
	m = pressKey(m, "enter")
	if len(src.plans) != 1 || src.plans[0].Target.Scope != "projects" || strings.Join(src.plans[0].Target.Projects, ",") != "proj-40,proj-41" {
		t.Fatalf("plans %+v", src.plans)
	}
}

func TestToolsSearchFiltersTheGridWithoutLeakingKeys(t *testing.T) {
	m, _ := toolsAtScreen(120, 34, 1)
	m = pressKey(m, "/")
	for _, k := range "review" {
		m = pressKey(m, string(k))
	}
	if m.tab != toolsTab {
		t.Fatal("typing must not switch tabs")
	}
	rows := m.shownRows()
	if len(rows) != 1 || rows[0].Name != "code-review" {
		t.Fatalf("rows %+v", rows)
	}
	m = pressKey(m, "esc")
	if m.tl.search != "" || len(m.shownRows()) < 30 {
		t.Fatalf("esc clears the search")
	}
}

func TestToolsTabWithoutToolsSourceExplainsItself(t *testing.T) {
	m := newModel(80, 24)
	m = openTools(m)
	if v := plain(m); !strings.Contains(v, "this daemon cannot list tools") {
		t.Errorf("view:\n%s", v)
	}
}

func TestToolsRescanReadsAgain(t *testing.T) {
	m, src := toolsAtScreen(120, 34, 0)
	m = pressKey(m, "r")
	if src.rescans != 1 || !m.tl.loaded {
		t.Fatalf("rescans %d", src.rescans)
	}
}

func TestToolsScreenGuidanceAndNavigation(t *testing.T) {
	m, _ := toolsAtScreen(120, 34, 0)
	if v := plain(m); !strings.Contains(v, "Things that do not match across your machines") || !strings.Contains(v, "[ ] switch screen") {
		t.Fatalf("differences guidance missing:\n%s", v)
	}
	m = pressKey(m, "right")
	if m.tl.screen != toolsAll {
		t.Fatalf("right did not switch screens: %d", m.tl.screen)
	}
	if v := plain(m); !strings.Contains(v, "Every skill and MCP server") {
		t.Fatalf("all tools guidance missing:\n%s", v)
	}
	col := m.tl.col
	m = pressKey(m, "right")
	if m.tl.screen != toolsAll || m.tl.col == col {
		t.Fatalf("arrow must select a machine cell: screen %d column %d", m.tl.screen, m.tl.col)
	}
	m = pressKey(m, "]")
	if v := plain(m); !strings.Contains(v, "What a session in this project gets") {
		t.Fatalf("project guidance missing:\n%s", v)
	}
	m = pressKey(m, "?")
	if v := plain(m); !strings.Contains(v, "Tools keys") || !strings.Contains(v, "turn on") {
		t.Fatalf("tools help missing:\n%s", v)
	}
}

func TestToolsEmptyScreensExplainWhatAppears(t *testing.T) {
	m, _ := toolsAtScreen(120, 34, 0)
	m.tl.cat = toolsdiff.Catalog{}
	m.tl.items = nil
	m.snap.Projects = nil
	m.tl.projSel = ""
	if v := plain(m); !strings.Contains(v, "Nothing differs:") {
		t.Fatalf("differences: %s", v)
	}
	m = pressKey(m, "]")
	if v := plain(m); !strings.Contains(v, "Skills and MCP servers appear here after a scan") {
		t.Fatalf("tools: %s", v)
	}
	m = pressKey(m, "]")
	if v := plain(m); !strings.Contains(v, "Registered projects and their tools appear here") {
		t.Fatalf("projects: %s", v)
	}
}

func TestRepeatedDifferencesExpand(t *testing.T) {
	m, _ := toolsAtScreen(120, 34, 0)
	d := m.tl.cat.Diffs[0]
	other := d
	other.Name, other.Key, other.Title = "another", "two-copies:another:research", "another has 2 different copies on research"
	m.tl.cat.Diffs = []toolsdiff.Difference{d, other}
	m.tl.row = 0
	if ds := m.shownDiffs(); len(ds) != 1 || !strings.Contains(ds[0].Title, "2 skills have 2 different copies on research") {
		t.Fatalf("group: %+v", ds)
	}
	m = pressKey(m, "enter")
	if ds := m.shownDiffs(); len(ds) != 3 {
		t.Fatalf("expanded: %+v", ds)
	}
}
