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

// wildcardNode is the edge end that stands for every project.
const wildcardNode = "*"

// displayNode is the name the map shows for a node name. The wildcard is not a
// project, so it reads "all projects".
func displayNode(name string) string {
	if name == wildcardNode {
		return "all projects"
	}
	return sanitize(name)
}

// edgeKindStyle colors an edge by its kind. The map and the graph share it.
func edgeKindStyle(kind string) lipgloss.Style {
	switch kind {
	case "governs":
		return lipgloss.NewStyle().Foreground(roleCTO)
	case "deploys":
		return lipgloss.NewStyle().Foreground(roleWarning)
	}
	return lipgloss.NewStyle().Foreground(roleAccent)
}

func ruleStyle(rule string) lipgloss.Style {
	switch rule {
	case "auto":
		return sBusy
	case "ask":
		return sNeeds
	case "never":
		return sBad
	}
	return sDim
}

// mapSessionDots lists the live sessions of a project: one dot each, colored
// by status.
func (m Model) mapSessionDots(project string) (dots []string, busy int) {
	for _, a := range m.snap.Agents {
		if !a.Online || firstNonEmpty(a.Repo.Project, a.Profile.Project) != project {
			continue
		}
		switch a.Status {
		case "busy":
			busy++
			dots = append(dots, sBusy.Render("●"))
		case "needs you":
			dots = append(dots, sNeeds.Render("●"))
		default:
			dots = append(dots, sIdle.Render("●"))
		}
	}
	return dots, busy
}

// mapEdgeEntry draws one edge of the selected node: the peer, an arrow in the
// color of the kind, the kind, and the rule when it is not "normal".
func mapEdgeEntry(e protocol.Edge, incoming bool) string {
	peer := e.To
	if incoming {
		peer = e.From
	}
	arrow := edgeKindStyle(e.Kind).Render("──▶")
	text := displayNode(peer)
	if peer == wildcardNode {
		text = sDim.Render(text)
	}
	info := sDim.Render(sanitize(e.Kind))
	if e.Rule != "" && e.Rule != "normal" {
		info += " " + ruleStyle(e.Rule).Render(sanitize(e.Rule))
	}
	if incoming {
		return text + " " + arrow + " " + info
	}
	return arrow + " " + text + " " + info
}

// mapEdgeColumns draws the IN and OUT edges side by side, or one above the
// other when the pane is narrow. A governing edge into the node is not listed:
// the "governed by" line has it.
func (m Model) mapEdgeColumns(node string, w, room int) []string {
	var in, out []protocol.Edge
	for _, e := range m.mp.edges {
		switch {
		case e.To == node && e.Kind != "governs":
			in = append(in, e)
		case e.From == node:
			out = append(out, e)
		}
	}
	sortEdges := func(list []protocol.Edge, peerOf func(protocol.Edge) string) {
		sort.SliceStable(list, func(i, j int) bool {
			a, b := strings.ToLower(peerOf(list[i])), strings.ToLower(peerOf(list[j]))
			if a != b {
				return a < b
			}
			return list[i].Kind < list[j].Kind
		})
	}
	sortEdges(in, func(e protocol.Edge) string { return e.From })
	sortEdges(out, func(e protocol.Edge) string { return e.To })
	if len(in) == 0 && len(out) == 0 {
		return []string{sDim.Render(clip("no edges yet · ask the CTO to connect it (c)", w))}
	}
	entries := func(list []protocol.Edge, incoming bool, room int) []string {
		var lines []string
		for i, e := range list {
			if i == room-1 && len(list) > room {
				lines = append(lines, sDim.Render(fmt.Sprintf("+%d more", len(list)-i)))
				break
			}
			lines = append(lines, mapEdgeEntry(e, incoming))
		}
		return lines
	}
	label := func(name string, n int) string {
		return sBold.Render(name) + " " + sDim.Render(fmt.Sprint(n))
	}
	var lines []string
	if w >= 52 {
		colW := (w - 2) / 2
		l, r := entries(in, true, room), entries(out, false, room)
		lines = append(lines, padStyled(label("IN", len(in)), colW)+"  "+label("OUT", len(out)))
		for i := 0; i < max(len(l), len(r)); i++ {
			a, b := "", ""
			if i < len(l) {
				a = l[i]
			}
			if i < len(r) {
				b = r[i]
			}
			lines = append(lines, padStyled(clip(a, colW), colW)+"  "+clip(b, colW))
		}
		return lines
	}
	each := max(room/2, 2)
	if len(in) > 0 {
		lines = append(lines, label("IN", len(in)))
		for _, l := range entries(in, true, each) {
			lines = append(lines, "  "+clip(l, w-2))
		}
	}
	if len(out) > 0 {
		lines = append(lines, label("OUT", len(out)))
		for _, l := range entries(out, false, each) {
			lines = append(lines, "  "+clip(l, w-2))
		}
	}
	return lines
}

// mapCharterField is one set charter field in display order.
type mapCharterField struct{ name, value string }

func mapCharterFields(c protocol.Charter) []mapCharterField {
	var out []mapCharterField
	for _, f := range []mapCharterField{
		{"purpose", c.Purpose}, {"maturity", c.Maturity}, {"accepts", c.Accepts}, {"refuses", c.Refuses},
		{"feature_requests", c.FeatureRequests}, {"consumers", c.Consumers}, {"escalate", c.Escalate},
		{"needs", c.Needs}, {"notes", c.Notes},
	} {
		if strings.TrimSpace(f.value) != "" {
			f.value = strings.TrimSpace(sanitize(f.value))
			out = append(out, f)
		}
	}
	return out
}

// mapCharterBlock wraps every set field under an aligned label column.
func mapCharterBlock(c protocol.Charter, w int) []string {
	labelW := 16
	if w < 48 {
		labelW = 9
	}
	var out []string
	for _, f := range mapCharterFields(c) {
		name := f.name
		if labelW < 16 && name == "feature_requests" {
			name = "features"
		}
		style := lipgloss.NewStyle()
		if f.name == "maturity" {
			style = mapLevelStyle(maturityLevel(f.value))
		}
		for i, l := range wrap(f.value, max(w-labelW-1, 8)) {
			label := strings.Repeat(" ", labelW)
			if i == 0 {
				label = sDim.Render(cell(name, labelW))
			}
			out = append(out, label+" "+style.Render(l))
		}
	}
	return out
}

// mapRecentLine lists the newest graph changes of a node on one line.
func (m Model) mapRecentLine(node string, w int) string {
	head := sDim.Render("RECENT") + "  "
	room := w - ansi.StringWidth("RECENT") - 2
	var parts []string
	used := 0
	for _, c := range m.mp.changes {
		if c.Node != node {
			continue
		}
		text := fmt.Sprintf("%s %s", m.mapAgo(c.At), sanitize(c.Attr))
		if c.By != "" {
			text += " by " + sanitize(c.By)
		}
		extra := ansi.StringWidth(text)
		if len(parts) > 0 {
			extra += 3
		}
		if used+extra > room {
			if len(parts) == 0 {
				parts = append(parts, clip(text, room))
			}
			break
		}
		used += extra
		parts = append(parts, text)
		if len(parts) == 3 {
			break
		}
	}
	if len(parts) == 0 {
		return head + sDim.Render("no changes yet")
	}
	return head + strings.Join(parts, sDim.Render(" · "))
}

// mapAgo says how long ago a change was, in the short form the map uses.
func (m Model) mapAgo(at int64) string {
	d := m.now().Sub(time.Unix(0, at))
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// mapPurposeLines wraps the start of a purpose over at most rows lines.
func mapPurposeLines(purpose string, w, rows int) []string {
	p := strings.TrimSpace(sanitize(purpose))
	if p == "" {
		return []string{sDim.Render("no purpose set")}
	}
	lines := wrap(p, w)
	if len(lines) > rows {
		lines = lines[:rows]
		words := strings.Fields(lines[rows-1])
		for len(words) > 1 && ansi.StringWidth(strings.Join(words, " "))+2 > w {
			words = words[:len(words)-1]
		}
		lines[rows-1] = strings.Join(words, " ") + " …"
	}
	return lines
}

// mapFocusView draws the selected node as a card: name, maturity, the start of
// the purpose and the sessions; its edges; the charter, which scrolls; and
// the recent changes on the last row.
func (m Model) mapFocusView(w, h int) []string {
	var head []string
	if m.mp.err != "" {
		head = append(head, sBad.Render(clip("could not read the graph: "+m.mp.err, w)))
		if m.mp.selected == "" {
			return head
		}
	}
	if m.mp.selected == "" {
		return append(head, sDim.Render("  no nodes yet"), sDim.Render("  no node selected"))
	}
	node, _ := m.mapNode(m.mp.selected)
	title := sBold.Render(clip(displayNode(node.Name), max(w-24, 8)))
	if level := maturityLevel(node.Charter.Maturity); level != "-" {
		title += "  " + mapLevelStyle(level).Render(level)
	}
	if node.Kind == "architect" {
		title += "  " + lipgloss.NewStyle().Foreground(roleCTO).Render("architect")
	}
	head = append(head, clip(title, w))
	purposeRows := 3
	if h < 16 {
		purposeRows = 2
	}
	head = append(head, mapPurposeLines(node.Charter.Purpose, w, purposeRows)...)
	dots, busy := m.mapSessionDots(node.Name)
	if n := len(dots); n == 0 {
		head = append(head, sDim.Render("○ no live sessions"))
	} else {
		text := fmt.Sprintf("%d sessions", n)
		if n == 1 {
			text = "1 session"
		}
		if busy > 0 {
			text += fmt.Sprintf(", %d busy", busy)
		}
		shown := dots
		if len(shown) > 12 {
			shown = append(shown[:12:12], sDim.Render("+"))
		}
		head = append(head, strings.Join(shown, "")+" "+sDim.Render(text))
	}
	var gov []string
	for _, e := range m.mp.edges {
		if e.Kind == "governs" && e.To == node.Name {
			gov = append(gov, sanitize(e.From)+" ("+dashIfEmpty(sanitize(e.Mode))+")")
		}
	}
	sort.Strings(gov)
	if len(gov) > 0 {
		head = append(head, clip(sDim.Render("governed by: ")+strings.Join(gov, ", "), w))
	}
	head = append(head, "")
	// The charter gets at least five rows when the pane allows it.
	room := min(max(h-len(head)-1-1-5, 1), 6)
	head = append(head, m.mapEdgeColumns(node.Name, w, room)...)
	recent := m.mapRecentLine(node.Name, w)
	if h-len(head)-1 >= 9 {
		head = append(head, "")
	}
	rows := h - len(head) - 1
	out := head
	if rows > 0 {
		all := mapCharterBlock(node.Charter, w)
		if len(all) == 0 {
			all = []string{sDim.Render("no charter set")}
		}
		body := rows - 1
		scroll := 0
		if m.mp.charterFor == node.Name {
			scroll = m.mp.charterScroll
		}
		scroll = min(max(scroll, 0), max(len(all)-body, 0))
		title := "Charter"
		if len(all) > body && body > 0 {
			title += sDim.Render(fmt.Sprintf(" %d-%d/%d  [ ] scroll", scroll+1, min(scroll+body, len(all)), len(all)))
		}
		out = append(out, section(title, w))
		for i := scroll; i < min(scroll+body, len(all)); i++ {
			out = append(out, all[i])
		}
	}
	for len(out) < h-1 {
		out = append(out, "")
	}
	if len(out) > h-1 {
		out = out[:h-1]
	}
	return append(out, recent)
}

// mapScrollCharter moves the charter window of the selected node.
func (m *Model) mapScrollCharter(delta int) {
	if m.mp.charterFor != m.mp.selected {
		m.mp.charterFor, m.mp.charterScroll = m.mp.selected, 0
	}
	m.mp.charterScroll = max(m.mp.charterScroll+delta, 0)
}
