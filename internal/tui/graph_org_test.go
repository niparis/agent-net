package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

// The all teams view names the staff above the teams and shows each team's
// authority beside its lead, with the held back count.
func TestTeamOverviewShowsStaffAndAuthority(t *testing.T) {
	m := mockTeamsModel(120, 38)
	v := m.src.(*fakeSource).graph
	v.Nodes = append(v.Nodes, protocol.Node{Name: "loose", Ref: teamRef("repo", "loose")})
	m.snap.Agents = append(m.snap.Agents, protocol.AgentView{SessionID: "loose", Repo: protocol.RepoInfo{Project: "loose"}, Online: true, Status: "idle"})
	m.applyMap(mapMsg{view: v, at: t0})
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: graphRefKey(teamRef("team", "forge"))})
	s := m.graphScene(m.graphState, graphview.Size{Width: 120, Height: 38})
	text := ansi.Strip(strings.Join((graphview.Braille{}).Render(s, graphview.Size{Width: 120, Height: 38}).Lines, "\n"))
	for _, want := range []string{
		"owner", "CTO busy", "chief of staff · not set up",
		"★ strategist · charter 1st", "held back: 2",
		"answers to owner", "☆ server · absolute",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("overview missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Desk") {
		t.Errorf("overview has a desk count:\n%s", text)
	}
}

// A focused team carries its lead, mode and held back in the frame title, and
// a repo lead has the smaller star.
func TestTeamFocusHeaderCarriesAuthority(t *testing.T) {
	m := mockTeamsModel(120, 38)
	m.setTeamFilter("forge")
	s := m.graphScene(m.graphState, graphview.Size{Width: 120, Height: 38})
	text := ansi.Strip(strings.Join((graphview.Braille{}).Render(s, graphview.Size{Width: 120, Height: 38}).Lines, "\n"))
	for _, want := range []string{"★ strategist · charter first", "held back: 2", "2 projects · 4 sessions"} {
		if !strings.Contains(text, want) {
			t.Errorf("focus missing %q:\n%s", want, text)
		}
	}
	m.setTeamFilter("ops")
	s = m.graphScene(m.graphState, graphview.Size{Width: 120, Height: 38})
	text = ansi.Strip(strings.Join((graphview.Braille{}).Render(s, graphview.Size{Width: 120, Height: 38}).Lines, "\n"))
	if !strings.Contains(text, "☆ server · absolute") {
		t.Errorf("repo lead has no smaller star:\n%s", text)
	}
}

// Enter on a team frame opens the team's own card, with its purpose, lead,
// mode, held back text, members and rules.
func TestTeamCardShowsPurposeLeadMembersAndRules(t *testing.T) {
	m := mockTeamsModel(120, 38)
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: graphRefKey(teamRef("team", "forge"))})
	m = keys(m, "enter")
	if !m.mapOn {
		t.Fatal("enter on a team frame did not open a card")
	}
	text := ansi.Strip(m.testView())
	for _, want := range []string{
		"forge", "purpose", "Shared forge work", "lead", "★ strategist",
		"mode", "charter first", "held back", "deploy to production",
		"Members", "ingest", "Team rules", "rules", "ask the team lead",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("team card missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "GOVERNS") || strings.Contains(text, "governs") {
		t.Errorf("team card still shows GOVERNS:\n%s", text)
	}
}

// The agent card has the place block on top, taken from the graph's place
// data, and every charter value names its level.
func TestAgentCardShowsPlaceAndCharterSources(t *testing.T) {
	agent := teamRef("agent", "research")
	team := teamRef("team", "forge")
	sources := protocol.CharterSources{
		"purpose": {{Level: "global", Value: "Research work"}},
		"accepts": {{Level: "team", Scope: "forge", Value: "studies"}},
	}
	v := protocol.GraphView{
		Nodes: []protocol.Node{
			{Ref: team, Name: "forge", Kind: "team", Active: teamActive(), Lead: agent, AuthorityMode: "charter_first", HeldBack: "deploy to production"},
			{Ref: agent, Name: "research", Kind: "architect", Charter: protocol.Charter{Purpose: "Research work", Accepts: "studies", Sources: &sources}, Place: &protocol.NodePlace{
				Team: "forge", TeamLead: "tenebrae-strats", Mode: "charter_first",
				Repo: "ingest-forge", RepoLead: "research", ReportsTo: "the forge lead",
				HeldBack: []protocol.PlaceHeldBack{{Text: "deploy to production", From: "team"}, {Text: "force pushes", From: "repo"}},
			}},
		},
		Edges: []protocol.Edge{{FromRef: agent, ToRef: team, Kind: "member_of", From: agent.ID, To: team.ID}},
	}
	m := openMap(t, mapModel(120, 40, v))
	m.mp.selected = graphRefKey(agent)
	text := ansi.Strip(m.testView())
	for _, want := range []string{
		"PLACE",
		"team       forge, lead ★ tenebrae-strats   mode charter first",
		"repo lead  ★ ingest-forge (research), reports to the forge lead",
		"held back  deploy to production (from team)",
		"force pushes (from repo)",
		"studies (team)", "Research work (global)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("agent card missing %q:\n%s", want, text)
		}
	}
}

// The Team card is one screen of its own, so its layout has a golden too.
func TestTeamCardGolden(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := mockTeamsModel(size[0], size[1])
		m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: graphRefKey(teamRef("team", "forge"))})
		m = keys(m, "enter")
		golden(t, fmt.Sprintf("graph_team_card_forge_%dx%d", size[0], size[1]), m.testView())
	}
}

// Orders are messages, never drawn edges. A team view draws only depends and
// deploys, and never a governs line.
func TestTeamViewNeverDrawsOrderOrGovernsEdges(t *testing.T) {
	m := mockTeamsModel(120, 38)
	agent, repo := graphRefKey(teamRef("agent", "strategist")), graphRefKey(teamRef("repo", "ingest"))
	m.mp.edges = append(m.mp.edges,
		protocol.Edge{From: agent, To: repo, Kind: "governs"},
		protocol.Edge{From: agent, To: repo, Kind: "order"})
	m.setTeamFilter("forge")
	m.mp.teamInside = true
	m.applyTeamView(true)
	s := m.graphScene(m.graphState, graphview.Size{Width: 120, Height: 38})
	for _, e := range s.Edges {
		if e.Kind != "depends" && e.Kind != "deploys" {
			t.Errorf("drew edge %q", e.Kind)
		}
	}
	text := ansi.Strip(strings.Join((graphview.Braille{}).Render(s, graphview.Size{Width: 120, Height: 38}).Lines, "\n"))
	for _, gone := range []string{"governs", "order"} {
		if strings.Contains(text, gone) {
			t.Errorf("team view shows %q:\n%s", gone, text)
		}
	}
}
