package gateway

import (
	"database/sql"
	"fmt"

	"github.com/niparis/agent-net/internal/protocol"
)

// workerTablesMarker names the migration in the migrate loop. The loop adds the
// columns below before the statement runs, and workerTablesFinish prices the
// runs and drops the copied columns after it.
const workerTablesMarker = "worker tables rework"

// workerTablesColumns are the columns the rework adds. The loop adds each one
// only when it is missing, so a replay keeps the values it has.
var workerTablesColumns = []struct{ table, name, spec string }{
	{"worker_runs", "usage_in", `REAL NOT NULL DEFAULT 0`},
	{"worker_runs", "usage_out", `REAL NOT NULL DEFAULT 0`},
	{"worker_runs", "usage_reasoning", `REAL NOT NULL DEFAULT 0`},
	{"worker_runs", "usage_cache_read", `REAL NOT NULL DEFAULT 0`},
	{"worker_runs", "usage_cache_write", `REAL NOT NULL DEFAULT 0`},
	{"worker_runs", "cost_usd", `REAL`},
	{"worker_runs", "run_generation", `INTEGER NOT NULL DEFAULT 0`},
	{"worker_runs", "proof_unavailable", `TEXT NOT NULL DEFAULT ''`},
	{"worker_runs", "offered_at", `INTEGER NOT NULL DEFAULT 0`},
	{"worker_runs", "rate_asked", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "rate_offered_at", `INTEGER NOT NULL DEFAULT 0`},
	{"sessions", "rate_asked", `INTEGER NOT NULL DEFAULT 0`},
	{"worker_defects", "blame", `TEXT NOT NULL DEFAULT 'worker'`},
	{"quota_blocks", "window_seconds", `INTEGER NOT NULL DEFAULT 0`},
	{"quota_blocks", "budget_tokens", `INTEGER NOT NULL DEFAULT 0`},
	{"inbox_items", "override_facts", `TEXT NOT NULL DEFAULT ''`},
	// The move columns are read by the copy below. A replay on a store that
	// already dropped them finds them again here.
	{"worker_defects", "moved_at", `INTEGER NOT NULL DEFAULT 0`},
	{"worker_defects", "brief_defect_id", `INTEGER NOT NULL DEFAULT 0`},
}

// workerTablesDropped are the copied columns the rework drops once the rows
// are moved. Every reader joins worker_runs by run_id instead.
var workerTablesDropped = []struct{ table, name string }{
	{"worker_ratings", "harness"}, {"worker_ratings", "model"}, {"worker_ratings", "effort"}, {"worker_ratings", "difficulty"},
	{"worker_defects", "harness"}, {"worker_defects", "model"}, {"worker_defects", "effort"}, {"worker_defects", "difficulty"},
	{"worker_defects", "moved_at"}, {"worker_defects", "moved_by"}, {"worker_defects", "brief_defect_id"},
}

// workerTablesMigration folds nine tables into the rows they describe (owner
// review of 2026-11-21). One worker run used to sit in five tables keyed by the
// same session.
//
//   - Usage, visual proof and the rate offer go onto worker_runs. A rate offer
//     is per session, so it goes onto the session's first run; an offer with no
//     run yet waits on its session row and moves to the run when it is recorded.
//     Usage goes onto the latest run, as the session's total always did.
//   - A brief defect is a worker_defects row with blame 'brief'. A moved defect
//     keeps its own row, now blamed on the brief and charged to the worker's
//     caller.
//   - A learned token budget is a quota_blocks row with no machine and no reset.
//   - A refused brief is a worker_starts row in state 'refused'. SQLite cannot
//     change a CHECK, so the table is rebuilt.
//   - Override facts move into their ask. The run version counter is a
//     gateway_state key.
//   - project_policies goes with the delegation policy feature.
const workerTablesMigration = `
-- worker tables rework: copy each fact into its row, then drop nine tables.
-- Usage onto the latest run of each session. A usage row with no run has no
-- place to go and is dropped with the table.
UPDATE worker_runs SET usage_in=COALESCE(json_extract(u.usage,'$.in'),0), usage_out=COALESCE(json_extract(u.usage,'$.out'),0),
 usage_reasoning=COALESCE(json_extract(u.usage,'$.reasoning'),0), usage_cache_read=COALESCE(json_extract(u.usage,'$.cache_read'),0),
 usage_cache_write=COALESCE(json_extract(u.usage,'$.cache_write'),0)
 FROM worker_usage u
 WHERE worker_runs.session_id<>'' AND u.session_id=worker_runs.session_id AND json_valid(u.usage)
 AND worker_runs.id=(SELECT MAX(w.id) FROM worker_runs w WHERE w.session_id=u.session_id);
UPDATE worker_runs SET proof_unavailable=v.unavailable FROM worker_visual_proof v
 WHERE v.node_id=worker_runs.node_id AND v.session_id=worker_runs.session_id AND v.at=worker_runs.ended_at;
UPDATE worker_runs SET offered_at=o.at, rate_asked=o.asked FROM worker_rate_offers o
 WHERE o.session_id=worker_runs.session_id AND worker_runs.id=(SELECT MIN(w.id) FROM worker_runs w WHERE w.session_id=o.session_id);
UPDATE sessions SET rate_offered_at=o.at, rate_asked=o.asked FROM worker_rate_offers o
 WHERE o.session_id=sessions.id AND NOT EXISTS (SELECT 1 FROM worker_runs w WHERE w.session_id=o.session_id);
UPDATE worker_ratings SET run_id=(SELECT MAX(w.id) FROM worker_runs w WHERE w.session_id=worker_ratings.session_id)
 WHERE run_id=0 AND EXISTS (SELECT 1 FROM worker_runs w WHERE w.session_id=worker_ratings.session_id);
UPDATE worker_defects SET blame='brief', caller=COALESCE((SELECT b.caller FROM worker_brief_defects b WHERE b.id=worker_defects.brief_defect_id), caller)
 WHERE moved_at>0;
INSERT INTO worker_defects(run_id,session_id,caller,rater_model,severity,note,at,blame)
 SELECT b.run_id,b.worker_session_id,b.caller,b.rater_model,b.severity,b.note,b.at,'brief' FROM worker_brief_defects b
 WHERE NOT EXISTS (SELECT 1 FROM worker_defects d WHERE d.brief_defect_id=b.id AND d.moved_at>0);
INSERT OR IGNORE INTO quota_blocks(node_id,provider,window_id,window_label,resets_at,detail,at,window_seconds,budget_tokens)
 SELECT '',provider,'w'||window_seconds,'',0,'',at,window_seconds,tokens FROM quota_budgets;
UPDATE inbox_items SET override_facts=(SELECT json_object('harness',f.harness,'model',f.model,'effort',f.effort,'level',f.level,'provider',f.provider,
 'caller_family',f.caller_family,'override_family',f.override_family,'review',json(CASE WHEN f.review<>0 THEN 'true' ELSE 'false' END))
 FROM start_override_facts f WHERE f.ask_id=inbox_items.id)
 WHERE id IN (SELECT ask_id FROM start_override_facts);
DROP TABLE IF EXISTS worker_starts_new;
CREATE TABLE worker_starts_new (
 ref TEXT PRIMARY KEY, node_id TEXT NOT NULL, owner TEXT NOT NULL,
 project TEXT NOT NULL DEFAULT '', harness TEXT NOT NULL DEFAULT '',
 permissions TEXT NOT NULL DEFAULT '', session_id TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'starting', 'closed', 'not_started', 'refused')),
 created_at INTEGER NOT NULL, closed_at INTEGER NOT NULL DEFAULT 0,
 legacy_ref TEXT NOT NULL DEFAULT '', launch_state TEXT NOT NULL DEFAULT '',
 caller_agent_id TEXT REFERENCES agents(id), done TEXT NOT NULL DEFAULT '[]',
 task_key TEXT NOT NULL DEFAULT '', failures TEXT NOT NULL DEFAULT '');
INSERT INTO worker_starts_new(ref,node_id,owner,project,harness,permissions,session_id,state,created_at,closed_at,legacy_ref,launch_state,caller_agent_id,done)
 SELECT ref,node_id,owner,project,harness,permissions,session_id,state,created_at,closed_at,legacy_ref,launch_state,caller_agent_id,done FROM worker_starts;
DROP TABLE worker_starts;
ALTER TABLE worker_starts_new RENAME TO worker_starts;
CREATE INDEX IF NOT EXISTS worker_starts_legacy_ref ON worker_starts(legacy_ref) WHERE legacy_ref <> '';
-- A refusal is closed when it is made, so the sweep of closed starts clears it after 30 days.
INSERT INTO worker_starts(ref,node_id,owner,state,created_at,closed_at,task_key,failures)
 SELECT 'refused_'||id,'',caller,'refused',at*1000000000,at*1000000000,task_key,failures FROM worker_brief_rejections;
INSERT OR IGNORE INTO gateway_state(key,value,updated_at) SELECT 'worker_runs_version',CAST(n AS TEXT),0 FROM table_versions WHERE name='worker_runs';
INSERT OR IGNORE INTO gateway_state(key,value,updated_at) VALUES('worker_runs_version','0',0);
DROP TRIGGER IF EXISTS worker_runs_version_insert;
DROP TRIGGER IF EXISTS worker_runs_version_update;
DROP TRIGGER IF EXISTS worker_runs_version_delete;
CREATE TRIGGER worker_runs_version_insert AFTER INSERT ON worker_runs
 BEGIN UPDATE gateway_state SET value = CAST(value AS INTEGER) + 1 WHERE key = 'worker_runs_version'; END;
CREATE TRIGGER worker_runs_version_update AFTER UPDATE ON worker_runs
 BEGIN UPDATE gateway_state SET value = CAST(value AS INTEGER) + 1 WHERE key = 'worker_runs_version'; END;
CREATE TRIGGER worker_runs_version_delete AFTER DELETE ON worker_runs
 BEGIN UPDATE gateway_state SET value = CAST(value AS INTEGER) + 1 WHERE key = 'worker_runs_version'; END;
DROP INDEX IF EXISTS worker_ratings_model;
DROP INDEX IF EXISTS worker_defects_model;
DROP TABLE IF EXISTS worker_usage;
DROP TABLE IF EXISTS worker_visual_proof;
DROP TABLE IF EXISTS worker_rate_offers;
DROP TABLE IF EXISTS worker_brief_defects;
DROP TABLE IF EXISTS worker_brief_rejections;
DROP TABLE IF EXISTS quota_budgets;
DROP TABLE IF EXISTS start_override_facts;
DROP TABLE IF EXISTS table_versions;
DROP TABLE IF EXISTS project_policies;
`

// workerTablesPrepare adds the new columns.
func workerTablesPrepare(tx *sql.Tx) error {
	for _, col := range workerTablesColumns {
		var has int
		err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, col.table, col.name).Scan(&has)
		if err == nil && has == 0 {
			_, err = tx.Exec(`ALTER TABLE ` + col.table + ` ADD COLUMN ` + col.name + ` ` + col.spec)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// workerTablesFinish prices the runs that carry usage at today's price list,
// then drops the copied columns. The copied columns are rebuilt by a replay of
// an older migration, so a column that is already gone is skipped.
func workerTablesFinish(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT id,prompt,completion,cache_read,cache_write FROM model_prices`)
	if err != nil {
		return err
	}
	var list []protocol.ModelPrice
	for rows.Next() {
		var p protocol.ModelPrice
		if err := rows.Scan(&p.ID, &p.Prompt, &p.Completion, &p.CacheRead, &p.CacheWrite); err != nil {
			rows.Close()
			return err
		}
		list = append(list, p)
	}
	rows.Close()
	book := protocol.NewPriceBook(list)
	rows, err = tx.Query(`SELECT id,harness,model,usage_in,usage_out,usage_reasoning,usage_cache_read,usage_cache_write FROM worker_runs
		WHERE cost_usd IS NULL AND usage_in+usage_out+usage_reasoning+usage_cache_read+usage_cache_write>0`)
	if err != nil {
		return err
	}
	costs := map[int64]float64{}
	for rows.Next() {
		var id int64
		var harness, model string
		var t protocol.RunTokens
		if err := rows.Scan(&id, &harness, &model, &t.In, &t.Out, &t.Reasoning, &t.CacheRead, &t.CacheWrite); err != nil {
			rows.Close()
			return err
		}
		if p, _, ok := book.PriceOf(protocol.PickerModelKey(harness, model)); ok {
			costs[id] = p.Cost(t)
		}
	}
	rows.Close()
	for id, cost := range costs {
		if _, err := tx.Exec(`UPDATE worker_runs SET cost_usd=? WHERE id=?`, cost, id); err != nil {
			return err
		}
	}
	for _, col := range workerTablesDropped {
		var has int
		err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, col.table, col.name).Scan(&has)
		if err == nil && has > 0 {
			_, err = tx.Exec(`ALTER TABLE ` + col.table + ` DROP COLUMN ` + col.name)
		}
		if err != nil {
			return fmt.Errorf("drop %s.%s: %w", col.table, col.name, err)
		}
	}
	return nil
}
