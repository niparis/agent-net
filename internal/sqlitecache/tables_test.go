package sqlitecache

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// openTables opens a database with three tables and a trigger-free schema.
func openTables(t testing.TB, size int, pragmas string) (*sql.DB, *Connector) {
	t.Helper()
	return openTablesBusy(t, size, 5000, pragmas)
}

// openTablesBusy is openTables with busy_timeout set to busyMS milliseconds.
func openTablesBusy(t testing.TB, size, busyMS int, pragmas string) (*sql.DB, *Connector) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(%d)%s", filepath.Join(t.TempDir(), "t.db"), busyMS, pragmas)
	c, err := New(dsn, size)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{
		`CREATE TABLE a (n INTEGER PRIMARY KEY, v TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE b (n INTEGER PRIMARY KEY, v TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE c (n INTEGER PRIMARY KEY, v TEXT NOT NULL DEFAULT '')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db, c
}

// moved says which of the tables a, b and c have a new write count after run.
func moved(t testing.TB, c *Connector, run func()) string {
	t.Helper()
	before := map[string]uint64{}
	for _, n := range []string{"a", "b", "c"} {
		before[n] = c.Stamp(n)
	}
	run()
	out := ""
	for _, n := range []string{"a", "b", "c"} {
		if c.Stamp(n) != before[n] {
			out += n
		}
	}
	return out
}

func mustExec(t testing.TB, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// A write moves the stamp of the tables it changes and no other.
func TestStampMovesOnlyForTheTablesAStatementChanges(t *testing.T) {
	db, c := openTables(t, 16, "")
	// Every run starts from the same rows, so the statement changes a row.
	reset := func() {
		for _, q := range []string{`DELETE FROM a`, `DELETE FROM b`, `DELETE FROM c`,
			`INSERT INTO a(n) VALUES (1)`, `INSERT INTO b(n) VALUES (1)`, `INSERT INTO c(n) VALUES (1)`} {
			mustExec(t, db, q)
		}
	}
	cases := []struct {
		name string
		sql  string
		args []any
		want string
	}{
		{"insert", `INSERT INTO a(n) VALUES (?)`, []any{7}, "a"},
		{"update", `UPDATE a SET v='x' WHERE n=1`, nil, "a"},
		{"delete", `DELETE FROM a WHERE n=1`, nil, "a"},
		{"delete all", `DELETE FROM c`, nil, "c"},
		{"replace into", `REPLACE INTO a(n,v) VALUES (2,'r')`, nil, "a"},
		{"insert or replace", `INSERT OR REPLACE INTO a(n,v) VALUES (2,'s')`, nil, "a"},
		{"upsert", `INSERT INTO a(n,v) VALUES (2,'u') ON CONFLICT(n) DO UPDATE SET v=excluded.v`, nil, "a"},
		{"insert select reads b, writes a", `INSERT INTO a(n,v) SELECT n+10, v FROM b`, nil, "a"},
		{"update with subquery on c", `UPDATE b SET v=(SELECT count(*) FROM c) WHERE n=1`, nil, "b"},
		{"with insert", `WITH x(k) AS (SELECT 30) INSERT INTO c(n) SELECT k FROM x`, nil, "c"},
		{"insert returning", `INSERT INTO c(n) VALUES (31) RETURNING n`, nil, "c"},
		{"upper case name", `UPDATE B SET v='y'`, nil, "b"},
		{"comment first", "-- note\nUPDATE a SET v='z'", nil, "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Twice: the second run uses the kept statement.
			for i := 0; i < 2; i++ {
				reset()
				got := moved(t, c, func() { mustExec(t, db, tc.sql, tc.args...) })
				if got != tc.want {
					t.Fatalf("run %d moved %q, want %q", i, got, tc.want)
				}
			}
		})
	}
}

// A statement that changed no row changed nothing, so it moves no stamp.
func TestStatementThatChangedNoRowMovesNothing(t *testing.T) {
	db, c := openTables(t, 16, "")
	mustExec(t, db, `CREATE TRIGGER a_to_b AFTER UPDATE ON a BEGIN UPDATE b SET v='t'; END`)
	mustExec(t, db, `INSERT INTO a(n) VALUES (1)`)
	writes := c.Writes()
	for _, q := range []string{
		`UPDATE a SET v='x' WHERE n=99`,
		`DELETE FROM a WHERE n=99`,
		`INSERT OR IGNORE INTO a(n) VALUES (1)`,
		`INSERT INTO a(n) VALUES (1) ON CONFLICT DO NOTHING`,
		`INSERT INTO b(n) SELECT n FROM c`,
	} {
		for i := 0; i < 2; i++ {
			if got := moved(t, c, func() { mustExec(t, db, q) }); got != "" {
				t.Fatalf("%s moved %q", q, got)
			}
		}
	}
	if c.Writes() != writes {
		t.Fatalf("writes %d -> %d", writes, c.Writes())
	}
	// With a row, the trigger runs and its table moves.
	if got := moved(t, c, func() { mustExec(t, db, `UPDATE a SET v='x' WHERE n=1`) }); got != "ab" {
		t.Fatalf("moved %q, want ab", got)
	}
	// A failed statement may have changed something (OR FAIL keeps earlier rows).
	mustExec(t, db, `INSERT INTO c(n) VALUES (5)`)
	if got := moved(t, c, func() {
		db.Exec(`INSERT OR FAIL INTO c(n) VALUES (6),(5),(7)`)
	}); got != "c" {
		t.Fatalf("failed statement moved %q, want c", got)
	}
}

// A write through a view runs triggers; its row count says nothing about them.
func TestWriteThroughAViewMovesTheTablesOfItsTriggers(t *testing.T) {
	db, c := openTables(t, 16, "")
	mustExec(t, db, `CREATE VIEW vb AS SELECT n, v FROM b`)
	mustExec(t, db, `CREATE TRIGGER vb_ins INSTEAD OF INSERT ON vb BEGIN INSERT INTO b(n,v) VALUES (NEW.n, NEW.v); END`)
	for i := 1; i <= 2; i++ {
		if got := moved(t, c, func() { mustExec(t, db, `INSERT INTO vb(n,v) VALUES (?, 'x')`, i) }); !contains(got, 'b') {
			t.Fatalf("run %d: an insert through the view moved %q, not b", i, got)
		}
	}
}

func contains(s string, r rune) bool {
	for _, x := range s {
		if x == r {
			return true
		}
	}
	return false
}

// Reads move no stamp.
func TestStampDoesNotMoveForReads(t *testing.T) {
	db, c := openTables(t, 16, "")
	mustExec(t, db, `INSERT INTO a(n) VALUES (1)`)
	total := c.Writes()
	got := moved(t, c, func() {
		for _, q := range []string{`SELECT * FROM a`, `SELECT count(*) FROM b`, `WITH x(k) AS (SELECT 1) SELECT k FROM x`, `PRAGMA page_count`} {
			rows, err := db.Query(q)
			if err != nil {
				t.Fatal(err)
			}
			rows.Close()
		}
	})
	if got != "" || c.Writes() != total {
		t.Fatalf("reads moved %q, writes %d -> %d", got, total, c.Writes())
	}
}

// A statement that counts as one write for Writes still counts once.
func TestWritesCountsOncePerStatement(t *testing.T) {
	db, c := openTables(t, 16, "")
	before := c.Writes()
	mustExec(t, db, `INSERT INTO a(n) VALUES (1)`)
	if c.Writes() != before+1 {
		t.Fatalf("writes %d -> %d", before, c.Writes())
	}
}

// A trigger writes a table the statement does not name. That table must move.
func TestStampSeesWritesByTriggers(t *testing.T) {
	db, c := openTables(t, 16, "")
	mustExec(t, db, `CREATE TRIGGER a_to_b AFTER INSERT ON a BEGIN INSERT INTO b(n,v) VALUES (NEW.n, NEW.v); END`)
	mustExec(t, db, `CREATE TRIGGER b_to_c AFTER INSERT ON b BEGIN UPDATE c SET v='seen'; END`)
	for i := 1; i <= 2; i++ {
		got := moved(t, c, func() { mustExec(t, db, `INSERT INTO a(n) VALUES (?)`, i) })
		if got != "abc" {
			t.Fatalf("run %d moved %q, want abc", i, got)
		}
	}
	got := moved(t, c, func() { mustExec(t, db, `UPDATE b SET v='q' WHERE n=1`) })
	if got != "b" {
		t.Fatalf("update on b moved %q", got)
	}
}

// A statement kept before a trigger was created must see the trigger after.
func TestStatementKeptBeforeATriggerSeesIt(t *testing.T) {
	db, c := openTables(t, 16, "")
	const q = `INSERT INTO a(n) VALUES (?)`
	mustExec(t, db, q, 1)
	if got := moved(t, c, func() { mustExec(t, db, q, 2) }); got != "a" {
		t.Fatalf("before the trigger moved %q", got)
	}
	mustExec(t, db, `CREATE TRIGGER a_to_c AFTER INSERT ON a BEGIN INSERT INTO c(n) VALUES (NEW.n); END`)
	if got := moved(t, c, func() { mustExec(t, db, q, 3) }); got != "ac" {
		t.Fatalf("after the trigger moved %q, want ac", got)
	}
	mustExec(t, db, `DROP TRIGGER a_to_c`)
	if got := moved(t, c, func() { mustExec(t, db, q, 4) }); got != "a" {
		t.Fatalf("after the trigger was dropped moved %q, want a", got)
	}
}

// Schema changes, scripts and statements the connector cannot place move every
// stamp: the fallback invalidates everything.
func TestStampFallsBackToEverything(t *testing.T) {
	db, c := openTables(t, 16, "")
	cases := map[string]func(){
		"create table": func() { mustExec(t, db, `CREATE TABLE d (x)`) },
		"create index": func() { mustExec(t, db, `CREATE INDEX a_v ON a(v)`) },
		"alter table":  func() { mustExec(t, db, `ALTER TABLE c ADD COLUMN w TEXT`) },
		"drop table":   func() { mustExec(t, db, `DROP TABLE d`) },
		"script":       func() { mustExec(t, db, `INSERT INTO a(n) VALUES (50); INSERT INTO b(n) VALUES (50);`) },
		"savepoint":    func() { mustExec(t, db, `SAVEPOINT sp`); mustExec(t, db, `RELEASE sp`) },
		"vacuum":       func() { mustExec(t, db, `VACUUM`) },
		"long text": func() {
			pad := make([]byte, maxQueryLen)
			for i := range pad {
				pad[i] = ' '
			}
			mustExec(t, db, `INSERT INTO a(n) VALUES (51)`+string(pad))
		},
	}
	names := make([]string, 0, len(cases))
	for n := range cases {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		if got := moved(t, c, cases[name]); got != "abc" {
			t.Errorf("%s moved %q, want abc", name, got)
		}
	}
}

// With the statement cache off a write cannot be placed, so it moves all.
func TestStampWithTheCacheOffMovesEverything(t *testing.T) {
	db, c := openTables(t, 0, "")
	if got := moved(t, c, func() { mustExec(t, db, `INSERT INTO a(n) VALUES (1)`) }); got != "abc" {
		t.Fatalf("moved %q, want abc", got)
	}
}

// A statement prepared by hand and used in a transaction is counted too, when
// the transaction commits.
func TestPreparedStatementWritesAreCounted(t *testing.T) {
	db, c := openTables(t, 16, "")
	mustExec(t, db, `INSERT INTO b(n) VALUES (1)`)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	st, err := tx.Prepare(`UPDATE b SET v=? WHERE n=?`)
	if err != nil {
		t.Fatal(err)
	}
	if got := moved(t, c, func() {
		if _, err := st.Exec("x", 1); err != nil {
			t.Fatal(err)
		}
	}); got != "" {
		t.Fatalf("prepared update moved %q before the commit", got)
	}
	ins, err := tx.Prepare(`INSERT INTO c(n) VALUES (?) RETURNING n`)
	if err != nil {
		t.Fatal(err)
	}
	if got := moved(t, c, func() {
		rows, err := ins.Query(5)
		if err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}); got != "bc" {
		t.Fatalf("the commit of a prepared update and insert moved %q, want bc", got)
	}
	// Outside a transaction a prepared statement counts when it ends.
	out, err := db.Prepare(`UPDATE b SET v=? WHERE n=?`)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if got := moved(t, c, func() {
		if _, err := out.Exec("y", 1); err != nil {
			t.Fatal(err)
		}
	}); got != "b" {
		t.Fatalf("prepared update outside a transaction moved %q, want b", got)
	}
}

// Stamp adds up, so one number tells whether any of the tables moved.
func TestStampIsTheSameUntilATableMoves(t *testing.T) {
	db, c := openTables(t, 16, "")
	s := c.Stamp("a", "b")
	mustExec(t, db, `INSERT INTO c(n) VALUES (1)`)
	if c.Stamp("a", "b") != s {
		t.Fatal("a write to c moved the stamp of a and b")
	}
	mustExec(t, db, `INSERT INTO b(n) VALUES (1)`)
	if c.Stamp("a", "b") == s {
		t.Fatal("a write to b did not move the stamp of a and b")
	}
	if c.Stamp("A") != c.Stamp("a") {
		t.Fatal("table names differ by case")
	}
	if c.Stamp("never_written") != c.Stamp("never_written") {
		t.Fatal("an unknown table changes by itself")
	}
}

// Foreign key actions write tables a statement does not name. SQLite reports
// them to the authorizer or the connector must say it cannot tell.
func TestStampSeesForeignKeyActions(t *testing.T) {
	db, c := openTables(t, 16, "&_pragma=foreign_keys(1)")
	mustExec(t, db, `CREATE TABLE parent (id INTEGER PRIMARY KEY)`)
	mustExec(t, db, `CREATE TABLE child (id INTEGER PRIMARY KEY, p INTEGER REFERENCES parent(id) ON DELETE CASCADE)`)
	mustExec(t, db, `CREATE TABLE setnull (id INTEGER PRIMARY KEY, p INTEGER REFERENCES parent(id) ON DELETE SET NULL)`)
	mustExec(t, db, `INSERT INTO parent(id) VALUES (1)`)
	mustExec(t, db, `INSERT INTO child(id,p) VALUES (1,1)`)
	mustExec(t, db, `INSERT INTO setnull(id,p) VALUES (1,1)`)
	cs, ss := c.Stamp("child"), c.Stamp("setnull")
	mustExec(t, db, `DELETE FROM parent WHERE id=1`)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM child`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("cascade did not run: %d %v", n, err)
	}
	if c.Stamp("child") == cs {
		t.Fatal("a cascade delete did not move the stamp of the child table")
	}
	if c.Stamp("setnull") == ss {
		t.Fatal("ON DELETE SET NULL did not move the stamp of its table")
	}
}

// The observer sees the tables SQLite says a statement reads and writes.
func TestObserverReportsTablesReadAndWritten(t *testing.T) {
	db, c := openTables(t, 16, "")
	mustExec(t, db, `CREATE VIEW vb AS SELECT n FROM b`)
	var mu sync.Mutex
	seen := map[string]Statement{}
	c.SetObserver(func(s Statement) {
		mu.Lock()
		seen[s.Query] = s
		mu.Unlock()
	})
	defer c.SetObserver(nil)
	queries := []struct {
		sql    string
		reads  []string
		writes []string
	}{
		{`SELECT v FROM a WHERE n=1`, []string{"a"}, nil},
		{`SELECT count(*) FROM a`, []string{"a"}, nil},
		{`SELECT EXISTS(SELECT 1 FROM b)`, []string{"b"}, nil},
		{`SELECT n FROM a WHERE n IN (SELECT n FROM b) AND NOT EXISTS (SELECT 1 FROM c WHERE c.n=a.n)`, []string{"a", "b", "c"}, nil},
		{`SELECT x.n FROM (SELECT n FROM a) x LEFT JOIN b ON b.n=x.n`, []string{"a", "b"}, nil},
		{`WITH r(k) AS (SELECT n FROM c) SELECT k FROM r`, []string{"c"}, nil},
		{`SELECT n FROM vb`, []string{"b"}, nil},
		{`INSERT INTO a(n,v) SELECT n, v FROM b`, []string{"b"}, []string{"a"}},
		{`UPDATE c SET v=(SELECT max(v) FROM a) WHERE n IN (SELECT n FROM b)`, []string{"a", "b", "c"}, []string{"c"}},
	}
	for _, q := range queries {
		rows, err := db.Query(q.sql)
		if err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
		rows.Close()
	}
	mu.Lock()
	defer mu.Unlock()
	for _, q := range queries {
		s, ok := seen[q.sql]
		if !ok || !s.Known {
			t.Errorf("%s: not seen or unknown: %+v", q.sql, s)
			continue
		}
		if !reflect.DeepEqual(nonNil(s.Reads), nonNil(q.reads)) && !subset(q.reads, s.Reads) {
			t.Errorf("%s: reads %v, want %v", q.sql, s.Reads, q.reads)
		}
		if !reflect.DeepEqual(nonNil(s.Writes), nonNil(q.writes)) {
			t.Errorf("%s: writes %v, want %v", q.sql, s.Writes, q.writes)
		}
	}
}

// Statements the connector does not keep are described for the observer too.
func TestObserverDescribesStatementsThatAreNotKept(t *testing.T) {
	db, c := openTables(t, 16, "")
	var got Statement
	c.SetObserver(func(s Statement) { got = s })
	defer c.SetObserver(nil)
	rows, err := db.Query(`SELECT replace(v, ';', ',') FROM a`)
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if !got.Known || !reflect.DeepEqual(got.Reads, []string{"a"}) {
		t.Fatalf("got %+v", got)
	}
	_, err = db.ExecContext(context.Background(), `INSERT INTO a(n) VALUES (60); INSERT INTO b(n) VALUES (60);`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Known {
		t.Fatalf("a script is known: %+v", got)
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func subset(want, got []string) bool {
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	for _, w := range want {
		if !have[w] {
			return false
		}
	}
	return len(want) > 0
}

func TestIsScript(t *testing.T) {
	for q, want := range map[string]bool{
		`SELECT 1`:                           false,
		`SELECT 1;`:                          false,
		"SELECT 1 ; \n -- done":              false,
		`SELECT replace(v, ';', ',') FROM a`: false,
		`SELECT "a;b" FROM [c;d]`:            false,
		"SELECT 1 /* ; */ FROM a":            false,
		`SELECT 1; SELECT 2`:                 true,
		`INSERT INTO a(n) VALUES (1);INSERT INTO b(n) VALUES (1);`: true,
		`SELECT 'it''s'; SELECT 2`:                                 true,
		"SELECT 1; -- c\nSELECT 2":                                 true,
	} {
		if got := isScript(q); got != want {
			t.Errorf("isScript(%q) = %v, want %v", q, got, want)
		}
	}
}

// A semicolon in a comment or a string does not make a script: the statement
// is kept and its tables are known.
func TestSemicolonInACommentOrStringIsNotAScript(t *testing.T) {
	db, c := openTables(t, 16, "")
	const q = "INSERT INTO a(n, v) VALUES (?, 'x;y') -- a note; with more\n"
	for i := 1; i <= 2; i++ {
		if got := moved(t, c, func() { mustExec(t, db, q, i) }); got != "a" {
			t.Fatalf("run %d moved %q, want a", i, got)
		}
	}
	var v string
	if err := db.QueryRow(`SELECT v FROM a WHERE n=2`).Scan(&v); err != nil || v != "x;y" {
		t.Fatalf("%q %v", v, err)
	}
	// A real script runs whole and moves every table.
	if got := moved(t, c, func() {
		mustExec(t, db, `INSERT INTO a(n) VALUES (10); INSERT INTO b(n) VALUES (10) -- ; c`)
	}); got != "abc" {
		t.Fatalf("script moved %q, want abc", got)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM b WHERE n=10`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the second statement of the script did not run: %d %v", n, err)
	}
}

// A statement that is not kept and does not touch the schema leaves what the
// connector knows about the kept statements alone.
func TestLongInsertDoesNotExpireKeptStatements(t *testing.T) {
	db, c := openTables(t, 16, "")
	schema := c.schema.Load()
	pad := make([]byte, maxQueryLen)
	for i := range pad {
		pad[i] = ' '
	}
	if got := moved(t, c, func() { mustExec(t, db, `INSERT INTO a(n) VALUES (1)`+string(pad)) }); got != "abc" {
		t.Fatalf("a long insert moved %q, want abc", got)
	}
	if c.schema.Load() != schema {
		t.Fatal("a long insert expired the kept statements")
	}
	mustExec(t, db, `CREATE TABLE d (x)`)
	if c.schema.Load() == schema {
		t.Fatal("a schema change did not expire the kept statements")
	}
}

// A write that fails with SQLITE_BUSY never took the write lock, so it changed
// no row and moves no count. Another connection holds the lock here, and the
// busy timeout is 1 ms so the write fails at once.
func TestBusyWriteMovesNoCount(t *testing.T) {
	db, c := openTablesBusy(t, 16, 1, "")
	ctx := context.Background()
	var file string
	if err := db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&file); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", "file:"+file+"?_pragma=busy_timeout(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	holder, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	before := c.Stamp("a")
	if _, err := db.Exec(`INSERT INTO a(n) VALUES (1)`); err == nil || !strings.Contains(err.Error(), "SQLITE_BUSY") {
		t.Fatalf("insert under another connection's lock: %v, want SQLITE_BUSY", err)
	}
	if got := c.Stamp("a"); got != before {
		t.Fatalf("a busy insert moved the stamp of a: %d, was %d", got, before)
	}
	if _, err := holder.ExecContext(ctx, `COMMIT`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM a WHERE n=1`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a busy insert changed a row: %d %v", n, err)
	}
}

// execWhenFree runs query and runs it again while SQLite reports the database
// locked. SQLite does not queue writers, so under load one writer can wait out
// busy_timeout while the others keep taking the lock. A busy statement changed
// nothing, so the retry is the same write, and it counts once.
func execWhenFree(db *sql.DB, query string, args ...any) error {
	deadline := time.Now().Add(time.Minute)
	for {
		_, err := db.Exec(query, args...)
		if err == nil || !isBusy(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(time.Millisecond)
	}
}

// Several connections write and read at once. Every changed row is counted
// once, on the right table, and nothing races (run with -race). The busy
// timeout is short: a writer that loses the lock retries through execWhenFree
// rather than waiting inside SQLite.
func TestStampsWithSeveralConnections(t *testing.T) {
	db, c := openTablesBusy(t, 16, 10, "")
	db.SetMaxOpenConns(4)
	const workers, each = 4, 60
	startA, startB := c.Stamp("a"), c.Stamp("b")
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				n := w*1000 + i
				table := []string{"a", "b"}[w%2]
				if err := execWhenFree(db, `INSERT INTO `+table+`(n) VALUES (?)`, n); err != nil {
					t.Error(err)
					return
				}
				var got int
				if err := db.QueryRow(`SELECT count(*) FROM `+table+` WHERE n=?`, n).Scan(&got); err != nil || got != 1 {
					t.Errorf("%d %v", got, err)
					return
				}
				_ = c.Stamp("a", "b")
			}
		}(w)
	}
	wg.Wait()
	if got := c.Stamp("a") - startA; got != each*2 {
		t.Errorf("a counted %d writes, want %d", got, each*2)
	}
	if got := c.Stamp("b") - startB; got != each*2 {
		t.Errorf("b counted %d writes, want %d", got, each*2)
	}
	if got := c.Stamp("c"); got != c.Stamp("c") {
		t.Error("c moved")
	}
}
