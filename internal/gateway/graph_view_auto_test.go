package gateway

import (
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
)

func TestGraphViewAutoTypedCutover(t *testing.T) {
	s, _ := newTestStore(t)
	graphSessions(t, s, "shop")
	g := &Gateway{store: s}
	for _, on := range []bool{false, true} {
		s.graphRefsOn.Store(on)
		out, err := g.handleGraphView([]byte(`{"auto_typed":true}`))
		if err != nil {
			t.Fatal(err)
		}
		v := out.(protocol.GraphView)
		if len(v.Nodes) == 0 {
			t.Fatal("no nodes")
		}
		for _, n := range v.Nodes {
			if (n.Ref != nil) != on {
				t.Fatalf("cutover %v: %+v", on, n)
			}
		}
	}
}

func TestGraphViewAutoTypedWithTeam(t *testing.T) {
	s := teamStore(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha"})
	repo := protocol.GraphRef{Kind: "repo", ID: "demo"}
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &repo})
	g := &Gateway{store: s}
	out, err := g.handleGraphView([]byte(`{"auto_typed":true}`))
	if err != nil {
		t.Fatal(err)
	}
	v := out.(protocol.GraphView)
	found := false
	for _, n := range v.Nodes {
		if n.Ref != nil && n.Ref.Kind == "team" && n.Ref.ID == "alpha" {
			found = true
		}
	}
	if !found || len(v.Edges) == 0 {
		t.Fatalf("missing team or membership: %+v", v)
	}
}
