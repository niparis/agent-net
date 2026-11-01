package tui

import (
	"context"
	"errors"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

func plain(m Model) string { return ansi.Strip(m.View()) }

func toolsAtScreen(w, h, screen int) (Model, *fakeTools) {
	m, src := newToolsModel(w, h)
	m = openTools(m)
	m.tl.screen = screen
	return m, src
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

func TestToolsAcceptFailureRevertsTheScreen(t *testing.T) {
	m, src := toolsAtScreen(120, 34, 0)
	src.acceptErr = fmt.Errorf("gateway said no")
	n := len(m.shownDiffs())
	m = resolveTools(m.toolsAccept())
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
	m = enterToolsPlan(m)
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
	m = enterToolsPlan(m)
	m = enterToolsPlan(m)
	if len(src.applied) != 1 || src.applied[0] != "plan_2" {
		t.Fatalf("applied %v plans %+v", src.applied, src.plans)
	}
	if v := plain(m); !strings.Contains(v, "pending") || !strings.Contains(v, "machine offline") {
		t.Errorf("the result must report the pending target:\n%s", v)
	}
}

func TestToolsTurnOffOnAMachineCellPlansAnUninstall(t *testing.T) {
	m, src := toolsAtScreen(120, 34, 1)
	m = rowByNameFrom(m, "research", "agent-net")
	m.tl.col++ // the second machine column is research
	m = resolveTools(m.openRowPlan(false))
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
	m = resolveTools(m.openRowPlan(true))
	if m.tl.modal == nil || len(src.imports) != 0 || len(src.plans) != 0 {
		t.Fatalf("the plan must come first: imports %v plans %v", src.imports, src.plans)
	}
	if v := plain(m); !strings.Contains(v, "agent-net store") {
		t.Errorf("the plan must say the skill goes into the store:\n%s", v)
	}
	m = enterToolsPlan(m)
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
	m = resolveTools(m.openRowPlan(false))
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
	m = resolveTools(m.openRowPlan(false))
	m = pressKey(m, "b")
	if len(src.sent) != 1 || !strings.HasPrefix(src.sent[0], "s_sovl: ") || !strings.Contains(src.sent[0], "deploy-test-server") || !strings.Contains(src.sent[0], "Do not commit to the default branch") {
		t.Fatalf("sent %v", src.sent)
	}
	if len(src.plans) != 2 || src.plans[1].Operation != "repo_remove" || len(src.applied) != 1 {
		t.Errorf("repo removal should use the repo operation without changing the assignment: plans=%+v applied=%v", src.plans, src.applied)
	}
}

func TestToolsProjectPickerFilterScalesAndPlansTheTickedProjects(t *testing.T) {
	m, src := newToolsModel(120, 34)
	for i := 0; i < 60; i++ {
		src.snap.Projects = append(src.snap.Projects, protocol.ProjectView{Name: fmt.Sprintf("proj-%02d", i), NodeID: toolNodeR, Source: protocol.SourceRegistered})
	}
	m = refresh(m, src)
	m = openTools(m)
	m.toolsSetScreen(m.tl.screen + 1)
	m = rowByName(m, "found-skill-10")
	m.tl.col++
	m.tl.col++
	m = resolveTools(m.openRowPlan(true))
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
	m = enterToolsPlan(m)
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
	m = enterToolsPlan(m)
	if m.tl.modal == nil || m.tl.modal.picker != nil {
		t.Fatalf("enter on the picker must open the plan")
	}
	if v := plain(m); !strings.Contains(v, "for proj-40, proj-41") {
		t.Errorf("plan must name the projects:\n%s", v)
	}
	m = enterToolsPlan(m)
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
	m = resolveTools(m.toolsRescan())
	if src.rescans != 1 || !m.tl.loaded {
		t.Fatalf("rescans %d", src.rescans)
	}
}

func TestToolsComparisonPlanShowsFiles(t *testing.T) {
	m, _ := toolsAtScreen(120, 34, 0)
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
		m, _ := toolsAtScreen(size[0], size[1], 0)
		newer := toolsdiff.Copy{Name: "demo", Item: "new", NodeID: toolNodeR, Machine: "research", Location: "/home/test/.claude/skills/demo", Hash: "new", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "new"}, {Path: "added.txt", Fingerprint: "add"}}}}
		older := toolsdiff.Copy{Name: "demo", Item: "old", NodeID: toolNodeR, Machine: "research", Location: "/home/test/.pi/agent/skills/demo", Hash: "old", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "old"}, {Path: "removed.txt", Fingerprint: "gone"}}}}
		m.tl.cat.Diffs = []toolsdiff.Difference{{Key: "two-copies:demo:research", Kind: toolsdiff.KindTwoCopies, Severity: toolsdiff.SevErr, Name: "demo", Title: "demo has 2 different copies on research, 3 files differ", Fix: "compare, then keep one", Action: toolsdiff.Action{Kind: toolsdiff.ActChoose, Name: "demo", Copies: []toolsdiff.Copy{newer, older}}}}
		m.tl.row = 0
		golden(t, fmt.Sprintf("tools_compare_%dx%d", size[0], size[1]), m.View())
		m = enterToolsPlan(m)
		m = pressKey(m, "a")
		golden(t, fmt.Sprintf("tools_compare_plan_%dx%d", size[0], size[1]), m.View())
	}
}

func TestToolsCompareFetchGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, src := toolsAtScreen(size[0], size[1], 0)
		withDetails := toolsdiff.Copy{Name: "demo", Item: "new", NodeID: toolNodeM, Machine: "Nicolas-MBP", Location: "/Users/n/.claude/skills/demo", Hash: "h1",
			Manifest: &protocol.SkillManifest{Newest: t0.Add(-24 * time.Hour), Files: []protocol.SkillFile{{Path: "SKILL.md", Size: 20, Fingerprint: "a"}}}}
		missing := toolsdiff.Copy{Name: "demo", Item: "old", NodeID: toolNodeR, Machine: "research", Location: "/home/n/.claude/skills/demo", Hash: "h2", Seen: t0.Add(-48 * time.Hour)}
		src.detailManifest = &protocol.SkillManifest{Newest: t0.Add(-72 * time.Hour), Files: []protocol.SkillFile{{Path: "SKILL.md", Size: 10, Fingerprint: "b"}, {Path: "notes.md", Size: 5, Fingerprint: "c"}}}
		model, cmd := m.openChoose("Compare, then keep one", toolsdiff.Action{Kind: toolsdiff.ActChoose, Name: "demo", Copies: []toolsdiff.Copy{withDetails, missing}})
		m = model.(Model)
		golden(t, fmt.Sprintf("tools_compare_fetching_%dx%d", size[0], size[1]), m.View())
		m = send(m, cmd())
		golden(t, fmt.Sprintf("tools_compare_fetched_%dx%d", size[0], size[1]), m.View())
	}
}

func TestDetailedDifferencesStillGroupByMachine(t *testing.T) {
	m, _ := toolsAtScreen(120, 34, 0)
	d := m.tl.cat.Diffs[0]
	d.Title = "first: Claude is newer than Pi, 2 files differ"
	d.Name = "first"
	e := d
	e.Name = "second"
	e.Key = "two-copies:second:research"
	e.Title = "second: Pi is newer than Claude, 3 files differ"
	m.tl.cat.Diffs = []toolsdiff.Difference{d, e}
	if ds := m.shownDiffs(); len(ds) != 1 || !strings.Contains(ds[0].Title, "2 skills have 2 different copies on research") {
		t.Fatalf("not grouped: %+v", ds)
	}
}

func TestToolsPlanCanScrollLongComparison(t *testing.T) {
	m, _ := toolsAtScreen(80, 24, 0)
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

func TestChooseShowsManifestDecision(t *testing.T) {
	m, _ := toolsAtScreen(80, 24, 0)
	old := toolsdiff.Copy{Machine: "alpha", Manifest: &protocol.SkillManifest{Newest: m.now().Add(-72 * time.Hour), Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "old"}}}}
	newer := toolsdiff.Copy{Machine: "beta", Manifest: &protocol.SkillManifest{Newest: m.now(), Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "new"}}}}
	model, _ := m.openChoose("Compare", toolsdiff.Action{Copies: []toolsdiff.Copy{old, newer}})
	v := plain(model.(Model))
	for _, want := range []string{"file modified", "origin unknown", "used by alpha", "no clear winner"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q: %s", want, v)
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

func TestChooseCopyHintsAndMissingManifest(t *testing.T) {
	m, _ := toolsAtScreen(120, 34, 0)
	a := toolsdiff.Copy{Machine: "host", Harness: "pi"}
	b := toolsdiff.Copy{Machine: "host", Harness: "claude"}
	model, _ := m.openChoose("Compare", toolsdiff.Action{Copies: []toolsdiff.Copy{a, b}})
	v := plain(model.(Model))
	for _, want := range []string{"a keep pi's copy", "b keep claude's copy", "no file details from host yet: its scan has no file list"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q: %s", want, v)
		}
	}
	if strings.Contains(v, "these machines") {
		t.Fatalf("old wording still there: %s", v)
	}
}

func TestChooseFetchesMissingDetails(t *testing.T) {
	m, src := toolsAtScreen(120, 34, 0)
	seen := t0.Add(-48 * time.Hour)
	withDetails := toolsdiff.Copy{Name: "demo", Item: "new", NodeID: toolNodeM, Machine: "Nicolas-MBP", Location: "/Users/n/.claude/skills/demo", Hash: "h1",
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
	for _, want := range []string{"file modified", "2 files differ", "notes.md only on research", "SKILL.md changed", "used by research"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	if len(src.details) != 1 || src.details[0].NodeID != toolNodeR || src.details[0].Location != missing.Location || src.details[0].Hash != "h2" {
		t.Fatalf("request: %+v", src.details)
	}
}

func TestChooseFetchFailureNamesTheMachineAndReason(t *testing.T) {
	m, src := toolsAtScreen(120, 34, 0)
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
	m, src := toolsAtScreen(120, 34, 0)
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
	for _, want := range []string{"research is offline: no details", "version", "last seen 2 days ago"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	if len(src.details) != 0 {
		t.Fatalf("asked an offline machine: %+v", src.details)
	}
}

func TestChooseViewsSelectedFileDiff(t *testing.T) {
	m, _ := toolsAtScreen(80, 24, 0)
	a := toolsdiff.Copy{Item: "old", Machine: "alpha", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "a"}, {Path: "other.md", Fingerprint: "a"}}}}
	b := toolsdiff.Copy{Item: "new", Machine: "beta", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "b"}, {Path: "other.md", Fingerprint: "b"}}}}
	model, _ := m.openChoose("Compare", toolsdiff.Action{Copies: []toolsdiff.Copy{a, b}})
	m = model.(Model)
	m = pressKey(m, "n")
	if !strings.Contains(plain(m), "other.md") {
		t.Fatal(plain(m))
	}
	m = pressKey(m, "v")
	for _, want := range []string{"other.md", "---", "+++", "-old text", "+new text"} {
		if !strings.Contains(plain(m), want) {
			t.Errorf("missing %q: %s", want, plain(m))
		}
	}
	m = pressKey(m, "down")
	if m.tl.modal.scroll != 1 {
		t.Fatal("diff does not scroll")
	}
	m = pressKey(m, "esc")
	if !strings.Contains(plain(m), "keep the copy") {
		t.Fatal(plain(m))
	}
}

func TestChooseWrapsWordsAt80Columns(t *testing.T) {
	m, _ := toolsAtScreen(80, 24, 0)
	a := toolsdiff.Copy{Machine: "alpha", Manifest: &protocol.SkillManifest{Newest: m.now().Add(-72 * time.Hour), Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "old"}}}}
	b := toolsdiff.Copy{Machine: "beta", Manifest: &protocol.SkillManifest{Newest: m.now().Add(-24 * time.Hour), Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "new"}, {Path: "scripts/run.sh", Fingerprint: "new"}}}}
	model, _ := m.openChoose("Compare", toolsdiff.Action{Copies: []toolsdiff.Copy{a, b}})
	v := plain(model.(Model))
	for _, word := range []string{"alpha", "beta", "no clear winner", "file"} {
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
	m, _ := toolsAtScreen(120, 34, 0)
	copies := []toolsdiff.Copy{{Machine: "host", Harness: "pi"}, {Machine: "host", Harness: "claude"}, {Machine: "host", Harness: "pi"}, {Machine: "host", Harness: "claude"}}
	model, _ := m.openChoose("Compare", toolsdiff.Action{Copies: copies})
	v := plain(model.(Model))
	if strings.Count(v, "keep claude's copy") > 1 {
		t.Fatal(v)
	}
}

func TestChooseReportsOversizedManifest(t *testing.T) {
	a := toolsdiff.Copy{Manifest: &protocol.SkillManifest{TooLarge: true}}
	b := toolsdiff.Copy{Manifest: &protocol.SkillManifest{}}
	if got := strings.Join(chooseComparison(a, b), " "); !strings.Contains(got, "too large") {
		t.Fatal(got)
	}
}

func TestToolsPlanWrapsItemsAndHintsAt80Columns(t *testing.T) {
	m, _ := toolsAtScreen(80, 24, 0)
	m = enterToolsPlan(m)
	v := plain(m)
	for _, item := range []string{"28 Oct", "25 Oct", "v view diff", "esc cancel", "no clear winner"} {
		if !strings.Contains(v, item) {
			t.Errorf("item split or missing %q:\n%s", item, v)
		}
	}
	panel := strings.SplitN(strings.SplitN(v, "╭─Compare, then keep one", 2)[1], "╰", 2)[0]
	for _, line := range strings.Split(panel, "\n") {
		if !strings.HasPrefix(line, "│ ") {
			continue
		}
		text := strings.TrimRight(strings.TrimPrefix(line, "│ "), " │")
		if text == "" || strings.HasPrefix(text, "a  ") || strings.HasPrefix(text, "b  ") || strings.HasPrefix(text, "enter ") || strings.HasPrefix(text, "a keep ") {
			continue
		}
		if !strings.HasPrefix(text, "  ") {
			t.Errorf("line starts at border: %q", line)
		}
	}
	for _, bad := range []string{"│ 28 Oct", "│ Oct ", "│ claude ", "│ view diff "} {
		if strings.Contains(v, bad) {
			t.Errorf("continuation at border %q:\n%s", bad, v)
		}
	}
}

func TestLongDifferenceKeepsLocationPhraseTogether(t *testing.T) {
	m, _ := toolsAtScreen(80, 24, 0)
	a := toolsdiff.Copy{Machine: "alpha", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{}}}
	b := toolsdiff.Copy{Machine: "beta-machine-with-a-long-name", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: strings.Repeat("a", 50), Fingerprint: "new"}}}}
	model, _ := m.openChoose("Compare", toolsdiff.Action{Copies: []toolsdiff.Copy{a, b}})
	m = model.(Model)
	for i := 0; i < 8; i++ {
		m = pressKey(m, "down")
	}
	v := plain(m)
	if !strings.Contains(v, "only on beta-machine-with-a-long-name") {
		t.Fatalf("location phrase split:\n%s", v)
	}
}

func TestModalWrapUsesHangingIndent(t *testing.T) {
	for _, line := range wrapStyled("  this sentence has several words and must wrap underneath its first words", 28)[1:] {
		if !strings.HasPrefix(ansi.Strip(line), "  ") {
			t.Fatalf("missing hanging indent: %q", line)
		}
	}
}

func TestCompareDecisionEvidenceAndKeys(t *testing.T) {
	m, _ := toolsAtScreen(80, 24, 0)
	a := toolsdiff.Copy{Machine: "alpha", Harness: "pi", Label: "v2", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "same"}, {Path: "extra", Fingerprint: "x"}}}}
	b := toolsdiff.Copy{Machine: "beta", Harness: "claude", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "same"}}}}
	model, _ := m.openChoose("Keep the newer one in both places", toolsdiff.Action{Name: "demo", Copies: []toolsdiff.Copy{a, b}})
	m = model.(Model)
	v := plain(m)
	for _, want := range []string{"Compare, then keep one", "pi", "claude", "agent-net store", "unknown", "summary", "keep alpha", "every file"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q: %s", want, v)
		}
	}
	if strings.Contains(strings.ToLower(v), "keep newer") || strings.Contains(strings.ToLower(v), "keep the newer") {
		t.Fatal(v)
	}
	m = enterToolsPlan(m)
	if !strings.Contains(plain(m), "Compare, then keep one") {
		t.Fatal("enter changed screen")
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
	m, base := toolsAtScreen(120, 34, 0)
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
	if !strings.Contains(plain(m), "summary by model") || !strings.Contains(plain(m), "runs tests first") {
		t.Fatal(plain(m))
	}
	model, cmd = m.openChoose("Compare", act)
	m = model.(Model)
	if cmd != nil || src.calls != 1 || !strings.Contains(plain(m), "runs tests first") {
		t.Fatalf("cache calls %d: %s", src.calls, plain(m))
	}
}
func TestCompareSummaryFailureFallsBack(t *testing.T) {
	m, base := toolsAtScreen(120, 34, 0)
	m.src = &summaryTools{fakeTools: base, fail: true}
	a := toolsdiff.Copy{Name: "demo", Item: "old", Hash: "a", Machine: "alpha"}
	b := toolsdiff.Copy{Name: "demo", Item: "new", Hash: "b", Machine: "beta"}
	model, cmd := m.openChoose("Compare", toolsdiff.Action{Copies: []toolsdiff.Copy{a, b}})
	m = model.(Model)
	m = send(m, cmd())
	if !strings.Contains(plain(m), "summary unavailable") {
		t.Fatal(plain(m))
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
		m, base := toolsAtScreen(size[0], size[1], 0)
		m.src = &summaryTools{fakeTools: base}
		a := toolsdiff.Copy{Name: "demo", Item: "old", Hash: "a", Machine: "alpha", Harness: "pi", Label: "v2", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "old"}}}}
		b := toolsdiff.Copy{Name: "demo", Item: "new", Hash: "b", Machine: "beta", Harness: "claude", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "new"}}}}
		model, cmd := m.openChoose("Compare", toolsdiff.Action{Copies: []toolsdiff.Copy{a, b}})
		m = send(model.(Model), cmd())
		golden(t, fmt.Sprintf("tools_summary_%dx%d", size[0], size[1]), m.View())
	}
}
