package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

func TestWorkerSandboxCutover(t *testing.T) {
	s, _ := newTestStore(t)
	for _, name := range []string{"Shop", "Keep", "Twins", "twins"} {
		if err := s.AddArchitect(name, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][2]string{{"Shop", "sandboxed"}, {"Keep", "read-only"}, {"Twins", "no-network"}} {
		if _, err := s.db.Exec(`INSERT INTO graph_node_settings VALUES(?, 'worker_sandbox', ?, 1, 'owner')`, row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][3]string{{"one", "shop", "no-network"}, {"two", "shop", "read-only"}, {"one", "twins", "sandboxed"}} {
		b, _ := json.Marshal(protocol.DelegationPolicy{Workers: row[2]})
		if _, err := s.db.Exec(`INSERT INTO project_policies VALUES(?,?,?,1)`, row[0], row[1], string(b)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.WriteSettings([]SettingsChange{{Key: "workers.sandbox_level", Level: "repo", Scope: "Keep", Value: json.RawMessage(`"no-network"`)}}, "owner", "existing"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "worker sandbox cutover")); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	snap, err := s.SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.String("workers.sandbox_level", settings.Where{Repo: "Shop"}); got != "read-only" {
		t.Errorf("strictest = %q", got)
	}
	if got := snap.String("workers.sandbox_level", settings.Where{Repo: "Keep"}); got != "no-network" {
		t.Errorf("existing = %q", got)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM setting_values WHERE key='workers.sandbox_level' AND scope IN ('Twins','twins')`).Scan(&count); err != nil || count != 0 {
		t.Errorf("ambiguous rows = %d, %v", count, err)
	}
	var cause, actor string
	if err := s.db.QueryRow(`SELECT cause,actor FROM setting_history WHERE key='workers.sandbox_level' AND scope='Shop'`).Scan(&cause, &actor); err != nil || cause != "cutover" || actor != settingsCutoverActor {
		t.Errorf("history = %q %q %v", cause, actor, err)
	}
	inbox, err := s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	notices := ""
	for _, item := range inbox {
		notices += item.Text + "\n"
	}
	if !strings.Contains(notices, "Shop") || !strings.Contains(notices, "twins") {
		t.Errorf("missing notices: %s", notices)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "worker sandbox cutover")); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	again, _ := s.SettingsSnapshot()
	if again.Revision != snap.Revision {
		t.Errorf("replay revision %d -> %d", snap.Revision, again.Revision)
	}
	inbox2, _ := s.ListInbox()
	if len(inbox2) != len(inbox) {
		t.Errorf("replay duplicated notices")
	}
}

func TestWorkerSandboxSettingsAdapters(t *testing.T) {
	s, _, session, _, _ := policyFixture(t)
	if err := s.AddArchitect("CogForge", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetDelegation(session, protocol.ScopeProject, protocol.DelegationPolicy{Workers: protocol.WorkersReadOnly}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !snap.WorkerSandboxCutover || snap.String("workers.sandbox_level", settings.Where{Repo: "CogForge"}) != "read-only" {
		t.Fatalf("policy did not write settings: %+v", snap)
	}
	got, err := s.WorkerPermissions("elsewhere", "cogforge")
	if err != nil || got != "read-only" {
		t.Fatalf("permissions = %q %v", got, err)
	}
	node, err := s.NodeSettings("CogForge")
	if err != nil || node.Effective.WorkerSandbox != "read-only" {
		t.Fatalf("node = %+v %v", node, err)
	}
	if err := s.SetNodeSetting("CogForge", protocol.SettingWorkerSandbox, "no-network", "owner"); err != nil {
		t.Fatal(err)
	}
	got, err = s.WorkerPermissions("elsewhere", "COGFORGE")
	if err != nil || got != "no-network" {
		t.Fatalf("node write = %q %v", got, err)
	}
}

func TestWorkerSandboxLegacyBackupDoesNotOverrideSettings(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("shop", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO graph_node_settings VALUES('shop','worker_sandbox','read-only',1,'owner')`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeSetting("shop", protocol.SettingWorkerSandbox, "no-network", "owner"); err != nil {
		t.Fatal(err)
	}
	g, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		if n.Name == "shop" && n.Settings[protocol.SettingWorkerSandbox] != "no-network" {
			t.Fatalf("stale graph: %+v", n.Settings)
		}
	}
	if err := s.RemoveArchitect("shop", "owner"); err != nil {
		t.Fatal(err)
	}
	var old string
	if err := s.db.QueryRow(`SELECT value FROM graph_node_settings WHERE node='shop' AND setting='worker_sandbox'`).Scan(&old); err != nil || old != "read-only" {
		t.Fatalf("lost backup %q %v", old, err)
	}
}
