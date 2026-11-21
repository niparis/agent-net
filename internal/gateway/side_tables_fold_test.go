package gateway

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// oldSideTablesStore makes the file a release before the fold left behind:
// every migration but the fold has run.
func oldSideTablesStore(t *testing.T) (path string, s *Store) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "gw.db")
	s = migrateOnly(t, path, migrationVersion(t, sideTablesFoldMarker)-1)
	return path, s
}

func mustExec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

const foldChange = "ch_fold"

// seedOldChange writes a change with two revisions into the pre-fold schema.
func seedOldChange(t *testing.T, s *Store, id, project, branch string) {
	t.Helper()
	mustExec(t, s, `INSERT INTO delivery_changes
		(id,project,machine,branch,target_branch,base_commit,head_commit,uncommitted,revision,origin,caller,title,difficulty,state,merged_commit,created_at,updated_at)
		VALUES(?,?,'',?,'','',?,0,2,'{}','s_caller','parser','','open','',1,1)`, id, project, branch, strings.Repeat("b", 40))
	for n, commit := range []string{strings.Repeat("a", 40), strings.Repeat("b", 40)} {
		mustExec(t, s, `INSERT INTO delivery_revisions(change_id,number,branch,commit_hash,uncommitted,at) VALUES(?,?,?,?,0,1)`, id, n+1, branch, commit)
	}
}

// A store with rows in every folded table migrates, and each gateway read that
// returned the rows returns the same values after it.
func TestSideTablesFoldKeepsEveryRead(t *testing.T) {
	path, old := oldSideTablesStore(t)
	seedOldChange(t, old, foldChange, "shop", "feat/fold")
	seedOldChange(t, old, "ch_plain", "shop", "feat/plain")
	signals := `[{"code":"heavy_tests","strength":"weak","evidence":"heavy tests: 300 test lines added for 12 code lines"}]`
	check := `{"status":"passed","exit_code":0,"duration_ms":1200}`
	stat := " a.go | 2 +-\n 1 file changed, 1 insertion(+), 1 deletion(-)"
	// Revision 1 has all three values, revision 2 only a stat, and the second
	// change has no stat row at all.
	mustExec(t, old, `INSERT INTO delivery_revision_stats(change_id,revision,diff_stat,signals,handback_check) VALUES(?,1,?,?,?)`, foldChange, stat, signals, check)
	mustExec(t, old, `INSERT INTO delivery_revision_stats(change_id,revision,diff_stat) VALUES(?,2,?)`, foldChange, " b.go | 1 +")
	// One message has a manifest and one has none. A manifest whose message is
	// gone is dropped with the table.
	for _, id := range []string{"m_files", "m_plain"} {
		mustExec(t, old, `INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
			VALUES(?,?,'','a','b','notification','body',0,?,1,2,'stored')`, id, id, id)
	}
	manifest := `[{"id":"f1","name":"one.txt","size":90,"sha256":"` + filesTestSum + `"}]`
	mustExec(t, old, `INSERT INTO message_files(message_id,files) VALUES('m_files',?)`, manifest)
	mustExec(t, old, `INSERT INTO message_files(message_id,files) VALUES('m_gone',?)`, manifest)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, table := range []string{"delivery_revision_stats", "message_files"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name=?`, table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s was not dropped: %d, %v", table, n, err)
		}
	}
	got, err := changeSignals(s.db, foldChange, 1)
	if err != nil || len(got) != 1 || got[0].Code != "heavy_tests" || got[0].Strength != "weak" {
		t.Fatalf("signals of revision 1: %+v, %v", got, err)
	}
	if got, err := changeSignals(s.db, foldChange, 2); err != nil || len(got) != 0 {
		t.Fatalf("signals of revision 2: %+v, %v", got, err)
	}
	hb, err := changeHandbackCheck(s.db, foldChange, 1)
	if err != nil || hb == nil || hb.Status != "passed" || hb.DurationMS != 1200 {
		t.Fatalf("hand-back check of revision 1: %+v, %v", hb, err)
	}
	if hb, err := changeHandbackCheck(s.db, foldChange, 2); err != nil || hb != nil {
		t.Fatalf("hand-back check of revision 2: %+v, %v", hb, err)
	}
	if hb, err := changeHandbackCheck(s.db, "ch_plain", 1); err != nil || hb != nil {
		t.Fatalf("hand-back check of a change with no stat row: %+v, %v", hb, err)
	}
	// The overlap check reads the diff stat of each open change's current
	// revision, which is revision 2 for ch_fold. ch_plain has no stat.
	briefs, err := s.openChangeBriefs("shop")
	if err != nil || len(briefs) != 1 || briefs[0].id != foldChange || !reflect.DeepEqual(briefs[0].paths, []string{"/b.go"}) {
		t.Fatalf("open change briefs: %+v, %v", briefs, err)
	}
	var stats []string
	for _, n := range []int{1, 2} {
		var v string
		if err := s.db.QueryRow(`SELECT diff_stat FROM delivery_revisions WHERE change_id=? AND number=?`, foldChange, n).Scan(&v); err != nil {
			t.Fatal(err)
		}
		stats = append(stats, v)
	}
	if want := []string{stat, " b.go | 1 +"}; !reflect.DeepEqual(stats, want) {
		t.Fatalf("diff stats: %q, want %q", stats, want)
	}
	m, ok, err := s.Message("m_files")
	if err != nil || !ok || len(m.Files) != 1 || m.Files[0].Name != "one.txt" || m.Files[0].Size != 90 {
		t.Fatalf("message with a manifest: %+v, %v, %v", m.Files, ok, err)
	}
	if m, ok, err := s.Message("m_plain"); err != nil || !ok || len(m.Files) != 0 {
		t.Fatalf("message without a manifest: %+v, %v, %v", m.Files, ok, err)
	}

	// Running the migration again, as a rewind does, keeps every value.
	mustExec(t, s, `DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, sideTablesFoldMarker))
	if err := replayMigrations(t, s); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if hb, err := changeHandbackCheck(s.db, foldChange, 1); err != nil || hb == nil || hb.Status != "passed" {
		t.Fatalf("hand-back check after a replay: %+v, %v", hb, err)
	}
	if m, _, err := s.Message("m_files"); err != nil || len(m.Files) != 1 {
		t.Fatalf("manifest after a replay: %+v, %v", m.Files, err)
	}
}

// A diff stat row with no revision row would be lost by the fold, so the
// migration stops with a message that names the row.
func TestSideTablesFoldRefusesAStatWithoutRevision(t *testing.T) {
	path, old := oldSideTablesStore(t)
	seedOldChange(t, old, foldChange, "shop", "feat/fold")
	mustExec(t, old, `INSERT INTO delivery_revision_stats(change_id,revision,diff_stat) VALUES(?,9,'x')`, foldChange)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := OpenStore(path)
	if err == nil || !strings.Contains(err.Error(), "delivery_revision_stats has a row for change "+foldChange+" revision 9") {
		t.Fatalf("open with an orphan stat row: %v", err)
	}
}

// The folded values are written on the revision row: a submit that carries a
// diff stat, signals and a hand-back check stores each on the revision it
// made, and a later revision keeps its own.
func TestSubmitWritesRevisionValuesOnTheRevisionRow(t *testing.T) {
	r := newDeliveryRig(t)
	q := r.workerSubmit("feat/columns", commitA)
	q.DiffStat = " a.go | 1 +"
	q.Signals = testSignals
	q.HandbackCheck = &protocol.HandbackCheck{Status: protocol.HandbackCheckPassed}
	id := r.submit(r.a, q).Change.Change.ID
	q.HeadCommit, q.DiffStat, q.Signals, q.HandbackCheck = commitB, " b.go | 2 ++", nil, nil
	r.submit(r.a, q)
	rows, err := r.s.db.Query(`SELECT number, diff_stat, signals<>'[]', handback_check<>'' FROM delivery_revisions WHERE change_id=? ORDER BY number`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n int
		var stat string
		var hasSignals, hasCheck bool
		if err := rows.Scan(&n, &stat, &hasSignals, &hasCheck); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.Join([]string{stat, boolWord(hasSignals), boolWord(hasCheck)}, "/"))
	}
	if want := []string{" a.go | 1 +/yes/yes", " b.go | 2 ++/no/no"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("revision rows: %q, want %q", got, want)
	}
}

func boolWord(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// unfoldSideTables turns the current schema back into the one the fold
// replaced, so a test can rewind to an older migration and replay forward.
func unfoldSideTables(t *testing.T, s *Store) {
	t.Helper()
	mustExec(t, s, `CREATE TABLE delivery_revision_stats (change_id TEXT NOT NULL, revision INTEGER NOT NULL, diff_stat TEXT NOT NULL, PRIMARY KEY(change_id, revision));
		INSERT INTO delivery_revision_stats SELECT change_id, number, diff_stat FROM delivery_revisions WHERE diff_stat<>'';
		CREATE TABLE message_files (message_id TEXT PRIMARY KEY, files TEXT NOT NULL);
		INSERT INTO message_files SELECT id, files FROM messages WHERE files<>'';
		ALTER TABLE delivery_revisions DROP COLUMN diff_stat;
		ALTER TABLE delivery_revisions DROP COLUMN signals;
		ALTER TABLE delivery_revisions DROP COLUMN handback_check;
		ALTER TABLE messages DROP COLUMN files`)
}
