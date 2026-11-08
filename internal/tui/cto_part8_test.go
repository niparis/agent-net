package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

const ctoTestSession = "s_00000000000000c0"

// ctoSnapshot is the fixture with the CTO network session in the Mac group.
func ctoSnapshot() Snapshot {
	snap := fakeSnapshot()
	snap.Agents = append(snap.Agents, agent(ctoTestSession, "n_mac", "Mac", "cto", "CTO", func(a *protocol.AgentView) {
		a.Title = "CTO"
		a.Status = "idle"
	}))
	return snap
}

// ctoPart8Model is the Sessions screen with the CTO row and its box.
func ctoPart8Model(w, h int, title string, proposals int) Model {
	snap := ctoSnapshot()
	src := &fakeCTOSource{fakeSource: &fakeSource{snap: snap, events: fakeEvents(), usage: fakeUsage(), counts: fakeCounts(), tops: fakeTops()}}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	m = settle(refresh(m, src.fakeSource))
	m.src = src
	m.cto = &ctoState{generation: 1, title: title, harness: "opencode", model: "fake/m1"}
	for i := 0; i < proposals; i++ {
		m.proposals = append(m.proposals, protocol.CTOProposal{ID: fmt.Sprintf("p_%d", i), NodeID: "n_mac", Status: "open",
			Tool: "edge_set", Action: "edge_set", Args: json.RawMessage(fmt.Sprintf(`{"from":"tenebrae","to":"cogforge","kind":"depends","rule":"auto","n":%d}`, i))})
	}
	return quietFixtures(m)
}

func TestCTOSessionsRowShowsTheOwnersTitle(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := ctoPart8Model(size[0], size[1], "Atlas", 0)
		m.filter = "CTO" // keep the machine group and the one row in view
		m.ensureSelection()
		m.fixScroll()
		view := m.testView()
		plain := ansi.Strip(view)
		if !strings.Contains(plain, "Atlas") {
			t.Fatalf("%dx%d: the row does not show the owner's title:\n%s", size[0], size[1], plain)
		}
		if !strings.Contains(plain, "Mac (here)") {
			t.Fatalf("%dx%d: the CTO row left its machine group:\n%s", size[0], size[1], plain)
		}
		golden(t, fmt.Sprintf("sessions_cto_%dx%d", size[0], size[1]), view)
	}
}

func TestCTOBoxShowsOpenProposals(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := ctoPart8Model(size[0], size[1], "Atlas", 2)
		m.focus = focusCTO
		m.cto.messages = []string{"you: hello", "cto: hi"}
		view := m.testView()
		if !strings.Contains(ansi.Strip(view), "2 open proposals") {
			t.Fatalf("%dx%d: the box does not show the proposal count:\n%s", size[0], size[1], ansi.Strip(view))
		}
		golden(t, fmt.Sprintf("cto_box_proposals_%dx%d", size[0], size[1]), view)
	}
}

// ctoGraphFixture has the fixed CTO node, an edge to it with a request rule,
// and one approved and one plain CTO message.
func ctoGraphFixture() protocol.GraphView {
	return protocol.GraphView{
		Nodes: []protocol.Node{
			{Name: "project-01", Kind: "component", SessionCount: 1, Sessions: []protocol.GraphSession{{ID: "session-1", Status: "idle"}}},
			{Name: "project-02", Kind: "component", SessionCount: 1, Sessions: []protocol.GraphSession{{ID: "session-2", Status: "idle"}}},
			{Name: protocol.CTONodeName, Kind: "cto"},
		},
		Edges: []protocol.Edge{
			{From: "project-01", To: protocol.CTONodeName, Kind: "depends", Rule: "ask"},
			{From: "project-01", To: "project-02", Kind: "depends", Rule: "normal"},
		},
	}
}

func ctoGraphModel(w, h int) Model {
	m := mapModel(w, h, ctoGraphFixture())
	m.applyMap(mapMsg{view: ctoGraphFixture(), at: t0})
	m.tab = graphTab
	m.graphState, _ = graphview.Update(m.graphState, graphview.TalkLoaded{Counts: []protocol.GraphTalkCount{
		{From: protocol.CTONodeName, To: "project-01", Count: 3, HasEdge: true, Voice: true},
		{From: protocol.CTONodeName, To: "project-02", Count: 5, HasEdge: false},
	}})
	gw, gh := m.graphSize()
	m.graphState.Camera = m.graphState.Graph.Fit(gw, gh)
	m.graphState.Graph.Message(protocol.CTONodeName, "project-01", "request", t0.Add(-time.Second), true)
	m.graphState.Graph.Message(protocol.CTONodeName, "project-02", "request", t0.Add(-2*time.Second))
	return m
}

func TestGraphShowsTheCTONodeAndItsRequestRule(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := ctoGraphModel(size[0], size[1])
		plain := ansi.Strip(m.testView())
		if !strings.Contains(plain, protocol.CTONodeName) {
			t.Fatalf("%dx%d: no CTO node:\n%s", size[0], size[1], plain)
		}
		if strings.Contains(plain, "…") {
			t.Fatalf("%dx%d: a graph label is cut:\n%s", size[0], size[1], plain)
		}
		golden(t, fmt.Sprintf("graph_cto_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestCTOCardShowsTheRequestRule(t *testing.T) {
	m := ctoGraphModel(120, 34)
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: protocol.CTONodeName})
	m.mp.selected = protocol.CTONodeName
	m.mapOn = true
	view := ansi.Strip(m.testView())
	if !strings.Contains(view, "project-01") || !strings.Contains(view, "ask") {
		t.Fatalf("the CTO card does not show the incoming rule:\n%s", view)
	}
}

func TestEnterOnTheCTORowAndBoxGiveTheSamePlan(t *testing.T) {
	snap := ctoSnapshot()
	src := &fakeCTOSource{fakeSource: &fakeSource{snap: snap, enterPlan: protocol.EnterPlan{Argv: []string{"fake-cto"}, Note: "same"}}}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.snap = snap
	m.cto = &ctoState{generation: 1, title: "Atlas", harness: "opencode", model: "fake/m1"}
	m.activeOnly = false
	m.selected = ctoTestSession
	m.ensureSelection()

	_, rowCmd := m.startEnter()
	if rowCmd == nil {
		t.Fatal("Enter on the CTO row did not ask for a plan")
	}
	row := rowCmd().(enterMsg)

	boxModel := m
	boxModel.focus = focusCTO
	_, boxCmd := boxModel.ctoKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if boxCmd == nil {
		t.Fatal("Enter on the CTO box did not ask for a plan")
	}
	box := boxCmd().(enterMsg)

	if len(src.enters) != 2 || src.enters[0].SessionID != ctoTestSession || src.enters[1].SessionID != ctoTestSession {
		t.Fatalf("session_enter requests: %+v", src.enters)
	}
	if fmt.Sprint(row.plan.Argv) != fmt.Sprint(box.plan.Argv) || row.plan.Note != box.plan.Note {
		t.Fatalf("different plans: row %+v box %+v", row.plan, box.plan)
	}
	if !row.cto || !box.cto {
		t.Fatal("the plan is not marked as the CTO's")
	}
}
