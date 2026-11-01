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
		return qualityView(cal.Quality, w, m.qualityAgent, m.qualityDetail)
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
			part := fmt.Sprintf("%s %d", r.Model, r.Count)
			if r.Defects > 0 {
				part += fmt.Sprintf(" (%d later)", r.Defects)
			}
			parts = append(parts, part)
		}
		for _, line := range wrap("ratings: "+strings.Join(parts, " · "), w-4) {
			body = append(body, sDim.Render(line))
		}
	} else {
		body = append(body, sDim.Render(clip("ratings: none yet", w-4)))
	}
	body = append(body, sDim.Render(clip(fmt.Sprintf("defects reported later: %d", cal.Defects), w-4)))
	body = append(body, sDim.Render(clip(fmt.Sprintf("last 7 finished tasks, at least 5 · rework = fix + %g * continue + defects", protocol.CalibrationContinueWeight), w-4)))
	return append(panel("Difficulty calibration · read only", body, w, roleAccent), qualityView(cal.Quality, w, m.qualityAgent, m.qualityDetail)...)
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

// qualityView only formats the gateway snapshot. Fitting, family folding,
// level buckets and per-caller tables all belong to the gateway; View picks a
// cached table.
func qualityView(q protocol.QualityAnalysis, w, agent int, detail bool) []string {
	if q.Status == "" {
		return nil
	}
	const tasksW, fixesW, continuesW, followW, lateW, headacheW = 5, 5, 9, 10, 12, 8
	gaps := 4
	fixed := tasksW + followW + lateW + headacheW + gaps
	if detail {
		gaps = 6
		fixed = tasksW + fixesW + continuesW + followW + lateW + headacheW + gaps
	}
	inner := max(w-4, 8)
	familyW := max(inner-fixed, 8)
	body := []string{qualitySelector(q, agent, w), sBold.Render(clip(qualityColumnHeader(familyW, detail), inner))}
	for _, level := range qualityLevels(q, agent) {
		label := fmt.Sprintf("%s · %d tasks", level.Difficulty, level.Tasks)
		if level.Difficulty == "unknown" {
			body = append(body, sDim.Render(clip(label, w-4)))
		} else {
			body = append(body, sBold.Render(clip(label, w-4)))
		}
		for _, f := range level.Families {
			line := qualityFamilyLine(f, familyW, detail)
			if qualityFamilyDim(level.Difficulty, f.Tasks) {
				line = sDim.Render(line)
			}
			body = append(body, clip(line, inner))
		}
	}
	body = append(body, sDim.Render(clip(qualityRatingsLine(q), w-4)))
	return panel("Quality analysis · read only", body, w, roleAccent)
}

// qualitySelector is the panel's first line: the agent cycled by left and
// right. An empty or stale selection falls back to all agents.
func qualitySelector(q protocol.QualityAnalysis, agent int, w int) string {
	name := "all agents"
	if agent > 0 && agent <= len(q.Agents) {
		name = strings.TrimSpace(q.Agents[agent-1].Name)
		if name == "" {
			name = shortID(q.Agents[agent-1].Caller)
		}
	}
	return sBold.Render(clip("agent ◂ "+name+" ▸", w-4))
}

// qualityLevels returns the cached level tables for the chosen agent, or the
// all-agent tables when none is chosen.
func qualityLevels(q protocol.QualityAnalysis, agent int) []protocol.QualityLevel {
	if agent > 0 && agent <= len(q.Agents) {
		return q.Agents[agent-1].Levels
	}
	return q.Levels
}

func qualityColumnHeader(familyW int, detail bool) string {
	parts := []string{cell("FAMILY", familyW), cellR("TASKS", 5)}
	if detail {
		parts = append(parts, cellR("FIXES", 5), cellR("CONTINUES", 9))
	}
	parts = append(parts, cellR("FOLLOW-UPS", 10), cellR("LATE DEFECTS", 12), cellR("HEADACHE", 8))
	return strings.Join(parts, " ")
}

func qualityFamilyLine(f protocol.QualityFamily, familyW int, detail bool) string {
	parts := []string{cell(f.Family, familyW), cellR(strconv.Itoa(f.Tasks), 5)}
	if detail {
		parts = append(parts, cellR(strconv.Itoa(f.Fixes), 5), cellR(strconv.Itoa(f.Continues), 9))
	}
	parts = append(parts,
		cellR(fmt.Sprintf("%.2f", f.FollowUps), 10),
		cellR(qualityLateDefects(f), 12),
		cellR(fmt.Sprintf("%.2f", f.Headache), 8))
	return strings.Join(parts, " ")
}

func qualityLateDefects(f protocol.QualityFamily) string {
	if f.LateDefects <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d · avg %.1f", f.LateDefects, f.LateDefectSeverity)
}

// qualityFamilyDim marks a level with no requested difficulty and a family
// with too few tasks to rank.
func qualityFamilyDim(level string, tasks int) bool {
	return level == "unknown" || tasks < protocol.CalibrationMinTasks
}

// qualityRatingsLine is the single dim line below all tables. It always gives
// the real rating count; once the fit is big enough it adds the factors that
// matter most, largest first.
func qualityRatingsLine(q protocol.QualityAnalysis) string {
	line := fmt.Sprintf("ratings: %d so far", q.Ratings)
	if q.Ratings >= protocol.QualityAnalysisMinRatings && len(q.Summary) > 0 {
		names := make([]string, 0, len(q.Summary))
		for _, f := range q.Summary {
			names = append(names, f.Kind)
		}
		return line + "; what matters most: " + strings.Join(names, " / ")
	}
	return line + fmt.Sprintf("; the analysis starts at %d", protocol.QualityAnalysisMinRatings)
}
