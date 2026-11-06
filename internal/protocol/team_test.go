package protocol

import "testing"

func TestTeamRefAndPublicRead(t *testing.T) {
	ref := GraphRef{Kind: "team", ID: "alpha"}
	if err := ref.Validate(); err != nil {
		t.Fatal(err)
	}
	active := false
	g := Graph{Nodes: []Node{{Ref: &ref, Name: "Alpha", Kind: "team", Active: &active, Charter: Charter{Purpose: "Shared work"}}}}
	page := PublicGraph(g, PublicGraphQuery{Ref: &ref})
	if len(page.Nodes) != 1 || len(page.Nodes[0].Rules) != 0 || page.Nodes[0].Active == nil || *page.Nodes[0].Active {
		t.Fatalf("team read: %+v", page)
	}
}
