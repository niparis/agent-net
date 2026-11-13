package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// graphSessions makes one session for each named project, all on one node.
func graphSessions(t testing.TB, s *Store, projects ...string) (map[string]string, string) {
	t.Helper()
	node, _, err := s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, p := range projects {
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n-" + p, Cwd: "/w/" + p,
			Repo: protocol.RepoInfo{Root: "/w/" + p, Project: p}})
		if err != nil {
			t.Fatal(err)
		}
		ids[p] = id
	}
	return ids, node
}

func TestGraphForAProjectHasItsCharterAndTheEdgesThatTouchIt(t *testing.T) {
	s, _ := newTestStore(t)
	graphSessions(t, s, "cogforge", "tenebrae", "other")
	if err := s.AddArchitect("wbg-architect", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("cogforge", "maturity", "mature", "owner"); err != nil {
		t.Fatal(err)
	}
	for _, e := range []protocol.Edge{
		{From: "tenebrae", To: "cogforge", Kind: "depends", Rule: "normal"},
		{From: "wbg-architect", To: "cogforge", Kind: "deploys", Rule: "normal"},
		{From: "other", To: "cogforge", Kind: "depends", Rule: "normal"},
	} {
		if err := s.SetEdge(e, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	g, err := s.GraphFor("cogforge")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, n := range g.Nodes {
		names = append(names, n.Name)
	}
	for _, want := range []string{"cogforge", "tenebrae", "wbg-architect", "other"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Errorf("node %q missing from %v", want, names)
		}
	}
	for _, n := range g.Nodes {
		if n.Name == "cogforge" && n.Charter.Maturity != "mature" {
			t.Errorf("charter: %+v", n.Charter)
		}
	}
	if len(g.Edges) != 3 {
		t.Fatalf("edges: %+v", g.Edges)
	}
	all, err := s.GraphFor("")
	if err != nil || len(all.Nodes) < 4 || len(all.Edges) != 3 {
		t.Fatalf("all: %+v, %v", all, err)
	}
}

func TestRelationIsComputedFromTheGraph(t *testing.T) {
	s, _ := newTestStore(t)
	ids, _ := graphSessions(t, s, "cogforge", "tenebrae", "unrelated")
	if err := s.AddArchitect("wb-architect", "owner"); err != nil {
		t.Fatal(err)
	}
	for _, e := range []protocol.Edge{
		{From: "tenebrae", To: "cogforge", Kind: "depends", Rule: "normal"},
		{From: "wb-architect", To: "cogforge", Kind: "deploys", Rule: "normal"},
	} {
		if err := s.SetEdge(e, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	rel, err := s.RelationFor(ids["tenebrae"], ids["cogforge"])
	if err != nil || !strings.Contains(rel, "tenebrae is a consumer of your node cogforge") {
		t.Fatalf("consumer relation: %q %v", rel, err)
	}
	if rel, _ := s.RelationFor(ids["unrelated"], ids["cogforge"]); !strings.Contains(rel, "no graph relation") {
		t.Fatalf("unrelated: %q", rel)
	}
	if rel, _ := s.RelationFor(ids["cogforge"], ids["cogforge"]); rel != "" {
		t.Fatalf("same node got a relation line: %q", rel)
	}
}

func TestDeliverySetsTheRelationOnlyOnARequest(t *testing.T) {
	s, _ := newTestStore(t)
	ids, _ := graphSessions(t, s, "cogforge", "tenebrae")
	if err := s.SetEdge(protocol.Edge{From: "tenebrae", To: "cogforge", Kind: "depends", Rule: "normal"}, "owner"); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s}
	m := protocol.Message{From: ids["tenebrae"], To: ids["cogforge"], Type: protocol.TypeRequest, Body: "please add it"}
	if got := g.relationForDelivery(m); !strings.Contains(got, "consumer of your node cogforge") {
		t.Fatalf("request relation: %q", got)
	}
	m.Type = protocol.TypeReply
	if got := g.relationForDelivery(m); got != "" {
		t.Fatalf("a reply got a relation: %q", got)
	}
	m.Type = protocol.TypeNotification
	if got := g.relationForDelivery(m); got != "" {
		t.Fatalf("a notification got a relation: %q", got)
	}
}
