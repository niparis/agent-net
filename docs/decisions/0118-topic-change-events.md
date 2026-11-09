# 0118 - Topic change events for front ends

Date: 2026-11-09
Status: Built on a worker branch.

## Context

A TUI asked the gateway for the same large agent and worker lists every second.
Gateway counters over ten minutes showed 21,801 calls and 2.6 GB across all
clients; TUIs made 83% of the calls and 72% of the bytes. Most reads returned
the same data. A web front end will need the same live view.

## Decision

The gateway is the source of change events for every front end. The daemon
subscribes on its existing authenticated node link, and fans out the events to
owner-gated local socket subscribers. Multiple local subscribers share one
subscription on the link. The phone and a future web front end can use the
same source in a later change.

Clients obtain snapshots using existing reads. A subscription starts with the
current version of every topic, and then sends `{topic, version}` when a topic
changes. A client refetches the affected snapshot parts. The gateway compares
the read cache's per-table write stamps, which move at commit, every 250 ms;
writes in the interval coalesce to the latest version. Memory-only usage has
its own counter, and a separate quota counter also moves the nodes topic. A
subscriber joining after a restart must treat the initial versions as new,
since versions are for one gateway process, not durable sequence numbers.

The dependency sets are declared in `internal/gateway/topic_events.go`, reusing
the table sets of the cached reads:

| Topic | Tables and other inputs |
| --- | --- |
| agents | `agentsTables`: sessions, nodes, profiles, apps, projects and their names and mergers, agents, repos, teams and members, settings, charters, messages, inbox and loops used by the agent read |
| nodes | `nodesTables`, `calibrationTables`, quota blocks, node health, playbook, owner and network settings; in-memory quotas |
| workers | `workerLogTables`, `calibrationTables`, worker starts, quota blocks, owner and network settings |
| graph | `graphTables`: graph nodes, edges, charters, teams, projects, settings, loops and the session columns used by the graph |
| inbox | `inboxTables`, messages, ask sorts, desk log, charter draft asks and owner settings |
| changes | delivery changes, revisions, evidence, tasks, messages, stats, inbox and delegates; events and messages |
| projects | projects, repos, names, mergers, sessions, nodes and `graphTables` |
| learned | `learnedListTables`: suggestions, details, phrase state, views, graph history and charter, drafts, agent charters and values, teams and agents |
| usage | sessions, nodes, quota blocks and the in-memory usage reports |

A topic can depend on several tables and a table can move several topics. A
slow safety refresh every 30 seconds covers missed events, values that change
with time and state that lives only on a daemon. A broken subscription returns
the TUI to one-second polling and it retries the subscription. The worker log
is read only while its panel is open; session text is read only while its panel
is visible.

We do not send deltas yet. Snapshot reads already enforce the existing access
rules and compute derived views from several tables. Deltas would need ordering,
replay, and per-view merge rules before clients could recover from a lost link.
Topic invalidation cuts repeated reads while keeping those rules in one place.

## Measurement and remaining work

A test runs the actual TUI refresh command against a local daemon and gateway,
using an accelerated 60-interval idle scenario. Gateway counters show 546
calls for 60 old refreshes and 24 for two safety refreshes. The test keeps the
worker log and CTO polling closed, omits subscription setup and independent
view timers, and runs intervals without advancing wall time, so it is a
comparison of refresh traffic, not a prediction of total live traffic. The
scheduler test separately checks that an idle subscription causes two safety
refreshes in 60 simulated seconds.

Part 2 will switch the phone and web endpoint to this source. This decision
does not add a browser endpoint or data deltas.
