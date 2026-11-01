package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// graphFixtureNode makes the project node visible and gives it a charter.
func graphFixtureNode(t *testing.T, n *node, cli *localClient, project, purpose, maturity string) {
	t.Helper()
	helloPi(t, n, "graph-"+project, "/w/"+project, true)
	cli.mustCall("charter_set", map[string]any{"node": project, "attr": "purpose", "value": purpose})
	cli.mustCall("charter_set", map[string]any{"node": project, "attr": "maturity", "value": maturity})
}

// injectNodeSetting writes an owner-only setting the way an older or broken
// writer might, so the canary value cannot pass the validator.
func injectNodeSetting(t *testing.T, gatewayDir, node, setting, value string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(gatewayDir, "gateway.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO graph_node_settings(node,setting,value,updated_at,updated_by) VALUES(?,?,?,?,?)
		ON CONFLICT(node,setting) DO UPDATE SET value=excluded.value`, node, setting, value, time.Now().UnixNano(), "owner")
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentGraphReadsEveryCharterAndEdge(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi"})
	cli := cliClient(t, n)
	graphFixtureNode(t, n, cli, "shop", "Sells things", "mature - stable")
	graphFixtureNode(t, n, cli, "billing", "Takes money", "growing")
	cli.mustCall("node_add", map[string]any{"name": "architect"})
	cli.mustCall("charter_set", map[string]any{"node": "architect", "attr": "purpose", "value": "Owns the design"})
	for _, e := range []map[string]any{
		{"from": "shop", "to": "billing", "kind": "depends", "rule": "ask"},
		{"from": "architect", "to": "shop", "kind": "governs", "mode": "owner", "rule": "normal"},
	} {
		cli.mustCall("edge_set", e)
	}

	cli.mustCall("node_settings_set", map[string]any{"node": "shop", "setting": protocol.SettingDeployRule, "value": protocol.DeployOwner})
	cli.mustCall("node_settings_set", map[string]any{"node": "shop", "setting": protocol.SettingWorkerSandbox, "value": protocol.WorkersNoNetwork})
	cli.mustCall("node_settings_set", map[string]any{"node": "shop", "setting": protocol.SettingJudgeConsumers, "value": protocol.JudgeStrict})

	agent, _ := helloPi(t, n, "graph-reader", "/w/reader", true)
	var page protocol.PublicGraphPage
	if err := json.Unmarshal(agent.mustCall("agent_graph", map[string]any{}), &page); err != nil {
		t.Fatal(err)
	}
	byName := map[string]protocol.PublicGraphNode{}
	for _, node := range page.Nodes {
		byName[node.Name] = node
	}
	for _, name := range []string{"shop", "billing", "architect"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("node %s missing from %+v", name, page.Nodes)
		}
	}
	if byName["shop"].Maturity != "mature - stable" || !strings.Contains(byName["shop"].Line, "Sells things") {
		t.Fatalf("shop line: %+v", byName["shop"])
	}
	rules := map[string]string{}
	for _, r := range byName["shop"].Rules {
		rules[r.Name] = r.Value
	}
	for name, want := range map[string]string{
		protocol.SettingDeployRule:     protocol.DeployOwner,
		protocol.SettingWorkerSandbox:  protocol.WorkersNoNetwork,
		protocol.SettingJudgeConsumers: protocol.JudgeStrict,
		protocol.SettingMergeAuthority: protocol.MergeDelegated,
	} {
		if rules[name] != want {
			t.Errorf("shop rule %s = %q, want %q (all: %+v)", name, rules[name], want, byName["shop"].Rules)
		}
	}
	if len(page.Edges) < 2 || page.EdgeTotal < 2 {
		t.Fatalf("edges: %+v (total %d)", page.Edges, page.EdgeTotal)
	}
	found := map[string]protocol.PublicGraphEdge{}
	for _, e := range page.Edges {
		found[e.From+"->"+e.To] = e
	}
	if e := found["shop->billing"]; e.Kind != "depends" || e.Rule != "ask" {
		t.Fatalf("depends edge: %+v", e)
	}
	if e := found["architect->shop"]; e.Kind != "governs" || e.Mode != "owner" {
		t.Fatalf("governs edge: %+v", e)
	}

	var detail protocol.PublicGraphPage
	if err := json.Unmarshal(agent.mustCall("agent_graph", map[string]any{"node": "shop"}), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Nodes) != 1 || detail.Nodes[0].Charter == nil {
		t.Fatalf("detail: %+v", detail)
	}
	if detail.Nodes[0].Charter.Purpose != "Sells things" || detail.Nodes[0].Charter.Maturity != "mature - stable" {
		t.Fatalf("detail charter: %+v", detail.Nodes[0].Charter)
	}
	detailRules := map[string]string{}
	for _, r := range detail.Nodes[0].Rules {
		detailRules[r.Name] = r.Value
	}
	if detailRules[protocol.SettingDeployRule] != protocol.DeployOwner || detailRules[protocol.SettingWorkerSandbox] != protocol.WorkersNoNetwork {
		t.Fatalf("detail rules: %+v", detail.Nodes[0].Rules)
	}
	if len(detail.Edges) != 2 {
		t.Fatalf("edges touching shop: %+v", detail.Edges)
	}
}

func TestAgentGraphPagesWithTotals(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi"})
	cli := cliClient(t, n)
	for i := 0; i < 3; i++ {
		graphFixtureNode(t, n, cli, fmt.Sprintf("p%d", i), fmt.Sprintf("Project %d", i), "growing")
	}
	agent, _ := helloPi(t, n, "pager", "/w/pager", true)
	var first protocol.PublicGraphPage
	if err := json.Unmarshal(agent.mustCall("agent_graph", map[string]any{"limit": 1}), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Nodes) != 1 || first.Total < 3 || first.Next != 1 {
		t.Fatalf("first page: %+v", first)
	}
	var second protocol.PublicGraphPage
	if err := json.Unmarshal(agent.mustCall("agent_graph", map[string]any{"offset": 1, "limit": 1}), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Nodes) != 1 || second.Next != 2 || second.Total != first.Total {
		t.Fatalf("second page: %+v", second)
	}
	if second.Nodes[0].Name == first.Nodes[0].Name {
		t.Fatalf("pages repeat %s", first.Nodes[0].Name)
	}
}

// A secret stored under a setting name that is not a rule is never returned.
// The rule settings are returned, with name and value.
func TestAgentGraphAndCTOViewHideSecretsButShowRules(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	if err := w.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	graphFixtureNode(t, w.node, w.cli, "shop", "Sells things", "mature - stable")
	graphFixtureNode(t, w.node, w.cli, "billing", "Takes money", "growing")
	w.cli.mustCall("edge_set", map[string]any{"from": "shop", "to": "billing", "kind": "depends", "rule": "ask"})
	w.cli.mustCall("node_settings_set", map[string]any{"node": "shop", "setting": protocol.SettingDeployRule, "value": protocol.DeployOwner})
	w.cli.mustCall("node_settings_set", map[string]any{"node": "shop", "setting": protocol.SettingWorkerSandbox, "value": protocol.WorkersNoNetwork})
	w.cli.mustCall("node_settings_set", map[string]any{"node": "shop", "setting": protocol.SettingMergeAuthority, "value": protocol.MergeAsk})
	marker := "OWNER_ONLY_NODE_SETTING_CANARY"
	injectNodeSetting(t, g.dir, "shop", "unknown_setting", marker)

	agent, _ := helloPi(t, w.node, "canary-reader", "/w/canary", true)
	for _, args := range []map[string]any{{}, {"node": "shop"}} {
		raw := string(agent.mustCall("agent_graph", args))
		if strings.Contains(raw, marker) || strings.Contains(raw, "unknown_setting") || strings.Contains(raw, "locations") {
			t.Fatalf("agent read leaked a secret or path: %s", raw)
		}
		if !strings.Contains(raw, "Sells things") {
			t.Fatalf("agent read has no charter: %s", raw)
		}
		for _, want := range []string{protocol.SettingDeployRule, protocol.DeployOwner, protocol.SettingWorkerSandbox, protocol.WorkersNoNetwork, protocol.SettingMergeAuthority, protocol.MergeAsk} {
			if !strings.Contains(raw, want) {
				t.Errorf("agent read hides rule %q: %s", want, raw)
			}
		}
	}

	if ctoToolClasses["graph"] != ctoRead {
		t.Fatalf("CTO graph class = %q", ctoToolClasses["graph"])
	}
	cto, err := w.d.runCTOToolRaw(context.Background(), "graph", json.RawMessage(`{"node":"shop"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cto)
	if strings.Contains(string(raw), marker) || strings.Contains(string(raw), "unknown_setting") || strings.Contains(string(raw), "locations") {
		t.Fatalf("CTO graph view leaked a secret or path: %s", raw)
	}
	if !strings.Contains(string(raw), "Sells things") || !strings.Contains(string(raw), "billing") {
		t.Fatalf("CTO graph view lacks charters or edges: %s", raw)
	}
	for _, want := range []string{protocol.SettingDeployRule, protocol.DeployOwner, protocol.SettingWorkerSandbox, protocol.WorkersNoNetwork} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("CTO graph view hides rule %q: %s", want, raw)
		}
	}

	// The CTO view is the same public read, not a copy with its own rules.
	var agentDetail protocol.PublicGraphPage
	if err := json.Unmarshal(agent.mustCall("agent_graph", map[string]any{"node": "shop"}), &agentDetail); err != nil {
		t.Fatal(err)
	}
	var ctoDetail struct {
		Node  string                     `json:"node"`
		Items []protocol.PublicGraphNode `json:"items"`
		Edges []protocol.PublicGraphEdge `json:"edges"`
		Total int                        `json:"total"`
	}
	rawDetail, _ := json.Marshal(cto)
	if err := json.Unmarshal(rawDetail, &ctoDetail); err != nil {
		t.Fatal(err)
	}
	if ctoDetail.Node != "shop" || ctoDetail.Total != 1 || len(ctoDetail.Items) != 1 {
		t.Fatalf("CTO graph detail shape: %s", rawDetail)
	}
	if ctoDetail.Items[0].Charter == nil || agentDetail.Nodes[0].Charter == nil ||
		*ctoDetail.Items[0].Charter != *agentDetail.Nodes[0].Charter {
		t.Fatalf("CTO charter differs from the agent read: %s vs %+v", rawDetail, agentDetail.Nodes)
	}
	if len(ctoDetail.Items[0].Rules) != len(agentDetail.Nodes[0].Rules) {
		t.Fatalf("CTO rules differ from the agent read: %s vs %+v", rawDetail, agentDetail.Nodes[0].Rules)
	}
	for i := range ctoDetail.Items[0].Rules {
		if ctoDetail.Items[0].Rules[i] != agentDetail.Nodes[0].Rules[i] {
			t.Fatalf("CTO rules differ from the agent read: %s vs %+v", rawDetail, agentDetail.Nodes[0].Rules)
		}
	}
	if len(ctoDetail.Edges) != len(agentDetail.Edges) || len(ctoDetail.Edges) == 0 {
		t.Fatalf("CTO edges differ from the agent read: %s vs %+v", rawDetail, agentDetail.Edges)
	}
}

// The delegation policy the owner set for a project is a rule, so the public
// graph shows it read-only, per machine.
func TestAgentGraphShowsDelegationPolicy(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi"})
	cli := cliClient(t, n)
	cli.mustCall("node_add", map[string]any{"name": "shop"})
	cli.mustCall("charter_set", map[string]any{"node": "shop", "attr": "maturity", "value": "mature"})
	sid, err := g.gw.Store().UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "delegation-session",
		Repo: protocol.RepoInfo{Project: "shop"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.gw.Store().SetDelegation(sid, protocol.ScopeProject, protocol.DelegationPolicy{
		Delegate: protocol.DelegateWorkers, CoderModel: "fake/m1", Workers: protocol.WorkersReadOnly}); err != nil {
		t.Fatal(err)
	}
	agent, _ := helloPi(t, n, "delegation-reader", "/w/reader", true)
	var detail protocol.PublicGraphPage
	if err := json.Unmarshal(agent.mustCall("agent_graph", map[string]any{"node": "shop"}), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Nodes) != 1 || len(detail.Nodes[0].Delegation) != 1 {
		t.Fatalf("delegation: %+v", detail.Nodes)
	}
	d := detail.Nodes[0].Delegation[0]
	if d.Machine != n.cfg.NodeID || d.Delegate != protocol.DelegateWorkers || d.Workers != protocol.WorkersReadOnly {
		t.Fatalf("delegation policy: %+v", d)
	}
}

// agent_get shows a session's node rules, so a session can check its own rules.
func TestAgentGetShowsSessionNodeRules(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi"})
	cli := cliClient(t, n)
	cli.mustCall("node_add", map[string]any{"name": "shop"})
	cli.mustCall("charter_set", map[string]any{"node": "shop", "attr": "maturity", "value": "mature - stable"})
	cli.mustCall("node_settings_set", map[string]any{"node": "shop", "setting": protocol.SettingDeployRule, "value": protocol.DeployOwner})
	cli.mustCall("node_settings_set", map[string]any{"node": "shop", "setting": protocol.SettingWorkerSandbox, "value": protocol.WorkersNoNetwork})
	cli.mustCall("node_settings_set", map[string]any{"node": "shop", "setting": protocol.SettingJudgeConsumers, "value": protocol.JudgeStrict})

	sid, err := g.gw.Store().UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "rules-session",
		Repo: protocol.RepoInfo{Project: "shop"}})
	if err != nil {
		t.Fatal(err)
	}
	reader, _ := helloPi(t, n, "rules-reader", "/w/reader", true)
	var out struct {
		Agent protocol.AgentView `json:"agent"`
	}
	if err := json.Unmarshal(reader.mustCall("get", map[string]any{"session_id": sid}), &out); err != nil {
		t.Fatal(err)
	}
	rules := map[string]string{}
	for _, r := range out.Agent.Charter.Rules {
		rules[r.Name] = r.Value
	}
	for name, want := range map[string]string{
		protocol.SettingDeployRule:       protocol.DeployOwner,
		protocol.SettingWorkerSandbox:    protocol.WorkersNoNetwork,
		protocol.SettingJudgeConsumers:   protocol.JudgeStrict,
		protocol.SettingMergeAuthority:   protocol.MergeDelegated,
		protocol.SettingTestsBeforeMerge: protocol.TestsSuiteNewTests,
	} {
		if rules[name] != want {
			t.Errorf("agent_get rule %s = %q, want %q (all: %+v)", name, rules[name], want, out.Agent.Charter.Rules)
		}
	}
}
