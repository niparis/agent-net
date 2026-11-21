package gateway

import "testing"

// restoreSettingsFoldTables puts back the three tables the settings fold
// migration dropped, filled from where their facts went, so a test that rewinds
// schema_version can replay the older steps. Some of those steps read them. A
// table that is still there is left alone.
func restoreSettingsFoldTables(t testing.TB, db repoDB) {
	t.Helper()
	missing := func(name string) bool {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 0
	}
	run := func(q string) {
		t.Helper()
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if missing("owner_settings") {
		run(`CREATE TABLE owner_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL)`)
		// The state keys come back from gateway_state when it exists. The
		// settings come back from setting_values, the two strings unquoted.
		if !missing("gateway_state") {
			run(`INSERT OR IGNORE INTO owner_settings(key,value,updated_at) SELECT key,value,updated_at FROM gateway_state
 WHERE key IN ('cto_session','chief_session','chief_cleared','chief_silent_at','worker_rotation','onboarding','hidden_items') OR key LIKE 'chief_stuck_ping:%'`)
		}
		run(`INSERT OR IGNORE INTO owner_settings(key,value,updated_at) SELECT substr(key,7),
 CASE WHEN key IN ('owner.cto_title','owner.time_zone') THEN json_extract(value,'$') ELSE value END,0
 FROM setting_values WHERE key LIKE 'owner.%' AND level='network'`)
	}
	if missing("setting_revision") {
		run(`CREATE TABLE setting_revision (id INTEGER PRIMARY KEY CHECK (id = 1), revision INTEGER NOT NULL CHECK (revision >= 0))`)
		run(`INSERT INTO setting_revision SELECT 1, COALESCE(MAX(revision),0) FROM setting_history`)
	}
	if missing("settings_target_identity") {
		run(`CREATE TABLE settings_target_identity (kind TEXT NOT NULL CHECK(kind IN ('network','team','repo','agent')), id TEXT NOT NULL, token TEXT NOT NULL, PRIMARY KEY(kind,id))`)
		for kind, table := range settingsTokenTables {
			var has int
			if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name='settings_token'`, table).Scan(&has); err != nil {
				t.Fatal(err)
			}
			if has > 0 {
				run(`INSERT OR IGNORE INTO settings_target_identity(kind,id,token) SELECT '` + kind + `',id,settings_token FROM ` + table + ` WHERE settings_token<>''`)
			}
		}
		if !missing("gateway_state") {
			run(`INSERT OR IGNORE INTO settings_target_identity(kind,id,token) SELECT 'network','network',value FROM gateway_state WHERE key='settings_network_token'`)
		}
	}
}

// dropSettingsTokenColumns removes the token columns, as an older release had
// them. The older steps insert rows into these tables by position, so a replay
// needs them gone until the migration adds them again.
func dropSettingsTokenColumns(t testing.TB, db repoDB) {
	t.Helper()
	if _, err := db.Exec(`DROP TRIGGER IF EXISTS settings_token_agent_tombstone`); err != nil {
		t.Fatal(err)
	}
	for _, table := range settingsTokenTables {
		var has int
		if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name='settings_token'`, table).Scan(&has); err != nil {
			t.Fatal(err)
		}
		if has > 0 {
			if _, err := db.Exec(`ALTER TABLE ` + table + ` DROP COLUMN settings_token`); err != nil {
				t.Fatal(err)
			}
		}
	}
}
