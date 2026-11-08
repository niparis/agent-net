package gateway

import (
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
)

func TestCTOGraphClass(t *testing.T) {
	s, _, _, _ := learnedProjects(t)
	for _, c := range []struct{ tool, args, want string }{
		{"charter_set", `{"node":"app","attr":"purpose","value":"Build useful things"}`, "minor"},
		{"charter_set", `{"node":"app","attr":"accepts","value":"feature work"}`, "minor"},
		{"charter_set", `{"node":"app","attr":"rules","value":"Useful context"}`, "minor"},
		{"charter_set", `{"node":"app","attr":"feature_requests","value":"Ask the project"}`, "major"},
		{"charter_set", `{"node":"app","attr":"rules","value":"help"}`, "minor"},
		{"charter_set", `{"node":"app","attr":"rules","value":"clients"}`, "minor"},
		{"charter_set", `{"node":"app","attr":"rules","value":"Only local work"}`, "major"},
		{"charter_set", `{"node":"app","attr":"rules","value":""}`, "major"},
		{"charter_set", `{"node":"app","attr":"refuses","value":"deploys"}`, "major"},
		{"charter_set", `{"node":"app","attr":"escalate","value":"owner"}`, "major"},
		{"charter_set", `{"node":"app","attr":"maturity","value":"mature"}`, "major"},
		{"node_add", `{"name":"advisor"}`, "minor"},
		{"node_remove", `{"name":"ops"}`, "major"},
		{"node_settings_set", `{"node":"app","setting":"requests","value":"auto"}`, "major"},
		{"node_settings_set", `{"node":"app","setting":"autonomy","value":"wake"}`, "rule"},
		{"edge_set", `{"from":"app","to":"ops","kind":"depends"}`, "minor"},
		{"edge_remove", `{"from":"app","to":"ops","kind":"depends"}`, "minor"},
		{"edge_set", `{"from":"app","to":"ops","kind":"governs","mode":"owner"}`, "major"},
		{"edge_set", `{"from":"app","to":"ops","kind":"deploys"}`, "rule"},
	} {
		t.Run(c.tool+c.args, func(t *testing.T) {
			got, err := s.CTOGraphClass(c.tool, json.RawMessage(c.args))
			if err != nil || got != c.want {
				t.Fatalf("class=%s err=%v want=%s", got, err, c.want)
			}
		})
	}
	if err := s.seedLegacyCharter("ops", "maturity", "critical - production", "owner"); err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{`{"from":"app","to":"ops","kind":"depends"}`, `{"from":"ops","to":"app","kind":"depends"}`} {
		if got, err := s.CTOGraphClass("edge_set", json.RawMessage(args)); err != nil || got != "major" {
			t.Fatalf("critical edge: %s %v", got, err)
		}
	}
	if got, err := s.CTOGraphClass("charter_set", json.RawMessage(`{"node":"ops","attr":"purpose","value":"help"}`)); err != nil || got != "major" {
		t.Fatalf("critical: %s %v", got, err)
	}
	// The transaction enforces classification even if the daemon preflight was stale.
	if err := s.SetCharter("ops", "purpose", "help", "CTO (minor)"); err == nil {
		t.Fatal("minor actor changed critical node")
	}
	if err := s.SetCharter("app", "refuses", "work", "CTO (minor)"); err == nil {
		t.Fatal("minor actor changed refuses")
	}
	if err := s.SetCharter("app", "purpose", "help", "CTO (minor)"); err != nil {
		t.Fatal(err)
	}
	h, err := s.CTOGraphHistory(0)
	if err != nil || len(h) != 1 {
		t.Fatalf("history: %+v %v", h, err)
	}
	if got, err := s.CTOGraphClass("graph_undo", json.RawMessage(`{"id":0}`)); err != nil || got != "major" {
		t.Fatalf("undo class %s %v", got, err)
	}
	if err := s.GraphUndo(h[0].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	g, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		if n.Name == "app" && n.Charter.Purpose != "" {
			t.Fatal("undo failed")
		}
	}
}

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
