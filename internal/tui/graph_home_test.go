package tui

import (
	tea "charm.land/bubbletea/v2"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

func graphHomeFixture(lead bool) protocol.GraphView {
	repo := teamRef("repo", "workbench")
	agent := teamRef("agent", "builder")
	home := protocol.Node{Name: "workbench", Ref: repo, Sessions: []protocol.GraphSession{{ID: "home-live", Status: "idle"}}}
	if lead {
		home.Lead = agent
	} else {
		home.Owner = agent
	}
	return protocol.GraphView{Nodes: []protocol.Node{
		home,
		{Name: "workbench", Ref: agent, Sessions: []protocol.GraphSession{{ID: "agent-live", Status: "idle"}}},
		{Name: "second", Ref: teamRef("repo", "second"), Owner: agent, Sessions: []protocol.GraphSession{{ID: "second-live", Status: "idle"}}},
		{Name: "forge", Ref: teamRef("team", "forge"), Lead: agent},
	}}
}

func TestGraphSameNameHomeMergesSharedOwnerOrLead(t *testing.T) {
	for _, lead := range []bool{false, true} {
		t.Run(map[bool]string{false: "owner", true: "lead"}[lead], func(t *testing.T) {
			v := graphHomeFixture(lead)
			m := mapModel(120, 34, v)
			m.applyMap(mapMsg{view: v, at: t0})
			if got := m.mp.aliases["agent\x00builder"]; got != "workbench" {
				t.Fatalf("shared agent's home alias = %q", got)
			}
			if _, ok := m.mp.merged["workbench"]; !ok {
				t.Fatal("home repo and same-name agent were not merged")
			}
			if _, ok := m.mp.merged["second"]; ok {
				t.Fatal("agent also merged into its other repo")
			}
			scene := m.graphScene(m.graphState, graphview.Size{Width: 116, Height: 24})
			count := 0
			for _, n := range scene.Nodes {
				if n.Label == "workbench" {
					count++
				}
				if n.Name == "agent\x00builder" {
					t.Fatal("merged agent still appears")
				}
			}
			if count != 1 {
				t.Fatalf("home label appears %d times", count)
			}
		})
	}
}

func TestGraphMergedHomeUsesEitherSidesTeamOnce(t *testing.T) {
	for _, side := range []string{"repo", "agent"} {
		t.Run(side, func(t *testing.T) {
			v := graphHomeFixture(false)
			member := teamRef("repo", "workbench")
			if side == "agent" {
				member = teamRef("agent", "builder")
			}
			v.Edges = []protocol.Edge{{FromRef: member, ToRef: teamRef("team", "forge"), Kind: "member_of"}}
			m := mapModel(120, 34, v)
			m.applyMap(mapMsg{view: v, at: t0})
			scene := m.graphScene(m.graphState, graphview.Size{Width: 116, Height: 24})
			count := 0
			for _, g := range scene.Groups {
				for _, name := range g.Members {
					if name == "workbench" {
						count++
						if g.Name != "team\x00forge" {
							t.Fatalf("home appears in %q", g.Name)
						}
					}
					if name == "agent\x00builder" {
						t.Fatalf("agent repeated in %q", g.Name)
					}
				}
			}
			if count != 1 {
				t.Fatalf("home appears in %d boxes", count)
			}
		})
	}
}

func TestGraphUnrelatedNamesGetDimKindSuffixes(t *testing.T) {
	v := graphHomeFixture(false)
	v.Nodes[0].Owner = nil
	v.Nodes[2].Owner = nil
	m := mapModel(120, 34, v)
	m.applyMap(mapMsg{view: v, at: t0})
	size := graphview.Size{Width: 116, Height: 24}
	scene := m.graphScene(m.graphState, size)
	names := map[string]string{"workbench": "workbench · repo", "agent\x00builder": "workbench · agent"}
	for _, n := range scene.Nodes {
		if want, ok := names[n.Name]; ok && n.Label != want {
			t.Fatalf("label %q, want %q", n.Label, want)
		}
	}
	if len(m.mp.merged) != 0 {
		t.Fatal("unrelated namesakes merged")
	}
	grouped := (graphview.Braille{}).Render(scene, size)
	flat := scene
	flat.Groups = nil
	flat.Nodes = nil
	for _, n := range scene.Nodes {
		if _, ok := names[n.Name]; ok {
			n.Position = graphview.Point{X: .15 + float64(len(flat.Nodes))*.5, Y: .4}
			flat.Nodes = append(flat.Nodes, n)
		}
	}
	braille := (graphview.Braille{}).Render(flat, size)
	pixelLabels, pixelHits := graphview.PixelLayout(flat, size, graphview.PixelMetrics{CellWidth: 8, CellHeight: 16})
	for mode, out := range map[string]graphview.Output{"groups": grouped, "braille": braille, "pixel": {Labels: pixelLabels, Hits: pixelHits}} {
		for key := range names {
			found := false
			for cell, label := range out.Labels {
				if out.Hits[cell] == key && label.Rune == '·' {
					found = true
					if label.Role != "labelDim" {
						t.Errorf("%s: suffix role %q", mode, label.Role)
					}
				}
			}
			if !found {
				t.Errorf("%s: missing suffix for %s", mode, key)
			}
		}
	}
	if text := strings.Join(grouped.Lines, "\n"); !strings.Contains(text, "workbench · repo") || !strings.Contains(text, "workbench · agent") {
		t.Fatal("overview omitted a namesake")
	}
}

func TestGraphMergedHomeHasOneTeamAcrossZooms(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		v := graphHomeFixture(false)
		v.Nodes = append(v.Nodes, protocol.Node{Name: "studio", Ref: teamRef("team", "studio")})
		v.Edges = []protocol.Edge{
			{FromRef: teamRef("repo", "workbench"), ToRef: teamRef("team", "forge"), Kind: "member_of"},
			{FromRef: teamRef("agent", "builder"), ToRef: teamRef("team", "studio"), Kind: "member_of"},
		}
		if reverse {
			v.Edges[0], v.Edges[1] = v.Edges[1], v.Edges[0]
		}
		m := mapModel(120, 34, v)
		m.applyMap(mapMsg{view: v, at: t0})
		m.setTeamFilter("studio")
		for _, n := range m.graphState.Graph.Nodes {
			if n.Name == "workbench" {
				t.Fatal("merged home also appears in the agent's other team")
			}
		}
		m.setTeamFilter("forge")
		found := false
		for _, n := range m.graphState.Graph.Nodes {
			found = found || n.Name == "workbench"
		}
		if !found {
			t.Fatal("merged home lost its repo's team")
		}
	}
}

func TestGraphSharedHomeCleanupArchivesReposBeforeAgent(t *testing.T) {
	v := graphHomeFixture(false)
	for i := range v.Nodes {
		v.Nodes[i].Sessions = nil
	}
	m := mapModel(120, 34, v)
	m.snap.Agents = nil
	src := &archiveGraphSource{fakeSource: m.src.(*fakeSource)}
	m.src = src
	m.applyMap(mapMsg{view: v, at: t0})
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: dormantChip})
	m, _ = m.startGraphRemoval(true)
	_, cmd := m.graphRemovalKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	cmd()
	if len(src.calls) != 3 {
		t.Fatalf("cleanup calls: %+v", src.calls)
	}
	agentSeen := false
	for _, call := range src.calls {
		if call.Kind == "agent" {
			agentSeen = true
		}
		if call.Kind == "repo" && agentSeen {
			t.Fatal("shared agent tombstoned before all of its repos were archived")
		}
	}
}
