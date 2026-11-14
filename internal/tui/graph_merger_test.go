package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

// The graph card reads the merger's parent from the stored tree, so it names
// the same lead as the Sessions tree and the gateway.
func TestGraphMergerCardNamesItsTreeLead(t *testing.T) {
	ref := func(kind, id string) *protocol.GraphRef { return &protocol.GraphRef{Kind: kind, ID: id} }
	merger := graphRefKey(ref("agent", "server-merger"))
	v := protocol.GraphView{Nodes: []protocol.Node{
		{Name: "tenebrae", Ref: ref("repo", "tenebrae"), Lead: ref("agent", "server-lead")},
		{Name: "server lead", Ref: ref("agent", "server-lead")},
		{Name: "server-merger", Ref: ref("agent", "server-merger"), ReportsTo: "server-lead"},
	}}
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := mapModel(size[0], size[1], v)
		m.snap.Agents = append(m.snap.Agents,
			protocol.AgentView{SessionID: "s_lead", AgentID: "server-lead", Profile: protocol.Profile{Name: "server lead"}, Repo: protocol.RepoInfo{Project: "tenebrae"}, Online: true},
			protocol.AgentView{SessionID: "s_merger", AgentID: "server-merger", Type: "merger", Profile: protocol.Profile{Name: "server merger"}, Repo: protocol.RepoInfo{Project: "tenebrae"}, Online: true},
		)
		m.applyMap(mapMsg{view: v, at: t0})
		m.tab = graphTab
		m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: merger})
		gw, gh := m.graphSize()
		m.graphState.Camera = m.graphState.Graph.Fit(gw, gh)
		name := fmt.Sprintf("graph_merger_lead_%dx%d", size[0], size[1])
		golden(t, name, m.testView())
		m.mapOn, m.mp.selected = true, merger
		if n, ok := m.mapNode(m.mp.selected); !ok || n.Ref == nil {
			t.Fatalf("selected %q has no node (%v)", m.mp.selected, m.mp.nodes)
		}
		card := ansi.Strip(m.testView())
		if !strings.Contains(card, "reports to server lead") {
			t.Fatalf("merger card does not name its tree lead:\n%s", card)
		}
		golden(t, name+"_card", m.testView())
	}
}

func TestGraphMergerDeliveryOwners(t *testing.T) {
	for _, width := range []int{80, 120} {
		for _, state := range []string{"caller", "online", "offline"} {
			t.Run(fmt.Sprintf("%d/%s", width, state), func(t *testing.T) {
				m := graphModel(width, 34)
				m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "project-03"})
				want := "accept, test, review, merge, release, verify: caller"
				if state != "caller" {
					m.snap.Agents = append(m.snap.Agents, protocol.AgentView{SessionID: "s_merger", Type: "merger", Harness: "claude", Online: state == "online", Repo: protocol.RepoInfo{Project: "project-03"}, Profile: protocol.Profile{Name: "project-03 merger"}})
					for _, kind := range []string{"tests", "merges"} {
						m.mp.edges = append(m.mp.edges, protocol.Edge{From: "/delivery/merger", To: "project-03", Kind: kind})
					}
					suffix := "online"
					if state == "offline" {
						suffix = "offline, tasks wait"
					}
					want = "test, merge: project-03 merger (" + suffix + ")"
				}
				for _, view := range []string{ansi.Strip(strings.Join(m.graphView(width, 34), "\n")), ansi.Strip(strings.Join(keys(m, "enter").mapFocusView(width, 30), "\n"))} {
					if strings.Contains(view, "/delivery/") {
						t.Fatalf("internal owner source shown: %s", view)
					}
					if !strings.Contains(view, "accept") || !strings.Contains(view, want) {
						t.Fatalf("missing delivery owners %q:\n%s", want, view)
					}
				}
				golden(t, fmt.Sprintf("graph_merger_%s_%d", state, width), m.testView())
				m = keys(m, "enter")
				golden(t, fmt.Sprintf("graph_merger_card_%s_%d", state, width), m.testView())
			})
		}
	}
}
