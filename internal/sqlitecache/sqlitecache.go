// Package sqlitecache opens SQLite connections that keep their prepared
// statements.
//
// database/sql prepares, runs and finalizes a statement for every Query and
// Exec call that has no *sql.Stmt. With SQLite that means the SQL text is
// parsed again on each call. For the gateway, which runs the same few hundred
// statements all day on one connection, parsing was a third of the CPU.
//
// A Connector wraps the modernc.org/sqlite driver. Each connection keeps the
// prepared statements of its last SQL texts and runs them again, so callers do
// not change. A statement that is still in use (its rows are open) is never
// shared: a second call with the same text gets a statement of its own.
//
// The Connector also counts the statements that may change the database, so a
// cache above it can tell that its data is stale without knowing which tables
// a write touched.
package sqlitecache

import (
	"container/list"
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"modernc.org/sqlite"
)

// maxQueryLen is the longest SQL text that is kept. Longer texts are scripts
// or one-off statements.
const maxQueryLen = 16 << 10

// Connector opens connections for sql.OpenDB.
type Connector struct {
	inner  driver.Connector
	size   int
	writes atomic.Uint64
}

// New returns a connector for the SQLite database dsn. Each connection keeps
// up to size prepared statements; size <= 0 turns the cache off, and the
// connector still counts writes.
func New(dsn string, size int) (*Connector, error) {
	inner, err := sqlite.NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	return &Connector{inner: inner, size: size}, nil
}

// Writes is the number of statements run so far that may have changed the
// database: everything except SELECT, VALUES, EXPLAIN, PRAGMA and a WITH
// statement without INSERT, UPDATE, DELETE or REPLACE. It only goes up.
// Read it before reading data you keep, and keep it with the data: the data is
// current while the number is the same. A statement inside a transaction counts
// when it runs, and a rolled back one counts too; that only makes a cache
// refresh when it did not have to.
func (c *Connector) Writes() uint64 { return c.writes.Load() }

// Connect implements driver.Connector.
func (c *Connector) Connect(ctx context.Context) (driver.Conn, error) {
	inner, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	cc := &conn{inner: inner, owner: c, cache: map[string]*list.Element{}, order: list.New()}
	// The modernc connection has all of these; keep the wrapper honest if a
	// later version drops one.
	var ok bool
	if cc.begin, ok = inner.(driver.ConnBeginTx); !ok {
		inner.Close()
		return nil, driver.ErrBadConn
	}
	if cc.prep, ok = inner.(driver.ConnPrepareContext); !ok {
		inner.Close()
		return nil, driver.ErrBadConn
	}
	cc.exec, _ = inner.(driver.ExecerContext)
	cc.query, _ = inner.(driver.QueryerContext)
	return cc, nil
}

// Driver implements driver.Connector.
func (c *Connector) Driver() driver.Driver { return c.inner.Driver() }

type entry struct {
	query string
	stmt  driver.Stmt
	busy  bool
	elem  *list.Element
}

type conn struct {
	inner driver.Conn
	begin driver.ConnBeginTx
	prep  driver.ConnPrepareContext
	exec  driver.ExecerContext
	query driver.QueryerContext
	owner *Connector

	mu    sync.Mutex
	cache map[string]*list.Element // query -> element holding *entry
	order *list.List               // front is the newest
}

func (c *conn) Prepare(query string) (driver.Stmt, error) { return c.inner.Prepare(query) }

func (c *conn) Begin() (driver.Tx, error) { return c.inner.Begin() } //nolint:staticcheck

func (c *conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.begin.BeginTx(ctx, opts)
}

func (c *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return c.prep.PrepareContext(ctx, query)
}

func (c *conn) Ping(ctx context.Context) error {
	if p, ok := c.inner.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *conn) ResetSession(ctx context.Context) error {
	if r, ok := c.inner.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *conn) IsValid() bool {
	if v, ok := c.inner.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

// Close finalizes the kept statements and closes the connection.
func (c *conn) Close() error {
	c.mu.Lock()
	for _, el := range c.cache {
		el.Value.(*entry).stmt.Close()
	}
	c.cache = map[string]*list.Element{}
	c.order.Init()
	c.mu.Unlock()
	return c.inner.Close()
}

// cacheable says whether a text is one statement worth keeping. A script
// (more than one statement) is not prepared whole by the driver, so keeping it
// saves nothing.
func (c *conn) cacheable(query string) bool {
	if c.owner.size <= 0 || len(query) > maxQueryLen {
		return false
	}
	return !strings.Contains(strings.TrimRight(query, " \t\r\n;"), ";")
}

// take returns a statement for query that nobody else is using, and whether
// it stays in the cache (a statement made because the kept one was busy is
// closed after use). It returns nil when query is not kept.
func (c *conn) take(ctx context.Context, query string) (*entry, bool, error) {
	if !c.cacheable(query) {
		return nil, false, nil
	}
	c.mu.Lock()
	if el, ok := c.cache[query]; ok {
		e := el.Value.(*entry)
		if !e.busy {
			e.busy = true
			c.order.MoveToFront(el)
			c.mu.Unlock()
			return e, true, nil
		}
		c.mu.Unlock()
		// Same text, already running: use a statement of its own.
		st, err := c.prep.PrepareContext(ctx, query)
		if err != nil {
			return nil, false, err
		}
		return &entry{query: query, stmt: st, busy: true}, false, nil
	}
	c.mu.Unlock()
	st, err := c.prep.PrepareContext(ctx, query)
	if err != nil {
		return nil, false, err
	}
	e := &entry{query: query, stmt: st, busy: true}
	c.mu.Lock()
	if _, dup := c.cache[query]; dup {
		// Another call kept the same text while this one prepared.
		c.mu.Unlock()
		return e, false, nil
	}
	e.elem = c.order.PushFront(e)
	c.cache[query] = e.elem
	c.evictLocked()
	c.mu.Unlock()
	return e, true, nil
}

// evictLocked drops the oldest idle statements while there are too many.
func (c *conn) evictLocked() {
	for el := c.order.Back(); el != nil && len(c.cache) > c.owner.size; {
		prev := el.Prev()
		if e := el.Value.(*entry); !e.busy {
			c.order.Remove(el)
			delete(c.cache, e.query)
			e.stmt.Close()
		}
		el = prev
	}
}

// done gives a statement back after its rows closed or its exec ended.
func (c *conn) done(e *entry, kept bool) {
	if !kept {
		e.stmt.Close()
		return
	}
	c.mu.Lock()
	e.busy = false
	c.evictLocked()
	c.mu.Unlock()
}

func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if mayWrite(query) {
		defer c.owner.writes.Add(1)
	}
	e, kept, err := c.take(ctx, query)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return c.exec.ExecContext(ctx, query, args)
	}
	defer c.done(e, kept)
	return e.stmt.(driver.StmtExecContext).ExecContext(ctx, args)
}

func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if mayWrite(query) {
		defer c.owner.writes.Add(1)
	}
	e, kept, err := c.take(ctx, query)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return c.query.QueryContext(ctx, query, args)
	}
	r, err := e.stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
	if err != nil {
		c.done(e, kept)
		return nil, err
	}
	return &rows{Rows: r, c: c, e: e, kept: kept}, nil
}

// rows gives its statement back when it closes.
type rows struct {
	driver.Rows
	c        *conn
	e        *entry
	kept     bool
	released bool
}

func (r *rows) Close() error {
	err := r.Rows.Close()
	if !r.released {
		r.released = true
		r.c.done(r.e, r.kept)
	}
	return err
}

func (r *rows) ColumnTypeDatabaseTypeName(i int) string {
	if t, ok := r.Rows.(driver.RowsColumnTypeDatabaseTypeName); ok {
		return t.ColumnTypeDatabaseTypeName(i)
	}
	return ""
}

func (r *rows) ColumnTypeLength(i int) (int64, bool) {
	if t, ok := r.Rows.(driver.RowsColumnTypeLength); ok {
		return t.ColumnTypeLength(i)
	}
	return 0, false
}

func (r *rows) ColumnTypeNullable(i int) (bool, bool) {
	if t, ok := r.Rows.(driver.RowsColumnTypeNullable); ok {
		return t.ColumnTypeNullable(i)
	}
	return false, false
}

func (r *rows) ColumnTypePrecisionScale(i int) (int64, int64, bool) {
	if t, ok := r.Rows.(driver.RowsColumnTypePrecisionScale); ok {
		return t.ColumnTypePrecisionScale(i)
	}
	return 0, 0, false
}

func (r *rows) ColumnTypeScanType(i int) reflect.Type {
	if t, ok := r.Rows.(driver.RowsColumnTypeScanType); ok {
		return t.ColumnTypeScanType(i)
	}
	return reflect.TypeOf(new(any)).Elem()
}

// mayWrite says whether a statement may change the database.
func mayWrite(query string) bool {
	word, rest := firstWord(query)
	switch strings.ToUpper(word) {
	case "SELECT", "VALUES", "EXPLAIN", "PRAGMA":
		return false
	case "WITH":
		for _, w := range []string{"INSERT", "UPDATE", "DELETE", "REPLACE"} {
			if containsWord(rest, w) {
				return true
			}
		}
		return false
	}
	return true
}

// firstWord skips spaces and SQL comments and returns the first word and what
// follows it.
func firstWord(q string) (string, string) {
	for {
		q = strings.TrimLeft(q, " \t\r\n")
		switch {
		case strings.HasPrefix(q, "--"):
			i := strings.IndexByte(q, '\n')
			if i < 0 {
				return "", ""
			}
			q = q[i+1:]
		case strings.HasPrefix(q, "/*"):
			i := strings.Index(q, "*/")
			if i < 0 {
				return "", ""
			}
			q = q[i+2:]
		default:
			end := 0
			for end < len(q) && (q[end] >= 'a' && q[end] <= 'z' || q[end] >= 'A' && q[end] <= 'Z') {
				end++
			}
			return q[:end], q[end:]
		}
	}
}

// containsWord reports whether s holds word as a whole word, in any case. A
// match inside a string literal counts, which only makes a statement look like
// a write.
func containsWord(s, word string) bool {
	up := strings.ToUpper(s)
	for from := 0; ; {
		i := strings.Index(up[from:], word)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(word)
		before := i == 0 || !isWordByte(up[i-1])
		after := end >= len(up) || !isWordByte(up[end])
		if before && after {
			return true
		}
		from = end
	}
}

func isWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}
