# 0107 - The read cache keeps its value across writes to other tables

Date: 2026-11-08
Status: Built on the branch of worker w_c95aeef5c493061e. Not yet released.

## Context

Decision 0104 keeps the graph, the settings snapshot and the agent list until
the next write. It keys every kept value on one counter: the number of
statements that may change the database. On the live gateway (v0.4.202, 20 s
CPU profile) that counter moved many times a minute, because session.upsert,
message acknowledgements, event rows and delivery steps all write. A kept value
rarely lived long enough to be read twice. The gateway used 30% CPU, and
`Store.readThrough` was 25% of the samples.

## Decision

- **Writes are counted per table.** `internal/sqlitecache` asks SQLite which
  tables a statement reads and writes. SQLite says so when it prepares the
  statement (the authorizer), and it includes the tables that triggers write.
  The connector prepares each statement once and keeps the answer with the
  statement. `Connector.Stamp(tables...)` returns one number for a set of
  tables. It changes exactly when one of them was written.
- **When the connector cannot tell, it counts every table.** A schema change, a
  script, a statement it does not keep and a statement that names no table
  (BEGIN, SAVEPOINT, VACUUM) move the stamp of every table. So the fallback
  invalidates everything.
- **A statement that changed no row counts for nothing.** Many writes of the
  gateway match no row. `PlaybookNotes`, which `query.nodes` calls, runs an
  UPDATE on every call that matches a row only rarely; it used to move the
  counter on every call. A trigger or a foreign key action only runs for a
  row that changed, so nothing else changed either. The exception is a write
  through a view: its triggers write tables the row count does not show, so
  such a statement always counts.
- **A kept statement follows the schema.** The tables of a statement change
  with a trigger. A schema change makes the connector prepare a kept statement
  again before it is used.
- **A semicolon in a comment or a string does not make a script.** The
  `INSERT INTO sessions` of every session.upsert has one in a comment, so the
  connector never kept it: it was parsed again on each call. It is kept now,
  and its tables are known.
- **A cached read declares its tables.** `readThrough(key, tables, timed, load)`
  keeps a value while the stamp of its tables is the same as when it was read.
  The sets are `settingsTables`, `graphTables` and `agentsTables`. Each is built
  from lists that sit next to the queries they describe (`graphNodesTables`,
  `charterTables`, `loopTables`, `pendingTables`, `delegationTables`,
  `agentListTables`, `nodesTables`, `shortAgentChartersTables`). A set that is
  empty means any write.
- **The rest of 0104 stays.** A value is kept only if no write to its tables ran
  while it loaded. The agent list is kept for one second at most. Each caller
  gets a copy.

A wrong set is a stale read, so the sets are tested three ways
(`internal/gateway/readcache_tables_test.go`):

1. `TestCachedReadsDeclareEveryTableTheyRead` runs each cached read on a store
   that takes every branch (delegation with models, charters, edges, a loop, a
   waiting request, graph refs on). It asks SQLite which tables each statement
   reads and fails when one is not declared. With `AGENT_NET_BENCH_DB` set it
   also runs on a copy of the live database.
2. `TestCachedReadsAreInvalidatedByEveryDeclaredTable` writes a row to each
   declared table and checks the read loads again.
3. `TestSchemaHasNoWritesTheConnectorMisses` fails if the schema gets a view or
   foreign keys are switched on.

The guard sees statements that run. A statement on a branch the test store does
not take is not seen, so when you add a query to a cached read, read the call
tree and add the table.

## Consequences

- A write to a table outside the set leaves the value alone. Acknowledgements,
  events, deliveries, the Desk log, worker runs and delivery steps no longer
  touch the graph or the settings snapshot. Acknowledgements write `messages`,
  which the agent list reads for pending requests, so they still reload that.
- **session.upsert still reloads the agent list and the graph.** Both read
  `sessions`, and an upsert writes it. The agent list shows `last_seen` and
  `status`, so it must reload. The graph reads four columns of `sessions`
  (`repo_project`, `repo_root`, `archived_at`, `graph_excluded`) and an upsert
  rewrites the same values. Comparing old and new values per column (SQLite's
  pre-update hook) would keep the graph through an upsert. That is left for
  later.
- The agent list is still capped at one second by the clock rule. Most of its
  time-dependent parts are a few lines of Go (the owner use decay, bot
  presence); the pending query and the model usage window also use the clock.
  Splitting the part that depends on the clock from the part that does not
  would let the rows live as long as their tables.
- `WorkerLog`, `NodesInfo`, `ListInbox` and `Agent(id)` (which `validToolOwner`
  calls) are not cached. In the live-mix benchmark they cost more than the
  three reads cached here. Each can use `readThrough` with its own table set.
- A kept read costs the stamp of its tables and nothing else: 0.3 us for the
  settings snapshot, 1.1 us for the graph and 3 us for the agent list, with no
  allocation (`BenchmarkReadThroughHit`).
- `docs/security.md` is unchanged: no limit or check moved.

## Measured

`BenchmarkRealLiveMix` and `BenchmarkFixtureLiveMix`
(`internal/gateway/perf_livemix_bench_test.go`). A round is one session.upsert,
one acknowledgement, then N passes of query.agents (two filters),
query.worker_log, query.nodes, graph.get, inbox.list and validToolOwner. The
store clock moves 250 ms per pass. Medians of six alternating runs, CPU time
per round, on a copy of the live database (host load 25 to 35):

| Round | Before | After |
| --- | --- | --- |
| 1 pass per write round | 93 ms | 88 ms |
| 4 passes per write round | 343 ms | 249 ms |
| 4 passes: query.agents | 190 ms | 53 ms |
| 4 passes: graph.get | 99 ms | 65 ms |

With one pass per write round every pass follows an upsert, so the agent list
and the graph reload, and the gain is small. With four passes the agent list and
the graph survive the acknowledgement and the `PlaybookNotes` update that each
`query.nodes` runs.
