package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestGraphOwnerRelay(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi", interval: time.Hour}, nil)
	agent, _ := helloPi(t, n, "graph-agent", "/w/graph", true)
	cli := cliClient(t, n)
	for _, method := range []string{"held_list", "held_answer", "graph_get", "charter_set", "charter_history", "node_add", "node_remove", "edge_set", "edge_remove", "graph_undo", "edge_history", "node_settings_get", "node_settings_set"} {
		if _, err := agent.call(method, map[string]any{}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
			t.Fatalf("%s gate: %v", method, err)
		}
	}
	cli.mustCall("node_add", map[string]any{"name": "architect"})
	cli.mustCall("charter_set", map[string]any{"node": "architect", "attr": "purpose", "value": "design"})
	var graph protocol.Graph
	if err := json.Unmarshal(cli.mustCall("graph_get", map[string]any{}), &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) == 0 {
		t.Fatal("no nodes")
	}
	found := false
	for _, node := range graph.Nodes {
		if node.Name == "architect" && node.Charter.Purpose == "design" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing charter: %+v", graph)
	}
	cli.mustCall("graph_undo", map[string]any{})
	var history []protocol.CharterChange
	if err := json.Unmarshal(cli.mustCall("charter_history", map[string]any{"node": "architect"}), &history); err != nil || len(history) != 2 {
		t.Fatalf("history: %+v %v", history, err)
	}
	cli.mustCall("node_add", map[string]any{"name": "other"})
	edge := protocol.Edge{From: "architect", To: "other", Kind: "depends"}
	cli.mustCall("edge_set", edge)
	var edges []protocol.EdgeChange
	if err := json.Unmarshal(cli.mustCall("edge_history", map[string]any{"node": "other"}), &edges); err != nil || len(edges) != 1 {
		t.Fatalf("edge history: %+v %v", edges, err)
	}
	cli.mustCall("graph_undo", protocol.GraphUndoRequest{ID: edges[0].ID})
	cli.mustCall("edge_set", edge)
	cli.mustCall("edge_remove", edge)
	cli.mustCall("node_remove", map[string]any{"name": "other"})
	cli.mustCall("node_remove", map[string]any{"name": "architect"})
}

func TestGraphCTOActor(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cli := cliClient(t, n)
	cli.mustCall("node_add", map[string]any{"name": "architect"})
	if _, err := n.d.call(context.Background(), "charter.set", map[string]any{"node": "architect", "attr": "purpose", "value": "design", "by": "cto"}); err != nil {
		t.Fatal(err)
	}
	var history []protocol.CharterChange
	if err := json.Unmarshal(cli.mustCall("charter_history", map[string]string{"node": "architect"}), &history); err != nil || len(history) != 1 || history[0].By != "cto" {
		t.Fatalf("history: %+v %v", history, err)
	}
	cli.mustCall("charter_set", map[string]any{"node": "architect", "attr": "purpose", "value": "owner", "by": "cto"})
	json.Unmarshal(cli.mustCall("charter_history", map[string]string{"node": "architect"}), &history)
	if history[0].By != "owner" {
		t.Fatalf("owner spoofed actor: %+v", history)
	}
}

func TestNodeSettingsOwnerOnly(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	agent, _ := helloPi(t, n, "settings-agent", "/w/shop", true)
	for _, method := range []string{"node_settings_get", "node_settings_set"} {
		if _, err := agent.call(method, map[string]any{}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
			t.Fatalf("%s gate: %v", method, err)
		}
	}
	cli := cliClient(t, n)
	cli.mustCall("charter_set", map[string]any{"node": "shop", "attr": "maturity", "value": "critical"})
	cli.mustCall("node_settings_set", map[string]any{"node": "shop", "setting": protocol.SettingDeployRule, "value": protocol.DeployOwner})
	var got protocol.NodeSettingsResult
	if err := json.Unmarshal(cli.mustCall("node_settings_get", map[string]any{"node": "shop"}), &got); err != nil {
		t.Fatal(err)
	}
	if got.Overrides[protocol.SettingDeployRule] != protocol.DeployOwner || got.Effective.DeployRule != protocol.DeployOwner {
		t.Fatalf("settings: %+v", got)
	}
	cli.mustCall("node_settings_set", map[string]any{"node": "shop", "setting": protocol.SettingDeployRule, "value": ""})
	got = protocol.NodeSettingsResult{}
	json.Unmarshal(cli.mustCall("node_settings_get", map[string]any{"node": "shop"}), &got)
	if len(got.Overrides) != 0 {
		t.Fatalf("override not cleared: %+v", got.Overrides)
	}
}

func TestCTOGatewayCapability(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	raw, err := n.d.call(context.Background(), "cto.capabilities", nil)
	if err != nil {
		t.Fatal(err)
	}
	var caps struct {
		Attribution bool `json:"attribution"`
	}
	if json.Unmarshal(raw, &caps) != nil || !caps.Attribution {
		t.Fatalf("CTO capability: %s", raw)
	}
}

func TestCharterSetReturnsBudgetWarnings(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cli := cliClient(t, n)
	cli.mustCall("node_add", map[string]any{"name": "architect"})
	cli.mustCall("charter_set", map[string]any{"node": "architect", "attr": "needs", "value": strings.Repeat("n", 700)})
	raw := cli.mustCall("charter_set", map[string]any{"node": "architect", "attr": "notes", "value": strings.Repeat("n", 501)})
	if !strings.Contains(string(raw), "the whole charter exceeds 1200 characters") {
		t.Fatalf("missing total warning: %s", raw)
	}
	raw = cli.mustCall("charter_set", map[string]any{"node": "architect", "attr": "notes", "value": ""})
	if strings.Contains(string(raw), "the whole charter exceeds 1200 characters") {
		t.Fatalf("warning after clearing: %s", raw)
	}
}
