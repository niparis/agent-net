package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

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
				{Family: "luna", Tasks: 5, Fixes: 1, Continues: 0, FollowUps: .20, Headache: 2},
				{Family: "deepseek", Tasks: 1, Fixes: 0, Continues: 1, FollowUps: .33, Headache: 2},
				{Family: "sol", Tasks: 1, Fixes: 0, Continues: 0, FollowUps: 0, Headache: 0},
				{Family: "terra", Tasks: 1, Fixes: 1, Continues: 0, FollowUps: 1.00, Headache: 10},
			}},
			{Difficulty: "normal", Tasks: 154, Families: []protocol.QualityFamily{
				{Family: "deepseek", Tasks: 66, Fixes: 6, Continues: 0, FollowUps: .09, Headache: 60.0 / 66.0},
				{Family: "terra", Tasks: 53, Fixes: 28, Continues: 30, FollowUps: .72, Headache: 340.0 / 53.0},
				{Family: "luna", Tasks: 19, Fixes: 5, Continues: 0, FollowUps: .26, Headache: 50.0 / 19.0},
				{Family: "sol", Tasks: 9, Fixes: 2, Continues: 1, FollowUps: .26, Headache: 22.0 / 9.0},
				{Family: "opus", Tasks: 7, Fixes: 0, Continues: 0, FollowUps: 0, Headache: 0},
			}},
			{Difficulty: "hard", Tasks: 33, Families: []protocol.QualityFamily{
				{Family: "terra", Tasks: 25, Fixes: 12, Continues: 10, FollowUps: .61, LateDefects: 1, LateDefectSeverity: 4, Headache: 320.0 / 25.0},
				{Family: "sol", Tasks: 8, Fixes: 3, Continues: 1, FollowUps: .42, Headache: 4},
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

func TestQuotaCalibrationTable(t *testing.T) {
	m := newModel(80, 40)
	m.tab = 2
	m.snap.Calibration = calibrationSnapshot()
	view := ansi.Strip(strings.Join(m.calibrationView(80), "\n"))
	for _, want := range []string{
		"Difficulty calibration · read only",
		"CALLER", "LEVEL", "MEDIAN", "TASKS", "WOULD",
		"agent-net", "homelab-agent *", "normal", "hard", "simple",
		"lower", "keep", "not enough tasks",
		"network: normal 1 · 9 tasks · keep",
		"ratings: deepseek-v4.1-flash 3 (1 later) · gpt-6.1-sol 14",
		"defects reported later: 2",
		"weak caller: follows the network and cannot lower a tier",
		"Quality analysis · read only",
		"agent ◂ all agents ▸",
		"FAMILY", "FOLLOW-UPS", "LATE DEFECTS", "HEADACHE",
		"simple · 8 tasks", "normal · 154 tasks", "hard · 33 tasks", "unknown · 23 tasks",
		"terra", "deepseek", "opus", "1 · avg 4.0",
		"headache = orchestrator minutes: continue 2, fix 10, late defect",
		"ratings: 40 so far; what matters most: model / effort / harness",
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

func TestQualityGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 40}, {120, 34}} {
		for _, tc := range []struct {
			name  string
			agent int
		}{
			{"quality_all", 0},
			{"quality_agent", 1},
		} {
			m := newModel(size[0], size[1])
			m.snap.Calibration = protocol.Calibration{Quality: qualityLevelSnapshot()}
			m.qualityAgent = tc.agent
			view := ansi.Strip(strings.Join(m.calibrationView(size[0]), "\n"))
			golden(t, fmt.Sprintf("%s_%dx%d", tc.name, size[0], size[1]), view)
		}
	}
}

func qualityPanel(q protocol.QualityAnalysis, w, agent int, detail bool) string {
	return ansi.Strip(strings.Join(qualityView(q, w, agent, detail), "\n"))
}

// qualityColumn reads one fixed-width column from a table row. Column starts
// come from the header in runes, so an ellipsis or a wide glyph in an earlier
// cell does not move the later ones.
func qualityColumn(t *testing.T, line, header, name string, width int) string {
	t.Helper()
	at := strings.Index(header, name)
	if at < 0 {
		t.Fatalf("no %q in header %q", name, header)
	}
	at = utf8.RuneCountInString(header[:at])
	runes := []rune(line)
	if at+width > len(runes) {
		t.Fatalf("%q: row %q is too short for %q", name, line, name)
	}
	return strings.TrimSpace(string(runes[at : at+width]))
}

func qualityRows(view string) (header string, rows []string) {
	for _, line := range strings.Split(view, "\n") {
		switch {
		case strings.Contains(line, "FAMILY"):
			header = line
		case strings.Contains(line, " · ") && strings.Contains(line, " tasks"):
			// Level header.
		case strings.Contains(line, "agent ◂"), strings.Contains(line, "ratings:"):
		case strings.Contains(line, "headache ="), strings.Contains(line, "by severity"):
		case strings.HasPrefix(line, "╭"), strings.HasPrefix(line, "╰"):
		default:
			if strings.TrimSpace(strings.Trim(line, "│ ")) != "" {
				rows = append(rows, line)
			}
		}
	}
	return header, rows
}

func TestQualityFamilyColumnsAlign(t *testing.T) {
	type row struct {
		family, tasks, follow, late, headache string
	}
	want := []row{
		{"luna", "5", "0.20", "-", "2 min"},
		{"deepseek", "1", "0.33", "-", "2 min"},
		{"sol", "1", "0.00", "-", "0 min"},
		{"terra", "1", "1.00", "-", "10 min"},
		{"deepseek", "66", "0.09", "-", "1 min"},
		{"terra", "53", "0.72", "-", "6 min"},
		{"luna", "19", "0.26", "-", "3 min"},
		{"sol", "9", "0.26", "-", "2 min"},
		{"opus", "7", "0.00", "-", "0 min"},
		{"terra", "25", "0.61", "1 · avg 4.0", "13 min"},
		{"sol", "8", "0.42", "-", "4 min"},
		{"deepseek", "12", "0.17", "-", "2 min"},
		{"sol", "8", "0.13", "-", "1 min"},
		{"terra", "3", "1.00", "-", "10 min"},
	}
	cols := []struct {
		name string
		w    int
		get  func(row) string
	}{
		{"TASKS", 5, func(r row) string { return r.tasks }},
		{"FOLLOW-UPS", 10, func(r row) string { return r.follow }},
		{"LATE DEFECTS", 12, func(r row) string { return r.late }},
		{"HEADACHE", 8, func(r row) string { return r.headache }},
	}
	for _, w := range []int{80, 120} {
		view := qualityPanel(qualityLevelSnapshot(), w, 0, false)
		header, rows := qualityRows(view)
		if header == "" {
			t.Fatalf("%d columns: no header:\n%s", w, view)
		}
		if at := strings.Index(header, "TASKS"); at < 0 || at > 20 {
			t.Errorf("%d columns: numbers are not next to the family name: %q", w, header)
		}
		if w == 120 && !strings.Contains(view, "headache = orchestrator minutes: continue 2, fix 10, late defect 10/30/60/180 by severity") {
			t.Errorf("missing headache footnote at 120 columns:\n%s", view)
		}
		if len(rows) != len(want) {
			t.Fatalf("%d columns: %d rows, want %d:\n%s", w, len(rows), len(want), view)
		}
		for i, r := range want {
			line := rows[i]
			if !strings.Contains(line, r.family) {
				t.Errorf("%d columns row %d: %q is not %s", w, i, line, r.family)
			}
			for _, c := range cols {
				if got := qualityColumn(t, line, header, c.name, c.w); got != c.get(r) {
					t.Errorf("%d columns row %d: %s = %q, want %q in %q", w, i, c.name, got, c.get(r), line)
				}
			}
		}
		for _, l := range strings.Split(view, "\n") {
			if ansi.StringWidth(l) > w {
				t.Errorf("%d columns: wide line %q", w, l)
			}
		}
	}
}

func TestQualityDetailAddsFixesAndContinues(t *testing.T) {
	plain := qualityPanel(qualityLevelSnapshot(), 80, 0, false)
	if strings.Contains(plain, "FIXES") || strings.Contains(plain, "CONTINUES") {
		t.Fatalf("default view has the detail columns:\n%s", plain)
	}
	detail := qualityPanel(qualityLevelSnapshot(), 80, 0, true)
	header, rows := qualityRows(detail)
	if !strings.Contains(header, "FIXES") || !strings.Contains(header, "CONTINUES") {
		t.Fatalf("detail header: %q", header)
	}
	if got := qualityColumn(t, rows[5], header, "FIXES", 5); got != "28" {
		t.Fatalf("detail fixes = %q, want 28 in %q", got, rows[5])
	}
	if got := qualityColumn(t, rows[5], header, "CONTINUES", 9); got != "30" {
		t.Fatalf("detail continues = %q, want 30 in %q", got, rows[5])
	}
}

func TestQualitySelectorCyclesAndFilters(t *testing.T) {
	m := newModel(80, 40)
	m.tab = quotasTab
	m.snap.Calibration.Quality = qualityLevelSnapshot()
	view := func() string { return qualityPanel(m.snap.Calibration.Quality, 80, m.qualityAgent, m.qualityDetail) }
	if !strings.Contains(view(), "agent ◂ all agents ▸") || !strings.Contains(view(), "normal · 154 tasks") ||
		!strings.Contains(view(), "unknown · 23 tasks") {
		t.Fatalf("all agents: %s", view())
	}
	m = keys(m, "right")
	if !strings.Contains(view(), "agent ◂ agent-net ▸") || !strings.Contains(view(), "normal · 20 tasks") ||
		!strings.Contains(view(), "hard · 8 tasks") || strings.Contains(view(), "unknown ·") {
		t.Fatalf("first caller: %s", view())
	}
	m = keys(m, "right")
	if !strings.Contains(view(), "agent ◂ homelab-agent ▸") || !strings.Contains(view(), "simple · 5 tasks") ||
		!strings.Contains(view(), "unknown · 12 tasks") || strings.Contains(view(), "hard ·") {
		t.Fatalf("second caller: %s", view())
	}
	m = keys(m, "right")
	if !strings.Contains(view(), "agent ◂ all agents ▸") {
		t.Fatalf("selector does not wrap forward: %s", view())
	}
	m = keys(m, "left")
	if !strings.Contains(view(), "agent ◂ homelab-agent ▸") {
		t.Fatalf("selector does not wrap back: %s", view())
	}
	// The CTO box has focus: left and right must not move the selector.
	m.focus = focusCTO
	m = keys(m, "right")
	if m.qualityAgent != 2 {
		t.Fatalf("CTO focus moved the quality selector to %d", m.qualityAgent)
	}
}

func TestQualityFamilyDimRule(t *testing.T) {
	if !qualityFamilyDim("normal", protocol.CalibrationMinTasks-1) {
		t.Fatal("a family below the minimum should be dim")
	}
	if qualityFamilyDim("normal", protocol.CalibrationMinTasks) {
		t.Fatal("a family at the minimum should not be dim")
	}
	if !qualityFamilyDim("unknown", protocol.CalibrationMinTasks+10) {
		t.Fatal("the unknown level should be dim")
	}
}

func TestQualityRatingsLine(t *testing.T) {
	q := qualityLevelSnapshot()
	q.Ratings = 3
	view := qualityPanel(q, 80, 0, false)
	if !strings.Contains(view, "ratings: 3 so far; the analysis starts at 20") {
		t.Errorf("missing real counts:\n%s", view)
	}
	if strings.Contains(view, "what matters most") {
		t.Errorf("analysis shown before enough ratings:\n%s", view)
	}
	q.Ratings = 40
	view = qualityPanel(q, 80, 0, false)
	if !strings.Contains(view, "ratings: 40 so far; what matters most: model / effort / harness") {
		t.Errorf("missing effects state:\n%s", view)
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
