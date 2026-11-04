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

func TestWorkerSandboxCutoverPreservesMachineFallback(t *testing.T) {
	for _, tc := range []struct{ name, maturity, override, want string }{
		{"critical maturity", "critical", "", protocol.WorkersNoNetwork},
		{"graph override", "growing", protocol.WorkersReadOnly, protocol.WorkersReadOnly},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestStore(t)
			if err := s.AddArchitect("shop", "owner"); err != nil {
				t.Fatal(err)
			}
			if err := s.SetCharter("shop", "maturity", tc.maturity, "owner"); err != nil {
				t.Fatal(err)
			}
			if tc.override != "" {
				if _, err := s.db.Exec(`INSERT INTO graph_node_settings VALUES('shop','worker_sandbox',?,1,'owner')`, tc.override); err != nil {
					t.Fatal(err)
				}
			}
			a, _, err := s.CreateNode("host-a", "linux")
			if err != nil {
				t.Fatal(err)
			}
			b, _, err := s.CreateNode("host-b", "darwin")
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{a, b} {
				if _, err := s.AddProjects(id, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "shop", Path: "/work/shop", GraphChecked: true}}}); err != nil {
					t.Fatal(err)
				}
			}
			raw, _ := json.Marshal(protocol.DelegationPolicy{Workers: protocol.WorkersSandboxed})
			if _, err := s.db.Exec(`INSERT INTO project_policies VALUES(?,'shop',?,1)`, a, string(raw)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "worker sandbox cutover")); err != nil {
				t.Fatal(err)
			}
			if err := s.migrate(); err != nil {
				t.Fatal(err)
			}
			snap, err := s.SettingsSnapshot()
			if err != nil || !snap.WorkerSandboxCutover {
				t.Fatalf("cutover flag: %+v %v", snap, err)
			}
			for _, id := range []string{a, b} {
				got, err := s.WorkerPermissions(id, "shop")
				if err != nil || got != tc.want {
					t.Errorf("machine sandbox = %q, want %q: %v", got, tc.want, err)
				}
			}
			inbox, err := s.ListInbox()
			if err != nil {
				t.Fatal(err)
			}
			if len(inbox) != 1 || !strings.Contains(inbox[0].Text, "host-a") || !strings.Contains(inbox[0].Text, "sandboxed") || !strings.Contains(inbox[0].Text, tc.want) || strings.Contains(inbox[0].Text, "An existing setting is kept") {
				t.Fatalf("notice: %+v", inbox)
			}
		})
	}
}

func TestWorkerSandboxCutoverTightensUnsafeExistingValue(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("shop", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("shop", "maturity", "critical", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteSettings([]SettingsChange{{Key: workerSandboxKey, Level: "repo", Scope: "shop", Value: json.RawMessage(`"sandboxed"`)}}, "owner", "existing"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "worker sandbox cutover")); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	got, err := s.WorkerPermissions("host", "shop")
	if err != nil || got != protocol.WorkersNoNetwork {
		t.Fatalf("unsafe existing value: %q %v", got, err)
	}
	var oldValue, newValue, cause, actor string
	if err = s.db.QueryRow(`SELECT old_value,new_value,cause,actor FROM setting_history WHERE key=? AND scope='shop' AND cause='cutover'`, workerSandboxKey).Scan(&oldValue, &newValue, &cause, &actor); err != nil || oldValue != `"sandboxed"` || newValue != `"no-network"` || actor != settingsCutoverActor {
		t.Fatalf("cutover history: %q %q %q %q %v", oldValue, newValue, cause, actor, err)
	}
	before, err := s.SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "worker sandbox cutover")); err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(); err != nil {
		t.Fatal(err)
	}
	after, err := s.SettingsSnapshot()
	if err != nil || after.Revision != before.Revision {
		t.Fatalf("replay revision: %d %d %v", before.Revision, after.Revision, err)
	}
}

func TestWorkerSandboxCutoverNoticesMachineWithoutPolicy(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("shop", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("shop", "maturity", "critical", "owner"); err != nil {
		t.Fatal(err)
	}
	a, _, err := s.CreateNode("host-a", "linux")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.CreateNode("host-b", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a, b} {
		if _, err := s.AddProjects(id, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "shop", Path: "/work/shop", GraphChecked: true}}}); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(protocol.DelegationPolicy{Workers: protocol.WorkersReadOnly})
	if _, err := s.db.Exec(`INSERT INTO project_policies VALUES(?,'shop',?,1)`, a, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "worker sandbox cutover")); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a, b} {
		if got, err := s.WorkerPermissions(id, "shop"); err != nil || got != protocol.WorkersReadOnly {
			t.Fatalf("effective: %q %v", got, err)
		}
	}
	inbox, err := s.ListInbox()
	if err != nil || len(inbox) != 1 {
		t.Fatalf("notice: %+v %v", inbox, err)
	}
	if !strings.Contains(inbox[0].Text, "host-b: no-network") || strings.Contains(inbox[0].Text, "host-a:") {
		t.Fatalf("old machine values: %q", inbox[0].Text)
	}
}

func TestWorkerSandboxCutoverPreservesExistingWhenNetworkEnforcesFloor(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("shop", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("shop", "maturity", "critical", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES
      ('workers.sandbox_level','network','network','','"read-only"',0),
      ('workers.sandbox_level','repo','shop','','"sandboxed"',0)`); err != nil {
		t.Fatal(err)
	}
	before, err := s.SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "worker sandbox cutover")); err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(); err != nil {
		t.Fatal(err)
	}
	got, err := s.WorkerPermissions("host", "shop")
	if err != nil || got != protocol.WorkersReadOnly {
		t.Fatalf("effective %q %v", got, err)
	}
	after, err := s.SettingsSnapshot()
	if err != nil || after.Revision != before.Revision {
		t.Fatalf("revision changed: %+v %v", after, err)
	}
	var raw string
	if err = s.db.QueryRow(`SELECT value FROM setting_values WHERE key=? AND level='repo' AND scope='shop'`, workerSandboxKey).Scan(&raw); err != nil || raw != `"sandboxed"` {
		t.Fatalf("safe existing address was changed: %q %v", raw, err)
	}
}

func TestWorkerSandboxLegacyFallbackWithoutMaturity(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("shop", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO graph_node_settings VALUES('shop','worker_sandbox','read-only',1,'owner')`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got, err := legacyNodeWorkerSandboxTx(tx, "shop")
	if err != nil || got != protocol.WorkersSandboxed {
		t.Fatalf("legacy fallback without maturity: %q %v", got, err)
	}
}

func TestWorkerSandboxCutoverPreservesExactMachineProjectFallback(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("shop", "owner"); err != nil {
		t.Fatal(err)
	}
	a, _, err := s.CreateNode("host-a", "linux")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.CreateNode("host-b", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddProjects(a, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "shop", Path: "/work/shop", GraphChecked: true}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddProjects(b, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "SHOP", Path: "/work/shop", GraphExcluded: true}}}); err != nil {
		t.Fatal(err)
	}
	// The old lookup uses the exact machine project name for the graph
	// fallback, even if that graph record is no longer a displayed node.
	if _, err = s.db.Exec(`INSERT INTO graph_charter VALUES('SHOP','maturity','critical',1,'owner')`); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(protocol.DelegationPolicy{Workers: protocol.WorkersSandboxed})
	if _, err = s.db.Exec(`INSERT INTO project_policies VALUES(?,'shop',?,1)`, a, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "worker sandbox cutover")); err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(); err != nil {
		t.Fatal(err)
	}
	got, err := s.WorkerPermissions(b, "SHOP")
	if err != nil || got != protocol.WorkersNoNetwork {
		t.Fatalf("machine with exact-name fallback became %q: %v", got, err)
	}
}

func TestWorkerSandboxCutoverPreservesMultipleOldNamesOnMachine(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("shop", "owner"); err != nil {
		t.Fatal(err)
	}
	id, _, err := s.CreateNode("host-a", "linux")
	if err != nil {
		t.Fatal(err)
	}
	entries := []protocol.ProjectEntry{{Name: "shop", Path: "/work/lower", GraphChecked: true}, {Name: "SHOP", Path: "/work/upper", GraphExcluded: true}}
	if _, err = s.AddProjects(id, protocol.ProjectAdd{Projects: entries}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO graph_charter VALUES('SHOP','maturity','critical',1,'owner')`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "worker sandbox cutover")); err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"shop", "SHOP"} {
		got, err := s.WorkerPermissions(id, project)
		if err != nil || got != protocol.WorkersNoNetwork {
			t.Errorf("%s became %q: %v", project, got, err)
		}
	}
	inbox, err := s.ListInbox()
	if err != nil || len(inbox) != 1 || !strings.Contains(inbox[0].Text, "host-a: sandboxed") {
		t.Fatalf("old values notice: %+v %v", inbox, err)
	}
}

func TestWorkerSandboxCutoverNoticeListsAllAffectedMachines(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("shop", "owner"); err != nil {
		t.Fatal(err)
	}
	a, _, err := s.CreateNode("host-a", "linux")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.CreateNode("host-b", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := s.CreateNode("host-c", "linux")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a, b, c} {
		if _, err = s.AddProjects(id, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "shop", Path: "/work/shop", GraphChecked: true}}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []struct{ id, value string }{{a, protocol.WorkersNoNetwork}, {c, protocol.WorkersReadOnly}} {
		raw, _ := json.Marshal(protocol.DelegationPolicy{Workers: p.value})
		if _, err = s.db.Exec(`INSERT INTO project_policies VALUES(?,'shop',?,1)`, p.id, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "worker sandbox cutover")); err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(); err != nil {
		t.Fatal(err)
	}
	inbox, err := s.ListInbox()
	if err != nil || len(inbox) != 1 {
		t.Fatalf("expected one notice for the project: %+v %v", inbox, err)
	}
	if text := inbox[0].Text; !strings.Contains(text, "host-a: no-network") || !strings.Contains(text, "host-b: sandboxed") || strings.Contains(text, "host-c:") {
		t.Fatalf("incorrect affected machines: %q", text)
	}
}

func TestWorkerSandboxCutoverNoticeUsesActualProjectCase(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("Shop", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("Shop", "maturity", "critical", "owner"); err != nil {
		t.Fatal(err)
	}
	id, _, err := s.CreateNode("host-a", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddProjects(id, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "Shop", Path: "/work/shop", GraphChecked: true}}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(protocol.DelegationPolicy{Delegate: protocol.DelegateOff})
	if _, err = s.db.Exec(`INSERT INTO project_policies VALUES(?,'shop',?,1)`, id, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "worker sandbox cutover")); err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(); err != nil {
		t.Fatal(err)
	}
	got, err := s.WorkerPermissions(id, "Shop")
	if err != nil || got != protocol.WorkersNoNetwork {
		t.Fatalf("effective: %q %v", got, err)
	}
	inbox, err := s.ListInbox()
	if err != nil || len(inbox) != 0 {
		t.Fatalf("unchanged machine got a notice: %+v %v", inbox, err)
	}
}

func TestWorkerSandboxCutoverNoticesSessionInferredProject(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("shop", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("shop", "maturity", "critical", "owner"); err != nil {
		t.Fatal(err)
	}
	a, _, err := s.CreateNode("host-a", "linux")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.CreateNode("host-b", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddProjects(a, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "shop", Path: "/work/shop", GraphChecked: true}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertSession(b, protocol.SessionInfo{Harness: "claude", NativeID: "test", Cwd: "/work/shop"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(protocol.DelegationPolicy{Workers: protocol.WorkersReadOnly})
	if _, err = s.db.Exec(`INSERT INTO project_policies VALUES(?,'shop',?,1)`, a, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "worker sandbox cutover")); err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(); err != nil {
		t.Fatal(err)
	}
	got, err := s.WorkerPermissions(b, "shop")
	if err != nil || got != protocol.WorkersReadOnly {
		t.Fatalf("effective: %q %v", got, err)
	}
	inbox, err := s.ListInbox()
	if err != nil || len(inbox) != 1 || !strings.Contains(inbox[0].Text, "host-b: no-network") {
		t.Fatalf("missing session machine: %+v %v", inbox, err)
	}
}

func TestWorkerSandboxCutoverNoticesSeenProjectWithDifferentRegistrationName(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("other", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("other", "maturity", "critical", "owner"); err != nil {
		t.Fatal(err)
	}
	a, _, err := s.CreateNode("host-a", "linux")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.CreateNode("host-b", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddProjects(a, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "other", Path: "/work/other", GraphChecked: true}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddProjects(b, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "different", Path: "/work/other", GraphChecked: true}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertSession(b, protocol.SessionInfo{Harness: "claude", NativeID: "test", Cwd: "/work/other", Repo: protocol.RepoInfo{Project: "other", Root: "/work/other"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(protocol.DelegationPolicy{Workers: protocol.WorkersReadOnly})
	if _, err = s.db.Exec(`INSERT INTO project_policies VALUES(?,'other',?,1)`, a, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "worker sandbox cutover")); err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(); err != nil {
		t.Fatal(err)
	}
	inbox, err := s.ListInbox()
	if err != nil || len(inbox) != 1 || !strings.Contains(inbox[0].Text, "host-b: no-network") {
		t.Fatalf("missing seen project machine: %+v %v", inbox, err)
	}
}
