package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func ruleValue(rules []protocol.PublicRule, name string) string {
	for _, r := range rules {
		if r.Name == name {
			return r.Value
		}
	}
	return ""
}

func TestAutonomyIsOwnerOnlyAndEveryAgentCanReadIt(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	agent, session := helloPi(t, n, "autonomy-agent", "/w/shop", true)
	if _, err := agent.call("node_settings_set", map[string]any{"node": "shop", "setting": protocol.SettingAutonomy, "value": protocol.AutonomyWake}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Fatalf("agent changed autonomy: %v", err)
	}
	cli := cliClient(t, n)
	cli.mustCall("charter_set", map[string]any{"node": "shop", "attr": "maturity", "value": "growing"})
	cli.mustCall("node_settings_set", map[string]any{"node": "shop", "setting": protocol.SettingAutonomy, "value": protocol.AutonomyWake})
	if _, err := cli.call("node_settings_set", map[string]any{"node": "shop", "setting": protocol.SettingAutonomy, "value": "always"}); err == nil {
		t.Fatal("owner set an invalid autonomy value")
	}

	var settings protocol.NodeSettingsResult
	if err := json.Unmarshal(cli.mustCall("node_settings_get", map[string]any{"node": "shop"}), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Autonomy != protocol.AutonomyWake || settings.Overrides[protocol.SettingAutonomy] != protocol.AutonomyWake {
		t.Fatalf("node settings: %+v", settings)
	}

	var got struct {
		Agent protocol.AgentView `json:"agent"`
	}
	if err := json.Unmarshal(agent.mustCall("get", map[string]any{"session_id": session}), &got); err != nil {
		t.Fatal(err)
	}
	if rule := ruleValue(got.Agent.Charter.Rules, protocol.SettingAutonomy); rule != protocol.AutonomyWake {
		t.Fatalf("agent_get autonomy = %+v", got.Agent.Charter.Rules)
	}

	var page protocol.PublicGraphPage
	if err := json.Unmarshal(agent.mustCall("agent_graph", map[string]any{"node": "shop"}), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Nodes) != 1 {
		t.Fatalf("agent_graph nodes: %+v", page.Nodes)
	}
	if rule := ruleValue(page.Nodes[0].Rules, protocol.SettingAutonomy); rule != protocol.AutonomyWake {
		t.Fatalf("agent_graph autonomy: %+v", page.Nodes[0].Rules)
	}
	if !strings.Contains(page.Nodes[0].Line, "shop") {
		t.Fatalf("agent_graph line: %q", page.Nodes[0].Line)
	}
}

func TestAutonomyCTOChangeIsAProposal(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi"})
	helloPi(t, n, "proposal-shop", "/w/shop", true)
	cli := cliClient(t, n)
	cli.mustCall("charter_set", map[string]any{"node": "shop", "attr": "maturity", "value": "growing"})
	ctx := context.Background()
	value, err := n.d.runCTOToolRaw(ctx, "node_settings_set", json.RawMessage(`{"node":"shop","setting":"autonomy","value":"wake"}`))
	if err != nil {
		t.Fatal(err)
	}
	text, ok := value.(string)
	if !ok || !strings.HasPrefix(text, "proposal p_") {
		t.Fatalf("autonomy change ran immediately: %#v", value)
	}
	var before protocol.NodeSettingsResult
	if err := json.Unmarshal(cli.mustCall("node_settings_get", map[string]any{"node": "shop"}), &before); err != nil {
		t.Fatal(err)
	}
	if before.Autonomy != protocol.AutonomyOff {
		t.Fatalf("autonomy applied before approval: %+v", before)
	}
	items, err := n.d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	if err := n.d.proposalRunner().decide(ctx, items[len(items)-1].ID, true); err != nil {
		t.Fatal(err)
	}
	var after protocol.NodeSettingsResult
	if err := json.Unmarshal(cli.mustCall("node_settings_get", map[string]any{"node": "shop"}), &after); err != nil {
		t.Fatal(err)
	}
	if after.Autonomy != protocol.AutonomyWake {
		t.Fatalf("approved autonomy change not applied: %+v", after)
	}
}
