package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

// mapView draws the selected agent card in the network's content area.
func (m Model) mapView(w, h int) []string { return m.mapFocusView(w, h) }

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
	title := m.graphName(node.Name)
	if node.Kind != "" {
		title += " · " + node.Kind
	}
	add(section(title, width))
	for _, machine := range m.snap.Nodes {
		if machine.NodeID != node.Name && machine.Hostname != node.Name {
			continue
		}
		for _, harness := range machine.Harnesses {
			if harness.DeliveryWarning != "" {
				for _, line := range wrap("! "+sanitize(harness.DeliveryWarning), max(width-2, 8)) {
					add(sBad.Render(line))
				}
			}
		}
	}

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
		if limit := repoLimitText(node.AgentCount, node.MaxAgents); limit != "" {
			if node.OverMax || node.AgentCount > node.MaxAgents {
				add(sBad.Render("  " + limit))
			} else {
				add(sDim.Render("  " + limit))
			}
		}
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
		{"autonomy", protocol.NodeAutonomy(node.Settings)},
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
		head := fmt.Sprintf("  %-3s  %s -> %s", dir, m.graphName(e.From), m.graphName(e.To))
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
