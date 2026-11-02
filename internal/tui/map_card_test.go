package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// richGraph has a node with all nine charter fields, a long purpose, a
// wildcard edge, a deploys edge and a changed rule.
func richGraph() protocol.GraphView {
	g := fakeGraph()
	for i := range g.Nodes {
		if g.Nodes[i].Name == "tenebrae" {
			g.Nodes[i].Charter = protocol.Charter{
				Purpose:         "Research platform that turns papers, figures and strategies into a searchable library for the trading desk, with citations kept for every claim.",
				Maturity:        "mature - stable since spring",
				Accepts:         "New document sources, retrieval fixes, and schema changes that keep old ids working.",
				Refuses:         "Anything that writes to the trading accounts; one-off client reports.",
				FeatureRequests: "Open an issue in the repo; the architect triages them every Monday.",
				Consumers:       "cogforge, shop, the nightly report job",
				Escalate:        "Data loss, or a change of the public ids.",
				Needs:           "MCP: tenebrae-library; a GPU box for embeddings.",
				Notes:           "Deploys go through homelab. Keep the library read only on laptops.",
			}
			g.Nodes[i].Settings = map[string]string{
				protocol.SettingAutonomy:   protocol.AutonomyWake,
				protocol.SettingDeployRule: protocol.DeployOwner,
			}
		}
	}
	g.Edges = append(g.Edges,
		protocol.Edge{From: "*", To: "tenebrae", Kind: "depends", Rule: "normal"},
		protocol.Edge{From: "homelab", To: "tenebrae", Kind: "deploys", Rule: "auto"},
		protocol.Edge{From: "tenebrae", To: "homelab", Kind: "depends", Rule: "ask"},
	)
	return g
}

func TestMapCardShowsWildcardAsAllProjectsAndRuleOnlyWhenNotNormal(t *testing.T) {
	m := openMap(t, mapModel(120, 34, richGraph()))
	m.mp.selected = "tenebrae"
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "all projects ──▶ depends") {
		t.Fatal(v)
	}
	if strings.Contains(v, "normal") || strings.Contains(v, " * ") {
		t.Fatalf("rule normal or the raw wildcard is shown:\n%s", v)
	}
	if !strings.Contains(v, "homelab ──▶ deploys auto") || !strings.Contains(v, "──▶ homelab depends ask") {
		t.Fatal(v)
	}
}

func TestMapCardGovernedByAppearsOnlyWithAnArchitect(t *testing.T) {
	m := openMap(t, mapModel(120, 34, richGraph()))
	m.mp.selected = "tenebrae"
	if v := ansi.Strip(m.View()); !strings.Contains(v, "governed by: wbg-architect (advisor)") {
		t.Fatal(v)
	}
	m.mp.selected = "homelab"
	v := ansi.Strip(m.View())
	if strings.Contains(v, "governed by") || strings.Contains(strings.ToLower(v), "add architect") {
		t.Fatal(v)
	}
}

func TestMapCardCharterShowsBlockFieldsWrappedAndNeverCut(t *testing.T) {
	m := openMap(t, mapModel(120, 40, richGraph()))
	m.mp.selected = "tenebrae"
	v := ansi.Strip(m.View())
	for _, want := range []string{"accepts", "refuses", "feature_requests", "consumers", "escalate", "needs", "notes", "New document sources", "one-off client reports.", "triages them every Monday.", "nightly report job", "public ids.", "tenebrae-library", "read only on laptops."} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
}

func TestMapCardNeverWiderThanTheScreenAndKeepsRecentLast(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}, {60, 20}, {100, 16}} {
		m := openMap(t, mapModel(size[0], size[1], richGraph()))
		for _, name := range []string{"tenebrae", "cogforge", "homelab", "trading-architect"} {
			m.mp.selected = name
			out := m.mapFocusView(size[0], size[1]-6)
			if len(out) != size[1]-6 {
				t.Fatalf("%v %s: %d rows, want %d", size, name, len(out), size[1]-6)
			}
			for _, l := range out {
				if ansi.StringWidth(l) > size[0] {
					t.Errorf("%v %s: line too wide: %q", size, name, ansi.Strip(l))
				}
			}
			if last := ansi.Strip(out[len(out)-1]); !strings.Contains(last, "RECENT") {
				t.Errorf("%v %s: last row %q", size, name, last)
			}
		}
	}
}

func TestMapCardShowsPurposeAndMaturityOnce(t *testing.T) {
	g := fakeGraph()
	g.Nodes = append(g.Nodes, protocol.Node{Name: "once", Kind: "component", Charter: protocol.Charter{
		Purpose:  "Serves exactly one purpose.",
		Maturity: "mature",
		Accepts:  "Small fixes.",
	}})
	m := openMap(t, mapModel(120, 34, g))
	m.mp.selected = "once"
	v := ansi.Strip(m.View())
	if n := strings.Count(v, "Serves exactly one purpose."); n != 1 {
		t.Fatalf("purpose shown %d times, want 1:\n%s", n, v)
	}
	if n := strings.Count(v, "mature"); n != 1 {
		t.Fatalf("maturity shown %d times, want 1:\n%s", n, v)
	}
}

func TestMapCardCharterScrolls(t *testing.T) {
	m := openMap(t, mapModel(80, 24, richGraph()))
	m.mp.selected = "tenebrae"
	before := ansi.Strip(m.View())
	if !strings.Contains(before, "ctrl+u/ctrl+d scroll") || strings.Contains(before, "embeddings.") {
		t.Fatal(before)
	}
	for i := 0; i < 3; i++ {
		m = keys(m, "ctrl+d")
	}
	if after := ansi.Strip(m.View()); !strings.Contains(after, "embeddings.") {
		t.Fatal(after)
	}
	// Another node starts at the top again.
	m.mp.selected = "cogforge"
	if v := ansi.Strip(m.View()); !strings.Contains(v, "accepts") {
		t.Fatal(v)
	}
}

// cardText draws the card at a tall size so every section is visible without
// scrolling, and drops the styling.
func cardText(m Model, w, h int) string {
	return ansi.Strip(strings.Join(m.mapFocusView(w, h), "\n"))
}

func TestMapCardLegendLivesInHelp(t *testing.T) {
	m := openMap(t, mapModel(120, 40, richGraph()))
	m.mp.selected = "tenebrae"
	if strings.Contains(cardText(m, 120, 60), "Legend") {
		t.Fatal("the card still carries the legend")
	}
	if line := ansi.Strip(m.helpLine(120)); strings.Contains(line, "! legend") {
		t.Fatalf("the key line still offers the legend: %q", line)
	}
	m = keys(m, "!")
	if strings.Contains(cardText(m, 120, 60), "Legend") {
		t.Fatal("a removed key opened the legend on the card")
	}
	shown := ansi.Strip(strings.Join(m.helpContent(120), "\n"))
	for _, want := range []string{
		"Legend", "consumer uses provider", "advisor, arbiter or owner",
		"deployer runs the app's deploys", "delivered; B judges it",
		"delivered with a trust note", "inside B's merge authority and refusals",
		"held in the Inbox", "refused", "specific edge beats", "all projects",
		"never grants deploy or merge permission", "deploy_rule and merge_authority",
		"only charter field with an enforced effect", "every turn",
		"first 40 characters", "Tools tab", "another agent looks this node up",
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("help legend lacks %q:\n%s", want, shown)
		}
	}
	for _, size := range [][2]int{{80, 24}, {120, 34}, {190, 45}} {
		wide := openMap(t, mapModel(size[0], size[1], richGraph()))
		wide.mp.selected = "tenebrae"
		for _, line := range wide.mapFocusView(size[0], size[1]-1) {
			if ansi.StringWidth(line) > size[0] {
				t.Errorf("%v: card line too wide: %q", size, ansi.Strip(line))
			}
			if strings.Contains(ansi.Strip(line), "Legend") {
				t.Errorf("%v: card still draws the legend: %q", size, ansi.Strip(line))
			}
		}
	}
}

func TestMapCardRulesShowEffectiveValuesAndOverrides(t *testing.T) {
	m := openMap(t, mapModel(120, 40, richGraph()))
	m.mp.selected = "tenebrae"
	view := cardText(m, 120, 60)
	if !strings.Contains(view, "Rules") {
		t.Fatalf("no Rules section:\n%s", view)
	}
	for _, want := range [][3]string{
		{"tests_before_merge", protocol.TestsSuiteNewTests, "maturity default"},
		{"merge_authority", protocol.MergeDelegated, "maturity default"},
		{"worker_sandbox", protocol.WorkersSandboxed, "maturity default"},
		{"judge_consumers", protocol.JudgeStrict, "maturity default"},
		{"deploy_rule", protocol.DeployOwner, "owner override"},
		{"autonomy", protocol.AutonomyWake, "owner override"},
	} {
		found := false
		for _, l := range strings.Split(view, "\n") {
			if strings.Contains(l, want[0]) && strings.Contains(l, want[1]) && strings.HasSuffix(strings.TrimSpace(l), want[2]) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no rule line for %s with value %q and source %q:\n%s", want[0], want[1], want[2], view)
		}
	}
}

func TestMapCardRulesWithoutMaturityAreDefaults(t *testing.T) {
	g := fakeGraph()
	g.Nodes = append(g.Nodes, protocol.Node{Name: "fresh", Kind: "component", Charter: protocol.Charter{Accepts: "Small fixes."}})
	m := openMap(t, mapModel(120, 40, g))
	m.mp.selected = "fresh"
	view := cardText(m, 120, 60)
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "tests_before_merge") {
			if !strings.Contains(line, protocol.TestsSuite) || !strings.HasSuffix(strings.TrimSpace(line), "default") {
				t.Fatalf("line %q, want the growing default with a default mark", line)
			}
			return
		}
	}
	t.Fatalf("no tests_before_merge line:\n%s", view)
}

func TestMapCardLearnedIsItsOwnSectionAfterCharterAndRules(t *testing.T) {
	m := openMap(t, mapModel(120, 40, richGraph()))
	m.mp.selected = "tenebrae"
	m.snap.Learned = []protocol.LearnedSuggestion{{
		ID: 11, Node: "tenebrae", Field: "needs", Phrase: "a backup box", NewValue: "a backup box",
		Status: "applied", By: "learned", HistoryID: 42,
		DecidedAt: t0.UnixNano(), Evidence: []protocol.LearnedEvidence{{ID: "m1", Excerpt: "ops owns backups"}},
	}}
	view := cardText(m, 120, 60)
	charter := strings.Index(view, "Charter")
	learned := strings.Index(view, "Learned changes")
	rules := strings.Index(view, "Rules")
	if charter < 0 || rules < 0 || learned < 0 {
		t.Fatalf("missing a section: charter=%d rules=%d learned=%d\n%s", charter, rules, learned, view)
	}
	if !(charter < learned && learned < rules) {
		t.Fatalf("section order charter=%d learned=%d rules=%d\n%s", charter, learned, rules, view)
	}
	if !strings.Contains(view, "accepts") || strings.Contains(view[charter:learned], "Learned changes") {
		t.Fatalf("learned changes still break the charter:\n%s", view)
	}
	if strings.Contains(view, "U next change") {
		t.Fatalf("the learned hint still floats in the card:\n%s", view)
	}
}

func TestMapCardLearnedWhyWrapsAtAWordWithAnEllipsis(t *testing.T) {
	excerpt := strings.Repeat("the owner asked for a quieter card and the queue was slow, so the change waited for the next quiet window before it applied to the node. ", 3)
	m := openMap(t, mapModel(120, 40, richGraph()))
	m.mp.selected = "tenebrae"
	m.snap.Learned = []protocol.LearnedSuggestion{{
		ID: 11, Node: "tenebrae", Field: "needs", Phrase: "a backup box", NewValue: "a backup box",
		Status: "applied", By: "learned", HistoryID: 42, DecidedAt: t0.UnixNano(),
		Evidence: []protocol.LearnedEvidence{{ID: "m1", Excerpt: excerpt}},
	}}
	view := cardText(m, 120, 60)
	lines := strings.Split(view, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, "why:") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("no why line:\n%s", view)
	}
	why := []string{strings.TrimSpace(strings.SplitN(lines[start], "why:", 2)[1])}
	for i := start + 1; i < len(lines) && strings.HasPrefix(lines[i], "         ") && len(why) < 3; i++ {
		why = append(why, strings.TrimSpace(lines[i]))
	}
	if len(why) > 2 {
		t.Fatalf("the why takes %d lines, want at most 2:\n%s", len(why), view)
	}
	last := why[len(why)-1]
	if !strings.HasSuffix(last, "…") {
		t.Fatalf("a cut why line does not end with an ellipsis: %q", last)
	}
	joined := strings.TrimSuffix(strings.Join(why, " "), " …")
	if !strings.HasPrefix(excerpt, joined) {
		t.Fatalf("the why is not a prefix of the excerpt after wrapping: %q", joined)
	}
	if n := len(joined); n < len(excerpt) && excerpt[n] != ' ' {
		t.Fatalf("the why cuts mid-word: %q then %q", joined, excerpt[n-10:n+10])
	}
}

func TestMapCardEdgesSitInTwoCappedColumns(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}, {190, 45}} {
		m := openMap(t, mapModel(size[0], size[1], richGraph()))
		m.mp.selected = "tenebrae"
		lines := m.mapFocusView(size[0], size[1]-1)
		label := ""
		for _, line := range lines {
			if strings.Contains(line, "IN") && strings.Contains(line, "OUT") {
				label = ansi.Strip(line)
				break
			}
		}
		if label == "" {
			t.Fatalf("%v: IN and OUT are not on one row:\n%s", size, ansi.Strip(strings.Join(lines, "\n")))
		}
		if out := strings.Index(label, "OUT"); out > 62 {
			t.Fatalf("%v: OUT starts at column %d, want next to IN: %q", size, out, label)
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("%v: line too wide: %q", size, ansi.Strip(line))
			}
		}
	}
}

func TestGoldenMapCard(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}, {190, 45}} {
		m := openMap(t, mapModel(size[0], size[1], richGraph()))
		m.mp.selected = "tenebrae"
		golden(t, fmt.Sprintf("map_card_%dx%d", size[0], size[1]), m.View())
	}
}
