package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// agent_get returns the session's node rules: the five settings resolved
// against the node's maturity. The list view stays compact and does not carry
// them.
func TestAgentGetShowsItsNodeRules(t *testing.T) {
	s, _ := newTestStore(t)
	_, id := sessionWithDescription(t, s, "a shop")
	if err := s.SetCharter("p", "maturity", "mature", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeSetting("p", protocol.SettingDeployRule, protocol.DeployOwner, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeSetting("p", protocol.SettingWorkerSandbox, protocol.WorkersNoNetwork, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeSetting("p", protocol.SettingJudgeConsumers, protocol.JudgeStrict, "owner"); err != nil {
		t.Fatal(err)
	}
	a := agentOf(t, s, id)
	got := map[string]string{}
	for _, r := range a.Charter.Rules {
		got[r.Name] = r.Value
	}
	for name, want := range map[string]string{
		protocol.SettingDeployRule:       protocol.DeployOwner,
		protocol.SettingWorkerSandbox:    protocol.WorkersNoNetwork,
		protocol.SettingJudgeConsumers:   protocol.JudgeStrict,
		protocol.SettingMergeAuthority:   protocol.MergeDelegated,
		protocol.SettingTestsBeforeMerge: protocol.TestsSuiteNewTests,
	} {
		if got[name] != want {
			t.Errorf("agent_get rule %s = %q, want %q (all: %+v)", name, got[name], want, a.Charter.Rules)
		}
	}
	list, err := s.Agents(protocol.QueryAgents{})
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	if len(list[0].Charter.Rules) != 0 {
		t.Fatalf("agent list carries node rules: %+v", list[0].Charter.Rules)
	}
}

// A secret stored under a setting name that is not a rule stays hidden from
// both the agent read and the public graph.
func TestAgentGetNeverReturnsUnknownSettings(t *testing.T) {
	s, _ := newTestStore(t)
	_, id := sessionWithDescription(t, s, "a shop")
	if err := s.SetCharter("p", "maturity", "mature", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO graph_node_settings(node,setting,value,updated_at,updated_by) VALUES(?,?,?,?,?)`,
		"p", "unknown_setting", "OWNER_ONLY_NODE_SETTING_CANARY", s.now().UnixNano(), "owner"); err != nil {
		t.Fatal(err)
	}
	a := agentOf(t, s, id)
	for _, r := range a.Charter.Rules {
		if r.Name == "unknown_setting" || r.Value == "OWNER_ONLY_NODE_SETTING_CANARY" {
			t.Fatalf("agent_get leaked an unknown setting: %+v", a.Charter.Rules)
		}
	}
}
