package tui

import (
	"context"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

// The selected agent card shares graph data with the network screen.

type mapMsg struct {
	view protocol.GraphView
	err  error
	at   time.Time
}

// mapState holds shared network data and the selected card state.
type mapState struct {
	nodes            []protocol.Node
	edges            []protocol.Edge
	changes          []protocol.CharterChange
	at               time.Time
	loaded           bool
	loading          bool
	err              string
	selected         string
	pendingSelection string
	finding          bool
	findQuery        string
	charterFor       string
	charterScroll    int
	learnedFor       string
	learnedIndex     int
}

func (m Model) mapCmd() tea.Cmd {
	src, now := m.src, m.now
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		at := now()
		view, err := src.GraphView(ctx, protocol.QueryGraph{})
		return mapMsg{view: view, err: err, at: at}
	}
}

// applyMap keeps the last graph when a read fails, like the rest of the screen
// keeps its last snapshot.
func (m *Model) applyMap(r mapMsg) {
	m.mp.loading = false
	if r.err != nil {
		m.mp.err = mapError(r.err)
		return
	}
	first := m.graphState.Graph == nil
	m.graphState, _ = graphview.Update(m.graphState, graphview.GraphChanged{Graph: r.view})
	if first {
		w, h := m.graphSize()
		m.graphState, _ = graphview.Update(m.graphState, graphview.Fit{Viewport: graphview.Viewport{Width: float64(w), Height: float64(h)}})
	}
	m.mp.nodes, m.mp.edges, m.mp.changes = r.view.Nodes, r.view.Edges, r.view.Changes
	m.mp.at, m.mp.loaded, m.mp.err = r.at, true, ""
	m.mapEnsureSelection()
	if m.mp.pendingSelection != "" {
		name := m.mp.selected
		if m.mp.pendingSelection != "" {
			name = m.mp.pendingSelection
		}
		m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: name})
		m.mp.pendingSelection = ""
	}
}

// mapKey handles the selected agent card. Esc returns to the network.
func (m Model) mapKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mapOn = false
		m.uiFilter = ""
		m.graphGeneration++
		return m, graphTick(m.graphGeneration)
	case "enter":
		m.mapOn, m.tab, m.focus = false, 0, focusSessions
		m.filter = m.mp.selected
		m.input.SetValue(m.filter)
		m.ensureSelection()
		m.fixScroll()
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "D":
		return m.startProjectRename()
	case "u":
		return m, m.mapUndoLearned()
	case "U":
		changes := m.mapLearned(m.mp.selected)
		if len(changes) > 0 {
			if m.mp.learnedFor != m.mp.selected {
				m.mp.learnedFor, m.mp.learnedIndex = m.mp.selected, 0
			}
			m.mp.learnedIndex = (m.mp.learnedIndex + 1) % len(changes)
		}
	case "?":
		m.help = true
	case "c":
		if m.cto != nil {
			m.focus = focusCTO
		}
	case "h":
		return m.toggleHiddenNode()
	case "H":
		m.showHiddenNodes = !m.showHiddenNodes
	case "]", "ctrl+d", "pgdown":
		m.mapScrollCharter(3)
	case "[", "ctrl+u", "pgup":
		m.mapScrollCharter(-3)
	case "r":
		if !m.mp.loading {
			m.mp.loading = true
			return m, m.mapCmd()
		}
	}
	return m, nil
}

// mapProjectNodes returns the visible nodes matching the network search.
func (m Model) mapProjectNodes() []protocol.Node {
	nodes := m.mapVisibleNodes()
	sort.SliceStable(nodes, func(i, j int) bool {
		ai := nodes[i].Kind == "architect"
		aj := nodes[j].Kind == "architect"
		if ai != aj {
			return ai
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	var out []protocol.Node
	for _, n := range nodes {
		if (m.uiFilter == "" || strings.Contains(strings.ToLower(n.Name), strings.ToLower(m.uiFilter))) && (!m.mp.finding || (strings.Contains(strings.ToLower(n.Name), strings.ToLower(m.mp.findQuery)) || strings.Contains(strings.ToLower(m.projectName(n.Name)), strings.ToLower(m.mp.findQuery)))) {
			out = append(out, n)
		}
	}
	return out
}
func (m Model) mapSelectable() []string {
	var out []string
	for _, n := range m.mapProjectNodes() {
		out = append(out, n.Name)
	}
	return out
}

func (m *Model) mapEnsureSelection() {
	for _, name := range m.mapSelectable() {
		if name == m.mp.selected {
			return
		}
	}
	list := m.mapSelectable()
	if len(list) == 0 {
		m.mp.selected = ""
		return
	}
	degree := map[string]int{}
	for _, e := range m.mapVisibleEdges() {
		if _, ok := m.mapNode(e.From); ok {
			if _, ok := m.mapNode(e.To); ok {
				degree[e.From]++
				degree[e.To]++
			}
		}
	}
	connected := false
	for _, name := range list {
		if degree[name] > 0 {
			connected = true
			break
		}
	}
	allowed := map[string]bool{}
	for _, name := range list {
		if !connected || degree[name] > 0 {
			allowed[name] = true
		}
	}
	var newest int64
	choice := ""
	found := false
	for _, c := range m.mp.changes {
		if allowed[c.Node] && (!found || c.At > newest) {
			newest = c.At
			choice = c.Node
			found = true
		}
	}
	if found {
		m.mp.selected = choice
		return
	}
	best := -1
	for _, name := range list {
		if allowed[name] && degree[name] > best {
			best = degree[name]
			choice = name
		}
	}
	m.mp.selected = choice
}

func (m Model) mapSelectedName() string { return m.mp.selected }

func (m Model) mapNode(name string) (protocol.Node, bool) {
	for _, n := range m.mp.nodes {
		if n.Name == name {
			return n, true
		}
	}
	return protocol.Node{}, false
}

// maturityLevel is the level word of a maturity value, or "-" when there is
// none or it is not one this version knows.
func maturityLevel(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	level, _, err := protocol.ParseMaturity(value)
	if err != nil {
		return "-"
	}
	return level
}

func mapError(err error) string {
	switch {
	case isUnavailable(err):
		return "the daemon or the gateway is too old for the graph (graph_view)"
	case ClassifyError(err) == ProblemDaemon:
		return "the daemon cannot be reached"
	case ClassifyError(err) == ProblemGateway:
		return "the gateway cannot be reached"
	}
	return sanitize(err.Error())
}
