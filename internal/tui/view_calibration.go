package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/niparis/agent-net/internal/protocol"
)

// calibrationView is the read-only difficulty table in the Quotas tab. It
// shows, per caller and difficulty, the median weighted rework of the last
// seven finished tasks and what the loop would do with the caller's label.
// Part 2 never changes a pick, so the panel says so.
func (m Model) calibrationView(w int) []string {
	cal := m.snap.Calibration
	if len(cal.Callers) == 0 && len(cal.Network) == 0 {
		return qualityView(cal.Quality, w)
	}
	callerWidth, levelWidth, medianWidth, tasksWidth, actionWidth := 16, 13, 6, 5, 16
	if w >= 100 {
		callerWidth, medianWidth, actionWidth = 22, 7, 18
	}
	header := fmt.Sprintf("%s %s %s %s %s",
		cell("CALLER", callerWidth), cell("LEVEL", levelWidth),
		cellR("MEDIAN", medianWidth), cellR("TASKS", tasksWidth), cell("WOULD", actionWidth))
	body := []string{sBold.Render(clip(header, w-4))}
	weak := false
	for _, c := range cal.Callers {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			name = shortID(c.Caller)
		}
		if !c.Strong {
			name += " *"
			weak = true
		}
		byLevel := map[string]protocol.CalibrationLevel{}
		for _, l := range c.Levels {
			byLevel[l.Difficulty] = l
		}
		for _, level := range protocol.DifficultyLevels {
			l, ok := byLevel[level]
			if !ok {
				continue
			}
			line := fmt.Sprintf("%s %s %s %s %s",
				cell(name, callerWidth), cell(l.Difficulty, levelWidth),
				cellR(calibrationMedian(l.Median), medianWidth), cellR(strconv.Itoa(l.Tasks), tasksWidth),
				calibrationAction(l.Action, actionWidth))
			body = append(body, clip(line, w-4))
		}
	}
	if weak {
		body = append(body, sDim.Render(clip("* weak caller: follows the network and cannot lower a tier", w-4)))
	}
	if len(cal.Network) > 0 {
		var parts []string
		for _, l := range cal.Network {
			parts = append(parts, fmt.Sprintf("%s %s · %d tasks · %s",
				l.Difficulty, calibrationMedian(l.Median), l.Tasks, l.Action))
		}
		for _, line := range wrap("network: "+strings.Join(parts, " · "), w-4) {
			body = append(body, sDim.Render(line))
		}
	} else {
		body = append(body, sDim.Render(clip("network: no strong caller data yet", w-4)))
	}
	if len(cal.Ratings) > 0 {
		var parts []string
		for _, r := range cal.Ratings {
			parts = append(parts, fmt.Sprintf("%s %d", r.Model, r.Count))
		}
		for _, line := range wrap("ratings: "+strings.Join(parts, " · "), w-4) {
			body = append(body, sDim.Render(line))
		}
	} else {
		body = append(body, sDim.Render(clip("ratings: none yet", w-4)))
	}
	body = append(body, sDim.Render(clip("last 7 finished tasks, at least 5 · rework = fix + 0.5 * continue", w-4)))
	return append(panel("Difficulty calibration · read only", body, w, roleAccent), qualityView(cal.Quality, w)...)
}

func calibrationMedian(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func calibrationAction(action string, w int) string {
	text := cell(action, w)
	switch action {
	case protocol.CalibrationRaise, protocol.CalibrationLower:
		return lipgloss.NewStyle().Foreground(roleWarning).Render(text)
	default:
		return sDim.Render(text)
	}
}

// qualityView only formats the gateway snapshot. Fitting belongs to the gateway.
func qualityView(q protocol.QualityAnalysis, w int) []string {
	status := q.Status
	if status == "" {
		return nil
	}
	if q.Ratings == 0 && len(q.Effects) == 0 && len(q.Profiles) == 0 {
		return panel("Quality analysis · read only", []string{sDim.Render("not enough ratings")}, w, roleAccent)
	}
	body := []string{sDim.Render(clip(status, w-4))}
	for _, section := range []struct{ kind, title string }{{"model", "Model matters"}, {"harness", "Harness matters"}, {"effort", "Effort matters (within model)"}} {
		body = append(body, sBold.Render(clip(section.title+" · quality / rework (tasks)", w-4)))
		found := false
		for _, e := range q.Effects {
			if e.Kind != section.kind {
				continue
			}
			found = true
			weak := ""
			if e.Ratings < 5 {
				weak = ", weak"
			}
			rweak := ""
			if e.Runs < 5 {
				rweak = ", weak"
			}
			text := fmt.Sprintf("%s  %+.2f (%d%s) / %+.2f (%d%s)", e.Name, e.Quality, e.Ratings, weak, e.Rework, e.Runs, rweak)
			for _, line := range wrap(text, w-4) {
				body = append(body, line)
			}
		}
		if !found {
			body = append(body, sDim.Render("not enough ratings"))
		}
	}
	body = append(body, sBold.Render("Predicted quality · normal difficulty"))
	for _, p := range q.Profiles {
		if q.Ratings == 0 {
			for _, line := range wrap(p.Name+"  not enough ratings", w-4) {
				body = append(body, line)
			}
			continue
		}
		note := ""
		if p.Ratings < 5 {
			note = ", weak"
		}
		if p.Unexpected {
			note += ", differs from fit"
		}
		for _, line := range wrap(fmt.Sprintf("%s  %.2f · %d ratings, %d runs%s", p.Name, p.Quality, p.Ratings, p.Runs, note), w-4) {
			body = append(body, line)
		}
	}
	return panel("Quality analysis · read only", body, w, roleAccent)
}
