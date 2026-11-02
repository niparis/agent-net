package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

func qualityCards(q protocol.QualityAnalysis, w, agent int, picks map[string]string, machine string) []string {
	if q.Status == "" {
		return nil
	}
	levels := qualityLevels(q, agent)
	byLevel := map[string]protocol.QualityLevel{}
	largest := 0.0
	for _, level := range levels {
		byLevel[level.Difficulty] = level
		for _, f := range level.Families {
			if f.Headache > largest {
				largest = f.Headache
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
			var eligible []protocol.QualityFamily
			few := 0
			for _, f := range level.Families {
				if f.Tasks < protocol.CalibrationMinTasks {
					few++
				} else {
					eligible = append(eligible, f)
				}
			}
			sort.Slice(eligible, func(a, b int) bool {
				if eligible[a].Headache == eligible[b].Headache {
					return eligible[a].Family < eligible[b].Family
				}
				return eligible[a].Headache < eligible[b].Headache
			})
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
				rows = append(rows, fmt.Sprintf("%s %s %.0f min %s · %d tasks%s", marker, f.Family, f.Headache, progressBar(frac, 5, roleAccent), f.Tasks, bug))
			}
			if len(eligible) > 3 {
				var rest []string
				for _, f := range eligible[3:] {
					rest = append(rest, fmt.Sprintf("%s %.0f min", f.Family, f.Headache))
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
	legend := "My time = minutes the orchestrator spends after a worker: nudges, fixes and bugs found later."
	if machine != "" {
		legend = "▸ = the model the picker would choose on " + machine + ".  " + legend
	}
	for _, line := range wrap(legend, w-4) {
		body = append(body, sDim.Render(line))
	}
	return panel("Quality analysis · read only", body, w, roleAccent)
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
	body := []string{qualitySelector(q, m.qualityAgent, w)}
	for _, name := range []string{"simple", "normal", "hard", "very_complex"} {
		l := byLevel[name]
		body = append(body, sBold.Render(fmt.Sprintf("%s · %d tasks", strings.ReplaceAll(name, "_", " "), l.Tasks)))
		if w < 95 {
			body = append(body, sBold.Render("MODEL      MY TIME / TASK  TASKS NEEDED HELP NUDGES / TASK"), sBold.Render("           LATE BUGS  WORST  RATING  PICKED"))
		} else {
			body = append(body, sBold.Render("MODEL      MY TIME / TASK  TASKS NEEDED HELP NUDGES / TASK LATE BUGS WORST RATING PICKED"))
		}
		families := append([]protocol.QualityFamily(nil), l.Families...)
		sort.Slice(families, func(i, j int) bool { return families[i].Headache < families[j].Headache })
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
			line := fmt.Sprintf("%-10s %3.0f min %s %5d %10.0f%% %13.2f %9d %5s %6s %6s%s", f.Family, f.Headache, progressBar(bar, 5, roleAccent), f.Tasks, f.NeededHelp*100, float64(f.Continues)/float64(max(f.Tasks, 1)), f.LateDefects, worst, rating, picked, few)
			if w < 95 {
				line = fmt.Sprintf("%-10s %3.0f min %s %5d %10.0f%% %13.2f", f.Family, f.Headache, progressBar(bar, 5, roleAccent), f.Tasks, f.NeededHelp*100, float64(f.Continues)/float64(max(f.Tasks, 1)))
				second := fmt.Sprintf("           %9d %6s %7s %7s%s", f.LateDefects, worst, rating, picked, few)
				if f.Tasks < protocol.CalibrationMinTasks {
					line = sDim.Render(line)
					second = sDim.Render(second)
				}
				body = append(body, clip(line, w), clip(second, w))
			} else {
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
		body = append(body, sBold.Render("by touched area"), strings.Replace(qualityColumnHeader(8, false), "FAMILY", "AREA  ", 1))
		for _, a := range areas {
			line := qualityFamilyLine(a, 8, false)
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
