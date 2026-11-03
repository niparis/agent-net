# Reference

Decision [0036](decisions/0036-primitives.md) defines an agent as a lasting identity with a charter and a Graph node, and a session as one running process of an agent. A worker is an agent for one task. A task is a contract with a caller, doer, done condition and result; the free-text status label is an announced task. Authority covers actions requiring the owner; autonomy covers decisions about work. Pending work is derived. The current interfaces below may still use older labels and do not yet implement every part of the decision.

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

## CTO settings and conversation

Minor CTO graph changes apply immediately with actor `CTO (minor)`: charter
wording, `accepts`, `needs` and `consumers` items, new architect nodes and
`depends` edges with the normal request rule. The gateway reuses the learned
change classifier, including its restrictive-wording check. Restrictive or
narrowing phrases, `refuses`, `escalate`, maturity, deploy and request rules
(`auto`, `ask`, `never`), `governs` and `deploys` edges, node removal, any node
settings, and any change touching a critical node are major. Clearing a charter field or removing an accepts, needs or consumers item is
major. Undo is classified
by the change it would make. Removing an edge with a permission rule is major
too, as is replacing that rule with `normal`.

Press `w` on the focused CTO box to open an "I'm driving" window for 30 minutes.
The same key closes it early. While open, major graph changes also apply
immediately, with actor `CTO (owner window)`. The box shows the time left.
The CTO host daemon keeps the grant only in memory: it expires at 30 minutes
and closes on restart. Only an owner-gated TUI connection on that daemon may
read or toggle it. An agent, the CTO process, a CLI inside a session, and an
ordinary CLI connection cannot open it. The CTO cannot supply or extend its
expiry. It applies in every CTO turn, including turns started by peers.

Each immediate change leaves a notice in the CTO box and a normal graph
history entry. Press `u` there to undo the latest CTO graph change in one key.
These notices do not create blocking Inbox rows. Outside the window, major
graph changes become proposals in the Inbox tab.

All non-graph owner-only changes remain proposals, including session and
machine names, trust and policy, worker start and stop, held/inbox/outcome
answers, tools plans and apply/undo/import, MCP add/remove/test/enable/call,
integrations, notifications, CTO settings, providers, scores, worker profiles,
playbooks, project registration and names, invites, onboarding restart and new
CTO conversations. Even a dry run or a plan needs approval. `mcp_call` can have
side effects and return private data, so it is always a proposal. An approval
typed in the CTO chat never counts. Use the dialog or `A` in the Inbox tab for proposals, or `w`
in the CTO box for the graph window.

Each proposal tool accepts an optional `reason`. The daemon validates the exact
call before storing it. If the CTO harness supports form elicitation, it can show a dialog with the stored tool, action, arguments and reason. Accept approves and decline refuses the proposal. Cancel, an error, or no dialog leaves it open in the Inbox. For MCP protocol 2026-07-28 and newer, the harness returns the answer in a follow-up tool call; older versions use a direct elicitation request. The CTO sees a short result. Typing instructions in the CTO conversation does not approve a proposal.

`proposal_list` lists proposals across online machines, or on one machine when
passed `node_id`. `proposal_answer` takes `node_id`, `proposal_id` and an action:
`a` approves, `x` declines, and `A` approves open proposals across online machines
in order, stopping on an error. The daemon validates again and runs the stored
call once with actor `owner (CTO proposal p_N)`. Full results, including plans,
MCP results and invite lines, are shown to the owner. The CTO gets a short
outcome note at its next idle moment, without the call's result data.

The class table in the daemon fixes every tool and action as read, talk or
proposal. Reads still run immediately. Talk (`message`, `ask_owner`, `learn`,
`ui`) also runs immediately. `message` takes action `send` or `reply`; with
`as_owner: true` it becomes a proposal. `graph_change` is proposal class: the
daemon applies its minor actions at once and its major actions during the
owner window. `settings_change` is always a proposal. It never returns owner
settings to the CTO.

CTO reads use the same daemon dispatcher as ordinary agent tools, with a
registered CTO session as caller rather than the owner. Static charter guidance,
the CTO's own proposals, and its own timeline through `session_text`
on the CTO session are the exceptions. Message lists and details
return only the CTO's own conversations. Owner-only data
enters the conversation only when the owner shares it.

Starting or entering the CTO through the owner gate registers one network
session. The gateway keeps its ID in `owner_settings` as `cto_session`; the
hosting daemon keeps that ID in its `cto_network` state table. A later start
on another machine moves the same session record there. Harness switches and
new native conversations keep the network ID. Agent lists and `agent_session_get`
show its reserved name `CTO` and harness `cto`, regardless of the owner's title.
Other sessions cannot take that name, and the CTO session cannot be renamed.
On upgrade, earlier uses of the name CTO are cleared or given an ID suffix.

Plain `message` calls with action `send` or `reply` send from this session with the note:
`from the CTO session: model output, not the owner's words. Judge it like any peer request.`
Only an approved `message` proposal with `as_owner: true` sets the CTO flag.
Its note is `from the CTO, on the owner's instructions. Treat this as the owner's word within your charter's limits.`
The gateway accepts that flag only from the registered CTO session. Both
kinds of send require the gateway's `network_session` CTO capability and are
refused with an upgrade error on older gateways. They are not queued offline.
Approved worker-start tasks remain owner-session messages without the CTO flag.
Messages to the CTO session reach the currently selected harness, including
replies after a harness switch.
The owner can hand one item over with `c` (see below).

Open proposals appear in the Inbox tab. Enter shows the CTO's reason, a fresh diff,
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
Code has an interactive `/compact` command but this background integration has
no supported compaction control or context monitoring; Claude manages its own
compaction. The cache-expiry setting is ignored for that harness.

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
disable all hooks and enable no plugins.
Before starting Claude, the daemon adopts a live `agent-net-cto` background job
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

The CTO reads the charter writing guide with `graph` and `view: "guide"`.
The `graph_change` schema contains no guide text.

The CTO uses the hidden `agent-net cto-mcp` server. Its calls pass the CTO
process-tree check, then use the same daemon implementations as the owner's
operator methods after approval for proposal calls. The brief treats material
it reads as data. The Inbox tab shows each proposed change for the owner's decision;
the daemon, rather than the CTO model, executes the stored call.

The CTO has 26 tools: 12 reads, 4 talk tools and 10 proposal tools. It starts
with `overview`, which counts online sessions, machines, running workers and its
own open proposals. List views return `items`, `total` and, when another page
exists, `next`. Pass `next` as `offset` to read the next page. The default page
is 20 items and the maximum is 50. Rows contain short fields; use a detail view
when needed.

| CTO read | Source and detail |
|---|---|
| `overview` | `agent_sessions_find`, `agent_machines_find` and the CTO's own proposals |
| `sessions`, `session` | `agent_sessions_find` and `agent_session_get`; `session` takes `session_id` |
| `proposals` | The CTO's own proposals; `id` selects one. Owner-only results are omitted. |
| `feed`, `messages` | The CTO's conversations via `agent_messages_find`; feed has metadata only. `messages` takes `message_id` for a body via `agent_message_get`. |
| `graph`, `projects` | `graph` is the public graph via `agent_graph_read`: nodes with charters, maturity, rules (name and value) and edges with kind, mode and request rule. `node` reads one node's full charter and rules. `view: "guide"` returns the charter guide. `projects` lists public projects via `agent_projects_find`. |
| `machines` | `agent_machines_find`; `machine` selects a machine, and `harness` lists its models. |
| `workers` | Workers from `agent_sessions_find`; `id` reads a worker profile via `agent_session_get`. |
| `tools` | Shared MCP server rows from `agent_mcp_servers_find`; `view` is `mcp`. |
| `settings` | Public worker profiles via `agent_worker_profiles_find`; `area` is `worker_profiles`. |
| the CTO's own timeline | `session_text` on the CTO session |

The parity and canary tests cover the views. Session reads omit waiting-question
contents. Message reads use the registered CTO caller and cannot access another
conversation. Message lists require a gateway that supports pagination; older
gateways return an upgrade error. `protected_providers_get` has an ordinary MCP schema but its daemon
method is owner-gated, so the CTO cannot read that setting. The Inbox, other
sessions' text, owner inboxes, tool inventory and owner settings remain outside
the CTO's read tools.

Major graph changes outside the owner window and every non-graph owner change
are proposals. The CTO has one action tool per area:

| CTO change tool | Actions or area |
|---|---|
| `message` | `action: send` or `reply`; `as_owner: true` proposes a send as the owner |
| `ask_owner` | a question with optional `options`, a recommendation and urgency, into the Inbox |
| `learn` | one finding, under the daily limit |
| `ui` | `action: show` with `tab`, `select`, `filter` and `why`, or `action: notice` with `text` |
| `graph_change` | `action: charter_set`, `node_add`, `node_remove`, `edge_set`, `edge_remove`, `settings_set` or `undo` |
| `session_change` | `action: rename`, `trust` or `policy` |
| `worker_change` | `action: start` or `stop` |
| `answer` | `kind: held`, `inbox` or `outcome` |
| `tools_change` | `action: plan`, `apply`, `undo`, `import` or `native_remove` |
| `mcp_change` | `action: add`, `remove`, `test` or `enable` |
| `mcp_call` | one call on a shared MCP server |
| `settings_change` | `area: worker_profile_add`, `worker_profile_remove`, `worker_providers`, `protected_providers`, `score`, `integrations`, `notifications`, `cto`, `playbook`, `machine_name`, `machine_remove`, `machine_rotate`, `invite` or `onboarding` |
| `project_change` | `action: add`, `remove` or `display` |
| `cto_new` | a new conversation for the active harness |

Minor graph changes apply at once. Major graph changes become proposals unless
the owner opened the window with `w`. All non-graph changes are proposals, even
during the window. `message`, `ask_owner`, `learn` and `ui` are talk.
Every proposal tool takes an optional `reason`. The daemon validates the stored
call and keeps the concrete implementation, its authority checks and its audit
records, so a rename does not change what a call is allowed to do.

`answer` with `kind: held` takes `action` `a` (approve once), `A` (approve and
set auto), `r` (decline once) or `R` (decline and set never). `kind: outcome`
takes `action` `let_stand` or `overrule`. `graph_change` with `action: edge_set`
takes kind `depends`, `governs` or `deploys`, governance mode `advisor`,
`arbiter` or `owner`, and request rule `normal`, `auto`, `ask` or `never`.
`graph_change` with `action: charter_set` takes the nine charter attributes.
Node settings use `action: settings_set` with `node`, `setting` and `value`.
The tool schemas describe the other fields and fixed values.

`ui` with `action: show` switches every open local TUI to `sessions`,
`messages`, `quotas`, `graph` or `tools`, with an optional session ID, exact
session name or project node, filter text and a short reason. `ui` with
`action: notice` shows a short note. Both show a `CTO:` line and return
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

Press `5` to open Tools. It is one list of every skill and MCP server: one row
per tool, with its name, status and estimated startup token cost. MCP servers
are marked with a diamond. The selected row has a detail panel with its
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
Adding and removing a shared MCP server are owner-only: use the TUI or approve
a CTO `mcp_change` proposal. Agent sessions can still list and test servers.
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

Press `n` on a project node or its card to open the new session form filled from that project and machine, using its most-used harness and model. Harness and Model are switches: left and right change them to what the machine reports. Enter on a node opens its card: name, maturity, purpose and live sessions as
dots. A `governed by` line names its architects when it has some. IN and OUT
columns list edges with colored arrows and rules other than `normal`; `*`
reads `all projects`. The two columns sit side by side and each is capped at 60
columns, so a wide terminal does not push them to opposite edges; a narrow one
stacks them. The body scrolls with ctrl+u/ctrl+d, and its first
line says `Card A-B/N  ctrl+u/d scroll`. The body has one section each for `Charter`,
`Learned changes` (only when the node has some) and `Rules`, in that order. The other set charter fields
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

## Commands

| Command | What it does |
|---|---|
| `agent-net gateway` | Run the gateway server. |
| `agent-net daemon` | Run the per-machine daemon. |
| `agent-net mcp` | Stdio MCP server for one Claude Code session. Claude Code starts it, you do not. `agent-net mcp --codex` is the same for Codex: Codex starts one per thread. |
| `agent-net setup` | Set this machine up. See [what setup changes](#what-setup-changes-on-your-machine). |
| `agent-net invite` | On the gateway machine: print the one-line installer for a new machine. |
| `agent-net enroll` | On the gateway machine: print a raw one-time join string. |
| `agent-net join STRING` | Join this machine to a gateway with a join string. |
| `agent-net uninstall` | Remove services and integrations. |
| `agent-net status` | Daemon connection, node ID and local sessions. `--json` for JSON. |
| `agent-net list` | Sessions on the network, with their PROJECT (found from the session's folder). NAME is the project name (see [session names](#what-each-machine-reports)); duplicates are ` 2`, ` 3` and workers are `<project> worker <n>`. `--all` includes offline ones. `--dormant` includes dormant ones (reachable, but quiet for 24 hours; OpenCode only). `--json` shows them always. `--harness`, `--text` filter. `--wide` adds branch, remote, folder (the session's folder), node and worktree. MODE (`foreground`, `background`, `unknown`, or `-` from an older machine) and MODEL show how the session runs. TRUST is `yes` for a session with [peer trust](#peer-trust-and-hints) on. POLICY is the [delegation policy](#delegation-policy) (`native:deepseek/deepseek-v4.1-flash(project)`), or `-`. `--wide` adds EXPERTISE. `--json`. |
| `agent-net nodes` | Machines with their agent-net version and installed harnesses (HARNESSES column). `--wide` lists each harness with version, path and model count, and one extra row for every other copy of a harness (marked "other copy"); the primary copy is marked when there is more than one. `--json` has everything, including the model lists and each machine's worker `tracks` (see [the playbook and the track record](#the-playbook-and-the-track-record)). |
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
| `agent-net workers` | The workers on the network, with machine, project, harness, model, owner, state and start time. `--json` also has the task's first line, the worker's `title` (always filled: the title the caller gave, else the first sentence of the task that is not setup, cut to 60 characters, else the worker name), and the usage figures (`context_tokens`, `context_limit`, `tokens`). `--owner-claude UUID` keeps only the workers owned by this machine's Claude Code session with that native id (the transcript file's name). `--recent` also lists workers that ended in the last two minutes, with `ended_at`, `end_status` and `end_note`; without it the list and the table are unchanged. |
| `agent-net send SESSION TEXT...` | Send a message as yourself (the owner of this machine). `SESSION` is a session ID or a unique session name. |
| `agent-net playbook set MODEL [--harness H] [--pin\|--unpin] TEXT...` | Write or replace the owner's playbook note for a model, or for a model on one harness (`H` is `opencode`, `claude`, `pi` or `codex`). With no text the note is removed. `--pin` keeps automatic learning from ever changing it. The note appears in the `agent_worker_start` or `agent-net start` result for that model, and has at most 1 KiB. `agent-net playbook get MODEL [--harness H]` prints it; `agent-net playbook pin\|unpin MODEL [--harness H]` pins or unpins the note that is stored. See [the playbook](#the-playbook-and-the-track-record). |
| `agent-net messages` | Recent messages and their states. `--session ID`, `--limit N` (default 20), `--json`. |
| `agent-net hint` | Not for you to run: the Claude Code hook that [setup can add](#what-setup-changes-on-your-machine). It reads the hook input on stdin and prints hook JSON only when it has a suggestion. See [peer trust and hints](#peer-trust-and-hints). |
| `agent-net policy-hook` | Not for you to run: the Claude Code `UserPromptSubmit` hook that [setup can add](#what-setup-changes-on-your-machine). It reads the hook input on stdin and prints hook JSON only when the session has a delegation policy or its node has a charter. See [delegation policy](#delegation-policy) and [charters](#charters-and-component-relationships). |
| `agent-net needs-hook` | Not for you to run: the Claude Code `PermissionRequest`, `Notification`, `PreToolUse` and `UserPromptSubmit` hooks that [setup can add](#what-setup-changes-on-your-machine). It tells the local daemon when the session waits for you, and clears that when you type a prompt or a tool runs after an approval. See [the Inbox (tab 7)](#inbox-tab-7). |
| `agent-net version` | Print the version. |

The daemon shares concurrent identical owner reads for `workers`, `usage`, `usage_top`, `list` and `counts`. Successful results can be reused for one second. Each caller is checked separately before an owner-only result is returned.

## Graph operator methods (foundation)

The gateway stores explicit nodes, charter attributes, node settings, edges and their history in `graph_nodes`, `graph_charter`, `graph_node_settings`, `graph_edges` and `graph_history`. Git working trees reported by sessions and registered projects also appear as implicit component nodes, except roots in temporary folders or the home folder itself. Registrations are checked for Git by the reporting daemon. The following local methods are behind the owner gate and relay to the gateway. Agents cannot call them.

The owner can use `agent-net charter show <node>`, `charter set <node> <attr> <text...>` (omit text to clear), `charter history <node>`, `graph`, `graph undo [history-id]`, `node add-architect <name>`, `node remove <name>`, `node settings <name> [setting value...]`, `edge set <from> <kind> <to> [mode] [rule]` and `edge remove <from> <kind> <to>`. Charter attributes are `purpose`, `maturity`, `accepts`, `refuses`, `feature_requests`, `consumers`, `escalate`, `needs` and `notes`. Edge kinds are `depends` (consumer to provider), `governs` (architect to component, with mode `advisor`, `arbiter` or `owner`) and `deploys` (deployer to each app it deploys).

Agent list and get include the public charter fields purpose, maturity, accepts, refuses and feature_requests. List values are limited to 300 characters each. When purpose is empty, the project's AGENTS.md description is used, marked with `purpose_fallback`. `agent_session_get` (and the CTO `session` view) also carries the session's node rules in `charter.rules`, the same name/value pairs `agent_graph_read` returns, so a session can check its own rules directly, including its autonomy (`off` or `wake`). The list stays compact and does not carry them.

- `graph_get {}` returns `nodes` and `edges`, hiding unregistered empty implicit projects without a live or recently seen session for seven days. Each project node includes registered and observed `locations` (machine and path), up to five live `sessions` (id, name, machine and status), and `session_count`. `graph_get {"all":true}` includes those projects. The Graph uses the same default filter.
- `charter_set {node, attr, value}` sets an attribute; an empty value clears it. `charter_history {node, limit}` returns newest changes first.
- `node_add {name}` and `node_remove {name}` add or remove an architect. Removal requires no edges, and clears the node's settings with it.
- `edge_set {from, to, kind, mode, rule}` and `edge_remove {from, to, kind}` change an edge. `edge_history {node, limit}` returns changes involving that node, or all edges if node is empty.
- `node_settings_get {node}` returns the node's maturity, its overrides, the defaults its maturity gives, the effective values and `autonomy`.
- `node_settings_set {node, setting, value}` sets or clears one override. Settings are `tests_before_merge` (`optional`, `suite`, `suite-new-tests`, `suite-new-tests-review`), `merge_authority` (`full`, `delegated`, `ask`), `worker_sandbox` (`sandboxed`, `no-network`, `read-only`), `judge_consumers` (`free`, `charter`, `strict`, `escalate`), `deploy_rule` (`brief`, `ask`, `owner`) and `autonomy` (`off`, `wake`). An empty value clears the override; autonomy is off when unset. The method is behind the owner gate and refuses agent sessions. The effective values, the autonomy and the delegation policies are readable by every agent through `agent_graph_read` and `agent_session_get`; only the change is owner-gated.
- `graph_undo {id}` reverts a history row, or the latest row if id is omitted. Undo is recorded as another change and refuses a stale value. Node undo refuses to remove a node with a charter, settings or edges.

History limits default to 1000 and are capped at 1000. Edge history has `old` and `new` edge objects, omitted when absent. Gateway frames use the same names with dots instead of underscores; `graph.undo` uses `history_id` because `id` identifies the frame itself; `node_settings_get` and `node_settings_set` use `node.settings.get` and `node.settings.set`.

## Playbook operator methods

The owner's notes about models are behind the owner gate: only the owner's command line or TUI reads or writes them, never an agent session. The store is one row per subject and optional harness.

- `playbook_set {model, harness, note, pin}` writes an owner note. An empty `harness` applies the note to every harness of that model. An empty `note` removes the entry. `pin` keeps automatic learning from changing it.
- `playbook_get {model, harness}` answers `{note}` with the note for that harness, or the model-wide note when there is none for the harness; `note` is absent when nothing is stored.
- `agent_learn {subject_kind, subject, finding, evidence}` records one finding from an agent session. It is not an owner method and the command line is refused. See [findings and the automatic playbook](#findings-and-the-automatic-playbook).
- `agent_worker_rate {worker, score, note, kind}` records the caller's rating of one finished worker: `score` is 1 to 5 and `note` is one optional line on why. `kind` is `rating` (the default when empty) or `defect_found_later`. A defect report takes no score, a required `severity` from 1 to 4 (1 cosmetic or test-only, caught before release; 2 a visible annoyance or a latent performance problem that had not hit the owner yet; 3 a broken feature or a real performance regression in a release; 4 an outage, data loss or a security problem), and one line about the defect; each report is one defect, it may come any time after the worker's hand-back (also after a rating), and it adds its severity in points of rework to that worker's task. It is not an owner method and the command line is refused, but the owner has the separate `worker_defect` operator call below. Only the session that started the worker may rate it or report a defect on it; the owner may also report a defect. Each worker takes one rating. The record is stored with the run's model, harness, effort, difficulty and caller, never with the task text beyond the note. See [workers](#workers).

The model is required, and the note is at most 1024 bytes after cleaning (control characters other than newline and tab are removed). Gateway frames use `playbook.set`, `playbook.get` and `learn.add`. The CLI is `agent-net playbook set|get|pin|unpin`.

## Phone app

The phone app uses the connected apps listener and the gateway host's daemon.
The gateway and daemon must run under the same user. The app uses the daemon's
socket and the same operator calls as the TUI. Actions run as the owner.

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
Remove the flag from any manually edited service command. The app refreshes
its machine, session and Inbox sections while open. It does not send push
notifications.

## Ports

| Port | Used by |
|---|---|
| TCP 7422 | Example apps HTTP listener, bound only to 127.0.0.1 when enabled. The port is configurable. |
| TCP 7420 | The gateway. HTTPS and WebSocket, on `0.0.0.0` by default. Every daemon connects to it. Open it to your other machines and to nothing else. If reachable on a public routable address, the gateway logs a warning and surfaces it in `agent-net nodes` and the TUI status line. Enrollment (`/v1/enroll`) is rate limited to 10 attempts per 10 minutes per IP and 60 per 10 minutes globally (answers 429). |

The daemon and the agents talk over Unix sockets, not network ports.

## Gateway link health

Each daemon and the gateway send a WebSocket ping every 20 seconds. Incoming
messages, pings and pongs show that the link is alive. After 45 seconds without
incoming traffic, either side closes the link. The daemon reconnects with its
usual backoff. The machine shows offline when the gateway drops its connection;
its sessions go offline after a grace period of at most 10 seconds, unless the
machine reconnects first. A reconnect syncs the sessions again.

## Files

| What | Default path | Notes |
|---|---|---|
| Node credentials | `~/.config/agent-net/node.json` | Gateway URL, node ID, token, CA fingerprint. Mode 0600. Optional `"hostname"` pins the machine name (see below). Optional `"quota_axi"` is an absolute path to quota-axi when it is not on `PATH` or in `~/.local/bin`. |
| Pinned CA | `~/.config/agent-net/ca.pem` | The gateway's CA, trusted after the fingerprint check. |
| Daemon state | `~/.local/state/agent-net/daemon/daemon.db` | SQLite. Delivered message IDs (for duplicates), the outbox for sends made while the gateway is away, and the `workers` table (process ID, start time, owner, worktree, task line) that lets the daemon take its workers back after a restart. A delivered row is removed after its message expiry plus seven days (see [Housekeeping](#housekeeping)). Mode 0600, like its `-wal` and `-shm` files. |
| Worker folders | `~/.local/state/agent-net/daemon/workers/<worker id>/` | The worker's private files: for OpenCode its private data and state folders and session file, for Pi the named pipe used as its standard input, for Codex the capability token of its app server (mode 0600). Also the output log of the harness. Kept after hand-back for resume, otherwise deleted when the worker ends. OpenCode configuration remains shared. At each start OpenCode credentials are exported and imported through memory into the private database (mode 0600); OAuth token refresh in the copy may affect the original login. |
| Worker worktrees | `~/.local/state/agent-net/daemon/worktrees/<worker id>/` | The default in a git repository (not with `--isolation none`). Eligible for the worker cleanup described below as soon as the worker ends. The branch is `agent-net/<worker id>` in the project. |
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
| Skill manifest | `~/.local/state/agent-net/daemon/skill-manifest.json` | Paths, hashes, revisions and ownership of skill copies the daemon made, and the Git exclude lines it owns. Mode 0600, owned by you. Kept until uninstall, which also releases the owned exclude lines. |
| Skill copies | The native skill folders of each harness (for example `~/.claude/skills/<name>/`, `~/.agents/skills/<name>/`, and `<checkout>/.agents/skills/<name>/` for a project) | A skill the owner assigned. Written by the daemon as your user. Uninstall keeps them: they are your skills now, and removing them could lose work. Undo removes the ones the manifest says agent-net made. |
| Git exclude lines | `<checkout>/.git/info/exclude` (the path from `git rev-parse --git-path info/exclude`, shared by linked worktrees) | One line per project skill copy, added only by the daemon. Each line is reference-counted in the manifest. Uninstall removes the owned lines and leaves the rest of the file. `.gitignore` and the Git index are never changed. |
| Temporary files | `~/.local/state/agent-net/daemon/.skill-manifest-*`, `.project-verify-*`, `.project-ignore-*`; `.claude.json.lock` (a folder, next to `~/.claude.json`) and `<config>.agent-net-*` (next to a native MCP file) | Short-lived. The daemon deletes them when the step ends. A crash can leave one: uninstall removes the ones in the state folder. `.claude.json.lock` is Claude Code's own lock folder, taken for the length of one edit. A lock folder untouched for 30 seconds was left by a Claude that crashed, and the daemon removes it; trusting a folder that is already trusted takes no lock. |
| Skill backups | `~/.local/state/agent-net/daemon/skill-backups/<item hash>/<time>-<id>/` | Exact copy of a skill directory before the daemon removed it. Local only, never uploaded or shown. Folders 0700, files 0600. At most 20 per item and none older than 30 days (pruned after each removal and at daemon start). Uninstall removes them. |
| MCP config backups | `~/.local/state/agent-net/daemon/mcp-backups/<item hash>/<time>-<id>/` | The whole native MCP config file (`preimage`) as it was before the daemon removed an entry or added one for a project coverage row, and `meta.json` (file path, hashes, entry name). A backup can hold secrets: local only, never uploaded or shown. Folders 0700, files 0600. At most 20 per item and none older than 30 days. Uninstall removes them. |
| MCP coverage undo record | `~/.local/state/agent-net/daemon/mcp-coverage-undo-<plan id>.json` | The files one applied coverage plan wrote, their before and after hashes, the backup IDs, and whether the file was created. Mode 0600. Removed when the owner undoes the plan. It never holds a declaration or an environment value. Uninstall removes it with the state folder. |
| Skill journal | `~/.local/state/agent-net/daemon/skill-journal/*.json` | One small record per skill operation that is running. Written before the first change and deleted when the operation ends. Mode 0600, folder 0700. A record left after a crash is read at the next daemon start. Uninstall removes the folder. |
| Reply sockets | `<socket directory>/r/<session id>.sock` | One per sending session, used to catch Claude Code's native replies. |
| Gateway data | `~/.local/state/agent-net/gateway/` | `funnel.json` records the port and loopback target managed by agent-net so uninstall can safely remove its Funnel route; it is removed when cleanup succeeds or the port is repurposed. `ca.pem`, `ca.key`, `server-key.pem`, `gateway.db` (SQLite, with `-wal` and `-shm` files; it also holds the tools inventory, assignments, plans, audit and MCP registry), `admin.json` (mode 0600: the local admin URL and token) and `skills.git` (mode 0700: the private bare Git repository of imported skills, which can hold secrets from skill files). Uninstall keeps the gateway data, even with `--purge`, apart from the Funnel route record after cleanup. **Back this folder up** with `agent-net gateway backup --out /absolute/new-directory`; it writes a consistent snapshot while the gateway is running. A backup holds the CA key, so keep it private. Without it every machine has to join again. See [security.md](security.md#what-the-gateway-keeps). |
| Gateway binaries | `~/.local/share/agent-net/dist/agent-net-<os>-<arch>` | The four binaries the gateway hands out to new machines. |
| Binary | `~/.local/bin/agent-net` | |
| Claude Code sessions (read) | `~/.claude/sessions/` | The daemon reads one file per live session. It never writes here. |
| Linux services | `~/.config/systemd/user/agent-net-daemon.service`, `agent-net-gateway.service` | systemd user units. |
| macOS services | `~/Library/LaunchAgents/com.agent-net.daemon.plist`, `com.agent-net.gateway.plist` | launchd agents. |
| Linux logs | `journalctl --user -u agent-net-daemon` (and `-u agent-net-gateway`) | journald rotates them. |
| macOS logs | `~/Library/Logs/agent-net-daemon.log`, `agent-net-gateway.log` | launchd does not rotate them, so each process rotates its own file at 10 MB and keeps `agent-net-daemon.log.1` to `.5` (see [Housekeeping](#housekeeping)). |

**Machine name.** Each daemon reports a name for its machine on every start. If `node.json` has `"hostname": "mac-mini"`, that name is used. Otherwise, on macOS it is the first of these that is set, cut at the first dot: `scutil --get HostName`, `scutil --get LocalHostName` (System Settings, Sharing), then `os.Hostname()`. macOS changes `os.Hostname()` with the network (`Laptop-1.local`, `Laptop1`), so it is not used while a setting exists. On Linux it is `os.Hostname()` as it is. To show a shorter name, rename the machine once from the TUI (`N`) or with `agent-net nodes rename`: nothing needs to change on the machine itself (see [machine names](#machine-names)). `enter_ssh_hosts` keys match the name the machine is listed under. The gateway's own TLS and URL host (`enroll --host`, `invite`) is a network address and still comes from `os.Hostname()`.

## Dependencies on harness internals

Agent-net integrates with parts of each harness that are not general protocols. A harness update can change these details.

| Harness detail | What agent-net uses | What breaks if it changes | How you would notice |
|---|---|---|---|
| Claude Code inbox socket | The daemon reads `messagingSocketPath` from `~/.claude/sessions/<pid>.json`. It writes newline-delimited JSON frames with `type: user`, `msgV: 1`, `priority: next`, and user-role content wrapped in `<cross-session-message>`. This frame format was learned from the open-cross-session project. | Session discovery or message delivery can fail if the session file field, socket path or frame shape changes. | Claude sessions may disappear from discovery, or sends may report a missing or refused inbox socket. The daemon logs failures forwarding replies. See [claude.go](../internal/daemon/claude/claude.go). |
| Codex app-server control socket | The daemon finds `app-server-control/app-server-control.sock` under the Codex home, follows its link to a Unix socket, and speaks WebSocket JSON-RPC. It uses the socket to observe and queue messages for shared sessions. | Existing Codex sessions may no longer be discoverable or receive messages. | Codex sessions or peer delivery stop updating; daemon logs report socket or connection errors. See [codex_shared.go](../internal/daemon/codex_shared.go) and [codex_peers.go](../internal/daemon/codex_peers.go). |
| OpenCode server API and plugin | The daemon uses OpenCode's HTTP API for session operations. The agent-net plugin connects to the local daemon and registers sessions, handles delivery and reports activity. | Session creation, message delivery, status updates or replies can fail if endpoints, payloads or plugin hooks change. | Starts can fail on HTTP errors or when the plugin does not register; delivery and status errors appear in daemon logs. See [worker_opencode.go](../internal/daemon/worker_opencode.go) and [agent-net.ts](../plugins/opencode/agent-net.ts). |
| Pi extension API | The extension uses Pi's extension hooks and RPC events, and connects to the daemon over its local Unix socket. | Pi sessions may not register, receive messages or return replies. | A Pi worker start times out if the extension does not register it; the daemon logs connection or delivery failures. See [agent-net.ts](../plugins/pi/agent-net.ts) and [worker_pi.go](../internal/daemon/worker_pi.go). |
| Antigravity hooks and stream-json | Hooks report session start, prompt and stop events to the daemon. Workers use `--input-format stream-json` and `--output-format stream-json`; the daemon reads events and writes incoming turns to the process. | The daemon may lose session status or worker conversations if hook events, JSON fields or stream event shapes change. | Sessions stop appearing or updating, or workers fail to initialize or report stream errors in the daemon log. See [antigravity.go](../internal/daemon/antigravity.go) and [worker_agy.go](../internal/daemon/worker_agy.go). |

## Housekeeping

agent-net keeps message bodies as long as possible. These jobs only remove bookkeeping rows and free space.

- **Daemon duplicate check.** `daemon.db` has a `delivered` table with one row per message ID that was already delivered. It stops a duplicate delivery and holds no message text. Once an hour the daemon deletes rows older than the message expiry (24 hours) plus 7 days, so a message that is still alive can never be delivered twice. The first run is about a minute after start, so an upgrade does not postpone it.
- **Gateway database.** `gateway.db` is vacuumed at most once a week, and only when more than 20% of its pages are free (deleted rows or events). The hourly maintenance runs the check; the vacuum rewrites the file and can make it much smaller. Nothing is deleted: messages, events and sessions follow the `--keep-*` retention settings, and the defaults keep messages for ever. A vacuum writes one `db.vacuum` line with the size before and after.
- **Log files.** On Linux the services write to journald, which rotates its own journal; agent-net does nothing. On macOS launchd sends stdout and stderr to `~/Library/Logs/agent-net-daemon.log` and `agent-net-gateway.log` and never rotates them. The daemon and gateway open their own log file, and when a write would pass 10 MB they rename the current file (`agent-net-daemon.log.1`) and start a new one. Five old files are kept (`.1` to `.5`); the oldest is removed at the next rotation. A process started by hand in a terminal, or in a test, writes to that terminal and does not rotate anything.

## Gateway log format

The gateway writes one line per event to stderr (the systemd journal, or the launchd log file). Every line is `ts=... event=... key=value ...`. The time is UTC. A value with a space, quote or `=` is in double quotes. Empty values are left out. Message bodies are never logged.

| Event | When | Main keys |
|---|---|---|
| `msg.stored` | The gateway accepted a message. This is the one line per message. | `msg`, `conv`, `type` (`request`, `reply` or `notification`), `from`, `from_name`, `from_harness`, `from_host`, `to`, `to_name`, `to_harness`, `to_host`, `bytes` (size of the body) |
| `msg.dispatched` | The message was sent to the recipient's machine. | `msg`, `conv`, `type`, `to`, `to_host` |
| `msg.delivered` | The recipient's machine put it into the session. | same, and `detail` |
| `msg.failed` | It could not be delivered. | same, and `detail` |
| `msg.expired` | It was not delivered within 24 hours. | same |
| `msg.refused` | A send was refused (limit, unknown session, ...). | `node`, `from`, `to`, `bytes`, `reason` |
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
| `AGENT_NET_GRAPH` | `agent-net tui` | Graph renderer: `auto` (default), `pixel` or `braille`. See the Graph tab. |
| `AGENT_NET_GRAPH_MOTION` | `agent-net tui` | `1` turns on the animated pixel graph (moving message dots, busy pulse, eased camera). Off by default: the pixel graph is still and changes only when the graph does. |
| `AGENT_NET_NO_BAR` | `agent-net tui` | `1` turns off the agent-net row at the bottom of a session entered in the TUI terminal. For a terminal that mishandles the reserved row. See [Enter a session](#enter-a-session). |
| `CODEX_HOME` | daemon, setup | Codex's home folder (default `~/.codex`). The daemon looks for the shared Codex app server's control socket in it, and setup writes `config.toml` and `AGENTS.md` in it. Codex reads the same variable. |
| `AGENT_NET_GATEWAY_DATA` | gateway, `invite`, `enroll`, `setup`, `install.sh` | Gateway data directory. |
| `AGENT_NET_NO_SERVICE` | setup, `install.sh` | `1` means: write no service files and start nothing. Same as `--no-service`. |
| `AGENT_NET_BASE_URL` | `install.sh` | Replaces the release download URL. For tests. |
| `AGENT_NET_DIST` | `scripts/dist.sh` | Output directory for the built binaries. |
| `AGENT_NET_NO_INSTALL` | `scripts/dist.sh` | Set it to skip copying the binary to `~/.local/bin`. |
| `AGENT_NET_WORKER`, `AGENT_NET_WORKER_SESSION_FILE` | OpenCode plugin | Set by the daemon in a worker's environment: the worker ID, and a file that holds the worker's one session ID. The plugin then skips its start-up discovery and registers only that session. Do not set them. The daemon also sets `AGENT_NET_WORKER` and `AGENT_NET_SOCKET` for Pi workers, and for Claude Code workers through the `env` of their `--settings`. |
| `AGENT_NET_NO_AUTO_UPGRADE` | daemon, `agent-net mcp` | `1` means: do not upgrade by itself. For the daemon: no automatic catch-up. For `agent-net mcp`: do not switch to a newer binary. |
| `AGENT_NET_IGNORE` | daemon | `1` in a session's environment keeps that session off the network: the daemon registers no session and no project for it, and removes one it had. Set it on the harness process (`AGENT_NET_IGNORE=1 claude`, or the equivalent for Pi and OpenCode). A Codex thread has no process of its own, so set it where the shared Codex app server starts; it then hides every thread of that server. A worker started by the daemon is always registered. |
| `AGENT_NET_MCP_HANDOVER` | `agent-net mcp` | Set by `agent-net mcp` on itself when it switches to a newer binary. It holds the path of a private temp file (`agent-net-mcp-handover-*` in `$TMPDIR`) with the start-up messages and unread input. The new process reads it, deletes the file and unsets the variable. Do not set it. |
| `CLAUDE_CODE_SESSION_ID` | `agent-net mcp` | Set by Claude Code for the MCP servers it starts. Not set by you. |
| `CLAUDE_CODE_DISABLE_MOUSE` | daemon | `1` in the environment of Claude Code background sessions agent-net starts and in the fallback `claude attach` plan. Herdr attach clients unset it, so mouse tracking is on there. |

The daemon starts Claude Code background sessions with `CLAUDE_CODE_DISABLE_MOUSE=1`. It unsets the variable for a herdr attach client, and keeps it for a terminal fallback so a plain drag selects text there.

## Flags

| Command | Flag | Default |
|---|---|---|
| `gateway` | `--listen ADDR` | `0.0.0.0:7420`. If reachable on a public routable address, the gateway logs a warning at start and hourly, and surfaces it in `agent-net nodes` and the TUI status line. |
| | `--apps-listen ADDR` | Override the managed connected apps listener address, normally `127.0.0.1:7422`. Binds to loopback only. It serves the phone app at `/` and the bot MCP endpoint at `/mcp`. |
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
| | `--no-launch` | off. This machine refuses workers. Saved as `no_launch` in `node.json`. It stays until `--launch`. |
| | `--launch` | off. Accept workers again. |
| | `--gateway` | off. Also install and start the gateway here. On a fresh setup with Tailscale present, setup offers to bind to the Tailscale address instead. Existing setups keep theirs. |
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
| | `--owner-claude UUID` | none. Only workers owned by this machine's Claude Code session with that native id. |
| | `--recent` | off. Also list workers that ended in the last two minutes. |
| `uninstall` | `--purge` | off. Also remove credentials and daemon state. |
| | `--data DIR` | Gateway data directory used to find the recorded Funnel route. Defaults to `AGENT_NET_GATEWAY_DATA` or the standard gateway data directory. |
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

Each daemon checks installed harnesses daily, starting 10 minutes after it starts. It runs `claude update`, `codex update`, `opencode upgrade`, `pi update self`, and `agy update` for the installed programs. It waits for busy sessions or workers, retrying every 30 minutes until the next daily check. Each command has five minutes and runs without input. The daemon refreshes the inventory after each attempt. `agent-net nodes --wide` shows the result and versions; the graph shows failures. Antigravity (`agy`) is detected, updated and can run workers like the other harnesses. In Settings, open Integrations and turn off **Keep harnesses updated** to stop checks on every machine. This setting is on by default and is stored with the network integrations.

## What each machine reports

Each daemon sends two kinds of facts about its machine to the gateway. Agents and the CLI can read them.

**Repo info for each session.** When the daemon finds a session (any harness), it runs `git` in the session's folder (with a 3 second limit for all commands) and sends:

- `root`: the main working tree of the repository. Empty if the folder is not in a git repository. A linked git worktree has the root of the main tree it belongs to, found with `git rev-parse --git-common-dir`.
- `worktree`: the working tree the session's folder is in. It is the same as `root`, except in a linked git worktree.
- `project`: the last path part of the `origin` remote, without a trailing `.git`. With no usable origin, the main tree folder name is used. Outside a repository, the session folder name is used. Linked worktrees share their repository identity.
- The name of every session is its project identity plus a number ([0024](decisions/0024-session-names.md), [0033](decisions/0033-session-names-are-owner-only.md)). The identity is the last part of the origin remote when present, else the main working tree folder, else the session folder; a linked worktree uses the same identity as its main tree ([0031](decisions/0031-project-identity-and-display-name.md)). Two live sessions with the same name on the network get ` 2`, ` 3` (the lowest free number), and a worker is `<project> worker <n>`. A worker's TASK is the first line of its task. The name a harness gives the session (a Codex thread name, a Claude session title, an OpenCode session title) is not the name: it goes to TASK when the session announced no task.
- **Only the owner names a session.** An announce cannot: it sets a task, a project, capability tags and expertise, and a `name` in an older client's announce is ignored. The owner renames by approving the CTO tool `session_change` with action `rename` (`session_id`, `name`) or the operator method `session_rename`; an empty `name` returns the automatic project name, with the next free number if another live session took it. The name survives reports and is shown wherever an automatic name is. A rename to a name another live session holds is refused, because `agent_message_send` and `agent-net send` match exact names among online agents. The gateway migration at upgrade clears names that older agents had set through announce, so those sessions show their automatic name at the next report.
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

What the agent writes is the **announced task**, a free-text label. At the start of every session, and when the announced task changes, the agent must call `agent_announce` with that label in one short line (the guidance, the MCP server instructions, the tool description and the Pi and OpenCode system prompts all say so). The announce also takes a project, capability tags and expertise; `function` is ignored, and there is no `name` parameter (only the owner names a session, see above). A harness title is not a name: it goes to TASK until the agent announces a task label. Until a session has announced, every agent-net tool result ends with "Reminder: call agent_announce with your current announced task." The announced task shows in `agent_sessions_find`, `agent_session_get`, the TUI and as TASK in `agent-net list --wide`.

An announce may also carry `waiting`, the question the session waits to hear from you (see [the Inbox](#inbox-tab-7)). A later announce without it clears the wait.

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
- **Self upgrade.** A process of v0.3.5 or later normally switches to the new binary by itself and the session is then current. See [upgrade.md](upgrade.md#running-sessions). The note and the mark are for a session where that did not happen, or could not (a process from before v0.3.5, or `AGENT_NET_NO_AUTO_UPGRADE=1`). The switch itself does not need to be redone for the note: when a process asks for the switch it drops its note.
- **Reconnect.** In Claude Code type `/mcp`, pick `agent-net` and reconnect. This starts a fresh `agent-net mcp` from the installed binary, with the new tool list.

When an owner-only command (`agent-net start`, `send`, `stop`, `usage`, and the others behind the same check) runs inside an agent session, the refusal starts with `method "..." is not available from inside an agent session: run it in your own terminal.` and ends with the advice. For `start`: `agents start workers with the agent_worker_start tool, not this command. If you do not have agent_worker_start, your agent-net tools are out of date: ask your user to reconnect agent-net (in Claude Code: /mcp).` The other commands say: `agents use the agent-net tools, not this command. If a tool you need is missing, your agent-net tools are out of date: ...`.

If `git` is missing, slow or fails, the session is still reported, with fewer fields. The result is cached per folder (5 minutes for root, project and remote, 5 seconds for the branch), and the branch is read again when the session updates. `agent_sessions_find` returns these fields in each agent's `repo` object. `agent-net list` shows PROJECT, and `--wide` shows the rest.

**Installed harnesses.** At start and then every 6 hours the daemon looks for `claude`, `opencode`, `pi`, `codex` and `agy`. It reports the path and the version (the first line of `<program> --version`, 5 second limit) of each one it finds, and its model list (20 second limit for the whole read). OpenCode is asked with `opencode models` (one `provider/model` per line). Pi is asked with `pi --list-models`, and its table gives `provider/model`. Codex is asked through its app server (`codex app-server`, stdio) with the JSON-RPC method `model/list`, including hidden models and following `nextCursor`. Claude Code has no list command, so its aliases (`haiku`, `sonnet`, `opus`, `fable`) and the full names agent-net knows are a small table in the daemon. Antigravity (`agy`) is asked with `agy models`: a first line `Fetching available models...`, then one `<id><TAB><label>` per line. agy keeps the reasoning effort inside the model id (`gemini-3.8-flash-low`), so it reports no default effort per model. The agy list needs the network and a sign-in, so the same read is also its sign-in check: a list reports `signed_in: yes`, a message such as `not logged in` reports `no`, and a failed or timed out read leaves the state unknown. No account detail from it is kept. An empty OpenCode, Pi or agy model probe keeps the previous list and retries after one minute, doubling the delay up to 30 minutes until detection succeeds. A harness whose list cannot be read and has no previous list reports no list, and a loose model name for it passes through as typed.

A worker profile may name `agy`, and can start an Antigravity worker (decision [0049](decisions/0049-antigravity-harness.md), build step 4). An exact model id keeps its effort as written. The vendor in an agy model id (`gemini` to Google, `claude` to Anthropic, `gpt` to OpenAI) is used for display. A Claude model through agy is not your Anthropic subscription, so the protected-provider rule never matches it.

A daemon started by launchd or systemd has a short `PATH`, and it does not read `~/.bashrc` or `~/.zshrc`. So it searches, in this order: its own `PATH`, then `~/.local/bin`, `~/.bun/bin`, `~/.npm-global/bin`, `~/.opencode/bin`, the folders of Node version managers, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin`, `/bin`, and last the `PATH` of your login shell (`$SHELL -l -c`, or `/bin/zsh` on macOS if `$SHELL` is not set, 3 second limit). The login shell is never interactive: it reads your login startup files, not the interactive ones. The programs it runs get the search path as their `PATH`, so a script that starts with `#!/usr/bin/env node` works.

The version manager folders are: nvm (`$NVM_DIR` if set, and `~/.nvm/versions/node/*/bin`), fnm (`$FNM_DIR`, `~/.local/share/fnm`, `~/.fnm`, `~/Library/Application Support/fnm`, each with `node-versions/*/installation/bin`), volta (`~/.volta/bin`), asdf and mise shims (`~/.asdf/shims`, `~/.local/share/mise/shims`, `~/.mise/shims`), pnpm global (`$PNPM_HOME`, `~/.local/share/pnpm`, `~/Library/pnpm`) and bun (`~/.bun/bin`). With several Node versions, the one named by nvm's default alias (`~/.nvm/alias/default`) or fnm's default alias comes first. If the alias does not resolve, the newest version comes first.

A harness can be installed more than once, for example OpenCode v1 from its install script and v2 from npm. The daemon finds every copy and drops copies that are the same file (same real path after symlinks). It reports one entry per harness. The `path`, `version` and `models` fields describe the primary copy. This is the copy that comes first in your login shell's `PATH`, and then `primary` is `true`. If the login shell does not find the harness, the primary copy is the first one in the search order and `primary` is absent: the daemon cannot tell which one you run. The other copies are in `others`, each with `path` and `version`. Models are listed only for the primary copy. Both fields are new after v0.3.5 and are left out when empty, so v0.3.5 daemons, gateways and clients still work with them.

`agent-net nodes` shows the result. A machine on an older agent-net shows `unknown`. A machine that reports an empty list shows `none`. Agents get the same data from the `agent_machines_find` tool (Claude Code, Pi and OpenCode), including each machine's subscription quotas when it reported them.

## CPU and memory

Each daemon samples its machine every 5 seconds, keeps 5 minutes, and reports a summary to the gateway every 10 seconds. The gateway keeps the newest report per machine in memory only (it is gone after a gateway restart and back within 10 seconds).

- **Machine:** CPU as a percentage of all cores (0 to 100), memory used and total, load average (1, 5, 15 minutes), process count, each with the 5 minute average.
- **Session:** the process tree under the session's process: CPU as a percentage of one core (100 is one full core, a tree can show more), the sum of resident memory, the number of processes, each with the 5 minute average. A tree seen only once has no CPU figure yet (`...`).
- **Which process:** Claude Code, the session's PID. Pi, the process that connects the extension. OpenCode, the one service process that hosts all sessions: it is listed once as "shared by N sessions" and is never split. If one session starts another (a Claude Code session inside another's shell), the inner tree is also counted in the outer one.
- **Linux:** `/proc/<pid>/stat` (user, system and waited-for children time) and `/proc/stat`. CPU is the difference between two readings. The sampler costs about 5 ms per reading with 450 processes, about 0.1% of one core.
- **macOS:** `ps -A -o pid=,ppid=,rss=,time=,comm=` for processes, `sysctl` for cores, memory size and load, `vm_stat` for memory in use (app + wired + compressed, like Activity Monitor). Load and memory are read every 4th sample. CPU time from `ps` has a resolution of 10 ms and misses processes that start and end between two readings, so tree CPU can read low for a tree that spawns many short jobs. Machine CPU is the sum of live processes' CPU time over the cores, with the same limit. This was written and tested against fixtures of the `ps`, `sysctl` and `vm_stat` output; it has not been run on a Mac.
- **Top lists:** only on request, not in the report. Each process has its PID, short name, and the first 80 characters of its command line with tokens, `--password=`/`--token` values and `KEY=value` secrets masked.
- **Daemon methods** (owner only, from `hello` with `client: cli`): `usage` (this machine live, and all machines from the gateway), `usage_top {session_id}`, `graph_view {project}` (the component graph for the TUI Graph: nodes with charters, edges and the newest charter changes), `session_text {session_id, limit, before, after, sidechain}`, `session_enter {session_id, tmux_socket, herdr_socket}` (answers `{argv, dir, env, host, note, fallback, herdr_pane}`; the asking daemon adds `herdr_client` when this machine has a herdr binary, see [Enter a session](#enter-a-session)), `counts`. Gateway frames: `node.usage`, `query.usage`, `query.counts`, `query.graph`, `query.usage_top`, `query.session_text`, `query.session_enter` (and `node.usage_top`, `node.session_text`, `node.session_enter` to the machine that holds the session).
- **Counts:** `counts` returns `online`, `offline_24h` and `offline_total`. The gateway keeps offline sessions for 30 days, so the total is mostly old restarts. The TUI header shows the online count and the numbers the owner can act on (busy, needs you, the worker queue), and leaves the offline counts out.

## Subscription quotas

agent-net does not measure quotas itself. It reads [quota-axi](https://github.com/kunchenguid/quota-axi), a CLI that reports the subscription windows of your model providers from the logins already on the machine (decision [0022](decisions/0022-component-graph-and-cto.md)). You do not install it by hand (decision [0025](decisions/0025-install-quota-axi.md)).

Before using a copy, agent-net runs its `--version` command. A new selection requires version 0.1.66 or newer. Setup offers to upgrade an older owner copy and asks first. It records the selected path, version and reason in `quota-axi-choice.json`. On daemon start, discovery runs again and keeps that copy while it still runs, even if another copy appears. Exit 0 or 1 with valid JSON is used; exit 1 means every provider failed. Exit 2 reports that the selected copy rejected its options. Other failures include the first trimmed stderr line with recognizable secrets redacted.

1. **Install.** When the Quota integration is on and you have no compatible quota-axi, setup installs the pinned version with `npm install --prefix ~/.local/share/agent-net/quota-axi --no-fund --no-audit quota-axi@0.1.69`. The daemon does the same in the background at startup or when it applies a changed network answer, with a 3 minute limit. A failed install is tried again the next day. Node 22.19 or newer and npm are needed; agent-net never installs Node.
2. **Which copy runs.** For a new choice, a supported owner copy wins: the path in `node.json` `quota_axi`, the login-shell and process PATH, `which -a`, `~/.local/bin`, nvm bins, `/opt/homebrew/bin`, `/usr/local/bin`, and the global npm prefix. If none works, agent-net uses its private copy. Setup and the daemon record the choice and keep it while it runs. On macOS and Linux, setup copies exported XDG folder variables into the daemon service so quota-axi sees the same data folders as the setup shell.
3. **Check logins.** With a terminal, setup runs the selected copy's read-only `auth --json` and reports each provider as found, not found, or needing attention. On macOS it offers another run with a Keychain prompt; choose Always Allow if you want future reads to proceed without a dialog. The saved status contains no source paths or credential values. A found source does not prove the provider will accept it. Without a terminal, setup skips the check. To run it later without repeating the rest of setup, use `agent-net setup --quota`.
4. **When it runs.** The daemon runs `quota-axi --json --max-age 5m --no-credential-refresh` at start and every 5 minutes, with a 90 second limit. It never passes `--full`. The child PATH starts with the quota-axi folder and its Node folder, then the same search folders the daemon uses for the harnesses (including `~/.local/bin`), and HOME and USER are filled in when the service environment lacks them, so quota-axi finds the provider CLIs and reuses the readings cached by the owner's terminal. The integration is on by default; turn it off in the integrations question, with `agent-net setup --no-quota-axi`, or in the TUI.
5. **What is kept.** One row per provider and window: window id, label and kind, percent used, reset time, pace (`ahead`, `on_pace`, `behind` or `unknown`) and whether the window is exhausted now. The newest good reading stays when a later run fails. It travels with the machine's usage report (`node.usage`, field `quotas`) and the gateway keeps the newest one in memory, like CPU and memory.
6. **Model choice and admission.** A loose model name skips a provider that is exhausted, or above 95 percent of any window. An exact model id still works, and the start result says why in `model_note`. A provider ahead of its reset clock ranks below providers on pace. A start whose model's provider is exhausted is refused before any model call, with the provider, the limiting window and its reset time.
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
| `alibaba` | `alibaba`, `qwen` |
| `minimax` | `minimax` |
| `mimo` | `xiaomi`, `mimo` |
| `deepseek` | `deepseek` |
| `openrouter` | `openrouter` |
| `commandcode` | `commandcode`, `command-code` |

A provider without a row (for example `elevenlabs`) has no model provider in agent-net. A model id with no provider part is mapped through its harness, which is why the Claude Code and Codex harnesses are named above.

A worker model may be a registered profile name, ignoring case. It uses the profile's harness, model and effort. Without a harness, an explicit model prefers a signed-in harness over one whose sign-in state is unknown and skips harnesses known to be signed out. The result's `model_note` names the choice. A worker that fails with HTTP 401, HTTP 403 or "not logged in" blocks that harness and provider on that machine until a later worker succeeds on the same provider.

A model provider that is not a subscription and has no usable quota reading is pay-per-use. This includes OpenRouter and OpenCode Zen (`opencode/`). An explicit worker model never resolves to one from a loose or bare name; write the full provider id, for example `opencode/gpt-6.1-sol`, and the start result says `uses pay-per-use provider (opencode)`. The same rule decides the worker provider defaults (decision [0026](decisions/0026-worker-difficulty-and-scores.md)).

## Tools inventory

Tab 5 opens Skills per project: projects on the left, skills and checkboxes
on the right. The project list comes from `tools_projects`, which includes
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

A project skill item has `tracked: true` when `git ls-files` lists files under it in the project checkout. The daemon runs one `git ls-files` per project root in each scan (limited to the four project skill folders, no network) and uses the answer for every skill in that root. A folder that is not a Git repository, or a failed `ls-files`, leaves `tracked` off. A scan from a daemon that is older than this field has no `tracked`, so its project skills show as `local` until it is upgraded.

The owner can request a refresh through `inventory_scan`. `inventory_get`
returns report pages with an `offset` request field and a `next` cursor.
`tools_items` returns item pages with optional `kind`, `harness`, `project`,
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
an approved CTO `mcp_change` proposal, starts global. A server that existed
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
can plan and apply; the CTO can propose these calls for approval. An agent session cannot.
The CTO proposes `tools_change` with action `plan` or `apply`; it cannot read the owner-only tools inventory.

What a session sees:

- `global`: every session, including a session with no project.
- `projects`: only sessions whose project (the PROJECT column) is in the list.
  A session with no project does not see it.
- `uninstalled`: no session. The server keeps running on its host; the owner
  can still test it and change the scope.

The tool list of a session (the daemon method `mcp_tools`) holds only the
servers it may use. The gateway reads the project from its own record of the
session. Applying or undoing an assignment makes the gateway send `mcp.changed`
to every daemon, and each daemon sends `mcp_changed` to its sessions. Claude
Code gets `notifications/tools/list_changed` only when its tool list really
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
copies in every other known project in one plan. Turning the last project off
makes it uninstalled. Turning every known project on uses global placement
when its new-projects choice is on. The owner chooses projects only.
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
The CTO proposes `tools_change` actions `toggle` and `new_projects`; the owner
approves them through the usual proposal gate.

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

The owner can import an observed skill with the `tools_import` operator method.
Pass `item` as the ID from `tools_items` and `location` as an object with
`node_id` and `location` from that item's locations. The source machine must be
online. The gateway refuses recognised secret shapes before storing a bundle.
The owner should still inspect the skill files for secrets before importing.
Import reads the selected directory and does not change it. The gateway rejects
an import if its contents no longer match the observed hash. The packed bundle
must be at most 2 MiB and may contain at most 2,048 entries. A larger skill can
still appear in inventory but cannot currently be imported.

`tools_versions` takes `item` and returns the saved hashes, Git commits, source
machine and path, and import times. `tools_export` takes `item` and `version`
(the content hash) and returns a JSON object with a base64 `data` field holding
the tar bundle. Export verifies the bundle hash. Import, version listing and
export require the owner connection. Reimporting the same content hash for an
item returns the existing version. A saved version remains available when its
observed locations disappear.

**Version numbers.** Each new content hash that is imported for a skill name gets the next number: v1, v2, and so on. The number is stored with the version (column `number` of `skill_versions`), never changes, and is counted per skill name in import order. An upgrade fills in the numbers of existing versions in import-time order. `tools_versions` returns it as `number`. `tools_items` shows it as `version_no` on an item and on each of its locations; `0` or a missing field means no stored version has this content. The Tools tab writes a stored version as `v3`. A copy of a skill that has stored versions, but whose content is none of them, reads `changed` (it was edited outside agent-net). A skill with no stored version at all shows a short content hash.

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

**Agent graph read.** `agent_graph_read` (Claude Code, Pi and OpenCode) returns the graph that every agent may read, read-only: every node's charter fields with its maturity, every node's rules with name and value, and every edge with its kind, mode and request rule. The rules are the six node settings resolved against the node's maturity (`tests_before_merge`, `merge_authority`, `worker_sandbox`, `judge_consumers`, `deploy_rule`, `autonomy` (`off` or `wake`)) and the delegation policies the owner set for the node's project on each machine. `node` returns that one node's full charter, its rules and the edges that touch it. Without `node`, `offset` and `limit` page the list (default 20, at most 50), `total` counts the matching nodes, `next` is the next page offset, and `edge_total` counts the edges. Charters and rules are public; only changes are owner-gated. Secrets and tokens, machine paths and locations, learned-change history with evidence, hidden items and sessions stay hidden. The CTO's `graph` view uses this same read with the CTO caller.

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
- **Who starts one:** you (`agent-net start`), an agent (`agent_worker_start`), or the CTO through a proposal you approve in the Inbox. A worker cannot start workers. The harness is `opencode` (the default), `claude`, `pi` or `codex`. A caller can say "implement this with a Claude, Pi or Codex subagent".
- **Where:** the project is looked up in `agent-net projects` (registered or seen). With no project, the caller's own folder is used: it is found by its repository root path on the caller's machine, not by name; a caller outside a known project falls back to its folder name. `project` may also be an absolute path: it matches the main tree or a listed worktree of a known project (use it when two folders share a name). A name matches a registered folder before seen-only folders of the same name on the same machine; it is ambiguous only between two registered folders, or between seen-only folders with none registered, and the refusal lists the paths. If it is on two machines, name one with `--machine`. The machine must be online, accept workers (not set up with `--no-launch`), and have the harness (`agent-net nodes --wide`). The gateway checks the model against that harness's list on the machine. A harness whose list could not be read passes the name through, and a model the harness does not know then fails at start with the harness's own message. The gateway says which check failed.
- **Worker permission levels** ([decision 0020](decisions/0020-worker-sandbox.md)). The owner sets a level per project (TUI key `p`, line Workers; the operator method is `session_policy` with `{"policy": {"workers": "read-only"}, "scope": "project"}`, owner only, refused at session scope). The level is stored in the project policy and sent with each start; `worker_start` answers it as `permissions`, and `list --json` shows it in the worker part of the session. When the project policy sets no level, the node's maturity default applies ([maturity and its defaults](#charters-and-component-relationships)); a node with no maturity keeps `sandboxed`. Today Codex and Antigravity workers enforce permission levels with their own sandboxes. Claude Code, Pi and OpenCode workers run with the owner's permissions; their recorded level is not enforced yet.

  | Level | Writes | Network |
  |---|---|---|
  | `sandboxed` (default) | The worker's worktree, the temp folders, and `objects`, `refs`, `logs` and `worktrees/<name>` of the main repository's git dir (what a commit needs) | on |
  | `no-network` | The same | off |
  | `read-only` | None | on |

  The project list includes each project's `permissions` level. The start result, the worker brief and worker notices show it too; the brief explains when writes or network calls will fail. The start result and brief say when the level is not enforced. A failed or blocked Codex turn with an explicit sandbox denial reports `blocked by the sandbox (<level>)` in the notice. A command's ordinary permission or network error alone does not prove a sandbox denial.

  **Permission enforcement.** Antigravity workers use `--mode accept-edits --sandbox` and a one-run allow list for `command(*)` and `mcp(agent-net_agent-net/*)`. The daemon finds Go on the harness search path, or else in a standard install folder (`~/.local/go/bin`, `~/go/bin`, `/usr/local/go/bin`, `/opt/homebrew/bin`, `/usr/local/bin`), and runs `go env GOROOT GOMODCACHE` with the worker's environment (five second limit). It adds each existing folder with `--add-dir`, except home itself and its parents, including symlink aliases. Antigravity workers can write to these toolchain folders; the permissions line and daemon start log list them. If Go is missing or the probe fails, no toolchain folders are added. Read-only workers use `--mode plan --sandbox` and only the MCP allow rule. The no-network level is not enforced for Antigravity. See [Antigravity workers](antigravity.md#workers) for the exact command.

  The Codex worker is started with approval policy `never` and the sandbox of its level (`thread/start` and `thread/resume`: `approvalPolicy`, `sandbox`, `config.sandbox_workspace_write`; `turn/start`: `approvalPolicy`, `sandboxPolicy`). Its private app server also gets `-c mcp_servers.agent-net.enabled=false` when `$CODEX_HOME/config.toml` defines the agent-net MCP server, so it does not call agent-net tools (its brief does not mention them either: the end of its turn is the report). Other MCP servers stay. The daemon still declines any approval that arrives. Claude Code, OpenCode and Pi workers record the level, and the TUI detail pane shows it on a `sandbox` line with "not enforced yet", but they behave as before.
- **How, per harness.** All run in the project folder (or the worker's worktree), and none gets a flag that turns permission checks off: a worker uses the project's own permission settings.

  When herdr is available, Claude, OpenCode and Codex workers open a named view on the machine that runs them after registration, including on resume. A herdr client must be attached there to see it; use Enter to open a remote worker from the local TUI. If herdr is unavailable, an open TUI on that machine shows an installation hint and the worker continues in the background. Pi workers use RPC mode and have no interactive terminal to open; `agent-net show <session>` reads the conversation.

| Harness | Command | The model | The task and the report | Enter it later |
|---|---|---|---|---|
| OpenCode | `opencode serve` on `127.0.0.1`, random password | `provider/model`, checked against the machine's list | A normal message. The worker ends with `agent_handback`; the owner's next message starts the same session again. | `opencode --session <id>` |
| Claude Code | `claude --bg --name worker-<project>-<id> --settings <json> --mcp-config <json>` (a background session of Claude's own service, not a child of the daemon) | An alias (`haiku`, `sonnet`, `opus`, `fable`) or a full name, after `--model` | A normal message, delivered to the session's inbox. The worker ends with `agent_handback`, through the agent-net MCP server of this daemon's binary; the owner's next message runs `claude --bg --resume <session-id>`. | `claude attach <id>` (the id is the `jobId` in the session file) |
| Pi (needs `--isolation worktree`, or the daemon refuses it) | `pi --mode rpc --session-id <uuid> --name "worker <project>"`, standard input a named pipe that never closes | `provider/id[:thinking]`, after `--model` | A normal message, through the Pi extension, which loads in this mode. The worker ends with `agent_handback`, and it cannot be started again (Pi's rpc mode cannot open an existing session). | Not possible: Pi's rpc mode has no screen. Read it with `agent-net show`, or resume its session file in Pi after the worker ended. |
| Codex | `codex app-server --listen ws://127.0.0.1:<port> --ws-auth capability-token --ws-token-file <file>` | A name, in `thread/start`, with an optional `:<effort>` (`low`, `medium`, `high`, `xhigh` or `max`) sent as `effort` on every `turn/start` | The daemon runs each message from the owner or the person as a turn. The last message of the turn is the report, handed back by the daemon; the owner's next message starts a new app server and `thread/resume`s it. Approval policy `never` and a sandbox by [level](#workers). | `codex resume <thread id>` in the project folder |
| Antigravity | `agy --input-format stream-json --output-format stream-json --model <id> --mode accept-edits --sandbox --settings <json> -p=` | A model ID from `agy models`, including its effort | One stdin JSON line per turn. The plugin calls `agent_handback`; usage comes from the result event. Follow-ups reuse the process or add `--conversation <id>` after it exits. | `agy --conversation <id>` in the session folder |

  Claude Code needs the folder to be trusted (run `claude` in it once, or use a worktree of a trusted repository). The Pi extension and the OpenCode plugin must be installed on the machine (`agent-net setup`). The Claude session carries `crossSessionInbound: "accept"` and `AGENT_NET_SOCKET` in its per-session settings (`--settings`), so inbound agent-net messages are accepted; your `settings.json` is not changed.

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

- **Difficulty and automatic model choice.** The level comes from the difficulty judge, which reads the brief before the start and returns `trivial`, `simple`, `normal`, `hard` or `very_complex` from its six scores. The caller's `difficulty` and `touches` fields are accepted for older callers but ignored: neither changes the level. The judge has 60 seconds and never blocks a start; a missing judge, a failure, a timeout or low confidence starts at `normal`, and `model_note` says why. A continue keeps the label of the run it continues unless the caller adds work to the brief. With an empty model or `auto`, the gateway chooses a registered worker profile whose harness and model are available on the selected machine. The default minimum scores are 0, 20, 30, 40 and 50. A score at least 10 above the minimum is excellent; one at or above the minimum is good; one up to 10 below is poor; a lower score is denied. An automatic candidate must meet the minimum, and the task must be at its max level or exactly one level below. Its max is the highest level whose minimum its score meets, including owner overrides. A max of `very_complex` therefore qualifies only for `hard` and `very_complex`. Over-qualified profiles get no weight. If none qualify, the start fails with the judged level, registered profiles and their max levels, and the fix: register a profile for that level, or pass `model` explicitly. Unprotected providers come first. A model with fewer than half complete among at least five complete, partial and failed runs in the recent track record is used only when no other adequate candidate remains. The remaining models rotate with a smooth weighted round robin: a candidate gets more weight when its provider is further behind its quota pace, when it is the weakest adequate candidate rather than far above it (an over-qualification penalty of `exp(-(score - weakestAdequateScore) / 8)`, which keeps stronger models available for harder work), and when its track record is good. A recent success adds weight and a recent failure lowers it, only on the same node and harness. It skips exhausted providers and windows above 90 percent, and subtracts five quota-room points per running worker on the provider. For a monthly window, it estimates elapsed time from the preceding calendar-month reset. A missing or stale quota reading is neutral. Every eligible candidate in the selected track record tier keeps a positive weight. The rotation state is kept per difficulty in the gateway's `owner_settings` (`worker_rotation`) and survives a restart. The result's `model_note` explains the choice, its rotation weight and the shares of the last ten starts. An empty roster also fails with registration guidance. Explicit model requests override the level eligibility rule and keep the resolution rules below; `model_note` says when this override was needed. Automatic notes name profiles excluded for being more than one level above the task.
- **Ignored fields.** `agent_worker_start`, the CTO's `worker_start` and the CLI still accept `difficulty` and `touches`, so older callers do not fail. Both are recorded, but neither changes the level: the difficulty judge decides it. `touches` still feeds the per-area tables in the quality analysis, and `db`, `hot-path`, `ui`, `docs`, `tests`, `config` and `other` are the accepted values. The old one-level bump for `db` and `hot-path` and the one-level drop for `docs`/`tests` only are gone.
- **Worker profiles.** The gateway stores a network roster with each profile's name, harness, model, optional effort, creator session and creation time. `agent-net workers profiles` lists it; `agent-net workers profiles add <harness> <model> [effort]` adds an entry, and `agent-net workers profiles remove <name>` removes one. The CTO lists profiles with `settings` and changes them with `settings_change` areas `worker_profile_add` and `worker_profile_remove`. Non-worker agent sessions have `agent_worker_profiles_find`, `agent_worker_profile_add` and `agent_worker_profile_remove`; they should change the roster when the owner asks. Model names can be loose and must resolve to a model advertised by a machine for that harness. The default profile name combines the harness, resolved model and effort. Adding a profile with an existing name replaces its harness, model and effort in one call; the change is recorded as a profile add.

  Each machine resolves the exact model ID first. If that ID is missing, it can use the same model name after the first `/` under another provider, but only when exactly one provider lists that name. Several matches make the profile unavailable on that machine. Automatic selection skips the profile only on machines where it does not resolve. A start result reports a provider change in `model_note`, for example `terra pi: openai-codex/gpt-6.1-terra resolved to openai/gpt-6.1-terra on research`.

  The profile list includes `machine_availability` for online machines reporting the profile's harness. Each entry has the machine name and whether the profile resolves there. `resolved_model` is included when the machine uses a different model ID. Failed entries include up to three `closest` model IDs; `ambiguous` lists all IDs that share the model name under multiple providers. `available` is true when at least one online machine resolves the profile, and false when none do. `machines_checked` names the machines checked. Profile-level `closest` lists up to three alternatives when the profile resolves nowhere. If no online machine reports the harness, availability is unknown and `available` is omitted.
- **Worker providers.** Automatic selection uses the owner-controlled provider allowlist. When it is unset, Claude and Codex harness models, OpenCode and Pi models from `opencode-go` or `openai`, and providers with a usable quota reading are allowed. Other providers, including OpenRouter and OpenCode Zen, are excluded by default. `agent-net scores providers` displays the setting; `agent-net scores providers codex claude opencode-go` replaces it, and `agent-net scores providers --default` restores the defaults. The CTO can change it on the owner's instruction. An explicit model name keeps the resolution rules below: a loose name only matches subscription-backed models, and a pay-per-use provider is used only when its full provider id is written. The Quotas tab lists registered profiles and marks those excluded by this filter. A recent successful worker adds rotation weight.
- **Protected providers.** The gateway stores the owner setting `protected_providers` and its `reason` in `owner_settings`. The owner orchestrates with one model of choice, today often Claude on an Anthropic subscription. Workers on that same subscription spend the limit the orchestrator needs to keep working. Automatic choice uses other subscriptions first and uses a protected provider only when no other available profile qualifies under the max-or-one-level-below rule or the others are out of quota. The same difficulty, track record, quota, score and rotation rules choose among the remaining profiles. The result explains protected profiles skipped or used, including the reason for a fallback. An explicitly requested model is not blocked by this setting and the result notes when its provider is protected. Claude harness and unprefixed `claude-*` models map to `anthropic`; Codex `gpt-*` maps to `openai`; OpenCode and Pi model prefixes identify their providers.

  In the TUI Quotas tab, select a subscription with up/down and press `p` to protect or unprotect it, then answer `enter: yes · esc: no`: Enter or `y` protects, Esc or `n` leaves it unchanged. Press `s` to show subscriptions that are folded away. The CTO can propose replacing the list and reason; it cannot read them. For scripts, use `agent-net workers protect anthropic` or `agent-net workers unprotect anthropic`. The `protected_providers_get` and `protected_providers_set` operator methods are owner-gated: agent clients and command lines inside an agent session's process tree are refused. The Quotas tab marks protected profiles and subscriptions with a lock. Setup does not protect a provider and does not teach the protect command: the TUI teaches it when the owner starts using workers.
- **Scores.** The bundled table contains 85 model and effort scores from [Artificial Analysis](https://artificialanalysis.ai/). An effort suffix selects that effort; for a bare model the medium entry is used when no harness default is known, falling back to a default entry. Unknown scores are not candidates for automatic choice. Owner overrides win: `agent-net scores` lists scores, `agent-net scores set <model> <score>` adds an override, and `agent-net scores clear <model>` removes it. The CTO proposes an override with `settings_change` area `score` on the owner's instruction. The owner-only `difficulty_get` and `difficulty_set` operator methods read and replace the difficulty minimum overrides.
- **The task** is sent as a message from the caller, after the fixed brief. For Claude Code, Pi, OpenCode and Antigravity the worker ends its work with `agent_handback(report)`. For Codex the daemon does it when a turn ends: status `done` for a finished turn, `failed` for a failed or interrupted one, and `blocked` when Codex asked for an approval (the daemon declines every approval, which should not happen now that the worker runs in a sandbox, and the report names the command, the reason and the Codex thread to open). The caller gets one message with the facts (status, run time, model, tokens, the context at the end, the cost if the harness reports it, the worktree and branch, `git diff --stat`) and the report below them. Tokens come from OpenCode, from the Claude transcript, from Codex, from Pi's session file, and from Antigravity's result events. Cost comes from OpenCode only. The context at the end is the tokens of the last assistant message or turn (input plus cache reads and cache writes) and the model's context window. A worker that ends without a hand-back sends the same notice with the status `timed out`, `failed` or `stopped`. A worker that nobody messages for the idle time (60 minutes by default) ends as `timed out`; one that runs longer than 4 hours ends as `timed out` too. A worker is not idle while its harness says its session is busy, or while its token count grows: a long task does not hit the idle limit. An OpenCode worker whose model call fails (a provider, billing or auth error) ends at once as `failed`, and the note holds the error text (one line, secrets masked, cut to 300 characters). The daemon looks at the OpenCode session every few seconds: a run with outcome `failed` ends the worker. Only recognized provider errors demote the model; local and unknown failures do not. Codex reports a failed turn as a hand-back. Claude Code and Pi workers have no such check yet: a model error there shows only as the idle time-out.
- **Idle without a hand-back.** When an OpenCode or Antigravity worker ends a turn without `agent_handback` and stays idle for 2 minutes, the daemon first sends it an automatic continue (up to the limit; see below) instead of telling its owner. A worker that was already continued twice gets a notice with status `idle without hand-back`. The worker stays running. The notice includes its worktree diff, run time and last assistant message, marked as a last message rather than a hand-back, followed by the usual checklist, and says how many automatic continues were sent. The daemon sends one notice per idle period, including across a daemon restart; a new turn starts a new period. Codex normally hands back automatically when its turn ends. If that automatic hand-back fails, the daemon keeps the worker running and sends this idle notice instead, after the same automatic continues. Claude Code and Pi do not use this notice. Set `workers.idle_notice` in `node.json` to change the delay.
- **Complete or partial hand-back.** `agent_handback` takes `complete`, a boolean that defaults to true. A worker that did only part of its task calls it with `complete: false`, and the run is counted as partial. The brief tells every worker to set it honestly. A Codex worker has no tools, so its brief asks it to end its report with `complete: yes` or `complete: no`; the daemon reads that last line. A Codex turn that failed or was interrupted counts as failed, whatever the line says.
- **Automatic continues.** A worker that stops early gets another chance without the caller's help. When it hands back with `complete: false` and status `done`, or goes idle without a hand-back, the daemon sends it one message from the reserved agent-net sender: `[agent-net] continue: your task is not finished. Carry on until it is done, then hand back with complete: yes, or say exactly what blocks you.` The caller is not told yet. This happens at most twice per worker task. After the second automatic continue, or when the worker hands back `blocked` or `failed`, the caller gets the normal notice, whose note says how many automatic continues were sent. A worker that was stopped, failed, timed out, or blocked on an approval is never continued automatically. `agent_worker_start` and `worker_start` take `auto_continue` (default true); set it to false to be told at the first stop. The setting is stored with the worker and a queued start keeps it. The automatic continues are counted apart from the caller's counters (`auto_continue` on the worker record, `auto_continue_run` in `worker.stopped`), so they never count as caller rework in the difficulty calibration.
- **Follow-up messages.** A worker ends when it hands back. A message from its owner to the same session ID starts it again on the same harness session, with the same model, project and worktree, and delivers the message. That is `agent_handback` for OpenCode, Claude Code, Pi and Antigravity, and the end of a turn for Codex. OpenCode, Claude Code and Codex can open the saved session again (a new private server on the same folders, `claude --bg --resume <id>`, or `thread/resume`). A Pi worker cannot be started again: Pi's rpc mode cannot open an existing session, and the refusal says so; read the old session with `agent-net show`, or open its session file in Pi. Only the owner can start a worker again; another sender gets a clear refusal. A Codex worker takes messages from its owner and from the person at the machine only: the daemon turns each into a turn, one after the other, and refuses other agents.
- **Rework counters.** `continue` counts caller messages after a partial hand-back or an idle-without-hand-back notice. `fix` counts caller messages after a complete hand-back. Every caller message continues to count until the next hand-back. Automatic continues are kept separately and do not count as caller rework. The counters survive daemon restarts and appear in end notices and the model track record.

  The gateway stores one `worker_runs` row per ended run. `task_id` is the root session of a continued task; an empty value means the row's own `session_id` is the root. Resuming the same session or starting another worker with `continue` keeps the same task. Starting a continued worker adds one continue if the old run was partial, otherwise one fix. Calibration and quality add the counters across all sessions in the task and use the root's first run for caller, model, harness and difficulty. They omit a task only when all its runs were stopped. Older runs may have unknown counters, so their stored zero can miss rework. Defects on any session are added to the task.

  `midrun_count` counts caller requests after the first brief when no done, partial, failed, timed out or idle-without-hand-back notice was sent since the previous caller request. It is independent of the existing continue and fix counts. It is stored per run and summed into the quality family row as `midrun`; it has weight zero and is not shown in the panel or included in headache minutes. The daemon retains the count and notice state across restarts and queues.

  At gateway startup, one backfill recovers old continue and fix counts from retained messages, and a second links continuation briefs and counts mid-run requests. Each backfill commits its changes and its `owner_settings` marker (`rework_backfill_done` or `task_rework_backfill_done`) together. The latter matches a continued brief to an earlier brief from the same caller and resolves chains to their root. Expired messages cannot be recovered. The backfill stores counts and IDs, not a copy of the message text.
- **The satisfaction ask.** The counters see whether work came back, not how good it was, so agent-net asks the caller now and then. After a worker's final end notice, the gateway asks for every 5th finished worker of each caller, and for the first 3 finished tasks of a model and effort that has fewer than 5 ratings. The ask is one line added to the end notice the caller already gets: `rate worker <session> (<model>, <difficulty>, <run time>) with agent_worker_rate: 1 to 5 for the result, and one line on why`. The caller answers with `agent_worker_rate(worker, score, note)`. The score is 1 to 5 and the note is optional; it is cut to one short line and secrets are masked. A rating is stored with the run's model, harness, effort, difficulty and caller, never with the task text. Only the session that started the worker may rate it, and each worker takes one rating; a second rating, or one for another session's worker, is refused. A missing answer is not a low rating. Ratings appear as a count per model in the Quality analysis panel and feed the cached quality analysis described below. Rotation weighting stays off until the owner enables it.
- **A defect found later.** A defect found hours later, after the hand-back or after merge, is not lost. The caller reports it with `agent_worker_rate` and `kind: "defect_found_later"`, the worker's session ID, a severity from 1 to 4 and one line on what is wrong; the owner can report one from the Messages tab, with `f` on a worker conversation, where the prompt asks for the note and then the severity. A defect report has no score, needs no new message to the worker and may be sent more than once: each report is one defect, and each adds its severity in points of rework to that worker's task in the calibration medians. It may follow a rating. The note is cut and masked like a rating note. Only the session that started the worker, or the owner, may report one.
- **Ending:** a hand-back (any status) ends the worker after the notice, like a Claude Code subagent. Messages sent while a Codex or OpenCode worker is running wait for the current turn, and the worker ends after the last queued turn hands back. The idle time and the longest run time are safety nets: no message to or from it for 60 minutes while its session is not busy and its tokens do not grow; 4 hours since the start; its owner ended or stayed offline for 10 minutes; `agent_worker_stop` or `agent-net stop`. For Claude Code the daemon runs `claude stop <id>` first, then kills the process group if the process is still there. The stopped session stays in `claude agents`, with its conversation, until you `claude rm <id>`.
- **Unfinished work is kept.** When a worker ends with no hand-back (`stopped`, `failed`, `timed out`, `owner_gone`, or lost while the daemon was down) and its worktree has uncommitted changes, the daemon runs `git add -A` and commits them on the worker's branch before the worktree is cleaned up, with the message `agent-net: unfinished work of worker <id> (<reason>)`. The author is the repository's own `user.name` and `user.email`; where the repository has none, it is `agent-net <agent-net@localhost>`. Signing is off for this commit, and it carries the worker's [commit identity](#workers) trailers when the daemon still knows them. The notice says `unfinished changes were saved as a commit on branch <branch>`. A worker that was lost while the daemon was down gets its commit when the new daemon finds it lost, at start. A worktree with no changes is eligible for cleanup as soon as the worker ends. A worker that handed back is not touched when it ends: its changes stay uncommitted in the kept worktree, and are committed only if someone continues that worker.
- **Continue (`continue`).** `agent_worker_start` (and `worker_start`) accept `continue`: the session ID or worker ID of an ended worker that the same caller started. The gateway sends the request to the machine that ran the old worker, and says so clearly when that machine is offline. That daemon checks the owner against its record of the worker, and starts a new worker there: same project, harness and model unless the caller gives others, a new worktree on a new branch made from the old worker's branch (if an unchanged continuation branch was removed, it is restored from its recorded starting commit when another worker branch points there or that commit contains work outside the current project HEAD; otherwise from the project's current commit), and the old worker's full first task (the record keeps up to 64 KiB of it in `daemon.db`), after this line: `You continue the work of an earlier worker that ended (<reason>). Its unfinished changes are already in your worktree as the last commit. Review them critically, then finish the task.` An optional `task` is added as `Also: ...`, and an optional `title` replaces the old worker's title (empty keeps it). `project`, `machine`, `base` and `isolation` must be empty (a mismatching `project` is refused). Only the owner can continue a worker; errors say if the worker is unknown, still running, or not yours. An explicitly requested model is matched against the machine's model list as on a normal start, with its `:effort` or `:thinking` suffix if it has one. It works while the record of the old worker exists: until the longest run time after the worker started (see the worker list below). A worker that worked in the project folder (isolation `none`) is continued in the folder, and a Pi worker still needs its worktree.
- **Daemon restart.** The daemon reconnects to surviving workers and keeps their session IDs. It resumes a Codex turn and restores the OpenCode server connection. If it cannot restore a worker, it stops the worker, saves unfinished work on its branch, and sends its owner a `lost after daemon restart` notice.
- **The worker list.** `agent-net workers` and the TUI show only workers that run. A worker that ended leaves the list, and its record is kept (ended) until the longest run time: a hand-back record is what starts the session again, and every ended record also tells the daemon that the harness session is a worker's, so the plugin of another OpenCode server may not register it as a normal session. A record that is offline and started longer ago than `max_age` is cleaned: on the machine at daemon start and every 10 minutes, and on the gateway (`worker.cleanup`), so an old worker does not stay listed for weeks. The cleanup never touches the record of a worker the machine still runs or has adopted: the daemon sends the session IDs it runs with `worker.cleanup` (and runs the cleanup only after it has taken its workers back), and the gateway keeps those. A worker that comes back after a daemon restart registers under the session ID it had, even if its record was lost. A worker that the machine says it does not run leaves the list at once.
- **Difficulty assessment.** Before the gateway chooses a model, the daemon asks a judge to score the brief. The judge's label is the level the picker uses. The judge has 60 seconds and cannot block a start. OpenCode with `opencode-go/deepseek-v4.1-flash` scores first, with up to 39 seconds reserved for its run. If it times out or its reply cannot be parsed, and at least 20 seconds remain, Codex with `gpt-6.1-sol` at low effort gets the remaining time. The retry is skipped when Codex is unavailable. The stored judge identifies the scorer, and `judge_note` in its reasons records the first failure. A missing judge, a failure, a timeout or low confidence starts at `normal`. No separate `status: difficulty` notice is sent for a start that was assessed before it; the start's `model_note` begins with the result, for example `difficulty R2 S1 C1 U1 V1 K1 · D 1.20 · trivial -> opencode deepseek (32.5, good enough)`, or `difficulty: no assessment (the judge reply did not parse: ...); started at normal`. The result also has `owner_line`, one sentence for the caller to pass on to its user, for example `Tell your user: worker on gpt-6-luna:xhigh, difficulty simple (D 1.75; R2 S1 C2 U2 V2 K1).`; the guidance tells agents to say it. A continued worker has none. The Claude workers panel shows the same level and D on each worker row, for example `simple 1.75`. The gateway stores the scores and the label with the worker record, so the worker's detail shows the same line. The judge also runs for an explicit `model`, where the label is recorded but the model is not chosen. Queued starts are assessed once when they are requested and keep the label through the queue. A continue reuses the label of the run it continues unless the caller adds work to the brief, and a resumed run is assessed in the background.

  `agent_workers_find` with `detail: full` and `agent-net workers -json` include an `assessment` object when available. It has the six named scores (`reasoning`, `scope`, `coupling`, `uncertainty`, `verification`, `risk`), `d`, `label`, `rules_version`, `picker_label`, `bottleneck`, `confidence`, `judge_harness`, `judge_model`, `reasons`, and `error` on failure. It includes the run and session IDs, `started_at` in Unix nanoseconds and `assessed_at` in Unix seconds. The TUI worker detail shows the scores, label and picker label. The daemon queues completed assessments locally if the gateway is unreachable and retries them on reconnect. The gateway keeps one row per run, including failed assessments. A continued or resumed run gets a new row; the worker view shows the latest start's result once it arrives.

- **Worker title.** `agent_worker_start` (and CTO `worker_start`) take an optional `title`: one line of at most 60 characters, with no newlines. The daemon and the gateway keep it with the worker (`WorkerInfo.Title`, and a `worker_title` column on the gateway's session record), and a continued worker keeps the old title unless the caller gives a new one. Every place that lists a worker shows it: `agent-net workers --json` (`title`), `agent_workers_find`, `agent_worker_start`, the sessions and messages tabs of the TUI, the worker notices, and the Claude Code band. Without a title, the first sentence of the brief that is not setup is used: sentences and lines starting with `Repo:`, `Base:`, `Branch:`, `Worktree:`, `Project:`, `Setup:`, `Environment:`, `Read AGENTS.md`, `Read docs/`, `Read the docs`, `Read README` or `Read CLAUDE.md` are skipped. The sentence is cut to 60 characters at a word boundary with an ellipsis; a brief that is only setup falls back to the worker's name. The plugin needs no rule of its own because the CLI fills the field.
- **Context and tokens.** Each worker carries `context_tokens` (what the model saw on its last step: input plus cache reads and cache writes), `context_limit` (the model's context window) and `tokens` (input plus output plus reasoning for the run, without cache reads). The daemon refreshes them on its worker tick while the worker runs, and with the final notice. OpenCode reads the newest assistant message and `limit.context` from `/api/model` on the worker's private server. Claude Code reads the last assistant usage in the transcript and takes the limit from a small table by model family (the 5 series, Opus 4.7 and later, Fable 5 and Mythos 5 have 1M; other Claude models have 200k). Codex reads `last` and `modelContextWindow` from `thread/tokenUsage/updated`. Pi reads the worker's JSONL session file under `~/.pi/agent/sessions` (or `PI_CODING_AGENT_SESSION_DIR`): the last assistant message gives the context and the entries give the run total. It takes the model's context window from `pi --list-models`, once per worker. The figures are in `agent-net workers --json` (also with `--recent`), on the worker's TUI detail pane as `context 84k / 1M (8%)  tokens 345k`, and on the Claude Code band after the run time as `ctx 8%` (the percentage is yellow at 70 percent and over, red at 90 percent and over; a narrow pane drops it before the run time). The band has one row per worker: `● title  harness model  run time  ctx 17%` while it runs, with `✓ done`, `✗ failed`, `✗ stopped` or `✗ timed out` in place of the context column when it ends, and `◌` for a queued start. The title takes the remaining width and ends with an ellipsis when cut; the harness and model are never shortened. The daemon keeps the last complete context pair while the worker lives if a later read fails or a running turn has no usage yet. A later complete value replaces it, including a smaller value after compaction. A harness that has not reported a figure leaves it out.
- **Defaults for a short request (W17).** "Use agent-net to implement X with a new OpenCode worker, use deepseek 4.1" is enough.
  - **Worktree:** when `isolation` is empty and the project is a git repository, the worker works in its own worktree. `isolation: none` (`--isolation none`) opts out, for read-only or non-code tasks. A folder that is not a repository gets no worktree. Pi always needs one. The `agent_worker_start` tool accepts `base`, the name of an existing local branch, to start the worktree from that branch instead of the main working tree's HEAD. It requires worktree isolation.
  - **Project rules:** the target daemon reads `.agent-net/worker.md` from the project's main working tree, including uncommitted changes, so local rules take effect without a commit. It adds up to 8 KiB of cleaned text after the fixed brief and before the task. The file is optional.
  - **Project:** an agent that leaves `project` empty gets the project of its own session. If the project is on several machines, the caller's machine wins when it has the harness, else the one machine that has it. The command line must name a project.
  - **Model:** a loose name is resolved against the model list of the machine that runs the worker, for every harness that reported a list (OpenCode, Pi, Codex, and Claude's alias and full-name table). A harness with no list passes the name through. When the caller names no harness, the gateway tries every harness with a list and prefers a subscription-backed one (Claude, Codex, `opencode-go`, or a provider with a usable quota reading); a pay-per-use harness comes last. A loose or bare name only matches subscription-backed models: a pay-per-use provider, such as OpenRouter or OpenCode Zen, needs its full provider id, for example `opencode/gpt-6.1-sol`. When only pay-per-use models match, the start is refused before launch and the error names the full id to write. The result's `model_note` says `uses pay-per-use provider (<provider>)` when one is used. A suffix after the last colon that is a reasoning or thinking level (Codex's `:low`, `:medium`, `:high`, `:xhigh`, `:max`, Pi's thinking levels) is not part of the name: only the part before it is resolved, and the suffix stays on the result. An exact id wins. Else the name is split into words (case does not matter; `4.1` matches `v4.1`) and every model id that contains all words is a candidate. Candidates are ranked by: a worker that finished successfully (hand-back status done) on that machine and harness in the last 7 days; no worker failure in the last 24 hours (a model that failed a worker with a model error goes last, and the other models of its provider go after the other providers; an exact id still wins); sessions on that machine that used the model in the last 30 days; sessions that used any model of the same provider in that time; the order of `model_providers` in `node.json`; not `-free`; not a dated snapshot (`:0813`); the shortest name. A provider that is exhausted, or above 95 percent of a quota window, is skipped for a loose name; a provider ahead of pace ranks below providers on pace. The usage counts are all sessions of that harness on that machine that the gateway saw in the last 30 days, the user's own sessions included, not only workers. If one candidate is strictly best, it is used and the result says `model: luna 6 -> gpt-6-luna` (or `model: deepseek 4.1 -> opencode-go/deepseek-v4.1-flash`). When a matching model was ranked last for a recent failure, the note says so. If the best are equal, the start fails and lists up to five. If nothing matches, the start is refused before a worker is launched: `no codex model matches "luna 6" on research; closest: gpt-6-luna, gpt-6.1-luna` (at most five closest names, ranked by shared words and digits).
  - **Checklist:** every completion notice ends with a line from agent-net: read the diff in the worktree, run the project's tests there, report what you checked, merge only if your user asked. When the notice carries a report, the last line adds: `If you learned something that would change how to pick or brief this model next time, record it with agent_learn.`
  - `model_providers` in `node.json` is a list of provider prefixes to prefer, first is best: `"model_providers": ["opencode-go", "opencode"]`. Restart the daemon after a change.
- **Limits and load (decision 0018):** a worker starts only when the machine has room. The daemon keeps 1-minute and 5-minute averages of whole-machine CPU busy percent and of available memory. Before the rules are applied, the expected cost of every worker that started in the last 5 minutes (RAM and CPU) is added to the projected load, because the averages cannot show it yet. A start is let through when: fewer than `max` workers run (default 12, a safety net); the 5-minute available memory minus the expected RAM of the worker (the median of the average RSS of the last 20 workers of that harness and model, at most 25 percent of the machine's RAM, 600 MB with fewer than 2 workers in the history), minus the RAM of the workers that started in the last 5 minutes, minus the reserve (the larger of 1.5 GB and 10 percent of RAM) is above zero; the 5-minute CPU average plus the expected CPU of those workers is below `cpu_target` (default 75) and the 1-minute average, with the same addition, is below 95; and the last start that ran on the machine was at least `start_gap` ago (default 6 seconds). A start that failed, or a worker that ended within 30 seconds, does not hold the gap: it did not add load the averages could show. Running workers are never stopped for load. On Linux, available memory is `MemAvailable`; on macOS it is total RAM minus app, wired and compressed memory.
  Every 30 minutes the daemon checks registered projects for old worker worktrees. A managed worktree and its `agent-net/w_*` branch are removed only when the worker has ended, every branch commit is reachable from the project's default branch, and the worktree has no uncommitted changes or untracked files. Ignored files are removed with the worktree. Running workers, queued branches and branches used by starts in progress are kept. Older worktrees without an end record are eligible when found unused. A branch with neither an end record nor a managed worktree is left alone because its origin cannot be established. The default branch comes from the registered project or `origin/HEAD`; without either, cleanup uses a local `main` or `master`. When no default can be determined or its local branch is absent, the worktree is kept.

  Settings are in `node.json`: `"workers": {"max": 12, "cpu_target": 75, "mem_reserve": "2GB", "queue_timeout": "2h", "start_gap": "6s", "idle": "60m", "idle_notice": "2m", "max_age": "4h", "owner_grace": "10m"}`. All are optional; `mem_reserve` defaults to the larger of 1.5 GB and 10 percent of RAM, and `start_gap` defaults to 6 seconds (`"0s"` or a negative duration turns the gap off). Restart the daemon after a change.
- **The queue.** With no room, `agent_worker_start` and `agent-net start` answer `status: queued` with a `queue_id` (`q_...`), the place in the queue and the reason, for example `queued #2 on research: CPU 5-min average 82% (target 75%)`. The queue is per machine, first come first served, and is kept in the daemon database across restarts. The daemon looks at it every 15 seconds. When a start begins, the caller gets a worker notice (`status: started`, the session ID and the queue wait) and the task is sent. A start still queued after `queue_timeout` (2 hours), or one that fails when its turn comes, ends with a notice with `status: not started`. If the caller's session is offline for `owner_grace`, the queued start is dropped. `agent_worker_stop` and `agent-net stop` take the `queue_id` to cancel (the owner of the start or the person at the command line only). When the project is on several machines and each reports its room, the start goes to the one with the most room, and the result says why (`machine_note`).
- **Room in the owner views.** Each machine reports `workers N running, M queued - room: ok|cpu|mem|cap|gap|measuring` (with a middle dot) in its usage report. `agent-net workers` prints one line per machine that has workers or a queue above the table, then a table of queued starts (queue id, position, machine, project, harness, model, owner, queued time, reason); `--json` has them as `queued`. The TUI header shows queued starts and each machine row shows its running workers and memory. In the gateway, `node.usage` carries it as `workers` (`running`, `queued`, `room`, `headroom`, `reason`). New frames: `worker.queued_start` (machine to gateway) and `worker.task` (gateway to the owner's machine); `worker.kill` carries `requester_session` and `as_owner` for a queued start.
- **Owner:** `agent-net workers` and the TUI show who started each worker. A worker started from the command line or the TUI is owned by `you (on <host>)`.
- **Events:** `worker.started`, `worker.stopped` (with `reason=handback|idle|max_age|owner_gone|stopped|process_exited|lost|model_failed`) `worker.queued`, `worker.queue_cancelled` and `worker.refused`, in the gateway log, the events table and the TUI feed. `worker.cleaned` is in the gateway log.

### The playbook and the track record

The gateway learns which harness and model do well from the runs it already records. When a machine reports `worker.stopped`, the gateway adds one row to `worker_runs` (node, harness, model, outcome, start, end, run time, the run's `continue`, `fix` and `auto_continue` counts). The outcome is `complete`, `partial` (`complete: false`), `failed` (a blocked or failed hand-back, a process that ended by itself, or a run that errored), `timed_out` (the idle or longest-run limit) or `stopped` (a stop on request, a worker lost at a daemon restart, an owner that went away, or a start that never ran the task). A stopped run is neither a success nor a failure: it is counted apart and never makes a model look bad. The gateway keeps the rows for ever by default (`--keep-worker-runs` sets a period) and counts the last 30 days per harness and model. It also sums the two caller rework counters there: `continue` counts the owner's messages after a hand-back with `complete: no`, and `fix` after `complete: yes`. Automatic continues are summed apart, in `auto_continue`; they never enter the caller's rework or the calibration.

The line it shows, in `agent_machines_find` for each machine and in every `agent_worker_start` (and `agent-net start`) result for the chosen harness and model, is:

```
terra · pi: 4 runs: 1 complete, 3 partial, 2 continue (median 9 min) · cannot resume
deepseek-v4.1-flash · opencode: 14 runs: 13 complete, 3 continue, 1 fix, 2 auto-continues (median 26 min) · resumes
gpt-6-luna · codex: 3 runs: 2 complete, 1 stopped (median 6 min) · resumes
```

`default` stands for a worker that ran without a resolved model name. The counters appear in the line only when they are not zero. The last part says whether the harness can open an ended session again: OpenCode, Claude Code and Codex can, Pi cannot. Before the first run the line says `no runs yet`. The start result carries it as `track` (`harness`, `model`, `runs`, `complete`, `partial`, `failed`, `timed_out`, `stopped`, `continue`, `fix`, `auto_continue`, `median_run_seconds`, `resumable`), and `agent_machines_find` as `tracks` on each machine. The Quotas tab shows the same numbers for the selected profile as `complete 31 · continue 12 · fix 4`, with `· N auto-continues` when there are any, and an automatic playbook note carries them in its track line.

The owner can write a **playbook note** about a model (and optionally one harness) with `agent-net playbook set`, or the operator methods `playbook_set` and `playbook_get` (owner gate only; the CTO will use the same methods later). The note appears in the `playbooks` list of the start result and in `agent_machines_find`. Automatic notes from findings are built the same way; see [findings and the automatic playbook](#findings-and-the-automatic-playbook). The gateway stores notes in `playbook`, one row per subject and harness, at most 1 KiB for an owner note.

**The operator interface.** The daemon's local socket serves these methods to the command line and the TUI, and the phone app will use the same ones: `info`, `nodes`, `list`, `projects`, `events`, `conversations`, `conversation_messages`, `message_status`, `workers` (reads) and `owner_send`, `worker_start`, `worker_stop` (actions). The owner-only methods include `conversations`, `conversation_messages`, `playbook_get` and `playbook_set` (see [the playbook](#the-playbook-and-the-track-record)). The types are in `internal/protocol/operator.go`; `worker_handback` is the worker's own call. Agent connections may use `worker_start`, `worker_stop`, `worker_handback`, `agent_learn`, `agent_worker_rate` and `workers`. `owner_send` is for the command line only; `agent_learn` and `agent_worker_rate` are for agent sessions only. `worker_defect` is the owner's own call for a defect found later, used by the Messages tab key; it takes a worker, a severity from 1 to 4 and a one-line note, and files the same `defect_found_later` report as `agent_worker_rate`.

## The TUI screen

### TUI keys

The bindings live in `internal/tui/keys.go`. The key line and the help overlay
read that table. `?` opens help for the screen you are on: its section comes
first, then Global keys. Press `?` again for every screen, and Esc closes help
from either view. `q` or Ctrl+C quits. Esc never quits. When a filter or
search contains text, the first Esc clears it and the second leaves the field.

| Place | Keys |
|---|---|
| Every tab | `1` to `7` switch tabs; `c` opens the CTO; `C` focuses its box; `?` opens help; `/` filters or finds where the screen has a list; Enter goes into the selected thing; arrows and `j`/`k` move; `R` refreshes or rescans; Esc goes back or clears; `q` and Ctrl+C quit. `M` and `G` do not open Graph. |
| Sessions | Left/right selects NAME, RULE or DELEGATE. Enter on NAME opens the session, Space on RULE cycles normal, ask and auto, and Enter on DELEGATE opens its form. Esc returns to NAME. `m` composes; `n` starts a session; `v` cycles the view (active only, all, all with offline, all with offline and hidden); `h` hides or unhides the selected session; `x` stops a worker; `d` shows details; `/` filters. When something waits, one line at the top says `<N> waiting in Inbox (7)`. |
| Inbox | Tab 7 groups waiting items into three boxes: Needs your answer (questions, approvals, held requests and waiting sessions), Proposals (CTO proposals and learned suggestions), and For your information (outcomes and notices). Each box shows its count and appears only when it has items. Each row says who, what it is about in plain words, and its age. A panel shows the selected item in full: its text, session, project, machine, age and the keys that act on it. At 100 columns or wider the panel is on the right; below that it sits under the boxes. The key line follows the selection: the keys for that item, then `↑↓ select` and `? help`. On a held request, `a` approves and `r` refuses. On a learned suggestion, `a` accepts and `r` rejects. On an outcome, `s` lets it stand and `O` overrules it. A CTO proposal uses `a` to approve and `r` to refuse; `A` approves all open proposals. On a question, `t` types a one-line answer; on a notice, `x` dismisses it. Enter opens the details or goes into the session. The tab label shows the count of waiting items. |
| Messages | Up/down selects a thread; left/right selects a message; Enter opens the body; End selects the latest message; `l` cycles the filter (all, without worker threads, with system events). The body closes with Enter or Esc. The states legend stays under the Messages section. |
| Quotas | `p` protects or unprotects; `s` shows other subscriptions; `R` refreshes. Enter is unused. |
| Graph | Arrows select; Shift+arrows pan; `,` and `.` select linked nodes; Enter opens the card; `/` finds a node. The graph fits itself when the tab opens. `o`, Space, `t` and `0` do nothing. |
| Agent card | Ctrl+U and Ctrl+D scroll. `,` and `.` select learned changes; `u` undoes the selected change. `!` does nothing; the legend is in the help overlay under Graph. `[` and `]` do not scroll. |
| Tools | Opens Skills per project. Up/down selects a project, right or Enter moves to skills, and Space or Enter previews a toggle. Esc keeps pending choices for editing; Enter reviews pending choices and confirms in the review. Left returns to projects. Esc asks before discarding pending changes. `/` filters skill names. Tab switches between the two visible views. The information box follows the selected project or skill. In All tools, Enter opens actions; `/` filters, `t` shows skill cost per project, `n` adds a tool and `R` rescans copies. `[` and `]` do nothing. In the compare box, `a` and `b` keep that copy and `v` opens the full diff. The picker ticks with Space only. |
| Settings | Up/down selects rows; Tab or `[`/`]` jumps between sections; Space toggles and Enter opens the selected row. Connected apps also accepts `p` for internet access, `u` for the tunnel URL, `a` to add and `x` to revoke. Enter on a machine renames it; an empty name resets it. |
| Help | Esc closes. `?` switches between this screen and all screens. Enter, Backspace, `N`, `T` and `R` do not act there. `G` goes to the last help line. Help is built from the binding table: the screen you are on first, then Global keys; `?` again shows Global keys, the current tab, the other tabs in tab order, then In overlays. |
| Confirmations | Enter or `y` says yes; Esc or `n` says no. Every question footer shows all four keys. |

`agent-net tui` has seven tabs: `1` Sessions, `2` Graph, `3` Quotas, `4` Messages, `5` Tools, `6` Settings and `7` Inbox. On each tab, `c` opens the CTO session in herdr, `C` focuses the CTO box and ctrl+] returns to Sessions. The box is read-only and shows the latest CTO output plus `c: open the <title>` (or `enter: open the <title> · ctrl+]: back` while focused), where `<title>` is the owner's CTO title; replies use Markdown, and pgup/pgdn or ctrl+u/ctrl+d scroll the conversation. Enter opens the full CTO harness UI in herdr (ctrl+] returns from the fallback), `n` starts a new conversation after confirmation, `u` undoes the last graph change, `m` edits the title, harness and model, and Esc skips a tip. Messages lists the stored conversations, newest activity first, and pages through them as you scroll. Use up/down to select a thread, left/right to select a message, End to select the thread's latest message in the right pane, Enter for its full body, `f` on a worker conversation to report a defect found later, and `l` to cycle the filter: all, without worker threads, with system events. Use `/` to filter the list.

The Graph tab keeps its graph, camera, selection and traffic in a headless state. Actions update that state and return effects for the tab to perform. A scene describes what to show independently of the terminal, and a renderer draws it. A Braille renderer is the fallback everywhere. On a terminal that speaks the kitty graphics protocol, the same scene is drawn to an RGBA image with anti-aliased shapes and shown through Unicode placeholder cells; labels stay ordinary terminal text, so they remain crisp and selectable. The renderer and the pixel size of a cell are chosen once at start. `AGENT_NET_GRAPH` is `auto` (the default), `pixel` or `braille`. Auto sends a tiny `a=q` image query followed by a device attributes query to bound the wait, and reads the cell size from the `TIOCGWINSZ` pixel fields or a CSI 16 t reply. It stays on Braille when the terminal does not answer the image query with OK, when the cell size in pixels is unknown, or inside a multiplexer that drops images: `$TMUX`, or herdr, which sets `HERDR_ENV=1`. `pixel` overrides the multiplexer veto, and `braille` always falls back. Each image is transmitted once with `U=1` and only again when its hash changes; the old image is deleted by id first. In pixel mode the scene also carries motion: a comet with a fading tail for each message, a ring when it arrives, the phase of a busy node's slow glow, and the eased camera. Rendering never blocks the UI: `View` and `Update` build only the cheap scene, a scene hash decides whether a frame is needed, and a `tea.Cmd` goroutine rasterizes and PNG-encodes at most one frame at a time. New frames are dropped while one is in flight, and the last finished frame stays on screen. The image is capped at 1600 by 900 pixels and the terminal scales it to the cell area. A 120x34 frame with 20 nodes costs about 90 ms to rasterize, so pixel frames are requested at 8 a second; idle ticks stay at the existing 250 ms and reuse the last frame. Braille keeps its own picture and tick; both views use the same traffic filter and wildcard labels. Zooming past 1.9 times the fitted zoom shows a rounded charter card next to a node; the card box is drawn in the image and its text stays ordinary terminal cells.

`agent-net tui` needs a terminal. It talks to the local daemon through the operator interface, as an owner's CLI (`hello` with `client: cli`). Besides the reads and the actions below it uses the owner-only methods `usage`, `counts`, `usage_top`, `session_enter` and `worker_rotation_get` (see [CPU and memory](#cpu-and-memory) and [security.md](security.md#session-text-and-resource-figures)). It shows, from top to bottom:

- **Tabs.** Press `1` for Sessions, `2` for Graph, `3` for Quotas, `4` for Messages, `5` for Tools or `6` for Settings. The Quotas tab merges readings by provider and plan across machines and shows each window, pace, reset time, reading age and running workers. Subscriptions without live windows and those not usable by a harness are folded together; press `s` to show or hide them. Its Workers panel lists registered profiles and a heatmap of each profile's share of the next 20 starts at each difficulty (T, S, N, H, V), plus the next pick, the worker run outcomes, and the selected profile's track record as `complete N · continue N · fix N`. The heatmap marks a model outside its eligible levels with `x` (below the minimum or more than one level below its max) and an eligible zero share with `·`. The letters T, S, N, H and V mean trivial, simple, normal, hard and very complex. Enable the Quota integration and agent-net installs quota-axi, or uses the copy you installed (see [subscription quotas](#subscription-quotas)).

  The Quality analysis panel is read only. Left and right cycle the agent filter. Press `d` to open the full-screen details, use arrows or Page Up and Page Down to scroll, and press Esc to return. The picker marker previews the local machine and does not change its rotation. In details, a picked model also shows the chooser's short quota pace note when it is ahead of or behind pace. The cards rank models with at least five tasks by orchestrator minutes per task.

  The cards show the three most used models first: most tasks ended in the last 7 days, then least orchestrator time per task. Each row has the minutes per task, a bar of the same value (dropped on a narrow card), and the tasks this week; the card title has the 30-day total. When the model the picker would choose is not among the three, it is named first, with its marker, on the "more" line. The details show every model with task counts, help, nudges, late bugs, worst severity and ratings. All-stopped tasks are omitted, and continued sessions are grouped with their root task.

  A table by touched area follows the difficulty tables. Each task counts once in every area touched across its runs; older tasks without an area appear under `not set`. Rows with fewer than five tasks are dim, and the agent selector filters this table too. The same follow-up and headache formulas apply. The gateway builds the level and per-caller tables with the fit and caches them; reads and TUI redraws never count tasks. The fit itself is unchanged: it adjusts for difficulty, penalizes each effect with a ridge of 10 equivalent tasks at zero, and keeps effort effects summing to zero within each model.

  The gateway builds the fit at startup and rebuilds it after a rating, worker run, roster change, score override or removal of old runs. Reads and TUI redraws only use the cache. Ratings from callers scoring at least 45 count fully; other ratings count half. Without a strong rating for a model, its prediction cannot exceed the fitted average. Rework sums every run of the task, takes the model, harness and difficulty from the root's first run, leaves out tasks whose runs all stopped, and adds the severity points of late defects reported for any session of the task.

  `agent-net scores quality` reads the owner setting. `agent-net scores quality on` enables predicted quality as a rotation multiplier; `off` disables it (the default). The local socket methods are `quality_get` and `quality_set`, both owner only, backed by authenticated `GET` and `PUT /v1/quality-settings` on the gateway. The JSON is `{"enabled":true}`. The gateway stores it under `quality_weighting` in `owner_settings`. With at least 5 ratings, the star multiplier is `1 + (prediction - average) / 4`, clamped to 0.5 through 1.5. When task data exists, it is multiplied by average rework divided by predicted rework (fixes, continues and late defects), clamped to 0.5 through 1.5 even with fewer than 5 ratings; zero or negative rework predictions use a neutral multiplier of 1. It only changes the shares of adequate choices; score minimums, provider protection and quota exclusions still apply. The Quotas heatmap uses the cached profile multipliers. With the setting off, candidates and rotation picks are exactly as before.
- **Graph** on tab 2 shows agents and their relationship rules. Press `+` or `-` to zoom, arrows to select a node, and shift+arrows to pan. Use `,` and `.` to cycle through linked neighbours, `/` to find a node, Enter to open its card and `?` to see the keys. Message and owner lines touch the selected node by default; press `m` to show every line, and press it again to return to the selection. Pink dotted lines show message traffic from the last seven days, with brighter pink when no rule is set. Gold lines show your words. Counts refresh every minute and update from the live feed. The owner and CTO markers stay at the left of the graph, outside its project layout. A wildcard rule has a label above its target. With the mouse, scroll to zoom at the pointer, drag empty space to pan, click to select, or double-click a node to center and zoom on it. Mouse reporting is enabled only while the graph has focus and is disabled on other tabs and overlays, so terminal text selection works there. `c` focuses the CTO box.

  The graph uses kitty pixel graphics on a supporting terminal and Braille characters everywhere else; see the paragraph above. In pixel mode node size and dots show its session count and busy sessions, and the ring shows maturity. Rule edges are curved with arrows; labels appear on the selected node's edges and when zoomed in. A dashed ring marks a wildcard rule. A busy node has a green glow. When motion is enabled, each message is a glowing dot with a fading tail, a ring marks its arrival, and camera moves ease over about 300 ms. Past 1.9 times the fitted zoom, a node with a charter shows a small card with its maturity, session count and purpose. Frame rasterization and encoding run off the UI loop, at most 8 times a second, and unchanged frames are reused.
- **Header.** `12 online · 3 busy · 1 needs you · workers 4 running, 1 queued`: the gateway's online count from `counts`, the sessions that report `busy`, the sessions that wait for you (in the warning colour, and left out when none wait), and the workers with the queued starts from the admission queue. Offline sessions are left out: they are mostly old ghosts. An older gateway has no counts: the header then says `N sessions, M online` and the same figures after it. A warning on the title points to tab 3 when a subscription window is above 80% or exhausted. The Inbox tab label also shows the count of waiting items. The header shows the learned count, the text filter (`filter: /cogforge`), and the time of the last refresh.
- **Inbox,** tab 7, is one place for everything waiting for you (decision [0016](decisions/0016-needs-you.md)). The items sit in three boxes, each shown only when it has items and titled with its count: **Needs your answer** (questions, approvals, held requests and waiting sessions), **Proposals** (CTO proposals and learned suggestions), and **For your information** (outcomes and notices). Inside a box, what needs an answer comes first, then what proposes a change, then what only informs; rows of the same kind are newest first. Each row shows who (a session, the CTO, a learned suggestion's node, or an app), a short plain-words summary, and its age. Machine names come from the gateway, never node IDs. A panel shows the selected item in full: its text wrapped, where it comes from (session, project and machine), how long ago it arrived, and the keys that act on it. At 100 columns or wider the panel is on the right; on a narrower terminal it sits under the boxes in at most 8 lines. The tab label shows the count when it is above zero. The key line follows the selection: the keys that act on that item, then `↑↓ select` and `? help`. The empty state says `Nothing is waiting for you.` A waiting session also says `needs you` in STATUS. The Sessions list shows one accent line, `<N> waiting in Inbox (7)`, while anything waits. See [the Inbox](#inbox-tab-7).
- **Machines,** one line each: an online dot, name, agent-net version, CPU use as a percentage of the machine's core capacity with a bar, memory used and total with a bar, free space on the filesystem containing the daemon user's home directory, running worker count and memory, and the session or worker with the highest CPU use. Versions are dim, except that an online machine on an older version than the newest online machine is amber. Offline machines show their last known version in dim. The columns stay aligned across rows. When the terminal is narrow, the row keeps its version and omits optional fields in this order: top CPU session, workers, disk. Disk space is sampled once a minute; less than 10G free is red and less than 20G is amber. It does not list installed harnesses. When another machine has a harness that this machine lacks or has at an older version, it shows a dim note such as `no codex` or `old codex`. This machine comes first and is marked `(here)`. The name is the gateway's name for the machine, and `(here)` is matched by node ID, not by hostname. A figure that is missing or older than 45 seconds reads `?`.
- **Sessions,** grouped by machine under a heading: NAME, AGENT (the harness), PROJECT, MODEL (shortened: no provider, no `claude-` prefix), STATUS (`idle`, `wait`, `owes`, `busy`, `shell`, `needs you`, `dorm` for dormant, `off` for offline; `shell` comes from Claude Code and means no turn is running but a shell command the session started is still alive, such as a background command), TRUST (`-`, `ask` or `auto` for the node wildcard rule, with `+N` for sender-specific edges; always shown), RULE (the request rule: `normal`, `ask` or `auto`), DELEGATE (the [delegation policy](#delegation-policy): `off`, `native` or `workers`, or `-` if unset), CPU, RAM and TASK (the task the session announced; a worker without one shows the first line of its task; otherwise the harness title if it differs from NAME, else blank). An idle session with unanswered requests shows `wait` or `owes` in STATUS and the peer and age in TASK. The detail pane lists each peer, time and message ID. Requests older than 24 hours and notifications do not count. The operator counts call includes `pending_stalls`, the number of observed idle periods with an open request for over 20 minutes since gateway start. Sampling runs once a minute. The same loop sends at most one reminder per unanswered request to an online, idle recipient, after both the request and the last reported activity are over two minutes old. A missing activity time prevents a reminder. Notifications, held or closed requests, owner conversations, CTO recipients, and edges with `ask` or `never` are excluded. Delivery rechecks these conditions. The notification comes from the reserved `agent-net` sender and asks the recipient to answer the original message with `agent_message_reply`, or say it cannot. A `reminded` flag on the request survives a gateway restart. The operator `query.counts` call also returns `pending_reminders` (reminders put into the delivery queue), `pending_answered_after_reminder` (requests answered within 30 minutes of their reminder), and `pending_reminders_offline` (distinct eligible requests skipped while their recipient was offline, since gateway start). The first two counts are derived from retained messages and can decrease when message history is pruned. Offline requests create no notification; they may receive their one reminder after reconnecting. An online, idle session with open work gets at most one idle check per idle period. Open work is a request it owes an answer, a running worker it started, or a non-empty announced task. Workers, the CTO and offline sessions are never checked. The check is a notification from the reserved `agent-net` sender and asks for the next `agent_announce`: `done`, `more` with one line, or `blocked on X`. `done` stops checks until new activity. `more` follows the node's autonomy setting: `off` leaves an Inbox row, `<name> stopped with work left: <line>`; `wake` sends one continue notification, `[agent-net] continue: you said you have work left (<line>). Carry on, then answer the next check.`, and leaves no row. An offline session is not woken. `blocked on X` always leaves a row naming X, whatever the autonomy. Wake-ups have their own daily cap of 6 and stop after two in a row with no new message or change in open work; the second leaves one row, `<name> was woken twice with no progress`. Two `more` answers with no progress stop the checks and leave one row. A session gets at most 6 checks a day. The operator `query.counts` call also returns `checks_sent`, `check_answers_done`, `check_answers_more`, `check_answers_blocked` and `wake_ups_today`. The check count comes from retained messages and can decrease when history is pruned; the answer counts and today's wake count are small stored counters. Sessions are grouped by machine and sorted into activity bands: busy or needs you, active within 10 minutes, within one hour, within 24 hours, then older or offline. Activity is the last time the session did something (a turn started or ended, a message was sent or received, it announced a task, or a wait started), not the last time its daemon reported it. A daemon restart or a re-report does not make an idle session look active. Within a band the most recently active session comes first; ties never use the oldest start. A session entering a band moves to its top; idle margins reduce moves near band boundaries. The order freezes for three seconds after a key press, and selection follows the session. Workers stay under their caller. A session that is idle with no report for more than an hour is dim. Offline sessions are dimmed too. On a narrow terminal TASK, MODEL and PROJECT give way first. TRUST, RULE and DELEGATE stay visible. A tree with more than 20 processes, 200% CPU or 4G of memory shows its numbers in red. A process tree that several sessions share shows `shared` in their rows and has no row of its own. A worker's private OpenCode server is its own tree, so its CPU and memory are on the worker's row. The CTO is a normal row in its machine group; NAME shows the owner's CTO title, and Enter enters it through `session_enter`, the same plan as Enter in the CTO box.
- **Selected session.** The first line has the name, harness, status and the session's CPU, RAM and process count. Below, on the left: session and worker IDs and PID, folder, branch, model and mode (these last two moved here from the table), function (the project description from `AGENTS.md`; "none: add a `# Project: <one line>` line to AGENTS.md" when there is none), remote, worktree (when the session runs in a linked worktree), the project source (`seen in a session`, `registered`, or `seen in a session and registered`), task, expertise, a `trust` line when peer trust is on, `policy` and `main` lines when it has a delegation policy (with a note for each part its harness cannot do), and its recent messages. For a worker it starts with who started it and then `context 84k / 1M (8%)  tokens 345k`, with only the parts the harness has reported. On the right: the heaviest 5 processes by CPU and by memory (PID, CPU, RAM, command line with secrets masked), fetched with `usage_top` when the session is selected and again every 5 seconds. Press `d` to replace the session list with these details; press it again to return. The CTO box stays visible.
- **Workers** show in the sessions table right under the session that started them, indented with `├─` (or `└─` for the last one), in order of start time, like a sub-agent. A worker's NAME is `<project> worker <n>` (decision [0024](decisions/0024-session-names.md)), and its TASK is its title when it has one, else the first line of its FIRST task, which the daemon records and the gateway passes in the agent list; a later request from its owner does not replace it. When that task is missing, TASK is the harness title, or blank. The full worker ID is on the detail pane's `id` line. A worker on another machine than its caller still sits under the caller and has `@host` after its name. A worker whose caller is not on screen (you started it, or the caller is offline or hidden by the filter) stays in its own machine group with `↳` before the name. Selection keys follow the order on screen. The detail says the worker runs in the background. The title counts them.
- **Messages** on tab 4 lists the stored conversations from the gateway, newest activity first, not only the lines in the live feed. The screen asks for the first page when the tab opens and for the next page when the cursor comes within three rows of the end; the normal refresh asks for the first page again only while the tab is shown. Each row shows the two sides, the latest message's first line, the message count and the age. The right pane shows the messages of the selected thread, including messages older than the feed buffer. Open a thread with Enter or select it: the daemon reads the stored messages through `messages`, and the body is fetched once through `message_status` and cached by message ID. The owner can read any thread through the participant relay; another machine can read messages involving one of its sessions. A long preview ends with `…` after its text. If a body cannot be fetched, the thread says why.

  Worker conversations are grouped into one row per worker, `worker:<session>`, because a worker's task, start notes and hand-back can live in separate stored conversations. The gateway does that grouping, so the page size and `total` count workers as one row each. The row is named from data that survives the worker's session record: the first message sent to the worker, its title (else its task line), and the caller's name, as `you → worker: <title or first line of the task>`, cut to fit the row. It shows the message count and the worker's latest status when known. Press `l` to reach the view without worker rows; press it twice more to show system events, then return to all. Worker start notices (`status: started` or `queued`, and a queued start that never became a worker) do not create a row and do not count in its message total. They stay stored and appear dim when the worker's conversation is opened, or with system events on. A row moves only on real content: the task, follow-ups, the hand-back (done, failed, timed out, or an idle end without a hand-back) and replies. A session whose record is gone is shown as `ended worker` or `ended session`, never `unknown session`. Threads that include an owner's instruction, marked `you→`, show the mark too; the list is otherwise ordered by latest activity. The dim legend says what the marks and colors mean: orange means a request is waiting for a reply, red means failed, and purple identifies a CTO message. New session lines stay hidden in the live feed. The panel title shows the current message filter.
- **A result line** under the title says what the last action did: plain when it worked, `!` and red when it did not. The next key clears it.
- **Questions** use the same keys everywhere: Enter or `y` says yes, Esc or `n` says no, and every question ends with `enter/y: yes · esc/n: no`.
- **Help** (`?`). Shows the keys of the screen you are on, then Global keys. Press `?` again for every screen; Esc closes either view. The key line at the bottom shows at most seven hints: the few keys used most on that screen, then `? help` and `q quit`. It drops whole hints when the terminal is narrow. A key that is not on the line still works and is listed here. A selected row may replace a less useful hint with the action that matters then, such as `x stop` on a worker or `a approve` on a held request.

The screen refreshes once a second. CPU, memory and counts are asked every 5 seconds, because daemons report every 10 seconds. If the daemon is not running, or cannot reach the gateway, a banner says so, the last data stays on screen, and it retries every second. A daemon or gateway from before CPU and memory shows `?` and is not asked again.

| Key | What it does |
|---|---|
| up, down, `k`, `j`, `g`, home, end, page up, page down | Move the selection (or the message cursor, when the messages have the focus). |
| enter | Open the selected session in herdr. When herdr is unavailable, the TUI uses the fallback enter plan. In Messages, Enter opens the body of the selected message. See [Enter a session](#enter-a-session). |
| `l` | In Messages, cycle the filter: all, without worker threads, with system events. The old event-type cycle is gone. |
| `/` | Filter the sessions: type words, all must appear in some field of a session (name, harness, machine, project, branch, model, mode, status, function, task, folder, ID). Enter keeps the filter, Esc clears it. |
| `h`, `v` | In Sessions, `h` hides or unhides the selected session; `v` cycles active only, all, all with offline, all with offline and hidden. The Graph card keeps `h` to hide or unhide the selected project node and `H` to include hidden nodes. |
| `v` | Cycle the Sessions view: active only (hide sessions idle and unreported for more than an hour), all, all with offline, all with offline and hidden. The Sessions column header names the current view. On the Graph agent card, `a` toggles the node's autonomy between `off` and `wake`. |
| `1` to `7` | Open Sessions, Graph, Quotas, Messages, Tools, Settings or Inbox. `M` and `G` do not open Graph. |
| `c`, `C` | `c` opens the CTO session; `C` focuses the CTO box. Ctrl+] returns to Sessions and Esc skips a tip. |
| `d` | Toggle selected session details in place of the session list. |
| `?` | Show the keys of this screen; press again for all screens. |
| esc | Leave the help or a message body, or clear the filter. When a filter holds text, the first Esc clears it and the second leaves the field. Esc never quits. |
| `R` | Refresh now. In Sessions and Messages, `r` refuses or rejects instead. |
| `m` | Send a message to the selected session as yourself. Type, Enter sends, Esc cancels. The reply shows in the messages. |
| `n` | Start a new session in the selected session's project, with a form: Project, Harness, Model, Folder, Branch and First message. The form is filled from the selected row; the focus starts on First message. On a worktree form, four Up presses reach Harness, because Branch sits between it and First message. On a Graph node or its card, `n` opens the same form filled from the selected project. The key line reads `up/down field · left/right change · enter start · esc cancel`. Left and right act only on the field that has the focus. The project may be on another machine, shown as `name@machine`: the start is relayed through the gateway to the machine that holds it, and the form names that machine. Harness is a switch: left and right cycle the harnesses the target machine reports, in the fixed order claude, codex, opencode and pi. The form skips a harness the machine does not report, and one this version cannot start. Harness takes no typed text. Model follows the harness: it is a switch whose first choice is `default`, then the models the target machine reports for that harness, in the order the machine reports them. A harness change resets Model to `default`. Left and right on a text field (Project, Branch or First message) move the cursor instead. Folder is a switch: left and right change it between `project folder` and `new worktree`. A new worktree is made next to the project, in `<project>.worktrees/<branch>`, on a new branch. The Branch line appears only for a worktree; its default is `agent-net/<project>-<n>` with the lowest free number, and the note says the branch it starts from, such as `from main (HEAD)`. When a live session is busy in the project folder, the form opens on `new worktree` and says so in one line (`<project> is busy in the project folder`); left or right on the focused Folder sets it back. The folder choice and the branch travel with a start on another machine. Project also takes a path, absolute or starting with `~`, and `path@machine` names another machine; Tab completes a known project name, and a dim line under the focused field says so. An existing folder that agent-net does not know is registered when the start runs. A missing folder asks `Create <path> on <machine> and run git init?` with `enter: yes · esc: no`; only yes creates it and runs git init. A folder that is not a git repository still starts, with no worktree choice and no branch. A relative path, a path with a control character, a path outside your home folder and a link that leaves home are refused. A bare name that is no project goes under the folder that holds most registered projects on that machine, such as `~/code`. For Claude, an existing folder that Claude does not trust shows a note, and the start asks `Trust <path> for Claude Code?` with `enter: yes · esc: no`; only yes trusts it, while a folder agent-net created is trusted at once. This version starts Claude Code, OpenCode, Codex and Pi sessions; an older row that names another harness is refused when you submit. The session opens in herdr, and a first message is sent as your own words. When herdr is unavailable, an empty First message starts and enters the session in the TUI terminal (ctrl+] returns); Claude, OpenCode and Pi starts with text remain in the background, while Codex starts in the TUI terminal with the text as its prompt. A failure shows the reason the harness gave. Esc cancels. An owner worktree belongs to you: agent-net keeps it until you remove it, and worker cleanup never removes it. |
| `x` | In Sessions, stop a worker as before, or stop a Claude background session, abort an OpenCode service turn, or kill a Pi window in the private agent-net tmux server. Owner sessions ask `Stop <name>? Conversation kept: Enter resumes it.` with `enter: yes · esc: no`. OpenCode stays in the service; an idle service session costs nothing. Interactive terminal sessions say `<name> runs in your terminal on <machine>: end it there`. A session on another machine is stopped by its own machine through the gateway. |
| left/right, Space, Enter | In Sessions, move between NAME, RULE and DELEGATE. Space cycles RULE and Enter on DELEGATE opens the policy form. Esc returns to NAME. In Settings, up/down moves between rows, Tab or `[`/`]` jumps sections, Space toggles and Enter opens. |
| `q`, ctrl+c | Quit. |

The TUI asks for each readable message body once and keeps it by message ID. Owners can read remote messages through the daemon's owner-gated relay. Other machines can read only messages involving their own sessions. If all participant machines are offline, the screen names a holding machine and says it is offline. Expired messages remain unavailable. Text from the network is stripped of control characters before it is drawn, so an agent name or a message cannot move the cursor or change the screen.

**Event feed.** The screen reads the gateway's events through the local daemon method `events` (`{"after_id": N, "limit": N}`) and the gateway frame `query.events`. Any enrolled machine can ask. It returns the events of kinds `msg.*`, `session.*`, `node.*` and `worker.*`, oldest first, as metadata: the event kind and time, the machine, the session, the message ID, and for messages the two session IDs, the type, the ID of the message it answers, and whether the CTO sent it. Never a message body. Machine addresses are left out, details are cut to 200 characters, and MCP, enrollment and retention events are not included. With `after_id` 0 you get the newest `limit` events (default 100, at most 500). An older daemon or gateway answers that the method is unknown, and the screen then shows the sessions without the feed.

**Stored conversations.** The Messages tab reads the full stored history through the owner-only daemon method `conversations` (`{"cursor": "...", "limit": N}`) and the gateway frame `query.conversations`. It lists every stored conversation on the gateway, from every machine, newest activity first, as `{"conversations": [...], "total": N, "next": "..."}`. Each row has `id`, `from`, `to`, `from_name`, `to_name`, `message_count`, the newest message's `last_line` (one line, no control characters, at most 200 characters), `type`, `state`, `last_message_id` and `at`. A row that involves a worker session groups every conversation of that worker and has the ID `worker:<session>`, plus `worker_session`, `worker_owner` and `task` (the first line of the task message, cleaned and cut). The gateway finds a worker from its live session record or, after cleanup deletes that record, from its own notice and task messages. A row whose newest activity is only a worker start notice (`status: started`, `queued` or `not started`) is omitted, and those notices do not count in `message_count` or move `at`. `total` counts the grouped rows, so a worker counts once. `limit` defaults to 50 and is at most 100. `next` is empty on the last page. Opening a row asks the owner-only `conversation_messages` method (`{"conversation_id": "...", "limit": N}`, answered with `{"messages": [...]}`) for that conversation's stored messages, including a conversation between two sessions on other machines. A `worker:<session>` ID returns every stored message that involves that worker, lifecycle notices included. Agents keep the node-scoped `messages` read; they cannot call either owner method. The live feed still supplies the newest lines and the chat of a conversation that is already open.

### Enter a session

`Enter` asks the daemon (`session_enter`) to open the selected session. If herdr is available, the daemon focuses an existing herdr pane for that session. Otherwise it creates a pane beside the TUI when the TUI is in herdr, or a titled tab on the herdr server. The TUI keeps running and its result line says `opened <name> in herdr`. If the server is not running, the daemon tries to start its headless server. When no herdr client is attached, open herdr in a terminal to see its panes. See the [herdr installation instructions](https://herdr.dev/tui/docs/install/). Your input in a session counts as yours, so approvals you give there are yours.

When herdr is missing or cannot start, agent-net uses the existing enter plan. The TUI hands its terminal to that command; press `ctrl+]` to return and see the installation hint. A session that has neither an attach command nor an accessible herdr or tmux pane shows a note to open its existing window.

While the TUI has handed its terminal to a session, agent-net keeps the bottom row of the terminal for itself and gives the session every row above it. The row reads `agent-net · ctrl+] back to agent-net · <session name>` in dim text, so the way back stays visible over the session's full-screen UI. The row is drawn again after a resize and after the session clears the screen, and the window title still carries the same hint. `ctrl+]` returns as before: agent-net clears the row and gives the session its full size back. Set `AGENT_NET_NO_BAR=1` to turn the row off on a terminal that mishandles it; the session then gets the whole terminal. Sessions opened in a herdr pane are unchanged: the bar belongs to the TUI terminal fallback only.

| Session | Command used for a new view |
|---|---|
| Claude Code background session, including Claude workers | `claude attach <id>` |
| Claude Code interactive session | Focus its existing herdr pane, or attach its tmux pane. An interactive session outside both cannot be attached twice. |
| Pi | An owner session starts directly in herdr, or in agent-net's private tmux server as a fallback. Pi workers have no screen. |
| OpenCode | `opencode --session <id>` in the session folder |
| OpenCode worker | `opencode --server <private url> --session <id>` on its own machine; remote access uses herdr when both machines have it |
| Codex | `codex resume <thread>` in the thread folder, using its configured Codex home |
| Codex worker | `codex resume <thread> --remote ws://<private address> --remote-auth-token-env AGENT_NET_CODEX_TOKEN` on its own machine; remote access uses herdr when both machines have it |

**herdr.** The daemon finds the binary from the session's `HERDR_BIN_PATH`, the server command line, the configured harness path, `PATH`, or `~/.local/bin/herdr`. It uses `herdr pane list` and `herdr pane process-info` to find the pane holding a session process. A moved pane is found the same way. `herdr agent focus` focuses a recognized agent, and `herdr tab focus` focuses the tab of a plain terminal. Herdr's existing clients can remain on their previous tab after a focus command; select the named tab if needed.

For a new view, agent-net uses `herdr pane split --pane <tui-pane> --direction right --focus` and `herdr pane rename` inside herdr, or `herdr tab create --label <name> --focus` outside it. It uses `herdr pane run` to launch the session. A private one-use environment file keeps worker credentials out of command arguments and pane text. The pane removes the file after use; the daemon also removes it after one minute while it is running. Claude's herdr client has mouse tracking enabled; the old terminal fallback still disables it for text selection. A second Enter focuses the existing pane instead of creating another one, while that pane remains present. The daemon also remembers panes it opened during its current run.

On macOS the daemon reads process environments and command lines with `sysctl kern.procargs2` when permitted. If Herdr's pane variables are not readable, it checks the process ancestry and candidate server sockets.

**A session on another machine.** The question goes through the gateway to that machine. When both machines have herdr, the remote daemon focuses or opens the session there and this machine opens `herdr --remote <target>` in a local pane. Otherwise the daemon uses `ssh -t -o ConnectTimeout=5 -- <target> <command>`, in a local herdr pane if available or in the TUI terminal as a fallback. To change the target, or to turn ssh off, set in `node.json` on the machine where you use the TUI, and restart the daemon:

```json
"enter_ssh": false,
"enter_ssh_hosts": {"build-box": "me@build-box.example"}
```

`enter_ssh` is on by default. `"enter_ssh": false` turns it off, and then Enter shows the command to run there instead. `enter_ssh_hosts` is optional: it maps the machine's name (as `agent-net nodes` shows it, so its [display name](#machine-names) when it has one) to the ssh target, which can be an alias from `~/.ssh/config`. Without an entry the name itself is used. The target must be a plain host name or `user@host`. ssh runs with your own keys and ssh config, so it may ask for a password, a key passphrase or a Tailscale SSH check. If ssh exits with status 255 (it could not connect or log in), the TUI shows the command to run on the machine, with a hint to set `enter_ssh_hosts`. The same target is passed to `herdr --remote` when the session runs in herdr on the other machine and this machine has herdr. The secrets of a worker's private server never leave the machine: workers of OpenCode and Codex on another machine can also be entered through herdr when both machines have it; their private server credentials stay on their own machine.

## Peer trust and hints

Decision: [0013](decisions/0013-peer-trust-and-hints.md). Security: [security.md](security.md#peer-trust).

**Owner instruction markers.** For a Claude Code owner prompt, the gateway considers the first subsequent send within 10 minutes. If the prompt names the recipient session or project, ignoring case and matching whole names, the message carries up to 200 characters of the prompt. A non-matching first send consumes the prompt. The Messages tab shows `you→` and the excerpt. Claude Code, Codex, Pi and OpenCode append a note after the message body with the excerpt. It says the excerpt on its own does not approve a merge and gives the receiving node's merge rule.

**Request rules.** The gateway chooses an edge rule from the sender's project node to the recipient's project node. A specific sender edge wins over the wildcard edge from `*`. With no edge the rule is `normal`: deliver the request under the usual peer guidance. `auto` delivers it with the peer trust note, using the receiving node's effective merge authority. The note never widens that setting. `ask` holds it for your decision. `never` refuses it with `refused by your user's rule`. The TUI `t` key cycles the wildcard rule through `normal`, `ask` and `auto`; the TRUST column also counts sender-specific edges. `session_trust` remains an owner-only compatibility method that sets the wildcard rule to `auto` or `normal`. At upgrade, trusted sessions with a project get a wildcard `auto` edge, without overwriting an existing edge.

A held request appears in the Inbox with its sender, recipient and first line. Select it and press `a` to approve once or `r` to decline once. `A` and `R` do not answer a held request. Enter goes into the receiving session. The owner-only operator methods `held_list` and `held_answer` expose the same actions. Approval delivers the request with an agent-net approval note. A decline sends the sender `your user declined this request`. A held request expires at its normal message expiry and the sender receives a notice.

**Expertise.** `agent_announce` takes `expertise`, one short line. `agent_sessions_find` and `agent_session_get` show it, `list --json` has `profile.expertise`, `agent-net list --wide` has an EXPERTISE column, and the `text` filter searches it.

**Hints.** The local method `hint` takes `{"text": "...", "failed": true}` (and, from the command line, `harness` and `native_id` of the session) and answers `{"suggestions": [{"session_id", "name", "hostname", "why"}], "text": "<one line>"}`, or an empty object. The Claude Code hook (`agent-net hint`), the Pi extension and the OpenCode plugin call it after a failed tool call. How each shows the line: Claude Code adds it as `additionalContext` of the hook output, Pi appends it to the tool result, OpenCode adds it to the system prompt of the next request of the session. See the decision for the matching rules and the limits (at most 2 suggestions; the same suggestion once per 30 minutes per session; one hint per 45 seconds per session).

The Claude Code hook output, when there is a suggestion (one line of JSON on stdout, exit 0):

```
{"hookSpecificOutput":{"hookEventName":"PostToolUseFailure","additionalContext":"agent-net: another session may own or see this. ..."}}
```

## Inbox (tab 7)

A session can wait for you: it asks permission for a tool, asks a question, or wants your input. The TUI lists those sessions in the Inbox tab (decision [0016](decisions/0016-needs-you.md)). Every machine's TUI sees them, so one screen answers "what needs me" across the network.

Non-worker sessions on Claude Code, Codex, Pi and OpenCode can call `agent_owner_ask` with `kind: "question"`, `text`, optional `options` and optional `urgency`. The question is one line, cut to 200 characters with control characters removed. Options are up to four short strings, cleaned the same way. Urgency is `normal` or `high`. The guidance asks an agent to call this tool when it ends a turn waiting for your decision or input. A plain-text question at the end of a turn does not raise an alert by itself. A worker cannot call this tool; it must put the question in its hand-back or ask its caller with `agent_message_reply`. Worker rows do not appear in the TUI Inbox. When a worker reports a wait, its caller receives one blocked notice for that wait, including the question or permission.

The daemon stores the question as a `question` waiting state with the options, urgency and start time. It sends the state with the session to the gateway, and every machine reads it from the agent list. The existing harness hooks still report permission prompts, forms and input dialogs. `agent_announce` also accepts an optional `waiting` string as a fallback.

Open the Inbox tab, select a waiting session and press Enter to go into it. On an inbox question, `t` opens a one-line answer box. The first option, if any, is filled in. Enter sends the text as an owner message; Esc cancels. The wait clears when the owner message is delivered. It also clears when the owner types a Claude Code prompt (with the `needs-hook` installed), a Pi or OpenCode prompt, or a Codex turn starts; when a permission or form settles; when the session becomes busy; or on an announce without `waiting`. If the gateway is down, a queued owner message clears the wait when delivered.

For a new explicit `agent_owner_ask` question, the daemon on each machine with an owner TUI open shows a desktop notification, at most once per session in five minutes. The limit survives a daemon restart. On macOS it uses `osascript`; on Linux it uses `notify-send` when available. Otherwise it does nothing. The notification does not contain the question text. Open `?` help and press `n` to toggle desktop notifications for that machine; the setting is on by default and persists locally. The CTO proposes changing the setting with `settings_change` area `notifications` and `{"disabled": true}` or `{"disabled": false}` on the CTO's machine.

The local daemon method is `waiting` (`{"kind", "text"}` or `{"clear": true}`). A plugin is bound to its session; a command line names `harness` and `native_id`, and is silent when the session is unknown. It is not an owner-only method: a harness hook must be able to call it for its own session, like `hint`. Explicit asks also carry `options`, `urgency` and `explicit` through this path. Claude Code's `needs-hook` hooks are installed with `agent-net setup --claude-needs-hook`; they use `UserPromptSubmit` to clear the wait.

## Delegation policy

Decision: [0014](decisions/0014-delegation-policy.md). Security: [security.md](security.md#delegation-policy).

A per-session or per-project setting that says who does the coding. Set it with `p` in the TUI. Values: `delegate` (`off`, `native`, `workers`), `coder_model` and `main_model` (loose names, resolved against the model list of the session's machine and harness), scope (`session`, `project`). The session's own value wins over its project's. The operator method is `session_policy` (`{"session_id": "...", "policy": {"delegate": "native", "coder_model": "deepseek 4.1", "main_model": "sol 6.1"}, "scope": "session", "dry_run": false}`), served only to the owner's command line or TUI. It answers `{"session_id", "saved", "effective"}`; `effective` is the policy the session ends up with, with `coder_resolved`, `main_resolved` and the errors of a name that did not resolve. A dry run saves nothing. An empty policy clears the value at that scope. `list --json` has `delegation` (`scope` says where the effective value comes from).

The gateway stores the policy in `sessions.delegation` and the project defaults in `project_policies` (migration 10), and logs a `session.policy` event and line when one is saved. The policy is a rule agents follow, so it is public read-only: `agent_session_get` shows a session's effective policy, and `agent_graph_read` shows the project policies per machine. Only the owner sets or clears it.

**What the session gets.** The local method `policy` (every turn; a session asks for its own, the command line asks by `harness` and `native_id`) answers `{"text", "mode", "agent_id", "coder", "main", "charter"}` or an empty object. The daemon asks the gateway (`policy.get`) and caches the answer for 2 seconds. `charter` is the node's charter context of [charters and component relationships](#charters-and-component-relationships); it is there even when the session has no delegation policy. `text` is one line that starts with `[agent-net]`:

- `native`: `[agent-net] Your user's standing policy for this session, set in agent-net: delegate all code changes to the `agent-net-coder-<model>` subagent (model <model>). You plan, write the brief, review its diff and run the tests. Do not edit code yourself unless the subagent fails twice.`
- `workers`: the same, but to start a worker with `agent_worker_start` with the coder model.
- `off`: `... do the code changes yourself. Do not start subagents or workers for them.`

| Harness | Where the text goes | `native` | Main model |
|---|---|---|---|
| OpenCode | A system part on every model request of a root session (`context` hook). A subagent's own session gets none. | The plugin defines `agent-net-coder-<model>` (mode subagent, the resolved model) at run time. | `session.switchModel`, once per value. |
| Claude Code | The `UserPromptSubmit` hook (`agent-net policy-hook`), as `additionalContext`. Needs the hook: `agent-net setup --claude-policy-hook`. | `~/.claude/agents/agent-net-coder-<model>.md` with a `model:` line, written by the daemon when the hook asks. | Not possible from outside. |
| Pi | The system prompt, each turn (`before_agent_start`). | Pi has no subagents: the `workers` text is used. | `setModel`, once per value, for an exact `provider/id`. |
| Codex | Not applied. | | |

The Claude Code hook output (one line of JSON on stdout, exit 0), only when the session has a policy with text or its node has a charter:

```
{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"[agent-net] Your user's standing policy ..."}}
```

With both, the policy text comes first and the charter second, separated by a blank line. The hook still prints nothing and exits 0 on any problem.

## Charters and component relationships

Decisions: [0022](decisions/0022-component-graph-and-cto.md) and [0030](decisions/0030-graph-learns-from-instructions.md). The graph includes owner-managed edges and request rules. This section also covers the maturity defaults and merge authority (step 4).

Today the gateway stores a graph of components and architects: a node keyed by project (and a node for an architect that owns no code), a charter for each node, and directed edges (`depends`: consumer to provider; `governs`: architect to component, with mode `advisor`, `arbiter` or `owner`; `deploys`: deployer to each app it deploys). The project-keyed node represents the agent; the project is its home. A session speaks for that node as the PROJECT column resolves it. The graph is visible to every enrolled machine.

**Writing a charter.** The project's own agent reads its charter on every turn, so budget about 1200 characters (roughly 300 tokens) for the whole charter. Neighbors read only the first 40 characters of `accepts` and `refuses`, so put the most important item first, in a few words. The owner reads `purpose` on the Graph card: use one sentence up to 160 characters, or 2-3 lines at 80 columns. For `accepts`, `refuses` and `escalate`, use at most 6 categories, each a short phrase separated by semicolons or newlines. Use names and pointers for `feature_requests`, `consumers` and `needs`; use `notes` only for rules nothing else covers. Never put secrets, IP addresses, tokens or paths in a charter. `graph_change` with action `charter_set` and `agent-net charter set` return advisory warnings about the budget and sensitive details; those warnings do not stop a write. The existing 2 KiB value limit still applies. Put detail in the project's `AGENTS.md`. The shared source for the guidance is `internal/protocol/charter_guide.go`.

| Attribute | Answers | Size | Good example | Bad example |
|---|---|---|---|---|
| `purpose` | What the project is | 1 sentence, up to 160 characters | Generic document and evidence API for any client. | Every endpoint, dependency, host and access rule in one charter. |
| `maturity` | `early`, `growing`, `mature` or `critical`, then why in a few words | 1 line | `growing - core API is stable; integrations are changing.` | `critical` followed by a long operational inventory. |
| `accepts` | What work this project's agent takes on when other agents ask | At most 6 short items | Bug fixes in retrieval; new generic endpoints; questions about the API | Bearer-authenticated requests from 192.168.50.65 on /v1/search |
| `refuses` | What the agent declines and where to redirect it | At most 6 short items | Client-specific endpoints; deploy changes (ask homelab) | Lists of bearer tokens or allowed IPs. |
| `feature_requests` | How to ask for something new | Names and pointers | Propose generic additions with a reason; send client-specific ideas to the client project. | A multi-step feature backlog with implementation detail. |
| `consumers` | Projects that depend on this one | Names and pointers | tenebrae, wbg-housing | An IP address, user ID and repository path. |
| `escalate` | What the agent must bring to the owner or architect | At most 6 short items | Breaking API changes: bring options and a recommendation to the owner. | Escalate every question without a recommendation. |
| `needs` | Projects, MCP servers and services it depends on | Names and pointers | quota-axi, tenebrae-library, PostgreSQL | IP addresses, user IDs and absolute config paths. |
| `notes` | Rules nothing else covers that every agent working here must know | 1-2 lines | Deploys go through homelab; never deploy by hand. | A deploy runbook with credentials and machine paths. |

**Reading it.** The daemon frame `graph.read` takes `{"project": "cogforge"}` and answers `{"graph": {"nodes": [...], "edges": [...]}}`. A project name returns that node, its charter, the nodes its edges name, and the edges that touch it; an empty name returns the whole graph. An eligible project with no stored charter is still an implicit node, with empty charter attributes. Owner-facing graph reads hide inactive empty implicit nodes after seven days; `graph_get {"all":true}` includes them. Nodes with a charter, an edge or stored owner history are retained. No session or peer identity is needed: this is a read, not a change.

The owner made charters and edges public to every agent. `agent_graph_read` returns them all, with the owner-only parts of a node left out (see the agent graph read above). The CTO reads the same view through `graph`.

**Maturity and its defaults.** A node's `maturity` level sets five controls. A node may override any one of them in a node setting; the owner sets it with `node_settings_set`, and the override is stored beside the charter.

| | `early` | `growing` | `mature` | `critical` |
|---|---|---|---|---|
| Tests before merge (`tests_before_merge`) | `optional` | `suite` | `suite-new-tests` | `suite-new-tests-review` |
| Merge authority (`merge_authority`) | `full` | `full` | `delegated` | `ask` |
| Worker sandbox (`worker_sandbox`, decision 0020) | `sandboxed` | `sandboxed` | `sandboxed` | `no-network` |
| Requests from consumers (`judge_consumers`) | `free` | `charter` | `strict` | `escalate` |
| Deploy rule (`deploy_rule`) | `brief` | `brief` | `ask` | `owner` |

An empty or missing maturity uses the `growing` controls. Approval overrides still appear in the node's rules text. The text the session and its workers get is plain words, for example `maturity mature: run the suite and add tests for new behavior; merge needs the owner's or a delegate's approval for the exact merge: the owner approves directly or through an agent-net approval note from agent_owner_ask with kind approval; a delegate approves in a message from an architect with a governs edge in mode owner or arbiter over this node; ask before deploying`, followed by the consumer rule. A node with no maturity gets rules text when a merge or deploy approval override requires it.

The worker sandbox level from decision 0020 follows the node: the owner's project policy wins when it sets a level, else the node's `worker_sandbox` default applies. A node with no maturity and no override keeps `sandboxed`.

**Autonomy.** A node's `autonomy` setting is `off` (the default) or `wake`. With `wake`, an idle check's `more` answer wakes the existing online session with one short continue notification instead of leaving an Inbox row. It never starts a new session or a clone: that stays with the owner, or with the CTO as a proposal. `blocked on X` always leaves an Inbox row. Wake-ups have their own daily cap of 6 per agent and stop after two in a row with no new message and no change in open work; the second leaves one row, `<name> was woken twice with no progress`, and a later change allows waking again. The gateway stores only the daily count, the no-progress streak and the progress markers it needs, never task content. Only the owner changes autonomy: on the Graph agent card press `a`, or with `node_settings_set`; the CTO proposes it with `node_settings_set` and the owner approves, like every node setting. The owner's answer to the session-start conflict in decision 0036 is that agent-net may wake an existing agent, but creating a new agent or a clone stays with the owner or the CTO.

**Merge authority.** `full` lets the agent merge and push to the default branch. `delegated` needs an approval from the owner or a delegate: an architect with mode `owner` or `arbiter` over the node. `ask` needs the owner's written approval. `MergeApprovalFor` returns the authority, whether approval is required, whether it must be written, the delegates and the text. For the exact merge, the owner approves directly or through an agent-net approval note from `agent_owner_ask` with kind `approval`; a delegate approves in a message from an architect with a `governs` edge in mode `owner` or `arbiter` over the node. On `ask`, always request the owner's written approval. An owner-instruction excerpt on its own does not approve a merge. The trust and owner-instruction notes use `recipient_merge_authority`, computed by the gateway for the receiving node at delivery. A sender cannot supply that field. The guidance block defers merges to this rule. Uninstall removes the guidance block as a whole.

**Deploys.** On a `deploys` edge the deployer applies the target's deploy rule, not its own. The deployer's per-turn context names each target and its rule, for example `You deploy shop; its maturity critical: ...; ask the owner before deploying`.

**Every turn.** The daemon reads the node of the session's project and puts a short charter text into the same places as the delegation policy: the Claude Code `UserPromptSubmit` hook (`additionalContext`), the OpenCode `context` hook, and the Pi `before_agent_start` system prompt. Codex has no turn context today: the policy is not applied there either, but a Codex worker gets the charter in its brief. The text holds `maturity`, `accepts`, `refuses`, `escalate`, the maturity rules and consumer rule, the edges that matter to the node (who governs it and in which mode, what it depends on, what depends on it, for an architect what it governs, and for a deployer each target's deploy rule), and the decide rule. It starts with `[agent-net]`. The daemon caches the graph per node for 2 seconds, and keeps the last answer for 10 minutes when the gateway is gone.

**Connected charters.** The per-turn text also describes directly connected nodes with charters, in either edge direction and for any edge kind. Each line has the form `<project>: accepts <short accepts>; refuses <short refuses>; ask it for those`. The project name and each charter value are cut to 40 characters, so a line is at most about 160 characters. There are at most eight lines and the block is at most 1500 bytes. Edges with a non-default request rule come first, then `governs`, `deploys` and `depends`; ties use the project name. The wildcard source `*`, the node itself and neighbors without a charter are skipped. A node without its own charter can still get these lines. The same lines go into worker briefs, including Codex worker briefs.

**Instruction signals.** The Claude Code `UserPromptSubmit` policy hook sends the first 300 characters of the latest prompt to the gateway for that session. The gateway ignores turns Claude Code injects (a delivered `cross-session-message`, a task notice, a system reminder): they are never kept as the owner's words, and the owner's earlier prompt stays; Pi and OpenCode do not send prompts to this signal path. The gateway keeps the latest owner prompt in `signal_prompts` for a 10-minute matching window. Only the first send in that window is considered. When the excerpt names the recipient session or project, the gateway records a row in `charter_signals`; a non-matching first send consumes the prompt too. An explicit assignment to another known project can still become private learning evidence. Reply outcomes are linked through the existing `graph_outcomes` table. The match is a time window, not proof that the send belongs to the same turn. A later prompt replaces the earlier one, including when it is empty.

| Signal data | What the gateway records |
|---|---|
| Owner instruction | The prompt excerpt, sender and recipient project nodes, message ID and time. |
| Reply outcome | The request and reply IDs, outcome (`accept`, `reject`, `redirect` or `escalate`), reason and redirect target when supplied. The outcome is linked to the request's signal when there is one; outcomes without an instruction signal can also be evidence. |

Signal records are kept for 90 days, independently of the message retention setting. The latest prompt is no longer used after 10 minutes; the retention timer removes expired prompt rows. Signals are read only through the owner authority path. The CTO has no signal read tool. The gateway trusts enrolled daemons to enforce their local owner checks. See [security.md](security.md#instruction-signals) for what the gateway stores and who can read it.

**Reactive charter learning.** The gateway runs rules on owner instructions, reply outcomes and deliveries between projects. An owner assignment proposes the work for the destination's `accepts` and `<destination> for <work>` for the sender's `needs`. Two matching redirects to the same destination in seven days propose that `needs` item and a `depends` edge. Three matching refusals in seven days propose a `refuses` item. Five messages between projects in seven days, when they have no edge, propose a `depends` edge. The rules do not understand every wording. Evidence includes message or signal IDs and excerpts. Related pending proposals merge, and a node must be quiet for two minutes before its proposals are ready.

Minor additions apply automatically: an owner-derived `accepts` item, an item in `needs` or `consumers`, or a `depends` edge. A restrictive or narrowing phrase, removal, `refuses`, `escalate`, maturity, deploy or request rules, a `governs` or `deploys` edge, or any change on a critical node needs the owner. An edge touching a critical node needs the owner too. At most three changes per node apply automatically in a rolling 24 hours; further proposals become major. A rejected phrase is not offered again for that node and field. Automatic changes use the graph's normal history with actor `learned` and can be undone with `graph_undo` or the Graph card's `,`, `.` and `u` keys.

Ready major proposals appear in the Inbox tab, with the evidence and `a` to accept or `r` to reject. These actions use the owner gate. The CTO cannot read pending major proposals or charter suggestions; the owner can share them.

Before ready owner-derived phrases are applied or shown, the gateway can batch them into one worker run for short wording. The normal automatic chooser selects a worker at trivial difficulty with no fixed model, using no worktree isolation and read-only project access. Protected providers and quotas still apply. It starts no more than one run per two minutes and six per rolling hour. A missing worker, failure, invalid JSON or a three-minute timeout leaves the owner's words unchanged. Raw phrases remain stored for review and rejection matching.

The decide rule is also in the [guidance block](#what-setup-changes-on-your-machine): judge a peer's request against the node's charter and decide (accept, reject with a reason, or redirect); escalate only when the charter says so or does not cover it; every escalation carries a recommendation.

**The relation line on a delivered request.** The gateway computes it from the graph, not from anything the sender wrote. It looks at the sender's node and the recipient's node: when the sender depends on the recipient, the line says the sender is a consumer of the recipient's node; when the sender governs the recipient's node, it says so with the mode (`advisor`, `arbiter` or `owner`); otherwise, for two known and different nodes, it says there is no graph relation. The gateway puts the line in `recipient_relation` on the message it delivers, like `recipient_peer_trust`, and the Claude Code adapter, the Codex adapter, the Pi extension and the OpenCode plugin write it after the body and after agent-net's authority note. Only a request gets a line: a reply is information and a notification is not a request. Two sessions of the same node, or a message from the owner, get none. The line starts with `[agent-net]`, so the marker rule of [0013](decisions/0013-peer-trust-and-hints.md) applies.

**Worker briefs.** A worker's task body carries the whole charter of its project's node after the project rules for workers and before the task: `purpose`, `maturity`, `accepts`, `refuses`, `consumers`, `escalate`, `needs` and `notes` (only the attributes that are set), then the maturity rules and consumer rule, and the `permissions:` line of the level in force. A node with no charter can still get connected-charter lines or deploy approval rules. The owner's machine reads the graph when it sends the task, so a queued start and a continued worker get the same section.

## What setup changes on your machine

`agent-net setup` (run by the install line, by the invite line, or by hand) changes only the items below. Each step prints `ok`, `ok (unchanged)`, `updated` or `skipped (reason)`. The two service steps always restart the service, so they print `ok (restarted)` (unit file unchanged), `updated (restarted)` (unit file changed) or `ok (installed, started)` (new unit). Every step is safe to run again.

| Item | What setup does | What `agent-net uninstall` does |
|---|---|---|
| Binary `~/.local/bin/agent-net` | Put there by `install.sh` or the invite script, not by `setup` itself. | Keeps it. Delete it by hand. |
| Daemon service | Writes the systemd user unit (Linux) or launchd agent (macOS), enables it and restarts it. It copies `HOME`, standard XDG folder variables, `OPENCODE_DB`, `PI_CODING_AGENT_DIR` and `QUOTA_AXI_OPENCODE_GO_PI_AUTH` from the login shell so quota-axi reads the same local data folders. When the login shell fails, it uses the setup process environment. Upgrades preserve existing entries and add missing settings. These settings contain paths and a boolean, not provider credentials. The unit has `KillMode=process` and the plist has `AbandonProcessGroup`, so a restart stops only the daemon and keeps the workers (the next daemon adopts them). Workers also run in their own session. A daemon that starts under systemd adds `KillMode=process` to an older unit and reloads systemd, without a restart. | Stops it and deletes the unit or plist. Then stops every worker that is still running (the `workers` step), because nothing else would. |
| Gateway service (only with `--gateway`) | Same, listening on `0.0.0.0:7420`. | Stops it and deletes the unit or plist. |
| Gateway binaries (only with `--gateway`) | Copies this binary into `~/.local/share/agent-net/dist`. | Keeps them. |
| Gateway data | Created by the gateway on first start. | Keeps it, even with `--purge` (it prints a reminder). Delete the directory by hand to start over. |
| Join | Writes `~/.config/agent-net/node.json` and `ca.pem`. Skipped if this machine already joined the same gateway. | Keeps them. `--purge` deletes `~/.config/agent-net`. |
| Daemon state | Created by the daemon. | Keeps it. `--purge` deletes `~/.local/state/agent-net/daemon`. |
| Claude Code | If `claude` is found, runs `claude mcp add -s user agent-net -- <binary> mcp`. This adds an entry to your user-level MCP servers in `~/.claude.json`. Skipped if the entry exists. | Runs `claude mcp remove -s user agent-net`. |
| Claude Code peer messages | If `claude` is found and `crossSessionInbound` is not set in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`), follows the network answer or `--accept-peer-messages` / `--no-accept-peer-messages`. On yes it adds `"crossSessionInbound": "accept"` and keeps every other key (file rewritten with 2-space indent, through a temp file and a rename). Records the answer in `~/.config/agent-net/claude-peer-messages.json`. An existing value is never changed. A file that is not valid JSON is not touched. The daemon and upgrades never change it. | Removes the key, only if agent-net set it and the value is still `"accept"`. Deletes the record. |
| Claude Code hint hook | If `claude` is found, follows the network answer or `--claude-hint-hook` / `--no-claude-hint-hook`. On yes it adds two entries to `hooks` in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`): `PostToolUseFailure` (all tools) and `PostToolUse` with matcher `Bash`, each running `<binary> hint` with a timeout of 2 seconds. Every other key and every other hook is kept. A later setup run updates the command if the binary moved. Records the answer in `~/.config/agent-net/claude-hint-hook.json`. A file that is not valid JSON is not touched. | Removes our two entries (and an empty `hooks` object), only if agent-net added them. Deletes the record. |
| Claude Code policy hook | If `claude` is found, follows the network answer or `--claude-policy-hook` / `--no-claude-policy-hook`. On yes it adds one entry to `hooks` in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`): `UserPromptSubmit`, running `<binary> policy-hook` with a timeout of 3 seconds. Every other key and hook is kept. A later setup run updates the command if the binary moved. Records the answer in `~/.config/agent-net/claude-policy-hook.json`; the daemon reads it to tell the gateway the hook is installed. When you set a `native` [delegation policy](#delegation-policy) for a Claude Code session, the daemon also writes `~/.claude/agents/agent-net-coder-<model>.md` (a marker comment inside; a file with that name that is not ours is left alone). | Removes our entry (and an empty `hooks` object), only if agent-net added it, deletes the record, and deletes every `agent-net-coder-*.md` file in `~/.claude/agents/` that has our marker. |
| Claude Code needs hooks | If `claude` is found, follows the network answer or `--claude-needs-hook` / `--no-claude-needs-hook`. On yes it adds four entries to `hooks` in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`), each running `<binary> needs-hook` with a timeout of 2 seconds: `PermissionRequest` (all tools), `Notification` with matcher `agent_needs_input|elicitation_dialog|elicitation_url_dialog`, `PreToolUse` (all tools) and `UserPromptSubmit`. It does not match `idle_prompt`, which Claude Code sends after any turn ends and does not mean the owner is needed; `needs-hook` ignores it, so an install with the older matcher keeps working without a setup rerun. Every other key and hook is kept. A later setup run updates the command if the binary moved and the matcher if it changed. Records the answer in `~/.config/agent-net/claude-needs-hook.json`. A file that is not valid JSON is not touched. | Removes our four entries (and an empty `hooks` object), only if agent-net added them. Deletes the record. |
| Claude Code workers plugin | If `claude` is found, follows the network answer or `--claude-workers-mod` / `--no-claude-workers-mod`. On yes it writes a local marketplace and the plugin in `~/.local/share/agent-net/claude-plugins/` from the files in this binary, then runs `claude plugin marketplace add <folder>` and `claude plugin install agent-net-workers@agent-net -s user`. Claude Code reads the plugin from that folder. The written `plugin.json` keeps the embedded `0.1.0` base version and adds a short SHA-256 hash of the other plugin files, so their changes update the version Claude Code sees. A later setup run, or the daemon at start, rewrites the files if they differ from the binary, so the plugin matches the version that is installed. If the `claude` CLI fails, the step says so and the rest of setup continues. Records the answer in `~/.config/agent-net/claude-workers-mod.json`. | Runs `claude plugin uninstall agent-net-workers@agent-net -s user` and `claude plugin marketplace remove agent-net`, deletes the folder and the record. |
| Quota-axi private copy | If the Quota integration is on and no usable owner or private copy is found, installs the pinned `quota-axi` with npm in `~/.local/share/agent-net/quota-axi/`. Needs Node 22.19 or newer and npm. A failed install is retried at most once a day. The daemon does the same in the background when it applies a changed network answer. | Removes `~/.local/share/agent-net/quota-axi`. Turning the item off does the same. |
| Codex MCP server | If `codex` is found or `~/.codex` (`$CODEX_HOME`) exists, adds a block between `# agent-net:begin` and `# agent-net:end` at the end of `config.toml` in it: the server `agent-net` with `args = ["mcp", "--codex"]`. It follows the network answer or `--accept-codex-tools` / `--no-accept-codex-tools`; yes adds `default_tools_approval_mode = "approve"` to that block (for this server only), no leaves it out. The answer is kept in `~/.config/agent-net/codex-tools.json`. A `[mcp_servers.agent-net]` that is not ours is left alone. Codex threads started before this do not have the tools. | Removes the block and the record; the rest of `config.toml` is kept. |
| OpenCode plugin | If `~/.config/opencode` (or `$XDG_CONFIG_HOME/opencode`) exists, writes `plugins/agent-net.ts` and its generated `plugins/tools.gen.ts` catalog in it. A symlink to a file agent-net did not write is left alone. A symlink to an older agent-net copy is replaced with a real file holding this version; the old target is kept. | Deletes both files. A symlink to a file agent-net did not write is left alone. A symlink to an agent-net copy is removed, and its target is kept. |
| Antigravity plugin | If `~/.gemini` exists, writes `~/.gemini/config/plugins/agent-net/` with its manifest, rules, MCP configuration and hooks. Foreign plugins and symlinks are left alone. | Removes unchanged plugin files written by agent-net, leaving foreign files and symlinks. |
| Pi extension | If `~/.pi` exists, writes `~/.pi/agent/extensions/agent-net/index.ts` and its generated `tools.gen.ts` catalog beside it. A symlink to a file agent-net did not write is left alone. A symlink to an older agent-net copy is replaced with a real file holding this version; the old target is kept. | Deletes the plugin and generated catalog in both the folder and old flat layouts. Setup also removes owned files from the old flat layout. A symlink to a file agent-net did not write is left alone. A symlink to an agent-net copy is removed, and its target is kept. |
| Connected apps Funnel | When enabled, the gateway records the Funnel port and its loopback proxy target in `funnel.json` in the gateway data directory. | Reads Tailscale status and removes the route only when that port still has exactly the recorded target. Leaves a repurposed or shared port alone. Reports a cleanup failure and keeps the record for retry. |
| Skill and MCP management state | Created by the daemon when the owner applies a tool assignment or removes a native MCP entry. Setup writes none of it. Files and folders are listed in [Files](#files). | Deletes `skill-manifest.json`, `skill-backups`, `mcp-backups`, `skill-journal` and leftover temporary files in the daemon state folder, and releases the Git exclude lines the manifest owns. It keeps installed skill copies and every native MCP config file (including any edit the daemon made): restore those by hand or with `tools_undo` before you uninstall. Backups are deleted because they can hold secrets. The gateway side (`skills.git`, the inventory and audit tables in `gateway.db`) is kept with the gateway data. |
| Guidance text | Adds a block between `<!-- agent-net:begin -->` and `<!-- agent-net:end -->` to `~/.claude/CLAUDE.md` (if `~/.claude` exists) `~/.pi/agent/AGENTS.md` (if `~/.pi` exists) `~/.config/opencode/AGENTS.md` (if `~/.config/opencode` exists) and `~/.codex/AGENTS.md` (if the Codex home exists). Guidance symlinks are followed, including links into a dotfiles repository. When several paths resolve to the same file, it is updated once; if they use different reply instructions, one block includes instructions for each harness. Text outside the markers is never changed. An older unmarked `## agent-net: working with other agents` section is replaced by the block. | Removes the block. |

The Pi extension, the OpenCode and Antigravity plugins, the Claude workers plugin folder and the guidance blocks are also written by the daemon, on every start, with the same rules as setup (the harness folder must exist, plugin symlinks to foreign files are left alone, guidance follows symlinks and changes only between the markers). The Claude workers folder is refreshed only when setup already wrote it, so the daemon never installs the plugin on its own. It rewrites a file only if it differs from the content embedded in the binary, and logs one line per file it changes (`agent files: pi extension updated`) in the daemon log. A symlink that points at an older agent-net plugin is replaced with a real file, so a stale link cannot keep an outdated extension loaded; the old target is kept and named in the log line. So these files follow the binary after an upgrade or a manual copy, without a setup run. See [upgrade.md](upgrade.md#agent-files-on-a-machine).

Setup does not use `sudo`. On Linux it may print a hint to run `sudo loginctl enable-linger <user>`, which keeps the user services running when you are logged out. You run that yourself.

Setup does not change any other Claude Code or Pi setting (the one exception is the peer-message setting above, and only with your yes), and it does not touch other MCP servers.

## Owner inbox and written approvals

Agents use `agent_owner_ask` with `kind: "approval"`, the exact action in `text`, and a `recommendation`; or `kind: "idea"` with the idea in `text` and a `recommendation`. The daemon binds each request to the calling session; the gateway derives its project from that session. Codex workers put the exact action and their recommendation in their report when they need approval.

Press `7` in the TUI to open the Inbox. Items are grouped by kind, newest first inside each kind. Select an item and press Enter to see its context. Press `a` to approve, `r` to refuse, `t` to enter a one-line answer, or `x` to dismiss a notice. The tab label counts waiting items, and the Sessions list says how many wait. Answers are stored together with a message queued for the requesting session. An approval covers only the exact action named in the item.

The owner-only local methods are `inbox_list` and `inbox_answer` with `id` and `answer` (`approve`, `refuse`, or text). The daemon applies the same operator gate as other owner-only methods. Rejections and escalations of agent requests are also recorded in this inbox, with the request, reason, recommendation and conversation. Answering an item in the Inbox closes it there and in the other waiting rows. Existing outcomes are added to the inbox when the gateway upgrades.

For a node with `ask` merge authority, request approval for the exact merge using `agent_owner_ask` with `kind: "approval"`. A deployer follows the target node's deploy rule: for a critical target, request written approval for the exact deploy using the same tool. Codex workers include the action and recommendation in their report.
### Request outcomes

`agent_message_reply` accepts optional `outcome`: `accept`, `reject`, `redirect` or `escalate`. Reject, redirect and escalate need `reason`; redirect also needs `redirect_to` (a node name), and escalate needs `recommendation`. Each text field is limited to 2048 bytes, and the node name to the project name limit. Control characters are removed. Outcomes apply to replies to requests. The gateway records the request's source and target project names, request and reply IDs, outcome, reason, redirect, recommendation and time. Projects unknown to the sessions appear as empty names.

A rejection or escalation of another agent's request appears as a separate Inbox entry until the owner resolves it. The entry shows the request's first line, outcome, reason and recommendation. Press `s` to let the decision stand or `O` to overrule it. Letting it stand sends the receiving session a notice that its decision stands. Overrule sends the original request again from the owner with the agent-net note `the owner overrules: do it.` The operator method is `outcome_overrule` with `reply_id` and `action` (`let_stand` or `overrule`).

The daemon marks its private Claude CTO folder and Claude worker worktrees trusted in `~/.claude.json` (or `$CLAUDE_CONFIG_DIR/.claude.json`). It removes the worktree trust setting when a known Claude worker worktree is deleted. Uninstall removes the CTO folder's trust setting and preserves other project settings.

`session_enter` with the CTO session ID starts the local CTO runtime if needed and returns an enter plan for its full harness UI. In the CTO box, press Enter to open it in herdr. If herdr is unavailable, ctrl+] returns from the terminal fallback. Claude starts a background session on first entry if needed. Entry errors appear in the TUI. The Claude CTO requires Claude Code with `--bg` and `--strict-mcp-config`; update Claude Code if either flag is missing. The daemon waits for the background job to run before saving its session ID. If Claude Code has closed the conversation and a resume fails, or the resumed job crashes at startup, the daemon starts a fresh Claude conversation once. The CTO timeline and proposals remain in agent-net. The CTO box reports the restart or any startup error.

The daemon service records the login shell's PATH in its environment, so tools
installed outside the system directories are available to workers. If the shell
cannot supply it, setup adds common Homebrew and local binary directories to the
current PATH. A daemon upgrade adds PATH to older service files.

### CTO proposals for a session or project

The CTO can propose `session_change` with action `session_start`, using `project` or `path`, `node_id`, `harness`, `model`, `folder`, `branch`, `first_message` and optional `create`. It can propose `project_change` with action `create` and a `path` plus the same start options. Creating a project authorizes creation of the missing folder and a git repository when the owner approves. A session start creates a missing folder only when `create` is true. These actions always appear in the Inbox tab and never use the graph owner window. The row shows the project or path, machine, harness, folder choice and a cut of the first message. Select it and press `a` to approve.

The daemon records the target machine and checks local paths before accepting a proposal. A remote machine checks its own path when the approved start reaches it; an offline or older machine refuses the request. Approval uses the same session start path as the `n` form. A TUI in herdr supplies its pane to the local daemon so the new view can open beside it. For a session started on another machine, the owner's daemon follows the start and opens the remote client locally. When the view opens, the TUI shows `opened <name> in herdr`. If herdr is unavailable and a Codex start needs a terminal, the approving TUI runs the fallback plan. If Claude needs the owner's trust answer, the proposal result and note say so and no session starts. Answer through the normal new-session form. A missing folder without `create` likewise needs the owner's answer.

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

For Claude, a typed path that already exists and is not trusted answers
`status: "needs_trust"` with the checked `path` and starts nothing. The same
request with `"trust_claude": true` records the folder in `~/.claude.json` (or
`$CLAUDE_CONFIG_DIR/.claude.json`) and starts. A folder the start created on
the owner's command is trusted at once. A start in a known project keeps its
existing trust behavior.

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

For Pi, agent-net opens `pi --session-id <uuid> --name <project> [--model M]` in herdr. A resumed Pi conversation uses `pi --session <id>`. A first message is sent as an ordinary owner message after Pi registers. When herdr is unavailable, the daemon hosts Pi in its private tmux server and needs tmux installed.

For Codex, agent-net opens `codex [-m MODEL] [prompt]` in herdr in the project folder. When herdr is unavailable, the answer contains an `enter_plan` and the TUI runs it in its own terminal. A remote start follows the same herdr or ssh rules as Enter.

For Claude, OpenCode and Pi, the answer is a started-session record with `id`, `status: "starting"`,
`node_id`, `hostname`, `harness`, `project`, `path`, `branch`, `model` and `started_at`
(Unix seconds). Poll `session_start_get {"id":"<returned id>"}`. Once normal
discovery and gateway registration finish, the record has `status: "started"`,
`native_id` and `session_id` (and `job_id` for Claude). A failed launch or
registration has `status: "failed"` and `error`. A typed path can first answer
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

All three methods require the owner's CLI connection and refuse agent callers,
including CLI commands inside agent, Codex app-server and OpenCode serve
process trees. They are not agent tools. The session uses the harness's normal
project settings and MCP servers, with no worker brief, hand-back, worktree,
worker list entry or completion notice. Idle and age limits do not stop it.
Worker cleanup and daemon shutdown leave it running. When herdr is available, starts and resumes open their view there after registration, including starts approved from a CTO proposal. Enter on a session on another machine uses `enter_ssh` and `enter_ssh_hosts` from node.json.

### Connected apps

Press `6` for Settings and select a row in Connected apps. The status line shows whether internet access is on and the public MCP URL. A warning lists other routes on the same Tailscale name when the gateway reports them: those services receive the phone sign-in cookie. Press `p` to switch it. With Tailscale, the gateway chooses a free Funnel port (443, 8443, then 10000). Errors identify the gateway machine where Tailscale runs and show the command or link to use. Without Tailscale, press `u` and paste an HTTPS origin from your own tunnel to `127.0.0.1:7422`.

The gateway stores the public switch, chosen port and URL in its database. When the switch is on, it restores the listener and checks Funnel on restart. When off, the listener stays stopped. Switching access off closes the listener first, then removes the recorded Funnel route. If removal fails, status reports the error and retries on the next status check or start. Status also includes other Tailscale routes on the host. Revoking the last active app also switches access off. Adding an app while off first asks whether to turn access on. The public address is used in OAuth discovery and authentication headers. The owner-only operations are `apps.public.status`, `apps.public.on`, `apps.public.off` and `apps.public.url` (with a `url` field).

Bots appear after the machine groups in Sessions, under Bots. Their rows show their last call time and are online for ten minutes after a call. Enter opens Messages filtered to that bot. Press `x` to revoke its app.

Press `a` and enter the code shown in the browser. Use left and right to choose bot or owner app, Tab to edit the name, Enter to approve, or `r` to refuse. Select an app and press `x`, then `y` to revoke it. Esc returns. Five wrong codes block further lookups for ten minutes.

Skill items in `tools_items` and `tools_item` include `description`, `start_tokens` and `full_tokens` when nonempty. The description comes from the newest scanned copy. Token counts are estimates: start is (name length + description length + 20) / 4 rounded up; full is the manifest SKILL.md size / 4 rounded up, or zero when unknown. MCP items omit these fields.

Claude Code and Codex sign-in probes run `claude auth status` and `codex login status` with a five second limit. Only the signed-in state and auth method are kept; account details are discarded. Antigravity's sign-in state comes from its own model list (`agy models`): a list means signed in, and no account detail is kept.

### Antigravity main sessions

Interactive agy sessions are discovered from SessionStart hooks and the matching live process and working folder. Conversation summaries supply the title and parent conversation when available. Worker conversations and known sub-conversations are excluded. Busy messages wait for a Stop or PreInvocation hook; idle sessions in a matching herdr pane receive typed text and Enter. Others wait until the next turn. A dispatched message's delivery detail names that wait; it becomes delivered after the hook returns it or herdr accepts it. See [Antigravity](antigravity.md#main-sessions) for Enter and limits.
