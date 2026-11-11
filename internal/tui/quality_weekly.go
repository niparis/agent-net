package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// qualityWeeklyView renders the weekly scores over time table.
func (m Model) qualityWeeklyView(w, h int) []string {
	out := []string{m.titleLine(w), m.quotasSubtabs(w)}
	if !m.weeklyLoaded && len(m.weeklyScores) == 0 {
		body := []string{sDim.Render("Reading weekly scores...")}
		if m.weeklyErr != "" {
			body = append(body, sBad.Render(clip("Could not read weekly scores: "+m.weeklyErr, w-4)))
		}
		body = append(body, sDim.Render("R reads it again."))
		out = append(out, panel("Quality · scores over time", body, w, roleAccent)...)
		for len(out) < h {
			out = append(out, "")
		}
		if len(out) > h {
			out = out[:h]
		}
		return out
	}
	body := m.qualityWeeklyBody(w, max(h-4, 1))
	out = append(out, panel("Quality · scores over time", body, w, roleAccent)...)
	for len(out) < h {
		out = append(out, "")
	}
	if len(out) > h {
		out = out[:h]
	}
	return out
}

func (m Model) qualityWeeklyBody(w, height int) []string {
	inner := max(w-4, 20)
	var body []string
	header := weeklyHeader(inner)
	body = append(body, sBold.Render(clip(header, inner)))
	for _, row := range m.weeklyScores {
		line := weeklyRowLine(row, inner)
		body = append(body, clip(line, inner))
	}
	body = append(body, "")
	body = append(body, sDim.Render(clip("Scores over time · 8 ISO weeks with agent-net release tags · w picker", inner)))
	return body
}

func weeklyHeader(w int) string {
	var b strings.Builder
	b.WriteString(cell("WEEK", 8))
	b.WriteString(" ")
	b.WriteString(cellR("RUNS", 4))
	b.WriteString(" ")
	b.WriteString(cellR("SCORE", 5))
	b.WriteString(" ")
	b.WriteString(cellR("EARLY", 5))
	b.WriteString(" ")
	b.WriteString(cellR("FIX", 4))
	b.WriteString(" ")
	b.WriteString(cellR("BRIEF", 5))
	b.WriteString(" ")
	b.WriteString(cellR("LATE", 4))
	b.WriteString(" ")
	b.WriteString(cellR("TOKENS", 6))
	b.WriteString(" ")
	b.WriteString("RELEASES")
	return b.String()
}

func weeklyRowLine(r protocol.ScoresWeeklyRow, w int) string {
	var b strings.Builder
	b.WriteString(cell(r.Week, 8))
	b.WriteString(" ")
	b.WriteString(cellR(fmt.Sprintf("%d", r.Runs), 4))
	b.WriteString(" ")
	b.WriteString(cellR(fmt.Sprintf("%.2f", r.MeanScore), 5))
	b.WriteString(" ")
	earlyStr := fmt.Sprintf("%d%%", int(math.Round(r.EarlyQuitRate*100)))
	b.WriteString(cellR(earlyStr, 5))
	b.WriteString(" ")
	b.WriteString(cellR(fmt.Sprintf("%.2f", r.MeanFixRounds), 4))
	b.WriteString(" ")
	b.WriteString(cellR(fmt.Sprintf("%d", r.BriefDefects), 5))
	b.WriteString(" ")
	b.WriteString(cellR(fmt.Sprintf("%d", r.Defects), 4))
	b.WriteString(" ")
	var tokensStr string
	if r.Runs == 0 || r.MeanTokens == 0 {
		tokensStr = "0"
	} else {
		tokensStr = protocol.CompactTokens(r.MeanTokens)
	}
	b.WriteString(cellR(tokensStr, 6))
	b.WriteString(" ")
	if len(r.Releases) > 0 {
		b.WriteString(releasesText(r.Releases))
	}
	return b.String()
}

// releasesText renders one week's release tags. Up to two tags are listed; a
// week with more is written as its first and last tag and the count, so a long
// release week stays on one line.
func releasesText(tags []string) string {
	switch {
	case len(tags) == 0:
		return ""
	case len(tags) <= 2:
		return "releases: " + strings.Join(tags, ", ")
	}
	return fmt.Sprintf("%s-%s (%d)", tags[0], tags[len(tags)-1], len(tags))
}
