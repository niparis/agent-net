package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

func plain(m Model) string { return ansi.Strip(m.testView()) }

func toolsAtScreen(w, h int) (Model, *fakeTools) {
	m, src := newToolsModel(w, h)
	return openTools(m), src
}

// resolveTools runs a command with the same message-draining helper as keys.
func resolveTools(model tea.Model, cmd tea.Cmd) Model {
	m := model.(Model)
	if cmd != nil {
		m = send(m, cmd())
	}
	return m
}
func enterToolsPlan(m Model) Model {
	if m.tl.modal == nil {
		return resolveTools(m.openFix())
	}
	return pressKey(m, "enter")
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

func TestToolsEnterOpensPlanFirstAndEscCancels(t *testing.T) {
	m, src := toolsAtScreen(120, 34)
	// A scoped turn on shows the gateway's plan first and changes nothing yet.
	m = toolFilter(m, "project:homelab code-review")
	m = pressKey(m, "enter")
	m = toolChoose(t, m, "Turn on for...")
	if m.tl.modal == nil {
		t.Fatal("the scope must open a plan")
	}
	v := plain(m)
	for _, want := range []string{"Turn on code-review", "+ research: code-review", "+ Nicolas-MBP: code-review", "enter apply", "d exact folders", "esc cancel"} {
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
	// enter opens the same plan again; a second enter applies it
	m = pressKey(m, "enter")
	m = toolChoose(t, m, "Turn on for...")
	m = pressKey(m, "enter")
	if len(src.applied) != 1 {
		t.Fatalf("applied %v plans %+v", src.applied, src.plans)
	}
	if v := plain(m); !strings.Contains(v, "pending") || !strings.Contains(v, "machine offline") {
		t.Errorf("the result must report the pending target:\n%s", v)
	}
}

func TestToolsTurningOnAnUnmanagedSkillImportsThenPlansThenApplies(t *testing.T) {
	m, src := toolsAtScreen(120, 34)
	m = toolFilter(m, "machine:research found-skill-10")
	m = pressKey(m, "enter")
	m = toolChoose(t, m, "Turn on for...")
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
	m, src := toolsAtScreen(120, 34)
	open := func() Model {
		m = toolFilter(m, "deploy-test-server")
		m = pressKey(m, "enter")
		return toolChoose(t, m, "Remove everywhere")
	}
	m = open()
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
	m = open()
	m = pressKey(m, "b")
	if len(src.sent) != 1 || !strings.HasPrefix(src.sent[0], "s_sovl: ") || !strings.Contains(src.sent[0], "deploy-test-server") || !strings.Contains(src.sent[0], "Do not commit to the default branch") {
		t.Fatalf("sent %v", src.sent)
	}
	if len(src.plans) != 2 || src.plans[1].Operation != "repo_remove" || len(src.applied) != 1 {
		t.Errorf("repo removal should use the repo operation without changing the assignment: plans=%+v applied=%v", src.plans, src.applied)
	}
}

func TestToolsTargetPickerFilterScalesAndAppliesTheTickedProjects(t *testing.T) {
	m, src := newToolsModel(120, 34)
	for i := 0; i < 60; i++ {
		src.snap.Projects = append(src.snap.Projects, protocol.ProjectView{Name: fmt.Sprintf("proj-%02d", i), NodeID: toolNodeR, Source: protocol.SourceRegistered})
	}
	m = refresh(m, src)
	m = openTools(m)
	m = toolFilter(m, "found-skill-10")
	m = pressKey(m, "enter")
	m = toolChoose(t, m, "Turn on for...")
	p := m.tl.modal.picker
	if p == nil || len(p.shown()) != 75 {
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
	if !p.sel["Project: proj-40"] || !p.sel["Project: proj-41"] {
		t.Fatalf("ticks %v", p.sel)
	}
	v := plain(m)
	if !strings.Contains(v, "[x] Project: proj-40") || !strings.Contains(v, "2 ticked") {
		t.Errorf("picker view:\n%s", v)
	}
	m = pressKey(m, "enter")
	if len(src.plans) != 1 || src.plans[0].Target.Scope != "projects" || strings.Join(src.plans[0].Target.Projects, ",") != "proj-40,proj-41" {
		t.Fatalf("plans %+v", src.plans)
	}
}

func TestToolsSearchFiltersTheGridWithoutLeakingKeys(t *testing.T) {
	m, _ := toolsAtScreen(120, 34)
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

func TestToolsTabGoldens(t *testing.T) {
	initScreenColors()
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		m, _ := toolsAtScreen(size[0], size[1])
		golden(t, fmt.Sprintf("tools_list_%dx%d", size[0], size[1]), m.testView())
	}
}

// The Tools tab is the one list the owner approved: there are no Differences,
// All tools and By project screens, and [ and ] do nothing.
func TestToolsIsOneListWithNoScreenSwitching(t *testing.T) {
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		m, _ := toolsAtScreen(size[0], size[1])
		m = openTools(m)
		before := plain(m)
		for _, key := range []string{"]", "["} {
			m = pressKey(m, key)
			if got := plain(m); got != before {
				t.Fatalf("%dx%d: %q changed the Tools screen:\n%s", size[0], size[1], key, got)
			}
		}
		v := plain(m)
		for _, gone := range []string{"Differences", "By project", "switch screen"} {
			if strings.Contains(v, gone) {
				t.Errorf("%dx%d: the old three screens are back (%q):\n%s", size[0], size[1], gone, v)
			}
		}
		if !strings.Contains(v, "NAME") || !strings.Contains(v, "STATUS") {
			t.Errorf("%dx%d: the one list is not shown:\n%s", size[0], size[1], v)
		}
	}
}

// Every row is one tool. No row groups several tools under a count.
func TestToolsListHasOneRowPerToolAndNoCategoryRow(t *testing.T) {
	m, _ := toolsAtScreen(120, 34)
	m = openTools(m)
	two := func(name string) toolsdiff.Difference {
		return toolsdiff.Difference{Key: "two-copies:" + name + ":research", Kind: toolsdiff.KindTwoCopies, Severity: toolsdiff.SevErr,
			Name: name, Title: name + " has 2 different copies on research", Fix: "compare, then keep one",
			Action: toolsdiff.Action{Kind: toolsdiff.ActChoose, Name: name, Copies: []toolsdiff.Copy{{Name: name}, {Name: name}}}}
	}
	m.tl.cat.Diffs = []toolsdiff.Difference{two("alpha"), two("beta")}
	m = pressKey(m, "[")
	v := plain(m)
	for _, bad := range []string{"skills have", "share a difference", "items share"} {
		if strings.Contains(v, bad) {
			t.Fatalf("a category row groups several tools (%q):\n%s", bad, v)
		}
	}
	rows := m.shownRows()
	for _, r := range rows {
		n := 0
		for _, other := range rows {
			if other.Kind == r.Kind && other.Name == r.Name {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("%s appears in %d rows", r.Name, n)
		}
	}
}

func TestToolsRescanReadsAgain(t *testing.T) {
	m, src := toolsAtScreen(120, 34)
	m = resolveTools(m.toolsRescan())
	if src.rescans != 1 || !m.tl.loaded {
		t.Fatalf("rescans %d", src.rescans)
	}
}

func TestToolsComparisonPlanShowsFiles(t *testing.T) {
	m, _ := toolsAtScreen(120, 34)
	old := toolsdiff.Copy{Name: "demo", Item: "old", NodeID: toolNodeR, Machine: "research", Location: "/home/test/.pi/agent/skills/demo", Hash: "old", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "gone", Fingerprint: "x"}, {Path: "SKILL.md", Fingerprint: "old"}}}}
	newer := toolsdiff.Copy{Name: "demo", Item: "new", NodeID: toolNodeR, Machine: "research", Location: "/home/test/.claude/skills/demo", Hash: "new", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "added", Fingerprint: "y"}, {Path: "SKILL.md", Fingerprint: "new"}}}}
	model, _ := m.openChoose("Keep the newer one in both places", toolsdiff.Action{Kind: toolsdiff.ActChoose, Name: "demo", Copies: []toolsdiff.Copy{newer, old}})
	m = model.(Model)
	m = pressKey(m, "a")
	v := plain(m)
	for _, want := range []string{"demo on research only", "added: added", "removed: gone", "changed: SKILL.md"} {
		if !strings.Contains(v, want) {
			t.Errorf("plan missing %q:\n%s", want, v)
		}
	}
}

func TestToolsComparisonGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, _ := toolsAtScreen(size[0], size[1])
		newer := toolsdiff.Copy{Name: "demo", Item: "new", NodeID: toolNodeR, Machine: "research", Location: "/home/test/.claude/skills/demo", Hash: "new", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "new"}, {Path: "added.txt", Fingerprint: "add"}}}}
		older := toolsdiff.Copy{Name: "demo", Item: "old", NodeID: toolNodeR, Machine: "research", Location: "/home/test/.pi/agent/skills/demo", Hash: "old", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "old"}, {Path: "removed.txt", Fingerprint: "gone"}}}}
		m.tl.cat.Diffs = []toolsdiff.Difference{{Key: "two-copies:demo:research", Kind: toolsdiff.KindTwoCopies, Severity: toolsdiff.SevErr, Name: "demo", Title: "demo has 2 different copies on research, 3 files differ", Fix: "compare, then keep one", Action: toolsdiff.Action{Kind: toolsdiff.ActChoose, Name: "demo", Copies: []toolsdiff.Copy{newer, older}}}}
		m.tl.row = 0
		m = enterToolsPlan(m)
		golden(t, fmt.Sprintf("tools_compare_%dx%d", size[0], size[1]), m.testView())
		m = pressKey(m, "a")
		golden(t, fmt.Sprintf("tools_compare_plan_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestToolsCompareFetchGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, src := toolsAtScreen(size[0], size[1])
		withDetails := toolsdiff.Copy{Name: "demo", Item: "new", NodeID: toolNodeM, Machine: "Nicolas-MBP", Location: "/Users/n/.claude/skills/demo", Hash: "h1",
			Manifest: &protocol.SkillManifest{Newest: t0.Add(-24 * time.Hour), Files: []protocol.SkillFile{{Path: "SKILL.md", Size: 20, Fingerprint: "a"}}}}
		missing := toolsdiff.Copy{Name: "demo", Item: "old", NodeID: toolNodeR, Machine: "research", Location: "/home/n/.claude/skills/demo", Hash: "h2", Seen: t0.Add(-48 * time.Hour)}
		src.detailManifest = &protocol.SkillManifest{Newest: t0.Add(-72 * time.Hour), Files: []protocol.SkillFile{{Path: "SKILL.md", Size: 10, Fingerprint: "b"}, {Path: "notes.md", Size: 5, Fingerprint: "c"}}}
		model, cmd := m.openChoose("Compare, then keep one", toolsdiff.Action{Kind: toolsdiff.ActChoose, Name: "demo", Copies: []toolsdiff.Copy{withDetails, missing}})
		m = model.(Model)
		golden(t, fmt.Sprintf("tools_compare_fetching_%dx%d", size[0], size[1]), m.testView())
		m = send(m, cmd())
		golden(t, fmt.Sprintf("tools_compare_fetched_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestDetailedDifferencesAreNotGrouped(t *testing.T) {
	m, _ := toolsAtScreen(120, 34)
	d := m.tl.cat.Diffs[0]
	d.Title = "first: Claude is newer than Pi, 2 files differ"
	d.Name = "first"
	e := d
	e.Name = "second"
	e.Key = "two-copies:second:research"
	e.Title = "second: Pi is newer than Claude, 3 files differ"
	m.tl.cat.Diffs = []toolsdiff.Difference{d, e}
	if ds := m.shownDiffs(); len(ds) != 2 || ds[0].Name == "" || ds[1].Name == "" {
		t.Fatalf("differences must stay one per tool: %+v", ds)
	}
	if v := plain(m); strings.Contains(v, "skills have") {
		t.Fatalf("a category row groups several tools:\n%s", v)
	}
}

func TestToolsPlanCanScrollLongComparison(t *testing.T) {
	m, _ := toolsAtScreen(80, 24)
	md := m.openModal("Comparison")
	for i := 0; i < 50; i++ {
		md.lines = append(md.lines, fmt.Sprintf("changed: file%02d", i))
	}
	md.foot = []string{"esc", "close"}
	if strings.Contains(plain(m), "changed: file49") {
		t.Fatal("last entry already visible")
	}
	for i := 0; i < 50; i++ {
		m = pressKey(m, "down")
	}
	if !strings.Contains(plain(m), "changed: file49") {
		t.Fatalf("last entry missing:\n%s", plain(m))
	}
}

func TestChooseDoesNotRecommendByFileDate(t *testing.T) {
	a := toolsdiff.Copy{Machine: "alpha", Manifest: &protocol.SkillManifest{Newest: time.Now().Add(-time.Hour), Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "a"}}}}
	b := toolsdiff.Copy{Machine: "beta", Manifest: &protocol.SkillManifest{Newest: time.Now(), Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "b"}}}}
	got := strings.Join(chooseComparison(a, b), "\n")
	if strings.Contains(got, "suggest: keep") || !strings.Contains(got, "no clear winner") {
		t.Fatalf("file dates alone cannot establish a winner: %s", got)
	}
}

func TestChooseShowsDatesAndUseWithoutARecommendation(t *testing.T) {
	m, _ := toolsAtScreen(80, 24)
	old := toolsdiff.Copy{Machine: "alpha", Harness: "pi", Manifest: &protocol.SkillManifest{Newest: m.now().Add(-72 * time.Hour), Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "old"}}}}
	newer := toolsdiff.Copy{Machine: "beta", Harness: "claude", Manifest: &protocol.SkillManifest{Newest: m.now(), Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "new"}}}}
	model, _ := m.openChoose("Compare", toolsdiff.Action{Name: "demo", Copies: []toolsdiff.Copy{old, newer}})
	v := plain(model.(Model))
	for _, want := range []string{"demo: 2 different copies on alpha and beta", "pi's copy", "claude's copy", "changed", "used by", "pi only", "claude only", "files", "backed up for 30 days", "a keep a", "b keep b"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q: %s", want, v)
		}
	}
	for _, bad := range []string{"keep alpha", "keep beta", "no clear winner", "suggest"} {
		if strings.Contains(v, bad) {
			t.Errorf("the box recommends %q: %s", bad, v)
		}
	}
}

func TestChooseExplainsMissingFilesWithoutRecommendation(t *testing.T) {
	a := toolsdiff.Copy{Machine: "alpha", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "a"}}}}
	b := toolsdiff.Copy{Machine: "beta", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "b"}}}}
	got := strings.Join(chooseComparison(a, b), "\n")
	for _, want := range []string{"a only on alpha", "b only on beta", "no clear winner"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
}

func TestChooseCopyHeadingsAndMissingManifest(t *testing.T) {
	m, _ := toolsAtScreen(120, 34)
	a := toolsdiff.Copy{Machine: "host", Harness: "pi"}
	b := toolsdiff.Copy{Machine: "host", Harness: "claude"}
	model, _ := m.openChoose("Compare", toolsdiff.Action{Copies: []toolsdiff.Copy{a, b}})
	v := plain(model.(Model))
	for _, want := range []string{"pi's copy", "claude's copy", "a keep a", "b keep b", "no file details from host"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q: %s", want, v)
		}
	}
	if strings.Contains(v, "these machines") {
		t.Fatalf("old wording still there: %s", v)
	}
}

func TestChooseFetchesMissingDetails(t *testing.T) {
	m, src := toolsAtScreen(120, 34)
	seen := t0.Add(-48 * time.Hour)
	withDetails := toolsdiff.Copy{Name: "demo", Item: "new", NodeID: toolNodeM, Machine: "Nicolas-MBP", Hash: "h1",
		Manifest: &protocol.SkillManifest{Newest: t0.Add(-24 * time.Hour), Files: []protocol.SkillFile{{Path: "SKILL.md", Size: 10, Fingerprint: "a"}}}}
	missing := toolsdiff.Copy{Name: "demo", Item: "old", NodeID: toolNodeR, Machine: "research", Location: "/home/n/.claude/skills/demo", Hash: "h2", Seen: seen}
	src.detailManifest = &protocol.SkillManifest{Newest: t0.Add(-72 * time.Hour), Files: []protocol.SkillFile{{Path: "SKILL.md", Size: 20, Fingerprint: "b"}, {Path: "notes.md", Size: 5, Fingerprint: "c"}}}
	model, cmd := m.openChoose("Compare, then keep one", toolsdiff.Action{Kind: toolsdiff.ActChoose, Name: "demo", Copies: []toolsdiff.Copy{withDetails, missing}})
	m = model.(Model)
	if !strings.Contains(plain(m), "fetching details from research...") {
		t.Fatalf("no fetching line:\n%s", plain(m))
	}
	if cmd == nil {
		t.Fatal("no fetch command")
	}
	m = send(m, cmd())
	v := plain(m)
	if strings.Contains(v, "fetching details") {
		t.Fatalf("fetching line stayed:\n%s", v)
	}
	for _, want := range []string{"28 Oct", "26 Oct", "one harness setup", "SKILL.md", "notes.md"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	if len(src.details) != 1 || src.details[0].NodeID != toolNodeR || src.details[0].Location != missing.Location || src.details[0].Hash != "h2" {
		t.Fatalf("request: %+v", src.details)
	}
}

func TestChooseFetchFailureNamesTheMachineAndReason(t *testing.T) {
	m, src := toolsAtScreen(120, 34)
	a := toolsdiff.Copy{Machine: "Nicolas-MBP", NodeID: toolNodeM, Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md"}}}}
	b := toolsdiff.Copy{Name: "demo", Machine: "research", NodeID: toolNodeR, Location: "/home/n/.claude/skills/demo", Hash: "h2", Seen: t0.Add(-48 * time.Hour)}
	src.detailErr = errors.New("skill changed since its scan")
	model, cmd := m.openChoose("Compare", toolsdiff.Action{Name: "demo", Copies: []toolsdiff.Copy{a, b}})
	m = model.(Model)
	m = send(m, cmd())
	v := plain(m)
	for _, want := range []string{"research: no details (skill changed since its scan)", "version", "last seen 2 days ago"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
}

func TestChooseOfflineSideSaysSo(t *testing.T) {
	m, src := toolsAtScreen(120, 34)
	for i := range m.tl.cat.Machines {
		if m.tl.cat.Machines[i].ID == toolNodeR {
			m.tl.cat.Machines[i].Online = false
		}
	}
	a := toolsdiff.Copy{Machine: "Nicolas-MBP", NodeID: toolNodeM, Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md"}}}}
	b := toolsdiff.Copy{Name: "demo", Machine: "research", NodeID: toolNodeR, Location: "/home/n/.claude/skills/demo", Hash: "h2", Seen: t0.Add(-48 * time.Hour)}
	model, cmd := m.openChoose("Compare", toolsdiff.Action{Name: "demo", Copies: []toolsdiff.Copy{a, b}})
	m = model.(Model)
	if cmd != nil {
		t.Fatal("an offline machine must not be asked")
	}
	v := plain(m)
	for _, want := range []string{"research is offline: no details", "seen 2 days ago"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	if len(src.details) != 0 {
		t.Fatalf("asked an offline machine: %+v", src.details)
	}
}

func TestChooseViewsTheFullDiff(t *testing.T) {
	m, _ := toolsAtScreen(80, 24)
	a := toolsdiff.Copy{Item: "old", Machine: "alpha", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "a"}, {Path: "other.md", Fingerprint: "a"}}}}
	b := toolsdiff.Copy{Item: "new", Machine: "beta", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "b"}, {Path: "other.md", Fingerprint: "b"}}}}
	model, _ := m.openChoose("Compare", toolsdiff.Action{Copies: []toolsdiff.Copy{a, b}})
	m = model.(Model)
	m = pressKey(m, "v")
	for _, want := range []string{"SKILL.md", "---", "+++", "-old text", "+new text"} {
		if !strings.Contains(plain(m), want) {
			t.Errorf("missing %q: %s", want, plain(m))
		}
	}
	m = pressKey(m, "down")
	if m.tl.modal.scroll != 1 {
		t.Fatal("diff does not scroll")
	}
	m = pressKey(m, "esc")
	if !strings.Contains(plain(m), "a keep a") {
		t.Fatal(plain(m))
	}
}

func TestChooseWrapsWordsAt80Columns(t *testing.T) {
	m, _ := toolsAtScreen(80, 24)
	a := toolsdiff.Copy{Machine: "alpha", Manifest: &protocol.SkillManifest{Newest: m.now().Add(-72 * time.Hour), Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "old"}}}}
	b := toolsdiff.Copy{Machine: "beta", Manifest: &protocol.SkillManifest{Newest: m.now().Add(-24 * time.Hour), Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "new"}, {Path: "scripts/run.sh", Fingerprint: "new"}}}}
	model, _ := m.openChoose("Compare", toolsdiff.Action{Copies: []toolsdiff.Copy{a, b}})
	v := plain(model.(Model))
	for _, word := range []string{"alpha", "beta", "scripts/run.sh", "backed up for 30 days", "a keep a"} {
		if !strings.Contains(v, word) {
			t.Errorf("word %q split or missing: %s", word, v)
		}
	}
	for _, line := range strings.Split(v, "\n") {
		if ansi.StringWidth(line) > 80 {
			t.Errorf("line exceeds 80: %q", line)
		}
	}
}

func TestLargeSkillDiffUsesBoundedWork(t *testing.T) {
	before := strings.Repeat("old line\n", 6000)
	after := strings.Repeat("new line\n", 6000)
	diff := unifiedSkillDiff("alpha", "beta", before, after)
	if !strings.Contains(diff, "-old line") || !strings.Contains(diff, "+new line") {
		t.Fatal("missing changes")
	}
}

func TestChooseHintsRemainDistinctWithFourCopies(t *testing.T) {
	m, _ := toolsAtScreen(120, 34)
	copies := []toolsdiff.Copy{{Machine: "host", Harness: "pi"}, {Machine: "host", Harness: "claude"}, {Machine: "host", Harness: "pi"}, {Machine: "host", Harness: "claude"}}
	model, _ := m.openChoose("Compare", toolsdiff.Action{Copies: copies})
	m = model.(Model)
	for _, key := range []string{"a", "b", "c", "d"} {
		if _, ok := m.tl.modal.actions[key]; !ok {
			t.Fatalf("no keep key %q: %v", key, m.tl.modal.actions)
		}
	}
	if v := plain(m); !strings.Contains(v, "a keep a  b keep b  c keep c  d keep d") {
		t.Fatalf("the keep keys are not distinct:\n%s", v)
	}
}

func TestChooseReportsOversizedManifest(t *testing.T) {
	a := toolsdiff.Copy{Manifest: &protocol.SkillManifest{TooLarge: true}}
	b := toolsdiff.Copy{Manifest: &protocol.SkillManifest{}}
	if got := strings.Join(chooseComparison(a, b), " "); !strings.Contains(got, "too large") {
		t.Fatal(got)
	}
}

func TestCompareBoxAt80Columns(t *testing.T) {
	m, _ := toolsAtScreen(80, 24)
	m = toolFilter(m, "design-taste-frontend")
	m = pressKey(m, "enter")
	m = toolChoose(t, m, "Compare the copies / Keep this one")
	v := plain(m)
	for _, item := range []string{"design-taste-frontend: 2 different copies on research", "changed", "used by", "files", "a keep a", "b keep b", "v full diff", "esc back"} {
		if !strings.Contains(v, item) {
			t.Errorf("box lacks %q:\n%s", item, v)
		}
	}
	panel := strings.SplitN(strings.SplitN(v, "╭─design-taste-frontend:", 2)[1], "╰", 2)[0]
	for _, line := range strings.Split(panel, "\n") {
		if !strings.HasPrefix(line, "│ ") {
			continue
		}
		text := strings.TrimRight(strings.TrimPrefix(line, "│ "), " │")
		if strings.Contains(text, "…") && !strings.Contains(text, "…/") {
			t.Errorf("box cuts a word with an ellipsis: %q", line)
		}
		if text != "" && !strings.HasPrefix(text, "  ") && !strings.Contains(text, "keep a") {
			t.Errorf("line starts at the border: %q", line)
		}
	}
	for _, line := range strings.Split(v, "\n") {
		if ansi.StringWidth(line) > 80 {
			t.Errorf("line exceeds 80: %q", line)
		}
	}
}

func TestCompareBoxKeepsLongValuesInsideTheBox(t *testing.T) {
	m, _ := toolsAtScreen(80, 24)
	long := strings.Repeat("a", 50)
	a := toolsdiff.Copy{Machine: "alpha", Harness: "pi", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "old"}}}}
	b := toolsdiff.Copy{Machine: "beta-machine-with-a-long-name", Harness: "claude", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: long, Fingerprint: "new"}}}}
	model, _ := m.openChoose("Compare", toolsdiff.Action{Name: "demo", Copies: []toolsdiff.Copy{a, b}})
	v := plain(model.(Model))
	for _, line := range strings.Split(v, "\n") {
		if ansi.StringWidth(line) > 80 {
			t.Errorf("line exceeds 80: %q", line)
		}
	}
	panel := strings.SplitN(strings.SplitN(v, "╭─demo:", 2)[1], "╰", 2)[0]
	if strings.Contains(panel, "…") {
		t.Errorf("the box cuts a word with an ellipsis:\n%s", panel)
	}
}

func TestModalWrapUsesHangingIndent(t *testing.T) {
	for _, line := range wrapStyled("  this sentence has several words and must wrap underneath its first words", 28)[1:] {
		if !strings.HasPrefix(ansi.Strip(line), "  ") {
			t.Fatalf("missing hanging indent: %q", line)
		}
	}
}

func TestCompareBoxShowsBothCopiesAndTheKeys(t *testing.T) {
	m, _ := toolsAtScreen(80, 24)
	a := toolsdiff.Copy{Machine: "alpha", Harness: "pi", Label: "v2", Manifest: &protocol.SkillManifest{Newest: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "same"}, {Path: "extra", Fingerprint: "x"}}}}
	b := toolsdiff.Copy{Machine: "beta", Harness: "claude", Manifest: &protocol.SkillManifest{Newest: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC), Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "same"}}}}
	model, _ := m.openChoose("Keep the newer one in both places", toolsdiff.Action{Name: "demo", Copies: []toolsdiff.Copy{a, b}})
	m = model.(Model)
	v := plain(m)
	for _, want := range []string{"demo: 2 different copies on alpha and beta", "a  pi's copy", "b  claude's copy", "20 Oct", "22 Oct", "pi only", "claude only", "extra", "The copy you keep goes to every harness on every machine.", "a keep a  b keep b  v full diff"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q: %s", want, v)
		}
	}
	if strings.Contains(strings.ToLower(v), "keep newer") || strings.Contains(strings.ToLower(v), "keep the newer") {
		t.Fatal(v)
	}
	m = pressKey(m, "enter")
	if !strings.Contains(plain(m), "demo: 2 different copies") {
		t.Fatal("enter changed the compare box")
	}
}

type summaryTools struct {
	*fakeTools
	calls int
	fail  bool
}

func (s *summaryTools) ToolsSummary(_ context.Context, req protocol.SkillSummaryRequest) (string, error) {
	s.calls++
	if s.fail {
		return "", errors.New("worker failed")
	}
	if req.A != "old text\n" || req.B != "new text\n" {
		return "", errors.New("wrong text")
	}
	return "a runs tests first; b uses the old install command", nil
}
func TestCompareSummaryAndCache(t *testing.T) {
	m, base := toolsAtScreen(120, 34)
	src := &summaryTools{fakeTools: base}
	m.src = src
	a := toolsdiff.Copy{Name: "demo", Item: "old", Hash: "a", Machine: "alpha"}
	b := toolsdiff.Copy{Name: "demo", Item: "new", Hash: "b", Machine: "beta"}
	act := toolsdiff.Action{Copies: []toolsdiff.Copy{a, b}}
	model, cmd := m.openChoose("Compare", act)
	m = model.(Model)
	if !strings.Contains(plain(m), "summarising the difference") {
		t.Fatal(plain(m))
	}
	m = send(m, cmd())
	if !strings.Contains(plain(m), "in short") || !strings.Contains(plain(m), "runs tests first") || !strings.Contains(plain(m), "uses the old install command") {
		t.Fatal(plain(m))
	}
	model, cmd = m.openChoose("Compare", act)
	m = model.(Model)
	if cmd != nil || src.calls != 1 || !strings.Contains(plain(m), "runs tests first") {
		t.Fatalf("cache calls %d: %s", src.calls, plain(m))
	}
}
func TestCompareSummaryFailureFallsBack(t *testing.T) {
	m, base := toolsAtScreen(120, 34)
	m.src = &summaryTools{fakeTools: base, fail: true}
	a := toolsdiff.Copy{Name: "demo", Item: "old", Hash: "a", Machine: "alpha"}
	b := toolsdiff.Copy{Name: "demo", Item: "new", Hash: "b", Machine: "beta"}
	model, cmd := m.openChoose("Compare", toolsdiff.Action{Copies: []toolsdiff.Copy{a, b}})
	m = model.(Model)
	m = send(m, cmd())
	if strings.Contains(plain(m), "in short") || strings.Contains(plain(m), "summary unavailable") {
		t.Fatalf("a missing summary must leave the row out:\n%s", plain(m))
	}
}

func TestCompareRecommendationRequiresMatchingFingerprints(t *testing.T) {
	a := toolsdiff.Copy{Machine: "alpha", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md"}, {Path: "extra", Fingerprint: "x"}}}}
	b := toolsdiff.Copy{Machine: "beta", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md"}}}}
	if got := strings.Join(chooseComparison(a, b), " "); !strings.Contains(got, "no clear winner") {
		t.Fatal(got)
	}
}

func TestCompareSummaryGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, base := toolsAtScreen(size[0], size[1])
		m.src = &summaryTools{fakeTools: base}
		a := toolsdiff.Copy{Name: "demo", Item: "old", Hash: "a", Machine: "alpha", Harness: "pi", Label: "v2", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "old"}}}}
		b := toolsdiff.Copy{Name: "demo", Item: "new", Hash: "b", Machine: "beta", Harness: "claude", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "new"}}}}
		model, cmd := m.openChoose("Compare", toolsdiff.Action{Copies: []toolsdiff.Copy{a, b}})
		m = send(model.(Model), cmd())
		golden(t, fmt.Sprintf("tools_summary_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestToolsModalDoesNotLeavePanelRemnants(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, _ := toolsAtScreen(size[0], size[1])
		m.tl.modal = &toolsModal{title: "Compare", lines: []string{"A comparison"}, foot: []string{"esc", "cancel"}}
		view := lines(plain(m))
		inModal, closed := false, false
		for _, line := range view {
			if strings.HasPrefix(line, "╭─Compare") {
				inModal = true
				continue
			}
			if strings.HasPrefix(line, "╭─CTO") {
				break
			}
			if inModal && strings.HasPrefix(line, "╰") {
				if closed {
					t.Fatalf("%dx%d: stray panel border after modal:\n%s", size[0], size[1], plain(m))
				}
				closed = true
			} else if closed && strings.TrimSpace(line) != "" {
				t.Fatalf("%dx%d: stray panel content after modal:\n%s", size[0], size[1], plain(m))
			}
		}
		if !closed {
			t.Fatalf("%dx%d: modal not found or not closed", size[0], size[1])
		}
	}
}
