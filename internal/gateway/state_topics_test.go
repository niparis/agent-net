package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// requireMoved fails when a topic that a screen shows did not move.
func requireMoved(t *testing.T, before, after map[string]uint64, topics ...string) {
	t.Helper()
	for _, topic := range topics {
		if after[topic] == before[topic] {
			t.Errorf("the %s topic did not move", topic)
		}
	}
}

// Hidden sessions and nodes are shown on the agents list, the machines and the
// network map, the workers panel and the asks of a hidden session.
func TestHiddenItemsMoveTheScreensThatShowThem(t *testing.T) {
	s, _ := newTestStore(t)
	before := s.topicVersions()
	if _, err := s.SetHiddenItems(protocol.HiddenItems{Sessions: []string{"hidden-session"}, Nodes: []string{"hidden-node"}}); err != nil {
		t.Fatal(err)
	}
	after := s.topicVersions()
	requireMoved(t, before, after, protocol.TopicAgents, protocol.TopicNodes, protocol.TopicWorkers, protocol.TopicGraph, protocol.TopicInbox)
}

// The CTO session is left out of the agents list's repo counts and appears on
// the network map. Moving it to another machine changes only its role row.
func TestCTOSessionMovesTheAgentsAndGraphTopics(t *testing.T) {
	s, _ := newTestStore(t)
	agent, peer := twoSessions(t, s)
	node := mustNode(t, s, agent)
	if _, err := s.RegisterCTOSession(node); err != nil {
		t.Fatal(err)
	}
	other := mustNode(t, s, peer)
	if other == node {
		t.Fatal("fixture needs different nodes")
	}
	before := s.topicVersions()
	if _, err := s.RegisterCTOSession(other); err != nil {
		t.Fatal(err)
	}
	after := s.topicVersions()
	requireMoved(t, before, after, protocol.TopicAgents, protocol.TopicGraph)
}

// The Inbox screen shows the chief and a deliberate clear of it.
func TestChiefClearMovesTheInbox(t *testing.T) {
	s, _, _, _, _ := chiefRig(t)
	before := s.topicVersions()
	if err := s.SetChief(protocol.ChiefSetRequest{Clear: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	after := s.topicVersions()
	requireMoved(t, before, after, protocol.TopicInbox)
}

// The setup overlay and the workers panel show the onboarding progress.
func TestOnboardingMovesTheInboxAndWorkers(t *testing.T) {
	s, _ := newTestStore(t)
	before := s.topicVersions()
	if _, err := s.UpdateOnboarding(protocol.OnboardingUpdate{Step: "see", State: "done"}, false); err != nil {
		t.Fatal(err)
	}
	after := s.topicVersions()
	requireMoved(t, before, after, protocol.TopicInbox, protocol.TopicWorkers)
}
