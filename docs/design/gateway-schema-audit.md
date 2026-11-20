# Gateway schema audit

Date: 2026-11-20. Read-only analysis for the agent-net lead. No product code changed.

## Summary

| | Tables | Columns |
|---|---|---|
| Live database today | 155 | 1158 |
| Proposed | 118 | 1042 |
| Change | -37 (24%) | -116 (10%) |

- 117 tables stay as they are. The schema is large mostly because the product keeps many kinds of things. It is not large because of waste.
- 7 tables can be dropped. 4 of them need no code change.
- 27 tables fold into a table that already exists, and 1 is replaced by a query. Most are side tables with the same key as their parent.
- 3 one-row tables become rows of one new key-value table, `gateway_state`.
- The audit found two more places where one decision is kept twice (proposal 0163): team lead history, and the repo lead.
- Table count is not a size problem. `messages` (73 MB) and `skill_archive` (50 MB) hold most of the 192 MB file.

The lead's brief said 151 tables and 1149 columns. The live file has 155 and 1158. The count here includes `schema_version` and the five tables of the notes full-text index, four of which SQLite makes by itself.

## How it was measured

- Row and column counts: `sqlite3 -readonly ~/.local/state/agent-net/gateway/gateway.db`, on 2026-11-20. Nothing was written.
- Writers and readers: a script searched every non-test Go file under `internal/` and `cmd/` for `INSERT INTO`, `REPLACE INTO`, `UPDATE`, `DELETE FROM`, `FROM` and `JOIN` followed by the table name. Code at revision `4a166732d2`.
- "In migrations" means the migration list (`internal/gateway/store.go:122-1484`), `Store.migrate` (`store.go:2028-3341`) and the `*_migration.go` files. Those reads and writes do not count as live use.
- Only `internal/gateway` opens `gateway.db` in production code. This was checked by searching for `sql.Open` and `OpenStore`.
- Limits of the method: a statement that builds a table name at run time is not found. The suspect tables were checked by hand. The per-table cells show the first two sites and the total count.

## All 155 tables

Columns: W = statements that write, R = statements that read, both in production Go outside migrations. Paths are under `internal/gateway/`.

| Table | Holds | Cols | Rows | W | R | Verdict |
|---|---|---|---|---|---|---|
| `agent_charter` | Charter values of one agent (agent, attribute) | 5 | 43 | 3: graph_cutover_auto.go:281, store_agent_charter.go:44 (+4 in migrations) | 9: cto_graph.go:64, charter_drafts.go:37 (+2 in migrations) | **merge into charter_values**. same shape; level=agent, scope=agent id |
| `agent_charter_history` | Old and new value of each agent charter change | 7 | 53 | 1: store_agent_charter.go:51 | 0 (+1 in migrations) | **drop**. copy of graph_history type agent_charter (53 rows both sides); drop when the double-write fix lands |
| `agent_loops` | Loop state of one agent node (ticks, streak, pause) | 16 | 0 | 9: loop.go:236, loop.go:242 | 3: loop.go:79, loop.go:83 | **keep**. empty but used |
| `agents` | The agents of the org tree | 8 | 50 | 18: lead_session.go:95, lead_session.go:218 (+26 in migrations) | 74: member_loop.go:62, member_loop.go:63 (+20 in migrations) | **keep** |
| `app_refresh_budget` | Refresh-token rotations per token family in the current window | 3 | 1 | 2: apps.go:223, apps.go:394 | 1: apps.go:205 | **keep**. keyed by token family, not a single-row table |
| `app_tokens` | Hashed access and refresh tokens of connected apps | 8 | 67 | 11: oauth_store.go:448, apps.go:100 (+2 in migrations) | 6: oauth_store.go:498, apps.go:145 (+1 in migrations) | **keep** |
| `apps` | Connected apps and their grants | 10 | 2 | 7: oauth_store.go:298, apps.go:72 | 26: oauth_store.go:97, oauth_store.go:107 | **keep** |
| `archived_repos` | Set of archived repo ids | 1 | 0 | 2: graph_lifecycle.go:164, graph_lifecycle.go:166 | 20: lead_session.go:58, authority.go:203 (+5 in migrations) | **columns of repos**. a one-column set; becomes repos.archived |
| `ask_sort_hide_receipts` | Undo receipt for a batch hide on the Desk | 3 | 1 | 2: chief_hide.go:92, chief_hide.go:127 | 1: chief_hide.go:106 | **keep** |
| `ask_sorts` | Each sort decision the chief made on an ask | 16 | 1506 | 6: chief_hide.go:96, chief_hide.go:116 | 19: standing.go:351, chief_hide.go:37 | **keep** |
| `caller_step_escalations` | When a stuck caller step was escalated | 3 | 0 | 5: caller_agent.go:168, caller_agent.go:177 | 2: caller_agent.go:64, caller_agent.go:161 | **keep**. empty but used |
| `charter_draft_asks` | Which charter draft ask was already sent, per level and scope | 5 | 46 | 1: charter_drafts.go:126 | 1: charter_drafts.go:67 | **keep** |
| `charter_drafts` | Proposed charter values waiting for a decision | 12 | 61 | 4: charter_drafts.go:405, charter_drafts.go:408 | 7: store_inbox.go:623, charter_drafts.go:395 | **keep** |
| `charter_signals` | Messages that count as evidence for a charter draft | 5 | 749 | 3: store_record.go:495, signal.go:75 (+2 in migrations) | 7: store_record.go:353, store.go:4683 | **keep** |
| `charter_values` | Charter values by level and scope (global, team) | 6 | 6 | 4: store_graph.go:650, store_graph.go:652 | 6: charter_drafts.go:41, store_graph.go:642 | **keep**. becomes the one charter store |
| `check_answer_counts` | Count of check answers per day and kind | 3 | 36 | 1: check.go:693 | 1: check.go:704 | **keep**. the answers themselves are not stored, so it cannot be derived |
| `chief_corrections` | Old store of owner marks on chief sorts | 6 | 0 | 0 | 1: chief.go:1253 | **drop**. 0 rows; only reader is a migration constant (chief.go:1253) |
| `cto_intake` | Problems fed to the CTO, with count and state | 15 | 361 | 5: intake.go:77, intake.go:89 | 5: intake.go:172, intake.go:212 | **keep** |
| `deliveries` | Delivery state log of each message | 5 | 23246 | 2: store_record.go:465, store.go:5469 | 2: undelivered.go:124, store.go:4682 | **keep** |
| `delivery_changes` | A change in the delivery pipeline | 21 | 1097 | 4: session_replace.go:128, session_replace.go:177 | 58: session_stall.go:34, store_inbox.go:451 (+8 in migrations) | **keep** |
| `delivery_evidence` | Reported step results of a change | 15 | 5947 | 1: delivery_changes.go:1375 | 8: member_loop.go:210, delivery_changes.go:890 | **keep** |
| `delivery_retries` | Retry counter and next try time of a message | 3 | 0 | 9: withdraw.go:174, withdraw.go:211 | 8: session_stall.go:57, retry.go:50 | **keep**. empty but used; short-lived rows |
| `delivery_revision_stats` | Diff stat and signals of one revision | 5 | 1559 | 3: delivery_changes.go:525, delivery_changes.go:535 | 5: delivery_changes.go:1385, delivery_changes.go:1406 | **columns of delivery_revisions**. same key as delivery_revisions (1559 of 1659 rows) |
| `delivery_revisions` | Each revision of a change | 6 | 1659 | 1: delivery_changes.go:1305 | 2: delivery_changes.go:973, delivery_changes.go:1220 | **keep** |
| `delivery_settings_legacy` | Backup copy of delivery settings before a cutover | 6 | 1 | 1: delivery_pipeline_settings.go:46 | 0 | **drop**. 1 row; written only by a migration step; never read |
| `delivery_step_delegates` | Nodes a step was delegated to | 6 | 6 | 1: delivery_tasks.go:1935 | 4: delivery_changes.go:1108, delivery_tasks.go:776 | **keep**. one step has many nodes |
| `delivery_step_inbox` | Desk item raised for a step | 7 | 11 | 1: delivery_tasks.go:2002 | 14: store_inbox.go:451, store_inbox.go:785 (+3 in migrations) | **columns of delivery_step_tasks**. same key as delivery_step_tasks; 11 rows |
| `delivery_step_messages` | Notice sent to each session for a step | 7 | 4612 | 8: session_replace.go:247, session_replace.go:248 (+2 in migrations) | 27: session_replace.go:235, session_replace.go:239 (+5 in migrations) | **keep**. one step has many sessions |
| `delivery_step_tasks` | The open or done task of each step | 16 | 5816 | 27: session_replace.go:129, session_replace.go:178 (+4 in migrations) | 49: session_stall.go:34, caller_agent.go:40 (+4 in migrations) | **keep** |
| `desk_log` | Log lines shown on the Desk | 8 | 2112 | 4: permissions.go:802, asks.go:58 (+1 in migrations) | 3: permissions.go:777, asks.go:85 (+1 in migrations) | **keep** |
| `enroll_codes` | Hashed one-time codes that enroll a machine | 3 | 2 | 2: store.go:3402, store.go:3421 | 1: store.go:3412 | **keep** |
| `events` | The gateway event log | 7 | 74348 | 2: store_record.go:485, store.go:3357 | 4: store_record.go:281, store_record.go:354 | **keep** |
| `findings` | Findings reported about a model or harness | 9 | 28 | 3: store_record.go:505, learn.go:38 (+1 in migrations) | 4: learn.go:29, learn.go:50 | **keep** |
| `friction` | Friction reports from workers | 10 | 540 | 1: friction.go:112 | 1: friction.go:75 | **keep** |
| `friction_status` | Current status of a friction report | 6 | 111 | 1: friction.go:348 | 1: friction.go:75 | **derive from friction_status_history**. equals the latest history row for 111 of 111 rows |
| `friction_status_history` | Every status change of a friction report | 7 | 133 | 1: friction.go:353 | 1: friction.go:300 | **keep** |
| `graph_charter` | Charter values of a repo node | 5 | 61 | 5: store_graph.go:173, store_graph.go:311 (+5 in migrations) | 26: agent_charter.go:30, cto_graph.go:111 (+4 in migrations) | **merge into charter_values**. same shape; all 14 nodes are repos; level=repo |
| `graph_cutover_state` | Gateway version and time of the last upgrade (one row) | 3 | 1 | 1: graph_cutover_auto.go:37 (+1 in migrations) | 2: graph_cutover_auto.go:32, graph_cutover_auto.go:46 | **rows of gateway_state** |
| `graph_edges` | Typed edges between agents and repos | 7 | 85 | 22: settings.go:755, settings.go:881 (+6 in migrations) | 40: cto_graph.go:135, learned_owner.go:169 (+5 in migrations) | **keep** |
| `graph_history` | History of org changes, used by undo | 10 | 309 | 10: graph_lifecycle.go:111, graph_cutover_auto.go:285 (+3 in migrations) | 19: cto_graph.go:162, cto_graph.go:164 (+2 in migrations) | **keep**. becomes the one org history |
| `graph_node_settings` | A setting on one graph node | 5 | 1 | 4: store_graph.go:421, store_graph.go:423 (+1 in migrations) | 5: worker_sandbox_settings.go:69, worker_sandbox_settings.go:101 (+1 in migrations) | **merge into setting_values**. 1 row (deploy_rule); setting_values already has level and scope |
| `graph_nodes` | Component nodes of the old graph | 3 | 14 | 5: store_graph.go:110, store_graph.go:201 (+4 in migrations) | 24: worker_sandbox_settings.go:28, worker_sandbox_settings.go:29 (+22 in migrations) | **merge into repos**. 14 rows, all kind component, each one matches a repo; 24 readers to move |
| `graph_outcomes` | How a request between two nodes ended | 10 | 726 | 4: store_inbox.go:702, store.go:5201 (+3 in migrations) | 15: bot_tools.go:533, store_record.go:468 (+1 in migrations) | **keep** |
| `graph_ref_gate` | Flag and backup path of the typed-graph cutover (one row) | 3 | 1 | 1: graph_cutover_auto.go:289 (+1 in migrations) | 7: session_agent.go:15, teams.go:79 (+2 in migrations) | **rows of gateway_state** |
| `inbox_items` | Items for the owner (the Desk) | 31 | 2283 | 61: session_stall.go:111, store_inbox.go:42 (+18 in migrations) | 104: session_stall.go:99, session_stall.go:103 (+3 in migrations) | **keep** |
| `inventory_reports` | Last tool inventory a machine reported | 2 | 2 | 2: inventory.go:48, inventory.go:118 | 6: inventory.go:28, inventory.go:82 (+2 in migrations) | **columns of nodes**. one row per machine; 1.3 MB for 2 rows, so read it by column |
| `learned_message_flow` | Seven days of message excerpts between node pairs | 5 | 1034 | 2: store_record.go:501, learned.go:315 (+2 in migrations) | 1: learned.go:333 | **keep** |
| `learned_phrase_runs` | Times of recent phrase runs, for the rate limit | 2 | 1 | 2: learned_phrase.go:19, learned_phrase.go:30 | 2: learned_phrase.go:24, learned_phrase.go:49 | **keep** |
| `learned_phrase_state` | Raw phrase of a suggestion | 2 | 0 | 1: learned_phrase.go:123 | 9: chief.go:1725, learned_apply.go:79 | **columns of learned_suggestions**. one row per suggestion; empty |
| `learned_suggestion_details` | Origin and history id of a suggestion | 3 | 50 | 3: learned_owner.go:198, learned_apply.go:206 (+1 in migrations) | 8: learned_owner.go:113, chief.go:1723 | **columns of learned_suggestions**. 50 of 50 suggestions have a row |
| `learned_suggestions` | Suggested charter and edge changes | 12 | 50 | 15: learned_owner.go:155, learned_owner.go:201 (+2 in migrations) | 16: store_inbox.go:831, learned_owner.go:113 (+1 in migrations) | **keep** |
| `learned_views` | Last suggestion each node has seen | 2 | 3 | 1: learned_owner.go:118 (+1 in migrations) | 1: learned_owner.go:55 | **keep** |
| `mcp_import_plans` | Plan to import MCP servers | 4 | 0 | 2: mcp_import_apply.go:161, mcp_import_apply.go:280 | 1: mcp_import_apply.go:235 | **merge into tool_plans**. same columns as tool_plans; add kind |
| `mcp_imports` | Imported MCP server records | 2 | 0 | 2: mcp_import_apply.go:213, mcp_scope.go:233 | 1: mcp_import.go:30 | **keep**. empty but used |
| `mcp_native_plans` | Plan to write native MCP config | 4 | 0 | 2: mcp_native_remove.go:157, mcp_native_remove.go:188 | 1: mcp_native_remove.go:169 | **merge into tool_plans**. same columns as tool_plans plus state |
| `mcp_servers` | Shared MCP servers and their health | 12 | 2 | 5: mcp_store.go:80, mcp_store.go:141 | 10: mcp_import.go:30, mcp_import.go:231 (+1 in migrations) | **keep** |
| `member_blocked_notices` | Hash of the last blocked notice sent per session | 3 | 2 | 2: member_blocked.go:210, member_blocked.go:261 | 1: member_blocked.go:236 | **keep**. takes the tick column |
| `member_blocked_ticks` | Hash seen at the last tick per session | 2 | 0 | 3: member_loop.go:120, member_loop.go:156 | 1: member_loop.go:165 | **columns of member_blocked_notices**. same key (session_id) |
| `merge_train_fails` | Failed merge trains in a row per project | 4 | 1 | 2: intake_feeds.go:119, intake_feeds.go:136 | 1: intake_feeds.go:128 | **keep** |
| `message_files` | File list attached to a message | 2 | 455 | 2: store_record.go:471, store.go:5171 | 1: store.go:4684 | **columns of messages**. one row per message; 455 of 20615 |
| `messages` | Messages between sessions | 31 | 20615 | 77: not_started_notice.go:80, not_started_notice.go:101 (+9 in migrations) | 122: session_stall.go:56, withdraw.go:75 (+9 in migrations) | **keep** |
| `model_failures` | Last failure of a model on a machine | 5 | 4 | 2: store_record.go:574, store_record.go:582 | 2: store_record.go:624, store_record.go:706 | **keep**. takes the success time |
| `model_prices` | Last good copy of the public price list | 6 | 472 | 2: prices.go:100, prices.go:105 | 1: prices.go:70 | **keep** |
| `model_successes` | Last success of a model on a machine | 4 | 44 | 1: store_record.go:649 | 2: store_record.go:659, store_record.go:707 | **columns of model_failures**. same key (node, harness, model) |
| `node_graph_refs` | Whether a machine supports graph refs | 3 | 2 | 1: graph_refs.go:32 | 2: server.go:1335, graph_refs.go:51 | **columns of nodes**. its version column repeats nodes.version (2 of 2 equal) |
| `node_health` | Last health report of a machine | 3 | 2 | 2: nodes.go:296, nodes.go:303 | 1: nodes.go:311 | **columns of nodes**. one row per machine |
| `node_scopes` | What a node accepts and refuses | 6 | 12 | 2: node_scopes.go:153, node_scopes.go:161 | 4: org_tree.go:689, agent_scopes.go:33 | **keep** |
| `node_scopes_history` | Old and new value of each scope change | 8 | 12 | 1: node_scopes.go:168 | 0 | **drop**. copy of graph_history type scope (12 rows both sides); never read; drop when the double-write fix lands |
| `node_silence` | Sleep window and drop times of a machine | 5 | 0 | 6: nodes_sleep.go:101, nodes_sleep.go:104 | 5: nodes.go:154, nodes_sleep.go:92 | **columns of nodes**. one row per machine; empty |
| `node_tailscale` | Tailscale address of a machine | 3 | 2 | 2: nodes.go:136, nodes.go:139 | 1: nodes.go:153 | **columns of nodes**. one row per machine |
| `node_time_zones` | Time zone of a machine | 3 | 2 | 1: nodes.go:124 | 1: nodes.go:153 | **columns of nodes**. one row per machine |
| `nodes` | Enrolled machines | 14 | 2 | 11: store.go:3433, store.go:3458 (+1 in migrations) | 48: store_inbox.go:754, mcp_import.go:165 (+2 in migrations) | **keep** |
| `nudge_fires` | Each nudge sent and whether it was followed | 10 | 743 | 4: nudge_fires.go:46, nudge_fires.go:66 | 3: nudge_fires.go:79, nudge_fires.go:154 | **keep** |
| `nudge_pauses` | Pauses on nudges | 13 | 2 | 5: nudge_pause.go:59, nudge_pause.go:197 | 3: nudge_pause.go:35, nudge_pause.go:237 | **keep** |
| `oauth_clients` | Registered OAuth clients | 8 | 2 | 6: oauth_store.go:97, oauth_store.go:114 | 5: oauth_store.go:107, oauth_store.go:111 | **keep** |
| `oauth_code_misses` | Failed code attempts per source, for the rate limit | 2 | 0 | 2: oauth_store.go:238, oauth_store.go:252 | 2: oauth_store.go:242, server.go:3347 | **keep**. takes a kind column |
| `oauth_codes` | Hashed authorization codes | 9 | 1 | 4: oauth_store.go:402, oauth_store.go:488 | 4: oauth_store.go:373, oauth_store.go:389 | **keep** |
| `oauth_pending` | Pending device and browser sign-ins | 15 | 0 | 10: oauth_store.go:111, oauth_store.go:172 | 12: oauth_store.go:189, oauth_store.go:207 | **keep**. empty but used; 2 columns no code names |
| `oauth_registration_attempts` | Client registrations per source, for the rate limit | 2 | 0 | 3: oauth_store.go:122, oauth_store.go:125 | 1: oauth_store.go:101 | **merge into oauth_code_misses**. same two columns (source, time) |
| `outcome_overrules` | Outcomes the owner overruled | 2 | 3 | 1: store_outcome.go:85 | 1: store.go:4683 | **keep** |
| `owner_settings` | Key-value store: owner settings and one-time markers | 3 | 23 | 34: cto_settings.go:47, cto_settings.go:90 (+1 in migrations) | 57: cto_settings.go:23, cto_settings.go:54 (+1 in migrations) | **keep**. 11 of 23 rows are markers; they move to gateway_state |
| `permission_proposals` | Permission changes proposed by agents | 15 | 23 | 7: permissions.go:216, permissions.go:552 | 6: permissions.go:420, permissions.go:475 | **keep** |
| `pick_candidates` | Every candidate of every automatic worker pick | 39 | 3716 | 1: worker_assessment.go:83 | 0 | **keep**. no reader in the gateway; read by tools/pickerreplay and research/picker-ml |
| `playbook` | Notes about models and harnesses | 11 | 15 | 4: track.go:352, track.go:360 (+3 in migrations) | 3: track.go:379, track.go:390 | **keep** |
| `playbook_notes` | Old model notes, replaced by playbook | 4 | 0 | 0 | 0 (+1 in migrations) | **drop**. 0 rows; read only by migration (store.go:292); still named in topic_events.go:15 |
| `profiles` | What each session announced (task, expertise) | 9 | 85 | 9: store_record.go:477, session_replace.go:206 (+2 in migrations) | 67: session_stall.go:59, store_inbox.go:754 (+4 in migrations) | **keep** |
| `project_bar_next` | Next rule number per project | 2 | 9 | 1: store_bar.go:229 | 1: store_bar.go:211 | **keep**. equals max+1 today, but rules can be deleted (store_bar.go:205), so the counter stops reuse |
| `project_bar_rules` | The bar rules of a project | 5 | 120 | 3: store_bar.go:200, store_bar.go:205 | 2: store_bar.go:37, store_bar.go:62 | **keep** |
| `project_display_names` | Display name override of a project | 2 | 0 | 2: project_display.go:48, project_display.go:50 | 2: store.go:4330, project_display.go:14 | **keep**. empty but used |
| `project_mergers` | The merger session of a project per machine | 3 | 1 | 6: session_replace.go:187, merger_restore.go:50 (+2 in migrations) | 23: lead_session.go:35, caller_agent.go:39 (+3 in migrations) | **keep** |
| `project_policies` | Policy of a project on a machine | 4 | 0 | 2: store_policy.go:93, store_policy.go:95 (+2 in migrations) | 4: worker_sandbox_settings.go:142, store_policy.go:85 (+1 in migrations) | **keep**. empty but used |
| `projects` | Registered project folders per machine | 10 | 21 | 3: store_projects.go:78, store_projects.go:87 (+1 in migrations) | 21: worker_sandbox_settings.go:29, worker_sandbox_settings.go:175 (+3 in migrations) | **keep** |
| `quota_blocks` | Provider quota blocks per machine | 7 | 4 | 1: store_record.go:594 | 1: store_record.go:603 | **keep** |
| `quota_budgets` | Learned token budget per provider window | 4 | 2 | 1: quota_burn.go:107 | 1: quota_burn.go:72 | **keep** |
| `repo_held_back` | Held-back flag of a repo | 2 | 0 | 4: authority.go:221, authority.go:223 | 2: authority.go:60, place_node.go:144 | **columns of repos**. one row per repo; empty |
| `repo_leads` | Lead override of a repo | 2 | 3 | 3: store_graph.go:728, store_graph.go:730 | 12: lead_session.go:59, store.go:4343 (+5 in migrations) | **columns of repos**. read as COALESCE(repo_leads.agent_id, repos.owner_agent_id) in 8 places |
| `repo_start_reservations` | Short reservation while a repo session starts | 6 | 0 | 7: session_max.go:32, session_max.go:39 | 2: session_max.go:62, session_max.go:70 | **keep**. empty but used |
| `repos` | The repos of the org tree | 5 | 43 | 1: graph_lifecycle.go:83 (+18 in migrations) | 44: lead_session.go:58, authority.go:203 (+8 in migrations) | **keep** |
| `retired_session_wakes` | Last wake attempt of a retired session | 2 | 37 | 1: session_wake.go:32 | 0 | **columns of session_wakes**. same key; read through its upsert (session_wake.go:32) |
| `schema_version` | Applied migration numbers | 1 | 219 | 0 (+1 in migrations) | 3: store_upgrade_backup.go:63, store_pool.go:123 (+1 in migrations) | **keep** |
| `session_agent_history` | Each move of a session to another agent | 6 | 2 | 1: session_agent.go:105 | 0 | **drop**. 2 rows; one writer (session_agent.go:105); no reader |
| `session_agent_start_failures` | Why an agent session start failed | 8 | 0 | 2: session_start_agent_expiry.go:74, session_start_agent_expiry.go:100 | 4: session_start_agent.go:37, session_start_agent.go:120 | **keep**. empty but used |
| `session_agent_starts` | Agent session starts in flight | 10 | 0 | 7: session_start_agent.go:61, session_start_agent.go:78 | 6: session_start_agent.go:90, session_start_agent.go:120 | **keep**. empty but used |
| `session_checks` | State of the check loop per session | 11 | 51 | 7: check.go:346, check.go:393 | 3: notice_lifecycle.go:20, check.go:62 | **keep**. 51 of 775 sessions; kept apart from the 54-column sessions row |
| `session_wake_failures` | Last wake failure of a session | 3 | 8 | 2: wake_failure.go:86, wake_failure.go:198 | 3: caller_agent.go:214, wake_failure.go:190 | **columns of session_wakes**. same key |
| `session_wakes` | Wake counters per session | 6 | 4 | 2: check.go:576, loop.go:420 | 4: check.go:62, check.go:413 | **keep**. takes the two tables above |
| `sessions` | Agent sessions | 54 | 775 | 65: track.go:98, store_record.go:480 (+10 in migrations) | 301: session_stall.go:33, session_stall.go:59 (+23 in migrations) | **keep** |
| `setting_history` | Every settings change, used by undo | 13 | 17 | 7: delivery_pipeline_settings.go:66, worker_sandbox_settings.go:446 | 4: settings.go:792, settings.go:846 | **keep** |
| `setting_revision` | Current settings revision (one row) | 2 | 1 | 5: settings.go:63, settings.go:258 (+2 in migrations) | 13: delivery_pipeline_settings.go:41, learned_owner.go:143 | **keep**. read inside settings transactions in 13 places; left alone |
| `setting_values` | Settings by key, level, scope and item | 6 | 15 | 9: delivery_pipeline_settings.go:55, delivery_pipeline_settings.go:71 (+1 in migrations) | 22: delivery_pipeline_settings.go:14, cto_graph.go:119 | **keep** |
| `settings_target_identity` | Stable token for a settings target | 3 | 60 | 1: settings_targets.go:114 (+10 in migrations) | 2: settings_targets.go:103, settings_targets.go:117 (+1 in migrations) | **keep** |
| `signal_prompts` | Last prompt of a session, kept 10 minutes | 3 | 1 | 3: store_record.go:498, signal.go:25 | 1: signal.go:56 | **keep** |
| `skill_archive` | Every version of every skill | 17 | 112 | 6: skillarchive.go:301, skillarchive.go:312 (+1 in migrations) | 16: skillarchive_read.go:155, skillarchive.go:163 (+4 in migrations) | **keep** |
| `skill_archive_blocks` | Blocked skills and versions | 4 | 26 | 4: skillarchive.go:321, skillarchive.go:390 (+2 in migrations) | 1: skillarchive.go:248 | **keep** |
| `skill_archive_changes` | Skill archive changes, used by undo | 10 | 126 | 4: skillarchive.go:332, skillarchive.go:410 | 1: skillarchive.go:430 (+2 in migrations) | **keep** |
| `skill_archive_latest` | The version in use of each skill | 2 | 88 | 2: skillarchive.go:192, skillarchive.go:209 (+1 in migrations) | 3: skillarchive.go:163, skillarchive.go:173 | **keep**. not derivable: 7 of 88 point below the highest version |
| `skill_archive_meta` | Two markers of the git intake | 2 | 2 | 2: skillarchive.go:976, skillarchive.go:997 | 1: skillarchive.go:989 | **rows of gateway_state** |
| `skill_list_changes` | Skill list changes, used by undo | 8 | 9 | 2: skill_lists.go:604, skill_lists.go:708 | 1: skill_lists.go:688 | **keep** |
| `skill_node_lists` | Skills added, removed or pinned per level | 7 | 126 | 4: skill_lists.go:591, skill_lists.go:596 | 2: skill_lists.go:94, skill_lists.go:449 | **keep** |
| `skill_suggestions` | Stored skill suggestions (feature removed) | 6 | 5 | 0 | 0 | **drop**. 5 rows; no reader and no writer; decision 0169 says unused |
| `skill_versions` | Skill versions seen on machines | 7 | 19 | 1: skillstore.go:143 (+1 in migrations) | 16: skillstore.go:83, skillstore.go:140 | **keep** |
| `standing_order_history` | Old text of changed notes, written by a trigger | 7 | 60 | 1: notes.go:37 (+4 in migrations) | 1: standing.go:165 | **keep** |
| `standing_orders` | Notes and owner judgments | 18 | 170 | 9: standing.go:88, standing.go:100 (+1 in migrations) | 15: standing.go:18, standing.go:38 (+1 in migrations) | **keep** |
| `standing_orders_fts` | Full-text index of notes | 2 | 170 | 2: notes.go:35, notes.go:36 (+9 in migrations) | 2: standing.go:144, standing.go:269 | **keep**. FTS5 |
| `standing_orders_fts_config` | FTS5 internal table | 2 | 1 | 0 | 0 | **keep**. made by SQLite |
| `standing_orders_fts_data` | FTS5 internal table | 2 | 35 | 0 | 0 | **keep**. made by SQLite |
| `standing_orders_fts_docsize` | FTS5 internal table | 2 | 170 | 0 | 0 | **keep**. made by SQLite |
| `standing_orders_fts_idx` | FTS5 internal table | 3 | 33 | 0 | 0 | **keep**. made by SQLite |
| `start_override_facts` | Facts of a model override ask | 9 | 10 | 1: start_override.go:41 | 1: start_override.go:50 | **keep** |
| `table_versions` | Change counter of worker_runs, moved by triggers | 2 | 1 | 0 (+4 in migrations) | 1: runs_snapshot.go:46 | **keep**. one row; trigger-driven, left alone |
| `team_history` | History of team changes | 10 | 41 | 6: bot_agent.go:30, bot_names.go:154 | 1: teams.go:347 | **merge into graph_history**. lead changes are written to both (store_graph.go:747 and 751, leads_max.go:211 and 215); one reader (teams.go:347) |
| `team_members` | Which repo or agent is in which team | 3 | 19 | 4: bot_agent.go:32, bot_names.go:156 | 40: store_inbox.go:809, member_loop.go:60 | **keep** |
| `teams` | Teams | 11 | 4 | 11: teams.go:121, teams.go:150 | 52: store_inbox.go:809, member_loop.go:60 (+6 in migrations) | **keep** |
| `tool_accepted` | Tool findings the owner accepted | 2 | 0 | 2: tools_accepted.go:27, tools_accepted.go:30 | 1: tools_accepted.go:8 | **keep**. empty but used |
| `tool_audit` | Audit log of tool plans and applies | 10 | 109 | 3: tools_assignment.go:492, tools_assignment.go:531 | 1: tools_assignment.go:496 | **keep** |
| `tool_identities` | Known tools and skills by hash | 4 | 63 | 5: inventory.go:59, inventory.go:66 (+2 in migrations) | 28: tools_choose.go:17, tools_choose.go:27 (+1 in migrations) | **keep** |
| `tool_locations` | Where each tool was seen on a machine | 4 | 247 | 3: inventory.go:51, inventory.go:62 (+1 in migrations) | 12: mcp_import.go:164, tools_choose.go:27 | **keep** |
| `tool_plans` | Plan to roll a tool out | 5 | 76 | 7: tools_assignment.go:352, tools_assignment.go:468 | 9: inventory.go:375, tools_feed.go:14 | **keep**. takes the two MCP plan tables |
| `tool_project_seen` | Projects a tool was seen in | 2 | 3306 | 4: tools_toggle.go:87, tools_toggle.go:202 | 2: tools_toggle.go:538, tools_toggle.go:577 | **keep** |
| `tool_project_settings` | Choice for new projects per tool | 2 | 92 | 2: tools_toggle.go:78, tools_toggle.go:197 | 3: tools_toggle.go:123, tools_toggle.go:538 | **keep** |
| `tool_results` | Result of a tool rollout per target | 8 | 1327 | 2: tools_feed.go:83, mcp_scope.go:246 | 4: inventory.go:399, tools_feed.go:14 | **keep** |
| `tool_revisions` | Assignment revisions of a tool | 7 | 33 | 2: tools_assignment.go:142, mcp_scope.go:247 | 16: inventory.go:66, inventory.go:276 | **keep** |
| `transfer_parts` | Received parts of a file transfer | 5 | 0 | 2: transfer.go:173, transfer.go:193 | 1: transfer.go:152 | **keep**. empty but used |
| `transfers` | File transfers in flight | 11 | 0 | 3: transfer.go:120, transfer.go:181 | 4: transfer.go:63, transfer.go:101 | **keep**. empty but used |
| `worker_assessments` | Difficulty judge result of a worker start | 21 | 2029 | 2: worker_assessment.go:41, worker_assessment.go:48 | 6: store.go:4338, worker_log.go:277 | **keep** |
| `worker_brief_defects` | Defects blamed on the brief | 9 | 40 | 3: brief_check.go:217, ratings.go:340 | 3: worker_log.go:199, ratings.go:613 | **keep** |
| `worker_brief_rejections` | Briefs the check refused | 5 | 90 | 1: brief_check.go:180 | 2: brief_check.go:188, brief_check.go:196 | **keep** |
| `worker_defect_auto` | Which change made an automatic defect | 5 | 133 | 1: defect_auto.go:104 | 6: defect_auto.go:72, defect_auto.go:74 | **columns of worker_defects**. one row per defect (133 of 217) |
| `worker_defects` | Defects found in a worker result | 17 | 217 | 3: defect_auto.go:92, ratings.go:275 | 10: quality_families.go:177, defect_auto.go:80 | **keep** |
| `worker_rate_offers` | Open offers to rate a worker | 8 | 1543 | 2: session_replace.go:175, ratings.go:87 | 3: ratings.go:58, ratings.go:68 | **keep** |
| `worker_ratings` | Ratings of worker results | 12 | 756 | 1: ratings.go:218 | 4: quality_families.go:214, quality.go:199 | **keep** |
| `worker_runs` | One row per worker run | 34 | 2447 | 9: track.go:76, store_record.go:490 (+2 in migrations) | 28: member_loop.go:208, track.go:19 | **keep** |
| `worker_starts` | Worker start requests and their state | 14 | 1345 | 13: session_replace.go:173, store.go:3750 (+1 in migrations) | 15: track.go:77, context_resolver.go:104 (+1 in migrations) | **keep** |
| `worker_usage` | Token usage of a worker session | 3 | 1795 | 3: prices.go:230, prices.go:334 | 1: prices.go:246 | **keep** |
| `worker_visual_proof` | Whether a worker gave visual proof | 6 | 11 | 1: track.go:71 | 1: scores_weekly.go:68 | **keep** |

24 tables are empty. 14 of those stay (used by code). The other 10 are dropped or folded in above.

## Findings

### Dead tables

| Table | Rows | Evidence |
|---|---|---|
| `chief_corrections` | 0 | No writer. The one reader is the text of a shipped migration (`chief.go:1249-1253`). |
| `playbook_notes` | 0 | No writer. Read only by a migration (`store.go:292`). Still named in the nodes topic list (`topic_events.go:15`). |
| `delivery_settings_legacy` | 1 | Written only by a migration step (`delivery_pipeline_settings.go:46`, called from `store.go:3276`). Never read. |
| `skill_suggestions` | 5 | No reader and no writer anywhere. Decision 0169 says the table stays unused. |
| `session_agent_history` | 2 | One writer (`session_agent.go:105`). No reader. |
| `agent_charter_history` | 53 | One writer (`store_agent_charter.go:51`). No reader outside a migration. `graph_history` holds the same 53 changes. |
| `node_scopes_history` | 12 | One writer (`node_scopes.go:168`). No reader. `graph_history` holds the same 12 changes. |

`pick_candidates` (3716 rows, 39 columns) has no reader in the gateway, but it is not dead. `tools/pickerreplay/data.go:492` and `research/picker-ml/picker_ml/study.py:218` read it from the file, and `docs/reference.md:2775` documents it.

`retired_session_wakes` looks write-only but is read through its upsert: the `WHERE` of the `ON CONFLICT` clause is the back-off check (`session_wake.go:32-34`).

### One decision in two places (proposal 0163)

1. **Team lead history, new.** A lead change writes `graph_history` and `team_history` in the same transaction: `store_graph.go:747` and `751`, `leads_max.go:211` and `215`. This is the same defect as the charter and scope double writes.
2. **Repo lead, new.** `repos.owner_agent_id` holds the lead and `repo_leads` holds an override. Readers must write `COALESCE(repo_leads.agent_id, repos.owner_agent_id)`, and 8 places do (`lead_session.go:59`, `node_scopes.go:241` and `374`, `graph_refs.go:190`, `place_node.go:78`, `org_read.go:20`, and others). A reader that forgets the override gets the wrong lead. Live data: 3 override rows, and only 1 equals the owner column.
3. **Charter values.** Three tables with the same five columns: `graph_charter` (61 rows, repo nodes), `agent_charter` (43 rows) and `charter_values` (6 rows, global and team). `charter_values` already has a level and a scope, so it can hold all three.
4. **Node version.** `node_graph_refs.version` repeats `nodes.version` (equal for 2 of 2 machines).
5. **Worker facts on ratings.** `worker_ratings`, `worker_defects` and `worker_rate_offers` each carry `harness`, `model`, `effort` and `difficulty`, which `worker_runs` also holds. For 542 of 744 ratings the copy differs from the run, because the run stores the model with its effort (`gpt-6-luna:xhigh`) and the rating stores them apart. This is not in the plan below, because the picker reads these columns. It needs its own decision on which side is right.

### Side tables with the parent's key

These hold at most one row per parent row and are read with the parent.

| Parent | Side tables | Rows |
|---|---|---|
| `nodes` (2) | `node_health`, `node_tailscale`, `node_time_zones`, `node_silence`, `node_graph_refs`, `inventory_reports` | 2, 2, 2, 0, 2, 2 |
| `repos` (43) | `archived_repos`, `repo_held_back`, `repo_leads` | 0, 0, 3 |
| `delivery_revisions` (1659) | `delivery_revision_stats` | 1559 |
| `delivery_step_tasks` (5816) | `delivery_step_inbox` | 11 |
| `learned_suggestions` (50) | `learned_suggestion_details`, `learned_phrase_state` | 50, 0 |
| `worker_defects` (217) | `worker_defect_auto` | 133 |
| `messages` (20615) | `message_files` | 455 |
| `session_wakes` (4) | `session_wake_failures`, `retired_session_wakes` | 8, 37 |
| `model_failures` (4) | `model_successes` | 44 |
| `member_blocked_notices` (2) | `member_blocked_ticks` | 0 |

`profiles`, `session_checks`, `worker_usage` and `worker_assessments` also share a parent key. They stay: `sessions` already has 54 columns, and each of these has its own writer and a different life than its parent.

### One-row state tables

| Table | Verdict |
|---|---|
| `graph_cutover_state`, `graph_ref_gate`, `skill_archive_meta` | Rows of the new `gateway_state(key, value, updated_at)`. |
| `owner_settings` | Stays. 11 of its 23 rows are markers and not settings: `rework_backfill_done`, `task_rework_backfill_done`, `asks_moved_on_cleanup_v1`, `front_door_flush_v1`, `cto_default_charter_v1`, `org_tree_built`, `node_scopes_seeded`, `cto_legacy_route_start`, `owed_replies_live_since`, and the three `chief.brief_last_*` keys. They move to `gateway_state`. `owner_settings` is in three topic lists, so today a marker write refreshes the Nodes, Workers and Inbox screens for nothing. |
| `setting_revision` | Stays. It is read inside settings transactions in 13 places. |
| `table_versions` | Stays. Triggers move it and the runs cache reads it. |
| `app_refresh_budget` | Stays. It has one row today but its key is the token family. |
| `project_bar_next` | Stays. It equals the highest rule number plus one for 9 of 9 projects, but rules can be deleted (`store_bar.go:205`) and the counter stops a number from being used twice. |

### History tables

| Table | Rows | Verdict |
|---|---|---|
| `graph_history` | 309 | Keep. It becomes the one history of the org tree. |
| `team_history` | 41 | Merge into `graph_history`. One reader (`teams.go:347`), six writers. |
| `agent_charter_history`, `node_scopes_history` | 53, 12 | Drop (copies). |
| `setting_history` | 17 | Keep. Its key is a revision and a sequence, and undo works on a whole revision. |
| `standing_order_history` | 60 | Keep. A trigger writes it, with the columns of a note. |
| `friction_status_history` | 133 | Keep. `friction_status` is derived from it. |
| `skill_archive_changes`, `skill_list_changes` | 126, 9 | Keep. Each stores a before and after state for its own undo. |

One generic history table for all of these would need a free-form payload column, and each undo would have to parse it. That would save 4 tables and make undo weaker. It is not proposed.

### Families with the same key columns

- **delivery_step_***: `tasks` and `inbox` have the same key and fold together. `messages` adds a session and `delegates` adds a node to the key, so one step has many rows there. They stay.
- **worker_***: `worker_ratings` and `worker_defects` look alike but have different lives (a defect can be moved to a brief defect, has a cause and an escape phase). They stay. `worker_defect_auto` folds into `worker_defects`.
- **skill_archive***: `latest` is a pointer and not the highest version (7 of 88 differ). It stays. `meta` moves to `gateway_state`.
- **tool_*** and **mcp_*_plans**: `tool_plans`, `mcp_import_plans` and `mcp_native_plans` have the same columns. They become one table with a `kind`.
- **learned_***: the two side tables fold into `learned_suggestions`.
- **oauth rate limits**: `oauth_code_misses` and `oauth_registration_attempts` both hold a source and a time. They become one table with a `kind`.

### Caches and counters

- `friction_status`: derived. It equals the latest `friction_status_history` row for 111 of 111 reports.
- `check_answer_counts`, `merge_train_fails`, `quota_budgets`, `model_prices`: not derivable. Their source data is not stored.
- `skill_archive_latest`, `project_bar_next`: look derivable and are not (see above).

### Columns

A search for each of the 1147 non-index columns by name found 2 that no production code names: `oauth_pending.browser_hash` and `oauth_pending.browser_expires_at`. A column with a common name (`at`, `id`) cannot be judged this way, so 2 is a lower bound.

## Proposed schema

118 tables and 1042 columns.

| Change | Tables | Columns |
|---|---|---|
| Today | 155 | 1158 |
| Drop 7 dead tables | -7 | -43 |
| Fold 6 side tables into `nodes` (+12 columns) | -6 | -7 |
| Fold 3 side tables into `repos` (+3 columns) | -3 | -2 |
| One charter store (`charter_values`) | -2 | -10 |
| Fold `graph_nodes` into `repos`, `graph_node_settings` into `setting_values` | -2 | -8 |
| Fold 10 same-key side tables into their parents (+17 columns) | -10 | -18 |
| Derive `friction_status` | -1 | -6 |
| One plan table, one OAuth attempts table (+3 columns) | -3 | -7 |
| `team_history` into `graph_history` | -1 | -10 |
| New `gateway_state`, three one-row tables removed | -2 | -5 |
| Proposed | 118 | 1042 |

Tables that gain columns: `nodes` 14 to 26, `repos` 5 to 8, `delivery_revisions` 6 to 9, `delivery_step_tasks` 16 to 18, `learned_suggestions` 12 to 15, `worker_defects` 17 to 20, `session_wakes` 6 to 9, `messages` 31 to 32, `model_failures` 5 to 6, `member_blocked_notices` 3 to 4, `tool_plans` 5 to 7, `oauth_code_misses` 2 to 3.

Without the `graph_nodes` step, which carries the most risk, the result is 119 tables.

## Risks

1. **Shipped migrations are append-only.** Every step below is a new migration at the end of the list: add the columns, copy the rows, drop the old table, in one transaction. No shipped migration text changes. An old database replays the old migrations first, so the old tables exist when the old steps need them.
2. **Rewind.** `store.go:2049` says a rewind can replay a migration after later ones ran. Each new migration must use `IF EXISTS` and `IF NOT EXISTS` so that a replay does not fail.
3. **No way back without the backup.** A gateway refuses a file with a newer schema version (`store.go:2040`). After a drop, going back to an older binary needs the upgrade backup.
4. **Older daemons.** Daemons do not read tables. They call gateway operations, and only `internal/gateway` opens the file. A merge is safe for them as long as each operation returns the same result. No wire type changes in this plan.
5. **Connected apps.** Apps also go through gateway operations. `apps`, `app_tokens`, `app_refresh_budget` and the `oauth_*` tables keep their shape, except the two rate-limit tables.
6. **Topic lists and the read cache.** Screens refresh from lists of table names (`topic_events.go:14-23`, `nodesTables` at `nodes.go:148`, `calibrationTables`, `workerLogTables`, `learnedListTables`, `graphTables`). A merged table must be replaced in these lists in the same change, or a screen stops refreshing. For `nodes` there is a second effect: a health or inventory report would move the `nodes` stamp, and the Projects and Usage topics read `nodes`. Those lists must use column keys (`nodes.hostname` and so on), which `internal/sqlitecache` supports.
7. **Direct readers of the file.** `tools/pickerreplay` reads `owner_settings`, `model_prices`, `pick_candidates` and the worker tables. `research/picker-ml` reads `pick_candidates` and `worker_assessments`. None of the tables they read change shape here. `worker_profiles` and `worker_rotation` stay in `owner_settings`.
8. **Triggers.** Six triggers name `graph_nodes` (two are on it, four on `projects` and `sessions` test it). They must be rewritten in the migration that removes it. No other removed table has a trigger.
9. **`inventory_reports` is large.** 1.3 MB for 2 machines. As a column of `nodes` it must be the last column, and no `SELECT *` may read `nodes`.
10. **`delivery_step_inbox` without a task row.** Before the fold, check that every inbox row has a task row with the same key. The migration must fail loudly if one does not.

## Migration plan

Each part ships alone. Order matters only where stated. Sizes are counts of statements to change, from the table above; with tests each part is about 1000 lines or less.

| Part | What | Tables removed | Sites to change |
|---|---|---|---|
| 1 | Drop migration (see the list below). Remove the `session_agent_history` writer and the `playbook_notes` name in `topic_events.go:15`. | 7 | 3 |
| 2 | `nodes`: fold `node_health`, `node_tailscale`, `node_time_zones`, `node_silence`, `node_graph_refs`, `inventory_reports`. Move the topic lists that read `nodes` to column keys. | 6 | 30 |
| 3 | `repos`: add `archived`, `held_back`, `lead_agent_id`. One helper returns the lead, and the 8 `COALESCE` copies go. | 3 | 43 |
| 4 | One charter store: move `agent_charter` and `graph_charter` into `charter_values` with levels `agent` and `repo`. | 2 | 43 |
| 5 | Delivery and messages: fold `delivery_revision_stats`, `delivery_step_inbox`, `message_files`. | 3 | 33 |
| 6 | Small same-key folds: learned (2), session wakes (2), `worker_defect_auto`, `model_successes`, `member_blocked_ticks`; derive `friction_status`. | 8 | 62 |
| 7 | One `tool_plans` with a kind, one OAuth attempts table. | 3 | 14 |
| 8 | `gateway_state`: three one-row tables and the 11 marker rows of `owner_settings`. `graph_node_settings` into `setting_values`. | 4 (+1 new) | about 40 |
| 9 | History: stop the team lead double write, then move `team_history` into `graph_history`. | 1 | 7 |
| 10 | `graph_nodes` into `repos`, with its six triggers. Do this last and only after part 3. Needs a short design note first, because the code still knows the kind `architect`. | 1 | 29 |

Parts 2 to 9 do not depend on each other. Two parts built at the same time both add a migration, so the second must rebase on the first.

## Drop list for one migration

Safe now, no code change needed:

```sql
DROP TABLE IF EXISTS chief_corrections;
DROP TABLE IF EXISTS delivery_settings_legacy;
DROP TABLE IF EXISTS skill_suggestions;
```

Safe with a small code change in the same commit:

```sql
DROP TABLE IF EXISTS playbook_notes;        -- also remove the name from topic_events.go:15
DROP TABLE IF EXISTS session_agent_history; -- also remove the INSERT at session_agent.go:105
```

Safe once the double-write fix now in progress has merged:

```sql
DROP TABLE IF EXISTS agent_charter_history; -- writer at store_agent_charter.go:51
DROP TABLE IF EXISTS node_scopes_history;   -- writer at node_scopes.go:168
```

Dropping a table drops its indexes (`agent_charter_history_agent`, `session_agent_history_session`, `node_scopes_history_node`).
