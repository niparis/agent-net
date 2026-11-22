# Roadmap

Work the owner wants, which nobody has started yet. Each item says who asked, why, and what is known. When an item starts, link its decision file or change here. When it ships, remove it.

## Token use by project, team and role

Asked by the owner on 2026-11-21: "are we storing enough details on the workers run to understand for example our token consumption per project? per team? and the overhead from CTO / COS?"

### What is stored today

- `worker_usage` holds one row per worker session: input, output, cache read and cache write tokens (1,921 rows on 2026-11-21).
- A join with `worker_starts` on `session_id` gives the project. On 2026-11-21 the output tokens were: agent-net 47.7M (862 runs), truenas-stacks 12.8M, tenebrae-strats 7.8M, ingest-forge 5.6M, and small amounts for the rest.

### What is missing

1. **Project on every run.** 535 of 1,921 worker runs (28%) have no project, mostly runs from before starts were tracked.
2. **Cost.** The worker notice shows a cost at API prices, but the gateway does not store it. Reasoning tokens, the price used and the time of the run are not stored either. So "what did this project cost this week" has no exact answer.
3. **Team.** No team on the row. A team total needs a join from project to repo to team in the graph. No tool or screen does it.
4. **Sessions that are not workers.** The CTO, the chief of staff and the leads report no usage at all, so the cost of coordination cannot be seen. Their usage exists only in each harness's own files on each machine (for example the usage field on each assistant turn in Claude Code transcripts).

### Proposed work

One area, so one worker session continued part by part:

1. Record usage for every session, not only workers: tokens by kind, model, a cost at the time of use, and the time. Read it from the same transcript and harness data that the worker notice already uses.
2. Store the project, the team and the role (worker, lead, chief of staff, CTO) on each row, so totals need no join.
3. Show totals by project, team and role over a chosen period in an existing TUI screen, and give the CTO a read tool for the same numbers.

Open question for the owner before step 3: which screen shows it.

## Let workers run the socket tests

Asked by the owner on 2026-11-22, after the lead had been running daemon tests himself to cover for this gap.

### The problem

Workers run inside a sandbox that blocks unix sockets. Tests that need a socket (most of `internal/daemon`, some of `internal/gateway` and `internal/setup`) skip there with `testsock.Skip`. A worker can hand back a change that fails those tests, and nobody sees it until CI runs on the release train.

### What it cost on 2026-11-21 and 2026-11-22

- A stack of schema reworks passed every worker's tests but failed 32 daemon tests in CI: an unused machine invite blocked the graph cutover.
- A gateway lock-order deadlock (a profile write against a message store) only showed in CI, and it held the release for a round.
- Each failure cost a full CI round of 30 minutes or more, plus a fix worker.

### Proposed work

1. Let the worker sandbox create and connect to unix sockets inside its own temporary folders, so `testsock.Skip` no longer fires for workers. Keep the daemon socket and other sessions' sockets hidden, as today.
2. Have `scripts/test-changed.sh` fail, not skip, when a socket test it selected was skipped, so a hand-back says plainly what did not run.
3. Then reviewers never need to run tests: the worker's own run covers the change, and CI stays the single gate (decision 0164).
