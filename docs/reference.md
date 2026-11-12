# Reference

Decision [0036](decisions/0036-primitives.md) defines an agent as a lasting identity with a charter and a Graph node, and a session as one running process of an agent. A worker is an agent for one task. A task is a contract with a caller, doer, done condition and result; the free-text status label is an announced task. Authority covers actions requiring the owner; autonomy covers decisions about work. Pending work is derived. The current interfaces below may still use older labels and do not yet implement every part of the decision.

## Typed Repo/Agent Graph cutover

The cutover is an application upgrade, so nobody triggers it. The gateway
checks at start and after every machine completes its hello. When every
enrolled machine, including an offline machine or one that has never
connected, has reported the `graph_refs` capability in `hello` with a known
release version, the gateway takes a validated backup and enables typed reads
and writes in one transaction. While any machine is older or unknown, the
cutover waits. There is no Settings action and no owner call.

The backup is a new directory beside the gateway data directory, named
`gateway-before-typed-graph-<date>-<time>`. A successful cutover writes one log
line with that path. If the cutover fails, the backup stays, the gateway logs
the error and raises one Inbox item, and it retries at the next machine hello
or gateway start. If blockers last more than 24 hours after the gateway
version first started, the owner gets one Inbox item naming the machines to
upgrade. It is closed when they upgrade. Turning the cutover off later
requires restoring the backup. Once on, it cannot run again.

## Remove a machine or rotate its token

In the TUI, open Settings (`6`) and select a machine. Press `x` to remove it
or `r` to rotate its token. Confirm the prompt naming the machine, or press
Esc to cancel. An offline machine can only be removed, not rotated.

Removal revokes its credential, closes its link and marks its sessions offline.
Its history stays. To join that machine again, run `agent-net setup` there.
Rotation saves a new token to that machine's `node.json` atomically with mode
0600 and reconnects it. The old token stops working when the save is confirmed.

The owner-only operator methods are `machine_remove` and `machine_rotate`, both
with `{node_id}`. The CTO uses `settings_change` with `area: machine_remove` or
`area: machine_rotate` and `node_id`. These create proposals for the owner's
approval in Inbox. Agent sessions cannot invoke the owner methods directly.

## Working rules and guidance

Settings tab 6 has a Working rules and guidance section. Choose network or a
repo, select a setting and press Enter to edit its value as JSON. The row shows
the effective value and source. The selected row shows its history. Press `u`
to undo the latest displayed revision and confirm; this undoes the entire batch.
A tighter ordered network rule moves less strict stored repo values. The result
names repos whose effective values changed. Owner-controlled rule text has no
strictness order: an owner-approved repo value replaces its network wording. Worker hand-back text
has required slots such as `{{handback}}`, `{{complete_yes}}` and
`{{complete_no}}`; retain them when editing so code can insert the protocol
tokens. Empty text and empty sets are values;
deleting an override makes it inherit again.

The advice setting `sessions.stall_timeout` controls how long a busy session
or pending delivery may go without harness progress before the owner sees a
stall warning. It defaults to 20 minutes and can be set for the network or a
repo. Sessions waiting for the owner are excluded. For a busy Claude session,
CPU use in its process tree is also a sign of progress when machine usage
sampling is available and the process is not shared. A warning is a prompt to
inspect the session; it never stops or restarts it. Workers use their own idle
notices. A session raises one warning for one activity time. Once it is open,
answered or closed by the chief of staff, the same activity time raises no new
warning; only later activity can. Offline sessions, and non-busy sessions that
announced done, are not warned about.

Agents, workers and the CTO can read public settings with `agent_settings_read`.
It accepts an optional `key` and `repo` and returns definitions, effective
values, sources, explicit values and history. Personal preferences marked owner-only are not exposed.
History records old and new values, actor, reason, revision and undo, and is
kept indefinitely. Use `settings_change` with `area: values_write`, `changes`
and `reason`, or `area: values_undo` and a `revision`. Each change gives `key`,
`level`, `scope`, and either a JSON `value` or `delete: true`. Give `expect` as
the currently stored value, or omit it if none is stored. A conflict reports the
current value and leaves the whole batch unchanged. Advice changes and advice
undo by the CTO apply immediately. Rules require an owner proposal, even in the
graph owner window. The owner uses the TUI or owner-gated `settings_write` and
`settings_undo` operator calls. A snapshot updates managed guidance files on
each machine. The Settings view names online daemons older than v0.4.117,
the first release with settings sync, and separately names machines whose version
cannot establish support. Their settings updates may not reach them. Definitions
marked not live are visible but do not yet control the old behavior; later build
steps switch their readers and writers.

Delivery values live in the embedded recipe and gateway settings tables. The
moved node settings are `delivery.tests_before_merge`,
`delivery.merge_authority`, `delivery.deploy_rule`, `charter.judge_consumers`
and `charter.autonomy_mode`. The older node settings methods are adapters to
the same values. The owner can change them in Settings or with `settings_write`;
the CTO can change these rules through its audited settings tools. A repo with no graph node uses the
network merge authority. Text and worker delivery settings appear under
`delivery.*` in Settings. The delegated merge text says that anyone above you
in the tree who holds merge may approve (decision 0089). Task reminders and alerts use
`delivery.step_reminder` (default 2h) and `delivery.step_stuck` (default 12h),
with 0 turning them off. Branch prefix changes apply to new workers;
a resumed worker keeps its recorded branch. Cleanup recognizes recorded branches
with a valid older prefix. The worker sandbox is also a repo setting, with the
older project policy and node-setting methods as adapters. Older daemons and extensions use their built-in merge text until upgraded, even when
the authority value has synced.

On first upgrade, the gateway copies old node setting rows into settings in one
transaction and records their cutover. Existing settings values take precedence,
except that an unsafe worker sandbox value is tightened to preserve the old
machine policies and node fallback. Inbox checks name machines made stricter
and their old values. Repeating the cutover changes no values. The old node rows stay as a backup. Delivery shorthand rows are copied to `delivery_settings_legacy` for inspection; older binaries do not read that table. There is no downgrade tool. An older binary also refuses to open a database written by a newer release, rather than running without the tables, columns and rules it does not know: `gateway.db schema version N is newer than this agent-net (knows M); upgrade agent-net or restore the backup taken before the upgrade`. A rollback after a release that changed the schema therefore means restoring the backup taken before that upgrade. See [Files](#files).

## CTO settings and conversation

The CTO applies changes immediately with an audit line and an Inbox/Desk
notice. Supported reversible changes include undo. Only node removal, agent
tombstone, repo archive, machine removal and machine token rotation require a
proposal and an owner decision. The older graph driving window remains an
owner TUI control; it is not an extra prerequisite for CTO changes.

Each proposal tool accepts an optional `reason`. The daemon validates the exact
call before storing it. If the CTO harness supports form elicitation, it can show a dialog with the stored tool, action, arguments and reason. Accept approves and decline refuses the proposal. Cancel, an error, or no dialog leaves it open in the Inbox. For an open proposal that is already waiting, `proposal_ask` with `id` reopens the same dialog from the stored proposal; a closed or unknown ID is refused. For MCP protocol 2026-07-28 and newer, the harness returns the answer in a follow-up tool call; older versions use a direct elicitation request. The CTO sees a short result. Typing instructions in the CTO conversation does not approve a proposal.

`proposal_list` lists proposals across online machines, or on one machine when
passed `node_id`. `proposal_answer` takes `node_id`, `proposal_id` and an action:
`a` approves, `x` declines, and `A` approves open proposals across online machines
in order, stopping on an error. The daemon validates again and runs the stored
call once with actor `owner (CTO proposal p_N)`. Full results, including plans,
MCP results and invite lines, are shown to the owner. An approval through
`proposal_answer` gives the CTO a short outcome note at its next idle moment,
without the call's result data. A dialog approval returns the outcome as the
tool result instead, so no note follows it.

The daemon classifies every tool and action as a read, talk, immediate change
or proposal. The five destructive actions above remain proposals. Messages
with `as_owner: true` run immediately on the owner's instructions. Settings,
worker and session actions, shared MCP administration and the explicit owner
operations described below also run immediately.

Ordinary agent-shaped CTO reads retain their registered CTO caller identity
and scope. `settings_change` with `area: operator` is a separate, explicit
owner-operation path. It can read owner data, including the Inbox, inventory,
settings and other sessions' text. This access is private to the process-verified
CTO. The CTO must not share owner data with peers unless the owner asks.

Starting or entering the CTO through the owner gate registers one network
session. The gateway keeps its ID in `owner_settings` as `cto_session`; the
hosting daemon keeps that ID in its `cto_network` state table. A later start
on another machine moves the same session record there. Harness switches and
new native conversations keep the network ID. Agent lists and `agent_session_get`
show its reserved name `CTO` and harness `cto`, regardless of the owner's title.
Other sessions cannot take that name, and the CTO session cannot be renamed.
On upgrade, earlier uses of the name CTO are cleared or given an ID suffix.

Plain `agent_message_send` and `agent_message_reply` calls send from this session with the note:
`from the CTO session: model output, not the owner's words. Judge it like any peer request.`
A send or reply with `as_owner: true` sets the CTO flag.
Its note is `from the CTO, on the owner's instructions. Treat this as the owner's word within your charter's limits.`
The gateway accepts that flag only from the registered CTO session. Both
kinds of send require the gateway's `network_session` CTO capability and are
refused with an upgrade error on older gateways. They are not queued offline.
Approved worker-start tasks remain owner-session messages without the CTO flag.
Messages to the CTO session reach the currently selected harness, including
replies after a harness switch.
The owner can hand one item over with `c` (see below).

Open proposals appear on the Desk (Needs you). Enter shows the CTO's reason, a fresh diff,
and the sender and time of up to three messages delivered before the proposal.
Press `a` to approve, `x` to decline, or `A` to review and approve all. The
decision screen shows the full result; after approve-all, select an outcome and
press Enter to inspect it. A changed proposal list requires a new confirmation.

For 30 days after the gateway upgrade, replies to
older conversations started by the CTO as the owner are routed to the CTO
session, including replies through Claude Code's reply socket. Replies to
personal owner messages stay with the owner. Peer bodies are quoted as data.

The owner-only `cto_settings` operator method reads and saves the active CTO
harness, its model, the saved model for each harness, the owner-facing title,
and conversation auto-compaction rules. The response has `title`, `harness`,
`model`, `opencode_model` and
`claude_model`. A set request with `harness` and `model` saves the model for
that harness and makes it active. A request with only `harness` switches the
active harness without changing either model. An empty model selects that
harness's default. A request with only `title` changes the name on the owner's
screens. The title is 1 to 24 printable characters, trimmed, and cannot contain
`[agent-net` or `<cross-session`. Empty stores `CTO`. It is kept in the
gateway's `owner_settings` as `cto_title`, not in the local daemon state. Agents
never see it: agent-net's notes always say `CTO`.

The active harness and both model preferences are stored in the local daemon
state. A legacy single model is moved to `opencode_model` when it has a
provider prefix such as `opencode-go/deepseek-v4.1-flash`; otherwise it is moved
to `claude_model`. OpenCode uses its inventory default when its saved model is
empty. Claude Code uses `opus` when its saved model is empty. Pass a `compaction`
object to change the auto-compaction rules; the gateway stores those rules with
the other owner settings. Saving settings does not start a CTO runtime.

Run `agent-net cto` to show the settings, `agent-net cto harness claude` or
`agent-net cto harness opencode` to change the harness, and
`agent-net cto model opus` to set the model for the active harness. Pass an
empty argument (`agent-net cto model ""`) to restore that harness's default.

```json
{"compaction":{"threshold_percent":50,"cache_expiry_enabled":true,
 "cache_window_seconds":{"anthropic":300,"openai":300}}}
```

`threshold_percent` defaults to 50. After a CTO reply, at or above this
share of the context window, the daemon compacts. `cache_expiry_enabled`
defaults to true. When enabled, the daemon also compacts an idle conversation
30 seconds before the provider's configured cache window expires. The window
is 300 seconds by default. One compaction is allowed per idle period and none
runs during a turn. A compaction object replaces the previous rules; omitted
fields use their defaults. Old floor and growth fields are ignored.

These daemon rules apply to OpenCode, which exposes a compaction API. Claude
Code has no such API. The daemon compacts an idle Claude session by attaching
with `claude attach <jobId>` and typing `/compact` with fixed focus
instructions, so the summary keeps what an agent-net session needs. The typed
line is:

```
/compact Keep: the current task and its goal; open questions to the owner and to other agents; every agent-net ID (sessions s_, workers, changes ch_, messages, asks); branches, worktree paths and full commit shas; decisions made; the next step.
```

The cache-expiry setting is ignored for that harness. The idle Claude rules are
set by the `sessions.idle_compaction` settings.

The daemon asks OpenCode 2.2.0 to compact with
`POST /api/session/{id}/compact` and `{"delivery":"steer"}`, which runs at the
next step boundary. The conversation's messages report the context before a
compaction. The first reply after it reports the context after. The CTO chat
shows one line, for example `compacted: 182k -> 14k tokens`; it is stored with
the other CTO action lines. Action lines are short. Graph change details remain
in graph history. The daemon keeps one displayed timeline across harnesses and
restarts. A switch adds a dim divider with the harness, model and local time.

An owner message to the CTO is `owner_send` with the CTO session ID (or the
name `CTO`) and the text. It starts the selected harness on first use. OpenCode runs as a private server. Claude Code runs as a
background session using the owner's Claude login, including a Claude Max plan
when that login uses one. It uses the model name or alias in the setting, such
as `opus` or `sonnet`; the default is `opus`. Its transcript stays in Claude's
projects directory.

Each harness keeps its own conversation ID in daemon state across restarts and
harness switches. `cto_new` starts a fresh conversation for the selected
harness only. When a harness is left, the daemon saves its messages and action
lines in the local state database. `session_text` on the CTO session merges that
saved history with the selected harness's live conversation and the durable
switch dividers, in chronological order. It returns the newest page by default
and `older` is the cursor for the previous page. Graph action lines carry the
tool name `cto_action`; a harness failure carries `cto_error`.
Changing harness stops the previous runtime; the next send resumes the selected
conversation. The CTO is a network session, not a worker.

Once registered, the CTO is reachable while its host daemon is connected, even
when no runtime is running. Its directory entry says `online: true`, with
`status: dormant` and `availability: wakes on message` while asleep. A message
from the owner, a peer, a bot or the phone starts the selected harness and is
delivered to its saved conversation. Messages sent while the daemon is offline
wait in the normal gateway queue. After a restart or upgrade, the host sync
restores the same network identity without moving it to another machine. The
first message starts OpenCode again or adopts a live Claude background job (and
resumes the saved conversation if the job is gone). Claude may be reported as
dormant until the new daemon adopts its job. The daemon does not stop the CTO
for idleness; a harness switch, daemon shutdown or an external process exit can
end supervision. A shutdown releases a live Claude job rather than killing it.

The host daemon log records `cto.start`, `cto.stop` with a reason, `cto.wake`
with a trigger, and `cto.crash` for unexpected exits. `cto.stop` on daemon
shutdown with `action=release` means that the Claude job was left running. A
Claude job in `done` state remains available while its process is alive;
`reason=job_done` records its exit after that state.

Each harness resumes its conversation with its own saved model. For OpenCode,
the model is resolved against the local inventory. With no preference, the
daemon tries GPT 6.1, Opus 5.5, then DeepSeek v4 Pro using the model resolver. If
none is available, it uses the first model in the inventory. If the owner picks
another model in OpenCode's full interface, the next send saves that live
selection in `opencode_model` and records an action line. A model changed
through the `m` form or `agent-net cto model` takes effect on the next send.
The `m` form shows the saved model for the selected harness, a dim `default`
when that model is empty, and suggestions from the local harness inventory.
Claude suggestions include `opus`, `sonnet`, `haiku` and `fable`. The CTO box
title shows the active harness and its model.
OpenCode uses private XDG data, state, config and cache directories under `cto`
in the daemon state directory.
Its credentials are copied with its auth export and import commands at start.
The private configuration copies the owner's general OpenCode settings, with no
plugins, and uses only the CTO's agent and MCP server. The
owner's agents, MCP servers, permissions, instructions, server and autoupdate
settings are excluded. A config that cannot be read falls back to private defaults
and is reported in the daemon log.

Claude Code uses a private working directory under `cto/claude` in daemon
state. The daemon passes a private MCP configuration with `--mcp-config` and
`--strict-mcp-config`; the only MCP server in it is `agent-net cto-mcp`. The
ordinary agent-net MCP tools are not registered for this session.
Claude starts with `--tools ""` and `--setting-sources ""`. Private settings
disable all hooks and enable no plugins. Because no user settings load, the
daemon copies one value from the owner's `~/.claude/settings.json` (or
`$CLAUDE_CONFIG_DIR/settings.json`) into those private settings: `tui`, when it
is `fullscreen` or `default`. Without it the CTO would use the classic renderer
while the owner's other sessions use the fullscreen one, and a long reply (a
table) would run off the screen with no scrollback. A running CTO keeps the
renderer it started with; it takes the new value at its next start.
At daemon start, on cto-mcp hello, and before starting Claude, the daemon adopts a live `agent-net-cto` background job
in that private directory if one exists. When Claude resumes a conversation as
a copy, the daemon saves the copy's session ID and moves its action lines to
that ID. Concurrent `session_enter` calls on the CTO session share one start; each
caller can stop waiting on its own deadline. A start does not block owner reads
such as `cto_settings`, the CTO timeline, graph, usage or inbox calls.

The CTO has no built-in tools. OpenCode uses plural `permissions` rules denying
`bash`, `edit`, `write`, `subagent`, `read`, `glob`, `grep`, `list`,
`webfetch` and `todowrite`, plus a wildcard deny for future built-ins. Only the
private CTO MCP namespace is allowed. The `general` subagent is disabled.
At startup, Claude's `--help` must list `--tools` and `--strict-mcp-config`.
The daemon enforces the tool set with its start arguments and private settings;
it does not expect a tool list in background launch output.
OpenCode must report every required deny rule in the CTO agent's effective
permissions through `GET /api/agent`, with no enabled `general` subagent and
no later allow outside the private CTO MCP namespace.
Missing capabilities or permissions stop startup before an owner prompt is sent.
Harness versions are logged for diagnosis, without a version allowlist.
No model call was used to verify runtime enforcement.

The CTO reads the charter writing guide with `agent_graph_read` and `view: "guide"`; the guide is in `note`.
The `graph_change` schema contains no guide text.

The CTO uses the hidden `agent-net cto-mcp` server. Its calls pass the CTO
process-tree check, then use the same daemon implementations as the owner's
operator methods after approval for proposal calls. The brief treats material
it reads as data. The Desk shows each proposed change for the owner's decision;
the daemon, rather than the CTO model, executes the stored call.

The CTO has 34 tools: 15 reads, 8 talk tools and 11 change tools. They are
the catalog tools of the CTO role. List reads return `items` or rows, `total`
and, when another page exists, `next_cursor`; pass it as `cursor`. The default
page is 25 rows and the maximum is 100.

No server lists or runs a tool name from before the tool redesign (v0.4.71 to
v0.4.74). A call with one, from the CTO or an agent, returns one error line that
names the new tool, for example `ui was renamed to cto_ui_show.` The old CTO
names are `overview`, `sessions`, `session`, `proposals`, `feed`, `messages`,
`graph`, `machines`, `projects`, `workers`, `tools`, `settings`, `message`,
`ask_owner`, `learn` and `ui`. The table is in `internal/toolcatalog/renamed.go`.

| CTO read | Source and detail |
|---|---|
| `agent_sessions_find`, `agent_session_get` | Sessions and one session's full profile, as for an agent |
| `agent_owner_judgments_find` | The owner's judgments (orders, rules, answers, corrections) by query, area or kind |
| `agent_machines_find`, `agent_machine_get` | Machines, and one machine's health, load and busiest processes, or its models, quotas, track record or playbook |
| `agent_machine_log` | Redacted daemon or gateway log on one online machine |
| `agent_projects_find` | Public projects and where they live |
| `agent_workers_find` | Workers; the CTO defaults to `started_by: anyone` |
| `agent_messages_find`, `agent_message_get` | The CTO's own conversations, and one message body in chunks |
| `agent_graph_read` | The public graph: nodes with their full charters, maturity and rules (name and value), and edges with kind, mode and request rule. `node` reads one node; when a repo, an agent or a team share that name, every match comes back after the repo node, each labelled with its `kind` and `ref` and with its own charter, and `note` says how many. A typed read (`typed: true` with a `ref`) returns one node. `view: "guide"` returns the charter guide in `note`. |
| `agent_mcp_servers_find` | Shared MCP servers |
| `mcp_change` actions `apps_list`, `public_status` | Owner-authorized connected-app metadata and internet-access status; no credentials |
| `agent_settings_read`, `agent_worker_profiles_find` | Public settings values and history, and public worker profiles |
| `projects_layout` | Read-only project layout of online machines (roots, groups, remotes, newest commits). Cached for 10 minutes. |
| `cto_proposals_find` | The CTO's own proposals; `id` selects one. `kind` filters the list: `proposal` is the default, `all` lists every pending owner item, and `draft` (charter suggestions), `learned` (pending major learned proposals), `held`, `inbox`, `approval`, `question`, `escalation` and `rejection` narrow it. Each row has `kind`, `from`, `summary`, `options` and `answer`, the exact call that resolves it. |
| the CTO's own timeline | `session_text` on the CTO session |

The parity and canary tests cover ordinary agent-shaped views. Those message
reads remain limited to the CTO's own conversations and omit waiting-question
contents. Explicit `settings_change` operator reads use the owner handlers,
including protected-provider settings, Inbox, tool inventory and session text.
Ordinary agents do not gain access to these owner handlers.
The items that wait for the owner are readable through
`cto_proposals_find` with `kind: "all"`, so the CTO can answer them when the
owner does not want to act by hand.

The CTO acts at once on changes. Node removal, agent tombstone, repo archive,
machine removal and machine token rotation remain proposals. The CTO has one
action tool per area:

| CTO change tool | Actions or area |
|---|---|
| `agent_message_send` | one message, or a group with `to_set`; `as_owner: true` sends on the owner's instructions; `urgent: true` goes ahead of older waiting messages |
| `agent_message_reply` | a reply with an outcome, or `ack: true` to close a request that needs no answer; `as_owner: true` replies on the owner's instructions |
| `agent_message_withdraw` | one undelivered message, or with `older_than: true` every undelivered message to that recipient that is this old or older; the owner and the CTO may withdraw another session's message |
| `agent_owner_ask` | a question, approval or idea with a recommendation and up to four answers, into the Inbox |
| `agent_learn` | one finding, under the daily limit |
| `cto_ui_show` | `tab` with optional `select`, `filter` and `why`, or `notice` with a short text |
| `proposal_ask` | `id`: reopen the approval dialog for an open proposal |
| `graph_change` | `action: charter_set`, `node_add`, `node_remove`, `edge_set`, `edge_remove`, `settings_set`, `repo_create`, `repo_archive`, `agent_create`, `agent_tombstone`, `team_create`, `team_rename`, `team_purpose`, `team_charter` (including `attr: mode` and `attr: held_back`), `team_archive`, `team_member_add`, `team_member_remove` or `undo` |
| `sessions_archive` | Archive offline sessions by `ids`, `name` or `folder` glob, `project` (empty string means no project), `harness`, `created_before`, or `ended_workers`. Reversible through Inbox undo. |
| `session_change` | `action: rename`, `trust`, `mcp_servers`, `policy`, `session_start`, `merger_start`, `merger_stop`, `stop`, `resume` or `restart`. `stop`, `resume` and `restart` take `session_id`; see [CTO session stop, resume and restart](#cto-session-stop-resume-and-restart). `trust` takes either `session_id` with `on` (peer trust) or one of `project` or `path`, with optional `node_id`, to trust a folder for Claude Code. `mcp_servers` takes one of `project` or `path`, optional `node_id`, and `mcp_servers: approve` or `disable`, to record the choice for the `.mcp.json` servers Claude Code would load there. `session_start` takes `trust: true` to trust the folder and start in one step, and `mcp_servers` to record that choice first. See [CTO folder trust](#cto-folder-trust) and [Claude MCP server approval](#claude-mcp-server-approval) |
| `cto_standing_orders` | `action: save` (with `text`), `read` (with `order_id`), `list` or `remove` (with `order_id`). See [standing orders](#standing-orders) |
| `worker_change` | `action: start` or `stop` |
| `answer` | `kind: held`, `inbox` or `outcome` |
| `tools_change` | `action: plan`, `apply`, `undo`, `import` or `native_remove` |
| `mcp_change` | `action: add`, `remove`, `test`, `enable`, `apps_list`, `app_grant`, `app_ungrant`, `app_rename`, `app_revoke`, `app_ask`, `public_status`, `public_on` or `public_off` |
| `mcp_call` | one call on a shared MCP server |
| `settings_change` | `area: values_write`, `values_undo`, `worker_profile_add`, `worker_profile_remove`, `worker_providers`, `protected_providers`, `score`, `integrations`, `notifications`, `cto`, `playbook`, `machine_name`, `machine_remove`, `machine_rotate`, `invite`, `onboarding`, `operator` or `process_stop`. `process_stop` takes `node_id` and `pids` and cannot be undone. See [CTO process stop](#cto-process-stop)  |
| `project_change` | `action: add`, `remove`, `display` or `create`; `add`, `remove` and `create` accept optional `node_id` (machine name or node ID); `remove` takes `name` or `path` |
| `cto_new` | a new conversation for the active harness |

Connected apps use `mcp_change`. Call `{"action":"apps_list"}` to find app
IDs, names, kinds, grants and revocation status. This list contains no access or
refresh tokens. `{"action":"public_status"}` reads internet access and its
setup errors. These two reads do not create notices.

Grant a bot access with `{"action":"app_grant","id":"app-id","grant":"mcp server example"}`.
Use `app_ungrant` with the same fields to remove it. `app_rename` takes `id`
and `name`. `app_revoke` takes `id`: it invalidates credentials and cannot be
undone; the app must connect again. `public_on` and `public_off` switch internet
access on the gateway. No extra approval is required for these CTO actions.
Each mutation has a CTO audit line and a notice in the Inbox and Desk log.
Grants, names and the public switch include undo using their previous values,
including when the requested value was already set. An optional `reason` is
included in the notice. Ordinary agent and worker tools cannot run these actions.
OAuth consent and phone pairing use `mcp_change` with
`{"action":"app_ask","code":"1234","kind":"bot","name":"Example"}`.
For a phone, omit `kind` to retain its owner-app pairing kind. The Inbox shows
the exact code and requested permissions. The owner presses `y` to approve
through the existing approval handler, or `n` to refuse. Approval, refusal,
expiry or completion elsewhere closes the ask automatically. Repeating the
same request reuses its ask; changing the requested permissions replaces it.
The CTO cannot approve its own consent ask.

If `public_on` needs a Tailscale login or Funnel consent, it opens an Inbox ask
with the HTTPS link. Pressing `y` opens that link in the owner's browser. The
ask stays open until access is enabled; status checks and the gateway retry
automatically complete the switch and close it. `public_off` withdraws it.

The remaining owner operations use the existing settings tool:

```json
{"area":"operator","method":"session_pin","arguments":{"session_id":"session-id","pinned":true},"reason":"Keep this session visible"}
```

`method` is an explicit schema enum, also defined in
`internal/protocol/cto_operator.go`. `arguments` has the same fields as the
matching owner API; unknown operations are refused. This is not an arbitrary
RPC passthrough. The available families are:

| Owner operation family | CTO operator methods |
|---|---|
| Owner data | `info`, `nodes`, `list`, `projects`, `events`, `message_status`, `workers`, `worker_log`, `sessions_archived`, `project_display_get`, `session_start_get`, `apps.pending`, `inbox_list`, `asks_list`, `desk_log`, `outcome_inbox`, `held_list`, `usage`, `usage_top`, `counts`, `session_text`, `conversations`, `conversation_messages`, `graph_view`, `graph_talk`, `graph_get`, `charter_history`, `edge_history`, `node_settings_get`, `cto_events`, `cto_graph_history`, `team_history`, `sign_in_list` |
| Inventory and tools | `inventory_get`, `inventory_scan`, `mcp_import_candidates`, `tools_items`, `tools_item`, `tools_projects`, `tools_history`, `tools_accepted`, `tools_needs`, `tools_read_file`, `tools_summary`, `tools_skill_details`, `tools_versions`, `tools_differences`, `tools_update_plan`, `tools_keep_copy`, `tools_save_changed`, `tools_update_copies`, `tools_import_folder`, `tools_accept`, `tools_import`, `tools_export`, `mcp_coverage_plan`, `mcp_coverage_apply`, `mcp_coverage_undo` |
| Session controls | `session_pin`, `sessions_archive` (including `restore` and `dry_run`), `session_stop`, `session_resume`, `session_agent_list`, `session_agent_set`, `session_enter`, `cto_enter`, `cto_send` |
| Owner preferences and worker selection | `hidden_get/set`, `onboarding_get/set`, `quality_get/set`, `timezone_get/set`, `worker_floors_get/set/reset`, `difficulty_get/set`, `integrations_get`, `protected_providers_get`, `worker_providers_get`, `scores_get`, `scores_weekly`, `worker_rotation_get`, `difficulty_judge_pick`, `settings_owner_read`, `playbook_get` |
| Teams, delivery and merger | `team_write`, `delivery_owner_set`, `merger_get`, `merger_set`, `merger_restore`, `changes_owner_find`, `changes_owner_report`, `worker_defect`, `learned_list`, `learned_view`, `learned_answer` |
| Other owner operations | `apps.refuse`, `apps.public.url`, `apps.public.restore`, `gateway_backup`, `operator_undo`, `sign_in_raise` |

Each call is audited, including reads and failures. Successful changes create
an Inbox/Desk notice; archive previews do not create a change notice. Undo
captures prior preferences, pins, bindings when previously bound, app URL,
accepted tool versions and merger registrations. Graph-backed Team/delivery
changes use graph history; configuring a merger also saves the setting undo.
Archive/restore and MCP coverage apply offer their existing inverse actions.
Onboarding undo restores the previous steps and disabled state. Composite undo
validates all operations before running them. Append-only reports, exported
files, backups, imported files and sent messages have no deletion-based undo.
Stopping or resuming a session offers the opposite lifecycle action, which does
not erase work done by the process.

`session_enter` and `cto_enter` ask one connected owner TUI to use its normal
entry path and herdr context. Optional `ui_node_id` selects that TUI's machine.
No owner TUI means an explicit error. These requests carry session IDs, not
commands or terminal arguments. `session_resume` uses the existing daemon
resume handler and does not require an owner TUI. Invites work from a CTO on
any enrolled machine; the gateway creates the existing invite result.

For team delivery, use `to_set: {"team": "finance"}` with the team name or ID. Only the CTO can send to a group: online sessions of member agents and the linked agents of member repos. The result lists recipients and members missed because they have no online session or a repo has no linked agent. Additional `to_set` filters narrow the recipients. `as_owner: true` sends to each recipient on the owner's instructions. A Team has an authority mode and held back text (see Authority along the tree). The CTO can list teams and membership with `agent_graph_read` using `typed: true`.

Changes other than the five proposal actions above apply at once. Reversible
changes include an undo action in the Inbox and Desk log. `agent_message_send`, `agent_message_reply`, `agent_owner_ask`,
`agent_learn`, `cto_ui_show` and `proposal_ask` are talk.
Every proposal tool takes an optional `reason`. The daemon validates the stored
call and keeps the concrete implementation, its authority checks and its audit
records, so a rename does not change what a call is allowed to do.

`answer` with `kind: held` takes `action` `a` (approve once), `A` (approve and
set auto), `r` (decline once) or `R` (decline and set never). `kind: outcome`
takes `action` `let_stand` or `overrule`. `kind: inbox` takes `id` and `answer`.
A charter draft ask holds all the drafted fields of one node ([decision 0115](decisions/0115-one-draft-ask-per-node.md)); the answer `accept` or `reject` applies to every field, and `accept repo:purpose` (`<level>:<field>`) decides one. For a charter draft ask (`kind: draft`), the answer is `accept` or `reject`:
accept writes the drafted value at its level exactly as the owner's TUI accept
does, records the CTO as the decider, and closes the Inbox row. No message is
sent, because a draft has no live asking session. For an approval, question,
escalation or rejection, the answer becomes a message to the asking session;
when that session is offline the answer is stored and delivered when it wakes.
`graph_change` with `action: edge_set`
takes kind `depends` or `deploys` and request rule `normal`, `auto`, `ask` or `never`.
An edge has no mode.
After typed Graph cutover, the owner can use `repo_create` and `agent_create`
with `name`, or `repo_archive` and `agent_tombstone` with a typed `ref`.
A Repo may have no checkout or owner; an Agent may have a free-text `role`.
The CTO creates identities and applies undo at once. Archive and tombstone
require an owner proposal. Peers cannot run them. `node_add` and
`node_remove` follow the same Agent lifecycle rules after cutover.

Archive and tombstone do not delete data. Active graph edges and delivery
assignments, online sessions, checkouts, pending worker starts, active delivery
changes and merger registrations block the relevant action and are listed in
its error. An Agent that owns a Repo cannot be tombstoned: reassign or clear
the owner first. An archived Repo keeps its owner. Charters, settings, offline
sessions, completed work and history remain attached. Undo restores the state
through Graph history; undoing a creation deactivates it and checks current
references. Owner-created display names must be unique within each kind,
ignoring case. Existing duplicates survive the upgrade, and automatic project
or session discovery may also introduce duplicates. Typed Graph reads return
warnings naming them for the owner to resolve. Repos and Agents may share a name.
If discovery finds a tombstoned Agent with the Repo's ID, a new Repo is left
unowned; discovery does not transfer ownership or revive the Agent.

The retained references include `projects`, `sessions`, `profiles`,
`project_policies`, `graph_charter`, `agent_charter`, `graph_node_settings`,
`graph_edges`, `graph_history`, `setting_values`, `setting_history`,
`delivery_changes`, `delivery_revisions`, `delivery_evidence`,
`delivery_step_tasks`, `delivery_step_messages`, `delivery_step_inbox`,
`project_mergers`, `worker_starts`, `worker_runs`, `inbox_items`, `desk_log`,
`learned_suggestions`, `learned_views`, `learned_message_flow`,
`charter_signals`, `findings`, `playbook` and `repos.owner_agent_id`.
No ownership, edge, delivery, charter or session is silently moved or deleted.

`graph_change` with `action: charter_set` takes the charter attributes described below, plus an optional `level` (`global`, `team`, `repo`, `agent`). `node` identifies the scope; a global write has no scope. A typed `ref` may select a team, repo or agent instead. An empty value clears the local override.
`action: team_charter` takes `team`, `attr` and `value` for inherited Team fields. `work_mode` takes `workers_code` or `on_request`; `asks_to` takes `owner` or `lead`; `lead_merges` takes `on` or `off` (empty means `off`, see [decision 0119](decisions/0119-lead-merge-grant.md)).
Node settings use `action: settings_set` with `node`, `setting` and `value`.
Team settings use `action: settings_set` with `ref` `{"kind":"team","id":"<team name or id>"}`, `setting` and `value`. A Team setting is `asks_to` (`owner` or `lead`), `lead_merges` (`on` or `off`) or `work_mode` (`workers_code` or `on_request`); an empty value clears it. Any other setting for a Team is refused with the valid names.
The tool schemas describe the other fields and fixed values.

`cto_ui_show` with `tab` switches every open local TUI to `sessions`,
`messages`, `quotas`, `graph` or `tools`, with an optional session ID, exact
session name or project node, filter text and a short reason. `cto_ui_show`
with `notice` shows a short note. Both show a `CTO:` line and return
`no TUI open` when no subscribed local TUI is connected.

In the TUI, `c` opens the CTO session from every tab, and `C` focuses the CTO box.
Its title shows the owner's CTO title, the selected harness and model.
The box is read-only: it shows the latest CTO output lines and the hint
`c: open the <title>`. When focused, the hint reads
`enter: open the <title> · ctrl+]: back`. Enter opens the full CTO harness UI through
`session_enter` on the CTO session in herdr. When herdr is unavailable, ctrl+]
returns from the terminal fallback. The title also shows how many CTO proposals are open. `n` asks before
starting a new conversation, `u` undoes the latest graph change, `m` opens the
harness and model form, and ctrl+] returns focus to Sessions. Esc skips the
current onboarding tip. The chat
polls once per second and shows recorded CTO tool calls, including reads and
failures, plus graph diffs; a graph change refreshes the Graph.

Press `5` to open Tools. It is one list of every skill, Claude Code command,
Claude Code agent and MCP server: one row per tool, with its name, status and
estimated startup token cost. MCP servers are marked with a diamond. A command
is shown as `/name` and an agent as `name (agent)`; a skill, a command and an
agent can have the same name and are separate rows. The selected row has a detail panel with its
description, estimated costs and source. Rows that need
attention come first with a `!` and the reason, such as a version that differs
between machines or two different copies on one machine. `R` rescans tool
copies. Press `t` to see skill costs per project, sorted by cost; Enter opens
the selected project and Esc returns. `[` and `]` do nothing here.

Use `j`/`k` or up/down to select, Enter to open the selected tool's actions,
`/` to filter, `n` to add, Esc to clear a nonempty filter, `?` for help and `q`
to quit. Digits keep their global tab meanings.

The filter combines words with `project:NAME`, `node:NAME` and `machine:NAME`.
A graph node is its network project, across registered checkouts. With a
project or node filter, statuses describe that scope, including tools available
to turn on. The title shows the highest estimated cost across the project's checkout
machines. It includes project skills and user-level skills observed on that
machine. The project cost table names that machine; a project with no known
machine shows `-`. Token counts are estimates. Turn on/Turn off uses the scope directly. Otherwise a picker lists
machines and projects: space ticks, `/` filters, Enter applies the selection, and Esc
cancels. Enter with nothing ticked explains that a target is required.

The action menu offers Read it, Turn on for..., Turn off for..., Remove
everywhere, Compare the copies / Keep this one, Test it and Import it as
applicable. Read it shows SKILL.md or a server's non-secret configuration and
scrolls with up/down. Stored skills can be read after their installed copies
are removed. Remove everywhere requires confirmation. Skill copies use the
existing guarded backup and removal path; all observed user variants are
backed up and a canonical version stays in the store.
A tracked repository skill uses the existing branch-and-commit removal plan.
A tracked command or agent in a repository cannot be removed this way: the plan
says to remove it in the repository.
Native MCP entries keep their import and guarded native-removal requirements.

Compare the copies opens one box with the two copies side by side. Each column
is headed `a  <harness>'s copy` or `b  <harness>'s copy` and shows the date of
its last change, how many harness setups use it, the files that differ, and,
when the daemon can make one, a model's short summary of that copy. A line under
the box says that the kept copy goes to every harness on that machine and that
the other copy is backed up for 30 days. `a` and `b` keep that copy, `v` opens
the full diff of a changed file, and Esc goes back. A chosen copy is imported
before other named, observed user copies are backed up. The host rechecks each
hash and refuses tracked, external or changed sources. The normal assignment
plan then installs the chosen copy. The plan title names the machine and
harness.

`n` offers MCP server or Skill from a folder. Choose the target machine in the
form. An MCP server takes a name, command or HTTP URL, arguments as a JSON
array, environment variable names, and optional HTTP header sources as JSON
objects with `name` and either `env` or `file`. Literal secret values are refused
by the registry. A folder import takes an absolute path containing SKILL.md;
the host packs a bounded snapshot and the gateway verifies and imports it into
the skill store. It does not execute the skill or install it until assigned.
The bottom key bar always names the current mode's keys.

These actions use owner-only daemon methods. New MCP wrappers are
`tools_mcp_add`, `tools_mcp_remove`, `tools_mcp_test` and `tools_mcp_list`.
Adding and removing a shared MCP server are owner-only: use the TUI, or the CTO
with `mcp_change`, which runs at once and leaves an Inbox notice with undo. Agent
sessions can still list and test servers.
`tools_import_folder` imports a folder on a chosen machine; `tools_keep_copy`
authorizes the exact observed copies being replaced. Agent clients, including
an agent running the owner CLI, cannot use them. Existing `tools_plan`,
`tools_apply`, `tools_import`, `tools_read_file`, MCP import and undo methods
continue to supply plans, changes and results. No setup files are added.

Graph and its node card show a Tools count. On an agent card, press `t` to open
tab 5 filtered to `node:NAME` for that node.

Press `2` to open the Graph tab, the network screen. Each node
is an agent with a lasting identity and charter. The CTO is a fixed node named
`CTO` with no charter; it appears once the owner has started the CTO. A gold
line from it is a send the owner approved as their own words; plain CTO talk is
an ordinary talk line. An edge into the CTO node carries a request rule, so you
can set `ask` or `never` for a noisy sender. Arrows, `,` and `.`, or a
click select a node. The selected node has a bracket ring on its dot in the
accent colour, its name in bold on an accent background, and its edges in the
accent colour while every other edge is dimmed, so its neighbourhood stands
out. The line above the keys reads `selected: NAME · KIND · N edges` and
follows the node when the camera zooms or pans. The graph fits itself when the
tab opens. Press `/` to find a node by identity or display name;
Enter selects the first match and Esc cancels.

With teams, the all teams view puts a staff row above the frames: owner, CTO
with its live state, and chief of staff as not set up. Each team frame shows
its lead with the authority mode, the project and session counts, and
`held back: N` when the team holds anything back. The unassigned frame reads
`answers to owner`. `f` focuses a team. Enter on a team frame opens the team
card: purpose, lead, mode, held back text, members and team rules.

Press `n` on a project node or its card to open the new session form filled from that project and machine, using its most-used harness and model. Harness and Model are switches: left and right change them to what the machine reports. Enter on a node opens its card: name, maturity, purpose and live sessions as
dots. An agent card starts with `PLACE`: its team and live lead, the mode, the
repo lead and who it reports to, and each held back item with the level that
set it (`from team` or `from repo`). Each charter value ends with its level,
for example `(team)` or `(global)`. IN and OUT
columns list edges with colored arrows and rules other than `normal`; `*`
reads `all projects`. The two columns sit side by side and each is capped at 60
columns, so a wide terminal does not push them to opposite edges; a narrow one
stacks them. The body scrolls with ctrl+u/ctrl+d, and its first
line says `Card A-B/N  ctrl+u/d scroll`. The body has one section each for `Charter`,
`Learned changes` (only when the node has some), `Loop` (only when the node has a standing loop) and `Rules`, in that order. The other set charter fields
appear under aligned labels. `Rules` lists the six effective settings, read the
way `agent_graph_read` reads them: `tests_before_merge`, `merge_authority`,
`worker_sandbox`, `judge_consumers`, `deploy_rule` and `autonomy`. Each line
ends with `maturity default` when the node's maturity set the value,
`owner override` when a valid owner setting replaced it, and `default` when
there is no maturity (autonomy is `owner override` only when it is `wake`).
The card shows unviewed automatic changes as `● N learned`, with values, times
and one wrapped evidence line (at most two lines, cut at a word with an
ellipsis). `,` and `.` select the previous or next learned change; `u` undoes it through
`graph_undo`, which refuses stale changes. Both keys are in the key line, not
in the card body. The legend lives in the help overlay under Graph, next to the
Agent card keys: it explains the edge kinds and their arrow colors, the
`normal`, `auto`, `ask` and `never` request rules, that a specific edge beats
the `all projects` wildcard, that an edge never grants deploy or merge
permission by itself, and what each charter field feeds (the per-turn context,
a neighbour's 40-character view, a worker's brief, the Tools tab, or the agent
lookup). RECENT on the last row lists the newest
changes. Leaving the card marks its current changes viewed. Esc returns to the
Graph with the same node selected. Enter on the card's sessions line opens
Sessions filtered to the agent's project. `c` focuses the CTO box; Esc
returns to Sessions.

Hidden item preferences are shared by every machine's TUI through the
gateway's `owner_settings` entry `hidden_items`. On Sessions, `h` hides or
unhides the selected session. The `v` view key includes hidden sessions as dim
rows tagged `hidden` in its last two views. The title shows `hidden N` while
preferences exist. On the card, `h` hides or unhides the selected project
node by name, and `H` includes hidden nodes when finding an agent to inspect or unhide. Session IDs stay hidden if a session comes back online. An ended
session's preference is dropped after 30 days; project preferences remain
until unhidden. The owner-only methods are `hidden_get` and `hidden_set`.

The gateway also stores onboarding progress in `owner_settings`. The owner-only
operator methods `onboarding_get`, `onboarding_set`, and `onboarding_restart`
read, update, and reset it across machines. The CTO proposes a reset with
`settings_change` area `onboarding`. The TUI shows one tip in the CTO box. `c` focuses
the box and Esc skips the current tip. In help, `T` turns tips off or on
and `R` restarts them from the first step. Progress is shared across machines.

Ports, files, environment variables, flags, and what setup changes on a machine. Paths are for the default user setup. `~` is your home directory.

## Standing orders

Standing orders are the owner's lasting instructions to the CTO. The gateway keeps them in the `standing_orders` table, so they survive CTO restarts and machine changes. Each order has an `id`, `text`, `saved_at` and `source`: `owner` for the owner's own words typed in the TUI, `cto` when the CTO saved it on the owner's instruction.

The CTO tool `cto_standing_orders` has the actions `save` (`text`), `read` (`order_id`), `list` and `remove` (`order_id`). It answers with `orders` (all, oldest first) and `order`. A save or a remove leaves an Inbox notice with an undo (remove for a save, restore for a remove). The CTO reads the orders at the start of every conversation: a Claude CTO gets them after the brief in its system prompt, an OpenCode CTO gets them in front of the first message of a new conversation.

The owner sees and edits them in the TUI, Settings, section "CTO standing orders": enter adds one, `x` removes the selected one and `u` puts the last removed one back. The operator method is `standing_orders` (`action`, `order_id`, `text`, `restore`); a save from the operator method is always source `owner`. The gateway frames are `standing.list`, `standing.save` and `standing.remove`.

## Commands

| Command | What it does |
|---|---|
| `agent-net gateway` | Run the gateway server. |
| `agent-net daemon` | Run the per-machine daemon. |
| `agent-net mcp` | Stdio MCP server for a Claude Code session. Claude Code starts it, you do not. `agent-net mcp --codex` serves Codex threads. When idle, the server switches itself to a newer installed binary while keeping the connection open. |
| `agent-net setup` | Set this machine up. See [what setup changes](#what-setup-changes-on-your-machine). |
| `agent-net invite` | On the gateway machine: print the one-line installer for a new machine. |
| `agent-net enroll` | On the gateway machine: print a raw one-time join string. |
| `agent-net join STRING` | Join this machine to a gateway with a join string. |
| `agent-net uninstall` | Remove services and integrations. |
| `agent-net status` | Daemon connection, node ID and local sessions. `--json` for JSON. |
| `agent-net list` | Sessions on the network, with their PROJECT (found from the session's folder). NAME is the project name (see [session names](#what-each-machine-reports)); duplicates are ` 2`, ` 3` and workers are `<project> worker <n>`. `--all` includes offline ones. A worker end report sets its session status to `ended`; a resume supplies its live status again. `--dormant` includes dormant ones (reachable, but quiet for 24 hours; OpenCode only). `--json` shows them always. `--harness`, `--text` filter. `--wide` adds branch, remote, folder (the session's folder), node and worktree. MODE (`foreground`, `background`, `unknown`, or `-` from an older machine) and MODEL show how the session runs. TRUST is `yes` for a session with [peer trust](#peer-trust-and-hints) on. POLICY is the [delegation policy](#delegation-policy) (`native:deepseek/deepseek-v4.1-flash(project)`), or `-`. `--wide` adds EXPERTISE. `--json`. |
| `agent-net nodes` | Machines with their agent-net version and installed harnesses (HARNESSES column). `--wide` lists each harness with version, path and the model count from the last detection at start (use `agent_machine_get` with `include: ["models"]` for the live list), and one extra row for every other copy of a harness (marked "other copy"); the primary copy is marked when there is more than one. `--json` has everything, including the model lists and each machine's worker `tracks` (see [the playbook and the track record](#the-playbook-and-the-track-record)). |
| `agent-net nodes rename NODE [NAME]` | Give a machine a display name that every machine shows, or clear it (no `NAME`). `NODE` is a node ID or a name the machine is listed under. Same as the `N` key in the TUI. See [machine names](#machine-names). |
| `agent-net projects` | Known projects, one per repository per machine: name, machine, path (the main working tree), git remote, SOURCE (`registered`, `seen` or `both`), which harnesses ran there, the linked worktrees where sessions ran, online sessions and last seen. `--name N` shows one project. `--json`. See [the project registry](#the-project-registry). |
| `agent-net projects add PATH...` | Register repositories on this machine. `--dry-run` shows what would happen. `--json`. |
| `agent-net projects scan DIR` | Register every repository under a folder on this machine. `--depth N` (default 3, at most 8), `--dry-run`, `--json`. |
| `agent-net projects remove NAME\|PATH` | Remove a registration. A name that fits two projects on this machine is refused: give the path. `--on MACHINE` removes the entry of another machine (host name or node ID), for example one that is gone. |
| `agent-net events` | On the gateway machine: the gateway's events table, newest last. `--node` (ID or host name), `--session`, `--kind` (exact, or a group like `msg`), `--since` (like `2h` or `2026-10-29T12:00:00Z`), `--limit N` (default 100), `--follow` (or `-f`), `--json`, `--data DIR`. |
| `agent-net tui` | A live terminal view of every session on the network, where you can start a session, message a session as yourself, and stop a worker. It talks to the local daemon through the operator interface, so it runs on any machine. See [the screen](#the-tui-screen). |
| `agent-net start --project NAME [--harness opencode|claude|pi|codex] [--model M] [--machine HOST] [--isolation worktree|none] "TASK"` | Start a worker: a new agent session in a known project, on the machine that holds it. It prints the worker's session ID. The task is sent to it as a message from you; its report comes back to you (see the TUI, or `agent-net messages`). In a git repository the worker gets its own git worktree on the branch `agent-net/<worker-id>` by default; `--isolation none` works in the project folder. `--model` takes a loose name such as `"deepseek 4.1"`. See [workers](#workers). |
| `agent-net stop SESSION` | Stop a worker. The process group is killed. |
| `agent-net cto [harness opencode\|claude\|model MODEL]` | Show the active CTO harness, model and compaction, or change the harness or model. An empty model selects the default. Owner only. The title is changed in the TUI or by the CTO, not on this command. |
| `agent-net workers` | The workers on the network, with machine, project, harness, model, owner, state and start time. `--json` also has the task's first line, the worker's `title` (always filled: the title the caller gave, else the first sentence of the task that is not setup, cut to 60 characters, else the worker name), and the usage figures (`context_tokens`, `context_limit`, `tokens`). `--owner-claude UUID` keeps only the workers owned by this machine's Claude Code session with that native id (the transcript file's name). It leaves out the machines' room and queued starts, which belong to the machine and not to the session: the Claude Code panel runs this call for every session every three seconds, and the queue costs a usage call to the gateway for the whole network. `--recent` also lists workers that ended in the last two minutes, with `ended_at`, `end_status` and `end_note`; without it the list and the table are unchanged. |
| `agent-net test-run [--timeout D] -- COMMAND...` | Run one local test suite through this machine's admission. The daemon grants one suite at a time per machine; a second waits in line and the command prints its position. The suite gets `GOMAXPROCS` and an appended `-p=<n>` in `GOFLAGS` from the free cores under `workers.cpu_target`, runs at nice 19 where the system allows it, and its whole process group is killed at the wall-clock timeout. The timeout is `merger.test_timeout` (default 90m); `--timeout` overrides it. The exit code is the command's own, or 124 when the timeout killed it. See [test suites on one machine](#test-suites-on-one-machine). |
| `agent-net send SESSION TEXT...` | Send a message as yourself (the owner of this machine). `SESSION` is a session ID or a unique session name. |
| `agent-net playbook set MODEL [--harness H] [--pin\|--unpin] TEXT...` | Write or replace the owner's playbook note for a model, or for a model on one harness (`H` is `opencode`, `claude`, `pi` or `codex`). With no text the note is removed. `--pin` keeps automatic learning from ever changing it. The note appears in the `agent_worker_start` or `agent-net start` result for that model, and has at most 1 KiB. `agent-net playbook get MODEL [--harness H]` prints it; `agent-net playbook pin\|unpin MODEL [--harness H]` pins or unpins the note that is stored. See [the playbook](#the-playbook-and-the-track-record). |
| `agent-net messages` | Recent messages and their states. `--session ID`, `--limit N` (default 20), `--json`. |
| `agent-net hint` | Not for you to run: the Claude Code hook that [setup can add](#what-setup-changes-on-your-machine). It reads the hook input on stdin and prints hook JSON only when it has a suggestion. See [peer trust and hints](#peer-trust-and-hints). |
| `agent-net policy-hook` | Not for you to run: the Claude Code `UserPromptSubmit`, `PostCompact` and `SessionStart` (matcher `compact`) hooks that [setup can add](#what-setup-changes-on-your-machine). It reads the hook input on stdin. On a prompt it prints hook JSON when the session has a delegation policy or its node has a charter. The full charter block is sent once per session, again when the charter changes, and again after a compaction. See [delegation policy](#delegation-policy) and [charters](#charters-and-component-relationships). |
| `agent-net needs-hook` | Not for you to run: the Claude Code `PermissionRequest`, `Notification`, `PreToolUse`, `UserPromptSubmit`, `PostToolUse` and `PostToolUseFailure` hooks that [setup can add](#what-setup-changes-on-your-machine). It tells the local daemon when the session waits for you (a permission, or the question of `AskUserQuestion` and the plan of `ExitPlanMode` at their `PreToolUse`), and clears that when you type a prompt, a tool runs after an approval, or the dialog gets its answer. See [the Inbox (tab 7)](#inbox-tab-7). |
| `agent-net version` | Print the version. |

The daemon shares concurrent identical owner reads for `workers`, `usage`, `usage_top`, `list` and `counts`. Successful results can be reused for one second. Each caller is checked separately before an owner-only result is returned.

## Graph operator methods (foundation)

Charters inherit through global defaults, Team, Repo and Agent, in that order.
The nearest set value wins, except `refuses` and `rules`: all levels contribute to their union. Each item shows once, split on semicolons and newlines, in level order (global, team, repo, agent), so a team rule still reaches a member whose repo has rules of its own ([decision 0100](decisions/0100-team-questions-go-to-the-lead.md)).
An effective charter includes `sources`, keyed by field, with each source's
`level`, `scope` and `value`. A Team supplies "ask the team lead" as its default
`escalate`. No charter is valid and adds no limit or approval prompt.

`purpose` describes the Repo or Team. `maturity` is the Repo enum `experimental`,
`early`, `growing` or `mature`; `maturity_note` is its optional explanation.
`accepts`, `refuses`, `escalate` and `feature_requests` describe Agent work.
Safety refusals on a Repo or Team always apply to its agents. `rules` holds taste
and house rules at any level; code never interprets this free text. The upgrade
moves old `consumers`, `needs` and `notes` into `rules` under matching headings,
and splits old maturity text into the enum and note. Unknown maturity words map
to `early`. The removed attributes read empty and are refused on writes.

On a Graph node or card, `e` opens the charter form at that node's level; `E`
opens global defaults. The form names the level, shows field sources, and saves
one selected field with Enter. Empty text clears the local override. Inherited
text is not copied into a local override when opening the form.

The gateway stores explicit nodes, repo charter attributes, edges and graph history in `graph_nodes`, `graph_charter`, `graph_edges` and `graph_history`. Global and Team fields use `charter_values`; Team purpose remains in `teams.purpose`. Agent charter attributes and their history are stored separately in `agent_charter` and `agent_charter_history`. Node settings, including the worker sandbox, use the settings layer and its history. Old node and project-policy worker values remain as a downgrade backup. Git working trees reported by sessions and registered projects also appear as implicit component nodes, except roots in temporary folders or the home folder itself. Registrations are checked for Git by the reporting daemon. The following local methods are behind the owner gate and relay to the gateway. Agents cannot call them.

The gateway stores one repo context and an agent identity for each lasting session. It defaults the agent to the repo owner. In Sessions, select a running session and press right until the AGENT cell appears; Space cycles through the available agents. The agent's charter and governance edges follow that selection, while the session keeps its repo and any merger assignment. The owner can change this field; sessions cannot. The gateway records each change, including the old agent, new agent and owner session, in `session_agent_history`. The owner-only daemon methods `session_agent_list {}` and `session_agent_set {session_id, agent}` list choices and set the agent by its ID.

A worker session belongs to its run instead; the gateway records the caller's agent when the worker starts and keeps it on the completed run. Owner pseudo-sessions have no agent, and the CTO uses the CTO agent. The legacy project strings remain available to older daemons.

The owner can use `agent-net charter show <node>`, `charter set <node> <attr> <text...>` (omit text to clear), `charter history <node>`, `graph`, `graph undo [history-id]`, `node add-architect <name>`, `node remove <name>`, `node settings <name> [setting value...]`, `edge set <from> <kind> <to> [rule]` and `edge remove <from> <kind> <to>`. Charter attributes are `purpose`, `maturity`, `maturity_note`, `accepts`, `refuses`, `feature_requests`, `escalate`, `rules` and `work_mode`. Edge kinds are `depends` (consumer to provider) and `deploys` (deployer to each app it deploys). Delivery assignments also use `accepts`, `reviews`, `approves` and `approves_deploy` edges. The owner can assign steps with `delivery_owner_set`.

Agent list and get include the public charter fields purpose, maturity, accepts, refuses and feature_requests. List values are limited to 300 characters each. When purpose is empty, the project's AGENTS.md description is used, marked with `purpose_fallback`. `agent_session_get` also carries the session's node rules in `charter.rules`, the same name/value pairs `agent_graph_read` returns, so a session can check its own rules directly, including its autonomy (`off` or `wake`). The list stays compact and does not carry them.

- `graph_get {}` returns `nodes` and `edges`, hiding unregistered empty implicit projects without a live or recently seen session for seven days. Each project node includes registered and observed `locations` (machine and path), up to five live `sessions` (id, name, machine and status), and `session_count`. `graph_get {"all":true}` includes those projects. The Graph uses the same default filter.
- `charter_set {node, attr, value}` sets an attribute; an empty value clears it. `charter_history {node, limit}` returns newest changes first.
- `node_add {name}` and `node_remove {name}` add or remove an architect. Removal requires no edges, and clears the node's settings with it.
- `edge_set {from, to, kind, mode, rule}` and `edge_remove {from, to, kind}` change an edge. `edge_history {node, limit}` returns changes involving that node, or all edges if node is empty.
- `node_settings_get {node}` returns the node's maturity, its overrides, the defaults its maturity gives, the effective values and `autonomy`.
- `node_settings_set {node, setting, value}` sets or clears one override. Settings are `tests_before_merge` (`optional`, `suite`, `suite-new-tests`, `suite-new-tests-review`), `merge_authority` (`full`, `delegated`, `ask`), `worker_sandbox` (`sandboxed`, `no-network`, `read-only`), `judge_consumers` (`free`, `charter`, `strict`, `escalate`), `deploy_rule` (`brief`, `ask`, `owner`), `autonomy` (`off`, `wake`), `wake_on_message` (`on`, `off`), `loop_every` (a duration above zero, such as `4h`), `loop_prompt` (free text), `loop_mode` (`always`, `on_change`), `loop_plan` (a relative path) and `work_mode` (`workers_code`, `on_request`; stored as the repo level charter attribute, see Work mode). An empty value clears the override; autonomy is off when unset, wake_on_message is on when unset, and a loop is off when `loop_every` is unset. The CTO sets `wake_on_message`, `loop_every` and `loop_prompt` without a proposal (minor / advice). The method is behind the owner gate and refuses agent sessions. The effective values, the autonomy and the delegation policies are readable by every agent through `agent_graph_read` and `agent_session_get`; only the change is owner-gated.
- `graph_undo {id}` reverts a history row, or the latest row if id is omitted. Undo is recorded as another change and refuses a stale value. Node undo refuses to remove a node with a charter, settings or edges. Old graph history for a moved node setting cannot be undone through graph undo; use settings history for later changes.

History limits default to 1000 and are capped at 1000. Edge history has `old` and `new` edge objects, omitted when absent. Gateway frames use the same names with dots instead of underscores; `graph.undo` uses `history_id` because `id` identifies the frame itself; `node_settings_get` and `node_settings_set` use `node.settings.get` and `node.settings.set`.

## Playbook operator methods

The owner's notes about models are behind the owner gate: only the owner's command line or TUI reads or writes them, never an agent session. The store is one row per subject and optional harness.

- `playbook_set {model, harness, note, pin}` writes an owner note. An empty `harness` applies the note to every harness of that model. An empty `note` removes the entry. `pin` keeps automatic learning from changing it.
- `playbook_get {model, harness}` answers `{note}` with the note for that harness, or the model-wide note when there is none for the harness; `note` is absent when nothing is stored.
- `agent_learn {subject_kind, subject, finding, evidence}` records one finding from an agent session. It is not an owner method and the command line is refused. See [findings and the automatic playbook](#findings-and-the-automatic-playbook).
- `agent_charter_draft {level, attr, value, team}` proposes one charter value drafted from AGENTS.md ([decision 0088](decisions/0088-charter-drafts.md)). It is open to agent sessions and refused for the command line. `level` is `repo` (purpose, maturity, refuses), `agent` (accepts, refuses, escalate, feature_requests, rules) or `team` (purpose, refuses; the caller must lead the team, and `team` names it only when it leads several). The scope is always the caller's own, never a parameter. The draft is stored as pending and changes no charter. The owner accepts or rejects it in the Inbox (`learned_answer` with the negative draft ID) or on the charter card (`y`, `x`); the CTO accepts or rejects the Inbox ask with `answer` `kind: inbox` and `answer: accept` or `reject`. Accept writes the value at its level with the normal charter write. The daemon frame is `charter.draft`; the gateway asks for a draft with `draft_request` in the `policy.get` answer, once per AGENTS.md hash.
- `agent_permission_propose {kind, rules, targets, machine, reason}` proposes Claude Code permission allow rules or folder trust for the owner to approve ([decision 0127](decisions/0127-claude-code-permission-proposals.md)). It is open to every session, including the CTO, and refused for the command line. `kind` is `permissions` (the default) or `trust`. For `permissions`, `rules` are Claude Code permission rule strings such as `Bash(gh pr merge *)` (only `permissions.allow` entries), and `targets` are registered repo or project names resolved through the project registry to a machine and a path; a name that does not resolve is refused and named. For `trust`, `machine` names one enrolled machine and `targets` are exact absolute folder paths, or paths under `~/`. `reason` says why. Nothing is written: the gateway stores the proposal and opens one owner-only ask, route-fixed to the owner so the chief of staff never adopts it. The ask lists every rule or folder, every target (repo or folder, machine, path), the reason and who proposed it. Approve and Reject are the only answers, and only the owner may give them. On approve the gateway tells each target machine's daemon to apply. For `permissions` it merges the rules into `<repo>/.claude/settings.local.json` `permissions.allow`: the file is created when missing, every other key and rule stays, no duplicate is added, and the write is atomic. When no ignore rule covers the file, the daemon adds `/.claude/settings.local.json` to `.git/info/exclude`; nothing is committed. For `trust` it calls the same folder trust writer the owner's `session_change` trust uses. The approval writes one Desk log line with the result per target and an Undo action; `permission_undo {id}` removes only the rules, or the trust, that approval added and is also owner-only. A Claude session already running in that repo may need a restart, or `/hooks` opened once, to load new rules. No agent, the CTO included, can apply a proposal without the owner's answer. The gateway also raises a `trust` ask by itself when a session start, resume or wake answers `needs_trust`: one open ask per machine and folder, with the folder, machine and session in the reason. The daemon frames are `permission.propose`, `permission.apply` and `permission.revoke`; the gateway frame for the undo is `permission.undo`.
- `agent_worker_rate {worker, score, note, kind, defect}` records the caller's rating of one finished worker: `score` is 1 to 5 and `note` is one optional line on why. `kind` is `rating` (the default when empty), `defect_found_later`, `brief_defect` or `move_to_brief_defect`. A defect report takes no score, a required `severity` from 1 to 4 (1 cosmetic or test-only, caught before release; 2 a visible annoyance or a latent performance problem that had not hit the owner yet; 3 a broken feature or a real performance regression in a release; 4 an outage, data loss or a security problem), and one line about the defect; each report is one defect, it may come any time after the worker's hand-back (also after a rating), and it adds its severity in points of rework to that worker's task. It is not an owner method and the command line is refused, but the owner has the separate `worker_defect` operator call below. Only the session that started the worker may rate it or report a defect on it; the owner may also report a defect. agent-net records some defects by itself, so do not file those (see **Defects agent-net records** under [workers](#workers)). Each worker takes one rating. The record is stored with the run's model, harness, effort, difficulty and caller, never with the task text beyond the note. A `brief_defect` takes no score, a required severity from 1 to 4 and one line about the bad brief; it is charged to the session that wrote the brief (the worker's caller) and never to the worker, so the worker's quality data does not change. A `defect_found_later` answer returns the new row id in `defect`. `move_to_brief_defect` reclassifies one of the caller's late defects on that worker: give the worker and the `defect` id, or leave `defect` out when exactly one unmoved defect the caller filed on that worker is left. If several match, the refusal lists each id with its severity, UTC date and the first 60 characters of its note. Only the session that filed the row may move it, the original row stays and records who moved it, when and the new row, and its severity stops counting against the worker. See [workers](#workers).

The model is required, and the note is at most 1024 bytes after cleaning (control characters other than newline and tab are removed). Gateway frames use `playbook.set`, `playbook.get` and `learn.add`. The CLI is `agent-net playbook set|get|pin|unpin`.

## Phone app

The web console is the owner's browser and phone interface. It uses the
connected apps listener and the gateway host's daemon ([decision
0122](decisions/0122-web-console.md)). The gateway and daemon must run under the
same user. The console uses the daemon's socket and the same operator calls as
the TUI. Actions run as the owner.

The console has six screens: Desk (`/`, key `1`), Sessions (`/sessions`, `2`),
Graph (`/graph`, `3`), Changes (`/changes`, `4`), Messages (`/messages`, `5`)
and Workers (`/workers`, `6`). On a desk they sit in a left rail. Under 760px
wide they sit in a bottom bar. The top bar has the title, a live dot and the CTO
button, which opens the owner's conversation with the CTO (key `c`). The panel
is a third column at 1180px and wider, an overlay below that, and a full-height
sheet on a phone. It stays open across screen changes, remembered in
`localStorage` and in a cookie of the same name.

- **Desk** shows Needs you (the open asks of the TUI Desk, with the
  recommendation and the "why you" line), In flight (open delivery changes that
  moved in the last day, read only), Handled for you and the Log. The owner
  answers an ask with the buttons of its kind: approve, reject, dismiss, an
  option, typed text, let stand or overrule, and "didn't need me". Proposals,
  learned suggestions, charter drafts and delivery steps are answered in the
  TUI. A daily brief appears as `Daily brief <date>` and offers Dismiss only.
- **Sessions** shows the machines (online, version, cpu, memory) and the
  sessions grouped by needs you, working, idle and offline. The detail panel
  shows the task, machine, project, model, place, charter purpose and recent
  conversations. It is read only. A session is opened from the TUI.
- **Graph** (`/graph?s=<node>`) draws the same read as the TUI Graph tab
  (`graph_view` with places and teams) as a server-rendered SVG. Team boxes
  carry the team name, its lead and the team mode. The owner, the CTO and the
  chief of staff sit above them, and records with no team sit in an "answers
  to you" box. Each node shows needs you, busy, idle or offline. Depends edges
  are solid; conversations whose newest message is in the last hour are dashed.
  A click selects a node in the address and fills the detail card with the
  name, harness and model, place (team, lead, mode), machine, step owners and
  charter purpose. The graph scrolls sideways inside its own box on a phone.
  It has no edit action: places and charters are edited in the TUI.
- **Workers** shows subscriptions (each provider, plan and window with the
  percent left as a bar, the pace, the reset time and a protected chip, read
  with `usage`), the model picks (model, harness, score, the T and S to V
  rotation columns and the used and done counts, from the registered profiles,
  `scores_get` and `worker_rotation_get`) and the workers that are running now
  (name, model, task, run time and tokens, from `worker_log`; the cost shows a
  dash because a run in progress has none). It is read only: protecting a
  provider and starting or stopping a worker stay in the TUI.
- **Changes** (`/changes?f=open|mine|all&s=<change>`) lists the delivery changes
  of the TUI Delivery tab, read with `changes_owner_find`. Each row has the
  title, the id, the revision, the risk, the step strip, a status line and the
  age. The filters are open (open and merged changes), mine to do (the change
  needs the owner: a step that needs the owner, or a step the owner owns) and
  all (also done and stopped changes, 50 of each). The detail panel shows the
  id, revision, risk, author and its model, caller, branch and head, and each
  step with its state, owner, who reported it and the evidence note. It has no
  button: a step is claimed, reported and approved from the TUI or by the
  agents.
- **Messages** (`/messages?s=<conversation>`) lists the newest 50 stored
  conversations (`conversations`) with the two sides, the last line or worker
  task, the age, the message count and a CTO chip. The thread reads the stored
  messages (`conversation_messages`, up to 200, oldest first). Worker notices
  and system lines are dimmed. The composer sends the owner's words to a
  session of that conversation with the same owner send as the TUI, so the
  message arrives marked as the owner, with the conversation id, so it stays in
  that thread. A worker row groups several conversations, so a reply there goes
  to the newest one that has the chosen session as a side.
- **CTO** (the side panel, key `c`) reads `cto_settings` for the title, harness
  and model, `cto_events` for the same timeline as the TUI CTO pane and
  `proposal_list` for the open proposals. The header shows the title from the
  setting and the harness and model. Owner lines say "you"; CTO lines are
  styled as the CTO and harness actions are dimmed. The model's text is text:
  it is escaped, markdown is not rendered, and its line breaks stay. An Older
  messages link reads the page before the current offset, and Latest returns to
  the newest page. The composer sends with `cto_send`, the call the TUI makes,
  so the words arrive from the owner in person and the daemon starts or wakes
  the CTO harness when it is not running. An open proposal is a card with Apply
  and Decline, answered with the TUI's owner action (`proposal_answer`, `a` or
  `x`). The card carries the machine as an encoded value, so no node ID is
  printed. The panel cannot start a worker or change a charter or setting; the
  CTO itself may do those when the owner asks it in the chat.
- The console is read and answer only. Its one write route is `POST /action`
  with the kinds `held`, `outcome`, `inbox`, `mark`, `send`, `reply`,
  `cto_send` and `proposal`. A `reply` takes the conversation id (`id`), the
  recipient session (`to`) and the text (`action`); the recipient must be a
  side of that conversation, and the text is at most 64 KiB. A `cto_send`
  takes the owner's words in `text`. A `proposal` takes the proposal id (`id`),
  an encoded machine target (`node`) and `action` `a` to apply or `x` to
  decline. There is no route that starts or stops sessions or workers, that
  claims, reports or approves a step, or that changes charters, teams, edges or
  settings.
- `GET /events?screen=<screen>[&s=<row>][&f=<filter>]` is a server-sent event
  stream that sends the screen again when it changes. It reads only what that
  screen shows: the Graph screen reads the graph, the chief status and the
  conversations of the last hour; the Workers screen reads the quota report
  and the worker snapshot. `GET /events?section=cto&screen=<screen>&h=<offset>`
  is the CTO panel's own section: it sends the panel body as `event: cto` to its
  own id and reads only the CTO timeline and proposals, so the screen and the
  panel update independently. An update is skipped while the owner is typing.
- The pages load nothing from outside the gateway.

For a manually configured gateway, pass `--apps-listen 127.0.0.1:7422` and
`--apps-public-url https://apps.example.test` to `agent-net gateway`. Point a
trusted HTTPS tunnel at that loopback listener and use its actual public URL in
the flag. The listen flag sets only the address. Turn on Connected apps in the
TUI to start it; a saved off setting stays off across restarts. Open that URL on
the phone. In the TUI, press `S`, choose Connected
apps, press `a`, and enter the code on the phone. Check the client name and
approve the owner app. In Safari, use Share, then Add to Home Screen. Revoke a
device in Connected apps to sign it out. A paired device expires 90 days after
issue or renewal; use with less than 30 days left renews it. See the [phone
security boundary](security.md#phone-owner-identity).

The old `agent-net phone owner` command and `--phone-listen` flag are removed.
Remove the flag from any manually edited service command. The console refreshes
its open screen while it is open. It does not send push notifications.

## Ports

| Port | Used by |
|---|---|
| TCP 7422 | Example apps HTTP listener, bound only to 127.0.0.1 when enabled. The port is configurable. |
| TCP 7420 | The gateway. HTTPS and WebSocket, on `0.0.0.0` by default. Every daemon connects to it. Open it to your other machines and to nothing else. If reachable on a public routable address, the gateway logs a warning and surfaces it in `agent-net nodes` and the TUI status line. Enrollment (`/v1/enroll`) is rate limited to 10 attempts per 10 minutes per IP and 60 per 10 minutes globally (answers 429). |

The daemon and the agents talk over Unix sockets, not network ports.

## Gateway link health

After `session.sync`, and after a settings revision changes, the gateway sends
`settings.sync` to connected daemons. The frame carries the revision, recipe
hash, stored values and repo maturity facts. A daemon saves the last valid
snapshot in `daemon.db`; older daemons ignore the frame. Before the first
snapshot, the embedded recipe supplies defaults. Guidance and delivery readers use
this snapshot.

Each daemon and the gateway send a WebSocket ping every 20 seconds. Incoming
messages, pings and pongs show that the link is alive. After 45 seconds without
incoming traffic, either side closes the link. The daemon reconnects with its
usual backoff. The machine shows offline when the gateway drops its connection;
its sessions go offline after a grace period of at most 10 seconds, unless the
machine reconnects first. A reconnect syncs the sessions again.

## Files

| What | Default path | Notes |
|---|---|---|
| Node credentials | `~/.config/agent-net/node.json` | Gateway URL, node ID, token, CA fingerprint. Mode 0600. Optional `"hostname"` pins the machine name (see below). `"no_auto_upgrade": true` disables daemon catch-up upgrades. Optional `"quota_axi"` is an absolute path to quota-axi when it is not on `PATH` or in `~/.local/bin`. |
| Pinned CA | `~/.config/agent-net/ca.pem` | The gateway's CA, trusted after the fingerprint check. |
| Daemon state | `~/.local/state/agent-net/daemon/daemon.db` | SQLite. Delivery records (for duplicates and interrupted writes), the `claude_deliveries` table (the messages delivered to Claude sessions, to match their replies, see [Claude replies](#claude-replies)), the outbox for sends made while the gateway is away, the `settings_snapshot` table (the last validated settings snapshot for offline use), and the `workers` table (process ID, start time, owner, worktree, task line) that lets the daemon take its workers back after a restart. A delivery record is removed after its message expiry plus seven days (see [Housekeeping](#housekeeping)). Mode 0600, like its `-wal` and `-shm` files. |
| Worker folders | `~/.local/state/agent-net/daemon/workers/<worker id>/` | The worker's private files: for OpenCode its private data and state folders and session file, for Pi the named pipe used as its standard input, for Codex the capability token of its app server (mode 0600). Also the output log of the harness. A worker in the OS sandbox also has `os-sandbox` (the wrapper's name), `tmp` (its private `/tmp`) and, for Pi, `pi-agent` (its private agent folder with a copy of the Pi credentials, made new at each start). Kept after hand-back for resume, otherwise deleted when the worker ends. OpenCode configuration remains shared. At each start OpenCode credentials are exported and imported through memory into the private database (mode 0600); OAuth token refresh in the copy may affect the original login. |
| Worker worktrees | `~/.local/state/agent-net/daemon/worktrees/<worker id>/` | The default in a git repository (not with `--isolation none`). Eligible for the worker cleanup described below as soon as the worker ends. The default branch name is `agent-net/<worker id>` in the project; `delivery.worker_branch_prefix` sets the prefix. A new worktree starts from the newest commit of the project's upstream: when the checked-out branch has a remote upstream, the daemon runs `git fetch` for it (10 seconds at most) and starts from the fetched tip if local HEAD is an ancestor of it. With unpushed or diverged commits, no upstream, a detached HEAD or a failed fetch, it starts from local HEAD and logs why. It never changes your checkout or local branches. A requested `base` is always the local branch. |
| Worker Go build cache | `/tmp/agent-net-gocache` | Shared Go build cache for workers. Trimmed hourly by the daemon. Entries unused for longer than `workers.cache_max_age` (default 24h) are removed. A size cap, `workers.cache_max_bytes` (default 15 GiB), trims the oldest entries across this cache and the owner's `~/.cache/go-build` down to `workers.cache_low_bytes` (default 10 GiB). |
| Owner Go build cache | `~/.cache/go-build` | The Go build cache of your own interactive builds. The daemon never trims it by age. It counts against the same size cap as the worker cache and is trimmed oldest first when the two are over the cap. README, trim.txt and the folder layout are kept, and only a folder holding a Go cache README is trimmed. |
| Worker test temp folders | `/tmp/agent-net-gotmp` | Build and test temporary folders created by worker test runs. Cleaned hourly by the daemon. Subfolders older than `workers.tmp_max_age` (default 6h) are removed. |
| Owner session worktrees | Next to the project, `<project>.worktrees/<branch>/`, for example `~/code/agent-net.worktrees/agent-net/agent-net-1/` | A New session start with Folder `new worktree`. The branch is what the Branch line says, by default `agent-net/<project>-<n>` with the lowest free number. The worktree and its branch are yours: agent-net never removes them, and worker cleanup never touches them. |
| Repo skill removal worktrees | `~/.local/state/agent-net/daemon/repo-removals/<id>/` | Temporary worktrees used to make an owner-approved skill removal branch. Removed after the commit; the `agent-net/remove-<skill>` branch remains in the project's Git repository. |
| Daemon socket | `$XDG_RUNTIME_DIR/agent-net/daemon.sock`, else `/tmp/agent-net-<uid>/daemon.sock` | Directory 0700, socket 0600. macOS has no `XDG_RUNTIME_DIR`, so it uses the `/tmp` path. |
| Claude hint hook record | `~/.config/agent-net/claude-hint-hook.json` | Whether you were asked, and whether agent-net added the hook (and to which settings file). |
| Claude needs hook record | `~/.config/agent-net/claude-needs-hook.json` | Whether you were asked, and whether agent-net added the hooks (and to which settings file). |
| Claude workers plugin record | `~/.config/agent-net/claude-workers-mod.json` | Whether you were asked, whether agent-net installed the plugin, and the marketplace folder. |
| Integration local answers | `~/.config/agent-net/integrations-local.json` | The per-item `--...` answers this machine made for itself. They win over the network answers. |
| Integration applied record | `~/.config/agent-net/integrations-applied.json` | The last answers this machine applied, so a daemon start does not repeat the work. |
| Claude workers marketplace | `~/.local/share/agent-net/claude-plugins/` | The local marketplace and the `agent-net-workers` plugin files, written from the binary. Claude Code reads the plugin from this folder. |
| quota-axi auth check | `~/.config/agent-net/quota-axi-auth.json` | Provider status and Keychain prompt outcome, without credentials or source paths. Uninstall removes it. |
| quota-axi choice | `~/.config/agent-net/quota-axi-choice.json` | Selected executable path, version, and reason. Setup and the daemon keep a working choice; uninstall removes the record. |
| quota-axi private copy | `~/.local/share/agent-net/quota-axi/` | The pinned npm package `quota-axi`, installed by setup or the daemon when the Quota integration is on and you have no quota-axi. The daemon uses it when no usable owner copy is selected. Removed by uninstall and when the item is turned off. |
| Skill manifest | `~/.local/state/agent-net/daemon/skill-manifest.json` | Paths, hashes, revisions and ownership of skill, command and agent copies the daemon made (the path is the folder of a skill and the file of a command or agent), and the Git exclude lines it owns. Mode 0600, owned by you. Kept until uninstall, which also releases the owned exclude lines. |
| Skill copies | The native skill folders of each harness (for example `~/.claude/skills/<name>/`, `~/.agents/skills/<name>/`, and `<checkout>/.agents/skills/<name>/` for a project), and for a command or an agent the file `~/.claude/commands/<name>.md`, `~/.claude/agents/<name>.md`, `<checkout>/.claude/commands/<name>.md` or `<checkout>/.claude/agents/<name>.md` | A skill the owner assigned. Written by the daemon as your user. Uninstall keeps them: they are your skills now, and removing them could lose work. Undo removes the ones the manifest says agent-net made. |
| Git exclude lines | `<checkout>/.git/info/exclude` (the path from `git rev-parse --git-path info/exclude`, shared by linked worktrees) | One line per project skill copy, added only by the daemon. A skill line ends with a slash (`/.claude/skills/name/`). A command or agent line names the file and has no slash (`/.claude/commands/name.md`), because a slash would match only a folder. Each line is reference-counted in the manifest. Uninstall removes the owned lines and leaves the rest of the file. `.gitignore` and the Git index are never changed. |
| Temporary files | `~/.local/state/agent-net/daemon/.skill-manifest-*`, `.project-verify-*`, `.project-ignore-*`; `.claude.json.lock` (a folder, next to `~/.claude.json`) and `<config>.agent-net-*` (next to a native MCP file) | Short-lived. The daemon deletes them when the step ends. A crash can leave one: uninstall removes the ones in the state folder. `.claude.json.lock` is Claude Code's own lock folder, taken for the length of one edit. A lock folder untouched for 30 seconds was left by a Claude that crashed, and the daemon removes it; trusting a folder that is already trusted takes no lock. |
| Skill backups | `~/.local/state/agent-net/daemon/skill-backups/<item hash>/<time>-<id>/` | Exact copy of a skill directory before the daemon removed it. Local only, never uploaded or shown. Folders 0700, files 0600. At most 20 per item and none older than 30 days (pruned after each removal and at daemon start). Uninstall removes them. |
| MCP config backups | `~/.local/state/agent-net/daemon/mcp-backups/<item hash>/<time>-<id>/` | The whole native MCP config file (`preimage`) as it was before the daemon removed an entry or added one for a project coverage row, and `meta.json` (file path, hashes, entry name). A backup can hold secrets: local only, never uploaded or shown. Folders 0700, files 0600. At most 20 per item and none older than 30 days. Uninstall removes them. |
| MCP coverage undo record | `~/.local/state/agent-net/daemon/mcp-coverage-undo-<plan id>.json` | The files one applied coverage plan wrote, their before and after hashes, the backup IDs, and whether the file was created. Mode 0600. Removed when the owner undoes the plan. It never holds a declaration or an environment value. Uninstall removes it with the state folder. |
| Shared MCP server records | `~/.local/state/agent-net/daemon/mcp-pids/<pid>` | One short file for each stdio server the MCP host started: the pid of the server (also its process group) and its start time. A hosted server runs in its own process group. When the daemon stops, or a server ends or is replaced, the host sends SIGTERM to the group, then SIGKILL after 2 seconds, so programs the server started (uv starts python, npx starts node) end with it. The unit has `KillMode=process`, so systemd would not end them. If the daemon died before it could, the next daemon ends the groups named in these files at start. It checks the start time first and never touches a process it did not start. Uninstall removes the folder with the state folder. |
| Skill journal | `~/.local/state/agent-net/daemon/skill-journal/*.json` | One small record per skill operation that is running. Written before the first change and deleted when the operation ends. Mode 0600, folder 0700. A record left after a crash is read at the next daemon start. Uninstall removes the folder. |
| Reply sockets | `<socket directory>/r/<session id>.<conversation id>.sock` and `<socket directory>/r/<session id>.sock` | One per sending session and conversation, used to catch Claude Code's native replies (see [Claude replies](#claude-replies)). The socket of a conversation is closed when its rows leave `daemon.db`. The one per sending session is for messages without a conversation and for frames from an older daemon. Mode 0600. |
| Gateway data | `~/.local/state/agent-net/gateway/` | `funnel.json` records the port and loopback target managed by agent-net so uninstall can safely remove its Funnel route; it is removed when cleanup succeeds or the port is repurposed. `tailscale-node.json` (mode 0600) holds the `--apps-tailscale-name` choice: the name and, if given, the auth key file path, never the key. `tailscale/` (mode 0700) is the state of agent-net's own Tailscale node, with its private keys; `tailscale/agent-net-owned.json` marks it as agent-net's. `ca.pem`, `ca.key`, `server-key.pem`, `gateway.db` (SQLite, with `-wal` and `-shm` files; it also holds the tools inventory, assignments, plans, audit, MCP registry, the `repos` and `agents` identity tables, the `setting_revision`, `setting_values` and append-only `setting_history` tables, and the delivery pipeline's `delivery_changes`, `delivery_revisions`, `delivery_revision_stats`, append-only `delivery_evidence`, `delivery_step_tasks`, `delivery_step_messages`, `delivery_step_inbox`, `delivery_step_delegates` and `delivery_settings_legacy` tables), `admin.json` (mode 0600: the local admin URL and token) and `skills.git` (mode 0700: the private bare Git repository of imported skills, which can hold secrets from skill files). Uninstall keeps the gateway data, even with `--purge`, apart from the Funnel route record after cleanup and agent-net's own Tailscale node. **Back this folder up** with `agent-net gateway backup --out /absolute/new-directory`; it writes a consistent snapshot while the gateway is running. A backup holds the CA key, so keep it private. Without it every machine has to join again. See [security.md](security.md#what-the-gateway-keeps). |
| Gateway transfer parts | `~/.local/state/agent-net/gateway/transfers/<upload id>/` | One file per part of an upload between machines, named by its part number. Folders 0700, files 0600. At most 100 MB per upload and 1 GB staged for each sending machine. A part file is deleted when the receiving machine confirms the whole file, or 7 days after the upload began. A `gateway backup` does not copy parts that are still in flight. Uninstall keeps them with the rest of the gateway data. See [security.md](security.md#files-between-machines). |
| Received files | `~/.local/state/agent-net/daemon/inbox/<session>/<message>/<name>` | The files that came with a message to a session of this machine (see [Files between machines](#files-between-machines)). Folders 0700, files 0600. The daemon deletes a message's folder 7 days after its last file arrived. Uninstall keeps them with the state folder; `--purge` removes them. |
| File send snapshots | `~/.local/state/agent-net/daemon/files/outgoing/o_*/` | A copy of each file being sent, taken while hashing, so the bytes sent match the manifest. Deleted when the send ends; a crash leftover is deleted after a day. |
| Same-machine file handoff | `~/.local/state/agent-net/daemon/files/local/` | A file sent between two sessions of this machine waits here, under its unique name, until the receiving side takes it into the inbox. Deleted after 7 days if nobody takes it. |
| Taildrop staging (Linux) | `~/.local/state/agent-net/daemon/files/taildrop/` | `tailscale file get` writes here while the daemon collects files. Announced files go to the inbox; every other file goes to `~/Downloads` at once. |
| Gateway binaries | `~/.local/share/agent-net/dist/agent-net-<os>-<arch>` | The four binaries the gateway hands out to new machines. |
| Signed release manifest | `~/.local/share/agent-net/dist/checksums.txt`, `checksums.txt.sig` | Original checksum file and Ed25519 signature served to upgrading nodes. Setup removes them before changing a dist binary; gateway rollback also clears them before running older setup. Kept on uninstall like the gateway binaries. |
| Binary | `~/.local/bin/agent-net` | |
| Claude Code sessions (read) | `~/.claude/sessions/` | The daemon reads one file per live session. It never writes here. |
| Linux services | `~/.config/systemd/user/agent-net-daemon.service`, `agent-net-gateway.service` | systemd user units. |
| macOS services | `~/Library/LaunchAgents/com.agent-net.daemon.plist`, `com.agent-net.gateway.plist` | launchd agents. |
| Linux logs | `journalctl --user -u agent-net-daemon` (and `-u agent-net-gateway`) | journald rotates them. `agent_machine_log` reads either service on an online machine. |
| macOS logs | `~/Library/Logs/agent-net-daemon.log`, `agent-net-gateway.log` | `agent_machine_log` includes rotated files. launchd does not rotate them, so each process rotates its own file at 10 MB and keeps `agent-net-daemon.log.1` to `.5` (see [Housekeeping](#housekeeping)). |

**Schema version guard.** The gateway refuses to open a database written by a newer agent-net. The error is `gateway.db schema version N is newer than this agent-net (knows M); upgrade agent-net or restore the backup taken before the upgrade`, and the process exits instead of serving requests. An older binary cannot run a newer schema safely: it would ignore tables, columns and triggers it does not know and could drop the rules the newer code enforced. Rolling back after a release that changed the schema means stopping the gateway and restoring the backup taken before that upgrade: `agent-net gateway restore --backup /path/to/backup --to /path/to/new-data`, then start the gateway with that new data directory. A backup made by a newer release is refused in the same way. The message appears in the gateway log: the systemd journal on Linux (`journalctl --user -u agent-net-gateway`) or `~/Library/Logs/agent-net-gateway.log` on macOS.

**Machine name.** Each daemon reports a name for its machine on every start. If `node.json` has `"hostname": "mac-mini"`, that name is used. Otherwise, on macOS it is the first of these that is set, cut at the first dot: `scutil --get HostName`, `scutil --get LocalHostName` (System Settings, Sharing), then `os.Hostname()`. macOS changes `os.Hostname()` with the network (`Laptop-1.local`, `Laptop1`), so it is not used while a setting exists. On Linux it is `os.Hostname()` as it is. To show a shorter name, rename the machine once from the TUI (`N`) or with `agent-net nodes rename`: nothing needs to change on the machine itself (see [machine names](#machine-names)). `enter_ssh_hosts` keys match the name the machine is listed under. The gateway's own TLS and URL host (`enroll --host`, `invite`) is a network address and still comes from `os.Hostname()`.

## Files between machines

A message can carry files (decision [0054](decisions/0054-files-between-machines.md)). The message holds a manifest, `files`: for each file its `id`, `name`, `size` and `sha256`. The bytes travel apart from the message. Agent sessions can use `agent_file_send`; workers can add files to `agent_handback`. The TUI Send file flow comes later.

**Session tool.** `agent_file_send(to, paths, note)` sends files with a note to one session, named by ID or exact online name. `paths` is a nonempty list of absolute file paths on the sender's machine. The result gives `message_id` and, for each file, `name` and `path` relative to the receiving machine's agent-net state directory. These are planned destinations; delivery can still be pending. The receiver gets the actual folder in the message. If a transfer fails after the message was stored, the result includes `error` and its message ID. If the local connection drops, the tool does not retry: check messages before sending again. Workers use `agent_handback` instead.

**Worker hand-back.** `agent_handback(report, status, complete, files)` accepts an optional `files` list of absolute paths. Every file must be inside that worker's own worktree, including after resolving symlinks. A worker without its own worktree cannot send files. The usual credential exclusions, secret scan and size limits apply, with no worker override. The files accompany the notice to the caller, whose message names the receiving folder. If the message was stored but a transfer fails, the error names its message ID; check that message before trying again. A partial hand-back that triggers an automatic continue sends no notice yet. Antigravity acknowledges its hand-back before its turn ends and sends the prepared files with the notice after the result event. If the daemon restarts before sending the notice, it checks and prepares the files again from their paths.

The Pi and OpenCode plugins provide both tools from their generated catalog. Claude Code and Antigravity use the agent-net MCP server. Normal Codex sessions use the MCP server too; Codex workers have agent-net MCP disabled and hand back automatically, so they cannot attach files through this tool.

**Sending.** The daemon of the sending session:

1. checks each path: an absolute path to a regular file, in the session's project or worktree or in `/tmp`, after symlinks are resolved (for the session tool a link must stay within the most specific allowed source folder containing it; for a worker hand-back it must stay in that worker's worktree), never in a credential folder or an agent-net folder except the worker's own worktree (the list is in [security.md](security.md#files-between-machines)); confined sends refuse files with multiple hard links; at most 100 MB per file and 10 files per message; no two files with one name;
2. copies each file into `files/outgoing/` while it computes the SHA-256, and runs the secret scanner on text files;
3. sends the message with the manifest. The gateway stores the manifest with the message and checks it again. Files need a live connection: a message with files is never put in the outbox;
4. pushes each file under its unique name `agent-net-<message id>-<file id>-<name>`:
   - to a session on the same machine: through `files/local/`;
   - over Taildrop (`tailscale file cp <file> <address>:`) when the receiving machine reported a Tailscale address, its daemon is not on macOS, and `tailscale status --json` lists it as an online peer;
   - else through the gateway in parts (`xfer.begin`, `xfer.part`, `xfer.finish`), with the unique name as the upload name.

A receiver whose daemon runs on macOS always gets its files through the gateway, for every message: the Mac daemon never reads `~/Downloads`, because an open there can block in the kernel (decision [0125](decisions/0125-worker-file-notice-timeout.md)). The sender reads the receiver's OS from `nodes`; when the OS is unknown, it uses the gateway too. A Taildrop that fails (for example a receiver that is offline) falls back to the gateway, which holds the bytes until the receiver comes online or 7 days pass. A message held for the owner's approval sends its files through the gateway, not over Taildrop. A worker notice also uses the gateway between machines. An empty file carries no bytes: the receiver writes it from the manifest. The daemon finds `tailscale` on `PATH`, then on macOS at `/Applications/Tailscale.app/Contents/MacOS/Tailscale`. With no Tailscale, files go through the gateway.

**Receiving.** When a message with a manifest arrives, the daemon of the receiving session collects its files before delivering it. A worker notice has a shorter bounded wait, described below. The daemon looks, in this order:

- in the inbox folder, for a file already there (after a restart, or when the gateway pushes the message again);
- in `files/local/`, for a sender on the same machine;
- on Linux, it runs `tailscale file get files/taildrop/`, and looks in that folder and in `~/Downloads` (in case you collected the file yourself). Files nobody announced go to `~/Downloads` at once, under a free name such as `scan (1).pdf`. An open of a file that arrived runs in a helper goroutine with a timeout, so a blocked syscall cannot keep the message or a daemon stop waiting;
- on macOS, it never stats or opens a file in `~/Downloads`. A Taildrop that an older sender leaves there is not collected;
- on the gateway, with `xfer.find` for the unique names of the missing files. It downloads a matching upload with `xfer.get`, checks it, and confirms it with `xfer.confirm`, so the gateway frees the space.

A file is taken only when its size and SHA-256 match the manifest. It is copied into `inbox/<session>/<message>/<name>` and then removed from where it arrived. The daemon looks again after 2 seconds, doubling the wait up to 30 seconds while nothing arrives. Until every file is in, the sender sees `waiting for files: <n> of <total> in`. Then the agent gets the message, with a note at the end:

```
[agent-net] This message came with 2 files. They are in /home/you/.local/state/agent-net/daemon/inbox/s_.../m_.../:
- report.html (90.0 KB)
- data.csv (1.2 MB)
agent-net deletes them after 7 days.
```

The delivery detail ends with the path: `files via Taildrop`, `files via the gateway`, `files via the local handoff on this machine`, or several joined with `and`. For an ordinary message, a file whose copy fails its hash twice in a row fails the message (`... arrived, but its sha256 does not match the file list`). Files that have not arrived when the message expires (24 hours) fail it with their names. A worker notice is different: the sender retries a failed file offer, and the receiving daemon delivers the report after at most 2 minutes from storage while it is online, or sooner if collection fails. The notice lists missing names in a `files missing: ..., transfer did not finish` line and names any files that arrived. On a reconnect the deadline still runs from when the gateway stored the notice. A message delivered to the agent earlier is not collected again.

**Frames.** `msg.send` and `msg.deliver` carry `files` on the message. `xfer.find` takes `names` (1 to 10) and returns `uploads`: the finished uploads addressed to the calling machine with one of those names, each with `upload_id`, `name`, `size`, `sha256` and its part layout. Every transfer state now also carries the upload `name`.

## Dependencies on harness internals

Agent-net integrates with parts of each harness that are not general protocols. A harness update can change these details.

| Harness detail | What agent-net uses | What breaks if it changes | How you would notice |
|---|---|---|---|
| Claude Code inbox socket | The daemon reads `messagingSocketPath` from `~/.claude/sessions/<pid>.json`. It writes newline-delimited JSON frames with `type: user`, `msgV: 1`, `priority: next`, and user-role content wrapped in `<cross-session-message>`. This frame format was learned from the open-cross-session project. | Session discovery or message delivery can fail if the session file field, socket path or frame shape changes. | Claude sessions may disappear from discovery, or sends may report a missing or refused inbox socket. The daemon logs failures forwarding replies. See [claude.go](../internal/daemon/claude/claude.go). |
| Codex app-server control socket | The daemon finds `app-server-control/app-server-control.sock` under the Codex home, follows its link to a Unix socket, and speaks WebSocket JSON-RPC. It uses the socket to observe and queue messages for shared sessions. | Existing Codex sessions may no longer be discoverable or receive messages. | Codex sessions or peer delivery stop updating; daemon logs report socket or connection errors. See [codex_shared.go](../internal/daemon/codex_shared.go) and [codex_peers.go](../internal/daemon/codex_peers.go). |
| OpenCode server API and plugin | The daemon uses OpenCode's HTTP API for session operations. The agent-net plugin connects to the local daemon and registers sessions, handles delivery and reports activity. | Session creation, message delivery, status updates or replies can fail if endpoints, payloads or plugin hooks change. | Starts can fail on HTTP errors or when the plugin does not register; delivery and status errors appear in daemon logs. See [worker_opencode.go](../internal/daemon/worker_opencode.go) and [agent-net.ts](../plugins/opencode/agent-net.ts). |
| Pi extension API | The extension uses Pi's extension hooks and RPC events, and connects to the daemon over its local Unix socket. | Pi sessions may not register, receive messages or return replies. | A Pi worker start times out if the extension does not register it; the daemon logs connection or delivery failures. See [agent-net.ts](../plugins/pi/agent-net.ts) and [worker_pi.go](../internal/daemon/worker_pi.go). |
| Antigravity hooks and stream-json | Hooks report session start, prompt and stop events to the daemon. Workers use `--input-format stream-json` and `--output-format stream-json`; the daemon reads events and writes incoming turns to the process. | The daemon may lose session status or worker conversations if hook events, JSON fields or stream event shapes change. | Sessions stop appearing or updating, or workers fail to initialize or report stream errors in the daemon log. See [antigravity.go](../internal/daemon/antigravity.go) and [worker_agy.go](../internal/daemon/worker_agy.go). |

## Messages that wait at the gateway

A message to an online session that reports `busy` does not go into the
session. The gateway keeps it with state `stored` and `held_for_busy` set,
then sends it when the session reports it is no longer busy. Workers keep
their own turn queue, and an offline session keeps the queue and wake path.
Decision [0130](decisions/0130-gateway-busy-hold.md) has the design.

When several messages waited, they go as one message, newest first:

```
3 messages arrived while you were busy. Act on the newest; read the older ones as context.

--- 1 of 3: message m_... from builder ---
<body>

--- 2 of 3: message m_... from reviewer ---
<body>
```

A single held message goes as it is. The batch keeps every original message in
its `batch` field, so a reply or an ack still names the original. The batch
row is what moves through the delivery states; its originals move with it.
The header text and the `batch` field are agent-net's own, not the sender's.

The provenance line an adapter adds after the body names the batch and its
newest sender, for example `[agent-net] batch of 3 held messages, newest from
builder (claude, session s_...); each message keeps its own id.` It never
calls the batch a reply, even when the newest original was one, and it names
the sender instead of showing a raw session ID with an unknown harness.

A message that has waited `BusyHoldLimit` (30 minutes) while the session still
reports busy is sent anyway, and the gateway logs `msg.released_stuck`.

`messages.inbox_limit` (default 5, levels network, repo and agent, resolved per
recipient) caps how many undelivered peer messages may wait for one session. A
message already in a batch counts once, as the batch. At the limit a send is
refused with `recipient has N waiting, oldest from you at <time>; withdraw one
or wait`. The owner's messages and the CTO's messages with `as_owner: true` are
exempt. So are three kinds of traffic that are not peer messages: a reply to
the recipient's own request, agent-net's own notices (step tasks, reminders,
not-started notices), and messages between a worker and the caller that started
it, in either direction (the task, the caller's follow-ups, and worker notices
such as done or started). Exempt traffic is never refused and does not count
toward the limit, but it still takes the busy hold and the batch. Any sender
may set `urgent`: it goes ahead of older held messages and still counts toward
the limit. A value of 0 turns the limit off.

The send result and `agent_message_status` carry `queue_position` (1 is next),
`queue_waiting`, `busy_for_seconds` and a one-line `queue` receipt such as
`queued behind 2, session busy for 6 min`. In the TUI the STATUS cell adds
`gw N · in M`: N held at the gateway, M dispatched but not yet delivered to
the harness.

`agent_message_withdraw` takes back an undelivered message. Its sender may
withdraw its own; an owner session and the CTO may withdraw any undelivered
message to one session. `older_than: true` withdraws every undelivered message
to that message's recipient that is as old as or older than the named one.
The message gets the state `withdrawn` and stays visible to its sender in
`agent_message_status`. A message the harness already accepted (delivered,
replied or acknowledged) cannot be withdrawn, and the error says so. A
withdrawn original leaves its pending batch, and the batch text is rebuilt
without it.

## Delivery states

The receiver saves `injecting` in `daemon.db` before calling a harness. It saves
`delivered` after confirmation, or `failed` after a known failure. Old rows in
the `delivered` table migrate as delivered. A confirmed duplicate push reports
`delivered` with detail `duplicate`, without writing to the harness again.

A write error after connecting, a missing plugin ack or a disconnect before
confirmation reports `failed` with a detail saying the message may have arrived.
The local record is `unconfirmed`, so another push cannot inject it again.
After a restart, an `injecting` record reports `failed` with
`may have arrived: the daemon stopped while delivering`. It also blocks another
write. These use the existing gateway and TUI failure state; there is no new
protocol state. A delivery-record write failure never permits a harness write.

Confirmation means acceptance by the harness: a Pi or OpenCode plugin ack,
a Codex queue accept, an Antigravity hook reply or herdr acceptance, or a Claude
inbox socket write. It does not mean the agent has read or answered the message.
An in-memory queue waiting for a later turn remains `dispatched`; its record is
cleared because nothing reached the harness. It can be pushed again after a
restart. OpenCode queued worker turns become delivered only after the plugin ack.
Pi's first worker task can be pushed once more within the same delivery if its
turn-start ack is missing. The extension remembers its last 200 accepted message
IDs, skips another injection and repeats the ack after the turn has started.
The gateway retries known temporary failures, such as an offline session or a
lost plugin connection, after 30 seconds, 2 minutes, 10 minutes, and then every
30 minutes until expiry. The schedule survives a gateway restart. Status shows
when a retry is scheduled. A success clears the schedule. Owner-only and policy
refusals, unknown errors, and failures saying the message may have arrived are
not retried.

When a message fails for good, the sender gets one notice from agent-net
itself (a notification, no reply needed). A failure is final when it is not
retried, or when the message expires before a confirmed delivery. The notice
says `message to <name> was not delivered: <reason>`, then the message ID and
the first line of the body (at most 200 characters). An expiry gives the reason
`expired after 24 hours`, plus the last error if there was one. When the
message may have arrived (the daemon stopped while delivering, a write failed
after connecting, or it expired while dispatched with no report), the notice
says `may not have arrived` instead. A delivered message gets no notice. A
message to an ended or offline worker also gets no notice.

The gateway stores the notice as a normal message with the idempotency key
`undelivered:<message id>`, so each message gets at most one, also after a
gateway restart. An offline sender gets it when it comes back. There is no
notice about agent-net's own messages, so a failed notice never makes another
one. The owner and connected apps get no notice: the TUI and the app already
show the failed or expired state. An expired held request gets its own reply
instead (see [Peer trust and hints](#peer-trust-and-hints)).

When a request is delivered to a session that waits for its user (see
[the Desk](#asks-and-the-desk-store-and-log-decision-0090)), the sender gets one notice from agent-net, once
per waiting period (decision [0060](decisions/0060-waiting-for-user-auto-answer.md)):
`[agent-net] <name> is waiting for its user's answer (since <time> UTC). Your
message was delivered and will be read when the user answers. No reply is
needed.` A permission wait says `approval` instead of `answer`. The notice
never quotes the waiting question or tool summary: those are for the owner's
TUI and CLI only. The session name in it is cleaned. The request itself is
delivered as usual. The key is `waiting:<recipient>:<since>:<sender>`, so a
second request from the same sender in the same wait gets no notice, also after
a gateway restart; another sender gets its own, and a new wait starts a new
period. A notification, a reply, agent-net's own messages and the owner's
messages get none. A connected app (a bot) gets no stored notice: its
`agent_message_send` receipt carries the same words in `note`.

## Claude replies

A Claude Code session answers a peer with its own SendMessage tool. The
answer names the reply address from the message's frame, not the message it
answers. The daemon matches it as follows.

- Each message delivered to a Claude session names a reply socket for its
  sender and conversation. So an answer tells the sender and the
  conversation it belongs to.
- Before the frame is written, the daemon saves a row in the
  `claude_deliveries` table of `daemon.db`: message ID, conversation ID,
  sender, recipient, type, delivered at, expires at, answered at, and how it
  was answered. No message text is kept. A refused inbox connection removes
  the row, because the session never saw the message.
- A request is open until it is answered or its message expires (24 hours).
- An answer with one open request from that sender in that conversation is
  the reply to that request.
- With several open requests there, a body that names exactly one of their
  message IDs is the reply to that one. Otherwise the answer is a reply in
  the conversation with no parent message. It closes every open request
  there, with the answer `conversation` in the row, and the daemon logs
  `threaded to conversation <id>`. The gateway marks those requests as
  replied.
- An answer with no open request stays in its conversation, with no parent.
- An answer to a sender's own socket (no conversation) uses the open
  requests from that sender in any conversation. If they are in several
  conversations and none is named, it goes out as a notification in a new
  conversation, as before.
- At start the daemon listens again on the reply socket of every
  conversation in the table, so an answer written after a restart still
  finds its request.

The gateway accepts a `msg.send` with `conversation_id` and no
`parent_message_id` only when the recipient has written to the sender in
that conversation. An older gateway ignores the field and starts a new
conversation. If the gateway refuses it, the daemon sends the answer again
as a notification in a new conversation, so it is not lost.

A `msg.send` whose sender is the owner session is the exception: with a
`conversation_id` and no parent it is stored as a reply in that conversation
when the conversation exists and the recipient is one of its sides (the owner
need not be a side). Otherwise the gateway refuses it with the reason. The
operator call `owner_send` takes the same optional `conversation_id`, and a send
without it opens a new conversation as before. The web console's Messages
composer uses it, so a reply stays in its thread.

A message with files stays `dispatched` while the receiver collects them, with
the detail `waiting for files: <n> of <total> in`. The delivery detail names
the paths the files took, for example `files via Taildrop`. An ordinary message
fails if a file fails its hash or does not arrive before expiry. A worker
notice is delivered with the names of missing files after at most 2 minutes
while the receiving daemon is online (see [Files between machines](#files-between-machines)).

## Housekeeping

agent-net keeps message bodies as long as possible. These jobs only remove bookkeeping rows and free space.

- **Daemon Claude reply rows.** The same hourly job deletes `claude_deliveries` rows 24 hours after their message expired. The reply socket of a conversation with no rows left is closed within ten minutes. Until then a late answer still arrives, and goes to the conversation.
- **Daemon duplicate check.** `daemon.db` has a `delivered` table with one delivery record per message ID. It stops duplicate or uncertain writes and holds no message text. Once an hour the daemon deletes rows older than the message expiry (24 hours) plus 7 days, so a message that is still alive can never be delivered twice. The first run is about a minute after start, so an upgrade does not postpone it.
- **Gateway database.** `gateway.db` is vacuumed at most once a week, and only when more than 20% of its pages are free (deleted rows or events). The hourly maintenance runs the check; the vacuum rewrites the file and can make it much smaller. Nothing is deleted: messages, events and sessions follow the `--keep-*` retention settings, and the defaults keep messages for ever. A vacuum writes one `db.vacuum` line with the size before and after.
- **Gateway transfer parts.** The parts of an upload between machines (see [Files](#files)) are deleted as soon as the receiving machine confirms the whole file. Every upload is also deleted 7 days after it began, downloaded or not. The hourly maintenance deletes expired parts even when no transfer runs. A failed removal keeps the row, so the next run tries again and the bytes keep counting against the sender's 1 GB cap.
- **Received files.** Once an hour (the first run about a minute after start) the daemon deletes each message folder under `inbox/` whose last file arrived more than 7 days ago, and an empty session folder after it. Send snapshots left by a crash go after a day; files in the same-machine handoff and the Taildrop staging folder that nobody took go after 7 days.
- **Worker build cache and test temp folders.** The daemon runs an hourly cleanup (first run about a minute after start) to keep worker builds from filling the disk. It trims `/tmp/agent-net-gocache` (or `AGENT_NET_GOCACHE`), removing cache entries whose modification time is older than `workers.cache_max_age` (default 24h, 0 turns it off). Go refreshes entry timestamps on use. One size cap, `workers.cache_max_bytes` (default 15 GiB), covers the worker cache and your `~/.cache/go-build` together. When their total is over the cap, the daemon removes the oldest entries across both caches until the total is at or below `workers.cache_low_bytes` (default 10 GiB). The owner cache is trimmed by size only, never by age. The cache README, trim.txt and directory structure are preserved in both, and only entry files inside the hash folders are removed. The same size trim runs at once when the disk usage warning fires, before the notice is written. The cleanup also removes subfolders of `/tmp/agent-net-gotmp` (or `AGENT_NET_GOTMPDIR`) older than `workers.tmp_max_age` (default 6h, 0 turns it off) when no file inside was modified within that window. Checking whether files are open by a running build is not checkable portably, so age limits are the guard for active runs. Each run logs one line with the total bytes freed, and each size trim logs one line with the bytes freed per cache. When the filesystem holding the daemon state directory or either Go build cache exceeds 85% used, the daemon raises an owner Inbox notice that names both caches and their sizes. The warning clears when disk usage drops below 80%.
- **uv cache.** Once an hour with the worker cleanup (first run about a minute after start), the daemon runs `uv cache prune` when `uv` is on `PATH` and `cleanup.uv_prune` is on (default on). It sets `UV_LOCK_TIMEOUT=10`, so uv waits about 10 seconds for another uv process, and stops `uv` after about 30 seconds. It never passes `--force` and never runs `uv cache clean`. A success logs uv's summary line as given, such as `Removed 2 files (1.5 MiB)` or `No unused entries found`. A lock timeout logs at most once a day.
- **Log files.** On Linux the services write to journald, which rotates its own journal; agent-net does nothing. On macOS launchd sends stdout and stderr to `~/Library/Logs/agent-net-daemon.log` and `agent-net-gateway.log` and never rotates them. The daemon and gateway open their own log file, and when a write would pass 10 MB they rename the current file (`agent-net-daemon.log.1`) and start a new one. Five old files are kept (`.1` to `.5`); the oldest is removed at the next rotation. A process started by hand in a terminal, or in a test, writes to that terminal and does not rotate anything.

## Gateway log format

The gateway writes one line per event to stderr (the systemd journal, or the launchd log file). Every line is `ts=... event=... key=value ...`. The time is UTC. A value with a space, quote or `=` is in double quotes. Empty values are left out. Message bodies are never logged.

| Event | When | Main keys |
|---|---|---|
| `msg.stored` | The gateway accepted a message. This is the one line per message. | `msg`, `conv`, `type` (`request`, `reply`, `ack` or `notification`), `from`, `from_name`, `from_harness`, `from_host`, `to`, `to_name`, `to_harness`, `to_host`, `bytes` (size of the body), `state`, and `recipient_busy_s` while the recipient is busy |
| `msg.held_busy` | The message waits at the gateway because the recipient is in a turn (decision 0130). | `msg`, `conv`, `type`, `to`, `to_host`, `busy_for_s`, `reason` |
| `msg.batch` | Several held messages go as one batch. | `msg` (the batch), `conv`, `type`, `to`, `to_host`, `count`, `busy_for_s` |
| `msg.released_busy` | One held message goes as it is. | same as `msg.batch`, `count` is 1 |
| `msg.released_stuck` | The 30-minute fallback sent held messages to a session that still reports busy. | `session`, `msg`, `count`, `limit_s` |
| `msg.withdrawn` | The sender, the owner or the CTO withdrew an undelivered message. | `msg`, `conv`, `type`, `to`, `to_host`, `by` |
| `msg.dispatched` | The message was sent to the recipient's machine. | `msg`, `conv`, `type`, `to`, `to_host`, and `recipient_busy_s` while the recipient is busy |
| `msg.delivered` | The recipient's machine put it into the session. | same, and `detail` |
| `msg.failed` | It could not be delivered. | same, and `detail` |
| `msg.expired` | It was not delivered within 24 hours. | same |
| `msg.refused` | A send was refused (limit, unknown session, ...). | `node`, `from`, `to`, `bytes`, `reason` |
| `transfer.started` | The gateway accepted an upload between machines. | `upload`, `from`, `to`, `bytes`, `parts` |
| `transfer.finished` | The whole SHA-256 of an upload matched, so the receiver may download it. | `upload`, `from`, `to`, `bytes` |
| `transfer.confirmed` | The receiving machine has the whole file and the parts were deleted. | `upload`, `from`, `to`, `bytes` |
| `transfer.expired` | The 7 day retention passed and the parts were deleted. | `upload`, `from`, `to`, `bytes` |
| `session.new` | The first time the gateway sees a session. | `session`, `node`, `host`, `harness`, `name` (the automatic name), `title` (the harness name, which TASK shows until the agent announces), `pid`, `cwd`, `project`, `git_root`, `worktree`, `remote`, `branch` |
| `node.harnesses` | The harnesses installed on a machine changed (or it reported for the first time). | `node`, `host`, `change`, `installed` |
| `project.added` | A project was registered. One line per project. | `node`, `host`, `session` (empty for the CLI), `harness` (`cli` for the CLI), `name`, `path`, `remote` |
| `project.identity_merge` | Existing project charters were merged during identity migration. | `from`, `to` |
| `project.identity_conflict` | Migration left a project pair unchanged because their graph controls conflict. | `from`, `to` |
| `project.removed` | A registration was removed. | `node` (who asked), `target` (the machine it was on), `host`, `name`, `path` |
| `node.connected`, `node.disconnected` | A machine's link came up or went down. | `node`, `host`, `remote` |
| `retention` | Old data was deleted. | `messages`, `deliveries`, `events`, `sessions` |
| `db.vacuum` | `gateway.db` was vacuumed because free pages passed 20%. At most once a week. | `before`, `after` (file size in bytes) |
| `error` | Something failed inside the gateway. | `where`, `err` |

Most of these events are also rows in the `events` table, which `agent-net events` shows.

The daemon honours `XDG_CONFIG_HOME` (config is then `$XDG_CONFIG_HOME/agent-net`) and `XDG_STATE_HOME` (state is then `$XDG_STATE_HOME/agent-net/daemon`). The gateway data default does not follow `XDG_STATE_HOME`.

Unix socket paths are limited to about 104 to 108 bytes. Keep `AGENT_NET_SOCKET` short.

## Environment variables

| Variable | Used by | Effect |
|---|---|---|
| `AGENT_NET_CONFIG_DIR` | daemon, CLI, setup, `install.sh` | Directory with `node.json` and `ca.pem`. |
| `AGENT_NET_STATE_DIR` | daemon | Directory for `daemon.db`. |
| `AGENT_NET_SOCKET` | daemon, CLI, `agent-net mcp`, Pi extension | Path of the daemon socket. |
| `AGENT_NET_CLAUDE_SESSIONS_DIR` | daemon | Where to look for Claude Code session files. For tests. |
| `AGENT_NET_HOME_DIR` | daemon | User home directory to scan for projects layout. For tests. |
| `AGENT_NET_GRAPH` | `agent-net tui` | Graph renderer: `auto` (default), `pixel` or `braille`. See the Graph tab. |
| `AGENT_NET_GRAPH_MOTION` | `agent-net tui` | `1` turns on the animated pixel graph (moving message dots, busy pulse, eased camera). Off by default: the pixel graph is still and changes only when the graph does. |
| `AGENT_NET_NO_BAR` | `agent-net tui` | `1` turns off the agent-net row at the bottom of a session entered in the TUI terminal. For a terminal that mishandles the reserved row. See [Enter a session](#enter-a-session). |
| `CODEX_HOME` | daemon, setup | Codex's home folder (default `~/.codex`). The daemon looks for the shared Codex app server's control socket in it, and setup writes `config.toml` and `AGENTS.md` in it. Codex reads the same variable. |
| `AGENT_NET_GATEWAY_DATA` | gateway, `invite`, `enroll`, `setup`, `install.sh` | Gateway data directory, including the `transfers` folder of in-flight parts. |
| `AGENT_NET_NO_SERVICE` | setup, `install.sh` | `1` means: write no service files and start nothing. Same as `--no-service`. |
| `AGENT_NET_BASE_URL` | `install.sh` | Replaces the release root URL, before `/latest` or `/download/<tag>`. For tests. |
| `AGENT_NET_DIST` | `scripts/dist.sh` | Output directory for the built binaries. |
| `AGENT_NET_NO_INSTALL` | `scripts/dist.sh` | Set it to skip copying the binary to `~/.local/bin`. |
| `RELEASE_SIGNING_KEY` | `scripts/release-key sign` | PKCS#8 Ed25519 private key PEM supplied by the release workflow secret. Never print it. |
| `AGENT_NET_WORKER`, `AGENT_NET_WORKER_SESSION_FILE` | OpenCode plugin | Set by the daemon in a worker's environment: the worker ID, and a file that holds the worker's one session ID. The plugin then skips its start-up discovery and registers only that session. Do not set them. The daemon also sets `AGENT_NET_WORKER` and `AGENT_NET_SOCKET` for Pi workers, and for Claude Code workers through the `env` of their `--settings`. |
| `AGENT_NET_NO_AUTO_UPGRADE` | daemon, `agent-net mcp` | `1` means: do not upgrade by itself. For the daemon: no automatic catch-up. For `agent-net mcp`: do not switch to a newer binary. |
| `AGENT_NET_IGNORE` | daemon | `1` in a session's environment keeps that session off the network: the daemon registers no session and no project for it, and removes one it had. Set it on the harness process (`AGENT_NET_IGNORE=1 claude`, or the equivalent for Pi and OpenCode). A Codex thread has no process of its own, so set it where the shared Codex app server starts; it then hides every thread of that server. A worker started by the daemon is always registered. |
| `AGENT_NET_MCP_HANDOVER` | `agent-net mcp` | Set by `agent-net mcp` on itself when it switches to a newer binary. It holds the path of a private temp file (`agent-net-mcp-handover-*` in `$TMPDIR`) with the start-up messages and unread input. The new process reads it, deletes the file and unsets the variable. Do not set it. |
| `CLAUDE_CODE_SESSION_ID` | `agent-net mcp` | Set by Claude Code for the MCP servers it starts. Not set by you. |
| `CLAUDE_CODE_DISABLE_MOUSE` | daemon | `0` in the `env` of your Claude Code settings (`~/.claude/settings.json`, or `$CLAUDE_CONFIG_DIR/settings.json`), written by setup and kept there by the daemon. Claude Code applies it inside every session, so the wheel scrolls the conversation in every session however it was started. agent-net adds no per-launch copy. |
| `DISABLE_AUTOUPDATER` | daemon | `1` in the `env` of every Claude Code process agent-net starts (owner starts and resumes, workers, the merger, the CTO, sign-in and the compaction attach), and in the `env` of your Claude Code settings for sessions you start by hand. Claude Code's own background updater stays off, so agent-net's daemon is the only updater ([decision 0133](decisions/0133-agent-net-is-the-only-harness-updater.md)). |

The daemon owns one value for mouse tracking: it writes `CLAUDE_CODE_DISABLE_MOUSE=0` to the `env` of your Claude Code settings file at start and checks it every minute, so a drifted value is set back without touching another key. A file that is not valid JSON is left alone and reported. Claude Code applies the user settings inside each session, and the launch settings of a managed session no longer carry a copy ([decision 0128](decisions/0128-cto-keeps-mouse-tracking-on.md)). The CTO keeps its own deliberate exception in its launch settings. The daemon also reports a live Claude session whose inherited environment still turns the wheel off, once per session per day, in the owner TUI and the Desk log. A Claude session that was already running keeps its old setting until it is started again. When Claude's background service itself carries the old value, the daemon reports it once and stops it with `claude daemon stop --any --keep-workers` only when Claude's fresh roster lists no live session, so even a session that starts during the check survives; the next launch starts a clean service.

With mouse tracking on, the wheel scrolls the conversation, and selecting text needs Shift while dragging (Option on some terminals), because a plain drag is a mouse event for the session.

The daemon owns one more value in the same file: it writes `DISABLE_AUTOUPDATER=1` to `env` at start and checks it every minute, with the same merge, drift and invalid-file rules as the mouse key, and setup writes it on a new machine. Every Claude Code process agent-net starts carries the same variable in its own environment, through one helper in `internal/claudelaunch`, so a new launch path cannot forget it. Claude Code's own background updater then stays off in every session, and agent-net's daemon is the only thing that updates the harnesses ([decision 0133](decisions/0133-agent-net-is-the-only-harness-updater.md)). Uninstall removes the key only while its value is still `"1"`.

## Flags

| Command | Flag | Default |
|---|---|---|
| `gateway` | `--listen ADDR` | `0.0.0.0:7420`. If reachable on a public routable address, the gateway logs a warning at start and hourly, and surfaces it in `agent-net nodes` and the TUI status line. |
| | `--apps-listen ADDR` | Override the managed connected apps listener address, normally `127.0.0.1:7422`. Binds to loopback only. It serves the web console (the phone app) at `/` and the bot MCP endpoint at `/mcp`. |
| | `--apps-public-url URL` | empty. Public HTTPS base URL of the apps listener, used for authorization discovery and phone action origin checks. Normally set by the "Reachable from the internet" switch. |
| | `--data DIR` | `~/.local/state/agent-net/gateway` |
| | `--dist DIR` | `~/.local/share/agent-net/dist` |
| | `--san LIST` | none. Extra host names or IPs for the TLS certificate, comma separated. |
| | `--keep-messages D` | `0` (keep for ever). Message bodies and delivery records are kept until deleted. A period deletes the ones older than it. |
| | `--keep-events D` | `2160h` (90 days). Events older than this are deleted. `0` keeps them for ever. |
| | `--keep-sessions D` | `720h` (30 days). Offline sessions not seen for this long are deleted. `0` keeps them for ever. |
| | `--keep-worker-runs D` | `0` (keep for ever). Worker runs behind the [model track record](#the-playbook-and-the-track-record) are kept until deleted. A period deletes the ones older than it. |
| `daemon` | `--gateway URL` | from `node.json` |
| | `--config-dir DIR` | `~/.config/agent-net` |
| `setup` | `--join STRING` | none. A join string from `invite` or `enroll`. |
| | `--no-auto-upgrade`, `--auto-upgrade` | Save or clear the daemon automatic upgrade opt-out in `node.json`. Later setup runs and rejoining preserve it. |
| | `--no-launch` | off. This machine refuses workers. Saved as `no_launch` in `node.json`. It stays until `--launch`. |
| | `--launch` | off. Accept workers again. |
| | `--gateway` | off. Also install and start the gateway here. On a fresh setup with Tailscale present, setup offers to bind to the Tailscale address instead. Existing setups keep theirs. |
| | `--apps-tailscale-name NAME` | none. With `--gateway`: connected apps get agent-net's own Tailscale node with this name, for example `agent-net`, and the public URL becomes `https://NAME.<tailnet>.ts.net`. Saved as `tailscale-node.json` in the gateway data folder, so later setups keep it. See [Connected apps](#connected-apps). |
| | `--apps-tailscale-authkey-file PATH` | none. With `--apps-tailscale-name`: the absolute path of a file that holds a Tailscale auth key. Only the path is saved. The gateway reads the file only for the node's first login. Without it, the TUI shows a login link. |
| | `--apps-tailscale-machine` | off. With `--gateway`: go back to the machine's own Tailscale node. The gateway logs agent-net's node out and deletes its state at the next start. |
| | `--no-service` | off |
| | `--all` | off. Answer the one integrations question with all seven on, without asking. |
| | `--none` | off. Answer it with all seven off, without asking. |
| | `--accept-peer-messages` | off. Set Claude Code `crossSessionInbound` to `accept` as a local answer for this machine, if it is not set. It wins over the network answer. |
| | `--no-accept-peer-messages` | off. Do not set it on this machine and do not ask. It wins over the network answer. |
| | `--claude-hint-hook` | off. Add the Claude Code hook that suggests which session to ask after a failed tool call, as a local answer for this machine. It wins over the network answer. |
| | `--no-claude-hint-hook` | off. Do not add it on this machine and do not ask. It wins over the network answer. |
| | `--claude-needs-hook` | off. Add the Claude Code hooks that show a waiting session in the Inbox, as a local answer for this machine. It wins over the network answer. |
| | `--no-claude-needs-hook` | off. Do not add them on this machine and do not ask. It wins over the network answer. |
| | `--claude-workers-mod` | off. Install the Claude Code plugin that shows the session's workers and register its marketplace, as a local answer for this machine. It wins over the network answer. |
| | `--no-claude-workers-mod` | off. Do not install it on this machine and do not ask. It wins over the network answer. |
| | `--quota` | Run only the quota setup and provider login check. |
| | `--quota-axi` | off. Read subscription quotas with quota-axi and install it when needed, as a local answer for this machine. It wins over the network answer. |
| | `--no-quota-axi` | off. Do not read subscription quotas on this machine, do not ask, and remove the private quota-axi copy. It wins over the network answer. |
| | `--data DIR`, `--dist DIR`, `--port P` | gateway data, dist dir, `7420` (with `--gateway`) |
| `invite` | `--host H` | the IPv4 address of the interface with the default route. A second line is printed for a Tailscale address if there is one. |
| | `--port P`, `--data DIR` | `7420`, gateway data |
| `enroll` | `--host H`, `--port P`, `--data DIR` | this host's name, `7420`, gateway data |
| `join` | `--config-dir DIR` | `~/.config/agent-net` |
| `workers` | `--json` | off. The task line, the usage figures and, with `--recent`, the end facts are in the JSON. |
| | `--owner-claude UUID` | none. Only workers owned by this machine's Claude Code session with that native id. No machine room or queue lines, and no `queued` in the JSON. |
| | `--recent` | off. Also list workers that ended in the last two minutes. |
| `uninstall` | `--purge` | off. Also remove credentials and daemon state. |
| | `--data DIR` | Gateway data directory used to find the recorded Funnel route and agent-net's own Tailscale node. Defaults to `AGENT_NET_GATEWAY_DATA` or the standard gateway data directory. |
| | `--no-service` | off |

`setup --gateway` does not pass the `--keep-*` flags, so the gateway service uses the defaults: messages and worker runs are kept for ever, events for 90 days and offline sessions for 30 days. To change them, add the flags to the `ExecStart` line of the systemd unit (or to `ProgramArguments` in the launchd plist) and restart the service. Setup keeps your edit only until the next `setup` run, which rewrites the unit.

`install.sh` takes `--gateway`, `--client`, `--binary-only`, `--all`, `--none`, `--accept-peer-messages`, `--no-accept-peer-messages` and `--version vX.Y.Z`. The integrations and peer-message flags are passed to `setup`. See [setup.md](setup.md).

## Machine names

The name of a machine is the host name its daemon reports in `hello`. On macOS that can be long (`Johns-MacBook-Pro-950`). To get a short name, give the machine a display name once, from any machine: press `N` in the TUI, or run `agent-net nodes rename NODE NAME`. Nothing needs to be done on the machine itself. The gateway keeps the display name in its database, apart from the reported host name, and a new `hello` does not change it. An empty name clears it, and the machine is listed under the reported host name again.

- **Where it shows.** Wherever the gateway gives out the name of a machine: `list` and `nodes` (the HOST column and the `hostname` field; `nodes --json` also has `display_name` and `reported_hostname`), the TUI, worker start results and notices, the `hostname` of hints, enter plans, the events and the gateway log. Every daemon also learns its own display name at `hello` (and at once when it is renamed), so a machine uses the same name for itself.
- **The rules.** 1 to 63 characters: letters, digits, `-`, `_` and `.`, starting with a letter or digit. Letter case is kept, but two machines cannot have names that differ only in case, and a name cannot be the name another machine is listed under.
- **The operator method** is `node_rename` (`{"node_id": "...", "name": "laptop-1"}`, an empty `name` clears it). It answers `{"node_id", "hostname", "display_name", "reported_hostname"}`. It is served only to the owner's command line or TUI, like `session_policy`, and refused for an agent client and for a command line inside an agent session's process tree. No MCP, Pi or OpenCode tool sets it.
- **`enter_ssh_hosts`.** Its keys are the names the gateway lists, so after a rename use the display name as the key (`{"laptop-1": "me@laptop-1.example"}`). Matching is exact. The name is also the ssh target when the entry is missing, which is why the rules above are those of a host name.

## Integrations

Seven items: Claude peer messages, Codex tool approval, the Claude hint hook, the Claude policy hook, the Claude "Needs you" hooks, the Claude workers plugin, and reading subscription quotas with quota-axi. The quota item installs quota-axi when needed; the other six change Claude Code or Codex settings. See [setup.md](setup.md#integrations).

The seven setup integrations ([decision 0021](decisions/0021-network-integrations.md)) are one answer for the whole network. The gateway keeps them in `owner_settings` and serves them to each machine with its node token. The owner answers once, with the one question in `agent-net setup` or with `I` in the TUI. The operator methods are `integrations_get` (answers `{"set": true, "integrations": {...}}`) and `integrations_set` (takes the same `integrations` object, answers it). They are served only to the owner's command line or TUI, like `session_policy`, and refused for an agent client and for a command line inside an agent session's process tree. No MCP, Pi or OpenCode tool sets them.

The daemon reads the answers at start and every minute. It applies them only when they differ from the answers it last applied (`integrations-applied.json`). A per-item setup flag is kept in `integrations-local.json` on that machine and wins over the network answer. Uninstall still removes the integrations of that machine. Each item is described in [setup.md](setup.md#integrations).

## Harness updates

Each daemon checks installed harnesses daily, starting 10 minutes after it starts. It runs `claude update`, `codex update`, `opencode upgrade`, `pi update self`, and `agy update` for the installed programs. A harness whose binary resolves into a Homebrew prefix is upgraded by Homebrew instead, because its own updater defers to the package manager and leaves the version alone: `brew upgrade --cask claude-code` for the Claude Code cask, and `brew upgrade <name>` for a formula. If the updater output says a package manager owns the install, the result says so instead of "already current". It waits for busy sessions or workers, retrying every 30 minutes until the next daily check. Each command has five minutes and runs without input. The daemon refreshes the inventory after each attempt. A version probe stopped by its five second deadline reports "timed out, retrying" and runs again on the next round, so load on the machine is not reported as a broken harness; a probe that ran and failed reports "failed". `agent-net nodes --wide` shows the result and versions; the graph shows failures. Antigravity (`agy`) is detected, updated and can run workers like the other harnesses. In Settings, open Integrations and turn off **Keep harnesses updated** to stop checks on every machine. This setting is on by default and is stored with the network integrations.

agent-net's daemon is the only updater. Claude Code's own in-session updater is turned off with `DISABLE_AUTOUPDATER=1` on every launch and in the user settings ([decision 0133](decisions/0133-agent-net-is-the-only-harness-updater.md)), so two updaters no longer race on the same install.

## What each machine reports

Each daemon sends two kinds of facts about its machine to the gateway. Agents and the CLI can read them.

**Time zone in `hello`.** Every `hello` carries `time_zone`, the machine's IANA zone (`Europe/Paris`), read without sudo: the target of `/etc/localtime` on macOS, then `/etc/timezone`, then `timedatectl show -p Timezone --value`, then `systemsetup -gettimezone` on macOS, then `TZ`. An empty value from an older daemon leaves the stored one alone. The gateway keeps it on the node, `nodes` gives it as `time_zone`, and the owner's display setting uses it (see [Display time zone](#display-time-zone)). It is a fact about the machine, not a change to its clock.

**Tailscale address in `hello`.** Every `hello` carries `tailscale_ip`, the machine's address in `100.64.0.0/10`, read from its network interfaces (no command runs). An empty value clears the stored one. `nodes` gives it as `tailscale_ip`. A daemon that sends files reads it to push them over Taildrop (see [Files between machines](#files-between-machines)).

**Repo info for each session.** When the daemon finds a session (any harness), it runs `git` in the session's folder (with a 3 second limit for all commands) and sends:

- `root`: the main working tree of the repository. Empty if the folder is not in a git repository. A linked git worktree has the root of the main tree it belongs to, found with `git rev-parse --git-common-dir`.
- `worktree`: the working tree the session's folder is in. It is the same as `root`, except in a linked git worktree.
- `project`: the last path part of the `origin` remote, without a trailing `.git`. With no usable origin, the main tree folder name is used. Outside a repository, the session folder name is used. Linked worktrees share their repository identity.
- The name of every session is its project identity plus a number ([0024](decisions/0024-session-names.md), [0033](decisions/0033-session-names-are-owner-only.md)). The identity is the last part of the origin remote when present, else the main working tree folder, else the session folder; a linked worktree uses the same identity as its main tree ([0031](decisions/0031-project-identity-and-display-name.md)). Two live sessions with the same name on the network get ` 2`, ` 3` (the lowest free number), and a worker is `<project> worker <n>`. A worker's TASK is the first line of its task. The name a harness gives the session (a Codex thread name, a Claude session title, an OpenCode session title) is not the name: it goes to TASK when the session announced no task.
- **Only the owner names a session.** An announce cannot: it sets a task, a project, capability tags and expertise, and a `name` in an older client's announce is ignored. The owner renames by approving the CTO tool `session_change` with action `rename` (`session_id`, `name`) or the operator method `session_rename`; an empty `name` returns the automatic project name, with the next free number if another live session took it. The name survives reports and is shown wherever an automatic name is. A rename to a name another live session holds is refused, because `agent_message_send` and `agent-net send` match exact names: a live session with that name first, otherwise the newest session of that agent. The gateway migration at upgrade clears names that older agents had set through announce, so those sessions show their automatic name at the next report.
- `remote`: the `origin` URL. Passwords and tokens are removed. For `http`, `https`, `ftp` and `git` URLs the whole user part goes, because a token is often the user name. For `ssh` URLs only the password goes. A query or fragment is removed. `git@host:owner/repo` is kept as it is.
- `branch`: the current branch, or `detached@<short hash>`.
- `description`: the one-line description of the project, read from `AGENTS.md` (else `CLAUDE.md`) in the main working tree, or in the session's folder when there is no repository. See "FUNCTION and the task" below.

**Temporary folders and the opt-out.** A session whose folder is a system temporary folder is still reported as a session, but it belongs to no project. The daemon sends no repository info and marks the session `no_project`, and the gateway derives no seen project from it. The folders are `os.TempDir()`, `$TMPDIR`, `/tmp`, `/private/tmp` and `/var/folders/*/*/T` (and `/private/var/folders/*/*/T`). This keeps a test suite that starts sessions in temp folders from filling the project list; it deletes no session and no old record. A session whose process environment has `AGENT_NET_IGNORE=1` is not registered at all: no session and no project, and the daemon removes a session it had registered before. The daemon reads the flag from the session's own process for Claude Code, and from the connecting process for the Pi and OpenCode plugins. A Codex thread has no process of its own, so the daemon reads the environment of the shared Codex app server, and the flag there hides all its threads. A worker session that the daemon started is always registered, because its caller waits for it. The daemon cannot read the environment of a process owned by another user; such a session is registered normally.

**FUNCTION and the task.** FUNCTION in `agent-net list` and the `function` field of an agent's profile is the project description above. In the TUI it is on the detail pane of a session, not in the main table, where TASK has its place. The model does not write it, and `agent_announce` cannot set it. With no `AGENTS.md` or `CLAUDE.md`, or no description line in it, FUNCTION is empty: `agent-net list` shows `-`. The daemon re-checks the file (one `stat`) at every update of the session, so a change shows within moments.

The description is found like this (code: `internal/repoinfo/description.go`). Details and reasons: [decision 0009](decisions/0009-function-from-agents-md.md).

1. Start at the first `# Project: <name>` heading (the standard template), else at the first `# ` heading, else at the top of the file. Ignore front matter and code blocks.
2. In the template form, take the first non-empty line after the heading. Otherwise take the first plain paragraph. Stop at the next heading.
3. Skip lists, tables, quotes, HTML, rules, `@file` imports and notes to agents (a paragraph that starts like "Notes for coding agents ...").
4. Strip markdown, keep the first sentence without its final full stop, and cut to 120 characters.

What the agent writes is the **announced task**, a free-text label. At the start of every session, and when the announced task changes, the agent must call `agent_announce` with that label in one short line (the guidance, the MCP server instructions, the tool description and the Pi and OpenCode system prompts all say so). The announce also takes a project, capability tags and expertise; `function` is ignored, and there is no `name` parameter (only the owner names a session, see above). A harness title is not a name: it goes to TASK until the agent announces a task label. Until a session has announced, every agent-net tool result ends with "Reminder: call agent_announce with your current announced task." The announced task shows in `agent_sessions_find`, `agent_session_get`, the TUI and as TASK in `agent-net list --wide`. When the task names a worker session id (`s_` and 16 hex characters) and that worker has ended, the gateway adds `(worker <id> ended <date> <time> UTC)` after the task in `agent_sessions_find`, `agent_session_get` (`task`), the CTO agents view and the TUI. It is worked out when the session is read and is not stored. The stored announce, the idle check and the searches use the announce itself, never the note ([decision 0111](decisions/0111-blocked-members-reach-the-lead.md)).

An announce may also carry `waiting`, the question the session waits to hear from you (see [the Desk](#asks-and-the-desk-store-and-log-decision-0090)). A later announce without it clears the wait.

To make a project show a good FUNCTION, start its `AGENTS.md` with:

```
# Project: <name>
A <one sentence: what it is and why it exists>.
```

**Mode and model for each session.** Every session also carries two fields. `agent_sessions_find` and `agent_session_get` return them as `mode` and `model` (both are left out by a machine on an older agent-net).

| Harness | `mode` | `model` |
|---|---|---|
| Claude Code | From the `kind` field of `~/.claude/sessions/<pid>.json`. `interactive` is `foreground`. `bg`, `daemon` and `daemon-worker` (the other values Claude Code 2.1.312 accepts) and any new value are `background`. No `kind` is `unknown`. | The `model` field of the newest assistant entry of the main conversation in `~/.claude/projects/<folder>/<session id>.jsonl`, found when the session folder is turned into a folder name (every character that is not a letter or digit becomes `-`), or by searching all project folders. The daemon reads the file from the end in blocks (at most 8 MiB) and only when its size or time changed. It keeps no transcript text and logs none. Sub-agent entries and Claude Code's `<synthetic>` placeholder are skipped. The model changes after the next answer, not at the moment of `/model`. |
| Pi | From `ctx.mode` in the extension: `tui` is `foreground`, `rpc`, `json` and `print` are `background`. | `provider/id` of `ctx.model`, updated on `model_select`. |
| OpenCode | Always `unknown`. OpenCode 2.2.0 has no signal for a client that is attached to a session. See [opencode.md](opencode.md#notes-and-limits). | `provider/model` of the session (`Session.Info.model`), updated on `session.model.selected` and `session.step.started` of the root session. |

**Tools version for each session.** A Claude Code or Codex session has its own `agent-net mcp` process, started with the session and kept for its life. After an upgrade it can run older tools than the machine (for example no `agent_worker_start`). Its `hello` carries its version, and the daemon compares it with its own. The session then carries two more fields (new after v0.4.6, left out by an older daemon): `tools_version` (the version of the session's tools, or `unknown` when the process sent no version) and `tools_old` (`true` when those tools are older than the machine's agent-net). The gateway stores them with the other session fields.

- **Owner.** The TUI detail pane shows a `tools` line in the warning color: `v0.4.5 (old: reconnect agent-net with /mcp)`, or `unknown (from before v0.4.7; reconnect agent-net with /mcp)` for a process that sent no version. Current tools show no line. The main table has no mark for this.
- **Agent.** When the tools are older, the daemon's answer to the `hello` carries `tools_note`. The process adds it once per session, as a last line of the next agent-net tool result: `agent-net: your agent-net tools are vX (this machine runs vY). Ask your user to reconnect agent-net: in Claude Code type /mcp, pick agent-net, reconnect. New tools appear after that.` For a Codex thread the advice is to restart Codex and resume the thread. A redial of the daemon connection does not show it again.
- **Old processes.** A process from before v0.4.7 does not know `tools_note` and cannot show it. The owner sees it in the TUI, and the refusal of `agent-net start` inside a session (see below) points the agent to the same fix. A process that sent no version is `unknown`, and gets no note.
- **Self upgrade.** A process of v0.3.5 or later normally switches to the new binary by itself and the session is then current. See [upgrade.md](upgrade.md#running-sessions). The note and the mark are for a session where that did not happen, or could not (a process from before v0.3.5, or `AGENT_NET_NO_AUTO_UPGRADE=1`). A session that switches says hello again with the new version.
- **Reconnect.** In Claude Code type `/mcp`, pick `agent-net` and reconnect. This starts a fresh `agent-net mcp` from the installed binary, with the new tool list.
- **Disconnected.** Claude Code can stop a session's `agent-net mcp` process and never start it again. The daemon counts the live MCP connections of each Claude session. A live session that is not a worker and has had none for one minute (counted from when the daemon first saw the session or lost its last connection) gets `tools_version: disconnected` in its session report. The daemon reuses the one-second discovery poll for this, so there is no extra scan. Owner: the Sessions row shows `! no tools` at the start of TASK, and the detail pane says `agent-net tools disconnected; the CTO can restart it (session_change restart), or type /mcp to reconnect`. Session: the hook turn text adds `[agent-net] Your agent-net tools are disconnected. Tell the CTO or agent-net; the CTO can restart this session (session_change restart). Until then you cannot message other agents.` (the `notice` field of the policy answer). Peers: `agent_sessions_find` and `agent_session_get` rows carry `tools: disconnected` while the session is online. A new MCP connection clears all of it. There is no Desk ask and no notification.

When an owner-only command (`agent-net start`, `send`, `stop`, `usage`, and the others behind the same check) runs inside an agent session, the refusal starts with `method "..." is not available from inside an agent session: run it in your own terminal.` and ends with the advice. For `start`: `agents start workers with the agent_worker_start tool, not this command. If you do not have agent_worker_start, your agent-net tools are out of date: ask your user to reconnect agent-net (in Claude Code: /mcp).` The other commands say: `agents use the agent-net tools, not this command. If a tool you need is missing, your agent-net tools are out of date: ...`.

If `git` is missing, slow or fails, the session is still reported, with fewer fields. The result is cached per folder (5 minutes for root, project and remote, 5 seconds for the branch), and the branch is read again when the session updates. `agent_sessions_find` returns these fields in each agent's `repo` object. `agent-net list` shows PROJECT, and `--wide` shows the rest.

**Installed harnesses.** At start the daemon looks for `claude`, `opencode`, `pi`, `codex` and `agy`. It reports the path and the version (the first line of `<program> --version`, 5 second limit) of each one it finds. There is no periodic model scan and no retry loop. The model list is read from the harness live, at the moment of use: when `agent_machine_get` asks for `models`, when a worker profile is added, or after a worker start fails. A live read is kept for at most five minutes per machine and harness so a burst of calls shares one harness run. OpenCode is asked with `opencode models` (one `provider/model` per line). Pi is asked with `pi --list-models`, and its table gives `provider/model`. Codex is asked through its app server (`codex app-server`, stdio) with the JSON-RPC method `model/list`, including hidden models and following `nextCursor`. Claude Code has no list command, so its aliases (`haiku`, `sonnet`, `opus`, `fable`) and the full names agent-net knows are a small table in the daemon. Antigravity (`agy`) is asked with `agy models`: a first line `Fetching available models...`, then one `<id><TAB><label>` per line. agy keeps the reasoning effort inside the model id (`gemini-3.8-flash-low`), so it reports no default effort per model. The agy list needs the network and a sign-in, so the same read is also its sign-in check: a list reports `signed_in: yes`, a message such as `not logged in` reports `no`, and a failed or timed out read leaves the state unknown. No account detail from it is kept. Detection still runs again when a harness update check needs it. An offline machine answers `machine <name> is offline, cannot list live`; its last reported list is not used as an answer.

The daemon commits a worker's leftover worktree changes at every complete hand-back, for every harness (see **Commit at hand-back** under [workers](#workers)); an Antigravity worker need not run git commit. At a partial hand-back, the daemon commits leftover changes as WIP for every harness except Antigravity, which keeps its title-based unfinished commit. Read-only workers have no worktree commit.

A worker profile may name `agy`, and can start an Antigravity worker (decision [0049](decisions/0049-antigravity-harness.md), build step 4). An exact model id keeps its effort as written. The vendor in an agy model id (`gemini` to Google, `claude` to Anthropic, `gpt` to OpenAI) is used for display. A Claude model through agy is not your Anthropic subscription, so the protected-provider rule never matches it.

Antigravity workers started with persistent streams keep running across daemon
restarts. The worker inherits a read/write FIFO for stdin and writes stdout to
a private file under its worker directory. The new daemon reopens the FIFO,
restores the in-flight turn and reads results from the saved offset. Usage and
an acknowledged hand-back survive the restart, including a result written while
the daemon was down. The normal done or blocked notice is sent once, without
counting recovery as caller rework. Workers started by older versions with
anonymous pipes cannot be reattached this way. See decision
[0055](decisions/0055-agy-worker-restart.md).


A daemon started by launchd or systemd has a short `PATH`, and it does not read `~/.bashrc` or `~/.zshrc`. So it searches, in this order: its own `PATH`, then `~/.local/bin`, `~/.bun/bin`, `~/.npm-global/bin`, `~/.opencode/bin`, the folders of Node version managers, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin`, `/bin`, and last the `PATH` of your login shell (`$SHELL -l -c`, or `/bin/zsh` on macOS if `$SHELL` is not set, 3 second limit). The login shell is never interactive: it reads your login startup files, not the interactive ones. The programs it runs get the search path as their `PATH`, so a script that starts with `#!/usr/bin/env node` works.

Herdr uses this executable search too, with the login shell's copy taking precedence over `~/.local/bin`. An explicit `harness_paths.herdr` in `node.json` takes precedence over the search. The daemon log records the selected path and a different copy in `~/.local/bin`, when present. Herdr does not appear as an agent harness in `nodes`. The daemon caches the executable path until it is no longer executable or a Herdr command fails. It logs the selection only when the path changes.

The version manager folders are: nvm (`$NVM_DIR` if set, and `~/.nvm/versions/node/*/bin`), fnm (`$FNM_DIR`, `~/.local/share/fnm`, `~/.fnm`, `~/Library/Application Support/fnm`, each with `node-versions/*/installation/bin`), volta (`~/.volta/bin`), asdf and mise shims (`~/.asdf/shims`, `~/.local/share/mise/shims`, `~/.mise/shims`), pnpm global (`$PNPM_HOME`, `~/.local/share/pnpm`, `~/Library/pnpm`) and bun (`~/.bun/bin`). With several Node versions, the one named by nvm's default alias (`~/.nvm/alias/default`) or fnm's default alias comes first. If the alias does not resolve, the newest version comes first.

A harness can be installed more than once, for example OpenCode v1 from its install script and v2 from npm. The daemon finds every copy and drops copies that are the same file (same real path after symlinks). It reports one entry per harness. The `path`, `version` and `models` fields describe the primary copy. This is the copy that comes first in your login shell's `PATH`, and then `primary` is `true`. If the login shell does not find the harness, the primary copy is the first one in the search order and `primary` is absent: the daemon cannot tell which one you run. The other copies are in `others`, each with `path` and `version`. Models are listed only for the primary copy. Both fields are new after v0.3.5 and are left out when empty, so v0.3.5 daemons, gateways and clients still work with them.

`agent-net nodes` shows the result. A machine on an older agent-net shows `unknown`. A machine that reports an empty list shows `none`. Agents get the same data from the `agent_machines_find` tool (Claude Code, Pi and OpenCode), including each machine's subscription quotas when it reported them.

`agent_machine_get` also returns `health`: the daemon's last restart time and
uptime, process PATH, the paths found on that PATH for herdr, claude, codex,
opencode, pi, git, gh and ssh, and free bytes on the state directory's
filesystem. Missing helpers say `not found`; a disk read failure omits the byte
count. The machine version is in `machine.version`. Health travels with hello
and harness inventory; `reported_at` identifies the latest sample. Offline
uptime is as of that sample. Older daemons show `not reported`.
Without a category, `agent_machine_get` also returns `usage`: what the machine is doing now, so nobody has to run `top`. It has `status` (`ok`, `offline` or `unavailable`) and a `note` when it is not `ok`. With `ok` it has `cores`, `load` (1, 5 and 15 minutes), `cpu_pct` (share of all cores in use), `cpu_split` (`user`, `sys`, `nice`, `idle`, `iowait`, `steal` in percent; Linux only), `mem_used` and `mem_total`, `swap_used` and `swap_total` (bytes), `age_s` (age of the reading) and `top`: the 10 processes that used the most CPU over the last sample interval, busiest first. Each `top` row has `pid`, `ppid`, `cpu` (percent of one core, so 300 is three cores), `rss` (bytes), `elapsed_s` (run time), `cmd` (the command line, secrets masked, cut to 120 characters) and the owner: `owner_kind` (`session`, `worker`, `daemon`, `gateway`, `mcp` or `other`), `owner` (the session or worker name, `agent-net daemon`, `agent-net gateway`, `mcp server <name>` or `other`) and `owner_id` (the session ID or the worker ID). The owner is the nearest known ancestor: a process under a worker's process tree belongs to that worker, even though the daemon started the worker. Sessions that share one process (OpenCode) show one shared owner. A machine that is offline gives `status: offline`; a daemon that cannot answer gives `status: unavailable`; the rest of the call still works. The category calls (`include`) do not carry `usage`.

Without a category, `agent_machine_get` also returns `requests`: the gateway's own request counters (the same on every machine's call), kept in memory only. They show where the gateway's work comes from, for example how much is the TUI's one-second polling. The daemon puts a short `via` label on every frame it sends: `tui`, `cli`, `mcp` (an agent session's tools), `worker`, `cto`, `daemon` (its own loops), `phone` or `app`. A frame with no label (an older daemon) counts as `unknown`. The label is only for counting, never for a decision. `requests` has `started_at`, `since_start` and `last_10_min` (ten buckets of one minute). Each window has `seconds`, `calls`, `total_ms`, `bytes`, `by_origin` (calls, total_ms and bytes per origin) and `top`: the 30 (method, origin) pairs with the most total handling time, each with `calls`, `total_ms`, `max_ms` and `bytes` (the size of the responses). More than 1000 pairs are counted as method `other`. There is no setting and no limit: a record is a few atomic adds and costs about 40 ns. `requests` also has `client_errors` once a TUI or web console request has failed: the failures the clients reported (`event=client_error` in the daemon log), counted per (method, origin) pair, with `since_start` and `last_10_min` lists, the most errors first. Each row has `method`, `origin` (`tui` or `phone` for the web console), `errors`, `timeouts` (the errors that ended by their deadline), `total_ms` and `max_ms` (the time the client waited). It is absent until one is reported.

The gateway asks the machine's daemon for each call (`node.machine_usage`). The daemon uses the readings its sampler already holds (every 5 seconds, see "CPU and memory" below) and starts no new loop. If the newest reading is more than 10 seconds old, it takes one reading first. The command lines are read only for the 10 listed processes. A daemon from before this feature gives `unavailable`.

With category `models`, each row also has `scores`: the capability score at each matching effort (`effort`, `score`, `source`).

`agent_machine_log` reads one online machine's service log. Give `machine` (name
or ID) and `log` (`daemon` or `gateway`). `since` is an RFC3339 timestamp or a
positive duration such as `30m`; it defaults to `1h`. An optional `filter`
matches text after redaction. `limit` defaults to 200 and cannot exceed 1,000.
The newest matching lines come back in chronological order, with a 256 KiB
response cap; `truncated` says older matching lines were left out. The target
daemon reads the log and masks recognizable secrets before sending it through
the gateway. This tool is available to agents, workers and the CTO.

On Linux it reads the named user service through `journalctl`; on macOS it
reads the fixed files in the logs table above, including rotations `.1` to
`.5`. Timestamped daemon and gateway lines determine the time window; lines
without a timestamp follow the preceding timestamp. An unfinished final line is
left out. A missing log, offline
machine, unsupported platform, oversized line or failed journal read returns an
error. Older daemons do not support the log tool.

## CPU and memory

Each daemon samples its machine every 5 seconds, keeps 5 minutes, and reports a summary to the gateway every 10 seconds. The gateway keeps the newest report per machine in memory only (it is gone after a gateway restart and back within 10 seconds).

- **Machine:** CPU as a percentage of all cores (0 to 100), memory used and total, load average (1, 5, 15 minutes), process count, each with the 5 minute average.
- **Session:** the process tree under the session's process: CPU as a percentage of one core (100 is one full core, a tree can show more), the sum of resident memory, the number of processes, each with the 5 minute average. A tree seen only once has no CPU figure yet (`...`).
- **Which process:** Claude Code, the session's PID. Pi, the process that connects the extension. OpenCode, the one service process that hosts all sessions: it is listed once as "shared by N sessions" and is never split. If one session starts another (a Claude Code session inside another's shell), the inner tree is also counted in the outer one.
- **Linux:** `/proc/<pid>/stat` (user, system and waited-for children time) and `/proc/stat`. CPU is the difference between two readings. The sampler costs about 5 ms per reading with 450 processes, about 0.1% of one core.
- **macOS:** `ps -A -o pid=,ppid=,rss=,time=,comm=` for processes, `sysctl` for cores, memory size and load, `vm_stat` for memory in use (app + wired + compressed, like Activity Monitor). Load and memory are read every 4th sample. CPU time from `ps` has a resolution of 10 ms and misses processes that start and end between two readings, so tree CPU can read low for a tree that spawns many short jobs. Machine CPU is the sum of live processes' CPU time over the cores, with the same limit. This was written and tested against fixtures of the `ps`, `sysctl` and `vm_stat` output; it has not been run on a Mac.
- **Top lists:** only on request, not in the report. Each process has its PID, short name, and the first 80 characters of its command line with tokens, `--password=`/`--token` values and `KEY=value` secrets masked.
- **Daemon methods** (owner only, from `hello` with `client: cli`): `usage` (this machine live, and all machines from the gateway), `usage_top {session_id}`, `graph_view {project}` (the component graph for the TUI Graph: nodes with charters, edges and the newest charter changes), `session_text {session_id, limit, before, after, sidechain}`, `session_enter {session_id, herdr_socket}` (answers `{argv, dir, env, host, note, fallback, herdr_pane}`; the asking daemon adds `herdr_client` when this machine has a herdr binary, see [Enter a session](#enter-a-session)), `counts`. Gateway frames: `node.usage`, `query.usage`, `query.counts`, `query.graph`, `query.usage_top`, `query.session_text`, `query.session_enter` (and `node.usage_top`, `node.session_text`, `node.session_enter` to the machine that holds the session).
- **Counts:** `counts` returns `online`, `offline_24h` and `offline_total`. The gateway keeps offline sessions for 30 days, so the total is mostly old restarts. The TUI header shows the online count and the numbers the owner can act on (busy, needs you, the worker queue), and leaves the offline counts out.

## Subscription quotas

agent-net does not measure quotas itself. It reads [quota-axi](https://github.com/kunchenguid/quota-axi), a CLI that reports the subscription windows of your model providers from the logins already on the machine (decision [0022](decisions/0022-component-graph-and-cto.md)). You do not install it by hand (decision [0025](decisions/0025-install-quota-axi.md)).

Before using a copy, agent-net runs its `--version` command. A new selection requires version 0.1.66 or newer. Setup offers to upgrade an older owner copy and asks first. It records the selected path, version and reason in `quota-axi-choice.json`. On daemon start, discovery runs again and keeps that copy while it still runs, even if another copy appears. Exit 0 or 1 with valid JSON is used; exit 1 means every provider failed. Exit 2 reports that the selected copy rejected its options. Other failures include the first trimmed stderr line with recognizable secrets redacted.

1. **Install.** When the Quota integration is on and you have no compatible quota-axi, setup installs the pinned version with `npm install --prefix ~/.local/share/agent-net/quota-axi --no-fund --no-audit quota-axi@0.1.69`. The daemon does the same in the background at startup or when it applies a changed network answer, with a 3 minute limit. A failed install is tried again the next day. Node 22.19 or newer and npm are needed; agent-net never installs Node.
2. **Which copy runs.** For a new choice, a supported owner copy wins: the path in `node.json` `quota_axi`, the login-shell and process PATH, `which -a`, `~/.local/bin`, nvm bins, `/opt/homebrew/bin`, `/usr/local/bin`, and the global npm prefix. If none works, agent-net uses its private copy. Setup and the daemon record the choice and keep it while it runs. On macOS and Linux, setup copies exported XDG folder variables into the daemon service so quota-axi sees the same data folders as the setup shell.
3. **Check logins.** With a terminal, setup runs the selected copy's read-only `auth --json` and reports each provider as found, not found, or needing attention. On macOS it offers another run with a Keychain prompt; choose Always Allow if you want future reads to proceed without a dialog. The saved status contains no source paths or credential values. A found source does not prove the provider will accept it. Without a terminal, setup skips the check. To run it later without repeating the rest of setup, use `agent-net setup --quota`.
4. **When it runs.** The daemon runs `quota-axi --json --max-age 5m --no-credential-refresh` at start and every 5 minutes, with a 90 second limit. It never passes `--full`. The child PATH starts with the quota-axi folder and its Node folder, then the same search folders the daemon uses for the harnesses (including `~/.local/bin`), and HOME and USER are filled in when the service environment lacks them, so quota-axi finds the provider CLIs and reuses the readings cached by the owner's terminal. The integration is on by default; turn it off in the integrations question, with `agent-net setup --no-quota-axi`, or in the TUI.
5. **What is kept.** One row per provider and window: window id, label and kind, percent used, reset time, pace (`ahead`, `on_pace`, `behind` or `unknown`) and whether the window is exhausted now. The newest good reading stays when a later run fails. It travels with the machine's usage report (`node.usage`, field `quotas`) and the gateway keeps the newest one in memory, like CPU and memory.
6. **Model choice and admission.** A loose model name skips a provider that is exhausted, or above 95 percent of any window. An exact model id still works, and the start result says why in `model_note`. A provider ahead of its reset clock ranks below providers on pace. A start whose model's provider is exhausted is refused before any model call, with the provider, the limiting window and its reset time. quota-axi reads agy per model family and does not see the account limit. When an agy worker fails with an account quota error that names a reset delay (for example `Individual quota reached ... Resets in 160h24m5s`), the gateway blocks the window that the run's model draws on (for example `Gemini Argon weekly` for an Argon model) on that machine until that time. Other agy models stay usable. A run whose model no window serves blocks every agy model. The block is stored per window in its database (`quota_blocks`), added to the machine's reading as an exhausted window, and refuses starts with `agy Gemini Argon weekly used up until <time> ...; other agy models may still be used`, or with `agy quota used up until <time> ...; pick another harness` for a block on every agy model. The picker skips the blocked models too. A newer usable reading that shows the window with allowance left ends a window block early; a reading from before the failure does not. A block on every agy model ends only at its reset. The failed run counts as `infra`, not against the model.
7. **Where it shows.** One compact line in the TUI header for this machine (`quotas: claude 59%w · codex 90%w · opencode-go 76%w · cursor exhausted`), and per machine in `agent_machines_find` for agents. The suffix is the window kind: `w` weekly, `s` session, `m` monthly, `d` daily, `c` credits. The Subscriptions panel identifies the quota-axi copy for each reporting machine. Providers needing attention point to `agent-net setup --quota`. The TUI Quotas tab shows `LEFT`; its bar fills with the remaining quota. `ahead` means usage is burning faster than the reset clock and stays warning-colored. Live subscriptions usable by a harness appear first. Other subscriptions, including those not set up, share a fold; press `s` to show or hide them.
8. **Stale readings and failures.** A reading quota-axi marks stale is shown but never used to choose or refuse a model. A failed or slow run changes nothing: the last good reading stays. OpenCode Go credentials are read from OpenCode's `opencode.db` under `$XDG_DATA_HOME/opencode` (default `~/.local/share/opencode`) and then the legacy `auth.json`; `OPENCODE_DB` selects a non-default database file. When Node or npm is missing, the TUI Quotas tab and `agent-net setup` print: `quota-axi needs Node 22.19 or newer; install Node and agent-net installs quota-axi for you.`
9. **Turn off.** Turning the item off removes the private folder, as does `agent-net uninstall`.

The provider mapping is fixed. It is the table used by model choice and admission (code: `internal/protocol/quota.go`):

| quota-axi provider | Model provider prefixes it serves |
|---|---|
| `claude` | `anthropic`, `claude`; also the Claude Code harness |
| `codex` | `openai`, `codex`, `openai-codex`; also the Codex harness |
| `opencode-go` | `opencode-go` |
| `cursor` | `cursor` |
| `copilot` | `github-copilot`, `copilot` |
| `grok` | `xai`, `grok` |
| `kimi` | `kimi`, `moonshot` |
| `zai` | `zai`, `z-ai`, `zhipu` |
| `agy` | `google`, `gemini`, `agy`, `antigravity` |
| `alibaba` | `alibaba`, `qwen`, `alibaba-token-plan` |
| `minimax` | `minimax` |
| `mimo` | `xiaomi`, `mimo` |
| `deepseek` | `deepseek` |
| `openrouter` | `openrouter` |
| `commandcode` | `commandcode`, `command-code` |

A provider without a row (for example `elevenlabs`) has no model provider in agent-net. A model id with no provider part is mapped through its harness, which is why the Claude Code and Codex harnesses are named above.

A worker model may be a registered profile name, ignoring case. It uses the profile's harness, model and effort. Without a harness, an explicit model prefers a signed-in harness over one whose sign-in state is unknown and skips harnesses known to be signed out. The result's `model_note` names the choice. A worker that fails with HTTP 401, HTTP 403 or "not logged in" blocks that harness and provider on that machine until a later worker succeeds on the same provider.

A model provider that is not a subscription and has no usable quota reading is pay-per-use. This includes OpenRouter and OpenCode Zen (`opencode/`). An explicit worker model never resolves to one from a loose or bare name; write the full provider id, for example `opencode/gpt-6.1-sol`, and the start result says `uses pay-per-use provider (opencode)`. The same rule decides the worker provider defaults (decision [0026](decisions/0026-worker-difficulty-and-scores.md)).

## Tools inventory

Tab 5 opens Skills per project: projects on the left, skills, commands and
agents with checkboxes on the right. Commands (`/name`) and agents (`name
(agent)`) are switched on and off per project exactly like skills, through the
same `tools_toggle` and `tools_apply` methods. They are listed after the
skills. Project and session token counts and costs count skills only. The
rest of this section says skill for all three kinds. The project list comes from `tools_projects`, which includes
only projects inside a Git repository. Seen folders without a Git root
are excluded. Each project keeps its skill count.
The visible tabs at the top show Skills per project and All
tools; Tab switches between them. The focused pane has an accent border.
The selected row has the Sessions highlight and marker. The project keeps
a dim highlight while focus is on skills.

The information box below the panes follows focus. For a project it shows
the name, checkout machines, skills on and tokens per session, plus any
known reason a checkout cannot hold project skills. Reasons reported by a
gateway plan remain visible here after closing the plan. For a skill it
shows the name, tokens per session, up to two lines of its front matter
description, how many projects have it on, its machines, and the standing
choice for new projects. The count and token total count each name once
on the machine with the highest total, using the highest cost among its
copies. Costs are tokens in every session.

Use up/down in either pane, right or Enter to enter skills, and left to
return to projects. Space or Enter on a skill previews an on/off change
through `tools_toggle`. Nothing is imported or applied during preview.
Esc cancels the preview and keeps the pending choice for editing. To review
several changes together, cancel each preview and select another skill;
the next preview includes all pending choices for that project. Enter
with pending choices reopens their review. `d` shows exact folders. Enter
in the review applies each skill's plan in turn through `tools_apply`.
The result reports changed targets or `Nothing changed: <reason>` when no
target changed. Esc with pending changes asks `Discard N changes? y/n`.
Repo skills stay on in their repo and must be changed there. The gateway
handles importing an observed skill and preserving other project choices.
When skills are installed outside agent-net, the view offers `i` to import
them all in one review. Confirmation saves each distinct version in the store.
Installed copies and their session availability stay unchanged; no assignment
or installation is applied. Repo skills are excluded.
Skills whose inventory manifest file sizes sum above the 2 MB import limit
show a one-column `×` marker and cannot be toggled here. The state column
explains the size limit; Space repeats the explanation. These skills still
show their StartTokens cost because their descriptions load in sessions.
Each copy is measured separately; no bundle is packed for this check.
Import all skips those skills and lists
the skipped count, names, sizes and limit in its review. Missing manifests
do not prove that a skill fits; the import service still checks the bundle.
`/` filters skill names. Tab switches between this view and all tools,
which keeps the action menu, comparison, filters and `n` to add a tool.
Every Tools screen lists its local actions with one display key per action.
Aliases still work and are listed in `?` help. The key bar wraps when
needed and includes navigation, toggles, review, view switching, filtering,
import, help and quit.
The list includes `t` for costs and `R` for rescan;
plans include `d` when exact folders are available.

`agent-net tools` groups the latest observations by kind, name and content
hash. Each row shows scope, harnesses, machines, the number of same-name
variants and state, desired assignment and pending application. Scope lists global and project observations;
unassigned identifies project locations that match neither the registry nor a seen project. Offline
machines retain their last observations as stale. The daemon scans at startup,
after reconnecting, after its binary version changes and every five minutes.

An in-home skill folder reached through a skill-entry symlink is recorded at its real path, with the link path in `aliases`. Links outside the home are not followed. A separate content fingerprint ignores executable bits, `.DS_Store`, `Thumbs.db`, and editor swap and backup files when comparing copies. The strict hash used for imports and integrity checks still includes these files and executable bits. Older observations without a content fingerprint are compared using their strict hash.

For each skill, inventory also records a file manifest: relative paths, sizes, per-file fingerprints and the newest modification time. It never contains file contents. Common noise files are excluded. The manifest is limited to 200 files and 64 KiB; total manifests in one report are limited to 1 MiB, alongside the other report limits. A manifest above its per-copy limit is marked too large to compare and has no partial file list. Missing or oversized manifests cannot produce an exact comparison. The comparison shows file additions, removals and changes. File dates do not decide which copy to keep. When all shared files match and one copy contains additional files, it recommends that copy; otherwise it says there is no clear winner. The comparison shows the known users, store match and file modification time, and marks unknown origins and editors. It asks a read-only automatically chosen worker for a summary of the two SKILL.md files, caches successful summaries for the content pair, and shows a fallback when a summary is unavailable. Press `a` or `b` to choose a copy; `enter` does not choose one. Times describe file metadata and do not prove which version the owner intended. The plan compares observed copies; a destination that has changed or conflicts can still refuse application.

**Prompts and agents.** Inventory also reports Claude Code slash commands and subagents as items of kind `prompt` and `agent`. A prompt is one Markdown file in `~/.claude/commands/` or `<checkout>/.claude/commands/`. An agent is one Markdown file in `~/.claude/agents/` or `<checkout>/.claude/agents/`. The item name is the file name without `.md`, and the harness is `claude`. Only the files directly in the folder are read: namespace folders below it, files that are not `.md` and names that start with a dot are ignored. A link to a file inside the home is followed and recorded at the real path with the link in `aliases`. A link outside the home is reported as an error, like a skill. A file is a bundle of one file named `<name>.md`: its hash is the strict hash of a folder that holds only that file, so the same hash, content fingerprint, manifest (one entry), size limit (64 MiB read limit, 2 MiB import limit) and description (front matter `description`) rules apply as for a skill. A skill, a prompt and an agent with the same name are three separate items.

A project skill item has `tracked: true` when `git ls-files` lists files under it in the project checkout. The daemon runs one `git ls-files` per project root in each scan (limited to the four project skill folders and the `.claude/commands` and `.claude/agents` folders, no network) and uses the answer for every skill in that root. A folder that is not a Git repository, or a failed `ls-files`, leaves `tracked` off. A scan from a daemon that is older than this field has no `tracked`, so its project skills show as `local` until it is upgraded.

The owner can request a refresh through `inventory_scan`. `inventory_get`
returns report pages with an `offset` request field and a `next` cursor.
`tools_items` returns item pages with optional `kind` (`skill`, `prompt`, `agent`, `mcp`, or `bundles` for the first three), `harness`, `project`,
`state` and `offset` filters. `tools_item` takes an `id` from that list and
returns its locations and file-list differences against same-name variants.
The owner-only `tools_skill_details` reads the file manifest of one observed
copy on one machine, so a comparison does not wait for a whole scan. The
machine checks that the skill is still observed and that its hash has not
changed before it reads the files. These methods require the owner connection
and cannot be called by an agent session. A variant count includes all observed
versions, even with filters.

When a compare plan opens and one side has no file manifest, the Tools tab asks
that machine for the one skill and shows `fetching details from <machine>...`
until the answer arrives. An offline machine shows `<machine> is offline: no
details` with the version and the last time the gateway saw the copy. A failed
read names the machine and the reason. A scan that fails for one source keeps
every other fresh observation and marks only that source stale, so one
unreadable or linked-out skill no longer hides new file manifests. A scan that
produces no successful observation at all leaves the previous observations
marked stale. The inventory is read-only and does not execute skills or servers.

A skill that cannot be hashed safely shows `cannot manage: <reason>` in Tools.
The reason names the file and rule: more than 64 MiB of total regular file
data, more than 10,000 entries, an absolute or escaping symlink, a special
file, an unreadable file, or a file that changes during the scan. For example,
a 114 MB model file exceeds the 64 MiB scan limit by itself. This is separate
from the 2 MiB import limit below. An `.env` file alone does not fail discovery;
recognized secret contents refuse import, and the refusal names the file
without showing its contents.

A fresh scan with one failed skill blocks plans for that skill name or an
overlapping source folder, including its aliases. It does not block unrelated
skills on the same machine. If a whole source root cannot be listed, its
unknown contents may hide the requested skill, so skill plans stay blocked.
A missing report, a report older than 15 minutes, a scanner or upload failure,
a report that exceeds its limits, or a scan with no successful observations
also blocks plans for that machine. Planning and applying refresh reachable
machines and require the fresh scan's upload acknowledgement. Conflict lines
name the machine, skill, project or source folder, and the actual reason.

MCP fingerprints omit
sensitive descriptor values. Project MCP entries also carry an opaque
`definition` digest so the same name can be compared across harnesses. It is
`opaque` when a value cannot be compared safely, and a same-name entry then
shows a conflict instead of a silent match. Extension-provided MCP servers, package-managed skills, OpenCode custom config
locations, Pi standalone Markdown skills and session-specific trust overrides need
manual inspection. A row summarizes observations, not proof that a running session has loaded it.

### Scoped MCP servers

A server in the [MCP registry](mcp-registry.md) is also a tool item of kind
`mcp`. `tools_items` marks it `federated` and shows its `scope` (`global`,
`projects` or `uninstalled`), the projects, its `host` and whether the host is
online. A server added by the owner with `agent-net mcp add` or the TUI, or by
a CTO `mcp_change` action, starts global. A server that existed
before scoping keeps that scope: no assignment means global. Agents can list
and test shared servers but cannot add or remove them.

Assign it the same way as a skill, with the exact item ID from `tools_items`:

```
agent-net tools plan 'ITEM_ID' project-one project-two
agent-net tools apply PLAN_ID
agent-net tools plan 'ITEM_ID' global
```

The plan has one target, with action `federate`, that names the host machine.
Nothing is copied to any machine: the gateway enforces the assignment. Apply
records the revision (with actor and audit event, like any assignment), and it
is in effect at once. `tools_undo` restores the earlier scope. Only the owner,
can plan and apply; the CTO runs these calls at once. An agent session cannot.
The CTO runs `tools_change` with action `plan` or `apply` and reads the owner inventory through `settings_change` area `operator`.

What a session sees:

- `global`: every session, including a session with no project.
- `projects`: only sessions whose project (the PROJECT column) is in the list.
  A session with no project does not see it.
- `uninstalled`: no session. The server keeps running on its host; the owner
  can still test it and change the scope.

The tool list of a session (the daemon method `mcp_tools`) holds only the
servers it may use. The gateway reads the project from its own record of the
session. The daemon keeps the answer for each project and serves it to every
session of that project, so repeated reads (the harnesses' safety polls) cost
the gateway nothing. A `mcp.changed` push, or a new gateway link, drops the
kept answer, so the next read is fresh. Applying or undoing an assignment
makes the gateway send `mcp.changed` to every daemon, and each daemon sends
`mcp_changed` to its sessions. Claude Code gets
`notifications/tools/list_changed` only when its tool list really
changed. Pi hides removed tools and updates its list from the next prompt.
Pi and OpenCode plugin connections are tested end to end: after the push, the
next `mcp_tools` is filtered for the session's project. The shipped OpenCode
plugin does not read `mcp_tools` yet, so an OpenCode session does not get
federated tools as tools; its connection gets the push and the filtered list,
and a stale call is refused at the gateway.

Codex starts one `agent-net mcp --codex` process for all threads of its app
server, and `tools/list` carries no thread. That process has no project, so it
lists only `global` servers. A server scoped to projects is not listed to Codex
at all, even for a thread of an assigned project; to use a server from Codex,
keep it `global`. The process hears `mcp_changed` at once (the same push as other
sessions) and updates its list, so an assignment change shows in the next
`tools/list` that Codex asks for. Whether Codex asks again during a running
session is Codex's choice and is not tested against the real Codex. A call to a
tool that is no longer listed fails with `unknown tool`. Removing a server deletes its plans and assignment; adding it
again starts global.

A call (`mcp_call`) is checked again at the gateway, whatever the client
cached; see [security](security.md#scoped-mcp-servers). It fails with `MCP
server "NAME" is not assigned to this project`. If the host machine is offline,
the call fails with `host NAME offline` and the tools stay listed, marked
`unavailable: host NAME offline` in their description. The assignment is not
changed. `agent_mcp_servers_find` and `mcp list` are the registry view of the owner and
of agents: they show every server and its `scope`.

Resources, resource templates and prompts of the registered servers follow the
same rules (see [the registry](mcp-registry.md#how-agents-see-resources-and-prompts)).
A session's `agent-net mcp` lists them with the daemon method `mcp_feature`
(`op` is `resources/list`, `resources/templates/list`, `resources/read`,
`prompts/list` or `prompts/get`). Lists hold only the servers the session may
see now; a server whose host is offline is left out of what the agent sees. A
resource is `agent-net://SERVER/URI` and a prompt `SERVER__NAME`. A read or
prompt of a server the session may not use fails with `MCP server "NAME" is not
assigned to this project`, of a disabled one with `MCP server "NAME" is
disabled`, and of an offline host with `host NAME offline`. A read or prompt
over 512 KiB is refused with its size and the limit. When the registry changes,
or a server reports a changed list, Claude Code gets
`notifications/resources/list_changed` and `notifications/prompts/list_changed`.

### Project MCP coverage

A project MCP server lives in the project files, not in the registry. The
inventory reads the project sources: `.mcp.json` (Claude), `.pi/mcp.json` (Pi,
trusted projects only), `opencode.json` or `opencode.jsonc` (OpenCode), and
`.codex/config.toml` (Codex, trusted projects only). For each server the daemon
records which installed harnesses have it in that project, on that machine. A
server present for some harnesses and missing for others is a difference.

The owner-gated methods are `mcp_coverage_plan`, `mcp_coverage_apply` and
`mcp_coverage_undo`. `mcp_coverage_plan` takes `project`, `server`, optional
`checkout`, `source_harness`, `source_path` and `node_id`, and `harnesses` (the
missing ones). It answers a plan with one target per file, the planned file
path, the source and the action (`create` or `update`). It re-reads the source
declaration and every destination; a source path in the request must match a
source the fresh scan found.
`mcp_coverage_apply` takes `plan_id`, checks every file still has the planned
hash, saves each file first, then writes. `mcp_coverage_undo` takes the same
`plan_id` and restores the files. Both answer `state` (`applied`, `conflict` or
`failed`) and the targets.

The written entry uses the harness's own shape. Claude and Pi use
`mcpServers`; OpenCode uses `mcp` with `type: local` and `command` as a list;
Codex uses `[mcp_servers.<name>]` in TOML. Only the new entry is inserted: the
rest of the file keeps its bytes. Environment values must be references such as
`${TOKEN}`; a literal value is refused. A same-name entry is never replaced.
Pi and Codex project files are read only in a trusted project, and the plan says
so.

### Importing native MCP servers

An MCP server that a harness has in its own config (`~/.claude.json` and
`.mcp.json` for Claude Code, `config.toml` for Codex, `opencode.json` for
OpenCode, `mcp.json` for Pi) can move into the federation. Import is explicit:
nothing is imported by a scan. See [decision 0027](decisions/0027-skills-and-mcp-management.md).

**Candidates.** `mcp_import_candidates` (owner-only daemon method) lists
every native entry on every machine. A request takes optional `machine`,
`harness`, `project`, `class` and `offset`. A page holds at most 30 entries and
`next` is the next offset. Each entry has a `key`, the machine, harness, scope
(`user` or `project`), project, file, name, and a `class`:

- `importable`: a stdio, http or sse server that the machine can run, whose
  secrets are given by environment variable names. The scan cannot know if the
  command exists; the test at import time finds out.
- `unsupported`, with a plain `reason`. The reasons are: the entry uses OAuth;
  it serves resources or prompts only; it hides some tools natively; it sets a
  working directory; an argument depends on the client's working directory, `~`
  or variable expansion (the server would need to run on every client); a
  secret is given as a value (environment value, header value, URL query,
  argument or URL credential) instead of a variable name; the transport is not
  stdio, http or sse; the entry is switched off; the name cannot be a server
  name; or it is agent-net's own server. Extension-provided MCP and entries
  from a machine that does not judge entries yet are `unsupported` too, so they
  stay visible. Names and reasons only: no value is ever returned.

An environment value counts as a variable name only when it is empty or a
plain reference to the same name (`"API_TOKEN": "${API_TOKEN}"`). The
federation runs the server on its host with the host's own environment.

**Plan and apply.** `mcp_import` (daemon method, owner only; the CTO uses `tools_change` action `import`) takes
either a `candidate` key and answers a plan, or a `plan_id` and applies it. A
plan changes nothing. It shows the server that would be registered (command,
arguments, variable names), the scope, and any conflict (an existing server of
that name, an offline machine, a changed entry, an unregistered project). A
plan with a conflict cannot be applied. Apply:

1. registers the server on the machine that has the native entry, disabled, so
   no session sees it;
2. runs the same test as `mcp_test`;
3. compares the tools the server reports with the tools the native entry lists
   (`enabled_tools` in Codex, `enabledTools` or `allowedTools` elsewhere), when it
   lists any. A listed tool that is missing, or a server with no tools, fails.
   When the entry has an allow-list, the federated server gets the same list
   (`allowed_tools` on the server): the gateway lists and lets through only those
   tools, even if the server reports more, so a session sees what it saw before.
   A call to another tool fails with `tool "X" is not exposed by MCP server`, and
   `mcp test` and health reports never widen the list. When the entry has no
   allow-list, every tool the server reports is accepted and the import record
   (`imported.tools`) keeps the list. The record also keeps `allowed_tools`;
4. enables it. A user-level entry becomes `global` (a per-machine scope cannot be
   expressed); a project-level entry becomes `projects` with that project.

If the test or the comparison fails, the server is removed again and nothing else
changed. The answer says `state: failed` and why. A plan applies once.

**What import does not change.** It writes no harness config file. The native
entry stays, so the server is now defined twice. The federated item shows
`imported` (where it came from), `native_still_present`, and a conflict that
names the machine and harness; the native item shows a conflict too. Removing
the native entry is a later step. Import does not carry resources, prompts,
OAuth or a hidden-tool list. The audit (`tools_history` of the federated item)
records the plan, the actor and the result.

### Removing a native MCP entry

After an import both the native entry and the federated server exist. The
owner may remove the native entry. `mcp_native_remove` (daemon method, owner
only; the CTO uses `tools_change` action `native_remove`) takes exactly one of
`candidate`, `plan_id` or `undo_plan_id`.

**Rules.** The plan refuses an entry whose server was not imported from that
exact entry, is switched off, is not healthy, reports no tools, or whose native
description no longer matches the federated server. Apply tests the federated
server again before it touches the file. The agent-net server entry is never
removed.

**Plan.** `candidate` (a key from `mcp_import_candidates`) answers a plan and
changes nothing: the file hash, the list of `gaps`, and any conflict. A
conflict cannot be applied. `gaps` and the `federation_gaps` of each
importable or imported candidate (and of the `imported` record on the
federated item) say what the native entry can do that the federation does
not: resources and prompts (the federation carries tools only), OAuth sign-in,
and per-client execution (the server runs on one host, not on each client).

**Files that can be edited.** The file must be a known native source that the
scan saw:

| Harness | File | Where the entry is |
|---|---|---|
| Claude Code | `~/.claude.json` | `mcpServers`, and `projects[<path>].mcpServers` for a project-local entry |
| Claude Code | `<project>/.mcp.json` | `mcpServers` |
| Codex | `~/.codex/config.toml`, `<project>/.codex/config.toml` | the `[mcp_servers.<name>]` table and its sub-tables |
| OpenCode | `opencode.json` or `opencode.jsonc` (user and project) | `mcp` |
| Pi | `~/.pi/agent/mcp.json`, `<project>/.pi/mcp.json` | `mcpServers` |

**Format preservation.** The edit cuts only the bytes of the entry. Other
keys, their order, comments (JSONC and TOML) and indentation stay as they were.
The daemon does not re-write the file from parsed data. In JSON it removes the
member and one comma. In TOML it removes the entry's tables; comment and blank
lines that follow a table stay with what comes next. Before it writes, it
parses the old and new text and proves that the new text no longer holds the
entry and equals the old document in every other value. If the entry is in a
form the editor does not own (a TOML inline table or dotted key, a duplicate
key, a file that does not parse), nothing is changed and the plan says to
remove it by hand. There is no whole-file rewrite fallback. A file that is a
symlink is not edited. The file mode is kept.

**Committed project files.** If Git tracks the file in a repository (a
committed `.mcp.json`), it is not edited. The conflict reads "committed in the
repo: remove it with a commit".

**Apply.** `plan_id` applies a plan once, only on the owner's explicit words
when the CTO does it. It edits only a file that still has the planned hash;
otherwise it answers `refused` with "changed since plan". It first saves the
whole original file in `mcp-backups` (mode 0600), writes a journal record, then
writes a temporary file beside the original, checks the hash again and renames
it over. For `~/.claude.json` it takes Claude Code's own `.claude.json.lock`
directory first. After the rename it reads the file again, checks the bytes
are exactly the planned result, and checks that the entry is gone. A session
that is running keeps its native tools until it restarts, so the gateway tells
it. Each online session of the entry's harness on that machine gets one message
from the owner session: "the MCP server X moved to agent-net; restart this
session to drop the old copy". A user-level entry reaches every such session; a
project-level entry reaches the sessions of its project. Sessions of other
harnesses and other machines are not told. The message needs no reply, and it
is sent at most once per session for a plan (undo sends nothing). The result
lists the session IDs in `notified` and adds a note with the count. A session
that cannot be reached still gets the stored message when it returns. A failure
to tell a session does not fail the removal.

**Undo.** `undo_plan_id` restores the original file only if the file still has
the hash of the edit's result. Otherwise it answers `refused` with "changed
since apply" and keeps the file: it never overwrites a later edit. Undo is
recorded in the audit as `mcp_native_undo`. After undo the native entry is back
and the federated server is still registered, so the same server exists twice;
the answer says so.

**Audit.** `tools_history` of the native item records `mcp_native_plan`,
`mcp_native_remove` and `mcp_native_undo` with actor, machine, file path and
result. It never holds file content.

**Secret sources.** An entry that gives a secret as a plain `${VAR}` reference
becomes a federated server that records only the variable name. The host reads
the value from its own environment when it starts the server. The value never
moves and is never stored in the registry. Only the backup of the original
file, which stays on that machine, can hold a literal value the file had.

### Desired tool assignments

The owner socket also accepts project switches for skills. `tools_toggle`
takes `item`, `project` and boolean `on`. It returns the ordinary plan; confirm
it with `tools_apply`. A switch applies to every content version of that skill
name across the project's checkouts. Different versions produce one group ID
with per-identity member plans, snapshots and revisions. Apply checks all
members before saving their revisions together. Undo accepts the group ID
or the selected item and reverses every member. The daemon feed stays per
identity. Placement uses the checkout's own version first, then its machine's
user copy, and otherwise the newest library version.
Turning a global skill off in one project moves it to
copies in every other known project in one plan. The review names the
user-level folder and says how many projects receive copies before the owner
confirms, and the plan removes the user-level copy. Turning the last project
off makes it uninstalled. Switching a skill on in a project where a user-level
copy already serves every project changes nothing and says so. The owner
chooses projects only.
The Skills row checks all versions across the project's known checkouts.
The information box lists machines whose copies differ.

`tools_new_projects` takes `item` and boolean `on`. It saves the standing
choice and returns a placement plan to confirm with `tools_apply`. For example,
turning this choice off for a global skill plans copies in all current projects
instead of user folders. The default is on for a global skill and off for other
assignments. Existing off cells stay off when the choice is enabled. When a
new project is first reported, the gateway adds it to project assignments with
this choice on. Those automatic plans use actor `standing choice`, the normal
journal, and the normal apply and undo path. Global assignments need no change.

`tools_projects` returns an array of `ToolProjectState` rows: `item`, `projects`
(a map from every known project name to on/off), `new_projects`, and `machines`
(the node IDs with observed copies). Targets use the same registered or seen
project set as assignments. The daemon checks Git placement before any copy.
These three socket methods require the owner connection. Their gateway frames
are `tools.toggle`, `tools.new_projects` and `query.tools_projects`.
The CTO runs `tools_change` actions `toggle` and `new_projects` at once; each
leaves an Inbox notice with undo.

A switch on an unmanaged user-folder skill first imports its current content.
A size, secret or hash refusal also refuses the switch. Import leaves the source
alone until the plan is applied. Migration limits the assignment to the node
IDs where that content is found today. It never spreads the skill to a machine
that did not already have it. Plugin, package and system copies cannot be used
as the import source for a switch.

The owner can plan a global, project or uninstalled assignment. Use the exact
item ID from `tools_items`, including its brackets and quotes:

```
agent-net tools plan 'ITEM_ID' global
agent-net tools plan 'ITEM_ID' project-one project-two
agent-net tools plan 'ITEM_ID' uninstalled
agent-net tools apply PLAN_ID
agent-net tools history 'ITEM_ID'
```

`tools_plan` takes `item` and `target`, where `target.scope` is `global`,
`projects` or `uninstalled`; projects scope also takes a `projects` array.
`target.machines` is an optional array of machine names or node IDs that limits
the change to those machines ("global on research only"). It combines with
every scope. The gateway stores node IDs, sorted and without duplicates, and
refuses a name that matches no enrolled machine or more than one. Empty or
missing means every machine, which is what every assignment stored before this
field existed means. The plan, its targets and its audit events carry the
machines (`assignment.machines`). A machine that is not named gets no target,
and a daemon also ignores any target that names another machine. A machine that
is not named keeps what it has: the new revision does not remove or restore
anything there. A registry MCP server runs on its host, so `machines` is refused
for it. `agent-net tools plan ITEM global --on research --on mac` sets it from
the command line. The last-copy check counts a copy on an unnamed machine as
staying.
It refreshes reachable machines and lists the known machines, harnesses,
project checkouts, sources, destinations, intended actions and conflicts. Offline machines and unsupported
harnesses are shown as unresolved. A skill that has not been imported and an
unmanaged destination are conflicts. Review the plan before applying it.

`tools_apply` takes `plan_id`. It refuses an out-of-date plan if its inventory,
known projects, item version, assignments or inspected paths have changed.
Applying records a new desired revision and nudges connected daemons. Each
daemon pulls its own targets again after reconnecting and on the regular scan
interval. A plan that would remove the last assigned copy of an unimported
skill is refused; import it first. For global skills the daemon fetches the stored version, verifies its hash
and installs a missing destination using a temporary directory beside it and an
atomic no-replace rename. A different existing destination is a conflict.
Global assignments place copies in `~/.claude/skills` and `~/.agents/skills`,
and in `~/.gemini/config/skills` when `~/.gemini` already exists. Project
assignments place copies in `.claude/skills` and `.agents/skills` in each
known Git checkout: registered ones and ones seen in a session. No
registration step is needed. OpenCode reads both folders and loads the skill
once. The machine refuses a checkout that is the home folder, a folder that is
not a Git working tree, or a worktree of another project, with that reason in
the plan. A same-named skill in another source that can shadow these copies is
a conflict. The scanner also observes project `.agent/skills` without managing
it. Removals move an agent-net-owned verified source to the daemon's local backup
directory before removing it from the skill location.

**Prompt and agent files.** A prompt or an agent is copied as one file, with the same checks, journal, manifest, backup and undo as a skill. The destination is `~/.claude/commands/<name>.md` or `~/.claude/agents/<name>.md` at user level, and `<checkout>/.claude/commands/<name>.md` or `<checkout>/.claude/agents/<name>.md` for a project. Only the `claude` harness and these two folders are used: no `.agents` folder, no Antigravity folder and no shadow check against other harness folders. The plan target names the folder in `root`, and the daemon refuses any other root for these kinds. The daemon unpacks the stored one-file bundle into a staging folder beside the destination, checks its hash, then moves the file into place with an atomic no-replace rename and removes the staging folder. An existing file with other content is a conflict and is never overwritten. A file with the same content that agent-net did not write is recorded as not owned and is never removed. Removal moves the file to the local backup folder (a copy and delete when the backup is on another filesystem), and undo of a removal puts it back from the backup if the original path is still free.
It compares the source hash with the planned version and reports a conflict if
the content changed. Backups retain the original path under a timestamped
directory. Moves wait for every planned copy
to succeed before removing an old copy. Results show partial progress while
targets remain unfinished.

`tools_apply` answers with the new revision and one result per target: `ok`,
`pending`, `conflict` or `failed`, with a reason when it is not `ok`. The
gateway waits up to three seconds for connected machines to report, so a
fast local apply shows its real result. A machine that is offline, or slow,
stays `pending` and applies when it reports later. `tools_item` shows the
final results.

#### Journal and crash recovery

Every skill write is journaled: a copy into a skill folder (global or
project), a removal into backup, a restore, and a move across filesystems.
The daemon writes the record to `skill-journal` before the first change, updates the
step after each change, and deletes the record when the operation ends. A Git
`index.lock` that the daemon creates is recorded first, with a private token
written inside the lock file.

At each daemon start the daemon reads the leftover records and:

- deletes a staged copy that was never renamed into place;
- finishes the manifest and exclude update when a source was already moved to
  backup;
- finishes a guarded source removal when a cross-filesystem backup was
  complete;
- removes an `index.lock` only if it holds the record's token. A lock that
  someone else made stays;
- leaves anything whose hash does not match the record alone. It writes a
  `skill journal conflict` line to the daemon log, keeps the record, and
  answers `conflict` for that path ("interrupted operation ...") until the
  owner has looked at it and deleted the record file.

A move to backup tries a rename. When the backup folder is on another
filesystem (`EXDEV`), the daemon copies the skill to a staging folder in the
backup folder, checks the hash, renames it into place, and removes the source
only if its hash still matches. Every step is journaled.

#### Undo

`tools_undo` takes an `item` and reverses the item's newest revision. Undo is
a new audited desired revision, like an apply: it takes the previous
assignment (or `uninstalled` when there is none) and a plan that reverses only
the targets that reported `applied`. Undoing a machine-scoped change therefore
touches only the machines it changed. It answers with the same per-target
results as `tools_apply`. Offline machines stay pending and undo when they
reconnect.

- An installed copy is removed only if it is still the copy agent-net made
  (owned, same hash). A copy backup is kept. An owned Git exclude line is
  released when its last reference goes.
- A removed skill is copied back from the local backup only if the original
  path is still absent. The backup stays for another undo.
- If the current content differs ("changed since apply"), that target stops
  with a `conflict` and the files stay as they are. Undo never overwrites a
  later edit.
- Undoing an undo applies the first revision again.

Running sessions may need a reload to see the change.

#### Backup retention

The daemon keeps the newest 20 backups per item and none older than 30 days.
It prunes after each removal and at start. It never prunes a backup that an
unfinished journal record names. `tools_history` adds a `backups` list for
this machine with each backup's `time`, `size` and original path. It never
returns content.

Before a project copy, the daemon checks for tracked files and Git ignore rules.
It locks and adds the exact managed directory to the path returned by
`git rev-parse --git-path info/exclude`. Linked worktrees can share that file;
the daemon records references to the shared line in its manifest. It does not
change `.gitignore` or the Git index. It removes an owned line after its last
reference is removed. Matching foreign copies are left alone.

Daemons report each target as applied, pending, conflict, manual or failed.
`tools_items` and `tools_item` show the desired revision and reported results.
A missing report stays pending, including while a machine is offline. The
daemon records installed paths, hashes and revisions in its owner-only
`skill-manifest.json` in the daemon state directory. Backups are in
`skill-backups` and the operation journal is in `skill-journal` under that
directory. Uninstall removes the manifest, backups, journal,
and owned Git exclude lines but does not remove installed skills.
A stored item with no remaining observed locations stays listed as `unobserved`.
`tools_history` takes `item` and returns plans and apply attempts with their
actors, targets and results. These methods require an owner connection.

The private CTO cannot inspect tool inventory. Plans, apply, undo, import and native removal
all become proposals. Only an approval in the Inbox runs the stored call. Charter `needs` are advisory text;
the owner tool list shows them as unverified needs and never turns them into
assignments.

### Managed skill versions

The owner can import an observed skill, prompt or agent with the `tools_import` operator method. A prompt or an agent is stored as a bundle of one file `<name>.md`, under its own kind: the same name as a skill is a separate item with its own numbers. Reading a stored version (`tools_read_file`) uses the path `<name>.md`. Applying works for the three kinds the same way (see "Prompt and agent files" below).
Pass `item` as the ID from `tools_items` and `location` as an object with
`node_id` and `location` from that item's locations. The source machine must be
online. The gateway copies skill contents as supplied and does not scan them
for secrets. The owner is responsible for keeping secrets out of skills before
importing them. Import reads the selected directory and does not change it. The
gateway rejects an import if its contents no longer match the observed hash.
The packed bundle must be at most 2 MiB and may contain at most 2,048 entries. A
larger skill can still appear in inventory but cannot currently be imported.

`tools_versions` takes `item` and returns the saved hashes, Git commits, source
machine and path, and import times. `tools_export` takes `item` and `version`
(the content hash) and returns a JSON object with a base64 `data` field holding
the tar bundle. Export verifies the bundle hash. Import, version listing and
export require the owner connection. Reimporting the same content hash for an
item returns the existing version. A saved version remains available when its
observed locations disappear.

**Version numbers.** Each new content hash that is imported for a skill name gets the next number: v1, v2, and so on. The number is stored with the version (column `number` of `skill_versions`), never changes, and is counted per skill name in import order. An upgrade fills in the numbers of existing versions in import-time order. `tools_versions` returns it as `number`. `tools_items` shows it as `version_no` on an item and on each of its locations; `0` or a missing field means no stored version has this content. The Tools tab writes a stored version as `v3`. A copy of a skill that has stored versions, but whose content is none of them, reads `changed` (it was edited outside agent-net). A skill with no stored version at all shows a short content hash.

**Changed here, save, update.** Copies drift when someone edits a deployed copy. agent-net notices this, can store the edit as the next version, and can then bring the other copies up to date. It never overwrites an edited copy by itself.

- *Noticing.* The daemon's `skill-manifest.json` holds the content hash of each copy the daemon deployed. During every inventory scan the daemon compares that hash with the copy's current hash. The scan already computes the current hash, so this adds one small file read and no extra scan. A copy that differs is marked `changed_here` in its inventory item, together with `deployed_hash` (what the daemon put there). `tools_items` shows both on each location, and `deployed_no`, the store number of `deployed_hash`. Copies the daemon did not make are never marked. In the Tools tab the skill's row reads `changed here on MACHINE` with the `!` mark, and its detail says which copies changed and which version they came from.
- *Save.* `tools_save_changed` takes the same `item` and `location` as `tools_import`. It refuses a copy that is not marked changed here, then does a normal import, with the same 2 MiB limit, hash check and provenance. The copy becomes the next version (v2 after v1, and so on). Nothing is deployed. In the Tools tab, open the skill's actions and choose `Save the copy on MACHINE as the next version`. The CTO uses the `tools_save_changed` operator method, which waits for approval in the Inbox.
- *Update.* `tools_update_plan` takes the `item` of a stored version and lists every other copy of that skill: `update` (an unedited copy of an older stored version, which will be replaced) or `kept`, with the reason (changed here, content that is not a stored version, offline machine). It writes nothing. `tools_update_copies` takes the same `item` and the `copies` (`node_id` and `location`) the owner confirmed. The gateway plans again, refuses the whole request if any named copy is no longer a plain update, and asks each machine to replace its copy. The machine repeats the checks (the copy still has the planned content, is not changed here, and lives in `.claude/skills` or `.agents/skills` under home or a project checkout), backs the old copy up with the same guarded removal that "Remove everywhere" uses, installs the new version with the normal copy code, and moves the backup back if the install fails. A project copy stays out of Git status as before. The result lists each copy as `applied` or `failed` with a reason. In the Tools tab, saving a changed copy ends with this plan, and `Update older copies to vN` in the skill's actions opens it at any time. Enter confirms. The CTO uses `tools_update_plan` (a read) and `tools_update_copies` (waits for approval).
- *Never overwritten.* A changed copy that is not saved stays as it is and stays marked. Update lists it as kept and refuses to name it, and the machine refuses it again at the moment of replacing.
- *Older assignments.* A copy that an older assignment made now holds a newer version. When that assignment is reconciled, it reports the conflict `replaced by a newer version` instead of `destination differs`. Assign the newer version to make the assignment current.

The gateway keeps the private bare Git repository in `skills.git` under its
data directory. Each imported identity has its own directory in the repository;
SQLite records the commit, content hash and provenance. Imports do not deploy
skills to any machine.

To back up a running gateway, connect through an owner daemon and use:

```
agent-net gateway backup --out /absolute/path/to/new-backup
```

The path is on the gateway machine and must be a new directory outside its data
directory. The backup includes a consistent database snapshot, the skill Git
repository, and the gateway CA and server keys. Keep it private. To restore on
the gateway machine, run:

```
agent-net gateway restore --backup /path/to/backup --to /path/to/new-data
```

The destination must not exist. Restore checks database integrity, Git objects,
and every stored skill hash before creating it. Configure the gateway to use the
new data directory before starting it. It never replaces a live data directory.

## The project registry

Without a registry, a project exists only after a session ran in it. A session in a system temporary folder does not make a seen project (see [what each machine reports](#what-each-machine-reports)), and a session with `AGENT_NET_IGNORE=1` is not registered at all. The registry lets you (or an agent, when you ask) say "this repository is a project" ahead of time. Workers (see [decision 0002](decisions/0002-workers.md)) start sessions only in known projects, and a registered project is known even if no session ever ran there.

Each entry has the machine, the path of the main working tree, the identity derived from the `origin` remote, the `origin` remote without credentials, the default branch (the branch `origin/HEAD` points to, when the clone has it), who added it (`cli`, or the harness and session), when it was added and when it was last checked. The gateway keeps entries in the `projects` table of `gateway.db`. `agent-net projects` merges them with the projects found from sessions, one row per repository per machine. SOURCE says where a row comes from: `registered`, `seen` (only sessions) or `both`. Registered projects stay when their sessions are deleted by retention. Skills accept both kinds: a project does not need to be registered to be a skill destination (decision [0048](decisions/0048-skills-on-off-per-project.md)).

**Project identity and display name.** The last part of the `origin` URL, without `.git`, is the project identity. For example, `https://example.com/team/agent-net.git` gives `agent-net` even when the checkout folder has another name. With no usable origin, the main tree folder name is used. Repositories with the same last remote path part share an identity, even when their owners or hosts differ.

The gateway stores an optional display name for each identity. In Graph, select a node, press Enter to open its card, then press `D`. Enter saves, an empty value clears the name, and Esc cancels. `?` lists the key. The CTO uses `project_change` with action `display`, `project` and `name`, and reads display names through `projects` or `session`. The owner operator methods have the same names. A project list entry carries `name` (identity) and `display_name`; `agent_sessions_find` and `agent_session_get` carry the identity in `repo.project` and the optional `project_display_name`. Actions continue to use the identity.

On upgrade, the gateway uses stored origin URLs to move old folder-based project data to the repository identity. When charters meet, both texts are kept and `project.identity_merge` is logged. If graph controls conflict, that pair is left unchanged, `project.identity_conflict` is logged with `from` and `to`, and the gateway continues with other projects. Ambiguous folder names that point to different repository identities are left alone. This migration runs once; logged conflicts and ambiguous mappings need review.

**Registration runs on the machine that holds the folder.** The daemon of that machine checks each path and sends the gateway the checked entry:

- the folder exists and is inside a git working tree (a bare repository, a plain folder, a file or a missing path is skipped, with the reason);
- the path is resolved to its main tree, with the same rule as for sessions: a linked git worktree belongs to its main tree, and a submodule is its own project. A folder inside a repository, and a symbolic link to a repository, register the repository's real main tree;
- the remote has its credentials removed, by the daemon and again by the gateway.

`~` and `~/path` mean the home folder of the daemon's user. Other paths must be absolute (the CLI makes relative paths absolute for you). The entry always lands on the machine whose daemon was called. There is no way to name another machine when adding: not for agents, and not for the CLI. Registering a path again is not an error: the entry is reported as existing, and its remote, default branch and last-checked time are refreshed.

**Scan.** `projects scan DIR` and `agent_project_add` with `dir` and `scan=true` walk a folder:

- up to `--depth` levels below it (default 3, at most 8). A repository at the deepest level is found;
- a folder that holds a `.git` entry is a repository: the scan registers it and does not go inside, so nested repositories and submodules are not looked at;
- it does not follow symbolic links, and it skips hidden folders (which covers `.git`, `.claude/worktrees` and `.worktrees`), `node_modules` and `vendor`;
- a linked worktree found by the scan is skipped, with a reason that names its main tree, because the main tree is the project;
- at most 200 repositories are looked at per call. Repositories found beyond that are counted as overflow and not registered: scan a smaller folder;
- the answer lists what was added, what already existed and what was skipped, each with a reason. `--dry-run` (or `dry_run`) makes the same list and changes nothing.

**Agent tools** (Claude Code, Pi, OpenCode): `agent_project_add` (`paths`, or `dir` with `scan`, plus `depth` and `dry_run`) and `agent_projects_find` (optional `name`). There is no tool to remove a project: removing is only done with `agent-net projects remove`. An agent that calls the remove method gets an error that tells it to ask you. The guidance text tells agents to register projects only when you ask.

**Project layout scan (`projects_layout`).** The CTO can query `projects_layout` (with optional `machine`) to inspect how repositories are structured across machines without manual configuration. The daemon on each machine scans disk under the user's home folder up to depth 4 for git repositories (folders with a `.git` directory). It is strictly read-only and never registers, writes, or modifies projects. Results are cached per machine for 10 minutes. Folders that hold 2 or more repositories at depth 1 or 2 become roots, categorized as `flat` (repos directly at depth 1) or `grouped` (repos at depth 2 under group folders, with loose depth-1 repos in `projects`). When the home folder directly holds 2 or more repositories, the home folder itself (`~`) acts as a flat root. Repositories not claimed by any root are listed in a per-machine `loose` list. Repositories and groups report newest commit timestamps, and groups report remote owners with project counts. Noise is dropped: the home folder itself as a repository (`~/.git`), git worktrees (a `.git` file), agent-net worker worktrees (`~/.local/state/agent-net/daemon/worktrees`), `.claude/worktrees`, `~/code/isolated-worktrees`, `node_modules`, `vendor`, caches, hidden folders, and temp folders.

**Agent graph read.** `agent_graph_read` (Claude Code, Pi and OpenCode) returns the graph that every agent may read, read-only: every node's charter fields with its maturity, every node's rules with name and value, and every edge with its kind, mode and request rule. The rules are the six node settings resolved against the node's maturity (`tests_before_merge`, `merge_authority`, `worker_sandbox`, `judge_consumers`, `deploy_rule`, `autonomy` (`off` or `wake`)) and the delegation policies the owner set for the node's project on each machine. `node` returns that node's full charter, its rules and the edges that touch it. A name that a repo node and an agent node (or a team) share returns each of them, the repo node first, so the agent's charter is not hidden; `note` names the count and the kinds, and a typed read with `ref` reads one alone. A name that only one node has reads as before. Without `node`, `offset` and `limit` page the list (default 20, at most 50), `total` counts the matching nodes, `next` is the next page offset, and `edge_total` counts the edges. Charters and rules are public; only changes are owner-gated. Secrets and tokens, machine paths and locations, learned-change history with evidence, hidden items and sessions stay hidden. The CTO's `graph` view uses this same read with the CTO caller.

**Versions.** A gateway from before this feature answers `unknown frame type` to the new requests, and the daemon turns that into "the gateway is too old for project registration". A daemon from before this feature does not know the new methods, and the CLI says so. An older gateway lists projects without SOURCE, and the CLI shows those rows as `seen`.

## Findings and the automatic playbook

The network learns from the agents that review workers. An agent records one **finding** with `agent_learn` (`agent_learn` in the MCP server and in the OpenCode and Pi plugins). It changes nothing by itself.

- **Subjects.** `subject_kind` is `model`, `harness`, `model_harness` (subject `model@harness`), `project`, `mcp` or `machine`. `subject` names it. `evidence` holds up to 20 session or worker IDs. A finding is at most 500 bytes.
- **Quiet.** Findings are never sent as messages and never appear in the Inbox. A session records at most 3 findings in 24 hours; the fourth is refused with the reason.
- **Promotion.** The gateway promotes a pattern to a playbook note on its own, with no model call, when there are at least 3 findings on the same subject from at least 2 different sessions within 30 days, or when the track record is clearly bad (at least 4 decided runs, stopped runs aside, and under half complete). The note is the latest finding's text, the support count and, for a model, harness or model on a harness, the track record line. An owner note or a pinned note is never changed by promotion.
- **Stale.** An automatic note whose support is all older than 60 days goes stale and is no longer shown. A finding that is never promoted goes stale after 90 days. Owner notes and pinned notes never go stale.
- **Delivery is only at the moment of choice:** the `agent_worker_start` result (notes for the chosen model, harness, project and machine) and `agent_machines_find` (the network's fresh notes). Never in the session context of every turn.
- **The owner sees it when he looks:** key `L` in the TUI lists the notes with their support; the header quietly says `learned N new` for automatic notes promoted in the last day. Nothing pops up.

Findings are in the gateway table `findings` with their reporter, time and status (`new`, `promoted`, `dismissed`, `stale`). Notes are in `playbook` with their source (`auto`, `owner`, `cto`), support, track record line and pin.

## Workers

A worker is an agent session that agent-net starts for you, with nobody watching it. See [decision 0002](decisions/0002-workers.md) for the rules and [0008](decisions/0008-operator-interface-and-worker-build.md) for how they are built.

- **Project skills:** Worker worktrees receive managed project skills and any other project skills already ignored by Git. Pi workers in those worktrees start with `--approve` so they load project skills.
- **Starting a worktree:** the result has `status: starting`, a session ID, the worktree path and branch while checkout and harness launch run in the background. The task is sent when the session is ready. A started or not-started notice follows; wait for it before messaging or stopping the session. The default background timeout is 10 minutes; set `workers.background_start_timeout` in `node.json` to change it. The gateway and the caller daemon must also support the `starting` result; upgrade them together with the worker machine.
- **Model fields:** `agent_worker_start` and CTO `worker_change` take `harness`, `provider`, `model` and `effort` separately. `provider` and `effort` are optional. For example, use `harness: "codex", provider: "openai", model: "gpt-6.1-sol", effort: "high"`. Old `provider/model:effort` inputs still work. Explicit fields override embedded values. Codex uses its reasoning effort, Claude uses `--effort`, OpenCode uses its model variant, and Pi uses its thinking level. For agy, the requested effort must exist in the machine's `agy models` inventory; a refusal lists the valid efforts. Results and notices display harness, provider, model and effort in that order.
- **Who starts one:** you (`agent-net start`), an agent (`agent_worker_start`), or the CTO with `worker_change`, which runs at once. A worker cannot start workers. The harness is `opencode` (the default), `claude`, `pi` or `codex`. A caller can say "implement this with a Claude, Pi or Codex subagent".
- **Where:** the project is looked up in `agent-net projects` (registered or seen). With no project, the caller's own folder is used: it is found by its repository root path on the caller's machine, not by name; a caller outside a known project falls back to its folder name. `project` may also be an absolute path: it matches the main tree or a listed worktree of a known project (use it when two folders share a name). A name matches a registered folder before seen-only folders of the same name on the same machine; it is ambiguous only between two registered folders, or between seen-only folders with none registered, and the refusal lists the paths. If it is on two machines, name one with `--machine`. The machine must be online, accept workers (not set up with `--no-launch`), and have the harness (`agent-net nodes --wide`). The gateway checks the model against that harness's list on the machine. A harness whose list could not be read passes the name through, and a model the harness does not know then fails at start with the harness's own message. The gateway says which check failed.
- **Worker permission levels** ([decision 0020](decisions/0020-worker-sandbox.md)). The owner sets a level per project (TUI key `p`, line Workers; the operator method is `session_policy` with `{"policy": {"workers": "read-only"}, "scope": "project"}`, owner only, refused at session scope). The level is stored as `workers.sandbox_level` for the project and applies on every machine. It is sent with each start; `worker_start` answers it as `permissions`, and `list --json` shows it in the worker part of the session. When the project policy sets no level, the node's maturity default applies ([maturity and its defaults](#charters-and-component-relationships)); a node with no maturity keeps `sandboxed`. Today Codex and Antigravity workers enforce permission levels with their own sandboxes. On Linux, Pi and OpenCode workers enforce them in the OS sandbox when it is on for the project (below). Otherwise Claude Code, Pi and OpenCode workers run with the owner's permissions; their recorded level is not enforced yet.

  | Level | Writes | Network |
  |---|---|---|
  | `sandboxed` (default) | The worker's worktree, the temp folders, and `objects`, `refs`, `logs` and `worktrees/<name>` of the main repository's git dir (what a commit needs) | on |
  | `no-network` | The same | off |
  | `read-only` | None | on |

  The project list includes each project's `permissions` level. The start result, the worker brief and worker notices show it too; the brief explains when writes or network calls will fail. The start result and brief say when the level is not enforced. A failed or blocked Codex turn with an explicit sandbox denial reports `blocked by the sandbox (<level>)` in the notice. A command's ordinary permission or network error alone does not prove a sandbox denial.

  **Permission enforcement.** Antigravity workers use `--mode accept-edits --sandbox` and a one-run allow list for `command(*)` and `mcp(agent-net_agent-net/*)`. The daemon finds Go on the harness search path, or else in a standard install folder (`~/.local/go/bin`, `~/go/bin`, `/usr/local/go/bin`, `/opt/homebrew/bin`, `/usr/local/bin`), and runs `go env GOROOT GOMODCACHE` with the worker's environment (five second limit). It adds each existing folder with `--add-dir`, except home itself and its parents, including symlink aliases. Antigravity workers can write to these toolchain folders; the permissions line and daemon start log list them. If Go is missing or the probe fails, no toolchain folders are added. Read-only workers use `--mode plan --sandbox` and only the MCP allow rule. The no-network level is not enforced for Antigravity. See [Antigravity workers](antigravity.md#workers) for the exact command.

  The Codex worker is started with approval policy `never` and the sandbox of its level (`thread/start` and `thread/resume`: `approvalPolicy`, `sandbox`, `config.sandbox_workspace_write`; `turn/start`: `approvalPolicy`, `sandboxPolicy`). Its private app server also gets `-c mcp_servers.agent-net.enabled=false` when `$CODEX_HOME/config.toml` defines the agent-net MCP server, so it does not call agent-net tools (its brief does not mention them either: the end of its turn is the report). Other MCP servers stay. The daemon still declines any approval that arrives. Claude Code, OpenCode and Pi workers record the level, and the TUI detail pane shows it on a `sandbox` line with "not enforced yet", but they behave as before, unless the OS sandbox is on.

  **OS sandbox (Linux, Pi and OpenCode).** `"workers": {"os_sandbox": ["shop", "blog"]}` in `node.json` on the machine that runs the workers turns it on for those projects; `"*"`, as the value or as an item, means every project. Names match without regard to case. Restart the daemon after a change. The daemon then runs `bwrap --unshare-all [--share-net] --ro-bind / / --dev /dev --proc /proc --tmpfs $XDG_RUNTIME_DIR --bind <worker folder>/tmp /tmp [--ro-bind /dev/null /run/docker.sock] --ro-bind <worker folder> --ro-bind <git common dir> --bind <worktree> --bind <git folders> --bind <private state> --ro-bind <worktree>/.git --bind <agent-net socket> --chdir <worktree> -- <harness> <args>`, with `TMPDIR=/tmp`. `--share-net` is left out at `no-network`; at `read-only` the worktree and git folders are bound read-only. The private state is `data` and `state` of the worker folder for OpenCode, and for Pi a private agent folder `pi-agent` (`PI_CODING_AGENT_DIR`, with a copy of `auth.json` and `models-store.json` and the other entries of your agent folder bound read-only) and the session folder (`PI_CODING_AGENT_SESSION_DIR`). There is no `--die-with-parent` and no `--new-session`, so a worker outlives a daemon restart and the daemon's stop reaches every process of the sandbox through bubblewrap's process group. Before each start the daemon checks that `bwrap` is on its `PATH` and that a probe sandbox works. The start is refused, with the reason, when a check fails, on macOS, for a `no-network` Pi or OpenCode worker, and for a Claude worker (also its follow-ups); a worker never runs without the sandbox while the switch is on. Codex and Antigravity workers are not wrapped. A worker that ran in the sandbox has the file `os-sandbox` in its worker folder, so its follow-ups and a daemon restart keep the sandbox even after the switch is turned off. The permissions line of the start notice, the brief and the completion notice ends with `(sandboxed by bubblewrap)` instead of `(enforced for Codex only for now)`, for example `permissions: sandboxed (writes only in its worktree, network on) (sandboxed by bubblewrap)`; the brief adds that writes outside the worktree and `/tmp` fail. The start result and the worker part of a session have `os_sandbox: "bubblewrap"`, and the TUI `sandbox` line shows `(sandboxed by bubblewrap)`. Design: [docs/design/worker-os-sandbox.md](design/worker-os-sandbox.md).
- **How, per harness.** All run in the project folder (or the worker's worktree), and none gets a flag that turns permission checks off: a worker uses the project's own permission settings.

  When herdr is available, Claude, OpenCode and Codex workers open a named view on the machine that runs them after registration, including on resume. A herdr client must be attached there to see it; use Enter to open a remote worker from the local TUI. If herdr is unavailable, an open TUI on that machine shows an installation hint and the worker continues in the background. Pi workers use RPC mode and have no interactive terminal to open; `agent-net show <session>` reads the conversation.

| Harness | Command | The model | The task and the report | Enter it later |
|---|---|---|---|---|
| OpenCode | `opencode serve` on `127.0.0.1`, random password | `provider/model`, checked against the machine's list | A normal message. The worker ends with `agent_handback`; the owner's next message starts the same session again. | `opencode --session <id>` |
| Claude Code | `claude --bg --name worker-<project>-<id> --settings <json> --mcp-config <json>` (a background session of Claude's own service, not a child of the daemon) | An alias (`haiku`, `sonnet`, `opus`, `fable`) or a full name, after `--model` | A normal message, delivered to the session's inbox. The worker ends with `agent_handback`, through the agent-net MCP server of this daemon's binary; the owner's next message runs `claude --bg --resume <session-id>`. | `claude attach <id>` (the id is the `jobId` in the session file) |
| Pi (needs `--isolation worktree`, or the daemon refuses it) | `pi --mode rpc --session-id <uuid> --name "worker <project>"`, standard input a named pipe that never closes | `provider/id[:thinking]`, after `--model` | A normal message, through the Pi extension, which loads in this mode. The worker ends with `agent_handback`, and it cannot be started again (Pi's rpc mode cannot open an existing session). | Not possible: Pi's rpc mode has no screen. Read it with `agent-net show`, or resume its session file in Pi after the worker ended. |
| Codex | `codex app-server --listen ws://127.0.0.1:<port> --ws-auth capability-token --ws-token-file <file>` | A name, in `thread/start`, with an optional `:<effort>` (`low`, `medium`, `high`, `xhigh` or `max`) sent as `effort` on every `turn/start` | The daemon runs each message from the owner or the person as a turn. The last message of the turn is the report, handed back by the daemon; the owner's next message starts a new app server and `thread/resume`s it. Approval policy `never` and a sandbox by [level](#workers). | `codex resume <thread id>` in the project folder |
| Antigravity | `agy --input-format stream-json --output-format stream-json --model <id> --mode accept-edits --sandbox --settings <json> -p=` | A model ID from `agy models`, including its effort | One stdin JSON line per turn. The plugin calls `agent_handback`; usage comes from the result event. Follow-ups reuse the process or add `--conversation <id>` after it exits. | `agy --conversation <id>` in the session folder |

  Claude Code needs the folder to be trusted (run `claude` in it once, or use a worktree of a trusted repository). The Pi extension and the OpenCode plugin must be installed on the machine (`agent-net setup`). The Claude session carries `crossSessionInbound: "accept"` and `AGENT_NET_SOCKET` in its per-session settings (`--settings`), so inbound agent-net messages are accepted; that per-session object does not change your `settings.json`.

  A Codex effort is checked at start against the model's `supported_reasoning_levels` in `codex debug models`. An effort the model does not list is refused, and the error names the levels it does. The model itself goes to `thread/start`; the effort goes to every `turn/start`, so it also holds after `thread/resume` and for a continued worker. The session, the start result and the notices show the full `model:effort`.

- **Commit identity.** Every commit a worker makes in its worktree carries its identity, for every harness, without the model adding any line:

  ```
  Agent-Net-Worker: s_6ca30600dd7bb802
  Agent-Net-Model: gpt-6.1-terra:high
  Agent-Net-Harness: codex
  Agent-Net-Difficulty: normal
  Agent-Net-Caller: agent-net (s_e01474bf7cf16e1e)
  ```

  The daemon writes a `prepare-commit-msg` hook in the worker's state folder and points only the worker's own processes at it, through `core.hooksPath` in the command-scope git config (`GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_0`, `GIT_CONFIG_VALUE_0`). The hook adds the trailers with `git interpret-trailers --if-exists replace`, so amending replaces the values instead of adding a second set, and then runs the repository's own `prepare-commit-msg` hook if it has one. The same folder holds a forwarding hook for every other standard git hook name (`pre-commit`, `commit-msg`, `pre-push`, `post-commit`, `pre-rebase` and the rest): each runs the repository's hook of the same name when it exists and is executable, passes the arguments and standard input through, and returns its exit code. A project's hooks therefore keep working, including one that blocks a commit. The values come from the worker record: its session, model with effort, harness, the difficulty judge's label (or `normal` on a fallback), and the caller's name and session. Each value is cut to one line. A worker without a worktree gets no hook, and a commit made outside the worker's environment, including a merge the caller makes later in their own checkout or another worktree, has no trailers.

  See who wrote a commit with:

  ```
  git log --format='%h %(trailers:key=Agent-Net-Model,valueonly)'
  ```

  Add `-1` for the newest commit, or use another trailer key such as `Agent-Net-Caller`.

- **Difficulty and automatic model choice.** The level comes from the difficulty judge, which reads the brief before the start and returns `trivial`, `simple`, `normal`, `hard` or `very_complex` from its six scores. The caller's `difficulty` and `touches` fields are accepted for older callers but ignored: neither changes the level. The judge has 90 seconds and never blocks a start; a missing judge, a failure, a timeout or low confidence starts at `normal`, and `model_note` says why. A continue keeps the label of the run it continues unless the caller adds work to the brief. With an empty model or `auto`, the gateway chooses a registered worker profile whose harness and model are available on the selected machine, by the results the picker has learned (decision [0105](decisions/0105-worker-picker-learns-from-results.md)).

  - **Task score.** Every finished worker task has a score from 0 to 1: 0 if it did not end complete, otherwise `1 - 0.25 x fix rounds - 0.25 x failed accept, review and test steps - 0.25 x (defects + defect severities)`, never below 0. A severity 4 defect costs 1.25. A run and its continuations are one task. The link is the task root of the run: `task_id` when the continue started a new session, otherwise the session id (a continue to the same session adds a run with the same session). The task has the first counted run's model, the first valid judge D and the final counted run's outcome. Fix rounds and session defects and failed steps (failed `accept`, `review` and `test` evidence rows) count once across the chain. Continuations have no penalty. A partial run with no continuation scores 0. Runs that ended `stopped` or `infra` do not count. The whole data set counts, with no time cut, and a defect counts whenever it arrives.
  - **Model key and cells.** The key is the model plus its effort, so `gpt-6.1-sol:low` and `gpt-6.1-sol:medium` are separate. A Codex run with no effort is `<model>:default`. D is cut into buckets of `picker.bucket_width` (one third) from 1.0. A cell is one key in one bucket: `score = (sum of scores + k x bucket mean) / (n + k)` with `picker.k` = 5, a variance shrunk the same way and never below half the variance of all tasks, and a 90 percent interval `score +- 1.645 x sqrt(variance / (n + k))` clipped to 0..1. An empty cell takes the bucket mean with n = 0. The gateway builds the table at startup and after a run, a judge result, a rating, a defect, a score or profile change, a `picker.*` setting or a failed step, and caches it. A pick reads the cache and runs no query for it.
  - **The pick.** The pick reads the bucket of the task's D (the judge's D, held inside its label's range; a failed or low-confidence judge uses the middle of the level). Candidates are the profiles available on the machine whose provider is under 90 percent used and not exhausted; protected providers, worker floors and the five-point penalty per running worker apply as before. `best` is the highest cell score among candidates with at least 3 tasks in the bucket, or the bucket mean when none has. A candidate with 3 tasks or more is allowed unless the top of its interval is below `picker.allowed_fraction` (0.9) x best. A candidate with fewer is allowed by its benchmark: the top of the D range of the highest level its score meets (trivial 1.5, simple 2.25, normal 3.0, hard 3.75, very_complex 5.0) must be above the bucket's start. A strong model is not left out for being far above the work: the qualifying set and the weaker bias below steer easy work to weaker models (decision [0112](decisions/0112-remove-the-overqualified-rule.md)). The qualifying set contains the allowed candidates with at least 3 tasks and a cell score (not the top of the interval) of at least `picker.allowed_fraction` times best. The pick rotates among them with weight equal to the quota pace factor times `(highest benchmark among the allowed / benchmark) ^ picker.weaker_bias` (default 1, so a lower benchmark weighs more). About `picker.explore_share` (default 0.10) of the picks go to an allowed thin cell (fewer than 3 tasks) instead, counted from a credit in the rotation state, not random. Explore is up only: a thin cell is explored only when the task is a step up for it, that is, it has a proven record (3 tasks or more and a score of at least `picker.allowed_fraction` x the best cell) in a lower bucket and none in this bucket or above, or it has no proven record anywhere and its benchmark level is exactly the task's level. Any other thin cell is left out of exploration and the note says `left out (explore is up only)`; when none is a step up, the proven models keep the pick. If nothing qualifies, the allowed set rotates as before, with the cell score and the uncertain boost in the weight. If no candidate is allowed, the old level rule picks (a score at or above the level minimum, the task at the model's max level or one below, a poor track record last) and the note says `fallback to the level rule`. If that fails too, the start fails with the judged level, the registered profiles and their max levels, and the fix: register a profile for that level, or pass `model` explicitly. The default minimum scores of the level rule are 0, 20, 30, 40 and 50.
  - **Settings.** `picker.bucket_width`, `picker.k`, `picker.allowed_fraction`, `picker.uncertain_boost`, `picker.uncertain_n` (fallback rotation only), `picker.weaker_bias` and `picker.explore_share` are network rules the owner and the CTO can change; a change rebuilds the table.
  - **Note.** The result's `model_note` follows the judge line with the bucket, the chosen cell, the qualifying set with the rotation weights, the thin cells open to the explore share and what was left out with its reason, for example `difficulty R3 S2 C2 U2 V2 K2 · D 2.65 · normal -> opencode opencode-go/deepseek-v4.1-flash (bucket 2.33-2.67: deepseek 0.76 [0.67-0.84] n42, best; qualifying: deepseek 0.76 [0.67-0.84] n42 weight 1.42, flash 0.72 [0.58-0.86] n17 weight 1.54; thin cells (explore share 0.1): sonnet n1 (benchmark) weight 1.52; terra:medium 0.27 [0.12-0.41] n6 left out (below 0.9×best); codex on pace (...); rotation weight 1.07; deepseek: 4 of the last 10 normal starts)`. A model with no record shows `n0` and `(benchmark)`; one with neither a record nor a benchmark is left out as `no record, no benchmark`. The gateway stores the note with the start, and the worker log shows it. The rotation state is kept per difficulty label in the gateway's `owner_settings` (`worker_rotation`) and survives a restart. An empty roster also fails with registration guidance.
  - **Read API.** The owner-only method `picker_get` (gateway `GET /v1/picker`) returns the settings in use, the task count, the mean and variance of all tasks, the buckets (`index`, `lo`, `hi`, `mean`, `n`), every model key with one cell (`score`, `lo`, `hi`, `n`) per bucket, and each registered profile with its benchmark score, level and ceiling. The TUI's regular refresh does not call it.
  - **Weekly scores.** The owner-only method `scores_weekly` (gateway `GET /v1/scores-weekly`) returns one row per ISO week for the last 8 weeks with `runs`, `mean_score` (the task score rule), `early_quit_rate`, `mean_fix_rounds`, `brief_defects`, `defects` (defects found later), `mean_tokens` per run, and `releases` (agent-net release tags recorded in delivery evidence during that week). The TUI Quality subtab toggles the weekly scores table with `w`.
  - Unprotected providers come first. It skips exhausted providers and windows above 90 percent, and subtracts five quota-room points per running worker on the provider. For a monthly window, it estimates elapsed time from the preceding calendar-month reset. A missing or stale quota reading is neutral. Explicit model requests override the picker and keep the resolution rules below; `model_note` says when this override was needed.
- **Ignored fields.** `agent_worker_start`, the CTO's `worker_start` and the CLI still accept `difficulty` and `touches`, so older callers do not fail. Both are recorded, but neither changes the level: the difficulty judge decides it. `touches` still feeds the per-area tables in the quality analysis, and `db`, `hot-path`, `ui`, `docs`, `tests`, `config` and `other` are the accepted values. The old one-level bump for `db` and `hot-path` and the one-level drop for `docs`/`tests` only are gone.
- **Worker profiles.** The gateway stores a network roster with each profile's name, harness, model, optional effort, creator session and creation time. `agent-net workers profiles` lists it; `agent-net workers profiles add <harness> <model> [effort]` adds an entry, and `agent-net workers profiles remove <name>` removes one. The CTO lists profiles with `agent_worker_profiles_find` and changes them with `settings_change` areas `worker_profile_add` and `worker_profile_remove`. Non-worker agent sessions have `agent_worker_profiles_find`, `agent_worker_profile_add` and `agent_worker_profile_remove`; they should change the roster when the owner asks. Model names can be loose and are checked against the harness's live model list on each chosen machine (or every online machine with that harness when the profile names none). `machines` limits a profile to those machines. `test: true` asks for a short real start before the profile is saved. Availability (`available_on`) comes from the profile's configured machines and the machines where it started a worker successfully; it never comes from a scan. The default profile name combines the harness, resolved model and effort. Adding a profile with an existing name replaces its harness, model and effort in one call; the change is recorded as a profile add.

  Each machine resolves the exact model ID first. If that ID is missing, it can use the same model name after the first `/` under another provider, but only when exactly one provider lists that name. Several matches make the profile unavailable on that machine. Automatic selection skips the profile only on machines where it does not resolve. A start result reports a provider change in `model_note`, for example `terra pi: openai-codex/gpt-6.1-terra resolved to openai/gpt-6.1-terra on research`.

  The profile list includes `machine_availability` for online machines reporting the profile's harness. Each entry has the machine name and whether the profile resolves there. `resolved_model` is included when the machine uses a different model ID. Failed entries include up to three `closest` model IDs; `ambiguous` lists all IDs that share the model name under multiple providers. `available` is true when at least one online machine resolves the profile, and false when none do. `machines_checked` names the machines checked. Profile-level `closest` lists up to three alternatives when the profile resolves nowhere. If no online machine reports the harness, availability is unknown and `available` is omitted. Each row also has `score` and `score_source` when the profile's model and effort match a capability score.
- **Temporary model health.** The gateway counts infrastructure outcomes across machines for each harness and provider-qualified model over the last two hours. Two failures within 15 minutes trigger a 15-minute cooldown; each further failure doubles it, up to two hours from the latest failure. A successful run clears the count. Automatic choice skips cooling models when another adequate model is available, and explains the skip or fallback in `model_note`. Explicit model requests are unchanged. Recorded judge timeouts also feed the signal, and a successful judge assessment clears it.

- **Worker providers.** Automatic selection uses the owner-controlled provider allowlist. When it is unset, Claude and Codex harness models, OpenCode and Pi models from `opencode-go` or `openai`, and providers with a usable quota reading are allowed. Other providers, including OpenRouter and OpenCode Zen, are excluded by default. `agent-net scores providers` displays the setting; `agent-net scores providers codex claude opencode-go` replaces it, and `agent-net scores providers --default` restores the defaults. The CTO can change it on the owner's instruction. An explicit model name keeps the resolution rules below: a loose name only matches subscription-backed models, and a pay-per-use provider is used only when its full provider id is written. The Quotas tab lists registered profiles and marks those excluded by this filter. A recent successful worker adds rotation weight.
- **Protected providers.** The gateway stores the owner setting `protected_providers` and its `reason` in `owner_settings`. The owner orchestrates with one model of choice, today often Claude on an Anthropic subscription. Workers on that same subscription spend the limit the orchestrator needs to keep working. Automatic choice uses other subscriptions first and uses a protected provider only when no other available profile qualifies under the max-or-one-level-below rule or the others are out of quota. The same difficulty, track record, quota, score and rotation rules choose among the remaining profiles. The result explains protected profiles skipped or used, including the reason for a fallback. An explicitly requested model is not blocked by this setting and the result notes when its provider is protected. Claude harness and unprefixed `claude-*` models map to `anthropic`; Codex `gpt-*` maps to `openai`; OpenCode and Pi model prefixes identify their providers.

  In the TUI Quotas tab, select a subscription with up/down and press `p` to protect or unprotect it, then answer `enter: yes · esc: no`: Enter or `y` protects, Esc or `n` leaves it unchanged. Press `s` to show subscriptions that are folded away. The CTO can propose replacing the list and reason; it cannot read them. For scripts, use `agent-net workers protect anthropic` or `agent-net workers unprotect anthropic`. The `protected_providers_get` and `protected_providers_set` operator methods are owner-gated: agent clients and command lines inside an agent session's process tree are refused. The Quotas tab marks protected profiles and subscriptions with a lock. Setup does not protect a provider and does not teach the protect command: the TUI teaches it when the owner starts using workers.
- **Worker quota floor.** The gateway stores the owner setting `worker_floors` in `owner_settings`, registered as `providers.worker_floor`. The owner can set, per provider, a minimum share of remaining quota (0 to 95 percent in increments of 5, where 0 turns it off) that workers must leave untouched. When a provider's remaining quota in any tracked window (such as a 5-hour, weekly or monthly window) is below its floor, the automatic worker picker excludes that provider's models from automatic picks entirely, not even as a last resort. An exhausted window (100% used) always excludes. The result's `model_note` explains the exclusion: `skipped <name>: <provider> has <N>% left in its <window> window, under your <F>% worker floor`. If the provider has no quota reading, the floor is not applied and `model_note` records `no quota reading, floor not applied`. A reading from another machine of the same network counts when the machine that would run the worker cannot read that provider's login: the subscription belongs to the owner, not the machine. An explicit worker start naming a model on a provider under its floor is refused with that same sentence, unless the caller is the owner's TUI. CTO starts follow the refusal rule. Running workers are not stopped when the floor is crossed; only new starts are affected.

  In the TUI Quotas tab, configured floors appear in a FLOOR column after PLAN. Press `f` to enter a floor for the selected provider (0 to 95 percent, 0 turns it off). When a window is under its provider's floor, its remaining quota is highlighted in warning color and its PACE cell shows `under floor`. The operator methods `worker_floors_get` and `worker_floors_set` are owner-gated.
- **Scores.** The bundled table contains 85 model and effort scores from [Artificial Analysis](https://artificialanalysis.ai/). An effort suffix selects that effort; for a bare model the medium entry is used when no harness default is known, falling back to a default entry. When a model has no score row at the requested effort, lookup falls back to the same model's score at the nearest known effort (preferring a lower effort, otherwise using the nearest higher effort). It also matches the provider-less base model so provider-prefixed models resolve to the underlying model's capability score (or base model when the exact build snapshot has none). Unknown scores are not candidates for automatic choice. Owner overrides win: `agent-net scores` lists scores, `agent-net scores set <model> <score>` adds an override, and `agent-net scores clear <model>` removes it. The CTO applies an override with `settings_change` area `score` on the owner's instruction. The owner-only `difficulty_get` and `difficulty_set` operator methods read and replace the difficulty minimum overrides.
- **The task** is sent as a message from the caller, after the fixed brief. For Claude Code, Pi, OpenCode and Antigravity the worker ends its work with `agent_handback(report)`. For Codex the daemon does it when a turn ends: status `done` for a finished turn, `failed` for a failed or interrupted one, and `blocked` when Codex asked for an approval (the daemon declines every approval, which should not happen now that the worker runs in a sandbox, and the report names the command, the reason and the Codex thread to open). The caller gets one message with the facts (status, run time, model, tokens, the context at the end, the cost if the harness reports it, the worktree and branch, `git diff --stat`) and the report below them. Tokens come from OpenCode, from the Claude transcript, from Codex, from Pi's session file, and from Antigravity's result events. Cost comes from OpenCode only. The context at the end is the tokens of the last assistant message or turn (input plus cache reads and cache writes) and the model's context window, when the harness reports one. For Antigravity it is the input plus cache reads of its last `agent_response` step, with no window, so the notice shows the token count alone. A worker that ends without a hand-back sends the same notice with the status `timed out`, `failed` or `stopped`. A worker that nobody messages for the idle time (60 minutes by default) ends as `timed out`; one that runs longer than 4 hours ends as `timed out` too. A worker is not idle while its harness says its session is busy, or while its token count grows: a long task does not hit the idle limit. An OpenCode worker whose model call fails (a provider, billing or auth error) ends at once as `failed`, and the note holds the error text (one line, secrets masked, cut to 300 characters). The daemon looks at the OpenCode session every few seconds: a run with outcome `failed` ends the worker. Only recognized provider errors demote the model; local and unknown failures do not. Codex reports a failed turn as a hand-back. Claude Code and Pi workers have no such check yet: a model error there shows only as the idle time-out.
- **Idle without a hand-back.** When an OpenCode, Antigravity or Claude Code worker ends a turn without `agent_handback` and stays idle for 2 minutes, the daemon first sends it an automatic continue (up to the limit; see below) instead of telling its owner. A worker that was already continued twice gets a notice with status `idle without hand-back`. The worker stays running. The notice includes its worktree diff, run time and last assistant message, marked as a last message rather than a hand-back, followed by the usual checklist, and says how many automatic continues were sent. The daemon sends one notice per idle period, including across a daemon restart; a new turn starts a new period. Codex normally hands back automatically when its turn ends. If that automatic hand-back fails, the daemon keeps the worker running and sends this idle notice instead, after the same automatic continues. Pi does not use this notice. Set `workers.idle_notice` in `node.json` to change the delay.
- **Complete or partial hand-back.** `agent_handback` takes `complete`, a boolean that defaults to true. A worker that did only part of its task calls it with `complete: false`, and the run is counted as partial. The brief tells every worker to set it honestly. A Codex worker has no tools, so its brief asks it to end its report with `complete: yes` or `complete: no`; the daemon reads that last line. A Codex turn that failed or was interrupted counts as failed, whatever the line says.
- **Automatic continues.** A worker that stops early gets another chance without the caller's help. When it hands back with `complete: false` and status `done`, or goes idle without a hand-back, the daemon sends it one message from the reserved agent-net sender: `[agent-net] continue: your task is not finished. Carry on until it is done, then hand back with complete: yes, or say exactly what blocks you.` The caller is not told yet. By default this happens at most twice per worker task. `workers.auto_continue` and `workers.auto_continue_max` set the network or project rule, and `workers.continue_prompt_text` sets the words. After the configured limit of automatic continues, or when the worker hands back `blocked` or `failed`, the caller gets the normal notice, whose note says how many automatic continues were sent. A worker that was stopped, failed, timed out, or blocked on an approval is never continued automatically. `agent_worker_start` and `worker_start` take `auto_continue` (default true); set it to false to be told at the first stop. The setting is stored with the worker and a queued start keeps it. The automatic continues are counted apart from the caller's counters (`auto_continue` on the worker record, `auto_continue_run` in `worker.stopped`), so they never count as caller rework in the difficulty calibration.
- **Follow-up messages.** A worker ends when it hands back. A message from its owner to the same session ID starts it again on the same harness session, with the same model, project and worktree, and delivers the message. For a Claude Code worker, the line agent-net adds after a request tells it to answer with its `agent_handback` tool when the work is done, not with `SendMessage`. That is `agent_handback` for OpenCode, Claude Code, Pi and Antigravity, and the end of a turn for Codex. OpenCode, Claude Code and Codex can open the saved session again (a new private server on the same folders, `claude --bg --resume <id>`, or `thread/resume`). A Pi worker cannot be started again: Pi's rpc mode cannot open an existing session, and the refusal says so; read the old session with `agent-net show`, or open its session file in Pi. Only the owner can start a worker again; another sender gets a clear refusal. A Codex worker takes messages from its owner and from the person at the machine only: the daemon turns each into a turn, one after the other, and refuses other agents.
- **Early quits.** At hand-back or an end without a hand-back, the daemon measures changed files and added plus deleted text lines against the worker's base, including committed and untracked changes. It measures before saving unfinished work. An incomplete run is flagged when it takes less than `workers.early_quit_threshold` (advice; network and repo levels; default `120s`), and has no changed files or fewer than 20 changed lines without committed file changes. Set the threshold to `0s` to disable detection. Binary files count as files with zero text lines. A missing worktree or failed measurement is unknown, not an early quit.

  A flagged notice adds a plain line such as `early quit: 1 file, 6 lines after 79s; the task was not attempted`. This is a heuristic; it does not verify the worker's test claims. The gateway stores `files_changed`, `lines_changed`, `worker_complete` (nullable for unknown facts), `run_seconds` and `early_quit` per run. The original outcome stays intact. Track records, including `agent_machine_get` with category `track_record`, include `early_quits` per harness and model. `agent_workers_find` lists active workers, not ended-run outcomes.

  Within the existing model failure window, an early quit uses the same 0.5 rotation-weight multiplier as a failed attempt, scoped to its machine, harness, model and difficulty. Repeated quits do not stack that multiplier, and a failed run is not counted twice. Other picker scoring stays the same. Detection does not restart workers. Automatic escalation would need a bounded retry policy, a different-model choice, preserved task and worktree state, duplicate-attempt protection, and a notice of the new attempt and its cost.

- **Struggle signs.** (Decision [0133](decisions/0133-struggle-detector.md).) A persistent worker can work around a broken instruction or a broken environment and still report success. At run end the daemon therefore reads the run's own tool record once and counts the signs that it struggled. No model call is made, nothing changes the worker's behaviour, and no setting controls it.

  The signals are: failed tool calls (a tool error, a non-zero exit, a permission or sandbox refusal); the same command run 3 or more times; retries of a command that had already failed; how many tool calls came before the first edit; and tool results that name a known environment problem: `packed-refs.lock`, `permission denied`, `operation not permitted`, `command not found`, `address already in use`, or `context deadline exceeded`. The patterns are code constants in `internal/protocol/struggle.go` and `internal/daemon/struggle.go`.

  Where the trace comes from: a Claude Code worker's transcript (`.jsonl`), a Codex worker's rollout file under `~/.codex/sessions` or `archived_sessions`, and an OpenCode worker's messages API. Pi and Antigravity have no tool-call record that the daemon reads, so their runs are reported as `no trace`, not guessed. The daemon reads only the last 20 MB of a long trace, streaming one line at a time, and says `(trace capped at the last 20 MB)` when it had to.

  The counts are kept on the run, in the daemon's worker record and in `worker_runs` (`struggle_failed`, `struggle_repeats`, `struggle_retries`, `struggle_exploration`, `struggle_env`, `struggle_top`, `struggle_trace`, `struggle_capped`). The run and the change origin also carry them, so the caller's accept task can show them before the run row reaches the gateway.

  The line `struggle: N failed tool calls, M repeats; top: <two worst, short>` appears in the worker notice and in the caller's accept step task when there are 3 or more failed tool calls or any command ran 3 or more times. Below that it appears nowhere. A run with no trace never shows the line. `agent_workers_find` with `detail: full` shows the full counts, including retries, exploration length and environment errors, or `no trace`.

- **Rework counters.** `continue` counts caller messages after a partial hand-back or an idle-without-hand-back notice. `fix` counts caller messages after a complete hand-back. Every caller message continues to count until the next hand-back. Automatic continues are kept separately and do not count as caller rework. The counters survive daemon restarts and appear in end notices and the model track record.

  The gateway stores one `worker_runs` row per ended run. `task_id` is the root session of a continued task; an empty value means the row's own `session_id` is the root. Resuming the same session or starting another worker with `continue` keeps the same task. Starting a continued worker adds one continue if the old run was partial, otherwise one fix. Calibration and quality add the counters across all sessions in the task and use the root's first run for caller, model, harness and difficulty. They omit a task only when all its runs were stopped. Older runs may have unknown counters, so their stored zero can miss rework. Defects on any session are added to the task.

  `midrun_count` counts caller requests after the first brief when no done, partial, failed, timed out or idle-without-hand-back notice was sent since the previous caller request. It is independent of the existing continue and fix counts. It is stored per run and summed into the quality family row as `midrun`; it has weight zero and is not shown in the panel or included in headache minutes. The daemon retains the count and notice state across restarts and queues.

  At gateway startup, one backfill recovers old continue and fix counts from retained messages, and a second links continuation briefs and counts mid-run requests. Each backfill commits its changes and its `owner_settings` marker (`rework_backfill_done` or `task_rework_backfill_done`) together. The latter matches a continued brief to an earlier brief from the same caller and resolves chains to their root. Expired messages cannot be recovered. The backfill stores counts and IDs, not a copy of the message text.
- **The satisfaction ask.** The counters see whether work came back, not how good it was, so agent-net asks the caller now and then. After a worker's final end notice, the gateway asks for every 5th finished worker of each caller, and for the first 3 finished tasks of a model and effort that has fewer than 5 ratings. The ask is one line added to the end notice the caller already gets: `rate worker <session> (<model>, <difficulty>, <run time>) with agent_worker_rate: 1 to 5 for the result, and one line on why`. The caller answers with `agent_worker_rate(worker, score, note)`. The score is 1 to 5 and the note is optional; it is cut to one short line and secrets are masked. A rating is stored with the run's model, harness, effort, difficulty and caller, never with the task text. Only the session that started the worker may rate it, and each worker takes one rating; a second rating, or one for another session's worker, is refused. A missing answer is not a low rating. Ratings appear as a count per model in the gateway's quality analysis and feed the cached analysis described below. The Quality sub-tab of Quotas shows the picker cells, not that table.
- **A defect found later.** A defect found hours later, after the hand-back or after merge, is not lost. The caller reports it with `agent_worker_rate` and `kind: "defect_found_later"`, the worker's session ID, a severity from 1 to 4 and one line on what is wrong; the owner can report one from the Messages tab, with `f` on a worker conversation, where the prompt asks for the note and then the severity. A defect report has no score, needs no new message to the worker and may be sent more than once: each report is one defect, and each adds its severity in points of rework to that worker's task in the calibration medians. It may follow a rating. The note is cut and masked like a rating note. Only the session that started the worker, or the owner, may report one. The answer returns the new row id in `defect`; the session that filed it can later move that row to a brief defect with `kind: "move_to_brief_defect"` and the same `defect` id (see **A bad brief**).

- **Defects agent-net records.** (Decision 0103.) The gateway knows each change's origin worker (`origin.session_id`), so it files these defects itself, with no `agent_worker_rate` call:
  - A **verify** step reported `fail` after merge: severity 3 (a broken feature in a release), or `fields.severity` (1 to 4) of the report. The step note is the defect note. This also covers the owner's "To try" fail from the Delivery tab.
  - A **test** step reported `fail` before merge: severity 1 (caught before release), once per revision, against the worker that made that revision. A failure the merger marks with `fields.flake=yes` records nothing. A failed test after merge records nothing.
  - A **fix of a shipped change**: `agent_worker_start` and `agent_change_submit` take an optional `fixes` (a change ID) and `fixes_severity` (1 to 4, default 2). The defect is recorded against the worker that made the fixed change, once per fixed change, and only when that change is merged or done. `agent_change_submit` counts only when the call creates the change; `agent_worker_start` records when the gateway accepts the start. The CTO's `worker_change` takes the same two fields.
  - A failed accept or review records nothing: that is normal iteration. A change made by a session that is not a worker records nothing.
  - **No duplicates.** There is one automatic defect per change, step and revision (a fix counts once per change, and not at all when a verify or test defect is already recorded for that change). An automatic defect is also skipped when a defect was filed by hand for the same worker in the last hour, so a manual `agent_worker_rate` call and the automatic record never double. Manual calls keep working and always count.
  - Each automatic defect is one row of `worker_defects`, like a manual one (the same severity points of rework, the same model, harness, effort and difficulty), plus one row of `worker_defect_auto` that holds its change, kind (`verify`, `test` or `fix`) and revision. The track record and the picker table refresh at once. The Desk log gets one line: `defect recorded: <worker session> sev <n> on <change> (verify failed)`, with `(test failed)` or `(fixed by a later change)` for the other two.
- **Defect hints.** (Decision 0116.) The defect is easy to forget once the fix is obvious, so agent-net names it in two places. A worker start whose title or task says fix, bug, defect, regression or broken, and that names no `fixes`, gets one line in its result: `If this fixes earlier work, pass fixes=<change id> so agent-net counts the defect. Recent changes in <project>: <ids, titles, workers, merged dates>`. The changes are the ones merged in that project in the last seven days, up to three. A hand-back whose diff touches files that a merged change by another worker in the same project changed in the last seven days, and whose start named no `fixes`, gets one line in the notice: `This touches files from <change> by <worker> (merged <date>). If it fixes that work, record it: agent_worker_rate defect_found_later on <worker>, or submit with fixes=<change>.` The daemon reads up to five recent changes from the gateway and checks one git file list per change against the worker's diff. Nothing blocks: a gateway or git error leaves the notice as it is. A repository that merges through GitHub pull requests without a delivery change gets no hint, because there is no change to name.
- **A bad brief.** (Decision 0116.) Rework caused by the brief itself is not the worker's fault, so `agent_worker_rate` takes `kind: "brief_defect"` with a severity from 1 to 4 and one line. Only the session that wrote the brief, its lead, or the owner may file one. It is stored in `worker_brief_defects` with the briefing session, the worker run it caused and the note, and it is counted per caller in the calibration data; the picker's quality data and the worker's score do not see it. The worker log detail shows it on the caller line as `N brief defects (charged to the caller, not the worker)`. A defect filed earlier as `defect_found_later` can be reclassified instead of deleted: `agent_worker_rate` with `kind: "move_to_brief_defect"`, the worker and the `defect` id. The id may be left out when only one unmoved defect that session filed on that worker is left; when several match, the refusal lists their ids, severities, UTC dates and the first 60 characters of each note. Only the session that filed that row may move it, and a second move changes nothing. The original `worker_defects` row stays with `moved_at`, `moved_by` and the new `brief_defect_id`; its severity stops counting against the worker in the picker, the calibration and the Workers log, and the new brief defect is charged to the caller who wrote the brief. The notice checklist carries one line about both: `Found a defect in earlier work? Set fixes=<change> on the fix, or file defect_found_later. Rework from your own brief: brief_defect.`
- **Ending:** a hand-back (any status) ends the worker after the notice, like a Claude Code subagent. Messages sent while a Codex or OpenCode worker is running wait for the current turn, and the worker ends after the last queued turn hands back. The idle time and the longest run time are safety nets: no message to or from it for 60 minutes while its session is not busy and its tokens do not grow; 4 hours since the start; its owner ended or stayed offline for 10 minutes; `agent_worker_stop` or `agent-net stop`. For Claude Code the daemon runs `claude stop <id>` first, then kills the process group if the process is still there. The stopped session stays in `claude agents`, with its conversation, until you `claude rm <id>`.
- **Worktree changes at worker end.** Before its end notice, the daemon commits uncommitted changes on the worker's branch. Other than Antigravity's title-based unfinished commits, the subject is `WIP: uncommitted changes at worker end (<reason>)`, with author and committer `agent-net <agent-net@localhost>`. The end job checks the worktree before cleanup; a lost worker's work is committed during adoption so it is safe even if notice delivery fails. The notice says `left uncommitted changes; committed as WIP <short sha>: N files`. Files over 5 MB and binaries that git detects are left uncommitted and listed in the notice; `.gitignore` rules still apply. If a commit fails, the notice gives the reason and the worktree is left in place. A clean worktree gets no commit. A later continue or resume starts from the worker branch, including the saved commit.
- **Commit at hand-back.** At a complete hand-back (status `done` and `complete: yes`, from any harness; for Codex, a finished turn whose report does not end with `complete: no`) whose worktree has changes, the daemon runs `git add -A` and commits them on the worker's branch, with the worker's title as the subject (the first line of the report when there is no title) and the worker's [commit identity](#workers) trailers. The author and signing follow the worker commit rules. `git add -A` never adds files that `.gitignore` matches, and nothing is forced. A worktree with nothing to commit gets no commit. When a git merge, rebase, cherry-pick or revert is in progress, the daemon does not commit, leaves the worktree as it is, and the notice says `A git merge was in progress; the worktree was left as is.` A partial hand-back (`complete: no`), or one with status `blocked` or `failed`, gets the WIP commit described above, except Antigravity, which keeps its title-based unfinished commit. Every notice for a worker with a worktree shows `head: <short hash>`, the worktree's HEAD; when the daemon made the commit, it also says `the daemon committed your leftover changes as <short hash>`. So the head at a complete hand-back holds the work.
  Daemon leftover commits exclude files larger than 5 MB, binary executables and build outputs (root agent-net, *.test and *.exe); excluded files remain in the worktree and are listed in the notice.

- **Delivery record at hand-back.** After that commit, a complete hand-back records the work as a [delivery change](#delivery-changes) in the gateway. The daemon sends the worker's branch, its full head commit, whether the worktree still has uncommitted changes (normally not, after the commit), the commit the worktree started from, the worker's title and the judge's difficulty label, with origin `worker` (the worker ID, the worker's session and its model). The caller of the change is the session that started the worker. The first record is revision 1. A later complete hand-back with a new head is the next revision of the same change: after a message from the caller (the same session and branch), and after a `continue`, which makes a new branch. For a continue the daemon passes the change on: the worker record keeps its change ID, and the new worker starts with it, also through the start queue, so the change moves to the new branch. When that change is already merged or closed, the work becomes a new change. The notice shows `change: <id>`, so the caller can report steps on it. A partial hand-back, a `blocked` or `failed` one, and a worker without a worktree record nothing. Recording never holds up or fails the hand-back: the daemon waits for the gateway for at most 10 seconds, and on any error it logs `worker.delivery_record` and adds one line to the notice, `delivery record: <error>`. A gateway too old to know `change.submit` is skipped without a line.
- **Continue (`continue`).** `agent_worker_start` (and `worker_start`) accept `continue`: the session ID or worker ID of an ended worker that the same caller started. The gateway sends the request to the machine that ran the old worker, and says so clearly when that machine is offline. That daemon checks the owner against its record of the worker, and starts a new worker there: same project, harness and model unless the caller gives others, a new worktree on a new branch made from the old worker's branch (if an unchanged continuation branch was removed, it is restored from its recorded starting commit when another worker branch points there or that commit contains work outside the current project HEAD; otherwise from the project's current commit), and the old worker's full first task (the record keeps up to 64 KiB of it in `daemon.db`), after this line: `You continue the work of an earlier worker that ended (<reason>). Its unfinished changes are already in your worktree as the last commit. Review them critically, then finish the task.` An optional `task` is added as `Also: ...`, and an optional `title` replaces the old worker's title (empty keeps it). `project`, `machine`, `base` and `isolation` must be empty (a mismatching `project` is refused). Only the owner can continue a worker; errors say if the worker is unknown, still running, or not yours. An explicitly requested model is matched against the machine's model list as on a normal start, with its `:effort` or `:thinking` suffix if it has one. Before it makes the new branch, the daemon commits any uncommitted changes left in the old worktree on the old branch, whatever `delivery.save_unfinished` says, so the new branch never loses them: with the old worker's title as the subject when it handed back complete, else `agent-net: unfinished work of worker <id> (<reason>)`. A worker with a worktree can be continued while its worktree and branch exist; a resumable hand-back can also be followed by a message from its caller. A worker that worked in the project folder (isolation `none`) is continued in the folder, and a Pi worker still needs its worktree.
- **Daemon restart.** The daemon reconnects to surviving workers and keeps their session IDs. It resumes a Codex turn and restores the OpenCode server connection. If it cannot restore a worker, it stops the worker, saves unfinished work on its branch, and sends its owner a `lost after daemon restart` notice.
- **The worker list.** `agent-net workers` and the TUI show only workers that run. A worker that ended leaves the list, and its record is kept (ended): a hand-back record is what starts the session again, and every ended record also tells the daemon that the harness session is a worker's, so the plugin of another OpenCode server may not register it as a normal session. An offline record without a worktree that started longer ago than `max_age` is cleaned: on the machine at daemon start and every 10 minutes, and on the gateway (`worker.cleanup`), so an old worker does not stay listed for weeks. The cleanup never touches the record of a worker the machine still runs or has adopted: the daemon sends the session IDs it runs and those with retained worktree records with `worker.cleanup` (and runs the cleanup only after it has taken its workers back), and the gateway keeps those. A worker that comes back after a daemon restart registers under the session ID it had, even if its record was lost. A worker that the machine says it does not run leaves the list at once.
- **Difficulty assessment.** Before the gateway chooses a model, the daemon asks a judge to score the brief. The judge's label is the level the picker uses. The judge has 90 seconds and cannot block a start. The gateway chooses the judge from registered worker profiles available on the daemon's machine and qualified for `normal`, using the worker picker's shared rotation, quota pace, track record, ratings and protected-provider rule. If the first judge fails or has not answered after 30 seconds, the gateway picks a backup from a different provider using the same rules. The first valid answer wins and the other judge stops. With no eligible candidate, the start uses `normal` and `model_note` includes `no judge: no registered normal-level model available on <machine>`. A brief over 4000 bytes is trimmed for the judge to its start, its Evidence section and its end. The judge reads the scores from the last JSON object in its reply that has them, so text or a code fence around the JSON is fine. A reply that does not parse is written to the daemon log (`judge.reply`, redacted, at most 16 KB). The stored judge identifies the scorer, and `judge_note` in its reasons records how the answer was reached, for example the first failure. A missing judge, a failure, a timeout or low confidence starts at `normal`. No separate `status: difficulty` notice is sent for a start that was assessed before it; the start's `model_note` begins with the result, for example `difficulty R2 S1 C1 U1 V1 K1 · D 1.20 · trivial -> opencode deepseek (bucket 1.00-1.33: deepseek 0.58 [0.29-0.86] n1, chosen (benchmark))`, or `difficulty: no assessment (the judge reply did not parse: ...); started at normal`. The result also has `owner_line`, one sentence for the caller to pass on to its user, for example `Tell your user: worker on gpt-6-luna:xhigh, difficulty simple (D 1.75; R2 S1 C2 U2 V2 K1).`; the guidance tells agents to say it. A continued worker has none. The Claude workers panel shows the same level and D on each worker row, for example `simple 1.75`. The gateway stores the scores and the label with the worker record, so the worker's detail shows the same line. The judge also runs for an explicit `model`, where the label is recorded but the model is not chosen. Queued starts are assessed once when they are requested and keep the label through the queue. A continue reuses the label of the run it continues unless the caller adds work to the brief, and a resumed run is assessed in the background. The result also has `reminder` when the caller named a model (`model` set and not `auto`, not a continue): a short text saying that naming a model should be exceptional, because the picker chooses by task difficulty, quota pace and each model's track record and spreads load, and that a named model needs a stated reason in the brief. A harness alone, the picker's own choice and a continued worker get no `reminder`. The text is the network setting `workers.named_model_reminder` (advice, live), read from the daemon's settings snapshot; the agent may ignore it.

  `agent_workers_find` with `detail: full` and `agent-net workers -json` include an `assessment` object when available. It has the six named scores (`reasoning`, `scope`, `coupling`, `uncertainty`, `verification`, `risk`), `d`, `label`, `rules_version`, `picker_label`, `bottleneck`, `confidence`, `judge_harness`, `judge_model`, `reasons`, and `error` on failure. It includes the run and session IDs, `started_at` in Unix nanoseconds and `assessed_at` in Unix seconds. The TUI worker detail shows the scores, label and picker label. The daemon queues completed assessments locally if the gateway is unreachable and retries them on reconnect. The gateway keeps one row per run, including failed assessments. A continued or resumed run gets a new row; the worker view shows the latest start's result once it arrives.

- **Worker title.** `agent_worker_start` (and CTO `worker_start`) take an optional `title`: one line of at most 60 characters, with no newlines. The daemon and the gateway keep it with the worker (`WorkerInfo.Title`, and a `worker_title` column on the gateway's session record), and a continued worker keeps the old title unless the caller gives a new one. Every place that lists a worker shows it: `agent-net workers --json` (`title`), `agent_workers_find`, `agent_worker_start`, the sessions and messages tabs of the TUI, the worker notices, and the Claude Code band. Without a title, the first sentence of the brief that is not setup is used: sentences and lines starting with `Repo:`, `Base:`, `Branch:`, `Worktree:`, `Project:`, `Setup:`, `Environment:`, `Read AGENTS.md`, `Read docs/`, `Read the docs`, `Read README` or `Read CLAUDE.md` are skipped. The sentence is cut to 60 characters at a word boundary with an ellipsis; a brief that is only setup falls back to the worker's name. The plugin needs no rule of its own because the CLI fills the field.
- **Brief checks.** (Decision [0135](decisions/0135-worker-brief-checks.md).) The gateway checks every new worker brief before launch, after the difficulty judge's pass. The checks are code only, deterministic, and ignore markdown decoration. A brief must have: a `Goal:` line (or first line) naming who asked by a quote, a message id such as `m_...`, or `owner`; at least one repository path; a `Limits:` section or the test scope and the real setup in words; `Definition of done` and `How you are graded`; an `Evidence:` section with at least three lines; and no file shared with a running worker's brief or an open change's changed files in the same project, unless it says `overlap ok: <reason>`. A refused start lists every failed item, one per line. The first two refusals of one task (caller, project, and title or goal line, within 24 hours) stop the start; the third starts anyway and records one `worker_brief_defect` against the caller with the remaining failures. `mode: "check"` runs the checks and returns the same list, or `brief ok`, and starts nothing. The six items and an example are in [brief-template.md](brief-template.md). Refusals are stored in `worker_brief_rejections`; `BriefRejectionCountsByCaller` gives the weekly per-caller count.
- **Context and tokens.** Each worker carries `context_tokens` (what the model saw on its last step: input plus cache reads and cache writes), `context_limit` (the model's context window) and `tokens` (input plus output plus reasoning for the run, without cache reads). The daemon refreshes them on its worker tick while the worker runs, and with the final notice. OpenCode reads the newest assistant message and `limit.context` from `/api/model` on the worker's private server. Claude Code reads the last assistant usage in the transcript and takes the limit from a small table by model family (the 5 series, Opus 4.7 and later, Fable 5 and Mythos 5 have 1M; other Claude models have 200k). Codex reads `last` and `modelContextWindow` from `thread/tokenUsage/updated`. Pi reads the worker's JSONL session file under `~/.pi/agent/sessions` (or `PI_CODING_AGENT_SESSION_DIR`): the last assistant message gives the context and the entries give the run total. It takes the model's context window from `pi --list-models`, once per worker. Antigravity reads the usage of each `agent_response` step in its stream: the context is that step's input plus cache reads, and the run total grows with each step. It reports no context window, so its figures carry the token count alone. The figures are in `agent-net workers --json` (also with `--recent`), on the worker's TUI detail pane as `context 84k / 1M (8%)  tokens 345k` (or `context 10k` when no window is known), and on the Claude Code band after the run time as `ctx 8%` (the percentage is yellow at 70 percent and over, red at 90 percent and over; with no window the band shows `ctx 31k` in the default colour; a narrow pane drops it before the run time). The band has one row per worker: `● title  harness model  run time  ctx 17%` while it runs, with `✓ done`, `✗ failed`, `✗ stopped` or `✗ timed out` in place of the context column when it ends, and `◌` for a queued start. The title takes the remaining width and ends with an ellipsis when cut; the harness and model are never shortened. The daemon keeps the last context figures while the worker lives if a later read fails or a running turn has no usage yet. A later complete value replaces it, including a smaller value after compaction. A harness that has not reported a figure leaves it out.
- **Defaults for a short request (W17).** "Use agent-net to implement X with a new OpenCode worker, use deepseek 4.1" is enough.
  - **Worktree:** when `isolation` is empty and the project is a git repository, the worker works in its own worktree. `isolation: none` (`--isolation none`) opts out, for read-only or non-code tasks. A folder that is not a repository gets no worktree. `workers.isolation_minimum` can require a worktree across the network or for a project; in that case a start without one is refused. Pi always needs one. The `agent_worker_start` tool accepts `base`, the name of an existing local branch, to start the worktree from that branch instead of the main working tree's HEAD. It requires worktree isolation. A Claude worker's worktree gets the MCP server choice of the main checkout, and `mcp_servers` (`approve` or `disable`) sets it for one worker: see [Claude MCP server approval](#claude-mcp-server-approval).
  - **Project rules:** the target daemon reads `.agent-net/worker.md` from the project's main working tree, including uncommitted changes, so local rules take effect without a commit. It adds up to 8 KiB of cleaned text after the fixed brief and before the task. The file is optional.
  - **Project:** an agent that leaves `project` empty gets the project of its own session. If the project is on several machines, the caller's machine wins when it has the harness, else the one machine that has it. The command line must name a project.
  - **Model:** a loose name is resolved against the model list of the machine that runs the worker, for every harness that reported a list (OpenCode, Pi, Codex, and Claude's alias and full-name table). A harness with no list passes the name through. When the caller names no harness, the gateway tries every harness with a list and prefers a subscription-backed one (Claude, Codex, `opencode-go`, or a provider with a usable quota reading); a pay-per-use harness comes last. A loose or bare name only matches subscription-backed models: a pay-per-use provider, such as OpenRouter or OpenCode Zen, needs its full provider id, for example `opencode/gpt-6.1-sol`. When only pay-per-use models match, the start is refused before launch and the error names the full id to write. The result's `model_note` says `uses pay-per-use provider (<provider>)` when one is used. A suffix after the last colon that is a reasoning or thinking level (Codex's `:low`, `:medium`, `:high`, `:xhigh`, `:max`, Pi's thinking levels) is not part of the name: only the part before it is resolved, and the suffix stays on the result. An exact id wins. Else the name is split into words (case does not matter; `4.1` matches `v4.1`) and every model id that contains all words is a candidate. Candidates are ranked by: a worker that finished successfully (hand-back status done) on that machine and harness in the last 7 days; no worker failure in the last 24 hours (a model that failed a worker with a model error goes last, and the other models of its provider go after the other providers; an exact id still wins); sessions on that machine that used the model in the last 30 days; sessions that used any model of the same provider in that time; the order of `model_providers` in `node.json`; not `-free`; not a dated snapshot (`:0813`); the shortest name. A provider that is exhausted, or above 95 percent of a quota window, is skipped for a loose name; a provider ahead of pace ranks below providers on pace. The usage counts are all sessions of that harness on that machine that the gateway saw in the last 30 days, the user's own sessions included, not only workers. If one candidate is strictly best, it is used and the result says `model: luna 6 -> gpt-6-luna` (or `model: deepseek 4.1 -> opencode-go/deepseek-v4.1-flash`). When a matching model was ranked last for a recent failure, the note says so. If the best are equal, the start fails and lists up to five. If nothing matches, the start is not refused: the model is launched as written and the start itself tests it. When the harness rejects the model, the failure or not-started notice carries the harness's own error text, then the result of one live lookup on that machine filtered to the model's base name.
  - **Checklist:** every completion notice ends with a line from agent-net: read the diff in the worktree, run the project's tests there, report what you checked, merge only if your user asked. When the notice carries a report, the last line adds: `If you learned something that would change how to pick or brief this model next time, record it with agent_learn.`
  - `model_providers` in `node.json` is a list of provider prefixes to prefer, first is best: `"model_providers": ["opencode-go", "opencode"]`. Restart the daemon after a change.
- **Limits and load (decision 0018):** a worker starts only when the machine has room. The daemon keeps 1-minute and 5-minute averages of whole-machine CPU busy percent and of available memory. Before the rules are applied, the expected cost of every worker that started in the last 5 minutes (RAM and CPU) is added to the projected load, because the averages cannot show it yet. A start is let through when: fewer than `max` workers run (default 12, a safety net); the 5-minute available memory minus the expected RAM of the worker (the median of the average RSS of the last 20 workers of that harness and model, at most 25 percent of the machine's RAM, 600 MB with fewer than 2 workers in the history), minus the RAM of the workers that started in the last 5 minutes, minus the reserve (the larger of 1.5 GB and 10 percent of RAM) is above zero; the 5-minute CPU average plus the expected CPU of those workers is below `cpu_target` (default 75) and the 1-minute average, with the same addition, is below 95; and the last start that ran on the machine was at least `start_gap` ago (default 6 seconds). A start that failed, or a worker that ended within 30 seconds, does not hold the gap: it did not add load the averages could show. Running workers are never stopped for load. On Linux, available memory is `MemAvailable`; on macOS it is total RAM minus app, wired and compressed memory.
  The daemon checks registered and seen projects for ended worker worktrees on the cleanup schedule. A registered project whose path does not exist or is not a git work tree is skipped and logged at most once per 24 hours, without unregistering the project. It removes a clean managed worktree when its branch is already in the default branch, when its delivery change is merged or done and its merged commit is in the default branch, or when its change has been stopped for more than three days. A squash merge lands as one normal commit on the default branch, so the merged commit check covers a squash as well as a merge; the branch head itself need not be an ancestor. When a branch has no delivery change, cleanup first fetches `origin/<default>` with a short timeout and checks both the local default branch and the refreshed remote ref. It also accepts a branch whose commits are all patch-equivalent to commits on the default branch (a cherry-pick or rebase), and a squash commit whose tree equals the result of merging the branch into that commit's parent. A failed fetch leaves the refs as they are, so an offline machine never reads a merge that it did not see. A branch with no delivery change can also be removed after the worker has ended for more than 14 days if no other worker, queued start, symbolic ref, branch or remote ref references its work. The gateway lookup includes historical change revisions. If the gateway cannot be reached, cleanup waits and keeps the worktree. A worker branch is deleted when its delivery change is merged or done and the merge is verified; a stopped change keeps its branch. A dirty worktree is kept and logged at most once a day. A worktree locked with the exact reason `initializing` left by an interrupted add is unlocked and removed when the lock file is older than one hour and the worktree is otherwise eligible for removal; any other lock reason or a lock younger than one hour is kept and never unlocked. Repeated worktree removal errors for the same failure are rate-limited to once per 24 hours per path. Each worktree removal logs its reason and bytes freed. Ignored files are removed with the worktree. Running workers, queued branches, branches used by starts in progress, owner worktrees, and branches checked out elsewhere are kept. Older worktrees without an end record are observed when found unused. A branch with neither an end record nor a managed worktree is left alone because its origin cannot be established. The default branch comes from the registered project or `origin/HEAD`; without either, cleanup uses a local `main` or `master`. When no default can be determined or its local branch is absent, the worktree is kept.

  Network defaults are in the settings layer. An explicit value in `node.json` wins on that machine, even when the network value is stricter; the Settings view names `node.json` and the machine as its source. Local choices are `"workers": {"max": 12, "cpu_target": 75, "mem_reserve": "2GB", "queue_timeout": "2h", "start_gap": "6s", "idle": "60m", "idle_notice": "2m", "max_age": "4h", "owner_grace": "10m", "cache_max_age": "24h", "tmp_max_age": "6h", "os_sandbox": ["shop"]}`. All are optional; `os_sandbox` is set only in `node.json` and is off when absent (see [the OS sandbox](#workers)); `mem_reserve` defaults to the larger of 1.5 GB and 10 percent of RAM, and `start_gap` defaults to 6 seconds (`"0s"` or a negative duration turns the gap off); `cache_max_age` (default 24h) and `tmp_max_age` (default 6h) control build cache and test temp folder cleanup (`"0s"` turns trimming off); the network or repo settings `cache_max_bytes` (default 15 GiB) and `cache_low_bytes` (default 10 GiB) set the one size cap and low mark for the worker cache and your `~/.cache/go-build` together. Restart the daemon after a change.
- **The queue.** With no room, `agent_worker_start` and `agent-net start` answer `status: queued` with a `queue_id` (`q_...`), the place in the queue and the reason, for example `queued #2 on research: CPU 5-min average 82% (target 75%)`. The queue is per machine, first come first served, and is kept in the daemon database across restarts. The daemon looks at it every 15 seconds. When a start begins, the caller gets a worker notice (`status: started`, the session ID and the queue wait) and the task is sent. When a message from its owner would restart an ended worker (a follow-up or fix round) and the machine is at its worker cap, the restart joins this same queue instead of failing. The send returns success with a queued note naming its position and reason, the message is stored, and the worker restarts with that message when a slot frees. The fix-round counters (continue, fix) stay as they are today. A stop of the worker (`worker_stop`) or the end of its restart window removes the queued restart. A start still queued after `queue_timeout` (2 hours), or one that fails when its turn comes, ends with a notice with `status: not started`. The gateway tracks every start it hands to a machine (table `worker_starts`, reference `start_ref`, which a queued start uses as its queue ID). A `worker.queued_start` that carries a `ref` must match the machine, owner and worker of that start, or it is refused before the task is relayed. A start the machine refused to launch, or that its owner cancelled, ends at once without a notice. A queued start dropped because its owner left, and a worker that ended before its task was sent, are reported with `worker.not_started`. A start is `open` until it is `closed` (the worker is running and its task was sent: a successful `worker.queued_start`, or a worker that was running when the machine answered the launch) or `not_started`. When the machine reports `worker.not_started` with that `ref`, the gateway sends the notice once from the reserved agent-net sender, to the owner it recorded, with the project and harness it recorded; the machine's reason is cut to 300 characters, put on one line and marked `the machine <host> reported: ...`. The state change and the notice are one transaction. The notice never carries the owner's label. A report with no `ref` (an older machine, which sends its own notice) only records an event; a report of a start the gateway does not track for that machine, or of a closed start, is refused. The machine repeats its report up to 5 times with a growing pause while the gateway does not answer, or answers with `retry: true` on the result, which only the gateway sets for a fault of its own such as a storage error; a refusal is final. The task send (`worker.queued_start`) is one call that may take up to 2 minutes, and the gateway waits that long for the owner's machine too, so a gateway that is busy or restarting does not end a running worker; a refusal or a failure ends the start, and the call stops when the worker ends. If the caller's session is offline for `owner_grace`, the queued start is dropped. `agent_worker_stop` and `agent-net stop` take the `queue_id` to cancel (the owner of the start or the person at the command line only). When the project is on several machines and each reports its room, the start goes to the one with the most room, and the result says why (`machine_note`).
- **A start answers fast.** The harness of the caller gives up on a slow tool call (Pi after 10 s), and the difficulty judge alone can take 30 s or more, so `agent_worker_start` does its checks first and does not hold the caller for the slow part.
  1. **Fast refusal.** Before the judge runs, the daemon sends the gateway a `worker.precheck` frame. The gateway runs the checks that need no judge: the project, the machine, the harness, whether the named model exists and can run, and the allowance of its provider (the quota floors and the quota readings, the same rules as at the start, for a named model). Nothing starts. When the caller named a model or a harness whose provider is exhausted or under its floor, the refusal says why and what the picker would choose instead, for example `machine nas: codex is exhausted (weekly; it resets at 2026-11-15 00:00 UTC); try claude claude-opus-5-5`. The alternative is the picker's choice for the same start without the caller's harness and model. A gateway that does not know `worker.precheck` is an older one: the checks then run inside the start, as before. The precheck never runs the picker, because the picker needs the judge's level and the judge may give a level that a model qualifies for. With an automatic model it refuses only what needs no level: a named harness whose whole account window is exhausted. Whether a model qualifies for the level is decided by the start, after the judge.
  2. **Accepted answer.** An agent's call (not the command line or the TUI, which wait) waits at most 5 s for the whole start. If the judge, the picker and the launch are done, the answer is the usual `worker`, `queued` or `starting` result. If not, the answer is `accepted` with a `start_id` and a note, with no worker yet. The judge, the picker and the launch go on in the daemon. The end reaches the caller as a notice from the new worker (`status: started` or `status: starting`, with `start: <start_id>`, the worker, the branch and the owner line), or from agent-net (`status: queued` with the queue id and place, or `status: not started` with the reason, both with `start: <start_id>`, sent through the gateway frame `worker.start_report`). The caller does not start the worker again. A start from the CTO and a continued worker keep waiting for the answer. A caller whose request ends before the 5 s keeps its start, and gets the notice.
  3. **Phase times.** The slow log line of `worker_start` (calls over 200 ms) carries the time of each part: `event=slow method=worker_start ms=34618 phases=validate=0 precheck=12 judge=33900 gateway=650(picker=20 launch=610) task=8`. `validate` is the check of the request, `precheck` the gateway check above, `judge` the difficulty judge, `gateway` the whole `worker.start` call with the gateway's own split (`picker` is the machine, model and quota choice, `launch` is the machine starting or queueing the worker) and `task` sending the task. An accepted start also writes `event=slow method=worker_start.background` with all the phases when it ends, and the gateway's `worker.started`, `worker.starting` and `worker.queued` log lines carry `picker_ms` and `launch_ms`.
- **Room in the owner views.** Each machine reports `workers N running, M queued - room: ok|cpu|mem|cap|gap|measuring` (with a middle dot) in its usage report. `agent-net workers` prints one line per machine that has workers or a queue above the table, then a table of queued starts (queue id, position, machine, project, harness, model, owner, queued time, reason); `--json` has them as `queued`. The TUI header shows queued starts and each machine row shows its running workers and memory. In the gateway, `node.usage` carries it as `workers` (`running`, `queued`, `room`, `headroom`, `reason`). New frames: `worker.queued_start` (machine to gateway) and `worker.task` (gateway to the owner's machine); `worker.kill` carries `requester_session` and `as_owner` for a queued start.
- **Owner:** `agent-net workers` and the TUI show who started each worker. A worker started from the command line or the TUI is owned by `you (on <host>)`.
- **Events:** `worker.started`, `worker.stopped` (with `reason=handback|idle|max_age|owner_gone|stopped|process_exited|lost|model_failed`) `worker.queued`, `worker.queue_cancelled` and `worker.refused`, in the gateway log, the events table and the TUI feed. `worker.cleaned` is in the gateway log.

### Test suites on one machine

A merger runs every local test suite through `agent-net test-run [--timeout D] -- <command>`. The command asks this machine's daemon for the one test slot over the local socket and keeps the connection open while the suite runs.

- **Admission.** A suite takes the same machine admission as a worker start. The daemon checks the five-minute CPU figure against `workers.cpu_target`, the memory available with `workers.memory_reserve` kept free, and the one-minute CPU limit `workers.cpu_hot`. A running suite is load for workers too: worker admission counts it as one running worker and adds its granted cores to the pending load, so a worker waits while a suite fills the machine. A second suite waits in a queue, first come first served. `test-run` prints its position and asks again every two seconds. The slot frees when the command exits, when its process is killed, or when its connection to the daemon ends; the daemon also frees a slot whose client connection dies.
- **Parallelism.** The daemon returns the free cores under `workers.cpu_target`, after the workers that started in the last five minutes, at least 1 and with no fixed cap. `test-run` sets `GOMAXPROCS` to it and appends `-p=<n>` to any `GOFLAGS` the caller set. It runs the command at nice 19 where the system allows it, streams its output, and kills the whole process group at the wall-clock timeout.
- **Timeout and exit code.** The timeout is `merger.test_timeout` (levels network and repo, default 90m); `--timeout` overrides it. The command exits with the suite's own exit code, or 124 when the timeout killed it.

### Delivery changes

The delivery pipeline (decision [0057](decisions/0057-delivery-pipeline.md)) records changes and the evidence of each step in the gateway. Node-owned ready steps have an initial queue; pipeline settings and owner controls are available, and a merge pass is refused until its required steps pass. Git itself is not blocked. A **change** is one unit of delivery: a project, the machine that holds the branch, the branch, its head commit, where it came from (`worker`, `session` or `person`), its caller, and its state (`open`, `merged`, `done` or `stopped`). A **revision** is the head at one complete hand-back or one submit. **Evidence** is one step report on one revision and one exact commit. Evidence of an older revision no longer counts: after a fix, the steps run again on the new head. By default the pre-merge test starts after accept passes on that head. A test task already ready or claimed before this rule remains available. The pipeline uses `delivery.pipeline.review.required_from`, `delivery.pipeline.review.required_risk`, `delivery.pipeline.review.independent`, `delivery.pipeline.review.requires`, `delivery.pipeline.merge.requires`, `delivery.pipeline.merge.approver` and `delivery.pipeline.deploy.approver`. Legacy delivery settings are shorthand for these rules. Settings also offers owner rows for delivery steps. When the next step waits only for the step that just became ready and has the same owner (such as `release` then `verify`, or `test` then `merge`), one task message names both and says to claim and report each step in order. Each step still needs its own report on the exact commit. A session eligible to claim a step, or its claimant, can report `waiting` with a required `note` naming the event, such as "owner opens the web console". This records no evidence and takes no claim. A new waiting report replaces the previous wait. The step sends no reminder, offline wake or stuck notice until new evidence on any step or revision of the change, or 24 hours after the waiting report. An optional `until` is an RFC 3339 time when evidence can first exist: the step stays quiet before that time, and the wait expires 24 hours after it. New evidence can end either wait earlier. Reminders then resume on the existing cadence, without a burst of missed notices. The step rows and Desk show "waiting on" with the reason and optional time; later task messages say "was waiting on" with the reason.

Four tools, for agent sessions, go through the daemon to the gateway:

- `agent_change_submit {branch, commit, target_branch, project, change, title, fixes, fixes_severity}` submits work done outside a worker. Workers cannot call it: their complete hand-back submits for them (see **Delivery record at hand-back** under [workers](#workers)). The daemon fills in the calling session, its project and model, and reads the branch from git. `branch` defaults to the session folder's current branch, and `commit` to its head; a `commit` must be the full hash of a commit on that branch. `project` must be the session's own project. When a local `target_branch` is given, the daemon records its merge base and a diff stat for the task. `change` names the change this work continues, so it follows the work to another branch. Without it, an open change on the same project and branch takes the new head, or a new change starts. A new head commit makes a new revision. `fixes` names a shipped change this new change fixes, and `fixes_severity` (1 to 4, default 2) sets the defect agent-net records on that change's worker; see **Defects agent-net records** under [workers](#workers).
- `agent_step_claim {change, step, for, after_merge, commit}` reserves a ready node-owned task before work starts. The first eligible session of the owner node to claim or report the step takes it. A repeat claim by that session is safe; another session, a worker, or a claim for a different commit is refused.
- `agent_step_report {change, step, for, after_merge, result, commit, fields, done_by, note, until}` reports one step. `step` is one of `accept`, `test`, `review`, `approve`, `merge`, `release`, `deploy` and `verify`. `result` is `pass`, `fail`, `needs_owner`, `waiting` or `close` (close without merge: the change stops). The change's caller may report `close` on the step the change is at even when another node owns that step, so a caller can stop its own change at any step; a close needs a `note` saying why. Only the caller, the owner or the step's own owner may close it. A close keeps the evidence, ends the pending step tasks so no reminder or stuck notice follows, dismisses open approval Inbox items, and tells a session that had claimed a step that the change is closed. `commit` is the full hash the step checked: the change's head, or the merged commit for a step after merge; a report for another commit or an older revision is refused. `fields` are evidence fields such as `suite`, `tests_added`, `findings_open`, `run` and `merged_commit`. Two more fields feed the defect records: `flake: yes` on a failed test marks a known flake, and `severity` (1 to 4) on a failed verify sets the severity of the defect agent-net records. `done_by` names a worker you started that did the work, such as a review worker; it defaults to you. For a delegated approval, the caller or an allowed delegate may report an owner who approved directly in the session with `done_by: "owner"`. An `ask` approval requires the owner's Inbox answer. Workers cannot call this tool: their caller reports. The gateway fills in who did the work and that session's model, and sets `independent` itself: `none` for the author's own session, `session` for another session of the same model maker, `family` for another model maker, the owner or GitHub. The model maker is Anthropic, OpenAI, Google, DeepSeek and so on, so a Sonnet review of an Opus change is `session`, not `family`. A `merge` pass marks the change merged; it is done when every step after merge has passed.
- `agent_changes_find {change, project, state, caller, limit, cursor}` reads changes, for agents and workers. `change` returns one change in full, with its revisions and the evidence of every revision. Otherwise it lists changes newest first: `state` is `active` (the default: open and merged), `open`, `merged`, `done`, `stopped` or `any`; `caller: "me"` keeps the changes whose caller you are. Each item has the change, where each step stands (`ready`, `running`, `waiting` with what is missing, `passed`, `failed`, `needs_owner`, `closed` or `skipped`), and `next`, the steps that may start now.

Review is required from difficulty `hard` up, or when the judge's risk score
is 4 or 5, or the diff touches a sensitive path. The optional `risk` field on
a change is 0 when unknown. It comes from `WorkerAssessment.Risk` (shown as
`K` in the judge's six-score note; `R` in that note is reasoning). The ordered
rule `delivery.pipeline.review.required_risk` defaults to 4. Network and repo
values may lower it to 1, 2 or 3; a repo cannot raise its network threshold.
Sensitive paths always require review: `internal/gateway/store.go`, a
`testdata/migrations.lock`, path components containing `auth`, `token` or `security`
(case insensitive), `internal/daemon/files*.go`, `install.sh`, and files under
`.github/workflows/`. The daemon checks both sides of a rename at hand-back.
If the diff cannot be read, review is required. A session submission with a
local `target_branch` also checks its diff. Without a target branch, or when it
cannot be resolved, the trigger is `diff unavailable` and review is required.
The step note says, for example,
`required: risk 4` or `required: touches migrations`. A later submit cannot
erase a risk or sensitive-path trigger already recorded on the change.
Older daemons omit these fields, so unknown risk keeps the difficulty rule.

With the default `delivery.pipeline.review.independent` value `none`, any
review pass counts, including the caller's review of its own change. The owner
can set it to `session` to require another session, or `family` to require
another model maker. Evidence from the owner or GitHub always counts as
`family`.
Worker risk is kept in the daemon's `worker_delivery_risk` table across restarts.

For an after-merge release pass without a `tag` field, the daemon looks for a
local Git tag containing the recorded merged commit and fills in the highest tag
by Git's version sort. An explicit tag is kept; if no local tag is found the
report remains unchanged.

For a merge pass with `merged_commit`, the daemon checks that the object is a
commit in the target repository and runs `git merge-base --is-ancestor` to
check that it contains the change's exact head. It also accepts a squash commit with exactly one parent when `git merge-tree --write-tree` for that parent and the change head produces the same tree as the merged commit. This requires Git 2.38 or newer. The evidence field `merge_kind` records `merge` for ancestry or `squash` for a matching tree. It uses the reporting session's
repository when it is the target project, or that project's local registration
when the reporter works elsewhere. Missing objects and failed git checks send
`merge_contains_head: false`; the gateway refuses the report without recording
merge evidence or changing the change's state. A positive proof still needs
all required steps. The optional proof belongs to the reported head and merged
commit. The gateway checks the head again when saving, so a concurrent revision
cannot reuse the proof. A daemon at version 0.4.131 or newer must send proof;
the gateway refuses a merge pass without it. Older daemons that omit the field keep the existing
behavior. Dev builds must also send proof. An older gateway ignores the new
field. This does not block git itself.

The daemon methods behind them are `change_submit`, `step_claim`, `step_report` and `changes_find`. They need an agent session; the command line is refused.

In Settings, select a repo to see its `delivery.owner` rows. Enter a graph
node name as a JSON string to assign a step to that node, or an empty string to
use the default. A concurrent assignment is refused until Settings is reloaded.
Changing a node assignment needs the owner, or an approved CTO proposal. The
`delivery_deploys` assignment is separate from a `deploys` request edge; when
there is no explicit assignment, the `deploys` node is the default deployer.

An owner-only approval creates an Inbox item for the change's exact revision
and commit. Approve or refuse records the owner's evidence; an answer for an
older revision is refused. A delegated approval also goes to sessions of the
repo lead and the lead of the repo's team, unless the lead is the caller's own
agent. The owner may approve directly, including
after a delegate has claimed the task. An answered approval and its evidence
are saved together.

The gateway rebuilds missing ready tasks for existing changes at startup. A
node task stays queued until a session claims or reports it. If an undelivered
task notice expires, the gateway can issue a fresh notice to that session.
When a ready step task stays unclaimed, the gateway re-sends a task reminder
after `delivery.step_reminder` (default 2h, 0 turns it off), marked with
`[agent-net step task reminder]` and the time it has been ready. A claimed step
gets no reminder. When one reminder names a run of steps, only the first step is
ready; the rest are named as then steps. A reminder whose first step still waits
on another step is not sent. At most one
reminder is sent per interval, and reminders follow standard batching. Unclaimed
verify steps after merge instead get reminders at 30 minutes, 2 hours, 8 hours
and 24 hours from becoming ready, then once every 24 hours. Their last notice
time survives gateway restarts. Setting the reminder interval to 0 also disables
these reminders. When
a step stays unclaimed past `delivery.step_stuck` (default 12h, 0 turns it off),
the gateway sends one stuck notice to the change's caller session:
`<title> (<id>): <step> has waited <age> for <owner>; nobody claimed it.`
This notice is sent only once per change, revision and step. For verify steps
after merge, this remains true even after message history is pruned.

### The playbook and the track record

The gateway learns which harness and model do well from the runs it already records. When a machine reports `worker.stopped`, the gateway adds one row to `worker_runs` (node, harness, model, outcome, start, end, run time, the run's `continue`, `fix` and `auto_continue` counts). The outcome is `complete`, `partial` (`complete: false`), `failed` (a blocked or failed hand-back, a process that ended by itself, or a run that errored), `timed_out` (the idle or longest-run limit) or `stopped` (a stop on request or an owner that went away), or `infra` (a provider quota or rate refusal, sign-in failure, worker lost at a daemon restart, or a start that never ran the task). Stopped and infra runs are neither successes nor failures: it is counted apart and never makes a model look bad. The gateway keeps the rows for ever by default (`--keep-worker-runs` sets a period) and counts the last 30 days per harness and model. It also sums the two caller rework counters there: `continue` counts the owner's messages after a hand-back with `complete: no`, and `fix` after `complete: yes`. Automatic continues are summed apart, in `auto_continue`; they never enter the caller's rework or the calibration.

The line it shows, in `agent_machines_find` for each machine and in every `agent_worker_start` (and `agent-net start`) result for the chosen harness and model, is:

```
terra · pi: 4 runs: 1 complete, 3 partial, 2 continue (median 9 min) · cannot resume
deepseek-v4.1-flash · opencode: 14 runs: 13 complete, 3 continue, 1 fix, 2 auto-continues (median 26 min) · resumes
gpt-6-luna · codex: 3 runs: 2 complete, 1 stopped (median 6 min) · resumes
```

`default` stands for a worker that ran without a resolved model name. The counters appear in the line only when they are not zero. The last part says whether the harness can open an ended session again: OpenCode, Claude Code and Codex can, Pi cannot. Before the first run the line says `no runs yet`. The start result carries it as `track` (`harness`, `model`, `runs`, `complete`, `partial`, `failed`, `timed_out`, `stopped`, `infra`, `continue`, `fix`, `auto_continue`, `early_quits`, `median_run_seconds`, `resumable`), and `agent_machines_find` as `tracks` on each machine. The Quotas tab shows the same numbers for the selected profile as `complete 31 · continue 12 · fix 4`, with `· N auto-continues` when there are any, and an automatic playbook note carries them in its track line.

The owner can write a **playbook note** about a model (and optionally one harness) with `agent-net playbook set`, or the operator methods `playbook_set` and `playbook_get` (owner gate only; the CTO will use the same methods later). The note appears in the `playbooks` list of the start result and in `agent_machines_find`. Automatic notes from findings are built the same way; see [findings and the automatic playbook](#findings-and-the-automatic-playbook). The gateway stores notes in `playbook`, one row per subject and harness, at most 1 KiB for an owner note.

**The operator interface.** The daemon's local socket serves these methods to the command line and the TUI, and the phone app will use the same ones: `info`, `nodes`, `list`, `projects`, `events`, `conversations`, `conversation_messages`, `message_status`, `workers` (reads) and `owner_send`, `worker_start`, `worker_stop` (actions). The owner-only methods include `conversations`, `conversation_messages`, `playbook_get`, `playbook_set`, `changes_owner_find` and `changes_owner_report` (see [the playbook](#the-playbook-and-the-track-record) and [the Delivery tab](#the-delivery-tab)). The types are in `internal/protocol/operator.go`; `worker_handback` is the worker's own call. Agent connections may use `worker_start`, `worker_stop`, `worker_handback`, `agent_learn`, `agent_worker_rate` and `workers`. `owner_send` is for the command line only; `agent_learn` and `agent_worker_rate` are for agent sessions only. `worker_defect` is the owner's own call for a defect found later, used by the Messages tab key; it takes a worker, a severity from 1 to 4 and a one-line note, and files the same `defect_found_later` report as `agent_worker_rate`.

## The TUI screen

Tab 3, Quotas, has three sub-tabs. Press `Tab` to switch between Quotas,
Workers log and Quality. The log shows the last 200 worker runs, newest first,
including running workers. Use `Up` / `Down` (or `j` / `k`) to select a run and `PgUp` /
`PgDown` to move ten rows. Use `Left` / `Right` to scroll long details. The detail box below shows the difficulty scores,
judge reasons, stored picker model note, machine, project, harness, run time and any stored
failure note. Missing assessments and picker notes show `-`. Fields from removed sessions or
cleaned notices may be unavailable. The log has no row actions.
`?` opens help.

Tab 8 opens Delivery. On a Graph project node, `d` opens Delivery filtered to
that project. `Esc` or `a` clears the project filter.

### The Delivery tab

The In flight section of the Desk (tab 7; the Delivery tab is gone) lists the open changes of the [delivery pipeline](decisions/0057-delivery-pipeline.md)
across all projects, newest first. A change is open while its state is `open`
(not merged yet) or `merged` (merged, with steps after merge left). The list
holds at most 300 changes.

Each row shows the title, project on wide screens, current step, status,
waiting time and evidence. A second line shows the merged commit, CI run link
and release tag when reported. The status names who must act or whether the
step is running, failed or done. The waiting time is since the last evidence
or update. The list has five bands, in this order: Needs you (a blocking
decision, an approval, or an owner step before merge), Moving, To try (a
released change whose after-merge verify waits for the owner to try it; its
status is `try it` and its second line says what to check), Stuck and Done. A
To try change never counts as needing you.

The daemon checks each local repo at most once every ten minutes: an open change with commits beyond its base whose head is on the remote default branch moves to Done as `merged outside agent-net`, and no step runs after it. When the head is not on that branch, the daemon asks GitHub once per repo for merged pull requests. A PR whose head branch and exact head commit match the change is an external merge too, so squash and rebase merges count; a PR merged from an older head does not. The gateway records that PR merge with its merge commit and URL, records accept from the lead's review there, and records the before-merge test only when every PR check passed. The change stays Merged so verify still runs for the caller. A missing `gh`, a signed-out `gh`, a non-GitHub origin or any `gh` error leaves the git-only check in place and is logged once; it never fails the loop. The gateway records a plain external merge only ten minutes after the last test or merge report on the change, so the merger can report its own merge first. A merge pass from the merger for the same merged commit then confirms the merge: the change moves to Merged, and its after-merge steps run until verify passes. A change with no step reports, older than seven days, closes as `abandoned: branch gone` when its branch is absent both locally and on the remote.

The evidence strip has a mark for each step: `a` accept, `r` review, `t`
test, `p` approve, `m` merge, `T` test after merge, `l` release, `d` deploy
and `v` verify. The glyphs mean `✓` passed, `✗` failed, `*` running, `-`
not needed and `·` waiting. They follow the step states, including whether a
report counts for the current revision.

`Enter` opens the selected change. The TUI reads the full change from the
gateway and lists each pipeline step with its owner and state, followed by every
report of every revision, oldest first, with
STEP, RESULT, COMMIT, BY, MODEL, TIME and the fields. Evidence for an older
commit than the change's head shows `old` after its commit. A report on a step
after merge has the field `after_merge=yes`, and one for `approve` has
`for=merge` or `for=deploy`. BY is `owner`, `github`, `caller` or the session
that reported. `Up` / `Down` scroll, `Esc` goes back. The selection stays on the
same change when the list changes. On a To try row, `y` reports that the owner
tried it and it works, and the change moves to Done; `n` asks for one line of
what is wrong and reports the verify step as failed. Both are the owner's own
reports on the merged commit, through `changes_owner_report`.

When a step waits for an offline node, the detail shows that note and the
selected change in the list shows it too. Node-owned ready steps have an
initial gateway queue and are sent to online non-worker sessions of that node.
The task includes the revision's diff stat when available and says whether
there are uncommitted changes. The first session to claim or report takes the
task. A failed step or a completed change notifies the caller. Pipeline settings and owner controls are available.

The TUI loads changes at startup, on `R`, and every 30 seconds. A failed load keeps the last list and shows the error above
it. The TUI reads through the daemon method `changes_owner_find`, which takes
the same arguments as `agent_changes_find` (without `caller: me`) and is read
only. Only the owner's command line or TUI may call it, never an agent
connection or a command line that runs inside an agent session. An old daemon
or gateway shows an error that says to upgrade it.

### Display time zone

Every time the TUI shows is in the owner's display time zone, not the zone of
the machine that runs it. That covers the Workers log, Messages and message
bodies, Inbox, Sessions and session details, Graph labels, Quotas reset times,
the `updated` header, Tools, and CTO proposals and timelines.

The owner setting is `time_zone` in the gateway's `owner_settings` table. It is
`auto` by default, or an IANA name such as `Europe/Paris`. Each daemon reports
its own machine's IANA zone in its `hello` (the target of `/etc/localtime` on
macOS, then `/etc/timezone` or `timedatectl` on Linux, then `TZ`); the gateway
keeps it on the node and `nodes` gives it as `time_zone`. `auto` takes the first
non-UTC zone among the owner's machines and prefers a Mac (Darwin), which is
the owner's own machine rather than a server. When every machine is on UTC it
shows UTC. The Settings tab shows the effective zone and where it came from,
for example `Time zone: Europe/Paris (auto, from Nicolas-MBP)` or `set by you`.

To change it, open Settings (`6`), select Time zone and press Enter. Type
`auto` or an IANA name, then Enter; Esc cancels. An empty answer means `auto`.
A name the zone database cannot load is refused before anything is saved. The
binary carries the zone database (`time/tzdata`), so names load on a machine
without zoneinfo files. The display is read-only: the setting never changes a
machine's clock or the times stored on the gateway.

### TUI keys

The bindings live in `internal/tui/keys.go`. The key line and the help overlay
read that table. `?` opens help for the screen you are on: its section comes
first, then Global keys. Press `?` again for every screen, and Esc closes help
from either view. `q` or Ctrl+C quits. Esc never quits. When a filter or
search contains text, the first Esc clears it and the second leaves the field.

| Place | Keys |
|---|---|
| Every tab | `1` to `7` switch tabs; `c` opens the CTO; `C` focuses its box; `?` opens help; `/` filters or finds where the screen has a list; Enter goes into the selected thing; arrows and `j`/`k` move; `R` refreshes or rescans; Esc goes back or clears; `q` and Ctrl+C quit. `M` and `G` do not open Graph. |
| Sessions | Left/right selects NAME, RULE or DELEGATE. Enter on NAME opens the session, Space on RULE cycles normal, ask and auto, and Enter on DELEGATE opens its form. Esc returns to NAME. `m` composes; `n` starts a session; `v` toggles active and all (including ended sessions); `z` collapses or expands the selected tree, and `Z` does the same for all trees; `h` hides or unhides the selected session; `x` stops a worker; `d` shows details; `/` filters. The first group header shows `Inbox: <N> to decide` while anything waits. |
| Inbox | Tab 7 groups waiting items into three boxes: Needs your answer (questions, approvals, held requests and waiting sessions), Proposals (CTO proposals and learned suggestions), and For your information (outcomes and notices). Each box shows its count and appears only when it has items. Each row says who, what it is about in plain words, and its age. A panel shows the selected item in full: its text, session, project, machine, age and the keys that act on it. At 100 columns or wider the panel is on the right; below that it sits under the boxes. The key line follows the selection: the keys for that item, then `↑↓ select` and `? help`. On a held request, `a` approves and `r` refuses. On a learned suggestion, `a` accepts and `r` rejects. On an outcome, `s` lets it stand and `O` overrules it. A CTO proposal uses `a` to approve and `r` to refuse; `A` approves all open proposals. On a question, `t` types a one-line answer. On an approval, a question or a notice, `x` dismisses the item: it is closed without sending an answer, and an item the gateway no longer has leaves the list with the note `already gone`. Enter opens the details or goes into the session. The tab label shows the count of waiting items. |
| Messages | Tab and Shift+Tab switch the sub-tabs: Agents, Workers and System. Up/down selects a thread; left/right selects a message; Enter opens the body; End selects the latest message; `e` expands or collapses a worker run; `p` and `t` cycle the project and team filters; `/` searches; `o` opens a loaded change or Desk ask; Esc clears the filters. The body closes with Enter or Esc. The legend under the title explains the marks. |
| Quotas | `p` protects or unprotects; `s` shows other subscriptions; `R` refreshes. In Quality, up/down selects a model, left/right selects a bucket, and `R` reads the picker table again. Enter is unused. |
| Graph | Arrows select; Shift+arrows pan; `,` and `.` select linked nodes; Enter opens the card; `/` finds a node; `t` cycles the Team filter through all nodes and each active Team, and the key line names the Team shown; `T` opens the Team list. The graph fits itself when the tab opens. `o`, Space and `0` do nothing. |
| Teams | Enter filters the Graph to the selected Team. `c` creates a Team, `r` renames it, Space adds or removes the selected Repo or Agent, and `a` archives a Team with no members, `o` sends the Team's questions for the owner to its lead or back to the owner. Esc goes back. |
| Agent card | Ctrl+U and Ctrl+D scroll. `,` and `.` select learned changes; `u` undoes the selected change. `!` does nothing; the legend is in the help overlay under Graph. `[` and `]` do not scroll. |
| Tools | Opens Skills per project. Up/down selects a project, right or Enter moves to skills, and Space or Enter previews a toggle. Esc keeps pending choices for editing; Enter reviews pending choices and confirms in the review. Left returns to projects. Esc asks before discarding pending changes. `/` filters skill names. Tab switches between the two visible views. The information box follows the selected project or skill. In All tools, Enter opens actions; `/` filters, `t` shows skill cost per project, `n` adds a tool and `R` rescans copies. `[` and `]` do nothing. In the compare box, `a` and `b` keep that copy and `v` opens the full diff. The picker ticks with Space only. |
| Settings | Up/down selects rows; Tab or `[`/`]` jumps between sections; Space toggles and Enter opens the selected row. Connected apps also accepts `p` for internet access, `u` for the tunnel URL, `a` to add and `x` to revoke. Enter on a machine renames it; an empty name resets it. Enter on Time zone asks for `auto` or an IANA name. |
| Help | Esc closes. `?` switches between this screen and all screens. Enter, Backspace, `N`, `T` and `R` do not act there. `G` goes to the last help line. Help is built from the binding table: the screen you are on first, then Global keys; `?` again shows Global keys, the current tab, the other tabs in tab order, then In overlays. |
| Confirmations | Enter or `y` says yes; Esc or `n` says no. Every question footer shows all four keys. |

`agent-net tui` has seven tabs: `1` Sessions, `2` Graph, `3` Quotas, `4` Messages, `5` Tools, `6` Settings and `7` Inbox. On each tab, `c` opens the CTO session in herdr, `C` focuses the CTO box and ctrl+] returns to Sessions. The box is read-only and shows the latest CTO output plus `c: open the <title>` (or `enter: open the <title> · ctrl+]: back` while focused), where `<title>` is the owner's CTO title; replies use Markdown, and pgup/pgdn or ctrl+u/ctrl+d scroll the conversation. Enter opens the full CTO harness UI in herdr (ctrl+] returns from the fallback), `n` starts a new conversation after confirmation, `u` undoes the last graph change, `m` edits the title, harness and model, and Esc skips a tip. Messages shows one row per conversation between two parties, with Agents, Workers and System sub-tabs switched by Tab or Shift+Tab, and pages through stored conversations as you scroll. Use up/down to select a thread, left/right to select a message, End to select the thread's latest message in the right pane, Enter for its full body, `f` on a worker conversation to report a defect found later, `p` and `t` to filter by project or team, `/` to search, and `o` to open a loaded change or Desk ask.

The Graph tab keeps its graph, camera, selection and traffic in a headless state. Actions update that state and return effects for the tab to perform. A scene describes what to show independently of the terminal, and a renderer draws it. A Braille renderer is the fallback everywhere. On a terminal that speaks the kitty graphics protocol, the same scene is drawn to an RGBA image with anti-aliased shapes and shown through Unicode placeholder cells; labels stay ordinary terminal text, so they remain crisp and selectable. The renderer and the pixel size of a cell are chosen once at start. `AGENT_NET_GRAPH` is `auto` (the default), `pixel` or `braille`. Auto sends a tiny `a=q` image query followed by a device attributes query to bound the wait, and reads the cell size from the `TIOCGWINSZ` pixel fields or a CSI 16 t reply. It stays on Braille when the terminal does not answer the image query with OK, when the cell size in pixels is unknown, or inside a multiplexer that drops images: `$TMUX`, or herdr, which sets `HERDR_ENV=1`. `pixel` overrides the multiplexer veto, and `braille` always falls back. Each image is transmitted once with `U=1` and only again when its hash changes; the old image is deleted by id first. In pixel mode the scene also carries motion: a comet with a fading tail for each message, a ring when it arrives, the phase of a busy node's slow glow, and the eased camera. Rendering never blocks the UI: `View` and `Update` build only the cheap scene, a scene hash decides whether a frame is needed, and a `tea.Cmd` goroutine rasterizes and PNG-encodes at most one frame at a time. New frames are dropped while one is in flight, and the last finished frame stays on screen. The image is capped at 1600 by 900 pixels and the terminal scales it to the cell area. A 120x34 frame with 20 nodes costs about 90 ms to rasterize, so pixel frames are requested at 8 a second; idle ticks stay at the existing 250 ms and reuse the last frame. Braille keeps its own picture and tick; both views use the same traffic filter and wildcard labels. Zooming past 1.9 times the fitted zoom shows a rounded charter card next to a node; the card box is drawn in the image and its text stays ordinary terminal cells.

`agent-net tui` needs a terminal. It talks to the local daemon through the operator interface, as an owner's CLI (`hello` with `client: cli`). Besides the reads and the actions below it uses the owner-only methods `usage`, `counts`, `usage_top`, `session_enter` and `worker_rotation_get` (see [CPU and memory](#cpu-and-memory) and [security.md](security.md#session-text-and-resource-figures)). It shows, from top to bottom:

- **Tabs.** Press `1` for Sessions, `2` for Graph, `3` for Quotas, `4` for Messages, `5` for Tools or `6` for Settings. The Quotas tab merges readings by provider and plan across machines and shows each window, pace, reset time, reading age and running workers. Subscriptions without live windows and those not usable by a harness are folded together; press `s` to show or hide them. Its Workers panel lists registered profiles and marks, for each difficulty (T, S, N, H, V), the profile the gateway's picker preview chooses at the middle D of that level, plus the worker run outcomes and the selected profile's track record as `complete N · continue N · fix N`. The preview already uses the picker's cell rule, so the panel never simulates the old level rule; `▸` marks the chosen profile and a line under the table names every level's pick by profile name, as the table shows it. The NAME column fits the longest profile name. A score shows its effort (`55/max`) only when the profile runs that effort. A protected provider shows `prot` in its own column. The letters T, S, N, H and V mean trivial, simple, normal, hard and very complex. Enable the Quota integration and agent-net installs quota-axi, or uses the copy you installed (see [subscription quotas](#subscription-quotas)).

  The Quality sub-tab shows the worker picker's table as the picker reads it (decision [0094](decisions/0094-worker-picker-learns-from-results.md)). There is one row per registered worker profile, with the same names and order as the Workers panel (benchmark score, strongest first, unscored last), and the benchmark score in a narrow `AA` column. Model keys from past runs that match no profile are hidden; a dim line under the grid counts them and `o` shows them, dimmed, after the profiles. A model that appears under two keys (with and without its provider prefix) is one row, and each bucket shows the cell with more tasks. There is one column per D bucket that has tasks. A cell with no task shows a dim dot, not a number. A cell with 1 or 2 tasks shows its shrunk score dim. A cell with 3 or more tasks shows it in plain weight, green when its whole interval is above the bucket mean and red when it is below. Cells past the profile's benchmark ceiling are underlined. The `bucket mean` and `tasks` header rows give each bucket's mean and task count. Up and down select a profile, and left and right select a bucket. The selected-cell pane shows the profile, the bucket range, the score with its interval and n, the bucket mean, the best profile with a record in that bucket, and the verdict the picker gives there (only registered profiles are candidates): allowed, chosen by benchmark (n<3), left out (below 0.9×best) or left out (above its benchmark). The verdict comes from the same protocol function the picker runs, so the screen cannot drift from the rule. At 80 columns the grid shows the buckets around the selection and says how many are outside the window. The line under the grid gives the settings in use: bucket width, k, allowed fraction, boost and uncertain n. `R` reads the table again; it is not polled on every tick, because it changes only on writes.

  The gateway builds the fit at startup and rebuilds it after a rating, worker run, roster change, score override or removal of old runs. Reads and TUI redraws only use the cache. Ratings from callers scoring at least 45 count fully; other ratings count half. Without a strong rating for a model, its prediction cannot exceed the fitted average. Rework sums every run of the task, takes the model, harness and difficulty from the root's first run, leaves out tasks whose runs all stopped, and adds the severity points of late defects reported for any session of the task.

  Quality weighting is off for good (decision [0105](decisions/0105-worker-picker-learns-from-results.md)): the cell table of the picker replaced it. `agent-net scores quality` and the socket methods `quality_get` and `quality_set` (owner only, `GET` and `PUT /v1/quality-settings`) stay so older clients work, but the answer is always `{"enabled":false}` and `quality_set` changes nothing. The fit above still feeds the analysis cards.
- **Graph** on tab 2 shows agents and their relationship rules. Press `+` or `-` to zoom, arrows to select a node, and shift+arrows to pan. Use `,` and `.` to cycle through linked neighbours, `/` to find a node, Enter to open its card and `?` to see the keys. Press `t` to cycle the Team filter through all nodes and each active Team; the key line names the Team the Graph shows. Press `T` to open the Team list, where `c` creates a Team, `r` renames it, Space adds or removes the selected Repo or Agent, and `a` archives a Team with no members. A selected Team shows only its members and the edges between them. Message and owner lines touch the selected node by default; press `m` to show every line, and press it again to return to the selection. Pink dotted lines show message traffic from the last seven days, with brighter pink when no rule is set. Gold lines show your words. Counts refresh every minute and update from the live feed. The owner and CTO markers stay at the left of the graph, outside its project layout. A wildcard rule has a label above its target. With the mouse, scroll to zoom at the pointer, drag empty space to pan, click to select, or double-click a node to center and zoom on it. Mouse reporting is enabled only while the graph has focus and is disabled on other tabs and overlays, so terminal text selection works there. `c` focuses the CTO box.

  The graph uses kitty pixel graphics on a supporting terminal and Braille characters everywhere else; see the paragraph above. In pixel mode node size and dots show its session count and busy sessions, and the ring shows maturity. Rule edges are curved with arrows; labels appear on the selected node's edges and when zoomed in. A dashed ring marks a wildcard rule. A busy node has a green glow. When motion is enabled, each message is a glowing dot with a fading tail, a ring marks its arrival, and camera moves ease over about 300 ms. Past 1.9 times the fitted zoom, a node with a charter shows a small card with its maturity, session count and purpose. Frame rasterization and encoding run off the UI loop, at most 8 times a second, and unchanged frames are reused.
- **Header.** `12 online · 3 busy · 1 needs you · workers 4 running, 1 queued`: the gateway's online count from `counts`, the sessions that report `busy`, the sessions that wait for you (in the warning colour, and left out when none wait), and the workers with the queued starts from the admission queue. Offline sessions are left out: they are mostly old ghosts. An older gateway has no counts: the header then says `N sessions, M online` and the same figures after it. A warning on the title points to tab 3 when a subscription window is above 80% or exhausted. The Inbox tab label also shows the count of waiting items. The header shows the learned count, the text filter (`filter: /cogforge`), and the time of the last refresh.
- **Desk,** tab 7, is the one place for everything that waits for you and everything in flight (decisions [0016](decisions/0016-needs-you.md) and [0090](decisions/0090-asks-and-the-desk.md)). It replaces the old Inbox and Delivery tabs; there are seven tabs. A title line says `Desk · N need you`, and `log closed (l opens it)` while the log is shut. Three sections follow, and one cursor walks all of them. **Needs you** lists the asks: questions, approvals, held requests, waiting sessions, steps that need you, outcomes, CTO proposals, learned suggestions and charter drafts, sign-in asks, and checks such as "blocked" or "stopped with work left". It is one list from the gateway (`asks_list`), oldest first inside a kind, with what needs an answer first, then what proposes a change, then what only informs. Each row shows who, a short plain-words summary, and its age. **In flight** lists the delivery changes (the old Delivery tab) with the same row look: title, step, who must act, age and the evidence strip. A change that has an open ask shows `asks you: <question>` under its row. **Log** is closed until you press `l`; it lists what the CTO and the gateway did, newest first, and a CTO line that can be undone shows `u undo` when selected (the line then reads `Undone: ...`). A panel shows the selected row in full: on the right at 100 columns or wider, under the sections (at most 8 lines) on a narrower terminal. The keys are the ones of each kind: `a` and `r` approve and refuse (held requests, proposals, approvals), `a` and `r` accept and reject (learned suggestions and drafts), `s` and `O` let stand and overrule (outcomes), `i` opens a sign-in pane, `t` answer, `x` dismiss, Enter for details or to open a session. On a question or approval with options, Enter opens the details; there up and down mark an option and Enter answers the marked one. The digits `1` to `7` always switch tabs, on the list and in the details, and never answer an ask. On a change row: Enter opens the evidence, `y` and `n` report a released change as working or failing, `v` shows or hides the done and stale changes, `R` reloads. A session that waits for you says `<session> waits for you: <question>`; it comes from the same ask list (a worker's or the owner's own wait is not an ask), and the offline, hidden and junk choices of the Sessions tab still apply to it. The tab label and the header show the count of the Needs you list, and the first Sessions group header shows `Desk: <N> to decide`. The empty state says `Nothing is waiting for you.` See [the Desk](#asks-and-the-desk-store-and-log-decision-0090).
- **Machines,** one line each: an online dot, name, agent-net version, CPU use as a percentage of the machine's core capacity with a bar, memory used and total with a bar, free space on the filesystem containing the daemon user's home directory, running worker count and memory, and the session or worker with the highest CPU use. Versions are dim, except that an online machine on an older version than the newest online machine is amber. Offline machines show their last known version in dim. The columns stay aligned across rows. When the terminal is narrow, the row keeps its version and omits optional fields in this order: top CPU session, workers, disk. Disk space is sampled once a minute; less than 10G free is red and less than 20G is amber. It does not list installed harnesses. When another machine has a harness that this machine lacks or has at an older version, it shows a dim note such as `no codex` or `old codex`. This machine comes first and is marked `(here)`. The name is the gateway's name for the machine, and `(here)` is matched by node ID, not by hostname. A figure that is missing or older than 45 seconds reads `?`.
- **Sessions,** grouped as Needs you, Working and Idle: NAME, MACHINE (short host name), PROJECT, MODEL (harness and model, shortened: no provider, no `claude-` prefix), STATUS (`idle`, `wait`, `owes`, `busy`, `shell`, `exited`, `needs you`, `limited` for a session that hit its provider usage limit, `dorm` for dormant, `off` for offline; `shell` comes from Claude Code and means no turn is running but a shell command the session started is still alive, such as a background command; `exited` means the harness process ended, so the session counts as ended and is not Working), TRUST (`-`, `ask` or `auto` for the node wildcard rule, with `+N` for sender-specific edges; always shown), RULE (the request rule: `normal`, `ask` or `auto`), DELEGATE (the [delegation policy](#delegation-policy): `off`, `native` or `workers`, or `-` if unset), CPU, RAM and TASK (the task the session announced; a worker without one shows the first line of its task; otherwise the harness title if it differs from NAME, else blank; a task that names an ended worker session id gets `(worker s_.. ended Nov 9 01:07 UTC)` after it, see [decision 0111](decisions/0111-blocked-members-reach-the-lead.md)). A session that hit its provider usage limit shows `limited` in STATUS and `until HH:MM` at the start of TASK, in your time zone, until the limit ends or the session completes a turn (the [usage limit](#provider-usage-limits) section says how it is found). An idle session with unanswered requests shows `wait` or `owes` in STATUS and the peer and age in TASK. The detail pane lists each peer, time and message ID. Requests older than 24 hours and notifications do not count. The operator counts call includes `pending_stalls`, the number of observed idle periods with an open request for over 20 minutes since gateway start. Sampling runs once a minute. The same loop sends at most one reminder per unanswered request to an online, idle recipient, after both the request and the last reported activity are over two minutes old. A missing activity time prevents a reminder. A session under a provider usage limit (see below) gets no reminder and no idle check until the limit ends. Notifications, held or closed requests, owner conversations, CTO recipients, and edges with `ask` or `never` are excluded. Delivery rechecks these conditions. The notification comes from the reserved `agent-net` sender and asks the recipient to answer the original message with `agent_message_reply`, or say it cannot. A request that needs no answer is closed with `agent_message_reply` and `ack: true`: the gateway records the acknowledgement, delivers nothing to the sender, and the request counts as answered for the reminder, the wait/owes status and the idle check. Closing a request this way never wakes a session and never resumes an ended worker. A `reminded` flag on the request survives a gateway restart. The operator `query.counts` call also returns `pending_reminders` (reminders put into the delivery queue), `pending_answered_after_reminder` (requests answered within 30 minutes of their reminder), and `pending_reminders_offline` (distinct eligible requests skipped while their recipient was offline, since gateway start). The first two counts are derived from retained messages and can decrease when message history is pruned. Offline requests create no notification; they may receive their one reminder after reconnecting. An online, idle session with open work gets at most one idle check per idle period. Open work is a request it owes an answer, a running worker it started, or a non-empty announced task. Workers, the CTO and offline sessions are never checked. The check is a notification from the reserved `agent-net` sender and asks for the next `agent_announce`: `done`, `more` with one line, or `blocked on X`. `done` stops checks until new activity. `more` follows the node's autonomy setting: `off` leaves an Inbox row, `<name> stopped with work left: <line>`; `wake` sends one continue notification, `[agent-net] continue: you said you have work left (<line>). Carry on, then answer the next check.`, and leaves no row. An offline session is not woken. `blocked on X` always leaves a row naming X, whatever the autonomy. Wake-ups have their own daily cap of 6 and stop after two in a row with no new message or change in open work; the second leaves one row, `<name> was woken twice with no progress`. Two `more` answers with no progress stop the checks and leave one row. A session gets at most 6 checks a day. The operator `query.counts` call also returns `checks_sent`, `check_answers_done`, `check_answers_more`, `check_answers_blocked` and `wake_ups_today`. The check count comes from retained messages and can decrease when history is pruned; the answer counts and today's wake count are small stored counters. Whole caller trees take their most urgent live member's group and position: Needs you (a wait or open owner question), Working (any busy member), then Idle. An ended or exited member never lifts a tree. A session that did something in the last hour stays in Working too; after an hour of quiet it moves to Idle. Empty groups are hidden. Idle shows pinned sessions; other idle trees fold into `N more idle · v to show`. Connected bots have their own Bots group at the bottom: a bot has no turns, so it is never Working. Press `v` to show all idle and ended sessions, and `H` to show hidden sessions. `z` toggles the selected tree, including when a child is selected; `Z` toggles all visible trees. Collapse state lasts for the TUI run, and a collapsed task ends with `+N (M busy)`. Within each group, activity bands order root trees by their most urgent live member: busy or needs you, active within 10 minutes, within one hour, within 24 hours, then older or offline. Activity is the last time the session did something (a turn started or ended, a message was sent or received, it announced a task, a wait started, or its harness wrote output), not the last time its daemon reported it. A daemon restart or a re-report does not make an idle session look active. Within a band the most recently active session comes first; ties never use the oldest start. A session entering a band moves to its top; idle margins reduce moves near band boundaries. The order freezes for three seconds after a key press, and selection follows the session. Workers stay under their caller. A merger belongs to its explicit online repo lead, then its starter if known, otherwise it stays at the top level. Trees stay together even when a text filter matches only a child. A session that is idle with no report for more than an hour is dim. Offline sessions are dimmed too. On a narrow terminal TASK, MODEL and PROJECT give way first. TRUST, RULE and DELEGATE stay visible. A tree with more than 20 processes, 200% CPU or 4G of memory shows its numbers in red. A process tree that several sessions share shows `shared` in their rows and has no row of its own. A worker's private OpenCode server is its own tree, so its CPU and memory are on the worker's row. CTO and merger roles appear as dim tags after NAME, and a session whose node has a standing loop gets a dim `↻` after its name. The CTO is a normal row in its urgency group; NAME shows the owner's CTO title, and Enter enters it through `session_enter`, the same plan as Enter in the CTO box.
- **Selected session.** The first line has the name, harness, status and the session's CPU, RAM and process count. Below, on the left: session and worker IDs and PID, folder, branch, model and mode (these last two moved here from the table), function (the project description from `AGENTS.md`; "none: add a `# Project: <one line>` line to AGENTS.md" when there is none), remote, worktree (when the session runs in a linked worktree), the project source (`seen in a session`, `registered`, or `seen in a session and registered`), task, expertise, a `trust` line when peer trust is on, `policy` and `main` lines when it has a delegation policy (with a note for each part its harness cannot do), and its recent messages. For a worker it starts with who started it and then `context 84k / 1M (8%)  tokens 345k`, with only the parts the harness has reported. On the right: the heaviest 5 processes by CPU and by memory (PID, CPU, RAM, command line with secrets masked), fetched with `usage_top` when the session is selected and again every 5 seconds. Press `d` to replace the session list with these details; press it again to return. The CTO box stays visible.
- **Workers** show in the sessions table right under the session that started them, indented with `├─` (or `└─` for the last one), in order of start time, like a sub-agent. A worker's NAME is `<project> worker <n>` (decision [0024](decisions/0024-session-names.md)), and its TASK is its title when it has one, else the first line of its FIRST task, which the daemon records and the gateway passes in the agent list; a later request from its owner does not replace it. When that task is missing, TASK is the harness title, or blank. The full worker ID is on the detail pane's `id` line. A worker on another machine than its caller still sits under the caller and has `@host` after its name. A worker whose caller is not on screen (you started it, or the caller is offline or hidden by the filter) stays in its own machine group with `↳` before the name. Selection keys follow the order on screen. The detail says the worker runs in the background. The title counts them.
- **Messages** on tab 4 shows threads, one row per unordered pair of parties, not a flat list of messages. A pair is one row even when its messages were stored under several conversation ids. Three sub-tabs sit under the title and switch with Tab and Shift+Tab, the same way as the Quotas sub-tabs. **Agents** holds agent-to-agent threads (including the owner's own threads), **Workers** holds agent-to-worker threads, and **System** holds what agent-net sends by itself. The list panel is narrower than the conversation panel and is titled `Threads`. Each thread row shows the two sides, the latest line, the message count and the state: `waiting` for an unanswered request, `answered` for a reply or an acknowledgement, `gw N` when the recipient holds N messages at the gateway, `failed` or `timed out`. Waiting requests and owner instructions sort first. The conversation panel shows the whole thread in order, across every conversation the pair used, and wraps every body line over as many lines as it needs; no body line is cut with an ellipsis. A heading above the messages names both sides and their types: `session`, `worker`, `app` (a connected bot such as `CTO (Trade)`) or `system`.

  The flags are marks on the row and words in the heading. `you→` marks a message sent in the owner's name or citing the owner's approval. `(app)`, `(worker)` and `(system)` name a sender that is not a plain session. `!` marks a thread where a session reports a blocked action. `⚡` marks a burst: the thread's busiest hour holds at least 20 messages and is more than three times its own median hourly count. The heading spells the burst out as `burst N/h` and lists `blocked` and `owner` too.

  Worker runs collapse to one row: `caller → worker N: task`, with the harness, run time, PR and status after it. Press `e` to expand the selected run in the list, or select it to read the whole run in the conversation panel. Worker start notices (`status: started` or `queued`, and a queued start that never became a worker) do not make a row and do not count in its message total. A row moves only on real content: the task, follow-ups, the hand-back (done, failed, timed out, or an idle end without a hand-back) and replies. A session whose record is gone is shown as `ended worker` or `ended session`, never `unknown session`.

  The System sub-tab groups agent-net's own messages by kind and recipient, so it reads `step reminders → agent-net 2  8 today`, not one row per message. The kinds are idle check, autonomy continue, reply reminder, loop tick, step task, step reminder, stuck step, chief brief wake, chief ask batch, undelivered or expiry, waiting notice, wake failed, worker not started, step result, step closed, member blocked and native MCP removed. One table-driven classifier in the TUI reads the sender and the body's first marker or words; an unknown agent-net notice is grouped as `other notices`. A native MCP removal comes from the owner session, so it is matched by its words. Non-message feed events (a machine connecting or a session disconnecting) stay in this sub-tab too.

  `p` cycles the project filter and `t` the team filter over the values the held threads name; the panel header shows both and the current `/` search. Esc clears them. A thread matches a project or team when either party does. The filters and the grouping run on the rows the TUI already holds: no per-row gateway read and no new poll. A thread's PR or change (`ch_...` or a GitHub pull URL) and its Desk ask (`i_...`) appear on a `links:` line above the conversation. `o` opens the first loaded change or ask in the Desk; a PR URL is named in the notice because the TUI has no browser action for it.

  The gateway groups a peer thread by the unordered pair of sessions, so a pair that used 36 conversation ids is one row, and the busiest and median hourly counts are over that pair. It sends the sender harnesses, the owner-named and blocked flags, the system marker and line, and those counts on the existing conversations page, so the screen does not make a second read for them. A thread the first page does not hold is still paged in through the existing cursor when its sub-tab has fewer than three rows.

- **A result line** under the title says what the last action did: plain when it worked, `!` and red when it did not. The next key clears it.
- **Questions** use the same keys everywhere: Enter or `y` says yes, Esc or `n` says no, and every question ends with `enter/y: yes · esc/n: no`.
- **Help** (`?`). Shows the keys of the screen you are on, then Global keys. Press `?` again for every screen; Esc closes either view. The key line at the bottom shows at most seven hints: the few keys used most on that screen, then `? help` and `q quit`. It drops whole hints when the terminal is narrow. A key that is not on the line still works and is listed here. A selected row may replace a less useful hint with the action that matters then, such as `x stop` on a worker or `a approve` on a held request.

The screen refreshes once a second. CPU, memory and counts are asked every 5 seconds, because daemons report every 10 seconds. If the daemon is not running, or cannot reach the gateway, a banner says so, the last data stays on screen, and it retries every second. A daemon or gateway from before CPU and memory shows `?` and is not asked again.

| Key | What it does |
|---|---|
| up, down, `k`, `j`, `g`, home, end, page up, page down | Move the selection (or the message cursor, when the messages have the focus). |
| enter | Open the selected session in herdr. When herdr is unavailable, the TUI uses the fallback enter plan. In Messages, Enter opens the body of the selected message. See [Enter a session](#enter-a-session). |
| `l` | In Messages, cycle the sub-tabs: Agents, Workers, System. `Tab` and `Shift+Tab` do the same. |
| `p`, `t` | In Messages, cycle the project or team filter over the values the held threads name; Esc clears both. |
| `e` | In Messages, expand or collapse the selected worker run in the list. |
| `o` | In Messages, open the thread's first loaded change or Desk ask; a PR URL is named in the notice. |
| `/` | Filter the sessions: type words, all must appear in some field of a session (name, harness, machine, project, branch, model, mode, status, function, task, folder, ID). Enter keeps the filter, Esc clears it. |
| `h`, `v` | In Sessions, `h` hides or unhides the selected session; `v` cycles active only, all, all with offline, all with offline and hidden. The Graph card keeps `h` to hide or unhide the selected project node and `H` to include hidden nodes. |
| `v` | Cycle the Sessions view: active only (hide sessions idle and unreported for more than an hour), all, all with offline, all with offline and hidden. The Sessions column header names the current view. On the Graph agent card, `a` toggles the node's autonomy between `off` and `wake`, and `L` cycles the standing loop: off, 1h, 4h, 12h, 24h. |
| `1` to `7` | Open Sessions, Graph, Quotas, Messages, Tools, Settings or Inbox. `M` and `G` do not open Graph. |
| `c`, `C` | `c` opens the CTO session; `C` focuses the CTO box. Ctrl+] returns to Sessions and Esc skips a tip. |
| `d` | Toggle selected session details in place of the session list. |
| `?` | Show the keys of this screen; press again for all screens. |
| esc | Leave the help or a message body, or clear the filter. When a filter holds text, the first Esc clears it and the second leaves the field. Esc never quits. |
| `R` | Refresh now. In Sessions and Messages, `r` refuses or rejects instead. |
| `m` | Send a message to the selected session as yourself. Type, Enter sends, Esc cancels. The reply shows in the messages. |
| `n` | Start a new session in the selected session's project, with a form: Project, Harness, Model, Folder, Branch and First message. The form is filled from the selected row; the focus starts on First message. On a worktree form, four Up presses reach Harness, because Branch sits between it and First message. On a Graph node or its card, `n` opens the same form filled from the selected project. The key line reads `up/down field · left/right change · enter start · esc cancel`. Left and right act only on the field that has the focus. The project may be on another machine, shown as `name@machine`: the start is relayed through the gateway to the machine that holds it, and the form names that machine. Harness is a switch: left and right cycle the harnesses the target machine reports, in the fixed order claude, codex, opencode and pi. The form skips a harness the machine does not report, and one this version cannot start. Harness takes no typed text. Model follows the harness: it is a switch whose first choice is `default`, then the models the target machine reports for that harness, in the order the machine reports them. A harness change resets Model to `default`. Left and right on a text field (Project, Branch or First message) move the cursor instead. Folder is a switch: left and right change it between `project folder` and `new worktree`. A new worktree is made next to the project, in `<project>.worktrees/<branch>`, on a new branch. The Branch line appears only for a worktree; its default is `agent-net/<project>-<n>` with the lowest free number, and the note says the branch it starts from, such as `from main (HEAD)`. When a live session is busy in the project folder, the form opens on `new worktree` and says so in one line (`<project> is busy in the project folder`); left or right on the focused Folder sets it back. The folder choice and the branch travel with a start on another machine. Project also takes a path, absolute or starting with `~`, and `path@machine` names another machine; Tab completes a known project name, and a dim line under the focused field says so. An existing folder that agent-net does not know is registered when the start runs. A missing folder asks `Create <path> on <machine> and run git init?` with `enter: yes · esc: no`; only yes creates it and runs git init. A folder that is not a git repository still starts, with no worktree choice and no branch. A relative path, a path with a control character, a path outside your home folder and a link that leaves home are refused. A bare name that is no project goes under the folder that holds most registered projects on that machine, such as `~/code`. For Claude, an existing folder that Claude does not trust shows a note, and the start asks `Trust <path> for Claude Code?` with `enter: yes · esc: no`; only yes trusts it, while a folder agent-net created is trusted at once. This version starts Claude Code, OpenCode, Codex and Pi sessions; an older row that names another harness is refused when you submit. The session opens in herdr, and a first message is sent as your own words. When herdr is unavailable, an empty First message for Claude, OpenCode or Codex starts and enters the session in the TUI terminal (ctrl+] returns); Claude and OpenCode starts with text remain in the background, while Codex starts in the TUI terminal with the text as its prompt. Pi requires herdr. A failure shows the reason the harness gave. Esc cancels. An owner worktree belongs to you: agent-net keeps it until you remove it, and worker cleanup never removes it. |
| `x` | In Sessions, stop a worker as before, or stop a Claude background session, abort an OpenCode service turn, or close the herdr pane holding a Pi session. Owner sessions ask `Stop <name>? Conversation kept: Enter resumes it.` with `enter: yes · esc: no`. OpenCode stays in the service; an idle service session costs nothing. Interactive terminal sessions say `<name> runs in your terminal on <machine>: end it there`. A session on another machine is stopped by its own machine through the gateway. |
| left/right, Space, Enter | In Sessions, move between NAME, RULE and DELEGATE. Space cycles RULE and Enter on DELEGATE opens the policy form. Esc returns to NAME. In Settings, up/down moves between rows, Tab or `[`/`]` jumps sections, Space toggles and Enter opens. |
| `q`, ctrl+c | Quit. |

The TUI asks for each readable message body once and keeps it by message ID. Owners can read remote messages through the daemon's owner-gated relay. Other machines can read only messages involving their own sessions. If all participant machines are offline, the screen names a holding machine and says it is offline. Expired messages remain unavailable. Text from the network is stripped of control characters before it is drawn, so an agent name or a message cannot move the cursor or change the screen.

**Event feed.** The screen reads the gateway's events through the local daemon method `events` (`{"after_id": N, "limit": N}`) and the gateway frame `query.events`. Any enrolled machine can ask. It returns the events of kinds `msg.*`, `session.*`, `node.*` and `worker.*`, oldest first, as metadata: the event kind and time, the machine, the session, the message ID, and for messages the two session IDs, the type, the ID of the message it answers, and whether the CTO sent it. Never a message body. Machine addresses are left out, details are cut to 200 characters, and MCP, enrollment and retention events are not included. With `after_id` 0 you get the newest `limit` events (default 100, at most 500). An older daemon or gateway answers that the method is unknown, and the screen then shows the sessions without the feed.

**Stored conversations.** The Messages tab reads the full stored history through the owner-only daemon method `conversations` (`{"cursor": "...", "limit": N}`) and the gateway frame `query.conversations`. It lists every stored thread on the gateway, from every machine, newest activity first, as `{"conversations": [...], "total": N, "next": "..."}`. A peer thread has the ID `pair:<a>\x00<b>` (the two session ids in sorted order) and counts every message between the pair; agent-net notices stay one row per conversation so the System sub-tab can group them by kind. Each row has `id`, `from`, `to`, `from_name`, `to_name`, `message_count`, the newest message's `last_line` (one line, no control characters, at most 200 characters), `type`, `state`, `last_message_id` and `at`. It also carries the display facts the Messages tab needs without a second read: `from_harness` and `to_harness` (so a bot shows as an app), `owner_named` (any message from the owner session, with the owner's approval, or citing the owner's words), `blocked` (any message that reports a blocked action), `system` and `system_line` (the newest agent-net message in the conversation, for the System classifier), and `peak_hour` and `median_hour` (the busiest hour's message count and the median hourly count). The screen marks a burst when `peak_hour` is at least 20 and more than three times `median_hour`. The gateway stores the thread key in a generated `pair_gid` column with a covering index, so the page reads the index in thread order and computes the count, flags and spread only for the page's threads. The spread costs one read for the whole page, not one per row. A row that involves a worker session groups every conversation of that worker and has the ID `worker:<session>`, plus `worker_session`, `worker_owner` and `task` (the first line of the task message, cleaned and cut). The gateway finds a worker from its live session record or, after cleanup deletes that record, from its own notice and task messages. A row whose newest activity is only a worker start notice (`status: started`, `queued` or `not started`) is omitted, and those notices do not count in `message_count` or move `at`. `total` counts the grouped rows, so a worker counts once. `limit` defaults to 50 and is at most 100. `next` is empty on the last page. Opening a row asks the owner-only `conversation_messages` method (`{"conversation_id": "...", "limit": N}`, answered with `{"messages": [...]}`) for that thread's stored messages, including a conversation between two sessions on other machines. A `worker:<session>` ID returns every stored message that involves that worker, lifecycle notices included. A `pair:<a>\x00<b>` ID returns every message between the two sessions, across all their conversations. Agents keep the node-scoped `messages` read; they cannot call either owner method. The live feed still supplies the newest lines and the chat of a conversation that is already open.

### Enter a session

`Enter` asks the daemon (`session_enter`) to open the selected session. If herdr is available, the daemon focuses an existing herdr pane for that session. Otherwise it creates a pane beside the TUI when the TUI is in herdr, or a titled tab on the herdr server. The TUI keeps running and its result line says `opened <name> in herdr`. If the server is not running, the daemon tries to start its headless server. When no herdr client is attached, open herdr in a terminal to see its panes. See the [herdr installation instructions](https://herdr.dev/tui/docs/install/). Your input in a session counts as yours, so approvals you give there are yours.

When herdr is missing or cannot start, agent-net uses the existing enter plan. The TUI hands its terminal to that command; press `ctrl+]` to return and see the installation hint. A session that has neither an attach command nor an accessible herdr pane shows a note to open its existing window.

While the TUI has handed its terminal to a session, agent-net keeps the bottom row of the terminal for itself and gives the session every row above it. The row reads `agent-net · ctrl+] back to agent-net · <session name>` in dim text, so the way back stays visible over the session's full-screen UI. The row is drawn again after a resize and after the session clears the screen, and the window title still carries the same hint. `ctrl+]` returns as before: agent-net clears the row and gives the session its full size back. Set `AGENT_NET_NO_BAR=1` to turn the row off on a terminal that mishandles it; the session then gets the whole terminal. Sessions opened in a herdr pane are unchanged: the bar belongs to the TUI terminal fallback only.

| Session | Command used for a new view |
|---|---|
| Claude Code background session, including Claude workers | `claude attach <id>` |
| Claude Code interactive session | Focus its existing herdr pane. An interactive session outside herdr cannot be attached twice. |
| Pi | An owner session starts directly in herdr. Pi workers have no screen. |
| OpenCode | `opencode --session <id>` in the session folder |
| OpenCode worker | `opencode --server <private url> --session <id>` on its own machine; remote access uses herdr when both machines have it |
| Codex | `codex resume <thread>` in the thread folder, using its configured Codex home |
| Codex worker | `codex resume <thread> --remote ws://<private address> --remote-auth-token-env AGENT_NET_CODEX_TOKEN` on its own machine; remote access uses herdr when both machines have it |

**herdr.** The daemon finds the binary from the session's `HERDR_BIN_PATH`, the server command line, the configured harness path, `PATH`, or `~/.local/bin/herdr`. It uses `herdr pane list` and `herdr pane process-info` to find the pane holding a session process. A moved pane is found the same way. `herdr agent focus` focuses a recognized agent, and `herdr tab focus` focuses the tab of a plain terminal. Herdr's existing clients can remain on their previous tab after a focus command; select the named tab if needed.

A background Claude session has no `agent_session` in `herdr pane list` when its pane runs `claude attach <id>`. Before opening a new view, the daemon reads the foreground processes (`herdr pane process-info`) and command lines of the panes that are claude agents with no session first, and focuses the first pane that already runs `claude attach <id>` for that session. It reads the other panes only when none of those matches. This also works after a daemon restart.

For a new view, agent-net uses `herdr pane split --pane <tui-pane> --direction right --focus` and `herdr pane rename` inside herdr, or `herdr tab create --label <name> --focus` outside it. It uses `herdr pane run` to launch the session. A private one-use environment file keeps worker credentials out of command arguments and pane text. The pane removes the file after use; the daemon also removes it after one minute while it is running. Claude's herdr client has mouse tracking enabled; the terminal fallback gets the same choice from the user's Claude settings (decision 0128). A second Enter focuses the existing pane instead of creating another one, while that pane remains present. The daemon also remembers panes it opened during its current run.

On macOS the daemon reads process environments and command lines with `sysctl kern.procargs2` when permitted. If Herdr's pane variables are not readable, it checks the process ancestry and candidate server sockets.

**A session on another machine.** The question goes through the gateway to that machine. When both machines have herdr, the remote daemon focuses or opens the session there and this machine opens `herdr --remote <target>` in a local pane. Otherwise the daemon uses `ssh -t -o ConnectTimeout=5 -- <target> <command>`, in a local herdr pane if available or in the TUI terminal as a fallback. To change the target, or to turn ssh off, set in `node.json` on the machine where you use the TUI, and restart the daemon:

```json
"enter_ssh": false,
"enter_ssh_hosts": {"build-box": "me@build-box.example"}
```

`enter_ssh` is on by default. `"enter_ssh": false` turns it off, and then Enter shows the command to run there instead. `enter_ssh_hosts` is optional: it maps the machine's name (as `agent-net nodes` shows it, so its [display name](#machine-names) when it has one) to the ssh target, which can be an alias from `~/.ssh/config`. Without an entry the name itself is used. The target must be a plain host name or `user@host`. ssh runs with your own keys and ssh config, so it may ask for a password, a key passphrase or a Tailscale SSH check. If ssh exits with status 255 (it could not connect or log in), the TUI shows the command to run on the machine, with a hint to set `enter_ssh_hosts`. The same target is passed to `herdr --remote` when the session runs in herdr on the other machine and this machine has herdr. The secrets of a worker's private server never leave the machine: workers of OpenCode and Codex on another machine can also be entered through herdr when both machines have it; their private server credentials stay on their own machine.

## Peer trust and hints

Decision: [0013](decisions/0013-peer-trust-and-hints.md). Security: [security.md](security.md#peer-trust).

**Owner instruction markers.** For a Claude Code owner prompt, the gateway considers the first subsequent send within 10 minutes. If the prompt names the recipient session or project, ignoring case and matching whole names, the message carries up to 200 characters of the prompt. A non-matching first send consumes the prompt. The Messages tab shows `you→` and the excerpt. Claude Code, Codex, Pi and OpenCode append a note after the message body with the excerpt. It says the words are the owner's own, typed in the sender's session at about the send time, that the receiver acts on them as if the owner had asked it and does not ask the owner again for what they name, and gives the receiving node's merge rule. The daemon forwards only prompts the owner typed; turns the harness injects are never kept.

**Request rules.** The gateway chooses an edge rule from the sender's project node to the recipient's project node. A specific sender edge wins over the wildcard edge from `*`. With no edge the rule is `normal`: deliver the request under the usual peer guidance. `auto` delivers it with the peer trust note, using the receiving node's effective merge authority. The note never widens that setting. `ask` holds it for your decision. `never` refuses it with `refused by your user's rule`. The TUI `t` key cycles the wildcard rule through `normal`, `ask` and `auto`; the TRUST column also counts sender-specific edges. `session_trust` remains an owner-only compatibility method that sets the wildcard rule to `auto` or `normal`. At upgrade, trusted sessions with a project get a wildcard `auto` edge, without overwriting an existing edge.

A held request appears in the Inbox with its sender, recipient and first line. Select it and press `a` to approve once or `r` to decline once. `A` and `R` do not answer a held request. Enter goes into the receiving session. The owner-only operator methods `held_list` and `held_answer` expose the same actions. Approval delivers the request with an agent-net approval note. A decline sends the sender `your user declined this request`. A held request expires at its normal message expiry and the sender receives a notice.

**Expertise.** `agent_announce` takes `expertise`, one short line. `agent_sessions_find` and `agent_session_get` show it, `list --json` has `profile.expertise`, `agent-net list --wide` has an EXPERTISE column, and the `text` filter searches it.

**Hints.** The local method `hint` takes `{"text": "...", "failed": true}` (and, from the command line, `harness` and `native_id` of the session) and answers `{"suggestions": [{"session_id", "name", "hostname", "why"}], "text": "<one line>"}`, or an empty object. The Claude Code hook (`agent-net hint`), the Pi extension and the OpenCode plugin call it after a failed tool call. How each shows the line: Claude Code adds it as `additionalContext` of the hook output, Pi appends it to the tool result, OpenCode adds it to the system prompt of the next request of the session. See the decision for the matching rules and the limits (at most 2 suggestions; the same suggestion once per 30 minutes per session; one hint per 45 seconds per session).

**A lead's step.** Before `gh pr merge`, `gh pr close` or `gh workflow run`, the Claude Code `PreToolUse` hook reads the session's team lead with the local method `team_lead` (`{"harness", "native_id"}` from the command line, or the bound session for a plugin). When the team has `asks_to=lead` and the session is not the lead, it adds context naming the lead and telling the member to hand over the change and review for the lead's own decision. When the team has `lead_merges=on` ([decision 0119](decisions/0119-lead-merge-grant.md)) and the step is `gh pr merge`, it states the owner's grant as a fact: the owner granted the lead merge rights for the team, and a merge the session cannot run goes to the lead with `agent_message_send`. A grant alone adds nothing for a close or a workflow run. It does not change the tool call or its permissions. The lead comes from the same lookup that routes `agent_owner_ask` to a team lead. The hook keeps its 280 ms budget, and on any error or timeout it prints nothing new.

Claude Code 2.1.319 does not send permission denials through `PostToolUseFailure`. Its `PermissionDenied` hook does not deliver context text to the model. Ordinary tool failures keep the ordinary hint.

The Claude Code hook output, when there is a suggestion (one line of JSON on stdout, exit 0):

```
{"hookSpecificOutput":{"hookEventName":"PostToolUseFailure","additionalContext":"agent-net: another session may own or see this. ..."}}
```

## Asks and the Desk (store and log, decision 0090)

Everything that waits on a person is an ask, kept in the gateway's `inbox_items` table. An ask has a source key (`source_ref`), a `from_kind` (session, cto, gateway, disk, apps), what it is `about` (a change, project or session), who must act `next_who` (owner, a session id, or chief), a `why_you` line and a longer `detail`. Its state is read from `status`: open (or "with someone" when the next actor is not the owner), answered or closed, with the close reason in `answer`.

| Operator method | Gateway frame | Answer |
|---|---|---|
| `asks_list` | `asks.list` | `{"asks": [...]}`: only the open asks, oldest first, in one query. Each has `id`, `kind`, `state`, `from_kind`, `from`, `from_session`, `about_kind`, `about_id`, `about_title`, `question`, `options`, `recommendation`, `detail`, `next`, `why_you`, `ref`, `message_id`, `created_at` and, for a delivery approval, `delivery`. |
| `desk_settle` | `desk.settle` | `{"ok": true}`: marks a log line as undone (its undo options go). |
| `desk_log` | `desk.log` | `{"entries": [...]}`: the newest log lines first (`limit`, default 100, at most 500). Each has `at`, `source`, `about_kind`, `about_id`, `text`, `detail` and `options` (the way to undo a CTO change). |

A daemon adds one line of its own with `desk.note` (`{"text", "detail"}`, answered `{"ok": true}`). It is what needs no answer: the line is written with source `daemon` and about the calling machine's node. agent-net uses it for a Claude session whose wheel is off ([decision 0128](decisions/0128-cto-keeps-mouse-tracking-on.md)).

Kinds: `question`, `approval`, `feature_idea`, `rejection`, `escalation`, `held`, `learned`, `draft`, `proposal`, `step`, `waiting`, `session_stall`, `check`, `brief` and `sign_in`. A source outside the gateway opens an ask with `ask.open`, closes it with `ask.close`, or replaces all of its open asks of one kind with `ask.sync`; each call is safe to repeat and only touches asks of the calling machine.

An ask closes by itself with a kept reason: `change_done` (the change it is about is done or stopped), `session_ended` (the asking session was archived or its worker run ended; held requests and outcomes are work between two sessions and stay), `resolved` (its source was decided), `expired` (a held request ran out), `activity_or_end` (a stall), `replaced` (the asker replaced or withdrew it). The gateway runs a set-based reconcile at start and every minute for the changes that come with time.

The log (`desk_log`) holds what is not a question: "CTO applied ...", "CTO sent ...", a new app, a daemon report (a Claude session or background service whose wheel is off, decision 0128), and what the gateway did by itself (closing an ask, reminders, idle checks, retired notices). It keeps the newest 2000 lines. The Desk tab (tab 7) shows the asks, the changes in flight and the log. The CTO and app notices are in the log only: the migration that came with the Desk closed their old Inbox rows (`moved_to_log`), and `desk_settle` marks a log line as undone.

### Sign-in asks (decision 0132)

A `sign_in` ask says that a CLI agent-net depends on needs the owner's
interactive login. It names the machine, a provider key and the reason. The
command is never part of the ask or of any frame. `protocol.SignInProviders` in
`internal/protocol/signin.go` is the fixed allowlist; for now `alibaba` runs
`bl auth login --console` for the Alibaba Bailian CLI. Adding another CLI means
adding an entry there, not accepting a command from a caller.

The daemon raises the ask from a quota reading whose provider error has a
known login remedy (`No console access token found` or `bl auth login
--console`). It raises one ask per machine and provider. Repeats refresh that
one ask, and a dismissed ask is not raised again until the provider error
clears. The CTO may raise an ask for a known provider and machine with
`sign_in_raise` and list them with `sign_in_list`; a command field is refused.

The owner's action is `i`, Open sign-in (`sign_in_open`, owner only). It opens
the allowlisted command in a herdr pane titled `sign in: <label>` on the
machine the ask names. On this machine that is the local herdr. On another
machine the target daemon opens its own pane and this machine shows it with
`herdr --remote <target>` (decision 0046); there is no ssh fallback. A machine
without herdr answers with a note in the result. After the pane closes, that
machine runs the provider's quota check once. When the login error is gone the
ask closes itself with `signed in`; a persisting error keeps it open. The web
console shows the same ask with an Open sign-in button when it lists the Desk.

## The chief of staff (decision 0114)

A registered agent session sorts the asks that would go to the owner first. The
owner or the CTO names it with `chief.set` (operator `chief_set`, `{session_id}`;
an empty id clears it). The session must be a live agent session, not a worker,
the owner, the CTO or a bot. Its model is the setting `chief.model`, default
`codex · openai · gpt-6.1-sol · medium`, which `chief.get` (operator `chief_get`)
returns with the session, its name and whether it is online.

**Routing.** A new ask goes to the chief (`next = chief`, the Desk shows it as
"with someone") when a chief is online and the ask does not name another next.
The chief's own asks go to the owner. A team lead's route is not changed.
When `chief.set` names a chief, and when the gateway starts with a chief set,
every open ask that still waits for the owner and was never offered to a chief
is marked as waiting for it. That includes asks opened before the chief
existed. An ask the owner already answered, one that was already sorted, or one
already sent to a session is left alone. The 15 minute fallback starts when the
ask starts waiting for the chief, so an adopted ask gets its full time.

**Batches.** Asks for the chief leave as one message from `agent-net` after a
quiet window of 5 seconds since the newest ask, and never later than 30 seconds
after the oldest. One message holds up to 25 asks: id, kind, asker, subject,
question, options, recommendation and up to five related open asks. The gateway
checks for due batches once a second. A sent ask is never sent again.

**Fallback.** An ask the chief has not sorted in 15 minutes (a constant) goes to
the owner. An ask waiting on a chief that is offline goes to the owner at once.
Each one writes a Desk log line (`The chief of staff did not sort it in 15
minutes, so it is with you: ...` or `The chief of staff is offline, ...`).

**Chief availability.** A chief whose session is online can still be unable to
work. The gateway marks it unavailable when any of these holds. The check runs
once a second with the batch tick, and when an ask is created.

- *No quota.* The chief's provider has an exhausted window in the machine's
  quota reading, the same data the picker uses (`quota exhausted (weekly window,
  until 2026-11-15 00:00 UTC)`). A stale reading is ignored. The gateway reads it
  from memory and the stored quota blocks, never inside a database transaction.
- *Not running.* The session is offline (`session offline`), the harness was
  lost (`harness lost`), or the session reports a provider usage limit from its
  last turn (`harness error: <limit> until <time>`, from `limited_until`).
- *Silent.* The chief was sent asks, made no sort and showed no harness
  activity (`sessions.harness_activity`: output, a turn, a delivery
  acknowledgement) for `ChiefSilentAfter` (10 minutes, a constant; `no sorts for
  10 minutes`). A chief that is still working is never silent. The mark is kept in the owner setting
  `chief_silent_at`. It ends after `ChiefRetryAfter` (15 minutes, a constant), when the next ask goes to the chief
  again as a probe, or when the chief sorts. Naming a new chief clears it.

While the chief is unavailable, an ask that would go to the chief goes to the
owner path instead (`next = owner`): the Desk's Needs you list, which the CTO
reads and answers as it did before the chief existed. The asks that already wait
for the chief move there at once, each with a Desk log line (`The chief of staff
cannot work (<reason>), so it is with you: ...`). No batch is sent. A chief that
is online but cannot work is not offered those asks again: they get
`route_fixed`, so adoption leaves them with the owner path when the chief is
back, while new asks go to the chief again. An offline chief keeps the earlier
behavior: its asks may be adopted when it returns. The chief may still sort only
asks that wait for it. `chief_get` returns the reason in `down` (empty when the
chief can work). No setting and no approval gate comes with this.

**`agent_ask_sort {ask, result, reason, into, to, priority, why_you, sorter, since, until, from_id, to_id}`**
(any agent session; only the live chief may call it, others are refused).
`reason` is required for every sort except `undo` and `list`, up to 300
characters. `result` is one of:

- `close`: the ask closes; its answer is `chief: <reason>`.
- `merge`: the ask closes; `into` is another open ask, which gets the line
  `merged: <id>` in its detail.
- `send`: `to` is a live session that gets the ask as a message from `agent-net`
  naming the chief. The ask waits on that session. A `to` that is the ask's
  own session is refused, with the reason `this ask is about <name>; sending it
  back to them makes a loop. Close it, send it to someone else, or send it to
  the owner.`, because the ask is about that session and would return to it.
  Every other send keeps the path it had.
- `owner`: the ask goes to the owner. `priority` (1 to 3) and `why_you` are
  required.
- `list`: reads only. It returns the open asks that wait for the chief, oldest
  first, in `asks`: `ask`, `kind`, `from`, `text`, `recommendation` and `age`
  (such as `12m` or `3h05m`). `ask` and `reason` are not needed. It changes no
  ask. It also returns `stats`, the counts described below.
- `undo`: puts the ask back the way it was before the chief's newest sort of
  it, so the chief can sort it again. It needs `ask` and takes no other sort
  field; `reason` is not needed. It is refused when the ask changed since that
  sort (for example, a session answered it), when the sort is undone already,
  or when the newest sort is not the chief's. It writes an `ask_sorts` row with
  sorter the chief and one Desk log line. A merge also removes its
  `merged: <ask>` line from the ask it merged into.

**`stats` of a list.** Every `list` answer carries `stats`: the successful sorts
of one sorter by result, counted in one grouped query. `sorter` defaults to the
caller (the chief); `since` and `until` are Unix seconds (both inclusive), and
`from_id` and `to_id` are sort ids (both inclusive). The shape is
`{"sorter":"s_...","counts":{"close":736,"merge":1,"owner":8,"send":16},"first_id":1,"last_id":761}`.
`counts` holds only the results that occurred, `undo` included when it did, and
`first_id` and `last_id` are the smallest and largest sort id that matched (zero
when none did). The chief uses it to audit its own sorts without reading every
row.

**Hide handled history.** The live chief or a live owner session may call
`agent_ask_sort` with `result: "hide"`. For a preview, set `mode: "preview"`,
`kind`, `hide_result`, `before` (an exclusive UTC RFC3339 cutoff), and optionally
`repeats_only: true`. The response includes the exact sort IDs, visible counts
before and after, and a token. Apply with `mode: "apply"`, the same filter and
`token`. The gateway rejects a changed match set. The response contains a
receipt and IDs. Undo with `mode: "undo"` and `receipt`. The operation changes
only sort history visibility, never inbox items. Open and dismissed asks are excluded. Visible counts cover all sort history,
not only the last 24 hours shown on the Desk. Hidden
rows are omitted from Handled for you in the Desk and web console. List stats
still count them and include `hidden_count`.

For `repeats_only`, only `session_stall` closes are supported. Stall asks carry
the session ID and the activity baseline in `conversation_id` (Unix seconds).
Stall sorts for a session with the same nonempty baseline are one episode
until a sort for that session has a different baseline; the first close stays
visible. A changed baseline starts another episode. Asks without a baseline are kept. This uses recorded stall baselines,
which have one-second precision.

**`agent_ask_sort {ask, detail: "full"}`** reads one ask in full for the chief. It
sorts nothing, and it takes only `ask`. The answer has `result` `read`, `next`,
`state` and `full` with the whole question and detail, the recommendation and
each option, the asking session (`from_session`, `from_name`, `from_kind`), the
kind, the stored `status` and the Desk `state`, the route (`next`, `next_name`,
`priority`, `why_you`), the ask's `routing` sort history, and the owner's
`answer` and `answered_at` when there is one. A draft-charter ask adds `charter`,
one row for each field with its level, scope, attribute and exact proposed
value. A learned ask adds `learned`, the proposal with its node, field, phrase,
kind, severity, evidence, and the old and new value. Secret values are masked,
but a token's scope and destination stay readable; the token itself never
leaves the gateway. This is the read the chief uses when a batch summary does
not say enough.

**`agent_ask_intake {action}`** (any agent session; only the live chief may call
it, others are refused). It finds questions that sit inside sessions waiting
for their user, reads them, and imports one as an ask. It reads and imports
only: it never sends input to a session, answers an ask, or changes a
session's state. The read happens on the machine that holds the session; the
chief's daemon asks the gateway, which relays one request to that machine.

- `list` (optional `limit`, default 25, at most 50) returns the sessions that
  are online or idle, are not workers or the CTO, are not busy, and whose last
  assistant message has not been imported. A row with `state` `ok` has
  `session`, `name`, `harness`, `machine`, `last_turn`, `last_turn_at`,
  `waiting` and `open_ask`. `waiting.kind` is `explicit` for a wait the session
  raised itself and `harness` for a wait its harness reported; it carries the
  stored `text` and `since`. `open_ask` is the id of an open ask from that
  session, so the chief can see what already reached the Desk. A harness with
  no reader, a missing transcript, an offline machine or a plugin that does not
  answer returns `state: "unavailable"` and a `reason`; agent-net never invents
  the text.
- `read` with `session` (the id or exact name) returns `turn` with the exact
  last assistant message in `text`, the input before it in `preceding_input`,
  their RFC 3339 times in `at` and `preceding_input_at`, `ended` and a stable
  `ref`. `preceding_input` is whatever the session stored as the user turn
  before the reply, so it is not necessarily the owner's words: `input_source`
  says who wrote it. It is `owner` when the user typed it in the session,
  `peer` for a message agent-net delivered from another session, `agent_net`
  for a notice agent-net wrote itself, and `unknown` for injected text whose
  writer the read cannot tell. A peer message also carries `input_from` (the
  sender name) and `input_session_id` when the stored text names them. A
  delivered message is never reported as `owner`. Secrets are masked with the
  same helpers as the owner's session text, and `cut` says the text was longer
  than the 2,000 character limit. The chief, not agent-net, decides from the
  text whether it is a question, an idle done or an FYI.
- `import` with `session`, `turn` (the `ref` from list or read), `text` (the
  question in one line), optional `options` (at most four) and
  `recommendation` creates one `question` ask about the session, with the turn
  reference as its `source_ref`. The ask routes to the chief like any other,
  so its normal sort applies. The same turn is imported at most once: a later
  call returns `state: "already_imported"` and the existing ask id, even after
  that ask closed. If the session already has an open ask, the call creates
  nothing and returns `state: "existing_open"` with that ask id.

The chief's own asks go to the owner, so importing a turn from its own session
would not create a second chief sort. Every import masks common secrets in the
text, recommendation and options before it stores them.

Every sort writes one `ask_sorts` row with the ask as it was (`prev_next`,
`prev_status`, `prev_answer`, `prev_answered_at`) and one Desk log line with
source `chief`. Nothing is deleted.

**The daily brief.** The owner can give the chief one morning prompt without a
new ask. The setting `chief.brief_time` is a local time `HH:MM` (default
`09:00`) read in the owner's time zone (operator `timezone_get`). A named zone
wins. When the setting is `auto`, the gateway resolves it from the zone a
machine reported, preferring the owner's Mac; a server zone does not win over
it. If no machine reported a usable zone, the timer does not run,
`agent_owner_brief` reports `waiting for the owner's time zone`, and the
gateway never stands in UTC. The zone is resolved again on each tick, so a
machine that reports its zone later starts the timer. The gateway checks on its minute tick and
sends the chief one message from `agent-net` on the first tick of each
owner-local date at or after that time: `Daily brief due for <local date>.
Write it and deliver it with agent_owner_brief.` The wake date is stored in
`owner_settings` (`chief.brief_last_wake`), so a gateway or daemon restart does
not send twice, and a tick missed while the gateway was down still sends once
later that day. A wake is not a delivery: the delivered date is stored on its
own, in `chief.brief_last_sent`, and only a delivered brief sets it. An older
gateway wrote the wake date to `chief.brief_last_sent`; when
`chief.brief_last_wake` is empty, that older value is read as the wake date, so
an upgrade does not wake the chief twice on the same day. The send wakes the
chief like any other message.

**`agent_owner_brief {body, date}`** (any agent session; only the live chief may
call it, others are refused). `body` is the brief, at most 15 lines, and `date`
is the owner-local date as `YYYY-MM-DD`. It creates one Desk item of kind
`brief`, labelled `Daily brief <date>`, with the body as its text. A brief is
informational: it has no approve or refuse, only dismiss, and it is never an
approval ask. A second call for the same date replaces that date's brief and
returns its item id and the date. For example:

```
agent_owner_brief {"body":"Handled the deploy fix and two reviews.\nDecisions waiting: PR #31 (recommend merge), the storage plan (recommend wait).\nStuck: the Mac build needs a signing key.","date":"2026-11-09"}
```

**`agent_owner_brief {}`** reads the schedule instead. It returns `brief_time`,
the resolved IANA `time_zone`, `next_run_local`, `next_run_utc`,
`last_date_sent`, `last_wake_date`, `last_item_id` and `status` (`active`,
`waiting for the owner's time zone` or `no chief of staff`). `last_date_sent`
is the owner-local date of the last brief that was actually delivered;
`last_wake_date` is the date of the last wake, which may have no brief yet. The
next run follows the local clock across daylight saving time. The last brief id
is also stored in `owner_settings` (`chief.brief_last_item`).

**`ask_history` `{ask}`** (operator `ask_history`, gateway `ask.history`) returns
`{"sorts": [...]}`, oldest first. Each sort has `at`, `sorter`, `result`,
`into`, `to`, `reason`, `priority`, `why_you` and the `prev_*` fields.

Asks now carry `priority` (the chief's rank for an ask sent to the owner).

### The chief on the Desk (decision 0114, part 2)

The Desk shows what the chief did and lets the owner correct it. Each method is an operator method; the CTO can run the changing ones too.

- **`chief_sorts`** (gateway `chief.sorts`, read) returns the chief's sorts of the last 24 hours, newest first. Each row has the sort fields, the ask's `question` and `undone`. The Desk's Handled for you list reads it.
- **`ask_undo` `{ask}`** (gateway `ask.undo`, owner or CTO) puts an ask back the way it was before the newest sort. It restores `next`, `status`, `answer` and `answered_at` from the sort's `prev_*` fields. A `next` of the chief becomes the owner, because the chief got its one batch and is not sent the ask again. `priority` and `why_you` are cleared. It writes an `ask_sorts` row with result `undo` and a log line from the owner (or the CTO). It is refused when the ask changed since the sort (for example, a session answered it), and when the sort is undone already. A merge also removes its `merged: <ask>` line from the ask it merged into. The chief has the same undo as `agent_ask_sort` with `result: "undo"`, with two differences: it undoes only its own newest sort, and the ask goes back to the chief, not to the owner, so the chief can sort it again. Its undo row names the chief as the sorter.
- **`ask_mark` `{ask, mark, why}`** (gateway `ask.mark`, owner or CTO) stores a mark. `mark` is `didnt_need_me` (the owner did not need to see the ask) or `should_have_asked` (the chief should have sent it to the owner). `why` is optional, one line, up to 300 characters. The mark is stored in the owner judgment store (the standing orders memory) as a judgment with `kind` `correction`, `area` `chief` and `ref` set to the ask id. Its text says what the owner marked, gives the ask's first line and the `why`. A log line is written. The chief's next batch message lists the newest five of them under "Owner corrections". The old table `chief_corrections` is no longer written or read; a migration copied its rows into the judgment store, and the table is kept.
- **Finding the marks.** `agent_owner_judgments_find {marks: true, limit, offset}` returns the owner's marks, newest first, with a page at a time. Each row keeps the judgment fields and adds `ask` (and `ref`), the ask's whole `question`, the chief's latest `disposition` (closed, merged, sent to a session, or sent to the owner with priority and why), `mark` (`didnt_need_me` or `should_have_asked`), `mark_text` in the owner's words, and `saved_at`, the mark's time. A `query` searches the mark text, which carries the ask's first line. The same call without `marks` searches every judgment as before. With `marks: true` the result also carries `marks_stored`, how many marks the judgment store holds, and with no rows a `note` that says so: zero marks stored means the owner has not marked an ask yet.
- **Answers in the judgment store.** Every ask the owner decides gets one judgment row with `kind` `answer` and `ref` set to the ask id: a typed answer, a button (`approved: <ask>`, `refused: <ask>`, `dismissed: no decision`, `accepted` or `rejected` for a charter draft, `resumed the loop` or `stopped the loop`), an overrule or let-stand of a rejection (with the reason and the request that went through), an app consent, and a reply the owner typed in the asking session (his exact text). The CTO answering for the owner writes the same row. A second answer of the same ask updates the row. The ask keeps its answer code. The gateway also fills missing rows at start, and rewrites a row that holds only a code. A search term with a hyphen or colon is plain text; `OR` between terms, a quoted phrase and a trailing `*` still work.

The CTO lists have `chief_sorts` (read), and `ask_undo` and `ask_mark` (changes, in the `settings_change` area `operator`).

**The Desk (tab 7).**

- The header reads `Desk · chief of staff <name> on` (or `off`, or `no chief of staff`), then `sorted N asks today`. "Today" is the last 24 hours of sorts. When the chief cannot work (see Chief availability), the first part reads `chief of staff offline: <reason>` in red. The phone and web Desk show the same words as one line above the asks, and show nothing when the chief can work.
- **Needs you** lists only the asks whose `next` is the owner. Each row shows its priority and why_you under it. The tab title (`7 Desk (3)`), the Sessions band and the header count this list. A daily brief shows as one row labelled `Daily brief <date>`; Enter reads it and `x` dismisses it. It offers no other action.
- **Handled for you** lists the sorts from `chief_sorts`. A row shows the result (closed, merged, sent, to you, or undone), the question, the reason when it fits, and `→ session` for a send. `u` undoes a sort that is not undone; `m` marks it "should have asked me". The full reason is in the panel.
- **n** on a Needs you row marks "didn't need me". **m** on a handled row marks "should have asked me". Each asks for one optional line of why; Enter stores it, Esc cancels.
- **The digest.** When the owner comes back after an hour or more without a key press, the Desk shows "While you were away (N h)" with four counts (closed, sent on, answered, for you). The counts are read once from the newest 500 log lines, and only the lines since the owner's last key press count. `x` hides the digest; while it shows, `x` only hides it, and does not dismiss an ask. The digest reads the log text the gateway writes (`Chief closed ask:`, `Chief sent ask to`, `so it is with you`, and `Closed ... (answered)`): change those lines and the counts change with them.

## Waiting sessions on the Desk (tab 7)

A session can wait for you: it asks permission for a tool, asks a question, or wants your input. The TUI lists those sessions on the Desk (decision [0016](decisions/0016-needs-you.md)). Every machine's TUI sees them, so one screen answers "what needs me" across the network.

Non-worker sessions on Claude Code, Codex, Pi and OpenCode can call `agent_owner_ask` with `kind: "question"`, `text`, optional `options`, optional `urgency`, and optional `replaces` (the ID of one earlier pending ask from this session to retire). A new ask adds to the pending ones: it never retires an earlier ask by itself, of the same kind or any other, so an approval for a data delete stays open when the same session later asks about money. Giving `replaces` closes exactly the named Inbox item as replaced; an empty question closes it without creating a new one. The result lists the session's other open asks in `other_open` (each with `id`, `kind` and the first line of `text`, at most 100 characters), with a note that `replaces` retires one of them, so the asking agent can retire a stale ask on purpose. This holds for questions, approvals and ideas, for asks sent to a team lead, and for the CTO's `agent_owner_ask`. The question is one line, cut to 200 characters with control characters removed. Options are up to four short strings, cleaned the same way. Urgency is `normal` or `high`. The guidance asks an agent to call this tool when it ends a turn waiting for your decision or input. A plain-text question at the end of a turn does not raise an alert by itself. A worker cannot call this tool; it must put the question in its hand-back or ask its caller with `agent_message_reply`. Worker rows do not appear in the TUI Inbox. When a worker reports a wait, its caller receives one blocked notice for that wait, naming the kind (approval or answer), the start time and the question text. The caller started the worker and must unblock it, so the text is for the caller, not an outside peer.

The daemon stores the question as a `question` waiting state with the options, urgency and start time. It sends the state with the session to the gateway, and every machine reads it from the agent list. The existing harness hooks still report permission prompts, forms and input dialogs. `agent_announce` also accepts an optional `waiting` string as a fallback.

Open the Desk, select a waiting session and press Enter to go into it. On an inbox question, `t` opens a one-line answer box. The first option, if any, is filled in. Enter sends the text as an owner message; Esc cancels. On a question or approval with options, Enter opens the details, up and down mark an option, and Enter answers the marked one. The digits `1` to `7` keep switching tabs there. The wait clears when the owner message is delivered. It also clears when the owner types a Claude Code prompt (with the `needs-hook` installed), a Pi or OpenCode prompt, or a Codex turn starts; when a permission or form settles; when the session becomes busy; or on an announce without `waiting`. A wait that a harness event reported (a permission, a form, a dialog) also ends with the turn: when a session goes from busy to idle, or a plugin says hello again while busy, that wait is dropped. This covers a permission that was denied or interrupted. An explicit `agent_owner_ask` and an announced `waiting` outlive the turn. Dismissing the question or approval on the Desk ends the wait too: the gateway tells the session's machine. If the gateway is down, a queued owner message clears the wait when delivered.

The owner's own prompt in a Claude Code, Pi or Codex session (Codex: a user message with no `clientId`; not a peer message, a reminder or a scheduled turn) also closes that session's open question, approval and `stopped with work left` Inbox items, with the reason `owner_replied`, because the owner answered in the session. An `agent_announce` without `waiting` closes the session's open question and approval items that it raised before its earlier announce, with the reason `moved_on`; an item raised since that announce stays. An escalation (a reply with outcome `escalate`) closes, with the reason `answered`, when the session that escalated later sends a plain reply in the same conversation; the outcome is then marked answered too. At the first gateway start after this rule, a one-time cleanup (marker `asks_moved_on_cleanup_v1` in `owner_settings`) closes, with the reason `moved on (cleanup)`, the open approvals and check items that a session left behind: its task started more than 10 minutes after the item. Questions are not part of it; they close by the rules above. Closed items are kept, never deleted.

For a new explicit `agent_owner_ask` question, the daemon on each machine with an owner TUI open shows a desktop notification, at most once per session in five minutes. The limit survives a daemon restart. On macOS it uses `osascript`; on Linux it uses `notify-send` when available. Otherwise it does nothing. The notification does not contain the question text. Open `?` help and press `n` to toggle desktop notifications for that machine; the setting is on by default and persists locally. The CTO proposes changing the setting with `settings_change` area `notifications` and `{"disabled": true}` or `{"disabled": false}` on the CTO's machine.

The local daemon method is `waiting` (`{"kind", "text"}` or `{"clear": true}`). A plugin is bound to its session; a command line names `harness` and `native_id`, and is silent when the session is unknown. It is not an owner-only method: a harness hook must be able to call it for its own session, like `hint`. Explicit asks also carry `options`, `urgency` and `explicit` through this path. Claude Code's `needs-hook` hooks are installed with `agent-net setup --claude-needs-hook`; they use `UserPromptSubmit` to clear the wait.

**Waits the harness reports itself** (decision [0060](decisions/0060-waiting-for-user-auto-answer.md)). Claude Code writes `status: "waiting"` and `waitingFor` into its session file while a dialog blocks the turn: a permission prompt, an `AskUserQuestion` question, an MCP input form, a network request or a plan approval. The daemon reads that file every second, with or without the hooks. The wait's words come from the transcript: the tool call that has no result yet (the first question of `AskUserQuestion` and its option labels, `approve the plan` for `ExitPlanMode`, or `<tool>(<command, path, url or pattern>)`). Only that line is read. A dialog you opened yourself (`waitingFor: "dialog open"`, for example `/config`) is not a wait. The session's status stays `unknown` while it waits, as before, so a worker stuck on a prompt still ends at its idle limit; the TUI shows `needs you`. When the file says `busy` or `idle` again, the wait ends, also a wait the hook started; an explicit `agent_owner_ask` stays. Codex marks a thread that waits with the active flags `waitingOnApproval` (a `permission` wait, `approve a command or a change`) and `waitingOnUserInput` (a `question` wait, `answer a question`); the flags carry no question. A wait the harness keeps reporting keeps its start time, and the gateway keeps the first start of the same wait (same kind and text) when a daemon reports it again after a restart. Waiting text is cleaned of secrets (`[REDACTED]`) as well as cut and made one line, at the daemon and at the gateway.

**What agents see.** `agent_sessions_find` and `agent_session_get` show an online session that waits with status `waiting for its user` and a `waiting` object with `kind` (`permission`, `question` or `input`) and `since`. The question is never in an agent read. A request to such a session gets the [waiting notice](#delivery-states) once per sender.

## Delegation policy

Decision: [0014](decisions/0014-delegation-policy.md). Security: [security.md](security.md#delegation-policy).

A per-session or per-project setting that says who does the coding. Set it with `p` in the TUI. Values: `delegate` (`off`, `native`, `workers`), `coder_model` and `main_model` (loose names, resolved against the model list of the session's machine and harness), scope (`session`, `project`). The session's own value wins over its project's. The operator method is `session_policy` (`{"session_id": "...", "policy": {"delegate": "native", "coder_model": "deepseek 4.1", "main_model": "sol 6.1"}, "scope": "session", "dry_run": false}`), served only to the owner's command line or TUI. It answers `{"session_id", "saved", "effective"}`; `effective` is the policy the session ends up with, with `coder_resolved`, `main_resolved` and the errors of a name that did not resolve. A dry run saves nothing. An empty policy clears the value at that scope. `list --json` has `delegation` (`scope` says where the effective value comes from).

The gateway stores the policy in `sessions.delegation` and the project defaults in `project_policies` (migration 10), and logs a `session.policy` event and line when one is saved. The policy is a rule agents follow, so it is public read-only: `agent_session_get` shows a session's effective policy, and `agent_graph_read` shows the project policies per machine. Only the owner sets or clears it.

**What the session gets.** The local method `policy` (every turn; a session asks for its own, the command line asks by `harness` and `native_id`) answers `{"text", "mode", "agent_id", "coder", "main", "charter"}` or an empty object. The daemon asks the gateway (`policy.get`) and caches the answer for 2 seconds. `charter` is the repo charter context of [charters and component relationships](#charters-and-component-relationships); it is there even when the session has no delegation policy. A nonempty agent charter follows as a separately labelled block. For a worker, that block describes its caller agent. `text` is one line that starts with `[agent-net]`:

- `native`: `[agent-net] Your user's standing policy for this session, set in agent-net: delegate all code changes to the `agent-net-coder-<model>` subagent (model <model>). You plan, write the brief, review its diff and run the tests. Do not edit code yourself unless the subagent fails twice.`
- `workers`: the same, but to start a worker with `agent_worker_start` with the coder model.
- `off`: `... do the code changes yourself. Do not start subagents or workers for them.`

| Harness | Where the text goes | `native` | Main model |
|---|---|---|---|
| OpenCode | A system part on every model request of a root session (`context` hook). A subagent's own session gets none. | The plugin defines `agent-net-coder-<model>` (mode subagent, the resolved model) at run time. | `session.switchModel`, once per value. |
| Claude Code | The `UserPromptSubmit` hook (`agent-net policy-hook`), as `additionalContext`. Every hook answer stays in the transcript, so the full charter block is sent once per session, again when the charter changes (with one line saying so), and again after a compaction. The delegation policy line is still sent every turn. Needs the hook: `agent-net setup --claude-policy-hook`. | `~/.claude/agents/agent-net-coder-<model>.md` with a `model:` line, written by the daemon when the hook asks. | Not possible from outside. |
| Pi | The system prompt, each turn (`before_agent_start`). | Pi has no subagents: the `workers` text is used. | `setModel`, once per value, for an exact `provider/id`. |
| Codex | The delegation policy is not applied: the app server takes no text per turn. The charter reaches a Codex session as [owner rules](#owner-rules-reach-every-session) instead. | | |

The Claude Code hook output (one line of JSON on stdout, exit 0), only when the session has a policy with text or its node has a charter:

```
{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"[agent-net] Your user's standing policy ..."}}
```

With both, the policy text comes first and the charter second, separated by a blank line. The daemon keeps, per session, the hash of the last charter block it sent and a compaction generation. A prompt gets the full block when the hash changes or the generation moved; otherwise the block is left out. `PostCompact` and `SessionStart` with source `compact` start a new generation, because the summary may have dropped the block. The change line before a new text is `[agent-net] The charter changed since it was last sent. The full text follows.` The hook still prints nothing and exits 0 on any problem.

## Charters and component relationships

Decision [0086](decisions/0086-charter-levels.md) defines charter fields and inheritance.
The graph separates Teams, Repos and lasting Agents. A session resolves its charter
with its current Repo and Agent. Charters and graph edges are public to enrolled machines.

**Writing a charter.** Budget about 1200 characters for the effective charter.
Neighbors read the first 40 characters of accepts and refuses; put the main category
first. Put detailed project instructions in AGENTS.md. Warnings about size and sensitive
details are advisory; the existing 2 KiB limit applies to each written value.

| Attribute | Set at | Meaning |
|---|---|---|
| `purpose` | Repo, Team | A sentence describing the project or team. |
| `maturity` | Repo | Enum: experimental, early, growing or mature. |
| `maturity_note` | Repo | Optional one-line explanation, separate from the enum. |
| `accepts` | Agent | Work it takes from other agents. |
| `refuses` | Any level | Work it declines; inherited safety refusals always apply. |
| `escalate` | Agent; Team default | Optional short line; the Team default is "ask the team lead". |
| `feature_requests` | Agent | How to raise an idea for new work. |
| `rules` | Any level | Free text for taste and house rules. Code does not interpret it. Inherited items from all levels are joined, each once. |
| `work_mode` | Any level | `workers_code` or `on_request` (the default). See below. |

**Work mode.** `work_mode` says who writes code in a session ([decision 0098](decisions/0098-work-mode.md)). With `on_request` (also what an unset value means), a session hands a task to a worker only when the owner asks. With `workers_code`, the session designs, architects and reviews, and agent-net workers write all code, with no owner ask per task. It is a charter attribute, so it inherits global -> Team -> Repo -> Agent and the nearest set value wins; the charter `sources` say which level set it. Setting it adds no limit and no gate. The owner sets it on the charter form of any card (Team, Repo or Agent) and the CTO sets it without a proposal with `charter_set` (any level), `node_settings_set {node, setting: "work_mode", value}` (the repo level), or `settings_set {ref: {kind: "team", id: "<team>"}, setting: "work_mode", value}` (the team level; an empty value clears it). The per-turn charter context prints one line only when the effective mode is `workers_code`, for example `Work mode: workers write all code (team forge). Design and review; start agent-net workers for code; no owner ask needed.`; a session in the default mode gets no extra line. The Graph card shows a `work_mode` row after the rules, with the level that set it (`default` when none did). `agent_graph_read` returns it as `charter.work_mode` and `agent_session_get` returns it as `charter.work_mode` too; `charter.sources.work_mode` names the level.

Inheritance is global -> Team -> Repo -> Agent. Each level stores only its overrides.
Every field takes the nearest set value except refuses and rules, which are the union of all levels.
If an Agent and its Repo belong to different Teams, both Teams' safety refusals and rules apply.
The effective charter carries a sources map with level, scope and value per field;
refuses and rules keep all contributors. A standalone Agent graph row uses its sole owned Repo
when unique; a session view supplies its explicit Repo context.

`graph_change` action `charter_set` takes level, node (the scope), attr and value.
Global scope is empty. A typed ref can select a Team, Repo or Agent. `team_charter`
takes team, attr and value. Its attr `asks_to` is the Team setting below, not a charter text. Empty text clears the local override. Legacy unscoped
repo clients can still edit existing work boundaries; the removed consumers, needs
and notes attributes are rejected. The upgrade preserves them under matching headings
in rules. It splits maturity at " - ": the first word is the enum, unknown words become
early, and the explanation becomes maturity_note. No charter is valid and adds no limit,
deploy approval or charter prompt. The Graph's e key edits the selected level; E edits
global defaults. The form shows sources and never copies inherited text into an override.

**Reading it.** The daemon frame `graph.read` takes `{"project": "cogforge"}` and answers `{"graph": {"nodes": [...], "edges": [...]}}`. A project name returns that node, its charter, the nodes its edges name, and the edges that touch it; an empty name returns the whole graph. An eligible project with no stored charter is still an implicit node, with empty charter attributes. Owner-facing graph reads hide inactive empty implicit nodes after seven days; `graph_get {"all":true}` includes them. Nodes with a charter, an edge or stored owner history are retained. No session or peer identity is needed: this is a read, not a change.

The owner made charters and edges public to every agent. `agent_graph_read` returns them all, with the owner-only parts of a node left out (see the agent graph read above). The CTO reads the same view through `graph`.

**The `calls` edge.** Each shared MCP server granted to a bot (`g` in Connected apps) shows in the typed Graph as a read-only `calls` edge from the bot node to the Repo that hosts the server. The gateway derives it on every read, with one query for all grants and the registry, and stores nothing. The Repo is the single project of the server's assignment (scope `projects` with one project), or else the Repo named like the server. A server with neither draws no edge, because the Graph has no machine nodes; the bot card still lists it under `can call`. A disabled or removed server draws no edge. The edge has no rule and no mode. `edge_set`, `edge_remove` and undo refuse the kind `calls`: only the grant changes it. In the TUI it is drawn like a `depends` edge, and its label is `calls`.

**Maturity and its defaults.** A node's `maturity` level sets five controls. A node may override any one of them in a node setting; the owner sets it with `node_settings_set`, and the override is stored beside the charter.

| | `experimental` | `early` | `growing` | `mature` |
|---|---|---|---|---|
| Tests before merge (`tests_before_merge`) | `optional` | `optional` | `suite` | `suite-new-tests` |
| Merge authority (`merge_authority`) | `full` | `full` | `full` | `delegated` |
| Worker sandbox (`worker_sandbox`, decision 0020) | `sandboxed` | `sandboxed` | `sandboxed` | `sandboxed` |
| Requests from consumers (`judge_consumers`) | `free` | `free` | `charter` | `strict` |
| Deploy rule (`deploy_rule`) | `brief` | `brief` | `brief` | `ask` |

An empty or missing maturity uses the `growing` controls. Approval overrides still appear in the node's rules text. The text the session and its workers get is plain words, for example `maturity mature: run the suite and add tests for new behavior; merge needs an approval for the exact merge from anyone above you in the tree who holds merge: the owner approves directly or through an agent-net approval note from agent_owner_ask with kind approval; your repo lead or team lead approves in a message from them; ask before deploying`, followed by the consumer rule. A node with no maturity gets rules text when a merge or deploy approval override requires it.

The worker sandbox level from decision 0020 follows the node. The project policy form and `node_settings_set worker_sandbox` both change the repo setting. When it is unset, the node's maturity default applies. A node with no maturity and no override keeps `sandboxed`.

**Autonomy.** A node's `autonomy` setting is `off` (the default) or `wake`. With `wake`, an idle check's `more` answer wakes the existing online session with one short continue notification instead of leaving an Inbox row, and a message queued for an offline session of the agent wakes that session (see message delivery below: Claude interactive, Codex and OpenCode; not Pi). It never starts a new session or a clone: that stays with the owner, or with the CTO as a proposal. `blocked on X` always leaves an Inbox row. Wake-ups have their own daily cap of 6 per agent and stop after two in a row with no new message and no change in open work; the second leaves one row, `<name> was woken twice with no progress`, and a later change allows waking again. The gateway stores only the daily count, the no-progress streak and the progress markers it needs, never task content. The owner changes autonomy on the Graph agent card with `a`, or with `node_settings_set`. The CTO sets it at once with `graph_change` action `settings_set`, `setting: "autonomy"` and `value: "off"` or `"wake"`. The owner's answer to the session-start conflict in decision 0036 is that agent-net may wake an existing agent, but creating a new agent or a clone stays with the owner or the CTO.

**Standing loops.** A node can have a loop: `loop_every` (a duration such as `4h`; empty is off) and `loop_prompt` (text; empty is the default), with the optional `loop_mode` and `loop_plan`. All are node settings (stored as `loop.every`, `loop.prompt`, `loop.mode` and `loop.plan`, repo level, kind advice), set with `node_settings_set` by the owner or the CTO with no approval step. The CTO tool enum includes all four (`loop_every`, `loop_prompt`, `loop_mode`, `loop_plan`; the last two are described below). `loop_prompt` keeps its case. On the Graph agent card the key `L` cycles `loop_every` through off, `1h`, `4h`, `12h`, `24h`; any other interval and a prompt are set with `node_settings_set`. The expiry loop runs the clock once a minute with one indexed query for nodes that have a loop. The first tick comes one interval after the loop is first seen. A tick goes to the repo lead, else the most recent live non-worker session of the node. It sends a notification from the reserved `agent-net` sender: `[agent-net] Loop: <what changed>. Your purpose is <purpose>. Check where you stand against it, do the next step, then announce what you did or what blocks you. If you need the owner, ask once with agent_owner_ask and carry on with work that does not depend on the answer; do not wait.` A set `loop_prompt` replaces the middle sentence (the one that starts `Check where you stand`). `<what changed>` is built from data the gateway already has, since the previous sent tick (or since the loop started): `Since your last loop tick (14:00 UTC): 3 messages (m_..), 1 worker hand-back (s_..), 2 step tasks (m_..), 1 new Desk ask (i_..), 1 closed Desk ask (i_..)`, with at most 5 ids per kind and `+N more`. It counts messages to the session from others, worker runs it called that ended, step task messages, and its own Desk asks that opened or closed (not the loop's own pause ask). With no change it reads `Nothing new since 14:00 UTC`. `loop_mode` is `always` (the default: every due tick is sent) or `on_change`: a due tick with nothing new is skipped, logged as `skipped, nothing new`, and costs one set of cheap queries, not a model call. A skip is not a tick, not a wake, and not a no-progress tick. `loop_plan` is a path relative to the session's folder, such as `PLAN.md` (no absolute path, `~` or `..`). When it is set the prompt adds `Read <plan>, do the next step, then update <plan>.` after the middle sentence. A busy session, one waiting for its owner and one under a usage limit are skipped (`skipped busy`). An offline Claude, Codex or OpenCode session is woken through the wake-on-message path (`woke`): the same session resumes, no new session starts, and no view opens. The loop is the owner's consent to that wake, so autonomy may be `off`. Pi cannot be woken, so its tick is skipped and the log says `skipped, cannot be woken (pi)`. The daily cap is the wake rule's 6 a day per session, counted with the idle-check wakes. Two ticks in a row with no progress (no announce, no new message, no change in open work) pause the loop. A pause or the cap opens one Desk ask with the options `resume` and `stop`; `resume` restarts the clock at once and `stop` clears `loop_every`. A cap pause also ends on the next UTC day. The ask closes when the loop resumes or stops. Each tick adds one `desk_log` line (`Loop <node>: sent to <name>`, `woke`, `skipped busy`, `skipped, nothing new`, `paused (...)`). The agent's next announce adds `Loop <node> result: <announce>` after it. The Graph card shows a `Loop` block: `loop every 4h · last 14:00 · next 18:00 · today 3 ticks, 2 made progress` and the last result line (`· ~45k tokens today` appears when tokens are known; today they are known only for workers, so it is left out). When the lead leads a Team with `asks_to=lead` ([decision 0111](decisions/0111-blocked-members-reach-the-lead.md)), the prompt adds a `Members:` part after the changes sentence, only when it has something to say, as short lines (each list shows at most 4 entries, then `- +N more`): `- <member> blocked or waiting for 4h 1m: <announce>` for each online member whose announce reads blocked or waiting (a task as for the blocked-member message, or an explicit wait; a harness permission prompt does not count), longest first, once for one text: a later tick does not list the same wait again, and a changed text is listed again ([decision 0126](decisions/0126-lead-waits-are-not-owner-asks.md)); and `- worker s_.. (<member>) ended 12m ago, change ch_.. not accepted yet` for each worker a member started (the run's caller) that ended since the last sent tick, or whose open change has no passed `accept` step on its current revision, however long ago it ended. An unreviewed worker stays in every tick until its change is accepted, closed or stopped. When only this part has something, the first sentence reads `Nothing new for you since 14:00 UTC`. `loop_mode: on_change` counts the part as a change, so a blocked member, or an unreviewed worker, makes the tick go. A Team with `asks_to=owner` adds nothing. The Sessions tab puts `↻` after the name of a session whose node has a loop. The graph read (`agent_graph_read`, the CTO `graph` view) and the session views carry a `loop` object with `every`, `mode`, `plan`, `last_tick`, `next_tick`, `ticks_today`, `progress_today`, `paused` and `last_result`. See decision 0094.

**Merge authority.** `full` lets the agent merge and push to the default branch. `delegated` needs an approval from the owner or from anyone above the node in the tree who holds merge: its repo lead or team lead. `ask` needs the owner's written approval. `MergeApprovalFor` returns the authority, whether approval is required, whether it must be written, the delegates and the text. For the exact merge, the owner approves directly or through an agent-net approval note from `agent_owner_ask` with kind `approval`; the repo lead or team lead approves in a message from them. On `ask`, always request the owner's written approval. An owner-instruction excerpt on its own does not approve a merge. The trust and owner-instruction notes use `recipient_merge_authority`, computed by the gateway for the receiving node at delivery. A sender cannot supply that field. The guidance block defers merges to this rule. Uninstall removes the guidance block as a whole.

**Deploys.** On a `deploys` edge the deployer applies the target's deploy rule, not its own. The deployer's per-turn context names each target and its rule, for example `You deploy shop; its maturity critical: ...; ask the owner before deploying`.

**Every turn.** The daemon reads the node of the session's project and puts a short charter text into the same places as the delegation policy: the Claude Code `UserPromptSubmit` hook (`additionalContext`), the OpenCode `context` hook, and the Pi `before_agent_start` system prompt. Codex has no turn context: its app server takes text only when a thread starts or resumes (see [owner rules](#owner-rules-reach-every-session)). The text holds `maturity`, `accepts`, `refuses`, `escalate`, the maturity rules and consumer rule, the edges that matter to the node (what it depends on, what depends on it, and for a deployer each target's deploy rule), and the decide rule. It starts with `[agent-net]`. The daemon caches the policy and its charter context for 2 seconds (a charter change clears the cache at once, see [owner rules](#owner-rules-reach-every-session)), and keeps the last answer for 10 minutes when the gateway is gone. With an older gateway, it reads and caches the graph per node instead.

**Connected charters.** The per-turn text also describes directly connected nodes with charters, in either edge direction and for any edge kind. Each line has the form `<project>: accepts <short accepts>; refuses <short refuses>; ask it for those`. The project name and each charter value are cut to 40 characters, so a line is at most about 160 characters. There are at most eight lines and the block is at most 1500 bytes. Edges with a non-default request rule come first, then `deploys` and `depends`; ties use the project name. The wildcard source `*`, the node itself and neighbors without a charter are skipped. A node without its own charter can still get these lines. The same lines go into worker briefs, including Codex worker briefs.

**Instruction signals.** The Claude Code `UserPromptSubmit` policy hook sends the first 300 characters of the latest prompt to the gateway for that session. The gateway ignores turns Claude Code injects (a delivered `cross-session-message`, a task notice, a system reminder): they are never kept as the owner's words, and the owner's earlier prompt stays; Pi and OpenCode do not send prompts to this signal path. The gateway keeps the latest owner prompt in `signal_prompts` for a 10-minute matching window. Only the first send in that window is considered. When the excerpt names the recipient session or project, the gateway records a row in `charter_signals`; a non-matching first send consumes the prompt too. An explicit assignment to another known project can still become private learning evidence. Reply outcomes are linked through the existing `graph_outcomes` table. The match is a time window, not proof that the send belongs to the same turn. A later prompt replaces the earlier one, including when it is empty.

| Signal data | What the gateway records |
|---|---|
| Owner instruction | The prompt excerpt, sender and recipient project nodes, message ID and time. |
| Reply outcome | The request and reply IDs, outcome (`accept`, `reject`, `redirect` or `escalate`), reason and redirect target when supplied. The outcome is linked to the request's signal when there is one; outcomes without an instruction signal can also be evidence. |

Signal records are kept for 90 days, independently of the message retention setting. The latest prompt is no longer used after 10 minutes; the retention timer removes expired prompt rows. Signals are read only through the owner authority path. The CTO has no signal read tool. The gateway trusts enrolled daemons to enforce their local owner checks. See [security.md](security.md#instruction-signals) for what the gateway stores and who can read it.

**Reactive charter learning.** The gateway runs rules on owner instructions, reply outcomes and deliveries between projects. An owner assignment proposes the work for the destination's `accepts` and `<destination> for <work>` for the sender's `needs`. Two matching redirects to the same destination in seven days propose that `needs` item and a `depends` edge. Three matching refusals in seven days propose a `refuses` item. Five messages between projects in seven days, when they have no edge, propose a `depends` edge. The rules do not understand every wording. Evidence includes message or signal IDs and excerpts. Related pending proposals merge, and a node must be quiet for two minutes before its proposals are ready.

Minor additions apply automatically: an owner-derived `accepts` item, free text in `rules`, or a `depends` edge. A restrictive or narrowing phrase, removal, `refuses`, `escalate`, maturity, deploy or request rules, a `deploys` edge, or any change on a critical node needs the owner. An edge touching a critical node needs the owner too. At most three changes per node apply automatically in a rolling 24 hours; further proposals become major. A rejected phrase is not offered again for that node and field. Automatic changes use the graph's normal history with actor `learned` and can be undone with `graph_undo` or the Graph card's `,`, `.` and `u` keys.

Ready major proposals appear on the Desk, with the evidence and `a` to accept or `r` to reject. These actions use the owner gate. The CTO reads pending major learned proposals and charter suggestions with `cto_proposals_find` (`kind: learned` and `kind: draft`, or `kind: all`), so it can show them to the owner and prepare the answer. Each row carries the exact `settings_change` `learned_answer` or `answer` call that decides it; the owner still decides.

Before ready owner-derived phrases are applied or shown, the gateway can batch them into one worker run for short wording. The normal automatic chooser selects a worker at trivial difficulty with no fixed model, using no worktree isolation and read-only project access. Protected providers and quotas still apply. It starts no more than one run per two minutes and six per rolling hour. A missing worker, failure, invalid JSON or a three-minute timeout leaves the owner's words unchanged. Raw phrases remain stored for review and rejection matching.

The decide rule is also in the [guidance block](#what-setup-changes-on-your-machine): judge a peer's request against the node's charter and decide (accept, reject with a reason, or redirect); escalate only when the charter says so or does not cover it; every escalation carries a recommendation.

**The relation line on a delivered request.** The gateway computes it from the graph, not from anything the sender wrote. It looks at the sender's node and the recipient's node: when the sender depends on the recipient, the line says the sender is a consumer of the recipient's node; otherwise, for two known and different nodes, it says there is no graph relation. The gateway puts the line in `recipient_relation` on the message it delivers, like `recipient_peer_trust`, and the Claude Code adapter, the Codex adapter, the Pi extension and the OpenCode plugin write it after the body and after agent-net's authority note. Only a request gets a line: a reply is information and a notification is not a request. Two sessions of the same node, or a message from the owner, get none. The line starts with `[agent-net]`, so the marker rule of [0013](decisions/0013-peer-trust-and-hints.md) applies.

**Cited message notes.** When a message body names a message ID (`m_` and 16 hex characters, as a whole word), the gateway adds one note per cited message to the delivered message, in `cited_notes`: `[agent-net] cited m_...: from <name> (session s_...) to <name> at 2026-11-09 07:31 UTC, request: "<body>"`. When the cited sender was the owner, the line says `from your user in person (<name>, session s_...)`. Only the first 3 distinct IDs of a body are looked at. A cited body is shown up to 1500 characters, then `(cut, N more characters)`. The note appears only when the sender of the new message was the sender or the recipient of the cited message; for any other ID nothing is added. The Claude Code adapter, the Codex adapter (threads and workers), the Pi extension and the OpenCode plugin write the notes after the body and after the other agent-net notes. `agent_message_get` returns them in `notes`, next to the body, never inside it. A sender cannot supply `cited_notes`: the gateway overwrites it on every delivery. Names and bodies in the note pass the marker rule, so a cited body cannot forge agent-net framing. See [decision 0121](decisions/0121-cited-message-notes.md).

**Worker briefs.** A worker's task body carries the whole charter of its project's node after the project rules for workers and before the task: `purpose`, `maturity`, `maturity_note`, `accepts`, `refuses`, `feature_requests`, `escalate` and `rules` (only the attributes that are set), then the maturity rules and consumer rule, and the `permissions:` line of the level in force. A node with no charter can still get connected-charter lines or deploy approval rules. The owner's machine reads the graph when it sends the task, so a queued start and a continued worker get the same section.

**Test scope in briefs.** When someone other than the caller owns the project's test step (the merger, a node or GitHub), every harness brief adds one line after the "do not gold-plate" and "do it yourself" lines. It tells the worker to run only the tests that cover its change, by name, and not whole packages, race runs, repeat or load runs, or full suites. The text is the rule `workers.brief_tests`, owner-scoped, and a repo can change it. The owner comes from the `tests` edge of the project, with the same pipeline the step tasks use. A caller-owned test step, or no assignment, adds no line. `delivery.tests_before_merge` does not change the line: the test step is in the pipeline either way, and that setting only decides whether merge waits for it.

### Owner rules reach every session

A rule the owner sets once at the global level (the Graph tab, key `E`, or the CTO's `charter_set` with level `global`) reaches every agent session in every harness, without editing `CLAUDE.md` or `AGENTS.md`. The charter joins `rules` and `refuses` across global, team, repo and agent by item ([decision 0100](decisions/0100-team-questions-go-to-the-lead.md)), so the effective charter line `rules: ...` of a session's node already holds the global rules, and a repo or agent with rules of its own does not hide them. Each text a model reads is that charter line, so there is no second block for rules.

One case needs text apart from the node: a session whose folder has no node (for example a folder outside any repository). It has no effective charter, so it gets the global level alone, as `rules:`, `refuses:` and `escalate:` lines under `[agent-net] Your effective charter, from agent-net (global level only: ...)`. The gateway adds it to `context.get` when the node text is empty.

| Harness | Rules reach the model | When |
|---|---|---|
| Claude Code | The `UserPromptSubmit` hook, and the MCP server instructions. | Every prompt (hook), and at start (instructions). |
| OpenCode | The `context` hook adds the text as a system part. | Every model request of a root session. |
| Pi | `before_agent_start` adds it to the system prompt. | Every turn. |
| Codex | The MCP server instructions, and for a thread that agent-net starts or resumes for the owner the `developerInstructions` of `thread/start` and `thread/resume` (checked against the Codex 0.167 app server schema: `turn/start` has no text field). Workers: the brief. | At start only. A change reaches a running thread when the owner resumes it. |
| Antigravity (`agy`) | The MCP server instructions. Workers: the brief. | At start only. |

**MCP server instructions.** `agent-net mcp` (every harness that uses it) asks the daemon once at start with the local method `charter_text` and adds the answer to its fixed instructions, which stay first and whole. The answer is the charter text of the bound session (node and agent), or only the global level when the connection has no session yet (a Codex or `agy` MCP process has none when it starts). The daemon asks the gateway with `policy.get` (without the one-time charter draft request) and `context.get`, or with `charter.global` for the global level. The server waits at most 1.5 seconds. On any failure, including an older daemon that does not know the method, the fixed instructions are used. Claude Code caps the length of MCP server text (the variable `CLAUDE_CODE_MAX_MCP_DESCRIPTION_LENGTH` sets that cap), so the total stays under 2000 characters and a long charter is cut at a line end with a note. The instructions are read once per session. A worker brief and a new Codex owner thread of a project with no node get the global level the same way (`charter.global`).

**A change shows on the next turn.** The daemon keeps the policy answer of each session and the graph of each node for 2 seconds. The gateway sends the frame `charter.changed` to every connected daemon after it commits a charter value at any level, a team write or an undo. The daemon counts these frames (its charter epoch) and uses a cached answer only from the epoch it was read in, so the next turn of every session reads the change at once. A turn costs no extra call and no timer is shortened. The 10-minute stale answer for a gateway that is gone is unchanged. An older gateway never sends the frame, so the 2-second cache applies as before.

## What setup changes on your machine

`agent-net setup` (run by the install line, by the invite line, or by hand) changes only the items below. Each step prints `ok`, `ok (unchanged)`, `updated` or `skipped (reason)`. The two service steps always restart the service, so they print `ok (restarted)` (unit file unchanged), `updated (restarted)` (unit file changed) or `ok (installed, started)` (new unit). Every step is safe to run again.

| Item | What setup does | What `agent-net uninstall` does |
|---|---|---|
| Binary `~/.local/bin/agent-net` | Put there by `install.sh` or the invite script, not by `setup` itself. | Keeps it. Delete it by hand. |
| Daemon service | Writes the systemd user unit (Linux) or launchd agent (macOS), enables it and restarts it. It copies `HOME`, standard XDG folder variables, `OPENCODE_DB`, `PI_CODING_AGENT_DIR` and `QUOTA_AXI_OPENCODE_GO_PI_AUTH` from the login shell so quota-axi reads the same local data folders. When the login shell fails, it uses the setup process environment. Upgrades preserve existing entries and add missing settings. These settings contain paths and a boolean, not provider credentials. The unit has `KillMode=process` and the plist has `AbandonProcessGroup`, so a restart stops only the daemon and keeps the workers (the next daemon adopts them). Workers also run in their own session. A daemon that starts under systemd adds `KillMode=process` to an older unit and reloads systemd. If it adds environment settings, or the running PATH differs from the stored PATH, it also queues a daemon restart. On macOS the same environment repair reloads only the daemon LaunchAgent with `bootout` and `bootstrap`; a kickstart alone would keep the loaded job's old environment. The reload helper runs outside the daemon process group so it can finish after bootout. | Stops it and deletes the unit or plist. Then stops every worker that is still running (the `workers` step), because nothing else would. |
| Gateway service (only with `--gateway`) | Same, listening on `0.0.0.0:7420`. | Stops it and deletes the unit or plist. |
| Gateway binaries (only with `--gateway`) | Copies this binary into `~/.local/share/agent-net/dist`. | Keeps them. |
| Gateway data | Created by the gateway on first start. | Keeps it, even with `--purge` (it prints a reminder). Delete the directory by hand to start over. |
| Join | Writes `~/.config/agent-net/node.json` and `ca.pem`. Skipped if this machine already joined the same gateway. | Keeps them. `--purge` deletes `~/.config/agent-net`. |
| Daemon state | Created by the daemon. | Keeps it. `--purge` deletes `~/.local/state/agent-net/daemon`. |
| Claude Code | If `claude` is found, runs `claude mcp add -s user agent-net -- <binary> mcp`. This adds an entry to your user-level MCP servers in `~/.claude.json`. Skipped if the entry exists. | Runs `claude mcp remove -s user agent-net`. |
| Claude Code mouse setting | If `claude` is found, writes `CLAUDE_CODE_DISABLE_MOUSE=0` to the `env` of `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`), keeping every other key (file rewritten with 2-space indent, through a temp file and a rename, mode kept). A file that is not valid JSON is not touched and the step says so. The daemon keeps the same value there and puts it back if it drifts. | Removes `env.CLAUDE_CODE_DISABLE_MOUSE`, only while its value is still `"0"`. Other keys stay. |
| Claude Code auto-update setting | If `claude` is found, writes `DISABLE_AUTOUPDATER=1` to the `env` of `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`) with the same merge, drift and invalid-file rules as the mouse setting, so Claude Code's own updater is off ([decision 0133](decisions/0133-agent-net-is-the-only-harness-updater.md)). Every Claude Code process agent-net starts gets the same variable in its environment. | Removes `env.DISABLE_AUTOUPDATER`, only while its value is still `"1"`. Other keys stay. |
| Claude Code peer messages | If `claude` is found and `crossSessionInbound` is not set in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`), follows the network answer or `--accept-peer-messages` / `--no-accept-peer-messages`. On yes it adds `"crossSessionInbound": "accept"` and keeps every other key (file rewritten with 2-space indent, through a temp file and a rename). Records the answer in `~/.config/agent-net/claude-peer-messages.json`. An existing value is never changed. A file that is not valid JSON is not touched. The daemon and upgrades never change it. | Removes the key, only if agent-net set it and the value is still `"accept"`. Deletes the record. |
| Claude Code hint hook | If `claude` is found, follows the network answer or `--claude-hint-hook` / `--no-claude-hint-hook`. On yes it adds five entries to `hooks` in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`): `PostToolUseFailure` (all tools), `PostToolUse` with matcher `Bash`, and three `PreToolUse` entries with matcher `Bash` and `if` conditions `Bash(gh pr merge*)`, `Bash(gh pr close*)` and `Bash(gh workflow run*)`, each running `<binary> hint` with a timeout of 2 seconds. Before a merge, close or workflow dispatch in a team that sends blocked steps to its lead, the hint adds one line naming the lead (see [peer trust and hints](#peer-trust-and-hints)). Every other key and every other hook is kept. A later setup run updates the command if the binary moved. Records the answer in `~/.config/agent-net/claude-hint-hook.json`. A file that is not valid JSON is not touched. | Removes our five entries (and an empty `hooks` object), only if agent-net added them. Deletes the record. |
| Claude Code policy hook | If `claude` is found, follows the network answer or `--claude-policy-hook` / `--no-claude-policy-hook`. On yes it adds three entries to `hooks` in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`), each running `<binary> policy-hook` with a timeout of 3 seconds: `UserPromptSubmit` (all prompts), `PostCompact` (after a compaction) and `SessionStart` with matcher `compact` (the same moment on versions that report it there). Every other key and hook is kept. A later setup run updates the command if the binary moved. The daemon does the same at start when the answer is on and agent-net added the hooks, so an upgrade that adds an event reaches the machine; it writes only when the hooks differ. Records the answer in `~/.config/agent-net/claude-policy-hook.json`; the daemon reads it to tell the gateway the hook is installed. When you set a `native` [delegation policy](#delegation-policy) for a Claude Code session, the daemon also writes `~/.claude/agents/agent-net-coder-<model>.md` (a marker comment inside; a file with that name that is not ours is left alone). | Removes our entry (and an empty `hooks` object), only if agent-net added it, deletes the record, and deletes every `agent-net-coder-*.md` file in `~/.claude/agents/` that has our marker. |
| Claude Code needs hooks | If `claude` is found, follows the network answer or `--claude-needs-hook` / `--no-claude-needs-hook`. On yes it adds six entries to `hooks` in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`), each running `<binary> needs-hook` with a timeout of 2 seconds: `PermissionRequest` (all tools), `Notification` with matcher `agent_needs_input|elicitation_dialog|elicitation_url_dialog`, `PreToolUse` (all tools), `UserPromptSubmit`, and `PostToolUse` and `PostToolUseFailure` with matcher `AskUserQuestion|ExitPlanMode`. It does not match `idle_prompt`, which Claude Code sends after any turn ends and does not mean the owner is needed; `needs-hook` ignores it, so an install with the older matcher keeps working without a setup rerun. Every other key and hook is kept. A later setup run updates the command if the binary moved and the matcher if it changed. The daemon does the same at start when the answer is on and agent-net added the hooks, so an upgrade that adds an event reaches the machine; it writes only when the hooks differ, and never for a binary whose name does not start with `agent-net`. Records the answer in `~/.config/agent-net/claude-needs-hook.json`. A file that is not valid JSON is not touched. | Removes our entries (and an empty `hooks` object), only if agent-net added them. Deletes the record. |
| Claude Code workers plugin | If `claude` is found, follows the network answer or `--claude-workers-mod` / `--no-claude-workers-mod`. On yes it writes a local marketplace and the plugin in `~/.local/share/agent-net/claude-plugins/` from the files in this binary, then runs `claude plugin marketplace add <folder>` and `claude plugin install agent-net-workers@agent-net -s user`. Claude Code reads the plugin from that folder. The plugin records the absolute path of the running binary and falls back to `~/.local/bin/agent-net` and then `agent-net` on PATH. The written `plugin.json` keeps the embedded `0.1.0` base version and adds a short SHA-256 hash of the other plugin files, so their changes update the version Claude Code sees. A later setup run, or the daemon at start, rewrites the files if they differ from the binary, so the plugin matches the version that is installed. When an installed plugin changes, setup or the daemon also updates the Claude Code marketplace and plugin cache. Open Claude Code sessions need `/reload-plugins` to load the update. Refresh failures are reported and do not stop the upgrade. If the `claude` CLI fails, the step says so and the rest of setup continues. Records the answer in `~/.config/agent-net/claude-workers-mod.json`. | Runs `claude plugin uninstall agent-net-workers@agent-net -s user` and `claude plugin marketplace remove agent-net`, deletes the folder and the record. |
| Quota-axi private copy | If the Quota integration is on and no usable owner or private copy is found, installs the pinned `quota-axi` with npm in `~/.local/share/agent-net/quota-axi/`. Needs Node 22.19 or newer and npm. A failed install is retried at most once a day. The daemon does the same in the background when it applies a changed network answer. | Removes `~/.local/share/agent-net/quota-axi`. Turning the item off does the same. |
| Codex MCP server | If `codex` is found or `~/.codex` (`$CODEX_HOME`) exists, adds a block between `# agent-net:begin` and `# agent-net:end` at the end of `config.toml` in it: the server `agent-net` with `args = ["mcp", "--codex"]` and `env = { AGENT_NET_SOCKET = "<daemon socket>" }`. Codex starts an MCP server with only a few variables of its own (HOME, PATH, USER and similar), so the server gets the daemon socket from this line. A block from an older setup, without the `env` line, is updated by the next `agent-net setup`. It follows the network answer or `--accept-codex-tools` / `--no-accept-codex-tools`; yes adds `default_tools_approval_mode = "approve"` to that block (for this server only), no leaves it out. The answer is kept in `~/.config/agent-net/codex-tools.json`. A `[mcp_servers.agent-net]` that is not ours is left alone. Codex threads started before this do not have the tools. | Removes the block and the record; the rest of `config.toml` is kept. |
| OpenCode plugin | If `~/.config/opencode` (or `$XDG_CONFIG_HOME/opencode`) exists, writes `plugins/agent-net.ts` and its generated `plugins/tools.gen.ts` catalog in it. A symlink to a file agent-net did not write is left alone. A symlink to an older agent-net copy is replaced with a real file holding this version; the old target is kept. | Deletes both files. A symlink to a file agent-net did not write is left alone. A symlink to an agent-net copy is removed, and its target is kept. |
| Antigravity plugin | If `~/.gemini` exists, writes `~/.gemini/config/plugins/agent-net/` with its manifest, rules, MCP configuration and hooks. Foreign plugins and symlinks are left alone. | Removes unchanged plugin files written by agent-net, leaving foreign files and symlinks. |
| Pi extension | If `~/.pi` exists, writes `~/.pi/agent/extensions/agent-net/index.ts` and its generated `tools.gen.ts` catalog beside it. A symlink to a file agent-net did not write is left alone. A symlink to an older agent-net copy is replaced with a real file holding this version; the old target is kept. | Deletes the plugin and generated catalog in both the folder and old flat layouts. Setup also removes owned files from the old flat layout. After deleting its files, uninstall removes the `agent-net` folder only if it is empty. A foreign file keeps the folder, and uninstall reports that it was kept. A symlink to a file agent-net did not write is left alone. A symlink to an agent-net copy is removed, and its target is kept. |
| Connected apps Tailscale node | `--apps-tailscale-name` writes `tailscale-node.json` in the gateway data folder. When the owner turns internet access on, the gateway writes the marker `tailscale/agent-net-owned.json` first, then starts the node with its state in `tailscale/`. The node appears as a machine with that name in the tailnet. | When the marker is there, starts the node, logs it out (this removes it from the tailnet) and deletes `tailscale/`. A folder without the marker is left alone. A node that never logged in is just deleted. If the logout fails, reports it and keeps the folder for retry. Deletes `tailscale-node.json`. The gateway does the same cleanup at start after `--apps-tailscale-machine`. |
| Connected apps Funnel | When enabled, the gateway records the Funnel port and its loopback proxy target in `funnel.json` in the gateway data directory. | Reads Tailscale status and removes the route only when that port still has exactly the recorded target. Leaves a repurposed or shared port alone. Reports a cleanup failure and keeps the record for retry. |
| Skill and MCP management state | Created by the daemon when the owner applies a tool assignment or removes a native MCP entry. Setup writes none of it. Files and folders are listed in [Files](#files). | Deletes `skill-manifest.json`, `skill-backups`, `mcp-backups`, `skill-journal` and leftover temporary files in the daemon state folder, and releases the Git exclude lines the manifest owns. It keeps installed skill copies and every native MCP config file (including any edit the daemon made): restore those by hand or with `tools_undo` before you uninstall. Backups are deleted because they can hold secrets. The gateway side (`skills.git`, the inventory and audit tables in `gateway.db`) is kept with the gateway data. |
| Guidance text | Adds a block between `<!-- agent-net:begin -->` and `<!-- agent-net:end -->` to `~/.claude/CLAUDE.md` (if `~/.claude` exists) `~/.pi/agent/AGENTS.md` (if `~/.pi` exists) `~/.config/opencode/AGENTS.md` (if `~/.config/opencode` exists) and `~/.codex/AGENTS.md` (if the Codex home exists). Guidance symlinks are followed, including links into a dotfiles repository. When several paths resolve to the same file, it is updated once; if they use different reply instructions, one block includes instructions for each harness. Text outside the markers is never changed. An older unmarked `## agent-net: working with other agents` section is replaced by the block. | Removes the block. |

The Pi extension, the OpenCode and Antigravity plugins, the Claude workers plugin folder and the guidance blocks are also written by the daemon, on every start, with the same rules as setup (the harness folder must exist, plugin symlinks to foreign files are left alone, guidance follows symlinks and changes only between the markers). The Claude workers folder is refreshed only when setup already wrote it, so the daemon never installs the plugin on its own. It rewrites a file only if it differs from the content embedded in the binary, and logs one line per file it changes (`agent files: pi extension updated`) in the daemon log. A symlink that points at an older agent-net plugin is replaced with a real file, so a stale link cannot keep an outdated extension loaded; the old target is kept and named in the log line. So these files follow the binary after an upgrade or a manual copy, without a setup run. See [upgrade.md](upgrade.md#agent-files-on-a-machine).

Setup does not use `sudo`. On Linux it may print a hint to run `sudo loginctl enable-linger <user>`, which keeps the user services running when you are logged out. You run that yourself.

Setup does not change any other Claude Code or Pi setting (the one exception is the peer-message setting above, and only with your yes), and it does not touch other MCP servers.

## Owner inbox and written approvals

Agents use `agent_owner_ask` with `kind: "approval"`, the exact action in `text`, and a `recommendation`; or `kind: "idea"` with the idea in `text` and a `recommendation` (and optional `replaces` to replace or close an earlier pending ask). The daemon binds each request to the calling session; the gateway derives its project from that session. Codex workers put the exact action and their recommendation in their report when they need approval.

Press `7` in the TUI to open the Inbox. Items are grouped by kind, newest first inside each kind. Select an item and press Enter to see its context. Press `a` to approve, `r` to refuse, `t` to enter a one-line answer, or `x` to dismiss an approval, a question or a notice. A dismissal closes the item without sending an answer, so an old item whose session is gone can still be cleared. An item the gateway no longer has leaves the list with the note `already gone`. The tab label counts waiting items, and the Sessions list says how many wait. Answers are stored together with a message queued for the requesting session. An approval covers only the exact action named in the item.

The owner-only local methods are `inbox_list` and `inbox_answer` with `id` and `answer` (`approve`, `refuse`, `dismiss`, or text). The daemon applies the same operator gate as other owner-only methods. Rejections and escalations of agent requests are also recorded in this inbox, with the request, reason, recommendation and conversation. Answering an item in the Inbox closes it there and in the other waiting rows. Existing outcomes are added to the inbox when the gateway upgrades.

For a node with `ask` merge authority, request approval for the exact merge using `agent_owner_ask` with `kind: "approval"`. A deployer follows the target node's deploy rule: for a critical target, request written approval for the exact deploy using the same tool. Codex workers include the action and recommendation in their report.
### Request outcomes

`agent_message_reply` accepts optional `outcome`: `accept`, `reject`, `redirect` or `escalate`. Reject, redirect and escalate need `reason`; redirect also needs `redirect_to` (a node name), and escalate needs `recommendation`. Each text field is limited to 2048 bytes, and the node name to the project name limit. Control characters are removed. Outcomes apply to replies to requests. The gateway records the request's source and target project names, request and reply IDs, outcome, reason, redirect, recommendation and time. Projects unknown to the sessions appear as empty names.

A request that needs no answer is closed with `ack: true` instead. The body may be empty, and `ack` cannot be combined with `outcome`. The gateway stores an `ack` message from the recipient and marks the request `acknowledged`, so `agent_message_status` and `agent_message_get` show the acknowledgement. Nothing is delivered to the sender and it is not woken. An acknowledged request counts as answered everywhere: no reminder follows, and it is no longer owed in the Sessions tab or in the idle check.

A rejection or escalation of another agent's request appears as a separate Inbox entry until the owner resolves it. The entry shows the request's first line, outcome, reason and recommendation. Press `s` to let the decision stand or `O` to overrule it. Letting it stand sends the receiving session a notice that its decision stands. Overrule sends the original request again from the owner with the agent-net note `the owner overrules: do it.` The operator method is `outcome_overrule` with `reply_id` and `action` (`let_stand` or `overrule`).

The daemon marks its private Claude CTO folder and Claude worker worktrees trusted in `~/.claude.json` (or `$CLAUDE_CONFIG_DIR/.claude.json`). It removes the worktree trust setting when a known Claude worker worktree is deleted. Uninstall removes the CTO folder's trust setting and preserves other project settings.

`session_enter` with the CTO session ID starts the local CTO runtime if needed and returns an enter plan for its full harness UI. In the CTO box, press Enter to open it in herdr. If herdr is unavailable, ctrl+] returns from the terminal fallback. Claude starts a background session on first entry if needed. Entry errors appear in the TUI. The Claude CTO requires Claude Code with `--bg` and `--strict-mcp-config`; update Claude Code if either flag is missing. The daemon waits for the background job to run before saving its session ID. If Claude Code has closed the conversation and a resume fails, or the resumed job crashes at startup, the daemon starts a fresh Claude conversation once. The CTO timeline and proposals remain in agent-net. The CTO box reports the restart or any startup error.

The daemon service records the login shell's PATH in its environment, so tools
installed outside the system directories are available to workers. If the shell
cannot supply it, setup adds common Homebrew and local binary directories to the
current PATH. A daemon upgrade adds PATH to older service files.

### CTO session stop, resume and restart

The owner can ask the CTO to stop, resume or restart an owner or agent session, as the owner does in the TUI. The CTO calls `session_change` with `action` `stop`, `resume` or `restart` and a `session_id`. The session may be on any machine: the request goes through the gateway like a session start.

- `stop` is the TUI stop, unchanged: the same guarded path as the operator method `session_stop`, which signals only the registered harness process. The conversation is kept. The Inbox notice has an undo, which is `session_resume` for the same session.
- `resume` reopens the same conversation in its recorded folder with the same harness, as `session_resume` does when the owner presses Enter on an offline session. It runs in the background and never opens a view, so the herdr rules of decision 0046 hold. The owner opens it with Enter. The CTO waits for the background launch to finish and reports a failure. Pi cannot reopen an old session, so `resume` and `restart` refuse a Pi session.
- `restart` stops the session, waits up to 30 seconds until the gateway reports it ended, then resumes it. A fresh Claude Code process reads `~/.claude.json` again, which brings back user MCP servers such as agent-net. A session that is already offline is only resumed. A Pi session is refused before anything is stopped.

A restart, and the owner's resume with Enter, keeps the session ID. A session ID is what its workers, open delivery changes, step claims and queued messages name, so a new ID would leave them behind. The gateway and the daemon keep it in two ways:

- **The same conversation under a new harness handle.** Claude Code can resume a background conversation as a copy with a new native ID. The daemon tells the gateway the new handle (`session.resume_rebind`) while its session discovery is still held, so the record keeps its ID and only its native ID changes. Codex and OpenCode keep the conversation's own ID, so nothing moves.
- **A new conversation.** When the harness cannot open the old one, a new conversation is a new session. The daemon then calls `session.replace` and the gateway moves, in one transaction: the caller role of the running and queued workers (`sessions.worker`, `worker_starts`, `worker_runs`), the caller of open delivery changes and the steps they claimed (`delivery_changes`, `delivery_step_tasks`, `delivery_step_messages`), undelivered messages and open requests addressed to the old session, its open Inbox items, its agent and repo binding, the repo and team lead places it held, and its name. The daemon moves the caller of its own worker records and its queue as well, because a hand-back and a step task name the caller from there.

The old record is marked replaced (`sessions.replaced_by`), not renamed to `<name> 2`: it keeps the conversation's name, offline, and the TUI shows `replaced by <new name>` (the STATUS cell says `replaced`). A later discovery report of the old conversation does not rename it. Pi cannot reopen an old session, so `resume` and `restart` refuse a Pi session and no move applies.

A restart that already orphaned its work is repaired with the same move. The operator method `session_replace` takes `{"old_session": "s_...", "new_session": "s_..."}` and is served only to the owner's command line or TUI, like `session_rename`; the CTO calls `session_change` with action `replace` and the same two IDs. It calls the same `ReplaceSession` move, uses the old session's own machine so the owner may ask from any machine, and refuses a worker or a new session on another machine. A successful move writes one Desk line naming who moved the work and the two sessions. It has no undo: the old conversation is gone and the new one holds the work. A move that finds the old session already replaced, or still working, changes nothing.

All three refuse a worker (use `worker_change` with `stop`) and the CTO's own session. They are immediate changes with an Inbox notice and an audit line (`session_stop`, `session_resume` or `session_restart`). Only `stop` has an undo. `resume` and `restart` have none, because the session is already running again.

### CTO folder trust

The owner can ask the CTO to trust a folder for Claude Code. The CTO calls `session_change` with action `trust` and one of `project` (a project registered on that machine) or `path` (an existing folder under the owner's home), plus `node_id` when the folder is on another machine. It writes `hasTrustDialogAccepted` for that folder in that machine's `~/.claude.json` (or `$CLAUDE_CONFIG_DIR/.claude.json`), with the same helper the TUI's `needs_trust` answer uses. `session_change` with action `session_start` and `trust: true` trusts the folder and starts in one step; a path that does not exist yet is created and trusted by the start itself, as before.

The request travels to another machine as the owner frames `session.trust_folder` (to the gateway) and `session.owner_trust_folder` (to the machine), like a session start. The machine checks the owner flag again. The operator method is `folder_trust` (`project` or `path`, `node_id`, `remove`); the result has `path`, `trusted` and `added`.

Each use leaves an Inbox notice. `added` is true only when the folder was not trusted before. Only then does the notice carry an undo, and the undo removes that one folder's trust (`folder_trust` with `remove: true`). A folder the owner had already trusted is left alone and has no undo. A CTO proposal still cannot set `trust_claude`, and the proposal relay still drops it: trust the CTO sets goes only through this explicit action or `trust: true`.

### CTO process stop

The owner can ask the CTO to stop orphan processes on a machine, for example an MCP server that a restart left behind. The CTO calls `settings_change` with `area: process_stop`, `node_id` and `pids`. It acts at once, like folder trust. It is not a proposal. The CTO decides when to use it. `node_id` is required and never means "this machine": name the machine that runs the processes (its node ID or host name). An empty `pids` list is an error. A repeated pid is stopped once.

The machine does this for each pid, in this order:

1. It refuses a pid that must not be stopped: a number below 1, pid 1, the agent-net daemon itself, a process whose command line is `agent-net daemon` or `agent-net gateway`, a live agent-net session or worker that this daemon knows (the process ID it registered), and any process that does not belong to the daemon's own user. The reason is in the result.
2. It reads the command line, the parent pid and the start time of each pid, before any signal. They are in the result for a refused pid too, when the machine can read them. The command line is cut at 400 characters.
3. It sends SIGTERM to every allowed pid, waits up to 5 seconds, then sends SIGKILL to any that is still the same process and still alive. Before SIGKILL it checks the process start time again, so a reused process number is never killed. It waits up to 2 more seconds for the process to end.

Each pid ends in one `outcome`: `refused` (with `reason`), `stopped_term`, `stopped_kill`, `not_found` (no such process, or a zombie that only its parent can remove) or `failed` (the signal failed, or the process was still there after SIGKILL; `reason` says why). The result is `{"node_id", "hostname", "processes": [{"pid", "outcome", "reason", "command", "parent_pid", "started"}]}`. `started` is RFC 3339 in UTC. The processes come back in the order asked.

The request travels to another machine as the owner frames `session.stop_process` (to the gateway) and `session.owner_stop_process` (to the machine), like a folder trust. The machine checks the owner flag again and applies its own refusals. The operator method is `process_stop` (`node_id`, `pids`). It is served only to the owner's command line or TUI, and to the CTO. It is refused for an agent client. No MCP, Pi or OpenCode tool for ordinary agents calls it.

Each use leaves an Inbox notice with a line per pid: outcome, command line, parent pid and start time. The notice says there is no undo, because a stopped process cannot be brought back. The daemon log has one `process.stop` line per pid with the same facts. The gateway keeps an event `session.process_stop` with the pids and outcomes, not the command lines. See [decision 0102](decisions/0102-cto-process-stop.md).

### Claude MCP server approval

Claude Code asks the user to approve each MCP server it finds in a `.mcp.json`
file before it uses it. A background session (`claude --bg`) cannot answer
that prompt. The job shows state `blocked` with the detail `1 new MCP server
needs approval` (`N new MCP servers need approval` for more), never writes a
session file, and waits forever.

**Where Claude Code 2.1.319 stores the answer.** In the launch folder's
`.claude/settings.local.json`, as two lists of server names:
`enabledMcpjsonServers` (approved) and `disabledMcpjsonServers` (refused). A
refusal wins over an approval for the same name. Claude Code reads `.mcp.json`
from the launch folder and from every folder above it, and a name in a nearer
file wins. A choice in a parent folder does not cover a session that starts in a
child folder, so the choice is written in the folder the session starts in.
The older `projects[<path>].enabledMcpjsonServers` lists in `~/.claude.json` are
legacy: version 2.1.319 clears them and does not read them for this. agent-net
never sets `enableAllProjectMcpServers`.

**What agent-net does.**

- While it waits for the session file of a Claude start, it reads the job's
  `state.json`. When the state is `blocked` with an MCP approval detail, it stops
  waiting at once. The start fails with the reason code `mcp_approval_needed`.
  The text names the folder, the servers with the command each runs, the
  `.mcp.json` files that define them, the Claude job ID and whether that job is
  still running. The job is left running, because it holds the prompt. In the
  start record (`session_start_get`) the failure has `error_code`
  (`mcp_approval_needed`), `job_id`, `job_alive` and `mcp_servers` (`name`,
  `command`, `file`).
- Every failure of a start the CTO asked for reaches the CTO. The start call
  answers `starting` long before it can fail, so the machine that runs the start
  sends the CTO a notification from the owner session: `[agent-net] Session start
  failed: <project> on <machine> (start <id>). Reason code: <code>. <error>`. The
  owner's TUI still shows the same notice, and the log and gateway event are
  unchanged. For a start on another machine, the owner's machine sends the
  notification when it sees the failed record.
- `session_start` takes `mcp_servers: approve` or `disable` (Claude only; other
  harnesses refuse it). The machine writes that choice for every server in the
  `.mcp.json` files above the session folder, into the session folder's
  `.claude/settings.local.json`, before it launches Claude Code. For a new
  worktree this is the worktree folder. The start result has `mcp_choice`.
- `session_change` with action `mcp_servers` does the same without starting a
  session: one of `project` (registered on that machine) or `path` (an existing
  folder under the owner's home), optional `node_id`, and `mcp_servers`.
  A folder with no `.mcp.json` server is refused. Approve also takes the name out
  of `disabledMcpjsonServers`, because a refusal would win; disable leaves
  `enabledMcpjsonServers` alone, because a refusal already wins. Other keys of
  the file are written back unchanged.

- **A worktree copies the choice of the main checkout.** A worktree is a new
  folder for Claude Code, so the answer the owner gave for the project's main
  checkout does not apply to it, and a Claude worker would wait on the prompt
  (decision 0101). Before it launches Claude in a worktree, the machine reads
  `.claude/settings.local.json` of the main checkout folder and of every folder
  above it, and writes the result into the worktree's own
  `.claude/settings.local.json`. This holds for a Claude worker and for a Claude
  session started with `folder: worktree`, the merger's included. The lists are
  joined: a name in `disabledMcpjsonServers` is copied as a refusal (a refusal
  wins), a name in `enabledMcpjsonServers` as an approval. The nearest file that
  has `enableAllProjectMcpServers` decides that switch; when it is true, every
  server the worktree's `.mcp.json` files define, and that is not refused,
  is written as an approval by name. The switch itself is never copied, so a
  server added to `.mcp.json` later still asks. Only names of servers that the
  worktree's `.mcp.json` files define are written, a name that the worktree file
  already has is left alone, and no other key is copied. A server with no answer
  is left without one: the worker starts anyway, and if Claude Code blocks on
  it, the start fails at once with `mcp_approval_needed` (see above), not after
  the start deadline. A worker's blocked job is stopped, and the text says to
  retry `worker_start` with `mcp_servers`, or to run `session_change` action
  `mcp_servers` once for the project so that every later worker copies it. A
  settings file that git tracks in the repository is left as it is.
- **The copied file stays out of git.** The worktree's `.claude/settings.local.json`
  must not show up in `git status`, because a worker ends with a commit of
  its changes. When `git check-ignore` says the file is already ignored (the
  repository's `.gitignore`, or the user's global ignore file, which Claude Code
  fills) nothing is written. Otherwise the line `/.claude/settings.local.json` is
  appended to `info/exclude` of the repository. Git has one `info/exclude` for the
  main checkout and all its worktrees, so the line also covers the main checkout.
  The repository's `.gitignore` is never changed.
- `worker_start` (`agent_worker_start`, the CTO's `worker_change` action `start`)
  takes `mcp_servers: approve` or `disable`, with the same meaning as for
  `session_start`. It applies to the worker's folder after the copy, so
  `approve` also lifts a refusal that was copied. It is for a Claude worker; an
  explicit non-Claude `harness` refuses it, and with automatic harness choice it
  is ignored for another harness. It cannot be combined with `continue`: a
  continued worker copies the choice of the main checkout. A queued start keeps
  it (`mcp_servers` column of `worker_queue`). With `isolation: none` the
  worker's folder is the project folder, and the choice is recorded there.

The request to another machine travels as the owner frames `session.mcp_servers`
(to the gateway) and `session.owner_mcp_servers` (to the machine). They are not
the folder trust frames, so an older gateway or machine refuses with `unknown
frame type` and a choice is never run as a plain trust. The operator method is
`folder_mcp` (`project` or `path`, `node_id`, `mcp_servers` or `undo`); the result
has `path`, `settings_file`, `servers` (`name`, `command`, `file`), `added` and
`restored`.

Each use leaves an Inbox notice that names the servers, the command each runs,
the `.mcp.json` file and the list written. `added` holds only the names that were
not in the list before, and `restored` the names an approval took out of the
refusal list. The notice carries an undo (`folder_mcp` with `undo`) only when
something changed. The undo removes exactly the `added` names from the list and
puts the `restored` names back in the refusal list; a choice the owner made
before stays. A start with `mcp_servers` leaves the same notice and undo.

### CTO actions for a session or project

The CTO can run `session_change` with action `session_start`, using `project` or `path`, `node_id`, `harness`, `model`, `folder`, `branch`, `first_message` and optional `create`. It can run `project_change` with action `create` and a `path` plus the same start options. Project creation runs on the owner's instructions and is recorded in the Inbox. Creating a project authorizes the missing folder and a git repository. A session start creates a missing folder only when `create` is true.

The CTO recipe uses the separately supplied `projects_layout` scan to choose a machine, root and group. It reads owner placement answers from `cto_placement_memory` and saves corrections or group answers there for later. Without an owner choice or learned default, the recipe chooses Claude and `claude-opus-5-5`. The CTO confirms the proposed placement in one line before the owner answers. Once the session is registered, the CTO can report that it is ready in Sessions, tab 1.

The CTO can run `project_change` with action `add`, using `paths` or `dir` with `scan: true`, and optional `node_id` (machine host name or node ID). The selected machine's daemon checks and registers the folders immediately. A scan can use `depth` and `dry_run`. Omit `node_id` to register on the CTO's machine. Add and remove requests for another machine are relayed through the gateway to that machine's daemon, as with create. The target must be online. Paths beginning with `~` are expanded there.

The CTO can run `project_change` with action `remove`, a project `name` or `path`, and optional `node_id` (machine host name or node ID). Use `node_id`, as with create, to remove a registration on another machine. Omit it to remove from the CTO's machine. Only the registration is removed; the project folder stays in place.

An optional `remote` on `project_change` create clones a repository instead of running `git init`. It accepts only `git@host:owner/repo.git` and `https://host/owner/repo.git`; local paths, `file://`, HTTP and credentials in HTTPS URLs are refused. The destination must not exist. The confirmation names the remote. The clone has a 45 second timeout and disables Git terminal credential prompts. A failed clone can leave its destination folder behind; remove it before retrying. The cloned repository is registered and started on the owner's instructions, subject to the session-start trust checks. As with other registered repositories, its project identity follows the origin repository name.

The daemon records the target machine and checks local paths before accepting a proposal. A remote machine checks its own path when the approved start reaches it; an offline or older machine refuses the request. Approved Claude starts run in the background without opening a view on either machine. Select the session in Sessions and press Enter to open it. TUI new-session starts and CTO starts of other harnesses keep their existing behavior. If Claude needs the owner's trust answer, the proposal result and note say so and no session starts. Answer through the normal new-session form. A missing folder without `create` likewise needs the owner's answer.

### Starting an owner session through the local API

`session_start` starts a Claude, OpenCode, Pi or Codex session in an existing project folder and opens it in herdr when available. The folder may be on another machine.
This is the daemon part of the [accepted session-start design](design/start-sessions.md).

The request is `{"project":"shop","harness":"claude","model":"haiku","first_message":"Inspect this project","node_id":"n_..."}`.
Project is a known project name or its exact path. Ambiguous names are
refused; use the path. `node_id` is optional: when it names another machine,
the local daemon asks the gateway to relay the start to that machine. Without
it, a project that exists only on another machine is relayed there; a project
on more than one machine needs the machine name. The gateway finds the machine
that holds the project and asks its daemon; the gateway never runs anything.
The receiving daemon checks that the owner flag names the owner session of the
sending machine, so an agent session cannot relay a start. A machine that is
offline, or that runs an agent-net older than v0.4.46, is refused with a line
that names it.

Harness may be omitted and defaults to `claude`. Model
and first_message are optional. An empty model keeps the harness's own default.
For Claude, OpenCode and Pi, an empty first message starts an idle background
session. For Claude, a nonempty message is the owner's first prompt, passed
after `--` to `claude --bg`. `folder` is the folder choice of the TUI form; it
is carried unchanged to the machine that runs the start.

`path` starts in a typed folder instead of a project name. The request is
`{"path":"~/code/garden-planner","harness":"claude","create":true}`. The path
must be absolute or start with `~`, must hold no control characters, and must
stay inside the home folder of the user of the machine that runs the start,
including through symbolic links. The machine cleans it with `filepath.Clean`
and refuses it otherwise. When `path` is set, `project` is empty.

A missing folder makes the start answer `status: "needs_create"` with the
checked `path`; nothing is created. The same request with `"create": true`
makes the folder with `os.MkdirAll` and runs `git init` in it, then registers
it and starts there. It never runs a shell: `git` gets plain arguments. An
existing folder that is a git working tree is registered before the start, and
the session runs in its main tree. A folder that is not a git repository starts
where it is and is not registered; the form says `not a git repository: no
worktrees, no branch shown`. A start on another machine carries `path` and
`create` through the gateway, and the receiving machine applies the same path,
home, create and trust checks.

For Claude, a start whose folder already exists and is not trusted answers
`status: "needs_trust"` with the checked `path` and starts nothing. This holds
for a typed path and for a known project name alike. The same request with
`"trust_claude": true` records the folder in `~/.claude.json` (or
`$CLAUDE_CONFIG_DIR/.claude.json`) and starts. A folder the start created on
the owner's command is trusted at once.

For OpenCode, the daemon reads the owner's running service address and password
from `$XDG_STATE_HOME/opencode/service.json` (default:
`~/.local/state/opencode/service.json`). It creates the session on that service
with the project folder in `x-opencode-directory`. A model uses
`provider/model[:level]`. A first message is queued as an owner prompt through
OpenCode 2.2's `/api/session/{id}/prompt` API. After registration, agent-net opens `opencode --session <id>` in herdr. Without herdr, an empty first message enters through the terminal fallback. The service keeps the
conversation, including offline and dormant rows; Enter keeps its existing
plan for those rows. The daemon never starts the owner's service. If its record
is missing or its address cannot be reached, the start is refused with
`the OpenCode service is not running on <machine>`. HTTP refusals report only
the status code. The service password stays in HTTP authentication; it is never
logged or put in a command argument or notice.

For Pi, agent-net opens `pi --session-id <uuid> --name <project> [--model M] --tui-mode regular` in herdr. A resumed Pi conversation uses `pi --session <id> --tui-mode regular`. Regular mode leaves the mouse to the terminal, so text selection and copy-paste work; Pi's default fullscreen mode captures the mouse. A first message is sent as an ordinary owner message after Pi registers. When herdr is unavailable, Pi start and resume fail with the folders searched.

For Codex, agent-net opens `codex [-m MODEL] [prompt]` in herdr in the project folder. When herdr is unavailable, the answer contains an `enter_plan` and the TUI runs it in its own terminal. A remote start follows the same herdr or ssh rules as Enter. A locally approved CTO start fails if no herdr pane opens and there is no approving TUI to receive the plan. When the TUI receives a plan, the proposal says `handed_to_tui` until the TUI reports whether the process started using `proposal_start_ack`; after 60 seconds without confirmation it fails. A daemon restart also fails any handoff still awaiting confirmation. Remote CTO Codex fallback plans fail because the confirmation cannot reach the proposal on the other node.

For Claude, OpenCode and Pi, the answer is a started-session record with `id`, `status: "starting"`,
`node_id`, `hostname`, `harness`, `project`, `path`, `branch`, `model` and `started_at`
(Unix seconds). Poll `session_start_get {"id":"<returned id>"}`. Once normal
discovery and gateway registration finish, the record has `status: "started"`,
`native_id` and `session_id` (and `job_id` for Claude). A failed launch or
registration has `status: "failed"` and `error`. A failure after the answer
`starting` is never silent: the machine that runs the start writes one log
line `event=session.start_failed`, records a `session.start_failed` event in the
gateway feed, and the owner's TUI shows the error. A start the CTO asked for also
sends the CTO a notification with the same error (see [Claude MCP server
approval](#claude-mcp-server-approval)). A failed record can carry `error_code`. A start can first answer
`needs_create` or `needs_trust`; those are not polled. An unknown id is refused. Records live in
memory for this daemon run. A daemon restart loses the records; the harness's
session remains discoverable as a normal session. Cancelling the requesting
socket does not cancel the background launch. For a start on another machine, `session_start_get` is relayed to that machine too. A completed herdr opening is returned in `enter_plan` with `opened_herdr: true`; the TUI shows its note without running another command.

`session_resume {"session_id":"<agent-net session id>"}` resumes an offline
Claude or Pi conversation in its original folder, on this machine or on the
machine that holds it. It returns the same starting record and uses
`session_start_get` for completion. If Claude creates a copy, the record
reports the new native and agent-net session IDs. A missing folder is reported.
The TUI resumes and enters an offline Claude or Pi row on Enter or `e`; after
more than an hour offline it asks for confirmation first. Offline OpenCode and
Codex rows keep their existing enter plans, wrapped in the remote plan when the
row is on another machine. Other offline harnesses are refused.

A message to an offline Claude background session also asks its machine to
resume it. This holds whatever the agent's autonomy is. A ready unclaimed delivery step assigned to the merger or caller
asks that specific session's machine to resume it. Node-owned steps wait for an
online session. Step notifications are queued after the session is online, so
its backlog is sent in one batch. The machine checks the output of
`claude agents --json --all`: a matching background record must be `done` or
`stopped` with no running process, its folder must exist, and the session must
not be a worker. When the record
omits its kind, agent-net must have recorded the session as a background
session. A live or running record, an unreadable listing, a worker and a folder
that is gone are refused. When the record is missing, ambiguous or not retired
(for example a record in another folder) and no matching record shows a live
process, the wake uses the same resume path as `session_change` resume. An
interactive session without autonomy is not taken by that fallback, and an
archived session is never resumed. The gateway makes at most one attempt per session per 10 minutes,
including across gateway restarts, and logs a failure once. A failed attempt
is kept: a restart does not retry it, only a new message asks again, and each
sender with a queued message gets one agent-net notice that says why. Queued messages
cannot expire during the bounded wake; normal delivery resumes when the
session registers. If Claude resumes into a copy with a new native ID, the
original agent-net session ID and its queued messages follow that copy. This
background wake never opens a view. A message queued while its machine is
disconnected is checked when the machine reconnects.

**Wake on messages (`wake_on_message`).** By default (`wake_on_message: on`),
a queued message to an offline owner session of a wakeable harness (Claude in
terminal, background or unknown mode, Codex, OpenCode; not Pi; never workers)
asks that session's machine to resume the same session, then the gateway
delivers the queued messages.
The limits are the ones above: one attempt per session per 10 minutes, kept
across gateway restarts, a failure logged once, never a worker, and never a new
session or a clone. A wake never opens a view, a herdr pane or a window: the
owner opens the session with Enter as usual. With `wake_on_message: off`,
a message asks for no wake unless the agent's autonomy is `wake` (kept for
backward compatibility). The sender's receipt for a message to an offline
session says which of three cases applies:

- `queued; <name> is offline and wake is off for this agent`. Wake is off and
  autonomy is not wake, so the session stays asleep.
- `queued; <name> is offline, waking it`. A wake is under way, started by this
  message or by an earlier one. The receipt does not repeat an earlier failure.
- `queued; <name> is offline and its last wake failed: <reason>`. No wake is
  under way, and the last wake the machine refused for a reason has not been
  cleared by a wake that worked. The sender also gets one notice with the same
  reason.

What each harness does:

- Claude, interactive: `claude --bg --resume <session id>` in the recorded
  folder, through the same launcher as the background wake. The session is not
  running, so it has no `done` record to check. A matching record that is
  running stops the wake. A record that is missing, names another session, has
  no kind (unless the session is recorded as background), is in another folder
  or is otherwise not retired does not: the machine takes the same resume path
  that `session_change` resume runs. A finished or stopped background
  run of the same session in the session's folder does not refuse it: Claude
  keeps that record after the run. The same rule holds for a session whose mode
  is background or not known, whatever the record. An interactive session
  without autonomy is still not woken by that fallback, and an archived
  session is never resumed. A copy with a new native ID keeps the agent-net
  session ID and its queued messages, as above.
- Codex: `thread/resume` on the shared Codex app server, with the session's
  folder. Codex lists the thread as loaded, the daemon registers it under the
  same session ID, and the message is queued with `thread/queue/add`, which
  starts a turn on an idle thread. After a restart of the machine the app
  server is not running and its control socket is missing. The wake then runs
  `codex app-server daemon start` (in the configured Codex home, with the
  start timeout), waits for the control socket, and resumes the thread. If that
  command fails, the wake fails with the command's own output. See
  [decision 0129](decisions/0129-wake-starts-the-codex-app-server.md).
- OpenCode: the saved session is loaded in the owner's OpenCode service
  (`service.json`), in the session's folder, so the plugin registers it again.
  The wake creates no session and is refused when the service is not running.
- Pi: not woken. Its rpc mode cannot open an existing session. The message
  stays queued as before, and the sender's receipt note says `Pi sessions are
  not woken by messages`.

All three methods require the owner's CLI connection and refuse agent callers,
including CLI commands inside agent, Codex app-server and OpenCode serve
process trees. They are not agent tools. The session uses the harness's normal
project settings and MCP servers, with no worker brief, hand-back, worktree,
worker list entry or completion notice. Idle and age limits do not stop it.
Worker cleanup and daemon shutdown leave it running. When herdr is available, starts and resumes open their view there after registration, including CTO starts. Enter on a session on another machine uses `enter_ssh` and `enter_ssh_hosts` from node.json.

### Connected apps

**Pair a tailnet browser.** On Linux, open the console through the machine's
Tailscale Serve while signed in to Tailscale as the user who owns that
machine's node. A fresh browser pairs automatically, with no code or TUI
approval. There is no login setting: the gateway reads the node owner's login
from local Tailscale status. Connected apps lists the device as
`Tailscale sign-in: <login>, <device and browser>`. Each browser has its own
entry, with a number added when names match. The owner Inbox receives one
information notice: `Paired <device> by Tailscale sign-in`.

Revoke that entry to block the device's cookie. It will show the pairing page
on its next visit, even from the tailnet. A browser whose cookie was cleared
can sign in through Tailscale as a fresh device. Cookies last 90 days and renew
on use with less than 30 days left, just like devices paired with a code.

Public internet requests through Funnel still need a code approved by the
owner in the TUI. Other Tailscale users, other OSes, the separate in-process
Tailscale node, and requests where the root-owned proxy or local node owner
cannot be verified also use the code flow. No agent or tool can approve it.

Press `6` for Settings and select a row in Connected apps. The status line shows whether internet access is on and the public MCP URL. A warning lists other routes on the same Tailscale name when the gateway reports them: those services receive the phone sign-in cookie. Press `p` to switch it. With Tailscale, the gateway chooses a free Funnel port (443, 8443, then 10000). Errors identify the gateway machine where Tailscale runs and show the command or link to use. Without Tailscale, press `u` and paste an HTTPS origin from your own tunnel to `127.0.0.1:7422`.

**Its own Tailscale name.** By default the Funnel runs on the gateway machine's own Tailscale node, so the public name is the machine's name and other services on that machine share it. To give connected apps a name of their own, run on the gateway machine:

```
agent-net setup --gateway --apps-tailscale-name agent-net
```

Setup saves the choice and restarts the gateway. Press `p` in Connected apps. The gateway starts its own Tailscale node in its process (tsnet, no second tailscaled and no root) and opens a Funnel on port 443 of that node. The URL becomes `https://agent-net.<tailnet>.ts.net`. The first time, the error line shows a Tailscale login link: open it, log in to your tailnet, then press `p` again. To log in without a browser, add `--apps-tailscale-authkey-file /absolute/path/to/key` (a file that holds an auth key), or set `TS_AUTHKEY` in the gateway service's environment. Funnel must be allowed for the new node in your tailnet policy; a node you log in yourself gets your node attributes, and a tagged auth key needs the `funnel` attribute for that tag. In this mode the warning about other routes is empty and the machine's own Serve and Funnel config is not changed, apart from removing a route agent-net made there before. A pasted tunnel URL (`u`) still works. To go back, run `agent-net setup --gateway --apps-tailscale-machine`; the gateway logs its node out at the next start.

The gateway stores the public switch, chosen port and URL in its database. When the switch is on, it restores the listener and checks Funnel on restart. When off, the listener stays stopped. Switching access off closes the listener first, then removes the recorded Funnel route. If removal fails, status reports the error and retries on the next status check or start. Status also includes other Tailscale routes on the host, except with agent-net's own node, where it has `node` (the node's name) instead. Switching access off closes the own node's Funnel listener and stops the node; it stays in the tailnet. Revoking the last active app also switches access off. Adding an app while off first asks whether to turn access on. The public address is used in OAuth discovery and authentication headers. The owner-only operations are `apps.public.status`, `apps.public.on`, `apps.public.off` and `apps.public.url` (with a `url` field).

Bots appear in the same urgency groups in Sessions. The active view folds idle bots with the other unpinned idle sessions. Their rows show their last call time and are online for ten minutes after a call. Enter opens Messages filtered to that bot. Press `x` to revoke its app.

**Name an app.** Each app has a name, the label you know it by, shown as NAME. The client name stays in CLIENT, so a bot named `cursor` by its client can be called `Trade`. Select a row and press `n` (Enter does the same). A one-line field opens with the current name; Enter saves it and Esc cancels. The name must be unique among active apps. A bot's name is also its name in Sessions. The owner-only operation is `apps.rename` (fields `id` and `name`). A bot cannot see or change its own name.

**Names for the bots of one app.** Several bots can share one connected app and still be different senders ([decision 0118](decisions/0118-bot-names.md)). Every bot tool takes an optional `as` argument: 1 to 32 characters, letters, digits, space, `-` and `_`. A bot can also set the name once per connection, with the header `X-Agent-Net-As: CTO` on its requests to `/mcp`, or with a client name written `as:CTO` in the MCP `initialize` `clientInfo`. A per-call `as` wins over the header, and the header wins over `clientInfo`. A client name without the `as:` marker is ignored. Each name (compared without case) has its own bot session, named `CTO (Trade)` when the app is called Trade, and its own agent node, a child of the app's node. A message sent with a name comes from that session. A reply goes to the name that was addressed, and `agent_messages_find`, `agent_message_get` and `agent_message_reply` see only that name's messages. A call without a name uses the app's shared session, as before. Renaming the app changes the suffix of every name. Revoking the app archives every named node with it. The name is not a security boundary: every name of an app has the same token, grants and limits, and any holder of the token can use any name.

Each bot row has a line under it that says which shared MCP servers the bot can call, by name: `can call: ingest-forge, tenebrae-library`. A bot with no grant shows `can call: agent-net tools only`. A bot that has used names shows one more line, `names: CTO, Shopping`. An owner app acts as you, so it has no such line.

Press `a` and enter the code shown in the browser. Use left and right to choose bot or owner app, Tab to edit the name, Enter to approve, or `r` to refuse. Select an app and press `x`, then `y` to revoke it. Esc returns. Five wrong codes block further lookups for ten minutes.

**Give a bot a shared MCP server.** Select a bot in Connected apps and press `g`. The picker title names the bot: `MCP servers <name> can call. Enter adds or removes, esc closes.` It lists the shared MCP servers the owner has added (the same ones that `agent-net mcp list` shows). Each line has the server, its host and the number of tools this bot may call through it, for example `[x] ingest-forge  on research · 1 tool`. The count follows the server's allow-list; a server that has not reported its tools shows no count. Move with up and down and press Enter to grant a server to this bot, or to remove the grant. The grant is stored as `mcp server <name>`, and the bot's row shows the server name under `can call:`. Only a bot can be granted a server, and it is per bot and per server: other bots, and the bot's other servers, are not affected. Esc closes the list. A grant for a server that was removed stays in the list, marked `no longer added`, so you can take it away.

The Tools tab shows the other side: the details of a shared MCP server name each bot that is granted it, as `bots: Trade, Grok Bot`. Owner apps and revoked apps are not listed. Nothing is shown when no bot has the server.

The bot then sees the allowed tools of that server in `tools/list` of the public `/mcp`, named `<server>__<tool>` as a session sees them. A server with an allow-list (`allowed_tools`) shows only those tools. A call goes through the same path as a session's call: the gateway sends it to the host daemon of the server, and the host adds the server's headers or environment from its own files. The bot never sees them. A tool of a server that is not granted, or a tool outside the allow-list, is an unknown tool. The call counts against the bot's call limit (60 a minute) like every other bot call; there is no other limit. The owner-only operations are `apps.grant` and `apps.ungrant` (fields `id` and `grant`). Removing the grant takes effect on the bot's next request.

Example: ingest-forge runs an MCP server on the LAN with one tool, `submit_x_post`. On the machine that can reach it (research), run `agent-net mcp add ingest-forge --url https://ingest-forge.local.org/mcp --header-file Authorization=/path/to/file`. Then press `g` on Trade's bot and grant `ingest-forge`. The bot sees one extra tool, `ingest-forge__submit_x_post`, and calls it with the file's header value, which stays on research. If the server had more tools and the entry has an allow-list (see [importing native MCP servers](#importing-native-mcp-servers)), the bot would see only the listed ones.

**A bot is an agent node.** When you approve an app as a bot, the gateway makes one agent node for it with the role `bot` (decision [0092](decisions/0092-bots-as-agents.md)). The node uses the app ID as its agent ID and the app name as its display name, and it follows a rename (`apps.rename`). The bot's session is linked to the node, so its charter and place resolve like an agent's. Revoking the app archives the node (a tombstone, nothing is deleted) and takes it out of its Team. Owner apps get no node. Apps approved before this release get their node at the first gateway start (migration 147). A bot node gives the bot no permission and no tool.

Skill items in `tools_items` and `tools_item` include `description`, `start_tokens` and `full_tokens` when nonempty. The description comes from the newest scanned copy. Token counts are estimates: start is (name length + description length + 20) / 4 rounded up; full is the manifest SKILL.md size / 4 rounded up, or zero when unknown. MCP items omit these fields.

Claude Code and Codex sign-in probes run `claude auth status` and `codex login status` with a five second limit. Only the signed-in state and auth method are kept; account details are discarded. Antigravity's sign-in state comes from its own model list (`agy models`): a list means signed in, and no account detail is kept.

### Antigravity main sessions

Interactive agy sessions are discovered from SessionStart hooks and the matching live process and working folder. Conversation summaries supply the title and parent conversation when available. Worker conversations and known sub-conversations are excluded. Busy messages wait for a Stop or PreInvocation hook; idle sessions in a matching herdr pane receive typed text and Enter. Others wait until the next turn. A dispatched message's delivery detail names that wait; it becomes delivered after the hook returns it or herdr accepts it. See [Antigravity](antigravity.md#main-sessions) for Enter and limits.

### Claude inbox health

At daemon start, after managed harness updates, and on each harness inventory
refresh, agent-net checks live Claude session files for `messagingSocketPath`
and tries connecting to an inbox. It sends no bytes: the known frame format
creates a user message and has no documented harmless probe. This checks the
transport, not whether Claude processes a message after accepting it.

With no live Claude session the result is unknown. If live sessions exist but
none has a reachable inbox on two checks in a row, the machine reports
`Claude delivery broken by Claude Code <version>` in the TUI machine note,
Graph machine card, and `agent-net nodes -wide`. Delivery to that machine's
Claude sessions fails immediately with that reason. A later successful check
clears the warning. Other harnesses continue to work.

Open a fresh Claude session and check that the installed Claude Code version
still exposes the inbox socket. If an update removed the inbox contract, use a
compatible Claude Code version and restart the daemon to check again. Do not
send test user frames to a working session just to test the transport.

The workers panel shows "agent-net panel is out of date: run /reload-plugins or restart Claude Code" when its loaded build differs from the installed build; run /reload-plugins or restart Claude Code to load the update.

### Project mergers

Ask the CTO to "set up a merger for garden". If a project lives on more than
one machine, name the machine too. The CTO runs `session_change` action
`merger_start` with `project`, optional `node_id`, and optional `model`.
It starts a separate Claude session in the project's
registered folder on that machine, with model `claude-opus-5-5` by default.
It does not open a view. Enter on its Sessions row opens it as usual. The
merger session gets `GOCACHE` set to the shared worker Go build cache, so its
builds and tests do not grow your own `~/.cache/go-build`.

The session is named `<project> merger`. Its type is `merger`; other types are
`agent` (the default), `worker`, `cto`, `owner`, and `bot`. Session info and
`agent_sessions_find` and `agent_session_get` include `type`. The read tools
also use it as `kind`. The Sessions table shows CTO and merger roles as dim tags after NAME, and the machine in its own column.

Setup assigns accept to the caller and test and merge to `@merger` in one
transaction with registration. If testing was optional, setup makes a test
suite required before merge and says "Enables the default pipeline" in the
approval line. Every project already has a default pipeline in this version;
there is no separate pipeline on/off switch. Existing review and approval
requirements remain in force.

The test owner handles tests before and after merge unless the project has an
explicit after-merge test assignment. Setup moves unfinished, unclaimed
after-merge tasks to the merger when their pipeline owner is `@merger`.
New after-merge test tasks follow that same owner and run on the merged commit.

When a dispatch pass finds several new tasks for one session, it sends one
notice listing each change ID, step, after-merge flag, commit and title. Claim
and report instructions appear once. A single task keeps the detailed notice.
Each listed task keeps its own notice association, so later tasks get a new
notice and listed tasks are not sent again until their notice expires.

A merger registration maps a project and machine to one session. Only that
session receives, claims, and reports `@merger` steps for changes on that
machine. Stopping the merger's harness session or taking it offline keeps
its registration; tasks wait and Delivery says "waiting for merger". Deleting
the session record clears the registration. Explicitly stopping routing with
`merger_stop` or clearing the registration does the same. All of the project's
`@merger` assignments and unfinished tasks, including after-merge steps, return
to the caller. Accept keeps its current owner. A stop works even when no
registration record exists. Stopping the merger stops it for the project on
every machine: the project's other registrations are removed too, so no merger
session stays registered without routing. Replacing the registered session
keeps `@merger` ownership, releases the old claims and queues notices for the
new merger. Completed evidence is preserved. Registrations are stored in the
gateway's `project_mergers` table.

The merger reads the project's own instructions and build files, tests the
changed parts, reads CI results where available, and merges in its own merge
worktree. When the project has CI, it pushes the merged commit to a CI
branch, for example `merge-<change id>`, and waits for CI to pass on that exact
commit. If no CI run appears for the CI branch within the project's usual
time, it reports fail and pushes nothing to the default branch. Once CI passes,
it fast-forwards and pushes the default branch. If CI fails, it
reports the merge step fail with the CI link and pushes nothing to the default
branch. Without CI, it runs the project's tests on the merged commit, then
pushes the default branch. It never force-pushes, retags or deletes tags.
The merger may own release after merge. It follows RELEASING.md only when
assigned that step, after full CI passes on the exact merged commit, and reports
the release tag. GitHub may own only test and merge.
It retries a failed push a few times, then reports fail.

It reports each step with commit hashes read from git. When a change conflicts
with the default branch or its merge batch, it resolves the conflict in its
own merge worktree. It reads the change's diff against its base and what
landed on the default branch since, keeps both intents, and commits the
resolution separately with a message naming the files and what it kept.
The merge report note names each resolved file and summarizes its resolution.
It runs targeted tests for the touched packages and CI as above. If tests or
CI fail after resolution, it reports test fail with the reason. It does not
rewrite features to make tests pass. It never writes features.
A mechanical renumber remains a special case: it may rebase in its own merge
worktree and renumber the store.go migration comment, migrations.lock line,
version numbers asserted in tests, and docs/decisions file number and links.
It changes no SQL, logic or other line for that renumber, runs targeted tests,
and commits it separately with the old and new numbers in the message.
The Graph
tab's selected project and project details card show the accept, test and merge
owners. An offline merger is marked "offline, tasks wait".

To stop routing to it, ask the CTO for `session_change` action `merger_stop`
with `project` and optional `node_id`. Approval clears the registration and
returns every `@merger` step to the caller, including after-merge tests. It
stops the merger for the project on every machine. It leaves the accept owner
unchanged. The Claude conversation stays available.
The owner can also register an existing project Claude session with
`agent-net merger set <project> <node-id> <session-id>`, or clear that record
with `agent-net merger clear <project> <node-id>`. Registering an existing
session keeps step assignments; use the Settings delivery owner rows to assign
`@merger`. Clearing the record returns existing `@merger` assignments to caller.
The local owner operation is `merger_set` with `project`, `node_id`, and
`session_id` (empty clears). Its `configure: true` option also applies the
start delivery assignments, as the approved CTO action does. Merger starts
always use the registered project folder; folder, branch and create options
are cleared. Cross-machine registration changes carry the calling machine's
authenticated owner session, as session relays do.

### Archive offline sessions

In Sessions, press `v` until the offline view appears, then `a` for
**archive offline...**. Enter previews all offline sessions. Press `f` to
select by name glob, folder glob, project, harness, created-before date,
explicit IDs, or ended workers. Filters combine with AND. IDs within the
list combine with OR. A project of `*` means any project; an empty project
field means no project. Dates accept YYYY-MM-DD or RFC3339 in UTC. Globs
are case-sensitive; `*` matches one path segment. An ended worker has a
completed run and no active run.

The dry run lists the count, names, IDs and folders. Review with arrows,
use Space to select records, then Enter to open confirmation and `y` to
confirm. Escape cancels. Only the previewed IDs are changed. If any selected
session comes online before confirmation, the entire operation is refused.

In the same action, `r` shows archived records. Select the records to restore,
press Enter, then `y`. Archiving hides records from session discovery, counts,
project observations and Graph reads. It keeps the database rows, names,
messages, worker runs, delivery changes and history. Retention cleanup does
not delete archived session rows or profiles. A session that reports online
again automatically leaves the archive.

The CTO can run `sessions_archive`, for example:

```json
{"name":"judge-*","folder":"/tmp/*","created_before":"2026-11-01","reason":"Clear old judge sessions"}
```

Archiving applies immediately and includes Inbox undo. To preview first or
restore records, use `settings_change` area `operator`, method
`sessions_archive`, with `dry_run: true` or `restore: true` in `arguments`.
A preview changes nothing and has no undo notice. Use `expected` to pin a
subsequent action to previewed IDs.

## Teams

After the typed Graph cutover, Teams have stable IDs,
names, purposes, an optional owning Agent, active state and immutable history.
An Agent or Repo may belong to one Team. Ownership does not imply membership
or authority over members. Settings and request rules do not inherit. Leads
can redirect messages from outside the crew, as described below.

The owner operator methods are `team_write` and `team_history`. `team_write`
takes `action` (`create`, `rename`, `archive`, `membership`, `mode`, `held_back`, `asks_to`, `lead_merges` or `charter`) and `team_id`.
`charter` takes `attr` and `value` for an inherited Team field, the same shape as `team_charter`; the CTO normally uses `graph_change` `team_charter` or `settings_set` with a Team `ref` instead.
Create takes `name`, optional `purpose` and optional typed `owner` Agent ref.
Rename takes `name` and `expect_name`. Membership takes a typed `member` ref
and `expect_team` (the old Team ID, empty when unassigned); an empty `team_id`
removes membership. A move replaces membership atomically. Archive requires
all members to be removed first and retains the identity and history. Team
ownership can also be changed with `lead_set`; undo is not available.
`team_history` takes `team_id` and returns the most recent 1,000 changes.

Read Teams with `agent_graph_read` using `typed: true` or a `ref` with
`kind: team`. Typed reads include archived Teams with their `active` state,
direct owner references and `member_of` edges. Legacy reads exclude Teams and
membership. The CTO can read and change Teams through `graph_change` and `settings_change` area `operator`, method `team_write`, including authority modes and held back text. For `asks_to`, `lead_merges` and `work_mode` the CTO uses `graph_change` `team_charter`, or `settings_set` with a Team `ref`. It sets `mode` (the authority mode) and `held_back` with `graph_change` `team_charter` too. The owner authorized the CTO to set the mode and held back text at once as an owner action ([security.md](security.md), private owner operations); there is no extra proposal. Graph-backed changes offer history undo.

**The lead may merge** ([decision 0119](decisions/0119-lead-merge-grant.md)). A Team has the setting `lead_merges`: `off` (the default) or `on`. When it is `on`, the owner has granted the Team lead the right to merge the work of its members. The owner or the CTO sets it with `team_charter`, `attr: lead_merges`, `value: on` or `off` (empty means `off`), with no proposal; it is recorded in the graph history and `undo` restores it. `node_settings_get` with the Team name shows it. It is a visible grant, not a gate: nothing is blocked by it. A member of the Team reads one line in its place block, `Merges: your lead <name> may merge this team's work on the owner's grant; hand a merge you cannot do to the lead.` The Claude Code hint before `gh pr merge` states the grant too. The grant does not depend on `asks_to`.

**Questions go to the lead** ([decision 0100](decisions/0100-team-questions-go-to-the-lead.md)). A Team has the setting `asks_to`: `owner` (the default) or `lead`. The owner sets it in the Teams list with `o` (`u` undoes it); `team_write` takes `action: asks_to` and `asks_to`. The CTO sets it with `graph_change` `team_charter`, `attr: asks_to`, `value: owner` or `lead` (empty means `owner`), with no proposal; it is recorded in the graph history and `undo` restores it. The Team node and `agent_graph_read` show `asks_to`. When a session in a Team with `asks_to=lead` calls `agent_owner_ask` (any kind), the gateway does not put the ask in front of the owner:

- It sends a request message from the asker to the Team lead's live session (otherwise the lead Agent's latest session), through the normal delivery and wake rules. The message has the question, the recommendation and the options, and says that deleting data, spending money, and using or sharing secrets that can spend money stay with the owner, so the lead asks the owner itself when needed.
- It opens a Desk ask for the asker with `next` set to the lead's session id, so the Desk shows it as with someone. The old Inbox does not list an open ask that waits on someone but the owner.
- The tool result has `state: sent_to_lead` and a `note`: `Your team (forge) sends questions to its lead, tenebrae. Sent there; carry on with work that does not need the answer.` The asker's session is not marked as waiting for its user. Pi and OpenCode send the question as an explicit wait; the daemon asks the gateway (`ask.redirect`) first and redirects it the same way.
- The lead answers with `agent_message_reply`. The answer reaches the asker as a normal reply, and the Desk ask closes as answered with the reply text. The lead can still ask the owner with its own `agent_owner_ask`.

A session that leads the Team (any session of the lead Agent, or only the pinned lead session), a session in no Team, the CTO and the owner are not redirected. The gateway has no classifier for the owner's held back items, so every ask of such a Team goes to its lead. If the lead Agent has no session, the ask goes to the owner.

**A blocked member reaches the lead** ([decision 0111](decisions/0111-blocked-members-reach-the-lead.md)). A member can be stuck without asking anything: it announces `blocked on the owner: ...` and waits, so the redirect above never runs. When a session in a Team with `asks_to=lead` announces a task that reads as blocked or waiting, the gateway sends the Team lead one notification from the reserved `agent-net` sender: `[agent-net] Team forge: <member> (<session id>) is blocked or waiting, since 00:10 UTC.`, then `Its announce: <text>` (the first line, up to 300 characters), then a line that says the member asked nothing and that the lead can message it or ask the owner. It goes to the same lead session as a redirected ask: a live session of the lead Agent, otherwise its latest one. An announce reads as blocked or waiting when it starts with `blocked` (the idle check's answer), when it has the phrase `blocked on` or `waiting on` as whole words that are not turned around by `not`, `no`, `no longer`, `never`, `was`, `were`, `isn't`, `wasn't` or `aren't` just before them, or when the announce set a wait (`agent_announce` `waiting`); the wait text is added to the text. An announce that starts with `done` never reads as blocked by its words. The gateway keeps a fingerprint of the last text it told the lead about, one row per session in `member_blocked_notices` (never the text itself). A repeat announce of the same text sends nothing. A changed text sends a new message. An announce that does not read as blocked removes the row, so a later block is new, and sends nothing. If the lead Agent has no session, one Desk log line is written instead (`Team forge: <member> is blocked or waiting and the team has no lead session to tell: <text>`), once per text. The lead itself, a session in no Team and a Team with `asks_to=owner` send nothing, except for a wait that names the lead (next paragraph).

**A wait on the lead is not an owner ask** ([decision 0126](decisions/0126-lead-waits-are-not-owner-asks.md)). When the text of a wait (`agent_announce` `waiting`) names the session's own Team lead or Repo lead, the wait makes no Desk ask, and an ask it already made closes. The lead gets the blocked-member notice above instead, in any Team, whatever its `asks_to`; the notice starts `[agent-net] Team forge:` or `[agent-net] Repo demo:`. The lead is named when its Agent id, its Agent display name, the id of one of its sessions or its pinned session stands in the text as a whole word, ignoring case. A wait that names the owner (the word `owner`, even when the lead is named too) or nobody keeps its Desk ask. A wait a harness event reported (a permission prompt) is never moved. The notice follows the text of the wait: the same wait text sends one message, however the task line around it changes; a changed text sends again. The lead's loop tick lists a blocked member once for one text. The gateway keeps the fingerprint of the last tick that listed it in `member_blocked_ticks` (never the text) and removes the row when the member is not blocked. A skipped tick records nothing.

**Replying to agent-net.** `agent-net` is the reserved sender of gateway messages, not a session. A reply or an ack (`agent_message_reply`, `ack: true`) to a message from `agent-net` that is about an ask, such as the chief of staff's `send` sort (`The chief of staff sent you ask i_...`), is accepted from the session the message went to. It is stored as history, never delivered, and it closes the ask: with a body the ask is `closed` with the answer `<session name>: <body>`, and with an ack it is `closed` with `<session name> acked`. One Desk log line records it. An ask that only the owner can decide (`route_fixed`) is not closed this way; the reply fails with `ask <id> is a decision only the owner can answer; a reply cannot close it. If it needs the owner, tell them with agent_owner_ask.` A reply to a message of `agent-net` that is not about an ask (a notice, a loop tick, a step task) fails with `agent-net sent <id> as a notice and does not read replies to it`. A session that did not get the message cannot answer it.

In the TUI, the Graph tab shows all nodes by default. Press `t` to cycle the
filter through all nodes, then each active Team in turn; the key line names the
Team shown. With a Team selected, the Graph shows only its members and the
edges between them. Press `T` to open the Team list: Enter filters the Graph by
the selected Team, `c` creates a Team with a name and purpose, `r` renames it,
Space adds or removes the selected Repo or Agent, and `a` archives a Team with
no members. A gateway refusal shows in the result line at the bottom. When
the typed Graph cutover waits for machines to upgrade, the screen says so
instead of hiding Teams.


**Bots in Teams.** A bot node joins a Team like any agent: with `team_write` `membership` and an `agent` ref, or with Space in the Teams list. It is shown in the Team frame and on the canvas with a `⚙` before its name, and its card shows CLIENT, CONNECTED, LAST USED and `can call: ...` from Connected apps, then its CHARTER and one dim line: a bot reads its charter only if its own instructions tell it to, and its grants and app limits are what bind it. A bot can never be a Team or Repo lead: `team_write` `create` with a bot owner, `lead_set` with a bot (or a bot session) and an undo that would restore one are refused with "a bot can never be a Team or Repo lead", and the lead picker does not list it. Revoking the app removes the membership and records it in the Team history with the actor `apps`. The bot node is not offered when a session picks its agent.

### Leads and agent counts

The owner operator `lead_set` takes a typed `ref` for a Repo or Team and a
`lead` Agent or session ref. A session lead records its Agent as the fallback. Omit `lead` to clear it. The CTO runs the same action
with `graph_change` at once; the Inbox notice has an undo. A Repo defaults to its owner Agent
until explicitly set or cleared. A Repo lead must own the Repo or have a
session recorded there. A Team lead can be an Agent or a session.

For messages from outside the recipient's Repo or Team crew, the gateway picks a lead in this order. First the Repo lead: a message to the Repo lead's own session stays with that session, and a message to any other session of a Repo that has a Repo lead goes to that Repo lead. Otherwise the Team lead, which does not take a message addressed to a Repo lead. The gateway redirects to the selected lead session when online, otherwise to a live session of its Agent, and adds a note to the message and receipt.
When the lead Agent has several live sessions, a session under a provider usage limit comes last, so another live session of the same Agent gets the message; if all are limited the usual order applies. The owner and CTO are exempt, as are workers messaging their caller. If there is
no lead or no live lead session, the original target receives the message and
the receipt says why. A reply is never redirected: it goes to the session that
sent the message it answers. Use `repo:<name>` or `team:<name>` to address a
lead by name or ID. The same address works when the lead has no live session: the
message waits for the newest non-archived, non-worker session of the lead and is
queued and woken under the usual wake rules, like a send to that session ID. An
exact agent name works the same way: a live session with that name comes first,
otherwise the newest session of that agent waits and is woken. A name with no
session at all is refused. Team addresses share the resolver, so they fall back
the same way. Typed Graph reads expose `lead`, `max_agents`, `agent_count` and
`over_max`; session views expose corresponding Repo counts and lead flags.

**Your place.** Every turn, a session that leads a Team, or leads a Repo that other agents work in, gets a block from agent-net that says so. The lead block names the Team or Repo, its members, and that the lead reports to the owner. It says that requests from outside to a member come to the lead, who answers each one or hands it off. For a Team lead, requests to a member Repo that has its own lead go to that Repo lead instead. A Team block ends with the Team purpose, cut to 300 characters. A session in a Team that it does not lead gets two lines: who leads the Team, and to escalate to that lead first, not to the owner. When the Team has `asks_to=lead`, a third line follows: `Questions for the owner go to your lead, <name>.` When the Team has `lead_merges=on`, a line follows: `Merges: your lead <name> may merge this team's work on the owner's grant; hand a merge you cannot do to the lead.` A session in no Team, and not leading a Repo with other agents, gets no block. The block comes from the live Team data on each turn, with the node charter, on every harness.

A routed request carries the redirect note and then one line for the lead: `You lead <scope>: answer this or hand it off to <original target> with agent_message_handoff.` The sender's receipt keeps the short note only.

**Handing off.** `agent_message_handoff(message_id, to_session, note)` passes a request or notification you received to another session (ID or exact name). The new message keeps the original sender and conversation, so the member's reply goes to the asker and not to the lead. The body starts with a line that names the lead, then the lead's optional note, then the original text without the redirect note. Your copy of the request counts as answered. When the member replies, the gateway stores a copy of the reply for the lead as a notification from the member. A reply cannot be handed off. The result is a message receipt.

**Authority along the tree (decision 0089).** The tree is owner, team lead, repo lead, agent. A level holds what its parent holds inside its subtree, minus what the owner holds back. The owner always holds back deleting data, spending money, and using or sharing secrets that can spend money. A team and a repo also have one free text `held_back` field, set by the owner. A team has an authority mode, set by the owner, default `charter_first`:

- `absolute`: an order is carried out like the owner's instruction. The member's charter does not override it.
- `charter_first`: an order is carried out unless the member's charter refuses it. Then the member says no, quotes the charter line, and the order goes up one level, above the lead who gave it.
- `negotiate`: the member may push back with a reason and a counter-proposal. The lead accepts, changes or overrules. Overruling a charter refusal goes up one level.

`agent_message_send` has an `order` flag. agent-net sets the label from the tree, never from the body: `order` (from above you, and the sender marked it), `request` (from a peer or outside your branch), `advice` (a reply to your own question, even from above, never binding). An order note reads `This is an order from your lead <name> (team <t>, mode <m>)`, then what the mode means, the level that decides a refusal, the always-held-back list and the held back text of the team and repo. A reply from above carries the advice note. A flag from a session that is not above the recipient is dropped and the message goes as a request. The place block shows the mode and the held back text. A repo with no team uses `charter_first` and reports to the owner. Edge request rules (`auto`, `ask`, `never`) are peer trust, not command, and stay.

In the Teams list (press `T` on the Graph tab) each team shows its mode, what it means and its held back text. `m` moves the selected team to the next mode, `h` edits its held back text, `H` edits the held back text of the repo selected on the Graph, and `u` puts back the last mode or held back change of this session. The focused team's header on the Graph names the lead, the mode and the held back text. Each change is in the team history. The CTO sets the same two fields with `graph_change` `team_charter`: `attr: mode` with `absolute`, `charter_first` or `negotiate`, and `attr: held_back` with free text; the gateway applies the same validation as the keys, and `graph_change` `undo` restores the earlier value. The `governs` edge, its modes `advisor`, `arbiter` and `owner`, and the setting `delivery.delegated_approver_modes` no longer exist; a migration deletes any left.

The repo setting `max_agents` is a non-negative integer; zero or unset means no
limit. The count excludes workers, mergers, the CTO and bots. Manually started
terminal sessions are included and are never refused. The gateway reserves a
place during an agent-net start, so simultaneous starts cannot take the same
place. The reservation is released on failure or session registration. A
terminal handoff without a native ID matches a new session of the same harness
in the Repo. An interrupted launch expires after two minutes. A late registration may put a
Repo over max and is never killed.

An agent-net start at the max returns `status: failed` and a
`suggested_change` setting `max_agents` to the current max plus one, with its
expected old value. CTO start proposals check the count before asking for
approval and return the same suggestion when the Repo is full. The start is
checked again when an approved proposal runs. Each machine that can start
sessions must run a version with Repo admission support; an older daemon
cannot enforce this rule for its own local starts. The TUI confirmation to
raise the max is a later step.

### Owner session use and pins

Session snapshots include `owner_used_at` and `pinned_at` as Unix seconds and
`owner_uses` as an integer. Zero or an omitted field means no recorded use,
score, or pin. The score increases on each recorded interaction, up to one
million, and halves for each full day since the preceding interaction. Reads
also apply that decay, so old scores fall to zero even without another event.
The timestamp is kept even after the score reaches zero.

The owner-only operator call `session_pin` takes `session_id` and `pinned`
(a boolean). It sets or clears the pin; repeating a pin keeps its original
time. The daemon forwards it as the gateway call `session.pin` with its owner
session. This call is available through the same Source `Call` interface as
`sessions_archive`, and refuses agent clients and commands inside an agent
session. Pins and use fields are visible in ordinary session snapshots.

Use is recorded when the owner opens or resumes a session, sends a message
from the owner session, or restores an archived record. Message retries with
the same idempotency key count once. Pi counts only input marked interactive;
RPC and extension input do not count. Claude Code counts prompts seen by its
optional policy hook, except known peer, notification, reminder, and scheduled
prompt wrappers. Claude does not expose a documented prompt-source field, so
other automated input may still be counted. OpenCode and Codex do not provide
a reliable human-input distinction in these paths, so their prompts do not
count. Recording a prompt or open needs a reachable local
daemon and gateway; these events are not queued if either is unavailable.

The gateway checks hourly for offline sessions to archive, starting one hour
after startup to give daemons time to reconnect. Session retention waits for
that first archive pass; other retention checks still run at startup. It
archives sessions in system temporary folders, including `/tmp` and
`/var/folders`, regardless of their last activity. It also archives other
sessions when neither owner use nor activity occurred in seven days. The
activity check uses the latest of creation, last report, and last activity.
It never automatically archives online or pinned sessions, sessions used by
the owner in the last seven days, the owner, CTO, bots, or registered mergers.
It logs each archived session ID. The normal archive retains history and can
be restored; restoring records owner use and prevents immediate re-archiving.
Session retention also preserves pins and sessions with use or activity newer
than its cutoff.

Ended OpenCode and Codex worker sessions without a worktree are deleted when their retained worker
record is dropped. Worktree worker records and their harness sessions are retained
while the local worktree directory or its branch exists. When both are gone,
the same cleanup removes them. The daemon reports the retained session IDs to
the gateway; session pruning preserves only those workers. A dedicated
background loop runs at daemon startup and on the
worker cleanup interval, independently of link setup and worker health checks.
Only one deletion pass runs at a time. Each pass attempts at most ten eligible
rows within a two-minute budget, continuing after the last attempted row on the
next pass. They remain
available for resume until then. Codex uses `codex delete --force <thread UUID>`
to remove the thread through Codex, including its database entries. OpenCode
starts a temporary private server against that worker's retained data folder and
calls `DELETE /api/session/{id}`, then removes the private worker folder. No
prompt or model call is made. If the harness cannot delete the session, the daemon
keeps the record and retries at the next cleanup pass. Codex deletion and byte
counting use the daemon's configured Codex home. A thread reported as not found
is treated as already deleted. If Codex does not support the delete subcommand,
or any harness deletion still fails seven days after the record became eligible for cleanup,
the daemon drops the record and logs `worker.session.delete_skipped` with the
reason. Remaining shared harness data is left in place in these cases. Safety refusals
for live workers or invalid worker identities or data paths never expire.

Set `workers.delete_sessions` to `false` in node.json to keep harness session data
(default: true). For OpenCode this keeps the private worker folder even after its
record is dropped. Normal owner, CTO and merger sessions are not worker records
and are excluded. Cleanup logs the worker and native session IDs and bytes freed
from private files; Codex rollout bytes are reported separately. Shared database
free pages may remain allocated, so database file sizes may not shrink.

### Provider usage limits

When a live session's provider says its usage or rate limit is hit, the daemon reports `limited_until` (Unix seconds) and `limited_reason` (for example `ChatGPT usage limit`) with the session. The reset time comes from the error text ("try again in ~102 min", "resets 8am (UTC)", a retry-after number); without one it is one hour from the error. The limit ends when that time passes or when the session completes a turn. Pi (turn errors), OpenCode (failed run events) and Codex (`turn/completed`) report it to the daemon; Claude Code's transcript is read for an API error entry. All use the same classifier as the worker failure check (`internal/providererr`). A message to a limited session is still delivered, and its receipt adds the line `<name> hit its usage limit; it can answer after HH:MM UTC`. Nothing else is blocked.

### Idle compaction of background Claude sessions

See [decision 0078](decisions/0078-idle-compaction-for-background-sessions.md).

The daemon checks once a minute for unattached background Claude sessions with
an idle status. Settings exposes three live values: `sessions.idle_compaction`
(default `true`), `sessions.idle_compaction_after` (default `"50m"`), and
`sessions.idle_compaction_min_tokens` (default `200000`). The CTO can read and
apply these advice settings immediately, without owner approval.

Idle time starts at the latest session-file or transcript write. Context tokens
are the latest assistant usage: input plus cache read plus cache creation.
A running `claude attach <jobId>` process, found through `ps`, prevents automatic
compaction only when the owner can be looking at it: the client runs outside
herdr, or in a herdr pane that `herdr pane list` reports as `focused: true`.
A client in an unfocused herdr pane does not block, so tabs left open for days
do not stop compaction. If herdr cannot say where a client runs, it counts as
viewed. One scan runs `herdr pane list` once per herdr server and `herdr pane process-info` at most once per pane, and only when a session has passed the token check. The daemon checks again before typing, excluding its own client.
If the process scan fails it skips compaction.

The daemon opens its own attach client in a pseudo-terminal, waits up to 30
seconds for the prompt, sends `/compact` and Enter, then waits up to five minutes
for a new `compact_boundary` transcript record. It closes and reaps the attach
client and checks that the background process is alive. Each idle stretch gets
one attempt, including failures; a later busy turn resets it. This guard resets
when the daemon restarts. Logs record the outcome. Each attempt or skip decision
writes one line, `event=idle_compact session=<id> result=<ok|skipped:reason|error>`,
with the reasons `small_context` and `viewing`; the same skip reason is logged
once per session in an idle stretch. After success the Sessions
row says `compacted while idle HH:MM`, in your display time zone, until a new turn.
The separate CTO compaction loop handles OpenCode, so it does not overlap.

### Ending a stopped background Claude tree

See [decision 0109](decisions/0109-end-stopped-claude-background-trees.md).

`claude stop <id>` asks Claude's supervisor to end a background job. The
supervisor's process tree can still be there afterwards, so when agent-net stops
a Claude worker or owner session it records the job's pty host and claimed
process before the stop, then checks again about ten seconds later.

The supervisor's roster, `~/.claude/daemon/roster.json`, is the first check. It
is written whenever the supervisor's workers change, and every pid in it is a
live session or worker, including idle interactive sessions whose
`~/.claude/jobs/<id>/state.json` says `done`. If the roster is missing, older
than 60 seconds, or still lists the job or either recorded pid, the check does
nothing. `daemon.status.json` is never used: it can be hours stale.

Only when a fresh roster no longer lists the tree does the check look at the
real process tree. It ends what is left only when the job's state is `stopped`,
`done` or `failed`, the pids carry the recorded start times, the tree belongs to
the daemon's user (real and effective), and it is not a registered live
agent-net session or worker. Ending is SIGTERM, then SIGKILL after two seconds,
with the start time checked again before each signal.

There is no periodic sweep. A read-only survey on research on 2026-11-09 found
no tree that was absent from a fresh roster and still had a job: every live tree
was in the roster, and the only other tree was the idle spare, which has no job.
So agent-net only closes the gap its own stops can create.

Each ended tree writes one line to the daemon log:
`claude.tree job=<id> pid=<host pid> age=<age> rss=<tree RSS> ended=<term|kill|failed>`.

### Delivery step contracts

`delivery.contract.accept`, `.review`, `.test`, `.approve`, `.merge`, `.release`,
`.deploy` and `.verify` are live advice text settings. Each has a network default
and an optional project override. The owner can edit them in the TUI Settings tab;
the CTO can read and set them through the settings tools. Changes apply to the
next task without restarting sessions.

`delivery.ci_mode` is a network and project setting with the values `per_change`
and `train`. Repo visibility is not stored, so the default stays `per_change` and
a public repo opts in with the project value `train`. In train mode the test step
uses `delivery.contract.test_train` instead of `delivery.contract.test`: test each
change locally with the targeted packages, run CI once on the train tip, cite that
one run for every change in the train, and bisect locally when the tip fails before
any re-push. Per-change mode keeps the existing test contract.

Step task messages and reminders include a `contract:` block directly after the
step line. Batched tasks include each step's contract. Worker hand-back notices
name `delivery.contract.test` as the test scope for worker briefs.

### Opt-in Go profiling

Set `AGENT_NET_PPROF=127.0.0.1:6061` in the gateway or daemon process
environment to serve Go profiles at `/debug/pprof/`. Use a different port for
each process on the same host. The address must contain a literal loopback IP
(IPv4 or IPv6). Other addresses are refused with `event=pprof.refused`.
When unset, no profiling listener starts. Setup and service files do not set it.

Work taking more than 200 ms produces a slow log line. Gateway requests use
`event=slow method=<frame-type> ms=<duration> node=<node-id>`. Daemon local
calls use `event=slow method=<method> ms=<duration>` (a `worker_start` line adds `phases=`, see [Workers](#workers)), including the calls
that compose the TUI snapshot and `session_enter`. Durations are elapsed
milliseconds. Fast calls produce no timing log.

A request of the TUI or the web console that fails or times out on its side is
reported to the daemon, which writes one line for it:
`event=client_error method=<operator method> ms=<time waited> timeout=<the deadline it set, in ms, 0 for none> screen=<tab or view> err="<short error>"`.
`screen` is `sessions`, `graph`, `quotas`, `messages`, `tools`, `settings`,
`desk` or `cto` for the TUI, and `web` for the web console. Text is cleaned and
cut to 120 bytes; a method or screen with unusual characters is quoted. The
daemon cannot see a timeout that the client ends, so this line is the only
trace of it. It is not written when the daemon is down (nobody to tell), when
the gateway is down (the screen already says so), or when the caller cancelled
the request. The local method is `client_error`; any `cli` client may call it,
and it only writes the line and bumps the counter below. On screen, a TUI read
that ends by its deadline no longer shows the raw `context deadline exceeded`.
The top banner, the Desk's In flight list and the CTO screen say `slow,
retrying`, keep the last data and ask once more in the background; a second
timeout waits for the next regular refresh. A refusal or any other error is
still shown as it is.

Gateway background runs use `event=slow loop=<name> ms=<duration>`; their
ticker waits are excluded. Step-task dispatches are counted each minute.
When the count exceeds 60, the gateway logs
`event=step.dispatch_count level=debug count=<count>`.

Standing orders and owner judgments live in the gateway's standing_orders table: id, text,
saved_at, source, tags (a JSON list), pinned, updated_at, kind (order, rule, answer,
correction), area (optional repo or team name) and ref (optional ask id). An external-content
standing_orders_fts FTS5 index follows text and tags through triggers.
standing_order_history records order_id, text, tags, kind, area, ref and changed_at before each
update or removal. Search returns up to 20 matches, ranked by bm25, with a
snippet; tag, source and saved_since narrow the results. The CTO tool accepts
save, list, read, update, remove, search and history. Update accepts text, tags
and pinned independently.

When the owner answers, approves or refuses an ask with an answer string (actor owner),
an answer row (kind answer) is automatically saved with text "Q: <question> / A: <answer>",
area set to the ask's project or team, and ref set to the ask id. Automatic closes,
CTO resolutions and chief actions do not record answer rows. The operator method
judgment_add records corrections or rules with kind, text, area and ref.

All agents can search the owner's past decisions with agent_owner_judgments_find
(query, area, kind, limit, offset). `marks: true` returns only the owner's Desk
marks, with the ask, its whole question, the chief's disposition and the time.
Judgments are queried on demand rather than injected into every prompt.

At the start of each CTO conversation, orders enter the system prompt or
precede the first owner message, depending on the harness. Only orders and rules
load. Up to 4096 bytes of order text load as before. Above that budget, pinned orders always load, then
the newest orders that fit. Pins may exceed the budget. Omitted orders add:
"N more standing orders: search them with cto_standing_orders search".

### Chief of staff nudge pauses

Only the registered chief session can use these tools. The chief can apply a
pause without asking the owner. Every apply and resume appears in the Desk log
with Undo. The owner can undo a pause to end it, or undo a resume to restore it
with its original expiry.

- `agent_nudge_pause {kind, team | agent, until?, reason?, mode}`: give exactly
  one team name or agent name/id. `mode: "preview"` lists target session names
  and the count of this kind's messages to them in the last 24 hours, without
  writing anything. `mode: "apply"` stores the pause and returns its `id`.
- `agent_nudge_resume {id}`: end a pause now. This sends no messages.
- `agent_nudge_pauses_find {}`: list active and ended pauses, including
  `ended_at` and `ended_by` for ended ones. Times are Unix nanoseconds.

`until` may be an RFC3339 time, such as `2026-11-18T00:00:00Z`, or
`"provider_reset"`. Provider reset ends when none of the target's sessions has
`limited_until` in the future, with a seven-day safety expiry. Omit `until` for
no expiry. An agent pause covers all its sessions; a team pause covers all
sessions of its current agent members and sessions in member repos.

| Kind | Automatic message paused |
| --- | --- |
| `idle_check` | Is your task done? |
| `autonomy_continue` | You said you have work left; carry on |
| `reply_reminder` | A peer is waiting for your answer |
| `loop_tick` | The repo lead's standing loop prompt |
| `step_reminder` | A ready step task reminder, including batched reminders |
| `stuck_step` | A step has waited without being claimed |
| `undelivered` | Your message was not delivered or may not have arrived |
| `waiting_notice` | The recipient is waiting for its user's answer |
| `wake_failed` | The recipient is offline and could not be woken |
| `member_blocked` | A member is blocked or waiting, sent to its lead |

The target is the recipient of the automatic message. Pauses suppress message
creation and leave each mechanism's bookkeeping unchanged. Nothing accumulates
for delivery on resume; the next due check uses the normal rules. First step
task notices, step results, owner messages, peer requests and replies, worker
notices, and the chief's own brief wake and ask batches are unaffected. Current
mechanisms and defaults remain unchanged unless the chief applies a pause.

Applying a pause also expires matching nudges already queued at the gateway.
Delivery rechecks pauses before sending or waking a session, so a saved queue
snapshot cannot replay an expired nudge after resume.

An `agent_announce` answers the idle check for the session's current work. The
check records its time and a fingerprint of the task, waiting field, owed
request count and running worker count. It asks again only after that state
changes. The idle interval and daily cap stay the same.

Reply reminders follow the original requests in a busy-hold batch. The batch
carrier never gets a reply reminder. Replying to an original message ID closes
that request, and an unanswered request keeps its own sender and ID.

Messages agent-net creates by itself use the `agent-net` sender. This includes
native MCP removal restart notices, asynchronous session start failures and
background phrasing worker tasks. Restart notices are notifications. Real
owner sends, replies, worker briefs and Desk answers keep the owner sender.
The reserved sender remains unavailable through public message sends.

When a step reminder, stuck-step notice or reply reminder is due, agent-net
sends one message containing the other eligible nudges for that session due
within the next 60 minutes. Future nudges alone do not trigger a message.
The step batch keeps the existing multi-change format. Each original notice
keeps its audit record and is marked sent when included, so it is not sent
again at its former due time. Verify reminders also record the covered due
slot to preserve the 30m, 2h, 8h, 24h, then daily backoff. Cadences, settings
and defaults do not change. Steps waiting on an event remain ineligible.

A pause on one part of a queued nudge envelope removes only that part. Other
eligible parts still arrive. Paused nudges cannot trigger the one-hour
lookahead. Session-start failure notifications keep the complete actionable
error text, including MCP server details, and use the agent-net sender even
when the requesting session is the CTO.

### Nudge firing log

Every automatic nudge in the table above writes one row to `nudge_fires` with
its kind, recipient session, the session's agent and team at fire time, message
id and time. A batched envelope has one row per original nudge message.

A nudge counts as followed when the first of these comes from the recipient
within 30 minutes after it fired: an agent-net tool call, an announce, step
evidence, or a message that is not an ack. The 30 minute window is a code
constant, not a setting. A tool call marks matching rows at once; the other
signals are marked when the stats are read, using indexed lookups rather than a
message scan.

`agent_nudge_stats {since?, kind?, team?, agent?}` returns fires, followed and
the useful rate per kind by agent and per kind by team, most fires first. The
window defaults to the last 24 hours; `since` is an RFC3339 time. The chief has
the tool through the normal agent role. The CTO tool list is at its schema
budget, so the CTO reads the same numbers in the TUI.

The TUI Settings tab has a Nudges section with the total and the five kinds
with the most fires. `agent_nudge_pause` in preview mode also returns a
`useful_line`, for example `useful 2 of 14 in 24h`; preview still writes
nothing. No nudge behaviour, default or setting changes.
