package tui

import (
	"context"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

type versionRecordingSource struct {
	fakeSource
	opts SnapshotOptions
}

func (s *versionRecordingSource) SnapshotWith(_ context.Context, opts SnapshotOptions) (Snapshot, error) {
	s.opts = opts
	return opts.Previous, nil
}

// TestFullRefreshSendsKnownVersions checks that a safety refresh tells the
// gateway which versions the screen already holds, so unchanged parts come
// back empty, and that the new versions are remembered for the next one.
func TestFullRefreshSendsKnownVersions(t *testing.T) {
	prev := fakeSnapshot()
	src := &versionRecordingSource{fakeSource: fakeSource{snap: prev}}
	m := New(src)
	m.now = func() time.Time { return t0 }
	m.snap, m.haveData = prev, true
	m.fetching = false
	m.topicActive = true
	m.topicLastFull = t0
	m.topicView = m.topicViewKey()
	m.topicDirty = map[string]bool{}
	m.topicVersions = map[string]uint64{
		protocol.TopicAgents: 4, protocol.TopicNodes: 5, protocol.TopicLearned: 6, protocol.TopicChanges: 7,
	}
	m.dataVersions = map[string]uint64{
		protocol.TopicAgents: 3, protocol.TopicNodes: 4, protocol.TopicLearned: 5, protocol.TopicChanges: 6,
	}
	msg, ok := m.fetchCmd()().(fetchedMsg)
	if !ok {
		t.Fatal("refresh did not answer a fetchedMsg")
	}
	if !src.opts.Lite || !src.opts.Previous.Connected {
		t.Fatalf("options %+v, want lite and the previous snapshot", src.opts)
	}
	for topic, want := range m.dataVersions {
		if src.opts.IfVersions[topic] != want {
			t.Fatalf("if_version for %s is %d, want %d", topic, src.opts.IfVersions[topic], want)
		}
		if msg.fetchedVersions[topic] != m.topicVersions[topic] {
			t.Fatalf("fetched version for %s is %d, want %d", topic, msg.fetchedVersions[topic], m.topicVersions[topic])
		}
	}
	next, _ := m.Update(msg)
	m = next.(Model)
	for topic, want := range m.topicVersions {
		if m.dataVersions[topic] != want {
			t.Fatalf("after the fetch %s is at %d, want %d", topic, m.dataVersions[topic], want)
		}
	}
}

// TestNewSubscriptionClearsReadVersions keeps old process versions from making
// the next read claim data it does not have.
func TestNewSubscriptionClearsReadVersions(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.dataVersions = map[string]uint64{protocol.TopicAgents: 9}
	next, _ := m.Update(topicSubscribedMsg{sub: &operator.TopicSubscription{}})
	m = next.(Model)
	if len(m.dataVersions) != 0 {
		t.Fatalf("versions kept across a new subscription: %+v", m.dataVersions)
	}
}

// TestDeliveryUnchangedKeepsTheList checks the other list the TUI reads every
// half minute.
func TestDeliveryUnchangedKeepsTheList(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.delivery.changes = deliveryScenes()
	m.delivery.loaded, m.delivery.loading = true, true
	m.delivery.dataVersion = 5
	m, _ = m.deliveryLoaded(deliveryLoadedMsg{notModified: true, version: 6})
	if len(m.delivery.changes) != len(deliveryScenes()) {
		t.Fatalf("unchanged load cleared the list: %d rows", len(m.delivery.changes))
	}
	if m.delivery.loading || !m.delivery.loaded || m.delivery.dataVersion != 6 {
		t.Fatalf("unchanged load state: %+v", m.delivery)
	}
}
