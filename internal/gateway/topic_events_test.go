package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestTopicVersionsFollowCommittedTables(t *testing.T) {
	s, _ := newTestStore(t)
	before := s.topicVersions()
	if len(before) != len(protocol.Topics) {
		t.Fatalf("initial versions: %v", before)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('topic-test','one',1)`); err != nil {
		t.Fatal(err)
	}
	during := s.topicVersions()
	if during[protocol.TopicAgents] != before[protocol.TopicAgents] {
		t.Fatal("version moved before commit")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if s.topicVersions()[protocol.TopicAgents] != before[protocol.TopicAgents] {
		t.Fatal("version moved on rollback")
	}
	if _, err = s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('topic-test','two',1)`); err != nil {
		t.Fatal(err)
	}
	after := s.topicVersions()
	if after[protocol.TopicAgents] == before[protocol.TopicAgents] {
		t.Fatal("agents version did not move")
	}
	if after[protocol.TopicLearned] != before[protocol.TopicLearned] {
		t.Fatal("unrelated learned version moved")
	}
}

func TestUsageTopicAndQuotaVersion(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	before := g.topicVersions()
	g.usage.set("node", time.Now(), protocol.UsageReport{})
	first := g.topicVersions()
	if first[protocol.TopicUsage] <= before[protocol.TopicUsage] {
		t.Fatal("usage report did not move usage")
	}
	g.usage.set("node", time.Now(), protocol.UsageReport{})
	again := g.topicVersions()
	if again[protocol.TopicUsage] <= first[protocol.TopicUsage] {
		t.Fatal("second usage report did not move usage")
	}
	if again[protocol.TopicNodes] != first[protocol.TopicNodes] {
		t.Fatal("unchanged quotas moved nodes")
	}
	g.usage.set("node", time.Now(), protocol.UsageReport{Quotas: &protocol.Quotas{}})
	if g.topicVersions()[protocol.TopicNodes] <= again[protocol.TopicNodes] {
		t.Fatal("changed quotas did not move nodes")
	}
}
