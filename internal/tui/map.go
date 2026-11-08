package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

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
	merged         map[string]protocol.Node
	aliases        map[string]string
	dormant        map[string]bool
	removed        map[string]bool
	everything     map[string]string
	outside        map[string]bool
	teamInside     bool
	teamCardScroll int
	showDormant    bool
	focusNode      string
	levels         []graphLevel
	removal        *graphRemoval
	labels         map[string]string
	nodes          []protocol.Node
	edges          []protocol.Edge
	changes        []protocol.CharterChange
	// full retains the typed read. nodes and edges retain the merged card data.
	full             protocol.GraphView
	teams            []teamEntry
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
	draftFor         string
	draftIndex       int
}

func (m Model) mapCmd() tea.Cmd {
	src, now := m.src, m.now
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		at := now()
		view, err := src.GraphView(ctx, protocol.QueryGraph{AutoTyped: true})
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
	m.mp.full = m.prepareGraph(r.view)
	m.mp.teams = teamsOf(r.view)
	if m.teamFilter != "" {
		if _, ok := m.teamByID(m.teamFilter); !ok {
			m.teamFilter = ""
		}
	}
	m.mp.changes = m.mp.full.Changes
	m.mp.at, m.mp.loaded, m.mp.err = r.at, true, ""
	m.applyTeamView(false)
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
func (m Model) mapKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := keyAction(contextCard, k.String())
	switch key {
	case "edit charter", "global charter":
		n, ok := m.mapNode(m.mp.selected)
		if key == "global charter" {
			n = protocol.Node{Ref: &protocol.GraphRef{Kind: "global"}}
			ok = true
		}
		if ok {
			m.startCharterForm(n)
			return m, m.charterForm.input.Focus()
		}
		return m, nil

	case "back":
		m.mapOn = false
		m.uiFilter = ""
		m.graphGeneration++
		return m, graphTick(m.graphGeneration)
	case "sessions":
		m.mapOn, m.tab, m.focus = false, 0, focusSessions
		m.filter = m.mp.selected
		if node, ok := m.mapNode(m.mp.selected); ok && node.Ref != nil {
			m.filter = strings.TrimPrefix(m.graphName(node.Name), node.Ref.Kind+" ")
		}
		m.input.SetValue(m.filter)
		m.ensureSelection()
		m.fixScroll()
	case actQuit:
		m.quitting = true
		return m, tea.Quit
	case "lead":
		if node, ok := m.mapNode(m.mp.selected); ok && node.Ref != nil && node.Ref.Kind == "repo" {
			return m.startLeadPicker(*node.Ref, m.graphName(node.Name)), nil
		}
		m.fail("Select a repo first.")
		return m, nil
	case "node tools":
		return m.startNodeTools(m.mp.selected)
	case "autonomy":
		return m.toggleAutonomy()
	case "rename project":
		return m.startProjectRename()
	case "new session":
		if node, ok := m.mapNode(m.mp.selected); ok && node.Kind == "component" {
			return m.startSessionFormFromNode()
		}
		return m, nil
	case "undo learned":
		return m, m.mapUndoLearned()
	case "accept draft":
		return m, m.mapDraftDecide("accept")
	case "reject draft":
		return m, m.mapDraftDecide("reject")
	case "previous learned", "next learned":
		// A card with drafts uses the same keys to choose among them.
		if node, ok := m.mapNode(m.mp.selected); ok {
			if n := len(m.mapDrafts(node)); n > 0 {
				if m.mp.draftFor != node.Name {
					m.mp.draftFor, m.mp.draftIndex = node.Name, 0
				}
				step := 1
				if key == "previous learned" {
					step = n - 1
				}
				m.mp.draftIndex = (m.mp.draftIndex + step) % n
				return m, nil
			}
		}
		changes := m.mapLearned(m.mp.selected)
		if len(changes) > 0 {
			if m.mp.learnedFor != m.mp.selected {
				m.mp.learnedFor, m.mp.learnedIndex = m.mp.selected, 0
			}
			step := 1
			if key == "previous learned" {
				step = len(changes) - 1
			}
			m.mp.learnedIndex = (m.mp.learnedIndex + step) % len(changes)
		}
	case "help":
		m.openHelp()
	case "hide project":
		return m.toggleHiddenNode()
	case "show hidden":
		m.showHiddenNodes = !m.showHiddenNodes
	case actPageDown:
		m.mapScrollCharter(3)
	case actPageUp:
		m.mapScrollCharter(-3)
	case "refresh":
		if !m.mp.loading {
			m.mp.loading = true
			return m, m.mapCmd()
		}
	}
	return m, nil
}

// autonomySource is optional for compatibility with older sources.
type autonomySource interface {
	SetNodeAutonomy(context.Context, string, string) error
}

// toggleAutonomy flips the selected project between off and wake. It is an
// owner action; the source uses the owner-only node settings method.
func (m Model) toggleAutonomy() (Model, tea.Cmd) {
	node, ok := m.mapNode(m.mp.selected)
	if !ok || node.Kind != "component" {
		m.fail("Pick a project to change autonomy.")
		return m, nil
	}
	src, ok := m.src.(autonomySource)
	if !ok {
		m.fail("Autonomy is unavailable: the daemon is older than this screen.")
		return m, nil
	}
	next := protocol.AutonomyWake
	if protocol.NodeAutonomy(node.Settings) == protocol.AutonomyWake {
		next = protocol.AutonomyOff
	}
	name := node.Name
	// Show the new rule at once; the next graph refresh confirms it.
	nodes := append([]protocol.Node(nil), m.mp.nodes...)
	for i := range nodes {
		if nodes[i].Name != name {
			continue
		}
		settings := map[string]string{}
		for k, v := range nodes[i].Settings {
			settings[k] = v
		}
		settings[protocol.SettingAutonomy] = next
		nodes[i].Settings = settings
	}
	m.mp.nodes = nodes
	m.say(fmt.Sprintf("Saving autonomy %s for %s...", next, name))
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		if err := src.SetNodeAutonomy(ctx, name, next); err != nil {
			return actionMsg{err: fmt.Errorf("Autonomy not changed: %s", explain(err))}
		}
		return actionMsg{text: fmt.Sprintf("Autonomy %s for %s.", next, name)}
	}
}

// mapProjectNodes returns the visible nodes matching the network search.
func (m Model) mapProjectNodes() []protocol.Node {
	nodes := m.mapVisibleNodes()
	if m.mp.finding && m.graphState.Graph != nil {
		visible := map[string]bool{}
		for _, n := range m.graphState.Graph.Nodes {
			visible[n.Name] = true
		}
		filtered := nodes[:0]
		for _, n := range nodes {
			if visible[n.Name] {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}

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
		if (m.uiFilter == "" || strings.Contains(strings.ToLower(m.graphName(n.Name)), strings.ToLower(m.uiFilter))) && (!m.mp.finding || (strings.Contains(strings.ToLower(n.Name), strings.ToLower(m.mp.findQuery)) || strings.Contains(strings.ToLower(m.graphName(n.Name)), strings.ToLower(m.mp.findQuery)))) {
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
	for _, n := range m.mp.full.Nodes {
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
