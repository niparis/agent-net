package sqlitecache

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// openPair opens a writer on a connector and a reader on the same file through
// a second, separate connection, as the gateway does with its read pool.
func openPair(t testing.TB) (w *sql.DB, c *Connector, r *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	c, err := New(dsn, 16)
	if err != nil {
		t.Fatal(err)
	}
	w = sql.OpenDB(c)
	w.SetMaxOpenConns(1)
	t.Cleanup(func() { w.Close() })
	mustExec(t, w, `CREATE TABLE t (n INTEGER PRIMARY KEY, name TEXT NOT NULL DEFAULT '', other TEXT NOT NULL DEFAULT '')`)
	mustExec(t, w, `INSERT INTO t(n, name) VALUES (1, 'old')`)
	r, err = sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	r.SetMaxOpenConns(2)
	t.Cleanup(func() { r.Close() })
	return w, c, r
}

func readName(t testing.TB, r *sql.DB) string {
	t.Helper()
	var name string
	if err := r.QueryRow(`SELECT name FROM t WHERE n=1`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	return name
}

// A cache reads the stamp, loads on another connection, and keeps what it
// loaded under that stamp. While a transaction is open the other connection
// still reads the data from before it. So the stamp must not move before the
// commit: if it did, the load would be kept under the new stamp and stay
// there after the commit, a stale value until the next write.
func TestStampDoesNotMoveBeforeCommit(t *testing.T) {
	for _, key := range []string{"t", "t.name"} {
		t.Run(key, func(t *testing.T) {
			w, c, r := openPair(t)
			// Name the column first, as the first cached read does.
			c.Stamp(key)
			tx, err := w.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec(`UPDATE t SET name='new' WHERE n=1`); err != nil {
				t.Fatal(err)
			}
			// The cache: stamp, then load, then check the stamp and keep.
			stamp := c.Stamp(key)
			loaded := readName(t, r)
			kept := c.Stamp(key) == stamp
			if loaded != "old" {
				t.Fatalf("the reader sees %q before the commit, want old", loaded)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if kept && c.Stamp(key) == stamp {
				t.Fatalf("the cache kept %q under a stamp that did not move at the commit: stale after the commit, which wrote %q", loaded, readName(t, r))
			}
		})
	}
}

// The same when the table is named for the first time while the transaction is
// open: the update ran before the column was watched, and the commit must still
// move the column.
func TestStampMovesAtCommitForAColumnNamedInsideTheTransaction(t *testing.T) {
	w, c, r := openPair(t)
	tx, err := w.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE t SET name='new' WHERE n=1`); err != nil {
		t.Fatal(err)
	}
	stamp := c.Stamp("t.name") // first use: the table is watched from here
	loaded := readName(t, r)
	if loaded != "old" {
		t.Fatalf("the reader sees %q before the commit, want old", loaded)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if c.Stamp("t.name") == stamp {
		t.Fatal("the commit did not move a column that was named after the update ran")
	}
}

// A transaction that rolls back changed nothing, so it moves nothing.
func TestCommitRollbackMovesNothing(t *testing.T) {
	w, c, _ := openPair(t)
	keys := []string{"t", "t.name", "other"}
	c.Stamp("t.name")
	before := c.Writes()
	moved := movedKeys(t, c, keys, func() {
		tx, err := w.Begin()
		if err != nil {
			t.Fatal(err)
		}
		mustExecTx(t, tx, `UPDATE t SET name='new' WHERE n=1`)
		mustExecTx(t, tx, `INSERT INTO t(n) VALUES (2)`)
		mustExecTx(t, tx, `DELETE FROM t WHERE n=1`)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	})
	if len(moved) != 0 {
		t.Fatalf("a rolled back transaction moved %v", moved)
	}
	if c.Writes() != before {
		t.Fatalf("a rolled back transaction counted %d writes", c.Writes()-before)
	}
}

// Nothing moves while the transaction is open. The commit then moves what its
// statements changed, and only that.
func TestCommitMovesWhatTheTransactionChanged(t *testing.T) {
	w, c, _ := openPair(t)
	mustExec(t, w, `CREATE TABLE other (n INTEGER PRIMARY KEY)`)
	keys := []string{"t", "t.name", "t.other", "other"}
	c.Stamp("t.name")
	tx, err := w.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	mid := movedKeys(t, c, keys, func() {
		mustExecTx(t, tx, `UPDATE t SET name='new' WHERE n=1`)
	})
	if len(mid) != 0 {
		t.Fatalf("an open transaction moved %v", mid)
	}
	moved := movedKeys(t, c, keys, func() {
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
	if got, want := strings.Join(moved, ","), "t,t.name"; got != want {
		t.Fatalf("a commit of one UPDATE moved %s, want %s", got, want)
	}
}

// A statement outside a transaction moves its stamp when it ends, as before.
func TestCommitAutocommitStatementMovesAtItsEnd(t *testing.T) {
	w, c, r := openPair(t)
	c.Stamp("t.name")
	before := c.Stamp("t.name")
	mustExec(t, w, `UPDATE t SET name='new' WHERE n=1`)
	if c.Stamp("t.name") == before {
		t.Fatal("an autocommit update did not move the column")
	}
	if got := readName(t, r); got != "new" {
		t.Fatalf("the reader sees %q after the statement ended, want new", got)
	}
}

// A write that returns rows (INSERT ... RETURNING through Query) commits when
// the statement ends. The stamp must move when the reader can see the row, and
// not before: a stamp that moves early lets a cache keep the old data under it.
func TestCommitQueryWriteMovesWhenTheDataIsVisible(t *testing.T) {
	w, c, r := openPair(t)
	c.Stamp("t")
	before := c.Stamp("t")
	rows, err := w.Query(`INSERT INTO t(n, name) VALUES (5, 'five') RETURNING n`)
	if err != nil {
		t.Fatal(err)
	}
	visible := func() bool {
		var n int
		return r.QueryRow(`SELECT n FROM t WHERE n=5`).Scan(&n) == nil
	}
	check := func(when string) {
		t.Helper()
		v, moved := visible(), c.Stamp("t") != before
		if v && !moved {
			t.Fatalf("%s: the reader sees the row and the stamp did not move", when)
		}
		if !v && moved {
			t.Fatalf("%s: the stamp moved before the reader could see the row", when)
		}
	}
	check("after Query")
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		check("after Next")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	check("after Close")
	if c.Stamp("t") == before {
		t.Fatal("the stamp never moved for an INSERT through Query")
	}
}

// Two statements of a transaction count once the transaction commits, and a
// statement that fails inside it does not stop the others from counting.
func TestCommitAfterAFailedStatementStillMoves(t *testing.T) {
	w, c, _ := openPair(t)
	before := c.Stamp("t")
	tx, err := w.Begin()
	if err != nil {
		t.Fatal(err)
	}
	mustExecTx(t, tx, `INSERT INTO t(n) VALUES (7)`)
	if _, err := tx.Exec(`INSERT INTO t(n) VALUES (7)`); err == nil {
		t.Fatal("want a constraint error")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if c.Stamp("t") == before {
		t.Fatal("the commit did not move the table")
	}
}

func mustExecTx(t testing.TB, tx *sql.Tx, q string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// A transaction that is written as statements (BEGIN ... COMMIT) is followed
// the same way: nothing moves until COMMIT, and ROLLBACK drops it.
func TestHandWrittenTransactionsMoveAtCommit(t *testing.T) {
	w, c, _ := openPair(t)
	ctx := context.Background()
	conn, err := w.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	run := func(q string) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	c.Stamp("t.name")
	stamp := func() uint64 { return c.Stamp("t", "t.name") }
	start := stamp()
	step := func(q string, moves bool) {
		t.Helper()
		before := stamp()
		run(q)
		if moved := stamp() != before; moved != moves {
			t.Fatalf("%s: moved=%v, want %v", q, moved, moves)
		}
	}
	step(`BEGIN`, false)
	step(`UPDATE t SET name='a' WHERE n=1`, false)
	step(`COMMIT`, true)

	step(`BEGIN`, false)
	step(`UPDATE t SET name='b' WHERE n=1`, false)
	step(`ROLLBACK`, false)

	// A savepoint inside BEGIN does not commit when it is released.
	step(`BEGIN`, false)
	step(`SAVEPOINT one`, false)
	step(`UPDATE t SET name='c' WHERE n=1`, false)
	step(`RELEASE one`, false)
	step(`COMMIT`, true)

	// A savepoint outside BEGIN is the transaction: releasing the outer one
	// commits.
	step(`SAVEPOINT outer1`, false)
	step(`SAVEPOINT inner1`, false)
	step(`UPDATE t SET name='d' WHERE n=1`, false)
	step(`RELEASE inner1`, false)
	step(`UPDATE t SET name='e' WHERE n=1`, false)
	step(`RELEASE outer1`, true)

	// RELEASE of the outer one closes the inner ones too.
	step(`SAVEPOINT outer2`, false)
	step(`SAVEPOINT inner2`, false)
	step(`UPDATE t SET name='f' WHERE n=1`, false)
	step(`RELEASE outer2`, true)

	// ROLLBACK TO leaves the transaction open.
	step(`BEGIN`, false)
	step(`SAVEPOINT sp`, false)
	step(`UPDATE t SET name='g' WHERE n=1`, false)
	step(`ROLLBACK TO sp`, false)
	step(`COMMIT`, true)

	if stamp() == start {
		t.Fatal("nothing moved")
	}
	// Back to autocommit: a statement moves at its end.
	step(`UPDATE t SET name='h' WHERE n=1`, true)
}

// A reader connector shares the counts of the connector it was made from, runs
// the same statements, and cannot write.
func TestPoolReaderConnectorSharesTheStampAndCannotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	c, err := New(dsn, 16)
	if err != nil {
		t.Fatal(err)
	}
	w := sql.OpenDB(c)
	w.SetMaxOpenConns(1)
	defer w.Close()
	mustExec(t, w, `CREATE TABLE t (n INTEGER PRIMARY KEY, name TEXT NOT NULL DEFAULT '')`)
	mustExec(t, w, `INSERT INTO t(n, name) VALUES (1, 'old')`)

	rc, err := c.NewReader("file:"+path+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)", 16)
	if err != nil {
		t.Fatal(err)
	}
	r := sql.OpenDB(rc)
	r.SetMaxOpenConns(3)
	defer r.Close()
	if rc.Stamp("t") != c.Stamp("t") {
		t.Fatal("the reader connector has its own stamp")
	}
	before := c.Stamp("t")
	mustExec(t, w, `UPDATE t SET name='new' WHERE n=1`)
	if rc.Stamp("t") == before {
		t.Fatal("a write on the writer did not move the reader's stamp")
	}
	if got := readName(t, r); got != "new" {
		t.Fatalf("the reader sees %q after the write, want new", got)
	}
	if _, err := r.Exec(`UPDATE t SET name='x'`); err == nil {
		t.Fatal("the reader wrote")
	}
	if _, err := r.Exec(`INSERT INTO t(n) VALUES (9)`); err == nil {
		t.Fatal("the reader inserted")
	}
	// A rejected write counts nothing.
	if rc.Stamp("t") != c.Stamp("t") || c.Stamp("t") == 0 {
		t.Fatal("stamps differ")
	}
	after := c.Stamp("t")
	r.Exec(`UPDATE t SET name='x'`)
	if c.Stamp("t") != after {
		t.Fatal("a write that the reader refused moved the stamp")
	}
	// The observer set on either side sees the statements of both.
	var seen int
	c.SetObserver(func(Statement) { seen++ })
	readName(t, r)
	mustExec(t, w, `UPDATE t SET name='y' WHERE n=1`)
	if seen != 2 {
		t.Fatalf("the observer saw %d statements, want 2", seen)
	}
}

// Readers keep a value under the stamp they read before loading, as a cache
// does, while a writer commits transactions. Whenever the writer has finished,
// a read that starts after it must not be served by a value that is older.
func TestPoolCacheIsNeverStaleAfterCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	c, err := New(dsn, 64)
	if err != nil {
		t.Fatal(err)
	}
	w := sql.OpenDB(c)
	w.SetMaxOpenConns(1)
	defer w.Close()
	mustExec(t, w, `CREATE TABLE t (n INTEGER PRIMARY KEY, v INTEGER NOT NULL DEFAULT 0, other INTEGER NOT NULL DEFAULT 0)`)
	mustExec(t, w, `INSERT INTO t(n) VALUES (1)`)
	rc, err := c.NewReader("file:"+path+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)", 64)
	if err != nil {
		t.Fatal(err)
	}
	r := sql.OpenDB(rc)
	r.SetMaxOpenConns(4)
	defer r.Close()

	type kept struct {
		stamp uint64
		v     int
	}
	var mu sync.Mutex
	cache := map[string]kept{}
	read := func(key string) int {
		stamp := rc.Stamp(key)
		mu.Lock()
		e, ok := cache[key]
		mu.Unlock()
		if ok && e.stamp == stamp {
			return e.v
		}
		var v int
		if err := r.QueryRow(`SELECT v FROM t WHERE n=1`).Scan(&v); err != nil {
			t.Error(err)
			return -1
		}
		if rc.Stamp(key) == stamp {
			mu.Lock()
			cache[key] = kept{stamp, v}
			mu.Unlock()
		}
		return v
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for _, key := range []string{"t", "t.v"} {
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						read(key)
					}
				}
			}()
		}
	}
	for i := 1; i <= 300; i++ {
		switch i % 3 {
		case 0:
			tx, err := w.Begin()
			if err != nil {
				t.Fatal(err)
			}
			mustExecTx(t, tx, `UPDATE t SET v=? WHERE n=1`, i)
			mustExecTx(t, tx, `UPDATE t SET other=other+1 WHERE n=1`)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		case 1:
			mustExec(t, w, `UPDATE t SET v=? WHERE n=1`, i)
		default:
			tx, err := w.Begin()
			if err != nil {
				t.Fatal(err)
			}
			mustExecTx(t, tx, `UPDATE t SET v=? WHERE n=1`, -i)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			mustExec(t, w, `UPDATE t SET v=? WHERE n=1`, i)
		}
		// The writer is done: every read now must give i.
		for _, key := range []string{"t", "t.v"} {
			if got := read(key); got != i {
				t.Fatalf("round %d: %s read %d after the write finished", i, key, got)
			}
		}
	}
	close(stop)
	wg.Wait()
}

func TestPoolIsRead(t *testing.T) {
	yes := []string{
		`SELECT 1`, "  select name FROM t WHERE n=?", "-- c\nSELECT 1", "/* c */ SELECT 1;",
		`VALUES (1)`, `WITH x AS (SELECT 1) SELECT * FROM x`, `SELECT 'a;b'`, `SELECT 1 -- a; b`,
	}
	no := []string{
		`INSERT INTO t VALUES (1)`, `UPDATE t SET a=1`, `DELETE FROM t`, `REPLACE INTO t VALUES (1)`,
		`WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x`, `WITH x AS (SELECT 1) DELETE FROM t`,
		`PRAGMA user_version`, `EXPLAIN SELECT 1`, `BEGIN`, `COMMIT`, `VACUUM`, ``,
		`SELECT 1; DELETE FROM t`, `CREATE TABLE x (a)`,
		`INSERT INTO t VALUES (1) RETURNING n`,
	}
	for _, q := range yes {
		if !IsRead(q) {
			t.Errorf("IsRead(%q) = false, want true", q)
		}
	}
	for _, q := range no {
		if IsRead(q) {
			t.Errorf("IsRead(%q) = true, want false", q)
		}
	}
}

// Statements that a reader keeps still give the right answer after the writer
// changes the schema or rewrites the file: a new column, a new index, a
// trigger, a VACUUM.
func TestPoolReaderKeepsWorkingAcrossSchemaChangesAndVacuum(t *testing.T) {
	w, c, r := openPair(t)
	rc, err := c.NewReader("file:"+filepath.Join(filepath.Dir(dbPathOf(t, w)), "t.db")+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)", 16)
	if err != nil {
		t.Fatal(err)
	}
	pool := sql.OpenDB(rc)
	pool.SetMaxOpenConns(2)
	defer pool.Close()
	_ = r
	read := func() string {
		t.Helper()
		var name string
		if err := pool.QueryRow(`SELECT name FROM t WHERE n=?`, 1).Scan(&name); err != nil {
			t.Fatal(err)
		}
		return name
	}
	if got := read(); got != "old" {
		t.Fatalf("read %q", got)
	}
	steps := []string{
		`ALTER TABLE t ADD COLUMN extra TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX t_name ON t(name)`,
		`CREATE TABLE log (n INTEGER)`,
		`CREATE TRIGGER t_log AFTER UPDATE ON t BEGIN INSERT INTO log VALUES (new.n); END`,
		`UPDATE t SET name='after-trigger' WHERE n=1`,
		`DROP INDEX t_name`,
		`VACUUM`,
		`UPDATE t SET name='after-vacuum' WHERE n=1`,
	}
	want := "old"
	for _, q := range steps {
		before := c.Stamp("t")
		mustExec(t, w, q)
		if strings.HasPrefix(q, "UPDATE") {
			want = strings.Split(q, "'")[1]
		}
		if got := read(); got != want {
			t.Fatalf("after %s the reader gave %q, want %q", q, got, want)
		}
		if c.Stamp("t") == before && !strings.HasPrefix(q, "CREATE TABLE") {
			t.Fatalf("%s did not move the stamp of t", q)
		}
	}
}

// dbPathOf returns the file of the database a handle is open on.
func dbPathOf(t testing.TB, db *sql.DB) string {
	t.Helper()
	var seq int
	var name, file string
	if err := db.QueryRow(`SELECT seq, name, file FROM pragma_database_list WHERE name='main'`).Scan(&seq, &name, &file); err != nil {
		t.Fatal(err)
	}
	return file
}
