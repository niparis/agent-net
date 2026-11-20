package gateway

import (
	"database/sql"
	"fmt"
)

// nodeSideMigration folds five tables that hold at most one row per node into
// the nodes row: node_health, node_tailscale, node_time_zones, node_silence and
// node_graph_refs. The new columns are added, and the rows copied and the tables
// dropped, by foldNodeSideTables, which the migrate loop runs for this marker in
// the same transaction. Appended at the end of the migration list and never
// edited once it ships.
//
// inventory_reports is not folded. Its report is about 630 KB on a machine with a
// large inventory, and SQLite rewrites the whole row on every update of it. On
// the nodes row that made each hello and each inventory write 10 to 12 times
// slower (BenchmarkNodeRowWrites), so it keeps its own table.
const nodeSideMigration = `SELECT 1 /* node side tables folded into nodes */;`

// nodeSideColumns are the columns the fold adds to nodes. An empty value (or 0)
// means the old table had no row for the node.
var nodeSideColumns = []struct{ name, spec string }{
	{"health_report", `TEXT NOT NULL DEFAULT ''`},
	{"health_reported_at", `INTEGER NOT NULL DEFAULT 0`},
	{"tailscale_ip", `TEXT NOT NULL DEFAULT ''`},
	{"tailscale_updated_at", `INTEGER NOT NULL DEFAULT 0`},
	{"time_zone", `TEXT NOT NULL DEFAULT ''`},
	{"time_zone_updated_at", `INTEGER NOT NULL DEFAULT 0`},
	{"silence_dropped_at", `INTEGER NOT NULL DEFAULT 0`},
	{"silence_asleep_from", `INTEGER NOT NULL DEFAULT 0`},
	{"silence_asleep_to", `INTEGER NOT NULL DEFAULT 0`},
	{"silence_silent_at", `INTEGER NOT NULL DEFAULT 0`},
	{"graph_refs_supported", `INTEGER NOT NULL DEFAULT 0`},
}

// nodeSideCopies copy one side table into the nodes row, then drop it. The
// node_graph_refs copy drops its version: nodes.version holds the same string,
// because a hello writes both from one value. A row whose version differs from
// nodes.version counts as not supported, as the blocker check counted it.
var nodeSideCopies = []struct{ table, copy string }{
	{"node_health", `UPDATE nodes SET (health_report, health_reported_at) = (SELECT report, reported_at FROM node_health x WHERE x.node_id = nodes.id)
		WHERE id IN (SELECT node_id FROM node_health)`},
	{"node_tailscale", `UPDATE nodes SET (tailscale_ip, tailscale_updated_at) = (SELECT ip, updated_at FROM node_tailscale x WHERE x.node_id = nodes.id)
		WHERE id IN (SELECT node_id FROM node_tailscale)`},
	{"node_time_zones", `UPDATE nodes SET (time_zone, time_zone_updated_at) = (SELECT zone, updated_at FROM node_time_zones x WHERE x.node_id = nodes.id)
		WHERE id IN (SELECT node_id FROM node_time_zones)`},
	{"node_silence", `UPDATE nodes SET (silence_dropped_at, silence_asleep_from, silence_asleep_to, silence_silent_at)
		= (SELECT dropped_at, asleep_from, asleep_to, silent_at FROM node_silence x WHERE x.node_id = nodes.id)
		WHERE id IN (SELECT node_id FROM node_silence)`},
	{"node_graph_refs", `UPDATE nodes SET graph_refs_supported = (SELECT CASE WHEN g.version = nodes.version AND trim(g.version) NOT IN ('','unknown')
		THEN g.supported ELSE 0 END FROM node_graph_refs g WHERE g.node_id = nodes.id)
		WHERE id IN (SELECT node_id FROM node_graph_refs)`},
}

// foldNodeSideTables runs one step of the migration inside its transaction. The
// columns are added only when they are missing, and a table is copied only
// while it exists, so a rewind that replays the step changes nothing. A row for
// a machine that is not in nodes cannot be folded, so the step stops and the
// upgrade rolls back.
func foldNodeSideTables(tx *sql.Tx) error {
	for _, col := range nodeSideColumns {
		var has int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('nodes') WHERE name = ?`, col.name).Scan(&has); err != nil {
			return err
		}
		if has == 0 {
			if _, err := tx.Exec(`ALTER TABLE nodes ADD COLUMN ` + col.name + ` ` + col.spec); err != nil {
				return err
			}
		}
	}
	for _, c := range nodeSideCopies {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, c.table).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			continue
		}
		var orphans int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM ` + c.table + ` WHERE node_id NOT IN (SELECT id FROM nodes)`).Scan(&orphans); err != nil {
			return err
		}
		if orphans > 0 {
			return fmt.Errorf("%s has %d rows for machines that are not in nodes, so they cannot be folded", c.table, orphans)
		}
		if _, err := tx.Exec(c.copy); err != nil {
			return err
		}
		if _, err := tx.Exec(`DROP TABLE IF EXISTS ` + c.table); err != nil {
			return err
		}
	}
	return nil
}
