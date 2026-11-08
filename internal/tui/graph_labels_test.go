package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

func TestGraphTypedLabels(t *testing.T) {
	for _, typed := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "typed"}[typed], func(t *testing.T) {
			v := protocol.GraphView{Nodes: []protocol.Node{{Name: "shop", Kind: "component"}, {Name: "lead", Kind: "architect"}}}
			if typed {
				v.Nodes[0].Ref = &protocol.GraphRef{Kind: "repo", ID: "opaque-repo"}
				v.Nodes[0].Kind = "repo"
				v.Nodes[1] = protocol.Node{Name: "shop", Kind: "agent", Ref: &protocol.GraphRef{Kind: "agent", ID: "opaque-agent"}}
			}
			m := mapModel(120, 40, v)
			m.applyMap(mapMsg{view: v})
			names := m.mapSelectable()
			if len(names) != 2 || names[0] == names[1] {
				t.Fatalf("conflated nodes: %v", names)
			}
			labels := map[string]bool{}
			for _, key := range names {
				m.mp.selected = key
				out := ansi.Strip(strings.Join(m.mapFocusView(120, 40), "\n"))
				label := "shop"
				n, _ := m.mapNode(key)
				if n.Kind == "architect" {
					label = "lead"
					if typed {
						label = "shop"
					}
				}
				if !strings.Contains(out, label) || strings.Contains(out, "opaque-") {
					t.Fatalf("missing %q or leaked ID:\n%s", label, out)
				}
				if typed && n.Ref.Kind == "agent" && strings.Contains(out, "architect") {
					t.Fatal("Agent with no role is labeled architect")
				}
				labels[label] = true
				if typed && n.Ref.Kind == "agent" {
					m.mapOn = true
					next := keys(m, "enter")
					if next.filter != "shop" || !matches(protocol.AgentView{AgentName: "shop"}, next.filter) {
						t.Fatal("Agent card did not filter its sessions")
					}
				}
			}
			if (!typed && len(labels) != 2) || (typed && len(labels) != 1) {
				t.Fatal(labels)
			}
		})
	}
}

func TestSessionDetailIdentityLabels(t *testing.T) {
	m, _ := workerModel(120, 40)
	a := m.snap.Agents[0]
	a.AgentID = "opaque-agent"
	a.AgentName = "shop"
	a.Repo.Project = "opaque-repo"
	a.ProjectDisplayName = "shop"
	_, lines := m.detailLeft(a, 80, 40)
	out := ansi.Strip(strings.Join(lines, "\n"))
	for _, label := range []string{"agent", "repo"} {
		if !strings.Contains(out, label+strings.Repeat(" ", 9-len(label))+"shop") {
			t.Fatalf("missing %s name:\n%s", label, out)
		}
	}
	if strings.Contains(out, "opaque-") {
		t.Fatal(out)
	}
}

func TestGraphIdentityGoldens(t *testing.T) {
	repo := &protocol.GraphRef{Kind: "repo", ID: "opaque-record"}
	agent := &protocol.GraphRef{Kind: "agent", ID: "opaque-record"}
	v := protocol.GraphView{
		Nodes: []protocol.Node{
			{Name: "shop", Kind: "repo", Ref: repo, Owner: agent, Sessions: []protocol.GraphSession{{ID: "shop-live", Status: "idle"}}, Charter: protocol.Charter{Purpose: "Build and ship the shop."}},
			{Name: "shop", Kind: "agent", Ref: agent, Role: "architect", Charter: protocol.Charter{Purpose: "Own the shop and review changes."}},
		},
		Edges: []protocol.Edge{{FromRef: agent, ToRef: repo, Kind: "accepts", Rule: "normal"}},
	}
	m := mapModel(80, 24, v)
	m.applyMap(mapMsg{view: v, at: t0})
	m.tab = graphTab
	m.mp.selected = graphRefKey(repo)
	m.mapOn = true
	golden(t, "graph_identity_repo_80x24", m.testView())
	m.mp.selected = graphRefKey(agent)
	golden(t, "graph_identity_agent_80x24", m.testView())
	m.mapOn = false
	golden(t, "graph_identity_network_80x24", m.testView())
	m.setTeamFilter(unassignedTeam)
	scene := m.graphScene(m.graphState, graphview.Size{Width: 80, Height: 20})
	labels := map[string]bool{}
	for _, n := range scene.Nodes {
		labels[n.Label] = true
	}
	if !labels["shop"] || len(m.mp.merged) != 1 {
		t.Fatalf("scene labels: %v", labels)
	}
	sessions := keys(newModel(80, 24), "d")
	for i := range sessions.snap.Agents {
		if sessions.snap.Agents[i].SessionID == sessions.selected {
			sessions.snap.Agents[i].AgentName = "shop"
			sessions.snap.Agents[i].ProjectDisplayName = "shop"
		}
	}
	golden(t, "session_identity_80x24", sessions.testView())
}
