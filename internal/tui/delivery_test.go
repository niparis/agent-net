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

	"github.com/niparis/agent-net/internal/protocol"
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

func (f *fakeDelivery) Change(_ context.Context, id string) (deliveryChange, error) {
	for _, ch := range f.changes {
		if ch.ID == id {
			return ch, nil
		}
	}
	return deliveryChange{}, errors.New("no such change")
}

func dEv(step, result, commit, by, model string, at time.Time, fields map[string]string) deliveryEvidence {
	return deliveryEvidence{Step: step, Result: result, Commit: commit, By: by, Model: model, Fields: fields, At: at}
}

// marks is a whole evidence strip for a scene.
func marks(states ...stripState) []stripState {
	return append(append(append([]stripState{}, states[:5]...), stripWaiting), states[5:]...)
}

// deliveryScenes returns changes in different states. Times are relative to t0.
// c_1 and c_2 are moving, c_3 needs the owner, c_4 is done, c_5 is stale,
// and c_6/c_7 collapse into one stuck line.
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
		{ID: "c_6", Title: "Wait for procurement accept A", Repo: "wbg-procurement", State: "open", Step: "accept", StepOwner: "wbg-procurement", OwnerKind: "node", StepState: "ready", Waiting: 15 * time.Hour},
		{ID: "c_7", Title: "Wait for procurement accept B", Repo: "wbg-procurement", State: "open", Step: "accept", StepOwner: "wbg-procurement", OwnerKind: "node", StepState: "ready", Waiting: 22 * time.Hour},
		{ID: "c_8", Title: "Approve the deploy gate", Repo: "voicemechs", State: "open", Step: "approve", StepOwner: "owner", OwnerKind: protocol.OwnerPerson, StepState: protocol.StepStateReady, Waiting: 3 * time.Hour},
	}
}

func deliveryModel(src deliverySource, w, h int) Model {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.snap = fakeSnapshot()
	m.snap.Agents = append(m.snap.Agents, protocol.AgentView{SessionID: "s_wbg", Profile: protocol.Profile{Name: "wbg-procurement"}, Online: false})
	m.haveData = true
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.width, m.height = w, h
	m = m.withDeliverySource(src)
	return m
}

// open switches to the Delivery tab and runs the load command.
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

// openChangeDetail selects a loaded change and opens its evidence.
func openChangeDetail(t *testing.T, m *Model, id string) tea.Cmd {
	t.Helper()
	for i, ch := range m.delivery.changes {
		if ch.ID != id {
			continue
		}
		m.delivery.detail = true
		m.delivery.scroll = 0
		m.delivery.row = i
		m.delivery.detailLoading = true
		return m.deliveryDetailCmd(id)
	}
	return nil
}
func TestChangeDetailIsTheMainColumn(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 120, 30))
	openChangeDetail(t, &m, "c_1")
	detail := deliveryText(m, 120, 30)
	if !strings.Contains(detail, "Show which provider each quota row belongs to") {
		t.Errorf("detail does not show the title:\n%s", detail)
	}
	if !strings.Contains(detail, "w_1bcfc4c4a93b2687") {
		t.Errorf("detail does not show the branch:\n%s", detail)
	}
	if !strings.Contains(detail, "a accept") || !strings.Contains(detail, "T test after merge") {
		t.Errorf("detail lost the evidence legend:\n%s", detail)
	}
}

func TestDeliverySaysWhatItWaitsFor(t *testing.T) {
	m := deliveryModel(nil, 120, 30)
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
	if status, _ := m.deliveryStatusText(other); !strings.Contains(status, "ci-runner") || !strings.Contains(status, "waiting for") {
		t.Errorf("caller session says %q", status)
	}
	running := deliveryChange{Step: "test", StepOwner: "merger", OwnerKind: "node", StepState: "running"}
	if status, _ := m.deliveryStatusText(running); status != "merger is testing" {
		t.Errorf("claimed step says %q", status)
	}
	claimed := deliveryChange{Step: "verify", StepOwner: "merger", StepState: "ready", ClaimedBy: "s_0000000000000004"}
	if status, _ := m.deliveryStatusText(claimed); status != "ci-runner (online) to verifying" {
		t.Errorf("ready claimed status %q", status)
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
	if !m.qualityDetail || m.workersLog {
		t.Fatal("Quality did not open")
	}
}

func TestChangeDetailAndEscCloses(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 120, 30))
	cmd := openChangeDetail(t, &m, "c_2")
	if cmd == nil || !m.delivery.detail {
		t.Fatal("the thread link did not open the detail")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	got := deliveryText(m, 120, 30)
	for _, want := range []string{"Say what a delivery row waits for", "delivery-pipeline", "0b77d4e91a22", "test", "caller", "waiting for ci-runner", "findings_open=0", "codex gpt-6.1-sol", "10-29 13:45"} {
		if !strings.Contains(got, want) {
			t.Errorf("detail missing %q: %s", want, got)
		}
	}
	m, _ = m.deliveryKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.delivery.detail {
		t.Fatal("esc should only close the detail")
	}
}

func TestDeliveryDetailUsesTheDisplayZone(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 120, 30))
	m.loc = time.FixedZone("CET", 3600)
	openChangeDetail(t, &m, "c_1")
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
	openChangeDetail(t, &m, "c_3")
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
	openChangeDetail(t, &m, c.ID)
	for _, l := range m.deliveryView(80, 24) {
		if strings.Contains(ansi.Strip(l), "\x1b") || strings.Contains(l, "\n") {
			t.Fatalf("unsafe line %q", l)
		}
	}
}
func TestDeliveryHelpAndHints(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 120, 30))
	openChangeDetail(t, &m, "c_1")
	hints := ""
	for _, h := range m.deliveryHints() {
		hints += h.key + " " + h.label + "|"
	}
	for _, want := range []string{"scroll", "back"} {
		if !strings.Contains(hints, want) {
			t.Errorf("hints %q missing %q", hints, want)
		}
	}
	if got := strings.Join(m.helpSectionContent(m.helpContextSection(contextDelivery), 120), "\n"); !strings.Contains(got, "On a change's evidence") {
		t.Errorf("help has no section for a change detail: %s", got)
	}
}
func TestDeliveryGoldens(t *testing.T) {
	scenes := deliveryScenes()
	type scene struct {
		name  string
		rowID string
	}
	for _, s := range []scene{
		{"delivery_detail", "c_1"},
		{"delivery_detail_failed", "c_3"},
	} {
		for _, size := range [][2]int{{80, 24}, {120, 24}, {120, 34}} {
			m := openDelivery(t, deliveryModel(&fakeDelivery{changes: scenes}, size[0], size[1]))
			openChangeDetail(t, &m, s.rowID)
			golden(t, fmt.Sprintf("%s_%dx%d", s.name, size[0], size[1]), deliveryText(m, size[0], size[1]))
		}
	}
}
