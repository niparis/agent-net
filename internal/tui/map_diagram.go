package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

type mapLink struct {
	name string
	edge protocol.Edge
}

func (m Model) mapLinks(side string) []mapLink {
	var links []mapLink
	for _, e := range m.mp.edges {
		switch {
		case side == "below" && e.Kind == "governs" && e.From == m.mp.selected:
			links = append(links, mapLink{e.To, e})
		case side == "above" && e.Kind == "governs" && e.To == m.mp.selected:
			links = append(links, mapLink{e.From, e})
		case side == "in" && e.Kind != "governs" && e.To == m.mp.selected:
			links = append(links, mapLink{e.From, e})
		case side == "out" && e.Kind != "governs" && e.From == m.mp.selected:
			links = append(links, mapLink{e.To, e})
		}
	}
	sort.Slice(links, func(i, j int) bool { return links[i].name < links[j].name })
	return links
}

func (m *Model) mapFollow(side string) {
	links := m.mapLinks(side)
	if len(links) > 0 {
		m.mp.selected = links[m.mp.choice%len(links)].name
		m.mp.choice = 0
	}
}

func (m Model) mapBox(name string, width int, selected bool) []string {
	width = max(width, 8)
	if name == "" {
		return []string{"", "", "", ""}
	}
	node, _ := m.mapNode(name)
	border := lipgloss.NewStyle().Foreground(roleFaint)
	tl, tr, bl, br, hor, vert := "╭", "╮", "╰", "╯", "─", "│"
	if selected {
		border = lipgloss.NewStyle().Foreground(roleAccent)
		tl, tr, bl, br, hor, vert = "╔", "╗", "╚", "╝", "═", "║"
	}
	busy := 0
	for _, a := range m.snap.Agents {
		if a.Online && a.Status == "busy" && firstNonEmpty(a.Repo.Project, a.Profile.Project) == name {
			busy++
		}
	}
	status := sDim.Render("○ 0")
	if busy > 0 {
		status = sBusy.Render(fmt.Sprintf("● %d busy", busy))
	}
	if width < 18 {
		status = sDim.Render("○0")
		if busy > 0 {
			status = sBusy.Render(fmt.Sprintf("●%d", busy))
		}
	}
	level := maturityLevel(node.Charter.Maturity)
	info := mapLevelStyle(level).Render(level) + " " + status
	return []string{border.Render(tl + strings.Repeat(hor, width-2) + tr), border.Render(vert) + padStyled(clip(sanitize(name), width-2), width-2) + border.Render(vert), border.Render(vert) + padStyled(clip(info, width-2), width-2) + border.Render(vert), border.Render(bl + strings.Repeat(hor, width-2) + br)}
}

func linkLabel(links []mapLink, choice int, width int) string {
	if len(links) == 0 {
		return strings.Repeat(" ", width)
	}
	e := links[choice%len(links)].edge
	return padStyled(clip(e.Kind+" "+e.Rule, width), width)
}
func linkArrow(links []mapLink, choice, width int) string {
	if len(links) == 0 {
		return strings.Repeat(" ", width)
	}
	e := links[choice%len(links)].edge
	style, fill := sDim, "─"
	switch e.Rule {
	case "auto":
		style, fill = sBusy, "━"
	case "ask":
		style, fill = sNeeds, "┄"
	case "never":
		style = sBad
	}
	return style.Render(strings.Repeat(fill, max(width-1, 0)) + "▶")
}
func (m Model) mapFocusView(w, h int) []string {
	var out []string
	if m.mp.err != "" {
		if m.mp.selected == "" {
			return append(out, sBad.Render(clip("could not read the graph: "+m.mp.err, w)))
		}
		out = append(out, sBad.Render(clip("could not read the graph: "+m.mp.err, w)))
	}
	if m.mp.selected == "" {
		return append(out, sDim.Render("  no nodes yet"), sDim.Render("  no node selected"))
	}
	node, _ := m.mapNode(m.mp.selected)
	left, right, above := m.mapLinks("in"), m.mapLinks("out"), m.mapLinks("above")
	pick := func(ls []mapLink) string {
		if len(ls) == 0 {
			return ""
		}
		return ls[m.mp.choice%len(ls)].name
	}
	gap := 12
	bw := max((w-2*gap)/3, 8)
	lb, cb, rb := m.mapBox(pick(left), bw, false), m.mapBox(node.Name, bw, true), m.mapBox(pick(right), bw, false)
	aboveWidth := min(max(bw, ansi.StringWidth(pick(above))+2), w-(bw+gap))
	ab := m.mapBox(pick(above), aboveWidth, false)
	indent := strings.Repeat(" ", bw+gap)
	for _, l := range ab {
		out = append(out, indent+l)
	}
	connector := ""
	if len(above) > 0 {
		e := above[m.mp.choice%len(above)].edge
		connector = sDim.Render("▼ governs " + e.Mode + " " + e.Rule)
	}
	out = append(out, indent+connector)
	for i := 0; i < 4; i++ {
		g1, g2 := strings.Repeat(" ", gap), strings.Repeat(" ", gap)
		if i == 0 {
			g1 = linkLabel(left, m.mp.choice, gap)
			g2 = linkLabel(right, m.mp.choice, gap)
		}
		if i == 2 {
			g1 = linkArrow(left, m.mp.choice, gap)
			g2 = linkArrow(right, m.mp.choice, gap)
		}
		out = append(out, clip(padStyled(lb[i], bw)+g1+padStyled(cb[i], bw)+g2+padStyled(rb[i], bw), w))
	}
	// Tall screens can show more than one neighbor on each side. On smaller
	// screens up/down cycles the visible neighbor.
	extras := min(max((h-len(out)-4)/4, 0), max(len(left), len(right))-1)
	for n := 1; n <= extras; n++ {
		var l, r []mapLink
		if n < len(left) {
			l = []mapLink{left[(m.mp.choice+n)%len(left)]}
		}
		if n < len(right) {
			r = []mapLink{right[(m.mp.choice+n)%len(right)]}
		}
		lbox, rbox := m.mapBox(pick(l), bw, false), m.mapBox(pick(r), bw, false)
		for i := 0; i < 4; i++ {
			g1, g2 := strings.Repeat(" ", gap), strings.Repeat(" ", gap)
			middle := strings.Repeat(" ", bw/2) + sDim.Render("│")
			if i == 0 {
				g1 = linkLabel(l, 0, gap)
				g2 = linkLabel(r, 0, gap)
			}
			if i == 2 {
				g1 = linkArrow(l, 0, gap)
				g2 = linkArrow(r, 0, gap)
				middle = sDim.Render(strings.Repeat("─", bw/2) + "┼" + strings.Repeat("─", bw-bw/2-1))
			}
			out = append(out, clip(padStyled(lbox[i], bw)+g1+padStyled(middle, bw)+g2+padStyled(rbox[i], bw), w))
		}
	}
	isolated := true
	for _, e := range m.mp.edges {
		if e.From == node.Name || e.To == node.Name {
			isolated = false
			break
		}
	}
	if isolated {
		out = append(out, sDim.Render(clip("no edges yet · ask the CTO to connect it (c)", w)))
	}
	last := "-"
	for _, c := range m.mp.changes {
		if c.Node == node.Name {
			last = c.Attr + ": " + sanitize(c.NewValue)
			break
		}
	}
	for _, pair := range [][2]string{{"purpose", node.Charter.Purpose}, {"accepts", node.Charter.Accepts}, {"refuses", node.Charter.Refuses}, {"last change", last}} {
		out = append(out, clip(sDim.Render(pair[0]+": ")+sanitize(dashIfEmpty(pair[1])), w))
	}
	if len(out) > h {
		out = out[:h]
	}
	return out
}
