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
