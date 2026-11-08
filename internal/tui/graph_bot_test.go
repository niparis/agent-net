package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

// botTeamModel is team "forge": the lead strategist, the repo ingest-forge and
// the bot "Trade" (app "cursor", renamed), which may call the ingest-forge
// server. The bot node is selected and the team is open inside.
func botTeamModel(w, h int) Model {
	ref := func(kind, id string) *protocol.GraphRef { return &protocol.GraphRef{Kind: kind, ID: id} }
	bot := ref("agent", "app_494a4ad58773c780")
	v := protocol.GraphView{Nodes: []protocol.Node{
		{Name: "forge", Ref: ref("team", "forge"), Lead: ref("agent", "strategist")},
		{Name: "strategist", Ref: ref("agent", "strategist"), Charter: protocol.Charter{Purpose: "Strategy research"}},
		{Name: "ingest-forge", Ref: ref("repo", "ingest-forge")},
		{Name: "Trade", Ref: bot, Role: "bot", Charter: protocol.Charter{Purpose: "Places paper trades", Accepts: "trade ideas"},
			SessionCount: 1, Sessions: []protocol.GraphSession{{ID: "s_bot", Name: "Trade", Status: "idle"}},
			Bot: &protocol.NodeBot{Client: "Cursor", ConnectedAt: t0.Add(-48 * time.Hour).UnixNano(), CanCall: []string{"ingest-forge"}}},
	}}
	for _, member := range []*protocol.GraphRef{ref("agent", "strategist"), ref("repo", "ingest-forge"), bot} {
		v.Edges = append(v.Edges, protocol.Edge{FromRef: member, ToRef: ref("team", "forge"), Kind: "member_of"})
	}
	v.Edges = append(v.Edges, protocol.Edge{FromRef: bot, ToRef: ref("repo", "ingest-forge"), Kind: "calls"})
	m := mapModel(w, h, v)
	m.snap.Agents = []protocol.AgentView{
		{SessionID: "lead", AgentID: "strategist", Profile: protocol.Profile{Name: "strategy lead"}, Hostname: "desk", Online: true, Status: "idle"},
		{SessionID: "s_bot", AgentID: bot.ID, Harness: "bot", Profile: protocol.Profile{Name: "Trade"}, Online: true, Status: "idle"},
		{SessionID: "ingest", Repo: protocol.RepoInfo{Project: "ingest-forge"}, Online: true, Status: "idle"},
	}
	m.applyMap(mapMsg{view: v, at: t0})
	m.tab = graphTab
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: graphRefKey(ref("team", "forge"))})
	m = keys(m, "f", "f")
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: graphRefKey(bot)})
	return m
}

func TestGraphBotNodeShowsMarkerCardAndCallsEdge(t *testing.T) {
	m := botTeamModel(130, 38)
	size := graphview.Size{Width: 130, Height: 38}
	scene := m.graphScene(m.graphState, size)
	rendered := strings.Join((graphview.Braille{}).Render(scene, size).Lines, "\n")
	for _, want := range []string{
		"⚙ Trade", "Trade  ", "CLIENT  Cursor", "CONNECTED  ", "LAST USED  never", "can call: ingest-forge",
		"CHARTER", "A bot reads its charter only if",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "YOUR CHOICES") {
		t.Error("a bot card shows repo choices")
	}
	var calls bool
	for _, e := range scene.Edges {
		calls = calls || e.Kind == "calls"
	}
	if !calls {
		t.Error("the calls edge is not drawn")
	}
	// The strategist is not a bot: no marker, and its card keeps its choices.
	if strings.Contains(rendered, "⚙ strategist") {
		t.Error("a plain agent got the bot marker")
	}
}

func TestGraphBotCannotBePickedAsLead(t *testing.T) {
	m := botTeamModel(130, 38)
	m = m.startLeadPicker(protocol.GraphRef{Kind: "team", ID: "forge"}, "forge")
	for _, c := range m.leadPicker.choices {
		if strings.Contains(c.name, "Trade") {
			t.Fatalf("the bot is offered as a lead: %q", c.name)
		}
	}
}

func TestGraphBotGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := botTeamModel(size[0], size[1])
		golden(t, fmt.Sprintf("graph_bot_inside_%dx%d", size[0], size[1]), m.testView())
	}
}
