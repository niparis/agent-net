# 0094 - The gateway read path

Date: 2026-11-08
Status: Built on the branch of worker w_c16d62ea19502b0f. Not yet released.

## Context

On 2026-11-08 the gateway ran at high CPU for hours and the TUI hung for nearly
a minute. The log showed 1,086 slow requests in 15 minutes: `session.sync` took
38 seconds on average, `policy.get` (once per prompt) 8 seconds. The host load
was 32 to 54 on 12 cores, so the gateway had to be fast without an idle machine.

Four causes, found by profile and by reading the log:

1. The read loop of a node ran most handlers itself. While a handler waited
   for the database, nothing read the socket. Pings and pongs stopped, the
   keepalive closed the link after 45 seconds, and the node reconnected and
   synced all its sessions again. The Mac did this several times an hour.
2. The standing choice for new skill projects read every skill, with all its
   locations, after every `session.upsert`, `session.sync` and `project.add`.
   That is about 20 ms, on every activity change of every session.
3. Every `policy.get` and `query.agents` rebuilt the whole graph (a charter
   lookup per node) and ran the joins again.
4. The database connection parsed the SQL text again on each call. Parsing was
   a third of the CPU of `session.sync`.

The database has one connection, so all of this work waited in one queue. When
the work that arrived each second was more than the connection could do, the
queue grew and every call became slow.

## Decision

- **The read loop only reads.** It hands a frame to a goroutine of its own when
  the type is a pure read (`parallelFrames`), to the node's write lane when it
  is not, and runs `hello` itself. The lane is one goroutine per connection that
  keeps the order of the node's writes. Frames that waited in a lane when the
  link ended are dropped; the node sends them again.
- **The standing choice runs when its inputs changed.** The inputs are the skill
  items, the known projects and the seen marks. `skillInputsOf` reads them in a
  few small queries and the choice runs only when they differ from the state at
  which it last had nothing to do.
- **Reads are kept until the next write.** `internal/sqlitecache` counts the
  statements that may change the database. The graph, the settings snapshot
  and the agent list are kept with that count and read again when it moved.
  The agent list also holds time windows, so it is kept for one second at most.
  Each caller gets a copy.
- **The connection keeps its prepared statements.** `internal/sqlitecache` wraps
  the SQLite driver and keeps up to 1,024 statements. A statement whose rows are
  open is never shared.

## Consequences

- A write anywhere makes the next read of the graph, settings and agent list
  load again. A write is much rarer than the reads, but `session.upsert` is a
  write. Counting writes per table would keep the graph through session
  writes; it needs the tables each read touches, and a write by a trigger,
  and is left for later.
- Reads of one node can now overtake its earlier writes. A caller that needs a
  write to be visible waits for its answer, as the daemon does.
- `docs/security.md` is unchanged: no limit or check moved.
