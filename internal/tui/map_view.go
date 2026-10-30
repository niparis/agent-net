package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// mapView draws the read-only graph map: the nodes on the left, the selected
// node on the right. It never writes more than width columns.
func (m Model) mapView(w, h int) []string {
	if m.mp.overview {
		if out, ok := m.mapOverviewView(w, h); ok {
			return out
		}
		out := m.mapFocusView(w, h)
		if len(out) > 0 {
			out = append([]string{sDim.Render("overview does not fit")}, out...)
			if len(out) > h {
				out = out[:h]
			}
		}
		return out
	}
	return m.mapFocusView(w, h)
}

func (m Model) mapTitle(w int) string {
	left := sTitle.Render("Map") + " " + sDim.Render("(read only)")
	mid := fmt.Sprintf("%d nodes, %d edges", len(m.mp.nodes), len(m.mp.edges))
	right := ""
	switch {
	case m.mp.loading:
		right = "reading..."
	case m.mp.loaded:
		right = "updated " + m.clock(m.mp.at)
	}
	used := ansi.StringWidth("Map (read only)") + 2 + ansi.StringWidth(mid) + 2 + ansi.StringWidth(right)
	gap := max(w-used, 2)
	return clip(left+"  "+sDim.Render(mid)+strings.Repeat(" ", gap)+sDim.Render(right), w)
}

// mapWidths splits the room between the tree and the detail. The tree gets
// about two fifths, within limits, so both sides stay readable on a wide
// terminal and the tree keeps its names on a narrow one.
func mapWidths(w int) (int, int) {
	if w < 64 {
		left := max(w/2, 12)
		return left, max(w-left-3, 8)
	}
	left := min(max(w*2/5, 28), 44)
	return left, max(w-left-3, 8)
}

func (m Model) mapLeftLines(width, rows int) []string {
	all := m.mapRows()
	if len(all) == 0 {
		return []string{sDim.Render(clip("  no nodes yet", width))}
	}
	// Keep the selected row on screen. The window follows the selection and
	// moves back when the selection goes above it.
	sel := -1
	for i, r := range all {
		if r.node != "" && r.node == m.mp.selected {
			sel = i
			break
		}
	}
	top := 0
	if sel >= 0 {
		if sel < top {
			top = sel
		}
		if sel >= top+rows {
			top = sel - rows + 1
		}
	}
	top = min(max(top, 0), max(len(all)-rows, 0))
	out := make([]string, 0, rows)
	marked := false
	for i := top; i < len(all) && len(out) < rows; i++ {
		r := all[i]
		if r.section {
			out = append(out, section(r.text, width))
			continue
		}
		selected := false
		if r.node == m.mp.selected && !marked {
			selected, marked = true, true
		}
		out = append(out, m.mapNodeLine(r, width, selected))
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out
}

func (m Model) mapNodeLine(r mapRow, width int, selected bool) string {
	name := r.text
	if name == "" {
		name = r.node
	}
	marker := "  "
	if selected {
		marker = "> "
	}
	indent := strings.Repeat(" ", max(r.indent, 0))
	note := ""
	if r.note != "" {
		note = " " + r.note
	}
	level := dashIfEmpty(r.level)
	levelW := ansi.StringWidth(level)
	left := marker + indent
	nameW := width - ansi.StringWidth(left) - ansi.StringWidth(note) - levelW - 1
	if nameW < 6 {
		// Too narrow for the level: keep the name and the mode.
		level, levelW = "", 0
		nameW = width - ansi.StringWidth(left) - ansi.StringWidth(note)
	}
	if nameW < 1 {
		nameW = 1
	}
	nameCell := cell(name, nameW)
	if r.bold {
		nameCell = padStyled(sBold.Render(clip(name, nameW)), nameW)
	}
	line := left + nameCell + note
	if level != "" {
		pad := max(width-ansi.StringWidth(line)-levelW, 1)
		line += strings.Repeat(" ", pad) + mapLevelStyle(r.level).Render(level)
	}
	switch {
	case selected:
		return sSel.Render(clip(stripStyles(line), width))
	case r.bold:
		return clip(line, width)
	}
	return clip(line, width)
}

func mapLevelStyle(level string) lipgloss.Style {
	switch level {
	case "mature":
		return lipgloss.NewStyle().Foreground(roleMature)
	case "critical":
		return sBad
	case "growing":
		return sBusy
	case "early":
		return sNeeds
	}
	return sDim
}

func (m Model) mapDetailLines(node protocol.Node, ok bool, width, rows int) []string {
	var out []string
	add := func(lines ...string) {
		for _, l := range lines {
			if len(out) < rows {
				out = append(out, clip(l, width))
			}
		}
	}
	if m.mp.err != "" {
		for _, l := range wrap("! could not read the graph: "+m.mp.err, max(width-2, 8)) {
			add("  " + sBad.Render(l))
		}
	}
	if !ok {
		if m.mp.loading && !m.mp.loaded {
			add(sDim.Render("  reading the graph..."))
		}
		add(sDim.Render("  no node selected"))
		for len(out) < rows {
			out = append(out, "")
		}
		return out[:rows]
	}
	title := node.Name
	if node.Kind != "" {
		title += " · " + node.Kind
	}
	add(section(title, width))
	if m.mp.err != "" {
		add("")
	}
	if node.Kind == "component" {
		add(section("Locations", width))
		for _, loc := range node.Locations {
			add(mapCharterLines(loc.Machine, loc.Path, width)...)
		}
		if len(node.Locations) == 0 {
			add(sDim.Render("  no locations"))
		}
		add(section("Live sessions", width))
		for _, session := range node.Sessions {
			add(mapCharterLines(session.Machine, session.Name+" ("+session.ID+", "+session.Status+")", width)...)
		}
		if node.SessionCount > len(node.Sessions) {
			add(fmt.Sprintf("  %d more", node.SessionCount-len(node.Sessions)))
		}
		if node.SessionCount == 0 {
			add(sDim.Render("  no live sessions"))
		}
	}
	add(section("Charter", width))
	for _, a := range []struct{ name, value string }{
		{"purpose", node.Charter.Purpose},
		{"maturity", node.Charter.Maturity},
		{"accepts", node.Charter.Accepts},
		{"refuses", node.Charter.Refuses},
		{"consumers", node.Charter.Consumers},
		{"escalate", node.Charter.Escalate},
		{"needs", node.Charter.Needs},
		{"notes", node.Charter.Notes},
	} {
		add(mapCharterLines(a.name, a.value, width)...)
	}
	add(section("Edges", width))
	edges := m.mapEdgeLines(node.Name, width)
	if len(edges) == 0 {
		edges = []string{sDim.Render("  no edges")}
	}
	add(edges...)
	add(section("Last changes", width))
	changes := m.mapChangeLines(node.Name, width)
	if len(changes) == 0 {
		changes = []string{sDim.Render("  no changes yet")}
	}
	add(changes...)
	for len(out) < rows {
		out = append(out, "")
	}
	return out[:rows]
}

// mapCharterLines draws one attribute, wrapped under a fixed label.
func mapCharterLines(name, value string, width int) []string {
	const labelW = 9
	value = strings.TrimSpace(sanitize(value))
	if value == "" {
		value = "-"
	}
	lines := wrap(value, max(width-labelW-3, 8))
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		if i == 0 {
			out = append(out, "  "+sDim.Render(cell(name, labelW))+" "+l)
		} else {
			out = append(out, "  "+strings.Repeat(" ", labelW)+" "+l)
		}
	}
	return out
}

// mapEdgeLines lists the edges in and out of a node. Each edge takes two lines
// so the kind, the mode and the rule survive a narrow pane.
func (m Model) mapEdgeLines(node string, width int) []string {
	var in, out []protocol.Edge
	for _, e := range m.mp.edges {
		switch {
		case e.To == node:
			in = append(in, e)
		case e.From == node:
			out = append(out, e)
		}
	}
	byPeer := func(list []protocol.Edge, from bool) {
		sort.Slice(list, func(i, j int) bool {
			a, b := list[i].To, list[j].To
			if from {
				a, b = list[i].From, list[j].From
			} else {
				a, b = list[i].To, list[j].To
			}
			if a != b {
				return a < b
			}
			return list[i].Kind < list[j].Kind
		})
	}
	byPeer(in, true)
	byPeer(out, false)
	var lines []string
	add := func(dir string, e protocol.Edge) {
		head := fmt.Sprintf("  %-3s  %s -> %s", dir, sanitize(e.From), sanitize(e.To))
		lines = append(lines, clip(head, width))
		detail := sanitize(e.Kind)
		if e.Mode != "" {
			detail += ", " + sanitize(e.Mode)
		}
		detail += ", rule " + dashIfEmpty(sanitize(e.Rule))
		lines = append(lines, sDim.Render(clip("       "+detail, width)))
	}
	for _, e := range in {
		add("in", e)
	}
	for _, e := range out {
		add("out", e)
	}
	return lines
}

// mapChangeLines shows the newest changes of one node: when, who, and which
// attribute. History is kept newest first by the gateway.
func (m Model) mapChangeLines(node string, width int) []string {
	var out []string
	n := 0
	for _, c := range m.mp.changes {
		if c.Node != node {
			continue
		}
		if n == 3 {
			break
		}
		n++
		at := m.clock(time.Unix(0, c.At))
		text := fmt.Sprintf("  %s  %s  %s", at, dashIfEmpty(sanitize(c.By)), sanitize(c.Attr))
		for _, l := range wrap(text, max(width, 8)) {
			out = append(out, l)
		}
	}
	return out
}
