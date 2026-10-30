package tui

import (
	"context"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// The map is a read-only screen of the component graph (decision 0022, step
// 1c). It shows architects with the nodes they govern and the mode in
// brackets, then components with no architect, then architects with no nodes.
// The right side shows the selected node's charter, its edges and its newest
// changes. It reads through the owner-only graph_view operator method and
// changes nothing.

type mapMsg struct {
	view protocol.GraphView
	err  error
	at   time.Time
}

// mapState is the map's part of the model.
type mapState struct {
	nodes     []protocol.Node
	edges     []protocol.Edge
	changes   []protocol.CharterChange
	at        time.Time
	loaded    bool
	loading   bool
	err       string
	selected  string
	choice    int
	overview  bool
	finding   bool
	findQuery string
}

// mapRow is one line of the left column. A row with a node is selectable; a
// section row is a heading only.
type mapRow struct {
	section bool
	node    string
	text    string
	level   string
	note    string
	indent  int
	bold    bool
}

// startMap opens the map and reads the graph. The last data stays on screen
// while the read runs.
func (m Model) startMap() (tea.Model, tea.Cmd) {
	m.mapOn, m.tab = true, 3
	m.notice = notice{}
	if m.mp.loading {
		return m, nil
	}
	m.mp.loading = true
	return m, m.mapCmd()
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
	m.mp.nodes, m.mp.edges, m.mp.changes = r.view.Nodes, r.view.Edges, r.view.Changes
	m.mp.at, m.mp.loaded, m.mp.err = r.at, true, ""
	m.mapEnsureSelection()
}

// mapKey handles the keys of the map. M and esc return to the session list.
func (m Model) mapKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mp.finding {
		switch k.Type {
		case tea.KeyEsc:
			m.mp.finding = false
			m.mp.findQuery = ""
		case tea.KeyEnter:
			if nodes := m.mapProjectNodes(); len(nodes) > 0 {
				m.mp.selected = nodes[0].Name
				m.mp.choice = 0
			}
			m.mp.finding = false
			m.mp.findQuery = ""
		case tea.KeyBackspace, tea.KeyCtrlH:
			r := []rune(m.mp.findQuery)
			if len(r) > 0 {
				m.mp.findQuery = string(r[:len(r)-1])
			}
		case tea.KeyRunes:
			m.mp.findQuery += string(k.Runes)
		}
		return m, nil
	}
	switch k.String() {
	case "/":
		m.mp.finding = true
		m.mp.findQuery = ""
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "M", "esc":
		m.mapOn, m.tab = false, 0
		m.uiFilter = ""
	case "?":
		m.help = true
	case "left":
		m.mapFollow("in")
	case "right":
		m.mapFollow("out")
	case "up", "k":
		m.mapMove(-1)
	case "down", "j":
		m.mapMove(1)
	case "z":
		m.mp.overview = !m.mp.overview
	case "enter":
		m.mapOn, m.tab, m.focus = false, 0, focusSessions
		m.filter = m.mp.selected
		m.input.SetValue(m.filter)
		m.ensureSelection()
		m.fixScroll()
	case "c":
		if m.cto != nil {
			m.focus = focusCTO
			m.cto.editing = true
			m.cto.input.SetValue("about " + m.mp.selected + ": ")
			m.cto.input.CursorEnd()
			return m, m.cto.input.Focus()
		}
	case "pgup":
		m.mapMove(-m.mapBodyRows())
	case "pgdown":
		m.mapMove(m.mapBodyRows())
	case "home", "g":
		m.mapMove(-1 << 20)
	case "end", "G":
		m.mapMove(1 << 20)
	case "r":
		if !m.mp.loading {
			m.mp.loading = true
			return m, m.mapCmd()
		}
	}
	return m, nil
}

// mapBodyRows is how many rows the two columns get: the screen minus the title
// row and the key line.
func (m Model) mapBodyRows() int {
	return max(max(m.height, 10)-2, 1)
}

// mapRows groups the nodes: each architect that governs something with its
// nodes and their mode in brackets, then components with no architect, then
// architects without nodes. Every group and node is sorted by name.
func (m Model) mapRows() []mapRow {
	nodes := append([]protocol.Node(nil), m.mp.nodes...)
	sort.Slice(nodes, func(i, j int) bool {
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	byName := map[string]protocol.Node{}
	for _, n := range nodes {
		byName[n.Name] = n
	}
	edges := append([]protocol.Edge(nil), m.mp.edges...)
	sort.Slice(edges, func(i, j int) bool {
		if strings.ToLower(edges[i].To) != strings.ToLower(edges[j].To) {
			return strings.ToLower(edges[i].To) < strings.ToLower(edges[j].To)
		}
		return edges[i].Kind < edges[j].Kind
	})
	governed := map[string][]protocol.Edge{}
	for _, e := range edges {
		if e.Kind == "governs" {
			governed[e.From] = append(governed[e.From], e)
		}
	}
	var architects []protocol.Node
	for _, n := range nodes {
		if n.Kind == "architect" {
			architects = append(architects, n)
		}
	}
	var rows []mapRow
	for _, a := range architects {
		es := governed[a.Name]
		if len(es) == 0 {
			continue
		}
		rows = append(rows, mapRow{node: a.Name, text: a.Name, level: maturityLevel(a.Charter.Maturity), bold: true})
		for _, e := range es {
			rows = append(rows, mapRow{node: e.To, level: maturityLevel(byName[e.To].Charter.Maturity),
				note: "[" + dashIfEmpty(e.Mode) + "]", indent: 2})
		}
	}
	hasArchitect := map[string]bool{}
	for _, e := range edges {
		if e.Kind == "governs" {
			hasArchitect[e.To] = true
		}
	}
	var loose []protocol.Node
	for _, n := range nodes {
		if n.Kind != "architect" && !hasArchitect[n.Name] {
			loose = append(loose, n)
		}
	}
	if len(loose) > 0 {
		rows = append(rows, mapRow{section: true, text: "Components with no architect"})
		for _, n := range loose {
			rows = append(rows, mapRow{node: n.Name, level: maturityLevel(n.Charter.Maturity), indent: 2})
		}
	}
	var empty []protocol.Node
	for _, a := range architects {
		if len(governed[a.Name]) == 0 {
			empty = append(empty, a)
		}
	}
	if len(empty) > 0 {
		rows = append(rows, mapRow{section: true, text: "Architects without nodes"})
		for _, a := range empty {
			rows = append(rows, mapRow{node: a.Name, level: maturityLevel(a.Charter.Maturity), indent: 2})
		}
	}
	if m.uiFilter != "" && m.mapOn {
		filtered := rows[:0]
		for _, row := range rows {
			if row.node != "" && strings.Contains(strings.ToLower(row.node), strings.ToLower(m.uiFilter)) {
				filtered = append(filtered, row)
			}
		}
		return filtered
	}
	return rows
}

// mapSelectable is the order the selection keys move through. A component that
// two architects govern appears under both but is selected once.
func (m Model) mapProjectNodes() []protocol.Node {
	nodes := append([]protocol.Node(nil), m.mp.nodes...)
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
		if (m.uiFilter == "" || strings.Contains(strings.ToLower(n.Name), strings.ToLower(m.uiFilter))) && (!m.mp.finding || strings.Contains(strings.ToLower(n.Name), strings.ToLower(m.mp.findQuery))) {
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

func (m *Model) mapMove(delta int) {
	list := m.mapSelectable()
	if len(list) == 0 {
		m.mp.selected = ""
		return
	}
	i := 0
	for j, name := range list {
		if name == m.mp.selected {
			i = j
			break
		}
	}
	i = min(max(i+delta, 0), len(list)-1)
	m.mp.selected = list[i]
}

func (m *Model) mapEnsureSelection() {
	if _, ok := m.mapNode(m.mp.selected); ok {
		return
	}
	list := m.mapSelectable()
	if len(list) == 0 {
		m.mp.selected = ""
		return
	}
	degree := map[string]int{}
	for _, e := range m.mp.edges {
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
		return "the daemon or the gateway is too old for the map (graph_view)"
	case ClassifyError(err) == ProblemDaemon:
		return "the daemon cannot be reached"
	case ClassifyError(err) == ProblemGateway:
		return "the gateway cannot be reached"
	}
	return sanitize(err.Error())
}
