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
