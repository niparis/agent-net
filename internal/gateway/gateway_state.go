package gateway

import (
	"database/sql"
	"errors"
)

// gateway_state is one key-value table for what the gateway remembers about
// itself: the typed Graph gate and cutover time, the skill archive intake
// marks, and the one-time markers of the seeds and backfills. They are not
// owner settings. owner_settings is in three screen topics, so a marker write
// used to refresh the Nodes, Workers and Inbox screens for nothing. gateway_state
// is in no topic list: a value here that a screen shows moves its topics with
// bumpStateTopics (see topic_events.go) when it is written.
//
// The migration copies the one-row tables graph_cutover_state, graph_ref_gate
// and skill_archive_meta, and the 12 marker rows of owner_settings, then drops
// the tables and the rows. graph_node_settings folds into setting_values: the
// ten settings the node settings door writes were copied there by their own
// cutovers long ago, so only a row with another name moves, under node_setting.
const gatewayStateMigration = `
-- Gateway state: one key-value table instead of three one-row tables and the marker rows of owner_settings.
CREATE TABLE IF NOT EXISTS gateway_state (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL DEFAULT 0);
INSERT OR IGNORE INTO gateway_state(key,value,updated_at) SELECT 'graph_cutover_version',gateway_version,upgraded_at FROM graph_cutover_state WHERE id=1;
INSERT OR IGNORE INTO gateway_state(key,value,updated_at) SELECT 'graph_cutover_upgraded_at',CAST(upgraded_at AS TEXT),upgraded_at FROM graph_cutover_state WHERE id=1;
INSERT OR IGNORE INTO gateway_state(key,value,updated_at) SELECT 'graph_ref_gate',CAST(enabled AS TEXT),0 FROM graph_ref_gate WHERE id=1;
INSERT OR IGNORE INTO gateway_state(key,value,updated_at) SELECT 'graph_ref_backup',backup,0 FROM graph_ref_gate WHERE id=1;
INSERT OR IGNORE INTO gateway_state(key,value,updated_at) SELECT 'skill_archive.'||key,value,0 FROM skill_archive_meta;
INSERT OR IGNORE INTO gateway_state(key,value,updated_at) SELECT key,value,updated_at FROM owner_settings WHERE key IN ('rework_backfill_done','task_rework_backfill_done','asks_moved_on_cleanup_v1','front_door_flush_v1','cto_default_charter_v1','org_tree_built','node_scopes_seeded','cto_legacy_route_start','owed_replies_live_since','chief.brief_last_item','chief.brief_last_sent','chief.brief_last_wake');
DELETE FROM owner_settings WHERE key IN ('rework_backfill_done','task_rework_backfill_done','asks_moved_on_cleanup_v1','front_door_flush_v1','cto_default_charter_v1','org_tree_built','node_scopes_seeded','cto_legacy_route_start','owed_replies_live_since','chief.brief_last_item','chief.brief_last_sent','chief.brief_last_wake');
INSERT OR IGNORE INTO setting_values(key,level,scope,item,value,revision) SELECT 'node_setting.'||setting,'repo',node,'',json_quote(value),0 FROM graph_node_settings WHERE setting NOT IN ('worker_sandbox','tests_before_merge','merge_authority','judge_consumers','deploy_rule','autonomy','wake_on_message','loop_every','loop_prompt','loop_mode','loop_plan');
DROP TABLE IF EXISTS graph_node_settings;
DROP TABLE IF EXISTS graph_cutover_state;
DROP TABLE IF EXISTS graph_ref_gate;
DROP TABLE IF EXISTS skill_archive_meta;
`

const (
	// graphRefGateKey is "1" once the typed Graph cutover has committed.
	graphRefGateKey = "graph_ref_gate"
	// graphRefBackupKey names the backup taken before that cutover.
	graphRefBackupKey = "graph_ref_backup"
	// graphCutoverVersionKey is the gateway version that last started, and
	// graphCutoverUpgradedKey the time (Unix nanos) it first did.
	graphCutoverVersionKey  = "graph_cutover_version"
	graphCutoverUpgradedKey = "graph_cutover_upgraded_at"
)

// graphRefGateSQL is a scalar subquery for SQL that tests the gate: '1' when on.
const graphRefGateSQL = `(SELECT value FROM gateway_state WHERE key='` + graphRefGateKey + `')`

// stateGet returns the value of key, and false when it is not set.
func stateGet(q interface{ QueryRow(string, ...any) *sql.Row }, key string) (string, bool, error) {
	var v string
	err := q.QueryRow(`SELECT value FROM gateway_state WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// stateSet writes key. updatedAt is a Unix nanosecond stamp.
func stateSet(q interface {
	Exec(string, ...any) (sql.Result, error)
}, key, value string, updatedAt int64) error {
	_, err := q.Exec(`INSERT INTO gateway_state(key,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value, updatedAt)
	return err
}

// graphRefGateOn says whether the typed Graph cutover is on. A missing row is off.
func graphRefGateOn(q interface{ QueryRow(string, ...any) *sql.Row }) (bool, error) {
	v, _, err := stateGet(q, graphRefGateKey)
	return v == "1", err
}

// A marker is written by a seed that runs inside the migration loop, so it
// meets two schemas: before this migration the marker lives in owner_settings,
// and gateway_state does not exist; after it the marker lives in gateway_state.
// The seed finds it in whichever holds it.
func stateTableExists(tx *sql.Tx) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='gateway_state'`).Scan(&n)
	return n > 0, err
}

// markerGetTx reads a seed marker, and false when it is not set.
func markerGetTx(tx *sql.Tx, key string) (string, bool, error) {
	has, err := stateTableExists(tx)
	if err != nil {
		return "", false, err
	}
	if has {
		return stateGet(tx, key)
	}
	var v string
	err = tx.QueryRow(`SELECT value FROM owner_settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// markerSetTx writes a seed marker. A marker written before this migration
// ran is moved by it.
func markerSetTx(tx *sql.Tx, key, value string, now int64) error {
	has, err := stateTableExists(tx)
	if err != nil {
		return err
	}
	if has {
		return stateSet(tx, key, value, now)
	}
	_, err = tx.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value, now)
	return err
}

// ctoLegacyRouteStartKey holds the time of the first start of the gateway that
// routes replies to old CTO messages (the 30 day window starts there).
const ctoLegacyRouteStartKey = "cto_legacy_route_start"

// legacyNodeSettingKey is the setting_values key of a node setting that the
// node settings door does not write any more.
func legacyNodeSettingKey(setting string) string { return "node_setting." + setting }
