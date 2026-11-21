package gateway

import "database/sql"

// oneCharterStoreMarker names the migration that merges the charter tables.
const oneCharterStoreMarker = "One charter store (schema reduction part 4)"

// oneCharterStoreMigration moves the repo and agent charter values into
// charter_values, which already held the global and team values, and drops the
// two old tables. charter_values is rebuilt because SQLite cannot widen a CHECK
// in place. Every statement is safe to replay: the old tables are made empty
// first, so a replay copies nothing and keeps the rows already merged.
const oneCharterStoreMigration = `
-- ` + oneCharterStoreMarker + `: repo and agent values live in charter_values.
CREATE TABLE IF NOT EXISTS graph_charter (node TEXT NOT NULL, attr TEXT NOT NULL, value TEXT NOT NULL, updated_at INTEGER NOT NULL, updated_by TEXT NOT NULL, PRIMARY KEY(node, attr));
CREATE TABLE IF NOT EXISTS agent_charter (agent_id TEXT NOT NULL, attr TEXT NOT NULL, value TEXT NOT NULL, updated_at INTEGER NOT NULL, updated_by TEXT NOT NULL, PRIMARY KEY(agent_id, attr));
DROP TABLE IF EXISTS charter_values_new;
CREATE TABLE charter_values_new (
 level TEXT NOT NULL CHECK(level IN ('global','team','repo','agent')),
 scope TEXT NOT NULL, attr TEXT NOT NULL, value TEXT NOT NULL,
 updated_at INTEGER NOT NULL, updated_by TEXT NOT NULL,
 PRIMARY KEY(level,scope,attr), CHECK(level<>'global' OR scope=''));
INSERT INTO charter_values_new(level,scope,attr,value,updated_at,updated_by) SELECT level,scope,attr,value,updated_at,updated_by FROM charter_values;
INSERT OR IGNORE INTO charter_values_new(level,scope,attr,value,updated_at,updated_by) SELECT 'repo',node,attr,value,updated_at,updated_by FROM graph_charter;
INSERT OR IGNORE INTO charter_values_new(level,scope,attr,value,updated_at,updated_by) SELECT 'agent',agent_id,attr,value,updated_at,updated_by FROM agent_charter;
DROP TABLE charter_values;
ALTER TABLE charter_values_new RENAME TO charter_values;
DROP TABLE graph_charter;
DROP TABLE agent_charter;
`

// The three old charter stores had the same shape. Repo values use level
// "repo" and the repo name as scope; agent values use level "agent" and the
// agent id. Every charter read and write goes through these four helpers.

// charterValueTx returns the stored value, or sql.ErrNoRows.
func charterValueTx(q draftQuerier, level, scope, attr string) (string, error) {
	var value string
	err := q.QueryRow(`SELECT value FROM charter_values WHERE level=? AND scope=? AND attr=?`, level, scope, attr).Scan(&value)
	return value, err
}

// putCharterValueTx writes the value, or deletes the row when value is empty.
func putCharterValueTx(tx *sql.Tx, level, scope, attr, value, actor string, now int64) error {
	if value == "" {
		_, err := tx.Exec(`DELETE FROM charter_values WHERE level=? AND scope=? AND attr=?`, level, scope, attr)
		return err
	}
	_, err := tx.Exec(`INSERT INTO charter_values VALUES(?,?,?,?,?,?) ON CONFLICT(level,scope,attr) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at,updated_by=excluded.updated_by`, level, scope, attr, value, now, actor)
	return err
}

// agentCharterTableLayout is the layout before the tables merged: one
// agent_charter table. Only the shipped migration step that seeds the CTO
// charter uses it, because that step runs before oneCharterStoreMigration.
var agentCharterTableLayout = charterLayout{
	get: func(tx *sql.Tx, agent, attr string) (string, error) {
		var value string
		err := tx.QueryRow(`SELECT value FROM agent_charter WHERE agent_id=? AND attr=?`, agent, attr).Scan(&value)
		return value, err
	},
	put: func(tx *sql.Tx, agent, attr, value, actor string, now int64) error {
		if value == "" {
			_, err := tx.Exec(`DELETE FROM agent_charter WHERE agent_id=? AND attr=?`, agent, attr)
			return err
		}
		_, err := tx.Exec(`INSERT INTO agent_charter VALUES(?,?,?,?,?) ON CONFLICT(agent_id,attr) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at,updated_by=excluded.updated_by`, agent, attr, value, now, actor)
		return err
	},
}
