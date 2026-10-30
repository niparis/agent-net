package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestNodeSettingsOverridesAreStoredOnTheNode(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("shop", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("shop", "maturity", "critical - money", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeSetting("shop", protocol.SettingMergeAuthority, protocol.MergeAsk, "owner"); err != nil {
		t.Fatal(err)
	}
	res, err := s.NodeSettings("shop")
	if err != nil {
		t.Fatal(err)
	}
	if res.Overrides[protocol.SettingMergeAuthority] != protocol.MergeAsk {
		t.Fatalf("overrides: %#v", res.Overrides)
	}
	if res.Defaults.MergeAuthority != protocol.MergeAsk || res.Effective.WorkerSandbox != protocol.WorkersNoNetwork {
		t.Fatalf("defaults/effective: %#v / %#v", res.Defaults, res.Effective)
	}
	g, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range g.Nodes {
		if n.Name == "shop" {
			found = true
			if n.Settings[protocol.SettingMergeAuthority] != protocol.MergeAsk {
				t.Fatalf("graph settings: %#v", n.Settings)
			}
		}
	}
	if !found {
		t.Fatal("node missing")
	}
	// An empty value clears the override.
	if err := s.SetNodeSetting("shop", protocol.SettingMergeAuthority, "", "owner"); err != nil {
		t.Fatal(err)
	}
	if res, _ := s.NodeSettings("shop"); len(res.Overrides) != 0 {
		t.Fatalf("override was not cleared: %#v", res.Overrides)
	}
	if err := s.SetNodeSetting("shop", "nonsense", "x", "owner"); err == nil {
		t.Fatal("unknown setting accepted")
	}
	if err := s.SetNodeSetting("shop", protocol.SettingMergeAuthority, "sometimes", "owner"); err == nil {
		t.Fatal("invalid value accepted")
	}
	if err := s.SetNodeSetting("ghost", protocol.SettingMergeAuthority, protocol.MergeAsk, "owner"); err == nil {
		t.Fatal("unknown node accepted")
	}
}

func TestNodeSettingsUndoAndNodeRemoval(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("shop", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeSetting("shop", protocol.SettingDeployRule, protocol.DeployOwner, "owner"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Query(`SELECT id FROM graph_history WHERE type='setting' AND node='shop' ORDER BY id DESC LIMIT 1`)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if rows.Next() {
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
	}
	rows.Close()
	if id == 0 {
		t.Fatal("no setting history")
	}
	if err := s.GraphUndo(id, "owner"); err != nil {
		t.Fatal(err)
	}
	if res, _ := s.NodeSettings("shop"); len(res.Overrides) != 0 {
		t.Fatalf("undo left an override: %#v", res.Overrides)
	}
	// A node with a setting can still be removed: removal records and clears
	// the setting with the node.
	if err := s.SetNodeSetting("shop", protocol.SettingDeployRule, protocol.DeployOwner, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveArchitect("shop", "owner"); err != nil {
		t.Fatal(err) // removal clears the setting with the node
	}
}

func TestWorkerPermissionsUseTheNodesMaturityUntilAProjectOverride(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("shop", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("shop", "maturity", "critical", "owner"); err != nil {
		t.Fatal(err)
	}
	got, err := s.WorkerPermissions("n_test", "shop")
	if err != nil || got != protocol.WorkersNoNetwork {
		t.Fatalf("critical maturity: %q %v", got, err)
	}
	if err := s.SetNodeSetting("shop", protocol.SettingWorkerSandbox, protocol.WorkersReadOnly, "owner"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.WorkerPermissions("n_test", "shop"); got != protocol.WorkersReadOnly {
		t.Fatalf("node override: %q", got)
	}
	// The owner's project policy still wins over both.
	nodeID, _, err := s.CreateNode("worker-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	sid, err := s.UpsertSession(nodeID, protocol.SessionInfo{Harness: "claude", NativeID: "n1", Cwd: "/w/shop",
		Repo: protocol.RepoInfo{Project: "shop"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetDelegation(sid, protocol.ScopeProject, protocol.DelegationPolicy{Workers: protocol.WorkersSandboxed}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.WorkerPermissions(nodeID, "shop"); got != protocol.WorkersSandboxed {
		t.Fatalf("project policy: %q", got)
	}
	// A node with no maturity keeps today's default.
	if got, _ := s.WorkerPermissions("n_test", "other"); got != protocol.WorkersSandboxed {
		t.Fatalf("no maturity: %q", got)
	}
}

func TestNodeSettingsValuesAreClean(t *testing.T) {
	for _, tc := range []struct{ setting, value string }{
		{protocol.SettingMergeAuthority, protocol.MergeAsk},
		{protocol.SettingWorkerSandbox, protocol.WorkersNoNetwork},
		{protocol.SettingTestsBeforeMerge, protocol.TestsSuite},
		{protocol.SettingJudgeConsumers, protocol.JudgeStrict},
		{protocol.SettingDeployRule, protocol.DeployOwner},
	} {
		if err := protocol.ValidateNodeSetting(tc.setting, tc.value); err != nil {
			t.Errorf("%s=%s: %v", tc.setting, tc.value, err)
		}
	}
	if !strings.Contains(strings.Join(protocol.NodeSettingNames(), ","), protocol.SettingMergeAuthority) {
		t.Fatalf("setting names: %v", protocol.NodeSettingNames())
	}
}
