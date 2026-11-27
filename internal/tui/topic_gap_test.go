package tui

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A changed topic is read at once after a quiet time. While topics keep
// changing, the reads are the gap apart, and nothing is lost: the topics stay
// changed until a read gets them.
func TestChangedTopicsAreReadNoSoonerThanTheGap(t *testing.T) {
	start := time.Unix(1000, 0)
	now := start
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.now = func() time.Time { return now }
	m.topicActive = true
	m.topicLastFull = start
	m.topicView = m.topicViewKey()
	m.topicDirty = map[string]bool{protocol.TopicAgents: true}
	if !m.topicRefreshDue() {
		t.Fatal("the first change after a quiet time was not read at once")
	}
	m.topicFetchedAt = start
	reads := 0
	for second := 1; second <= 20; second++ {
		now = start.Add(time.Duration(second) * time.Second)
		if m.topicRefreshDue() {
			reads++
			m.topicFetchedAt = now
			m.topicLastFull = now // a read of every part ends the safety wait too
		}
	}
	// A topic that changes in every one of 20 seconds was read before in every one.
	if reads != 6 {
		t.Fatalf("%d reads in 20 changed seconds, want one in each gap of %v", reads, topicRefreshGap)
	}
	t.Logf("20 simulated seconds of changed topics: reads before=20 after=%d", reads)
	if !m.topicDirty[protocol.TopicAgents] {
		t.Fatal("a deferred read dropped the changed topic")
	}
}

// chiefCaller counts what a refresh asks the daemon for.
type chiefCaller struct {
	*fakeSource
	mu    sync.Mutex
	calls map[string]int
}

func (c *chiefCaller) Call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	c.mu.Lock()
	c.calls[method]++
	c.mu.Unlock()
	return json.RawMessage(`{}`), nil
}

// The chief's status is read when what waits for the owner changes, every 10
// seconds, and when the CTO box is opened, but not when a topic moves: the
// Inbox and agents topics move with every session report.
func TestChiefStatusIsNotReadForEveryTopicChange(t *testing.T) {
	src := &chiefCaller{fakeSource: &fakeSource{snap: fakeSnapshot()}, calls: map[string]int{}}
	now := time.Unix(5000, 0)
	m := New(src)
	m.now = func() time.Time { return now }
	m.topicActive = true
	m.topicLastFull = now
	m.topicView = m.topicViewKey()
	m.chiefSeen = chiefMarkerOf(fakeSnapshot())
	m.chiefAt = now.Add(-time.Second)
	ask := func(dirty ...string) fetchedMsg {
		t.Helper()
		m.topicDirty = map[string]bool{}
		for _, topic := range dirty {
			m.topicDirty[topic] = true
		}
		got, ok := m.fetchCmd()().(fetchedMsg)
		if !ok {
			t.Fatal("no fetchedMsg")
		}
		return got
	}
	if got := ask(protocol.TopicAgents); got.chiefAsked || src.calls[protocol.OpChiefGet] != 0 {
		t.Fatalf("an agents change asked for the chief's status: %+v", src.calls)
	}
	if got := ask(protocol.TopicInbox); got.chiefAsked || src.calls[protocol.OpChiefGet] != 0 {
		t.Fatalf("an Inbox change asked for the chief's status: %+v", src.calls)
	}
	m.chiefAt = now.Add(-chiefRefreshEvery)
	if got := ask(protocol.TopicAgents); !got.chiefAsked {
		t.Fatal("the chief's status was not read after the wait")
	}
	m.chiefAt = now
	m.chiefSeen = "changed"
	if got := ask(protocol.TopicAgents); !got.chiefAsked {
		t.Fatal("a change of what waits for the owner did not read the chief's status")
	}
	m.chiefSeen = chiefMarkerOf(fakeSnapshot())
	m.chiefAt = now
	m.focus = focusCTO
	// The safety read while the CTO box is open reads every part, the
	// chief's status too.
	if got := ask(); !got.chiefAsked {
		t.Fatal("a full read with the CTO box open did not read the chief's status")
	}
}
