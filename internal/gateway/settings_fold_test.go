package gateway

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// settingsFoldRows are the rows of owner_settings a gateway had before the
// fold: the owner's settings, the role and runtime state, and a key that
// nothing reads.
var settingsFoldRows = map[string]string{
	"integrations":        `{"peer_messages":true,"codex_tools":false,"hint_hook":true,"policy_hook":true,"needs_hook":false,"workers_mod":true,"quota_axi":false}`,
	"cto_settings":        `{"model":"m1","harness":"claude","opencode_model":"","claude_model":""}`,
	"cto_title":           "Atlas",
	"protected_providers": `{"protected_providers":["anthropic"],"reason":"orchestrator"}`,
	"worker_floors":       `{"floors":{"anthropic":20}}`,
	"model_scores":        `[{"match":"claude-test","score":32,"source":"owner"}]`,
	"public":              `{"on":false}`,
	"worker_profiles":     `[{"name":"p1","harness":"claude","model":"claude-test","added_by":"owner","added_at":5}]`,
	"worker_providers":    `{"providers":["anthropic","google"]}`,
	"difficulty_minimums": `{"hard":40}`,
	"time_zone":           "Europe/Paris",
	"cto_session":         "s_cto",
	"chief_session":       "s_chief",
	"worker_rotation":     `{"levels":{"normal":{"explore_credit":0.5}}}`,
	"onboarding":          `{"steps":{"a":"done"},"disabled":false}`,
	"hidden_items":        `{"sessions":["s_h"]}`,
	"stale_unknown_key":   "x",
}

// settingsFoldMarks are one-time marks: the one nothing reads goes, and the
// marks a replay or a running gateway reads stay.
var settingsFoldMarks = map[string]bool{
	"org_tree_built": true, "node_scopes_seeded": true, "front_door_flush_v1": true, "cto_default_charter_v1": true, "graph_ref_backup": false,
	"cto_legacy_route_start": true, "rework_backfill_done": true, "task_rework_backfill_done": true, "asks_moved_on_cleanup_v1": true,
}

// oldSettingsStore makes a store file as the release before the fold left it:
// the three tables with rows, the revision ahead of the history, the tokens in
// settings_target_identity, and the schema version just before the migration.
func oldSettingsStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(seededStorePath(t, "gw.db"))
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	restoreSettingsFoldTables(t, s.db)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`DELETE FROM owner_settings`)
	for key, value := range settingsFoldRows {
		exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,?,7)`, key, value)
	}
	for key := range settingsFoldMarks {
		exec(`INSERT OR REPLACE INTO gateway_state(key,value,updated_at) VALUES(?,'1',9)`, key)
	}
	exec(`DELETE FROM setting_values WHERE key LIKE 'owner.%'`)
	exec(`DELETE FROM gateway_state WHERE key IN ('cto_session','chief_session','worker_rotation','onboarding','hidden_items','settings_network_token')`)
	exec(`UPDATE setting_revision SET revision=37`)
	exec(`INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES('a1','a1','',0,0,0)`)
	exec(`INSERT INTO repos(id,display_name,created_at,updated_at) VALUES('r1','r1',0,0)`)
	exec(`UPDATE agents SET settings_token='' WHERE id='a1'`)
	exec(`UPDATE repos SET settings_token='' WHERE id='r1'`)
	exec(`DELETE FROM settings_target_identity`)
	exec(`INSERT INTO settings_target_identity(kind,id,token) VALUES('network','network','tok-net'),('repo','r1','tok-repo'),('agent','a1','tok-agent')`)
	exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, settingsFoldMarker))
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restoreRepoSideTablesAt(t, path)
	return path
}

// Every key that moved reads the same after the migration, through the same
// readers the tools and screens use.
func TestSettingsFoldKeepsEveryOwnerValue(t *testing.T) {
	path := oldSettingsStore(t)
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	same := func(name string, got any, raw string, into any) {
		t.Helper()
		if err := json.Unmarshal([]byte(raw), into); err != nil {
			t.Fatal(err)
		}
		if want := reflect.ValueOf(into).Elem().Interface(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s after the migration = %+v, want %+v", name, got, want)
		}
	}
	integrations, set, err := s.Integrations()
	if err != nil || !set {
		t.Fatalf("integrations: %v %v", set, err)
	}
	same("integrations", integrations, settingsFoldRows["integrations"], new(protocol.Integrations))
	ctoSettings, err := s.CTOSettings()
	if err != nil {
		t.Fatal(err)
	}
	if ctoSettings.Title != "Atlas" || ctoSettings.Model != "m1" || ctoSettings.Harness != "claude" {
		t.Errorf("cto settings = %+v", ctoSettings)
	}
	if title, err := s.CTOTitle(); err != nil || title.Title != "Atlas" || !title.Set {
		t.Errorf("cto title = %+v %v", title, err)
	}
	protected, err := s.protectedProviders()
	if err != nil || !reflect.DeepEqual(protected.Providers, []string{"anthropic"}) || protected.Reason != "orchestrator" {
		t.Errorf("protected providers = %+v %v", protected, err)
	}
	if floors, err := s.workerFloors(); err != nil || floors.Floors["anthropic"] != 20 {
		t.Errorf("worker floors = %+v %v", floors, err)
	}
	scores, err := s.scores()
	same("model scores", scores, settingsFoldRows["model_scores"], new([]protocol.ModelScore))
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := s.WorkerProfiles()
	same("worker profiles", profiles, settingsFoldRows["worker_profiles"], new([]protocol.WorkerProfile))
	if err != nil {
		t.Fatal(err)
	}
	if providers, err := s.workerProviders(); err != nil || !reflect.DeepEqual(providers.Providers, []string{"anthropic", "google"}) {
		t.Errorf("worker providers = %+v %v", providers, err)
	}
	if min, err := s.difficultyMinimums(); err != nil || min["hard"] != 40 {
		t.Errorf("difficulty minimums = %v %v", min, err)
	}
	if zone, err := s.timeZoneSetting(); err != nil || zone.Name != "Europe/Paris" {
		t.Errorf("time zone = %+v %v", zone, err)
	}
	if raw, _, err := ownerRaw(s.db, publicKey); err != nil || raw != settingsFoldRows["public"] {
		t.Errorf("public = %q %v", raw, err)
	}

	// Role and runtime state moved to gateway_state.
	if id, err := s.CTOSessionID(); err != nil || id != "s_cto" {
		t.Errorf("cto session = %q %v", id, err)
	}
	if id, err := chiefSession(s.db); err != nil || id != "s_chief" {
		t.Errorf("chief session = %q %v", id, err)
	}
	if rotation, err := s.WorkerRotation(); err != nil || rotation.Levels["normal"] == nil || rotation.Levels["normal"].ExploreCredit != 0.5 {
		t.Errorf("worker rotation = %+v %v", rotation, err)
	}
	if onboarding, err := s.Onboarding(); err != nil || onboarding.Steps["a"] != "done" {
		t.Errorf("onboarding = %+v %v", onboarding, err)
	}
	if _, set, err := stateGet(s.db, hiddenItemsKey); err != nil || !set {
		t.Errorf("hidden items were not kept: %v %v", set, err)
	}

	// The tables are gone and nothing is left in owner_settings' place.
	for _, table := range []string{"owner_settings", "setting_revision", "settings_target_identity"} {
		if tableExists(t, s, table) {
			t.Errorf("table %s was not dropped", table)
		}
	}
	state := stateRows(t, s)
	if _, ok := state["stale_unknown_key"]; ok {
		t.Error("a key nothing reads was copied")
	}
	for mark, kept := range settingsFoldMarks {
		if _, ok := state[mark]; ok != kept {
			t.Errorf("mark %s kept = %v, want %v", mark, ok, kept)
		}
	}
}

// The moved settings have history and undo like any setting, and the revision
// does not go back.
func TestSettingsFoldWritesHistoryAndKeepsTheRevision(t *testing.T) {
	s, err := OpenStore(oldSettingsStore(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rev, err := settingsRevision(s.db)
	if err != nil || rev < 38 {
		t.Fatalf("revision = %d %v, want at least 38 (the old revision plus the move)", rev, err)
	}
	history, err := s.SettingsHistory("owner.worker_floors", 0, true)
	if err != nil || len(history) != 1 || history[0].Actor != settingsCutoverActor || string(history[0].New) != settingsFoldRows["worker_floors"] {
		t.Fatalf("history of a moved key = %+v %v", history, err)
	}
	if err := s.setOwner(workerFloorsKey, map[string]any{"floors": map[string]int{"anthropic": 50}}); err != nil {
		t.Fatal(err)
	}
	if floors, _ := s.workerFloors(); floors.Floors["anthropic"] != 50 {
		t.Fatalf("a change through the settings path was not read back: %+v", floors)
	}
	changed, _ := settingsRevision(s.db)
	if _, err := s.UndoSettings(changed, "owner"); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if floors, _ := s.workerFloors(); floors.Floors["anthropic"] != 20 {
		t.Fatalf("undo did not restore the moved value: %+v", floors)
	}
}

// A settings target keeps its token across the migration, and a re-created
// repo gets a fresh one.
func TestSettingsFoldKeepsTheSettingsTokens(t *testing.T) {
	s, err := OpenStore(oldSettingsStore(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, c := range []struct{ level, name, token string }{{"network", "", "tok-net"}, {"repo", "r1", "tok-repo"}, {"agent", "a1", "tok-agent"}} {
		res, err := s.ResolveSettingsTarget(protocol.SettingsTarget{Level: c.level, Name: c.name})
		if err != nil || res.Target == nil || res.Target.Token != c.token {
			t.Fatalf("%s token = %+v %v, want %s", c.level, res.Target, err, c.token)
		}
	}
	for _, q := range []string{`DELETE FROM repos WHERE id='r1'`, `INSERT INTO repos(id,display_name,created_at,updated_at) VALUES('r1','r1',0,0)`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.ResolveSettingsTarget(protocol.SettingsTarget{Level: "repo", Name: "r1"})
	if err != nil || res.Target == nil || res.Target.Token == "" || res.Target.Token == "tok-repo" {
		t.Fatalf("a re-created repo reuses its token: %+v %v", res.Target, err)
	}
}
