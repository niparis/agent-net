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
	"github.com/muesli/termenv"

	"github.com/niparis/agent-net/internal/tui/palette"
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

// marks is a whole evidence strip for a scene.
func marks(states ...stripState) []stripState {
	return append(append(append([]stripState{}, states[:5]...), stripWaiting), states[5:]...)
}

// deliveryScenes returns changes in different states. Times are relative to t0.
// c_1 and c_2 wait on agents, c_3 needs the owner, c_4 is done and c_5 is a
// stale open change: the last two are hidden until the owner presses a.
func deliveryScenes() []deliveryChange {
	return []deliveryChange{
		{
			ID: "c_1", Title: "Show which provider each quota row belongs to", Repo: "agent-net",
			Branch: "agent-net/w_1bcfc4c4a93b2687", Head: "9f3a1c2e7b4d", Revision: 2, State: "open", Caller: "s_0000000000000001",
			Step: "review", StepOwner: "merger", OwnerKind: "node", StepState: "ready", Waiting: 2*time.Hour + 14*time.Minute,
			Marks: marks(stripPassed, stripWaiting, stripPassed, stripNotNeeded, stripWaiting, stripWaiting, stripWaiting, stripWaiting),
			Evidence: []deliveryEvidence{
				dEv("accept", "passed", "9f3a1c2e7b4d", "caller", "", t0.Add(-3*time.Hour), nil),
				dEv("test", "passed", "9f3a1c2e7b4d", "caller", "", t0.Add(-2*time.Hour-30*time.Minute), map[string]string{"suite": "changed", "tests_added": "yes"}),
				dEv("review", "waiting", "9f3a1c2e7b4d", "merger", "", t0.Add(-2*time.Hour-14*time.Minute), nil),
			},
		},
		{
			ID: "c_2", Title: "Say what a delivery row waits for", Repo: "agent-net",
			Branch: "delivery-pipeline", Head: "0b77d4e91a22", MergedCommit: "abcdef1234567890", Revision: 1, State: "merged", Caller: "s_0000000000000004",
			Step: "test", StepOwner: "caller", OwnerKind: "caller", StepState: "ready", Waiting: 6 * time.Minute,
			Marks: marks(stripPassed, stripPassed, stripWaiting, stripNotNeeded, stripPassed, stripWaiting, stripWaiting, stripWaiting),
			Evidence: []deliveryEvidence{
				dEv("accept", "passed", "0b77d4e91a22", "caller", "", t0.Add(-40*time.Minute), nil),
				dEv("review", "passed", "0b77d4e91a22", "merger", "codex gpt-6.1-sol", t0.Add(-20*time.Minute), map[string]string{"findings_open": "0"}),
				dEv("test", "waiting", "0b77d4e91a22", "github", "", t0.Add(-10*time.Minute), map[string]string{"suite": "ci"}),
				dEv("approve", "not needed", "0b77d4e91a22", "", "", t0.Add(-9*time.Minute), nil),
			},
		},
		{
			ID: "c_3", Title: "Fix the deploy gate", Repo: "voicemechs",
			Branch: "feature/a-very-long-branch-name-for-the-new-deploy-gate-and-more", Head: "c41de0aa5512", Revision: 3, State: "open", Caller: "s_0000000000000002",
			Step: "review", StepOwner: "owner", OwnerKind: "owner", StepState: "failed", Waiting: 2*time.Hour + 5*time.Minute,
			Marks: marks(stripPassed, stripFailed, stripWaiting, stripNotNeeded, stripWaiting, stripWaiting, stripWaiting, stripWaiting),
			Evidence: []deliveryEvidence{
				dEv("accept", "passed", "c41de0aa5512", "caller", "", t0.Add(-6*time.Hour), nil),
				dEv("review", "failed", "c41de0aa5512", "merger", "claude-opus-5-5", t0.Add(-2*time.Hour-5*time.Minute), map[string]string{"findings_open": "2", "independent": "yes"}),
				dEv("review", "passed", "77aa00bb11cc", "merger", "claude-opus-5-5", t0.Add(-40*time.Hour), map[string]string{"findings_open": "0"}),
			},
		},
		{
			ID: "c_4", Title: "Drop the old parser", Repo: "agent-net",
			Branch: "agent-net/w_4d2b", Head: "5511aa22bb33", Revision: 2, State: "done", Waiting: 3 * 24 * time.Hour,
			Marks: marks(stripPassed, stripPassed, stripPassed, stripPassed, stripPassed, stripPassed, stripPassed, stripPassed),
			Evidence: []deliveryEvidence{
				dEv("verify", "passed", "5511aa22bb33", "caller", "", t0.Add(-3*24*time.Hour), nil),
			},
		},
		{
			ID: "c_5", Title: "Import suppliers from the CSV", Repo: "wbg-procurement",
			Branch: "agent-net/w_9a8b", Head: "77cc33dd44ee", Revision: 1, State: "open", Caller: "s_unknown",
			Step: "accept", StepOwner: "caller", OwnerKind: "caller", StepState: "ready", Waiting: 30 * time.Hour,
			Marks: marks(stripWaiting, stripWaiting, stripWaiting, stripNotNeeded, stripWaiting, stripWaiting, stripWaiting, stripWaiting),
		},
	}
}

func deliveryModel(src deliverySource, w, h int) Model {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.snap = fakeSnapshot()
	m.haveData = true
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab = deliveryTab
	m.width, m.height = w, h
	m = m.withDeliverySource(src)
	return m
}

// open switches to the Delivery tab and runs the load command.
func openDelivery(t *testing.T, m Model) Model {
	t.Helper()
	m.workersLog = false
	m.delivery.on = true
	m.tab = deliveryTab
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

// visibleRow is the cursor position of a change in the grouped list, or -1.
func visibleRow(m Model, id string) int {
	for i, ch := range m.deliveryVisible() {
		if ch.ID == id {
			return i
		}
	}
	return -1
}

func TestDeliveryReadableInDarkAndLight(t *testing.T) {
	old := testColorProfile
	defer setTestColorProfile(old)
	defer palette.SetDark(true)
	setTestColorProfile(termenv.TrueColor)
	for _, dark := range []bool{true, false} {
		palette.SetDark(dark)
		m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 120, 24))
		for _, w := range []int{80, 120} {
			lines := m.deliveryView(w, 24)
			for _, l := range lines {
				if ansi.StringWidth(l) > w {
					t.Errorf("dark %v width %d overflow: %q", dark, w, ansi.Strip(l))
				}
			}
			got := ansi.Strip(strings.Join(lines, "\n"))
			for _, want := range []string{"Needs you", "Waiting on agents", "waiting for merger", "waiting for ci-runner"} {
				if !strings.Contains(got, want) {
					t.Errorf("dark %v width %d missing %q:\n%s", dark, w, want, got)
				}
			}
		}
	}
}

func TestDeliveryTitleIsTheMainColumn(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 120, 30))
	for _, w := range []int{80, 120} {
		got := deliveryText(m, w, 30)
		for _, want := range []string{"Show which", "Say what", "Fix the"} {
			if !strings.Contains(got, want) {
				t.Errorf("width %d: title missing %q:\n%s", w, want, got)
			}
		}
		// The branch is for the detail view only.
		for _, branch := range []string{"w_1bcfc4c4a93b2687", "delivery-pipeline", "feature/a-very-long"} {
			if strings.Contains(got, branch) {
				t.Errorf("width %d: branch %q is in the list:\n%s", w, branch, got)
			}
		}
	}
	m.delivery.row = visibleRow(m, "c_1")
	m.delivery.detail = true
	detail := deliveryText(m, 120, 30)
	if !strings.Contains(detail, "delivery-pipeline") && !strings.Contains(detail, "w_1bcfc4c4a93b2687") {
		t.Errorf("detail does not show the branch:\n%s", detail)
	}
}

func TestDeliverySaysWhatItWaitsFor(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 120, 30))
	got := deliveryText(m, 120, 30)
	for _, want := range []string{"waiting for merger", "waiting for ci-runner"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "running") {
		t.Errorf("nothing is claimed, but the list says running:\n%s", got)
	}

	// A ready step owned by the person, or by the owner's own session, waits
	// for "you"; a claimed step is the only one that says running.
	m.snap.OwnerSession = "s_owner"
	owner := deliveryChange{Step: "approve", StepOwner: "owner", OwnerKind: "owner", StepState: "ready"}
	if status, _ := m.deliveryStatusText(owner); status != "waiting for you" {
		t.Errorf("person-owned ready step says %q", status)
	}
	caller := deliveryChange{Step: "test", StepOwner: "caller", OwnerKind: "caller", StepState: "ready", Caller: "s_owner"}
	if status, _ := m.deliveryStatusText(caller); status != "waiting for you" {
		t.Errorf("owner's caller session says %q", status)
	}
	other := deliveryChange{Step: "test", StepOwner: "caller", OwnerKind: "caller", StepState: "ready", Caller: "s_0000000000000004"}
	if status, _ := m.deliveryStatusText(other); status != "waiting for ci-runner" {
		t.Errorf("caller session says %q", status)
	}
	running := deliveryChange{Step: "test", StepOwner: "merger", OwnerKind: "node", StepState: "running"}
	if status, _ := m.deliveryStatusText(running); status != "running" {
		t.Errorf("claimed step says %q", status)
	}
}

func TestDeliveryGroupsAndHidesDoneAndStale(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 120, 24))
	got := deliveryText(m, 120, 24)
	for _, want := range []string{"Needs you · 1", "Waiting on agents · 2", "1 done and 1 stale hidden · a shows all"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	for _, hidden := range []string{"Drop the old parser", "Import suppliers from the CSV"} {
		if strings.Contains(got, hidden) {
			t.Errorf("hidden change %q is on screen:\n%s", hidden, got)
		}
	}
	// a shows everything, with the age of the stale change.
	next, _ := m.Update(tea.KeyPressMsg{Text: "a", Code: 'a'})
	m = next.(Model)
	if !m.delivery.showAll {
		t.Fatal("a did not show all")
	}
	got = deliveryText(m, 120, 24)
	for _, want := range []string{"Waiting on agents · 3", "Done · 1", "Drop the old parser", "Import suppliers", "1d6h!"} {
		if !strings.Contains(got, want) {
			t.Errorf("show all missing %q:\n%s", want, got)
		}
	}
	// a again hides them.
	next, _ = m.Update(tea.KeyPressMsg{Text: "a", Code: 'a'})
	m = next.(Model)
	if got := deliveryText(m, 120, 24); strings.Contains(got, "Drop the old parser") {
		t.Errorf("a did not hide done changes again:\n%s", got)
	}
}

func TestDeliveryStaleAndGroups(t *testing.T) {
	for _, tc := range []struct {
		name string
		ch   deliveryChange
	}{
		{"open for a day", deliveryChange{State: "open", Waiting: 24 * time.Hour}},
		{"open for 23h", deliveryChange{State: "open", Waiting: 23 * time.Hour}},
		{"merged for a week", deliveryChange{State: "merged", Waiting: 7 * 24 * time.Hour}},
		{"done", deliveryChange{State: "done", Waiting: 7 * 24 * time.Hour}},
		{"stopped", deliveryChange{State: "stopped", Waiting: 7 * 24 * time.Hour}},
		{"closed step", deliveryChange{State: "open", StepState: "closed", Waiting: 7 * 24 * time.Hour}},
	} {
		want := tc.ch.Waiting >= 24*time.Hour && tc.ch.State == "open" && tc.ch.StepState != "closed"
		if got := deliveryStale(tc.ch); got != want {
			t.Errorf("%s: stale %v, want %v", tc.name, got, want)
		}
	}
	for _, tc := range []struct {
		name string
		ch   deliveryChange
		want deliveryGroup
	}{
		{"person, ready", deliveryChange{State: "open", Step: "review", StepState: "ready", OwnerKind: "owner"}, groupNeedsYou},
		{"delegate, ready", deliveryChange{State: "open", Step: "approve", StepState: "needs_owner", OwnerKind: "delegate"}, groupNeedsYou},
		{"approval", deliveryChange{State: "open", Step: "approve", StepState: "ready", OwnerKind: "node"}, groupNeedsYou},
		{"owner's caller", deliveryChange{State: "open", Step: "test", StepState: "ready", OwnerKind: "caller", Caller: "s_owner"}, groupNeedsYou},
		{"other caller", deliveryChange{State: "open", Step: "test", StepState: "ready", OwnerKind: "caller", Caller: "s_agent"}, groupWaiting},
		{"node", deliveryChange{State: "open", Step: "review", StepState: "ready", OwnerKind: "node"}, groupWaiting},
		{"passed", deliveryChange{State: "open", Step: "review", StepState: "passed", OwnerKind: "owner"}, groupWaiting},
		{"done", deliveryChange{State: "done"}, groupDone},
		{"stopped", deliveryChange{State: "stopped"}, groupDone},
		{"closed step", deliveryChange{State: "open", StepState: "closed"}, groupDone},
	} {
		if got := deliveryGroupOf(tc.ch, "s_owner"); got != tc.want {
			t.Errorf("%s: group %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDeliveryEmptyStateAndErrorLine(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{}, 80, 24))
	got := deliveryText(m, 80, 24)
	for _, want := range []string{"Delivery", "No open changes", "Quota"} {
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
	if !strings.Contains(got, "Could not load changes: gateway down") || !strings.Contains(got, "Say what a delivery row waits") {
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
		for _, want := range []string{"Show which", "Say what", "Fix the", "waiting for merger", "waiting for ci-runner", "2h14m", "6m", "failed"} {
			if !strings.Contains(got, want) {
				t.Errorf("width %d missing %q: %s", w, want, got)
			}
		}
		if w >= 96 {
			for _, want := range []string{"agent-net", "voicemechs"} {
				if !strings.Contains(got, want) {
					t.Errorf("width %d missing repo %q: %s", w, want, got)
				}
			}
		}
		for _, branch := range []string{"w_1bcfc4c4a93b2687", "delivery-pipeline", "feature/a-very-long"} {
			if strings.Contains(got, branch) {
				t.Errorf("width %d: branch %q is in the list: %s", w, branch, got)
			}
		}
	}
}

func TestDeliveryLegendNamesEveryLetter(t *testing.T) {
	for _, w := range []int{80, 120, 200} {
		inner := w - 4
		lines := deliveryLegend(inner)
		got := ansi.Strip(strings.Join(lines, " "))
		for _, want := range []string{"a accept", "r review", "t test", "p approve", "m merge", "l release", "d deploy", "v verify.", "✓ passed", "✗ failed", "waiting", "not needed"} {
			if !strings.Contains(got, want) {
				t.Errorf("width %d legend missing %q: %q", w, want, got)
			}
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > inner {
				t.Errorf("width %d legend overflow (%d): %q", w, ansi.StringWidth(l), ansi.Strip(l))
			}
		}
	}
}

func TestDeliveryColumnsAlign(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 80, 30))
	for _, w := range []int{80, 120, 200} {
		cols := deliveryLayout(w-4, m.deliveryVisible())
		if got := cols.width(); got > w-4 {
			t.Errorf("width %d: columns take %d of %d", w, got, w-4)
		}
		header := ansi.Strip(cols.header())
		stepAt := strings.Index(header, "STEP")
		if stepAt < 0 {
			t.Fatalf("width %d: no STEP header: %q", w, header)
		}
		for _, ch := range m.deliveryVisible() {
			row := ansi.Strip(m.deliveryRow(cols, ch, false))
			if ansi.StringWidth(row) != cols.width() {
				t.Errorf("width %d: row %d wide, want %d: %q", w, ansi.StringWidth(row), cols.width(), row)
			}
			want := firstNonEmpty(ch.Step, "-")
			prefix := ansi.Truncate(row, stepAt, "")
			if got := row[len(prefix):]; !strings.HasPrefix(got, want) {
				t.Errorf("width %d: step column shows %q, want %q in %q", w, got, want, row)
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
		"accept": stripPassed, "review": stripFailed, "test": stripWaiting, "approve": stripNotNeeded,
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
	if line != "a✓r✗t·p-m·T·l·d·v·" {
		t.Errorf("compact strip %q", line)
	}
	line = ansi.Strip(deliveryStrip(deliveryStripStates(deliveryScenes()[1]), true))
	if line != "a✓ r✓ t· p- m✓ T· l· d· v·" {
		t.Errorf("spaced strip %q", line)
	}
	if ansi.StringWidth(deliveryStrip(deliveryStripStates(deliveryScenes()[0]), false)) != deliveryStripWidth(false) {
		t.Error("strip width differs from deliveryStripWidth")
	}
}

func TestDeliverySubtabCycle(t *testing.T) {
	m := deliveryModel(&fakeDelivery{}, 80, 24)
	m.tab = quotasTab
	m, _ = m.nextQuotasSubtab()
	if !m.workersLog {
		t.Fatal("workers log did not open")
	}
	m, _ = m.nextQuotasSubtab()
	if m.workersLog {
		t.Fatal("quotas did not open")
	}
	next, cmd := m.Update(tea.KeyPressMsg{Text: "8", Code: '8'})
	m = next.(Model)
	if m.tab != deliveryTab || cmd == nil {
		t.Fatal("delivery tab did not open and load")
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
	press(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.delivery.detail {
		t.Fatal("enter did not open the detail")
	}
	got := deliveryText(m, 120, 30)
	for _, want := range []string{"Say what a delivery row waits for", "delivery-pipeline", "0b77d4e91a22", "test", "caller", "waiting for ci-runner", "findings_open=0", "codex gpt-6.1-sol", "10-29 13:45"} {
		if !strings.Contains(got, want) {
			t.Errorf("detail missing %q: %s", want, got)
		}
	}
	press(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.delivery.detail || m.tab != deliveryTab {
		t.Fatal("esc should only close the detail")
	}
}

func TestDeliveryDetailUsesTheDisplayZone(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 120, 30))
	m.loc = time.FixedZone("CET", 3600)
	m.delivery.row, m.delivery.detail = visibleRow(m, "c_1"), true
	got := deliveryText(m, 120, 30)
	// The first change's accept evidence is 11:05 UTC, so 12:05 in CET.
	if !strings.Contains(got, "10-29 12:05") || strings.Contains(got, "10-29 11:05") {
		t.Errorf("detail ignored the display zone: %s", got)
	}
}

func TestDeliveryDetailFillsAvailableHeight(t *testing.T) {
	m := deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 80, 24)
	ch := deliveryScenes()[2]
	got := m.deliveryDetail(ch, nil, 80, 20)
	if len(got) != 22 {
		t.Fatalf("detail used %d rows, want 22 to fill the available area", len(got))
	}
}

func TestDeliveryDetailMarksOldEvidenceAndHidesNothing(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 80, 40))
	m.delivery.row, m.delivery.detail = visibleRow(m, "c_3"), true
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
	for _, want := range []string{"evidence", "show all", "refresh"} {
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
		rowID  string
		detail bool
		all    bool
	}
	for _, s := range []scene{
		{"delivery_empty", &fakeDelivery{}, "", false, false},
		{"delivery_error", &fakeDelivery{err: errors.New("the gateway did not answer in time")}, "", false, false},
		{"delivery_list", &fakeDelivery{changes: scenes}, "", false, false},
		{"delivery_all", &fakeDelivery{changes: scenes}, "", false, true},
		{"delivery_failed_review", &fakeDelivery{changes: scenes}, "c_3", false, false},
		{"delivery_detail", &fakeDelivery{changes: scenes}, "c_1", true, false},
		{"delivery_detail_failed", &fakeDelivery{changes: scenes}, "c_3", true, false},
	} {
		for _, size := range [][2]int{{80, 24}, {120, 34}} {
			m := openDelivery(t, deliveryModel(s.src, size[0], size[1]))
			m.delivery.detail, m.delivery.showAll = s.detail, s.all
			if s.rowID != "" {
				m.delivery.row = visibleRow(m, s.rowID)
			}
			golden(t, fmt.Sprintf("%s_%dx%d", s.name, size[0], size[1]), m.testView())
		}
	}
}

func TestDeliveryTopLevelAndProjectFilter(t *testing.T) {
	m := deliveryModel(nil, 80, 24)
	m.delivery.changes = []deliveryChange{{ID: "ch_a", Repo: "one"}, {ID: "ch_b", Repo: "two"}}
	m.delivery.project = "one"
	if got := m.deliveryVisible(); len(got) != 1 || got[0].ID != "ch_a" {
		t.Fatalf("project filter: %+v", got)
	}
	if len(tabNames) != 8 || tabNames[7] != "Delivery" {
		t.Fatalf("tabs: %v", tabNames)
	}
}
