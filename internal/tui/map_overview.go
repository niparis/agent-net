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
	text  string
	nodes []string
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
	bw := min(25, max(w, 8))
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
			out = append(out, overviewLine{text: sDim.Render(line)})
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
	return border.Render("╭") + " " + padStyled(content, max(w-4, 0)) + " " + border.Render("╮")
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
	for _, l := range lines[start:end] {
		out = append(out, clip(l.text, w))
		for _, n := range l.nodes {
			visible[n] = true
		}
	}
	remaining := max(h-len(out)-1, 0)
	off := 0
	for _, e := range m.mp.edges {
		if !visible[e.From] || !visible[e.To] || remaining == 0 {
			off++
			continue
		}
		line := sanitize(e.From) + " " + linkArrow([]mapLink{{edge: e}}, 0, 4) + " " + sanitize(e.To) + sDim.Render(" · "+sanitize(e.Kind)+" "+sanitize(e.Rule))
		out = append(out, clip(line, w))
		remaining--
	}
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
