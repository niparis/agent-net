package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// fetchCountSource counts the calls the one-second refresh makes, so a test can
// say what each tab asks for.
type fetchCountSource struct {
	*fakeSource
	calls      map[string]int
	inbox      []protocol.InboxItem
	proposals  []protocol.CTOProposal
	onboarding protocol.Onboarding
	toolsCall  int
}

func (f *fetchCountSource) Call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[method]++
	switch method {
	case "onboarding_get":
		if f.onboarding.Steps == nil {
			f.onboarding.Steps = map[string]string{}
		}
		return json.Marshal(f.onboarding)
	case "inbox_list":
		return json.Marshal(map[string]any{"items": f.inbox})
	case protocol.OpProposalList:
		return json.Marshal(f.proposals)
	}
	return nil, nil
}

func (f *fetchCountSource) ToolsDifferences(context.Context) (int, error) {
	f.toolsCall++
	f.countCall("tools_differences")
	return 0, nil
}

func (f *fetchCountSource) Snapshot(ctx context.Context) (Snapshot, error) {
	f.countCall("Snapshot")
	f.countCall("worker_log") // a full Snapshot always reads the worker log
	return f.fakeSource.Snapshot(ctx)
}

func (f *fetchCountSource) SnapshotWith(ctx context.Context, opts SnapshotOptions) (Snapshot, error) {
	f.countCall("Snapshot")
	if opts.WorkerLog {
		f.countCall("worker_log")
	}
	return f.fakeSource.Snapshot(ctx)
}

func (f *fetchCountSource) Events(ctx context.Context, after int64, limit int) ([]protocol.FeedEvent, error) {
	f.countCall("Events")
	return f.fakeSource.Events(ctx, after, limit)
}

func (f *fetchCountSource) HiddenItems(ctx context.Context) (protocol.HiddenItems, error) {
	f.countCall("hidden_get")
	return f.fakeSource.HiddenItems(ctx)
}

func (f *fetchCountSource) Usage(ctx context.Context) (Usage, error) {
	f.countCall("usage")
	return f.fakeSource.Usage(ctx)
}

func (f *fetchCountSource) Counts(ctx context.Context) (protocol.SessionCounts, error) {
	f.countCall("counts")
	return f.fakeSource.Counts(ctx)
}

func (f *fetchCountSource) countCall(method string) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[method]++
}

func (f *fetchCountSource) resetCalls() { f.calls = map[string]int{} }

func (f *fetchCountSource) count(method string) int { return f.calls[method] }

func refreshTimes(m Model, n int) Model {
	for i := 0; i < n; i++ {
		m = send(m, m.fetchCmd()())
	}
	return m
}

// The Inbox and the proposals are only asked for on the screen that shows
// them. The first refresh seeds the change marker, then a quiet Sessions tab
// makes no such calls for a whole minute.
func TestFetchAsksForInboxListsOnlyWhereNeeded(t *testing.T) {
	tests := []struct {
		name          string
		tab           int
		focus         focus
		workersLog    bool
		wantPerMinute int
	}{
		{"Sessions", sessionsTab, focusSessions, false, 0},
		{"Graph", graphTab, focusSessions, false, 0},
		{"Messages", messagesTab, focusFeed, false, 0},
		{"Inbox", inboxTab, focusSessions, false, 60},
		{"CTO pane focused", sessionsTab, focusCTO, false, 60},
		{"Quotas workers log", quotasTab, focusSessions, true, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := &fetchCountSource{fakeSource: &fakeSource{snap: fakeSnapshot(), usage: fakeUsage(), counts: fakeCounts()}}
			m := New(src)
			clock := t0
			m.loc, m.now = time.UTC, func() time.Time { return clock }
			m.tab, m.focus, m.workersLog = tc.tab, tc.focus, tc.workersLog
			m = refreshTimes(m, 1) // seed the marker
			src.resetCalls()
			for i := 0; i < 60; i++ {
				clock = clock.Add(time.Second)
				m = send(m, m.fetchCmd()())
			}
			for _, method := range []string{"inbox_list", protocol.OpProposalList} {
				if got := src.count(method); got != tc.wantPerMinute {
					t.Errorf("%s: %d calls per minute, want %d", method, got, tc.wantPerMinute)
				}
			}
			t.Logf("%s per minute: Snapshot=%d worker_log=%d Events=%d hidden_get=%d onboarding_get=%d usage=%d counts=%d inbox_list=%d proposal_list=%d tools_differences=%d",
				tc.name, src.count("Snapshot"), src.count("worker_log"), src.count("Events"), src.count("hidden_get"), src.count("onboarding_get"),
				src.count("usage"), src.count("counts"), src.count("inbox_list"), src.count(protocol.OpProposalList), src.count("tools_differences"))
		})
	}
}

// A changed count in the Snapshot brings the lists back even on Sessions, so
// the Inbox badge and the CTO box stay correct.
func TestFetchPicksUpAChangedInboxMarkerOnAnyTab(t *testing.T) {
	src := &fetchCountSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab, m.focus = sessionsTab, focusSessions
	m = refreshTimes(m, 1)
	if got := m.waitingCount(); got != 0 {
		t.Fatalf("badge starts at %d", got)
	}

	src.proposals = []protocol.CTOProposal{{ID: "p_1", Status: "open", CreatedAt: t0}}
	src.snap.ProposalCount = 1
	src.resetCalls()
	m = refreshTimes(m, 1)

	if src.count(protocol.OpProposalList) != 1 || src.count("inbox_list") != 1 {
		t.Fatalf("lists were not fetched after the count changed: %v", src.calls)
	}
	if got := m.waitingCount(); got != 1 {
		t.Fatalf("badge = %d, want 1", got)
	}
	if line := m.titleLine(160); !strings.Contains(line, "Inbox (1)") {
		t.Fatalf("title %q", line)
	}

	// The next refresh is quiet again.
	src.resetCalls()
	m = refreshTimes(m, 1)
	if src.count(protocol.OpProposalList) != 0 || src.count("inbox_list") != 0 {
		t.Fatalf("lists fetched again without a change: %v", src.calls)
	}
}

// A session that starts waiting is a change in the Snapshot and brings the
// Inbox list back while the owner is on another tab.
func TestFetchPicksUpANewWaitingSession(t *testing.T) {
	snap := fakeSnapshot()
	src := &fetchCountSource{fakeSource: &fakeSource{snap: snap}}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab = sessionsTab
	m = refreshTimes(m, 1)

	src.snap.Agents[0].Online = true
	src.snap.Agents[0].Waiting = &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "may I?", Since: t0.Unix()}
	src.resetCalls()
	m = refreshTimes(m, 1)
	if src.count("inbox_list") != 1 {
		t.Fatalf("inbox not fetched after a wait appeared: %v", src.calls)
	}
}

// Tool differences only feed the last onboarding lesson. Once that lesson is
// done, the screen stops asking.
func TestFetchStopsAskingForToolDifferencesAfterTheLesson(t *testing.T) {
	snap := fakeSnapshot()
	snap.Nodes = append(snap.Nodes, protocol.NodeView{NodeID: "n_other", Hostname: "other", Online: true})
	snap.ProposalCount = 0
	src := &fetchCountSource{fakeSource: &fakeSource{snap: snap}}
	src.onboarding.Steps = map[string]string{"see": "done"}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.onboardingLoaded = true
	m.onboarding.Steps = map[string]string{"see": "done"}
	m = refreshTimes(m, 1)
	if src.toolsCall != 1 {
		t.Fatalf("first refresh asked %d times, want 1", src.toolsCall)
	}
	src.toolsCall = 0
	src.onboarding.Steps["tools"] = "done"
	m.now = func() time.Time { return t0.Add(usageEvery) }
	m = refreshTimes(m, 1)
	if src.toolsCall != 0 {
		t.Fatalf("asked %d times after the lesson was done", src.toolsCall)
	}
}

// The CTO poll runs only while its box is on screen.
func TestTickPollsCTOOnlyWhenItsBoxIsVisible(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.cto.polling = false
	m.tab, m.focus = graphTab, focusSessions
	next, _ := m.update(tickMsg{})
	if next.(Model).cto.polling {
		t.Fatal("the CTO polled while its box was hidden on Graph")
	}

	m = New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.cto.polling = false
	m.tab, m.focus = sessionsTab, focusSessions
	next, _ = m.update(tickMsg{})
	if !next.(Model).cto.polling {
		t.Fatal("the CTO did not poll while its box was on screen")
	}
}

// Delivery changes feed the Sessions task column and the Delivery tab. Other
// tabs do not ask for them.
func TestDeliveryTickOnlyOnShowingScreens(t *testing.T) {
	clock := t0
	m := treeModel(80, 24, mergerFixture())
	m.deliverySrc = &fakeDelivery{}
	m.now = func() time.Time { return clock }
	clock = t0.Add(2 * deliveryRefreshEvery)
	m.tab = graphTab
	if _, cmd := m.deliveryTick(); cmd != nil {
		t.Fatal("Graph asked for delivery changes")
	}
	m.tab = inboxTab
	if _, cmd := m.deliveryTick(); cmd != nil {
		t.Fatal("Inbox asked for delivery changes")
	}
	m.tab = sessionsTab
	if _, cmd := m.deliveryTick(); cmd == nil {
		t.Fatal("Sessions did not ask for delivery changes")
	}
}
