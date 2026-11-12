package tui

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestTopicRefreshScheduleAndIdleCalls(t *testing.T) {
	start := time.Unix(1000, 0)
	m := New(&fakeSource{snap: fakeSnapshot()})
	now := start
	m.now = func() time.Time { return now }
	m.fetching = false
	m.topicActive = true
	m.topicLastFull = start
	m.topicView = m.topicViewKey()
	m.topicDirty = map[string]bool{}
	oldRefreshes, newRefreshes := 0, 0
	for second := 1; second <= 60; second++ {
		now = start.Add(time.Duration(second) * time.Second)
		oldRefreshes++
		if m.topicRefreshDue() {
			newRefreshes++
			m.topicLastFull = now
		}
	}
	if oldRefreshes != 60 || newRefreshes != 2 {
		t.Fatalf("idle refreshes before=%d after=%d", oldRefreshes, newRefreshes)
	}
	t.Logf("60 simulated seconds, refreshes before=%d after=%d", oldRefreshes, newRefreshes)
	m.topicDirty[protocol.TopicAgents] = true
	if !m.topicRefreshDue() {
		t.Fatal("changed topic did not trigger refresh")
	}
	m.topicActive = false
	m.topicDirty = nil
	if !m.topicRefreshDue() {
		t.Fatal("fallback must poll")
	}
}

func TestTopicEventsKeepNewerChangesDuringFetchAndFallback(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.topicActive = true
	m.topicVersions = map[string]uint64{protocol.TopicAgents: 3}
	m.topicDirty = map[string]bool{protocol.TopicAgents: true}
	m.fetching = true
	next, _ := m.Update(topicsChangedMsg{ok: true, versions: map[string]uint64{protocol.TopicAgents: 4}})
	m = next.(Model)
	if m.topicVersions[protocol.TopicAgents] != 4 || !m.topicDirty[protocol.TopicAgents] {
		t.Fatal("new event lost")
	}
	next, _ = m.Update(fetchedMsg{snap: fakeSnapshot(), at: time.Now(), topicVersions: map[string]uint64{protocol.TopicAgents: 3}})
	m = next.(Model)
	if !m.topicDirty[protocol.TopicAgents] {
		t.Fatal("older fetch cleared a newer event")
	}
	next, _ = m.Update(topicsChangedMsg{ok: false})
	m = next.(Model)
	if m.topicActive || !m.topicRefreshDue() {
		t.Fatal("closed subscription must restore polling")
	}
}

func TestTopicEventRefreshesDeliveryWhenOpen(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m = m.withDeliverySource(&fakeDelivery{})
	m.topicActive = true
	m.topicVersions = map[string]uint64{protocol.TopicChanges: 2}
	// The last load was a while ago: a change on a quiet topic loads at once.
	// A load less than topicRefreshGap ago waits (see
	// TestDeliveryTopicLoadsAreSpacedByTheGap).
	m.delivery.loadedAt = time.Now().Add(-topicRefreshGap)
	m.delivery.topicVersion, m.delivery.topicSeen = 1, true
	m.tab = sessionsTab
	_, cmd := m.deliveryTick()
	if cmd == nil {
		t.Fatal("changed delivery topic did not refresh")
	}
}

func TestTopicGraphChangeDuringLoadRefreshesAgain(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.tab, m.topicActive = graphTab, true
	m.topicView, m.topicLastFull = m.topicViewKey(), m.now()
	m.topicVersions = map[string]uint64{protocol.TopicGraph: 1}
	cmd := m.mapCmd()
	m.topicVersions[protocol.TopicGraph] = 2
	m.applyMap(cmd().(mapMsg))
	m.fetching = true
	next, _ := m.Update(tickMsg{})
	m = next.(Model)
	if !m.mp.loading {
		t.Fatal("graph change during load was lost")
	}
}
