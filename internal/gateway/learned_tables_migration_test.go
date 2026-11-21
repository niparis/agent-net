package gateway

import (
	"path/filepath"
	"testing"
)

// The learned tables rework (owner review 2026-11-21) moves the facts of six
// tables into columns and drops the tables. This seeds an older database with
// one row of each fact, replays from the version before the rework, and checks
// where each fact landed.
func TestLearnedTablesMigrationMovesEachFact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s := migrateOnly(t, path, migrationVersion(t, learnedTablesMarker)-1)
	defer s.Close()
	for _, q := range []string{
		`INSERT INTO sessions(id,node_id,harness,native_id,last_seen) VALUES('s_owner','n1','claude','native-1',1)`,
		`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state) VALUES
			('m_prompt','c1','','s_owner','s_peer','request','ask bob',0,'k1',100,200,'delivered'),
			('m_outcome','c1','','s_peer','s_owner','reply','done',0,'k2',101,201,'delivered')`,
		`INSERT INTO charter_signals(message_id,source,target,prompt,at) VALUES ('m_prompt','a','b','ask bob',100),('m_outcome','a','b','',101)`,
		`INSERT INTO learned_message_flow(message_id,source,target,excerpt,at) VALUES ('m_prompt','a','b','ask bob',100)`,
		`INSERT INTO signal_prompts(session_id,prompt,at) VALUES ('s_owner','ask carol',123)`,
		`INSERT INTO learned_suggestions(node,field,edge_kind,kind,phrase,evidence,status,severity,created_at,ready_at,history_id) VALUES
			('cogforge','edge','depends','add','tenebrae','[]','applied','minor',1,1,5),
			('cogforge','edge','depends','add','ops','[]','applied','minor',1,1,9)`,
		`INSERT INTO learned_views(node,last_seen_id) VALUES ('cogforge',5)`,
		`INSERT INTO learned_phrase_runs(at) VALUES (100)`,
		`INSERT INTO friction(project,session,change_id,harness,model,area,text,created_at) VALUES
			('demo','s_owner','','claude','','ci','the suite flaked',1),
			('demo','s_owner','','claude','','ci','never marked',2)`,
		`INSERT INTO friction_status_history(friction_id,status,changed_by,changed_at,link,note) VALUES
			(1,'read','cto',10,'ch_1','seen'),(1,'fixed','owner',20,'ch_2','by hand')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	if err := replayMigrations(t, s); err != nil {
		t.Fatal(err)
	}

	type fact struct {
		name, query string
		want        any
	}
	for _, f := range []fact{
		{"message with an owner prompt keeps it", `SELECT owner_prompt FROM messages WHERE id='m_prompt'`, "ask bob"},
		{"message with only an outcome has no prompt", `SELECT owner_prompt FROM messages WHERE id='m_outcome'`, ""},
		{"session prompt keeps its text", `SELECT owner_prompt FROM sessions WHERE id='s_owner'`, "ask carol"},
		{"session prompt keeps its time", `SELECT owner_prompt_at FROM sessions WHERE id='s_owner'`, int64(123)},
		{"a change at or before the last view is seen", `SELECT learned_seen FROM learned_suggestions WHERE history_id=5`, 1},
		{"a change after the last view is not seen", `SELECT learned_seen FROM learned_suggestions WHERE history_id=9`, 0},
		{"a report keeps its latest mark", `SELECT status FROM friction WHERE id=1`, "fixed"},
		{"the latest mark's reader", `SELECT status_by FROM friction WHERE id=1`, "owner"},
		{"the latest mark's time", `SELECT status_at FROM friction WHERE id=1`, int64(20)},
		{"the latest mark's link", `SELECT status_link FROM friction WHERE id=1`, "ch_2"},
		{"the latest mark's note", `SELECT status_note FROM friction WHERE id=1`, "by hand"},
		{"a report with no mark is new", `SELECT status FROM friction WHERE id=2`, "new"},
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

	for _, table := range []string{"charter_signals", "signal_prompts", "learned_message_flow", "learned_phrase_runs", "learned_views", "friction_status_history"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("table %s is still there", table)
		}
	}
}
