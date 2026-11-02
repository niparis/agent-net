package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

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
		{Difficulty: "simple", Tasks: 9, Families: []protocol.QualityFamily{{Family: "luna", Tasks: 9, TasksWeek: 3, Headache: 1}}},
		{Difficulty: "normal", Tasks: 196, Families: []protocol.QualityFamily{{Family: "opus", Tasks: 6, TasksWeek: 1, Headache: 0}, {Family: "luna", Tasks: 24, TasksWeek: 6, Headache: 2}, {Family: "sol", Tasks: 19, TasksWeek: 4, Headache: 4, NeededHelp: .26, Continues: 3, AvgRating: &score}, {Family: "deepseek", Tasks: 87, TasksWeek: 25, Headache: 8}, {Family: "terra", Tasks: 60, TasksWeek: 18, Headache: 9, NeededHelp: .35, Continues: 12, LateDefects: 1, WorstSeverity: 3, AvgRating: &score}}},
		{Difficulty: "hard", Tasks: 42, Families: []protocol.QualityFamily{{Family: "sol", Tasks: 10, TasksWeek: 3, Headache: 5}, {Family: "terra", Tasks: 32, TasksWeek: 9, Headache: 8}},
			Models: []protocol.QualityFamily{{Family: "sol medium", Tasks: 10, TasksWeek: 3, Headache: 5}, {Family: "terra high", Tasks: 26, TasksWeek: 7, Headache: 9, LateDefects: 2, WorstSeverity: 3}, {Family: "terra max", Tasks: 6, TasksWeek: 2, Headache: 4}}},
		{Difficulty: "very_complex", Tasks: 5, Families: []protocol.QualityFamily{{Family: "terra", Tasks: 5, TasksWeek: 2, Headache: 7}}},
	}}
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		m := newModel(size[0], size[1])
		m.tab = quotasTab
		m.snap.Calibration.Quality = q
		m.snap.Nodes = []protocol.NodeView{{NodeID: m.snap.NodeID, Hostname: "workstation", QualityPicks: map[string]string{"normal": "sol", "hard": "terra"}, QualityPickModels: map[string]string{"normal": "sol", "hard": "terra high"}, QualityPickReasons: map[string]string{"normal": "codex 5% ahead of pace"}}}
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

func TestQualityCardsColumnsAlign(t *testing.T) {
	q := protocol.QualityAnalysis{Status: "ready", Levels: []protocol.QualityLevel{{Difficulty: "normal", Families: []protocol.QualityFamily{{Family: "luna", Tasks: 9, TasksWeek: 3, Headache: 1}, {Family: "deepseek", Tasks: 87, TasksWeek: 25, Headache: 12}}}}}
	view := ansi.Strip(strings.Join(qualityCards(q, 120, 0, nil, ""), "\n"))
	var rows []string
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "luna") || strings.Contains(line, "deepseek") {
			rows = append(rows, line)
		}
	}
	if len(rows) != 2 {
		t.Fatal(view)
	}
	for _, token := range []string{"min", "this week"} {
		if strings.Index(rows[0], token) != strings.Index(rows[1], token) {
			t.Errorf("%s columns differ: %q / %q", token, rows[0], rows[1])
		}
	}
	if strings.Index(rows[0], "█") != strings.Index(rows[1], "█") {
		t.Errorf("bar columns differ: %q / %q", rows[0], rows[1])
	}
}

func TestQualityDetailsAlignedSections(t *testing.T) {
	m := newModel(120, 34)
	m.snap.Calibration.Quality = protocol.QualityAnalysis{Status: "ready", Levels: []protocol.QualityLevel{{Difficulty: "simple", Tasks: 9, Families: []protocol.QualityFamily{{Family: "luna", Tasks: 9, Headache: 1}}}}, Areas: []protocol.QualityFamily{{Family: "ui", Tasks: 18}}}
	view := ansi.Strip(strings.Join(m.qualityDetailsView(120, 60), "\n"))
	for _, want := range []string{"MODEL", "  MY TIME / TASK  THIS WEEK  TASKS  NEEDED HELP     LATE BUGS  RATING", "╭─simple · 9 tasks", "╭─normal · ", "╭─hard · ", "╭─very complex · "} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in %s", want, view)
		}
	}
	// The retired touches field no longer has a section.
	if strings.Contains(view, "touched area") {
		t.Errorf("the touched area section is still shown:\n%s", view)
	}
	var header, row string
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "MODEL") && header == "" {
			header = line
		}
		if strings.Contains(line, " luna ") {
			row = line
		}
	}
	for _, pair := range [][2]string{{"TASKS", "9"}, {"NEEDED HELP", "0%"}} {
		h := strings.Index(header, pair[0])
		r := strings.Index(row, pair[1])
		if h < 0 || r < 0 || utf8.RuneCountInString(header[:h])+len(pair[0]) != utf8.RuneCountInString(row[:r])+len(pair[1]) {
			t.Errorf("%s not aligned: %q / %q", pair[0], header, row)
		}
	}
}

func TestQualityCardsNarrowKeepsTaskCounts(t *testing.T) {
	q := protocol.QualityAnalysis{Status: "ready", Levels: []protocol.QualityLevel{{Difficulty: "normal", Families: []protocol.QualityFamily{{Family: "opus", Tasks: 6, TasksWeek: 1, Headache: 0}, {Family: "luna", Tasks: 24, TasksWeek: 6, Headache: 2}, {Family: "sol", Tasks: 19, TasksWeek: 4, Headache: 4}, {Family: "deepseek", Tasks: 87, TasksWeek: 25, Headache: 8}}}}}
	view := ansi.Strip(strings.Join(qualityCards(q, 80, 0, nil, ""), "\n"))
	if !strings.Contains(view, "· 25 this week") || !strings.Contains(view, "·  6 this week") || strings.Contains(view, "…") {
		t.Fatal(view)
	}
}

// The model the picker would choose keeps its marker when it is not among the
// three most used: it is named first on the "more" line.
func TestQualityCardsMarkThePickInTheMoreLine(t *testing.T) {
	q := protocol.QualityAnalysis{Status: "ready", Levels: []protocol.QualityLevel{{Difficulty: "normal", Families: []protocol.QualityFamily{{Family: "opus", Tasks: 6, TasksWeek: 1, Headache: 0}, {Family: "luna", Tasks: 24, TasksWeek: 6, Headache: 2}, {Family: "sol", Tasks: 19, TasksWeek: 4, Headache: 4}, {Family: "deepseek", Tasks: 87, TasksWeek: 25, Headache: 8}, {Family: "terra", Tasks: 60, TasksWeek: 18, Headache: 9}}}}}
	view := ansi.Strip(strings.Join(qualityCards(q, 120, 0, map[string]string{"normal": "opus"}, "workstation"), "\n"))
	if !strings.Contains(view, "2 more: ▸ opus 0 min · sol 4 min") {
		t.Fatal(view)
	}
}

// The bars compare the rows on the cards. A model with too few tasks to rank
// does not set the scale, and a row that reads "0 min" has an empty bar.
func TestQualityCardBarsScaleToTheShownRows(t *testing.T) {
	q := protocol.QualityAnalysis{Status: "ready", Levels: []protocol.QualityLevel{
		{Difficulty: "normal", Families: []protocol.QualityFamily{{Family: "deepseek", Tasks: 90, TasksWeek: 20, Headache: 6}, {Family: "luna", Tasks: 24, TasksWeek: 6, Headache: 2}, {Family: "opus", Tasks: 1, Headache: 60}}},
		{Difficulty: "very_complex", Families: []protocol.QualityFamily{{Family: "terra", Tasks: 5, TasksWeek: 5, Headache: 0.4}}},
	}}
	view := ansi.Strip(strings.Join(qualityCards(q, 120, 0, nil, ""), "\n"))
	bar := func(family string) string {
		for _, line := range strings.Split(view, "\n") {
			if i := strings.Index(line, " "+family+" "); i >= 0 {
				rest := line[i:]
				if j := strings.IndexAny(rest, "█░"); j >= 0 {
					return strings.TrimRight(rest[j:j+len("█████")*3], " ")
				}
			}
		}
		t.Fatalf("no bar for %s in\n%s", family, view)
		return ""
	}
	if got := strings.Count(bar("deepseek"), "█"); got != 5 {
		t.Errorf("deepseek, the slowest shown row, fills %d of 5 cells", got)
	}
	if got := strings.Count(bar("luna"), "█"); got != 2 {
		t.Errorf("luna at 2 of 6 minutes fills %d of 5 cells, want 2", got)
	}
	if got := strings.Count(bar("terra"), "█"); got != 0 {
		t.Errorf("terra at 0 min fills %d cells, want none", got)
	}
}
