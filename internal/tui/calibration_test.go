package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

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
		Ratings: []protocol.CalibrationRating{
			{Model: "deepseek-v4.1-flash", Count: 3},
			{Model: "gpt-6.1-sol", Count: 14},
		},
	}
}

func TestQuotaCalibrationTable(t *testing.T) {
	m := newModel(80, 40)
	m.tab = 2
	m.snap.Calibration = calibrationSnapshot()
	view := ansi.Strip(strings.Join(m.quotasView(80, 40), "\n"))
	for _, want := range []string{
		"Difficulty calibration · read only",
		"CALLER", "LEVEL", "MEDIAN", "TASKS", "WOULD",
		"agent-net", "homelab-agent *", "normal", "hard", "simple",
		"lower", "keep", "not enough tasks",
		"network: normal 1 · 9 tasks · keep",
		"ratings: deepseek-v4.1-flash 3 · gpt-6.1-sol 14",
		"weak caller: follows the network and cannot lower a tier",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in:\n%s", want, view)
		}
	}
}

func TestQuotaCalibrationGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 40}, {120, 34}} {
		m := newModel(size[0], size[1])
		m.tab = 2
		m.snap.Calibration = calibrationSnapshot()
		view := m.View()
		for _, line := range lines(view) {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("wide line at %d columns: %q", size[0], line)
			}
		}
		golden(t, fmt.Sprintf("calibration_%dx%d", size[0], size[1]), view)
	}
}
