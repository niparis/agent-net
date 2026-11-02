package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

func formatTokens(n int64) string {
	if n >= 1000 {
		return fmt.Sprintf("~%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("~%d", n)
}

// shortenWords never cuts a status or a file name in the middle of a word.
func shortenWords(s string, width int) string {
	if ansi.StringWidth(s) <= width {
		return s
	}
	var out string
	for _, word := range strings.Fields(s) {
		next := strings.TrimSpace(out + " " + word)
		if ansi.StringWidth(next)+1 > width {
			break
		}
		out = next
	}
	if out == "" {
		return "…"
	}
	return out + "…"
}
func wordsLines(s string, width int) []string {
	var lines []string
	var line string
	for _, word := range strings.Fields(s) {
		if ansi.StringWidth(word) > width {
			word = "…"
		}
		next := strings.TrimSpace(line + " " + word)
		if ansi.StringWidth(next) > width {
			lines = append(lines, line)
			line = word
		} else {
			line = next
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func (m Model) toolDetailView(w, h int) []string {
	r, ok := m.selectedRow()
	var body []string
	if ok {
		add := func(s string) {
			if s != "" {
				body = append(body, wordsLines(s, w-4)...)
			}
		}
		if r.Kind == protocol.InventoryMCP {
			add("scope: " + orStr(r.Item.Scope, "global"))
			if r.Item.Host != "" {
				add("host: " + r.Item.Host)
			}
		} else {
			desc := wordsLines(sanitize(r.Item.Description), w-4)
			limit := min(6, max(h-4, 1))
			if len(desc) > limit {
				marker := wordsLines("… (enter, Read it, shows all)", w-4)
				keep := max(0, limit-len(marker))
				desc = append(desc[:keep], marker...)
			}
			body = append(body, desc...)
			add(formatTokens(r.Item.StartTokens) + " tokens in every session")
			add("+" + formatTokens(r.Item.FullTokens) + " when the skill is used")
			add("on: " + m.toolBaseStatus(r))
			if reason := m.toolAttentionReason(r); reason != "" {
				add("! " + reason)
				for _, d := range m.tl.cat.Diffs {
					if d.Name == r.Name && d.Fix != "" && !m.tl.accepted[d.Key] {
						add("enter: " + d.Fix)
						break
					}
				}
			}
			var files []string
			for _, l := range r.Item.Locations {
				if l.Manifest != nil {
					for _, f := range l.Manifest.Files {
						files = append(files, f.Path)
					}
					break
				}
			}
			if len(files) > 0 {
				add("files: " + strings.Join(files, ", "))
			}
			if r.Item.Desired != nil || r.Item.VersionNo > 0 {
				add("from: managed by agent-net " + r.Version)
			} else {
				add("from: found on your machines, not managed")
			}
		}
	}
	body = body[:min(len(body), max(h-2, 0))]
	for len(body) < h-2 {
		body = append(body, "")
	}
	title := "Details"
	if ok {
		title = shortenWords(r.Name, w-4)
	}
	return panel(title, body, w, roleAccent)
}

type projectCostRow struct {
	name    string
	skills  int
	cost    int64
	biggest string
}

// Use the same assignment and project membership rule as the scoped status.
func toolOnProject(r toolsdiff.Row, project string) bool {
	if r.Item.Desired != nil {
		a := r.Item.Desired.Assignment
		if a.Scope == "global" || (a.Scope == "projects" && hasStr(a.Projects, project)) {
			return true
		}
	}
	for _, p := range r.Projects {
		if p.Name == project {
			return true
		}
	}
	return false
}
func (m Model) projectCost(project string) (int, int64, string) {
	copy := m
	copy.tl.search = ""
	rows := copy.shownRows()
	var n int
	var total, largest int64
	var name string
	for _, r := range rows {
		if r.Kind != protocol.InventorySkill || !toolOnProject(r, project) {
			continue
		}
		n++
		total += r.Item.StartTokens
		if name == "" || r.Item.StartTokens > largest {
			largest = r.Item.StartTokens
			name = r.Name
		}
	}
	return n, total, name
}
func (m Model) projectCosts() []projectCostRow {
	var rows []projectCostRow
	for _, p := range m.toolsProjects() {
		n, c, b := m.projectCost(p)
		rows = append(rows, projectCostRow{name: p, skills: n, cost: c, biggest: b})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].cost == rows[j].cost {
			return rows[i].name < rows[j].name
		}
		return rows[i].cost > rows[j].cost
	})
	return rows
}
func (m Model) projectCostView(w, h int) []string {
	rows := m.projectCosts()
	nameW := max(12, (w-35)/2)
	biggestW := max(10, w-nameW-28)
	body := []string{cell("PROJECT", nameW) + cell("SKILLS", 8) + cell("EVERY SESSION", 16) + "BIGGEST"}
	start := max(0, m.tl.projectRow-max(h-5, 0))
	end := min(len(rows), start+max(h-3, 0))
	for i := start; i < end; i++ {
		r := rows[i]
		line := cell(shortenWords(r.name, nameW), nameW) + cell(fmt.Sprint(r.skills), 8) + cell(formatTokens(r.cost), 16) + cell(shortenWords(r.biggest, biggestW), biggestW)
		if i == m.tl.projectRow {
			line = selectedRow(padStyled(line, w-4))
		}
		body = append(body, line)
	}
	for len(body) < h-2 {
		body = append(body, "")
	}
	return panel("Skill cost per project · tokens in every session", body, w, roleAccent)
}
