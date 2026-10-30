package protocol

import (
	"strings"
	"testing"
)

func contextGraph() Graph {
	return Graph{
		Nodes: []Node{
			{Name: "cogforge", Kind: "component", Charter: Charter{
				Purpose:  "Generic document and evidence API.",
				Maturity: "mature - careful with breaking changes.",
				Accepts:  "Generic endpoints and bug fixes from any consumer.",
				Refuses:  "Client-specific endpoints for one consumer.",
				Escalate: "Breaking API changes: ask Nicolas.",
				Notes:    "Deploys go through homelab.",
			}},
			{Name: "tenebrae", Kind: "component", Charter: Charter{Maturity: "early - speed over safety."}},
			{Name: "wbg-architect", Kind: "architect", Charter: Charter{Purpose: "Look after WBG."}},
			{Name: "homelab", Kind: "component", Charter: Charter{Maturity: "growing - deploys matter."}},
		},
		Edges: []Edge{
			{From: "tenebrae", To: "cogforge", Kind: "depends", Rule: "normal"},
			{From: "wbg-architect", To: "cogforge", Kind: "governs", Mode: "owner", Rule: "normal"},
			{From: "cogforge", To: "tenebrae", Kind: "governs", Mode: "advisor", Rule: "normal"},
			{From: "cogforge", To: "homelab", Kind: "depends", Rule: "normal"},
		},
	}
}

func TestNodeContextIsShortAndSaysTheEdgesThatMatter(t *testing.T) {
	got := NodeContext(contextGraph(), "cogforge")
	for _, want := range []string{
		"[agent-net]",
		"cogforge",
		"mature - careful with breaking changes.",
		"Generic endpoints and bug fixes from any consumer.",
		"Client-specific endpoints for one consumer.",
		"Breaking API changes: ask Nicolas.",
		"wbg-architect governs your node as owner.",
		"You depend on homelab.",
		"tenebrae depends on your node",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("context lacks %q:\n%s", want, got)
		}
	}
	// Purpose and notes are for the worker brief, not the short per-turn text.
	if strings.Contains(got, "Generic document and evidence API.") || strings.Contains(got, "Deploys go through homelab.") {
		t.Errorf("per-turn context is not short:\n%s", got)
	}
	if !strings.Contains(got, "Decide, do not defer") {
		t.Errorf("context lacks the decide rule:\n%s", got)
	}
	if strings.Contains(got, "\u2014") {
		t.Errorf("em dash:\n%s", got)
	}
}

func TestNodeContextForAnArchitectNamesTheNodesItGoverns(t *testing.T) {
	got := NodeContext(contextGraph(), "wbg-architect")
	if !strings.Contains(got, "You govern") || !strings.Contains(got, "cogforge as owner") {
		t.Errorf("architect context lacks its governs edges:\n%s", got)
	}
}

func TestNodeContextIsEmptyWithoutACharter(t *testing.T) {
	g := contextGraph()
	g.Nodes = append(g.Nodes, Node{Name: "bare", Kind: "component"})
	if got := NodeContext(g, "bare"); got != "" {
		t.Errorf("a node without a charter got context:\n%s", got)
	}
	if got := NodeContext(g, "unknown"); got != "" {
		t.Errorf("an unknown node got context:\n%s", got)
	}
}

func TestCharterBriefHasTheWholeCharter(t *testing.T) {
	node := contextGraph().Nodes[0]
	got := CharterBrief(node)
	for _, want := range []string{
		"Charter of cogforge",
		"purpose: Generic document and evidence API.",
		"maturity: mature - careful with breaking changes.",
		"accepts: Generic endpoints and bug fixes from any consumer.",
		"refuses: Client-specific endpoints for one consumer.",
		"escalate: Breaking API changes: ask Nicolas.",
		"notes: Deploys go through homelab.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("brief lacks %q:\n%s", want, got)
		}
	}
	if got := CharterBrief(Node{Name: "bare", Kind: "component"}); got != "" {
		t.Errorf("a node without a charter got a brief:\n%s", got)
	}
}

func TestRelationText(t *testing.T) {
	g := contextGraph()
	tests := []struct {
		name, from, to, want string
	}{
		{"consumer", "tenebrae", "cogforge", "tenebrae is a consumer of your node cogforge"},
		{"owner architect", "wbg-architect", "cogforge", "wbg-architect governs your node cogforge as owner"},
		{"advisor architect", "cogforge", "tenebrae", "cogforge governs your node tenebrae as advisor"},
		{"none", "name-that-is-not-here", "cogforge", "no graph relation"},
	}
	for _, tc := range tests {
		got := RelationText(g, tc.from, tc.to)
		if !strings.Contains(got, tc.want) || !strings.HasPrefix(got, "[agent-net]") {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	for _, same := range [][2]string{{"cogforge", "cogforge"}, {"", "cogforge"}, {"cogforge", ""}} {
		if got := RelationText(g, same[0], same[1]); got != "" {
			t.Errorf("%q -> %q got %q", same[0], same[1], got)
		}
	}
}
