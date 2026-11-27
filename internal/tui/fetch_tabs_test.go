package tui

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// fetchCountSource counts the calls the one-second refresh makes, so a test can
// say what each tab asks for.
type fetchCountSource struct {
	*fakeSource
	mu         sync.Mutex // a refresh asks its reads together
	calls      map[string]int
	onboarding protocol.Onboarding
}

func (f *fetchCountSource) Call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	f.countCall(method)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch method {
	case "onboarding_get":
		if f.onboarding.Steps == nil {
			f.onboarding.Steps = map[string]string{}
		}
		return json.Marshal(f.onboarding)
	case protocol.OpFrictionFind:
		return json.Marshal(protocol.FrictionFindResult{})
	}
	return nil, nil
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[method]++
}

func (f *fetchCountSource) resetCalls() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = map[string]int{}
}

func (f *fetchCountSource) count(method string) int { return f.calls[method] }

func refreshTimes(m Model, n int) Model {
	for i := 0; i < n; i++ {
		m = send(m, m.fetchCmd()())
	}
	return m
}

// The chief's status is asked for often only while the CTO box has focus. The
// first refresh seeds the change marker. The CTO has no stored proposals, so
// no refresh asks for them on any tab.
func TestFetchChiefStatusOnlyWhereNeeded(t *testing.T) {
	tests := []struct {
		name          string
		tab           int
		focus         focus
		workersLog    bool
		wantPerMinute int
	}{
		{"Sessions", sessionsTab, focusSessions, false, 6},
		{"Graph", graphTab, focusSessions, false, 6},
		{"Messages", messagesTab, focusFeed, false, 6},
		{"CTO pane focused", sessionsTab, focusCTO, false, 60},
		{"Quotas workers log", quotasTab, focusSessions, true, 6},
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
			if got := src.count(protocol.OpChiefGet); got != tc.wantPerMinute {
				t.Errorf("%d chief_get calls per minute, want %d", got, tc.wantPerMinute)
			}
			if got := src.count("proposal_list"); got != 0 {
				t.Errorf("%d proposal_list calls per minute, want none", got)
			}
			t.Logf("%s per minute: Snapshot=%d worker_log=%d Events=%d hidden_get=%d onboarding_get=%d usage=%d counts=%d chief_get=%d tools_differences=%d",
				tc.name, src.count("Snapshot"), src.count("worker_log"), src.count("Events"), src.count("hidden_get"), src.count("onboarding_get"),
				src.count("usage"), src.count("counts"), src.count(protocol.OpChiefGet), src.count("tools_differences"))
		})
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

// Delivery changes feed the Sessions task column. Other tabs do not ask for
// them.
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
	m.tab = sessionsTab
	if _, cmd := m.deliveryTick(); cmd == nil {
		t.Fatal("Sessions did not ask for delivery changes")
	}
}

// The friction section is read only on the Work tab, and at most once a minute.
func TestFrictionReadOnlyOnTheWorkTab(t *testing.T) {
	src := &fetchCountSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
	clock := t0
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return clock }
	m.tab = sessionsTab
	m = refresh(m, src)
	if got := src.count(protocol.OpFrictionFind); got != 0 {
		t.Fatalf("the Sessions tab read friction %d times", got)
	}
	m.tab = quotasTab
	m = refresh(m, src)
	if got := src.count(protocol.OpFrictionFind); got != 1 {
		t.Fatalf("the Work tab read friction %d times", got)
	}
	m = refresh(m, src)
	if got := src.count(protocol.OpFrictionFind); got != 1 {
		t.Fatalf("a second refresh inside the minute read friction %d times", got)
	}
	clock = t0.Add(frictionRefreshEvery)
	m = refresh(m, src)
	if got := src.count(protocol.OpFrictionFind); got != 2 {
		t.Fatalf("the read did not refresh after a minute: %d", got)
	}
}
