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

func (m Model) qualityDetailsView(w, h int) []string {
	q := m.snap.Calibration.Quality
	var picks, reasons map[string]string
	for _, n := range m.snap.Nodes {
		if n.NodeID != "" && n.NodeID == m.snap.NodeID {
			// The details list models with their effort, so the pick names one
			// too; an older gateway sends only the family.
			picks = n.QualityPickModels
			if len(picks) == 0 {
				picks = n.QualityPicks
			}
			reasons = n.QualityPickReasons
			break
		}
	}
	levels := qualityLevels(q, m.qualityAgent)
	byLevel := map[string]protocol.QualityLevel{}
	familyWidth := 5
	for _, l := range levels {
		// One row per model and effort; an older gateway has families only.
		if len(l.Models) > 0 {
			l.Families = l.Models
		}
		byLevel[l.Difficulty] = l
		for _, f := range l.Families {
			familyWidth = max(familyWidth, ansi.StringWidth(sanitize(f.Family)))
		}
	}
	body := []string{qualitySelector(q, m.qualityAgent, w)}
	for _, name := range []string{"simple", "normal", "hard", "very_complex"} {
		body = append(body, qualityDetailsLevel(byLevel[name], name, picks[name], reasons[name], familyWidth, w)...)
	}
	legend := wrap("NEEDED HELP: share of tasks with at least one nudge or fix. LATE BUGS: bugs found after the hand-back, with the worst severity (1 to 4).", w)
	room := max(0, h-1-len(legend))
	offset := min(m.quotaScroll, max(0, len(body)-room))
	end := min(len(body), offset+room)
	out := []string{sBold.Render("Quality analysis · details") + sDim.Render("   j/k scroll · esc back")}
	out = append(out, body[offset:end]...)
	for len(out) < h-len(legend) {
		out = append(out, "")
	}
	for _, line := range legend {
		out = append(out, sDim.Render(line))
	}
	return out
}

// qualityDetailsColumn is one column of a details table. drop orders the
// columns that give way on a narrow screen: higher goes first, 0 never.
type qualityDetailsColumn struct {
	head  string
	width int
	right bool
	drop  int
}

// qualityDetailsLevel is one level's box: the ranked models in the cards'
// order, each with its bar, and one dim line for models with too few tasks to
// rank, which get no bar.
func qualityDetailsLevel(l protocol.QualityLevel, name, pick, reason string, familyWidth, w int) []string {
	title := fmt.Sprintf("%s · %s", strings.ReplaceAll(name, "_", " "), plural(l.Tasks, "task"))
	if pick != "" {
		title += " · picker: ▸ " + sanitize(pick)
		if reason != "" {
			title += " (" + sanitize(reason) + ")"
		}
	}
	ranked, _ := qualityRankedFamilies(l.Families)
	var few []protocol.QualityFamily
	for _, f := range l.Families {
		if f.Tasks < protocol.CalibrationMinTasks {
			few = append(few, f)
		}
	}
	largest := 0.0
	for _, f := range ranked {
		largest = max(largest, math.Round(f.Headache))
	}
	cols := []qualityDetailsColumn{
		{head: "MODEL", width: 2 + familyWidth},
		{head: "MY TIME / TASK", width: 14, right: true},
		{head: "THIS WEEK", width: 9, right: true, drop: 2},
		{head: "TASKS", width: 5, right: true, drop: 3},
		{head: "NEEDED HELP", width: 11, right: true},
		{head: "LATE BUGS", width: 12, right: true},
		{head: "RATING", width: 6, right: true, drop: 1},
	}
	inner := w - 4
	for total := 0; ; total = 0 {
		for _, c := range cols {
			total += c.width + 2
		}
		if total-2 <= inner {
			break
		}
		worst := -1
		for i, c := range cols {
			if c.drop > 0 && (worst < 0 || c.drop > cols[worst].drop) {
				worst = i
			}
		}
		if worst < 0 {
			break
		}
		cols = append(cols[:worst], cols[worst+1:]...)
	}
	row := func(values map[string]string) string {
		parts := make([]string, 0, len(cols))
		for _, c := range cols {
			v := values[c.head]
			if c.right {
				v = strings.Repeat(" ", max(0, c.width-ansi.StringWidth(v))) + v
			} else {
				v = cell(v, c.width)
			}
			parts = append(parts, v)
		}
		return strings.Join(parts, "  ")
	}
	heads := map[string]string{}
	for _, c := range cols {
		heads[c.head] = c.head
	}
	var lines []string
	if len(ranked) == 0 {
		lines = append(lines, sDim.Render("no model has enough tasks to rank yet"))
	} else {
		lines = append(lines, sBold.Render(row(heads)))
	}
	for _, f := range ranked {
		marker := "  "
		if pick == f.Family {
			marker = "▸ "
		}
		frac := 0.0
		if largest > 0 {
			frac = math.Round(f.Headache) / largest
		}
		late := "-"
		if f.LateDefects > 0 {
			late = fmt.Sprintf("%d, worst %d", f.LateDefects, f.WorstSeverity)
		}
		rating := "-"
		if f.AvgRating != nil {
			rating = fmt.Sprintf("%.1f", *f.AvgRating)
		}
		lines = append(lines, row(map[string]string{
			"MODEL":          marker + sanitize(f.Family),
			"MY TIME / TASK": fmt.Sprintf("%.0f min ", f.Headache) + progressBar(frac, 5, roleAccent),
			"THIS WEEK":      fmt.Sprint(f.TasksWeek),
			"TASKS":          fmt.Sprint(f.Tasks),
			"NEEDED HELP":    fmt.Sprintf("%.0f%%", f.NeededHelp*100),
			"LATE BUGS":      late,
			"RATING":         rating,
		}))
	}
	if len(few) > 0 {
		names := make([]string, 0, len(few))
		for _, f := range few {
			names = append(names, fmt.Sprintf("%s %.0f min (%s)", sanitize(f.Family), f.Headache, plural(f.Tasks, "task")))
		}
		for _, line := range wrap("too few tasks to rank: "+strings.Join(names, " · "), inner) {
			lines = append(lines, sDim.Render(line))
		}
	}
	return panel(title, lines, w, roleAccent)
}

// qualityTaskCount is a card's count column, "·  9 this week", right-aligned
// so the rows of a card line up. The 30-day total is in the card title.
func qualityTaskCount(f protocol.QualityFamily, weekDigits int) string {
	return fmt.Sprintf("· %*d this week", weekDigits, f.TasksWeek)
}
