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
	m.dataVersionsAt = map[string]time.Time{
		protocol.TopicAgents: t0, protocol.TopicNodes: t0, protocol.TopicLearned: t0, protocol.TopicChanges: t0,
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

// TestGatewayRestartClearsReadVersions covers the case where the daemon finds
// a restarted gateway. Topic versions are in-process counters that start over,
// so an old held value may match a new stamp. A new subscription, or a lost
// one, must drop every held version, including the change list's.
func TestGatewayRestartClearsReadVersions(t *testing.T) {
	seed := func() Model {
		m := New(&fakeSource{snap: fakeSnapshot()})
		m.dataVersions = map[string]uint64{protocol.TopicAgents: 9, protocol.TopicChanges: 9}
		m.dataVersionsAt = map[string]time.Time{protocol.TopicAgents: t0, protocol.TopicChanges: t0}
		m.delivery.dataVersion = 7
		m.delivery.dataAt = t0
		return m
	}
	m := seed()
	next, _ := m.Update(topicSubscribedMsg{sub: &operator.TopicSubscription{}})
	m = next.(Model)
	if len(m.dataVersions) != 0 || len(m.dataVersionsAt) != 0 || m.delivery.dataVersion != 0 || !m.delivery.dataAt.IsZero() {
		t.Fatalf("new subscription kept versions: %+v %+v delivery=%d/%v", m.dataVersions, m.dataVersionsAt, m.delivery.dataVersion, m.delivery.dataAt)
	}
	m = seed()
	next, _ = m.Update(topicsChangedMsg{ok: false})
	m = next.(Model)
	if len(m.dataVersions) != 0 || len(m.dataVersionsAt) != 0 || m.delivery.dataVersion != 0 || !m.delivery.dataAt.IsZero() {
		t.Fatalf("lost subscription kept versions: %+v %+v delivery=%d/%v", m.dataVersions, m.dataVersionsAt, m.delivery.dataVersion, m.delivery.dataAt)
	}
}

type versionedFakeDelivery struct {
	fakeDelivery
	version uint64
}

func (f *versionedFakeDelivery) SetIfVersion(v uint64) { f.version = v }

// TestOldReadsAreNotSentAsVersions makes sure a quiet network cannot keep a
// held version forever. OwnerUses decays and ended workers leave the list after
// RecentWorkerWindow, though no table write moves the topic.
func TestOldReadsAreNotSentAsVersions(t *testing.T) {
	now := time.Unix(1000, 0)
	src := &versionRecordingSource{fakeSource: fakeSource{snap: fakeSnapshot()}}
	m := New(src)
	m.now = func() time.Time { return now }
	m.snap = fakeSnapshot()
	m.topicActive = true
	m.topicLastFull = now
	m.topicView = m.topicViewKey()
	m.topicDirty = map[string]bool{}
	m.topicVersions = map[string]uint64{
		protocol.TopicAgents: 4, protocol.TopicNodes: 5, protocol.TopicLearned: 6, protocol.TopicChanges: 7,
	}
	m.dataVersions = map[string]uint64{
		protocol.TopicAgents: 3, protocol.TopicNodes: 4, protocol.TopicLearned: 5, protocol.TopicChanges: 6,
	}
	old := now.Add(-protocol.RecentWorkerWindow - time.Second)
	m.dataVersionsAt = map[string]time.Time{
		protocol.TopicAgents: old, protocol.TopicNodes: old, protocol.TopicLearned: old, protocol.TopicChanges: old,
	}
	_ = m.fetchCmd()()
	if len(src.opts.IfVersions) != 0 {
		t.Fatalf("old reads were sent as versions: %+v", src.opts.IfVersions)
	}
	recent := now.Add(-protocol.RecentWorkerWindow / 2)
	m.dataVersionsAt = map[string]time.Time{
		protocol.TopicAgents: recent, protocol.TopicNodes: recent, protocol.TopicLearned: recent, protocol.TopicChanges: recent,
	}
	_ = m.fetchCmd()()
	if len(src.opts.IfVersions) != 4 {
		t.Fatalf("young reads were not sent as versions: %+v", src.opts.IfVersions)
	}

	delivery := &versionedFakeDelivery{}
	m.deliverySrc = delivery
	m.delivery.dataVersion = 5
	m.delivery.dataAt = old
	m.delivery.loading = false
	if _, cmd := m.startDeliveryLoad(); cmd == nil || delivery.version != 0 {
		t.Fatalf("old change list was sent as version %d", delivery.version)
	}
	m.delivery.loading = false
	m.delivery.dataAt = recent
	if _, cmd := m.startDeliveryLoad(); cmd == nil || delivery.version != 5 {
		t.Fatalf("young change list was sent as version %d", delivery.version)
	}
}
