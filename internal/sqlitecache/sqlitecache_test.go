package sqlitecache

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t testing.TB, size int) (*sql.DB, *Connector) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "t.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	c, err := New(dsn, size)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE t (n INTEGER PRIMARY KEY, name TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	return db, c
}

func TestRepeatedQueriesAndExecsGiveRightAnswers(t *testing.T) {
	db, _ := openTest(t, 16)
	for i := 0; i < 200; i++ {
		if _, err := db.Exec(`INSERT INTO t(n, name) VALUES (?, ?)`, i, fmt.Sprint("name", i)); err != nil {
			t.Fatal(err)
		}
		var name string
		if err := db.QueryRow(`SELECT name FROM t WHERE n=?`, i).Scan(&name); err != nil || name != fmt.Sprint("name", i) {
			t.Fatalf("row %d: %q %v", i, name, err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t WHERE n>=?`, 150).Scan(&count); err != nil || count != 50 {
		t.Fatalf("count %d %v", count, err)
	}
	// No row, then a row: a kept statement must not remember the last call.
	if err := db.QueryRow(`SELECT name FROM t WHERE n=?`, 1000).Scan(new(string)); err != sql.ErrNoRows {
		t.Fatalf("want no rows, got %v", err)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM t WHERE n=?`, 7).Scan(&name); err != nil || name != "name7" {
		t.Fatalf("%q %v", name, err)
	}
}

// The same text can run again while its first rows are still open, in a
// transaction. The two runs must not share a statement.
func TestSameTextWhileRowsAreOpen(t *testing.T) {
	db, _ := openTest(t, 16)
	for i := 1; i <= 6; i++ {
		db.Exec(`INSERT INTO t(n) VALUES (?)`, i)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	const q = `SELECT n FROM t WHERE n>? ORDER BY n`
	outer, err := tx.Query(q, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer outer.Close()
	var outerSeen, innerSeen int
	for outer.Next() {
		var n int
		if err := outer.Scan(&n); err != nil {
			t.Fatal(err)
		}
		outerSeen++
		inner, err := tx.Query(q, n)
		if err != nil {
			t.Fatal(err)
		}
		for inner.Next() {
			innerSeen++
		}
		inner.Close()
	}
	if outerSeen != 6 || innerSeen != 5+4+3+2+1+0 {
		t.Fatalf("outer %d inner %d", outerSeen, innerSeen)
	}
	// After both closed, the text is kept and works again.
	var c int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&c); err != nil || c != 6 {
		t.Fatalf("%d %v", c, err)
	}
}

func TestFailedStatementCanRunAgain(t *testing.T) {
	db, _ := openTest(t, 16)
	if _, err := db.Exec(`INSERT INTO t(n, name) VALUES (?, ?)`, 1, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t(n, name) VALUES (?, ?)`, 1, "dup"); err == nil {
		t.Fatal("want a constraint error")
	}
	if _, err := db.Exec(`INSERT INTO t(n, name) VALUES (?, ?)`, 2, "b"); err != nil {
		t.Fatalf("the statement did not recover: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestRowsClosedEarlyReleaseTheStatement(t *testing.T) {
	db, _ := openTest(t, 16)
	for i := 1; i <= 5; i++ {
		db.Exec(`INSERT INTO t(n) VALUES (?)`, i)
	}
	for i := 0; i < 50; i++ {
		rows, err := db.Query(`SELECT n FROM t ORDER BY n`)
		if err != nil {
			t.Fatal(err)
		}
		rows.Next() // read one row, then stop
		rows.Close()
	}
	var c int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&c); err != nil || c != 5 {
		t.Fatalf("%d %v", c, err)
	}
	// A write needs no open reader: this fails if a statement is still active.
	if _, err := db.Exec(`DELETE FROM t WHERE n=1`); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaChangeAfterTheStatementWasKept(t *testing.T) {
	db, _ := openTest(t, 16)
	db.Exec(`INSERT INTO t(n, name) VALUES (1, 'a')`)
	read := func() int {
		rows, err := db.Query(`SELECT * FROM t`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		return len(cols)
	}
	if got := read(); got != 2 {
		t.Fatalf("%d columns", got)
	}
	if _, err := db.Exec(`ALTER TABLE t ADD COLUMN extra TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != 3 {
		t.Fatalf("%d columns after the change", got)
	}
}

func TestMoreTextsThanTheCacheKeeps(t *testing.T) {
	db, _ := openTest(t, 4)
	for i := 0; i < 40; i++ {
		// Each text is different, as when a list is built into the SQL.
		var got int
		if err := db.QueryRow(fmt.Sprintf(`SELECT %d`, i)).Scan(&got); err != nil || got != i {
			t.Fatalf("%d: %d %v", i, got, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO t(n) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
}

func TestScriptsRunWhole(t *testing.T) {
	db, _ := openTest(t, 16)
	script := `INSERT INTO t(n, name) VALUES (1, 'a'); INSERT INTO t(n, name) VALUES (2, 'b');`
	for i := 0; i < 2; i++ {
		if _, err := db.Exec(`DELETE FROM t`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(script); err != nil {
			t.Fatal(err)
		}
		var c int
		if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&c); err != nil || c != 2 {
			t.Fatalf("%d %v", c, err)
		}
	}
}

func TestCacheOffStillWorksAndCountsWrites(t *testing.T) {
	db, c := openTest(t, 0)
	before := c.Writes()
	db.Exec(`INSERT INTO t(n) VALUES (1)`)
	if c.Writes() != before+1 {
		t.Fatalf("writes %d -> %d", before, c.Writes())
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestWritesCountsOnlyStatementsThatMayChangeData(t *testing.T) {
	db, c := openTest(t, 16)
	start := c.Writes()
	reads := []string{
		`SELECT 1`, ` select 2`, "-- comment\nSELECT 3", "/* c */ SELECT 4", `VALUES (1)`, `EXPLAIN SELECT 1`,
		`PRAGMA page_count`, `WITH x(a) AS (SELECT 1) SELECT a FROM x`,
	}
	for _, q := range reads {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		rows.Close()
	}
	if got := c.Writes(); got != start {
		t.Fatalf("reads counted as %d writes", got-start)
	}
	writes := []func(){
		func() { db.Exec(`INSERT INTO t(n) VALUES (10)`) },
		func() { db.Exec(`UPDATE t SET name='x' WHERE n=10`) },
		func() { db.Exec(`DELETE FROM t WHERE n=10`) },
		func() { db.Exec(`INSERT INTO t(n) VALUES (11) RETURNING n`) },
		func() { r, _ := db.Query(`INSERT INTO t(n) VALUES (12) RETURNING n`); r.Close() },
		func() {
			r, _ := db.Query(`WITH x(a) AS (SELECT 13) INSERT INTO t(n) SELECT a FROM x RETURNING n`)
			r.Close()
		},
		func() { db.Exec(`CREATE TABLE u (a)`) },
	}
	for i, w := range writes {
		before := c.Writes()
		w()
		if c.Writes() != before+1 {
			t.Fatalf("write %d counted %d times", i, c.Writes()-before)
		}
	}
}

func TestTransactionAndContext(t *testing.T) {
	db, c := openTest(t, 16)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	before := c.Writes()
	tx.Exec(`INSERT INTO t(n) VALUES (1)`)
	if c.Writes() != before {
		t.Fatal("a write in a transaction was counted before its commit")
	}
	tx.Rollback()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rollback: %d %v", n, err)
	}
	if c.Writes() != before {
		t.Fatal("a rolled back write was counted")
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	tx.Exec(`INSERT INTO t(n) VALUES (1)`)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if c.Writes() != before+1 {
		t.Fatalf("a committed write was counted %d times", c.Writes()-before)
	}
	db.Exec(`DELETE FROM t`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := 0; i < 5; i++ {
		db.Exec(`INSERT INTO t(n) VALUES (?)`, i)
	}
	rows, err := db.QueryContext(ctx, `SELECT n FROM t ORDER BY n`)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	for rows.Next() {
	}
	rows.Close()
	var c2 int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&c2); err != nil || c2 != 5 {
		t.Fatalf("after cancel: %d %v", c2, err)
	}
}

func TestColumnTypesStillWork(t *testing.T) {
	db, _ := openTest(t, 16)
	db.Exec(`INSERT INTO t(n, name) VALUES (1, 'a')`)
	rows, err := db.Query(`SELECT n, name FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INTEGER" {
		t.Fatalf("%v %v", types, err)
	}
}

func TestCloseWithKeptStatements(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "c.db")
	c, err := New(dsn, 16)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	db.SetMaxOpenConns(1)
	db.Exec(`CREATE TABLE t (n INTEGER)`)
	for i := 0; i < 5; i++ {
		db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM t WHERE n > %d`, i)).Scan(new(int))
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// Parsing is what the cache saves. Run this to see it:
//
//	go test ./internal/sqlitecache -bench Parse -benchmem
func benchSelect(b *testing.B, size int) {
	db, _ := openTest(b, size)
	for i := 0; i < 100; i++ {
		db.Exec(`INSERT INTO t(n, name) VALUES (?, 'x')`, i)
	}
	q := `SELECT t.n, t.name, (SELECT COUNT(*) FROM t u WHERE u.n < t.n) FROM t WHERE t.n >= ? AND t.name <> '' ORDER BY t.n LIMIT 5`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.Query(q, i%90)
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
		}
		rows.Close()
	}
}

func BenchmarkParseEveryCall(b *testing.B) { benchSelect(b, 0) }
func BenchmarkKeepStatements(b *testing.B) { benchSelect(b, 64) }
