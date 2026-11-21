# Gateway data

This page says what each table in the gateway database holds. For each table it gives who writes it, who reads it, where its rows show, and how long they are kept.

The tables are the ones the schema has after the reduction in [decision 0179](decisions/0179-gateway-schema-reduction.md). The SQLite full text index adds four storage tables behind `standing_orders_fts`. The page does not list them one by one.

## How to read an entry

- **Holds** says what one row is.
- **Written by** and **Read by** list Go files. Paths are relative to `internal/`. The lists come from a text scan of the production code. They leave out test files. A query built from parts of SQL can be missed. The list shows ten files in alphabetical order, then "and others" when there are more.
- **Shown in** names the TUI screen, the agent tool (`tool`), or the CTO operation (`CTO op`, an operation of the CTO tool) that shows or changes the rows.
- **Kept** says how long rows stay, and what removes them.

Rows are not counted here. Counts change with use.

## How this page stays current

A Go test in `internal/gateway` opens a fresh store and lists its tables. It fails when a table has no heading here, or when a heading names a table the schema no longer has. A migration that adds, renames or drops a table therefore fails the test until this page changes. The journeys below are checked the same way.

## Journeys

Each journey shows the tables that one common task touches, in the order the code touches them.

## Journey: Send a message and get a reply

A message is checked, stored, handed to the receiver's machine, confirmed as delivered, and later closed by the receiver's reply.

1. `sessions` (reads): The gateway checks that the sending session really lives on the machine that sent the request.
2. `messages` (reads): It looks for an earlier message with the same sender and idempotency key, so a retry never stores the message twice.
3. `sessions` (reads): It checks that the receiving session exists and follows it to the session that replaced it, if it was replaced.
4. `owner_settings` (reads): It reads which session is the CTO, because the CTO only accepts replies and requests from lead agents.
5. `graph_edges` (reads): For a request, it looks up the owner's rule between the two projects: normal, ask first, or never.
6. `messages` (reads): It counts the messages already in this conversation to enforce the conversation limit.
7. `messages` (writes): The new message is saved with state 'stored', its conversation, its expiry time and its sender and receiver.
8. `messages` (writes): If the owner's last prompt to the sender explains this first request, the message keeps that prompt. The sender's copy is cleared either way, so the prompt is used once.
9. `events` (writes): The audit log records 'msg.stored' with the type, receiver and size (never the text).
10. `messages` (writes): Just before sending to the receiver's machine, the message state moves from 'stored' to 'dispatched'.
11. `events` (writes): The audit log records 'msg.dispatched' for the receiver.
12. `deliveries` (writes): The receiver's daemon reports back, and the gateway keeps that report as one delivery row.
13. `messages` (writes): The message state moves to 'delivered' (it stays 'replied' or 'failed' if it already was).
14. `messages` (reads): When a message is delivered between two projects that have no link yet, the gateway reads the messages those projects exchanged in the last 7 days, as evidence that they talk.
15. `events` (writes): The audit log records 'msg.delivered' for the receiver.
16. `messages` (writes): The receiver's reply is saved as a new message in the same conversation, and in the same transaction the original request is marked 'replied'.
17. `delivery_retries` (writes): Any pending retry for the answered request is deleted, because it no longer needs delivering.
18. `inbox_items` (writes): An open 'owed answer' reminder for the request is closed as replied.

## Journey: Start a worker and get its hand-back

The picker reads the machine, quota, past results and prices to choose a model, the worker starts and runs, and its hand-back and run record reach the starter.

1. `projects` (reads): The gateway looks up the project folders registered on each machine, to find where the worker can run (project folders seen in sessions are read too).
2. `nodes` (reads): It reads the machine's state: online, accepts workers, which agent tools are installed, and each tool's models.
3. `quota_blocks` (reads): It reads quota windows that this machine already learned are used up, so the picker does not choose a provider that is blocked (live quota readings come from memory, not the database).
4. `owner_settings` (reads): It reads the owner's picker settings: registered worker profiles, model scores, minimum scores per difficulty, allowed and protected providers, floors, and the rotation counter.
5. `model_prices` (reads): It reads the price of each model, so the picker can compare the expected cost per accepted result.
6. `worker_runs` (reads): It reads the track record of past worker runs (complete, partial, failed, fixes needed) per model, which scores each model on tasks of this difficulty.
7. `worker_assessments` (reads): It reads the difficulty the judge gave to past tasks, to place each past run in the right difficulty bucket.
8. `worker_usage` (reads): It reads the tokens past runs used, to turn token counts into a cost per run.
9. `model_failures` (reads): It reads which models recently failed or lost sign-in on this machine, to skip models that are cooling down.
10. `owner_settings` (writes): The rotation counter is saved, so the next start spreads work across models instead of always picking the same one.
11. `worker_starts` (writes): The gateway records the start (reference, machine, who asked, project, harness, permissions, the 'done' list) before it asks the machine to launch.
12. `worker_assessments` (writes): The judge's difficulty result and the picker's explanation are saved under the new worker session.
13. `pick_candidates` (writes): One row per candidate model is saved: its score, cost, quota room, chance of being picked, and why it was skipped or chosen.
14. `events` (writes): The audit log records 'worker.started' with project, harness, model and who started it.
15. `delivery_changes` (writes): At a complete hand-back, the worker's branch is submitted as a change to deliver (a first revision row is saved with it).
16. `messages` (writes): The hand-back notice is saved as a reply to the starter's task message, and the task is marked 'replied'. It is delivered to the starter like any message.
17. `worker_runs` (writes): When the worker stops, its run is saved: model, outcome, seconds, files and lines changed, fixes, and struggle counters. This feeds the track record the picker reads next time.
18. `worker_usage` (writes): The tokens the worker used are saved with the run, so its cost can be computed later.

## Journey: A machine connects and reports health

A machine enrolls once with a code, then on each connection proves its token, says hello with its health and tools, syncs its sessions, and is marked silent if it drops without a reason.

1. `enroll_codes` (writes): On first setup, the one-time enrollment code is checked and marked used, so it cannot be used again.
2. `nodes` (writes): A new machine row is created with its name, operating system and the hash of its secret token (the token itself is never stored).
3. `events` (writes): The audit log records 'node.enrolled' with the machine's host name.
4. `nodes` (reads): On every connection, the gateway checks that the machine was not removed and that its token hash matches.
5. `nodes` (writes): The hello is stored on the machine row: host name, version, last seen time, whether it accepts workers, time zone, Tailscale address, and whether it supports graph references.
6. `nodes` (writes): The machine's health report (restart time, load and similar) replaces the previous one, with the time it arrived.
7. `nodes` (writes): The list of installed agent tools (Claude, Codex, and so on) with versions, models and sign-in state is saved. The same write is repeated later when the machine sends node.harnesses.
8. `events` (writes): If the tool list changed, the audit log records 'node.harnesses' with what changed.
9. `nodes` (writes): The machine is marked as back: any open outage (dropped, asleep, silent) is cleared.
10. `events` (writes): The audit log records 'node.connected' with the remote address.
11. `sessions` (writes): The machine lists its agent sessions. Each is added or updated, and sessions the gateway thought were online but are missing from the list are marked offline.
12. `inventory_reports` (writes): The machine's scan of skills, MCP servers and other tools is saved as its latest inventory (kept as stale if a scan fails).
13. `tool_locations` (writes): Each item from the inventory is saved as a location on this machine (tool_identities is updated with it).
14. `nodes` (writes): When the link drops, the last time a frame arrived is saved, and the time of the drop starts a possible outage (unless the machine announced it was going to sleep).
15. `events` (writes): The audit log records 'node.disconnected'.
16. `sessions` (writes): After the offline grace period, every session of that machine is marked offline, and ended workers are marked 'ended'.
17. `nodes` (writes): If the outage stays unexplained for the silence limit, the machine is marked silent. The once-a-minute check does this.
18. `cto_intake` (writes): A silent machine is queued for the CTO with the text '<host> went silent ... it may be hung' (it is not sent to the owner).

## Journey: A change from accept to release

An agent hands in a change, the delivery steps open one after another, each step is claimed and reported with evidence, and the change ends as done.

1. `sessions` (reads): The gateway checks that the session handing in the change really lives on the machine that is calling.
2. `setting_values` (reads): It reads the delivery rules for this repo (which tests, who approves the merge, review settings) to build the pipeline of steps.
3. `graph_edges` (reads): It reads the org graph links that name the owner of each step (accepts, reviews, tests, approves, merges, releases, verifies). A step with no link is owned by the caller.
4. `delivery_changes` (writes): A new change row is stored with the branch, the head commit, the caller and the state open. A second submit on the same branch updates this row instead.
5. `delivery_revisions` (writes): The head commit is recorded as revision 1. A new head later adds revision 2, 3 and so on, and starts the steps again.
6. `delivery_step_tasks` (writes): The first ready step (accept) gets a task row that names its owner. Steps that need an earlier step wait.
7. `messages` (writes): A notice 'you have a step to claim' is stored for the session that owns the step.
8. `delivery_step_messages` (writes): The notice is linked to the step task, so it can be cancelled when the step is claimed, finished or replaced.
9. `desk_log` (writes): A line 'Sent step ... of change ... to a session' is added to the Desk log.
10. `delivery_step_tasks` (writes): The owner session claims the step (agent_step_claim). The task now shows who works on it, and nobody else can report it.
11. `delivery_step_tasks` (writes): When the step is reported (agent_step_report), the task is marked completed under the same session.
12. `delivery_evidence` (writes): The result (pass, fail or needs owner), the commit, the fields and who did the work are stored as evidence. Evidence is never edited, a newer report just wins.
13. `delivery_step_tasks` (writes): The gateway reads all evidence, finds which steps are now ready, and opens the next task (review, test, approve, merge, release, verify in turn).
14. `project_mergers` (reads): For a step owned by the merger, the gateway looks up which session is the merger for this repo on this machine, and only that session may claim the step.
15. `delivery_step_delegates` (writes): For an approve step set to delegated, the repo lead and the team lead (found from the repo and team tables) are listed as the sessions allowed to approve. The caller is never its own approver.
16. `delivery_step_inbox` (writes): For an approve step that needs the owner's written approval, an Inbox item is created and linked to the step, revision and commit.
17. `delivery_changes` (writes): A passing merge report moves the change to merged and stores the merged commit. When every step after merge has passed, the state becomes done (a close report makes it stopped).
18. `inbox_items` (writes): When the change is done or stopped, the open asks about it are closed so nothing stale stays on the Desk.

## Journey: The chief of staff sorts an ask

An agent asks the owner a question, it becomes an Inbox item that the chief of staff sorts, the Desk shows it, the owner answers, and the answer goes back to the asker.

1. `sessions` (reads): The gateway checks the asking session is on the calling machine and finds its repo and display name.
2. `owner_settings` (reads): It reads which session is the chief of staff, then decides if the ask goes to the chief or straight to the owner.
3. `inbox_items` (reads): It looks for an open ask, or one closed in the last 10 minutes, that asks the same thing, so a repeat joins it instead of making a duplicate.
4. `inbox_items` (writes): A new open ask is stored with the question, the options, the recommendation and who handles it next (the chief).
5. `inbox_items` (reads): Every few seconds the gateway collects the asks waiting for the chief and bundles those that arrived close together.
6. `messages` (writes): One message listing the asks to sort is stored for the chief of staff session.
7. `inbox_items` (writes): Each ask in the batch is marked as sent to the chief, so it is not sent twice.
8. `standing_orders` (reads): The chief searches the owner's earlier decisions and rules (agent_owner_judgments_find) to see if the owner already decided this kind of question.
9. `inbox_items` (reads): When the chief calls agent_ask_sort, the gateway reads the ask and checks that the caller is the live chief.
10. `inbox_items` (writes): For the result 'owner', the ask gets its priority, the reason why only the owner can decide, and next_who set to the owner.
11. `ask_sorts` (writes): The sort is recorded with who sorted, the result, the reason and the previous state, so it can be shown and undone.
12. `desk_log` (writes): A line 'Chief sent ask to you (priority N): ...' is added to the Desk log.
13. `inbox_items` (reads): The Desk reads the asks that are open and waiting for the owner.
14. `desk_log` (reads): The Desk also reads the latest log lines, newest first.
15. `inbox_items` (writes): The owner answers in the TUI. The ask is marked answered (or approved or refused) with the answer text and time.
16. `standing_orders` (writes): The question and the owner's answer are saved as an owner judgment, so the chief can find it the next time (step 8).
17. `messages` (writes): The answer is stored as a message for the asking session, and forevery session that joined the ask as a repeat. The gateway then delivers it.

## Journey: The owner changes a charter or a setting

A charter attribute or a setting is written with a new revision and a history row, and readers pick it up from the store or from a push to every daemon.

1. `repos` (reads): For a repo charter, the gateway checks the repo exists and is not archived (for an agent charter it checks the agents table the same way).
2. `charter_values` (reads): It reads the current value of this attribute, so an unchanged value writes nothing.
3. `charter_values` (writes): The new value is stored for the level (global, team, repo or agent), the scope and the attribute, with time and author. An empty value deletes the row.
4. `graph_history` (writes): A history row keeps the old value, the new value, the author and the time. Its id is the revision that graph undo and charter history use.
5. `charter_values` (reads): After the commit every daemon is told to drop its cached charter. The next reader resolves the charter from global, team, repo and agent values, in that order.
6. `settings_target_identity` (reads): For a CTO settings change that names a repo, team or agent by name, the gateway turns the name into an id and reads the lasting token of that target (it creates the token on first use).
7. `setting_revision` (reads): The setting write starts by reading the current settings revision number.
8. `setting_values` (reads): It reads all current values. The write is refused if the value the caller expected is no longer the stored one.
9. `setting_revision` (writes): The settings revision goes up by one. One write, even with many changes, is one revision.
10. `setting_values` (writes): The new value is stored for its key, level and scope, with the new revision number. A reset deletes the row.
11. `setting_history` (writes): One history row per changed value keeps the revision, old and new value, author, reason and cause. Undo of a revision reads these rows back.
12. `setting_values` (reads): After the commit the gateway rebuilds the settings snapshot and pushes it to every connected daemon. Daemons and the TUI read the new snapshot and its revision.

## Tables

## Machines

Computers that run the daemon, and how they enrol and report health.

### enroll_codes

- **Holds:** One row per one-time invite code (stored hashed) that lets a new machine join, with its expiry time and the time it was used.
- **Written by:** `gateway/store.go`
- **Read by:** `gateway/store.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Forever; an expired or used code stays but is refused.

### inventory_reports

- **Holds:** One row per machine holding the latest report of the tools, skills and MCP servers installed there.
- **Written by:** `gateway/inventory.go`
- **Read by:** `gateway/inventory.go`, `gateway/skillarchive_intake.go`, `gateway/tools_assignment.go`
- **Shown in:** TUI, Tools; CTO op `tools_items`; CTO op `tools_versions`; CTO op `tools_import`; CTO op `tools_export`
- **Kept:** Replaced on each report.

### nodes

- **Holds:** One row per machine enrolled in the network: its name, system, hashed access token, agent-net version, last-seen time, which agent programs it can run, plus its health report, Tailscale address, time zone and quiet-hours marks.
- **Written by:** `gateway/graph_refs.go`, `gateway/machine_tokens.go`, `gateway/node_fold_migration.go`, `gateway/nodes.go`, `gateway/nodes_sleep.go`, `gateway/store.go`
- **Read by:** `gateway/apps_public.go`, `gateway/ask_intake.go`, `gateway/delivery_changes.go`, `gateway/graph_refs.go`, `gateway/inventory.go`, `gateway/lead_route.go`, `gateway/machine_tokens.go`, `gateway/mcp_import.go`, `gateway/mcp_store.go`, `gateway/nodes.go` and others
- **Shown in:** TUI, Sessions; TUI, Quotas; tool `agent_machines_find`; tool `agent_machine_get`; CTO op `machine_remove`; CTO op `machine_rotate`
- **Kept:** Forever; a removed machine is only marked removed and its token wiped.

### transfer_parts

- **Holds:** One row per received piece of a file transfer: its index, size, checksum and arrival time.
- **Written by:** `gateway/transfer.go`
- **Read by:** `gateway/transfer.go`
- **Shown in:** tool `agent_file_send`
- **Kept:** For 7 days after upload, deleted with its transfer by the hourly sweep.

### transfers

- **Holds:** One row per file sent between machines: sender, receiver, file name, size, checksum, part size and number of parts, with an expiry time and a finished time.
- **Written by:** `gateway/transfer.go`
- **Read by:** `gateway/transfer.go`
- **Shown in:** tool `agent_file_send`
- **Kept:** For 7 days after upload, swept hourly together with the stored file parts.

## Sessions

Agent sessions, and the checks and wake-ups that go with them.

### check_answer_counts

- **Holds:** How many answers the idle check got per day, by kind (done, more, blocked).
- **Written by:** `gateway/check.go`
- **Read by:** `gateway/check.go`
- **Shown in:** CTO op `counts`
- **Kept:** Forever.

### profiles

- **Holds:** One row per session with what it announced about itself: name, function, project, capabilities, current task, expertise and when it started the task.
- **Written by:** `gateway/cto_session.go`, `gateway/merger.go`, `gateway/session_replace.go`, `gateway/sessionrename.go`, `gateway/store.go`, `gateway/store_record.go`
- **Read by:** `gateway/ask_intake.go`, `gateway/asks.go`, `gateway/calibration.go`, `gateway/caller_agent.go`, `gateway/check.go`, `gateway/chief.go`, `gateway/cited.go`, `gateway/context_resolver.go`, `gateway/delivery_changes.go`, `gateway/delivery_tasks.go` and others
- **Shown in:** TUI, Sessions; tool `agent_announce`; tool `agent_sessions_find`; tool `agent_session_get`
- **Kept:** Deleted with its session (cleanup or the 30 day prune of un-archived offline sessions).

### repo_start_reservations

- **Holds:** One row per short hold on a repo slot while a session is being started, so two launches cannot pass the repo's session cap together.
- **Written by:** `gateway/session_max.go`, `gateway/store.go`
- **Read by:** `gateway/session_max.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** For 2 minutes; removed on the next start request or when the session registers.

### session_agent_start_failures

- **Holds:** One row per failed Agent choice for a new session: the machine, reference, project, the Desk item raised and the reason.
- **Written by:** `gateway/session_start_agent_expiry.go`
- **Read by:** `gateway/session_start_agent.go`, `gateway/session_start_agent_expiry.go`
- **Shown in:** TUI, Desk
- **Kept:** Forever; no code deletes it.

### session_agent_starts

- **Holds:** One row per new session waiting to be bound to an Agent of the org graph, chosen by the owner before the session first registers.
- **Written by:** `gateway/session_start_agent.go`, `gateway/session_start_agent_expiry.go`
- **Read by:** `gateway/org_tree.go`, `gateway/session_start_agent.go`, `gateway/session_start_agent_expiry.go`
- **Shown in:** TUI, Sessions
- **Kept:** For 2 minutes; deleted when the session registers or the choice expires.

### session_checks

- **Holds:** One row per session holding the state of the idle check loop: whether a check is active or waiting, open work and message counts, the day's check count and the last announce time.
- **Written by:** `gateway/check.go`
- **Read by:** `gateway/check.go`, `gateway/notice_lifecycle.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Forever; never deleted, even after the session is removed.

### session_dues

- **Holds:** One row per due step a session set with agent_due: the session, what is due, the due time, and whether it is pending, woken or raised to the chief.
- **Written by:** `gateway/session_due.go`
- **Read by:** `gateway/session_due.go` (the minute tick)
- **Shown in:** tool `agent_due`; the wake message (idempotency key `due:<id>`); a chief ask after 15 minutes idle
- **Kept:** Forever; the state moves from pending to woken or raised.

### session_wakes

- **Holds:** One row per session counting automatic wake-ups: the day's count, the no-progress streak, open work, and when and why the last wake failed.
- **Written by:** `gateway/check.go`, `gateway/loop.go`, `gateway/session_wake.go`, `gateway/side_fold.go`, `gateway/wake_failure.go`
- **Read by:** `gateway/caller_agent.go`, `gateway/check.go`, `gateway/loop.go`, `gateway/session_wake.go`, `gateway/wake_failure.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Forever; never deleted, even after the session is removed.

### sessions

- **Holds:** One row per agent session (a coding agent running on a machine, a worker, the CTO or the chief): name, folder, repo, status, online state, model, worker data and archive and pin marks. It also holds the owner's last typed prompt to the session (`owner_prompt`, with its time `owner_prompt_at`).
- **Written by:** `gateway/apps.go`, `gateway/bot_agent.go`, `gateway/bot_names.go`, `gateway/bot_session.go`, `gateway/busy_hold.go`, `gateway/chief.go`, `gateway/cto_session.go`, `gateway/machine_tokens.go`, `gateway/merger.go`, `gateway/org_tree.go` and others
- **Read by:** `gateway/ask_holder.go`, `gateway/ask_intake.go`, `gateway/asks.go`, `gateway/asks_to.go`, `gateway/authority.go`, `gateway/automatic_message.go`, `gateway/bot_names.go`, `gateway/bot_session.go`, `gateway/bot_tools.go`, `gateway/busy_hold.go` and others
- **Shown in:** TUI, Sessions; TUI, Graph; tool `agent_sessions_find`; tool `agent_session_get`; CTO op `session_change`; CTO op `sessions_archive`
- **Kept:** Offline sessions are archived after 7 days and kept; an un-archived offline session unused for 30 days is deleted by the hourly prune; cleaned workers are deleted on request. The owner prompt is cleared at the session's first send, and after 10 minutes at most (hourly prune).

## Messages

Messages between sessions, their delivery records, and automatic nudges.

### deliveries

- **Holds:** One row per delivery state change of a message (stored, delivered, failed and so on) with detail text and time.
- **Written by:** `gateway/store.go`, `gateway/store_record.go`
- **Read by:** `gateway/store.go`, `gateway/undelivered.go`
- **Shown in:** TUI, Messages; tool `agent_message_get`
- **Kept:** Forever by default (--keep-messages is 0); when set, deleted with their messages by the hourly prune.

### delivery_retries

- **Holds:** One row per message that is waiting to be delivered again, with the number of attempts so far and the time of the next try.
- **Written by:** `gateway/place.go`, `gateway/store.go`, `gateway/withdraw.go`
- **Read by:** `gateway/retry.go`, `gateway/server.go`, `gateway/session_stall.go`, `gateway/store.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Until the message is delivered, answered, expired or withdrawn.

### events

- **Holds:** One row per gateway event in the audit log: time, kind, machine, session, message and a detail line.
- **Written by:** `gateway/store.go`, `gateway/store_record.go`
- **Read by:** `gateway/graph_talk.go`, `gateway/store.go`, `gateway/store_record.go`
- **Shown in:** TUI, Messages; TUI, Graph
- **Kept:** For 90 days, pruned hourly.

### messages

- **Holds:** One row per message between sessions, the owner and bots: sender, recipient, type, body, thread, state, expiry, urgency, hold and handoff data and attached file ids. A request that carries the owner's prompt keeps that prompt (`owner_prompt`).
- **Written by:** `gateway/agentnet_reply.go`, `gateway/automatic_message.go`, `gateway/bot_tools.go`, `gateway/busy_hold.go`, `gateway/caller_agent.go`, `gateway/check.go`, `gateway/chief.go`, `gateway/cto_lead.go`, `gateway/delivery_reconcile.go`, `gateway/delivery_tasks.go` and others
- **Read by:** `gateway/ask_holder.go`, `gateway/asks.go`, `gateway/brief_check.go`, `gateway/busy_hold.go`, `gateway/check.go`, `gateway/cto_lead.go`, `gateway/delivery_tasks.go`, `gateway/follow_ups.go`, `gateway/graph_talk.go`, `gateway/held.go` and others
- **Shown in:** TUI, Messages; TUI, Desk; tool `agent_message_send`; tool `agent_message_reply`; tool `agent_messages_find`; tool `agent_message_get`; tool `agent_message_withdraw`; tool `agent_message_handoff`
- **Kept:** Forever by default (the gateway flag --keep-messages is 0); when set, older rows go on the hourly prune except bot mail not yet read and rows behind open reject or escalate outcomes. The owner prompt on a message is cleared after 90 days (hourly prune).

### nudge_fires

- **Holds:** One row per automatic nudge sent to an agent: its kind, recipient, team and message, when it fired and whether and how work followed within 30 minutes.
- **Written by:** `gateway/nudge_fires.go`
- **Read by:** `gateway/nudge_fires.go`
- **Shown in:** tool `agent_nudge_stats`
- **Kept:** Forever; no code deletes it.

### nudge_pauses

- **Holds:** One row per pause of automatic nudges for a team or an agent: kind, until when, reason, who paused and ended it, and how to undo it.
- **Written by:** `gateway/nudge_pause.go`
- **Read by:** `gateway/nudge_pause.go`
- **Shown in:** tool `agent_nudge_pause`; tool `agent_nudge_resume`; tool `agent_nudge_pauses_find`
- **Kept:** Forever; ended pauses are kept on purpose.

## Apps and OAuth

Connected apps, and the OAuth sign-in they use.

### app_refresh_budget

- **Holds:** One row per refresh-token family counting how many times it rotated in the current 24 hour window.
- **Written by:** `gateway/apps.go`
- **Read by:** `gateway/apps.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Until no token of the family is left; swept every 10 minutes.

### app_tokens

- **Holds:** One row per hashed access or refresh token of a connected app, with its type, expiry, token family and the time it was used.
- **Written by:** `gateway/apps.go`, `gateway/oauth_store.go`, `gateway/phone_auth.go`
- **Read by:** `gateway/apps.go`, `gateway/oauth_store.go`, `gateway/phone_auth.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Access tokens 1 hour, refresh tokens 90 days, used refresh hashes 90 days; swept every 10 minutes; at most 20 access tokens per app.

### apps

- **Holds:** One row per connected app (phone, bot or other client): name, kind, OAuth client, redirect addresses, when created and last used, when revoked, and what it is granted.
- **Written by:** `gateway/apps.go`, `gateway/oauth_store.go`, `gateway/phone_tailnet.go`
- **Read by:** `gateway/apps.go`, `gateway/bot_agent.go`, `gateway/bot_mcp_grant.go`, `gateway/bot_names.go`, `gateway/bot_session.go`, `gateway/oauth_store.go`, `gateway/phone_auth.go`, `gateway/phone_pairing.go`, `gateway/phone_tailnet.go`, `gateway/store.go` and others
- **Shown in:** TUI, Settings; CTO op `apps_list`; CTO op `app_grant`; CTO op `app_ungrant`; CTO op `app_rename`; CTO op `app_revoke`
- **Kept:** Forever; a revoked app is only marked revoked.

### oauth_attempts

- **Holds:** One row per recent failed pairing-code guess or client registration, used to rate-limit the public sign-in endpoints.
- **Written by:** `gateway/oauth_store.go`, `gateway/plan_kinds_migration.go`
- **Read by:** `gateway/oauth_store.go`, `gateway/server.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Registrations 1 hour, wrong codes 10 minutes.

### oauth_clients

- **Holds:** One row per OAuth client an app registered (or a phone pairing created): client name, redirect addresses, grant types and where it came from.
- **Written by:** `gateway/oauth_store.go`, `gateway/phone_pairing.go`
- **Read by:** `gateway/oauth_store.go`
- **Shown in:** TUI, Settings
- **Kept:** For 1 hour if no connected app uses it; kept while an app does; swept every 10 minutes.

### oauth_codes

- **Holds:** One row per hashed one-time authorization code issued after the owner approves a sign-in, with its expiry and use time.
- **Written by:** `gateway/oauth_store.go`
- **Read by:** `gateway/oauth_store.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Unused codes 2 minutes; used codes 90 days; swept every 10 minutes.

### oauth_pending

- **Holds:** One row per sign-in waiting for the owner to approve (device code or browser): user code, client, requested resource, status and expiry.
- **Written by:** `gateway/oauth_store.go`, `gateway/phone_pairing.go`
- **Read by:** `gateway/apps_asks.go`, `gateway/oauth_store.go`, `gateway/phone_pairing.go`
- **Shown in:** TUI, Desk; TUI, Settings
- **Kept:** For 10 minutes, swept every 10 minutes and when a new request starts.

## Settings

Owner settings and their history.

### gateway_state

- **Holds:** A key-value table of what the gateway remembers about itself: graph cutover version and gate, skill archive marks, brief last-sent dates and one-time seed markers.
- **Written by:** `gateway/gateway_state.go`
- **Read by:** `gateway/gateway_state.go`, `gateway/store.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Until overwritten; keys are not deleted.

### owner_settings

- **Holds:** A key-value table of owner-level settings and role markers: chief and CTO session ids, CTO title and settings, time zone, worker profiles, providers, floors and rotation, hidden items, integrations, brief and public listener state.
- **Written by:** `gateway/apps_public.go`, `gateway/brief.go`, `gateway/chief.go`, `gateway/chief_health.go`, `gateway/chief_stuck.go`, `gateway/cto_session.go`, `gateway/cto_settings.go`, `gateway/gateway_state.go`, `gateway/hidden.go`, `gateway/integrations.go` and others
- **Read by:** `gateway/apps_public.go`, `gateway/ask_intake.go`, `gateway/check.go`, `gateway/chief.go`, `gateway/chief_health.go`, `gateway/chief_stuck.go`, `gateway/cto_lead.go`, `gateway/cto_session.go`, `gateway/cto_settings.go`, `gateway/gateway_state.go` and others
- **Shown in:** TUI, Settings; TUI, Quotas; tool `agent_settings_read`; tool `agent_worker_profiles_find`; tool `agent_protected_providers_get`; CTO op `chief_set`
- **Kept:** Until changed; a key is replaced on write and a few keys are deleted when a role is cleared.

### schema_version

- **Holds:** One row per applied database migration number.
- **Written by:** `gateway/store.go` (the migration step)
- **Read by:** `gateway/store.go`, `gateway/store_pool.go`, `gateway/store_upgrade_backup.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Forever; one row added per migration.

### setting_history

- **Holds:** One row per settings change: revision, key, level, scope, old and new value, who changed it, why and what it undoes.
- **Written by:** `gateway/delivery_pipeline_settings.go`, `gateway/delivery_settings.go`, `gateway/settings.go`, `gateway/worker_sandbox_settings.go`
- **Read by:** `gateway/settings.go`, `gateway/store_graph.go`
- **Shown in:** TUI, Settings; tool `agent_settings_read`; CTO op `settings_change`
- **Kept:** Forever; no code deletes it.

### setting_revision

- **Holds:** A single row with the current settings revision number, raised on every settings change.
- **Written by:** `gateway/learned_apply.go`, `gateway/merger.go`, `gateway/settings.go`
- **Read by:** `gateway/delivery_pipeline_settings.go`, `gateway/delivery_settings.go`, `gateway/learned_apply.go`, `gateway/learned_owner.go`, `gateway/settings.go`, `gateway/store_graph.go`, `gateway/worker_sandbox_settings.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** One row, updated in place.

### setting_values

- **Holds:** One row per stored setting value, keyed by setting name, level (network, repo, agent and so on), scope and item, with the revision that wrote it.
- **Written by:** `gateway/delivery_pipeline_settings.go`, `gateway/delivery_settings.go`, `gateway/gateway_state.go`, `gateway/settings.go`, `gateway/store_graph.go`, `gateway/worker_sandbox_settings.go`
- **Read by:** `gateway/cto_graph.go`, `gateway/delivery_pipeline_settings.go`, `gateway/delivery_settings.go`, `gateway/leads_max.go`, `gateway/loop.go`, `gateway/merger.go`, `gateway/session_max.go`, `gateway/settings.go`, `gateway/store.go`, `gateway/store_graph.go` and others
- **Shown in:** TUI, Settings; tool `agent_settings_read`; CTO op `settings_change`
- **Kept:** Until the owner changes or unsets it.

### settings_target_identity

- **Holds:** One row per network, repo or agent with a stable token, so a setting stays attached to it after a rename.
- **Written by:** `gateway/settings_targets.go`
- **Read by:** `gateway/settings_targets.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Forever; no code deletes it.

## Org graph

Projects, agents, teams, repos, and the rules and charters between them.

### agent_loops

- **Holds:** One row per org-graph node that has a standing loop: when it started, the last tick, the day's tick count, the no-progress streak and whether the loop is paused or waiting.
- **Written by:** `gateway/loop.go`
- **Read by:** `gateway/loop.go`
- **Shown in:** TUI, Graph
- **Kept:** Until the node's loop setting is removed.

### agents

- **Holds:** One agent of the org tree: a named role (coding agent, bot, lead) with the session that currently leads for it, and a tombstone flag when it is retired.
- **Written by:** `gateway/bot_agent.go`, `gateway/bot_names.go`, `gateway/graph_lifecycle.go`, `gateway/lead_session.go`, `gateway/lead_session_migration.go`, `gateway/leads_max.go`, `gateway/org_tree.go`, `gateway/repo_fold_migration.go`, `gateway/session_replace.go`, `gateway/store.go` and others
- **Read by:** `gateway/agent_scopes.go`, `gateway/asks_to.go`, `gateway/authority.go`, `gateway/bot_agent.go`, `gateway/caller_agent.go`, `gateway/charter_drafts.go`, `gateway/cto_charter_default.go`, `gateway/cto_lead.go`, `gateway/friction.go`, `gateway/graph_lifecycle.go` and others
- **Shown in:** TUI, Graph; tool `agent_graph_read`; CTO op `agent_create`; CTO op `agent_tombstone`; CTO op `lead_set`
- **Kept:** Forever (a retired agent is only flagged as tombstoned).

### charter_draft_asks

- **Holds:** A marker that a session was already asked to draft a charter for one level and scope, with the hash of the source text, so it is not asked again.
- **Written by:** `gateway/charter_drafts.go`
- **Read by:** `gateway/charter_drafts.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Replaced when the source text changes (one row per level and scope).

### charter_drafts

- **Holds:** One charter value that a session drafted from the project AGENTS.md and that waits for the owner to accept or reject it.
- **Written by:** `gateway/charter_drafts.go`
- **Read by:** `gateway/charter_drafts.go`, `gateway/chief.go`, `gateway/store_inbox.go`
- **Shown in:** TUI, Graph; TUI, Desk; tool `agent_charter_draft`; CTO op `needs_you`
- **Kept:** Forever (the status changes to accepted or rejected, the row stays).

### charter_values

- **Holds:** One charter setting (a rule or text) for a level (global, team, agent or repo), a scope and an attribute, with who set it and when.
- **Written by:** `gateway/charter_levels.go`, `gateway/charter_store.go`, `gateway/graph_cutover_auto.go`, `gateway/store_graph.go`
- **Read by:** `gateway/agent_charter.go`, `gateway/charter_levels.go`, `gateway/charter_resolver.go`, `gateway/charter_store.go`, `gateway/chief.go`, `gateway/cto_graph.go`, `gateway/graph_refs.go`, `gateway/graph_view.go`, `gateway/learned.go`, `gateway/learned_apply.go` and others
- **Shown in:** TUI, Graph; tool `agent_graph_read`; tool `agent_charter_draft`; CTO op `charter_set`; CTO op `charter_history`
- **Kept:** Until the value is cleared, undone, or its repo is removed.

### graph_edges

- **Holds:** One typed link between two nodes of the org tree (for example reports-to, may-ask, merger or delivery owner), with its mode and rule.
- **Written by:** `gateway/edge_kinds.go`, `gateway/held.go`, `gateway/merger.go`, `gateway/merger_restore.go`, `gateway/org_tree.go`, `gateway/settings.go`, `gateway/store_graph.go`
- **Read by:** `gateway/cto_graph.go`, `gateway/graph_lifecycle.go`, `gateway/graph_refs.go`, `gateway/graph_view.go`, `gateway/graphread.go`, `gateway/held.go`, `gateway/learned.go`, `gateway/learned_apply.go`, `gateway/learned_owner.go`, `gateway/merger.go` and others
- **Shown in:** TUI, Graph; tool `agent_graph_read`; CTO op `edge_set`; CTO op `edge_remove`; CTO op `merger_set`
- **Kept:** Until the link is removed or an undo reverts it.

### graph_history

- **Holds:** One recorded change to the org tree (charter value, link, lifecycle or team change) with old and new value, who made it and when. Undo and the history views read it.
- **Written by:** `gateway/edge_kinds.go`, `gateway/graph_cutover_auto.go`, `gateway/graph_lifecycle.go`, `gateway/leads_max.go`, `gateway/store_graph.go`, `gateway/team_history_migration.go`
- **Read by:** `gateway/charter_drafts.go`, `gateway/cto_graph.go`, `gateway/graph_refs.go`, `gateway/graph_view.go`, `gateway/learned_apply.go`, `gateway/learned_owner.go`, `gateway/store_graph.go`, `gateway/team_history_migration.go`, `gateway/teams.go`
- **Shown in:** TUI, Graph; CTO op `graph_undo`; CTO op `charter_history`; CTO op `edge_history`; CTO op `team_history`
- **Kept:** Forever.

### graph_outcomes

- **Holds:** How one request between two nodes ended: a reply with an outcome (accept, reject, escalate or redirect), the reason and recommendation, and whether the owner resolved it.
- **Written by:** `gateway/asks.go`, `gateway/store.go`, `gateway/store_inbox.go`, `gateway/store_outcome.go`
- **Read by:** `gateway/asks.go`, `gateway/bot_tools.go`, `gateway/learned.go`, `gateway/owner_answers.go`, `gateway/signal.go`, `gateway/store.go`, `gateway/store_outcome.go`, `gateway/store_record.go`
- **Shown in:** TUI, Desk; tool `agent_message_reply`; CTO op `outcome_inbox`; CTO op `outcome_overrule`
- **Kept:** Forever (messages are kept forever by default, so their outcomes are too).

### member_blocked_notices

- **Holds:** A fingerprint of the last 'blocked' or 'waiting' announce that was told to a team lead, one per session, so the lead is not told twice.
- **Written by:** `gateway/member_blocked.go`, `gateway/member_loop.go`, `gateway/side_fold.go`
- **Read by:** `gateway/member_blocked.go`, `gateway/member_loop.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Removed when the session announces something that is not blocked.

### node_scopes

- **Holds:** One row per permission the owner gave to a role in the org graph: a kind of action over a target, with a note, who set it and when. A session in that role may then act without asking the owner.
- **Written by:** `gateway/node_scopes.go`
- **Read by:** `gateway/agent_scopes.go`, `gateway/node_scopes.go`, `gateway/org_tree.go`
- **Shown in:** TUI, Graph; tool `agent_graph_read`; CTO op `scope_set`; CTO op `scope_remove`
- **Kept:** Until the owner removes the scope (changes are logged in node_scopes_history).

### outcome_overrules

- **Holds:** A mark that the owner overruled the outcome of one request, pointing at the request and the reply.
- **Written by:** `gateway/store_outcome.go`
- **Read by:** `gateway/store.go`
- **Shown in:** TUI, Desk; CTO op `outcome_overrule`
- **Kept:** Forever.

### project_display_names

- **Holds:** A friendly name the owner gave to a project, used instead of the folder name.
- **Written by:** `gateway/project_display.go`
- **Read by:** `gateway/project_display.go`, `gateway/store.go`
- **Shown in:** TUI, Graph; TUI, Sessions; CTO op `project_display_set`
- **Kept:** Until the owner clears the name.

### projects

- **Holds:** One project folder registered on one machine, with its path, name, git remote and default branch.
- **Written by:** `gateway/store_projects.go`
- **Read by:** `gateway/delivery_changes.go`, `gateway/graph_lifecycle.go`, `gateway/graph_view.go`, `gateway/inventory.go`, `gateway/learned.go`, `gateway/mcp_import_apply.go`, `gateway/project_display.go`, `gateway/settings.go`, `gateway/skill_lists.go`, `gateway/skillarchive_intake.go` and others
- **Shown in:** TUI, Tools; TUI, Graph; tool `agent_projects_find`; tool `agent_project_add`; CTO op `project_add`
- **Kept:** Until the project is removed.

### repos

- **Holds:** One repo (project) node of the org tree, with its display name, owner agent, lead agent and flags for archived and held back.
- **Written by:** `gateway/authority.go`, `gateway/graph_lifecycle.go`, `gateway/leads_max.go`, `gateway/repo_fold_migration.go`, `gateway/store.go`, `gateway/store_graph.go`
- **Read by:** `gateway/authority.go`, `gateway/charter_levels.go`, `gateway/graph_lifecycle.go`, `gateway/graph_refs.go`, `gateway/intake.go`, `gateway/lead_session.go`, `gateway/lead_session_migration.go`, `gateway/leads_max.go`, `gateway/node_scopes.go`, `gateway/org_read.go` and others
- **Shown in:** TUI, Graph; tool `agent_graph_read`; CTO op `repo_create`; CTO op `repo_archive`; CTO op `lead_set`
- **Kept:** Forever (an archived repo is only flagged).

### team_members

- **Holds:** One line saying that an agent or repo belongs to a team (each member is in one team).
- **Written by:** `gateway/bot_agent.go`, `gateway/bot_names.go`, `gateway/team_history_migration.go`, `gateway/teams.go`
- **Read by:** `gateway/asks_to.go`, `gateway/authority.go`, `gateway/bot_agent.go`, `gateway/bot_names.go`, `gateway/charter_drafts.go`, `gateway/charter_resolver.go`, `gateway/desk_rules.go`, `gateway/lead_route.go`, `gateway/member_loop.go`, `gateway/node_scopes.go` and others
- **Shown in:** TUI, Graph; CTO op `team_member_add`; CTO op `team_member_remove`; tool `agent_graph_read`
- **Kept:** Until the member is removed from the team, or the bot is revoked.

### teams

- **Holds:** One team: a name, purpose, owner, authority mode, held-back text, where asks go and whether the lead merges.
- **Written by:** `gateway/leads_max.go`, `gateway/store_graph.go`, `gateway/teams.go`
- **Read by:** `gateway/asks_to.go`, `gateway/authority.go`, `gateway/charter_drafts.go`, `gateway/charter_levels.go`, `gateway/charter_resolver.go`, `gateway/cto_lead.go`, `gateway/desk_rules.go`, `gateway/lead_route.go`, `gateway/lead_session.go`, `gateway/lead_session_migration.go` and others
- **Shown in:** TUI, Graph; CTO op `team_create`; CTO op `team_settings_set`; CTO op `team_history`; tool `agent_graph_read`
- **Kept:** Forever (an archived team is only marked inactive).

## Desk

What the owner sees on the Desk: asks, sorts, follow-ups and the log.

### ask_sort_hide_receipts

- **Holds:** An undo receipt for one batch hide of asks on the Desk: the ids hidden and whether it was undone.
- **Written by:** `gateway/chief_hide.go`
- **Read by:** `gateway/chief_hide.go`
- **Shown in:** TUI, Desk; tool `agent_ask_sort`
- **Kept:** Forever.

### ask_sorts

- **Holds:** One sort decision the chief of staff made on an ask (answer, route, merge, close, hide), with what it replaced so it can be undone.
- **Written by:** `gateway/chief.go`, `gateway/chief_hide.go`
- **Read by:** `gateway/asks.go`, `gateway/chief.go`, `gateway/chief_health.go`, `gateway/chief_hide.go`, `gateway/chief_stuck.go`, `gateway/standing.go`
- **Shown in:** TUI, Desk; tool `agent_ask_sort`; CTO op `chief_sorts`; CTO op `ask_history`; CTO op `ask_undo`
- **Kept:** Forever.

### cto_intake

- **Holds:** One problem fed to the CTO, deduplicated by kind and subject, with severity, count, first and last seen and state.
- **Written by:** `gateway/intake.go`
- **Read by:** `gateway/intake.go`
- **Shown in:** CTO op `cto_intake_find`
- **Kept:** Forever (a closed entry keeps its close time and reason).

### delivery_step_inbox

- **Holds:** One row per Desk item raised to get the owner's approval of a delivery step, linking the step to the Desk item.
- **Written by:** `gateway/delivery_tasks.go`
- **Read by:** `gateway/asks.go`, `gateway/delivery_approval.go`, `gateway/delivery_changes.go`, `gateway/delivery_reconcile.go`, `gateway/delivery_tasks.go`, `gateway/desk_flush.go`, `gateway/desk_rules.go`, `gateway/store.go`, `gateway/store_inbox.go`
- **Shown in:** TUI, Desk (Needs you)
- **Kept:** Forever.

### desk_log

- **Holds:** One log line about something that happened on the Desk (an ask sorted, a permission applied), with what undoes it.
- **Written by:** `gateway/asks.go`, `gateway/permissions.go`, `gateway/store.go`
- **Read by:** `gateway/asks.go`, `gateway/permissions.go`, `gateway/server.go`, `gateway/store.go`
- **Shown in:** TUI, Desk; CTO op `desk_log`; CTO op `desk_settle`
- **Kept:** The newest 2000 lines, trimmed on every 200th insert.

### follow_ups

- **Holds:** One row per follow-up check set by a message sender or the chief: the event expected (reply, change, conversation, session), who waits, the due time and whether it is pending, met or raised.
- **Written by:** `gateway/follow_ups.go`
- **Read by:** `gateway/follow_ups.go`
- **Shown in:** tool `agent_message_send`; tool `agent_ask_sort`; TUI, Desk
- **Kept:** Forever; status moves from pending to met or raised, rows are not deleted.

### inbox_items

- **Holds:** One ask: a question or request that waits for the owner (or the chief of staff), with its state, answer, priority and who sorted or routed it.
- **Written by:** `gateway/agentnet_reply.go`, `gateway/apps_public_asks.go`, `gateway/asks.go`, `gateway/asks_to.go`, `gateway/brief.go`, `gateway/check.go`, `gateway/chief.go`, `gateway/chief_health.go`, `gateway/chief_stuck.go`, `gateway/delivery_approval.go` and others
- **Read by:** `gateway/agentnet_reply.go`, `gateway/apps_asks.go`, `gateway/apps_public_asks.go`, `gateway/ask_intake.go`, `gateway/asks.go`, `gateway/brief.go`, `gateway/caller_agent.go`, `gateway/check.go`, `gateway/chief.go`, `gateway/chief_health.go` and others
- **Shown in:** TUI, Desk; tool `agent_owner_ask`; tool `agent_ask_intake`; tool `agent_ask_sort`; CTO op `inbox_list`; CTO op `inbox_answer`
- **Kept:** Forever (answered asks stay).

### permission_proposals

- **Holds:** One permission change an agent proposed (rules, targets, reason) with its status, who decided and the result.
- **Written by:** `gateway/permissions.go`
- **Read by:** `gateway/permissions.go`
- **Shown in:** TUI, Desk; tool `agent_permission_propose`; CTO op `cto_proposals_find`; CTO op `proposal_ask`
- **Kept:** Forever.

## Learning

Standing orders, notes, findings, and what the gateway learns from signals.

### findings

- **Holds:** One finding reported about a model or a harness. Enough findings promote a playbook note.
- **Written by:** `gateway/learn.go`, `gateway/store_record.go`
- **Read by:** `gateway/learn.go`
- **Shown in:** tool `agent_learn`; CTO op `agent_learn`
- **Kept:** Forever; a finding never promoted is marked stale after 90 days.

### friction

- **Holds:** One friction report: a line from a worker or agent saying what slowed it down, with project, area, model and change. It also holds the read status of the report (new, read, triaged, fixed, wontfix, infra) with who set it, when, and the link and note of that mark. Only the latest mark is kept.
- **Written by:** `gateway/friction.go`
- **Read by:** `gateway/friction.go`
- **Shown in:** TUI, Quotas; tool `agent_friction_mark`; CTO op `friction_find`; CTO op `friction_mark`
- **Kept:** Forever.

### learned_suggestions

- **Holds:** One suggestion the system learned from message traffic: a change to a charter field or to a link, with its evidence and status (new, accepted, rejected). For an applied change, `learned_seen` says whether the owner has seen it.
- **Written by:** `gateway/learned.go`, `gateway/learned_apply.go`, `gateway/learned_owner.go`, `gateway/side_fold.go`, `gateway/store_graph.go`
- **Read by:** `gateway/asks.go`, `gateway/chief.go`, `gateway/learned.go`, `gateway/learned_apply.go`, `gateway/learned_owner.go`, `gateway/store_inbox.go`
- **Shown in:** TUI, Desk; TUI, Graph; CTO op `learned_list`; CTO op `learned_answer`; CTO op `learned_view`
- **Kept:** Forever (status changes, rows stay).

### playbook

- **Holds:** One note about a model or a harness (written by the owner or promoted from findings), shown to workers that start with that model.
- **Written by:** `gateway/learn.go`, `gateway/track.go`
- **Read by:** `gateway/learn.go`, `gateway/track.go`
- **Shown in:** tool `agent_machine_get`; tool `agent_worker_start`; CTO op `playbook_get`; CTO op `playbook_set`; TUI, Graph
- **Kept:** Until the owner clears the note; automatic notes are flagged stale after 60 days.

### standing_order_history

- **Holds:** The old text of a standing order or note, saved each time it is changed or deleted, by a database trigger.
- **Written by:** `gateway/notes.go`, `gateway/store.go`
- **Read by:** `gateway/standing.go`
- **Shown in:** CTO op `cto_standing_orders`
- **Kept:** Forever.

### standing_orders

- **Holds:** One lasting text kept by the gateway: an owner standing order or rule, an owner judgment (answer or correction), or a named note written by an agent, with its tags and use count. A judgment can be voided (`voided_at`, `void_reason`): the row stays, with the reason, and no judgment search, list or brief shows it.
- **Written by:** `gateway/chief.go`, `gateway/notes.go`, `gateway/notes_copy.go`, `gateway/owner_answers.go`, `gateway/standing.go`, `gateway/store.go`
- **Read by:** `gateway/brief.go`, `gateway/chief.go`, `gateway/desk_rules.go`, `gateway/node_scopes.go`, `gateway/notes.go`, `gateway/notes_copy.go`, `gateway/owner_answers.go`, `gateway/standing.go`, `gateway/store.go`
- **Shown in:** TUI, Settings; tool `agent_note_write`; tool `agent_notes_find`; tool `agent_owner_judgment_record`; tool `agent_owner_judgments_find`; CTO op `cto_standing_orders`
- **Kept:** Until the owner or the note writer removes it.

### standing_orders_fts

- **Holds:** The searchable full text index (FTS5) of standing orders: the text and tags of each row, so notes and judgments can be searched by word.
- **Written by:** Triggers on `standing_orders` keep it in step. Also `gateway/notes.go` and `gateway/store.go`.
- **Read by:** `gateway/standing.go`
- **Shown in:** tool `agent_owner_judgments_find`; CTO op `cto_standing_orders`
- **Kept:** Kept in step with standing_orders by triggers (insert, update, delete).

## Delivery

Changes, steps and merger work, from accept to release.

### caller_step_escalations

- **Holds:** One row per agent owner whose waiting delivery steps were escalated: when its offline session was woken and whether its lead was offered the steps.
- **Written by:** `gateway/caller_agent.go`
- **Read by:** `gateway/caller_agent.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Removed once the owner is live again or has nothing waiting, checked on each sweep.

### delivery_changes

- **Holds:** One row per change (a branch of work) that a worker or session has submitted for delivery, with its project, branch, title, current state and merged commit.
- **Written by:** `gateway/delivery_changes.go`, `gateway/session_replace.go`
- **Read by:** `gateway/asks.go`, `gateway/brief_check.go`, `gateway/caller_agent.go`, `gateway/delivery_approval.go`, `gateway/delivery_changes.go`, `gateway/delivery_reconcile.go`, `gateway/delivery_tasks.go`, `gateway/desk_rules.go`, `gateway/follow_ups.go`, `gateway/graph_lifecycle.go` and others
- **Shown in:** TUI, Desk (Delivery and Change panels); tool `agent_changes_find`; tool `agent_change_submit`
- **Kept:** Forever.

### delivery_evidence

- **Holds:** One row per report on a delivery step (accept, review, test, merge, release or verify) of a change: the result, who reported it, which model, and a note.
- **Written by:** `gateway/delivery_changes.go`
- **Read by:** `gateway/delivery_changes.go`, `gateway/delivery_tasks.go`, `gateway/delivery_waiting.go`, `gateway/follow_ups.go`, `gateway/member_loop.go`, `gateway/nudge_fires.go`, `gateway/picker.go`, `gateway/scores_weekly.go`
- **Shown in:** TUI, Desk (Evidence panel); tool `agent_step_report`; tool `agent_changes_find`
- **Kept:** Forever, the database refuses to update or delete these rows.

### delivery_revisions

- **Holds:** One row per revision of a change (each time the branch is pushed again), with its commit, the size of the diff, the risk signals and the hand-back check result.
- **Written by:** `gateway/delivery_changes.go`, `gateway/side_tables_fold.go`
- **Read by:** `gateway/brief_check.go`, `gateway/delivery_changes.go`, `gateway/delivery_tasks.go`, `gateway/side_tables_fold.go`
- **Shown in:** TUI, Desk (Change panel); tool `agent_changes_find`; tool `agent_change_submit`
- **Kept:** Forever.

### delivery_step_delegates

- **Holds:** One row per machine that a delivery step was handed to, when a rule delegates that step instead of asking the owner.
- **Written by:** `gateway/delivery_tasks.go`
- **Read by:** `gateway/delivery_changes.go`, `gateway/delivery_tasks.go`
- **Shown in:** TUI, Desk (Steps and evidence panel)
- **Kept:** Until the step task is completed and queued again, then removed.

### delivery_step_messages

- **Holds:** One row per notice message sent to a session telling it to do a delivery step, linking the step to that message.
- **Written by:** `gateway/delivery_tasks.go`, `gateway/merger.go`, `gateway/session_replace.go`
- **Read by:** `gateway/delivery_tasks.go`, `gateway/delivery_waiting.go`, `gateway/merger.go`, `gateway/session_replace.go`, `gateway/store_graph.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Until the step is completed, re-queued or its merger task is reset; moved when a session is replaced.

### delivery_step_tasks

- **Holds:** One row per delivery step of a change that needs someone to do it: who owns it, who claimed it, whether it is done, and when it was last nudged or is waiting.
- **Written by:** `gateway/delivery_approval.go`, `gateway/delivery_changes.go`, `gateway/delivery_tasks.go`, `gateway/delivery_waiting.go`, `gateway/merger.go`, `gateway/session_replace.go`, `gateway/store_graph.go`
- **Read by:** `gateway/caller_agent.go`, `gateway/delivery_approval.go`, `gateway/delivery_changes.go`, `gateway/delivery_tasks.go`, `gateway/delivery_waiting.go`, `gateway/graph_lifecycle.go`, `gateway/merger.go`, `gateway/nudge_batch.go`, `gateway/owed_replies.go`, `gateway/session_stall.go` and others
- **Shown in:** tool `agent_step_claim`; tool `agent_step_report`; TUI, Desk (Steps and evidence panel)
- **Kept:** A finished task is removed when its step is queued again; otherwise kept.

### merge_train_fails

- **Holds:** One row per project counting how many merge-queue test runs failed in a row on the train tip.
- **Written by:** `gateway/intake_feeds.go`
- **Read by:** `gateway/intake_feeds.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Forever, one row per project, overwritten.

### project_bar_next

- **Holds:** One row per project holding the number the next bar rule will get, so numbers are never reused.
- **Written by:** `gateway/store_bar.go`
- **Read by:** `gateway/store_bar.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Forever, one row per project.

### project_bar_rules

- **Holds:** One row per numbered rule in a project's bar: the short quality rules that workers and reviewers check diffs against.
- **Written by:** `gateway/store_bar.go`
- **Read by:** `gateway/store_bar.go`
- **Shown in:** TUI, Graph (Project rules card); tool `agent_bar_edit`; CTO op `bar_edit`
- **Kept:** Until the rule is removed with a bar edit.

### project_mergers

- **Holds:** Which session is the merger of a project on a machine.
- **Written by:** `gateway/merger.go`, `gateway/merger_restore.go`, `gateway/session_replace.go`
- **Read by:** `gateway/caller_agent.go`, `gateway/cto_session.go`, `gateway/delivery_changes.go`, `gateway/delivery_tasks.go`, `gateway/graph_lifecycle.go`, `gateway/lead_session.go`, `gateway/lead_session_migration.go`, `gateway/leads_max.go`, `gateway/node_scopes.go`, `gateway/org_read.go` and others
- **Shown in:** TUI, Desk; TUI, Graph; CTO op `merger_set`; CTO op `merger_get`; CTO op `merger_restore`
- **Kept:** Until the merger is stopped or replaced.

## Workers and picker

Worker runs, their quality and cost, and the data the picker reads.

### model_failures

- **Holds:** One row per machine, harness and model holding when it last failed a worker call, when it last had an auth failure and when it last succeeded.
- **Written by:** `gateway/side_fold.go`, `gateway/store_record.go`
- **Read by:** `gateway/store_record.go`
- **Shown in:** tool `agent_worker_start`
- **Kept:** One row per machine and model, overwritten; only failures of the last 24 hours and successes of the last 7 days are read.

### model_prices

- **Holds:** The last good copy of the public model price list (prompt, completion and cache prices), used to cost worker runs.
- **Written by:** `gateway/prices.go`
- **Read by:** `gateway/prices.go`
- **Shown in:** TUI, Quotas (Workers log, cost column)
- **Kept:** Replaced whole on each price refresh.

### pick_candidates

- **Holds:** One row per candidate (worker, model and effort) of every automatic worker pick, with the prices, scores and weights the picker read and why it was chosen or left out.
- **Written by:** `gateway/worker_assessment.go`
- **Read by:** No reader in the gateway. `tools/pickerreplay` and `research/picker-ml` read the table from the database file.
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Forever.

### project_policies

- **Holds:** The delegation policy the owner set for a project on a machine: how its sessions handle code changes (do it themselves, a subagent or a worker) and which models to use.
- **Written by:** `gateway/store_policy.go`
- **Read by:** `gateway/store_graph.go`, `gateway/store_policy.go`, `gateway/worker_sandbox_settings.go`
- **Shown in:** TUI, Sessions; CTO op `session_policy`; tool `agent_settings_read`
- **Kept:** Until the owner clears the policy.

### quota_blocks

- **Holds:** One row per machine, provider and quota window that a worker run reported as used up, with the time it resets.
- **Written by:** `gateway/store_record.go`
- **Read by:** `gateway/store_record.go`
- **Shown in:** TUI, Quotas (Subscriptions)
- **Kept:** Rows are not deleted; one row per window, overwritten, and ignored after its reset time.

### quota_budgets

- **Holds:** One row per provider and window length holding the token budget learned from the last time that window ran out.
- **Written by:** `gateway/quota_burn.go`
- **Read by:** `gateway/quota_burn.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** One row per provider and window length, replaced by the newest failure.

### start_override_facts

- **Holds:** One row per Desk ask about a model override on a worker start, with the harness, model, effort and family facts that the ask is about.
- **Written by:** `gateway/start_override.go`
- **Read by:** `gateway/start_override.go`
- **Shown in:** TUI, Desk (Needs you)
- **Kept:** Forever.

### table_versions

- **Holds:** One row per watched table holding a change counter that database triggers raise; today only worker_runs.
- **Written by:** Triggers on `worker_runs`. No Go code writes it.
- **Read by:** `gateway/runs_snapshot.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** One row, updated in place by triggers.

### worker_assessments

- **Holds:** One row per worker start holding the difficulty judge's verdict on the task: scores, difficulty level, which judge model ran, and the picker's note.
- **Written by:** `gateway/worker_assessment.go`
- **Read by:** `gateway/model_health.go`, `gateway/picker.go`, `gateway/store.go`, `gateway/worker_assessment.go`, `gateway/worker_log.go`
- **Shown in:** TUI, Quotas (Workers log, Difficulty details); tool `agent_workers_find`
- **Kept:** Forever.

### worker_brief_defects

- **Holds:** One row per defect that was blamed on the brief the caller wrote rather than on the worker, with severity and note.
- **Written by:** `gateway/brief_check.go`, `gateway/ratings.go`
- **Read by:** `gateway/ratings.go`, `gateway/scores_weekly.go`, `gateway/worker_log.go`
- **Shown in:** TUI, Quotas (Workers log); tool `agent_worker_rate`
- **Kept:** Forever.

### worker_brief_rejections

- **Holds:** One row per time the brief check refused a worker brief from a caller, counting the failures for that task.
- **Written by:** `gateway/brief_check.go`
- **Read by:** `gateway/brief_check.go`
- **Shown in:** tool `agent_worker_start`
- **Kept:** Forever.

### worker_defects

- **Holds:** One row per defect found in a worker's result, from a rating or filed automatically (failed verify, failed test, fix of a shipped change), with severity, cause and the change it came from.
- **Written by:** `gateway/defect_auto.go`, `gateway/ratings.go`, `gateway/side_fold.go`
- **Read by:** `gateway/defect_auto.go`, `gateway/picker.go`, `gateway/quality_families.go`, `gateway/ratings.go`, `gateway/scores_weekly.go`
- **Shown in:** tool `agent_worker_rate`; TUI, Quotas (Quality · worker picker)
- **Kept:** Forever.

### worker_rate_offers

- **Holds:** One row per finished worker for which the caller was offered a chance to rate it, with whether the ask was actually made.
- **Written by:** `gateway/ratings.go`, `gateway/session_replace.go`
- **Read by:** `gateway/ratings.go`
- **Shown in:** tool `agent_worker_rate`
- **Kept:** Forever.

### worker_ratings

- **Holds:** One row per rating a caller gave to a worker's result, with a score and note.
- **Written by:** `gateway/ratings.go`
- **Read by:** `gateway/quality.go`, `gateway/quality_families.go`, `gateway/ratings.go`
- **Shown in:** tool `agent_worker_rate`; TUI, Quotas (Quality · worker picker)
- **Kept:** Forever.

### worker_runs

- **Holds:** One row per worker run: which machine, harness and model, how it ended, how long it ran, how much rework it needed and how hard it struggled.
- **Written by:** `gateway/rework_backfill.go`, `gateway/session_replace.go`, `gateway/store_record.go`, `gateway/task_rework_backfill.go`, `gateway/track.go`, `gateway/worker_cause.go`
- **Read by:** `gateway/context_resolver.go`, `gateway/delivery_tasks.go`, `gateway/intake_feeds.go`, `gateway/loop.go`, `gateway/member_loop.go`, `gateway/model_health.go`, `gateway/quota_burn.go`, `gateway/ratings.go`, `gateway/rework_backfill.go`, `gateway/runs_snapshot.go` and others
- **Shown in:** TUI, Quotas (Workers log); tool `agent_workers_find`
- **Kept:** Forever by default; the gateway flag keep-worker-runs can prune runs older than a set time.

### worker_starts

- **Holds:** One row per request to start a worker, with the machine, project, harness, session, whether it is open or closed, and the brief's done list. Two columns serve the picker: `brief_hash` (a hash of the project and the problem and task text) and `not_attempted_at` (when a run of the start ended without attempting the task).
- **Written by:** `gateway/brief_launch.go`, `gateway/session_replace.go`, `gateway/store.go`, `gateway/worker_starts.go`
- **Read by:** `gateway/brief_launch.go`, `gateway/context_resolver.go`, `gateway/graph_lifecycle.go`, `gateway/track.go`, `gateway/worker_starts.go`
- **Shown in:** tool `agent_worker_start`; tool `agent_workers_find`
- **Kept:** Closed starts are deleted after 30 days, when the next start is recorded.

### worker_usage

- **Holds:** One row per worker session holding its token usage, used to work out what the run cost.
- **Written by:** `gateway/prices.go`
- **Read by:** `gateway/prices.go`
- **Shown in:** TUI, Quotas (Workers log)
- **Kept:** Replaced on each usage report, never deleted.

### worker_visual_proof

- **Holds:** One row per worker run saying whether the worker gave visual proof (a screenshot or similar) or the check was unavailable.
- **Written by:** `gateway/track.go`
- **Read by:** `gateway/scores_weekly.go`
- **Shown in:** TUI, Quotas (Quality · scores over time)
- **Kept:** Forever.

## Tools and skills

Tools, shared MCP servers, skills, and their versions.

### mcp_imports

- **Holds:** One row per MCP server imported from a machine's own config, keeping the original record.
- **Written by:** `gateway/mcp_import_apply.go`, `gateway/mcp_scope.go`
- **Read by:** `gateway/mcp_import.go`
- **Shown in:** CTO op `mcp_import`; CTO op `mcp_import_candidates`
- **Kept:** Until the tool is forgotten.

### mcp_servers

- **Holds:** One row per shared MCP server: its name, the machine that runs it, its spec, whether it is on, who added it, and its last health and tool list.
- **Written by:** `gateway/mcp_import_apply.go`, `gateway/mcp_store.go`
- **Read by:** `gateway/inventory.go`, `gateway/mcp_import.go`, `gateway/mcp_import_apply.go`, `gateway/mcp_scope.go`, `gateway/mcp_store.go`
- **Shown in:** tool `agent_mcp_servers_find`; tool `agent_mcp_test`; TUI, Tools; CTO op `mcp_change`
- **Kept:** Until the server is removed.

### plans

- **Holds:** One row per prepared plan before it is applied: a tool plan, an MCP import plan or a native MCP removal plan (told apart by kind), with the snapshot it was built on and whether it was applied.
- **Written by:** `gateway/mcp_import_apply.go`, `gateway/mcp_native_remove.go`, `gateway/mcp_scope.go`, `gateway/plan_kinds_migration.go`, `gateway/tools_assignment.go`, `gateway/tools_group.go`
- **Read by:** `gateway/inventory.go`, `gateway/mcp_import_apply.go`, `gateway/mcp_native_remove.go`, `gateway/tools_assignment.go`, `gateway/tools_feed.go`, `gateway/tools_group.go`, `gateway/tools_plan.go`
- **Shown in:** CTO op `tools_change`; CTO op `mcp_import`
- **Kept:** Forever, except tool plans of a tool that is forgotten.

### skill_archive

- **Holds:** One row per stored version of every skill, with its full text, hash, source, who added it and whether it was removed.
- **Written by:** `gateway/skill_archive_migration.go`, `gateway/skillarchive.go`
- **Read by:** `gateway/skill_archive_migration.go`, `gateway/skill_lists.go`, `gateway/skillarchive.go`, `gateway/skillarchive_intake.go`, `gateway/skillarchive_read.go`
- **Shown in:** CTO op `skill_archive`
- **Kept:** Forever; a removal only marks it, and undoing an add deletes the row.

### skill_archive_blocks

- **Holds:** One row per skill name (and optionally hash) that the owner blocked, so it is not taken into the archive again.
- **Written by:** `gateway/skill_archive_migration.go`, `gateway/skillarchive.go`
- **Read by:** `gateway/skillarchive.go`
- **Shown in:** CTO op `skill_archive`
- **Kept:** Until the same skill is added again or a removal is undone.

### skill_archive_changes

- **Holds:** One row per change made to the skill archive (add, remove, promote), kept so the change can be undone.
- **Written by:** `gateway/skillarchive.go`
- **Read by:** `gateway/skill_archive_migration.go`, `gateway/skillarchive.go`
- **Shown in:** CTO op `skill_archive`
- **Kept:** Forever.

### skill_archive_latest

- **Holds:** One row per skill naming the version that is in use now.
- **Written by:** `gateway/skill_archive_migration.go`, `gateway/skillarchive.go`
- **Read by:** `gateway/skillarchive.go`
- **Shown in:** CTO op `skill_archive`
- **Kept:** One row per skill, replaced when the version in use changes.

### skill_list_changes

- **Holds:** One row per change to the skill lists (what was added, removed or pinned at a level), kept so the change can be undone.
- **Written by:** `gateway/skill_lists.go`
- **Read by:** `gateway/skill_lists.go`
- **Shown in:** CTO op `skills`
- **Kept:** Forever.

### skill_node_lists

- **Holds:** One row per skill that is added, removed or pinned at a level and scope (global, project or agent).
- **Written by:** `gateway/skill_lists.go`
- **Read by:** `gateway/skill_lists.go`
- **Shown in:** CTO op `skills`
- **Kept:** The rows of a level and scope are replaced as a whole on each set or undo.

### skill_versions

- **Holds:** One row per version of a skill imported into the gateway's own copy store, with its hash, git commit, source machine and version number.
- **Written by:** `gateway/skillstore.go`
- **Read by:** `gateway/inventory.go`, `gateway/skillarchive.go`, `gateway/skillbackup.go`, `gateway/skillstore.go`, `gateway/tools_assignment.go`, `gateway/tools_plan.go`, `gateway/tools_toggle.go`, `gateway/tools_update.go`
- **Shown in:** CTO op `tools_versions`; CTO op `tools_export`; CTO op `tools_import`
- **Kept:** Forever.

### tool_accepted

- **Holds:** One row per tool finding (a difference between machines) that the owner chose to accept as it is.
- **Written by:** `gateway/tools_accepted.go`
- **Read by:** `gateway/tools_accepted.go`
- **Shown in:** CTO op `tools_accepted`; CTO op `tools_accept`; TUI, Tools
- **Kept:** Until the owner takes the acceptance back.

### tool_audit

- **Holds:** One row per tool plan or apply, including refused ones: who did it, which item, the revision, the targets and the result.
- **Written by:** `gateway/tools_assignment.go`
- **Read by:** `gateway/tools_assignment.go`
- **Shown in:** CTO op `tools_history`
- **Kept:** Forever.

### tool_identities

- **Holds:** One row per distinct skill, MCP server or other tool by kind, name and content hash.
- **Written by:** `gateway/inventory.go`, `gateway/mcp_scope.go`, `gateway/tools_folder.go`
- **Read by:** `gateway/inventory.go`, `gateway/mcp_import_apply.go`, `gateway/mcp_scope.go`, `gateway/skillarchive.go`, `gateway/skillarchive_intake.go`, `gateway/skillstore.go`, `gateway/tools_assignment.go`, `gateway/tools_choose.go`, `gateway/tools_feed.go`, `gateway/tools_plan.go` and others
- **Shown in:** TUI, Tools; CTO op `tools_items`; CTO op `tools_item`
- **Kept:** Removed when no machine location, skill version or revision refers to it any more.

### tool_locations

- **Holds:** One row per tool a machine reported in its inventory, with the folder or file where it was seen.
- **Written by:** `gateway/inventory.go`
- **Read by:** `gateway/inventory.go`, `gateway/mcp_import.go`, `gateway/skillarchive.go`, `gateway/skillarchive_intake.go`, `gateway/skillstore.go`, `gateway/tools_assignment.go`, `gateway/tools_choose.go`, `gateway/tools_toggle.go`, `gateway/tools_update.go`
- **Shown in:** TUI, Tools; CTO op `tools_items`; CTO op `inventory_get`
- **Kept:** Replaced on each inventory report of the machine.

### tool_project_seen

- **Holds:** One row per tool and project pair that the gateway has already seen, so only new projects get the 'new projects' choice.
- **Written by:** `gateway/tools_toggle.go`
- **Read by:** `gateway/tools_toggle.go`
- **Shown in:** Not shown on a screen or in a tool. The gateway uses it itself.
- **Kept:** Forever.

### tool_project_settings

- **Holds:** One row per tool holding the owner's choice for new projects: whether the tool is turned on or off there.
- **Written by:** `gateway/tools_toggle.go`
- **Read by:** `gateway/tools_toggle.go`
- **Shown in:** TUI, Tools; CTO op `tools_change`
- **Kept:** Forever, one row per tool, overwritten.

### tool_results

- **Holds:** One row per target of a tool rollout holding whether it worked, was refused or failed, with the branch and commit it produced.
- **Written by:** `gateway/mcp_scope.go`, `gateway/tools_feed.go`
- **Read by:** `gateway/inventory.go`, `gateway/tools_assignment.go`, `gateway/tools_feed.go`
- **Shown in:** TUI, Tools; CTO op `tools_items`
- **Kept:** Until the tool is forgotten (an MCP item removed).

### tool_revisions

- **Holds:** One row per assignment revision of a tool: which projects or machines it is turned on for, who set it and when.
- **Written by:** `gateway/mcp_scope.go`, `gateway/tools_assignment.go`
- **Read by:** `gateway/inventory.go`, `gateway/mcp_scope.go`, `gateway/tools_assignment.go`, `gateway/tools_feed.go`, `gateway/tools_group.go`, `gateway/tools_toggle.go`
- **Shown in:** TUI, Tools; CTO op `tools_change`; CTO op `tools_history`
- **Kept:** Until the tool is forgotten (an MCP item removed).

