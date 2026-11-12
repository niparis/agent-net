# 0141 - The TUI reads what changed, not every list every second

**Date:** 2026-11-12
**Status:** Accepted.

## Context

The owner's TUI on the Mac pulled about 1.1 GB from the gateway in 10 minutes
and burned about half a core, and the Mac daemon that relays it burned as much.
The daemon's own counters showed every list called about 540 times in 10
minutes: one full refresh almost every second.

The cause was not a missing change signal. The gateway already pushes topic
versions, and the TUI already reads a topic only when its version moved. The
signal moved all the time:

- Every session report writes the `sessions` table (`last_seen`). Agents, nodes,
  projects and usage all depend on it, so on a busy network they changed every
  second or two. A write moves the version whether or not any drawn row changed.
- The changes topic also covers `events` and `messages`, because the message feed
  rides on it. The change list moved with every message.
- On the Desk, any change of the agents topic also asked for the proposals and
  the Desk lists. The proposal list asked the gateway for the full machine list
  (the quality tables of every machine) just to find which machines are online,
  and then asked each of them.
- The full agents list was about 800 KB, 40% of it the judge's reasons for each
  worker, which the list does not draw.

## Decision

1. **ETag.** `query.agents`, `query.nodes` and `change.list` take `want_etag` and
   `if_etag`. The gateway hashes the rows it would send. A caller that holds the
   same hash gets `not_modified` and no rows, even when a table under them was
   written. The topic version stays as the cheap first check. Callers that do not
   ask get the same answers as before, and an older gateway ignores both fields.
2. **The TUI sends the ETag it holds** (`Snapshot.AgentsETag`, `NodesETag`, and one
   per state for the change list). The change list keeps the gateway's rows, not
   the view's, because how long a change has waited depends on the time of each
   draw. every 2 minutes the list is read in full, so a change on a later page is
   never missed for long.
3. **A gap between reads.** A changed topic is read at once after a quiet time, and
   no sooner than 3 seconds after the last read on a busy one. Changed topics stay
   marked until a read gets them, so nothing is lost. Reads the owner asks for do
   not wait.
4. **The Desk lists and the proposals.** The asks and chief lists are read when the
   Inbox topic moves, when what waits for the owner changes, every 10 seconds, and
   on a full read. The proposals are read when what waits for the owner changes
   (the count in the Snapshot), every 10 seconds, and on a full read, not when a
   topic moves: the Inbox topic also moves with every session report, and each
   proposal read goes through the daemon to every online machine.
5. **Lite rows drop what the list does not draw:** the judge's reasons, the picker's
   note and the judge's name on each worker (the worker log shows them from its own
   call). An online session's `last_seen` is kept to the minute, because the TUI
   reads it only for "idle over an hour" and its exact value moves with every
   report. An offline session keeps its exact value, which is drawn as an age.
6. **The machine list for proposals is brief** (`query.nodes` with `brief`: who is
   on the network, no quality tables).

## Measured

The real TUI and the real operator client, with a fake daemon in front of the
real gateway handlers and a copy of the gateway database (340 sessions, 22 of
them online). Real writes to the copy move the topics: 3 session reports a
second and 0.3 events a second, the rates read from the Mac's counters. The
Desk tab, 60 seconds, scaled to 10 minutes (`tab 7`):

| list | before calls/min | before bytes/10 min | after calls/min | after bytes/10 min |
|---|---|---|---|---|
| agents (`list`) | 57 | 484 MB | 16 (4 with rows) | 24 MB |
| changes (`changes_owner_find`, 3 states) | 72 | 129 MB | 54 (all unchanged) | 0.04 MB |
| machines (`nodes`) | 57 | 105 MB | 16 (all unchanged) | 0.03 MB |
| `proposal_list` | 57 | 0.001 MB | 6 | 0.0001 MB |
| projects | 57 | 15.9 MB | 16 | 4.5 MB |
| asks | 57 | 9.0 MB | 16 | 2.5 MB |
| everything the TUI calls | 1580 | 744 MB | 654 | 31 MB |

Each `proposal_list` also made the daemon ask the gateway for the full machine
list (185 KB), and before this change that was not in the table: 57 calls a
minute, another 105 MB in 10 minutes. It is brief now.

The Sessions tab gives the same: 722 MB before, 31 MB after.

Rerun:

```
sqlite3 ~/.local/state/agent-net/gateway/gateway.db ".backup 'copy.db'"
AGENT_NET_MEASURE_DB=$PWD/copy.db AGENT_NET_MEASURE_TAB=7 AGENT_NET_MEASURE_SECONDS=60 \
  go test -tags measure ./internal/gateway -run 'TestMeasureTUIPolling$' -v -count=1
```

## Not done

- Reading only the topics the visible tab draws. The split by tab is not clean:
  the header and the Sessions tree draw agents on every tab. The gap and the ETag
  already make an unchanged part cost a few hundred bytes.
- A since-revision delta. The ETag removes unchanged answers. A real change still
  sends the whole list.
- The cost of the session reports themselves: about 3 `session.upsert` a second
  from one Mac, each a gateway write. They are the reason the versions move.

## Consequences

- A part of the screen is at most 3 seconds old on a busy network (it was 1).
- `last_seen` of an online session in the TUI's lite rows is rounded down to the
  minute.
- The measurement is repeatable: see `internal/gateway/poll_harness_test.go` (build
  tag `measure`).
- About 4 reads a minute still send the whole agents list, because rows really
  changed (a session went busy, a worker's token count moved). Only a delta would
  shrink those.
- Sessions that report inside one minute are listed in id order in the TUI's rows,
  not in the order of their last report. The TUI orders sessions by activity.

## Why `query.proposal_list` timed out

On the Mac it averaged 21 ms and timed out 4 times in 575 calls at the TUI's 5
second limit, up to 10 s since start. On the gateway host the same call took up to
30 s. Every other frame stalled with it at those moments (`session.upsert` up to
25 s, `query.projects` up to 25 s), while the host load was above 30. The call is
the slowest path: TUI to daemon, daemon to gateway for the machine list, gateway
to each other machine and back, over a link that was carrying about 2 MB a second
of list answers. It was the first thing to show a stall of that link. Less
traffic and a small machine list remove the load that made it worse. The call
still fails as a whole when one machine is slow, as before: a list that hid a
machine's proposals could hide a request for the owner.
