package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGraphTypedRefRoundTrip(t *testing.T) {
	raw := `{"from":{"kind":"agent","id":"same"},"to":{"kind":"repo","id":"same"},"kind":"reviews","rule":"ask"}`
	var e Edge
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatal(err)
	}
	if e.From != "same" || e.To != "same" || e.FromRef == nil || e.FromRef.Kind != "agent" || e.ToRef.Kind != "repo" {
		t.Fatalf("lost refs: %+v", e)
	}
	b, err := json.Marshal(e)
	if err != nil || !strings.Contains(string(b), `"from":{"kind":"agent","id":"same"}`) {
		t.Fatalf("wire: %s %v", b, err)
	}
	var legacy Edge
	if err := json.Unmarshal([]byte(`{"from":"same","to":"other","kind":"depends"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(legacy)
	if !strings.Contains(string(b), `"from":"same"`) {
		t.Fatalf("legacy wire: %s", b)
	}
}

func TestPublicGraphTypedPagingAndFilter(t *testing.T) {
	repo, agent := GraphRef{Kind: "repo", ID: "same"}, GraphRef{Kind: "agent", ID: "same"}
	g := Graph{Nodes: []Node{{Name: "Display", Kind: "repo", Ref: &repo}, {Name: "Display", Kind: "agent", Ref: &agent}}, Edges: []Edge{{From: "same", To: "same", Kind: "reviews", FromRef: &agent, ToRef: &repo}}}
	a := PublicGraph(g, PublicGraphQuery{Limit: 1})
	b := PublicGraph(g, PublicGraphQuery{Limit: 1, Offset: a.Next})
	if a.Total != 2 || a.Nodes[0].Ref == nil || b.Nodes[0].Ref == nil || *a.Nodes[0].Ref == *b.Nodes[0].Ref {
		t.Fatalf("conflated pages: %+v %+v", a, b)
	}
	detail := PublicGraph(g, PublicGraphQuery{Ref: &agent})
	if detail.Total != 1 || detail.Nodes[0].Kind != "agent" || len(detail.Edges) != 1 {
		t.Fatalf("detail: %+v", detail)
	}
}
