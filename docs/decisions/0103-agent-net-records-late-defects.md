# 0103 - agent-net records late defects by itself

Date: 2026-11-08
Status: Built. The owner asked for it on 2026-11-08: "I need everyone in the
network to apply it."

## Context

A defect found after a worker's hand-back must be recorded against that worker
with `agent_worker_rate` and `kind: "defect_found_later"`. The record feeds the
worker picker's quality data and the difficulty calibration. The rule lived
only in the guidance text, and agents forgot it. On 2026-11-08 the
orchestrator missed two. The `worker_defects` table held 84 rows since
2026-11-01, and every one was filed by hand. Change ch_0605 failed verify after
release and nothing was recorded. A missing record skews which models get work.

The gateway already knows each change's origin worker
(`delivery_changes.origin.session_id`), and it sees every step report.

## Decision

The delivery steps record these defects. No agent has to remember.

1. A **verify** step reported `fail` after merge, on a change made by a worker:
   severity 3. The report may carry `fields.severity` (1 to 4) to set another
   one. The step note is the defect note.
2. A **test** step reported `fail` before merge: severity 1, once per revision,
   against the worker that made that revision. A report with `fields.flake=yes`
   (a flake the merger knows) records nothing. A failed test after merge
   records nothing.
3. A failed **accept** or **review** records nothing. That is normal iteration.
4. **A fix for a shipped change.** `agent_worker_start` and `agent_change_submit`
   take an optional `fixes` (a change ID) and `fixes_severity` (default 2). The
   defect goes to the worker that made the fixed change, once per fixed change,
   and only when that change is merged or done.
5. **No duplicates.** One automatic defect per change, kind and revision (a fix
   counts once per change, and not when a verify or test defect is already
   recorded for it). An automatic defect is skipped when a defect was filed by
   hand for the same worker in the last hour. Manual calls keep working.
6. Each automatic defect is a normal `worker_defects` row, so the quality data
   and the picker table refresh the way they do for a manual one. A
   `worker_defect_auto` row keeps its change, kind and revision for the
   duplicate check. The Desk log gets one line: `defect recorded: <worker>
   sev <n> on <change> (verify failed)`.

There is no new approval and no new gate. The guidance line now says that
agent-net records these by itself, so agents file only what it cannot see.

## Consequences

- The manual window is by worker, not by change, because `agent_worker_rate`
  names a worker and no change. A manual defect on one change can hide an
  automatic one on another change of the same worker within the hour. We
  accept this: it errs toward a missed record of a worker that was just
  reported, never toward a double.
- `agent_worker_start` records the fix defect when the gateway accepts the
  start, not when the worker's change appears at hand-back. The daemon keeps no
  extra per-worker state, and the caller already says what it fixes.
- A change that a plain session made (origin `session`) records nothing: there
  is no worker to blame.
