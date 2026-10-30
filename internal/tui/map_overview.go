package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

type overviewLine struct {
	text     string
	nodes    []string
	isolated bool
}

func (m Model) overviewLines(w int) []overviewLine {
	degree := map[string]int{}
	for _, e := range m.mp.edges {
		degree[e.From]++
		degree[e.To]++
	}
	var architects, components, isolated []protocol.Node
	for _, n := range m.mp.nodes {
		if degree[n.Name] == 0 {
			isolated = append(isolated, n)
		} else if n.Kind == "architect" {
			architects = append(architects, n)
		} else {
			components = append(components, n)
		}
	}
	less := func(ns []protocol.Node) {
		sort.Slice(ns, func(i, j int) bool { return strings.ToLower(ns[i].Name) < strings.ToLower(ns[j].Name) })
	}
	less(architects)
	less(components)
	less(isolated)
	bw := 32
	if w >= 110 {
		bw = 36
	}
	if w < 70 {
		bw = 25
	}
	bw = min(bw, max(w, 8))
	cols := max((w+1)/(bw+1), 1)
	var out []overviewLine
	layer := func(title string, nodes []protocol.Node) {
		if len(nodes) == 0 {
			return
		}
		out = append(out, overviewLine{text: sDim.Render(title)})
		for i := 0; i < len(nodes); i += cols {
			var boxes, names []string
			for _, n := range nodes[i:min(i+cols, len(nodes))] {
				boxes = append(boxes, m.overviewBox(n, bw))
				names = append(names, n.Name)
			}
			out = append(out, overviewLine{text: strings.Join(boxes, " "), nodes: names})
		}
	}
	layer("Architects", architects)
	layer("Components", components)
	if len(isolated) > 0 {
		names := make([]string, 0, len(isolated))
		for _, n := range isolated {
			name := sanitize(n.Name)
			if n.Name == m.mp.selected {
				name = "▸ " + name
			}
			names = append(names, name)
		}
		for _, line := range wrap("Isolated: "+strings.Join(names, ", "), max(w, 8)) {
			out = append(out, overviewLine{text: sDim.Render(line), isolated: true})
		}
	}
	if len(out) == 0 {
		out = append(out, overviewLine{text: sDim.Render("No projects yet")})
	}
	return out
}

func (m Model) overviewBox(n protocol.Node, w int) string {
	busy := 0
	for _, a := range m.snap.Agents {
		if a.Online && a.Status == "busy" && firstNonEmpty(a.Repo.Project, a.Profile.Project) == n.Name {
			busy++
		}
	}
	status := sDim.Render("○0")
	if busy > 0 {
		status = sBusy.Render(fmt.Sprintf("●%d", busy))
	}
	level := maturityLevel(n.Charter.Maturity)
	suffix := " · " + level + " · " + ansi.Strip(status)
	room := max(w-4-ansi.StringWidth(suffix), 1)
	name := clip(sanitize(n.Name), room)
	content := name + sDim.Render(" · ") + mapLevelStyle(level).Render(level) + sDim.Render(" · ") + status
	border := lipgloss.NewStyle().Foreground(roleFaint)
	if n.Name == m.mp.selected {
		border = lipgloss.NewStyle().Foreground(roleAccent)
	}
	return border.Render("[") + " " + padStyled(content, max(w-4, 0)) + " " + border.Render("]")
}

func overviewNodeRoom(h int) int { return max(h-5, 1) }
func (m Model) mapOverviewView(w, h int) ([]string, bool) {
	if h <= 0 {
		return nil, true
	}
	lines := m.overviewLines(w)
	room := overviewNodeRoom(h)
	start := min(max(m.mp.overviewScroll, 0), max(len(lines)-room, 0))
	end := min(start+room, len(lines))
	visible := map[string]bool{}
	out := []string{clip(sTitle.Render("Overview")+sDim.Render("  enter focus"), w)}
	var isolated []string
	for _, l := range lines[start:end] {
		if l.isolated {
			isolated = append(isolated, clip(l.text, w))
			continue
		}
		out = append(out, clip(l.text, w))
		for _, n := range l.nodes {
			visible[n] = true
		}
	}
	remaining := max(h-len(out)-len(isolated)-1, 0)
	var eligible []protocol.Edge
	for _, e := range m.mp.edges {
		if visible[e.From] && visible[e.To] {
			eligible = append(eligible, e)
		}
	}
	edgeStart := min(max(m.mp.overviewEdgeScroll, 0), max(len(eligible)-1, 0))
	edgeEnd := min(edgeStart+remaining, len(eligible))
	for _, e := range eligible[edgeStart:edgeEnd] {
		line := sanitize(e.From) + " " + linkArrow([]mapLink{{edge: e}}, 0, 4) + " " + sanitize(e.To) + sDim.Render(" · "+sanitize(e.Kind)+" "+sanitize(e.Rule))
		out = append(out, clip(line, w))
	}
	off := len(m.mp.edges) - (edgeEnd - edgeStart)

	out = append(out, isolated...)
	if off > 0 {
		out = append(out, sDim.Render(clip(fmt.Sprintf("+%d edges off screen", off), w)))
	} else if start > 0 || end < len(lines) {
		out = append(out, sDim.Render("more projects above or below"))
	}
	if len(out) > h {
		out = out[:h]
	}
	return out, true
}

func (m Model) overviewHeight() int {
	h := max(m.height, 10)
	box := min(max((h-1)/3, 6), max(h-4, 1))
	return max(h-2-box, 1)
}
func (m *Model) overviewRevealSelection() {
	rows := m.overviewLines(max(m.width, 40))
	room := overviewNodeRoom(m.overviewHeight())
	for i, row := range rows {
		for _, name := range row.nodes {
			if name == m.mp.selected {
				if i < m.mp.overviewScroll {
					m.mp.overviewScroll = i
				}
				if i >= m.mp.overviewScroll+room {
					m.mp.overviewScroll = i - room + 1
				}
				return
			}
		}
	}
}
func (m *Model) overviewMove(direction string) {
	rows := m.overviewLines(max(m.width, 40))
	type position struct {
		row, col int
		name     string
	}
	var positions []position
	for r, line := range rows {
		for c, name := range line.nodes {
			positions = append(positions, position{r, c, name})
		}
	}
	if len(positions) == 0 {
		return
	}
	index := 0
	for i, p := range positions {
		if p.name == m.mp.selected {
			index = i
			break
		}
	}
	current := positions[index]
	target := index
	switch direction {
	case "left":
		target = max(index-1, 0)
	case "right":
		target = min(index+1, len(positions)-1)
	case "up", "down":
		bestRow := -1
		for _, p := range positions {
			if direction == "up" && p.row < current.row && p.row > bestRow {
				bestRow = p.row
			}
			if direction == "down" && p.row > current.row && (bestRow < 0 || p.row < bestRow) {
				bestRow = p.row
			}
		}
		if bestRow < 0 {
			step := 1
			if direction == "up" {
				step = -1
			}
			room := overviewNodeRoom(m.overviewHeight())
			m.mp.overviewScroll = min(max(m.mp.overviewScroll+step, 0), max(len(rows)-room, 0))
			m.mp.overviewEdgeScroll = 0
			return
		}
		if bestRow >= 0 {
			distance := int(^uint(0) >> 1)
			for i, p := range positions {
				if p.row == bestRow {
					d := p.col - current.col
					if d < 0 {
						d = -d
					}
					if d < distance {
						distance = d
						target = i
					}
				}
			}
		}
	}
	m.mp.selected = positions[target].name
	m.mp.choice = 0
	oldScroll := m.mp.overviewScroll
	m.overviewRevealSelection()
	if oldScroll != m.mp.overviewScroll {
		m.mp.overviewEdgeScroll = 0
	}
}
func (m *Model) overviewPage(delta int) {
	h := m.overviewHeight()
	room := overviewNodeRoom(h)
	lines := m.overviewLines(max(m.width, 40))
	start := min(max(m.mp.overviewScroll, 0), max(len(lines)-room, 0))
	end := min(start+room, len(lines))
	visible := map[string]bool{}
	for _, row := range lines[start:end] {
		for _, name := range row.nodes {
			visible[name] = true
		}
	}
	eligible := 0
	for _, e := range m.mp.edges {
		if visible[e.From] && visible[e.To] {
			eligible++
		}
	}
	capacity := max(h-2-(end-start), 0)
	if delta > 0 && capacity > 0 && m.mp.overviewEdgeScroll+capacity < eligible {
		m.mp.overviewEdgeScroll += capacity
		return
	}
	if delta < 0 && m.mp.overviewEdgeScroll > 0 {
		m.mp.overviewEdgeScroll = max(m.mp.overviewEdgeScroll-max(capacity, 1), 0)
		return
	}
	m.mp.overviewScroll = min(max(start+delta*room, 0), max(len(lines)-room, 0))
	m.mp.overviewEdgeScroll = 0
}
