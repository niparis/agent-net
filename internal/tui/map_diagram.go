package tui

import (
	"sort"
	"strings"

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
	var links []mapLink
	for _, l := range m.mapLinks(side) {
		if l.name != wildcardNode {
			links = append(links, l)
		}
	}
	if len(links) > 0 {
		m.mp.selected = links[m.mp.choice%len(links)].name
		m.mp.choice = 0
	}
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
