# 0164 - The merge queue's CI is the single test gate

Date: 2026-11-14
Status: Approved by the owner on 2026-11-14.

## Problem

Every complete hand-back waited in one serial queue for a daemon-run test
check. The check repeated what the merge queue's CI runs later, on the same
work, and it was slow and often wrong.

The owner asked on 2026-11-13: "who's doing these checks? 20 mins per check?
what is this madness?" and "the merge queue is running the tests no? and then we
have CI? what is the point of this?". On 2026-11-14 he said: "Yes to the test
pipeline redesign you proposed on 11-13: remove the daemon's hand-back check,
workers run their own changed tests plus a small contract set, and the merge
queue's CI is the single test gate. Start it now."

The data from 11-12 and 11-13:

- 89 hand-back checks: 22 passed, 32 failed, 23 timed out, 5 could not run and
  7 were not configured.
- Of the 32 failures: 17 were `go: not found`, 5 were package timeouts, 6 were
  the migrations lock after main moved, 2 were flakes, and about 4 were real.
- Merger pre-merge test failures since 11-06: 116. 41 were real on the change's
  own head. The top tests were the migrations lock and append-only checks
  (10), `TestCTOSchemaBudgetAndCharterGuide` (8), `TestDescriptions` (8),
  `TestOutputSchemasCoverProtocolTypes` (3) and `TestCTORegisteredReadSizes`
  (3).
- Workers already ran `scripts/test-changed.sh` before hand-back, which
  `AGENTS.md` asks them to do.
- The merger runs one CI per train tip and records its test steps. Post-merge
  tests passed 414 of 414.
- The merger fast-forwards local main after each push. A stale local main made
  checks test more than 140 unrelated files on 11-13.

## Decision

1. The daemon no longer runs a hand-back check. A complete hand-back records
   its change and sends its notice at once, and the notice has no
   `hand-back check:` line. The test slot stays for `agent-net test-run` and
   the merger's suites. Old change records keep their `handback_check` result
   and still read, and a hand-back row an older daemon saved in
   `worker_pending_handback` is replayed after a restart.
2. A worker runs the tests its diff needs with `scripts/test-changed.sh`. The
   script always adds a small contract set: the migration lock and append-only
   tests, `TestCTOSchemaBudgetAndCharterGuide`, `TestCTORegisteredReadSizes`,
   `TestDescriptions` and `TestOutputSchemasCoverProtocolTypes`. A branch that
   fell behind the default branch still catches those breaks before hand-back.
   The worker's hand-back lists what the script ran, with the production and
   test line counts.
3. A worker worktree starts from origin's default branch, not from a possibly
   stale local branch. When local HEAD is an ancestor of the fetched tip, the
   worktree starts at that tip, even when the owner's checkout is on another
   branch. Unpushed commits, no origin, a missing default branch or a failed
   fetch keep local HEAD and log why.
4. In train mode, the merger reports one CI run on the train tip and puts that
   tip in `fields.tested_commit` on each change's test step. When a change's
   merged commit is that tested tip, the gateway records its after-merge test
   from the same run. Other changes record their after-merge evidence as
   before. No other delivery step rule changes.

## Consequences

- A hand-back no longer waits for a test queue, so one slow or broken queue
  cannot hold every caller's answer.
- The merge queue's CI is the one test gate. A real failure is found there, on
  the exact commit that lands.
- Workers carry the cross-cutting checks that used to be the merger's most
  common pre-merge failures, at the moment they still have the context.
- Older gateways and daemons keep working: the check fields stay on the wire
  and in the database, and they are only read, never written by a new
  hand-back.