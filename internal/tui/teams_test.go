package tui

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

func teamActive() *bool { b := true; return &b }

func teamRef(kind, id string) *protocol.GraphRef {
	return &protocol.GraphRef{Kind: kind, ID: id}
}

// teamGraphFixture is a typed read with two Teams, three Repos and one Agent.
// Alpha owns two members, Beta one, and homelab has no Team.
func teamGraphFixture() protocol.GraphView {
	return protocol.GraphView{
		Nodes: []protocol.Node{
			{Ref: teamRef("team", "alpha"), Name: "Alpha", Kind: "team", Active: teamActive(), Charter: protocol.Charter{Purpose: "Shared API work"}},
			{Ref: teamRef("team", "beta"), Name: "Beta", Kind: "team", Active: teamActive(), Charter: protocol.Charter{Purpose: "Storefront work"}},
			{Ref: teamRef("repo", "cogforge"), Name: "cogforge", Kind: "component"},
			{Ref: teamRef("repo", "shop"), Name: "shop", Kind: "component"},
			{Ref: teamRef("repo", "homelab"), Name: "homelab", Kind: "component"},
			{Ref: teamRef("agent", "api-architect"), Name: "api-architect", Kind: "architect"},
		},
		Edges: []protocol.Edge{
			{From: "cogforge", To: "alpha", Kind: "member_of", FromRef: teamRef("repo", "cogforge"), ToRef: teamRef("team", "alpha")},
			{From: "api-architect", To: "alpha", Kind: "member_of", FromRef: teamRef("agent", "api-architect"), ToRef: teamRef("team", "alpha")},
			{From: "shop", To: "beta", Kind: "member_of", FromRef: teamRef("repo", "shop"), ToRef: teamRef("team", "beta")},
			{From: "api-architect", To: "cogforge", Kind: "governs", FromRef: teamRef("agent", "api-architect"), ToRef: teamRef("repo", "cogforge")},
			{From: "cogforge", To: "shop", Kind: "depends", FromRef: teamRef("repo", "cogforge"), ToRef: teamRef("repo", "shop")},
		},
	}
}

func teamModel(w, h int) (Model, *fakeSource) {
	m := mapModel(w, h, teamGraphFixture())
	m.applyMap(mapMsg{view: teamGraphFixture(), at: t0})
	m.tab = graphTab
	gw, gh := m.graphSize()
	m.graphState.Camera = m.graphState.Graph.Fit(gw, gh)
	return m, m.src.(*fakeSource)
}

// teamLargeGraphFixture has a five member Team and enough other nodes for the
// full Graph to have its normal spacing.
func teamLargeGraphFixture() protocol.GraphView {
	v := protocol.GraphView{
		Nodes: []protocol.Node{
			{Ref: teamRef("team", "crew"), Name: "Crew", Kind: "team", Active: teamActive(), Charter: protocol.Charter{Purpose: "Shared platform work"}},
			{Ref: teamRef("team", "other"), Name: "Other", Kind: "team", Active: teamActive(), Charter: protocol.Charter{Purpose: "Other work"}},
			{Ref: teamRef("repo", "crew-a"), Name: "crew-a", Kind: "component"},
			{Ref: teamRef("repo", "crew-b"), Name: "crew-b", Kind: "component"},
			{Ref: teamRef("repo", "crew-c"), Name: "crew-c", Kind: "component"},
			{Ref: teamRef("agent", "ops"), Name: "ops", Kind: "architect"},
			{Ref: teamRef("agent", "qa"), Name: "qa", Kind: "architect"},
			{Ref: teamRef("repo", "other-a"), Name: "other-a", Kind: "component"},
			{Ref: teamRef("repo", "other-b"), Name: "other-b", Kind: "component"},
			{Ref: teamRef("repo", "other-c"), Name: "other-c", Kind: "component"},
			{Ref: teamRef("agent", "other-d"), Name: "other-d", Kind: "architect"},
		},
	}
	for _, member := range []*protocol.GraphRef{
		teamRef("repo", "crew-a"), teamRef("repo", "crew-b"), teamRef("repo", "crew-c"),
		teamRef("agent", "ops"), teamRef("agent", "qa"),
	} {
		v.Edges = append(v.Edges, protocol.Edge{From: member.ID, To: "crew", Kind: "member_of", FromRef: member, ToRef: teamRef("team", "crew")})
	}
	for _, member := range []*protocol.GraphRef{
		teamRef("repo", "other-a"), teamRef("repo", "other-b"), teamRef("repo", "other-c"), teamRef("agent", "other-d"),
	} {
		v.Edges = append(v.Edges, protocol.Edge{From: member.ID, To: "other", Kind: "member_of", FromRef: member, ToRef: teamRef("team", "other")})
	}
	v.Edges = append(v.Edges,
		protocol.Edge{From: "crew-a", To: "crew-b", Kind: "depends", FromRef: teamRef("repo", "crew-a"), ToRef: teamRef("repo", "crew-b")},
		protocol.Edge{From: "ops", To: "crew-c", Kind: "governs", FromRef: teamRef("agent", "ops"), ToRef: teamRef("repo", "crew-c")},
		protocol.Edge{From: "crew-b", To: "crew-c", Kind: "depends", FromRef: teamRef("repo", "crew-b"), ToRef: teamRef("repo", "crew-c")},
		protocol.Edge{From: "crew-a", To: "other-a", Kind: "depends", FromRef: teamRef("repo", "crew-a"), ToRef: teamRef("repo", "other-a")},
	)
	return v
}

func teamLargeModel(w, h int) (Model, *fakeSource) {
	m := mapModel(w, h, teamLargeGraphFixture())
	m.applyMap(mapMsg{view: teamLargeGraphFixture(), at: t0})
	m.tab = graphTab
	return m, m.src.(*fakeSource)
}

func TestTeamFilterShowsOnlyOneTeamsMembers(t *testing.T) {
	m, _ := teamModel(120, 34)
	m.setTeamFilter("alpha")
	got := map[string]bool{}
	for _, n := range m.graphState.Graph.Nodes {
		got[n.Name] = true
	}
	for _, want := range []string{"cogforge", "agent\x00api-architect"} {
		if !got[want] {
			t.Errorf("alpha filter dropped %q: %v", want, got)
		}
	}
	for _, want := range []string{"team\x00alpha", "team\x00beta", "shop", "homelab"} {
		if got[want] {
			t.Errorf("alpha filter kept %q: %v", want, got)
		}
	}
	if len(m.graphState.Graph.Edges) != 1 {
		t.Fatalf("alpha has %d edges, want 1: %+v", len(m.graphState.Graph.Edges), m.graphState.Graph.Edges)
	}
	for _, e := range m.graphState.Graph.Edges {
		if !got[e.From] || !got[e.To] {
			t.Errorf("edge leaves the team: %+v", e)
		}
	}
	if _, ok := m.mapNode("shop"); ok {
		t.Error("the card still lists a member of another team")
	}
	w, h := m.graphSize()
	if m.mp.baseZoom <= 0 || m.graphState.Camera.Zoom > m.mp.baseZoom {
		t.Errorf("filtered zoom %v is not capped at the full graph zoom %v", m.graphState.Camera.Zoom, m.mp.baseZoom)
	}
	for _, name := range []string{"cogforge", "agent\x00api-architect"} {
		x, y := m.graphState.Camera.Project(m.graphState.Graph.Positions[name], w, h)
		if math.Abs(x-float64(w)/2) > float64(w)*0.3 || math.Abs(y-float64(h)/2) > float64(h)*0.3 {
			t.Errorf("%s is not near the middle: %.1f,%.1f in %dx%d", name, x, y, w, h)
		}
	}
	if !strings.Contains(ansi.Strip(m.testView()), "team: Alpha") {
		t.Error("the Graph footer does not name the Team")
	}
}

func TestTeamFilterKeyCyclesAllThenActiveTeams(t *testing.T) {
	m, _ := teamModel(120, 34)
	for _, want := range []string{"alpha", "beta", ""} {
		m = keys(m, "t")
		if m.teamFilter != want {
			t.Fatalf("after t filter is %q, want %q", m.teamFilter, want)
		}
		if want == "" {
			if !strings.Contains(ansi.Strip(m.testView()), "team: all") {
				t.Error("footer does not say all")
			}
		}
	}
}

func TestTeamCreateFormWritesTeam(t *testing.T) {
	m, src := teamModel(80, 24)
	m = keys(m, "T")
	if m.mode != modeTeams {
		t.Fatalf("T opened mode %v", m.mode)
	}
	m = keys(m, "c")
	if m.mode != modeTeamForm {
		t.Fatalf("c opened mode %v", m.mode)
	}
	m = typeText(m, "Alpha team")
	m = keys(m, "tab")
	m = typeText(m, "Shared API work")
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter gave no command")
	}
	m, _ = press2(m, cmd())
	if len(src.teamWrites) != 1 {
		t.Fatalf("wrote %d Teams", len(src.teamWrites))
	}
	q := src.teamWrites[0]
	if q.Action != "create" || q.ID != "Alpha team" || q.Name != "Alpha team" || q.Purpose != "Shared API work" {
		t.Fatalf("create request %+v", q)
	}
	if m.notice.err || !strings.Contains(m.notice.text, "Created team Alpha team.") {
		t.Fatalf("result line %+v", m.notice)
	}
}

func TestTeamWriteCutoverErrorIsClear(t *testing.T) {
	m, src := teamModel(80, 24)
	m = keys(m, "T", "c")
	m = typeText(m, "Alpha team")
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	// Enter on Name moves to Purpose, a second enter submits.
	m, cmd = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	src.teamErr = errors.New("typed Graph cutover has not been enabled")
	m, _ = press2(m, cmd())
	if !m.notice.err || !strings.Contains(m.notice.text, "typed Graph cutover") {
		t.Fatalf("cutover error was not clear: %+v", m.notice)
	}
}

func TestTeamMemberToggleWritesMembership(t *testing.T) {
	t.Run("add", func(t *testing.T) {
		m, src := teamModel(120, 34)
		m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "homelab"})
		m = keys(m, "T", "down")
		m, cmd := press(m, spaceKey())
		if cmd == nil {
			t.Fatal("space gave no command")
		}
		m, _ = press2(m, cmd())
		q := src.teamWrites[0]
		if q.Action != "membership" || q.ID != "beta" || q.ExpectTeam != "" || q.Member == nil || q.Member.Kind != "repo" || q.Member.ID != "homelab" {
			t.Fatalf("membership request %+v", q)
		}
		if !strings.Contains(m.notice.text, "Added repo homelab to Beta.") {
			t.Fatalf("result line %+v", m.notice)
		}
	})
	t.Run("remove", func(t *testing.T) {
		m, src := teamModel(120, 34)
		m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "cogforge"})
		m = keys(m, "T")
		m, cmd := press(m, spaceKey())
		if cmd == nil {
			t.Fatal("space gave no command")
		}
		m, _ = press2(m, cmd())
		q := src.teamWrites[0]
		if q.Action != "membership" || q.ID != "" || q.ExpectTeam != "alpha" || q.Member == nil || q.Member.ID != "cogforge" {
			t.Fatalf("membership request %+v", q)
		}
		if !strings.Contains(m.notice.text, "Removed repo cogforge from Alpha.") {
			t.Fatalf("result line %+v", m.notice)
		}
	})
}

func TestTeamMenuRenameAndArchiveWrite(t *testing.T) {
	t.Run("rename", func(t *testing.T) {
		m, src := teamModel(80, 24)
		m = keys(m, "T", "r")
		if m.mode != modeTeamForm {
			t.Fatalf("r opened mode %v", m.mode)
		}
		m.teamForm.in[0].SetValue("Gamma")
		m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("enter gave no command")
		}
		m, _ = press2(m, cmd())
		q := src.teamWrites[0]
		if q.Action != "rename" || q.ID != "alpha" || q.Name != "Gamma" || q.ExpectName != "Alpha" {
			t.Fatalf("rename request %+v", q)
		}
		if !strings.Contains(m.notice.text, "Renamed team Alpha to Gamma.") {
			t.Fatalf("result line %+v", m.notice)
		}
	})
	t.Run("archive", func(t *testing.T) {
		m, src := teamModel(80, 24)
		m = keys(m, "T")
		m, cmd := press(m, tea.KeyPressMsg{Text: "a", Code: 'a'})
		if cmd == nil {
			t.Fatal("a gave no command")
		}
		m, _ = press2(m, cmd())
		q := src.teamWrites[0]
		if q.Action != "archive" || q.ID != "alpha" {
			t.Fatalf("archive request %+v", q)
		}
		if !strings.Contains(m.notice.text, "Archived team Alpha.") {
			t.Fatalf("result line %+v", m.notice)
		}
	})
}

func TestTeamGraphGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, _ := teamModel(size[0], size[1])
		m.setTeamFilter("alpha")
		m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "cogforge"})
		golden(t, fmt.Sprintf("graph_team_alpha_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestTeamLargeGraphGolden(t *testing.T) {
	m, _ := teamLargeModel(80, 24)
	m.setTeamFilter("crew")
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "crew-a"})
	if got := len(m.graphState.Graph.Nodes); got != 5 {
		t.Fatalf("crew has %d nodes, want 5", got)
	}
	golden(t, "graph_team_crew_80x24", m.testView())
}

func TestTeamMemberGolden(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, _ := teamModel(size[0], size[1])
		m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "homelab"})
		m = keys(m, "T", "down")
		golden(t, fmt.Sprintf("team_member_add_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestTeamCreateFormGolden(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, _ := teamModel(size[0], size[1])
		m = keys(m, "T", "c")
		m = typeText(m, "Alpha team")
		m = keys(m, "tab")
		m = typeText(m, "Shared API work")
		golden(t, fmt.Sprintf("team_create_%dx%d", size[0], size[1]), m.testView())
	}
}
