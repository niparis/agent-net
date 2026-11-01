package tui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/protocol"
)

// calibrationView shows the cached quality analysis in the Quotas tab.
func (m Model) calibrationView(w int) []string {
	return qualityView(m.snap.Calibration, w, m.qualityAgent, m.qualityDetail)
}

// qualityView only formats the gateway snapshot. Fitting, family folding,
// level buckets and per-caller tables all belong to the gateway; View picks a
// cached table.
func qualityView(cal protocol.Calibration, w, agent int, detail bool) []string {
	q := cal.Quality
	if q.Status == "" {
		return nil
	}
	levels := qualityLevels(q, agent)
	familyW := qualityFamilyWidth(levels)
	body := []string{qualitySelector(q, agent, w), sBold.Render(clip(qualityColumnHeader(familyW, detail), w-4))}
	for _, level := range levels {
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
			body = append(body, clip(line, w-4))
		}
	}
	areas := q.Areas
	if agent > 0 && agent <= len(q.Agents) {
		areas = q.Agents[agent-1].Areas
	}
	if len(areas) > 0 {
		body = append(body, sBold.Render(clip("by touched area", w-4)))
		body = append(body, sBold.Render(clip(strings.Replace(qualityColumnHeader(8, false), "FAMILY", "AREA  ", 1), w-4)))
		for _, area := range areas {
			line := qualityFamilyLine(area, 8, false)
			if area.Tasks < protocol.CalibrationMinTasks {
				line = sDim.Render(line)
			}
			body = append(body, clip(line, w-4))
		}
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
	body = append(body, sDim.Render(clip(qualityRatingsLine(q), w-4)))
	for _, line := range wrap(qualityHeadacheFootnote(), w-4) {
		body = append(body, sDim.Render(line))
	}
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

func qualityFamilyWidth(levels []protocol.QualityLevel) int {
	w := len("FAMILY")
	for _, level := range levels {
		for _, f := range level.Families {
			if n := utf8.RuneCountInString(f.Family); n > w {
				w = n
			}
		}
	}
	// The known families are eight characters at most. A long unknown model
	// name is cut with an ellipsis instead of pushing the numbers right.
	return min(max(w, len("FAMILY")), 9)
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
		cellR(fmt.Sprintf("%.0f min", f.Headache), 8))
	return strings.Join(parts, " ")
}

func qualityLateDefects(f protocol.QualityFamily) string {
	if f.LateDefects <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d · avg %.1f", f.LateDefects, f.LateDefectSeverity)
}

// qualityHeadacheFootnote names the orchestrator costs behind HEADACHE.
func qualityHeadacheFootnote() string {
	return fmt.Sprintf("headache = orchestrator minutes: continue %d, fix %d, late defect %d/%d/%d/%d by severity",
		protocol.QualityContinueMinutes, protocol.QualityFixMinutes,
		protocol.QualityDefectMinutesSeverity1, protocol.QualityDefectMinutesSeverity2,
		protocol.QualityDefectMinutesSeverity3, protocol.QualityDefectMinutesSeverity4)
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
