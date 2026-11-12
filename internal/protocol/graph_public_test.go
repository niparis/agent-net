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
				Settings: map[string]string{
					SettingTestsBeforeMerge: TestsSuiteNewTests,
					SettingMergeAuthority:   MergeDelegated,
					SettingWorkerSandbox:    WorkersNoNetwork,
					SettingJudgeConsumers:   JudgeStrict,
					SettingDeployRule:       DeployOwner,
					SettingAutonomy:         AutonomyWake,
					"unknown_setting":       "OWNER_ONLY_NODE_SETTING_CANARY",
				},
				Locations: []GraphLocation{{Machine: "m1", Path: "/home/owner/PRIVATE_PATH_CANARY"}},
				Delegation: []NodeDelegation{{
					Machine:          "m1",
					DelegationPolicy: DelegationPolicy{Delegate: DelegateWorkers, CoderModel: "deepseek/deepseek-v4.1-flash", Workers: WorkersReadOnly},
				}},
			},
			{Name: "billing", Kind: "component", Charter: Charter{Purpose: "Takes money", Maturity: "growing"}},
			{Name: "architect", Kind: "architect", Charter: Charter{Purpose: "Owns the design", Maturity: "early"}},
		},
		Edges: []Edge{
			{From: "shop", To: "billing", Kind: "depends", Rule: "ask"},
			{From: "architect", To: "shop", Kind: "deploys", Rule: "normal"},
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

// Every rule an agent must follow is public, with name and value: the six node
// settings, resolved against the node's maturity, plus the delegation policy the
// owner set for the node's project on a machine.
func TestPublicGraphReturnsNodeRules(t *testing.T) {
	want := map[string]string{
		SettingTestsBeforeMerge: TestsSuiteNewTests,
		SettingMergeAuthority:   MergeDelegated,
		SettingWorkerSandbox:    WorkersNoNetwork,
		SettingJudgeConsumers:   JudgeStrict,
		SettingDeployRule:       DeployOwner,
		SettingAutonomy:         AutonomyWake,
	}
	for _, q := range []PublicGraphQuery{{}, {Node: "shop"}} {
		page := PublicGraph(publicGraphFixture(), q)
		if len(page.Nodes) == 0 {
			t.Fatalf("query %+v returned no nodes", q)
		}
		var shop PublicGraphNode
		for _, n := range page.Nodes {
			if n.Name == "shop" {
				shop = n
			}
		}
		if len(shop.Rules) != len(want) {
			t.Fatalf("query %+v rules: %+v", q, shop.Rules)
		}
		got := map[string]string{}
		for _, r := range shop.Rules {
			got[r.Name] = r.Value
		}
		for name, value := range want {
			if got[name] != value {
				t.Errorf("query %+v rule %s = %q, want %q (all: %+v)", q, name, got[name], value, shop.Rules)
			}
		}
		if len(shop.Delegation) != 1 || shop.Delegation[0].Machine != "m1" ||
			shop.Delegation[0].Delegate != DelegateWorkers || shop.Delegation[0].Workers != WorkersReadOnly {
			t.Fatalf("query %+v delegation: %+v", q, shop.Delegation)
		}
	}
}

// A secret, an unknown setting or a machine path is not a rule. The public
// read must not carry it, even when old or hand-written data holds it.
func TestPublicGraphNeverReturnsSecretsOrPaths(t *testing.T) {
	for _, q := range []PublicGraphQuery{{}, {Node: "shop"}} {
		raw, err := json.Marshal(PublicGraph(publicGraphFixture(), q))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, hidden := range []string{"OWNER_ONLY_NODE_SETTING_CANARY", "PRIVATE_PATH_CANARY", "unknown_setting", "locations"} {
			if strings.Contains(text, hidden) {
				t.Fatalf("query %+v leaked %q: %s", q, hidden, text)
			}
		}
		if !strings.Contains(text, SettingDeployRule) || !strings.Contains(text, DeployOwner) {
			t.Fatalf("query %+v hid the deploy rule: %s", q, text)
		}
	}
}

// A rule value the validator refuses is not a rule. The node gets its maturity
// default instead, and the refused value never reaches an agent.
func TestPublicGraphNeverReturnsAnInvalidRuleValue(t *testing.T) {
	marker := "INVALID_RULE_VALUE_CANARY"
	g := publicGraphFixture()
	g.Nodes[0].Settings[SettingWorkerSandbox] = marker
	page := PublicGraph(g, PublicGraphQuery{Node: "shop"})
	if len(page.Nodes) != 1 {
		t.Fatalf("detail: %+v", page.Nodes)
	}
	for _, r := range page.Nodes[0].Rules {
		if r.Value == marker {
			t.Fatalf("invalid rule value leaked: %+v", page.Nodes[0].Rules)
		}
		if r.Name == SettingWorkerSandbox && r.Value != WorkersSandboxed {
			t.Fatalf("worker_sandbox = %q, want the maturity default %q", r.Value, WorkersSandboxed)
		}
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), marker) {
		t.Fatalf("invalid rule value leaked: %s", raw)
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

// The public read is what the CTO and every agent read, so the lead level must
// reach it, and only when the Agent has one.
func TestPublicGraphCarriesLeadLevel(t *testing.T) {
	g := Graph{Nodes: []Node{
		{Name: "lead-agent", Kind: "agent", LeadLevel: LeadLevelGrant},
		{Name: "plain-agent", Kind: "agent"},
	}}
	page := PublicGraph(g, PublicGraphQuery{})
	raw, err := json.Marshal(page.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"lead_level":"grant"`) {
		t.Fatalf("lead level missing from the public read: %s", raw)
	}
	if strings.Count(string(raw), "lead_level") != 1 {
		t.Fatalf("lead level shown on a node without one: %s", raw)
	}
}
