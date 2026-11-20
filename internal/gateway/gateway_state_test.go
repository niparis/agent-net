package gateway

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// markerKeys are the 12 rows of owner_settings that are one-time marks and not
// settings. The migration moves them to gateway_state.
var markerKeys = []string{
	"rework_backfill_done", "task_rework_backfill_done", "asks_moved_on_cleanup_v1", "front_door_flush_v1",
	"cto_default_charter_v1", "org_tree_built", "node_scopes_seeded", "cto_legacy_route_start",
	"owed_replies_live_since", "chief.brief_last_item", "chief.brief_last_sent", "chief.brief_last_wake",
}

// legacyNodeSetting writes a row into graph_node_settings, which a migrated
// store no longer has. A test that replays a step older than the gateway state
// migration needs the row there, as the old version left it.
func legacyNodeSetting(t testing.TB, s *Store, node, setting, value string) {
	t.Helper()
	if _, err := s.db.Exec(legacyStateTables); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO graph_node_settings VALUES(?,?,?,1,'owner')`, node, setting, value); err != nil {
		t.Fatal(err)
	}
}

func tableExists(t testing.TB, s *Store, name string) bool {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func stateRows(t testing.TB, s *Store) map[string]string {
	t.Helper()
	rows, err := s.db.Query(`SELECT key,value FROM gateway_state`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		out[k] = v
	}
	return out
}

// downgradeToOldState puts the database in the shape the previous version left:
// the four old tables with rows, the markers in owner_settings, no gateway_state,
// and a schema version just before the migration.
func downgradeToOldState(t *testing.T, s *Store) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`DROP TABLE gateway_state`)
	exec(legacyStateTables)
	exec(`UPDATE graph_ref_gate SET enabled=1, backup='/backups/gateway.db'`)
	exec(`UPDATE graph_cutover_state SET gateway_version='v9.9.9', upgraded_at=4242`)
	exec(`INSERT INTO skill_archive_meta(key,value) VALUES('git_intake_done','77'),('git_intake_at','78')`)
	for i, key := range markerKeys {
		exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,?,?)`, key, "v-"+key, int64(100+i))
	}
	exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('worker_rotation','{}',5),('cto_session','s_cto',6)`)
	exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "-- Gateway state: one key-value table"))
}

func TestGatewayStateMigrationMovesTheOldRows(t *testing.T) {
	path := seededStorePath(t, "gw.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	downgradeToOldState(t, s)
	// A node setting of an older version has a name the node settings door no
	// longer writes. One of the ten current names was copied by its cutover long
	// ago, so the migration must not copy it again.
	legacyNodeSetting(t, s, "alpha", "sandbox", "workspace")
	legacyNodeSetting(t, s, "alpha", "merge_authority", "ask")
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	want := map[string]string{
		"graph_cutover_version": "v9.9.9", "graph_cutover_upgraded_at": "4242",
		"graph_ref_gate": "1", "graph_ref_backup": "/backups/gateway.db",
		"skill_archive.git_intake_done": "77", "skill_archive.git_intake_at": "78",
	}
	for _, key := range markerKeys {
		want[key] = "v-" + key
	}
	if got := stateRows(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("gateway_state\n got %v\nwant %v", got, want)
	}
	var at int64
	if err = s.db.QueryRow(`SELECT updated_at FROM gateway_state WHERE key='cto_legacy_route_start'`).Scan(&at); err != nil || at != 107 {
		t.Fatalf("a marker keeps its time: %d %v", at, err)
	}
	for _, table := range []string{"graph_node_settings", "graph_cutover_state", "graph_ref_gate", "skill_archive_meta"} {
		if tableExists(t, s, table) {
			t.Errorf("table %s was not dropped", table)
		}
	}
	rows, err := s.db.Query(`SELECT key FROM owner_settings ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var left []string
	for rows.Next() {
		var k string
		if err = rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		left = append(left, k)
	}
	if !reflect.DeepEqual(left, []string{"cto_session", "worker_rotation"}) {
		t.Fatalf("owner_settings keeps the real settings only: %v", left)
	}
	var value string
	if err = s.db.QueryRow(`SELECT value FROM setting_values WHERE key='node_setting.sandbox' AND level='repo' AND scope='alpha' AND item=''`).Scan(&value); err != nil || value != `"workspace"` {
		t.Fatalf("folded setting: %q %v", value, err)
	}
	var n int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM setting_values WHERE key LIKE 'node_setting.%'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("only the old name moves: %d %v", n, err)
	}
	if on, _ := s.graphRefsEnabled(); !on {
		t.Fatal("the typed Graph gate was on and is off after the migration")
	}
}

// A rewind replays the steps older than the migration, and some of them read
// the old tables. The replay must neither fail nor undo what was moved.
func TestGatewayStateMigrationReplaysAfterARewind(t *testing.T) {
	path := seededStorePath(t, "gw.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	downgradeToOldState(t, s)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	first := stateRows(t, s)
	if _, err = s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "Typed Graph capability reports")); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	defer s.Close()
	if got := stateRows(t, s); !reflect.DeepEqual(got, first) {
		t.Fatalf("replay changed gateway_state\n got %v\nwant %v", got, first)
	}
	if tableExists(t, s, "graph_ref_gate") {
		t.Fatal("the replay left the old gate table")
	}
}

func TestFreshStoreKeepsItsMarkersInGatewayState(t *testing.T) {
	s, _ := newTestStore(t)
	state := stateRows(t, s)
	for _, key := range []string{"front_door_flush_v1", "org_tree_built", "node_scopes_seeded", "cto_legacy_route_start", "owed_replies_live_since", "rework_backfill_done", "task_rework_backfill_done", "graph_ref_gate", "graph_cutover_version"} {
		if _, ok := state[key]; !ok {
			t.Errorf("gateway_state has no %s: %v", key, state)
		}
	}
	for _, key := range markerKeys {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM owner_settings WHERE key=?`, key).Scan(&n); err != nil || n != 0 {
			t.Errorf("owner_settings holds the marker %s: %d %v", key, n, err)
		}
	}
}

// owner_settings is in three topic lists, so a write to it refreshes those
// screens. A marker is not a setting and no screen shows it.
func TestMarkerWriteDoesNotRefreshTheOwnerScreens(t *testing.T) {
	s, _ := newTestStore(t)
	topics := []string{protocol.TopicNodes, protocol.TopicWorkers, protocol.TopicInbox}
	before := s.topicVersions()
	if _, err := s.db.Exec(`DELETE FROM gateway_state WHERE key=?`, owedRepliesLiveSinceKey); err != nil {
		t.Fatal(err)
	}
	if err := s.initOwedRepliesLiveSince(); err != nil {
		t.Fatal(err)
	}
	if _, ok := stateRows(t, s)[owedRepliesLiveSinceKey]; !ok {
		t.Fatal("the marker was not written")
	}
	if err := stateSet(s.db, protocol.BriefLastSentKey, "2026-11-20", s.now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	after := s.topicVersions()
	for _, topic := range topics {
		if after[topic] != before[topic] {
			t.Errorf("a marker write moved the %s topic", topic)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('worker_rotation','{}',1)`); err != nil {
		t.Fatal(err)
	}
	moved := s.topicVersions()
	for _, topic := range topics {
		if moved[topic] == after[topic] {
			t.Errorf("an owner setting write did not move the %s topic", topic)
		}
	}
}

// node_settings_get must answer the same before and after the migration for a
// node that has an override, even when an old row sits in graph_node_settings.
func TestNodeSettingsAreTheSameAcrossTheMigration(t *testing.T) {
	path := seededStorePath(t, "gw.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AddArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	for setting, value := range map[string]string{protocol.SettingMergeAuthority: "delegated", protocol.SettingWorkerSandbox: "read-only", protocol.SettingAutonomy: "wake"} {
		if err = s.SetNodeSetting("alpha", setting, value, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	read := func(s *Store) string {
		t.Helper()
		got, err := s.NodeSettings("alpha")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(got)
		return string(b)
	}
	before := read(s)
	if got, _ := s.NodeSettings("alpha"); got.Overrides[protocol.SettingMergeAuthority] != "delegated" {
		t.Fatalf("no override to compare: %+v", got)
	}
	downgradeToOldState(t, s)
	legacyNodeSetting(t, s, "alpha", "merge_authority", "ask")
	legacyNodeSetting(t, s, "alpha", "sandbox", "workspace")
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if after := read(s); after != before {
		t.Fatalf("node_settings_get changed\n before %s\n after  %s", before, after)
	}
}
