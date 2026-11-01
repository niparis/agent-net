package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func publicGraphFixture() Graph {
	return Graph{
		Nodes: []Node{
			{
				Name: "shop",
				Kind: "component",
				Charter: Charter{
					Purpose:         "Sells things",
					Maturity:        "mature - stable",
					Accepts:         "bug fixes; new product pages",
					Refuses:         "payment changes",
					FeatureRequests: "ask the owner first",
					Consumers:       "web",
					Escalate:        "pricing changes",
					Needs:           "billing",
					Notes:           "deploys on Fridays",
				},
				Settings: map[string]string{SettingMergeAuthority: "OWNER_ONLY_NODE_SETTING_CANARY"},
			},
			{Name: "billing", Kind: "component", Charter: Charter{Purpose: "Takes money", Maturity: "growing"}},
			{Name: "architect", Kind: "architect", Charter: Charter{Purpose: "Owns the design", Maturity: "early"}},
		},
		Edges: []Edge{
			{From: "shop", To: "billing", Kind: "depends", Rule: "ask"},
			{From: "architect", To: "shop", Kind: "governs", Mode: "owner", Rule: "normal"},
		},
	}
}

func TestPublicGraphReturnsEveryCharterAndEdge(t *testing.T) {
	page := PublicGraph(publicGraphFixture(), PublicGraphQuery{})
	if page.Total != 3 || len(page.Nodes) != 3 {
		t.Fatalf("nodes: total=%d page=%+v", page.Total, page.Nodes)
	}
	byName := map[string]PublicGraphNode{}
	for _, n := range page.Nodes {
		byName[n.Name] = n
		if !strings.Contains(n.Line, n.Name) {
			t.Errorf("%s line %q does not name the node", n.Name, n.Line)
		}
	}
	shop := byName["shop"]
	if shop.Maturity != "mature - stable" || !strings.Contains(shop.Line, "Sells things") {
		t.Fatalf("shop list line: %+v", shop)
	}
	if len(page.Edges) != 2 || page.EdgeTotal != 2 {
		t.Fatalf("edges: %+v", page.Edges)
	}
	var depends PublicGraphEdge
	for _, e := range page.Edges {
		if e.From == "shop" && e.To == "billing" {
			depends = e
		}
	}
	if depends.Kind != "depends" || depends.Rule != "ask" {
		t.Fatalf("depends edge %+v", depends)
	}
}

func TestPublicGraphDetailReturnsTheFullCharter(t *testing.T) {
	page := PublicGraph(publicGraphFixture(), PublicGraphQuery{Node: "shop"})
	if page.Total != 1 || len(page.Nodes) != 1 {
		t.Fatalf("detail nodes: %+v", page.Nodes)
	}
	n := page.Nodes[0]
	if n.Charter == nil {
		t.Fatal("detail has no charter")
	}
	if n.Charter.Purpose != "Sells things" || n.Charter.Accepts != "bug fixes; new product pages" ||
		n.Charter.Refuses != "payment changes" || n.Charter.FeatureRequests != "ask the owner first" ||
		n.Charter.Consumers != "web" || n.Charter.Escalate != "pricing changes" ||
		n.Charter.Needs != "billing" || n.Charter.Notes != "deploys on Fridays" {
		t.Fatalf("full charter: %+v", n.Charter)
	}
	if len(page.Edges) != 2 {
		t.Fatalf("edges touching shop: %+v", page.Edges)
	}
	found := false
	for _, e := range page.Edges {
		found = found || (e.From == "shop" && e.To == "billing" && e.Rule == "ask")
	}
	if !found {
		t.Fatalf("depends edge missing: %+v", page.Edges)
	}
	if empty := PublicGraph(publicGraphFixture(), PublicGraphQuery{Node: "missing"}); empty.Total != 0 || len(empty.Nodes) != 0 {
		t.Fatalf("unknown node: %+v", empty)
	}
}

// A setting is owner-only even when old or hand-written data holds a value the
// validator would now refuse. The public read must not carry it at all.
func TestPublicGraphNeverReturnsNodeSettings(t *testing.T) {
	marker := "OWNER_ONLY_NODE_SETTING_CANARY"
	for _, q := range []PublicGraphQuery{{}, {Node: "shop"}} {
		raw, err := json.Marshal(PublicGraph(publicGraphFixture(), q))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if strings.Contains(text, marker) || strings.Contains(text, "settings") || strings.Contains(text, SettingMergeAuthority) {
			t.Fatalf("query %+v leaked an owner-only setting: %s", q, text)
		}
	}
}

func TestPublicGraphPagesWithTotals(t *testing.T) {
	var g Graph
	for i := 0; i < 45; i++ {
		g.Nodes = append(g.Nodes, Node{Name: fmt.Sprintf("n%02d", i), Kind: "component"})
	}
	first := PublicGraph(g, PublicGraphQuery{})
	if len(first.Nodes) != PublicGraphDefaultLimit || first.Total != 45 || first.Next != PublicGraphDefaultLimit {
		t.Fatalf("first page: %d nodes, total %d, next %d", len(first.Nodes), first.Total, first.Next)
	}
	second := PublicGraph(g, PublicGraphQuery{Offset: 20, Limit: 10})
	if len(second.Nodes) != 10 || second.Nodes[0].Name != "n20" || second.Next != 30 || second.Total != 45 {
		t.Fatalf("second page: %+v", second)
	}
	last := PublicGraph(g, PublicGraphQuery{Offset: 40})
	if len(last.Nodes) != 5 || last.Nodes[0].Name != "n40" || last.Next != 0 {
		t.Fatalf("last page: %+v", last)
	}
	var many Graph
	for i := 0; i < 60; i++ {
		many.Nodes = append(many.Nodes, Node{Name: fmt.Sprintf("m%02d", i)})
	}
	if capped := PublicGraph(many, PublicGraphQuery{Limit: 1000}); len(capped.Nodes) != PublicGraphMaxLimit {
		t.Fatalf("limit not capped: %d", len(capped.Nodes))
	}
	if over := PublicGraph(g, PublicGraphQuery{Offset: 99}); len(over.Nodes) != 0 || over.Next != 0 || over.Total != 45 {
		t.Fatalf("offset past the end: %+v", over)
	}
}
