package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// fakeDelivery is the fake data for the Delivery view. A later step maps the
// gateway's real types onto deliveryChange.
type fakeDelivery struct {
	changes []deliveryChange
	err     error
	calls   int
	repo    string
}

func (f *fakeDelivery) Changes(_ context.Context, repo string) ([]deliveryChange, error) {
	f.calls++
	f.repo = repo
	return f.changes, f.err
}

func dEv(step, result, commit, by, model string, at time.Time, fields map[string]string) deliveryEvidence {
	return deliveryEvidence{Step: step, Result: result, Commit: commit, By: by, Model: model, Fields: fields, At: at}
}

// deliveryScenes returns changes in different states. Times are relative to t0.
func deliveryScenes() []deliveryChange {
	return []deliveryChange{
		{
			ID: "c_1", Repo: "agent-net", Branch: "agent-net/w_1bcfc4c4a93b2687", Head: "9f3a1c2e7b4d", Revision: 2,
			Step: "review", StepOwner: "merger", Waiting: 2*time.Hour + 14*time.Minute,
			Evidence: []deliveryEvidence{
				dEv("accept", "passed", "9f3a1c2e7b4d", "caller", "", t0.Add(-3*time.Hour), nil),
				dEv("test", "passed", "9f3a1c2e7b4d", "caller", "", t0.Add(-2*time.Hour-30*time.Minute), map[string]string{"suite": "changed", "tests_added": "yes"}),
				dEv("review", "running", "9f3a1c2e7b4d", "merger", "claude-opus-5-5", t0.Add(-2*time.Hour-14*time.Minute), nil),
			},
		},
		{
			ID: "c_2", Repo: "agent-net", Branch: "delivery-pipeline", Head: "0b77d4e91a22", Revision: 1,
			Step: "merge", StepOwner: "caller", Waiting: 6 * time.Minute,
			Evidence: []deliveryEvidence{
				dEv("accept", "passed", "0b77d4e91a22", "caller", "", t0.Add(-40*time.Minute), nil),
				dEv("review", "passed", "0b77d4e91a22", "merger", "codex gpt-6.1-sol", t0.Add(-20*time.Minute), map[string]string{"findings_open": "0"}),
				dEv("test", "passed", "0b77d4e91a22", "github", "", t0.Add(-10*time.Minute), map[string]string{"suite": "ci"}),
				dEv("approve", "not needed", "0b77d4e91a22", "", "", t0.Add(-9*time.Minute), nil),
			},
		},
		{
			ID: "c_3", Repo: "voicemechs", Branch: "feature/a-very-long-branch-name-for-the-new-deploy-gate-and-more", Head: "c41de0aa5512", Revision: 3,
			Step: "review", StepOwner: "owner", Waiting: 26*time.Hour + 5*time.Minute,
			Evidence: []deliveryEvidence{
				dEv("accept", "passed", "c41de0aa5512", "caller", "", t0.Add(-30*time.Hour), nil),
				dEv("review", "failed", "c41de0aa5512", "merger", "claude-opus-5-5", t0.Add(-26*time.Hour-5*time.Minute), map[string]string{"findings_open": "2", "independent": "yes"}),
				dEv("review", "passed", "77aa00bb11cc", "merger", "claude-opus-5-5", t0.Add(-40*time.Hour), map[string]string{"findings_open": "0"}),
			},
		},
	}
}

func deliveryModel(src deliverySource, w, h int) Model {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.snap = fakeSnapshot()
	m.haveData = true
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab = quotasTab
	m.width, m.height = w, h
	m = m.withDeliverySource(src)
	return m
}

// open switches to the Delivery sub-tab and runs the load command.
func openDelivery(t *testing.T, m Model) Model {
	t.Helper()
	m.workersLog = false
	m.delivery.on = true
	cmd := m.deliveryLoad()
	if cmd == nil {
		t.Fatal("no load command")
	}
	next, _ := m.Update(cmd())
	return next.(Model)
}

func deliveryText(m Model, w, h int) string {
	return ansi.Strip(strings.Join(m.deliveryView(w, h), "\n"))
}

func TestDeliveryEmptyStateAndErrorLine(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{}, 80, 24))
	got := deliveryText(m, 80, 24)
	for _, want := range []string{"Delivery", "No open changes", "Quotas", "Workers log"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
	m = openDelivery(t, deliveryModel(&fakeDelivery{err: errors.New("gateway said:\nno such thing")}, 80, 24))
	got = deliveryText(m, 80, 24)
	if !strings.Contains(got, "Could not load changes: gateway said: no such thing") {
		t.Errorf("error line missing: %s", got)
	}
	if strings.Contains(got, "No open changes") {
		t.Errorf("empty text beside an error: %s", got)
	}
}

func TestDeliveryErrorKeepsTheLastList(t *testing.T) {
	src := &fakeDelivery{changes: deliveryScenes()}
	m := openDelivery(t, deliveryModel(src, 120, 30))
	src.changes, src.err = nil, errors.New("gateway down")
	next, _ := m.Update(m.deliveryLoad()())
	got := deliveryText(next.(Model), 120, 30)
	if !strings.Contains(got, "Could not load changes: gateway down") || !strings.Contains(got, "delivery-pipeline") {
		t.Errorf("error should sit above the old list: %s", got)
	}
}

func TestDeliveryRowsFitEveryWidth(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 80, 30))
	for _, w := range []int{80, 100, 120, 160, 200} {
		lines := m.deliveryView(w, 30)
		for _, l := range lines {
			if ansi.StringWidth(l) > w {
				t.Errorf("width %d overflow (%d): %q", w, ansi.StringWidth(l), ansi.Strip(l))
			}
		}
		got := ansi.Strip(strings.Join(lines, "\n"))
		for _, want := range []string{"agent-net", "voicemechs", "r2", "review", "merger", "2h14m", "6m", "1d2h"} {
			if !strings.Contains(got, want) {
				t.Errorf("width %d missing %q: %s", w, want, got)
			}
		}
		if strings.Contains(got, "agent-net/w_1bcfc") {
			t.Errorf("width %d: branch keeps the agent-net/ prefix", w)
		}
	}
}

func TestDeliveryColumnsAlign(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 80, 30))
	for _, w := range []int{80, 120, 200} {
		lines := m.deliveryView(w, 30)
		var header string
		var rows []string
		for _, l := range lines {
			s := ansi.Strip(l)
			switch {
			case strings.Contains(s, "REPO") && strings.Contains(s, "BRANCH"):
				header = s
			case strings.Contains(s, "r2 ") || strings.Contains(s, "r1 ") || strings.Contains(s, "r3 "):
				rows = append(rows, s)
			}
		}
		if header == "" || len(rows) != 3 {
			t.Fatalf("width %d: header %q rows %d", w, header, len(rows))
		}
		// The REV column starts at the same place in the header and every row.
		col := len([]rune(header[:strings.Index(header, "REV")]))
		for _, r := range rows {
			rr := []rune(r)
			if rr[col] != 'r' || rr[col-1] != ' ' {
				t.Errorf("width %d: rev column misaligned: %q vs %q", w, header, r)
			}
			if ansi.StringWidth(r) != ansi.StringWidth(rows[0]) {
				t.Errorf("width %d: row widths differ", w)
			}
		}
	}
}

func TestShortBranch(t *testing.T) {
	for in, want := range map[string]string{
		"agent-net/w_1bcfc4c4a93b2687": "w_1bcfc4c4a93b2687",
		"refs/heads/feature/x":         "feature/x",
		"main":                         "main",
		"":                             "-",
	} {
		if got := shortBranch(in); got != want {
			t.Errorf("shortBranch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeliveryWaitingText(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "-", 20 * time.Second: "<1m", 6 * time.Minute: "6m", 59 * time.Minute: "59m",
		2*time.Hour + 14*time.Minute: "2h14m", 26*time.Hour + 5*time.Minute: "1d2h", 100 * 24 * time.Hour: "100d",
	} {
		if got := deliveryWaitText(d); got != want {
			t.Errorf("deliveryWaitText(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestDeliveryStripStates(t *testing.T) {
	c := deliveryScenes()[2] // accept passed, review failed on head, older review pass on another commit
	want := map[string]stripState{
		"accept": stripPassed, "review": stripFailed, "test": stripWaiting, "approve": stripWaiting,
		"merge": stripWaiting, "release": stripWaiting, "deploy": stripWaiting, "verify": stripWaiting,
	}
	got := deliveryStripStates(c)
	if len(got) != len(deliverySteps) {
		t.Fatalf("strip has %d marks", len(got))
	}
	for i, step := range deliverySteps {
		if got[i] != want[step] {
			t.Errorf("%s: %v, want %v", step, got[i], want[step])
		}
	}
	// A pass for an older commit does not count, and the newest evidence for the head wins.
	c = deliveryChange{Head: "bbb", Step: "test", Evidence: []deliveryEvidence{
		dEv("review", "passed", "aaa", "x", "", t0, nil),
		dEv("test", "failed", "bbb", "x", "", t0, nil),
		dEv("test", "passed", "bbb", "x", "", t0.Add(time.Minute), nil),
		dEv("approve", "not needed", "bbb", "x", "", t0, nil),
		dEv("merge", "running", "bbb", "x", "", t0, nil),
	}}
	st := deliveryStripStates(c)
	if st[1] != stripWaiting || st[2] != stripPassed || st[3] != stripNotNeeded || st[4] != stripRunning {
		t.Errorf("states %v", st)
	}
}

func TestDeliveryStripShowsGlyphsNotOnlyColour(t *testing.T) {
	line := ansi.Strip(deliveryStrip(deliveryStripStates(deliveryScenes()[2]), false))
	if line != "a✓r✗t·p·m·l·d·v·" {
		t.Errorf("compact strip %q", line)
	}
	line = ansi.Strip(deliveryStrip(deliveryStripStates(deliveryScenes()[1]), true))
	if line != "a✓ r✓ t✓ p– m· l· d· v·" {
		t.Errorf("spaced strip %q", line)
	}
	if ansi.StringWidth(deliveryStrip(deliveryStripStates(deliveryScenes()[0]), false)) != deliveryStripWidth(false) {
		t.Error("strip width differs from deliveryStripWidth")
	}
}

func TestDeliverySubtabCycle(t *testing.T) {
	src := &fakeDelivery{changes: deliveryScenes()}
	m := deliveryModel(src, 80, 24)
	press := func(k tea.KeyPressMsg) tea.Cmd {
		next, cmd := m.Update(k)
		m = next.(Model)
		return cmd
	}
	tab := tea.KeyPressMsg{Code: tea.KeyTab}
	press(tab)
	if !m.workersLog || m.delivery.on {
		t.Fatal("first tab should open the Workers log")
	}
	cmd := press(tab)
	if m.workersLog || !m.delivery.on {
		t.Fatal("second tab should open Delivery")
	}
	if cmd == nil {
		t.Fatal("opening Delivery should load")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if src.calls != 1 || src.repo != "" || len(m.delivery.changes) != 3 {
		t.Fatalf("calls %d repo %q changes %d", src.calls, src.repo, len(m.delivery.changes))
	}
	if got := ansi.Strip(m.testView()); !strings.Contains(got, "[ Delivery ]") {
		t.Errorf("subtab not marked: %s", got)
	}
	press(tab)
	if m.workersLog || m.delivery.on {
		t.Fatal("third tab should return to Quotas")
	}
	// R reloads while on the Delivery sub-tab.
	press(tab)
	press(tab)
	cmd = press(tea.KeyPressMsg{Text: "R", Code: []rune("R")[0]})
	if cmd == nil {
		t.Fatal("R should reload")
	}
}

func TestDeliveryEnterOpensDetailAndEscCloses(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 120, 30))
	press := func(k tea.KeyPressMsg) {
		next, _ := m.Update(k)
		m = next.(Model)
	}
	press(tea.KeyPressMsg{Code: tea.KeyDown})
	press(tea.KeyPressMsg{Code: tea.KeyDown})
	press(tea.KeyPressMsg{Code: tea.KeyDown}) // clamps at the last row
	if m.delivery.row != 2 {
		t.Fatalf("row %d", m.delivery.row)
	}
	press(tea.KeyPressMsg{Code: tea.KeyUp})
	press(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.delivery.detail {
		t.Fatal("enter did not open the detail")
	}
	got := deliveryText(m, 120, 30)
	for _, want := range []string{"delivery-pipeline", "0b77d4e91a22", "merge", "caller", "findings_open=0", "codex gpt-6.1-sol", "10-29 13:45"} {
		if !strings.Contains(got, want) {
			t.Errorf("detail missing %q: %s", want, got)
		}
	}
	press(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.delivery.detail || m.tab != quotasTab || !m.delivery.on {
		t.Fatal("esc should only close the detail")
	}
}

func TestDeliveryDetailUsesTheDisplayZone(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 120, 30))
	m.loc = time.FixedZone("CET", 3600)
	m.delivery.detail = true
	got := deliveryText(m, 120, 30)
	// The first change's accept evidence is 11:05 UTC, so 12:05 in CET.
	if !strings.Contains(got, "10-29 12:05") || strings.Contains(got, "10-29 11:05") {
		t.Errorf("detail ignored the display zone: %s", got)
	}
}

func TestDeliveryDetailMarksOldEvidenceAndHidesNothing(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 80, 40))
	m.delivery.row, m.delivery.detail = 2, true
	for _, w := range []int{80, 120, 200} {
		lines := m.deliveryView(w, 40)
		for _, l := range lines {
			if ansi.StringWidth(l) > w {
				t.Errorf("width %d overflow: %q", w, ansi.Strip(l))
			}
		}
		got := ansi.Strip(strings.Join(lines, "\n"))
		for _, want := range []string{"failed", "77aa00b", "old", "findings_open=2", "independent=yes", "claude-opus-5-5"} {
			if !strings.Contains(got, want) {
				t.Errorf("width %d detail missing %q: %s", w, want, got)
			}
		}
	}
}

func TestDeliverySanitizesText(t *testing.T) {
	c := deliveryScenes()[0]
	c.Repo = "bad\x1b[31mrepo"
	c.StepOwner = "ow\nner"
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: []deliveryChange{c}}, 80, 24))
	for _, l := range m.deliveryView(80, 24) {
		if strings.Contains(ansi.Strip(l), "\x1b") || strings.Contains(l, "\n") {
			t.Fatalf("unsafe line %q", l)
		}
	}
}

func TestDeliveryNoSourceShowsTheEmptyState(t *testing.T) {
	m := deliveryModel(nil, 80, 24)
	m.delivery.on = true
	if cmd := m.deliveryLoad(); cmd != nil {
		t.Fatal("no source, no load")
	}
	if got := deliveryText(m, 80, 24); !strings.Contains(got, "No open changes") {
		t.Fatal(got)
	}
}

func TestDeliveryHelpAndHints(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 120, 30))
	hints := ""
	for _, h := range m.hints() {
		hints += h.key + " " + h.label + "|"
	}
	for _, want := range []string{"evidence", "tab", "refresh"} {
		if !strings.Contains(hints, want) {
			t.Errorf("hints %q missing %q", hints, want)
		}
	}
	m.help = true
	if got := ansi.Strip(strings.Join(m.helpContent(120), "\n")); !strings.Contains(got, "Delivery") {
		t.Errorf("help has no Delivery section: %s", got)
	}
}

func TestDeliveryGoldens(t *testing.T) {
	scenes := deliveryScenes()
	type scene struct {
		name   string
		src    *fakeDelivery
		row    int
		detail bool
	}
	for _, s := range []scene{
		{"delivery_empty", &fakeDelivery{}, 0, false},
		{"delivery_error", &fakeDelivery{err: errors.New("the gateway did not answer in time")}, 0, false},
		{"delivery_list", &fakeDelivery{changes: scenes}, 0, false},
		{"delivery_failed_review", &fakeDelivery{changes: scenes}, 2, false},
		{"delivery_detail", &fakeDelivery{changes: scenes}, 0, true},
		{"delivery_detail_failed", &fakeDelivery{changes: scenes}, 2, true},
	} {
		for _, size := range [][2]int{{80, 24}, {120, 24}} {
			m := openDelivery(t, deliveryModel(s.src, size[0], size[1]))
			m.delivery.row, m.delivery.detail = s.row, s.detail
			golden(t, fmt.Sprintf("%s_%dx%d", s.name, size[0], size[1]), m.testView())
		}
	}
}
