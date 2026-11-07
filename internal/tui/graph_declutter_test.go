package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
	"strings"
	"testing"
	"time"
)

func declutterFixture() protocol.GraphView {
	ref := func(k, id string) *protocol.GraphRef { return &protocol.GraphRef{Kind: k, ID: id} }
	return protocol.GraphView{Nodes: []protocol.Node{
		{Name: "shop", Ref: ref("repo", "shop"), Owner: ref("agent", "builder")},
		{Name: "builder", Ref: ref("agent", "builder")},
		{Name: "api", Ref: ref("repo", "api")},
		{Name: "review", Ref: ref("agent", "review")},
		{Name: "tmp.demo", Ref: ref("repo", "tmp")},
		{Name: "forge", Ref: ref("team", "forge"), Lead: ref("agent", "builder")},
	}, Edges: []protocol.Edge{
		{FromRef: ref("agent", "builder"), ToRef: ref("repo", "shop"), Kind: "accepts"},
		{FromRef: ref("repo", "shop"), ToRef: ref("team", "forge"), Kind: "member_of"},
		{FromRef: ref("agent", "builder"), ToRef: ref("team", "forge"), Kind: "member_of"},
		{FromRef: ref("repo", "shop"), ToRef: ref("repo", "api"), Kind: "depends"},
	}}
}
func TestGraphDeclutterMergesOneToOne(t *testing.T) {
	m := mapModel(120, 34, declutterFixture())
	m.applyMap(mapMsg{view: declutterFixture(), at: t0})
	if m.graphState.Graph.Selected() == dormantChip {
		t.Fatal("initial selection is the dormant chip")
	}
	if _, ok := m.mp.merged["shop"]; !ok {
		t.Fatal("one-to-one owner was not merged")
	}
	if m.graphName("shop") != "shop" {
		t.Fatal("kind prefix in label")
	}
	v := declutterFixture()
	v.Nodes = append(v.Nodes, protocol.Node{Name: "second", Ref: &protocol.GraphRef{Kind: "repo", ID: "second"}, Owner: &protocol.GraphRef{Kind: "agent", ID: "builder"}})
	m.applyMap(mapMsg{view: v, at: t0})
	if len(m.mp.merged) != 0 {
		t.Fatal("shared owner merged")
	}
	v = declutterFixture()
	v.Edges = append(v.Edges, protocol.Edge{FromRef: &protocol.GraphRef{Kind: "agent", ID: "review"}, ToRef: &protocol.GraphRef{Kind: "repo", ID: "shop"}, Kind: "accepts"})
	m.applyMap(mapMsg{view: v, at: t0})
	if len(m.mp.merged) != 0 {
		t.Fatal("repo with several agents merged")
	}
}
func TestGraphDeclutterDormant(t *testing.T) {
	m := mapModel(120, 34, declutterFixture())
	m.now = func() time.Time { return t0 }
	m.applyMap(mapMsg{view: declutterFixture(), at: t0})
	m.tab = graphTab
	if len(m.mp.dormant) == 0 {
		t.Fatal("dormant nodes not folded")
	}
	m = keys(m, "d")
	if !m.mp.showDormant {
		t.Fatal("d did not show dormant nodes")
	}
}
func TestGraphDeclutterFocusAndTeamZoom(t *testing.T) {
	m := mapModel(120, 34, declutterFixture())
	m.applyMap(mapMsg{view: declutterFixture(), at: t0})
	m.tab = graphTab
	m = keys(m, "d")
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: graphRefKey(&protocol.GraphRef{Kind: "team", ID: "forge"})})
	m = keys(m, "f")
	if m.teamFilter != "forge" || !m.mp.outside["api"] {
		t.Fatal("team zoom missing outside neighbour")
	}
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "api"})
	m = keys(m, "f")
	if m.mp.focusNode != "api" {
		t.Fatal("node focus missing")
	}
	for _, n := range m.graphState.Graph.Nodes {
		if n.Name != "api" && n.Name != "shop" && n.Name != dormantChip {
			t.Fatalf("unrelated node %s in focus", n.Name)
		}
	}
	m = keys(m, "esc")
	if m.teamFilter != "forge" || m.mp.focusNode != "" {
		t.Fatal("first escape lost team level")
	}
	m = keys(m, "esc")
	if m.teamFilter != "" {
		t.Fatal("second escape did not return overview")
	}
}
func TestGraphDeclutterEverything(t *testing.T) {
	v := declutterFixture()
	for _, id := range []string{"one", "two", "three", "four", "tmp"} {
		r := &protocol.GraphRef{Kind: "repo", ID: id}
		v.Nodes = append(v.Nodes, protocol.Node{Name: id, Ref: r})
		v.Edges = append(v.Edges, protocol.Edge{FromRef: &protocol.GraphRef{Kind: "agent", ID: "review"}, ToRef: r, Kind: "depends", Mode: "auto"})
	}
	// A shared agent remains separate and its repeated default edges fold.
	v.Edges = append(v.Edges, protocol.Edge{FromRef: &protocol.GraphRef{Kind: "agent", ID: "review"}, ToRef: &protocol.GraphRef{Kind: "repo", ID: "api"}, Kind: "depends", Mode: "auto"})
	m := mapModel(120, 34, v)
	m.applyMap(mapMsg{view: v, at: t0})
	key := graphRefKey(&protocol.GraphRef{Kind: "agent", ID: "review"})
	if m.mp.everything[key] != "→ all projects · auto" {
		t.Fatalf("missing everything mark: %v", m.mp.everything)
	}
	for _, e := range m.graphState.Graph.Edges {
		if e.From == key && e.Kind == "depends" {
			t.Fatal("everything lines still drawn")
		}
	}
	if len(m.mp.edges) < 6 {
		t.Fatal("card lost full edges")
	}
	for i := range v.Edges {
		if v.Edges[i].Mode == "auto" {
			v.Edges[i].Mode = ""
			v.Edges[i].Rule = "auto"
		}
	}
	m.applyMap(mapMsg{view: v, at: t0})
	if m.mp.everything[key] != "→ all projects · auto" {
		t.Fatal("default rule missing from mark")
	}

}

type archiveGraphSource struct {
	*fakeSource
	calls []protocol.GraphRef
	fail  string
}

func (s *archiveGraphSource) ArchiveGraphNode(_ context.Context, ref protocol.GraphRef) error {
	s.calls = append(s.calls, ref)
	if ref.ID == s.fail {
		return fmt.Errorf("archive refused for %s", ref.ID)
	}
	return nil
}
func TestGraphDeclutterRemoval(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprint(all), func(t *testing.T) {
			m := mapModel(120, 34, declutterFixture())
			m.snap.Agents = nil
			src := &archiveGraphSource{fakeSource: m.src.(*fakeSource)}
			m.src = src
			m.applyMap(mapMsg{view: declutterFixture(), at: t0})
			m.tab = graphTab
			m = keys(m, "d")
			selected, key := "shop", "x"
			if all {
				selected, key = dormantChip, "X"
			}
			m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: selected})
			next, cmd := m.graphKey(tea.KeyPressMsg{Text: key, Code: []rune(key)[0]})
			m = next.(Model)
			if m.mp.removal == nil || cmd != nil || len(src.calls) != 0 {
				t.Fatal("removal ran before confirmation")
			}
			if !strings.Contains(ansi.Strip(m.testView()), "for good?") {
				t.Fatal("confirmation missing")
			}
			m = keys(m, "n")
			if m.mp.removal != nil || len(src.calls) != 0 {
				t.Fatal("cancel removed nodes")
			}
			next, _ = m.graphKey(tea.KeyPressMsg{Text: key, Code: []rune(key)[0]})
			m = next.(Model)
			next, cmd = m.graphRemovalKey(tea.KeyPressMsg{Text: "y", Code: 'y'})
			m = next.(Model)
			if cmd == nil {
				t.Fatal("yes did not issue removal")
			}
			next, _ = m.Update(cmd())
			m = next.(Model)
			want := 2
			if all {
				want = 5
			}
			if len(src.calls) != want {
				t.Fatalf("calls: %+v want %d", src.calls, want)
			}
			if !strings.Contains(ansi.Strip(m.testView()), m.notice.text) {
				t.Fatal("removal result not visible")
			}
			if !strings.Contains(m.notice.text, "Removed") || !m.mp.removed["shop"] || !m.mp.removed["agent\x00builder"] {
				t.Fatalf("missing removal notice or records: %+v", m.notice)
			}
		})
	}
}
func TestGraphDeclutterRemovalFailure(t *testing.T) {
	m := mapModel(120, 34, declutterFixture())
	m.snap.Agents = nil
	src := &archiveGraphSource{fakeSource: m.src.(*fakeSource), fail: "shop"}
	m.src = src
	m.applyMap(mapMsg{view: declutterFixture(), at: t0})
	m.tab = graphTab
	m = keys(m, "d")
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "shop"})
	m, _ = m.startGraphRemoval(false)
	next, cmd := m.graphRemovalKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if !strings.Contains(m.notice.text, "archive refused") || m.mp.removed["shop"] {
		t.Fatal("failure hidden or failed node removed")
	}
}
func TestGraphDeclutterLiveState(t *testing.T) {
	m := mapModel(120, 34, declutterFixture())
	m.snap.Agents = nil
	for i, status := range []string{"offline", "idle", "busy", "needs you"} {
		m.snap.Agents = append(m.snap.Agents, protocol.AgentView{SessionID: fmt.Sprint(i), AgentID: "builder", Repo: protocol.RepoInfo{Project: "shop"}, Online: i > 0, Status: status, LastActive: t0.Unix()})
		m.applyMap(mapMsg{view: declutterFixture(), at: t0})
		n, _ := m.mapNode("shop")
		if got := m.graphLiveRole(n); got != status {
			t.Fatalf("role %s want %s", got, status)
		}
		if m.graphDormant(n) {
			t.Fatal("recent or live session called dormant")
		}
		scene := m.graphScene(m.graphState, graphview.Size{Width: 116, Height: 20})
		for _, n := range scene.Nodes {
			if n.Name == "shop" && n.Role != status {
				t.Fatal("scene lost live state")
			}
		}
	}
	m.snap.Agents = []protocol.AgentView{{AgentID: "builder", LastActive: t0.Add(-31 * 24 * time.Hour).Unix()}}
	m.applyMap(mapMsg{view: declutterFixture(), at: t0})
	n, _ := m.mapNode("shop")
	if !m.graphDormant(n) {
		t.Fatal("old offline session not dormant")
	}
}

func declutterScreenModel(w, h int) Model {
	v := protocol.GraphView{}
	ref := func(k, id string) *protocol.GraphRef { return &protocol.GraphRef{Kind: k, ID: id} }
	names := []string{"shop", "api", "checkout", "accounts", "billing", "search", "catalog", "gateway", "storage", "events", "metrics", "docs", "desktop", "mobile", "tools", "deploy", "website", "support"}
	for _, team := range []string{"forge", "platform"} {
		v.Nodes = append(v.Nodes, protocol.Node{Name: team, Ref: ref("team", team), Lead: ref("agent", names[0]+"-agent")})
	}
	for i, name := range names {
		v.Nodes = append(v.Nodes, protocol.Node{Name: name, Ref: ref("repo", name), Owner: ref("agent", name+"-agent"), Charter: protocol.Charter{Purpose: "Build and maintain " + name + "."}}, protocol.Node{Name: name, Ref: ref("agent", name+"-agent"), Charter: protocol.Charter{Purpose: "Own changes and review delivery."}})
		if i < 8 {
			team := "forge"
			if i >= 3 {
				team = "platform"
			}
			v.Edges = append(v.Edges, protocol.Edge{FromRef: ref("repo", name), ToRef: ref("team", team), Kind: "member_of"})
		}
		v.Edges = append(v.Edges, protocol.Edge{FromRef: ref("agent", "standards"), ToRef: ref("repo", name), Kind: "depends", Mode: "auto"})
	}
	v.Nodes = append(v.Nodes, protocol.Node{Name: "standards", Ref: ref("agent", "standards")}, protocol.Node{Name: "review", Ref: ref("agent", "review")})
	for _, name := range []string{"tmp", "tmp.demo", "agent-net-scratch-test", "retired"} {
		n := protocol.Node{Name: name, Ref: ref("repo", name)}
		if name == "retired" {
			active := false
			n.Active = &active
		}
		v.Nodes = append(v.Nodes, n)
	}
	for _, pair := range [][2]string{{"shop", "accounts"}, {"api", "billing"}, {"checkout", "gateway"}, {"gateway", "docs"}, {"storage", "deploy"}, {"shop", "api"}} {
		v.Edges = append(v.Edges, protocol.Edge{FromRef: ref("repo", pair[0]), ToRef: ref("repo", pair[1]), Kind: "depends"})
	}
	m := mapModel(w, h, v)
	m.snap.Agents = nil
	for i, name := range names {
		status := "idle"
		if i == 0 {
			status = "needs you"
		}
		if i == 1 || i == 4 {
			status = "busy"
		}
		m.snap.Agents = append(m.snap.Agents, protocol.AgentView{SessionID: name + "-session", AgentID: name + "-agent", Repo: protocol.RepoInfo{Project: name}, Online: i < 14, Status: status, LastActive: t0.Add(-time.Hour).Unix()})
	}
	for _, name := range []string{"standards", "review"} {
		m.snap.Agents = append(m.snap.Agents, protocol.AgentView{SessionID: name + "-session", AgentID: name, Online: true, Status: "idle"})
	}
	m.applyMap(mapMsg{view: v, at: t0})
	m.tab = graphTab
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "shop"})
	return m
}
func TestGraphDeclutterGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		for _, view := range []string{"overview", "dormant", "focus", "team_zoom"} {
			m := declutterScreenModel(size[0], size[1])
			switch view {
			case "dormant":
				m = keys(m, "d")
			case "focus":
				m = keys(m, "f")
			case "team_zoom":
				m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "team\x00forge"})
				m = keys(m, "f")
			}
			plain := ansi.Strip(m.testView())
			if strings.Contains(plain, "repo shop") || strings.Contains(plain, "agent shop") {
				t.Fatal("kind prefix returned")
			}
			if view == "overview" {
				for _, want := range []string{"forge", "★ shop", "platform", "unassigned", "3 depends", "4 dormant · d to show", "→ all projects · auto"} {
					if !strings.Contains(plain, want) {
						t.Errorf("%s missing in overview:\n%s", want, plain)
					}
				}
			}
			golden(t, fmt.Sprintf("graph_declutter_%s_%dx%d", view, size[0], size[1]), m.testView())
		}
	}
}

func TestGraphRemovalKeepsQuitKey(t *testing.T) {
	m := declutterScreenModel(80, 24)
	m.mp.removal = &graphRemoval{question: "Remove temporary repo for good?"}
	m = keys(m, "ctrl+c")
	if !m.quitting {
		t.Fatal("confirmation swallowed ctrl+c")
	}
}

func TestGraphFindRespectsFocusedView(t *testing.T) {
	m := declutterScreenModel(120, 34)
	m = keys(m, "f", "/")
	m = typeText(m, "metrics")
	if found := m.mapProjectNodes(); len(found) != 0 {
		t.Fatalf("find returned hidden node %s", found[0].Name)
	}
	m = keys(m, "esc")
	m = typeText(m, "api")
	m = keys(m, "enter")
	if got := m.graphState.Graph.Selected(); got != "api" {
		t.Fatalf("visible match not selected: %s", got)
	}
}

func TestGraphTeamCycleLeavesNodeFocus(t *testing.T) {
	m := declutterScreenModel(120, 34)
	m = keys(m, "f", "t")
	if m.mp.focusNode != "" || m.teamFilter != "forge" {
		t.Fatal("team cycle retained node focus")
	}
}

func TestGraphFocusKeepsLiveNeighboursAndCollapsedLinks(t *testing.T) {
	m := declutterScreenModel(120, 34)
	m = keys(m, "f")
	scene := m.graphScene(m.graphState, graphview.Size{Width: 116, Height: 24})
	found := map[string]string{}
	for _, n := range scene.Nodes {
		found[n.Name] = n.Role
	}
	if found["api"] != "busy" || found["accounts"] != "idle" {
		t.Fatalf("node focus lost live colours: %v", found)
	}
	if _, ok := found["agent\x00standards"]; !ok {
		t.Fatal("collapsed default link lost its direct neighbour")
	}
	m = keys(m, "esc")
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "team\x00forge"})
	m = keys(m, "f")
	if !m.mp.outside["agent\x00standards"] {
		t.Fatal("team zoom lost outside default-link neighbour")
	}
}
