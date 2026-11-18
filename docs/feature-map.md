# agent-net feature map

This file is generated from the code. Do not edit it by hand. Run `go generate ./internal/featuremap` to rewrite it; a test fails while it is out of date.

Use these names for screens, panels, keys and tools. The owner may say "the Friction panel on the Quotas tab"; this file says where that is.

## Tabs

| key | tab | action |
|---|---|---|
| `1` | Sessions | `sessions tab` |
| `2` | Graph | `graph tab` |
| `3` | Quotas | `quotas tab` |
| `4` | Messages | `messages tab` |
| `5` | Tools | `tools tab` |
| `6` | Settings | `settings tab` |
| `7` | Desk | `inbox tab` |

The key `c` opens the CTO session from any tab. The CTO is not a tab.

## 1. Sessions

Key `1` opens it.

### Panels

- `Machines`
- `Selected session` (in the expanded view)

### Keys

#### Sessions

| keys | what it does |
|---|---|
| pgup/pgdn | page |
| home/g, end | first, last row |
| enter | open the selected session; details show trust, request rule and delegation policy |
| m | send a message to the session as yourself |
| n | start a session in herdr: project, harness, model, folder and first message (a form) |
| x | stop or remove the selected session |
| X | remove all offline sessions shown |
| h | hide or unhide the selected session |
| left, right | move between session actions |
| space | cycle the request rule: normal, ask, auto |
| z | collapse or expand the selected tree; on a child, act on its lead |
| Z | collapse or expand all session trees |
| v | cycle the view: active (default) or all, which includes offline sessions |
| G | switch between one tree (the default) and groups: Needs you, Working and Idle |
| H | show or hide sessions hidden with h, and temp-folder sessions in the all view |
| a | in the offline view, preview and archive offline sessions, or show archived and restore |
| p | pin or unpin the selected session so it stays visible in Idle |
| d | show or hide the selected session details |

## 2. Graph

Key `2` opens it.

### Panels

- `Graph · all teams` (or Graph · team NAME when one team is shown)
- `Legend`
- `PLACE`, in Agent card
- `Charter`, in Agent card
- `Project rules`, in Agent card
- `Loop`, in Agent card
- `Rules`, in Agent card
- `Members`, in Agent card
- `Team rules`, in Agent card
- `Locations`, in Agent card
- `Live sessions`, in Agent card
- `Edges`, in Agent card
- `Last changes`, in Agent card
- `Teams`, in Teams

### Keys

#### Graph

| keys | what it does |
|---|---|
| D | delivery |
| d | show or hide dormant nodes |
| f | focus on a node and its neighbours, or zoom into a team |
| esc | go back one focus or team level |
| x | archive the selected dormant repo and tombstone its agent after confirmation |
| X | on the dormant chip, confirm removal of every dormant node |
| / | find a node; enter selects the first match |
| +, = | zoom in |
| - | zoom out |
| shift+arrows | pan |
| , . | select a linked neighbour |
| n | start a session in this project |
| enter | open the selected agent card |
| m | toggle all message and owner lines |
| t | show all nodes, then each active team in turn |
| T | create, rename, fill or archive a team |
| e | charter |
| E | global charter |

#### Graph find

| keys | what it does |
|---|---|
| enter | select the first match |
| backspace, ctrl+h | delete |

#### Agent card

| keys | what it does |
|---|---|
| e | charter |
| E | global charter |
| enter | open Sessions filtered to this agent's project |
| n | start a new session in this project (the same form, filled from here) |
| t | open Tools filtered to this node |
| a | toggle autonomy off or wake (wakes on messages) |
| L | cycle the standing loop: off, 1h, 4h, 12h, 24h (wakes the lead on a clock) |
| D | name this project |
| u | undo the selected one (a learned change) |
| , . | select a previous or next learned change or charter draft |
| y | accept the selected charter draft (drafted from AGENTS.md) |
| x | reject the selected charter draft |
| l | choose or clear the repo lead |
| h | hide or unhide this project |
| H | show hidden projects |
| ctrl+u/d, pgup/pgdn | scroll the card: charter, rules and learned changes |

## 3. Quotas

Key `3` opens it.

### Panels

- `Subscriptions`
- `Friction`
- `Workers` (with the registered and running counts)
- `Workers log`, in Workers log
- `Difficulty details`, in Workers log
- `Quality · worker picker`, in Quality details
- `Quality · scores over time`, in Quality details
- `Selected cell`, in Quality details

### Keys

#### Quotas

| keys | what it does |
|---|---|
| tab, shift+tab | Quotas / Workers log / Quality |
| p | protect or unprotect this subscription |
| f | set worker quota floor (0-95%) |
| s | show or hide subscriptions that are not set up |

#### Workers log

| keys | what it does |
|---|---|
| tab, shift+tab | sub tab |
| pgup | page up |
| pgdown | page down |

## 4. Messages

Key `4` opens it.

### Panels

- `Threads`
- `Conversation`

### Keys

#### Messages

| keys | what it does |
|---|---|
| left/right | select the previous or next message |
| pgup/pgdn | page |
| home/g, end | first thread, latest message |
| ctrl+u | scroll the open conversation up; the mouse wheel over it works too |
| ctrl+d | scroll the open conversation down |
| enter | open the selected message body |
| f | report a defect found later on the selected worker conversation |
| tab/shift+tab | sub tab |
| p | filter the threads by project |
| t | filter the threads by team |
| o | open the thread's change or Desk ask |
| e | expand or collapse the selected worker run |
| l | filter |
| m | send a message to the session as yourself |
| x | stop the selected worker or session |
| d | show or hide details |

## 5. Tools

Key `5` opens it.

### Panels

- `Tools` (with the search and the tool count)
- `Project information`, in skills per project
- `Skill information`, in skills per project
- `Skill cost per project · tokens in every session`, in tools project costs

### Keys

#### skills per project

| keys | what it does |
|---|---|
| i | import |
| j/k, down/up | move |
| tab | switches |
| / | filter |
| ? | help |
| q | quit |
| esc | back |
| right, enter | skills |

#### project skills

| keys | what it does |
|---|---|
| i | import |
| j/k, down/up | move |
| tab | switches |
| / | filter |
| ? | help |
| q | quit |
| esc | back |
| left | projects |
| space | on/off |
| enter | on/off or review |

#### Tools

| keys | what it does |
|---|---|
| tab | switches |
| t | show skill cost per project |
| n | add an MCP server or a skill from a folder |
| enter | open the actions for the selected tool |
| R | rescan tool copies |

## 6. Settings

Key `6` opens it.

### Panels

- `Connected apps`, in Connected apps
- `Integrations (all machines)` (opened from the Integrations row)
- `Machines`
- `Time zone`
- `Notifications`
- `Tips`
- `Standing orders and notes`
- `Working rules and guidance`
- `Nudges`

### Keys

#### Settings

| keys | what it does |
|---|---|
| tab, ] | jump to the next section |
| shift+tab, [ | jump to the previous section |
| enter | open the selected setting: integrations, name this machine, or restart tips |
| space | toggle internet access, desktop notifications or tips |
| p | switch internet access in Connected apps |
| u | set the tunnel URL, or undo the selected setting revision |
| a | add a connected app |
| x | remove the selected machine or revoke the selected app |
| g | choose the shared MCP servers the selected bot may use |
| n | rename the selected app |
| r | rotate the selected online machine token |

## 7. Desk

Key `7` opens it.

### Panels

- `Needs you`
- `Delivery`
- `Change`, in On a change in flight
- `Evidence`, in On a change in flight (or Steps and evidence for a change with steps)
- `Inbox detail`, in Desk
- `Log`, in Desk (Log · SOURCE for one log source)

### Keys

#### Desk

| keys | what it does |
|---|---|
| pgup/pgdn | page |
| home/g, end | first, last row |
| a | approve the request |
| r | refuse the request |
| s | keep the refusal |
| O | grant the request |
| A | approve all changes |
| t | type answer |
| x | dismiss the ask |
| i | on a sign-in ask: open that CLI's login in a herdr pane on the machine that needs it |
| w | in the details of a question or approval: answer the marked option and keep it as a standing rule. Type the action and its area: merge in repo:<name>, team:<name> or network. A rule applies only on an exact match; undo it on its Log line. |
| d | on an open ask: talk it over. A chief item opens the chief's session and wakes it; a CTO item opens your CTO chat. |
| u | undo |
| n | on an ask that reached you, open or answered (in Handled for you): the chief did not need to send it. Type one optional line of why; the chief reads it in its next batch. |
| m | on a handled ask: the chief should have asked you. Type one optional line of why; the chief reads it in its next batch. |
| v | show or hide the done and stale changes in the In flight section |
| l | open or close the Log section: what the CTO and the gateway did, with the undo of a CTO change |

#### On a held request row

| keys | what it does |
|---|---|
| a | approve the request |
| r | refuse the request |

#### On a learned suggestion row

| keys | what it does |
|---|---|
| a | accept it |
| r | reject it |

#### On an outcome row

| keys | what it does |
|---|---|
| s | keep the refusal |
| O | grant the request |

#### On a CTO proposal row

| keys | what it does |
|---|---|
| enter | open the proposal detail |
| a | approve the change |
| r | decline the change |
| A | approve all the changes |

#### On a change in flight

| keys | what it does |
|---|---|
| pgup | ten changes up |
| pgdown | ten changes down |
| a | show all |
| y | on a To try row, report that the check passed: the owner tried it and it works |
| n | on a To try row, report that the check failed: type one line of what is wrong |

## Global keys

| keys | what it does |
|---|---|
| 1-7 | switch tabs: Sessions, Graph, Quotas, Messages, Tools, Settings, Desk |
| enter | go into the selected thing |
| arrows, j/k | move |
| esc | back; first clears a filter you typed |
| / | filter or search |
| ctrl+p | find a session by name, project, folder, machine or task |
| R | refresh |
| c | open the CTO session |
| C | focus the CTO box |
| ? | help |
| q, ctrl+c | quit |

## Overlays and forms

These open over any tab. Each has its own keys.

### Message body

| keys | what it does |
|---|---|
| esc, enter | close |
| ? | help |
| q | quit |
| up/down, j/k | scroll |
| pgup/pgdn | page |

### Connected apps

| keys | what it does |
|---|---|
| up/down, j/k | move |
| p | internet access |
| u | tunnel URL |
| a | add |
| x | revoke |
| g | grant |
| enter | approve a pending app; on an app row, rename it (same as n) |
| r | refuse |
| left, right | change kind |
| n | rename the selected app |

### Quality details

| keys | what it does |
|---|---|
| tab, shift+tab | sub tab |
| ? | help |
| up/down, j/k | model |
| left/right, h/l | bucket |
| pgup/pgdn | page |
| o | older keys |
| w | weekly table |
| R | refresh |

### Proposal detail

| keys | what it does |
|---|---|
| esc | back |
| up/down, j/k | scroll |
| ? | help |
| a | approve the change |
| r | decline the change |
| A | approve all the changes |

### Proposal history

| keys | what it does |
|---|---|
| esc | back |
| ? | help |
| up/down, j/k | select |
| enter | open the proposal detail |

### Tools plan

| keys | what it does |
|---|---|
| enter, y, Y | apply |
| esc, n, N | cancel |
| ? | help |
| up/down, j/k | scroll |
| d | use the exact folders |

### Tools picker

| keys | what it does |
|---|---|
| esc | cancel |
| ? | help |
| up/down, j/k | select |
| space | tick |
| / | filter |
| enter | apply |

### Tools menu

| keys | what it does |
|---|---|
| esc | back |
| ? | help |
| up/down, j/k | move |
| enter | choose |

### Tools add

| keys | what it does |
|---|---|
| esc | cancel |
| tab, down | move to the next field |
| shift+tab, up | previous field |
| left/right | choose the machine |
| enter | add |
| backspace, ctrl+h | delete |

### discard skill changes

| keys | what it does |
|---|---|
| y, Y | discard |
| n, N, esc | keep changes |

### tools filter

| keys | what it does |
|---|---|
| enter | keep filter |
| esc | clear/back |
| backspace, ctrl+h | delete |

### tools project costs

| keys | what it does |
|---|---|
| j/k, down/up | move |
| enter | open |
| esc | back |

### CTO box

| keys | what it does |
|---|---|
| ctrl+], tab | back to Sessions |
| ? | help |
| esc | skip tip |
| enter | opens the full CTO |
| pgup/pgdn, ctrl+u/d | scroll |
| m | model |
| n | new |
| u | undo |
| w | drive |

### Help

| keys | what it does |
|---|---|
| q | quit |
| esc | close |
| ? | all screens |
| up/down, j/k | scroll |
| pgup/pgdn | page |
| home/g, end/G | first, last line |

### Confirmations

| keys | what it does |
|---|---|
| enter, y, Y | yes |
| esc, n, N | no |

### Forms

| keys | what it does |
|---|---|
| enter | submit or next field |
| esc | cancel |
| tab/down | move to the next field |
| shift+tab, up | previous field |
| left/right | change the choice |
| space | toggle |

### Teams

| keys | what it does |
|---|---|
| q | quit |
| up/down, j/k | select |
| enter | filter the Graph to this team |
| c | make a team with a name and purpose |
| r | change the team's display name |
| p | change the team's purpose |
| a | archive a team with no members |
| space | add or remove the selected repo or agent |
| l | choose or clear the team lead |
| m | move the team to the next authority mode: absolute, charter first, negotiate |
| o | send the questions of the team's members to its lead, or back to the owner |
| h | change what the owner holds back from the team |
| H | change what the owner holds back from the selected repo |
| u | put back the last mode or held back change |
| ? | help |
| esc | back |

### Session finder

| keys | what it does |
|---|---|
| enter | open |
| esc | close |
| up/down | select |
| pgup/pgdn | page |
| backspace, ctrl+h | delete |

## Agent and CTO tools

Tools are listed by the roles that may call them. Actions are the values of the `action` field in the tool's input schema.

| tool | roles | what it does | actions |
|---|---|---|---|
| `agent_announce` | agent | REQUIRED: set your current task (one short line) and expertise; fill expertise once. Read AGENTS.md. Call it at the start of every session and whenever your task changes. | - |
| `agent_ask_intake` | agent | Chief of staff only: list sessions whose last turn may wait for the owner, read that turn with the preceding input (labelled owner, peer, agent_net or unknown) and secrets masked, or import it as one owner ask. Never answers, sends to, or changes a session. | `list`, `read`, `import` |
| `agent_ask_sort` | agent | Chief of staff: sort waiting asks (close, merge, send, owner), withdraw an ask the asker took back or a newer ask replaces, undo its own newest sort, list asks and sort counts, or read an ask with detail full. Result hide (preview, apply, undo) hides sort history reversibly. Never answer for the owner. | - |
| `agent_bar_edit` | agent | Add, edit or remove the project rules that workers and reviewers check diffs against. Lead of the project, owner or CTO only. | - |
| `agent_change_submit` | agent | Submit work done outside a worker as a change, so agent-net moves it through the project's delivery steps. A worker's complete hand-back submits its change by itself. A new head commit on the same change makes a new revision. | - |
| `agent_changes_find` | agent, worker | Find delivery changes: their revision, the step each is in, who owns it and the evidence so far. Give change to read one change in full with its revisions and evidence. | - |
| `agent_charter_draft` | agent | Propose one charter value drafted from AGENTS.md, only when agent-net asked you to. The owner accepts or rejects it; nothing changes before that. | - |
| `agent_file_send` | agent, worker | Send files from this session to one peer with a note. Paths must be absolute and within the session project, worktree or /tmp. Workers send files with agent_handback. | - |
| `agent_friction_mark` | agent | Lead sessions only: set the read status of friction reports (new, read, triaged, fixed, wontfix or infra) by ids, by group, or by a filter with project, area and text. Give link (a change id or a web address) and note when the mark has them. dry_run counts the matches first. Other sessions are refused. | - |
| `agent_graph_read` | agent, worker, cto, bot | Read the graph: nodes with charters and rules, and their edges. Give node for one node and its edges; a shared name returns every node. | - |
| `agent_handback` | worker | Workers only: end your task with your report and status. This is how the caller gets your result. | - |
| `agent_learn` | agent, worker, cto | Record a finding about a model, harness, project or machine, with evidence. Findings feed the playbook; they change nothing directly. | - |
| `agent_machine_get` | agent, worker, cto, bot | Read a machine's health, load, CPU and busiest processes, or one detail category: processes, models, quotas, track record or playbook. Processes accepts sort rss (default) or cpu and reports stop refusals. Pages long lists. | - |
| `agent_machine_log` | agent, worker, cto | Read a machine's redacted daemon or gateway log for a time window, with an optional text filter. At most 1000 lines and 256 KB. | - |
| `agent_machines_find` | agent, worker, cto, bot | List machines with online state and versions. Use agent_machine_get for one machine's models, quotas, track record or playbook. | - |
| `agent_mcp_servers_find` | agent, worker, cto | List the shared MCP servers, their health and tools, and whether you may use them. Read-only. | - |
| `agent_mcp_test` | agent | Test that a shared MCP server answers. Does not change it. | - |
| `agent_message_get` | agent, worker, cto, bot | Read one message's full body, delivery state and outcome. Long bodies come in chunks: follow next_body_cursor until it is absent. Attachments are in files: pass file to read one as text, then follow next_file_cursor. A binary file gives name, size and type only. | - |
| `agent_message_handoff` | agent | Hand a request you received as a team or repo lead to a member. The member gets the original message with the original sender kept, answers the asker directly, and you get a copy of the reply. Use it when the request is the member's to answer; otherwise answer it yourself with agent_message_reply. | - |
| `agent_message_reply` | agent, worker, cto, bot | Reply to a message you received, with an outcome on requests: accept, reject (reason), redirect (reason, redirect_to), escalate (recommendation). ack: true closes a request with no answer. | - |
| `agent_message_send` | agent, worker, cto, bot | Send to one session, or as the CTO to_set for a group. to also takes the role address chief or cto. urgent jumps the queue and still counts. A peer message is a request, not the owner's approval. Lead-level agents may start a request to the CTO for a tool or a second opinion; others go through their lead or the chief. | - |
| `agent_message_withdraw` | agent, worker, cto | Withdraw undelivered message; owner and CTO may withdraw another session's; older_than clears the older backlog. | - |
| `agent_messages_find` | agent, worker, cto, bot | Find messages by conversation, peer, direction or time. Rows hold a preview; use agent_message_get for the body. | - |
| `agent_note_write` | agent, worker, cto | Save a note that outlives this session, for your agent or project: name, one-line description, body. The same name in the same scope updates it. The answer lists up to 3 similar notes: merge instead of adding. One fact per note. Owner-wide notes are for the owner and the CTO. | - |
| `agent_notes_find` | agent, worker, cto | Read notes with their bodies. Your start text lists only titles, the most used first. Give text to search names, descriptions and bodies, scope to narrow, stale to list notes nobody used for 30 days. | - |
| `agent_nudge_pause` | agent | Chief of staff only: preview or pause one automatic nudge kind for a team or agent. Preview lists sessions and the last 24h message count, without writes. Apply logs an owner-undoable pause. Real messages and first step notices are unaffected. | - |
| `agent_nudge_pauses_find` | agent | Chief of staff only: list nudge pauses, including when and why ended pauses ended. | - |
| `agent_nudge_resume` | agent | Chief of staff only: end a nudge pause by id. Logs an owner-undoable resume. No backlog is sent. | - |
| `agent_nudge_stats` | agent | Read the automatic nudge firing log: fires, followed and useful rate per kind by agent and by team, over the last 24 hours by default. A nudge counts as followed when a tool call, announce, step evidence or a message that is not an ack comes from its recipient within 30 minutes. | - |
| `agent_owner_ask` | agent, cto | Ask the owner a question, an approval or an idea, with your recommendation and up to four answers. It goes to the chief of staff, who brings decisions to the owner; carry on, do not wait for the answer. A new ask adds to your pending ones; replaces retires one. | - |
| `agent_owner_brief` | agent | Chief of staff only: deliver one informational daily brief to the owner's Desk. Give body (at most 15 lines) and the owner-local date as YYYY-MM-DD; the same date replaces it. Call with no body for the schedule. A brief never asks for an answer. | - |
| `agent_owner_judgment_record` | agent, cto | Chief of staff and CTO only: record, in the same turn, a decision the owner made in your chat. area and action make it a standing rule. | - |
| `agent_owner_judgments_find` | agent, worker, cto | Search the owner's past decisions, standing orders, rules, answers and corrections. Ranked for a query, else newest first. marks: true returns only the Desk marks with their asks; offset pages. Agent notes show only with kind feedback, fact or reference (see agent_notes_find to read yours). | - |
| `agent_permission_propose` | agent, worker, cto | Propose Claude Code permission rules or folder trust for the owner to approve. | - |
| `agent_project_add` | agent | Register project folders on this machine, or scan a folder for repositories (use dry_run first). Only when the owner asked. | - |
| `agent_projects_find` | agent, worker, cto, bot | Find registered projects and the machines and folders that hold them. Use agent_sessions_find for their sessions. | - |
| `agent_session_get` | agent, worker, cto, bot | Read one session's profile: folder, branch, charter, rules, delegation, worker facts. Use agent_sessions_find when the ID or name is unknown. | - |
| `agent_sessions_find` | agent, worker, cto, bot | Find sessions by machine, project, harness, model, kind, online state or text. Use agent_session_get for one. | - |
| `agent_settings_read` | agent, worker, cto, bot | Read settings, values, sources and history by key, repo or named target. A target names level network, repo or agent; a name is looked up only in that kind. | - |
| `agent_step_claim` | agent | Claim a ready node-owned delivery step before starting it. The first eligible session to claim or report it takes it. Name the exact commit in the task. The caller may set reviewer to hand an accept or review step to another session. | - |
| `agent_step_report` | agent | Report a delivery step you own: pass, fail with findings, needs_owner, or close without merge (the caller may close at any step, with a note). Name the exact commit you checked. Send reports for several steps. The reply is short; agent_changes_find has the change. | - |
| `agent_worker_profiles_find` | agent, cto | List worker profiles: harness, model, effort and the machines that can run them. Read before choosing a worker model. | - |
| `agent_worker_rate` | agent | Rate a worker 1 to 5, or report a defect found later with severity 1 to 4. brief_defect charges rework from a bad brief to the briefing session, not the worker. move_to_brief_defect moves a defect you filed, by id or the only match, to a brief_defect. Only the session that started it may rate it. | - |
| `agent_worker_start` | agent | Start or continue a worker with a structured brief. Only if the owner or work mode asks. problem and done required; evidence, hypothesis, scope optional; task: extra notes. Refused only for a file a running worker or open change holds, unless "overlap ok: reason". mode "check" checks only. brief_flags never block. For a feature with several tasks, list the tasks first, then group them into sessions: tasks that share code and design continue one session (continue) instead of starting new workers. | - |
| `agent_worker_stop` | agent | Stop a worker you started, or cancel a queued start. Its worktree is kept. | - |
| `agent_workers_find` | agent, worker, cto | Find workers with their caller, model, difficulty, state and run time. detail full adds the start time, the struggle counts and the recorded cause of the latest run's failure or rework. Defaults to running workers you started. | - |
| `answer` | cto | Apply the owner's answer to a held request, an inbox item or a contested outcome (let stand or overrule). | `a`, `A`, `r`, `R`, `let_stand`, `overrule` |
| `cto_intake_find` | cto | Open system-health alerts of the CTO intake queue, newest first. Read it at the start of each owner conversation. | - |
| `cto_new` | cto | Start a new CTO conversation. The timeline and proposals stay. | - |
| `cto_placement_memory` | cto | Read or save placement answers explicitly given by the owner. | `read`, `save` |
| `cto_proposals_find` | cto | Find proposals, or all pending owner items, each with its answer call. | - |
| `cto_standing_orders` | cto | List, save, update, remove, search or read history of standing orders. | `save`, `read`, `list`, `remove`, `search`, `history`, `update` |
| `cto_ui_show` | cto | Show the owner a TUI tab with an item selected, or a short notice. Fails with no TUI open when no local TUI is connected. | - |
| `graph_change` | cto | Change the graph or undo. Lifecycle removal (node_remove, agent_tombstone, repo_archive) is a rare op in cto_op and needs owner approval; other actions apply at once. | `charter_set`, `node_add`, `edge_set`, `edge_remove`, `settings_set`, `undo`, `repo_create`, `agent_create`, `team_create`, `team_rename`, `team_purpose`, `team_charter`, `team_archive`, `team_member_add`, `team_member_remove`, `lead_set`, `scope_set`, `scope_remove` |
| `mcp_call` | cto | Call a tool on a shared MCP server at once. Credentials never appear in the call. | - |
| `mcp_change` | cto | Manage shared MCP servers and connected apps. apps_list lists IDs; app_grant/ungrant take id and grant; app_rename takes id and name; app_revoke invalidates credentials. public_status/on/off control internet access. | `add`, `remove`, `test`, `enable`, `app_ask` |
| `project_change` | cto | Add, remove, rename or create a project at once. | `add`, `remove`, `display`, `create` |
| `projects_layout` | cto | Read-only project layout of online machines: roots, groups, remotes and newest commits. | - |
| `proposal_ask` | cto | Open the approval dialog in the chat for an open proposal that is already waiting. | - |
| `session_change` | cto | Change a session: rename, replace, trust, policy, start, stop, resume, restart, or merger start or stop. | `rename`, `trust`, `mcp_servers`, `policy`, `session_start`, `merger_start`, `merger_stop`, `stop`, `resume`, `restart` |
| `sessions_archive` | cto | Archive offline sessions. Records can be restored by the owner. | - |
| `settings_change` | cto | Change settings values or undo a revision at once. Machine removal, token rotation, process stop and the owner operations are rare ops in cto_op. | - |
| `skill_archive` | cto | The skill archive: list (latest version and candidates), read a version (SKILL.md and the file list; file and offset read one page of a file; after pages the list), add from a machine folder or Git URL, promote, remove, undo, or intake every enrolled machine's skills (the answer lists each skill not stored). An intake copy of a name with a version is a candidate; promote chooses. A removal stays out of intake until an explicit add. Sessions get their node's skills at start. | `list`, `read`, `add`, `add_git`, `remove`, `undo`, `intake`, `promote` |
| `skills` | cto | Skills on graph nodes: suggest a short list for a repo, team or agent, set it with undo, read each node's effective list, or undo a set. Suggest matches the node's charter and project facts by word and makes no model call. | `suggest`, `set`, `effective`, `undo` |
| `tools_change` | cto | Change tools, skills, commands, agents: plan, apply, undo, remove a native tool. Import is a rare op in cto_op. | `plan`, `apply`, `undo`, `native_remove`, `toggle`, `new_projects` |
| `worker_change` | cto | Start or stop a worker with a complete brief. | `start`, `stop` |
