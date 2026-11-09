package gateway

import (
	"context"
	"database/sql"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Conn returns the writer connection, the one every write waits for. Tests
// hold it to make the writer busy.
func (d *storeDB) Conn(ctx context.Context) (*sql.Conn, error) { return d.w.Conn(ctx) }

// holdAllConnections takes the writer connection and every read connection of
// the store, so any statement waits. The returned function gives them back; it
// is safe to call twice.
func holdAllConnections(t testing.TB, s *Store) func() {
	t.Helper()
	ctx := context.Background()
	var conns []*sql.Conn
	w, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	conns = append(conns, w)
	if r := s.db.r.Load(); r != nil {
		for i := 0; i < r.Stats().MaxOpenConnections; i++ {
			c, err := r.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			conns = append(conns, c)
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			for _, c := range conns {
				c.Close()
			}
		})
	}
}

func setOwnerSetting(t testing.TB, q interface {
	Exec(string, ...any) (sql.Result, error)
}, key, value string) {
	t.Helper()
	if _, err := q.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,?,0) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
		t.Fatal(err)
	}
}

// readCachedSetting reads one owner setting through the read cache, as a cached
// read of the store does. keys are the tables or columns the load reads.
func readCachedSetting(s *Store, cacheKey string, keys []string, setting string) (string, error) {
	v, err := s.readThrough(cacheKey, keys, false, func() (any, error) {
		var value string
		err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key=?`, setting).Scan(&value)
		if err == sql.ErrNoRows {
			return "", nil
		}
		return value, err
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

// A read must not wait for a write, nor for the other reads. This holds the
// writer connection (a long transaction would do the same) and reads.
func TestPoolReadsDoNotWaitForTheWriterConnection(t *testing.T) {
	s, _ := newTestStore(t)
	if s.db.r.Load() == nil {
		t.Skip("the read pool is off")
	}
	setOwnerSetting(t, s.db, "k", "v1")
	w, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	done := make(chan string, 1)
	go func() {
		var v string
		s.db.QueryRow(`SELECT value FROM owner_settings WHERE key='k'`).Scan(&v)
		got, _ := readCachedSetting(s, "pool-test", []string{"owner_settings"}, "k")
		done <- v + "/" + got
	}()
	select {
	case got := <-done:
		if got != "v1/v1" {
			t.Fatalf("read %q while the writer was held, want v1/v1", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a read waited for the writer connection")
	}
}

// A write goes to the writer: it waits for the writer connection, and a
// statement that is not a plain read never runs on a pool connection.
func TestPoolWritesAndTransactionsRunOnTheWriter(t *testing.T) {
	s, _ := newTestStore(t)
	r := s.db.r.Load()
	if r == nil {
		t.Skip("the read pool is off")
	}
	if _, err := r.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('x','y',0)`); err == nil {
		t.Fatal("a pool connection accepted a write")
	}
	for _, q := range []string{`INSERT INTO owner_settings(key,value,updated_at) VALUES('x','y',0) RETURNING key`, `PRAGMA user_version`, `WITH a AS (SELECT 1) INSERT INTO owner_settings(key,value,updated_at) SELECT 'z','y',0 FROM a`} {
		if s.db.reader(q) != s.db.w {
			t.Fatalf("%q is routed to the pool", q)
		}
	}
	// A transaction keeps its own reads: it sees what it wrote.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	setOwnerSetting(t, tx, "t", "in-tx")
	var v string
	if err := tx.QueryRow(`SELECT value FROM owner_settings WHERE key='t'`).Scan(&v); err != nil || v != "in-tx" {
		t.Fatalf("a read inside the transaction gave %q, %v", v, err)
	}
	// Outside it, nobody sees the row yet.
	if err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key='t'`).Scan(&v); err != sql.ErrNoRows {
		t.Fatalf("an uncommitted row is visible outside its transaction: %q, %v", v, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key='t'`).Scan(&v); err != nil || v != "in-tx" {
		t.Fatalf("a committed row is not visible: %q, %v", v, err)
	}
}

// With the pool off every statement runs on the writer, as before.
func TestReadPoolOffRunsEverythingOnTheWriter(t *testing.T) {
	old := readPoolMax
	readPoolMax = 0
	defer func() { readPoolMax = old }()
	s, _ := newTestStore(t)
	if s.db.r.Load() != nil {
		t.Fatal("the pool is open with readPoolMax 0")
	}
	setOwnerSetting(t, s.db, "k", "v")
	if got, err := readCachedSetting(s, "off", []string{"owner_settings"}, "k"); err != nil || got != "v" {
		t.Fatalf("%q %v", got, err)
	}
}

// The pool has min(readPoolMax, GOMAXPROCS) connections and at least one.
func TestReadPoolSize(t *testing.T) {
	s, _ := newTestStore(t)
	r := s.db.r.Load()
	if r == nil {
		t.Fatal("no read pool")
	}
	if got, want := r.Stats().MaxOpenConnections, readPoolSize(); got != want || got < 1 || got > 4 {
		t.Fatalf("pool size %d, want %d (1 to 4)", got, want)
	}
	if got := s.db.w.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("writer connections %d, want 1", got)
	}
}

// The stale read that a pool makes possible when a write is counted before it
// commits. A cached read loads while the writer's transaction is open and so
// gets the old data; the commit must move the stamp, or the old data stays.
func TestCachedReadIsFreshAfterACommitThatRanDuringItsLoad(t *testing.T) {
	for _, keys := range [][]string{{"owner_settings"}, {"owner_settings.value"}, nil} {
		name := "any write"
		if len(keys) > 0 {
			name = keys[0]
		}
		t.Run(name, func(t *testing.T) {
			s, _ := newTestStore(t)
			if s.db.r.Load() == nil {
				t.Skip("the read pool is off")
			}
			setOwnerSetting(t, s.db, "k", "v1")
			tx, err := s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			setOwnerSetting(t, tx, "k", "v2")
			// The read runs now, on a pool connection, while the transaction is
			// open: the data it loads is from before the transaction.
			got, err := readCachedSetting(s, "during", keys, "k")
			if err != nil || got != "v1" {
				t.Fatalf("read %q (%v) during the transaction, want v1", got, err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if got, err := readCachedSetting(s, "during", keys, "k"); err != nil || got != "v2" {
				t.Fatalf("read %q (%v) after the commit, want v2: the cache kept the old value", got, err)
			}
		})
	}
}

// A rolled back transaction leaves the cached value in place.
func TestCachedReadIsKeptAcrossARolledBackTransaction(t *testing.T) {
	s, _ := newTestStore(t)
	setOwnerSetting(t, s.db, "k", "v1")
	loads := 0
	read := func() {
		v, err := s.readThrough("rollback", []string{"owner_settings"}, false, func() (any, error) {
			loads++
			return "v", nil
		})
		if err != nil || v.(string) != "v" {
			t.Fatal(v, err)
		}
	}
	read()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	setOwnerSetting(t, tx, "k", "v2")
	read()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	read()
	if loads != 1 {
		t.Fatalf("the read loaded %d times across a rolled back write, want 1", loads)
	}
}

// Writers and readers at once. Each round the writer sets a new value, in a
// transaction or not, and when its call has returned every read, cached or
// not, must give that value or a newer one. A value from before the write is
// the stale read.
func TestPoolReadsAfterAWriteSeeItWhileReadersRun(t *testing.T) {
	s, _ := newTestStore(t)
	if s.db.r.Load() == nil {
		t.Skip("the read pool is off")
	}
	setOwnerSetting(t, s.db, "n", "0")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			keys := [][]string{{"owner_settings"}, {"owner_settings.value"}, nil}[i%3]
			for {
				select {
				case <-stop:
					return
				default:
					readCachedSetting(s, "stress", keys, "n")
				}
			}
		}(i)
	}
	defer func() { close(stop); wg.Wait() }()
	for i := 1; i <= 200; i++ {
		want := strconv.Itoa(i)
		if i%2 == 0 {
			tx, err := s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			setOwnerSetting(t, tx, "n", want)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		} else {
			setOwnerSetting(t, s.db, "n", want)
		}
		for _, keys := range [][]string{{"owner_settings"}, {"owner_settings.value"}, nil} {
			got, err := readCachedSetting(s, "stress", keys, "n")
			if err != nil {
				t.Fatal(err)
			}
			if n, _ := strconv.Atoi(got); n < i {
				t.Fatalf("round %d: a read gave %q after the write of %d returned", i, got, i)
			}
		}
		var direct string
		if err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key='n'`).Scan(&direct); err != nil || direct != want {
			t.Fatalf("round %d: a plain read gave %q, %v", i, direct, err)
		}
	}
}
