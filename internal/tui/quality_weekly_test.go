package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func weeklyScoresFixture() []protocol.ScoresWeeklyRow {
	return []protocol.ScoresWeeklyRow{
		{Week: "2026-W39"},
		{Week: "2026-W40"},
		{Week: "2026-W41"},
		{Week: "2026-W42"},
		{Week: "2026-W43"},
		{Week: "2026-W44"},
		{Week: "2026-W45", Runs: 14, MeanScore: 0.82, EarlyQuitRate: 0.0, MeanFixRounds: 0.25, BriefDefects: 1, Defects: 0, MeanTokens: 12400,
			Causes: protocol.CauseCounts{Machine: 2, Work: 1, Brief: 1, Caught: 2, Escaped: 1}, Releases: []string{"v0.5.21"}},
		{Week: "2026-W46", Runs: 18, MeanScore: 0.88, EarlyQuitRate: 0.05, MeanFixRounds: 0.10, BriefDefects: 0, Defects: 1, MeanTokens: 15100,
			Causes: protocol.CauseCounts{Caught: 1}, Releases: []string{"v0.5.22", "v0.5.23"}},
	}
}

func TestQualityWeeklyGoldens(t *testing.T) {
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		m := newModel(size[0], size[1])
		m.tab = quotasTab
		m.qualityDetail = true
		m.showWeekly = true
		m.weeklyScores = weeklyScoresFixture()
		m.weeklyLoaded = true
		view := m.testView()
		for _, line := range lines(view) {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("%dx%d: line is %d columns: %q", size[0], size[1], ansi.StringWidth(line), ansi.Strip(line))
			}
			if strings.Contains(ansi.Strip(line), "…") {
				t.Fatalf("%dx%d: a label was cut: %q", size[0], size[1], ansi.Strip(line))
			}
		}
		golden(t, fmt.Sprintf("quality_weekly_%dx%d", size[0], size[1]), view)
	}
}

// TestQualityWeeklyCompactReleases covers a week with many release tags: it
// must read as the first and last tag plus the count, on one line.
func TestQualityWeeklyCompactReleases(t *testing.T) {
	tags := []string{"v0.4.165", "v0.4.166", "v0.4.207", "v0.4.208"}
	if got, want := releasesText(tags), "v0.4.165-v0.4.208 (4)"; got != want {
		t.Errorf("many tags = %q, want %q", got, want)
	}
	if got, want := releasesText(tags[:2]), "releases: v0.4.165, v0.4.166"; got != want {
		t.Errorf("two tags = %q, want %q", got, want)
	}
	if got := releasesText(nil); got != "" {
		t.Errorf("no tags = %q, want empty", got)
	}
}

// TestWeeklyCauseColumn covers the CAUSE column: it shows the rework causes
// and the caught/escaped split on a wide screen, and is left out when the
// release tags need the room.
func TestWeeklyCauseColumn(t *testing.T) {
	row := protocol.ScoresWeeklyRow{Week: "2026-W46", Causes: protocol.CauseCounts{Machine: 2, Work: 1, Brief: 1, Caught: 3, Escaped: 1}}
	if line := weeklyRowLine(row, 116); !strings.Contains(line, "m2 w1 b1 c3 e1") {
		t.Fatalf("wide row has no cause column: %q", line)
	}
	if line := weeklyRowLine(row, 76); strings.Contains(line, "m2 w1") {
		t.Fatalf("narrow row kept the cause column: %q", line)
	}
	if got := weeklyCauseText(protocol.CauseCounts{}); got != "-" {
		t.Fatalf("empty causes = %q, want -", got)
	}
}
