package gateway

import (
	"path/filepath"
	"testing"
)

// The worker tables rework (owner review 2026-11-21) folds nine tables into the
// rows they describe. This seeds an older database with a row in every one of
// them, replays the newest migration, and checks where each fact landed.
func TestWorkerTablesMigrationMovesEachFact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s := migrateOnly(t, path, migrationVersion(t, workerTablesMarker)-1)
	defer s.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	// Two runs of one session (a continue), a session with one run, and a
	// session that never recorded a run.
	exec(`INSERT INTO sessions(id,node_id,harness,native_id,last_seen) VALUES('s_two','n1','opencode','native-two',1),('s_one','n1','opencode','native-one',1),('s_none','n1','opencode','native-none',1)`)
	exec(`INSERT INTO worker_runs(id,node_id,session_id,harness,model,outcome,started_at,ended_at,caller,caller_session_id,difficulty) VALUES
		(1,'n1','s_two','opencode','p/m:high','complete',90,100,'s_caller','s_caller','hard'),
		(2,'n1','s_two','opencode','p/m:high','complete',190,200,'','','hard'),
		(3,'n1','s_one','opencode','p/other','partial',290,300,'s_caller','s_caller','normal')`)
	exec(`INSERT INTO model_prices(id,prompt,completion,cache_read,cache_write,updated_at) VALUES('p/m',0.000001,0.000002,0,0,1)`)
	// Usage: the session's total sits on its latest run, priced from the list. A
	// session with no run has nowhere to go and is dropped.
	exec(`INSERT INTO worker_usage(session_id,usage,updated_at) VALUES
		('s_two','{"in":1000,"out":500,"cache_read":200}',1),('s_one','{"in":10}',1),('s_gone','{"in":99}',1)`)
	exec(`INSERT INTO worker_visual_proof(node_id,session_id,generation,model,unavailable,at) VALUES('n1','s_one',1,'p/other','no display',300)`)
	exec(`INSERT INTO worker_rate_offers(session_id,caller,harness,model,effort,difficulty,asked,at) VALUES
		('s_two','s_caller','opencode','p/m','high','hard',1,95),('s_none','s_caller','opencode','p/m','high','hard',0,96)`)
	// Ratings and defects carried a copy of the run's facts.
	exec(`INSERT INTO worker_ratings(run_id,session_id,caller,harness,model,effort,difficulty,score,at) VALUES
		(2,'s_two','s_caller','opencode','p/m','high','hard',5,210),(3,'s_one','s_caller','opencode','p/other','','normal',2,310)`)
	exec(`INSERT INTO worker_defects(id,run_id,session_id,caller,harness,model,effort,difficulty,severity,note,at,moved_at,moved_by,brief_defect_id) VALUES
		(1,2,'s_two','s_caller','opencode','p/m','high','hard',2,'late',220,0,'',0),
		(2,3,'s_one','s_owner','opencode','p/other','','normal',3,'moved',320,330,'s_owner',7)`)
	exec(`INSERT INTO worker_brief_defects(id,run_id,worker_session_id,caller,rater,severity,note,at) VALUES
		(7,3,'s_one','s_caller','s_owner',3,'moved',330),(8,0,'','s_caller','s_caller',2,'third attempt',340)`)
	exec(`INSERT INTO quota_budgets(provider,window_seconds,tokens,at) VALUES('codex',18000,4000000,400)`)
	exec(`INSERT INTO quota_blocks(node_id,provider,window_id,window_label,resets_at,detail,at) VALUES('n1','codex','5h','5h',500,'used up',450)`)
	exec(`INSERT INTO inbox_items(id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status) VALUES('a_held','question','s_one','shop','hold','','','',1,'open')`)
	exec(`INSERT INTO start_override_facts(ask_id,harness,model,effort,level,provider,caller_family,override_family,review) VALUES('a_held','claude','claude-opus-5-5','high','hard','anthropic','gpt','claude',1)`)
	exec(`INSERT INTO worker_brief_rejections(caller,task_key,failures,at) VALUES('s_caller','key-1','Limits: missing',600)`)
	exec(`INSERT INTO table_versions(name,n) VALUES('worker_runs',41) ON CONFLICT(name) DO UPDATE SET n=41`)
	exec(`INSERT INTO project_policies(node_id,project,policy,updated_at) VALUES('n1','shop','{"delegate":"off"}',1)`)

	if err := replayMigrations(t, s); err != nil {
		t.Fatal(err)
	}

	type fact struct {
		name, query string
		want        any
	}
	for _, f := range []fact{
		{"usage lands on the latest run", `SELECT usage_in FROM worker_runs WHERE id=2`, 1000.0},
		{"usage by kind", `SELECT usage_out+usage_cache_read FROM worker_runs WHERE id=2`, 700.0},
		{"the earlier run holds none", `SELECT usage_in FROM worker_runs WHERE id=1`, 0.0},
		{"a session with one run", `SELECT usage_in FROM worker_runs WHERE id=3`, 10.0},
		{"cost from the price list", `SELECT round(cost_usd*1e6) FROM worker_runs WHERE id=2`, 1000.0 + 500*2 + 200.0},
		{"an unlisted model takes the middle price", `SELECT round(cost_usd*1e6) FROM worker_runs WHERE id=3`, 10.0},
		{"a run with no usage has no cost", `SELECT cost_usd IS NULL FROM worker_runs WHERE id=1`, 1},
		{"proof lands on its run", `SELECT proof_unavailable FROM worker_runs WHERE id=3`, "no display"},
		{"a run with no proof", `SELECT proof_unavailable FROM worker_runs WHERE id=1`, ""},
		{"offer on the session's first run", `SELECT offered_at*10+rate_asked FROM worker_runs WHERE id=1`, 951},
		{"the later run has no offer", `SELECT offered_at FROM worker_runs WHERE id=2`, 0},
		{"an offer with no run waits on its session", `SELECT rate_offered_at FROM sessions WHERE id='s_none'`, 96},
		{"a late defect keeps blame on the worker", `SELECT blame FROM worker_defects WHERE id=1`, "worker"},
		{"a moved defect is blamed on the brief", `SELECT blame FROM worker_defects WHERE id=2`, "brief"},
		{"and charged to the caller", `SELECT caller FROM worker_defects WHERE id=2`, "s_caller"},
		{"the moved brief row is not copied twice", `SELECT COUNT(*) FROM worker_defects WHERE session_id='s_one'`, 1},
		{"a brief defect with no worker", `SELECT COUNT(*) FROM worker_defects WHERE blame='brief' AND session_id='' AND caller='s_caller' AND severity=2`, 1},
		{"the budget is a quota row with no machine", `SELECT budget_tokens FROM quota_blocks WHERE node_id='' AND provider='codex' AND window_seconds=18000`, int64(4000000)},
		{"the block is unchanged", `SELECT resets_at FROM quota_blocks WHERE node_id='n1' AND provider='codex' AND window_id='5h'`, int64(500)},
		{"override facts move into the ask", `SELECT json_extract(override_facts,'$.model') FROM inbox_items WHERE id='a_held'`, "claude-opus-5-5"},
		{"and keep the review flag", `SELECT json_extract(override_facts,'$.review') FROM inbox_items WHERE id='a_held'`, 1},
		{"a refusal is a refused start", `SELECT state FROM worker_starts WHERE owner='s_caller' AND task_key='key-1'`, "refused"},
		{"with its failures", `SELECT failures FROM worker_starts WHERE task_key='key-1'`, "Limits: missing"},
		{"and the time in nanoseconds", `SELECT created_at FROM worker_starts WHERE task_key='key-1'`, int64(600) * 1e9},
		{"the run version carries over", `SELECT CAST(value AS INTEGER)>=41 FROM gateway_state WHERE key='worker_runs_version'`, 1},
	} {
		t.Run(f.name, func(t *testing.T) {
			var got any
			switch f.want.(type) {
			case string:
				var v string
				if err := s.db.QueryRow(f.query).Scan(&v); err != nil {
					t.Fatal(err)
				}
				got = v
			case int64:
				var v int64
				if err := s.db.QueryRow(f.query).Scan(&v); err != nil {
					t.Fatal(err)
				}
				got = v
			case float64:
				var v float64
				if err := s.db.QueryRow(f.query).Scan(&v); err != nil {
					t.Fatal(err)
				}
				got = v
			default:
				var v int
				if err := s.db.QueryRow(f.query).Scan(&v); err != nil {
					t.Fatal(err)
				}
				got = v
			}
			if got != f.want {
				t.Fatalf("got %v, want %v", got, f.want)
			}
		})
	}

	// A run bumps the version through the new triggers, as it did through the table.
	var v1, v2 int64
	if err := s.db.QueryRow(`SELECT CAST(value AS INTEGER) FROM gateway_state WHERE key='worker_runs_version'`).Scan(&v1); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE worker_runs SET fix_count=1 WHERE id=1`)
	if err := s.db.QueryRow(`SELECT CAST(value AS INTEGER) FROM gateway_state WHERE key='worker_runs_version'`).Scan(&v2); err != nil || v2 != v1+1 {
		t.Fatalf("version after an update: %d then %d (%v)", v1, v2, err)
	}

	for _, table := range workerDroppedTables {
		if tableExists(t, s, table) {
			t.Errorf("table %s is still there", table)
		}
	}
	for _, col := range workerTablesDropped {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, col.table, col.name).Scan(&n); err != nil || n != 0 {
			t.Errorf("column %s.%s is still there (%v)", col.table, col.name, err)
		}
	}

}
