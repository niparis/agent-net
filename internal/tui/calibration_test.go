package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// qualityLevelSnapshot is a cached per-level table shaped like today's data.
func qualityLevelSnapshot() protocol.QualityAnalysis {
	return protocol.QualityAnalysis{
		Status:  "tentative: overlap and more ratings needed",
		Ratings: 40,
		Summary: []protocol.QualityFactor{
			{Kind: "model", Size: .8},
			{Kind: "effort", Size: .3},
			{Kind: "harness", Size: .1},
		},
		Levels: []protocol.QualityLevel{
			{Difficulty: "simple", Tasks: 8, Families: []protocol.QualityFamily{
				{Family: "luna", Tasks: 5, TasksWeek: 3, Fixes: 1, Continues: 0, FollowUps: .20, Headache: 2},
				{Family: "deepseek", Tasks: 1, TasksWeek: 1, Fixes: 0, Continues: 1, FollowUps: .33, Headache: 2},
				{Family: "sol", Tasks: 1, Fixes: 0, Continues: 0, FollowUps: 0, Headache: 0},
				{Family: "terra", Tasks: 1, Fixes: 1, Continues: 0, FollowUps: 1.00, Headache: 10},
			}},
			{Difficulty: "normal", Tasks: 154, Families: []protocol.QualityFamily{
				{Family: "deepseek", Tasks: 66, TasksWeek: 22, Fixes: 6, Continues: 0, FollowUps: .09, Headache: 60.0 / 66.0},
				{Family: "terra", Tasks: 53, TasksWeek: 18, Fixes: 28, Continues: 30, FollowUps: .72, Headache: 340.0 / 53.0},
				{Family: "luna", Tasks: 19, TasksWeek: 6, Fixes: 5, Continues: 0, FollowUps: .26, Headache: 50.0 / 19.0},
				{Family: "sol", Tasks: 9, TasksWeek: 4, Fixes: 2, Continues: 1, FollowUps: .26, Headache: 22.0 / 9.0},
				{Family: "opus", Tasks: 7, TasksWeek: 1, Fixes: 0, Continues: 0, FollowUps: 0, Headache: 0},
			}},
			{Difficulty: "hard", Tasks: 33, Families: []protocol.QualityFamily{
				{Family: "terra", Tasks: 25, TasksWeek: 9, Fixes: 12, Continues: 10, FollowUps: .61, LateDefects: 1, LateDefectSeverity: 4, Headache: 320.0 / 25.0},
				{Family: "sol", Tasks: 8, TasksWeek: 3, Fixes: 3, Continues: 1, FollowUps: .42, Headache: 4},
			}},
			{Difficulty: "unknown", Tasks: 23, Families: []protocol.QualityFamily{
				{Family: "deepseek", Tasks: 12, Fixes: 2, Continues: 0, FollowUps: .17, Headache: 20.0 / 12.0},
				{Family: "sol", Tasks: 8, Fixes: 1, Continues: 0, FollowUps: .13, Headache: 10.0 / 8.0},
				{Family: "terra", Tasks: 3, Fixes: 3, Continues: 0, FollowUps: 1.00, Headache: 10},
			}},
		},
		Agents: []protocol.QualityAgent{
			{Caller: "s_alpha", Name: "agent-net", Levels: []protocol.QualityLevel{
				{Difficulty: "normal", Tasks: 20, Families: []protocol.QualityFamily{
					{Family: "terra", Tasks: 20, Fixes: 10, Continues: 10, FollowUps: .665, Headache: 6},
				}},
				{Difficulty: "hard", Tasks: 8, Families: []protocol.QualityFamily{
					{Family: "sol", Tasks: 8, Fixes: 3, Continues: 1, FollowUps: .416, Headache: 4},
				}},
			}},
			{Caller: "s_beta", Name: "homelab-agent", Levels: []protocol.QualityLevel{
				{Difficulty: "simple", Tasks: 5, Families: []protocol.QualityFamily{
					{Family: "luna", Tasks: 5, Fixes: 1, Continues: 0, FollowUps: .20, Headache: 2},
				}},
				{Difficulty: "unknown", Tasks: 12, Families: []protocol.QualityFamily{
					{Family: "deepseek", Tasks: 12, Fixes: 2, Continues: 0, FollowUps: .1667, Headache: 20.0 / 12.0},
				}},
			}},
		},
	}
}

func calibrationSnapshot() protocol.Calibration {
	return protocol.Calibration{
		Callers: []protocol.CalibrationCaller{
			{Caller: "s_agentnet", Name: "agent-net", Model: "claude-opus-5-5", Strong: true, Levels: []protocol.CalibrationLevel{
				{Difficulty: "normal", Median: 0, Tasks: 9, Action: protocol.CalibrationLower, Offset: -1},
				{Difficulty: "hard", Median: 1, Tasks: 7, Action: protocol.CalibrationKeep},
			}},
			{Caller: "s_homelab", Name: "homelab-agent", Model: "gpt-6.1-terra", Strong: false, Levels: []protocol.CalibrationLevel{
				{Difficulty: "simple", Median: 0.5, Tasks: 3, Action: protocol.CalibrationNotEnough},
				{Difficulty: "normal", Median: 0, Tasks: 5, Action: protocol.CalibrationKeep},
			}},
		},
		Network: []protocol.CalibrationLevel{
			{Difficulty: "normal", Median: 1, Tasks: 9, Action: protocol.CalibrationKeep},
			{Difficulty: "hard", Median: 0.5, Tasks: 12, Action: protocol.CalibrationKeep},
		},
		Quality: qualityLevelSnapshot(),
		Ratings: []protocol.CalibrationRating{
			{Model: "deepseek-v4.1-flash", Count: 3, Defects: 1},
			{Model: "gpt-6.1-sol", Count: 14},
		},
		Defects: 2,
	}
}

// quotaPanelText also checks that a visible panel closes before the next one.
func quotaPanelText(t *testing.T, view, title string) string {
	t.Helper()
	start := strings.Index(view, "╭─"+title)
	if start < 0 {
		t.Fatalf("missing %s panel:\n%s", title, view)
	}
	var body []string
	for i, line := range strings.Split(view[start:], "\n") {
		if i > 0 && strings.HasPrefix(line, "╭") {
			t.Fatalf("%s panel has no bottom border:\n%s", title, view)
		}
		body = append(body, line)
		if strings.HasPrefix(line, "╰") {
			return strings.Join(body, "\n")
		}
	}
	t.Fatalf("%s panel has no bottom border:\n%s", title, view)
	return ""
}

func TestQuotasHasNoQualityPanel(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := newModel(size[0], size[1])
		m.tab = quotasTab
		m.snap.Calibration = calibrationSnapshot()
		view := ansi.Strip(m.testView())
		if strings.Contains(view, "Quality analysis") {
			t.Errorf("Quality panel remains in Quotas:\n%s", view)
		}
		for _, key := range []string{"d", "left", "right"} {
			if keyAction(contextQuotas, key) != "" {
				t.Errorf("Quality-only key %q remains in Quotas", key)
			}
		}
		golden(t, fmt.Sprintf("calibration_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestQuotaOverflowPreservesAllPanelBordersAcrossHeights(t *testing.T) {
	// Include the room available with both the current CTO box and a shorter
	// box. Scroll through Subscriptions and Workers, including joins.
	for _, height := range []int{8, 12, 17, 22, 28} {
		m := newModel(120, 34)
		m.snap.Calibration = calibrationSnapshot()
		m.snap.WorkerProfiles = []protocol.WorkerProfile{{Harness: "codex", Model: "gpt-6.1-sol"}}
		var seen strings.Builder
		for scroll := range 80 {
			m.quotaScroll = scroll
			rows := m.quotasView(120, height)
			if len(rows) != height {
				t.Fatalf("height %d scroll %d: got %d rows", height, scroll, len(rows))
			}
			open := false
			for _, row := range rows {
				row = ansi.Strip(row)
				seen.WriteString(row + "\n")
				switch {
				case strings.HasPrefix(row, "╭"):
					if open {
						t.Fatalf("height %d scroll %d: panel lost its bottom border", height, scroll)
					}
					open = true
				case strings.HasPrefix(row, "╰"):
					if !open {
						t.Fatalf("height %d scroll %d: panel lost its top border", height, scroll)
					}
					open = false
				case strings.HasPrefix(row, "│"):
					if !open {
						t.Fatalf("height %d scroll %d: panel body lost its top border", height, scroll)
					}
				}
			}
			if open {
				t.Fatalf("height %d scroll %d: panel lost its bottom border", height, scroll)
			}
		}
		for _, want := range []string{"Subscriptions", "Workers"} {
			if !strings.Contains(seen.String(), want) {
				t.Errorf("height %d: scrolling never showed %q", height, want)
			}
		}
	}
}

func TestQuotaCalibrationGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 40}, {120, 34}} {
		m := newModel(size[0], size[1])
		m.tab = 2
		m.snap.Calibration = calibrationSnapshot()
		view := m.testView()
		for _, line := range lines(view) {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("wide line at %d columns: %q", size[0], line)
			}
		}
		golden(t, fmt.Sprintf("calibration_%dx%d", size[0], size[1]), view)
	}
}
