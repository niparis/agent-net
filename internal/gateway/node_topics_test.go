package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A health report is a column of the nodes row, and an inventory report is its
// own table. The Projects and Usage topics read only the node columns they show,
// so neither report refreshes them. The nodes topic shows health, so a health
// report refreshes it. An inventory write must not move the nodes table, or every
// topic that lists nodes would refresh on each scan.
func TestNodeHealthAndInventoryMoveOnlyTheTopicsThatShowThem(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("topic-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s}
	before := g.topicVersions()

	if err := s.SetNodeHealth(node, &protocol.MachineHealth{Status: "reported", RestartTime: "2026-11-20T08:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	afterHealth := g.topicVersions()
	if afterHealth[protocol.TopicNodes] == before[protocol.TopicNodes] {
		t.Fatal("a health report did not move the nodes topic")
	}
	for _, topic := range []string{protocol.TopicProjects, protocol.TopicUsage} {
		if afterHealth[topic] != before[topic] {
			t.Fatalf("a health report moved the %s topic", topic)
		}
	}
}

// The Projects, Usage and Nodes topics name the nodes columns their reads use.
// Cached reads have their own guard (TestCachedReadsDeclareEveryTableTheyRead);
// these reads sit outside it. Only the nodes table is checked here: the Usage
// and Nodes topics also read other tables (messages, model_successes) whose keys
// were not part of the node fold.
func TestNodeTopicsNameTheNodeColumnsTheyRead(t *testing.T) {
	s := richReadStore(t)
	g := &Gateway{store: s}
	reads := map[string]func(){
		protocol.TopicProjects: func() {
			if _, err := s.Projects(""); err != nil {
				t.Fatal(err)
			}
		},
		protocol.TopicUsage: func() {
			if _, err := s.Nodes(); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SessionCounts(24 * time.Hour); err != nil {
				t.Fatal(err)
			}
		},
		protocol.TopicNodes: func() {
			if _, err := g.NodesInfo(); err != nil {
				t.Fatal(err)
			}
		},
	}
	for topic, run := range reads {
		s.rc = readCache{}
		seen, cols, unknown := observed(t, s, run)
		if len(unknown) > 0 {
			t.Errorf("%s: the connector could not tell the tables of: %.100s", topic, unknown[0])
		}
		if len(seen) == 0 {
			t.Fatalf("%s: no statement was seen", topic)
		}
		var missing []string
		for _, m := range undeclared(topicTables[topic], seen, cols) {
			if m == "nodes" || strings.HasPrefix(m, "nodes.") {
				missing = append(missing, m)
			}
		}
		if len(missing) > 0 {
			t.Errorf("the %s topic reads %v, which its keys do not name", topic, missing)
		}
	}
}
