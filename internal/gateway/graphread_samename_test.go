package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func sameNameStore(t *testing.T) *Store {
	t.Helper()
	s, _ := newTestStore(t)
	graphSessions(t, s, "demo", "solo")
	if _, err := s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRefCharter(protocol.GraphRef{Kind: "repo", ID: "demo"}, "purpose", "Repo purpose", "owner", false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRefCharter(protocol.GraphRef{Kind: "agent", ID: "demo"}, "purpose", "Agent purpose", "owner", false); err != nil {
		t.Fatal(err)
	}
	return s
}

// An untyped read by a name that a repo node and an agent node share must not
// hide the agent node: it returns both, the repo node first, each with its kind
// and ref and its own charter.
func TestGraphReadSameNameRepoAndAgentReturnsBoth(t *testing.T) {
	s := sameNameStore(t)
	g := &Gateway{store: s}
	out, err := g.handleGraphRead([]byte(`{"project":"demo","also":true}`))
	if err != nil {
		t.Fatal(err)
	}
	page := protocol.PublicGraph(out.(protocol.GraphGot).Graph, protocol.PublicGraphQuery{Node: "demo"})
	if len(page.Nodes) != 2 {
		t.Fatalf("want repo and agent, got %+v", page.Nodes)
	}
	repo, agent := page.Nodes[0], page.Nodes[1]
	if repo.Kind != "component" || repo.Charter.Purpose != "Repo purpose" {
		t.Fatalf("repo node first and unchanged: %+v", repo)
	}
	if agent.Kind != "agent" || agent.Ref == nil || agent.Ref.ID != "demo" || agent.Charter.Purpose != "Agent purpose" {
		t.Fatalf("agent node: %+v", agent)
	}
	if !strings.Contains(page.Note, "2 nodes are named") {
		t.Fatalf("note: %q", page.Note)
	}
}

// Without also (the per-turn charter read) the answer is unchanged.
func TestGraphReadWithoutAlsoStaysOneNode(t *testing.T) {
	s := sameNameStore(t)
	g := &Gateway{store: s}
	out, err := g.handleGraphRead([]byte(`{"project":"demo"}`))
	if err != nil {
		t.Fatal(err)
	}
	page := protocol.PublicGraph(out.(protocol.GraphGot).Graph, protocol.PublicGraphQuery{Node: "demo"})
	if len(page.Nodes) != 1 || page.Note != "" {
		t.Fatalf("%+v", page)
	}
}

// A name that only one node has reads as before.
func TestGraphReadUniqueNameIsUnchanged(t *testing.T) {
	s := sameNameStore(t)
	for _, q := range []string{`UPDATE repos SET owner_agent_id=NULL WHERE id='solo'`, `UPDATE agents SET tombstone=1 WHERE id='solo'`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	g := &Gateway{store: s}
	out, err := g.handleGraphRead([]byte(`{"project":"solo","also":true}`))
	if err != nil {
		t.Fatal(err)
	}
	page := protocol.PublicGraph(out.(protocol.GraphGot).Graph, protocol.PublicGraphQuery{Node: "solo"})
	if len(page.Nodes) != 1 || page.Nodes[0].Kind != "component" || page.Note != "" {
		t.Fatalf("%+v", page)
	}
}
