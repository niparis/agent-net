package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func qualityCards(q protocol.QualityAnalysis, w, agent int, picks map[string]string, machine string) []string {
	if q.Status == "" {
		return nil
	}
	levels := qualityLevels(q, agent)
	byLevel := map[string]protocol.QualityLevel{}
	largest := 0.0
	familyWidth, minutesWidth, weekDigits := 0, 7, 1
	for _, level := range levels {
		byLevel[level.Difficulty] = level
		for _, f := range level.Families {
			largest = max(largest, f.Headache)
		}
		switch level.Difficulty {
		case "simple", "normal", "hard", "very_complex":
			ranked, _ := qualityRankedFamilies(level.Families)
			for _, f := range ranked[:min(len(ranked), 3)] {
				familyWidth = max(familyWidth, ansi.StringWidth(sanitize(f.Family)))
				minutesWidth = max(minutesWidth, ansi.StringWidth(fmt.Sprintf("%.0f min", f.Headache)))
				weekDigits = max(weekDigits, len(fmt.Sprint(f.TasksWeek)))
			}
		}
	}
	widths := []int{(w - 5) / 2, w - 5 - (w-5)/2}
	var body []string
	body = append(body, qualitySelector(q, agent, w))
	for i := 0; i < 4; i += 2 {
		var cards [2][]string
		for j := 0; j < 2; j++ {
			name := []string{"simple", "normal", "hard", "very_complex"}[i+j]
			level := byLevel[name]
			eligible, few := qualityRankedFamilies(level.Families)
			var rows []string
			for _, f := range eligible[:min(len(eligible), 3)] {
				marker := " "
				if picks[name] == f.Family {
					marker = "▸"
				}
				frac := 0.0
				if largest > 0 {
					frac = f.Headache / largest
				}
				bug := ""
				if f.LateDefects > 0 {
					bug = fmt.Sprintf(" · %d late bug", f.LateDefects)
					if f.LateDefects != 1 {
						bug += "s"
					}
				}
				head := marker + " " + cell(f.Family, familyWidth) + " " + cellR(fmt.Sprintf("%.0f min", f.Headache), minutesWidth) + " "
				count := qualityTaskCount(f, weekDigits)
				row := head + progressBar(frac, 5, roleAccent) + " " + count
				// On a narrow card the bar goes first: it repeats the minutes.
				if ansi.StringWidth(row) > widths[j]-4 {
					row = head + count
				}
				if bug != "" && ansi.StringWidth(row+bug) > widths[j]-4 {
					rows = append(rows, row, sDim.Render("  "+strings.TrimPrefix(strings.TrimSpace(bug), "· ")))
				} else {
					rows = append(rows, row+bug)
				}
			}
			if len(eligible) > 3 {
				var rest []string
				for _, f := range eligible[3:] {
					item := fmt.Sprintf("%s %.0f min", f.Family, f.Headache)
					if picks[name] == f.Family {
						// The pick keeps its marker and comes first.
						rest = append([]string{"▸ " + item}, rest...)
						continue
					}
					rest = append(rest, item)
				}
				rows = append(rows, sDim.Render(fmt.Sprintf("%d more: %s", len(rest), strings.Join(rest, " · "))))
			}
			if few > 0 {
				rows = append(rows, sDim.Render(fmt.Sprintf("%d more with too few tasks to rank", few)))
			}
			cards[j] = panel(fmt.Sprintf("%s · %d tasks", strings.ReplaceAll(name, "_", " "), level.Tasks), rows, widths[j], roleAccent)
		}
		for j := 0; j < 2; j++ {
			for len(cards[j]) < max(len(cards[0]), len(cards[1])) {
				blank := panel("", []string{""}, widths[j], roleAccent)[1]
				n := len(cards[j])
				cards[j] = append(cards[j][:n-1], blank, cards[j][n-1])
			}
		}
		for k := 0; k < max(len(cards[0]), len(cards[1])); k++ {
			left, right := strings.Repeat(" ", widths[0]), strings.Repeat(" ", widths[1])
			if k < len(cards[0]) {
				left = cards[0][k]
			}
			if k < len(cards[1]) {
				right = cards[1][k]
			}
			body = append(body, left+" "+right)
		}
	}
	legend := "Most used first: tasks this week. My time = minutes the orchestrator spends after a worker: nudges, fixes and bugs found later."
	if machine != "" {
		legend = "▸ = the model the picker would choose on " + machine + ".  " + legend
	}
	for _, line := range wrap(legend, w-4) {
		body = append(body, sDim.Render(line))
	}
	return panel("Quality analysis · read only", body, w, roleAccent)
}

func qualityRankedFamilies(families []protocol.QualityFamily) ([]protocol.QualityFamily, int) {
	var eligible []protocol.QualityFamily
	few := 0
	for _, f := range families {
		if f.Tasks < protocol.CalibrationMinTasks {
			few++
		} else {
			eligible = append(eligible, f)
		}
	}
	sortQualityFamilies(eligible)
	return eligible, few
}

// sortQualityFamilies puts the most used models first: tasks in the last 7
// days, then less orchestrator time, then the name.
func sortQualityFamilies(families []protocol.QualityFamily) {
	sort.Slice(families, func(i, j int) bool {
		a, b := families[i], families[j]
		if a.TasksWeek != b.TasksWeek {
			return a.TasksWeek > b.TasksWeek
		}
		if a.Headache != b.Headache {
			return a.Headache < b.Headache
		}
		return a.Family < b.Family
	})
}

func (m Model) qualityDetailsView(w, h int) []string {
	q := m.snap.Calibration.Quality
	var picks, reasons map[string]string
	for _, n := range m.snap.Nodes {
		if n.NodeID != "" && n.NodeID == m.snap.NodeID {
			picks = n.QualityPicks
			reasons = n.QualityPickReasons
			break
		}
	}
	levels := qualityLevels(q, m.qualityAgent)
	byLevel := map[string]protocol.QualityLevel{}
	largest := 0.0
	for _, l := range levels {
		byLevel[l.Difficulty] = l
		for _, f := range l.Families {
			largest = max(largest, f.Headache)
		}
	}
	familyWidth := 5
	for _, l := range levels {
		for _, f := range l.Families {
			familyWidth = max(familyWidth, ansi.StringWidth(sanitize(f.Family)))
		}
	}
	body := []string{qualitySelector(q, m.qualityAgent, w)}
	for index, name := range []string{"simple", "normal", "hard", "very_complex"} {
		l := byLevel[name]
		if index > 0 {
			body = append(body, "")
		}
		body = append(body, sBold.Render(fmt.Sprintf("%s · %d tasks", strings.ReplaceAll(name, "_", " "), l.Tasks)))
		header := []string{cell("MODEL", familyWidth), cellR("MY TIME / TASK", 14), cellR("TASKS", 5), cellR("NEEDED HELP", 11), cellR("NUDGES / TASK", 13), cellR("LATE BUGS", 9), cellR("WORST", 5), cellR("RATING", 6), cellR("PICKED", 6)}
		narrow := ansi.StringWidth(strings.Join(header, "  ")) > w
		if narrow {
			body = append(body, sBold.Render(strings.Join(header[:5], "  ")), sBold.Render(strings.Repeat(" ", familyWidth+2)+strings.Join(header[5:], "  ")))
		} else {
			body = append(body, sBold.Render(strings.Join(header, "  ")))
		}
		families := append([]protocol.QualityFamily(nil), l.Families...)
		sortQualityFamilies(families)
		for _, f := range families {
			bar := 0.0
			if largest > 0 {
				bar = f.Headache / largest
			}
			worst := "-"
			if f.WorstSeverity > 0 {
				worst = fmt.Sprint(f.WorstSeverity)
			}
			rating := "-"
			if f.AvgRating != nil {
				rating = fmt.Sprintf("%.1f", *f.AvgRating)
			}
			picked := ""
			if picks[name] == f.Family {
				picked = "yes"
			}
			few := ""
			if f.Tasks < protocol.CalibrationMinTasks {
				few = fmt.Sprintf(" (only %d tasks)", f.Tasks)
			}
			timeValue := fmt.Sprintf("%3.0f min ", f.Headache) + progressBar(bar, 5, roleAccent)
			timeValue = strings.Repeat(" ", max(0, 14-ansi.StringWidth(timeValue))) + timeValue
			columns := []string{cell(f.Family, familyWidth), timeValue, cellR(fmt.Sprint(f.Tasks), 5), cellR(fmt.Sprintf("%.0f%%", f.NeededHelp*100), 11), cellR(fmt.Sprintf("%.2f", float64(f.Continues)/float64(max(f.Tasks, 1))), 13), cellR(fmt.Sprint(f.LateDefects), 9), cellR(worst, 5), cellR(rating, 6), cellR(picked, 6)}
			var lines []string
			if narrow {
				lines = []string{strings.Join(columns[:5], "  "), strings.Repeat(" ", familyWidth+2) + strings.Join(columns[5:], "  ") + few}
			} else {
				lines = []string{strings.Join(columns, "  ") + few}
			}
			for _, line := range lines {
				if f.Tasks < protocol.CalibrationMinTasks {
					line = sDim.Render(line)
				}
				body = append(body, clip(line, w))
			}
			if picked != "" && reasons[name] != "" {
				body = append(body, clip(fmt.Sprintf("▸ %s (%s)", f.Family, sanitize(reasons[name])), w))
			}
		}
	}
	areas := q.Areas
	if m.qualityAgent > 0 && m.qualityAgent <= len(q.Agents) {
		areas = q.Agents[m.qualityAgent-1].Areas
	}
	if len(areas) > 0 {
		body = append(body, "", sBold.Render("by touched area"), sBold.Render(strings.Join([]string{cell("AREA", 8), cellR("TASKS", 5), cellR("FOLLOW-UPS", 10), cellR("LATE DEFECTS", 12), cellR("HEADACHE", 8)}, "  ")))
		for _, a := range areas {
			line := strings.Join([]string{cell(a.Family, 8), cellR(fmt.Sprint(a.Tasks), 5), cellR(fmt.Sprintf("%.2f", a.FollowUps), 10), cellR(qualityLateDefects(a), 12), cellR(fmt.Sprintf("%.0f min", a.Headache), 8)}, "  ")
			if a.Tasks < protocol.CalibrationMinTasks {
				line = sDim.Render(line)
			}
			body = append(body, line)
		}
	}
	legend := wrap("NEEDED HELP: share of tasks with at least one nudge or fix. WORST: highest severity among late bugs (1 to 4).", w)
	room := max(0, h-1-len(legend))
	offset := min(m.quotaScroll, max(0, len(body)-room))
	end := min(len(body), offset+room)
	out := []string{sBold.Render("Quality analysis · details")}
	out = append(out, body[offset:end]...)
	for len(out) < h-len(legend) {
		out = append(out, "")
	}
	for _, line := range legend {
		out = append(out, sDim.Render(line))
	}
	return out
}

// qualityTaskCount is a card's count column, "·  9 this week", right-aligned
// so the rows of a card line up. The 30-day total is in the card title.
func qualityTaskCount(f protocol.QualityFamily, weekDigits int) string {
	return fmt.Sprintf("· %*d this week", weekDigits, f.TasksWeek)
}
