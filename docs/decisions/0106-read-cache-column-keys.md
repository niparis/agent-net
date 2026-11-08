# 0106 - The read cache depends on columns, and more reads are kept

Date: 2026-11-08
Status: Built on the branch of worker w_1b84912bf312fde5. Not yet released.

## Context

Decision 0105 keeps a read until one of its tables is written. Every
session.upsert writes `sessions`, so the graph and the agent list reloaded on
every report, although the graph shows four columns of `sessions`
(`repo_project`, `repo_root`, `archived_at`, `graph_excluded`) and an upsert
rewrites the same values. The reads that were not cached (WorkerLog,
ListInbox, Agent(id), the extra work of visibleGraph and the database part of
NodesInfo) cost most of the time of a live round.

## Decision

- **A key may name a column.** `readThrough` takes keys such as
  `sessions.repo_project`. `Connector.Stamp` takes the same keys. A column key
  moves when a write changes the value of that column in a row, and for any
  INSERT or DELETE of the table, since those write whole rows.
- **Column changes come from the pre-update hook, not the authorizer.** The
  authorizer names the columns an UPDATE sets, but it cannot tell that a value
  stayed the same, and an upsert (`INSERT ... ON CONFLICT DO UPDATE`) is an
  INSERT to SQLite. The modernc driver offers `RegisterPreUpdateHook`, which
  gives the old and the new value of every column of each changed row. The
  connector counts the columns whose value changed. A write it cannot place
  (no names for the table, or a count of columns that does not match) counts for
  every column of the table.
- **The SQL does not change.** Other ways were considered and rejected. Splitting
  the upsert into an UPDATE for known rows and an INSERT for new ones would have
  changed which triggers fire: `repo_agent_session_update` and
  `session_identity_repo_update` run on every write that names `repo_project`,
  and skipping that write when the value is the same changes `repo_id` in edge
  cases. Writing the graph columns only when they differ was not possible while
  the upsert stays an INSERT.
- **Each cached read names its columns.** The guard test reads the columns each
  read touches (`SQLITE_READ` gives table and column) and fails when a column is
  not named. The graph names its four columns of `sessions`. Table keys stay
  for the reads that use whole rows (the agent list, the visible graph).
- **More reads are kept.** WorkerLog, ListInbox, Agent(id), visibleGraph,
  Calibration and RecentWorkerFailures are cached with `readThrough`, each with a
  table set beside its queries. Time-dependent reads use `timed=true`, as before.
  NodesInfo as a whole is not cached: it reads the usage reports held in memory
  and the live connections, which no table write shows. Its two costly database
  reads (Calibration and RecentWorkerFailures) are cached instead.

## Consequences

- A session.upsert that keeps status, last_seen and the other columns the graph
  does not read keeps the graph. The agent list and the reads that use whole
  `sessions` rows still reload on every upsert.
- A column that a read does not name may change without moving the read. A
  read that starts to use another column must name it; the guard test fails
  until it does.
- A table with a column the connector cannot name (a schema change since the
  statement was prepared) counts for every column. This is the safe fallback.
- The hook runs once per changed row, inside the statement. It reads every
  column of the row (66 driver calls for the 33 columns of `sessions`). Its cost
  on a session.upsert is not settled; see Measured.
- `docs/security.md` is unchanged. No limit or check moved.

## Tests

- `internal/sqlitecache/columns_test.go`: a column moves only when its value
  changes; an INSERT, a DELETE and an upsert that changes a value are counted
  per column; a trigger's writes count for the columns they touch; the names
  are followed after a column is dropped; the observer reports read columns.
- `internal/gateway/readcache_tables_test.go`: every cached read, including the
  six added here, declares every table and column it reads, on the rich store
  and on a copy of the live database (`AGENT_NET_BENCH_DB`); every declared key
  invalidates its read (a column key by a changed value); a session.upsert that
  changes no graph column keeps the graph; a change of repo root or a new
  session reloads it; a write to `last_seen` and `status` keeps it.
- The declare test fails when one graph column is removed from its set. This was
  checked by hand.

## Measured

`BenchmarkRealLiveMix` on a copy of the live database, medians of three
alternating runs, each run on a fresh copy. The before tree is the branch base
(62282d5f8). Both trees skip the schema version guard, because the live copy is
at schema 154 and this branch knows 150. Benchmark only; the branch keeps the
guard. Host load was about 25.

CPU time per round, four passes per write round:

| Read (per round) | Before | After |
| --- | --- | --- |
| all reads | 304 ms | 174 ms |
| query.worker_log | 146 ms | 72 ms |
| query.agents | 74 ms | 72 ms |
| graph.get | 44 ms | 19 ms |
| inbox.list | 23 ms | 11 ms |
| query.nodes | 21 ms | 12 ms |
| validToolOwner | 9 ms | 4 ms |
| session.upsert (a write) | 9 ms | 16 ms (noisy: 8 to 20 ms) |

One pass per write round:

| Read (per round) | Before | After |
| --- | --- | --- |
| all reads | 99 ms | 95 ms |
| query.worker_log | 33 ms | 35 ms |
| query.agents | 34 ms | 36 ms |
| graph.get | 15 ms | 12 ms |
| inbox.list | 6 ms | 6 ms |
| query.nodes | 4 ms | 5 ms |
| validToolOwner | 2 ms | 2 ms |
| session.upsert (a write) | 18 ms | 11 ms (noisy: 9 to 23 ms) |

With one pass, each pass follows a write, so every key is stale and nothing is
kept. The benchmark moves the clock 250 ms per pass, so a timed read is also
loaded once per wall second: in the four-pass case that is about once per round
for each timed read. That is why `query.agents` and `query.worker_log` still cost
one load each per round.

### Session upsert cost: not settled

One `session.upsert` on a fixture store, best of three rounds, one process per
tree: before (62282d5f8) 1.18 ms and 0.85 ms; after 1.19 ms and 1.12 ms. The
host load was 23 to 26 and the runs were not interleaved, so these do not show
whether the hook adds cost. A timing of one single-row UPDATE of `sessions` on
both trees, alternating, was stopped at the memory limit before it finished.
Measure it again before release; if the hook costs more than a few percent on
an upsert, the next step is to skip the hook for tables no column key watches
and to look again at the upsert's SQL.

The `-race` run from the brief was not done in this session for the same
reason (memory). Run it before release.
