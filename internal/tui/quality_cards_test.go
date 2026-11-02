package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestQualityCardsRankAndPick(t *testing.T) {
	q := protocol.QualityAnalysis{Status: "ready", Levels: []protocol.QualityLevel{{Difficulty: "normal", Tasks: 25, Families: []protocol.QualityFamily{
		{Family: "slow", Tasks: 5, Headache: 9}, {Family: "fast", Tasks: 5, Headache: 1}, {Family: "mid", Tasks: 5, Headache: 3}, {Family: "fourth", Tasks: 5, Headache: 7}, {Family: "tiny", Tasks: 1},
	}}}}
	view := ansi.Strip(strings.Join(qualityCards(q, 120, 0, map[string]string{"normal": "mid"}, "test"), "\n"))
	for _, want := range []string{"▸ mid", "1 more: slow 9 min", "1 more with too few tasks to rank"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q: %s", want, view)
		}
	}
	if strings.Index(view, "fast") < 0 || strings.Index(view, "mid") < strings.Index(view, "fast") || strings.Index(view, "fourth") < strings.Index(view, "mid") || strings.Index(view, "slow") < strings.Index(view, "fourth") {
		t.Fatal(view)
	}
}

func TestQualityDetailsOpenClose(t *testing.T) {
	m := newModel(120, 34)
	m.tab = quotasTab
	m = keys(m, "d")
	if !m.qualityDetail {
		t.Fatal("d did not open details")
	}
	m = keys(m, "esc")
	if m.qualityDetail {
		t.Fatal("esc did not close details")
	}
}

func TestQualityNewScreenGoldens(t *testing.T) {
	score := 4.5
	q := protocol.QualityAnalysis{Status: "ready", Areas: []protocol.QualityFamily{{Family: "ui", Tasks: 18, Headache: 2}}, Levels: []protocol.QualityLevel{
		{Difficulty: "simple", Tasks: 9, Families: []protocol.QualityFamily{{Family: "luna", Tasks: 9, Headache: 1}}},
		{Difficulty: "normal", Tasks: 196, Families: []protocol.QualityFamily{{Family: "opus", Tasks: 6, Headache: 0}, {Family: "luna", Tasks: 24, Headache: 2}, {Family: "sol", Tasks: 19, Headache: 4, NeededHelp: .26, Continues: 3, AvgRating: &score}, {Family: "deepseek", Tasks: 87, Headache: 8}, {Family: "terra", Tasks: 60, Headache: 9, NeededHelp: .35, Continues: 12, LateDefects: 1, WorstSeverity: 3, AvgRating: &score}}},
		{Difficulty: "hard", Tasks: 42, Families: []protocol.QualityFamily{{Family: "sol", Tasks: 10, Headache: 5}, {Family: "terra", Tasks: 32, Headache: 8}}},
		{Difficulty: "very_complex", Tasks: 5, Families: []protocol.QualityFamily{{Family: "terra", Tasks: 5, Headache: 7}}},
	}}
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		m := newModel(size[0], size[1])
		m.tab = quotasTab
		m.snap.Calibration.Quality = q
		m.snap.Nodes = []protocol.NodeView{{NodeID: m.snap.NodeID, Hostname: "workstation", QualityPicks: map[string]string{"normal": "sol", "hard": "terra"}, QualityPickReasons: map[string]string{"normal": "codex 5% ahead of pace"}}}
		golden(t, fmt.Sprintf("quality_overview_%dx%d", size[0], size[1]), strings.Join(m.calibrationView(size[0]), "\n"))
		m.qualityDetail = true
		golden(t, fmt.Sprintf("quality_details_%dx%d", size[0], size[1]), m.View())
	}
}

func TestQualityDetailsShowsPickerPaceReason(t *testing.T) {
	m := newModel(120, 34)
	m.tab = quotasTab
	m.snap.Calibration.Quality = protocol.QualityAnalysis{Status: "ready", Levels: []protocol.QualityLevel{{Difficulty: "normal", Families: []protocol.QualityFamily{{Family: "luna", Tasks: 6}}}}}
	m.snap.Nodes = []protocol.NodeView{{NodeID: m.snap.NodeID, QualityPicks: map[string]string{"normal": "luna"}, QualityPickReasons: map[string]string{"normal": "codex 5% ahead of pace"}}}
	view := ansi.Strip(strings.Join(m.qualityDetailsView(120, 33), "\n"))
	if !strings.Contains(view, "▸ luna (codex 5% ahead of pace)") {
		t.Fatal(view)
	}
}
