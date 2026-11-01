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
		Quality: protocol.QualityAnalysis{Status: "tentative: overlap and more ratings needed", Ratings: 14, Average: 3.5, Effects: []protocol.QualityEffect{{Kind: "model", Name: "gpt-6.1-sol", Quality: .6, Rework: -.3, Ratings: 14, Runs: 20}, {Kind: "harness", Name: "codex", Quality: .1, Ratings: 14, Runs: 20}, {Kind: "effort", Name: "gpt-6.1-sol high", Quality: .1, Ratings: 3, Runs: 5}}, Profiles: []protocol.ProfileQuality{{Name: "sol high", Quality: 4.2, Ratings: 3, Runs: 5}}},
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
		"Model matters", "Harness matters", "Effort matters", "Predicted quality", "sol high", "4.20", "CALLER", "LEVEL", "MEDIAN", "TASKS", "WOULD",
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
		m.quotaScroll = 14
		golden(t, fmt.Sprintf("quality_%dx%d", size[0], size[1]), m.View())
	}
}

func TestQuotaQualityEmpty(t *testing.T) {
	m := newModel(80, 40)
	m.snap.Calibration.Quality.Status = "not enough ratings"
	view := ansi.Strip(strings.Join(m.calibrationView(80), "\n"))
	if !strings.Contains(view, "not enough ratings") {
		t.Fatalf("empty: %s", view)
	}
}

func TestQuotaQualityWeightsMatchChooserSetting(t *testing.T) {
	m := newModel(80, 40)
	m.snap.Calibration.Quality = protocol.QualityAnalysis{WeightingEnabled: true, Profiles: []protocol.ProfileQuality{{Name: "a", Weights: map[string]float64{"normal": 1.5}}}}
	candidates := []protocol.WorkerModelCandidate{{Name: "a", Harness: "codex", Model: "gpt-6.1-sol"}}
	normal, _ := m.workerCandidateTiers("normal", candidates)
	if normal[0].QualityWeight != 1.5 {
		t.Fatalf("preview weight: %+v", normal)
	}
	m.snap.Calibration.Quality.WeightingEnabled = false
	normal, _ = m.workerCandidateTiers("normal", candidates)
	if normal[0].QualityWeight != 0 {
		t.Fatalf("disabled preview: %+v", normal)
	}
}
