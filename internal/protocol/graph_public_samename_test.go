package protocol

import (
	"strings"
	"testing"
)

func TestPublicGraphSameNameReturnsEveryMatchByName(t *testing.T) {
	g := Graph{Nodes: []Node{
		{Name: "cos", Kind: "component", Charter: Charter{Purpose: "repo"}},
		{Name: "cos", Kind: "agent", Ref: &GraphRef{Kind: "agent", ID: "cos"}, Charter: Charter{Purpose: "agent"}},
		{Name: "other", Kind: "component"},
	}}
	page := PublicGraph(g, PublicGraphQuery{Node: "cos"})
	if len(page.Nodes) != 2 || page.Nodes[0].Kind != "component" || page.Nodes[1].Kind != "agent" || page.Total != 2 {
		t.Fatalf("%+v", page)
	}
	if !strings.Contains(page.Note, "2 nodes are named") || !strings.Contains(page.Note, "component, agent") {
		t.Fatalf("note %q", page.Note)
	}
	// A typed read still returns exactly one node and no note.
	page = PublicGraph(g, PublicGraphQuery{Ref: &GraphRef{Kind: "agent", ID: "cos"}})
	if len(page.Nodes) != 1 || page.Nodes[0].Charter.Purpose != "agent" || page.Note != "" {
		t.Fatalf("%+v", page)
	}
	// A unique name has no note.
	page = PublicGraph(g, PublicGraphQuery{Node: "other"})
	if len(page.Nodes) != 1 || page.Note != "" {
		t.Fatalf("%+v", page)
	}
}
