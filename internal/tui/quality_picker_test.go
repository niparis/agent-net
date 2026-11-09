package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
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
	if best < 0 || m.picker.Models[best].Key != "opencode-go/deepseek-v4.1-flash:high" {
		t.Fatalf("best = %d, want the deepseek cell with a record", best)
	}

	// Each row's detail line names the verdict the rule gives it.
	wantReason := map[string]string{
		"opencode-go/deepseek-v4.1-flash:high": "verdict: allowed",
		"openai-codex/gpt-6.1-sol:low":         "verdict: chosen by benchmark (n<3)",
		"anthropic/claude-opus-5-5":            "verdict: chosen by benchmark (n<3)",
	}
	for i, row := range m.picker.Models {
		m.pickerSelModel = i
		body := ansi.Strip(strings.Join(m.qualityPickerBody(120, 30), "\n"))
		if reason, ok := wantReason[row.Key]; ok && !strings.Contains(body, reason) {
			t.Errorf("%s: missing %q in\n%s", row.Key, reason, body)
		}
	}

	// A bucket where sol's thin record is above its ceiling says so.
	for i, row := range m.picker.Models {
		if row.Key == "openai-codex/gpt-6.1-sol:low" {
			m.pickerSelModel = i
		}
	}
	m.pickerSelBucket = 8
	body := ansi.Strip(strings.Join(m.qualityPickerBody(120, 30), "\n"))
	if !strings.Contains(body, "verdict: left out (above its benchmark)") {
		t.Fatalf("sol above its benchmark is not named:\n%s", body)
	}
}

// A benchmark ceiling past the last bucket on screen draws no marker: a mark
// at the window edge reads as a ceiling there, or as a second panel border.
// A ceiling that ends before a visible bucket still marks its cell.
func TestQualityPickerCeilingPastTheWindowHasNoMark(t *testing.T) {
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		m := newModel(size[0], size[1])
		m.picker, m.pickerLoaded = pickerFixture(), true
		m.defaultPickerSelection()
		inner := max(size[0]-4, 20)
		fit := max(1, (inner-pickerModelWidth-2)/pickerCellWidth)
		start, end := pickerColumnWindow(m.pickerColumns(), m.pickerSelBucket, fit)
		cols := m.pickerColumns()[start:end]
		lastLo := m.picker.Buckets[cols[len(cols)-1]].Lo
		bench := m.pickerBenchmarks()
		marked := map[string]bool{}
		for i, row := range m.picker.Models {
			line := ansi.Strip(m.pickerModelLine(i, -1, pickerModelWidth, cols, inner, bench))
			b := bench[row.Key]
			if !b.scored {
				continue
			}
			if b.ceiling > lastLo {
				if strings.Contains(line, "│") {
					t.Errorf("%dx%d: %s ceiling %.2f is past the window ending at %.2f but the row has a marker:\n%s",
						size[0], size[1], row.Key, b.ceiling, lastLo, line)
				}
				continue
			}
			marked[row.Key] = strings.Contains(line, "│")
		}
		if !marked["openai-codex/gpt-6.1-sol:low"] {
			t.Errorf("%dx%d: the ceiling inside the window is not marked:\n%s", size[0], size[1], strings.Join(m.qualityPickerBody(size[0], 30), "\n"))
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
	wide := pickerCellStyle(protocol.PickerCell{Score: .5, Lo: .2, Hi: .8, N: 10}, .5, false)
	if !wide.GetFaint() {
		t.Error("a wide interval is not dim")
	}
	narrow := pickerCellStyle(protocol.PickerCell{Score: .5, Lo: .45, Hi: .55, N: 10}, .5, false)
	if !narrow.GetBold() || narrow.GetFaint() {
		t.Error("a narrow interval is not bright")
	}
	thin := pickerCellStyle(protocol.PickerCell{Score: .5, Lo: .45, Hi: .55, N: 2}, .5, false)
	if !thin.GetFaint() {
		t.Error("a cell with fewer than three tasks is not dim")
	}
	above := pickerCellStyle(protocol.PickerCell{Score: .8, Lo: .7, Hi: .9, N: 10}, .5, false)
	if above.GetForeground() != roleGood {
		t.Error("a cell whose interval is above the mean is not green")
	}
	below := pickerCellStyle(protocol.PickerCell{Score: .2, Lo: .1, Hi: .3, N: 10}, .5, false)
	if below.GetForeground() != roleError {
		t.Error("a cell whose interval is below the mean is not red")
	}
	selected := pickerCellStyle(protocol.PickerCell{Score: .5, Lo: .2, Hi: .8, N: 10}, .5, true)
	if !selected.GetUnderline() {
		t.Error("the selected cell is not underlined")
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
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
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
