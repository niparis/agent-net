package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"sync/atomic"

	"github.com/niparis/agent-net/internal/sqlitecache"
)

// The store has one writer connection and a small pool of read-only
// connections to the same file (decision 0110). With one connection for
// everything, every read waited for every write and for every other read; the
// gateway was not short of CPU, it waited.
//
//   - The writer is one connection (MaxOpenConns 1). Every write, every
//     transaction and every statement that is not a plain read runs on it, in
//     the order the callers reach it, as before.
//   - A plain read (a SELECT, VALUES or WITH without a write) outside a
//     transaction runs on a pool connection. WAL gives it the data as of the
//     last commit, so a caller whose write has returned sees it. A read inside
//     a transaction stays on the transaction's connection.
//   - Nothing else changes for the callers: Store.db has the methods of
//     *sql.DB that the store uses, and sends each call to the right side.
//
// The read cache (readThrough) stays correct because the connector counts a
// write when it is visible to the pool, at the commit (see sqlitecache.pending).

// readPoolMax is the most read connections the store opens. The pool has
// min(readPoolMax, GOMAXPROCS) of them; 0 turns the pool off, so every read
// runs on the writer (tests and benchmarks).
var readPoolMax = 4

// readPoolSize is how many read connections to open.
func readPoolSize() int {
	n := min(readPoolMax, runtime.GOMAXPROCS(0))
	if readPoolMax > 0 && n < 1 {
		n = 1
	}
	return max(n, 0)
}

// storeDB is Store.db: the writer, and the read pool once it is open.
type storeDB struct {
	w *sql.DB
	r atomic.Pointer[sql.DB] // nil until openReadPool: start-up runs on the writer alone
}

// reader returns the connection pool that runs query, or the writer.
func (d *storeDB) reader(query string) *sql.DB {
	if r := d.r.Load(); r != nil && sqlitecache.IsRead(query) {
		return r
	}
	return d.w
}

func (d *storeDB) Exec(query string, args ...any) (sql.Result, error) {
	return d.w.Exec(query, args...)
}

func (d *storeDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.w.ExecContext(ctx, query, args...)
}

func (d *storeDB) Query(query string, args ...any) (*sql.Rows, error) {
	return d.reader(query).Query(query, args...)
}

func (d *storeDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.reader(query).QueryContext(ctx, query, args...)
}

func (d *storeDB) QueryRow(query string, args ...any) *sql.Row {
	return d.reader(query).QueryRow(query, args...)
}

func (d *storeDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.reader(query).QueryRowContext(ctx, query, args...)
}

func (d *storeDB) Begin() (*sql.Tx, error) { return d.w.Begin() }

func (d *storeDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return d.w.BeginTx(ctx, opts)
}

// Close closes the pool and then the writer. The writer closes last, so the
// last connection to the file checkpoints it.
func (d *storeDB) Close() error {
	if r := d.r.Swap(nil); r != nil {
		r.Close()
	}
	return d.w.Close()
}

// openReadPool opens the read pool of the store. It returns at once when the
// pool is turned off. Call it after start-up: the migrations and the repair
// steps run on the writer alone, and a reader opened earlier could see the
// schema before it is made.
func (s *Store) openReadPool() error {
	size := readPoolSize()
	if size == 0 {
		return nil
	}
	// The file is opened read-only, so a write that reaches the pool by a
	// mistake fails instead of running there. query_only says it again for a
	// connection that the driver opens another way. WAL is already on: the
	// writer set it, and it is a property of the file. The wait for a busy file
	// is the writer's.
	dsn := "file:" + s.path + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)"
	rc, err := s.connector.NewReader(dsn, stmtCacheSize)
	if err != nil {
		return fmt.Errorf("read pool: %w", err)
	}
	rdb := sql.OpenDB(rc)
	rdb.SetMaxOpenConns(size)
	rdb.SetMaxIdleConns(size)
	// Open one connection now, so a file that cannot be read this way is an
	// error at start-up and not at the first read.
	var version int
	if err := rdb.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&version); err != nil {
		rdb.Close()
		return fmt.Errorf("read pool: %w", err)
	}
	s.db.r.Store(rdb)
	return nil
}
