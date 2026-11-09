# 0107 - Reads run on a pool of read-only connections

Date: 2026-11-09
Status: Built on the branch of worker w_3d82b45246138828. Not yet released.

## Context

The gateway store had one SQLite connection (`SetMaxOpenConns(1)`). Every read
waited for every write and for every other read. On the live gateway (v0.4.208,
2026-11-08 23:28 UTC) `learned.list` took about 270 ms a call although its table
has 29 rows and the same call takes 2 ms of CPU on a copy. `query.agents` took
250 ms and up to 2.4 s under load, `session.upsert` about 1.1 s. The gateway
used 30 to 38% CPU. It was not short of CPU, it was waiting in a queue of one.

Decisions 0104 to 0106 made the read cache depend on a write count that moved
when a statement ran. That was safe only because of the single connection: a
reader could not load while a transaction was open, so it never loaded data
from before a change that the count already showed.

## Decision

- **One writer, a pool of readers.** `Store.db` is a `storeDB` (`store_pool.go`).
  It holds the writer (one connection, as before) and a second `*sql.DB` on the
  same file for reads. The pool has `min(4, GOMAXPROCS)` connections, at least
  one. `storeDB` has the methods of `*sql.DB` that the store uses, so the 600
  call sites do not change.
- **What goes where.** A plain read outside a transaction runs on the pool: a
  SELECT, VALUES, or a WITH without INSERT, UPDATE, DELETE or REPLACE
  (`sqlitecache.IsRead`). Everything else runs on the writer, in the order the
  callers reach it: writes, `INSERT ... RETURNING`, PRAGMA, EXPLAIN, VACUUM,
  scripts, and every transaction with the reads inside it. A word in a string
  literal can send a read to the writer, never the other way.
  This serves the pure-read frames (`parallelFrames`) and every `readThrough`
  load, and also the reads that write frames do between their writes.
- **The pool is read-only.** Its DSN is `mode=ro` with `query_only(1)`, and
  `busy_timeout(5000)` like the writer. WAL is a property of the file and the
  writer sets it, so the pool does not. A write that reaches the pool by a
  mistake fails. Pure Go (modernc), no cgo.
- **Start-up stays on the writer.** The pool opens at the end of `OpenStore`,
  after the migrations and the repair steps. The schema version guard
  (`migrate`, `SchemaVersion`) runs on the writer.
- **A write counts when it is visible, not when it runs.** This is the change
  that makes the pool safe. A reader reads the stamp, loads, and keeps the data
  under the stamp if the stamp did not move. With a pool, a load can run while a
  transaction is open and read the data from before it. If the stamp had
  already moved, the old data would be kept under the new stamp and stay there
  after the commit. So `sqlitecache` keeps the counts of each connection in
  `pending` and moves them:
  - at COMMIT, for a transaction. A rollback drops them. A commit that fails
    moves them anyway: a count that moves for nothing costs a load, a count
    that does not move for a change that is there is a stale read.
  - at the end of the statement, for a statement outside a transaction. For a
    write that returns rows (`INSERT ... RETURNING` through Query) the end is
    `rows.Close`, because SQLite commits when the statement finishes.
  - for hand-written transactions (`BEGIN`, `SAVEPOINT`, `RELEASE`, `COMMIT`,
    `END`, `ROLLBACK` run as statements), by following the statement text.
    A savepoint inside `BEGIN` does not commit when released; the outermost
    savepoint of a transaction that a `SAVEPOINT` started does. The gateway does
    not use these, `database/sql` transactions are followed exactly.
  The pre-update hook, which counts changed columns, only collects into
  `pending` now. A table that no key named by column when the hook saw its row
  is remembered; if a key names it before the commit, the commit moves every
  column of it, since the hook did not see that row for the key.
- **Readers share the stamp.** `Connector.NewReader` makes the connector of the
  pool. It shares the counts, the column names and the observer of the writer's
  connector, keeps prepared statements like it, and counts nothing.
- **The rule of the cache stays.** `readThrough` keeps a value only if the stamp
  did not move while it loaded. With counts that move at commit, a value kept
  under a stamp holds every write the stamp counts.

## Why a read after a write sees the write

The write call returns after the commit. The lane answers the node after that,
and a client that reads next starts a statement on a pool connection, which in
WAL mode reads the last committed state. The counts moved before the write call
returned, so a cached read misses and loads. `TestLanePoolReadSeesAWriteOnceItIsAnswered`
checks it through the node connection.

Reads of one node can still overtake its earlier writes that are not answered
yet. That was already so after 0104.

## Consequences

- A read does not wait for a write, and a long transaction no longer stops the
  reads. A read sees the data as of its start, which does not include a
  transaction that is still open.
- Code that opened a transaction and then called another store method that reads
  would have waited forever on the single connection. Now that read runs on the
  pool and sees the data from before the transaction. No such code exists
  (it would have hung), but it is a new way to be wrong.
- Four more connections, each with its own prepared statements (at most 1,024
  texts, as the writer). A pool connection holds a snapshot while its rows are
  open, so a caller that keeps rows open for long holds back the checkpoint of
  the log.
- Under saturated readers the writer pays a little. Its own reads queue for a
  pool connection behind the readers' statements. See the numbers below.
- `docs/security.md` and `docs/reference.md` are unchanged: no limit, check or
  setting moved.

## Tests

- `internal/sqlitecache/commit_test.go`: a stamp does not move before the
  commit (table and column keys); a column named inside the transaction moves at
  the commit; a rollback moves nothing; an autocommit statement moves at its
  end; an `INSERT ... RETURNING` moves when its data is visible and not before;
  hand-written transactions and savepoints; the reader connector shares the
  stamp and cannot write; a cache over a pool of readers is never stale after a
  write (race); readers keep working across schema changes and a VACUUM.
- `internal/gateway/store_pool_test.go`: reads do not wait for the writer
  connection; writes, PRAGMA and `RETURNING` go to the writer; a transaction
  sees its own rows and nobody else does until the commit; **a cached read
  loaded while a transaction is open is fresh after the commit**; a rolled back
  transaction keeps the cache; readers and a writer at once; the pool size.
- `TestServeKeepsReadingWhileRequestsWaitForTheDatabase` holds the writer and
  every pool connection now.

The stale-read tests fail when the count moves when the statement runs (checked
by moving it back): `TestStampDoesNotMoveBeforeCommit` ("the cache kept old
under a stamp that did not move at the commit"),
`TestCachedReadIsFreshAfterACommitThatRanDuringItsLoad` ("read v1 after the
commit, want v2") and the two stress tests.

## Measured

`BenchmarkRealPoolLatency` (`perf_pool_bench_test.go`) on a copy of the live
database (368 sessions, 8,233 messages), 8 s a run, medians of three
alternating runs, wall time of each call. Four readers cycle through
`query.agents`, `learned.list` and `graph.get`; one writer calls `session.upsert`
every 5 ms. "Before" is the same binary with the pool off (`readPoolMax = 0`):
one connection for everything. `learned.list` is the handler's work: the owner
check and `OwnerLearned`.

Wall time in ms, p50 / p95, medians of three alternating runs. `n` is the number
of calls in 8 s (all runs of the median pair are within 5% of it).

**Readers wait 1 ms between calls (the pool is saturated), cache on:**

| Call | Before p50 / p95 | After p50 / p95 | Calls in 8 s, before / after |
| --- | --- | --- | --- |
| query.agents | 50.6 / 71.2 | 31.1 / 40.4 | 296 / 584 |
| learned.list | 29.2 / 64.9 | 6.8 / 12.8 | 293 / 583 |
| graph.get | 18.5 / 43.7 | 13.0 / 18.4 | 294 / 582 |
| session.upsert (writer) | 24.1 / 52.2 | 25.0 / 40.5 | 267 / 254 |

**Same, cache off (every read goes to the database, as `learned.list` did live):**

| Call | Before p50 / p95 | After p50 / p95 | Calls in 8 s, before / after |
| --- | --- | --- | --- |
| query.agents | 30.8 / 49.4 | 27.9 / 34.5 | 162 / 413 |
| learned.list | 90.5 / 141.6 | 20.2 / 27.4 | 163 / 410 |
| graph.get | 73.1 / 103.9 | 25.1 / 31.9 | 162 / 411 |
| session.upsert (writer) | 12.6 / 28.7 | 21.0 / 29.5 | 392 / 293 |

**Readers wait 25 ms between calls (a TUI and a CTO polling), cache on:**

| Call | Before p50 / p95 | After p50 / p95 |
| --- | --- | --- |
| query.agents | 35.3 / 46.4 | 21.3 / 31.3 |
| learned.list | 18.8 / 50.0 | 5.5 / 11.1 |
| graph.get | 12.0 / 23.5 | 10.6 / 15.5 |
| session.upsert (writer) | 11.3 / 24.9 | 10.6 / 20.1 |

**Same, cache off:**

| Call | Before p50 / p95 | After p50 / p95 |
| --- | --- | --- |
| query.agents | 29.5 / 43.2 | 20.9 / 29.6 |
| learned.list | 64.6 / 120.5 | 13.6 / 21.9 |
| graph.get | 51.0 / 88.4 | 17.9 / 25.4 |
| session.upsert (writer) | 10.8 / 24.1 | 12.9 / 20.4 |

Reads are faster in every row, and twice as many finish in the same time when
the readers are saturated. The cost is in one cell: with the cache off and
saturated readers the writer's median goes from 12.6 to 21.0 ms (its 95th
percentile stays at 29 ms). A pool of 8 instead of 4 gives the same 20 to 22 ms
(two runs each), so it is not the writer waiting for a pool connection. The
cause is not found. The readers do 2.5 times more work in parallel, so CPU or
the allocator inside the SQLite port are the suspects. With the cache on, which
is how the gateway runs, the writer's median is the same and its 95th
percentile is lower, and at a polling pace of 25 ms it is lower in both cases.

The host was shared (12 cores; the load average was 4.5 when the runs began and
8.9 over the five minutes before), and the absolute times move with it. Compare the rows of one table, not the tables.
The "before" runs use the pool-off switch of the same binary
(`AGENT_NET_BENCH_POOL=0`), so they carry the commit-time counts too. Those do
not change the cost of a statement.

To repeat: copy `gateway.db` and `gateway.db-wal` of a gateway to a temporary
folder, remove the schema versions above what the branch knows from the copy if
the gateway is newer, and run

```
AGENT_NET_BENCH_DB=<copy> go test ./internal/gateway -run XXX -bench RealPoolLatency -benchtime 1x
```

Settings are in the comment of `BenchmarkRealPoolLatency`.
