package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

func TestGraphMergerDeliveryOwners(t *testing.T) {
	for _, width := range []int{80, 120} {
		for _, state := range []string{"caller", "online", "offline"} {
			t.Run(fmt.Sprintf("%d/%s", width, state), func(t *testing.T) {
				m := graphModel(width, 34)
				m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "project-03"})
				want := "accept, test, review, merge, release, verify: session-3 (offline)"
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
