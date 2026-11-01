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
		return nil
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
	return panel("Difficulty calibration · read only", body, w, roleAccent)
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
