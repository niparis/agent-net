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
// The Connector also counts the statements that may change the database, one
// count for each table, so a cache above it can tell that the tables it read
// are unchanged. SQLite tells the connector which tables a statement reads and
// writes when the statement is prepared (the authorizer), including the tables
// that triggers write. When the connector cannot tell, it counts a write for
// every table, so the fallback is always safe.
package sqlitecache

import (
	"bytes"
	"container/list"
	"context"
	"database/sql/driver"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"modernc.org/sqlite"
)

// maxQueryLen is the longest SQL text that is kept. Longer texts are scripts
// or one-off statements.
const maxQueryLen = 16 << 10

// preUpdateRegisterer is what the modernc connection offers for the pre-update
// hook (see Connector.preUpdate).
type preUpdateRegisterer interface {
	RegisterPreUpdateHook(sqlite.PreUpdateHookFn)
}

// Connector opens connections for sql.OpenDB.
type Connector struct {
	inner driver.Connector
	size  int

	// writes counts every statement that may change the database. global
	// counts the ones whose tables are not known; it is part of every Stamp.
	// schema counts the changes of the schema, which expire what the
	// connector knows about a kept statement.
	writes atomic.Uint64
	global atomic.Uint64
	schema atomic.Uint64
	// tables counts every write to a table, whatever its columns.
	tables sync.Map // lower case table name -> *atomic.Uint64
	// wide counts the writes to a table that name no column (an INSERT, a
	// DELETE): they move every column of the table.
	wide sync.Map // lower case table name -> *atomic.Uint64
	// cols counts the changes of a column's value. The key is table.column.
	cols sync.Map // lower case table.column -> *atomic.Uint64
	// names keeps the column names of the tables statements write.
	names sync.Map // lower case table name -> *tableNames
	// watched holds the tables that a column key names. The pre-update hook
	// counts only these: a table no key reads by column costs nothing per row.
	watched sync.Map // lower case table name -> struct{}

	views    atomic.Pointer[viewSet]
	observer atomic.Pointer[func(Statement)]
}

// New returns a connector for the SQLite database dsn. Each connection keeps
// up to size prepared statements; size <= 0 turns the cache off, and the
// connector still counts writes (for every table, as it cannot place them).
func New(dsn string, size int) (*Connector, error) {
	inner, err := sqlite.NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	return &Connector{inner: inner, size: size}, nil
}

// Writes is the number of statements run so far that may have changed the
// database: everything except SELECT, VALUES, EXPLAIN, PRAGMA, a WITH
// statement without INSERT, UPDATE, DELETE or REPLACE, and an INSERT, UPDATE or
// DELETE that reports no changed row. It only goes up. A statement inside a
// transaction counts when it runs, and a rolled back one counts too; that only
// makes a cache refresh when it did not have to.
func (c *Connector) Writes() uint64 { return c.writes.Load() }

// Stamp is a number that changes whenever a statement that may change one of
// the keys has run. A key is a table name, in any case, or table.column. Read
// it before reading data you keep, and keep it with the data: the data is
// current while Stamp of the same keys gives the same number.
//
// A write counts for the tables SQLite says the statement changes, which
// includes the tables its triggers change. A statement that ran with Exec and
// changed no row counts for none. A write the connector cannot place
// (a schema change, a script, a statement it does not keep, a savepoint)
// counts for every table, so Stamp of any keys moves.
//
// A table key moves for every write to the table. A table.column key moves when
// a write changes the value of that column in a row, and for any INSERT or
// DELETE of the table, since those write every column of a row. A write that
// leaves a column's value as it was does not move it, and neither does a write
// to other columns. So a column key is right for a value that depends on the
// columns it names and on which rows exist, and on nothing else of the table.
//
// The number is a sum of counts that only go up, so it is the same exactly
// when none of them moved. It is not a count of writes.
func (c *Connector) Stamp(keys ...string) uint64 {
	sum := c.global.Load()
	for _, k := range keys {
		if i := strings.IndexByte(k, '.'); i >= 0 {
			c.watch(k[:i])
			sum += c.count(&c.wide, k[:i]).Load() + c.count(&c.cols, k).Load()
			continue
		}
		sum += c.count(&c.tables, k).Load()
	}
	return sum
}

// watch marks table as one that a column key names, so the hook counts its rows.
// A table is watched before the key is read, so every write after that read is
// counted, and a write before it is in the data the read loads.
func (c *Connector) watch(table string) {
	table = strings.ToLower(table)
	if _, ok := c.watched.Load(table); !ok {
		c.watched.LoadOrStore(table, struct{}{})
	}
}

// count returns the counter of key in m, made when it is first used.
func (c *Connector) count(m *sync.Map, key string) *atomic.Uint64 {
	key = strings.ToLower(key)
	if v, ok := m.Load(key); ok {
		return v.(*atomic.Uint64)
	}
	v, _ := m.LoadOrStore(key, new(atomic.Uint64))
	return v.(*atomic.Uint64)
}

// Statement describes a statement the connector ran, for SetObserver.
type Statement struct {
	Query string
	// Known is false when the connector could not work out the tables: a
	// script, or a driver without an authorizer. Reads and Writes are then
	// empty.
	Known bool
	// Reads and Writes are the sorted, lower case names of the tables SQLite
	// says the statement reads and writes, triggers and views included.
	Reads, Writes []string
	// ReadColumns are the sorted table.column names of the columns the
	// statement reads. A table that is read through no column is in Reads
	// only.
	ReadColumns []string
	// Schema is true for a statement that changes the schema.
	Schema bool
}

// SetObserver sets a function that is called with every statement before it
// runs. It is for tests and diagnostics: a nil fn turns it off. The function
// runs on the caller's goroutine and must not use the database.
func (c *Connector) SetObserver(fn func(Statement)) {
	if fn == nil {
		c.observer.Store(nil)
		return
	}
	c.observer.Store(&fn)
}

// stmtInfo is what the authorizer told about one statement.
type stmtInfo struct {
	reads, writes []string
	readColumns   []string
	counters      []*atomic.Uint64 // the counts of writes, one for each table
	schemaChange  bool
	// rowCounted is true when the statement changes tables only (none of the
	// names it writes is a view), so it changed nothing when it reports that
	// no row was changed.
	rowCounted bool
}

// record counts the write of a statement that has run. info is nil when the
// connector does not know the statement's tables.
func (c *Connector) record(info *stmtInfo, query string) {
	switch {
	case info == nil:
		if mayWrite(query) {
			c.writes.Add(1)
			c.global.Add(1)
			if mayChangeSchema(query) {
				c.schema.Add(1)
			}
		}
	case info.schemaChange:
		c.writes.Add(1)
		c.schema.Add(1)
		c.global.Add(1)
	case len(info.counters) > 0:
		c.writes.Add(1)
		for _, n := range info.counters {
			n.Add(1)
		}
	case mayWrite(query):
		// A statement that names no table and is not a read: BEGIN, COMMIT,
		// SAVEPOINT, VACUUM.
		c.writes.Add(1)
		c.global.Add(1)
	}
}

// recordExec counts the write of an exec that has run. A statement that
// changed no row changed nothing: a trigger or a foreign key action only runs
// for a row that changed.
func (c *Connector) recordExec(info *stmtInfo, query string, res driver.Result, err error) {
	if err == nil && info != nil && info.rowCounted && res != nil {
		if n, rerr := res.RowsAffected(); rerr == nil && n == 0 {
			return
		}
	}
	c.record(info, query)
}

// observe calls the observer, if there is one.
func (c *Connector) observe(query string, info *stmtInfo) {
	f := c.observer.Load()
	if f == nil {
		return
	}
	st := Statement{Query: query}
	if info != nil {
		st.Known, st.Reads, st.Writes, st.Schema = true, info.reads, info.writes, info.schemaChange
		st.ReadColumns = info.readColumns
	}
	(*f)(st)
}

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
	// Without the hook the connector could not count the columns a write
	// changes, and a column stamp would never move.
	hooks, ok := inner.(preUpdateRegisterer)
	if !ok {
		inner.Close()
		return nil, driver.ErrBadConn
	}
	hooks.RegisterPreUpdateHook(c.preUpdate)
	cc.exec, _ = inner.(driver.ExecerContext)
	cc.query, _ = inner.(driver.QueryerContext)
	// Without an authorizer the connector cannot place a write, and counts it
	// for every table.
	if reg, ok := inner.(sqlite.AuthorizerRegisterer); ok {
		if reg.RegisterAuthorizer(cc.authorize) == nil {
			cc.authorized = true
		}
	}
	return cc, nil
}

// Driver implements driver.Connector.
func (c *Connector) Driver() driver.Driver { return c.inner.Driver() }

type entry struct {
	query string
	stmt  driver.Stmt
	busy  bool
	elem  *list.Element
	info  *stmtInfo // nil when the connector does not know the tables
	// schema is the schema count when the statement was prepared. The tables
	// of a statement change with the schema (a trigger), so an entry made
	// before a schema change is prepared again.
	schema uint64
}

type conn struct {
	inner      driver.Conn
	begin      driver.ConnBeginTx
	prep       driver.ConnPrepareContext
	exec       driver.ExecerContext
	query      driver.QueryerContext
	owner      *Connector
	authorized bool
	collect    atomic.Pointer[collector]

	mu    sync.Mutex
	cache map[string]*list.Element // query -> element holding *entry
	order *list.List               // front is the newest
}

func (c *conn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *conn) Begin() (driver.Tx, error) { return c.inner.Begin() } //nolint:staticcheck

func (c *conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.begin.BeginTx(ctx, opts)
}

// PrepareContext prepares a statement for a caller that keeps it (sql.Stmt).
// Its writes are counted like the others.
func (c *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	schema := c.owner.schema.Load()
	st, info, err := c.prepare(ctx, query)
	if err != nil {
		return nil, err
	}
	return &countedStmt{Stmt: st, owner: c.owner, query: query, info: info, schema: schema}, nil
}

// countedStmt counts the writes of a statement the caller prepared.
type countedStmt struct {
	driver.Stmt
	owner  *Connector
	query  string
	info   *stmtInfo
	schema uint64
}

// current is what the statement is known to touch. A schema change since the
// statement was prepared may have given it other tables, so it is then unknown.
func (s *countedStmt) current() *stmtInfo {
	if s.schema != s.owner.schema.Load() {
		return nil
	}
	return s.info
}

func (s *countedStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.owner.observe(s.query, s.info)
	res, err := s.Stmt.Exec(args) //nolint:staticcheck
	s.owner.recordExec(s.current(), s.query, res, err)
	return res, err
}

func (s *countedStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.owner.observe(s.query, s.info)
	defer func() { s.owner.record(s.current(), s.query) }()
	return s.Stmt.Query(args) //nolint:staticcheck
}

func (s *countedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	s.owner.observe(s.query, s.info)
	res, err := s.Stmt.(driver.StmtExecContext).ExecContext(ctx, args)
	s.owner.recordExec(s.current(), s.query, res, err)
	return res, err
}

func (s *countedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	s.owner.observe(s.query, s.info)
	defer func() { s.owner.record(s.current(), s.query) }()
	return s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
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
// saves nothing. A semicolon in a comment or a string does not make a script.
func (c *conn) cacheable(query string) bool {
	if c.owner.size <= 0 || len(query) > maxQueryLen {
		return false
	}
	return strings.IndexByte(query, ';') < 0 || !isScript(query)
}

// take returns a statement for query that nobody else is using, and whether
// it stays in the cache (a statement made because the kept one was busy is
// closed after use). It returns nil when query is not kept.
func (c *conn) take(ctx context.Context, query string) (*entry, bool, error) {
	if !c.cacheable(query) {
		return nil, false, nil
	}
	schema := c.owner.schema.Load()
	c.mu.Lock()
	if el, ok := c.cache[query]; ok {
		e := el.Value.(*entry)
		switch {
		case e.busy:
			c.mu.Unlock()
			// Same text, already running: use a statement of its own.
			st, info, err := c.prepare(ctx, query)
			if err != nil {
				return nil, false, err
			}
			return &entry{query: query, stmt: st, busy: true, info: info, schema: schema}, false, nil
		case e.schema == schema:
			e.busy = true
			c.order.MoveToFront(el)
			c.mu.Unlock()
			return e, true, nil
		}
		// The schema changed since the statement was prepared: its tables may
		// have changed too. Prepare it again.
		c.order.Remove(el)
		delete(c.cache, query)
		c.mu.Unlock()
		e.stmt.Close()
		c.mu.Lock()
	}
	c.mu.Unlock()
	st, info, err := c.prepare(ctx, query)
	if err != nil {
		return nil, false, err
	}
	e := &entry{query: query, stmt: st, busy: true, info: info, schema: schema}
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
	e, kept, err := c.take(ctx, query)
	if err != nil {
		return nil, err
	}
	if e == nil {
		defer c.owner.record(nil, query)
		c.describe(ctx, query)
		return c.exec.ExecContext(ctx, query, args)
	}
	defer c.done(e, kept)
	c.owner.observe(query, e.info)
	res, err := e.stmt.(driver.StmtExecContext).ExecContext(ctx, args)
	c.owner.recordExec(e.info, query, res, err)
	return res, err
}

func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	e, kept, err := c.take(ctx, query)
	if err != nil {
		return nil, err
	}
	if e == nil {
		defer c.owner.record(nil, query)
		c.describe(ctx, query)
		return c.query.QueryContext(ctx, query, args)
	}
	defer c.owner.record(e.info, query)
	c.owner.observe(query, e.info)
	r, err := e.stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
	if err != nil {
		c.done(e, kept)
		return nil, err
	}
	return &rows{Rows: r, c: c, e: e, kept: kept}, nil
}

// describe tells the observer about a statement that is not kept. It prepares
// the statement a second time, so it only works when somebody is watching.
func (c *conn) describe(ctx context.Context, query string) {
	if c.owner.observer.Load() == nil {
		return
	}
	var info *stmtInfo
	if !isScript(query) {
		if st, i, err := c.prepare(ctx, query); err == nil {
			st.Close()
			info = i
		}
	}
	c.owner.observe(query, info)
}

// isScript says whether query holds more than one statement. A semicolon in a
// string, a quoted name or a comment does not count.
func isScript(query string) bool {
	semi := false
	for i := 0; i < len(query); i++ {
		switch ch := query[i]; {
		case ch == '\'' || ch == '"' || ch == '`':
			// A doubled quote inside a quoted text is an escaped quote, and
			// reads as the end of one text and the start of the next.
			for i++; i < len(query) && query[i] != ch; i++ {
			}
			if semi {
				return true
			}
		case ch == '[':
			for i++; i < len(query) && query[i] != ']'; i++ {
			}
			if semi {
				return true
			}
		case ch == '-' && i+1 < len(query) && query[i+1] == '-':
			for i += 2; i < len(query) && query[i] != '\n'; i++ {
			}
		case ch == '/' && i+1 < len(query) && query[i+1] == '*':
			j := strings.Index(query[i+2:], "*/")
			if j < 0 {
				return semi
			}
			i += j + 3
		case ch == ';':
			semi = true
		case ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n':
		default:
			if semi {
				return true
			}
		}
	}
	return false
}

// collector gathers what the authorizer reports while one statement is
// prepared.
type collector struct {
	reads, writes map[string]struct{}
	readCols      map[string]struct{} // table.column
	schemaChange  bool
}

// authorize is the SQLite authorizer of the connection. It only watches: it
// collects the tables of the statement being prepared by prepare, and allows
// everything.
func (c *conn) authorize(action sqlite.AuthorizerActionCode, arg1, arg2, _, _ string) sqlite.AuthorizerReturnCode {
	col := c.collect.Load()
	if col == nil {
		return sqlite.AuthorizerOK
	}
	switch action {
	case sqlite.AuthRead:
		table := strings.ToLower(arg1)
		addName(&col.reads, table)
		if arg2 != "" {
			addName(&col.readCols, table+"."+strings.ToLower(arg2))
		}
	case sqlite.AuthInsert, sqlite.AuthUpdate, sqlite.AuthDelete:
		// The columns a write changes are counted by the pre-update hook, row
		// by row, so only the table is named here.
		addName(&col.writes, arg1)
	case sqlite.AuthCreateIndex, sqlite.AuthCreateTable, sqlite.AuthCreateTempIndex, sqlite.AuthCreateTempTable,
		sqlite.AuthCreateTempTrigger, sqlite.AuthCreateTempView, sqlite.AuthCreateTrigger, sqlite.AuthCreateView,
		sqlite.AuthDropIndex, sqlite.AuthDropTable, sqlite.AuthDropTempIndex, sqlite.AuthDropTempTable,
		sqlite.AuthDropTempTrigger, sqlite.AuthDropTempView, sqlite.AuthDropTrigger, sqlite.AuthDropView,
		sqlite.AuthAlterTable, sqlite.AuthReindex, sqlite.AuthAnalyze, sqlite.AuthAttach, sqlite.AuthDetach,
		sqlite.AuthCreateVTable, sqlite.AuthDropVTable:
		col.schemaChange = true
	}
	return sqlite.AuthorizerOK
}

// prepare prepares query and works out its tables. The info is nil when the
// driver has no authorizer.
func (c *conn) prepare(ctx context.Context, query string) (driver.Stmt, *stmtInfo, error) {
	if !c.authorized {
		st, err := c.prep.PrepareContext(ctx, query)
		return st, nil, err
	}
	col := &collector{}
	c.collect.Store(col)
	st, err := c.prep.PrepareContext(ctx, query)
	c.collect.Store(nil)
	if err != nil {
		return nil, nil, err
	}
	info := &stmtInfo{reads: sortedNames(col.reads), writes: sortedNames(col.writes), readColumns: sortedNames(col.readCols), schemaChange: col.schemaChange}
	for _, w := range info.writes {
		info.counters = append(info.counters, c.owner.count(&c.owner.tables, w))
	}
	// The pre-update hook numbers the columns of a row, so the names of the
	// tables this statement writes (triggers included) are known before it runs.
	c.rememberColumns(ctx, info.writes)
	if len(info.writes) > 0 && !info.schemaChange {
		info.rowCounted = c.noViews(ctx, info.writes)
	}
	return st, info, nil
}

// viewSet is the names of the views, as of one schema count.
type viewSet struct {
	schema uint64
	names  map[string]struct{}
}

// noViews says whether none of the names is a view. Writing through a view
// runs INSTEAD OF triggers and the row count of the statement does not say
// what they changed. It is false when the views cannot be listed.
func (c *conn) noViews(ctx context.Context, names []string) bool {
	schema := c.owner.schema.Load()
	vs := c.owner.views.Load()
	if vs == nil || vs.schema != schema {
		if c.query == nil {
			return false
		}
		rows, err := c.query.QueryContext(ctx, `SELECT lower(name) FROM sqlite_master WHERE type='view' UNION SELECT lower(name) FROM sqlite_temp_master WHERE type='view'`, nil)
		if err != nil {
			return false
		}
		vs = &viewSet{schema: schema, names: map[string]struct{}{}}
		dest := make([]driver.Value, 1)
		for rows.Next(dest) == nil {
			if n, ok := dest[0].(string); ok {
				vs.names[n] = struct{}{}
			}
		}
		rows.Close()
		c.owner.views.Store(vs)
	}
	for _, n := range names {
		if _, isView := vs.names[n]; isView {
			return false
		}
	}
	return true
}

// tableNames are the column names of a table, in the order the pre-update hook
// numbers them, as of one schema count.
type tableNames struct {
	schema uint64
	cols   []string
}

// rememberColumns keeps the column names of tables for the current schema. It
// runs before a statement, outside the hook, because the hook may not query the
// database. A table whose names are not known is counted as a whole when it is
// written (see preUpdate).
func (c *conn) rememberColumns(ctx context.Context, tables []string) {
	if c.query == nil {
		return
	}
	schema := c.owner.schema.Load()
	for _, t := range tables {
		key := strings.ToLower(t)
		if v, ok := c.owner.names.Load(key); ok && v.(*tableNames).schema == schema {
			continue
		}
		rows, err := c.query.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, []driver.NamedValue{{Ordinal: 1, Value: t}})
		if err != nil {
			continue
		}
		var cols []string
		dest := make([]driver.Value, 1)
		for rows.Next(dest) == nil {
			if n, ok := dest[0].(string); ok {
				cols = append(cols, strings.ToLower(n))
			}
		}
		rows.Close()
		if len(cols) > 0 {
			c.owner.names.Store(key, &tableNames{schema: schema, cols: cols})
		}
	}
}

// columnNames returns the column names of table in the order the pre-update
// hook numbers them, when they are known for the current schema.
func (c *Connector) columnNames(table string) ([]string, bool) {
	v, ok := c.names.Load(table)
	if !ok {
		return nil, false
	}
	tn := v.(*tableNames)
	if tn.schema != c.schema.Load() {
		return nil, false
	}
	return tn.cols, true
}

// preUpdate counts the rows a statement changes. SQLite calls it for each row
// before the change, with the old and the new values of every column. An
// UPDATE moves the count of each column whose value changed, so an UPDATE
// that sets a column to the value it has moves nothing for it. An INSERT or a
// DELETE moves every column of the table. When the column names are not known,
// a write counts for every column of its table.
func (c *Connector) preUpdate(d sqlite.SQLitePreUpdateData) {
	table := strings.ToLower(d.TableName)
	if _, ok := c.watched.Load(table); !ok {
		return
	}
	names, known := c.columnNames(table)
	n := d.Count()
	if sqlite.AuthorizerActionCode(d.Op) != sqlite.AuthUpdate || !known || len(names) != n {
		c.count(&c.wide, table).Add(1)
		return
	}
	old, nw := make([]any, n), make([]any, n)
	if d.Old(old...) != nil || d.New(nw...) != nil {
		c.count(&c.wide, table).Add(1)
		return
	}
	for i := range old {
		if !sameValue(old[i], nw[i]) {
			c.count(&c.cols, table+"."+names[i]).Add(1)
		}
	}
}

// sameValue says whether two column values are the same. The driver gives
// int64, float64, string, []byte or nil, and the common cases are compared
// without reflection: a row is compared on every write.
func sameValue(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case int64:
		y, ok := b.(int64)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	case []byte:
		y, ok := b.([]byte)
		return ok && bytes.Equal(x, y)
	}
	return reflect.DeepEqual(a, b)
}

// addName adds name, in lower case, to the set *m. The set is made on first use.
func addName(m *map[string]struct{}, name string) {
	if *m == nil {
		*m = map[string]struct{}{}
	}
	(*m)[strings.ToLower(name)] = struct{}{}
}

func sortedNames(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
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

// mayChangeSchema says whether a statement the connector knows nothing about
// may change the schema: a script, or a statement that starts like one that
// does. A long INSERT does not.
func mayChangeSchema(query string) bool {
	word, _ := firstWord(query)
	switch strings.ToUpper(word) {
	case "CREATE", "ALTER", "DROP", "REINDEX", "ANALYZE", "ATTACH", "DETACH", "VACUUM":
		return true
	}
	return strings.IndexByte(query, ';') >= 0 && isScript(query)
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
