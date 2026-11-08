package tui

import (
	"fmt"
	"math"
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
		switch level.Difficulty {
		case "simple", "normal", "hard", "very_complex":
			ranked, _ := qualityRankedFamilies(level.Families)
			for _, f := range ranked[:min(len(ranked), 3)] {
				// The bars compare the rows the cards show, not models with too
				// few tasks to rank, which would shrink every bar to one cell.
				largest = max(largest, math.Round(f.Headache))
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
				// The bar follows the minutes as written: "0 min" is an empty bar.
				frac := 0.0
				if largest > 0 {
					frac = math.Round(f.Headache) / largest
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
	return panel("Quality analysis · read only · d details", body, w, roleAccent)
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

// qualityTaskCount is a card's count column, "·  9 this week", right-aligned
// so the rows of a card line up. The 30-day total is in the card title.
func qualityTaskCount(f protocol.QualityFamily, weekDigits int) string {
	return fmt.Sprintf("· %*d this week", weekDigits, f.TasksWeek)
}
