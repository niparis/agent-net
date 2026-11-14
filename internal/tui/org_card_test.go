package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// orgCardGraph is an Agent with a lead session and two scopes, one for the
// whole network and one for a team.
func orgCardGraph(lead *protocol.GraphSession) protocol.GraphView {
	return protocol.GraphView{Nodes: []protocol.Node{
		{Ref: teamRef("agent", "org-research"), Name: "research", Kind: "architect",
			LeadSession: lead,
			Scopes: []protocol.Scope{
				{Node: "org-research", Kind: protocol.ScopeReview, Over: protocol.ScopeOverAll},
				{Node: "org-research", Kind: protocol.ScopeMerge, Over: "team:forge"},
			}},
	}}
}

// The Agent card names the lead session of its Agent: online, offline since a
// time, or gone. It lists the scopes in the words the session reads.
func TestAgentCardShowsLeadSessionAndAuthority(t *testing.T) {
	const leadID = "s_0000000000000321"
	cases := []struct {
		name   string
		online bool // the lead is in the snapshot
		live   bool // the snapshot says it is online
		want   string
	}{
		{"online", true, true, "strategy lead · online"},
		{"offline", true, false, "strategy lead · offline since 12:05"},
		{"gone", false, false, "strategy lead · gone"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lead := &protocol.GraphSession{ID: leadID, Name: "strategy lead", Online: c.live}
			m := openMap(t, mapModel(120, 40, orgCardGraph(lead)))
			if c.online {
				m.snap.Agents = append(m.snap.Agents, agent(leadID, "n_mac", "Mac", "claude", "strategy lead", func(a *protocol.AgentView) {
					a.Online = c.live
					a.LastSeen = t0.Add(-2 * time.Hour).Unix()
				}))
			}
			m.mp.selected = graphRefKey(teamRef("agent", "org-research"))
			text := ansi.Strip(m.testView())
			for _, want := range []string{"agent lead", c.want} {
				if !strings.Contains(text, want) {
					t.Errorf("card missing %q:\n%s", want, text)
				}
			}
		})
	}
	t.Run("authority", func(t *testing.T) {
		m := openMap(t, mapModel(120, 40, orgCardGraph(nil)))
		m.mp.selected = graphRefKey(teamRef("agent", "org-research"))
		text := ansi.Strip(m.testView())
		for _, want := range []string{
			"authority", "you may review and accept changes everywhere",
			"you may merge in team forge",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("card missing %q:\n%s", want, text)
			}
		}
		if strings.Contains(text, "agent lead") {
			t.Errorf("card with no lead session has an agent lead row:\n%s", text)
		}
	})
}

// The selected session shows the Delegated authority its Agent holds, in the
// words its instructions carry. A session with no scope shows no such line.
func TestSessionDetailShowsDelegatedAuthority(t *testing.T) {
	snap := fakeSnapshot()
	snap.Agents = append(snap.Agents, agent(childSessionID, "n_mac", "Mac", "claude", "research", func(a *protocol.AgentView) {
		a.AgentID = "org-research"
		a.Scopes = []protocol.Scope{{Node: "org-research", Kind: protocol.ScopeMerge, Over: "team:forge"}}
	}), agent(leadSessionID, "n_mac", "Mac", "claude", "strategy lead", func(a *protocol.AgentView) {
		a.AgentID = "org-strategy"
	}))
	m := treeModel(120, 34, snap)
	m.expand = true
	text := ansi.Strip(selectID(m, childSessionID).testView())
	if !strings.Contains(text, "allowed  you may merge in team forge") {
		t.Errorf("detail has no labelled allowed row:\n%s", text)
	}
	text = ansi.Strip(selectID(m, leadSessionID).testView())
	if strings.Contains(text, "allowed  ") {
		t.Errorf("session with no scope shows an authority line:\n%s", text)
	}
}

// The Agent card is one screen with a golden at each size: its lead session
// and its authority under the place block.
func TestOrgCardGolden(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}, {160, 40}} {
		lead := &protocol.GraphSession{ID: "s_0000000000000321", Name: "strategy lead", Online: true}
		graph := orgCardGraph(lead)
		graph.Nodes[0].Scopes = append(graph.Nodes[0].Scopes, protocol.Scope{Node: "org-research", Kind: protocol.ScopeAdmin, Over: "repo:forge"})
		m := mapModel(size.w, size.h, graph)
		m.snap.Agents = append(m.snap.Agents, agent("s_0000000000000321", "n_mac", "Mac", "claude", "strategy lead", nil))
		m = openMap(t, m)
		m.mp.selected = graphRefKey(teamRef("agent", "org-research"))
		golden(t, fmt.Sprintf("org_card_%dx%d", size.w, size.h), m.testView())
	}
}
