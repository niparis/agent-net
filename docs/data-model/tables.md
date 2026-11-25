# Gateway tables

Every table in the gateway database (`gateway.db`), grouped by the same areas as the Features list.
For each table: what it holds, its columns, how many rows it had, which code writes and reads it,
where the owner sees it, and how long rows are kept.

> A snapshot taken on 2026-11-22 from v0.5.126 (62 tables), by reading the code. It is not generated
> by a tool and is not checked by CI, so it can drift. The code is the reference.

The architecture guide links here. The MCP tools and the tables they touch are in
[tools.md](tools.md).

## Network

### `gateway_state`

A key-value table of what the gateway remembers about itself: graph cutover version and gate, skill archive marks, brief last-sent dates, the change counter of `worker_runs` and one-time seed markers.

- **Columns:** `key`, `value`, `updated_at`
- **Rows on 2026-11-22:** 21
- **Written by:** `gateway/gateway_state.go`
- **Read by:** `gateway/gateway_state.go`, `gateway/runs_snapshot.go`, `gateway/store.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself
- **Kept:** Until overwritten; keys are not deleted. The `worker_runs_version` key is raised by triggers on `worker_runs`.
- **Note:** Also holds the runtime ids that were in owner_settings, caller_escalation:<owner> keys, the network settings token and worker_runs_version.

### `nodes` (core)

One row per machine or invite in the network: its name, system, hashed access token, agent-net version, last-seen time, which agent programs it can run, plus its health report, Tailscale address, time zone and quiet-hours marks. Invite rows in 'invited' state hold the hashed invite code, expiry and used time with no access token.

- **Columns:** `id`, `hostname`, `os`, `token_hash`, `created_at`, `version`, `seen_at`, `harnesses`, `harnesses_at`, `launch`, `reported_hostname`, `display_name`, `removed`, `pending_token_hash`, `health_report`, `health_reported_at`, `tailscale_ip`, `tailscale_updated_at`, `time_zone`, `time_zone_updated_at`, `silence_dropped_at`, `silence_asleep_from`, `silence_asleep_to`, `silence_silent_at`, `graph_refs_supported`, `state`, `code_hash`, `expires_at`, `used_at`
- **Rows on 2026-11-22:** 4
- **Written by:** `gateway/graph_refs.go`, `gateway/machine_tokens.go`, `gateway/node_fold_migration.go`, `gateway/nodes.go`, `gateway/nodes_sleep.go`, `gateway/store.go`
- **Read by:** `gateway/apps_public.go`, `gateway/ask_intake.go`, `gateway/delivery_changes.go`, `gateway/graph_refs.go`, `gateway/lead_route.go`, `gateway/machine_tokens.go`, `gateway/mcp_store.go`, `gateway/nodes.go`, `and others`
- **Shown in:** TUI, Sessions, TUI, Quotas, tool `agent_machines_find`, tool `agent_machine_get`, CTO op `machine_remove`, CTO op `machine_rotate`
- **Kept:** Forever; a removed machine is only marked removed and its token wiped.

### `schema_version`

One row per applied database migration number.

- **Columns:** `version`
- **Rows on 2026-11-22:** 247
- **Written by:** `gateway/store.go`
- **Read by:** `gateway/store.go`, `gateway/store_pool.go`, `gateway/store_upgrade_backup.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself
- **Kept:** Forever; one row added per migration.

### `transfers`

One row per file sent between machines: sender, receiver, file name, size, checksum, part size and number of parts, with an expiry time, a finished time and the list of received parts.

- **Columns:** `id`, `sender`, `receiver`, `name`, `size`, `sha256`, `part_size`, `parts`, `created_at`, `expires_at`, `finished_at`, `parts_received`
- **Rows on 2026-11-22:** 0
- **Written by:** `gateway/transfer.go`
- **Read by:** `gateway/transfer.go`
- **Shown in:** tool `agent_file_send`
- **Kept:** For 7 days after upload, swept hourly together with the stored file parts.

## Sessions

### `member_blocked_notices`

A fingerprint of the last 'blocked' or 'waiting' announce that was told to a team lead, one per session, so the lead is not told twice.

- **Columns:** `session_id`, `text_hash`, `at`, `tick_hash`
- **Rows on 2026-11-22:** 2
- **Written by:** `gateway/member_blocked.go:blockedMemberNotice`, `gateway/member_loop.go:clearMemberTick`, `gateway/member_loop.go:recordMemberTold`, `gateway/side_fold.go:foldMemberTicksTx`
- **Read by:** `gateway/member_blocked.go:blockedMemberNotice`, `gateway/member_loop.go:loopMembers`
- **Shown in:** not shown to the owner
- **Kept:** removed when the session announces something that is not blocked
- **Note:** Audit text updated: columns text_hash and tick_hash were folded in from the lead loop tick table.

### `session_dues`

One row per due step a session set with agent_due: the session, what is due, the due time, and whether it is pending, woken or raised to the chief.

- **Columns:** `id`, `session_id`, `what`, `due_at`, `after_kind`, `after_ref`, `blocker`, `status`, `woken_at`, `created_at`
- **Rows on 2026-11-22:** 36
- **Written by:** `gateway/session_due.go`
- **Read by:** `gateway/session_due.go`
- **Shown in:** tool `agent_due`, the wake message (idempotency key `due:<id>`), a chief ask after 15 minutes idle
- **Kept:** Forever; the state moves from pending to woken or raised.

### `sessions` (core)

One row per agent session (a coding agent running on a machine, a worker, the CTO or the chief): name, folder, repo, status, online state, model, worker data and archive and pin marks. It also holds the owner's last typed prompt to the session (`owner_prompt`, with its time `owner_prompt_at`), what the session announced (`announced_project`, `capabilities`, `task`, `task_started`, `expertise`, `announced_at`), the idle check state (the `check_` columns), and the automatic wake state (the `wake_` columns: the day's count, the no-progress streak, the last refused wake and the last wake attempt). `name_fixed` is 1 when the owner or the merger set the name, so a new report keeps it. A session's project is its detected repo (`repo_project`), else its announced project.

- **Columns:** `id`, `node_id`, `harness`, `native_id`, `name`, `cwd`, `pid`, `status`, `delivery`, `last_seen`, `online`, `repo_root`, `repo_project`, `repo_remote`, `repo_branch`, `repo_worktree`, `mode`, `model`, `worker`, `repo_description`, `peer_trust`, `worker_started`, `tools_version`, `tools_old`, `created_at`, `waiting`, `title`, `graph_excluded`, `last_active`, `no_project`, `worker_title`, `worker_run_active`, `agent_id`, `repo_id`, `worker_run_generation`, `harness_activity`, `harness_lost`, `archived_at`, `owner_used_at`, `owner_uses`, `pinned_at`, `closed_at`, `idle_compacted_at`, `limited_until`, `limited_reason`, `worker_retained`, `busy_since`, `replaced_by`, `last_compacted_reason`, `owner_typed_at`, `started_by`, `turn_error`, `turn_error_at`, `owner_prompt`, `owner_prompt_at`, `function`, `capabilities`, `task`, `expertise`, `task_started`, `announced_at`, `name_fixed`, `announced_project`, `check_active`, `check_waiting`, `check_more`, `check_open`, `check_msgs`, `check_day`, `check_day_count`, `check_task_hash`, `check_announced_at`, `check_fingerprint`, `wake_day`, `wake_day_count`, `wake_streak`, `wake_open`, `wake_msgs`, `wake_failed_at`, `wake_failed_reason`, `wake_attempted_at`, `rate_offered_at`, `rate_asked`
- **Rows on 2026-11-22:** 789
- **Written by:** `gateway/apps.go`, `gateway/bot_agent.go`, `gateway/bot_names.go`, `gateway/bot_session.go`, `gateway/busy_hold.go`, `gateway/chief.go`, `gateway/cto_session.go`, `gateway/machine_tokens.go`, `gateway/merger.go`, `gateway/org_tree.go`, `and others`
- **Read by:** `gateway/ask_holder.go`, `gateway/ask_intake.go`, `gateway/asks.go`, `gateway/asks_to.go`, `gateway/authority.go`, `gateway/automatic_message.go`, `gateway/bot_names.go`, `gateway/bot_session.go`, `gateway/bot_tools.go`, `gateway/busy_hold.go`, `and others`
- **Shown in:** TUI, Sessions, TUI, Graph, tool `agent_sessions_find`, tool `agent_session_get`, CTO op `session_change`, CTO op `sessions_archive`
- **Kept:** Offline sessions are archived after 7 days and kept; an un-archived offline session unused for 30 days is deleted by the hourly prune; cleaned workers are deleted on request. The owner prompt is cleared at the session's first send, and after 10 minutes at most (hourly prune).
- **Note:** Also holds the rate offer state (rate_offered_at, rate_asked) and the owner prompt (owner_prompt, owner_prompt_at). The delegation column is removed with the delegation policy.

## Communication

### `deliveries`

One row per delivery state change of a message (stored, delivered, failed and so on) with detail text and time.

- **Columns:** `id`, `message_id`, `state`, `detail`, `at`
- **Rows on 2026-11-22:** 26012
- **Written by:** `gateway/store.go:ApplyDeliveryReport`, `gateway/store_record.go:Prune`
- **Read by:** `gateway/store.go:agentsBaseOwned`, `gateway/undelivered.go:expiryNotice`
- **Shown in:** TUI: Messages, tool: agent_message_get
- **Kept:** for ever by default (--keep-messages is 0); when set, deleted with their messages by the hourly prune

### `delivery_retries`

One row per message that is waiting to be delivered again, with the number of attempts so far and the time of the next try.

- **Columns:** `message_id`, `attempts`, `next_at`
- **Rows on 2026-11-22:** 0
- **Written by:** `gateway/place.go:StoreHandoff`, `gateway/store.go:ApplyDeliveryReport`, `gateway/store.go:expireMessage`, `gateway/store.go:storeMessage`, `gateway/withdraw.go:removeFromBatch`, `gateway/withdraw.go:withdrawOne`
- **Read by:** `gateway/retry.go:RunRetries`, `gateway/server.go:sendDeliverWith`, `gateway/session_stall.go:checkSessionStalls`, `gateway/store.go:ApplyDeliveryReport`, `gateway/store.go:Expire`, `gateway/store.go:agentsBaseOwned`, `gateway/store.go:expireMessage`, `gateway/store.go:upsertSession`
- **Shown in:** not shown to the owner
- **Kept:** until the message is delivered, answered, expired or withdrawn
- **Note:** Area corrected from delivery: it is about message delivery, not the delivery pipeline. Rows are also deleted in storeMessage, expireMessage, withdrawOne, StoreHandoff and removeFromBatch.

### `events` (core)

One row per gateway event in the audit log: time, kind, machine, session, message and a detail line.

- **Columns:** `id`, `at`, `kind`, `node_id`, `session_id`, `message_id`, `detail`
- **Rows on 2026-11-22:** 84960
- **Written by:** `gateway/store.go:event`, `gateway/store_record.go:Prune`
- **Read by:** `gateway/graph_talk.go:GraphTalkSnapshot`, `gateway/store.go:Events`, `gateway/store_record.go:QueryEvents`, `gateway/store_record.go:feedEventsBefore`
- **Shown in:** TUI: Messages, TUI: Graph
- **Kept:** 90 days, pruned hourly
- **Note:** Events are read through query.events; the Messages tab feed and the Graph tab talk feed use them. Area kept as messages although it is a general audit log.

### `messages` (core)

One row per message between sessions, the owner and bots: sender, recipient, type, body, thread, state, expiry, urgency, hold and handoff data and attached file ids. A request that carries the owner's prompt keeps that prompt (`owner_prompt`).

- **Columns:** `id`, `conversation_id`, `parent_id`, `sender`, `recipient`, `type`, `body`, `hop`, `idem_key`, `created_at`, `expires_at`, `state`, `sender_cto`, `owner_approved`, `approval_note`, `reminded`, `worker_task`, `handed_off_by`, `is_order`, `urgent`, `held_for_busy`, `held_at`, `batch_id`, `batch_json`, `withdrawn_at`, `pair_gid`, `worker_lifecycle`, `blocked`, `worker_side`, `hold_kind`, `hold_until`, `files`, `progress`, `progress_until`, `owner_prompt`
- **Rows on 2026-11-22:** 23880
- **Written by:** `gateway/agentnet_reply.go`, `gateway/automatic_message.go`, `gateway/bot_tools.go`, `gateway/busy_hold.go`, `gateway/caller_agent.go`, `gateway/check.go`, `gateway/chief.go`, `gateway/cto_lead.go`, `gateway/delivery_reconcile.go`, `gateway/delivery_tasks.go`, `and others`
- **Read by:** `gateway/ask_holder.go`, `gateway/asks.go`, `gateway/brief_check.go`, `gateway/busy_hold.go`, `gateway/check.go`, `gateway/cto_lead.go`, `gateway/delivery_tasks.go`, `gateway/follow_ups.go`, `gateway/graph_talk.go`, `gateway/held.go`, `and others`
- **Shown in:** TUI, Messages, TUI, Desk, tool `agent_message_send`, tool `agent_message_reply`, tool `agent_messages_find`, tool `agent_message_get`, tool `agent_message_withdraw`, tool `agent_message_handoff`
- **Kept:** Forever by default (the gateway flag --keep-messages is 0); when set, older rows go on the hourly prune except bot mail not yet read and rows behind open reject or escalate outcomes. The owner prompt on a message is cleared after 90 days (hourly prune).

### `nudge_fires`

One row per automatic nudge sent to an agent: its kind, recipient, team and message, when it fired and whether and how work followed within 30 minutes.

- **Columns:** `id`, `kind`, `recipient`, `agent_id`, `team_id`, `message_id`, `fired_at`, `followed_at`, `followed_kind`, `checked_at`
- **Rows on 2026-11-22:** 975
- **Written by:** `gateway/nudge_fires.go:TouchNudgeWork`, `gateway/nudge_fires.go:markNudgeFollows`, `gateway/nudge_fires.go:recordNudgeFire`
- **Read by:** `gateway/nudge_fires.go:markNudgeFollows`, `gateway/nudge_fires.go:nudgeStats`, `gateway/nudge_fires.go:nudgeUseful`
- **Shown in:** tool: agent_nudge_stats
- **Kept:** for ever; no code deletes it
- **Note:** Area changed from sessions to messages: each row points to a message.

### `nudge_pauses`

One row per pause of automatic nudges for a team or an agent: kind, until when, reason, who paused and ended it, and how to undo it.

- **Columns:** `id`, `kind`, `team_id`, `agent_id`, `until_at`, `provider_reset`, `reason`, `created_at`, `created_by`, `ended_at`, `ended_by`, `receipt`, `undo_action`
- **Rows on 2026-11-22:** 2
- **Written by:** `gateway/nudge_pause.go:PauseNudge`, `gateway/nudge_pause.go:ResumeNudge`, `gateway/nudge_pause.go:expireNudgePauses`, `gateway/nudge_pause.go:undoNudge`
- **Read by:** `gateway/nudge_pause.go:(package level)`, `gateway/nudge_pause.go:FindNudgePauses`, `gateway/nudge_pause.go:readNudge`
- **Shown in:** tool: agent_nudge_pause, tool: agent_nudge_resume, tool: agent_nudge_pauses_find
- **Kept:** for ever; ended pauses are kept on purpose
- **Note:** Area changed from sessions to messages (nudges are automatic messages). expireNudgePauses only marks rows ended; the comment says ended rows are preserved.

## Organization

### `agent_loops`

One row per org-graph node that has a standing loop: when it started, the last tick, the day's tick count, the no-progress streak and whether the loop is paused or waiting.

- **Columns:** `node`, `started_at`, `last_at`, `sent_at`, `session_id`, `day`, `day_ticks`, `day_progress`, `streak`, `last_open`, `last_msgs`, `announced`, `awaiting`, `counted`, `paused`, `last_result`
- **Rows on 2026-11-22:** 1
- **Written by:** `gateway/loop.go:answerLoopAsk`, `gateway/loop.go:loopAnnounce`, `gateway/loop.go:loopOne`, `gateway/loop.go:loopPause`, `gateway/loop.go:loopRemove`, `gateway/loop.go:loopTick`
- **Read by:** `gateway/loop.go:loadLoops`, `gateway/loop.go:loopAnnounce`
- **Shown in:** TUI: Graph
- **Kept:** until the node's loop setting is removed
- **Note:** Area changed from machines to org-graph: node is a graph node. The Graph tab node card shows a Loop block (tui/map_card.go).

### `agents` (core)

One agent of the org tree: a named role (coding agent, bot, lead) with its team (`team_id`), its pinned lead session (`lead_session`), its explicit lead-level grant (`lead_level`), and a tombstone flag when it is retired. `agents.lead_session` and `agents.lead_level` are the single source for an agent's lead session and explicit lead-level grant.

- **Columns:** `id`, `display_name`, `role`, `created_at`, `updated_at`, `tombstone`, `lead_level`, `lead_session`, `settings_token`, `team_id`
- **Rows on 2026-11-22:** 52
- **Written by:** `gateway/bot_agent.go`, `gateway/bot_names.go`, `gateway/graph_lifecycle.go`, `gateway/lead_session.go`, `gateway/lead_session_migration.go`, `gateway/leads_max.go`, `gateway/org_tree.go`, `gateway/repo_fold_migration.go`, `gateway/session_replace.go`, `gateway/store.go`, `and others`
- **Read by:** `gateway/agent_scopes.go`, `gateway/asks_to.go`, `gateway/authority.go`, `gateway/bot_agent.go`, `gateway/caller_agent.go`, `gateway/charter_drafts.go`, `gateway/cto_charter_default.go`, `gateway/cto_lead.go`, `gateway/friction.go`, `gateway/graph_lifecycle.go`, `and others`
- **Shown in:** TUI, Graph, tool `agent_graph_read`, CTO op `agent_create`, CTO op `agent_tombstone`, CTO op `lead_set`
- **Kept:** Forever (a retired agent is only flagged as tombstoned).

### `charter_drafts`

One charter value that a session drafted from the project AGENTS.md (`pending`, `accepted`, `rejected`, `superseded`), or an `asked` marker (`status='asked'`, `source_hash`, `asked_at`) so a session is not asked twice for the same source text.

- **Columns:** `id`, `level`, `scope`, `attr`, `value`, `source`, `source_hash`, `status`, `drafted_by`, `created_at`, `decided_at`, `decided_by`, `asked_at`
- **Rows on 2026-11-22:** 112
- **Written by:** `gateway/charter_drafts.go`
- **Read by:** `gateway/charter_drafts.go`, `gateway/chief.go`, `gateway/store_inbox.go`
- **Shown in:** TUI, Graph, TUI, Desk, tool `agent_charter_draft`, CTO op `needs_you`
- **Kept:** Forever (the status changes to accepted or rejected, the row stays; the `asked` row is replaced when the source text changes).

### `charter_values`

One charter setting (a rule or text) for a level (global, team, agent or repo), a scope and an attribute, with who set it and when.

- **Columns:** `level`, `scope`, `attr`, `value`, `updated_at`, `updated_by`
- **Rows on 2026-11-22:** 110
- **Written by:** `gateway/charter_levels.go:setLevelCharter`, `gateway/charter_store.go:putCharterValueTx`, `gateway/graph_cutover_auto.go:enableGraphRefsAutomatically`, `gateway/store_graph.go:RemoveArchitect`, `gateway/store_graph.go:graphUndo`
- **Read by:** `gateway/agent_charter.go:applyAgentChartersWith`, `gateway/charter_levels.go:setLevelCharter`, `gateway/charter_resolver.go:agentCharter`, `gateway/charter_resolver.go:globalCharter`, `gateway/charter_resolver.go:preloadAgents`, `gateway/charter_resolver.go:preloadRepos`, `gateway/charter_resolver.go:preloadTeams`, `gateway/charter_resolver.go:repoCharter`, `gateway/charter_resolver.go:teamCharter`, `gateway/charter_store.go:charterValueTx`, `gateway/charter_store.go:ensureOldCharterTablesTx`, `gateway/chief.go:askFullLearned`, `gateway/cto_graph.go:CTOGraphClass`, `gateway/cto_graph.go:ctoGraphClassTx`, `gateway/graph_refs.go:graphForRefRecords`, `gateway/graph_view.go:visibleGraphRead`, `gateway/learned.go:learnedAdd`, `gateway/learned_apply.go:ProcessLearnedContext`, `gateway/learned_owner.go:decideLearned`, `gateway/learned_owner.go:ownerLearnedRead`, `gateway/learned_owner.go:settleLearnedDone`, `gateway/settings.go:settingsSnapshotTx`, `gateway/store_graph.go:RemoveArchitect`, `gateway/store_graph.go:graphUndo`, `gateway/store_graph.go:graphWith`
- **Shown in:** TUI: Graph, tool: agent_graph_read, tool: agent_charter_draft, CTO: charter_set, CTO: charter_history
- **Kept:** until the value is cleared, undone, or its repo is removed
- **Note:** Audit text updated: levels agent and repo were folded in from agent_charter and graph_charter. Repo rows are deleted by RemoveArchitect.

### `graph_edges` (core)

One typed link between two nodes of the org tree (`depends`, `deploys`, or a `/delivery/*` step owner), with its mode and rule. `reports_to` is computed on read from leadership and team membership. Only an explicit move by the CTO or the owner (`edge_set`) is stored here, and it wins over the computed parent.

- **Columns:** `source`, `target`, `kind`, `mode`, `rule`, `source_kind`, `target_kind`
- **Rows on 2026-11-22:** 59
- **Written by:** `gateway/edge_kinds.go`, `gateway/held.go`, `gateway/merger.go`, `gateway/merger_restore.go`, `gateway/settings.go`, `gateway/store_graph.go`
- **Read by:** `gateway/cto_graph.go`, `gateway/graph_lifecycle.go`, `gateway/graph_refs.go`, `gateway/graph_view.go`, `gateway/graphread.go`, `gateway/held.go`, `gateway/learned.go`, `gateway/learned_apply.go`, `gateway/learned_owner.go`, `gateway/merger.go`, `and others`
- **Shown in:** TUI, Graph, tool `agent_graph_read`, CTO op `edge_set`, CTO op `edge_remove`, CTO op `merger_set`
- **Kept:** Until the link is removed or an undo reverts it.
- **Note:** Stored reports_to rows are deleted: reports_to is now computed from repos.lead_agent_id, teams.owner_agent_id and agents.team_id. An explicit edge_set reports_to is still stored and wins.

### `graph_history`

One recorded change to the org tree (charter value, link, lifecycle or team change) with old and new value, who made it and when. Undo and the history views read it.

- **Columns:** `id`, `type`, `node`, `attr`, `old_value`, `new_value`, `at`, `actor`, `source_kind`, `target_kind`
- **Rows on 2026-11-22:** 331
- **Written by:** `gateway/edge_kinds.go:edgeKindsMigration`, `gateway/graph_cutover_auto.go:enableGraphRefsAutomatically`, `gateway/graph_lifecycle.go:recordLifecycle`, `gateway/leads_max.go:SetLead`, `gateway/store_graph.go:graphRecord`, `gateway/store_graph.go:graphUndo`, `gateway/team_history_migration.go:(package level)`
- **Read by:** `gateway/charter_drafts.go:DecideCharterDraftRow`, `gateway/cto_graph.go:CTOGraphClass`, `gateway/graph_refs.go:RefCharterHistory`, `gateway/graph_view.go:graphView`, `gateway/learned_apply.go:ProcessLearnedContext`, `gateway/learned_owner.go:MarkLearnedViewed`, `gateway/learned_owner.go:decideLearned`, `gateway/learned_owner.go:ownerLearnedRead`, `gateway/store_graph.go:CTOGraphHistory`, `gateway/store_graph.go:CTOLastGraphID`, `gateway/store_graph.go:CharterHistory`, `gateway/store_graph.go:edgeHistory`, `gateway/store_graph.go:graphUndo`, `gateway/team_history_migration.go:(package level)`, `gateway/teams.go:TeamHistory`
- **Shown in:** TUI: Graph, CTO: graph_undo, CTO: charter_history, CTO: edge_history, CTO: team_history
- **Kept:** for ever
- **Note:** Audit text updated: team changes are folded in here from team_history. No DELETE found in production code.

### `graph_outcomes`

How one request between two nodes ended: a reply with an outcome (accept, reject, escalate or redirect), the reason and recommendation, whether the owner resolved it, and whether it was overruled (`overruled`, `overrule_message_id`).

- **Columns:** `source`, `target`, `message_id`, `reply_id`, `outcome`, `reason`, `redirect_to`, `recommendation`, `at`, `resolution`, `overruled`, `overrule_message_id`
- **Rows on 2026-11-22:** 874
- **Written by:** `gateway/asks.go`, `gateway/store.go`, `gateway/store_inbox.go`, `gateway/store_outcome.go`
- **Read by:** `gateway/asks.go`, `gateway/bot_tools.go`, `gateway/learned.go`, `gateway/owner_answers.go`, `gateway/signal.go`, `gateway/store.go`, `gateway/store_outcome.go`, `gateway/store_record.go`
- **Shown in:** TUI, Desk, tool `agent_message_reply`, CTO op `outcome_inbox`, CTO op `outcome_overrule`
- **Kept:** Forever (messages are kept forever by default, so their outcomes are too).

### `node_scopes`

One row per permission the owner gave to a role in the org graph: a kind of action over a target, with a note, who set it and when. A session in that role may then act without asking the owner.

- **Columns:** `node`, `kind`, `over_ref`, `note`, `set_by`, `set_at`
- **Rows on 2026-11-22:** 12
- **Written by:** `gateway/node_scopes.go:writeScopeTx`
- **Read by:** `gateway/agent_scopes.go:applyAgentScopes`, `gateway/node_scopes.go:scopeRowTx`, `gateway/node_scopes.go:scopes`, `gateway/org_tree.go:sessionOrgLines`
- **Shown in:** TUI: Graph, tool: agent_graph_read, CTO: scope_set, CTO: scope_remove
- **Kept:** until the owner removes the scope (changes are logged in node_scopes_history)
- **Note:** Area changed from machines to org-graph: node here means a role node (Agent) of the org graph, not a machine. Table not read for the TUI directly; Graph tab shown via scopes in the graph view.

### `permission_proposals`

One permission change an agent proposed (rules, targets, reason) with its status, who decided and the result.

- **Columns:** `id`, `kind`, `rules`, `targets`, `reason`, `proposed_by`, `proposed_name`, `status`, `created_at`, `decided_at`, `decided_by`, `results`, `desk_log_id`, `dedupe_key`, `owner_words`
- **Rows on 2026-11-22:** 29
- **Written by:** `gateway/permissions.go:ApprovePermissionOnOwnerWords`, `gateway/permissions.go:MarkPermissionUndone`, `gateway/permissions.go:RecordPermissionResult`, `gateway/permissions.go:answerPermissionAsk`, `gateway/permissions.go:insertPermissionRow`, `gateway/permissions.go:rejectPermissionTx`
- **Read by:** `gateway/permissions.go:ApprovePermissionOnOwnerWords`, `gateway/permissions.go:MarkPermissionUndone`, `gateway/permissions.go:PermissionProposal`, `gateway/permissions.go:RaiseTrustAsk`, `gateway/permissions.go:RecordPermissionResult`, `gateway/permissions.go:answerPermissionAsk`
- **Shown in:** TUI: Desk, tool: agent_permission_propose, CTO: cto_proposals_find, CTO: proposal_ask
- **Kept:** for ever
- **Note:** No DELETE found.

### `projects`

One project folder registered on one machine, with its path, name, git remote and default branch.

- **Columns:** `node_id`, `path`, `name`, `remote`, `default_branch`, `added_by_session`, `added_by_harness`, `added_at`, `checked_at`, `graph_excluded`
- **Rows on 2026-11-22:** 21
- **Written by:** `gateway/store_projects.go`
- **Read by:** `gateway/delivery_changes.go`, `gateway/graph_lifecycle.go`, `gateway/graph_view.go`, `gateway/learned.go`, `gateway/project_display.go`, `gateway/settings.go`, `gateway/skill_lists.go`, `gateway/skillarchive_intake.go`, `and others`
- **Shown in:** TUI, Graph, tool `agent_projects_find`, tool `agent_project_add`, CTO op `project_add`
- **Kept:** Until the project is removed.

### `repos` (core)

One repo (project) node of the org tree, with its display name, its single lead agent (`lead_agent_id`), its team (`team_id`), and flags for archived and held back. `repos.lead_agent_id` is the single source for a repo's lead.

- **Columns:** `id`, `display_name`, `created_at`, `updated_at`, `archived`, `held_back`, `lead_agent_id`, `settings_token`, `bar_next`, `train_fails`, `train_last_run`, `merger_node`, `merger_session`, `team_id`
- **Rows on 2026-11-22:** 45
- **Written by:** `gateway/authority.go`, `gateway/graph_lifecycle.go`, `gateway/leads_max.go`, `gateway/project_display.go`, `gateway/repo_fold_migration.go`, `gateway/store.go`, `gateway/store_graph.go`
- **Read by:** `gateway/authority.go`, `gateway/charter_levels.go`, `gateway/graph_lifecycle.go`, `gateway/graph_refs.go`, `gateway/intake.go`, `gateway/lead_session.go`, `gateway/leads_max.go`, `gateway/node_scopes.go`, `gateway/org_read.go`, `and others`
- **Shown in:** TUI, Graph, TUI, Sessions, tool `agent_graph_read`, CTO op `repo_create`, CTO op `repo_archive`, CTO op `lead_set`, CTO op `project_display_set`
- **Kept:** Forever (an archived repo is only flagged).
- **Note:** Also holds, after the delivery rework, the merger of the repo (merger_node, merger_session), the next bar number and the merge train fail count; and, after the settings rework, its settings token.

### `setting_history`

One row per settings change: revision, key, level, scope, old and new value, who changed it, why and what it undoes. The newest revision is the current settings revision. A change that is not a setting (a delivery owner, a maturity) adds a marker row at seq 0.

- **Columns:** `revision`, `seq`, `key`, `level`, `scope`, `item`, `old_value`, `new_value`, `actor`, `reason`, `cause`, `undo_of`, `at`
- **Rows on 2026-11-22:** 33
- **Written by:** `gateway/delivery_pipeline_settings.go`, `gateway/delivery_settings.go`, `gateway/settings.go`, `gateway/worker_sandbox_settings.go`
- **Read by:** `gateway/settings.go`, `gateway/store_graph.go`
- **Shown in:** TUI, Settings, tool `agent_settings_read`, CTO op `settings_change`
- **Kept:** Forever; no code deletes it.

### `setting_values`

One row per stored setting value, keyed by setting name, level (network, repo, agent and so on), scope and item, with the revision that wrote it. The owner's own values (integrations, CTO title and settings, worker profiles, model scores, providers, floors, difficulty minimums, time zone and the public listener) are network settings under the key prefix `owner.`; they are not sent to the machines.

- **Columns:** `key`, `level`, `scope`, `item`, `value`, `revision`
- **Rows on 2026-11-22:** 27
- **Written by:** `gateway/delivery_pipeline_settings.go`, `gateway/delivery_settings.go`, `gateway/gateway_state.go`, `gateway/owner_values.go`, `gateway/settings.go`, `gateway/store_graph.go`, `gateway/worker_sandbox_settings.go`
- **Read by:** `gateway/cto_graph.go`, `gateway/delivery_pipeline_settings.go`, `gateway/delivery_settings.go`, `gateway/leads_max.go`, `gateway/loop.go`, `gateway/merger.go`, `gateway/session_max.go`, `gateway/settings.go`, `gateway/store.go`, `gateway/store_graph.go`, `and others`
- **Shown in:** TUI, Settings, tool `agent_settings_read`, CTO op `settings_change`
- **Kept:** Until the owner changes or unsets it.

### `teams` (core)

One team: a name, purpose, owner, authority mode, held-back text, where asks go, whether the lead merges, and its settings token.

- **Columns:** `id`, `name`, `purpose`, `owner_agent_id`, `active`, `created_at`, `updated_at`, `authority_mode`, `held_back`, `asks_to`, `lead_merges`, `settings_token`
- **Rows on 2026-11-22:** 4
- **Written by:** `gateway/leads_max.go`, `gateway/store_graph.go`, `gateway/teams.go`
- **Read by:** `gateway/asks_to.go`, `gateway/authority.go`, `gateway/charter_drafts.go`, `gateway/charter_levels.go`, `gateway/charter_resolver.go`, `gateway/cto_lead.go`, `gateway/desk_rules.go`, `gateway/lead_route.go`, `gateway/lead_session.go`, `and others`
- **Shown in:** TUI, Graph, CTO op `team_create`, CTO op `team_settings_set`, CTO op `team_history`, tool `agent_graph_read`
- **Kept:** Forever (an archived team is only marked inactive).

## Owner

### `ask_sorts`

One sort decision the chief of staff made on an ask (answer, route, merge, close, hide), with what it replaced so it can be undone.

- **Columns:** `id`, `ask_id`, `at`, `sorter`, `result`, `into_id`, `to_id`, `reason`, `priority`, `why_you`, `prev_next`, `prev_status`, `prev_answer`, `prev_answered_at`, `hidden_at`, `rule_id`, `owner_message`
- **Rows on 2026-11-22:** 1637
- **Written by:** `gateway/chief.go:SortAskWith`, `gateway/chief.go:commitSort`, `gateway/chief.go:reopenOrMoveMergedAsks`, `gateway/chief_hide.go:hideApply`, `gateway/chief_hide.go:hideUndo`
- **Read by:** `gateway/asks.go:closeWaitingAsk`, `gateway/chief.go:AskSortHistory`, `gateway/chief.go:ChiefSorts`, `gateway/chief.go:adoptChiefAsks`, `gateway/chief.go:askSortStats`, `gateway/chief.go:chiefMaySort`, `gateway/chief.go:reopenOrMoveMergedAsks`, `gateway/chief.go:replacementCheck`, `gateway/chief.go:undoAsk`, `gateway/chief_health.go:chiefSilent`, `gateway/chief_hide.go:hidePreview`, `gateway/chief_hide.go:hideUndo`, `gateway/chief_stuck.go:stuckItems`, `gateway/standing.go:askDisposition`
- **Shown in:** TUI: Desk, tool: agent_ask_sort, CTO: chief_sorts, CTO: ask_history, CTO: ask_undo
- **Kept:** for ever

### `desk_log`

One log line about something that happened on the Desk (an ask sorted, a permission applied, a batch hide undo receipt), with what undoes it.

- **Columns:** `id`, `at`, `source`, `about_kind`, `about_id`, `text`, `detail`, `options`
- **Rows on 2026-11-22:** 2184
- **Written by:** `gateway/asks.go`, `gateway/chief_hide.go`, `gateway/permissions.go`, `gateway/store.go`
- **Read by:** `gateway/asks.go`, `gateway/chief_hide.go`, `gateway/permissions.go`, `gateway/server.go`, `gateway/store.go`
- **Shown in:** TUI, Desk, tool `agent_ask_sort`, CTO op `desk_log`, CTO op `desk_settle`
- **Kept:** The newest 2000 lines, trimmed on every 200th insert (active hide undo receipts stay until undone).
- **Note:** Also holds the hide receipts of the Desk (about_kind hide_receipt), for undo.

### `follow_ups`

One row per follow-up check set by a message sender or the chief: the event expected (reply, change, conversation, session), who waits, the due time and whether it is pending, met or raised.

- **Columns:** `id`, `event`, `set_by`, `waiter`, `message_id`, `ask_id`, `conversation_id`, `recipient`, `change_id`, `watch_session`, `summary`, `created_at`, `due_at`, `status`
- **Rows on 2026-11-22:** 88
- **Written by:** `gateway/follow_ups.go:checkFollowUps`, `gateway/follow_ups.go:insert`
- **Read by:** `gateway/follow_ups.go:checkFollowUps`
- **Shown in:** tool: agent_message_send, tool: agent_ask_sort, TUI: Desk
- **Kept:** for ever; status moves from pending to met or raised, rows are not deleted
- **Note:** Area changed from messages to desk: when due and unmet, the minute tick raises one ask to the chief. Shown on the Desk only as that raised ask.

### `inbox_items` (core)

One ask or CTO problem: a question, approval (including delivery step approvals via `about_kind='step'`), or deduplicated CTO intake alert (`owner='cto'`, with `dedupe_key`, `count`, `first_seen`, `last_seen`, `severity`), with its single `status`, answer, priority and who sorted or routed it.

- **Columns:** `id`, `kind`, `session_id`, `node`, `text`, `recommendation`, `message_id`, `conversation_id`, `created_at`, `status`, `answer`, `answered_at`, `options`, `asker_name`, `change_title`, `from_kind`, `source_ref`, `about_kind`, `about_id`, `next_who`, `why_you`, `detail`, `priority`, `chief_sent_at`, `chief_at`, `route_fixed`, `owner`, `category`, `override_facts`, `dedupe_key`, `count`, `first_seen`, `last_seen`, `severity`
- **Rows on 2026-11-22:** 2770
- **Written by:** `gateway/agentnet_reply.go`, `gateway/apps_public_asks.go`, `gateway/asks.go`, `gateway/asks_to.go`, `gateway/brief.go`, `gateway/check.go`, `gateway/chief.go`, `gateway/chief_health.go`, `gateway/chief_stuck.go`, `gateway/delivery_approval.go`, `gateway/delivery_tasks.go`, `gateway/intake.go`, `and others`
- **Read by:** `gateway/agentnet_reply.go`, `gateway/apps_asks.go`, `gateway/apps_public_asks.go`, `gateway/ask_intake.go`, `gateway/asks.go`, `gateway/brief.go`, `gateway/caller_agent.go`, `gateway/check.go`, `gateway/chief.go`, `gateway/chief_health.go`, `gateway/intake.go`, `and others`
- **Shown in:** TUI, Desk, tool `agent_owner_ask`, tool `agent_ask_intake`, tool `agent_ask_sort`, CTO op `inbox_list`, CTO op `inbox_answer`, CTO op `cto_intake_find`
- **Kept:** Forever (answered and closed rows stay).

### `learned_suggestions`

One suggestion the system learned from message traffic: a change to a charter field or to a link, with its evidence and status (new, accepted, rejected). For an applied change, `learned_seen` says whether the owner has seen it.

- **Columns:** `id`, `node`, `field`, `edge_kind`, `kind`, `phrase`, `evidence`, `status`, `severity`, `created_at`, `ready_at`, `decided_at`, `origin`, `history_id`, `raw_phrase`, `learned_seen`
- **Rows on 2026-11-22:** 50
- **Written by:** `gateway/learned.go`, `gateway/learned_apply.go`, `gateway/learned_owner.go`, `gateway/side_fold.go`, `gateway/store_graph.go`
- **Read by:** `gateway/asks.go`, `gateway/chief.go`, `gateway/learned.go`, `gateway/learned_apply.go`, `gateway/learned_owner.go`, `gateway/store_inbox.go`
- **Shown in:** TUI, Desk, TUI, Graph, CTO op `learned_list`, CTO op `learned_answer`, CTO op `learned_view`
- **Kept:** Forever (status changes, rows stay).

### `standing_order_history`

The old text of a standing order or note, saved each time it is changed or deleted, by a database trigger.

- **Columns:** `order_id`, `text`, `tags`, `changed_at`, `kind`, `area`, `ref`
- **Rows on 2026-11-22:** 67
- **Written by:** `gateway/notes.go:(package level)`, `gateway/store.go:messagesThreadTriggersSQL`
- **Read by:** `gateway/standing.go:StandingOrderHistory`
- **Shown in:** CTO: cto_standing_orders
- **Kept:** for ever
- **Note:** Area corrected from desk to learning. History is read via standing.history (StandingOrderHistory) in the standing_orders op.

### `standing_orders`

One lasting text kept by the gateway: an owner standing order or rule, an owner judgment (answer or correction), or a named note written by an agent, with its tags and use count.

- **Columns:** `id`, `text`, `saved_at`, `source`, `tags`, `pinned`, `updated_at`, `kind`, `area`, `ref`, `action`, `category`, `scope`, `scope_ref`, `name`, `description`, `used_at`, `uses`, `voided_at`, `void_reason`
- **Rows on 2026-11-22:** 226
- **Written by:** `gateway/chief.go:AskSortHistory`, `gateway/notes.go:findNotes`, `gateway/notes.go:writeNote`, `gateway/notes_copy.go:UncopyNotes`, `gateway/notes_copy.go:copyAgentNotesQ`, `gateway/owner_answers.go:upsertAnswerJudgment`, `gateway/standing.go:RemoveStandingOrder`, `gateway/standing.go:SaveStandingOrder`, `gateway/standing.go:addJudgment`, `gateway/store.go:messagesThreadTriggersSQL`
- **Read by:** `gateway/brief.go:briefFacts`, `gateway/chief.go:AskSortHistory`, `gateway/desk_rules.go:ruleApplies`, `gateway/node_scopes.go:seedNodeScopesTx`, `gateway/notes.go:visibleNotes`, `gateway/notes.go:writeNote`, `gateway/notes_copy.go:copyAgentNotesQ`, `gateway/owner_answers.go:upsertAnswerJudgment`, `gateway/standing.go:FindJudgments`, `gateway/standing.go:MarksStored`, `gateway/standing.go:RemoveStandingOrder`, `gateway/standing.go:SearchStandingOrders`, `gateway/standing.go:StandingNotes`, `gateway/standing.go:StandingOrders`, `gateway/standing.go:UpdateStandingOrder`, `gateway/store.go:messagesThreadTriggersSQL`
- **Shown in:** TUI: Settings, tool: agent_note_write, tool: agent_notes_find, tool: agent_owner_judgment_record, tool: agent_owner_judgments_find, CTO: cto_standing_orders
- **Kept:** until the owner or the note writer removes it
- **Note:** Area corrected from desk to learning. Audit text updated: also holds named notes (scope agent, project, owner) and judgments. Notes copies are undone by UncopyNotes.

### `standing_orders_fts`

The searchable full text index (FTS5) of standing orders: the text and tags of each row, so notes and judgments can be searched by word.

- **Columns:** `text`, `tags`
- **Rows on 2026-11-22:** 226
- **Written by:** `standing_orders`, `gateway/notes.go`, `gateway/store.go`
- **Read by:** `gateway/standing.go`
- **Shown in:** tool `agent_owner_judgments_find`, CTO op `cto_standing_orders`
- **Kept:** Kept in step with standing_orders by triggers (insert, update, delete).

## Workers

### `findings`

One finding reported about a model or a harness. Enough findings promote a playbook note.

- **Columns:** `id`, `subject_kind`, `subject`, `finding`, `evidence`, `reporter`, `node_id`, `at`, `status`
- **Rows on 2026-11-22:** 30
- **Written by:** `gateway/learn.go:AddFinding`, `gateway/learn.go:evaluatePlaybook`, `gateway/store_record.go:Prune`
- **Read by:** `gateway/learn.go:AddFinding`, `gateway/learn.go:Findings`, `gateway/learn.go:evaluatePlaybook`
- **Shown in:** tool: agent_learn, CTO: agent_learn
- **Kept:** for ever; a finding never promoted is marked stale after 90 days
- **Note:** Prune sets status stale after FindingStaleDays=90, it does not delete.

### `friction`

One friction report: a line from a worker or agent saying what slowed it down, with project, area, model and change. It also holds the read status of the report (new, read, triaged, fixed, wontfix, infra) with who set it, when, and the link and note of that mark. Only the latest mark is kept.

- **Columns:** `id`, `project`, `session`, `change_id`, `harness`, `model`, `area`, `text`, `created_at`, `digest_id`, `status`, `status_by`, `status_at`, `status_link`, `status_note`
- **Rows on 2026-11-22:** 797
- **Written by:** `gateway/friction.go`
- **Read by:** `gateway/friction.go`
- **Shown in:** TUI, Quotas, tool `agent_friction_mark`, CTO op `friction_find`, CTO op `friction_mark`
- **Kept:** Forever.

### `model_failures`

One row per machine, harness and model holding when it last failed a worker call, when it last had an auth failure and when it last succeeded.

- **Columns:** `node_id`, `harness`, `model`, `at`, `auth_at`, `success_at`
- **Rows on 2026-11-22:** 50
- **Written by:** `gateway/side_fold.go:foldModelSuccessesTx`, `gateway/store_record.go:RecordModelAuthFailure`, `gateway/store_record.go:RecordModelFailure`, `gateway/store_record.go:RecordModelSuccess`
- **Read by:** `gateway/store_record.go:FailedModels`, `gateway/store_record.go:SucceededModels`, `gateway/store_record.go:modelAuthIndex`
- **Shown in:** tool: agent_worker_start
- **Kept:** one row per machine and model, overwritten; only failures of the last 24 hours and successes of the last 7 days are read
- **Note:** Rows never deleted. Windows are protocol.ModelFailureHours and ModelSuccessDays.

### `model_prices`

The last good copy of the public model price list (prompt, completion and cache prices), used to cost worker runs.

- **Columns:** `id`, `prompt`, `completion`, `cache_read`, `cache_write`, `updated_at`
- **Rows on 2026-11-22:** 468
- **Written by:** `gateway/prices.go:ReplacePrices`
- **Read by:** `gateway/prices.go:storedPrices`
- **Shown in:** TUI: Quotas (Workers log, cost column)
- **Kept:** replaced whole on each price refresh

### `pick_candidates`

One row per candidate (worker, model and effort) of every automatic worker pick, with the prices, scores and weights the picker read and why it was chosen or left out.

- **Columns:** `run_id`, `pos`, `name`, `harness`, `provider`, `model`, `effort`, `rule`, `bucket_lo`, `bucket_hi`, `fallback`, `cell_score`, `cell_low`, `cell_high`, `cell_n`, `is_allowed`, `is_qualified`, `is_prior`, `is_best`, `is_chosen`, `is_winner`, `left_out_reason`, `cost_known`, `cost_recorded`, `price_unknown`, `cost_per_run`, `p_clean`, `rescue_cost`, `expected_cost`, `pace_room`, `pace_factor`, `adjusted_cost`, `weight`, `probability`, `skipped_reason`, `explore`, `explore_credit_before`, `explore_credit_after`, `margin`
- **Rows on 2026-11-22:** 6474
- **Written by:** `gateway/worker_assessment.go`
- **Read by:** `tools/pickerreplay`, `research/picker-ml`, `gateway/pick_quota.go` (names and reasons for `quota_read`)
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself
- **Kept:** Forever.

### `pick_quota`

One row per recorded automatic pick (same `run_id` as its `pick_candidates` rows): the quota windows it read, once per provider, which window decided each candidate and which gate step held it back; and, when the chosen run ends, its tokens by kind and its provider's windows at the end (decision 0191). Added after the 2026-11-22 snapshot.

- **Columns:** `run_id`, `read`, `run_end`
- **Written by:** `gateway/worker_assessment.go` (with the pick rows), `gateway/pick_quota.go` (run end)
- **Read by:** `gateway/pick_quota.go`
- **Shown in:** tool `agent_workers_find` (detail full, `quota_read`), CTO `agent_workers_find`
- **Kept:** Forever, as the pick rows.

### `playbook`

One note about a model or a harness (written by the owner or promoted from findings), shown to workers that start with that model.

- **Columns:** `subject_kind`, `subject`, `harness`, `note`, `source`, `support`, `track_line`, `pinned`, `stale`, `created_at`, `updated_at`
- **Rows on 2026-11-22:** 15
- **Written by:** `gateway/learn.go:evaluatePlaybook`, `gateway/track.go:PlaybookNotes`, `gateway/track.go:SetPlaybookPin`
- **Read by:** `gateway/learn.go:evaluatePlaybook`, `gateway/track.go:PlaybookNotes`
- **Shown in:** tool: agent_machine_get, tool: agent_worker_start, CTO: playbook_get, CTO: playbook_set, TUI: Graph
- **Kept:** until the owner clears the note; automatic notes are flagged stale after 60 days
- **Note:** Graph entry is the 'learned new' count in the header (view_learned.go). agent_machine_get has a playbook view (toolread.go). Stale flag from PlaybookStaleDays=60; deletion only by owner.

### `quota_blocks`

One row per quota window of a provider. A row with a machine says that a worker run reported the window used up there, and when it resets. A row with no machine says how many tokens the window holds, learned from the last time it ran out.

- **Columns:** `node_id`, `provider`, `window_id`, `window_label`, `resets_at`, `detail`, `at`, `window_seconds`, `budget_tokens`
- **Rows on 2026-11-22:** 7
- **Written by:** `gateway/quota_burn.go`, `gateway/store_record.go`
- **Read by:** `gateway/quota_burn.go`, `gateway/store_record.go`
- **Shown in:** TUI, Quotas (Subscriptions)
- **Kept:** Rows are not deleted; one row per window, overwritten. A machine row is ignored after its reset time. A learned budget is replaced by the newest failure.

### `worker_assessments`

One row per worker start holding the difficulty judge's verdict on the task: scores, difficulty level, which judge model ran, and the picker's note.

- **Columns:** `run_id`, `session_id`, `started_at`, `assessed_at`, `reasoning`, `scope`, `coupling`, `uncertainty`, `verification`, `risk`, `d`, `label`, `bottleneck`, `confidence`, `rules_version`, `judge_harness`, `judge_model`, `reasons`, `picker_label`, `error`, `picker_note`
- **Rows on 2026-11-22:** 2218
- **Written by:** `gateway/worker_assessment.go:recordWorkerAssessment`
- **Read by:** `gateway/model_health.go:ModelHealth`, `gateway/model_health.go:load`, `gateway/picker.go:pickerTaskRecordsUsing`, `gateway/store.go:AnnounceNotify`, `gateway/worker_assessment.go:WorkerAssessment`, `gateway/worker_log.go:workerRunLookups`
- **Shown in:** TUI: Quotas (Workers log, Difficulty details), tool: agent_workers_find
- **Kept:** for ever

### `worker_defects`

One row per defect found in a worker's result, from a rating or filed automatically (failed verify, failed test, fix of a shipped change), with severity, cause and the change it came from. Its blame says whether the worker or the brief the caller wrote caused it. A brief defect is charged to the caller and never reaches the picker. The run it names holds the model, harness and difficulty.

- **Columns:** `id`, `run_id`, `session_id`, `caller`, `rater_model`, `note`, `at`, `severity`, `brief_defect_id`, `cause`, `escape_phase`, `auto_change_id`, `auto_kind`, `auto_revision`, `blame`
- **Rows on 2026-11-22:** 279
- **Written by:** `gateway/defect_auto.go`, `gateway/ratings.go`, `gateway/side_fold.go`
- **Read by:** `gateway/defect_auto.go`, `gateway/picker.go`, `gateway/quality_families.go`, `gateway/ratings.go`, `gateway/scores_weekly.go`, `gateway/worker_log.go`
- **Shown in:** tool `agent_worker_rate`, TUI, Quotas (Workers log, Quality · worker picker)
- **Kept:** Forever.

### `worker_ratings`

One row per rating a caller gave to a worker's result, with a score and note. The run it names holds the model, harness and difficulty.

- **Columns:** `id`, `run_id`, `session_id`, `caller`, `rater_model`, `score`, `note`, `at`
- **Rows on 2026-11-22:** 828
- **Written by:** `gateway/ratings.go`
- **Read by:** `gateway/quality.go`, `gateway/quality_families.go`, `gateway/ratings.go`
- **Shown in:** tool `agent_worker_rate`, TUI, Quotas (Quality · worker picker)
- **Kept:** Forever.

### `worker_runs` (core)

One row per worker run: which machine, harness and model, how it ended, how long it ran, how much rework it needed and how hard it struggled. It also holds the tokens the run's session used by kind and their cost at API prices when the run was recorded, a visual proof report when the worker said it could not give one, and whether the caller was offered a rating (that offer is per session and sits on its first run).

- **Columns:** `id`, `node_id`, `session_id`, `harness`, `model`, `outcome`, `started_at`, `ended_at`, `run_seconds`, `continue_count`, `fix_count`, `caller`, `difficulty`, `counters_known`, `touches`, `difficulty_used`, `auto_continue_count`, `task_id`, `midrun_count`, `caller_session_id`, `caller_agent_id`, `files_changed`, `lines_changed`, `worker_complete`, `early_quit`, `struggle_failed`, `struggle_repeats`, `struggle_retries`, `struggle_exploration`, `struggle_env`, `struggle_top`, `struggle_trace`, `struggle_capped`, `cause`, `usage_in`, `usage_out`, `usage_reasoning`, `usage_cache_read`, `usage_cache_write`, `cost_usd`, `run_generation`, `proof_unavailable`, `offered_at`, `rate_asked`
- **Rows on 2026-11-22:** 2637
- **Written by:** `gateway/ratings.go`, `gateway/rework_backfill.go`, `gateway/session_replace.go`, `gateway/store_record.go`, `gateway/task_rework_backfill.go`, `gateway/track.go`, `gateway/worker_cause.go`
- **Read by:** `gateway/context_resolver.go`, `gateway/delivery_tasks.go`, `gateway/intake_feeds.go`, `gateway/loop.go`, `gateway/member_loop.go`, `gateway/model_health.go`, `gateway/quota_burn.go`, `gateway/ratings.go`, `gateway/rework_backfill.go`, `gateway/runs_snapshot.go`, `and others`
- **Shown in:** TUI, Quotas (Workers log), tool `agent_workers_find`
- **Kept:** Forever by default; the gateway flag keep-worker-runs can prune runs older than a set time.

### `worker_starts`

One row per request to start a worker, with the machine, project, harness, session, whether it is open or closed, and the brief's done list. A start that the brief check refused is a closed row in state `refused`, with the task key and the failures, so the third attempt of a task can start.

- **Columns:** `ref`, `node_id`, `owner`, `project`, `harness`, `permissions`, `session_id`, `state`, `created_at`, `closed_at`, `legacy_ref`, `launch_state`, `caller_agent_id`, `done`, `brief_hash`, `not_attempted_at`, `task_key`, `failures`, `owner_choice` (what the owner named at the start, decision 0191)
- **Rows on 2026-11-22:** 1643
- **Written by:** `gateway/brief_check.go`, `gateway/session_replace.go`, `gateway/store.go`, `gateway/worker_starts.go`
- **Read by:** `gateway/brief_check.go`, `gateway/context_resolver.go`, `gateway/graph_lifecycle.go`, `gateway/track.go`, `gateway/worker_starts.go`
- **Shown in:** tool `agent_worker_start`, tool `agent_workers_find`
- **Kept:** Closed starts are deleted after 30 days, when the next start is recorded. A start that holds the owner's choice is kept while its worker session exists.
- **Note:** Also holds refused briefs (state refused), counted by the third-attempt rule.

## Delivery

### `delivery_changes` (core)

One row per change (a branch of work) that a worker or session has submitted for delivery, with its project, branch, title, current state and merged commit.

- **Columns:** `seq`, `id`, `project`, `machine`, `branch`, `target_branch`, `base_commit`, `head_commit`, `uncommitted`, `revision`, `origin`, `caller`, `title`, `difficulty`, `state`, `merged_commit`, `created_at`, `updated_at`, `risk`, `sensitive_path`, `done`
- **Rows on 2026-11-22:** 1203
- **Written by:** `gateway/delivery_changes.go:writeChange`, `gateway/session_replace.go:ReplaceSession`
- **Read by:** `gateway/asks.go:DeskLog`, `gateway/asks.go:reconcileChangeAsks`, `gateway/brief_check.go:openChangeBriefs`, `gateway/caller_agent.go:sessionActsForCaller`, `gateway/caller_agent.go:unownedCallerTasks`, `gateway/caller_agent.go:wakeCallerAgent`, `gateway/delivery_approval.go:answerDeliveryApproval`, `gateway/delivery_changes.go:ListChanges`, `gateway/delivery_changes.go:changeView`, `gateway/delivery_changes.go:queryChange`, `gateway/delivery_reconcile.go:repairExternalDone`, `gateway/delivery_tasks.go:backfillDeliveryTasks`, `gateway/delivery_tasks.go:checkStuckStepTasks`, `gateway/delivery_tasks.go:dispatchStepTasks`, `gateway/delivery_tasks.go:holds`, `gateway/delivery_tasks.go:retireObsoleteStepTasks`, `gateway/delivery_tasks.go:retireStepMessages`, `gateway/delivery_tasks.go:retireUnlinkedStepMessages`, `gateway/delivery_tasks.go:stepMessageCurrent`, `gateway/delivery_tasks.go:stepTaskDeliverable`, `gateway/delivery_tasks.go:storeStepTaskMessagesInternal`, `gateway/delivery_tasks.go:wakeOfflineStepOwners`, `gateway/desk_rules.go:askRepoTeam`, `gateway/follow_ups.go:eventHappened`, `gateway/follow_ups.go:recordSortFollowUp`, `gateway/graph_lifecycle.go:lifecycleReferences`, `gateway/member_loop.go:memberWorkerLines`, `gateway/merger.go:mergerLifecycleMigration`, `gateway/merger.go:resetConfiguredMergerTasks`, `gateway/merger.go:resetStoppedMergerTasks`, `gateway/nudge_batch.go:dueNudgeSessions`, `gateway/owed_replies.go:initOwedRepliesLiveSince`, `gateway/picker.go:pickerTaskRecordsUsing`, `gateway/session_replace.go:ReplaceSession`, `gateway/session_replace.go:moveStepNotices`, `gateway/session_stall.go:announcedDone`, `gateway/store.go:messagesThreadTriggersSQL`, `gateway/store_graph.go:SetDeliveryOwner`, `gateway/store_inbox.go:askArea`, `gateway/store_inbox.go:inboxRead`, `gateway/worker_hints.go:RecentWorkerChanges`
- **Shown in:** TUI: Desk (Delivery and Change panels), tool: agent_changes_find, tool: agent_change_submit
- **Kept:** for ever

### `delivery_evidence`

One row per report on a delivery step (accept, review, test, merge, release or verify) of a change: the result, who reported it, which model, and a note.

- **Columns:** `seq`, `change_id`, `revision`, `step`, `for_step`, `after_merge`, `result`, `commit_hash`, `fields`, `by_session`, `reported_by`, `by_model`, `by_model_family`, `note`, `at`
- **Rows on 2026-11-22:** 6539
- **Written by:** `gateway/delivery_changes.go:insertEvidence`
- **Read by:** `gateway/delivery_changes.go:changeEvidence`, `gateway/delivery_changes.go:reopenOwnReport`, `gateway/delivery_tasks.go:checkNodeTask`, `gateway/delivery_waiting.go:(package level)`, `gateway/follow_ups.go:eventHappened`, `gateway/member_loop.go:memberWorkerLines`, `gateway/nudge_fires.go:markNudgeFollows`, `gateway/picker.go:pickerTaskRecordsUsing`, `gateway/scores_weekly.go:ScoresWeeklyAt`
- **Shown in:** TUI: Desk (Evidence panel), tool: agent_step_report, tool: agent_changes_find
- **Kept:** for ever, the database refuses to update or delete these rows
- **Note:** Append-only: triggers abort UPDATE and DELETE.

### `delivery_revisions`

One row per revision of a change (each time the branch is pushed again), with its commit and, since the fold, the size of the diff, the risk signals and the hand-back check result.

- **Columns:** `change_id`, `number`, `branch`, `commit_hash`, `uncommitted`, `at`, `diff_stat`, `signals`, `handback_check`
- **Rows on 2026-11-22:** 1829
- **Written by:** `gateway/delivery_changes.go:insertRevision`, `gateway/delivery_changes.go:setRevisionColumn`, `gateway/side_tables_fold.go:foldRevisionStatsTx`
- **Read by:** `gateway/brief_check.go:openChangeBriefs`, `gateway/delivery_changes.go:ListChanges`, `gateway/delivery_changes.go:changeHandbackCheck`, `gateway/delivery_changes.go:changeSignals`, `gateway/delivery_changes.go:changeView`, `gateway/delivery_tasks.go:storeStepTaskMessagesInternal`, `gateway/side_tables_fold.go:foldRevisionStatsTx`
- **Shown in:** TUI: Desk (Change panel), tool: agent_changes_find, tool: agent_change_submit
- **Kept:** for ever
- **Note:** Audit text was only 'each revision'; diff_stat, signals and handback_check come from the folded delivery_revision_stats.

### `delivery_step_tasks` (core)

One row per open delivery step of a change that needs someone to do it: who owns it, who claimed it, and when it was last nudged or is waiting. It also holds the notices sent for the step (`notices`, a map from a session to the notice message) and the agents that may approve it when it is delegated (`delegates`).

- **Columns:** `change_id`, `revision`, `step`, `for_step`, `after_merge`, `owner_node`, `claimed_by`, `created_at`, `urgent`, `last_notice_at`, `stuck_notified`, `waiting_reason`, `waiting_at`, `waiting_until`, `last_notice_due_at`, `notices`, `delegates`
- **Rows on 2026-11-22:** 115
- **Written by:** `gateway/delivery_approval.go`, `gateway/delivery_changes.go`, `gateway/delivery_tasks.go`, `gateway/delivery_waiting.go`, `gateway/merger.go`, `gateway/session_replace.go`, `gateway/store_graph.go`
- **Read by:** `gateway/caller_agent.go`, `gateway/delivery_approval.go`, `gateway/delivery_changes.go`, `gateway/delivery_tasks.go`, `gateway/delivery_waiting.go`, `gateway/graph_lifecycle.go`, `gateway/merger.go`, `gateway/nudge_batch.go`, `gateway/owed_replies.go`, `gateway/session_stall.go`, `and others`
- **Shown in:** tool `agent_step_claim`, tool `agent_step_report`, TUI, Desk (Steps and evidence panel)
- **Kept:** Until the step is reported, closed, retired or its change ends. A done step keeps no task: its row in `delivery_evidence` is the record.

### `project_bar_rules`

One row per numbered rule in a project's bar: the short quality rules that workers and reviewers check diffs against.

- **Columns:** `project`, `n`, `text`, `source`, `created_at`
- **Rows on 2026-11-22:** 120
- **Written by:** `gateway/store_bar.go:EditBar`
- **Read by:** `gateway/store_bar.go:allBarRules`, `gateway/store_bar.go:barRules`
- **Shown in:** TUI: Graph (Project rules card), tool: agent_bar_edit, CTO: bar_edit
- **Kept:** until the rule is removed with a bar edit

## Shared resources

### `app_tokens`

One row per hashed access or refresh token of a connected app, with its type, expiry, token family and the time it was used.

- **Columns:** `hash`, `app_id`, `type`, `expires_at`, `created_at`, `family_id`, `used_at`, `purpose`
- **Rows on 2026-11-22:** 68
- **Written by:** `gateway/apps.go:DeleteExpiredTokens`, `gateway/apps.go:RevokeApp`, `gateway/apps.go:RotateRefresh`, `gateway/apps.go:issueFamilyToken`, `gateway/oauth_store.go:exchangeOAuthCode`, `gateway/phone_auth.go:phoneAppForToken`
- **Read by:** `gateway/apps.go:DeleteExpiredTokens`, `gateway/apps.go:RotateRefresh`, `gateway/apps.go:appForToken`, `gateway/oauth_store.go:refreshOAuth`, `gateway/phone_auth.go:phoneAppForToken`, `gateway/phone_auth.go:phoneAuth`
- **Shown in:** not shown to the owner
- **Kept:** access tokens 1 hour, refresh tokens 90 days, used refresh hashes 90 days; swept every 10 minutes; at most 20 access tokens per app
- **Note:** Also the source of the refresh-rotation count (rotations of a family in the last 24 hours).

### `apps`

One row per connected app (phone, bot or other client), and, after the fold, per registered sign-in client waiting for approval: name, kind, OAuth client id and name, redirect addresses, grant and response types, token auth method, source, state, when created, last used and revoked, and what it is granted.

- **Columns:** `id`, `name`, `kind`, `client_id`, `client_name`, `redirect_uris`, `created_at`, `last_used_at`, `revoked_at`, `grants`, `state`, `grant_types`, `response_types`, `token_endpoint_auth_method`, `source`
- **Rows on 2026-11-22:** 2
- **Written by:** `gateway/apps.go:CreateApp`, `gateway/apps.go:RenameApp`, `gateway/apps.go:RevokeApp`, `gateway/apps.go:SetAppGrant`, `gateway/apps.go:appForToken`, `gateway/oauth_store.go:ApprovePending`, `gateway/phone_tailnet.go:pairPhoneTailnet`
- **Read by:** `gateway/apps.go:ListApps`, `gateway/apps.go:RotateRefresh`, `gateway/apps.go:SetAppGrant`, `gateway/apps.go:appForToken`, `gateway/apps.go:issueFamilyToken`, `gateway/bot_agent.go:(package level)`, `gateway/bot_agent.go:addBotGraph`, `gateway/bot_mcp_grant.go:AppGrants`, `gateway/bot_names.go:botNames`, `gateway/bot_names.go:touchBotNamed`, `gateway/bot_session.go:touchBot`, `gateway/oauth_store.go:CleanupOAuth`, `gateway/oauth_store.go:newOAuthPending`, `gateway/oauth_store.go:oauthStatus`, `gateway/oauth_store.go:refreshOAuth`, `gateway/oauth_store.go:registerOAuthClient`, `gateway/phone_auth.go:phoneAppForToken`, `gateway/phone_auth.go:phoneAuth`, `gateway/phone_pairing.go:redeemPhonePending`, `gateway/phone_tailnet.go:pairPhoneTailnet`, `gateway/store.go:AnnounceNotify`, `gateway/usage.go:SessionCounts`
- **Shown in:** TUI: Settings, CTO: apps_list, CTO: app_grant, CTO: app_ungrant, CTO: app_rename, CTO: app_revoke
- **Kept:** for ever; a revoked app is only marked revoked
- **Note:** Planned by the Apps and sign-in rework, which is still being written. Rows in state registered are not connected apps and are never shown as such.

### `mcp_servers`

One row per shared MCP server: its name, the machine that runs it, its spec, whether it is on, who added it, and its last health and tool list.

- **Columns:** `name`, `node_id`, `spec`, `enabled`, `added_by_node`, `added_by_session`, `added_by_harness`, `added_at`, `health`, `health_detail`, `health_at`, `tools`
- **Rows on 2026-11-22:** 2
- **Written by:** `gateway/mcp_store.go`
- **Read by:** `gateway/mcp_store.go`
- **Shown in:** tool `agent_mcp_servers_find`, tool `agent_mcp_test`, CTO op `mcp_change`
- **Kept:** Until the server is removed.

### `oauth_attempts`

One row per recent failed pairing-code guess or client registration, used to rate-limit the public sign-in endpoints.

- **Columns:** `kind`, `source`, `at`
- **Rows on 2026-11-22:** 0
- **Written by:** `gateway/oauth_store.go:CleanupOAuth`, `gateway/oauth_store.go:PendingByUserCode`, `gateway/oauth_store.go:registerOAuthClient`, `gateway/plan_kinds_migration.go:(package level)`
- **Read by:** `gateway/oauth_store.go:PendingByUserCode`, `gateway/oauth_store.go:registerOAuthClient`, `gateway/server.go:handle`
- **Shown in:** not shown to the owner
- **Kept:** registrations 1 hour, wrong codes 10 minutes
- **Note:** Audit text was empty. Code-miss rows are deleted in PendingByUserCode (10 minute cutoff); CleanupOAuth deletes registration rows older than 1 hour every 10 minutes.

### `oauth_codes`

One row per hashed one-time authorization code issued after the owner approves a sign-in, with its expiry and use time.

- **Columns:** `hash`, `pending_id`, `app_id`, `client_id`, `redirect_uri`, `code_challenge`, `expires_at`, `used_at`, `retry_code`
- **Rows on 2026-11-22:** 1
- **Written by:** `gateway/oauth_store.go:CleanupOAuth`, `gateway/oauth_store.go:exchangeOAuthCode`, `gateway/oauth_store.go:oauthStatus`
- **Read by:** `gateway/oauth_store.go:exchangeOAuthCode`, `gateway/oauth_store.go:oauthStatus`
- **Shown in:** not shown to the owner
- **Kept:** unused codes 2 minutes; used codes 90 days; swept every 10 minutes

### `oauth_pending`

One row per sign-in waiting for the owner to approve (device code or browser): user code, client, requested resource, status and expiry.

- **Columns:** `id`, `user_code`, `client_id`, `redirect_uri`, `code_challenge`, `state`, `resource`, `created_at`, `expires_at`, `status`, `app_id`, `approved_at`, `browser_hash`, `browser_expires_at`, `source`
- **Rows on 2026-11-22:** 0
- **Written by:** `gateway/oauth_store.go:ApprovePending`, `gateway/oauth_store.go:CleanupOAuth`, `gateway/oauth_store.go:RefusePending`, `gateway/oauth_store.go:newOAuthPending`, `gateway/oauth_store.go:oauthPendingQuota`, `gateway/oauth_store.go:registerOAuthClient`, `gateway/phone_pairing.go:newPhonePending`, `gateway/phone_pairing.go:redeemPhonePending`
- **Read by:** `gateway/apps_asks.go:OpenAppConsent`, `gateway/apps_asks.go:reconcileAppConsentAsks`, `gateway/oauth_store.go:ApprovePending`, `gateway/oauth_store.go:CleanupOAuth`, `gateway/oauth_store.go:PendingByUserCode`, `gateway/oauth_store.go:newOAuthPending`, `gateway/oauth_store.go:oauthPendingQuota`, `gateway/oauth_store.go:oauthStatus`, `gateway/phone_pairing.go:newPhonePending`, `gateway/phone_pairing.go:redeemPhonePending`
- **Shown in:** TUI: Desk, TUI: Settings
- **Kept:** 10 minutes, swept every 10 minutes and when a new request starts
- **Note:** Shown as an app consent ask (OpenAppConsent in gateway/apps_asks.go) and in the Settings Apps screen.

### `skill_archive`

One row per stored version of every skill, with its full text, hash, source, who added it, whether it was removed and `in_use`: one version per skill has `in_use` 1, the version a node that follows the latest gets.

- **Columns:** `name`, `version`, `hash`, `content`, `description`, `tokens`, `source_kind`, `source_node`, `source_path`, `source_url`, `source_commit`, `source_session`, `added_by`, `added_at`, `removed_at`, `source_project`, `context_tokens`, `in_use`
- **Rows on 2026-11-22:** 115
- **Written by:** `gateway/skill_archive_migration.go`, `gateway/skillarchive.go`
- **Read by:** `gateway/skill_archive_migration.go`, `gateway/skill_lists.go`, `gateway/skillarchive.go`, `gateway/skillarchive_intake.go`, `gateway/skillarchive_read.go`
- **Shown in:** CTO op `skill_archive`
- **Kept:** Forever; a removal only marks it, and undoing an add deletes the row.

### `skill_archive_blocks`

One row per skill name (and optionally hash) that the owner blocked, so it is not taken into the archive again.

- **Columns:** `name`, `hash`, `actor`, `at`
- **Rows on 2026-11-22:** 26
- **Written by:** `gateway/skill_archive_migration.go:(package level)`, `gateway/skillarchive.go:skillArchivePut`, `gateway/skillarchive.go:skillArchiveRemove`, `gateway/skillarchive.go:skillArchiveUndo`
- **Read by:** `gateway/skillarchive.go:archiveBlock`
- **Shown in:** CTO: skill_archive
- **Kept:** until the same skill is added again or a removal is undone

### `skill_changes`

One row per change to the skill archive (add, remove, promote, undo of those) or to the skill lists (what was added, removed or pinned at a level), told apart by `kind` (`archive` or `list`). It is kept so the change can be undone.

- **Columns:** `id`, `kind`, `action`, `name`, `version`, `versions`, `restored`, `previous`, `level`, `scope`, `before`, `after`, `actor`, `at`, `undone_at`
- **Rows on 2026-11-22:** 137
- **Written by:** `gateway/skillarchive.go`, `gateway/skill_lists.go`
- **Read by:** `gateway/skill_archive_migration.go`, `gateway/skillarchive.go`, `gateway/skill_lists.go`
- **Shown in:** CTO op `skill_archive`, CTO op `skills`, Desk undo
- **Kept:** Forever.

### `skill_node_lists`

One row per skill that is added, removed or pinned at a level and scope (global, project or agent).

- **Columns:** `level`, `scope`, `name`, `action`, `pin`, `added_by`, `added_at`
- **Rows on 2026-11-22:** 126
- **Written by:** `gateway/skill_lists.go:SkillListSet`, `gateway/skill_lists.go:SkillListUndo`
- **Read by:** `gateway/skill_lists.go:skillListRows`, `gateway/skill_lists.go:skillNodeState`
- **Shown in:** CTO: skills
- **Kept:** the rows of a level and scope are replaced as a whole on each set or undo
