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
func (m Model) mapEdgeEntry(e protocol.Edge, incoming bool) string {
	peer := e.To
	if incoming {
		peer = e.From
	}
	arrow := edgeKindStyle(e.Kind).Render("──▶")
	text := m.projectName(peer)
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
			lines = append(lines, m.mapEdgeEntry(e, incoming))
		}
		return lines
	}
	label := func(name string, n int) string {
		return sBold.Render(name) + " " + sDim.Render(fmt.Sprint(n))
	}
	var lines []string
	if w >= 52 {
		// Two columns side by side, capped so a wide terminal does not push
		// the two lists to opposite edges.
		colW := min((w-2)/2, 60)
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

// mapCharterField is one set charter field in display order. Purpose and
// maturity are left out: the card header shows them already.
type mapCharterField struct{ name, value string }

func mapCharterFields(c protocol.Charter) []mapCharterField {
	var out []mapCharterField
	for _, f := range []mapCharterField{
		{"accepts", c.Accepts}, {"refuses", c.Refuses},
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

// mapCharterBlock wraps every set field that the header does not show under an
// aligned label column.
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
		for i, l := range wrap(f.value, max(w-labelW-1, 8)) {
			label := strings.Repeat(" ", labelW)
			if i == 0 {
				label = sDim.Render(cell(name, labelW))
			}
			out = append(out, label+" "+l)
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

// mapRuleLine is one effective rule of a node, with where its value comes from.
type mapRuleLine struct{ name, value, source string }

// mapRuleValues reads the node's rules the way agent_graph does: the five
// maturity defaults, each replaced by a valid owner setting, and autonomy,
// which has no maturity default. The source says which one won.
func mapRuleValues(node protocol.Node) []mapRuleLine {
	hasMaturity := maturityLevel(node.Charter.Maturity) != "-"
	rules := protocol.RulesFor(node.Charter.Maturity, node.Settings)
	out := make([]mapRuleLine, 0, len(rules))
	for _, r := range rules {
		source := "maturity default"
		if !hasMaturity {
			source = "default"
		}
		if r.Name == protocol.SettingAutonomy {
			source = "default"
			if protocol.NodeAutonomy(node.Settings) == protocol.AutonomyWake {
				source = "owner override"
			}
		} else if value, ok := node.Settings[r.Name]; ok {
			value = strings.TrimSpace(value)
			if value != "" && protocol.ValidateNodeSetting(r.Name, value) == nil {
				source = "owner override"
			}
		}
		out = append(out, mapRuleLine{name: r.Name, value: r.Value, source: source})
	}
	return out
}

// mapRuleMark is the short or long note beside a rule value.
func mapRuleMark(source string, narrow bool) string {
	switch {
	case source == "owner override":
		if narrow {
			return "owner"
		}
		return "owner override"
	case source == "maturity default":
		if narrow {
			return "maturity"
		}
		return "maturity default"
	}
	return "default"
}

// mapRulesLines draws the effective rules under the card's Rules heading.
func mapRulesLines(node protocol.Node, w int) []string {
	nameW, markW := 18, 16
	narrow := w < 64
	if narrow {
		nameW, markW = 12, 8
	}
	// Keep the value column near its content so the mark stays beside it on a
	// very wide card.
	valueW := min(22, max(w-nameW-markW-2, 8))
	out := make([]string, 0, len(protocol.NodeSettingNames()))
	for _, r := range mapRuleValues(node) {
		mark := sDim.Render(cell(mapRuleMark(r.source, narrow), markW))
		if r.source == "owner override" {
			mark = sNeeds.Render(cell(mapRuleMark(r.source, narrow), markW))
		}
		out = append(out, sDim.Render(cell(r.name, nameW))+" "+cell(r.value, valueW)+" "+mark)
	}
	return out
}

// mapLegendEntry wraps one legend line under an aligned label.
func mapLegendEntry(label string, labelW int, text string, style lipgloss.Style, w int) []string {
	body := wrap(text, max(w-labelW-3, 8))
	out := []string{"  " + padStyled(style.Render(label), labelW) + " " + body[0]}
	for _, l := range body[1:] {
		out = append(out, "  "+strings.Repeat(" ", labelW)+" "+l)
	}
	return out
}

// mapLegendLines explains the edges and the charter fields. The claims about
// the charter follow the code: NodeContext sends maturity, accepts, refuses
// and escalate every turn, NeighborCharters sends 40 characters of accepts and
// refuses, CharterBrief sends the rest to a worker, and feature_requests is
// only in the agent view.
func mapLegendLines(w int) []string {
	out := []string{section("Legend", w)}
	out = append(out, sBold.Render("  Edges"))
	for _, e := range []struct{ kind, text string }{
		{"depends", "consumer uses provider"},
		{"governs", "architect over component; mode advisor, arbiter or owner"},
		{"deploys", "deployer runs the app's deploys"},
	} {
		label := edgeKindStyle(e.kind).Render("──▶") + " " + e.kind
		out = append(out, mapLegendEntry(label, 12, e.text, lipgloss.NewStyle(), w)...)
	}
	out = append(out, sBold.Render("  Request rule on an edge from A to B"))
	out = append(out, mapLegendEntry("normal", 7, "delivered; B judges it", sDim, w)...)
	out = append(out, mapLegendEntry("auto", 7, "delivered with a trust note; B can act without asking you, still inside B's merge authority and refusals", sBusy, w)...)
	out = append(out, mapLegendEntry("ask", 7, "held in Needs you", sNeeds, w)...)
	out = append(out, mapLegendEntry("never", 7, "refused", sBad, w)...)
	for _, l := range wrap("a specific edge beats the all projects wildcard; an edge never grants deploy or merge permission by itself (deploy_rule and merge_authority do)", max(w-2, 8)) {
		out = append(out, "  "+l)
	}
	out = append(out, sBold.Render("  Charter fields"))
	out = append(out, mapLegendEntry("maturity", 16, "sets the default rules; the only charter field with an enforced effect", sDim, w)...)
	out = append(out, mapLegendEntry("accepts", 16, "your agent reads it every turn; every neighbour reads the first 40 characters", sDim, w)...)
	out = append(out, mapLegendEntry("refuses", 16, "same as accepts", sDim, w)...)
	out = append(out, mapLegendEntry("escalate", 16, "your agent reads it every turn", sDim, w)...)
	out = append(out, mapLegendEntry("purpose", 16, "in a worker's brief", sDim, w)...)
	out = append(out, mapLegendEntry("consumers", 16, "in a worker's brief", sDim, w)...)
	out = append(out, mapLegendEntry("needs", 16, "in a worker's brief, and in the Tools tab", sDim, w)...)
	out = append(out, mapLegendEntry("notes", 16, "in a worker's brief", sDim, w)...)
	out = append(out, mapLegendEntry("feature_requests", 16, "shown only when another agent looks this node up", sDim, w)...)
	return out
}

// mapCardBody is the scrollable part of the card: the optional legend, the
// charter, the learned changes and the effective rules, each in its own
// section.
func (m Model) mapCardBody(node protocol.Node, w int) []string {
	var out []string
	if m.mp.legend {
		out = append(out, mapLegendLines(w)...)
	}
	out = append(out, section("Charter", w))
	charter := mapCharterBlock(node.Charter, w)
	if len(charter) == 0 {
		charter = []string{sDim.Render("no charter set")}
	}
	out = append(out, charter...)
	out = append(out, m.mapLearnedLines(node.Name, w)...)
	out = append(out, section("Rules", w))
	out = append(out, mapRulesLines(node, w)...)
	return out
}

// mapFocusView draws the selected node as a card: name, maturity, the start of
// the purpose and the sessions; its edges; and a scrolling body with the
// charter, the rules, the learned changes and the optional legend. The recent
// changes stay on the last row.
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
	title := sBold.Render(clip(m.projectName(node.Name), max(w-24, 8)))
	if level := maturityLevel(node.Charter.Maturity); level != "-" {
		title += "  " + mapLevelStyle(level).Render(level)
	}
	title += "  " + sDim.Render("autonomy: "+protocol.NodeAutonomy(node.Settings))
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
			gov = append(gov, m.projectName(e.From)+" ("+dashIfEmpty(sanitize(e.Mode))+")")
		}
	}
	sort.Strings(gov)
	if len(gov) > 0 {
		head = append(head, clip(sDim.Render("governed by: ")+strings.Join(gov, ", "), w))
	}
	head = append(head, "")
	// The body gets at least five rows when the pane allows it.
	room := min(max(h-len(head)-1-1-5, 1), 6)
	head = append(head, m.mapEdgeColumns(node.Name, w, room)...)
	recent := m.mapRecentLine(node.Name, w)
	rows := h - len(head) - 1
	out := head
	if rows > 0 {
		all := m.mapCardBody(node, w)
		body := rows - 1
		scroll := 0
		if m.mp.charterFor == node.Name {
			scroll = m.mp.charterScroll
		}
		scroll = min(max(scroll, 0), max(len(all)-body, 0))
		title := "Card"
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
