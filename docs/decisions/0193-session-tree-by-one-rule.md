# 0193 - The session tree is computed by one rule and cannot be wrong

Date: 2026-11-26
Status: Approved by the owner on 2026-11-26 ("go 0193", through the CTO, message m_9d47e62900aa8b07), as written: the Agent graph of 0160 stays the one stored hierarchy, with no separate per-session parent. Asked for by the CTO on the owner's instructions (message m_3ac935db643ccd78).

## Context

The owner, on the Sessions tree on 2026-11-26: "can you please fix that super messy org chart? i dont understand why its so hard to get something logical", and after that day's fixes: "my point is that it's systematically bad".

On that day the live tree was wrong in three ways, each from a different cause:

- homelab showed two roots. Two live sessions held the lead Agent `homelab-lead`, and the Agent had no pinned lead session. The gateway never picks between two candidates: it asks the CTO, and an old offline session that was never archived counted as a third candidate.
- gemini had four live sessions on its lead Agent and no pinned lead, so the project had no root.
- A `session_change replace` of a busy lead moved nothing and recorded no `replaced_by`.

The tree today is a guess made in several places. The TUI's `reportParent` (internal/tui/model.go) reads the Agent's `reports_to`, then special cases for mergers, workers and the project lead. Lead pins are set lazily, at session upsert or `lead_set`, and only when exactly one candidate exists. Nothing checks the result. Each fix so far patched one case.

## The owner's earlier design, which this keeps

Decision 0160 (approved 2026-11-13) holds his words: "You need to have access to the full hierarchy graph. That needs to be stored and accessible", "workers report to an agent; merge Q report to an agent; agents report to team lead; team lead report to cto/chief", and "a single data structure embedding these relationships, and Session and Graph are 2 views of the same."

So the stored hierarchy stays the Agent graph (`reports_to` edges). What changes is that a session's place becomes a pure function of that graph plus one stored field, with invariants enforced when data is written.

## Decision

### One rule

Every session has exactly one parent, computed in one gateway function (`sessionParent`) that the TUI, the web console, `agent_sessions_find`, the CTO's tools and routing all read. Nobody else computes placement.

1. A worker's parent is the session that started it (`worker.owner`).
2. A session that is the lead session of its Agent has, as its parent, the lead session of the Agent it `reports_to`. When that Agent has no live lead session, or the edge points to the root, the parent is the root (owner, then CTO and chief, as today).
3. Any other session of an Agent that leads something (a Repo, an active Team, or a lead-level grant) has the Agent's lead session as its parent.
4. A session of an Agent that leads nothing (a member Agent such as `<project>-agent`, or a merger Agent) has, as its parent, the lead session of the Agent it `reports_to`, the same as rule 2. Such an Agent has no lead session of its own, so its sessions sit side by side under their lead, never under one of their peers.

There is no fallback chain, and no rule reads team membership, repo columns or names. A lead whose `reports_to` is the CTO Agent sits under the CTO session (0160, 0184); only the owner edge is a root.

Rule 4 was added on 2026-11-26 after the part 1 golden picture: without it, member and merger sessions showed "(no parent: no lead)", and I1 would have made one member session the parent of its peers.

### Invariants, kept at write time

The gateway enforces these in the same transaction as the write that could break them. It never repairs them later.

- I1. An Agent that leads something (rule 3) and has at least one live session has exactly one lead session, and it is live. An Agent that leads nothing has no lead session. When the lead session stops, is archived or is replaced, the same transaction picks the next one: the replacement, else the live session the owner used last, else the oldest live one. There is no question to the CTO; the CTO can change the choice with `lead_set`, which stays the only override.
- I2. A session holds exactly one Agent. A new session takes the Agent it was started for. With none named, it takes its project's member Agent, which is created at once if the project has none (`<project>-agent`, the convention since 0160). It never takes the lead Agent while that Agent has a live lead session.
- I3. `session_change replace`, a stop, an archive and a merger or lead move change parents in one transaction: the member and lead sessions under the old session follow the new one. A replace never answers `moved:false` without saying why. Workers are the exception: a worker always sits under the session that started it (rule 1), because the daemon that runs it owns that link and its hand-back goes there. A replace moves its workers through the daemon, as before. A worker ends when its starter ends. A lead move that keeps the old lead session live leaves its workers with it, inside the new lead's branch. Amended on 2026-11-26 during part 2: a gateway-only move of workers would be undone by the daemon's next sync.
- I4. Offline sessions that were never archived do not count for I1. A session that stays offline past the existing retention is archived by the gateway.
- I5. A start that would break I1 to I3 is refused with one line naming the rule. No other start is refused.

### Self-check

A gateway loop (once a minute, one indexed query per invariant) looks for violations. A violation is logged as `event=tree.violation`, sent once to the agent-net lead's Desk as one item per cause, and is never shown as a broken tree: a session whose parent cannot be computed is drawn under its project's lead session with a marker `(no parent: <rule>)`.

### End-to-end test

A fixture is made from a scrubbed copy of the live gateway of 2026-11-26: every ID, name and host is replaced by a stable alias, so no real data is committed. It holds homelab, gemini, wbgpoc with ai-arch, agent-net with its merger, the chief, the CTO and running workers. The test renders the real Sessions tree from it at 120x34 and compares it with a golden file. The owner looks at that golden picture and says whether it is logical before the code merges.

## Migration

One migration, appended at the end, applies I1, I2 and I4 to the stored data once: it pins one live lead session per Agent by the I1 order, moves extra holders of a lead Agent to the member Agent, and archives stale offline holders. Its report lists every change, and the CTO can undo any single one with `lead_set` or `session_agent_set`.

## What this removes

The placement guesses in internal/tui/model.go (`reportParent`, `pinnedLeadOfAgent`, `projectLeadSession`, the merger special case), the lazy pin and the "which lead" CTO question in internal/gateway/lead_session.go, and the separate parent logic in the web console and `place.go`. Rough size: a few hundred lines out, one function in.

## The question that was settled

The CTO had proposed that each session store its own parent and that Agent ids, `reports_to` and team membership stop deciding placement. That conflicted with the owner's design in 0160. The owner chose 0160: placement is a function of the Agent graph.

## Parts

1. The rule, `sessionParent`, and every reader moved to it, with the end-to-end test and its golden picture.
2. The invariants at write time (I1 to I5) and the migration.
3. The self-check loop.
