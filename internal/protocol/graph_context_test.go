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
				Escalate: "Breaking API changes: ask the owner.",
				Rules:    "Deploys go through homelab.",
			}},
			{Name: "tenebrae", Kind: "component", Charter: Charter{Maturity: "early - speed over safety."}},
			{Name: "wbg-architect", Kind: "architect", Charter: Charter{Purpose: "Look after WBG."}},
			{Name: "homelab", Kind: "component", Charter: Charter{Maturity: "growing - deploys matter."}},
		},
		Edges: []Edge{
			{From: "tenebrae", To: "cogforge", Kind: "depends", Rule: "normal"},
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
		"Breaking API changes: ask the owner.",
		"You depend on homelab.",
		"tenebrae depends on your node",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("context lacks %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "rules: Deploys go through homelab.") {
		t.Fatalf("missing house rules: %s", got)
	}
	if !strings.Contains(got, "Decide, do not defer") {
		t.Errorf("context lacks the decide rule:\n%s", got)
	}
	if strings.Contains(got, "\u2014") {
		t.Errorf("em dash:\n%s", got)
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

func TestDeliveryAssignmentsAppearInNodeContext(t *testing.T) {
	g := Graph{Nodes: []Node{{Name: "demo", Kind: "component"}, {Name: "merger", Kind: "architect"}},
		Edges: []Edge{{From: "merger", To: "demo", Kind: "reviews"}, {From: "merger", To: "demo", Kind: "merges"}}}
	for name, wants := range map[string][]string{
		"merger": {"You own review for demo.", "You own merge for demo."},
		"demo":   {"merger owns review for your project.", "merger owns merge for your project."},
	} {
		text := NodeContext(g, name)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s context lacks %q: %s", name, want, text)
			}
		}
	}
}

func TestDeliveryAssignmentContextNamesPhase(t *testing.T) {
	g := Graph{Nodes: []Node{{Name: "demo"}, {Name: "runner"}}, Edges: []Edge{{From: "runner", To: "demo", Kind: "tests_after_merge"}}}
	if text := NodeContext(g, "runner"); !strings.Contains(text, "test after merge") {
		t.Fatalf("missing phase in assignment: %q", text)
	}
}

func TestDeliveryAssignedDeployerGetsTargetApprovalRule(t *testing.T) {
	g := Graph{Nodes: []Node{{Name: "deployer", Kind: "architect"}, {Name: "app", Kind: "component", Charter: Charter{Maturity: "critical"}}},
		Edges: []Edge{{From: "deployer", To: "app", Kind: "delivery_deploys"}}}
	for _, text := range []string{NodeContext(g, "deployer"), WorkerDeployApprovalRules(g, "deployer")} {
		if !strings.Contains(text, "app") || !strings.Contains(text, "agent_owner_ask") {
			t.Fatalf("assigned deployer does not get target's approval rule: %q", text)
		}
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
		"escalate: Breaking API changes: ask the owner.",
		"rules: Deploys go through homelab.",
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
