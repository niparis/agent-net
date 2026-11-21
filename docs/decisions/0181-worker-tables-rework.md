# 0181 - Worker and picker tables rework

Date: 2026-11-21

Status: Approved by the owner on 2026-11-21 ("rework: go ; project policies: drop, no limits on pick candidates, its pretty tiny").

## Context

The owner reviewed the gateway's worker and picker tables. One worker run was spread over five tables keyed by the same session. Defects lived in two tables. Several small tables were a value or a cache. This continues [decision 0179](0179-gateway-schema-reduction.md): fewer tables, a data model that the MCP tools show as it is, and less production code.

## Decision

One migration copies each fact and drops nine tables.

- **Usage, visual proof and the rate offer move onto `worker_runs`.** A run holds the tokens of its session by kind and their cost at API prices, computed from `model_prices` when the run is recorded. The usage of a session still sits on its latest run, as its total always did. A proof report sits on the run it came from. A rate offer is per session, so it sits on the session's first run. The hand-back notice, and the offer with it, comes before the run is recorded, so an offer made before that waits on its session row and moves to the run. A rating made in that gap waits with `run_id` 0 and the run adopts it.
- **A brief defect is a `worker_defects` row with blame `brief`.** A moved defect keeps its row and changes its blame. Every picker reader asks for blame `worker`, so the picker scores do not change. `agent_worker_rate` keeps its kinds `brief_defect` and `move_to_brief_defect`.
- **Ratings and defects read the model, harness and difficulty through their run.** The copied columns are dropped. The copies differed from the run only in format (`claude-sonnet-5-5` against `claude-sonnet-5-5:high`).
- **A learned token budget is a `quota_blocks` row with no machine and no reset time.**
- **A refused brief is a closed `worker_starts` row in state `refused`.** The third-attempt rule counts these rows in the last 24 hours. The 30 day sweep of closed starts clears them.
- **The override facts of a held start sit on its Desk ask** (`inbox_items.override_facts`, JSON).
- **The version of `worker_runs` is the `gateway_state` key `worker_runs_version`**, raised by the same triggers.
- **The delegation policy is removed**: the table `project_policies`, `sessions.delegation`, the operator method and CTO operation `session_policy`, the TUI form (key `p`) and the Sessions cell that opened it, the DELEGATE and POLICY columns, and the policy text and subagent files the daemon built from it. The per-turn `policy` call stays, because it carries the charter. The worker permission level of a project is the repo setting `workers.sandbox_level`, which the settings layer and `node_settings_set` already write.

`pick_candidates`, `worker_starts`, `worker_runs`, `worker_assessments`, `worker_ratings`, `worker_defects`, `model_prices` and `model_failures` stay. `pick_candidates` has no retention limit.

## Consequences

- The Workers log shows tokens and cost per run. A session that was continued shows them on its latest run only, where it showed the session's total on every run before.
- A rating whose run is gone (the run was never recorded, or was pruned) no longer feeds the quality fit, because it has no run to read its model from.
- `tools/pickerreplay` and `research/picker-ml` read `worker_runs.usage_*` and `worker_defects.blame` from a database file.
