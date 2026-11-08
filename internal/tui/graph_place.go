package tui

import (
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Place is the record's position in the authority tree (decision 0089). The
// gateway builds it from the same Team and Repo data as the turn text; the
// fallback below reads the same fields from the graph for an older gateway.

func heldBackParts(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var out []string
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == ';' || r == '\n' }) {
		if part = strings.Join(strings.Fields(part), " "); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func heldBackCount(text string) int { return len(heldBackParts(text)) }

// hasPlace is true when the record sits in a team or repo, so the card has a
// place to show. A legacy free-form node has none.
func hasPlace(p protocol.NodePlace) bool {
	return p.Team != "" || p.Repo != "" || len(p.HeldBack) > 0
}

func (m Model) nodePlace(n protocol.Node) protocol.NodePlace {
	if n.Place != nil {
		return *n.Place
	}
	return m.derivedPlace(n)
}

// derivedPlace reads a place from the typed graph for a gateway that does not
// send one. It uses the same member_of, team and repo edges as the live read.
func (m Model) derivedPlace(n protocol.Node) protocol.NodePlace {
	var p protocol.NodePlace
	if n.Ref == nil {
		return p
	}
	ref := *n.Ref
	repo := ""
	if ref.Kind == "repo" {
		repo = ref.ID
	} else if ref.Kind == "agent" {
		for _, a := range m.snap.Agents {
			if a.AgentID == ref.ID {
				if name := firstNonEmpty(a.Repo.Project, a.Profile.Project); name != "" {
					repo = name
					break
				}
			}
		}
		if repo == "" {
			for _, r := range m.mp.full.Nodes {
				if r.Ref != nil && r.Ref.Kind == "repo" && r.Owner != nil && graphRefKey(r.Owner) == graphRefKey(&ref) {
					repo = r.Ref.ID
					break
				}
			}
		}
	}
	p.Repo = m.graphName(repo)
	if repo != "" {
		if r, ok := m.mapNode(graphRefKey(&protocol.GraphRef{Kind: "repo", ID: repo})); ok {
			p.RepoLead = m.refName(r.Lead)
			p.HeldBack = append(p.HeldBack, placeHeld(r.HeldBack, "repo")...)
		}
	}
	teamKey := ""
	for _, e := range m.mp.full.Edges {
		if e.Kind != "member_of" || e.FromRef == nil || e.ToRef == nil || e.ToRef.Kind != "team" {
			continue
		}
		if *e.FromRef == ref || repo != "" && e.FromRef.Kind == "repo" && e.FromRef.ID == repo {
			teamKey = graphRefKey(e.ToRef)
			break
		}
	}
	if teamKey != "" {
		if t, ok := m.mapNode(teamKey); ok {
			p.Team = m.graphName(t.Name)
			p.TeamLead = m.refName(t.Lead)
			if p.TeamLead == "" {
				p.TeamLead = m.refName(t.Owner)
			}
			p.Mode = protocol.NormalAuthorityMode(t.AuthorityMode)
			p.HeldBack = append(placeHeld(t.HeldBack, "team"), p.HeldBack...)
		}
	}
	switch {
	case p.TeamLead != "":
		p.ReportsTo = "the " + p.Team + " lead"
	case p.RepoLead != "" && (ref.Kind == "agent" && p.RepoLead != ref.ID || ref.Kind == "repo" && p.RepoLead != ref.ID):
		p.ReportsTo = "the repo lead " + p.RepoLead
	default:
		p.ReportsTo = "the owner"
	}
	return p
}

func placeHeld(text, from string) []protocol.PlaceHeldBack {
	var out []protocol.PlaceHeldBack
	for _, item := range heldBackParts(text) {
		out = append(out, protocol.PlaceHeldBack{Text: item, From: from})
	}
	return out
}

func (m Model) refName(ref *protocol.GraphRef) string {
	if ref == nil {
		return ""
	}
	return m.graphName(graphRefKey(ref))
}

// teamLeadStar marks a team lead with the full star and a repo lead with a
// smaller one, so the frame says which level leads the team.
func teamLeadStar(kind string) string {
	if kind == "repo" {
		return "☆ "
	}
	return "★ "
}

func (m Model) staffLine() string {
	state := "idle"
	for _, a := range m.snap.Agents {
		if a.Harness == "cto" {
			if a.Online && (a.Status == "busy" || a.Status == "needs you") {
				state = "busy"
			}
			break
		}
	}
	return " owner   CTO " + state + "   chief of staff · not set up"
}

// placeLines lays out the place block. A wide card puts the team, lead and
// mode on one row, like the turn text; a narrow panel splits them.
func placeLines(p protocol.NodePlace, w int) []string {
	labelW := 10
	if w < 34 {
		labelW = 8
	}
	wrapLine := func(first bool, label, text string) []string {
		prefix := strings.Repeat(" ", labelW) + " "
		if first {
			prefix = sDim.Render(cell(label, labelW)) + " "
		}
		body := wrap(text, max(w-labelW-1, 8))
		if len(body) == 0 {
			body = []string{""}
		}
		out := []string{prefix + body[0]}
		for _, line := range body[1:] {
			out = append(out, strings.Repeat(" ", labelW+1)+line)
		}
		return out
	}
	var out []string
	team := "answers to owner"
	if p.Team != "" {
		team = p.Team
		if p.TeamLead != "" {
			team += ", lead ★ " + p.TeamLead
		}
		if w < 60 {
			out = append(out, wrapLine(true, "team", p.Team)...)
			if p.TeamLead != "" {
				out = append(out, wrapLine(true, "lead", "★ "+p.TeamLead)...)
			}
			if p.Mode != "" {
				out = append(out, wrapLine(true, "mode", protocol.AuthorityModeLabel(p.Mode))...)
			}
		} else {
			if p.Mode != "" {
				team += "   mode " + protocol.AuthorityModeLabel(p.Mode)
			}
			out = append(out, wrapLine(true, "team", team)...)
		}
	} else {
		out = append(out, wrapLine(true, "team", team)...)
	}
	if p.Repo != "" {
		text := "★ " + p.Repo
		if p.RepoLead != "" && p.RepoLead != p.Repo {
			text += " (" + p.RepoLead + ")"
		}
		if p.ReportsTo != "" {
			text += ", reports to " + p.ReportsTo
		}
		out = append(out, wrapLine(true, "repo lead", text)...)
	}
	held := p.HeldBack
	if len(held) == 0 {
		out = append(out, wrapLine(true, "held back", "nothing beyond the always list")...)
	}
	for _, h := range held {
		text := h.Text
		if h.From != "" {
			text += " (from " + h.From + ")"
		}
		out = append(out, wrapLine(true, "held back", text)...)
	}
	return out
}

// placePanelLines is the shorter block for the side panel: one level per row.
func placePanelLines(p protocol.NodePlace, w int) []string {
	labelW := 9
	if w < 34 {
		labelW = 8
	}
	if w < 26 {
		labelW = 6
	}
	heldLabel, reportsLabel, repoLabel := "held back", "reports", "repo lead"
	if labelW < 9 {
		heldLabel = "held"
		repoLabel = "repo"
	}
	if labelW >= 10 {
		reportsLabel = "reports to"
	}
	add := func(label, text string) []string {
		body := wrap(text, max(w-labelW-1, 6))
		if len(body) == 0 {
			body = []string{""}
		}
		out := []string{cell(label, labelW) + " " + body[0]}
		for _, line := range body[1:] {
			out = append(out, strings.Repeat(" ", labelW+1)+line)
		}
		return out
	}
	var out []string
	if p.Team != "" {
		out = append(out, add("team", p.Team)...)
		if p.TeamLead != "" {
			out = append(out, add("lead", "★ "+p.TeamLead)...)
		}
		if p.Mode != "" {
			out = append(out, add("mode", protocol.AuthorityModeLabel(p.Mode))...)
		}
	} else {
		out = append(out, add("team", "answers to owner")...)
	}
	if p.Repo != "" {
		text := "★ " + p.Repo
		if p.RepoLead != "" && p.RepoLead != p.Repo {
			text += " (" + p.RepoLead + ")"
		}
		out = append(out, add(repoLabel, text)...)
	}
	if p.ReportsTo != "" {
		out = append(out, add(reportsLabel, p.ReportsTo)...)
	}
	if len(p.HeldBack) == 0 {
		out = append(out, add(heldLabel, "nothing beyond the always list")...)
	}
	for _, h := range p.HeldBack {
		text := h.Text
		if h.From != "" {
			text += " (from " + h.From + ")"
		}
		out = append(out, add(heldLabel, text)...)
	}
	return out
}

// charterSourceSuffix names the levels that set one effective charter field.
func charterSourceSuffix(c protocol.Charter, attr string) string {
	if c.Sources == nil {
		return ""
	}
	var levels []string
	seen := map[string]bool{}
	for _, s := range (*c.Sources)[attr] {
		if s.Level == "" || seen[s.Level] {
			continue
		}
		seen[s.Level] = true
		levels = append(levels, s.Level)
	}
	if len(levels) == 0 {
		return ""
	}
	return " (" + strings.Join(levels, ", ") + ")"
}
