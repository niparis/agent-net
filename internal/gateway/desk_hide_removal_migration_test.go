package gateway

import (
	"path/filepath"
	"testing"
)

// The Desk history-hiding feature is removed and the owner asked for its data
// to go. The migration deletes its receipts and drops its column, and leaves
// the rest of the sort history and the log alone.
func TestDeskHideRemovalMigrationDeletesReceiptsAndColumn(t *testing.T) {
	v := migrationVersion(t, deskHideRemovalMarker)
	s := migrateOnly(t, filepath.Join(t.TempDir(), "old.db"), v-1)
	defer s.Close()
	for _, q := range []string{
		`INSERT INTO ask_sorts(id,ask_id,at,sorter,result,reason,hidden_at) VALUES
			(1,'i_a',10,'s_chief','close','done',100),
			(2,'i_b',20,'s_chief','send','to carol',0)`,
		`INSERT INTO desk_log(at,source,about_kind,about_id,text,detail,options) VALUES
			(10,'chief','hide_receipt','rcpt_1','Hidden 1 ask sort(s)','[1]','["hide_undo"]'),
			(20,'chief','hide_receipt','rcpt_2','Undone: Hidden 1 ask sort(s)','[2]','[]'),
			(30,'cto','node','n1','Node added','','[]')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}

	if err := replayMigrations(t, s); err != nil {
		t.Fatalf("replay: %v", err)
	}

	var has int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('ask_sorts') WHERE name='hidden_at'`).Scan(&has); err != nil || has != 0 {
		t.Errorf("ask_sorts.hidden_at still exists (%d, %v)", has, err)
	}
	var receipts int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM desk_log WHERE about_kind='hide_receipt'`).Scan(&receipts); err != nil || receipts != 0 {
		t.Errorf("hide receipts = %d (%v), want 0", receipts, err)
	}
	var sorts int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ask_sorts`).Scan(&sorts); err != nil || sorts != 2 {
		t.Errorf("ask_sorts rows = %d (%v), want the 2 other rows", sorts, err)
	}
	var logRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM desk_log WHERE about_kind='node' AND about_id='n1'`).Scan(&logRows); err != nil || logRows != 1 {
		t.Errorf("other desk_log rows = %d (%v), want the node line", logRows, err)
	}
}
