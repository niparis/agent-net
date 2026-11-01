package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoGraphFixture is a store with two projects and a registered CTO session.
func ctoGraphFixture(t *testing.T) (*Store, map[string]string, string, string) {
	t.Helper()
	s, _ := newTestStore(t)
	ids, node := graphSessions(t, s, "cogforge", "tenebrae")
	cto, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	return s, ids, node, cto
}

func TestGraphViewHasTheFixedCTONode(t *testing.T) {
	s, _, _, _ := ctoGraphFixture(t)
	view, err := s.GraphView("", protocol.GraphHistoryPerNode)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range view.Nodes {
		if n.Name == protocol.CTONodeName {
			found = true
			if n.Kind != "cto" {
				t.Fatalf("CTO kind %q, want cto", n.Kind)
			}
			if n.Charter != (protocol.Charter{}) {
				t.Fatalf("the CTO node has a charter: %+v", n.Charter)
			}
			if n.SessionCount != 1 || len(n.Sessions) != 1 {
				t.Fatalf("the CTO session is not on its node: %+v", n)
			}
		}
	}
	if !found {
		t.Fatalf("no CTO node in %+v", view.Nodes)
	}
}

func TestGraphEdgeToCTOCarriesItsRequestRule(t *testing.T) {
	s, ids, _, cto := ctoGraphFixture(t)
	if err := s.SetEdge(protocol.Edge{From: "cogforge", To: protocol.CTONodeName, Kind: "depends", Rule: "ask"}, "owner"); err != nil {
		t.Fatal(err)
	}
	rule, err := s.RequestRule(ids["cogforge"], cto)
	if err != nil || rule != "ask" {
		t.Fatalf("request rule from cogforge to the CTO = %q, %v", rule, err)
	}
	if rule, err := s.RequestRule(ids["tenebrae"], cto); err != nil || rule != "normal" {
		t.Fatalf("request rule from an unconfigured project = %q, %v", rule, err)
	}
	view, err := s.GraphView("cogforge", protocol.GraphHistoryPerNode)
	if err != nil {
		t.Fatal(err)
	}
	var edge *protocol.Edge
	for i := range view.Edges {
		if view.Edges[i].To == protocol.CTONodeName {
			edge = &view.Edges[i]
		}
	}
	if edge == nil || edge.Rule != "ask" {
		t.Fatalf("graph view lacks the rule edge: %+v", view.Edges)
	}
}

func TestFeedEventCarriesOwnerApprovalOfACTOSend(t *testing.T) {
	s, ids, _, cto := ctoGraphFixture(t)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: cto, Recipient: ids["cogforge"], Type: protocol.TypeRequest,
		Body: "the owner's words", FromCTO: true, OwnerApproved: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: cto, Recipient: ids["tenebrae"], Type: protocol.TypeRequest,
		Body: "a peer request"}); err != nil {
		t.Fatal(err)
	}
	events, err := s.FeedEvents(0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var approved, plain bool
	for _, e := range events {
		if e.Kind != "msg.stored" {
			continue
		}
		switch e.To {
		case ids["cogforge"]:
			approved = e.FromCTO && e.OwnerApproved
			if e.Voice {
				t.Fatal("a CTO message is voice even without owner approval")
			}
		case ids["tenebrae"]:
			plain = !e.OwnerApproved && !e.Voice
		}
	}
	if !approved || !plain {
		t.Fatalf("owner approval not carried: approved=%v plain=%v events=%+v", approved, plain, events)
	}
}

func TestGraphTalkCountsTheCTONodeAndMarksOnlyApprovedSendsAsVoice(t *testing.T) {
	s, ids, _, cto := ctoGraphFixture(t)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: cto, Recipient: ids["cogforge"], Type: protocol.TypeRequest,
		Body: "approved", FromCTO: true, OwnerApproved: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: cto, Recipient: ids["tenebrae"], Type: protocol.TypeRequest,
		Body: "plain"}); err != nil {
		t.Fatal(err)
	}
	counts, err := s.GraphTalkCounts()
	if err != nil {
		t.Fatal(err)
	}
	voice := map[string]bool{}
	for _, c := range counts {
		if c.From == protocol.CTONodeName || c.To == protocol.CTONodeName {
			other := c.From
			if other == protocol.CTONodeName {
				other = c.To
			}
			voice[other] = c.Voice
		}
	}
	if !voice["cogforge"] {
		t.Fatalf("approved CTO send is not voice: %+v", counts)
	}
	if voice["tenebrae"] {
		t.Fatalf("plain CTO talk is voice: %+v", counts)
	}
}
