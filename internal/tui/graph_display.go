package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

const dormantChip = "\x00dormant"
const unassignedTeam = "\x00unassigned"

type graphLevel struct{ team, focus, selected string }
type graphRemoval struct {
	question string
	nodes    []protocol.Node
}
type graphRemovedMsg struct {
	keys   []string
	failed []string
}

// graphProjection merges identities only when the repo-agent relationship is
// unambiguous in both directions. Ownership, explicit links and live sessions
// all contribute; names alone never establish identity.
func (m *Model) graphProjection() protocol.GraphView {
	v := m.mp.full
	v.Nodes = append([]protocol.Node(nil), v.Nodes...)
	kept := v.Nodes[:0]
	for _, n := range v.Nodes {
		if !m.mp.removed[n.Name] {
			kept = append(kept, n)
		}
	}
	v.Nodes = kept
	m.mp.merged = map[string]protocol.Node{}
	m.mp.aliases = map[string]string{}
	repos, agents := map[string]map[string]bool{}, map[string]map[string]bool{}
	kinds := map[string]string{}
	for _, n := range v.Nodes {
		if n.Ref != nil {
			kinds[n.Name] = n.Ref.Kind
		}
	}
	pair := func(r, a string) {
		if kinds[r] != "repo" || kinds[a] != "agent" {
			return
		}
		if repos[r] == nil {
			repos[r] = map[string]bool{}
		}
		repos[r][a] = true
		if agents[a] == nil {
			agents[a] = map[string]bool{}
		}
		agents[a][r] = true
	}
	for _, n := range v.Nodes {
		if n.Ref != nil && n.Ref.Kind == "repo" {
			for _, ref := range []*protocol.GraphRef{n.Owner, n.Lead} {
				if ref != nil {
					pair(n.Name, graphRefKey(ref))
				}
			}
		}
	}
	for _, e := range v.Edges {
		if e.Kind == "governs" || protocol.DeliveryEdgeStep(e.Kind) != "" {
			pair(e.To, e.From)
			pair(e.From, e.To)
		}
	}
	for _, a := range m.snap.Agents {
		if a.AgentID != "" {
			pair(firstNonEmpty(a.Repo.Project, a.Profile.Project), graphRefKey(&protocol.GraphRef{Kind: "agent", ID: a.AgentID}))
		}
	}
	for r, as := range repos {
		if len(as) != 1 {
			continue
		}
		for a := range as {
			if len(agents[a]) == 1 {
				m.mp.aliases[a] = r
				for _, n := range v.Nodes {
					if n.Name == a {
						m.mp.merged[r] = n
						break
					}
				}
			}
		}
	}
	alias := func(key string) string {
		if a, ok := m.mp.aliases[key]; ok {
			return a
		}
		return key
	}
	out := protocol.GraphView{Changes: v.Changes}
	for _, n := range v.Nodes {
		if alias(n.Name) != n.Name {
			continue
		}
		if a, ok := m.mp.merged[n.Name]; ok {
			seen := map[string]bool{}
			n.Sessions = append([]protocol.GraphSession(nil), n.Sessions...)
			for _, s := range n.Sessions {
				seen[s.ID] = true
			}
			for _, s := range a.Sessions {
				if !seen[s.ID] {
					n.Sessions = append(n.Sessions, s)
				}
			}
			n.SessionCount = max(n.SessionCount, len(n.Sessions))
		}
		out.Nodes = append(out.Nodes, n)
	}
	seen := map[string]bool{}
	for _, e := range v.Edges {
		e.From, e.To = alias(e.From), alias(e.To)
		key := e.From + "\x01" + e.To + "\x01" + e.Kind + "\x01" + e.Mode + "\x01" + e.Rule
		if !seen[key] {
			seen[key] = true
			out.Edges = append(out.Edges, e)
		}
	}
	return out
}

func (m Model) graphSessions(n protocol.Node) []protocol.AgentView {
	var out []protocol.AgentView
	ids := map[string]bool{}
	for _, s := range n.Sessions {
		ids[s.ID] = true
	}
	merged := m.mp.merged[n.Name]
	for _, a := range m.snap.Agents {
		match := ids[a.SessionID]
		if n.Ref != nil {
			match = match || n.Ref.Kind == "repo" && firstNonEmpty(a.Repo.Project, a.Profile.Project) == n.Ref.ID || n.Ref.Kind == "agent" && a.AgentID == n.Ref.ID
		}
		if merged.Ref != nil {
			match = match || a.AgentID == merged.Ref.ID
		}
		if match {
			out = append(out, a)
		}
	}
	return out
}
func (m Model) graphDormant(n protocol.Node) bool {
	if n.Ref == nil || n.Ref.Kind != "repo" && n.Ref.Kind != "agent" {
		return false
	}
	if n.Active != nil && !*n.Active {
		return true
	}
	name := filepath.Base(m.graphName(n.Name))
	if name == "tmp" || strings.HasPrefix(name, "tmp.") || strings.HasSuffix(name, "-scratch-test") {
		return true
	}
	cutoff := m.now().Add(-30 * 24 * time.Hour).Unix()
	for _, a := range m.graphSessions(n) {
		if a.Online || a.LastActive >= cutoff {
			return false
		}
	}
	// Typed graph sessions are live even if the snapshot has not arrived yet.
	for _, s := range n.Sessions {
		if _, ok := m.agentByID(s.ID); !ok && s.Status != "offline" {
			return false
		}
	}
	return true
}
func (m Model) graphLiveRole(n protocol.Node) string {
	role := "offline"
	rank := func(s string) int {
		switch s {
		case "needs you":
			return 3
		case "busy":
			return 2
		case "idle":
			return 1
		}
		return 0
	}
	for _, a := range m.graphSessions(n) {
		if a.Online {
			s := a.Status
			if rank(s) == 0 {
				s = "idle"
			}
			if rank(s) > rank(role) {
				role = s
			}
		}
	}
	for _, s := range n.Sessions {
		if _, ok := m.agentByID(s.ID); !ok && rank(s.Status) > rank(role) {
			role = s.Status
		}
	}
	return role
}

// graphDisplayView keeps cards complete while reducing only what is drawn.
func (m *Model) graphDisplayView(v protocol.GraphView) protocol.GraphView {
	m.mp.dormant = map[string]bool{}
	keep := map[string]bool{}
	for _, n := range v.Nodes {
		if m.mp.removed[n.Name] {
			continue
		}
		d := m.graphDormant(n)
		if d {
			m.mp.dormant[n.Name] = true
		}
		keep[n.Name] = !d || m.mp.showDormant
		if n.Ref != nil && n.Ref.Kind == "team" && n.Active != nil && !*n.Active {
			keep[n.Name] = false
		}
	}
	out := protocol.GraphView{}
	for _, n := range v.Nodes {
		if keep[n.Name] {
			out.Nodes = append(out.Nodes, n)
		}
	}
	// Suppress repeated fan-out or fan-in by source/kind/mode/rule, using
	// distinct project coverage so duplicate rules do not trigger the mark.
	m.mp.everything = map[string]string{}
	projects := map[string]bool{}
	for _, n := range v.Nodes {
		if n.Kind == "component" {
			projects[n.Name] = true
		}
	}
	type bucket struct{ node, dir, kind, mode, rule string }
	groups := map[bucket]map[string]bool{}
	for _, e := range v.Edges {
		if e.From == e.To {
			continue
		}
		for _, b := range []bucket{{e.From, "→", e.Kind, e.Mode, e.Rule}, {e.To, "←", e.Kind, e.Mode, e.Rule}} {
			peer := e.To
			if b.dir == "←" {
				peer = e.From
			}
			if projects[peer] {
				if groups[b] == nil {
					groups[b] = map[string]bool{}
				}
				groups[b][peer] = true
			}
		}
	}
	broad := map[bucket]bool{}
	for b, ps := range groups {
		total := len(projects)
		if projects[b.node] {
			total--
		}
		if total >= 3 && len(ps)*5 >= total*4 {
			broad[b] = true
		}
	}
	for _, e := range v.Edges {
		if e.From == e.To {
			continue
		}
		marked := false
		for _, b := range []bucket{{e.From, "→", e.Kind, e.Mode, e.Rule}, {e.To, "←", e.Kind, e.Mode, e.Rule}} {
			if broad[b] {
				label := graphAllProjectsMark(b.dir, b.mode, b.rule)
				m.mp.everything[b.node] = label
				marked = true
			}
		}
		if e.From == "*" {
			m.mp.everything[e.To] = graphAllProjectsMark("←", e.Mode, e.Rule)
			marked = true
		}
		if !marked && keep[e.From] && keep[e.To] {
			out.Edges = append(out.Edges, e)
		}
	}
	// Focus and team zoom retain only direct outside neighbours.
	m.mp.outside = map[string]bool{}
	if m.mp.focusNode != "" || m.teamFilter != "" {
		inner := map[string]bool{}
		if m.mp.focusNode != "" {
			inner[m.mp.focusNode] = true
		} else {
			for _, e := range v.Edges {
				if e.Kind == "member_of" && e.To == graphRefKey(&protocol.GraphRef{Kind: "team", ID: m.teamFilter}) {
					inner[e.From] = true
				}
			}
			if m.teamFilter == unassignedTeam {
				for _, n := range out.Nodes {
					if n.Ref == nil || n.Ref.Kind != "team" {
						inner[n.Name] = true
					}
				}
				for _, e := range v.Edges {
					if e.Kind == "member_of" {
						delete(inner, e.From)
					}
				}
			}
		}
		visible := map[string]bool{}
		for k := range inner {
			visible[k] = true
		}
		for _, e := range v.Edges {
			if e.Kind == "member_of" || !keep[e.From] || !keep[e.To] {
				continue
			}
			if inner[e.From] {
				visible[e.To] = true
			}
			if inner[e.To] {
				visible[e.From] = true
			}
		}
		nodes := out.Nodes[:0]
		for _, n := range out.Nodes {
			if visible[n.Name] {
				nodes = append(nodes, n)
				if m.mp.focusNode == "" {
					m.mp.outside[n.Name] = !inner[n.Name]
				}
			}
		}
		out.Nodes = nodes
		edges := out.Edges[:0]
		for _, e := range out.Edges {
			if visible[e.From] && visible[e.To] && e.Kind != "member_of" {
				edges = append(edges, e)
			}
		}
		out.Edges = edges
	} else {
		// Membership is a box boundary, not a web of lines.
		edges := out.Edges[:0]
		for _, e := range out.Edges {
			if e.Kind != "member_of" {
				edges = append(edges, e)
			}
		}
		out.Edges = edges
	}
	if m.teamFilter == "" && m.mp.focusNode == "" {
		typed := false
		for _, n := range v.Nodes {
			typed = typed || n.Ref != nil
		}
		if typed {
			m.mp.labels[unassignedTeam] = "unassigned"
			out.Nodes = append(out.Nodes, protocol.Node{Name: unassignedTeam, Kind: "team"})
		}
	}
	if len(m.mp.dormant) > 0 {
		label := fmt.Sprintf("%d dormant · d to show", len(m.mp.dormant))
		if m.mp.showDormant {
			label = fmt.Sprintf("%d dormant · d to hide", len(m.mp.dormant))
		}
		m.mp.labels[dormantChip] = label
		out.Nodes = append(out.Nodes, protocol.Node{Name: dormantChip, Kind: "dormant"})
	}
	return out
}

func (m Model) graphFocus() (Model, tea.Cmd) {
	name := m.graphState.Graph.Selected()
	if name == dormantChip {
		m.mp.showDormant = !m.mp.showDormant
		m.applyTeamView(true)
		return m, nil
	}
	m.mp.levels = append(append([]graphLevel(nil), m.mp.levels...), graphLevel{m.teamFilter, m.mp.focusNode, name})
	if n, ok := m.mapNode(name); ok && n.Ref != nil && n.Ref.Kind == "team" {
		m.teamFilter = n.Ref.ID
		m.mp.focusNode = ""
	} else if name == unassignedTeam {
		m.teamFilter = unassignedTeam
		m.mp.focusNode = ""
	} else {
		m.mp.focusNode = name
	}
	m.applyTeamView(true)
	if m.teamFilter != "" {
		m.selectGraphInterior()
	}
	return m, nil
}
func (m Model) graphBack() (Model, tea.Cmd) {
	if len(m.mp.levels) == 0 {
		if m.teamFilter != "" || m.mp.focusNode != "" {
			m.teamFilter, m.mp.focusNode = "", ""
			m.applyTeamView(true)
		}
		return m, nil
	}
	n := len(m.mp.levels) - 1
	l := m.mp.levels[n]
	m.mp.levels = m.mp.levels[:n]
	m.teamFilter, m.mp.focusNode = l.team, l.focus
	m.applyTeamView(true)
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: l.selected})
	return m, nil
}
func (m Model) startGraphRemoval(all bool) (Model, tea.Cmd) {
	name := m.graphState.Graph.Selected()
	if all && name != dormantChip {
		m.say("Select the dormant chip first.")
		return m, nil
	}
	var nodes []protocol.Node
	for _, n := range m.mp.nodes {
		if m.mp.dormant[n.Name] && (all || n.Name == name) {
			nodes = append(nodes, n)
			if a, ok := m.mp.merged[n.Name]; ok {
				nodes = append(nodes, a)
			}
		}
	}
	if len(nodes) == 0 {
		m.say("Select a dormant repo or agent first.")
		return m, nil
	}
	question := "Remove " + m.graphName(name) + " for good? It is archived and leaves the graph."
	if all {
		question = fmt.Sprintf("Remove all %d dormant for good?", len(m.mp.dormant))
	}
	m.mp.removal = &graphRemoval{question, nodes}
	return m, nil
}

type graphArchiveSource interface {
	ArchiveGraphNode(context.Context, protocol.GraphRef) error
}

func (m Model) graphRemovalKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	yes, no := confirmAnswer(k)
	if !yes && !no {
		return m, nil
	}
	r := m.mp.removal
	if r == nil {
		return m, nil
	}
	m.mp.removal = nil
	if no {
		return m, nil
	}
	src := m.src
	return m, func() tea.Msg {
		result := graphRemovedMsg{}
		s, ok := src.(graphArchiveSource)
		if !ok {
			result.failed = append(result.failed, "Removal needs a newer daemon.")
			return result
		}
		for _, n := range r.nodes {
			if n.Ref == nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			err := s.ArchiveGraphNode(ctx, *n.Ref)
			cancel()
			if err != nil {
				result.failed = append(result.failed, sanitize(err.Error()))
			} else {
				result.keys = append(result.keys, n.Name)
			}
		}
		return result
	}
}
func (m Model) onGraphRemoved(r graphRemovedMsg) (Model, tea.Cmd) {
	if m.mp.removed == nil {
		m.mp.removed = map[string]bool{}
	}
	for _, key := range r.keys {
		m.mp.removed[key] = true
	}
	m.applyTeamView(true)
	text := fmt.Sprintf("Removed %d records for good.", len(r.keys))
	if len(r.failed) > 0 {
		text += " " + strings.Join(r.failed, "; ")
		m.fail(text)
	} else {
		m.say(text)
	}
	return m, nil
}

// graphGroups returns the visible boxes and their display members.
func (m Model) graphGroups(scene graphview.Scene) []graphview.Group {
	if m.teamFilter != "" || m.mp.focusNode != "" {
		return nil
	}
	var groups []graphview.Group
	membership := map[string]string{}
	for _, e := range m.mp.edges {
		if e.Kind == "member_of" {
			membership[e.From] = e.To
		}
	}
	lead := map[string]string{}
	for _, n := range m.mp.nodes {
		if n.Ref != nil && n.Ref.Kind == "team" {
			if n.Active != nil && !*n.Active {
				continue
			}
			ref := n.Lead
			if ref == nil {
				ref = n.Owner
			}
			if ref != nil {
				key := graphRefKey(ref)
				if a, ok := m.mp.aliases[key]; ok {
					key = a
				}
				lead[n.Name] = key
			}
			groups = append(groups, graphview.Group{Name: n.Name, Label: m.graphName(n.Name)})
		}
	}
	if len(groups) == 0 { // Legacy reads keep their established free-form layout.
		typed := false
		for _, n := range m.mp.nodes {
			typed = typed || n.Ref != nil
		}
		if !typed {
			return nil
		}
	}
	groups = append(groups, graphview.Group{Name: unassignedTeam, Label: "unassigned"})
	for _, n := range scene.Nodes {
		if n.Kind == "team" || n.Name == dormantChip || n.Name == unassignedTeam {
			continue
		}
		group := membership[n.Name]
		found := false
		for i := range groups {
			if groups[i].Name == group {
				groups[i].Members = append(groups[i].Members, n.Name)
				groups[i].Lead = lead[group]
				found = true
				break
			}
		}
		if !found {
			i := len(groups) - 1
			groups[i].Members = append(groups[i].Members, n.Name)
		}
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Name == unassignedTeam {
			return false
		}
		if groups[j].Name == unassignedTeam {
			return true
		}
		return groups[i].Label < groups[j].Label
	})
	return groups
}

func (m *Model) selectGraphInterior() {
	if m.graphState.Graph == nil {
		return
	}
	target := ""
	if team, ok := m.mapNode(graphRefKey(&protocol.GraphRef{Kind: "team", ID: m.teamFilter})); ok {
		ref := team.Lead
		if ref == nil {
			ref = team.Owner
		}
		if ref != nil {
			target = graphRefKey(ref)
			if alias, ok := m.mp.aliases[target]; ok {
				target = alias
			}
		}
	}
	for _, n := range m.graphState.Graph.Nodes {
		if n.Name == target {
			m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: target})
			return
		}
	}
	for _, n := range m.graphState.Graph.Nodes {
		if !m.mp.outside[n.Name] && n.Kind != "dormant" {
			m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: n.Name})
			return
		}
	}
}

func graphAllProjectsMark(direction, mode, rule string) string {
	label := direction + " all projects"
	if mode != "" && mode != "normal" {
		label += " · " + mode
	}
	if rule != "" && rule != "normal" && rule != mode {
		label += " · " + rule
	}
	return label
}
