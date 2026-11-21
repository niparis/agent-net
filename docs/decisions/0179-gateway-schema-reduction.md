# 0179 - Gateway schema reduction

Date: 2026-11-20

Status: Approved by the owner on 2026-11-20 ("i approve the full plan").

## Context

The gateway database had 155 tables and 1158 columns. The owner said this "feels like too much" and asked for an in-depth check.

The check is [docs/design/gateway-schema-audit.md](../design/gateway-schema-audit.md). It lists every table with what it holds, its writers and readers in production Go, its live row count and a verdict. It is the evidence for this decision. Read it before you build a part.

What the audit found:

- 117 tables are in real use and stay.
- 7 tables are dead.
- 27 tables repeat the key of another table, or the shape of another table, and fold into it.
- 1 table (`friction_status`) equals the latest row of its history table.
- 3 one-row tables are key-value state.
- Three decisions are stored twice (proposal 0163): charter and scope history, team lead history, and the repo lead.

## Decision

Reduce the schema from 155 tables and 1158 columns to 118 tables and 1042 columns, in 10 parts. Each part ships alone.

| Part | What | Tables removed |
|---|---|---|
| 1 | Drop the dead tables (list below). Remove the `session_agent_history` writer and the `playbook_notes` name in `topic_events.go`. | 7 |
| 2 | `nodes`: fold `node_health`, `node_tailscale`, `node_time_zones`, `node_silence`, `node_graph_refs`, `inventory_reports` into columns. | 6 |
| 3 | `repos`: add `archived`, `held_back`, `lead_agent_id`; remove `archived_repos`, `repo_held_back`, `repo_leads`. One helper returns the lead. | 3 |
| 4 | One charter store: move `agent_charter` and `graph_charter` into `charter_values` with levels `agent` and `repo`. | 2 |
| 5 | Delivery and messages: fold `delivery_revision_stats`, `delivery_step_inbox`, `message_files`. | 3 |
| 6 | Small same-key folds: `learned_phrase_state`, `learned_suggestion_details`, `session_wake_failures`, `retired_session_wakes`, `worker_defect_auto`, `model_successes`, `member_blocked_ticks`. Derive `friction_status` from its history. | 8 |
| 7 | One `tool_plans` with a kind (takes `mcp_import_plans`, `mcp_native_plans`). One OAuth attempts table (takes `oauth_registration_attempts`). | 3 |
| 8 | New `gateway_state(key, value, updated_at)`: takes `graph_cutover_state`, `graph_ref_gate`, `skill_archive_meta` and the 12 marker rows of `owner_settings`. `graph_node_settings` moves into `setting_values`. | 4 (1 new) |
| 9 | History: stop the team lead double write, then move `team_history` into `graph_history`. | 1 |
| 10 | `graph_nodes` into `repos`, with its six triggers. | 1 |

Order:

- Part 1 first.
- Parts 2 to 9 do not depend on each other.
- Part 10 is last, after part 3, and only after a short design note. The code still knows the node kind `architect`.

### Drop list (part 1)

No code change needed:

- `chief_corrections`
- `delivery_settings_legacy`
- `skill_suggestions`

With a small code change in the same commit:

- `playbook_notes`: remove its name from the nodes topic list in `topic_events.go`.
- `session_agent_history`: remove its one writer in `session_agent.go`.

After the double-write fix has merged:

- `agent_charter_history`
- `node_scopes_history`

### What stays, on purpose

- `pick_candidates` has no reader in the gateway. `tools/pickerreplay` and `research/picker-ml` read it from the file.
- `skill_archive_latest` is a pointer and not the highest version.
- `project_bar_next` is a counter that stops a deleted rule number from being used again.
- `setting_revision` and `table_versions` are one-row tables read in transactions or moved by triggers.
- `setting_history`, `standing_order_history`, `friction_status_history`, `skill_archive_changes` and `skill_list_changes` each keep their own typed undo. They do not become one generic history table.
- `profiles`, `session_checks`, `worker_usage` and `worker_assessments` stay apart from their parent rows.

### Not decided here

`worker_ratings`, `worker_defects` and `worker_rate_offers` carry copies of harness, model, effort and difficulty that differ from `worker_runs` for 542 of 744 ratings. The picker reads these columns. Which side is right needs its own decision.

## Rules a builder must respect

1. **Migrations are append-only.** Each part adds a new migration at the end of the list: add the columns, copy the rows, drop the old table, in one transaction. Never change the text of a shipped migration.
2. **Use `IF EXISTS` and `IF NOT EXISTS`.** A rewind can replay a migration after later ones ran. A replay must not fail.
3. **Update the topic lists in the same change.** Screens refresh from lists of table names (`topic_events.go`, `nodesTables`, `calibrationTables`, `workerLogTables`, `learnedListTables`, `graphTables`). A folded table must be replaced there, or a screen stops refreshing.
4. **`nodes` needs column keys (part 2).** A health or inventory report will move the `nodes` stamp. The Projects and Usage topics read `nodes`, so their lists must name columns (`nodes.hostname` and so on). `inventory_reports` is large (1.3 MB for 2 machines): make it the last column and never read `nodes` with `SELECT *`.
5. **Check for orphans in `delivery_step_inbox` (part 5).** Every inbox row must have a task row with the same key. The migration fails loudly if one does not.
6. **`graph_nodes` goes last (part 10).** Six triggers name it. They are rewritten in the migration that removes it. A design note comes first.
7. **Results on the wire do not change.** Daemons and connected apps call gateway operations and do not read tables. Each operation returns what it returned before.
8. **Two parts built at the same time both add a migration.** The second rebases on the first.
9. **No way back without the backup.** A gateway refuses a file with a newer schema version. After a drop, an older binary needs the upgrade backup.

## Consequences

- 37 fewer tables and 116 fewer columns.
- The repo lead, the team lead history and the charter values each live in one place.
- Marker rows leave `owner_settings`, so a one-time marker no longer refreshes the Nodes, Workers and Inbox screens.

## Part 6 result

One migration (`foldSideTablesTx`, `internal/gateway/side_fold.go`) folds all seven side tables, so none stays as it was. Every one has its parent key as primary key, so a parent holds at most one row of each. The live counts that looked one-to-many (8 wake failures for 4 wakes, 44 model successes for 4 failures, 37 retired wakes for 4 wakes) are keys with no parent row yet, not many rows for one key. Such a key gets a parent row with default values.

| Side table | Now |
|---|---|
| `learned_suggestion_details`, `learned_phrase_state` | columns `origin`, `history_id`, `raw_phrase` of `learned_suggestions` (`raw_phrase` is NULL when the cleaner did not change the phrase) |
| `session_wake_failures`, `retired_session_wakes` | columns `wake_failed_at`, `wake_failed_reason`, `wake_attempted_at` of `session_wakes`; the back-off is still the `WHERE` of the upsert |
| `worker_defect_auto` | columns `auto_change_id`, `auto_kind`, `auto_revision` of `worker_defects`, NULL for a defect filed by hand, with a unique index |
| `model_successes` | column `success_at` of `model_failures` |
| `member_blocked_ticks` | column `tick_hash` of `member_blocked_notices` |
| `friction_status` | dropped; the current mark is the latest row of `friction_status_history` |

Tables after the reduction, with what each holds and who uses it: [gateway-data.md](../gateway-data.md).

## Step tables result

On 2026-11-21 the owner reviewed the delivery step tables and approved one more rework (his words: "perfect. Can we refactor delivery steps in // with learning ?"). Four entities stay: the change (`delivery_changes`), its revision (`delivery_revisions`), the step report (`delivery_evidence`) and the project bar (`project_bar_rules`). The open work queue `delivery_step_tasks` stays too, and it holds open work only. One migration (`step_tables_migration.go`) copies each fact into its column, deletes the done tasks and drops six tables.

| Dropped table | Now |
|---|---|
| `delivery_step_messages` | column `notices` of `delivery_step_tasks`: a JSON map from a session to the notice sent to it. A task can have several live notices at once, because a task owned by a repo is sent to every online session of that repo, so one column for one notice is not enough. |
| `delivery_step_delegates` | column `delegates` of `delivery_step_tasks`: a JSON list of the nodes that may approve a delegated step |
| `caller_step_escalations` | keys `caller_escalation:<owner>` in `gateway_state`: the value says whether the repo lead was offered the steps, and `updated_at` is when the owner was woken. This took less code than columns on `agents` and on `sessions`, because the owner key is an agent or, with no agent, a session. |
| `project_bar_next` | column `bar_next` of `repos` |
| `merge_train_fails` | columns `train_fails` and `train_last_run` of `repos` |
| `project_mergers` | columns `merger_node` and `merger_session` of `repos`. A repo has one merger: registering a session on another machine replaces the first one. The two triggers that return the merger's work now sit on `repos`. |

A done step keeps no task. The column `completed` is gone, and a task is deleted when its step is reported, closed or retired. Its notices expire with it, unless another open task of the same message still points at them. The delivery evidence of the step is the record. These readers of done tasks moved to the evidence:

- A second report of a step by another session is refused with "already reported by": the reporter is read from the newest evidence of the step.
- A session that reported a step may report it again, or take it again after `needs_owner`: its task is queued again from the pipeline, claimed by that session, when its own newest evidence allows it. The same holds for the merger that confirms a merge the daemon saw from git.
- A delegated approval that a delegate reported is refused when the owner answers it later: the evidence of the step says it was reported.
- A step notice counts as recent activity of a session from the message itself, not from a link to a task.
- The list of changes, the Desk Steps panel, stuck notices and the merger reset read open tasks only. They never read done tasks.

The graph edge `/delivery/merger merges <repo>` stays. It says that the repo routes its merge step to the merger. It does not say who the merger is: `repos.merger_session` is the only place for that. The trigger that ends a registration moves the edge back to the caller.

A session that owns a node step of a change may not do the caller's steps of that change. This check read the tasks of the change, done ones included. It now reads open tasks, so a session that already finished its node step may act for the caller afterwards.

## Update 2026-11-21: tools retire

The owner reviewed the tools, skills and MCP tables and said to execute the plan. [Decision 0169](0169-skill-archive.md) task 5 retires the old Tools rollout. One appended migration drops twelve tables (`tool_identities`, `tool_locations`, `tool_revisions`, `tool_results`, `tool_audit`, `tool_project_settings`, `tool_project_seen`, `tool_accepted`, `plans`, `skill_versions`, `mcp_imports`, `inventory_reports`) and folds two more:

- `skill_archive_latest` is the column `in_use` of `skill_archive`. This replaces "What stays, on purpose" for that table: the pointer is now a column, and it is still not always the highest version.
- `skill_archive_changes` and `skill_list_changes` are one table, `skill_changes`, with a `kind` column. This replaces the line that kept them apart. Undo by id still works for both kinds.

This change drops 15 tables and adds one, so the schema has 14 fewer tables.
