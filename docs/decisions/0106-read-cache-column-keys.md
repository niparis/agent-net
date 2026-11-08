# 0106 - The read cache depends on columns, and more reads are kept

Date: 2026-11-08
Status: Built on the branch of worker w_1b84912bf312fde5. Not yet released.

## Context

Decision 0105 keeps a read until one of its tables is written. Every
session.upsert writes `sessions`, so the graph and the agent list reloaded on
every report, although the graph shows four columns of `sessions`
(`repo_project`, `repo_root`, `archived_at`, `graph_excluded`) and an upsert
rewrites the same values. The reads that were not cached (WorkerLog,
ListInbox, Agent(id), the extra work of visibleGraph, the database part of
NodesInfo, and learned.list) cost most of the time of a live round.

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
  (no names for the table, or a count of columns that does not match, or a
  schema change since the statement was prepared) counts for every column.
- **The hook counts only tables that a column key names.** A key names a table
  when a read first asks for it (`Stamp`). Until then the hook returns at once
  for that table. This keeps the per-row cost off the hot writes of tables that
  no read depends on by column (events, messages, deliveries). It does not help
  `sessions`, which the graph names.
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
  Calibration, RecentWorkerFailures and learned.list (`OwnerLearned`) are cached
  with `readThrough`, each with a table set beside its queries. The timed reads
  (all but learned.list) pass `timed=true`, as before. NodesInfo as a whole is not
  cached: it reads the usage reports held in memory and the live connections,
  which no table write shows. Its two costly database reads (Calibration and
  RecentWorkerFailures) are cached instead.
- **The clock check stays out of the cached rows.** `OwnerLearned` caches every
  suggestion, ready or not, and applies `ReadyAt > now` on each call. So a
  suggestion shows as soon as its time passes, with no write, and the rows need
  no clock rule. `TestLearnedListShowsAReadySuggestionWithoutAWrite` checks it.
  The same split would let the other timed reads keep their rows between
  seconds; that is not done here.

## Consequences

- A session.upsert that keeps status, last_seen and the other columns the graph
  does not read keeps the graph. The agent list and the reads that use whole
  `sessions` rows still reload on every upsert.
- A column that a read does not name may change without moving the read. A
  read that starts to use another column must name it; the guard test fails
  until it does.
- A table with a column the connector cannot name counts for every column. This
  is the safe fallback.
- The timed reads (WorkerLog, ListInbox, Agent, visibleGraph, Calibration,
  RecentWorkerFailures) are kept for one wall second at most. In the live-mix
  benchmark that is about one reload per round for each of them.
- `docs/security.md` is unchanged. No limit or check moved.

## Hook cost: above the 5% limit, not settled

The hook reads every column of each changed row (94 driver calls for the 47
columns of `sessions`). Five alternating runs per tree, one process per run,
CPU time per call, medians. The before tree is the branch base (62282d5f8), the
after tree is this branch. Both trees skip the schema version guard for the
benchmark only (see Measured). Each run first calls `Graph()`, as the gateway
does, so `sessions` is watched.

| Operation | Before | After | Change |
| --- | --- | --- | --- |
| session.upsert (one call) | 421 us (414 to 453) | 503 us (480 to 505) | +19% |
| single-row UPDATE of sessions | 17.5 us (16.2 to 18.3) | 35.3 us (30.1 to 36.6) | +100% |

Without the watch (no read names `sessions`), the hook returns at once for it and
the cost is gone: upsert 429 us before and 418 us after (medians), UPDATE 18.2 us
and 17.5 us. This is why the first run of this measurement showed no cost: the
test did not read the graph.

Against the live-mix round the absolute cost is small: about 80 us more for each
session.upsert, against the reads the round no longer does (about 130 ms of CPU
per round with four passes). The percentage still fails the limit, so the
choice is the owner's:

1. Accept the cost, as the net per-round CPU still falls.
2. Keep the graph's four columns out of the hook: a stamp row, kept by SQL
   triggers on `sessions` that fire only when a watched value changes. This
   needs a schema migration, and the connector must count that table by value.
3. Split the upsert, as above. This changes trigger firing and is not
   recommended.

Until the owner chooses, the hook stays as it is.

## Measured (live-mix, before the hook filter)

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

With one pass, each pass follows a write, so every key is stale and nothing is
kept. The benchmark moves the clock 250 ms per pass, so a timed read is also
loaded once per wall second: in the four-pass case that is about once per round
for each timed read. That is why `query.agents` and `query.worker_log` still cost
one load each per round. The session.upsert rows of this table were noisy (8 to
20 ms) and are not used; see the hook table above.

## learned.list

The owner's list (`OwnerLearned`) is cached on its rows, with the ready check
per call (see Decision). Copy of the live database with 29 suggestions, five
runs, medians:

| Call | Before (no cache) | After |
| --- | --- | --- |
| call with no write since the last one (cache hit), CPU | 2.0 ms | 0.26 ms |
| call after a write to a table the list reads (miss), CPU | 1.14 ms | 1.25 ms (+9%) |

Wall time for the hit case was 0.17 ms after and 1.55 ms before. The miss is
slightly slower: a miss now loads hidden suggestions as well, and the rows still
go through `copyOf`. The peer's live figure (about 270 ms a call) does not
reproduce on the copy, which has 29 rows and runs at 1 to 2 ms a call. The live
cost may come from a larger table, from host load, or both; the copy cannot say.

## query.session_text: not cached

It cannot be cached safely. The gateway relays the request to the target
machine's daemon and waits for its answer (`node.session_text`). The transcript
is read on that machine, and the gateway keeps no copy of the answer (see the
comment in `usage.go`). No table write shows a change to a transcript, and the
only other key would be time, which the cache rules rule out. A cache would
serve an old transcript.

## Tests

- `internal/sqlitecache/columns_test.go`: a column moves only when its value
  changes; an INSERT, a DELETE and an upsert that changes a value are counted per
  column; a trigger's writes count for the columns they touch; the names are
  followed after a column is dropped; a key made after an earlier write still
  moves with the next write; the observer reports read columns.
- `internal/gateway/readcache_tables_test.go`: every cached read, including the
  new ones, declares every table and column it reads, on the rich store, on a
  store with the phrase cleaner on, and on a copy of the live database
  (`AGENT_NET_BENCH_DB`); every declared key invalidates its read (a column key
  by a changed value); a session.upsert that changes no graph column keeps the
  graph; a change of repo root or a new session reloads it; a write to
  `last_seen` and `status` keeps it.
- `internal/gateway/learned_owner_test.go`: a ready suggestion shows without a
  write once its time passes.
- The declare test fails when one graph column is removed from its set (checked
  by hand, in a copy).

## Race

The brief's `-race` command, one test binary at a time (`-p 1`), on the final
tree: `internal/sqlitecache` ok (3 s) and `internal/gateway` ok (174 s), exit 0,
no data race reported.
