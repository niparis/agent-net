# MCP tools and the tables they touch

Every tool agent-net served to agents, the CTO and connected bots, with the gateway tables each one reads and writes.

> A snapshot traced on 2026-11-22 against v0.5.126, by reading the code. It was traced before the MCP tool cut
> (parts 1 to 3), so some tools listed here were since merged or removed, and it is not checked by CI.

Most tools first resolve the caller, its repo and the effective settings and org graph (SettingsSnapshot, Graph). These reads are listed once here instead of on every tool; uses_common_reads marks the tools that do them.

**Common reads** (tables almost every tool reads): `agent_loops`, `charter_values`, `graph_edges`, `project_bar_rules`, `projects`, `repos`, `sessions`, `setting_values`, `agents`, `teams`, `worker_assessments`, `worker_runs`

## Agent tools

### `agent_announce`

REQUIRED: set your current task (one short line) and expertise; fill expertise once. Read AGENTS.md. Call it at the start of every session and whenever your task changes.

- **Reads:** `agents`, `messages`, `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_retries`, `delivery_revisions`, `delivery_step_tasks`, `model_failures`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `repos`, `worker_starts` (plus the common reads)
- **Writes:** `agent_loops`, `ask_sorts`, `gateway_state`, `delivery_step_tasks`, `desk_log`, `events`, `inbox_items`, `member_blocked_notices`, `messages`, `nudge_fires`, `setting_values`, `permission_proposals`, `sessions`
- **Gateway operations:** `profile.announce`, `session.upsert`

### `agent_ask_intake`

Chief of staff only: list sessions whose last turn may wait for the owner, read that turn with the preceding input (labelled owner, peer, agent_net or unknown) and secrets masked, or import it as one 

- **Reads:** `agents`, `ask_sorts`, `model_failures`, `node_scopes`, `nodes`, `sessions` (plus the common reads)
- **Writes:** `inbox_items`, `messages`, `setting_values`
- **Gateway operations:** `ask.intake`

### `agent_ask_sort`

Chief of staff: sort waiting asks (close, merge, send, owner), withdraw an ask the asker took back or a newer ask replaces, undo its own newest sort, list asks and sort counts, or read an ask with det

- **Reads:** `agents`, `charter_drafts`, `messages`, `deliveries`, `delivery_changes`, `delivery_retries`, `inbox_items`, `delivery_step_tasks`, `learned_suggestions`, `model_failures`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `standing_orders`, `worker_starts` (plus the common reads)
- **Writes:** `desk_log`, `ask_sorts`, `delivery_step_tasks`, `events`, `follow_ups`, `inbox_items`, `messages`, `nudge_fires`, `setting_values`, `permission_proposals`, `sessions`
- **Gateway operations:** `ask.sort`

### `agent_bar_edit`

Add, edit or remove the project rules that workers and reviewers check diffs against. Lead of the project, owner or CTO only.

- **Reads:** `agents`, `sessions` (plus the common reads)
- **Writes:** `repos`, `project_bar_rules`
- **Gateway operations:** `bar.edit`

### `agent_change_submit`

Submit work done outside a worker as a change, so agent-net moves it through the project's delivery steps. A worker's complete hand-back submits its change by itself. A new head commit on the same cha

- **Reads:** `agents`, `delivery_evidence`, `delivery_step_tasks`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `sessions`, `repos`, `gateway_state`, `worker_ratings`, `worker_starts`, `worker_runs` (plus the common reads)
- **Writes:** `ask_sorts`, `charter_drafts`, `delivery_changes`, `delivery_revisions`, `delivery_step_tasks`, `inbox_items`, `desk_log`, `messages`, `setting_values`, `sessions`, `worker_defects`
- **Gateway operations:** `change.get`, `change.submit`, `policy.get`, `query.projects`

### `agent_changes_find`

Find delivery changes: their revision, the step each is in, who owns it and the evidence so far. Give change to read one change in full with its revisions and evidence.

- **Reads:** `delivery_changes`, `delivery_evidence`, `delivery_revisions`, `delivery_step_tasks`, `inbox_items`, `sessions`, `repos` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `change.get`, `change.list`

### `agent_charter_draft`

Propose one charter value drafted from AGENTS.md, only when agent-net asked you to. The owner accepts or rejects it; nothing changes before that.

- **Reads:** `agents`, `deliveries`, `delivery_changes`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `worker_starts` (plus the common reads)
- **Writes:** `ask_sorts`, `charter_drafts`, `messages`, `delivery_retries`, `desk_log`, `events`, `follow_ups`, `graph_outcomes`, `inbox_items`, `learned_suggestions`, `nudge_fires`, `setting_values`, `permission_proposals`, `sessions`
- **Gateway operations:** `charter.draft`

### `agent_file_send`

Send files from this session to one peer with a note. Paths must be absolute and within the session project, worktree or /tmp. Workers send files with agent_handback.

- **Reads:** `agents`, `deliveries`, `delivery_changes`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `quota_blocks`, `worker_defects`, `worker_ratings` (plus the common reads)
- **Writes:** `ask_sorts`, `messages`, `inbox_items`, `delivery_retries`, `desk_log`, `events`, `follow_ups`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `setting_values`, `permission_proposals`, `playbook`, `sessions`, `transfers`, `worker_starts`
- **Gateway operations:** `lead.resolve`, `msg.send`, `xfer.begin`, `xfer.part`, `xfer.finish`, `xfer.status`

### `agent_friction_mark`

Lead sessions only: set the read status of friction reports (new, read, triaged, fixed, wontfix or infra) by ids, by group, or by a filter with project, area and text. Give link (a change id or a web 

- **Reads:** `agents`, `friction`, `sessions`
- **Writes:** `friction`
- **Gateway operations:** `friction.mark`

### `agent_graph_read`

Read the graph: nodes with charters and rules, and their edges. Give node for one node and its edges; a shared name returns every node.

- **Reads:** `agents`, `apps`, `mcp_servers`, `messages`, `model_failures`, `node_scopes`, `nodes`, `setting_values`, `sessions`, `repos` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `graph.read`

### `agent_handback`

Workers only: end your task with your report and status. This is how the caller gets your result.

- **Reads:** `agents`, `deliveries`, `delivery_evidence`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `quota_blocks`, `worker_defects`, `worker_ratings`, `worker_runs` (plus the common reads)
- **Writes:** `ask_sorts`, `messages`, `inbox_items`, `delivery_changes`, `delivery_retries`, `delivery_revisions`, `delivery_step_tasks`, `desk_log`, `events`, `follow_ups`, `friction`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `setting_values`, `permission_proposals`, `playbook`, `sessions`, `worker_defects`, `worker_runs`, `worker_starts`
- **Gateway operations:** `msg.send`, `change.submit`, `change.list`, `change.get`, `friction.report`, `worker.recent_changes`, `worker.cost`, `rate.offer`, `delivery.needs_push`, `query.projects`, `query.agent`

### `agent_learn`

Record a finding about a model, harness, project or machine, with evidence. Findings feed the playbook; they change nothing directly.

- **Reads:** `sessions`, `worker_runs`
- **Writes:** `findings`, `playbook`
- **Gateway operations:** `learn.add`

### `agent_machine_get`

Read a machine's health, load, CPU and busiest processes, or one detail category: processes, models, quotas, track record or playbook. Processes accepts sort rss (default) or cpu and reports stop refu

- **Reads:** `nodes`, `setting_values`, `quota_blocks`, `sessions`, `gateway_state`, `worker_runs`
- **Writes:** `playbook`
- **Gateway operations:** `query.machine_get`

### `agent_machine_log`

Read a machine's redacted daemon or gateway log for a time window, with an optional text filter. At most 1000 lines and 256 KB.

- **Reads:** none found
- **Writes:** none found

### `agent_machines_find`

List machines with online state and versions. Use agent_machine_get for one machine's models, quotas, track record or playbook.

- **Reads:** `nodes`, `sessions`
- **Writes:** none found
- **Gateway operations:** `query.machines_find`

### `agent_mcp_servers_find`

List the shared MCP servers, their health and tools, and whether you may use them. Read-only.

- **Reads:** `charter_values`, `delivery_changes`, `delivery_evidence`, `graph_edges`, `inbox_items`, `mcp_servers`, `messages`, `model_failures`, `model_prices`, `nodes`, `sessions`, `repos`, `setting_values`, `gateway_state`, `agents`, `teams`, `worker_assessments`, `worker_defects`, `worker_ratings`, `worker_runs`
- **Writes:** `events`, `setting_values`
- **Gateway operations:** `mcp.list`, `worker.profile.list`

### `agent_mcp_test`

Test that a shared MCP server answers. Does not change it.

- **Reads:** `nodes`
- **Writes:** `mcp_servers`
- **Gateway operations:** `mcp.test`

### `agent_message_get`

Read one message's full body, delivery state and outcome. Long bodies come in chunks: follow next_body_cursor until it is absent. Attachments are in files: pass file to read one as text, then follow n

- **Reads:** `agents`, `apps`, `charter_drafts`, `messages`, `deliveries`, `delivery_retries`, `gateway_state`, `learned_suggestions`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `graph_outcomes`, `sessions`, `repos`, `quota_blocks`, `inbox_items`, `worker_ratings`, `worker_starts`, `worker_runs` (plus the common reads)
- **Writes:** `desk_log`, `ask_sorts`, `gateway_state`, `inbox_items`, `delivery_changes`, `delivery_evidence`, `delivery_revisions`, `delivery_step_tasks`, `events`, `follow_ups`, `friction`, `repos`, `messages`, `nudge_fires`, `nudge_pauses`, `setting_values`, `permission_proposals`, `playbook`, `sessions`, `standing_orders`, `worker_defects`, `worker_runs`
- **Gateway operations:** `query.message`, `query.messages`

### `agent_message_handoff`

Hand a request you received as a team or repo lead to a member. The member gets the original message with the original sender kept, answers the asker directly, and you get a copy of the reply. Use it 

- **Reads:** `agents`, `ask_sorts`, `messages`, `deliveries`, `delivery_changes`, `delivery_step_tasks`, `model_failures`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `worker_starts` (plus the common reads)
- **Writes:** `delivery_retries`, `events`, `inbox_items`, `messages`, `nudge_fires`, `setting_values`, `permission_proposals`, `sessions`
- **Gateway operations:** `lead.resolve`, `msg.handoff`

### `agent_message_reply`

Reply to a message you received, with an outcome on requests: accept, reject (reason), redirect (reason, redirect_to), escalate (recommendation). ack: true closes a request with no answer; progress: t

- **Reads:** `agents`, `apps`, `charter_drafts`, `deliveries`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `graph_outcomes`, `sessions`, `repos`, `quota_blocks`, `inbox_items`, `worker_defects`, `worker_ratings`, `worker_runs` (plus the common reads)
- **Writes:** `desk_log`, `ask_sorts`, `gateway_state`, `messages`, `inbox_items`, `delivery_changes`, `delivery_evidence`, `delivery_retries`, `delivery_revisions`, `delivery_step_tasks`, `events`, `follow_ups`, `friction`, `graph_outcomes`, `learned_suggestions`, `repos`, `nudge_fires`, `nudge_pauses`, `setting_values`, `permission_proposals`, `playbook`, `sessions`, `standing_orders`, `worker_defects`, `worker_runs`, `worker_starts`
- **Gateway operations:** `inbox.create`, `msg.send`, `query.message`, `tools.report`

### `agent_message_send`

Send to one session, or as the CTO to_set for a group. to also takes the role address chief or cto. urgent jumps the queue and still counts. A peer message is a request, not the owner's approval. Lead

- **Reads:** `agents`, `apps`, `charter_drafts`, `deliveries`, `delivery_changes`, `inbox_items`, `delivery_step_tasks`, `gateway_state`, `mcp_servers`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `quota_blocks`, `standing_orders`, `worker_defects`, `worker_ratings` (plus the common reads)
- **Writes:** `desk_log`, `ask_sorts`, `messages`, `inbox_items`, `delivery_retries`, `events`, `follow_ups`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `setting_values`, `permission_proposals`, `playbook`, `sessions`, `worker_starts`
- **Gateway operations:** `graph.read`, `inbox.create`, `lead.resolve`, `msg.send`, `query.agents`, `tools.report`

### `agent_message_withdraw`

Withdraw undelivered message; owner and CTO may withdraw another session's; older_than clears the older backlog.

- **Reads:** `messages`, `deliveries`, `nodes`, `graph_outcomes`, `sessions`
- **Writes:** `ask_sorts`, `delivery_retries`, `desk_log`, `events`, `inbox_items`, `messages`
- **Gateway operations:** `msg.withdraw`

### `agent_messages_find`

Find messages by conversation, peer, direction or time. Rows hold a preview; use agent_message_get for the body.

- **Reads:** `agents`, `messages`, `deliveries`, `delivery_retries`, `model_failures`, `node_scopes`, `nodes`, `graph_outcomes`, `sessions` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `query.messages`, `query.agents`

### `agent_note_write`

Save a note that outlives this session, for your agent or project: name, one-line description, body. The same name in the same scope updates it. The answer lists up to 3 similar notes: merge instead o

- **Reads:** `sessions`, `worker_starts` (plus the common reads)
- **Writes:** `standing_orders`
- **Gateway operations:** `note.write`, `notes.find`

### `agent_notes_find`

Read notes with their bodies. Your start text lists only titles, the most used first. Give text to search names, descriptions and bodies, scope to narrow, stale to list notes nobody used for 30 days.

- **Reads:** `sessions`, `worker_starts` (plus the common reads)
- **Writes:** `standing_orders`
- **Gateway operations:** `note.write`, `notes.find`

### `agent_nudge_pause`

Chief of staff only: preview or pause one automatic nudge kind for a team or agent. Preview lists sessions and the last 24h message count, without writes. Apply logs an owner-undoable pause. Real mess

- **Reads:** `agents`, `nudge_fires`, `setting_values`, `sessions`, `teams`
- **Writes:** `desk_log`, `messages`, `nudge_pauses`
- **Gateway operations:** `nudge.pause`

### `agent_nudge_pauses_find`

Chief of staff only: list nudge pauses, including when and why ended pauses ended.

- **Reads:** `setting_values`, `sessions`, `agents`
- **Writes:** `nudge_pauses`
- **Gateway operations:** `nudge.pauses.find`

### `agent_nudge_resume`

Chief of staff only: end a nudge pause by id. Logs an owner-undoable resume. No backlog is sent.

- **Reads:** `setting_values`, `sessions`, `agents`
- **Writes:** `desk_log`, `nudge_pauses`
- **Gateway operations:** `nudge.resume`

### `agent_nudge_stats`

Read the automatic nudge firing log: fires, followed and useful rate per kind by agent and by team, over the last 24 hours by default. A nudge counts as followed when a tool call, announce, step evide

- **Reads:** `agents`, `delivery_evidence`, `messages`, `sessions`, `teams`
- **Writes:** `nudge_fires`
- **Gateway operations:** `nudge.stats`

### `agent_owner_ask`

Ask the owner a question, an approval or an idea, with your recommendation and up to four answers. It goes to the chief of staff, who brings decisions to the owner; carry on, do not wait for the answe

- **Reads:** `agents`, `charter_drafts`, `deliveries`, `delivery_changes`, `inbox_items`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `standing_orders`, `worker_starts` (plus the common reads)
- **Writes:** `desk_log`, `ask_sorts`, `messages`, `delivery_retries`, `events`, `follow_ups`, `graph_outcomes`, `inbox_items`, `learned_suggestions`, `nudge_fires`, `setting_values`, `permission_proposals`, `sessions`
- **Gateway operations:** `inbox.create`

### `agent_owner_brief`

Chief of staff only: deliver one informational daily brief to the owner's Desk. Give body (at most 15 lines) and the owner-local date as YYYY-MM-DD; the same date replaces it. Call with no body for th

- **Reads:** `agents`, `ask_sorts`, `inbox_items`, `gateway_state`, `model_failures`, `node_scopes`, `nodes`, `sessions`, `standing_orders`, `worker_starts` (plus the common reads)
- **Writes:** `inbox_items`, `messages`, `setting_values`, `sessions`
- **Gateway operations:** `brief.put`, `brief.status`

### `agent_owner_judgment_record`

Chief of staff and CTO only: record, in the same turn, a decision the owner made in your chat. area and action make it a standing rule.

- **Reads:** `setting_values`, `sessions`
- **Writes:** `desk_log`, `standing_orders`
- **Gateway operations:** `judgment.record`

### `agent_owner_judgments_find`

Search the owner's past decisions, standing orders, rules, answers and corrections. Ranked for a query, else newest first. marks: true returns only the Desk marks with their asks; offset pages. Agent 

- **Reads:** `ask_sorts`, `inbox_items`, `sessions`, `standing_orders`, `standing_orders_fts`
- **Writes:** none found
- **Gateway operations:** `judgment.find`

### `agent_permission_propose`

Propose Claude Code permission rules or folder trust for the owner to approve.

- **Reads:** `agents`, `ask_sorts`, `node_scopes`, `nodes`, `sessions`, `projects`, `repos`
- **Writes:** `desk_log`, `inbox_items`, `messages`, `setting_values`, `permission_proposals`
- **Gateway operations:** `permission.propose`

### `agent_project_add`

Register project folders on this machine, or scan a folder for repositories (use dry_run first). Only when the owner asked.

- **Reads:** `agents`, `inbox_items`, `messages`, `model_failures`, `nodes`, `setting_values`, `repos` (plus the common reads)
- **Writes:** `events`, `graph_edges`, `projects`
- **Gateway operations:** `cto.proposal.validate`, `project.add`, `project.change`, `project.remove`

### `agent_projects_find`

Find registered projects and the machines and folders that hold them. Use agent_sessions_find for their sessions.

- **Reads:** `charter_values`, `graph_edges`, `nodes`, `repos`, `projects`, `sessions`, `setting_values`, `agents`, `teams`
- **Writes:** none found
- **Gateway operations:** `query.projects_find`

### `agent_protected_providers_get`



- **Reads:** none found
- **Writes:** `events`, `nodes`, `setting_values`
- **Gateway operations:** `http:handleProtectedProviders`

### `agent_protected_providers_set`



- **Reads:** none found
- **Writes:** `events`, `nodes`, `setting_values`
- **Gateway operations:** `http:handleProtectedProviders`

### `agent_session_get`

Read one session's profile: folder, branch, charter, rules, delegation, worker facts. Use agent_sessions_find when the ID or name is unknown.

- **Reads:** `agents`, `inbox_items`, `messages`, `model_failures`, `node_scopes`, `nodes`, `sessions` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `query.session_get`

### `agent_sessions_find`

Find sessions by machine, project, harness, model, kind, online state or text. Use agent_session_get for one.

- **Reads:** `agents`, `messages`, `model_failures`, `node_scopes`, `nodes`, `sessions` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `query.sessions_find`

### `agent_settings_read`

Read settings, values, sources and history by key, repo or named target. A target names level network, repo or agent; a name is looked up only in that kind.

- **Reads:** `agents`, `charter_values`, `graph_edges`, `repos`, `setting_history`, `setting_values`, `teams`, `worker_assessments`, `worker_runs`
- **Writes:** `repos`
- **Gateway operations:** `settings.history`, `settings.snapshot`, `settings.target`

### `agent_step_claim`

Claim a ready node-owned delivery step before starting it. The first eligible session to claim or report it takes it. Name the exact commit in the task. The caller may set reviewer to hand an accept o

- **Reads:** `agents`, `delivery_changes`, `delivery_evidence`, `delivery_step_tasks`, `gateway_state`, `sessions`, `repos` (plus the common reads)
- **Writes:** `delivery_step_tasks`, `messages`
- **Gateway operations:** `step.claim`

### `agent_step_report`

Report a delivery step you own: pass, fail with findings, needs_owner, or close without merge (the caller may close at any step, with a note). Name the exact commit you checked. Send reports for sever

- **Reads:** `agents`, `delivery_revisions`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `sessions`, `repos`, `worker_ratings`, `worker_runs` (plus the common reads)
- **Writes:** `ask_sorts`, `inbox_items`, `delivery_changes`, `delivery_evidence`, `delivery_step_tasks`, `desk_log`, `friction`, `repos`, `messages`, `setting_values`, `worker_defects`, `worker_runs`
- **Gateway operations:** `change.get`, `query.agent`, `query.projects`, `step.report`

### `agent_worker_profile_add`



- **Reads:** `charter_values`, `delivery_changes`, `delivery_evidence`, `graph_edges`, `inbox_items`, `messages`, `model_failures`, `model_prices`, `nodes`, `sessions`, `repos`, `setting_values`, `gateway_state`, `agents`, `teams`, `worker_assessments`, `worker_defects`, `worker_ratings`, `worker_runs`
- **Writes:** `events`, `setting_values`
- **Gateway operations:** `worker.profile.add`

### `agent_worker_profile_remove`



- **Reads:** `charter_values`, `delivery_changes`, `delivery_evidence`, `graph_edges`, `inbox_items`, `messages`, `model_failures`, `model_prices`, `nodes`, `sessions`, `repos`, `setting_values`, `gateway_state`, `agents`, `teams`, `worker_assessments`, `worker_defects`, `worker_ratings`, `worker_runs`
- **Writes:** `events`, `setting_values`
- **Gateway operations:** `worker.profile.remove`

### `agent_worker_profiles_find`

List worker profiles: harness, model, effort and the machines that can run them. Read before choosing a worker model.

- **Reads:** `charter_values`, `delivery_changes`, `delivery_evidence`, `graph_edges`, `inbox_items`, `messages`, `model_failures`, `model_prices`, `nodes`, `sessions`, `repos`, `setting_values`, `gateway_state`, `agents`, `teams`, `worker_assessments`, `worker_defects`, `worker_ratings`, `worker_runs`
- **Writes:** `events`, `setting_values`
- **Gateway operations:** `worker.profile.add`, `worker.profile.list`, `worker.profile.remove`

### `agent_worker_rate`

Rate a worker 1 to 5, or report a defect found later with severity 1 to 4. brief_defect charges rework from a bad brief to the briefing session, not the worker. move_to_brief_defect moves a defect you

- **Reads:** `agents`, `charter_values`, `delivery_changes`, `delivery_evidence`, `graph_edges`, `model_prices`, `setting_values`, `sessions`, `repos`, `gateway_state`, `teams`, `worker_assessments`, `worker_runs`
- **Writes:** `worker_defects`, `worker_ratings`, `worker_runs`
- **Gateway operations:** `rate.add`

### `agent_worker_start`

Start or continue a worker with a structured brief. Only if the owner or work mode asks. problem and done required; evidence, hypothesis, scope optional; task: extra notes. Refused only for a file a r

- **Reads:** `agents`, `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_revisions`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `quota_blocks`, `worker_ratings`, `worker_runs` (plus the common reads)
- **Writes:** `ask_sorts`, `charter_drafts`, `messages`, `inbox_items`, `delivery_retries`, `desk_log`, `events`, `follow_ups`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `setting_values`, `permission_proposals`, `pick_candidates`, `playbook`, `sessions`, `worker_assessments`, `worker_defects`, `worker_starts`
- **Gateway operations:** `cto.proposal.validate`, `lead.resolve`, `msg.send`, `policy.get`, `session.remove`, `tools.report`, `worker.override_ask`, `worker.precheck`, `worker.start`

### `agent_worker_stop`

Stop a worker you started, or cancel a queued start. Its worktree is kept.

- **Reads:** `inbox_items`, `messages`, `model_failures`, `nodes` (plus the common reads)
- **Writes:** `events`, `sessions`, `worker_starts`
- **Gateway operations:** `worker.stop`

### `agent_workers_find`

Find workers with their caller, model, difficulty, state and run time. detail full adds the start time, the struggle counts and the recorded cause of the latest run's failure or rework. Defaults to ru

- **Reads:** `agents`, `inbox_items`, `messages`, `model_failures`, `node_scopes`, `nodes`, `sessions` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `query.agents`, `query.agent`

## CTO tools

### `answer`

Apply the owner's answer to a held request, an inbox item or a contested outcome (let stand or overrule).

- **Reads:** `agents`, `deliveries`, `delivery_revisions`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `apps`, `sessions`, `project_bar_rules`, `repos`, `projects`, `inbox_items`, `worker_assessments`, `worker_defects`, `worker_runs`, `worker_starts`
- **Writes:** `agent_loops`, `apps`, `ask_sorts`, `gateway_state`, `charter_drafts`, `messages`, `charter_values`, `delivery_changes`, `delivery_evidence`, `delivery_retries`, `delivery_step_tasks`, `inbox_items`, `desk_log`, `events`, `follow_ups`, `graph_edges`, `graph_history`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `oauth_pending`, `setting_values`, `permission_proposals`, `repos`, `sessions`, `setting_history`, `standing_orders`, `agents`, `teams`
- **Gateway operations:** `held.answer`, `inbox.answer`, `outcome.resolve`

### `cto_dialog_answer`



- **Reads:** none found
- **Writes:** none found
- **Gateway operations:** `cto.proposal.validate`

### `cto_intake_find`

Open system-health alerts of the CTO intake queue, newest first. Read it at the start of each owner conversation.

- **Reads:** `inbox_items`
- **Writes:** none found
- **Gateway operations:** `cto.intake`

### `cto_new`

Start a new CTO conversation. The timeline and proposals stay.

- **Reads:** `graph_history`
- **Writes:** none found
- **Gateway operations:** `cto.graph_latest`

### `cto_op`



- **Reads:** none found
- **Writes:** none found

### `cto_op:agent_tombstone`

Deactivate a typed agent identity; owner approval, or at once with his words in owner_words.

- **Reads:** `delivery_changes`, `delivery_step_tasks`, `inbox_items`, `messages`, `model_failures`, `nodes`, `repos`, `worker_starts` (plus the common reads)
- **Writes:** `agents`, `graph_history`, `repos`
- **Gateway operations:** `agent.tombstone`

### `cto_op:app_grant`

Grant a shared MCP server to a bot.

- **Reads:** `mcp_servers`, `nodes`
- **Writes:** `apps`
- **Gateway operations:** `apps.grant`

### `cto_op:app_rename`

Rename an active app.

- **Reads:** none found
- **Writes:** `apps`, `sessions`
- **Gateway operations:** `apps.rename`

### `cto_op:app_revoke`

Revoke an app and its credentials; cannot be undone.

- **Reads:** `sessions`
- **Writes:** `app_tokens`, `apps`, `ask_sorts`, `desk_log`, `inbox_items`, `oauth_pending`, `setting_values`
- **Gateway operations:** `apps.revoke`

### `cto_op:app_ungrant`

Remove a bot's grant of a shared MCP server.

- **Reads:** `mcp_servers`, `nodes`
- **Writes:** `apps`
- **Gateway operations:** `apps.ungrant`

### `cto_op:apps.pending`

Read apps pending through the owner operation.

- **Reads:** `apps`, `oauth_pending`
- **Writes:** `oauth_attempts`
- **Gateway operations:** `apps.pending`

### `cto_op:apps.public.restore`

Run the owner operation apps.public.restore.

- **Reads:** `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_revisions`, `delivery_step_tasks`, `gateway_state`, `mcp_servers`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `quota_blocks`, `setting_history`, `worker_defects`, `worker_ratings`, `worker_runs` (plus the common reads)
- **Writes:** `agents`, `app_tokens`, `apps`, `ask_sorts`, `messages`, `inbox_items`, `delivery_retries`, `desk_log`, `events`, `follow_ups`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `oauth_pending`, `setting_values`, `permission_proposals`, `pick_candidates`, `playbook`, `sessions`, `repos`, `worker_assessments`, `worker_defects`, `worker_starts`
- **Gateway operations:** `apps.public.restore`, `apps.public.status`

### `cto_op:apps.public.url`

Run the owner operation apps.public.url.

- **Reads:** `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_revisions`, `delivery_step_tasks`, `gateway_state`, `mcp_servers`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `quota_blocks`, `setting_history`, `worker_defects`, `worker_ratings`, `worker_runs` (plus the common reads)
- **Writes:** `agents`, `app_tokens`, `apps`, `ask_sorts`, `messages`, `inbox_items`, `delivery_retries`, `desk_log`, `events`, `follow_ups`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `oauth_pending`, `setting_values`, `permission_proposals`, `pick_candidates`, `playbook`, `sessions`, `repos`, `worker_assessments`, `worker_defects`, `worker_starts`
- **Gateway operations:** `apps.public.restore`, `apps.public.status`, `apps.public.url`

### `cto_op:apps.refuse`

Run the owner operation apps.refuse.

- **Reads:** `sessions`
- **Writes:** `app_tokens`, `apps`, `ask_sorts`, `desk_log`, `inbox_items`, `oauth_pending`, `setting_values`
- **Gateway operations:** `apps.refuse`

### `cto_op:apps_list`

List connected apps and their IDs.

- **Reads:** `apps`, `sessions`
- **Writes:** none found
- **Gateway operations:** `apps.list`

### `cto_op:ask_history`

Read ask history through the owner operation.

- **Reads:** `ask_sorts`
- **Writes:** none found
- **Gateway operations:** `ask.history`

### `cto_op:ask_mark`

Run the owner operation ask_mark.

- **Reads:** `inbox_items`
- **Writes:** `desk_log`, `standing_orders`
- **Gateway operations:** `ask.mark`

### `cto_op:ask_undo`

Run the owner operation ask_undo.

- **Reads:** none found
- **Writes:** `ask_sorts`, `desk_log`, `inbox_items`
- **Gateway operations:** `ask.undo`

### `cto_op:asks_list`

Read asks list through the owner operation.

- **Reads:** `delivery_changes`, `inbox_items`, `sessions` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `asks.list`

### `cto_op:bar_edit`

Add, edit or remove the short rules that workers and reviewers check a project's diffs against. Name the project.

- **Reads:** `agents`, `sessions` (plus the common reads)
- **Writes:** `repos`, `project_bar_rules`
- **Gateway operations:** `bar.edit`

### `cto_op:changes_owner_find`

Read changes owner find through the owner operation.

- **Reads:** `delivery_changes`, `delivery_evidence`, `delivery_revisions`, `delivery_step_tasks`, `inbox_items`, `sessions`, `repos` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `change.get`, `change.list`

### `cto_op:changes_owner_report`

Run the owner operation changes_owner_report.

- **Reads:** `agents`, `delivery_revisions`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `sessions`, `repos`, `worker_ratings`, `worker_runs` (plus the common reads)
- **Writes:** `ask_sorts`, `inbox_items`, `delivery_changes`, `delivery_evidence`, `delivery_step_tasks`, `desk_log`, `friction`, `repos`, `messages`, `setting_values`, `worker_defects`, `worker_runs`
- **Gateway operations:** `change.get`, `step.report`

### `cto_op:charter_history`

Read charter history through the owner operation.

- **Reads:** `graph_history`
- **Writes:** none found
- **Gateway operations:** `charter.history`

### `cto_op:chief_get`

Read chief get through the owner operation.

- **Reads:** `ask_sorts`, `charter_values`, `graph_edges`, `inbox_items`, `sessions`, `repos`, `setting_values`, `agents`, `teams`, `worker_assessments`, `worker_runs`
- **Writes:** `setting_values`
- **Gateway operations:** `chief.get`

### `cto_op:chief_set`

Run the owner operation chief_set.

- **Reads:** `agent_loops`, `agents`, `ask_sorts`, `charter_values`, `delivery_changes`, `delivery_evidence`, `delivery_step_tasks`, `gateway_state`, `model_prices`, `node_scopes`, `nodes`, `sessions`, `project_bar_rules`, `projects`, `repos`, `teams`, `worker_assessments`, `worker_defects`, `worker_runs`
- **Writes:** `delivery_step_tasks`, `inbox_items`, `graph_edges`, `graph_history`, `messages`, `setting_values`, `sessions`, `setting_history`
- **Gateway operations:** `chief.set`

### `cto_op:conversation_messages`

Read conversation messages through the owner operation.

- **Reads:** `messages`, `deliveries`, `delivery_retries`, `graph_outcomes`, `sessions`
- **Writes:** none found
- **Gateway operations:** `query.conversation_messages`

### `cto_op:conversations`

Read conversations through the owner operation.

- **Reads:** `messages`, `sessions`
- **Writes:** none found
- **Gateway operations:** `query.conversations`

### `cto_op:counts`

Read counts through the owner operation.

- **Reads:** `apps`, `inbox_items`, `messages`, `nodes`, `sessions`
- **Writes:** none found
- **Gateway operations:** `query.counts`

### `cto_op:cto_enter`

Run the owner operation cto_enter.

- **Reads:** `inbox_items`, `messages`, `model_failures`, `nodes` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `owner.ui.request`

### `cto_op:cto_events`

Read cto events through the owner operation.

- **Reads:** `agent_loops`, `agents`, `charter_drafts`, `charter_values`, `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_revisions`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `project_bar_rules`, `repos`, `projects`, `standing_orders`, `inbox_items`, `teams`, `worker_assessments`, `worker_defects`, `worker_runs`, `worker_starts`
- **Writes:** `desk_log`, `ask_sorts`, `gateway_state`, `messages`, `delivery_retries`, `delivery_step_tasks`, `inbox_items`, `events`, `follow_ups`, `graph_edges`, `graph_history`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `setting_values`, `permission_proposals`, `sessions`, `setting_history`
- **Gateway operations:** `inbox.create`, `settings.snapshot`, `settings.write`

### `cto_op:cto_graph_history`

Read cto graph history through the owner operation.

- **Reads:** `graph_history`
- **Writes:** none found
- **Gateway operations:** `cto.graph_history`

### `cto_op:cto_send`

Run the owner operation cto_send.

- **Reads:** `agents`, `deliveries`, `delivery_changes`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `repos`, `quota_blocks`, `worker_defects`, `worker_ratings` (plus the common reads)
- **Writes:** `ask_sorts`, `messages`, `inbox_items`, `delivery_retries`, `desk_log`, `events`, `follow_ups`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `setting_values`, `permission_proposals`, `playbook`, `sessions`, `worker_starts`
- **Gateway operations:** `cto.session.register`, `msg.send`, `tools.report`

### `cto_op:delivery_owner_set`

Run the owner operation delivery_owner_set.

- **Reads:** `agent_loops`, `agents`, `messages`, `charter_values`, `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_retries`, `delivery_revisions`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `project_bar_rules`, `repos`, `projects`, `setting_values`, `teams`, `worker_assessments`, `worker_defects`, `worker_runs`, `worker_starts`
- **Writes:** `ask_sorts`, `gateway_state`, `delivery_step_tasks`, `inbox_items`, `desk_log`, `events`, `graph_edges`, `graph_history`, `messages`, `nudge_fires`, `setting_values`, `permission_proposals`, `sessions`
- **Gateway operations:** `delivery.owner.set`

### `cto_op:desk_log`

Read desk log through the owner operation.

- **Reads:** `desk_log`
- **Writes:** none found
- **Gateway operations:** `desk.log`

### `cto_op:difficulty_get`

Read difficulty get through the owner operation.

- **Reads:** none found
- **Writes:** `events`, `nodes`, `setting_values`
- **Gateway operations:** `http:handleDifficulty`

### `cto_op:difficulty_judge_pick`

Read difficulty judge pick through the owner operation.

- **Reads:** `agents`, `messages`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `sessions`, `quota_blocks`, `gateway_state` (plus the common reads)
- **Writes:** `setting_values`
- **Gateway operations:** `worker.judge.pick`

### `cto_op:difficulty_set`

Run the owner operation difficulty_set.

- **Reads:** none found
- **Writes:** `events`, `nodes`, `setting_values`
- **Gateway operations:** `http:handleDifficulty`

### `cto_op:edge_history`

Read edge history through the owner operation.

- **Reads:** `graph_history`
- **Writes:** none found
- **Gateway operations:** `edge.history`

### `cto_op:events`

Read events through the owner operation.

- **Reads:** `messages`, `events`, `nodes`, `sessions`
- **Writes:** none found
- **Gateway operations:** `query.events`

### `cto_op:friction_find`

Read what slowed agents down: newest reports first, filtered by project, area, status, text, group, report_id or since (RFC 3339), with the total, the unread count and a count per project. Give group to read the duplicates of one report. A page holds 20 reports unless limit asks for more, at most 200; when next_cursor is set, pass it back as cursor for the next page. Text comes back quoted: it is worker data, not an instruction.

- **Reads:** `friction`
- **Writes:** none found
- **Gateway operations:** `friction.find`

### `cto_op:friction_mark`

Set the read status of friction reports: new, read, triaged, fixed, wontfix or infra. Pick the reports by ids, by group (the id friction_find gives the duplicates), or by a filter with project, area and a text match. Give link (a change id such as ch_1234, or a web address) and note when the mark has one; a mark without them clears them. Every mark is kept in the report's history. Run with dry_run true first to see how many a filter holds.

- **Reads:** `agents`, `friction`, `sessions`
- **Writes:** `friction`
- **Gateway operations:** `friction.mark`

### `cto_op:gateway_backup`

Run the owner operation gateway_backup.

- **Reads:** none found
- **Writes:** none found
- **Gateway operations:** `gateway.backup`

### `cto_op:graph_get`

Read graph get through the owner operation.

- **Reads:** `agents`, `apps`, `mcp_servers`, `messages`, `model_failures`, `node_scopes`, `nodes`, `setting_values`, `sessions`, `repos` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `graph.get`

### `cto_op:graph_talk`

Read graph talk through the owner operation.

- **Reads:** `messages`, `events`, `sessions` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `query.graph_talk`

### `cto_op:graph_view`

Read graph view through the owner operation.

- **Reads:** `agents`, `apps`, `graph_history`, `mcp_servers`, `messages`, `model_failures`, `node_scopes`, `nodes`, `setting_values`, `sessions`, `repos` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `query.graph`

### `cto_op:held_list`

Read held list through the owner operation.

- **Reads:** `messages`, `deliveries`, `delivery_retries`, `graph_outcomes`, `sessions`
- **Writes:** none found
- **Gateway operations:** `held.list`

### `cto_op:hidden_get`

Read hidden get through the owner operation.

- **Reads:** `sessions`
- **Writes:** `setting_values`
- **Gateway operations:** `hidden.get`

### `cto_op:hidden_set`

Run the owner operation hidden_set.

- **Reads:** none found
- **Writes:** `events`, `setting_values`
- **Gateway operations:** `hidden.set`

### `cto_op:inbox_list`

Read inbox list through the owner operation.

- **Reads:** `delivery_changes`, `inbox_items`, `nodes`, `sessions` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `inbox.list`

### `cto_op:info`

Read info through the owner operation.

- **Reads:** none found
- **Writes:** none found

### `cto_op:integrations_get`

Read integrations get through the owner operation.

- **Reads:** `setting_values`
- **Writes:** `nodes`
- **Gateway operations:** `http:handleIntegrationsGet`

### `cto_op:inventory_get`

Read inventory get through the owner operation.

- **Reads:** `nodes`
- **Writes:** none found
- **Gateway operations:** `query.inventory`

### `cto_op:inventory_scan`

Read inventory scan through the owner operation.

- **Reads:** `charter_values`, `graph_edges`, `mcp_servers`, `nodes`, `repos`, `projects`, `sessions`, `setting_values`, `agents`, `teams`
- **Writes:** none found
- **Gateway operations:** `mcp.list`, `node.inventory`, `query.projects`

### `cto_op:learned_answer`

Decide a learned suggestion (suggestion_id, action accept or reject), or a charter draft with suggestion_id as minus its draft number. Read drafts with cto_proposals_find kind draft. Accept on a draft writes the value and returns the stored row with history_id; undo it with graph_change action undo on that id. Reject writes nothing.

- **Reads:** `agent_loops`, `agents`, `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `project_bar_rules`, `repos`, `projects`, `setting_values`, `worker_assessments`, `worker_defects`, `worker_runs`, `worker_starts`
- **Writes:** `ask_sorts`, `charter_drafts`, `messages`, `charter_values`, `delivery_retries`, `delivery_step_tasks`, `inbox_items`, `desk_log`, `events`, `follow_ups`, `graph_edges`, `graph_history`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `setting_values`, `permission_proposals`, `repos`, `sessions`, `agents`, `teams`
- **Gateway operations:** `learned.answer`

### `cto_op:learned_list`

Read learned list through the owner operation.

- **Reads:** `agent_loops`, `agents`, `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `project_bar_rules`, `repos`, `projects`, `setting_values`, `worker_assessments`, `worker_defects`, `worker_runs`, `worker_starts`
- **Writes:** `ask_sorts`, `charter_drafts`, `messages`, `charter_values`, `delivery_retries`, `delivery_step_tasks`, `inbox_items`, `desk_log`, `events`, `follow_ups`, `graph_edges`, `graph_history`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `setting_values`, `permission_proposals`, `repos`, `sessions`, `agents`, `teams`
- **Gateway operations:** `learned.list`

### `cto_op:learned_view`

Read learned view through the owner operation.

- **Reads:** `agent_loops`, `agents`, `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `project_bar_rules`, `repos`, `projects`, `setting_values`, `worker_assessments`, `worker_defects`, `worker_runs`, `worker_starts`
- **Writes:** `ask_sorts`, `charter_drafts`, `messages`, `charter_values`, `delivery_retries`, `delivery_step_tasks`, `inbox_items`, `desk_log`, `events`, `follow_ups`, `graph_edges`, `graph_history`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `setting_values`, `permission_proposals`, `repos`, `sessions`, `agents`, `teams`
- **Gateway operations:** `learned.view`

### `cto_op:list`

Read list through the owner operation.

- **Reads:** `agents`, `deliveries`, `delivery_changes`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `quota_blocks`, `standing_order_history`, `standing_orders_fts`, `worker_defects`, `worker_ratings` (plus the common reads)
- **Writes:** `ask_sorts`, `messages`, `inbox_items`, `delivery_retries`, `desk_log`, `events`, `follow_ups`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `setting_values`, `permission_proposals`, `playbook`, `sessions`, `standing_orders`, `worker_starts`
- **Gateway operations:** `msg.send`, `query.agents`, `standing.list`, `tools.report`

### `cto_op:machine_remove`

Remove a machine and revoke its token; owner approval, or at once with his words in owner_words.

- **Reads:** `inbox_items`, `messages`, `model_failures` (plus the common reads)
- **Writes:** `events`, `nodes`, `sessions`
- **Gateway operations:** `machine.remove`

### `cto_op:machine_rotate`

Rotate an online machine token; owner approval, or at once with his words in owner_words.

- **Reads:** `inbox_items`, `messages`, `model_failures` (plus the common reads)
- **Writes:** `events`, `nodes`, `sessions`
- **Gateway operations:** `machine.rotate`

### `cto_op:mcp_coverage_apply`

Run the owner operation mcp_coverage_apply.

- **Reads:** none found
- **Writes:** none found

### `cto_op:mcp_coverage_plan`

Run the owner operation mcp_coverage_plan.

- **Reads:** none found
- **Writes:** none found

### `cto_op:mcp_coverage_undo`

Run the owner operation mcp_coverage_undo.

- **Reads:** none found
- **Writes:** none found

### `cto_op:merger_get`

Read merger get through the owner operation.

- **Reads:** `inbox_items`, `messages`, `model_failures`, `nodes`, `repos` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `merger.get`

### `cto_op:merger_restore`

Run the owner operation merger_restore.

- **Reads:** `messages`, `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_retries`, `delivery_revisions`, `model_failures`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `worker_starts` (plus the common reads)
- **Writes:** `agents`, `ask_sorts`, `gateway_state`, `delivery_step_tasks`, `inbox_items`, `desk_log`, `events`, `graph_edges`, `messages`, `nudge_fires`, `setting_values`, `permission_proposals`, `repos`, `sessions`
- **Gateway operations:** `merger.restore`, `query.agent`

### `cto_op:merger_set`

Run the owner operation merger_set.

- **Reads:** `agent_loops`, `messages`, `charter_values`, `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_retries`, `delivery_revisions`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `project_bar_rules`, `projects`, `repos`, `agents`, `teams`, `worker_assessments`, `worker_defects`, `worker_runs`, `worker_starts`
- **Writes:** `agents`, `ask_sorts`, `gateway_state`, `delivery_step_tasks`, `inbox_items`, `desk_log`, `events`, `graph_edges`, `graph_history`, `messages`, `nudge_fires`, `setting_values`, `permission_proposals`, `sessions`, `repos`, `setting_history`
- **Gateway operations:** `merger.set`, `query.agent`

### `cto_op:message_status`

Read message status through the owner operation.

- **Reads:** `agents`, `apps`, `charter_drafts`, `messages`, `deliveries`, `delivery_retries`, `gateway_state`, `learned_suggestions`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `graph_outcomes`, `sessions`, `repos`, `quota_blocks`, `inbox_items`, `worker_ratings`, `worker_starts`, `worker_runs` (plus the common reads)
- **Writes:** `desk_log`, `ask_sorts`, `gateway_state`, `inbox_items`, `delivery_changes`, `delivery_evidence`, `delivery_revisions`, `delivery_step_tasks`, `events`, `follow_ups`, `friction`, `repos`, `messages`, `nudge_fires`, `nudge_pauses`, `setting_values`, `permission_proposals`, `playbook`, `sessions`, `standing_orders`, `worker_defects`, `worker_runs`
- **Gateway operations:** `query.message`, `func:relayMessageBody`

### `cto_op:node_remove`

Remove an edgeless architect; owner approval, or at once with his words in owner_words.

- **Reads:** `delivery_changes`, `delivery_evidence`, `delivery_step_tasks`, `gateway_state`, `model_prices`, `setting_values`, `repos`, `projects`, `sessions`, `agents`, `teams`, `worker_assessments`, `worker_defects`, `worker_runs`, `worker_starts`
- **Writes:** `agents`, `charter_values`, `graph_edges`, `graph_history`, `repos`, `setting_history`, `setting_values`
- **Gateway operations:** `node.remove`

### `cto_op:node_settings_get`

Read node settings get through the owner operation.

- **Reads:** none found (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `node.settings.get`

### `cto_op:nodes`

Read nodes through the owner operation.

- **Reads:** `model_failures`, `model_prices`, `nodes`, `sessions`, `quota_blocks`, `gateway_state`, `worker_assessments`, `worker_defects`, `worker_ratings`, `worker_runs`
- **Writes:** `setting_values`, `playbook`
- **Gateway operations:** `query.nodes`

### `cto_op:notes_copy`

Copy the agent-scope notes of one agent node to another (from_agent, to_agent, both agent ids). It only adds: the first agent keeps all its notes, and a note whose name the second agent already has is kept as it is. Returns copied_notes (the new note ids) and kept_notes (names it skipped). Undo removes only the copies it made.

- **Reads:** `agents`, `sessions`
- **Writes:** `standing_orders`
- **Gateway operations:** `notes.copy`

### `cto_op:notes_uncopy`

Run the owner operation notes_uncopy.

- **Reads:** `sessions`
- **Writes:** `standing_orders`
- **Gateway operations:** `notes.uncopy`

### `cto_op:nudge_stats`

Read nudge stats through the owner operation.

- **Reads:** `agents`, `delivery_evidence`, `messages`, `sessions`, `teams`
- **Writes:** `nudge_fires`
- **Gateway operations:** `nudge.stats`

### `cto_op:onboarding_get`

Read onboarding get through the owner operation.

- **Reads:** `setting_values`
- **Writes:** none found
- **Gateway operations:** `onboarding.get`

### `cto_op:onboarding_set`

Run the owner operation onboarding_set.

- **Reads:** none found
- **Writes:** `setting_values`
- **Gateway operations:** `onboarding.set`

### `cto_op:operator_undo`

Run the owner operation operator_undo.

- **Reads:** `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_revisions`, `delivery_step_tasks`, `gateway_state`, `mcp_servers`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `quota_blocks`, `setting_history`, `worker_defects`, `worker_ratings`, `worker_runs` (plus the common reads)
- **Writes:** `agents`, `app_tokens`, `apps`, `ask_sorts`, `messages`, `inbox_items`, `delivery_retries`, `desk_log`, `events`, `follow_ups`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `oauth_pending`, `setting_values`, `permission_proposals`, `pick_candidates`, `playbook`, `sessions`, `repos`, `worker_assessments`, `worker_defects`, `worker_starts`
- **Gateway operations:** `apps.public.restore`

### `cto_op:outcome_inbox`

Read outcome inbox through the owner operation.

- **Reads:** `graph_outcomes`, `messages`, `sessions`
- **Writes:** none found
- **Gateway operations:** `query.outcome_inbox`

### `cto_op:permission_apply`

When the owner tells you in this chat to grant a Claude Code permission rule or folder trust, apply it now with his words quoted in owner_words; do not file a Desk proposal. Give rules and targets (registered repo names) as agent_permission_propose takes them, kind trust with machine and folders, or id alone to approve a pp_ proposal that waits on the Desk. Returns the stored record: each target's status, the rules added and the file written. Undo with permission_undo.

- **Reads:** `messages`, `model_failures`, `nodes`, `sessions` (plus the common reads)
- **Writes:** `desk_log`, `inbox_items`, `permission_proposals`
- **Gateway operations:** `permission.owner_apply`

### `cto_op:permission_undo`

Remove exactly the rules or trust one permission approval added, by its pp_ id.

- **Reads:** `inbox_items`, `messages`, `model_failures`, `nodes` (plus the common reads)
- **Writes:** `desk_log`, `permission_proposals`
- **Gateway operations:** `permission.undo`

### `cto_op:picker_get`

Read picker get through the owner operation.

- **Reads:** `setting_values`
- **Writes:** `nodes`
- **Gateway operations:** `http:handlePicker`

### `cto_op:playbook_get`

Read playbook get through the owner operation.

- **Reads:** none found
- **Writes:** `playbook`
- **Gateway operations:** `playbook.get`

### `cto_op:process_stop`

Stop processes on a machine: TERM then KILL; no undo.

- **Reads:** `inbox_items`, `messages`, `model_failures`, `nodes` (plus the common reads)
- **Writes:** `events`
- **Gateway operations:** `session.stop_process`

### `cto_op:project_display_get`

Read project display get through the owner operation.

- **Reads:** `projects`, `sessions`
- **Writes:** `repos`
- **Gateway operations:** `project.display.get`, `project.display.set`

### `cto_op:projects`

Read projects through the owner operation.

- **Reads:** `charter_values`, `graph_edges`, `nodes`, `repos`, `projects`, `sessions`, `setting_values`, `agents`, `teams`
- **Writes:** none found
- **Gateway operations:** `query.projects`

### `cto_op:protected_providers_get`

Read protected providers get through the owner operation.

- **Reads:** none found
- **Writes:** `events`, `nodes`, `setting_values`
- **Gateway operations:** `http:handleProtectedProviders`

### `cto_op:public_off`

Turn internet access off for connected apps.

- **Reads:** `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_revisions`, `delivery_step_tasks`, `gateway_state`, `mcp_servers`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `quota_blocks`, `setting_history`, `worker_defects`, `worker_ratings`, `worker_runs` (plus the common reads)
- **Writes:** `agents`, `app_tokens`, `apps`, `ask_sorts`, `messages`, `inbox_items`, `delivery_retries`, `desk_log`, `events`, `follow_ups`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `oauth_pending`, `setting_values`, `permission_proposals`, `pick_candidates`, `playbook`, `sessions`, `repos`, `worker_assessments`, `worker_defects`, `worker_starts`
- **Gateway operations:** `apps.public.off`

### `cto_op:public_on`

Turn internet access on for connected apps.

- **Reads:** `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_revisions`, `delivery_step_tasks`, `gateway_state`, `mcp_servers`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `quota_blocks`, `setting_history`, `worker_defects`, `worker_ratings`, `worker_runs` (plus the common reads)
- **Writes:** `agents`, `app_tokens`, `apps`, `ask_sorts`, `messages`, `inbox_items`, `delivery_retries`, `desk_log`, `events`, `follow_ups`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `oauth_pending`, `setting_values`, `permission_proposals`, `pick_candidates`, `playbook`, `sessions`, `repos`, `worker_assessments`, `worker_defects`, `worker_starts`
- **Gateway operations:** `apps.public.on`

### `cto_op:public_status`

Read internet access for connected apps.

- **Reads:** `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_revisions`, `delivery_step_tasks`, `gateway_state`, `mcp_servers`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `quota_blocks`, `setting_history`, `worker_defects`, `worker_ratings`, `worker_runs` (plus the common reads)
- **Writes:** `agents`, `app_tokens`, `apps`, `ask_sorts`, `messages`, `inbox_items`, `delivery_retries`, `desk_log`, `events`, `follow_ups`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `oauth_pending`, `setting_values`, `permission_proposals`, `pick_candidates`, `playbook`, `sessions`, `repos`, `worker_assessments`, `worker_defects`, `worker_starts`
- **Gateway operations:** `apps.public.status`

### `cto_op:quality_get`

Read quality get through the owner operation.

- **Reads:** none found
- **Writes:** `events`, `nodes`
- **Gateway operations:** `http:handleQualitySettings`

### `cto_op:quality_set`

Run the owner operation quality_set.

- **Reads:** none found
- **Writes:** `events`, `nodes`
- **Gateway operations:** `http:handleQualitySettings`

### `cto_op:repo_archive`

Deactivate a typed repo identity; owner approval, or at once with his words in owner_words.

- **Reads:** `delivery_changes`, `delivery_step_tasks`, `inbox_items`, `messages`, `model_failures`, `nodes`, `repos`, `worker_starts` (plus the common reads)
- **Writes:** `agents`, `graph_history`, `repos`
- **Gateway operations:** `repo.archive`

### `cto_op:scores_get`

Read scores get through the owner operation.

- **Reads:** `setting_values`
- **Writes:** `nodes`
- **Gateway operations:** `http:handleScoresGet`

### `cto_op:scores_weekly`

Read scores weekly through the owner operation.

- **Reads:** `delivery_changes`, `delivery_evidence`, `sessions`, `gateway_state`, `worker_assessments`, `worker_defects`, `worker_runs`
- **Writes:** `nodes`
- **Gateway operations:** `http:handleScoresWeekly`

### `cto_op:session_agent_list`

Read session agent list through the owner operation.

- **Reads:** `agents`, `sessions`
- **Writes:** none found
- **Gateway operations:** `session.agent.list`

### `cto_op:session_agent_set`

Move a lasting agent session to another agent node (session_id, agent). Notes belong to the agent node, so the moved session stops seeing the old agent's notes. Set copy_notes true to copy the old agent's agent-scope notes to the new agent first: it keeps the new agent's own notes of the same name and leaves the old agent's notes in place. Undo moves the session back and removes only the notes the call copied.

- **Reads:** `ask_sorts`, `gateway_state`, `model_failures`, `node_scopes`, `nodes`, `sessions`, `repos` (plus the common reads)
- **Writes:** `agents`, `inbox_items`, `messages`, `setting_values`, `sessions`, `standing_orders`
- **Gateway operations:** `query.agent`, `session.agent.set`

### `cto_op:session_enter`

Run the owner operation session_enter.

- **Reads:** `agents`, `delivery_changes`, `inbox_items`, `messages`, `model_failures`, `node_scopes`, `nodes`, `sessions` (plus the common reads)
- **Writes:** `ask_sorts`, `desk_log`, `inbox_items`, `sessions`, `standing_orders`
- **Gateway operations:** `owner.ui.request`, `query.agent`, `query.agents`, `query.session_enter`, `session.owner_use`, `session.remove`

### `cto_op:session_pin`

Run the owner operation session_pin.

- **Reads:** `inbox_items`, `messages`, `model_failures`, `nodes` (plus the common reads)
- **Writes:** `sessions`
- **Gateway operations:** `query.agent`, `session.pin`

### `cto_op:session_replace`

Move an old session's work to the session that replaced it.

- **Reads:** `model_failures`, `nodes` (plus the common reads)
- **Writes:** `agents`, `delivery_changes`, `delivery_step_tasks`, `desk_log`, `inbox_items`, `messages`, `sessions`, `repos`, `worker_runs`, `worker_starts`
- **Gateway operations:** `session.owner_replace`

### `cto_op:session_resume`

Run the owner operation session_resume.

- **Reads:** `agent_loops`, `messages`, `charter_values`, `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_retries`, `delivery_revisions`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `project_bar_rules`, `repos`, `projects`, `agents`, `teams`, `worker_assessments`, `worker_defects`, `worker_runs`, `worker_starts`
- **Writes:** `agents`, `ask_sorts`, `gateway_state`, `delivery_step_tasks`, `inbox_items`, `desk_log`, `events`, `graph_edges`, `graph_history`, `messages`, `nudge_fires`, `setting_values`, `permission_proposals`, `sessions`, `repos`, `setting_history`, `standing_orders`
- **Gateway operations:** `merger.set`, `query.agent`, `session.owner_use`, `session.resume`

### `cto_op:session_start_get`

Read session start get through the owner operation.

- **Reads:** `ask_sorts`, `model_failures`, `node_scopes`, `nodes`, `sessions`, `repos` (plus the common reads)
- **Writes:** `events`, `inbox_items`, `messages`, `setting_values`, `permission_proposals`
- **Gateway operations:** `query.session_enter`, `session.get`

### `cto_op:session_stop`

Run the owner operation session_stop.

- **Reads:** `ask_sorts`, `model_failures`, `node_scopes`, `nodes`, `sessions`, `repos` (plus the common reads)
- **Writes:** `events`, `inbox_items`, `messages`, `setting_values`, `permission_proposals`, `sessions`
- **Gateway operations:** `query.agent`, `session.remove`, `session.stop`

### `cto_op:session_text`

Read session text through the owner operation.

- **Reads:** `agents`, `messages`, `model_failures`, `node_scopes`, `nodes`, `sessions` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `query.agents`, `query.session_text`

### `cto_op:sessions_archive`

Run the owner operation sessions_archive.

- **Reads:** `ask_sorts`, `model_failures`, `node_scopes`, `nodes`, `sessions`, `repos` (plus the common reads)
- **Writes:** `agents`, `inbox_items`, `messages`, `setting_values`, `sessions`
- **Gateway operations:** `sessions.archive`, `sessions.archived`

### `cto_op:sessions_archived`

Read sessions archived through the owner operation.

- **Reads:** `ask_sorts`, `model_failures`, `node_scopes`, `nodes`, `sessions`, `repos` (plus the common reads)
- **Writes:** `agents`, `inbox_items`, `messages`, `setting_values`, `sessions`
- **Gateway operations:** `sessions.archive`, `sessions.archived`

### `cto_op:settings_owner_read`

Read settings owner read through the owner operation.

- **Reads:** `agents`, `charter_values`, `graph_edges`, `repos`, `setting_history`, `setting_values`, `teams`, `worker_assessments`, `worker_runs`
- **Writes:** `repos`
- **Gateway operations:** `settings.history`, `settings.snapshot`, `settings.target`

### `cto_op:sign_in_list`

Read sign in list through the owner operation.

- **Reads:** `delivery_changes`, `inbox_items`, `sessions` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `sign_in.list`

### `cto_op:sign_in_raise`

Run the owner operation sign_in_raise.

- **Reads:** `ask_sorts`, `node_scopes`, `nodes`, `sessions`, `repos`, `agents`
- **Writes:** `inbox_items`, `messages`, `setting_values`
- **Gateway operations:** `sign_in.raise`

### `cto_op:step_report`

Report pass, fail or needs_owner for the accept or review step that a change's caller named you to report. Name the full head commit you checked. Refused when the caller did not name you for that step.

- **Reads:** `agents`, `delivery_revisions`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `sessions`, `repos`, `worker_ratings`, `worker_runs` (plus the common reads)
- **Writes:** `ask_sorts`, `inbox_items`, `delivery_changes`, `delivery_evidence`, `delivery_step_tasks`, `desk_log`, `friction`, `repos`, `messages`, `setting_values`, `worker_defects`, `worker_runs`
- **Gateway operations:** `step.report`, `change.get`

### `cto_op:team_history`

Read team history through the owner operation.

- **Reads:** `agent_loops`, `agents`, `ask_sorts`, `delivery_changes`, `delivery_evidence`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `sessions`, `project_bar_rules`, `repos`, `projects`, `setting_values`, `worker_assessments`, `worker_defects`, `worker_runs`
- **Writes:** `charter_values`, `delivery_step_tasks`, `inbox_items`, `graph_edges`, `graph_history`, `messages`, `setting_values`, `repos`, `agents`, `teams`
- **Gateway operations:** `team.history`

### `cto_op:team_write`

Run the owner operation team_write.

- **Reads:** `agent_loops`, `agents`, `ask_sorts`, `delivery_changes`, `delivery_evidence`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `sessions`, `project_bar_rules`, `repos`, `projects`, `setting_values`, `worker_assessments`, `worker_defects`, `worker_runs`
- **Writes:** `charter_values`, `delivery_step_tasks`, `inbox_items`, `graph_edges`, `graph_history`, `messages`, `setting_values`, `repos`, `agents`, `teams`
- **Gateway operations:** `team.write`

### `cto_op:timezone_get`

Read timezone get through the owner operation.

- **Reads:** none found
- **Writes:** `events`, `nodes`, `setting_values`
- **Gateway operations:** `http:handleTimeZoneSettings`

### `cto_op:timezone_set`

Run the owner operation timezone_set.

- **Reads:** none found
- **Writes:** `events`, `nodes`, `setting_values`
- **Gateway operations:** `http:handleTimeZoneSettings`

### `cto_op:usage`

Read usage through the owner operation.

- **Reads:** `nodes`
- **Writes:** `mcp_servers`
- **Gateway operations:** `mcp.report`, `query.usage`

### `cto_op:usage_top`

Read usage top through the owner operation.

- **Reads:** none found
- **Writes:** none found
- **Gateway operations:** `relay:query.usage_top`

### `cto_op:worker_defect`

Run the owner operation worker_defect.

- **Reads:** `agents`, `charter_values`, `delivery_changes`, `delivery_evidence`, `graph_edges`, `model_prices`, `setting_values`, `sessions`, `repos`, `gateway_state`, `teams`, `worker_assessments`, `worker_runs`
- **Writes:** `worker_defects`, `worker_ratings`, `worker_runs`
- **Gateway operations:** `rate.add`

### `cto_op:worker_floors_get`

Read worker floors get through the owner operation.

- **Reads:** none found
- **Writes:** `events`, `nodes`, `setting_values`
- **Gateway operations:** `http:handleWorkerFloors`

### `cto_op:worker_floors_reset`

Run the owner operation worker_floors_reset.

- **Reads:** `inbox_items`, `messages`, `model_failures`, `nodes` (plus the common reads)
- **Writes:** `events`, `setting_values`
- **Gateway operations:** `owner.worker_floors.reset`

### `cto_op:worker_floors_set`

Run the owner operation worker_floors_set.

- **Reads:** none found
- **Writes:** `events`, `nodes`, `setting_values`
- **Gateway operations:** `http:handleWorkerFloors`

### `cto_op:worker_log`

Read worker log through the owner operation.

- **Reads:** `messages`, `model_prices`, `nodes`, `sessions`, `worker_assessments`, `worker_defects`, `worker_runs`
- **Writes:** none found
- **Gateway operations:** `query.worker_log`

### `cto_op:worker_providers_get`

Read worker providers get through the owner operation.

- **Reads:** none found
- **Writes:** `events`, `nodes`, `setting_values`
- **Gateway operations:** `http:handleWorkerProviders`

### `cto_op:worker_rotation_get`

Read worker rotation get through the owner operation.

- **Reads:** `setting_values`
- **Writes:** `nodes`
- **Gateway operations:** `http:handleWorkerRotation`

### `cto_op:workers`

Read workers through the owner operation.

- **Reads:** `agents`, `messages`, `model_failures`, `node_scopes`, `nodes`, `sessions` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `query.agents`, `func:watchWorkers`

### `cto_ops_list`



- **Reads:** none found
- **Writes:** none found

### `cto_placement_memory`

Read or save placement answers explicitly given by the owner.

- **Reads:** none found
- **Writes:** none found

### `cto_proposals_find`

Find proposals, or all pending owner items, each with its answer call.

- **Reads:** `agents`, `delivery_changes`, `inbox_items`, `messages`, `model_failures`, `nodes`, `permission_proposals`, `sessions` (plus the common reads)
- **Writes:** `charter_drafts`
- **Gateway operations:** `asks.list`, `permission.get`, `charter.drafts`

### `cto_send`



- **Reads:** `inbox_items`, `messages`, `model_failures`, `nodes` (plus the common reads)
- **Writes:** `setting_values`, `sessions`
- **Gateway operations:** `cto.session.register`

### `cto_settings`



- **Reads:** none found
- **Writes:** `events`, `nodes`, `setting_values`
- **Gateway operations:** `http:handleCTOSettingsGet`, `http:handleCTOSettingsSet`

### `cto_standing_orders`

List, save, update, remove, search or read history of standing orders.

- **Reads:** `agent_loops`, `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_revisions`, `gateway_state`, `model_failures`, `model_prices`, `nodes`, `nudge_pauses`, `graph_outcomes`, `project_bar_rules`, `projects`, `standing_order_history`, `standing_orders_fts`, `inbox_items`, `worker_assessments`, `worker_defects`, `worker_runs`, `worker_starts`
- **Writes:** `agents`, `desk_log`, `ask_sorts`, `gateway_state`, `charter_drafts`, `messages`, `charter_values`, `delivery_retries`, `delivery_step_tasks`, `inbox_items`, `events`, `follow_ups`, `graph_edges`, `graph_history`, `graph_outcomes`, `learned_suggestions`, `node_scopes`, `nudge_fires`, `setting_values`, `permission_proposals`, `playbook`, `sessions`, `repos`, `setting_history`, `standing_orders`, `teams`
- **Gateway operations:** `standing.list`, `standing.save`, `standing.update`, `standing.remove`, `standing.search`, `standing.history`, `inbox.create`

### `cto_tool`



- **Reads:** none found
- **Writes:** none found

### `cto_ui_show`

Show the owner a TUI tab with an item selected, or a short notice. Fails with no TUI open when no local TUI is connected.

- **Reads:** none found
- **Writes:** none found

### `graph_change`

Change the graph or undo. Lifecycle removal (node_remove, agent_tombstone, repo_archive) is a rare op in cto_op and needs owner approval; other actions apply at once.

- **Reads:** `agent_loops`, `messages`, `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_retries`, `delivery_revisions`, `gateway_state`, `model_failures`, `model_prices`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `project_bar_rules`, `repos`, `projects`, `worker_assessments`, `worker_defects`, `worker_runs`, `worker_starts`
- **Writes:** `agents`, `ask_sorts`, `gateway_state`, `charter_values`, `delivery_step_tasks`, `inbox_items`, `desk_log`, `events`, `graph_edges`, `graph_history`, `messages`, `node_scopes`, `nudge_fires`, `setting_values`, `permission_proposals`, `repos`, `sessions`, `setting_history`, `teams`
- **Gateway operations:** `charter.set`, `node.add`, `node.remove`, `edge.set`, `edge.remove`, `node.settings.set`, `graph.undo`, `repo.create`, `repo.archive`, `agent.create`, `agent.tombstone`, `lead.set`, `team.write`, `scope.set`, `scope.remove`

### `mcp_call`

Call a tool on a shared MCP server at once. Credentials never appear in the call.

- **Reads:** `inbox_items`, `mcp_servers`, `messages`, `model_failures`, `nodes`, `sessions` (plus the common reads)
- **Writes:** `events`
- **Gateway operations:** `mcp.call`

### `mcp_change`

Manage shared MCP servers and connected apps. apps_list lists IDs; app_grant/ungrant take id and grant; app_rename takes id and name; app_revoke invalidates credentials. public_status/on/off control i

- **Reads:** `charter_drafts`, `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_revisions`, `inbox_items`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `quota_blocks`, `setting_history`, `worker_defects`, `worker_ratings`, `worker_runs` (plus the common reads)
- **Writes:** `agents`, `app_tokens`, `apps`, `desk_log`, `ask_sorts`, `messages`, `inbox_items`, `delivery_retries`, `events`, `follow_ups`, `graph_outcomes`, `learned_suggestions`, `mcp_servers`, `nudge_fires`, `oauth_attempts`, `oauth_pending`, `setting_values`, `permission_proposals`, `pick_candidates`, `playbook`, `sessions`, `repos`, `standing_orders`, `worker_assessments`, `worker_defects`, `worker_starts`
- **Gateway operations:** `mcp.add`, `mcp.remove`, `mcp.test`, `mcp.enable`, `apps.ask`, `inbox.create`, `apps.list`, `apps.grant`, `apps.ungrant`, `apps.rename`, `apps.revoke`, `apps.public.status`, `apps.public.on`, `apps.public.off`

### `project_change`

Add, remove, rename or create a project at once.

- **Reads:** `agent_loops`, `agents`, `ask_sorts`, `charter_values`, `model_failures`, `node_scopes`, `nodes`, `sessions`, `project_bar_rules`, `repos`, `setting_values`, `teams`, `worker_assessments`, `worker_runs`
- **Writes:** `events`, `graph_edges`, `inbox_items`, `messages`, `setting_values`, `permission_proposals`, `repos`, `projects`, `sessions`
- **Gateway operations:** `project.add`, `project.remove`, `project.display.set`, `project.display.get`, `session.proposal_start`, `session.proposal_clone`, `session.repo.reserve`, `session.agent.reserve`, `query.projects`

### `projects_layout`

Read-only project layout of online machines: roots, groups, remotes and newest commits.

- **Reads:** `nodes`
- **Writes:** none found
- **Gateway operations:** `query.projects_layout`

### `proposal_ask`

Open the approval dialog in the chat for an open proposal that is already waiting.

- **Reads:** `inbox_items`, `messages`, `model_failures`, `nodes`, `permission_proposals` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `permission.get`

### `session_change`

Change a session: rename, replace, trust, policy, start, stop, resume, restart, or merger start or stop.

- **Reads:** `agent_loops`, `messages`, `charter_values`, `deliveries`, `delivery_evidence`, `delivery_retries`, `delivery_revisions`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `project_bar_rules`, `repos`, `projects`, `agents`, `teams`, `worker_assessments`, `worker_defects`, `worker_runs`
- **Writes:** `agents`, `ask_sorts`, `gateway_state`, `delivery_changes`, `delivery_step_tasks`, `inbox_items`, `desk_log`, `events`, `graph_edges`, `graph_history`, `messages`, `nudge_fires`, `setting_values`, `permission_proposals`, `sessions`, `repos`, `setting_history`, `worker_runs`, `worker_starts`
- **Gateway operations:** `session.rename`, `session.owner_replace`, `session.trust`, `session.mcp_servers`, `session.policy`, `session.start`, `session.proposal_start`, `session.proposal_clone`, `session.resume`, `session.stop`, `session.get`, `session.remove`, `merger.set`, `session.agent.reserve`, `session.agent.status`, `session.start.failed`, `session.repo.reserve`, `query.projects`, `query.agent`

### `sessions_archive`

Archive offline sessions. Records can be restored by the owner.

- **Reads:** `ask_sorts`, `model_failures`, `node_scopes`, `nodes`, `sessions`, `repos` (plus the common reads)
- **Writes:** `agents`, `inbox_items`, `messages`, `setting_values`, `sessions`
- **Gateway operations:** `sessions.archive`, `sessions.archived`

### `settings_change`

Change settings values or undo a revision at once. Machine removal, token rotation, process stop and the owner operations are rare ops in cto_op.

- **Reads:** `agent_loops`, `charter_values`, `deliveries`, `delivery_evidence`, `model_failures`, `model_prices`, `nudge_pauses`, `oauth_pending`, `graph_outcomes`, `sessions`, `project_bar_rules`, `repos`, `projects`, `gateway_state`, `agents`, `teams`, `worker_assessments`, `worker_defects`, `worker_ratings`
- **Writes:** `agents`, `app_tokens`, `ask_sorts`, `gateway_state`, `charter_drafts`, `messages`, `delivery_changes`, `delivery_retries`, `delivery_revisions`, `delivery_step_tasks`, `inbox_items`, `desk_log`, `nodes`, `events`, `follow_ups`, `graph_edges`, `graph_history`, `graph_outcomes`, `learned_suggestions`, `node_scopes`, `nudge_fires`, `setting_values`, `permission_proposals`, `playbook`, `repos`, `schema_version`, `sessions`, `setting_history`, `standing_orders`, `worker_runs`, `worker_starts`
- **Gateway operations:** `settings.write`, `settings.undo`, `worker.profile.add`, `worker.profile.remove`, `node.rename`, `machine.remove`, `machine.rotate`, `owner.invite`, `onboarding.restart`, `playbook.set`, `session.stop_process`

### `skill_archive`

The skill archive: list (latest version and candidates), read a version (SKILL.md and the file list; file and offset read one page of a file; after pages the list), add from a machine folder or Git UR

- **Reads:** `nodes`, `projects`
- **Writes:** `skill_archive`, `skill_archive_blocks`, `skill_changes`
- **Gateway operations:** `skills.archive_list`, `skills.archive_read`, `skills.archive_add`, `skills.archive_add_git`, `skills.archive_remove`, `skills.archive_undo`, `skills.archive_intake`, `skills.archive_promote`

### `skills`

Skills on graph nodes: set the add, remove and pin lists of a global, team, repo or agent node with undo, read each node's effective list, or undo a set. The CTO sets skills when the owner asks; agent

- **Reads:** `agents`, `charter_values`, `sessions`, `projects`, `repos`, `skill_archive`, `teams`
- **Writes:** `skill_changes`, `skill_node_lists`
- **Gateway operations:** `skills.set`, `skills.effective`, `skills.undo`

### `worker_change`

Start or stop a worker with a complete brief.

- **Reads:** `agents`, `deliveries`, `delivery_changes`, `delivery_evidence`, `delivery_revisions`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `model_prices`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `quota_blocks`, `worker_ratings`, `worker_runs` (plus the common reads)
- **Writes:** `ask_sorts`, `messages`, `inbox_items`, `delivery_retries`, `desk_log`, `events`, `follow_ups`, `graph_outcomes`, `learned_suggestions`, `nudge_fires`, `setting_values`, `permission_proposals`, `pick_candidates`, `playbook`, `sessions`, `worker_assessments`, `worker_defects`, `worker_starts`
- **Gateway operations:** `worker.start`, `worker.precheck`, `worker.queued_start`, `worker.start_report`, `worker.override_ask`, `msg.send`, `worker.stop`

## Bot tools

### `agent_graph_read`

Read the graph: nodes with charters and rules, and their edges. Give node for one node and its edges; a shared name returns every node.

- **Reads:** none found (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `bot:agent_graph_read`

### `agent_machine_get`

Read a machine's health, load, CPU and busiest processes, or one detail category: processes, models, quotas, track record or playbook. Processes accepts sort rss (default) or cpu and reports stop refu

- **Reads:** `nodes`, `setting_values`, `quota_blocks`, `sessions`, `gateway_state`, `worker_runs`
- **Writes:** `playbook`
- **Gateway operations:** `bot:agent_machine_get`

### `agent_machines_find`

List machines with online state and versions. Use agent_machine_get for one machine's models, quotas, track record or playbook.

- **Reads:** `nodes`, `sessions`
- **Writes:** none found
- **Gateway operations:** `bot:agent_machines_find`

### `agent_message_get`

Read one message's full body, delivery state and outcome. Long bodies come in chunks: follow next_body_cursor until it is absent. Attachments are in files: pass file to read one as text, then follow n

- **Reads:** `messages`, `deliveries`, `delivery_retries`, `graph_outcomes`, `sessions`
- **Writes:** `messages`
- **Gateway operations:** `bot:agent_message_get`

### `agent_message_reply`

Reply to a message you received, with an outcome on requests: accept, reject (reason), redirect (reason, redirect_to), escalate (recommendation). ack: true closes a request with no answer; progress: t

- **Reads:** `agents`, `ask_sorts`, `deliveries`, `delivery_changes`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `worker_starts` (plus the common reads)
- **Writes:** `messages`, `delivery_retries`, `desk_log`, `events`, `follow_ups`, `graph_outcomes`, `inbox_items`, `learned_suggestions`, `nudge_fires`, `setting_values`, `permission_proposals`, `sessions`
- **Gateway operations:** `bot:agent_message_reply`

### `agent_message_send`

Send to one session, or as the CTO to_set for a group. to also takes the role address chief or cto. urgent jumps the queue and still counts. A peer message is a request, not the owner's approval. Lead

- **Reads:** `agents`, `ask_sorts`, `deliveries`, `delivery_changes`, `delivery_step_tasks`, `gateway_state`, `model_failures`, `node_scopes`, `nodes`, `nudge_pauses`, `graph_outcomes`, `sessions`, `repos`, `worker_starts` (plus the common reads)
- **Writes:** `messages`, `delivery_retries`, `desk_log`, `events`, `follow_ups`, `graph_outcomes`, `inbox_items`, `learned_suggestions`, `nudge_fires`, `setting_values`, `permission_proposals`, `sessions`
- **Gateway operations:** `bot:agent_message_send`

### `agent_messages_find`

Find messages by conversation, peer, direction or time. Rows hold a preview; use agent_message_get for the body.

- **Reads:** `messages`, `deliveries`, `delivery_retries`, `graph_outcomes`, `sessions`
- **Writes:** `messages`
- **Gateway operations:** `bot:agent_messages_find`

### `agent_projects_find`

Find registered projects and the machines and folders that hold them. Use agent_sessions_find for their sessions.

- **Reads:** `charter_values`, `graph_edges`, `nodes`, `repos`, `projects`, `sessions`, `setting_values`, `agents`, `teams`
- **Writes:** none found
- **Gateway operations:** `bot:agent_projects_find`

### `agent_session_get`

Read one session's profile: folder, branch, charter, rules, delegation, worker facts. Use agent_sessions_find when the ID or name is unknown.

- **Reads:** `agents`, `inbox_items`, `messages`, `model_failures`, `node_scopes`, `nodes`, `sessions` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `bot:agent_session_get`

### `agent_sessions_find`

Find sessions by machine, project, harness, model, kind, online state or text. Use agent_session_get for one.

- **Reads:** `agents`, `messages`, `model_failures`, `node_scopes`, `nodes`, `sessions` (plus the common reads)
- **Writes:** none found
- **Gateway operations:** `bot:agent_sessions_find`

### `agent_settings_read`

Read settings, values, sources and history by key, repo or named target. A target names level network, repo or agent; a name is looked up only in that kind.

- **Reads:** `agents`, `charter_values`, `graph_edges`, `repos`, `setting_history`, `setting_values`, `teams`, `worker_assessments`, `worker_runs`
- **Writes:** `repos`
- **Gateway operations:** `bot:agent_settings_read`
