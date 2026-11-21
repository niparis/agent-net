package gateway

import (
	"database/sql"
	"fmt"
	"strings"
)

// graphNodesFoldMarker finds the migration that drops graph_nodes. The runner
// uses it, and it appears only in that migration's text.
const graphNodesFoldMarker = "Graph nodes fold (schema reduction part 10)"

// graph_nodes held two things, and both live elsewhere now (decision 0179,
// docs/design/graph-nodes-fold.md):
//
//   - "this repo is explicit" is "this repo has a charter value or an edge";
//   - "this name is a legacy architect" is an agent with the role architect
//     that is not tombstoned.
//
// The architect kind stays on the wire. It is computed from agents.role.
//
// architectAgentSQL says whether the name bound to ? is a legacy architect.
const architectAgentSQL = `EXISTS(SELECT 1 FROM agents WHERE id=? AND role='architect' AND tombstone=0)`

// isArchitect reports whether name is a legacy architect.
func isArchitect(q leadDB, name string) (bool, error) {
	var architect bool
	err := q.QueryRow(`SELECT `+architectAgentSQL, name).Scan(&architect)
	return architect, err
}

// putArchitectTx makes name a legacy architect: it creates the agent, or
// revives a tombstoned one. The insert trigger of graph_nodes did this.
func putArchitectTx(tx *sql.Tx, name string, now int64) error {
	_, err := tx.Exec(`INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(?,?,'architect',?,?,0)
 ON CONFLICT(id) DO UPDATE SET role='architect', tombstone=0, updated_at=excluded.updated_at`, name, name, now, now)
	return err
}

// dropArchitectTx removes an architect: the repos it owned have no owner, and
// its agent is tombstoned. The delete trigger of graph_nodes did this.
func dropArchitectTx(tx *sql.Tx, name string, now int64) error {
	if _, err := tx.Exec(`UPDATE repos SET owner_agent_id=NULL WHERE owner_agent_id=?`, name); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE agents SET tombstone=1, updated_at=? WHERE id=?`, now, name)
	return err
}

// explicitNodesSQL lists the explicit nodes: the architects, and the repos that
// have a charter value. A repo kept visible by an edge is not here, because the
// edge branches of the node read already list it. The columns are name, kind,
// created_at (the time of the repo, 0 when it has no repos row) and a rank. An
// architect that also has a repo charter comes twice, and the lower rank wins.
const explicitNodesSQL = `SELECT id AS name,'architect' AS kind,created_at,0 AS rank FROM agents WHERE role='architect' AND tombstone=0
 UNION ALL SELECT c.scope,'component',COALESCE(r.created_at,0),1 FROM (SELECT DISTINCT scope FROM charter_values WHERE level='repo') c LEFT JOIN repos r ON r.id=c.scope`

// chartedSQL lists the repos that have a charter value. The other branches of
// the node read leave them out with NOT IN, which is cheaper than one lookup of
// charter_values for each session row.
const chartedSQL = `SELECT scope FROM charter_values WHERE level='repo'`

// graphNodesFoldMigration rewrites the four triggers that guard a project name
// against an architect, drops the two triggers on graph_nodes and drops the
// table. The runner repairs the architects' agents first (foldGraphNodesTx).
// Every statement is safe to replay: the triggers are dropped before they are
// made, and the table is dropped with IF EXISTS.
const graphNodesFoldMigration = `
-- ` + graphNodesFoldMarker + `: graph_nodes is gone. A repo with a charter value or
-- an edge is an explicit node. An agent with the role architect is an architect.
DROP TRIGGER IF EXISTS repo_agent_architect_insert;
DROP TRIGGER IF EXISTS repo_agent_architect_delete;
DROP TRIGGER IF EXISTS repo_agent_project_insert;
CREATE TRIGGER repo_agent_project_insert AFTER INSERT ON projects WHEN NEW.name<>'' AND NOT EXISTS (SELECT 1 FROM agents WHERE id=NEW.name AND role='architect' AND tombstone=0) BEGIN
 INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(NEW.name,NEW.name,'',NEW.added_at,NEW.added_at,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES(NEW.name,NEW.name,NEW.added_at,NEW.added_at,CASE WHEN EXISTS (SELECT 1 FROM agents WHERE id=NEW.name AND tombstone=0) THEN NEW.name ELSE NULL END) ON CONFLICT(id) DO NOTHING;
END;
DROP TRIGGER IF EXISTS repo_agent_project_update;
CREATE TRIGGER repo_agent_project_update AFTER UPDATE OF name ON projects WHEN NEW.name<>'' AND NOT EXISTS (SELECT 1 FROM agents WHERE id=NEW.name AND role='architect' AND tombstone=0) BEGIN
 INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(NEW.name,NEW.name,'',NEW.checked_at,NEW.checked_at,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES(NEW.name,NEW.name,NEW.checked_at,NEW.checked_at,CASE WHEN EXISTS (SELECT 1 FROM agents WHERE id=NEW.name AND tombstone=0) THEN NEW.name ELSE NULL END) ON CONFLICT(id) DO NOTHING;
END;
DROP TRIGGER IF EXISTS repo_agent_session_insert;
CREATE TRIGGER repo_agent_session_insert AFTER INSERT ON sessions WHEN NEW.repo_project<>'' AND NOT EXISTS (SELECT 1 FROM agents WHERE id=NEW.repo_project AND role='architect' AND tombstone=0) BEGIN
 INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(NEW.repo_project,NEW.repo_project,'',NEW.created_at,NEW.created_at,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES(NEW.repo_project,NEW.repo_project,NEW.created_at,NEW.created_at,CASE WHEN EXISTS (SELECT 1 FROM agents WHERE id=NEW.repo_project AND tombstone=0) THEN NEW.repo_project ELSE NULL END) ON CONFLICT(id) DO NOTHING;
END;
DROP TRIGGER IF EXISTS repo_agent_session_update;
CREATE TRIGGER repo_agent_session_update AFTER UPDATE OF repo_project ON sessions WHEN NEW.repo_project<>'' AND NOT EXISTS (SELECT 1 FROM agents WHERE id=NEW.repo_project AND role='architect' AND tombstone=0) BEGIN
 INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(NEW.repo_project,NEW.repo_project,'',NEW.last_seen,NEW.last_seen,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES(NEW.repo_project,NEW.repo_project,NEW.last_seen,NEW.last_seen,CASE WHEN EXISTS (SELECT 1 FROM agents WHERE id=NEW.repo_project AND tombstone=0) THEN NEW.repo_project ELSE NULL END) ON CONFLICT(id) DO NOTHING;
END;
DROP TABLE IF EXISTS graph_nodes;
`

// graphNodesExist says whether the store still has the table. A store that has
// it has the old layout, and the shipped steps that read it keep their old
// query behind this one test (workerProjectNamesTx).
func graphNodesExist(q leadDB) (bool, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='graph_nodes'`).Scan(&n)
	return n > 0, err
}

// foldGraphNodesTx runs before the migration text drops the table. It makes
// each architect row's agent say so (the trigger did that when the row was
// made), gives a component its creation time in repos when repos has none, and
// records the components that lose their flag. A store with no graph_nodes
// has nothing to fold, so a replay is safe.
func (s *Store) foldGraphNodesTx(tx *sql.Tx) error {
	has, err := graphNodesExist(tx)
	if err != nil || !has {
		return err
	}
	for _, q := range []string{
		`INSERT OR IGNORE INTO agents(id,display_name,role,created_at,updated_at,tombstone) SELECT name,name,'architect',created_at,created_at,0 FROM graph_nodes WHERE kind='architect'`,
		`UPDATE agents SET role='architect', tombstone=0 WHERE id IN (SELECT name FROM graph_nodes WHERE kind='architect') AND (role<>'architect' OR tombstone<>0)`,
		`UPDATE repos SET created_at=(SELECT g.created_at FROM graph_nodes g WHERE g.name=repos.id) WHERE created_at=0 AND id IN (SELECT name FROM graph_nodes WHERE kind='component' AND created_at<>0)`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	rows, err := tx.Query(`SELECT name FROM graph_nodes g WHERE kind='component'
 AND NOT EXISTS (SELECT 1 FROM charter_values c WHERE c.level='repo' AND c.scope=g.name)
 AND NOT EXISTS (SELECT 1 FROM graph_edges e WHERE e.kind<>'reports_to' AND (e.source=g.name OR e.target=g.name)) ORDER BY name`)
	if err != nil {
		return err
	}
	var lost []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		lost = append(lost, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(lost) > 0 {
		shown := lost
		if len(shown) > 20 {
			shown = shown[:20]
		}
		s.event(tx, "graph.nodes_fold", "", "", "", fmt.Sprintf("%d component nodes with no charter value and no edge lose their explicit flag: %s", len(lost), strings.Join(shown, ", ")))
	}
	return nil
}

// graphNodesReplay brings graph_nodes back for a rewind. A folded store has no
// graph_nodes, and the shipped steps before the fold name it in their SQL. The
// table is made again before each of those steps and filled from the new
// sources: a component row per repo charter scope, an architect row per agent
// with that role. The fill is not empty, because the worker sandbox cutover
// reads it for the project names. A step may delete rows (the junk clean-up
// does), so the fill runs before every step. The fold at the end drops the
// table again.
//
// A store that is not folded is left alone: it has the table, or it is older
// than the table, and its own steps make and fill it.
type graphNodesReplay struct {
	checked, active bool
}

func (r *graphNodesReplay) before(tx *sql.Tx) error {
	if !r.checked {
		r.checked = true
		has, err := graphNodesExist(tx)
		if err != nil || has {
			return err
		}
		// Repos and the merged charter store are what a folded store has.
		var folded bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='repos')
 AND EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='agents')
 AND EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='charter_values' AND sql LIKE '%''repo''%')`).Scan(&folded); err != nil {
			return err
		}
		r.active = folded
	}
	if !r.active {
		return nil
	}
	// The architect row is filled with the agent's own update time, so the
	// old trigger (when a replayed step has made it) rewrites nothing.
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS graph_nodes (name TEXT PRIMARY KEY, kind TEXT NOT NULL, created_at INTEGER NOT NULL);
INSERT OR IGNORE INTO graph_nodes(name,kind,created_at) SELECT id,'architect',updated_at FROM agents WHERE role='architect' AND tombstone=0;
INSERT OR IGNORE INTO graph_nodes(name,kind,created_at) SELECT c.scope,'component',COALESCE((SELECT created_at FROM repos r WHERE r.id=c.scope),0)
 FROM (SELECT DISTINCT scope FROM charter_values WHERE level='repo') c WHERE c.scope NOT IN (SELECT id FROM agents WHERE role='architect' AND tombstone=0)`)
	return err
}

// graphNodesFoldIndex is the 0-based position of the fold in migrations, and
// graphNodesCreateIndex the position of the step that makes the table.
func graphNodesFoldIndex() int { return migrationIndex(graphNodesFoldMarker) }

func graphNodesCreateIndex() int { return migrationIndex("CREATE TABLE graph_nodes (") }

func migrationIndex(marker string) int {
	for i, m := range migrations {
		if strings.Contains(m, marker) {
			return i
		}
	}
	return len(migrations)
}
