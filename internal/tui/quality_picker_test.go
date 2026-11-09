package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// pickerFixture is one picker table with a record, a thin record and a model
// with no tasks yet. It is built by the protocol so the cells are the real
// cells the picker reads.
func pickerFixture() protocol.PickerAnalysis {
	deepseek := "opencode-go/deepseek-v4.1-flash:high"
	terra := "openai-codex/gpt-6.1-terra:max"
	sol := "openai-codex/gpt-6.1-sol:low"
	opus := "anthropic/claude-opus-5-5"
	var tasks []protocol.PickerTask
	for i := 0; i < 12; i++ {
		d := 1 + (float64(i)+.5)/3
		for j := 0; j < 5; j++ {
			tasks = append(tasks,
				protocol.PickerTask{Key: deepseek, D: d, Score: .80 - .03*float64(i)},
				protocol.PickerTask{Key: terra, D: d, Score: .72 - .045*float64(i)},
			)
		}
		// Sol has a thin record in the easy buckets and none later.
		if i < 6 {
			for j := 0; j < 2; j++ {
				tasks = append(tasks, protocol.PickerTask{Key: sol, D: d, Score: .70 - .05*float64(i)})
			}
		}
	}
	table := protocol.BuildPickerTable(tasks, protocol.DefaultPickerSettings())
	a := table.Analysis(opus)
	a.Profiles = []protocol.PickerProfileRow{
		{Name: "deepseek high", Key: deepseek, Model: "deepseek-v4.1-flash", Score: 38, Level: "normal", Ceiling: 3, Scored: true},
		{Name: "terra max", Key: terra, Model: "gpt-6.1-terra", Score: 55, Level: "very_complex", Ceiling: 5, Scored: true},
		{Name: "sol low", Key: sol, Model: "gpt-6.1-sol", Score: 32, Level: "simple", Ceiling: 2.25, Scored: true},
		{Name: "opus", Key: opus, Model: "claude-opus-5-5", Score: 60, Level: "very_complex", Ceiling: 5, Scored: true},
	}
	return a
}

// The subtab shows the verdict protocol gives for the selected cell, byte for
// byte, because it runs the same function the picker runs.
func TestQualityPickerVerdictsMatchProtocol(t *testing.T) {
	m := newModel(120, 34)
	m.tab = quotasTab
	m.qualityDetail = true
	m.picker, m.pickerLoaded = pickerFixture(), true

	facts, verdicts, best := m.pickerVerdicts(0)
	b := m.picker.Buckets[0]
	want, wantBest := protocol.PickerBucketVerdicts(facts, b.Lo, b.Mean, b.Index, m.picker.Settings)
	if !reflect.DeepEqual(verdicts, want) || best != wantBest {
		t.Fatalf("the screen's verdicts differ from protocol's:\n got %+v best %d\nwant %+v best %d", verdicts, best, want, wantBest)
	}
	rows, _, _ := m.pickerRowSet()
	if best < 0 || rows[best].key != "opencode-go/deepseek-v4.1-flash:high" {
		t.Fatalf("best = %d, want the deepseek cell with a record", best)
	}

	// Each row's detail line names the verdict the rule gives it.
	wantReason := map[string]string{
		"opencode-go/deepseek-v4.1-flash:high": "verdict: allowed",
		"openai-codex/gpt-6.1-sol:low":         "verdict: chosen by benchmark (n<3)",
		"anthropic/claude-opus-5-5":            "verdict: left out (overqualified)",
	}
	for i, row := range rows {
		m.pickerSelModel = i
		body := ansi.Strip(strings.Join(m.qualityPickerBody(120, 30), "\n"))
		if reason, ok := wantReason[row.key]; ok && !strings.Contains(body, reason) {
			t.Errorf("%s: missing %q in\n%s", row.key, reason, body)
		}
	}

	// A bucket where sol's thin record is above its ceiling says so.
	for i, row := range rows {
		if row.key == "openai-codex/gpt-6.1-sol:low" {
			m.pickerSelModel = i
		}
	}
	m.pickerSelBucket = 8
	body := ansi.Strip(strings.Join(m.qualityPickerBody(120, 30), "\n"))
	if !strings.Contains(body, "verdict: left out (above its benchmark)") {
		t.Fatalf("sol above its benchmark is not named:\n%s", body)
	}
}

// underlinedRunes counts the characters drawn with the underline attribute.
func underlinedRunes(line string) int {
	n := 0
	for _, m := range regexp.MustCompile("\x1b\\[([0-9;]*)m").FindAllStringSubmatch(line, -1) {
		for _, tok := range strings.Split(m[1], ";") {
			if tok == "4" {
				n++
				break
			}
		}
	}
	return n
}

// Cells past a profile's benchmark ceiling are underlined, and nothing else
// is: the old | mark between cells is gone, so every cell has the same width.
func TestQualityPickerCeilingUnderlinesCellsPastIt(t *testing.T) {
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		m := newModel(size[0], size[1])
		m.picker, m.pickerLoaded = pickerFixture(), true
		m.defaultPickerSelection()
		inner := max(size[0]-4, 20)
		fit := max(1, (inner-pickerLeftWidth(pickerModelWidth))/pickerCellWidth)
		start, end := pickerColumnWindow(m.pickerColumns(), m.pickerSelBucket, fit)
		cols := m.pickerColumns()[start:end]
		rows, _, _ := m.pickerRowSet()
		for _, row := range rows {
			line := m.pickerModelLine(row, false, pickerModelWidth, cols, inner)
			if strings.Contains(ansi.Strip(line), "│") {
				t.Errorf("%dx%d: %s still has a ceiling mark: %q", size[0], size[1], row.name, ansi.Strip(line))
			}
			if w := ansi.StringWidth(line); w != pickerLeftWidth(pickerModelWidth)+len(cols)*pickerCellWidth {
				t.Errorf("%dx%d: %s is %d columns wide, want %d", size[0], size[1], row.name, w, pickerLeftWidth(pickerModelWidth)+len(cols)*pickerCellWidth)
			}
			past := 0
			for _, col := range cols {
				if m.picker.Buckets[col].Lo >= row.ceiling {
					past++
				}
			}
			if got := underlinedRunes(line) / 4; got != past {
				t.Errorf("%dx%d: %s has %d underlined cells, want %d (ceiling %.2f): %q", size[0], size[1], row.name, got, past, row.ceiling, line)
			}
		}
	}
}

// The grid needs no horizontal scroll state at 80 columns: it shows the
// buckets around the selection and says how many are outside the window.
func TestQualityPickerKeepsTheSelectedBucketVisibleAt80(t *testing.T) {
	m := newModel(80, 24)
	m.tab = quotasTab
	m.qualityDetail = true
	m.picker, m.pickerLoaded = pickerFixture(), true
	m.pickerSelModel = 1
	m.pickerSelBucket = len(m.picker.Buckets) - 1
	view := ansi.Strip(strings.Join(m.qualityDetailsView(80, 23), "\n"))
	if !strings.Contains(view, "◂ ") || !strings.Contains(view, "more · left/right moves buckets") {
		t.Fatalf("the hidden buckets are not named:\n%s", view)
	}
	last := m.picker.Buckets[len(m.picker.Buckets)-1]
	if !strings.Contains(view, fmt.Sprintf("%.2f", last.Lo)) {
		t.Fatalf("the selected bucket %.2f is not visible:\n%s", last.Lo, view)
	}
}

func TestQualityPickerCellShading(t *testing.T) {
	none := pickerCellStyle(protocol.PickerCell{Score: .5, Lo: .5, Hi: .5}, .5, false, false, false)
	if !none.GetFaint() {
		t.Error("a cell with no task is not dim")
	}
	thin := pickerCellStyle(protocol.PickerCell{Score: .9, Lo: .8, Hi: .95, N: 2}, .5, false, false, false)
	if !thin.GetFaint() || thin.GetForeground() == roleGood {
		t.Error("a cell with fewer than three tasks must be dim and not green")
	}
	plain := pickerCellStyle(protocol.PickerCell{Score: .5, Lo: .2, Hi: .8, N: 3}, .5, false, false, false)
	if plain.GetFaint() || plain.GetBold() {
		t.Error("a cell with three tasks is not plain weight, even with a wide interval")
	}
	above := pickerCellStyle(protocol.PickerCell{Score: .8, Lo: .7, Hi: .9, N: 10}, .5, false, false, false)
	if above.GetForeground() != roleGood {
		t.Error("a cell whose interval is above the mean is not green")
	}
	below := pickerCellStyle(protocol.PickerCell{Score: .2, Lo: .1, Hi: .3, N: 10}, .5, false, false, false)
	if below.GetForeground() != roleError {
		t.Error("a cell whose interval is below the mean is not red")
	}
	past := pickerCellStyle(protocol.PickerCell{Score: .5, Lo: .45, Hi: .55, N: 10}, .5, false, true, false)
	if !past.GetUnderline() {
		t.Error("a cell past the benchmark ceiling is not underlined")
	}
	selected := pickerCellStyle(protocol.PickerCell{Score: .5, Lo: .2, Hi: .8, N: 10}, .5, true, false, false)
	if selected.GetBackground() != roleAccent || selected.GetFaint() {
		t.Error("the selected cell does not have the selection colours")
	}
	older := pickerCellStyle(protocol.PickerCell{Score: .8, Lo: .7, Hi: .9, N: 10}, .5, false, false, true)
	if !older.GetFaint() || older.GetForeground() == roleGood {
		t.Error("a cell of an older model key must be dim and not coloured")
	}
}

// ownerShapedPicker is the table the owner saw on 2026-11-08: profiles whose
// benchmark order is not alphabetical, one model under two keys, two stale
// keys, and mostly empty cells.
func ownerShapedPicker() protocol.PickerAnalysis {
	var tasks []protocol.PickerTask
	add := func(key string, bucket, n int, score float64) {
		for i := 0; i < n; i++ {
			tasks = append(tasks, protocol.PickerTask{Key: key, D: 1 + (float64(bucket)+.5)/3, Score: score - .02*float64(i)})
		}
	}
	for b := 0; b < 4; b++ {
		add("claude-sonnet-5-5", b, 6, .85-.04*float64(b))
		add("opencode-go/deepseek-v4.1-flash:high", b, 4, .8-.03*float64(b))
	}
	add("openai-codex/gpt-6.1-sol:medium", 1, 4, .8)
	add("gpt-6.1-sol:medium", 2, 5, .7) // the same model under a bare key
	add("claude-opus-5-5-high", 0, 3, .9)
	add("claude-sonnet-5-5-medium", 3, 3, .6)
	add("zeta-old", 4, 1, .5)
	table := protocol.BuildPickerTable(tasks, protocol.DefaultPickerSettings())
	a := table.Analysis("claude-sonnet-5-5", "opencode-go/deepseek-v4.1-flash:high", "openai-codex/gpt-6.1-sol:medium", "anthropic/claude-opus-5-5:high")
	a.Profiles = []protocol.PickerProfileRow{
		{Name: "sonnet claude", Key: "claude-sonnet-5-5", Score: 44, Level: "hard", Ceiling: 4, Scored: true},
		{Name: "deepseek opencode", Key: "opencode-go/deepseek-v4.1-flash:high", Score: 52, Level: "very_complex", Ceiling: 5, Scored: true},
		{Name: "sol codex", Key: "openai-codex/gpt-6.1-sol:medium", Score: 33, Level: "simple", Ceiling: 2.25, Scored: true},
		{Name: "opus claude", Key: "anthropic/claude-opus-5-5:high", Score: 57, Level: "very_complex", Ceiling: 5, Scored: true},
		{Name: "aardvark unscored", Key: "aardvark", Scored: false},
	}
	return a
}

func TestQualityPickerRowsAreProfilesInBenchmarkOrder(t *testing.T) {
	m := newModel(120, 34)
	m.picker, m.pickerLoaded = ownerShapedPicker(), true
	rows, profiles, hidden := m.pickerRowSet()
	var names []string
	for _, r := range rows {
		names = append(names, r.name)
	}
	want := []string{"opus claude", "deepseek opencode", "sonnet claude", "sol codex", "aardvark unscored"}
	if !reflect.DeepEqual(names, want) || profiles != 5 {
		t.Fatalf("rows = %v (%d profiles), want %v: highest score first, unscored last", names, profiles, want)
	}
	if hidden != 3 {
		t.Fatalf("hidden older keys = %d, want 3 (opus-5-5-high, sonnet-5-5-medium, zeta-old)", hidden)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[pickerModelNorm(r.key)] {
			t.Fatalf("model %s appears twice", r.key)
		}
		seen[pickerModelNorm(r.key)] = true
	}
	// The two sol keys are one row, and it keeps the cell with more tasks.
	var sol pickerRow
	for _, r := range rows {
		if r.name == "sol codex" {
			sol = r
		}
	}
	if sol.cellAt(1).N != 4 || sol.cellAt(2).N != 5 {
		t.Fatalf("the merged sol row lost a cell: bucket 1 n%d, bucket 2 n%d", sol.cellAt(1).N, sol.cellAt(2).N)
	}
	// With the toggle the older keys follow, in the same list.
	m.pickerShowOlder = true
	rows, _, _ = m.pickerRowSet()
	if len(rows) != 8 || !rows[5].older || rows[4].older {
		t.Fatalf("older rows are not after the profiles: %d rows", len(rows))
	}
	// Showing them changes no verdict of a profile.
	_, shown, _ := m.pickerVerdicts(1)
	m.pickerShowOlder = false
	_, hiddenV, _ := m.pickerVerdicts(1)
	if !reflect.DeepEqual(shown, hiddenV) {
		t.Fatalf("the older toggle changed the verdicts:\n%+v\n%+v", shown, hiddenV)
	}
}

func TestQualityPickerEmptyCellsShowADotNotANumber(t *testing.T) {
	m := newModel(120, 34)
	m.tab = quotasTab
	m.qualityDetail = true
	m.picker, m.pickerLoaded = ownerShapedPicker(), true
	m.defaultPickerSelection()
	body := ansi.Strip(strings.Join(m.qualityPickerBody(120, 30), "\n"))
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimLeft(line, "│ ▸"), "gpt-6.1-sol:medium") {
			continue
		}
		// sol has tasks in two of the shown buckets and a dot everywhere else.
		if got := strings.Count(line, "·"); got < 3 {
			t.Errorf("the sol row shows too few dots: %q", line)
		}
		if nums := strings.Count(line, "0."); nums != 2 {
			t.Errorf("the sol row shows %d decimals, want 2 scores: %q", nums, line)
		}
	}
	if !strings.Contains(body, "3 older model keys hidden · o to show") {
		t.Errorf("the hidden-keys line is missing:\n%s", body)
	}
	if strings.Contains(body, "zeta-old") || strings.Contains(body, "claude-opus-5-5-high") {
		t.Errorf("an older key shows without the toggle:\n%s", body)
	}
	m = keys(m, "o")
	body = ansi.Strip(strings.Join(m.qualityPickerBody(120, 30), "\n"))
	if !strings.Contains(body, "zeta-old") || !strings.Contains(body, "3 older model keys shown, dim · o to hide") {
		t.Errorf("o did not show the older keys:\n%s", body)
	}
	m = keys(m, "o")
	if m.pickerShowOlder {
		t.Error("a second o did not hide them")
	}
}

func ownerShapedProfileList() []protocol.WorkerProfile {
	return []protocol.WorkerProfile{
		{Name: "sonnet claude", Harness: "claude", Model: "claude-sonnet-5-5"},
		{Name: "deepseek opencode", Harness: "opencode", Model: "opencode-go/deepseek-v4.1-flash", Effort: "high"},
		{Name: "sol codex", Harness: "codex", Model: "openai-codex/gpt-6.1-sol", Effort: "medium"},
		{Name: "opus claude", Harness: "claude", Model: "anthropic/claude-opus-5-5", Effort: "high"},
		{Name: "aardvark unscored", Harness: "codex", Model: "aardvark"},
	}
}

func TestQualityPickerOwnerShapedGoldens(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {120, 34}, {80, 24}} {
		m := newModel(size[0], size[1])
		m.tab = quotasTab
		m.qualityDetail = true
		m.snap.WorkerProfiles = ownerShapedProfileList()
		m.picker, m.pickerLoaded = ownerShapedPicker(), true
		m.defaultPickerSelection()
		golden(t, fmt.Sprintf("quality_picker_owner_%dx%d", size[0], size[1]), m.testView())
		m.pickerShowOlder = true
		golden(t, fmt.Sprintf("quality_picker_owner_older_%dx%d", size[0], size[1]), m.testView())
	}
}

// pickerFetchSource adds the owner-only picker_get call to the counting fake.
type pickerFetchSource struct {
	*fetchCountSource
	picker      protocol.PickerAnalysis
	pickerCalls int
}

func (s *pickerFetchSource) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if method == protocol.OpPickerGet {
		s.pickerCalls++
		return json.Marshal(s.picker)
	}
	return s.fetchCountSource.Call(ctx, method, params)
}

func TestQualityPickerReadsOnOpenAndRefreshOnly(t *testing.T) {
	src := &pickerFetchSource{
		fetchCountSource: &fetchCountSource{fakeSource: &fakeSource{snap: fakeSnapshot(), usage: fakeUsage(), counts: fakeCounts()}},
		picker:           pickerFixture(),
	}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m = refresh(m, src)
	m.tab = quotasTab
	m = keys(m, "tab", "tab")
	if !m.qualityDetail || !m.pickerWanted {
		t.Fatal("opening Quality did not ask for the picker table")
	}
	m = refresh(m, src)
	if src.pickerCalls != 1 || !m.pickerLoaded {
		t.Fatalf("picker_get calls after open = %d, loaded %v; want 1, true", src.pickerCalls, m.pickerLoaded)
	}
	// A tick does not poll it again.
	for i := 0; i < 5; i++ {
		m = refresh(m, src)
	}
	if src.pickerCalls != 1 {
		t.Fatalf("picker_get polled on ticks: %d calls", src.pickerCalls)
	}
	// R asks for it again.
	m = keys(m, "R")
	m = send(m, m.fetchCmd()())
	if src.pickerCalls != 2 {
		t.Fatalf("R did not read the picker table: %d calls", src.pickerCalls)
	}
}

func TestQualityPickerGoldens(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {120, 34}, {80, 24}} {
		m := newModel(size[0], size[1])
		m.tab = quotasTab
		m.qualityDetail = true
		m.picker, m.pickerLoaded = pickerFixture(), true
		m.defaultPickerSelection()
		view := m.testView()
		for _, line := range lines(view) {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("%dx%d: line is %d columns: %q", size[0], size[1], ansi.StringWidth(line), ansi.Strip(line))
			}
			if strings.Contains(ansi.Strip(line), "…") {
				t.Fatalf("%dx%d: a label was cut: %q", size[0], size[1], ansi.Strip(line))
			}
		}
		golden(t, fmt.Sprintf("quality_picker_%dx%d", size[0], size[1]), view)
	}
}

// rowContaining returns the first stripped line that holds every needle.
func rowContaining(body string, needles ...string) string {
	for _, line := range strings.Split(body, "\n") {
		ok := true
		for _, needle := range needles {
			if !strings.Contains(line, needle) {
				ok = false
				break
			}
		}
		if ok {
			return line
		}
	}
	return ""
}

// A row is named by the model key the worker log shows, not by the profile's
// free text, and the harness sits in its own column.
func TestQualityRowsShowModelKeyAndHarness(t *testing.T) {
	m := newModel(120, 34)
	m.tab = quotasTab
	m.qualityDetail = true
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "sol codex", Harness: "codex", Model: "gpt-6.1-sol", Effort: "medium"}}
	a := pickerFixture()
	a.Profiles = []protocol.PickerProfileRow{{Name: "sol codex", Key: "openai-codex/gpt-6.1-sol:medium", Model: "gpt-6.1-sol", Score: 33, Level: "simple", Ceiling: 2.25, Scored: true}}
	m.picker, m.pickerLoaded = a, true
	m.defaultPickerSelection()

	body := ansi.Strip(strings.Join(m.qualityPickerBody(120, 30), "\n"))
	row := rowContaining(body, "gpt-6.1-sol:medium", "codex")
	if row == "" {
		t.Fatalf("no row names gpt-6.1-sol:medium next to codex:\n%s", body)
	}
	if strings.Contains(body, "sol codex") {
		t.Fatalf("a profile name is still in the grid:\n%s", body)
	}
	if !strings.Contains(body, "gpt-6.1-sol:medium · codex") {
		t.Fatalf("the detail pane does not name the key and harness:\n%s", body)
	}
}

// One model under two harnesses is two rows, because the harness tells them
// apart, even when the model key is the same.
func TestQualityShowsSameModelUnderTwoHarnesses(t *testing.T) {
	m := newModel(120, 34)
	m.tab = quotasTab
	m.qualityDetail = true
	m.snap.WorkerProfiles = []protocol.WorkerProfile{
		{Name: "sonnet claude", Harness: "claude", Model: "claude-sonnet-5-5"},
		{Name: "sonnet agy", Harness: "agy", Model: "claude-sonnet-5-5"},
	}
	a := ownerShapedPicker()
	a.Profiles = []protocol.PickerProfileRow{
		{Name: "sonnet claude", Key: "claude-sonnet-5-5", Score: 44, Level: "hard", Ceiling: 4, Scored: true},
		{Name: "sonnet agy", Key: "claude-sonnet-5-5", Score: 44, Level: "hard", Ceiling: 4, Scored: true},
	}
	m.picker, m.pickerLoaded = a, true

	rows, profiles, _ := m.pickerRowSet()
	if profiles != 2 || len(rows) != 2 {
		t.Fatalf("rows = %d (%d profiles), want two rows for the two harnesses", len(rows), profiles)
	}
	body := ansi.Strip(strings.Join(m.qualityPickerBody(120, 30), "\n"))
	if !strings.Contains(body, "claude") || !strings.Contains(body, "agy") {
		t.Fatalf("one harness column is missing:\n%s", body)
	}
}

// At 80 columns a key is cut in the middle, never at its effort end.
func TestQualityLongKeyKeepsItsEffortAt80(t *testing.T) {
	m := newModel(80, 24)
	m.tab = quotasTab
	m.qualityDetail = true
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "terra coder", Harness: "codex", Model: "gpt-6.1-terra-coder-preview", Effort: "medium"}}
	a := pickerFixture()
	a.Profiles = []protocol.PickerProfileRow{{Name: "terra coder", Key: "openai-codex/gpt-6.1-terra-coder-preview:medium", Model: "gpt-6.1-terra-coder-preview", Score: 55, Level: "very_complex", Ceiling: 5, Scored: true}}
	m.picker, m.pickerLoaded = a, true
	m.defaultPickerSelection()

	body := ansi.Strip(strings.Join(m.qualityPickerBody(80, 23), "\n"))
	row := rowContaining(body, "…:medium", "codex")
	if row == "" {
		t.Fatalf("the long key lost its effort or was not cut:\n%s", body)
	}
	for _, line := range strings.Split(body, "\n") {
		if w := ansi.StringWidth(line); w > 76 {
			t.Errorf("a row at 80 columns is %d wide: %q", w, line)
		}
	}
}
