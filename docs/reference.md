# Reference

## CTO settings and conversation

The owner-only `cto_settings` operator method reads and saves the active CTO
harness, its model, the saved model for each harness, and conversation
auto-compaction rules. The response has `harness`, `model`, `opencode_model` and
`claude_model`. A set request with `harness` and `model` saves the model for
that harness and makes it active. A request with only `harness` switches the
active harness without changing either model. An empty model selects that
harness's default.

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
{"compaction":{"threshold_percent":70,"floor_tokens":40000,
 "cache_window_seconds":{"anthropic":300,"openai":300}}}
```

`threshold_percent` defaults to 70. After a CTO reply, when the conversation's
context is at or above that share of the model's context window, the daemon
compacts at once. `floor_tokens` defaults to 40000 and applies only to the idle
rule. `cache_window_seconds` is per provider and defaults to 300 seconds when
the provider is not listed. A `compaction` object replaces the previous rules:
a field left out takes its default. The daemon compacts an idle conversation
above the floor at the window minus 30 seconds, so the next message does not
reread a large cold history. One compaction is allowed per idle period, and
none runs while a turn is active. The model reports its context window through
OpenCode; with no window known, only the idle floor rule applies. These rules
apply to the OpenCode CTO. Claude Code manages its own automatic compaction.

The daemon asks OpenCode 2.2.0 to compact with
`POST /api/session/{id}/compact` and `{"delivery":"steer"}`, which runs at the
next step boundary. The conversation's messages report the context before a
compaction. The first reply after it reports the context after. The CTO chat
shows one line, for example `compacted: 182k -> 14k tokens`; it is stored with
the other CTO action lines. Action lines are short. Graph change details remain
in Map history. The daemon keeps one displayed timeline across harnesses and
restarts. A switch adds a dim divider with the harness, model and local time.

The owner-only `cto_send` method takes `{"text":"..."}` and starts the selected
harness on first use. OpenCode runs as a private server. Claude Code runs as a
background session using the owner's Claude login, including a Claude Max plan
when that login uses one. It uses the model name or alias in the setting, such
as `opus` or `sonnet`; the default is `opus`. Its transcript stays in Claude's
projects directory.

Each harness keeps its own conversation ID in daemon state across restarts and
harness switches. `cto_new` starts a fresh conversation for the selected
harness only. When a harness is left, the daemon saves its messages and action
lines in the local state database. `cto_events` merges that saved history with
the selected harness's live conversation and the durable switch dividers, in
chronological order. It returns at most 500 entries per page. An `index` of 0
returns the newest page. `older` is the cursor for the previous page; the TUI
sends its negative value as `index` when scrolling reaches the oldest loaded
entry. `next` remains the forward offset.
Changing harness stops the previous runtime; the next send resumes the selected
conversation. The CTO is not a worker or a network session.

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
The private configuration copies the owner's general OpenCode settings and plugins,
except the agent-net plugin, and uses only the CTO's agent and MCP server. The
owner's agents, MCP servers, permissions, instructions, server and autoupdate
settings are excluded. A config that cannot be read falls back to private defaults
and is reported in the daemon log.

Claude Code uses a private working directory under `cto/claude` in daemon
state. The daemon passes a private MCP configuration with `--mcp-config` and
`--strict-mcp-config`; the only MCP server in it is `agent-net cto-mcp`. The
ordinary agent-net MCP tools are not registered for this session.

The CTO uses the hidden `agent-net cto-mcp` server. Its calls pass the CTO
process-tree check, then use the same daemon implementations as the owner's
operator methods. Graph changes and messages are attributed to the CTO. The
brief treats material it reads as data, and calls for a diff and undo after
graph changes. Destructive actions require the owner's explicit words in the
chat.

The table audits the owner operator methods and ordinary agent-net tools. CTO
tool names are private to the CTO and are not in the shared MCP registry.

| Owner method or ordinary tool | CTO tool or equivalent |
|---|---|
| `info`, `nodes`, `agent_nodes` | `info`, `nodes` (compact machine summaries) |
| `list`, `agent_list` | `agents` |
| `get`, `agent_get` | `get` |
| `projects`, `agent_project_list`, `project_add`, `agent_project_add`, `project_remove` | `projects`, `project_add`, `project_remove` |
| `events`, `message_status`, `agent_message_status` | `feed`, `message_status` |
| `messages`, `agent_messages`, `session_text` | `messages`, `session_text` |
| `workers`, `worker_start`, `agent_start`, `worker_stop`, `agent_stop` | `workers`, `worker_start`, `worker_stop` |
| `owner_send`, `agent_send`, `agent_reply` | `send`, `reply` (reply to messages addressed to the owner) |
| `usage`, `usage_top`, `counts`, `graph_view`, `session_enter` | Same names |
| `session_trust`, `session_policy`, `node_rename` | Same names; read a session policy with `get` |
| `integrations_get`, `integrations_set` | Same names |
| `held_list`, `held_answer`, `inbox_list`, `inbox_answer`, `outcome_inbox`, `outcome_overrule` | Same names |
| `graph_get`, `charter_set`, `charter_history`, `node_add`, `node_remove`, `edge_set`, `edge_remove`, `edge_history`, `graph_undo` | Same names; request rules are on `edge_set` |
| `node_settings_get`, `node_settings_set`, `playbook_get`, `playbook_set` | Same names |
| `agent_learn` | `learn` |
| `mcp_import_candidates`, `mcp_import`, `mcp_native_remove` | Same names; see [importing native MCP servers](#importing-native-mcp-servers) and [removing a native MCP entry](#removing-a-native-mcp-entry) |
| `mcp_list`, `mcp_add`, `mcp_remove`, `mcp_test`, `mcp_enable`, `mcp_call`; `agent_mcp_list`, `agent_mcp_add`, `agent_mcp_remove`, `agent_mcp_test` | Same `mcp_*` names |
| `cto_settings` | `cto_settings` |
| `cto_enter`, `cto_send`, `cto_new`, `cto_events`, `cto_graph_history` | Same names; these methods manage the CTO itself. |
| `agent_announce`, `agent_handback`, `agent_request_approval`, `agent_propose_idea` | Session or worker self-service only. The CTO is neither a network session nor a worker; it talks to the owner in this chat. |

CTO `nodes` returns machine name, online state, version, harness versions and
model counts, worker count and a quota summary. To read one model list, call
`nodes {"models":{"machine":"box","harness":"pi"}}`. `agents` returns a
compact session summary. `feed`, `workers` and `projects` return at most 20
items by default, or up to 50 with `limit`. Pass the returned `next_before` as
`before` to read the next page. An empty cursor means there are no more items.
`held_answer` accepts `a` (approve once), `A` (approve and set auto), `r`
(decline once), or `R` (decline and set never). `edge_set` takes kind `depends`,
`governs` or `deploys`, governance mode `advisor`, `arbiter` or `owner`, and
request rule `normal`, `auto`, `ask` or `never`. `charter_set` takes the nine
charter attributes. Node settings can override merge authority and worker
sandbox defaults. The tool schemas describe the other fields and fixed values.


`ui_show` switches every open local TUI to `sessions`, `messages`, `quotas`,
`map` or `tools`, with an optional session ID, exact session name or project node,
filter text and a short reason. `ui_notice` shows a short note. Both show a
`CTO:` line and return `no TUI open` when no subscribed local TUI is connected.

In the TUI, `C` focuses the CTO box on the Sessions tab.
Its title shows the selected harness and model.
Type in the focused input and press Enter to send. When the input is empty,
`n` asks before starting a new conversation, `u` undoes the latest graph change,
`m` opens the harness and model form,
and Esc returns focus to the session list. The chat polls once per second and shows recorded CTO
tool calls, including reads and failures, plus graph diffs; a graph change
refreshes an open map.

Press `5` to open the Tools tab (skills and MCP servers, [decision 0027](decisions/0027-skills-and-mcp-management.md)).
It has three screens. Digits stay the global tabs and `tab` goes to the CTO box, so `[` and `]` switch screens. Left and right arrows also switch screens except in All tools, where they select a machine or projects cell. Press `?` for Tools keys.
The tab never shows harness columns: a tool is on or off for a machine, or on for chosen projects.
A harness limit shows as a `◐` cell and a note in the details pane.

- **Differences** (opens first). Similar differences are grouped. Press `enter` on a group to expand it, then press `enter` on an individual difference to open its suggested fix. The screen shows: two machines on different versions, two different copies on one machine, a project's own copy hiding a managed one, the same content under two names, skills only on some machines and not managed, native MCP servers set up by hand, failed targets, and pending targets (info only). `enter` opens the plan of the fix. `x` accepts a difference as it is: the gateway stores the mark (`tools_accept`, table `tool_accepted`), so it stays after a restart. `.` shows accepted ones.
- **All tools.** NAME, FROM (agent-net store with a version number such as v3, your machines, repo, gateway), one column per machine, PROJECTS and NOTE, with a details pane. `h`/`l` pick a machine cell or PROJECTS, `a` turns on and `u` turns off, `/` searches. `a` or `u` on a machine cell plans the change for that machine only (a global assignment limited to it), and the plan says "only on NAME: other machines are not changed". With a single machine there is no limit to set. A machine the desired state does not name is never reported as drift and never shows a pending cell. On PROJECTS, a picker with a filter (it works for 50 or more projects) ticks the projects. A skill that is only found on machines is imported into the store when you apply. A skill inside a project folder is `⌂ repo` only when the repository tracks files under it. One that is not tracked is `local` (shown as `local PROJECT`, with a `PROJECT (local)` project cell): a copy on one machine. A local copy is managed like a copy found on a machine: `a` imports it and turns it on, `u` turns it off, and there is no branch plan.
- **By project.** Pick a project (`j`/`k`, `/` filters) and a machine (`m`): what a session gets, in layers: from the repo, on for this project, on this machine for every project, MCP servers. Hidden ones are struck through with the reason. The last line is the charter's `needs` text and whether a tool covers it; it assigns nothing.

Every change opens a plan first, in machines and projects. `d` shows the exact folders, `enter` applies, `esc` cancels. Removing a skill committed in a project repo offers a local branch with one commit, or asking that project's agent through the owner send path (`b`). The plan names the branch and exact tracked files. It also says when the skill folder has uncommitted changes in the owner's checkout. Applying creates `agent-net/remove-<skill>` from the project's default branch head in a temporary worktree under the daemon state directory, commits the removal and deletes the temporary worktree. The branch stays in the project for you to merge when ready. agent-net never commits to the default branch and never pushes. The action uses the owner-gated `tools_plan` and `tools_apply` methods and is available to the CTO through the same methods. The tab also uses `tools_items`, `tools_import`, `tools_accepted`, `tools_accept`, `tools_needs`, `inventory_scan`, `mcp_import_candidates`, `mcp_import` and `mcp_native_remove`. The CTO read tool `tools_differences` returns the same differences (the computation is `internal/toolsdiff`).

Press `4` or `M` to open the Map tab. The focus view shows the selected
project as a card: name, maturity, the start of the purpose, and its live sessions
as dots. Below it, a `governed by` line names its architects (only when it has
some), then IN and OUT columns list the edges with colored arrows. The rule is
shown only when it is not `normal`, and the `*` wildcard reads `all projects`.
The charter follows with every field that is set, wrapped under aligned labels;
use `[` and `]` to scroll it when it does not fit. The last row, RECENT, lists
the newest graph changes of the project. In the left list, a dot marks a
project with a live session. Left and
right follow dependencies; up and down move through all projects in the left list.
Press `/` to find a project, Enter to focus the first match, or Esc to cancel.
The initial focus uses the most recent connected charter change, then the most
connected node, then the first project. Your selection stays when you return.
Press `z` for the
overview. It wraps project boxes across rows and lists isolated projects below.
Use the arrows to select a box, Page Up and Page Down to scroll the projects
and visible edges, and Enter to
focus the selected project. Edges are shown only when both endpoints are
visible; a count shows the edges off screen. In focus view, Enter opens
Sessions filtered to the selected project, and `c` starts a CTO question
about it. Tab moves between the map and CTO box.

Hidden item preferences are shared by every machine's TUI through the
gateway's `owner_settings` entry `hidden_items`. On Sessions, `h` hides or
unhides the selected session, and `H` toggles hidden sessions into the list as
dim rows tagged `hidden`. The title shows `hidden N` while preferences exist.
Sessions already use `x` to stop a selected worker, so `h` and `H` are the
hide and show keys on both Sessions and Map. On Map, `h` hides or unhides the selected project
node by name, and `H` toggles hidden nodes into the view as dim rows tagged
`[hidden]`. Session IDs stay hidden if a session comes back online. An ended
session's preference is dropped after 30 days; project preferences remain
until unhidden. The owner-only methods are `hidden_get` and `hidden_set`.

Ports, files, environment variables, flags, and what setup changes on a machine. Paths are for the default user setup. `~` is your home directory.

## Commands

| Command | What it does |
|---|---|
| `agent-net gateway` | Run the gateway server. |
| `agent-net phone owner LOGIN` | Set the phone owner Tailscale login once on the gateway machine. `--data DIR` selects its gateway data directory. |
| `agent-net daemon` | Run the per-machine daemon. |
| `agent-net mcp` | Stdio MCP server for one Claude Code session. Claude Code starts it, you do not. `agent-net mcp --codex` is the same for Codex: Codex starts one per thread. |
| `agent-net setup` | Set this machine up. See [what setup changes](#what-setup-changes-on-your-machine). |
| `agent-net invite` | On the gateway machine: print the one-line installer for a new machine. |
| `agent-net enroll` | On the gateway machine: print a raw one-time join string. |
| `agent-net join STRING` | Join this machine to a gateway with a join string. |
| `agent-net uninstall` | Remove services and integrations. |
| `agent-net status` | Daemon connection, node ID and local sessions. `--json` for JSON. |
| `agent-net list` | Agents on the network, with their PROJECT (found from the agent's folder). NAME is the project name (see [session names](#what-each-machine-reports)); duplicates are ` 2`, ` 3` and workers are `<project> worker <n>`. `--all` includes offline ones. `--dormant` includes dormant ones (reachable, but quiet for 24 hours; OpenCode only). `--json` shows them always. `--harness`, `--text` filter. `--wide` adds branch, remote, folder (the session's folder), node and worktree. MODE (`foreground`, `background`, `unknown`, or `-` from an older machine) and MODEL show how the session runs. TRUST is `yes` for a session with [peer trust](#peer-trust-and-hints) on. POLICY is the [delegation policy](#delegation-policy) (`native:deepseek/deepseek-v4.1-flash(project)`), or `-`. `--wide` adds EXPERTISE. `--json`. |
| `agent-net nodes` | Machines with their agent-net version and installed harnesses (HARNESSES column). `--wide` lists each harness with version, path and model count, and one extra row for every other copy of a harness (marked "other copy"); the primary copy is marked when there is more than one. `--json` has everything, including the model lists and each machine's worker `tracks` (see [the playbook and the track record](#the-playbook-and-the-track-record)). |
| `agent-net nodes rename NODE [NAME]` | Give a machine a display name that every machine shows, or clear it (no `NAME`). `NODE` is a node ID or a name the machine is listed under. Same as the `N` key in the TUI. See [machine names](#machine-names). |
| `agent-net projects` | Known projects, one per repository per machine: name, machine, path (the main working tree), git remote, SOURCE (`registered`, `seen` or `both`), which harnesses ran there, the linked worktrees where sessions ran, online sessions and last seen. `--name N` shows one project. `--json`. See [the project registry](#the-project-registry). |
| `agent-net projects add PATH...` | Register repositories on this machine. `--dry-run` shows what would happen. `--json`. |
| `agent-net projects scan DIR` | Register every repository under a folder on this machine. `--depth N` (default 3, at most 8), `--dry-run`, `--json`. |
| `agent-net projects remove NAME\|PATH` | Remove a registration. A name that fits two projects on this machine is refused: give the path. `--on MACHINE` removes the entry of another machine (host name or node ID), for example one that is gone. |
| `agent-net events` | On the gateway machine: the gateway's events table, newest last. `--node` (ID or host name), `--session`, `--kind` (exact, or a group like `msg`), `--since` (like `2h` or `2026-10-29T12:00:00Z`), `--limit N` (default 100), `--follow` (or `-f`), `--json`, `--data DIR`. |
| `agent-net tui` | A live terminal view of every session on the network, with three actions: message a session as yourself, start a worker, stop a worker. It talks to the local daemon through the operator interface, so it runs on any machine. See [the screen](#the-tui-screen). |
| `agent-net start --project NAME [--harness opencode|claude|pi|codex] [--model M] [--machine HOST] [--isolation worktree|none] "TASK"` | Start a worker: a new agent session in a known project, on the machine that holds it. It prints the worker's session ID. The task is sent to it as a message from you; its report comes back to you (see the TUI, or `agent-net messages`). In a git repository the worker gets its own git worktree on the branch `agent-net/<worker-id>` by default; `--isolation none` works in the project folder. `--model` takes a loose name such as `"deepseek 4.1"`. See [workers](#workers). |
| `agent-net stop SESSION` | Stop a worker. The process group is killed. |
| `agent-net cto [harness opencode\|claude\|model MODEL]` | Show CTO settings or change the harness or model. An empty model selects the default. Owner only. |
| `agent-net workers` | The workers on the network, with machine, project, harness, model, owner, state and start time. `--json` also has the task's first line and the usage figures (`context_tokens`, `context_limit`, `tokens`). `--owner-claude UUID` keeps only the workers owned by this machine's Claude Code session with that native id (the transcript file's name). `--recent` also lists workers that ended in the last two minutes, with `ended_at`, `end_status` and `end_note`; without it the list and the table are unchanged. |
| `agent-net send SESSION TEXT...` | Send a message as yourself (the owner of this machine). `SESSION` is a session ID or a unique agent name. |
| `agent-net playbook set MODEL [--harness H] [--pin\|--unpin] TEXT...` | Write or replace the owner's playbook note for a model, or for a model on one harness (`H` is `opencode`, `claude`, `pi` or `codex`). With no text the note is removed. `--pin` keeps automatic learning from ever changing it. The note appears in the `agent_start` or `agent-net start` result for that model, and has at most 1 KiB. `agent-net playbook get MODEL [--harness H]` prints it; `agent-net playbook pin\|unpin MODEL [--harness H]` pins or unpins the note that is stored. See [the playbook](#the-playbook-and-the-track-record). |
| `agent-net messages` | Recent messages and their states. `--session ID`, `--limit N` (default 20), `--json`. |
| `agent-net hint` | Not for you to run: the Claude Code hook that [setup can add](#what-setup-changes-on-your-machine). It reads the hook input on stdin and prints hook JSON only when it has a suggestion. See [peer trust and hints](#peer-trust-and-hints). |
| `agent-net policy-hook` | Not for you to run: the Claude Code `UserPromptSubmit` hook that [setup can add](#what-setup-changes-on-your-machine). It reads the hook input on stdin and prints hook JSON only when the session has a delegation policy or its node has a charter. See [delegation policy](#delegation-policy) and [charters](#charters-and-component-relationships). |
| `agent-net needs-hook` | Not for you to run: the Claude Code `PermissionRequest`, `Notification`, `PreToolUse` and `UserPromptSubmit` hooks that [setup can add](#what-setup-changes-on-your-machine). It tells the local daemon when the session waits for you, and clears that when you type a prompt or a tool runs after an approval. See [Needs you](#needs-you). |
| `agent-net version` | Print the version. |

## Graph operator methods (foundation)

The gateway stores explicit nodes, charter attributes, node settings, edges and their history in `graph_nodes`, `graph_charter`, `graph_node_settings`, `graph_edges` and `graph_history`. Git working trees reported by sessions and registered projects also appear as implicit component nodes, except roots in temporary folders or the home folder itself. Registrations are checked for Git by the reporting daemon. The following local methods are behind the owner gate and relay to the gateway. Agents cannot call them.

The owner can use `agent-net charter show <node>`, `charter set <node> <attr> <text...>` (omit text to clear), `charter history <node>`, `graph`, `graph undo [history-id]`, `node add-architect <name>`, `node remove <name>`, `node settings <name> [setting value...]`, `edge set <from> <kind> <to> [mode] [rule]` and `edge remove <from> <kind> <to>`. Charter attributes are `purpose`, `maturity`, `accepts`, `refuses`, `feature_requests`, `consumers`, `escalate`, `needs` and `notes`. Edge kinds are `depends` (consumer to provider), `governs` (architect to component, with mode `advisor`, `arbiter` or `owner`) and `deploys` (deployer to each app it deploys).

Agent list and get include the public charter fields purpose, maturity, accepts, refuses and feature_requests. List values are limited to 300 characters each. When purpose is empty, the project's AGENTS.md description is used, marked with `purpose_fallback`.

- `graph_get {}` returns `nodes` and `edges`, hiding unregistered empty implicit projects without a live or recently seen session for seven days. Each project node includes registered and observed `locations` (machine and path), up to five live `sessions` (id, name, machine and status), and `session_count`. `graph_get {"all":true}` includes those projects. The map uses the same default filter.
- `charter_set {node, attr, value}` sets an attribute; an empty value clears it. `charter_history {node, limit}` returns newest changes first.
- `node_add {name}` and `node_remove {name}` add or remove an architect. Removal requires no edges, and clears the node's settings with it.
- `edge_set {from, to, kind, mode, rule}` and `edge_remove {from, to, kind}` change an edge. `edge_history {node, limit}` returns changes involving that node, or all edges if node is empty.
- `node_settings_get {node}` returns the node's maturity, its overrides, the defaults its maturity gives and the effective values.
- `node_settings_set {node, setting, value}` sets or clears one override. Settings are `tests_before_merge` (`optional`, `suite`, `suite-new-tests`, `suite-new-tests-review`), `merge_authority` (`full`, `delegated`, `ask`), `worker_sandbox` (`sandboxed`, `no-network`, `read-only`), `judge_consumers` (`free`, `charter`, `strict`, `escalate`) and `deploy_rule` (`brief`, `ask`, `owner`). An empty value clears the override. The method is behind the owner gate and refuses agent sessions.
- `graph_undo {id}` reverts a history row, or the latest row if id is omitted. Undo is recorded as another change and refuses a stale value. Node undo refuses to remove a node with a charter, settings or edges.

History limits default to 1000 and are capped at 1000. Edge history has `old` and `new` edge objects, omitted when absent. Gateway frames use the same names with dots instead of underscores; `graph.undo` uses `history_id` because `id` identifies the frame itself; `node_settings_get` and `node_settings_set` use `node.settings.get` and `node.settings.set`.

## Playbook operator methods

The owner's notes about models are behind the owner gate: only the owner's command line or TUI reads or writes them, never an agent session. The store is one row per subject and optional harness.

- `playbook_set {model, harness, note, pin}` writes an owner note. An empty `harness` applies the note to every harness of that model. An empty `note` removes the entry. `pin` keeps automatic learning from changing it.
- `playbook_get {model, harness}` answers `{note}` with the note for that harness, or the model-wide note when there is none for the harness; `note` is absent when nothing is stored.
- `agent_learn {subject_kind, subject, finding, evidence}` records one finding from an agent session. It is not an owner method and the command line is refused. See [findings and the automatic playbook](#findings-and-the-automatic-playbook).

The model is required, and the note is at most 1024 bytes after cleaning (control characters other than newline and tab are removed). Gateway frames use `playbook.set`, `playbook.get` and `learn.add`. The CLI is `agent-net playbook set|get|pin|unpin`.

## Phone app

The phone app is off by default. Run a daemon on the gateway machine, joined to
that gateway, under the same user as the gateway. The app uses that daemon's
socket and the same operator calls as the TUI. Actions run as that machine's
owner session. The gateway and daemon must both be running.

After the gateway has created its data directory, set the Tailscale login once:

```sh
agent-net phone owner owner@example.com
```

Pass `--data DIR` before the login when the gateway uses a non-default data
directory. The same login may be set again; a different login is refused.
Start the gateway with `--phone-listen 127.0.0.1:7421` and run:

```sh
tailscale serve --bg http://127.0.0.1:7421
```

Open the HTTPS URL printed by Tailscale on a phone connected to the tailnet.
In Safari, use Share, then Add to Home Screen. Tailscale Serve must be enabled
for the tailnet. Do not use Funnel. Tagged devices do not send user identity
headers. Only the configured login can use the app. See the [phone security
boundary](security.md#phone-owner-identity).

For a gateway installed as a service, add the flag to the gateway service
command and restart it. A later `setup` run rewrites that command, so restore
the flag after setup. The app refreshes its machine, session and Needs you
sections while open. It does not send push notifications.

## Ports

| Port | Used by |
|---|---|
| TCP 7421 | Optional phone HTTP listener, bound only to 127.0.0.1 when enabled. The port is configurable. |
| TCP 7420 | The gateway. HTTPS and WebSocket, on `0.0.0.0` by default. Every daemon connects to it. Open it to your other machines and to nothing else. |

The daemon and the agents talk over Unix sockets, not network ports.

## Files

| What | Default path | Notes |
|---|---|---|
| Node credentials | `~/.config/agent-net/node.json` | Gateway URL, node ID, token, CA fingerprint. Mode 0600. Optional `"hostname"` pins the machine name (see below). Optional `"quota_axi"` is an absolute path to quota-axi when it is not on `PATH` or in `~/.local/bin`. |
| Pinned CA | `~/.config/agent-net/ca.pem` | The gateway's CA, trusted after the fingerprint check. |
| Daemon state | `~/.local/state/agent-net/daemon/daemon.db` | SQLite. Delivered message IDs (for duplicates), the outbox for sends made while the gateway is away, and the `workers` table (process ID, start time, owner, worktree, task line) that lets the daemon take its workers back after a restart. Mode 0600, like its `-wal` and `-shm` files. |
| Worker folders | `~/.local/state/agent-net/daemon/workers/<worker id>/` | The worker's private files: for OpenCode its private data and state folders and session file, for Pi the named pipe used as its standard input, for Codex the capability token of its app server (mode 0600). Also the output log of the harness. Kept after hand-back for resume, otherwise deleted when the worker ends. OpenCode configuration remains shared. At each start OpenCode credentials are exported and imported through memory into the private database (mode 0600); OAuth token refresh in the copy may affect the original login. |
| Worker worktrees | `~/.local/state/agent-net/daemon/worktrees/<worker id>/` | The default in a git repository (not with `--isolation none`). Kept when the worker changed something, removed when not. The branch is `agent-net/<worker id>` in the project. |
| Repo skill removal worktrees | `~/.local/state/agent-net/daemon/repo-removals/<id>/` | Temporary worktrees used to make an owner-approved skill removal branch. Removed after the commit; the `agent-net/remove-<skill>` branch remains in the project's Git repository. |
| Daemon socket | `$XDG_RUNTIME_DIR/agent-net/daemon.sock`, else `/tmp/agent-net-<uid>/daemon.sock` | Directory 0700, socket 0600. macOS has no `XDG_RUNTIME_DIR`, so it uses the `/tmp` path. |
| Claude hint hook record | `~/.config/agent-net/claude-hint-hook.json` | Whether you were asked, and whether agent-net added the hook (and to which settings file). |
| Claude needs hook record | `~/.config/agent-net/claude-needs-hook.json` | Whether you were asked, and whether agent-net added the hooks (and to which settings file). |
| Claude workers plugin record | `~/.config/agent-net/claude-workers-mod.json` | Whether you were asked, whether agent-net installed the plugin, and the marketplace folder. |
| Integration local answers | `~/.config/agent-net/integrations-local.json` | The per-item `--...` answers this machine made for itself. They win over the network answers. |
| Integration applied record | `~/.config/agent-net/integrations-applied.json` | The last answers this machine applied, so a daemon start does not repeat the work. |
| Claude workers marketplace | `~/.local/share/agent-net/claude-plugins/` | The local marketplace and the `agent-net-workers` plugin files, written from the binary. Claude Code reads the plugin from this folder. |
| quota-axi private copy | `~/.local/share/agent-net/quota-axi/` | The pinned npm package `quota-axi`, installed by setup or the daemon when the Quota integration is on and you have no quota-axi. The daemon runs `node_modules/.bin/quota-axi` from it. Removed by uninstall and when the item is turned off. |
| Skill manifest | `~/.local/state/agent-net/daemon/skill-manifest.json` | Paths, hashes, revisions and ownership of skill copies the daemon made, and the Git exclude lines it owns. Mode 0600, owned by you. Kept until uninstall, which also releases the owned exclude lines. |
| Skill copies | The native skill folders of each harness (for example `~/.claude/skills/<name>/`, `~/.agents/skills/<name>/`, and `<checkout>/.agents/skills/<name>/` for a project) | A skill the owner assigned. Written by the daemon as your user. Uninstall keeps them: they are your skills now, and removing them could lose work. Undo removes the ones the manifest says agent-net made. |
| Git exclude lines | `<checkout>/.git/info/exclude` (the path from `git rev-parse --git-path info/exclude`, shared by linked worktrees) | One line per project skill copy, added only by the daemon. Each line is reference-counted in the manifest. Uninstall removes the owned lines and leaves the rest of the file. `.gitignore` and the Git index are never changed. |
| Temporary files | `~/.local/state/agent-net/daemon/.skill-manifest-*`, `.project-verify-*`, `.project-ignore-*`; `.claude.json.lock` (a folder, next to `~/.claude.json`) and `<config>.agent-net-*` (next to a native MCP file) | Short-lived. The daemon deletes them when the step ends. A crash can leave one: uninstall removes the ones in the state folder. `.claude.json.lock` is Claude Code's own lock folder, taken for the length of one edit. |
| Skill backups | `~/.local/state/agent-net/daemon/skill-backups/<item hash>/<time>-<id>/` | Exact copy of a skill directory before the daemon removed it. Local only, never uploaded or shown. Folders 0700, files 0600. At most 20 per item and none older than 30 days (pruned after each removal and at daemon start). Uninstall removes them. |
| MCP config backups | `~/.local/state/agent-net/daemon/mcp-backups/<item hash>/<time>-<id>/` | The whole native MCP config file (`preimage`) as it was before the daemon removed an entry, and `meta.json` (file path, hashes, entry name). A backup can hold secrets: local only, never uploaded or shown. Folders 0700, files 0600. At most 20 per item and none older than 30 days. Uninstall removes them. |
| Skill journal | `~/.local/state/agent-net/daemon/skill-journal/*.json` | One small record per skill operation that is running. Written before the first change and deleted when the operation ends. Mode 0600, folder 0700. A record left after a crash is read at the next daemon start. Uninstall removes the folder. |
| Reply sockets | `<socket directory>/r/<session id>.sock` | One per sending session, used to catch Claude Code's native replies. |
| Gateway data | `~/.local/state/agent-net/gateway/` | `ca.pem`, `ca.key`, `server-key.pem`, `gateway.db` (SQLite, with `-wal` and `-shm` files; it also holds the tools inventory, assignments, plans, audit and MCP registry), `admin.json` (mode 0600: the local admin URL and token) and `skills.git` (mode 0700: the private bare Git repository of imported skills, which can hold secrets from skill files). Uninstall never deletes any of it, not even with `--purge`. **Back this folder up** with `agent-net gateway backup --out DIR` (see [managed skill versions](#managed-skill-versions)); a backup holds the CA key, so keep it private. Without it every machine has to join again. See [security.md](security.md#what-the-gateway-keeps). |
| Gateway binaries | `~/.local/share/agent-net/dist/agent-net-<os>-<arch>` | The four binaries the gateway hands out to new machines. |
| Binary | `~/.local/bin/agent-net` | |
| Claude Code sessions (read) | `~/.claude/sessions/` | The daemon reads one file per live session. It never writes here. |
| Linux services | `~/.config/systemd/user/agent-net-daemon.service`, `agent-net-gateway.service` | systemd user units. |
| macOS services | `~/Library/LaunchAgents/com.agent-net.daemon.plist`, `com.agent-net.gateway.plist` | launchd agents. |
| Linux logs | `journalctl --user -u agent-net-daemon` (and `-u agent-net-gateway`) | |
| macOS logs | `~/Library/Logs/agent-net-daemon.log`, `agent-net-gateway.log` | |

**Machine name.** Each daemon reports a name for its machine on every start. If `node.json` has `"hostname": "mac-mini"`, that name is used. Otherwise, on macOS it is the first of these that is set, cut at the first dot: `scutil --get HostName`, `scutil --get LocalHostName` (System Settings, Sharing), then `os.Hostname()`. macOS changes `os.Hostname()` with the network (`Laptop-1.local`, `Laptop1`), so it is not used while a setting exists. On Linux it is `os.Hostname()` as it is. To show a shorter name, rename the machine once from the TUI (`N`) or with `agent-net nodes rename`: nothing needs to change on the machine itself (see [machine names](#machine-names)). `enter_ssh_hosts` keys match the name the machine is listed under. The gateway's own TLS and URL host (`enroll --host`, `invite`) is a network address and still comes from `os.Hostname()`.

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
| `project.removed` | A registration was removed. | `node` (who asked), `target` (the machine it was on), `host`, `name`, `path` |
| `node.connected`, `node.disconnected` | A machine's link came up or went down. | `node`, `host`, `remote` |
| `retention` | Old data was deleted. | `messages`, `deliveries`, `events`, `sessions` |
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
| `CODEX_HOME` | daemon, setup | Codex's home folder (default `~/.codex`). The daemon looks for the shared Codex app server's control socket in it, and setup writes `config.toml` and `AGENTS.md` in it. Codex reads the same variable. |
| `AGENT_NET_GATEWAY_DATA` | gateway, `invite`, `enroll`, `setup`, `install.sh` | Gateway data directory. |
| `AGENT_NET_NO_SERVICE` | setup, `install.sh` | `1` means: write no service files and start nothing. Same as `--no-service`. |
| `AGENT_NET_BASE_URL` | `install.sh` | Replaces the release download URL. For tests. |
| `AGENT_NET_DIST` | `scripts/dist.sh` | Output directory for the built binaries. |
| `AGENT_NET_NO_INSTALL` | `scripts/dist.sh` | Set it to skip copying the binary to `~/.local/bin`. |
| `AGENT_NET_WORKER`, `AGENT_NET_WORKER_SESSION_FILE` | OpenCode plugin | Set by the daemon in a worker's environment: the worker ID, and a file that holds the worker's one session ID. The plugin then skips its start-up discovery and registers only that session. Do not set them. The daemon also sets `AGENT_NET_WORKER` and `AGENT_NET_SOCKET` for Pi workers, and for Claude Code workers through the `env` of their `--settings`. |
| `AGENT_NET_NO_AUTO_UPGRADE` | daemon, `agent-net mcp` | `1` means: do not upgrade by itself. For the daemon: no automatic catch-up. For `agent-net mcp`: do not switch to a newer binary. |
| `AGENT_NET_MCP_HANDOVER` | `agent-net mcp` | Set by `agent-net mcp` on itself when it switches to a newer binary. It holds the path of a private temp file (`agent-net-mcp-handover-*` in `$TMPDIR`) with the start-up messages and unread input. The new process reads it, deletes the file and unsets the variable. Do not set it. |
| `CLAUDE_CODE_SESSION_ID` | `agent-net mcp` | Set by Claude Code for the MCP servers it starts. Not set by you. |

## Flags

| Command | Flag | Default |
|---|---|---|
| `gateway` | `--listen ADDR` | `0.0.0.0:7420` |
| | `--data DIR` | `~/.local/state/agent-net/gateway` |
| | `--dist DIR` | `~/.local/share/agent-net/dist` |
| | `--san LIST` | none. Extra host names or IPs for the TLS certificate, comma separated. |
| | `--keep-messages D` | `720h` (30 days). Message bodies and delivery records older than this are deleted. `0` keeps them for ever. |
| | `--keep-events D` | `2160h` (90 days). Events older than this are deleted. `0` keeps them for ever. |
| | `--keep-sessions D` | `720h` (30 days). Offline sessions not seen for this long are deleted. `0` keeps them for ever. |
| | `--keep-worker-runs D` | `720h` (30 days). Worker runs behind the [model track record](#the-playbook-and-the-track-record) older than this are deleted. `0` keeps them for ever. |
| `daemon` | `--gateway URL` | from `node.json` |
| | `--config-dir DIR` | `~/.config/agent-net` |
| `setup` | `--join STRING` | none. A join string from `invite` or `enroll`. |
| | `--no-launch` | off. This machine refuses workers. Saved as `no_launch` in `node.json`. It stays until `--launch`. |
| | `--launch` | off. Accept workers again. |
| | `--gateway` | off. Also install and start the gateway here. |
| | `--no-service` | off |
| | `--all` | off. Answer the one integrations question with all seven on, without asking. |
| | `--none` | off. Answer it with all seven off, without asking. |
| | `--accept-peer-messages` | off. Set Claude Code `crossSessionInbound` to `accept` as a local answer for this machine, if it is not set. It wins over the network answer. |
| | `--no-accept-peer-messages` | off. Do not set it on this machine and do not ask. It wins over the network answer. |
| | `--claude-hint-hook` | off. Add the Claude Code hook that suggests which session to ask after a failed tool call, as a local answer for this machine. It wins over the network answer. |
| | `--no-claude-hint-hook` | off. Do not add it on this machine and do not ask. It wins over the network answer. |
| | `--claude-needs-hook` | off. Add the Claude Code hooks that show a waiting session under "Needs you", as a local answer for this machine. It wins over the network answer. |
| | `--no-claude-needs-hook` | off. Do not add them on this machine and do not ask. It wins over the network answer. |
| | `--claude-workers-mod` | off. Install the Claude Code plugin that shows the session's workers and register its marketplace, as a local answer for this machine. It wins over the network answer. |
| | `--no-claude-workers-mod` | off. Do not install it on this machine and do not ask. It wins over the network answer. |
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
| | `--no-service` | off |

`setup --gateway` does not pass the `--keep-*` flags, so the gateway service uses the defaults. To change them, add the flags to the `ExecStart` line of the systemd unit (or to `ProgramArguments` in the launchd plist) and restart the service. Setup keeps your edit only until the next `setup` run, which rewrites the unit.

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

## What each machine reports

Each daemon sends two kinds of facts about its machine to the gateway. Agents and the CLI can read them.

**Repo info for each session.** When the daemon finds a session (any harness), it runs `git` in the session's folder (with a 3 second limit for all commands) and sends:

- `root`: the main working tree of the repository. Empty if the folder is not in a git repository. A linked git worktree has the root of the main tree it belongs to, found with `git rev-parse --git-common-dir`.
- `worktree`: the working tree the session's folder is in. It is the same as `root`, except in a linked git worktree.
- `project`: the base name of `root`, or of the folder if there is no git repository. So a worktree is not a project of its own: the sessions of `tenebrae` and of its worktrees `agent-a76c...` are all in project `tenebrae`.
- The name of every session is its project name ([0024](decisions/0024-session-names.md)). A session in a worktree is named after the main tree, not the worktree folder; a folder that is not a repository gives its own name. The git remote plays no part. Two live sessions with the same name on the network get ` 2`, ` 3` (the lowest free number), and a worker is `<project> worker <n>`. A worker's TASK is the first line of its task. The name a harness gives the session (a Codex thread name, a Claude session title, an OpenCode session title) is not the name: it goes to TASK when the session announced no task.
- `remote`: the `origin` URL. Passwords and tokens are removed. For `http`, `https`, `ftp` and `git` URLs the whole user part goes, because a token is often the user name. For `ssh` URLs only the password goes. A query or fragment is removed. `git@host:owner/repo` is kept as it is.
- `branch`: the current branch, or `detached@<short hash>`.
- `description`: the one-line description of the project, read from `AGENTS.md` (else `CLAUDE.md`) in the main working tree, or in the session's folder when there is no repository. See "FUNCTION and the task" below.

**FUNCTION and the task.** FUNCTION in `agent-net list` and the `function` field of an agent's profile is the project description above. In the TUI it is on the detail pane of a session, not in the main table, where TASK has its place. The model does not write it, and `agent_announce` cannot set it. With no `AGENTS.md` or `CLAUDE.md`, or no description line in it, FUNCTION is empty: `agent-net list` shows `-`. The daemon re-checks the file (one `stat`) at every update of the session, so a change shows within moments.

The description is found like this (code: `internal/repoinfo/description.go`). Details and reasons: [decision 0009](decisions/0009-function-from-agents-md.md).

1. Start at the first `# Project: <name>` heading (the standard template), else at the first `# ` heading, else at the top of the file. Ignore front matter and code blocks.
2. In the template form, take the first non-empty line after the heading. Otherwise take the first plain paragraph. Stop at the next heading.
3. Skip lists, tables, quotes, HTML, rules, `@file` imports and notes to agents (a paragraph that starts like "Notes for coding agents ...").
4. Strip markdown, keep the first sentence without its final full stop, and cut to 120 characters.

What the agent writes is the **task**. At the start of every session, and when the task changes, the agent must call `agent_announce` with its task in one short line (the guidance, the MCP server instructions, the tool description and the Pi and OpenCode system prompts all say so). `name` renames the session only when the agent passes it, and that name wins over the automatic project name; `function` is ignored. A harness title is not a name: it goes to TASK until the agent announces a task. Until a session has announced, every agent-net tool result ends with "Reminder: call agent_announce with your current task." The task shows in `agent_list`, `agent_get`, the TUI and as TASK in `agent-net list --wide`.

An announce may also carry `waiting`, the question the session waits to hear from you (see [Needs you](#needs-you)). A later announce without it clears the wait.

To make a project show a good FUNCTION, start its `AGENTS.md` with:

```
# Project: <name>
A <one sentence: what it is and why it exists>.
```

**Mode and model for each session.** Every session also carries two fields. `agent_list` and `agent_get` return them as `mode` and `model` (both are left out by a machine on an older agent-net).

| Harness | `mode` | `model` |
|---|---|---|
| Claude Code | From the `kind` field of `~/.claude/sessions/<pid>.json`. `interactive` is `foreground`. `bg`, `daemon` and `daemon-worker` (the other values Claude Code 2.1.312 accepts) and any new value are `background`. No `kind` is `unknown`. | The `model` field of the newest assistant entry of the main conversation in `~/.claude/projects/<folder>/<session id>.jsonl`, found when the session folder is turned into a folder name (every character that is not a letter or digit becomes `-`), or by searching all project folders. The daemon reads the file from the end in blocks (at most 8 MiB) and only when its size or time changed. It keeps no transcript text and logs none. Sub-agent entries and Claude Code's `<synthetic>` placeholder are skipped. The model changes after the next answer, not at the moment of `/model`. |
| Pi | From `ctx.mode` in the extension: `tui` is `foreground`, `rpc`, `json` and `print` are `background`. | `provider/id` of `ctx.model`, updated on `model_select`. |
| OpenCode | Always `unknown`. OpenCode 2.2.0 has no signal for a client that is attached to a session. See [opencode.md](opencode.md#notes-and-limits). | `provider/model` of the session (`Session.Info.model`), updated on `session.model.selected` and `session.step.started` of the root session. |

**Tools version for each session.** A Claude Code or Codex session has its own `agent-net mcp` process, started with the session and kept for its life. After an upgrade it can run older tools than the machine (for example no `agent_start`). Its `hello` carries its version, and the daemon compares it with its own. The session then carries two more fields (new after v0.4.6, left out by an older daemon): `tools_version` (the version of the session's tools, or `unknown` when the process sent no version) and `tools_old` (`true` when those tools are older than the machine's agent-net). The gateway stores them with the other session fields.

- **Owner.** The TUI detail pane shows a `tools` line in the warning color: `v0.4.5 (old: reconnect agent-net with /mcp)`, or `unknown (from before v0.4.7; reconnect agent-net with /mcp)` for a process that sent no version. Current tools show no line. The main table has no mark for this.
- **Agent.** When the tools are older, the daemon's answer to the `hello` carries `tools_note`. The process adds it once per session, as a last line of the next agent-net tool result: `agent-net: your agent-net tools are vX (this machine runs vY). Ask your user to reconnect agent-net: in Claude Code type /mcp, pick agent-net, reconnect. New tools appear after that.` For a Codex thread the advice is to restart Codex and resume the thread. A redial of the daemon connection does not show it again.
- **Old processes.** A process from before v0.4.7 does not know `tools_note` and cannot show it. The owner sees it in the TUI, and the refusal of `agent-net start` inside a session (see below) points the agent to the same fix. A process that sent no version is `unknown`, and gets no note.
- **Self upgrade.** A process of v0.3.5 or later normally switches to the new binary by itself and the session is then current. See [upgrade.md](upgrade.md#running-sessions). The note and the mark are for a session where that did not happen, or could not (a process from before v0.3.5, or `AGENT_NET_NO_AUTO_UPGRADE=1`). The switch itself does not need to be redone for the note: when a process asks for the switch it drops its note.
- **Reconnect.** In Claude Code type `/mcp`, pick `agent-net` and reconnect. This starts a fresh `agent-net mcp` from the installed binary, with the new tool list.

When an owner-only command (`agent-net start`, `send`, `stop`, `usage`, and the others behind the same check) runs inside an agent session, the refusal starts with `method "..." is not available from inside an agent session: run it in your own terminal.` and ends with the advice. For `start`: `agents start workers with the agent_start tool, not this command. If you do not have agent_start, your agent-net tools are out of date: ask your user to reconnect agent-net (in Claude Code: /mcp).` The other commands say: `agents use the agent-net tools, not this command. If a tool you need is missing, your agent-net tools are out of date: ...`.

If `git` is missing, slow or fails, the session is still reported, with fewer fields. The result is cached per folder (5 minutes for root, project and remote, 5 seconds for the branch), and the branch is read again when the session updates. `agent_list` returns these fields in each agent's `repo` object. `agent-net list` shows PROJECT, and `--wide` shows the rest.

**Installed harnesses.** At start and then every 6 hours the daemon looks for `claude`, `opencode`, `pi` and `codex`. It reports the path and the version (the first line of `<program> --version`, 5 second limit) of each one it finds, and its model list (20 second limit for the whole read). OpenCode is asked with `opencode models` (one `provider/model` per line). Pi is asked with `pi --list-models`, and its table gives `provider/model`. Codex is asked through its app server (`codex app-server`, stdio) with the JSON-RPC method `model/list`, including hidden models and following `nextCursor`. Claude Code has no list command, so its aliases (`haiku`, `sonnet`, `opus`, `fable`) and the full names agent-net knows are a small table in the daemon. A harness whose list cannot be read reports no list, and a loose model name for it passes through as typed.

A daemon started by launchd or systemd has a short `PATH`, and it does not read `~/.bashrc` or `~/.zshrc`. So it searches, in this order: its own `PATH`, then `~/.local/bin`, `~/.bun/bin`, `~/.npm-global/bin`, `~/.opencode/bin`, the folders of Node version managers, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin`, `/bin`, and last the `PATH` of your login shell (`$SHELL -l -c`, or `/bin/zsh` on macOS if `$SHELL` is not set, 3 second limit). The login shell is never interactive: it reads your login startup files, not the interactive ones. The programs it runs get the search path as their `PATH`, so a script that starts with `#!/usr/bin/env node` works.

The version manager folders are: nvm (`$NVM_DIR` if set, and `~/.nvm/versions/node/*/bin`), fnm (`$FNM_DIR`, `~/.local/share/fnm`, `~/.fnm`, `~/Library/Application Support/fnm`, each with `node-versions/*/installation/bin`), volta (`~/.volta/bin`), asdf and mise shims (`~/.asdf/shims`, `~/.local/share/mise/shims`, `~/.mise/shims`), pnpm global (`$PNPM_HOME`, `~/.local/share/pnpm`, `~/Library/pnpm`) and bun (`~/.bun/bin`). With several Node versions, the one named by nvm's default alias (`~/.nvm/alias/default`) or fnm's default alias comes first. If the alias does not resolve, the newest version comes first.

A harness can be installed more than once, for example OpenCode v1 from its install script and v2 from npm. The daemon finds every copy and drops copies that are the same file (same real path after symlinks). It reports one entry per harness. The `path`, `version` and `models` fields describe the primary copy. This is the copy that comes first in your login shell's `PATH`, and then `primary` is `true`. If the login shell does not find the harness, the primary copy is the first one in the search order and `primary` is absent: the daemon cannot tell which one you run. The other copies are in `others`, each with `path` and `version`. Models are listed only for the primary copy. Both fields are new after v0.3.5 and are left out when empty, so v0.3.5 daemons, gateways and clients still work with them.

`agent-net nodes` shows the result. A machine on an older agent-net shows `unknown`. A machine that reports an empty list shows `none`. Agents get the same data from the `agent_nodes` tool (Claude Code, Pi and OpenCode), including each machine's subscription quotas when it reported them.

## CPU and memory

Each daemon samples its machine every 5 seconds, keeps 5 minutes, and reports a summary to the gateway every 10 seconds. The gateway keeps the newest report per machine in memory only (it is gone after a gateway restart and back within 10 seconds).

- **Machine:** CPU as a percentage of all cores (0 to 100), memory used and total, load average (1, 5, 15 minutes), process count, each with the 5 minute average.
- **Session:** the process tree under the session's process: CPU as a percentage of one core (100 is one full core, a tree can show more), the sum of resident memory, the number of processes, each with the 5 minute average. A tree seen only once has no CPU figure yet (`...`).
- **Which process:** Claude Code, the session's PID. Pi, the process that connects the extension. OpenCode, the one service process that hosts all sessions: it is listed once as "shared by N sessions" and is never split. If one session starts another (a Claude Code session inside another's shell), the inner tree is also counted in the outer one.
- **Linux:** `/proc/<pid>/stat` (user, system and waited-for children time) and `/proc/stat`. CPU is the difference between two readings. The sampler costs about 5 ms per reading with 450 processes, about 0.1% of one core.
- **macOS:** `ps -A -o pid=,ppid=,rss=,time=,comm=` for processes, `sysctl` for cores, memory size and load, `vm_stat` for memory in use (app + wired + compressed, like Activity Monitor). Load and memory are read every 4th sample. CPU time from `ps` has a resolution of 10 ms and misses processes that start and end between two readings, so tree CPU can read low for a tree that spawns many short jobs. Machine CPU is the sum of live processes' CPU time over the cores, with the same limit. This was written and tested against fixtures of the `ps`, `sysctl` and `vm_stat` output; it has not been run on a Mac.
- **Top lists:** only on request, not in the report. Each process has its PID, short name, and the first 80 characters of its command line with tokens, `--password=`/`--token` values and `KEY=value` secrets masked.
- **Daemon methods** (owner only, from `hello` with `client: cli`): `usage` (this machine live, and all machines from the gateway), `usage_top {session_id}`, `graph_view {project}` (the component graph for the TUI map: nodes with charters, edges and the newest charter changes), `session_text {session_id, limit, before, after, sidechain}`, `session_enter {session_id, tmux_socket, herdr_socket}` (answers `{argv, dir, env, host, note}`, see [Enter a session](#enter-a-session)), `counts`. Gateway frames: `node.usage`, `query.usage`, `query.counts`, `query.graph`, `query.usage_top`, `query.session_text`, `query.session_enter` (and `node.usage_top`, `node.session_text`, `node.session_enter` to the machine that holds the session).
- **Counts:** `counts` returns `online`, `offline_24h` and `offline_total`. The gateway keeps offline sessions for 30 days, so the total is mostly old restarts. The TUI header shows the online count and the numbers the owner can act on (busy, needs you, the worker queue), and leaves the offline counts out.

## Subscription quotas

agent-net does not measure quotas itself. It reads [quota-axi](https://github.com/kunchenguid/quota-axi), a CLI that reports the subscription windows of your model providers from the logins already on the machine (decision [0022](decisions/0022-component-graph-and-cto.md)). You do not install it by hand (decision [0025](decisions/0025-install-quota-axi.md)).

Before using an owner-installed copy, agent-net runs its fast `--version` command and skips versions older than the pinned `0.1.69`, using the private pinned copy and installing it if needed. Exit 0 or 1 with valid JSON is used; exit 1 means every provider failed. Exit 2 means the copy rejected its options, so the daemon falls back to the pinned copy. Other failures include the first trimmed stderr line with recognizable secrets redacted.

1. **Install.** When the Quota integration is on and you have no compatible quota-axi, setup installs the pinned version with `npm install --prefix ~/.local/share/agent-net/quota-axi --no-fund --no-audit quota-axi@0.1.69`. The daemon does the same in the background at startup or when it applies a changed network answer, with a 3 minute limit. A failed install is tried again the next day. Node 22.19 or newer and npm are needed; agent-net never installs Node.
2. **Which copy runs.** A compatible owner copy wins: the absolute path in `node.json` `quota_axi`, then `PATH`, then `~/.local/bin`, then on macOS the nvm folders, `/opt/homebrew/bin`, `/usr/local/bin` and the global npm prefix. Otherwise the daemon uses the private copy. On macOS and Linux, setup copies exported XDG folder variables into the daemon service so quota-axi sees the same data folders as the setup shell.
3. **When it runs.** The daemon runs `quota-axi --json --max-age 5m --no-credential-refresh` at start and every 5 minutes, with a 90 second limit. It never passes `--full`. The integration is on by default; turn it off in the integrations question, with `agent-net setup --no-quota-axi`, or in the TUI.
4. **What is kept.** One row per provider and window: window id, label and kind, percent used, reset time, pace (`ahead`, `on_pace`, `behind` or `unknown`) and whether the window is exhausted now. The newest good reading stays when a later run fails. It travels with the machine's usage report (`node.usage`, field `quotas`) and the gateway keeps the newest one in memory, like CPU and memory.
5. **Model choice and admission.** A loose model name skips a provider that is exhausted, or above 95 percent of any window. An exact model id still works, and the start result says why in `model_note`. A provider ahead of its reset clock ranks below providers on pace. A start whose model's provider is exhausted is refused before any model call, with the provider, the limiting window and its reset time.
6. **Where it shows.** One compact line in the TUI header for this machine (`quotas: claude 59%w · codex 90%w · opencode-go 76%w · cursor exhausted`), and per machine in `agent_nodes` for agents. The suffix is the window kind: `w` weekly, `s` session, `m` monthly, `d` daily, `c` credits. The TUI Quotas tab shows `LEFT`; its bar fills with the remaining quota. `ahead` means usage is burning faster than the reset clock and stays warning-colored. A provider with a quota window is not grouped under "not set up". Subscriptions not used by registered agents have a separate fold; press `s` to show or hide the folded rows.
7. **Stale readings and failures.** A reading quota-axi marks stale is shown but never used to choose or refuse a model. A failed or slow run changes nothing: the last good reading stays. OpenCode Go credentials are read from OpenCode's `opencode.db` under `$XDG_DATA_HOME/opencode` (default `~/.local/share/opencode`) and then the legacy `auth.json`; `OPENCODE_DB` selects a non-default database file. When Node or npm is missing, the TUI Quotas tab and `agent-net setup` print: `quota-axi needs Node 22.19 or newer; install Node and agent-net installs quota-axi for you.`
8. **Turn off.** Turning the item off removes the private folder, as does `agent-net uninstall`.

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

## Tools inventory

`agent-net tools` groups the latest observations by kind, name and content
hash. Each row shows scope, harnesses, machines, the number of same-name
variants and state, desired assignment and pending application. Scope lists global and registered project observations;
unassigned identifies project locations that do not match the registry. Offline
machines retain their last observations as stale. The daemon scans at startup,
after reconnecting and every five minutes.

An in-home skill folder reached through a skill-entry symlink is recorded at its real path, with the link path in `aliases`. Links outside the home are not followed. A separate content fingerprint ignores executable bits, `.DS_Store`, `Thumbs.db`, and editor swap and backup files when comparing copies. The strict hash used for imports and integrity checks still includes these files and executable bits. Older observations without a content fingerprint are compared using their strict hash.

A project skill item has `tracked: true` when `git ls-files` lists files under it in the project checkout. The daemon runs one `git ls-files` per project root in each scan (limited to the four project skill folders, no network) and uses the answer for every skill in that root. A folder that is not a Git repository, or a failed `ls-files`, leaves `tracked` off. A scan from a daemon that is older than this field has no `tracked`, so its project skills show as `local` until it is upgraded.

The owner can request a refresh through `inventory_scan`. `inventory_get`
returns report pages with an `offset` request field and a `next` cursor.
`tools_items` returns item pages with optional `kind`, `harness`, `project`,
`state` and `offset` filters. `tools_item` takes an `id` from that list and
returns its locations and file-list differences against same-name variants.
These methods require the owner connection and cannot be called by an agent
session. A variant count includes all observed versions, even with filters.

A failed scan leaves the previous observations marked stale. The inventory is
read-only and does not execute skills or servers. MCP fingerprints omit
sensitive descriptor values. Extension-provided MCP servers, package-managed skills, OpenCode custom config
locations, Pi standalone Markdown skills and session-specific trust overrides need
manual inspection. A row summarizes observations, not proof that a running session has loaded it.

### Scoped MCP servers

A server in the [MCP registry](mcp-registry.md) is also a tool item of kind
`mcp`. `tools_items` marks it `federated` and shows its `scope` (`global`,
`projects` or `uninstalled`), the projects, its `host` and whether the host is
online. A server added with `agent_mcp_add` or `agent-net mcp add` starts
global, and a server that existed before scoping keeps that scope: no
assignment means global.

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
or the CTO on the owner's words, can plan and apply; an agent session cannot.
The CTO uses the same `tools_list`, `tools_plan` and `tools_apply` tools.

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
changed. `agent_mcp_list` and `mcp list` are the registry view of the owner and
of agents: they show every server and its `scope`.

### Importing native MCP servers

An MCP server that a harness has in its own config (`~/.claude.json` and
`.mcp.json` for Claude Code, `config.toml` for Codex, `opencode.json` for
OpenCode, `mcp.json` for Pi) can move into the federation. Import is explicit:
nothing is imported by a scan. See [decision 0027](decisions/0027-skills-and-mcp-management.md).

**Candidates.** `mcp_import_candidates` (daemon method and CTO read tool) lists
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

**Plan and apply.** `mcp_import` (daemon method, owner only; CTO tool) takes
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
only; CTO tool) takes exactly one of `candidate`, `plan_id` or `undo_plan_id`.

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
registered projects, item version, assignments or inspected paths have changed.
Applying records a new desired revision and nudges connected daemons. Each
daemon pulls its own targets again after reconnecting and on the regular scan
interval. A plan that would remove the last assigned copy of an unimported
skill is refused; import it first. For global skills the daemon fetches the stored version, verifies its hash
and installs a missing destination using a temporary directory beside it and an
atomic no-replace rename. A different existing destination is a conflict.
For project assignments it also copies into registered Git checkouts. The plan
uses shared compatibility roots where harnesses can read the same copy. A
combination that would expose the name twice, or an existing shadowing source,
is a conflict. Non-Git checkouts stay pending until the owner chooses an ignore
mechanism. Removals move the verified source to the daemon's local backup
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

The private CTO may inspect tools and propose plans. It may apply a plan only
on the owner's explicit words in the chat. Charter `needs` are advisory text;
the CTO tool list shows them as unverified needs and never turns them into
assignments.

### Managed skill versions

The owner can import an observed skill with the `tools_import` operator method.
Pass `item` as the ID from `tools_items` and `location` as an object with
`node_id` and `location` from that item's locations. The source machine must be
online. The owner should inspect the skill files for secrets before importing.
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

Without a registry, a project exists only after a session ran in it. The registry lets you (or an agent, when you ask) say "this repository is a project" ahead of time. Workers (see [decision 0002](decisions/0002-workers.md)) start sessions only in known projects, and a registered project is known even if no session ever ran there.

Each entry has the machine, the path of the main working tree, the name (its base name), the `origin` remote without credentials, the default branch (the branch `origin/HEAD` points to, when the clone has it), who added it (`cli`, or the harness and session), when it was added and when it was last checked. The gateway keeps entries in the `projects` table of `gateway.db`. `agent-net projects` merges them with the projects found from sessions, one row per repository per machine. SOURCE says where a row comes from: `registered`, `seen` (only sessions) or `both`. Registered projects stay when their sessions are deleted by retention.

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

**Agent tools** (Claude Code, Pi, OpenCode): `agent_project_add` (`paths`, or `dir` with `scan`, plus `depth` and `dry_run`) and `agent_project_list` (optional `name`). There is no tool to remove a project: removing is only done with `agent-net projects remove`. An agent that calls the remove method gets an error that tells it to ask you. The guidance text tells agents to register projects only when you ask.

**Versions.** A gateway from before this feature answers `unknown frame type` to the new requests, and the daemon turns that into "the gateway is too old for project registration". A daemon from before this feature does not know the new methods, and the CLI says so. An older gateway lists projects without SOURCE, and the CLI shows those rows as `seen`.

## Findings and the automatic playbook

The network learns from the agents that review workers. An agent records one **finding** with `agent_learn` (`agent_learn` in the MCP server and in the OpenCode and Pi plugins). It changes nothing by itself.

- **Subjects.** `subject_kind` is `model`, `harness`, `model_harness` (subject `model@harness`), `project`, `mcp` or `machine`. `subject` names it. `evidence` holds up to 20 session or worker IDs. A finding is at most 500 bytes.
- **Quiet.** Findings are never sent as messages and never appear in Needs you. A session records at most 3 findings in 24 hours; the fourth is refused with the reason.
- **Promotion.** The gateway promotes a pattern to a playbook note on its own, with no model call, when there are at least 3 findings on the same subject from at least 2 different sessions within 30 days, or when the track record is clearly bad (at least 4 decided runs, stopped runs aside, and under half complete). The note is the latest finding's text, the support count and, for a model, harness or model on a harness, the track record line. An owner note or a pinned note is never changed by promotion.
- **Stale.** An automatic note whose support is all older than 60 days goes stale and is no longer shown. A finding that is never promoted goes stale after 90 days. Owner notes and pinned notes never go stale.
- **Delivery is only at the moment of choice:** the `agent_start` result (notes for the chosen model, harness, project and machine) and `agent_nodes` (the network's fresh notes). Never in the session context of every turn.
- **The owner sees it when he looks:** key `L` in the TUI lists the notes with their support; the header quietly says `learned N new` for automatic notes promoted in the last day. Nothing pops up.

Findings are in the gateway table `findings` with their reporter, time and status (`new`, `promoted`, `dismissed`, `stale`). Notes are in `playbook` with their source (`auto`, `owner`, `cto`), support, track record line and pin.

## Workers

A worker is an agent session that agent-net starts for you, with nobody watching it. See [decision 0002](decisions/0002-workers.md) for the rules and [0008](decisions/0008-operator-interface-and-worker-build.md) for how they are built.

- **Who starts one:** you (`agent-net start`, the TUI) or an agent (`agent_start`). A worker cannot start workers. The harness is `opencode` (the default), `claude`, `pi` or `codex`. A caller can say "implement this with a Claude, Pi or Codex subagent".
- **Where:** the project is looked up in `agent-net projects` (registered or seen). With no project, the caller's own folder is used: it is found by its repository root path on the caller's machine, not by name; a caller outside a known project falls back to its folder name. `project` may also be an absolute path: it matches the main tree or a listed worktree of a known project (use it when two folders share a name). A name matches a registered folder before seen-only folders of the same name on the same machine; it is ambiguous only between two registered folders, or between seen-only folders with none registered, and the refusal lists the paths. If it is on two machines, name one with `--machine`. The machine must be online, accept workers (not set up with `--no-launch`), and have the harness (`agent-net nodes --wide`). The gateway checks the model against that harness's list on the machine. A harness whose list could not be read passes the name through, and a model the harness does not know then fails at start with the harness's own message. The gateway says which check failed.
- **Permissions of a worker** ([decision 0020](decisions/0020-worker-sandbox.md)). Nobody watches a worker, so it must never need an approval. Instead it runs in a sandbox, at a level that the owner sets per project (TUI key `p`, line Workers; the operator method is `session_policy` with `{"policy": {"workers": "read-only"}, "scope": "project"}`, owner only, refused at session scope). The level is stored in the project policy and sent with each start; `worker_start` answers it as `permissions`, and `list --json` shows it in the worker part of the session. When the project policy sets no level, the node's maturity default applies ([maturity and its defaults](#charters-and-component-relationships)); a node with no maturity keeps `sandboxed`.

  | Level | Writes | Network |
  |---|---|---|
  | `sandboxed` (default) | The worker's worktree, the temp folders, and `objects`, `refs`, `logs` and `worktrees/<name>` of the main repository's git dir (what a commit needs) | on |
  | `no-network` | The same | off |
  | `read-only` | None | on |

  The project list includes each project's `permissions` level. The TUI start form shows the selected project's level. The start result, the worker brief and worker notices show it too; the brief explains when writes or network calls will fail. The start result and brief say when the level is not enforced. A failed or blocked Codex turn with an explicit sandbox denial reports `blocked by the sandbox (<level>)` in the notice. A command's ordinary permission or network error alone does not prove a sandbox denial.

  **Codex only for now.** The Codex worker is started with approval policy `never` and the sandbox of its level (`thread/start` and `thread/resume`: `approvalPolicy`, `sandbox`, `config.sandbox_workspace_write`; `turn/start`: `approvalPolicy`, `sandboxPolicy`). Its private app server also gets `-c mcp_servers.agent-net.enabled=false` when `$CODEX_HOME/config.toml` defines the agent-net MCP server, so it does not call agent-net tools (its brief does not mention them either: the end of its turn is the report). Other MCP servers stay. The daemon still declines any approval that arrives. Claude Code, OpenCode and Pi workers record the level, and the TUI detail pane shows it on a `sandbox` line with "not enforced yet", but they behave as before.
- **How, per harness.** All run in the project folder (or the worker's worktree), and none gets a flag that turns permission checks off: a worker uses the project's own permission settings.

| Harness | Command | The model | The task and the report | Enter it later |
|---|---|---|---|---|
| OpenCode | `opencode serve` on `127.0.0.1`, random password | `provider/model`, checked against the machine's list | A normal message. The worker ends with `agent_handback`; the owner's next message starts the same session again. | `opencode --session <id>` |
| Claude Code | `claude --bg --name worker-<project>-<id> --settings <json> --mcp-config <json>` (a background session of Claude's own service, not a child of the daemon) | An alias (`haiku`, `sonnet`, `opus`, `fable`) or a full name, after `--model` | A normal message, delivered to the session's inbox. The worker ends with `agent_handback`, through the agent-net MCP server of this daemon's binary; the owner's next message runs `claude --bg --resume <session-id>`. | `claude attach <id>` (the id is the `jobId` in the session file) |
| Pi (needs `--isolation worktree`, or the daemon refuses it) | `pi --mode rpc --session-id <uuid> --name "worker <project>"`, standard input a named pipe that never closes | `provider/id[:thinking]`, after `--model` | A normal message, through the Pi extension, which loads in this mode. The worker ends with `agent_handback`, and it cannot be started again (Pi's rpc mode cannot open an existing session). | Not possible: Pi's rpc mode has no screen. Read it with `agent-net show`, or resume its session file in Pi after the worker ended. |
| Codex | `codex app-server --listen ws://127.0.0.1:<port> --ws-auth capability-token --ws-token-file <file>` | A name, in `thread/start`, with an optional `:<effort>` (`low`, `medium`, `high`, `xhigh` or `max`) sent as `effort` on every `turn/start` | The daemon runs each message from the owner or the person as a turn. The last message of the turn is the report, handed back by the daemon; the owner's next message starts a new app server and `thread/resume`s it. Approval policy `never` and a sandbox by [level](#workers). | `codex resume <thread id>` in the project folder |

  Claude Code needs the folder to be trusted (run `claude` in it once, or use a worktree of a trusted repository). The Pi extension and the OpenCode plugin must be installed on the machine (`agent-net setup`). The Claude session carries `crossSessionInbound: "accept"` and `AGENT_NET_SOCKET` in its per-session settings (`--settings`), so inbound agent-net messages are accepted; your `settings.json` is not changed.

  A Codex effort is checked at start against the model's `supported_reasoning_levels` in `codex debug models`. An effort the model does not list is refused, and the error names the levels it does. The model itself goes to `thread/start`; the effort goes to every `turn/start`, so it also holds after `thread/resume` and for a continued worker. The session, the start result and the notices show the full `model:effort`.

- **Difficulty and automatic model choice.** `agent_start` and the CTO's `worker_start` accept `difficulty`: `trivial`, `simple`, `normal`, `hard` or `very_complex`. Empty means `normal`. With an empty model or `auto`, the gateway chooses a registered worker profile whose harness and model are available on the selected machine. The default minimum scores are 0, 20, 30, 40 and 50. A score at least 10 above the minimum is excellent; one at or above the minimum is good; one up to 10 below is poor; a lower score is denied. The gateway ranks fit first, then quota room and recent failures. It skips exhausted providers and windows above 90 percent, and subtracts five quota-room points per running worker on the provider. For a monthly window, it estimates elapsed time from the preceding calendar-month reset. When usable quota readings are absent, equally ranked choices rotate. The result's `model_note` explains the choice. When the roster is empty, the harness default is used and the result says how to register a worker. Explicit model requests keep the resolution rules below.
- **Worker profiles.** The gateway stores a network roster with each profile's name, harness, model, optional effort, creator session and creation time. `agent-net workers profiles` lists it; `agent-net workers profiles add <harness> <model> [effort]` adds an entry, and `agent-net workers profiles remove <name>` removes one. The CTO can list, add and remove profiles. Non-worker agent sessions have `agent_worker_profiles`, `agent_worker_profile_add` and `agent_worker_profile_remove`; they should change the roster when the owner asks. Model names can be loose and must resolve to a model advertised by a machine for that harness. The default profile name combines the harness, resolved model and effort. Adding a profile with an existing name replaces its harness, model and effort in one call; the change is recorded as a profile add.

  Each machine resolves the exact model ID first. If that ID is missing, it can use the same model name after the first `/` under another provider, but only when exactly one provider lists that name. Several matches make the profile unavailable on that machine. Automatic selection skips the profile only on machines where it does not resolve. A start result reports a provider change in `model_note`, for example `terra pi: openai-codex/gpt-6.1-terra resolved to openai/gpt-6.1-terra on research`.

  The profile list includes `machine_availability` for online machines reporting the profile's harness. Each entry has the machine name and whether the profile resolves there. `resolved_model` is included when the machine uses a different model ID. Failed entries include up to three `closest` model IDs; `ambiguous` lists all IDs that share the model name under multiple providers. `available` is true when at least one online machine resolves the profile, and false when none do. `machines_checked` names the machines checked. Profile-level `closest` lists up to three alternatives when the profile resolves nowhere. If no online machine reports the harness, availability is unknown and `available` is omitted.
- **Worker providers.** Automatic selection uses the owner-controlled provider allowlist. When it is unset, Claude and Codex harness models, OpenCode and Pi models from `opencode-go` or `openai`, and providers with a usable quota reading are allowed. Other providers, including OpenRouter, are excluded by default. `agent-net scores providers` displays the setting; `agent-net scores providers codex claude opencode-go` replaces it, and `agent-net scores providers --default` restores the defaults. The CTO can change it on the owner's instruction. An explicit model request keeps its existing behavior. The Quotas tab lists registered profiles and marks those excluded by this filter. Among choices with the same fit and quota room, a recent successful worker ranks ahead of a model without one.
- **Protected providers.** The gateway stores the owner setting `protected_providers` and its `reason` in `owner_settings`. The owner orchestrates with one model of choice, today often Claude on an Anthropic subscription. Workers on that same subscription spend the limit the orchestrator needs to keep working. Automatic choice uses other subscriptions first and uses a protected provider only when no other available profile meets the difficulty minimum or the others are out of quota. With protection enabled, only profiles at or above the minimum qualify. The existing fit, quota and success rules rank the remaining profiles. The result explains protected profiles skipped or used, including the reason for a fallback. An explicitly requested model is not blocked by this setting and the result notes when its provider is protected. Claude harness and unprefixed `claude-*` models map to `anthropic`; Codex `gpt-*` maps to `openai`; OpenCode and Pi model prefixes identify their providers.

  Run `agent-net workers protect anthropic` to add a provider, or `agent-net workers unprotect anthropic` to remove one. The CTO can set the list and reason when the owner asks. The `protected_providers_get` and `protected_providers_set` operator methods are owner-gated: agent clients and command lines inside an agent session's process tree are refused. The Quotas tab marks protected profiles and subscriptions with a lock. When Claude is installed and no setting exists, setup suggests protecting Anthropic and shows the command; it does not apply the setting.
- **Scores.** The bundled table contains 85 model and effort scores from [Artificial Analysis](https://artificialanalysis.ai/). An effort suffix selects that effort; for a bare model the medium entry is used when no harness default is known, falling back to a default entry. Unknown scores are not candidates for automatic choice. Owner overrides win: `agent-net scores` lists scores, `agent-net scores set <model> <score>` adds an override, and `agent-net scores clear <model>` removes it. The CTO's private `score_set` tool can also change an override on the owner's instruction. The owner-only `difficulty_get` and `difficulty_set` operator methods read and replace the difficulty minimum overrides.
- **The task** is sent as a message from the caller, after the fixed brief. For Claude Code, Pi and OpenCode the worker ends its work with `agent_handback(report)`. For Codex the daemon does it when a turn ends: status `done` for a finished turn, `failed` for a failed or interrupted one, and `blocked` when Codex asked for an approval (the daemon declines every approval, which should not happen now that the worker runs in a sandbox, and the report names the command, the reason and the Codex thread to open). The caller gets one message with the facts (status, run time, model, tokens, the context at the end, the cost if the harness reports it, the worktree and branch, `git diff --stat`) and the report below them. Tokens come from OpenCode, from the Claude transcript, from Codex, and from Pi's session file. Cost comes from OpenCode only. The context at the end is the tokens of the last assistant message or turn (input plus cache reads and cache writes) and the model's context window. A worker that ends without a hand-back sends the same notice with the status `timed out`, `failed` or `stopped`. A worker that nobody messages for the idle time (60 minutes by default) ends as `timed out`; one that runs longer than 4 hours ends as `timed out` too. A worker is not idle while its harness says its session is busy, or while its token count grows: a long task does not hit the idle limit. An OpenCode worker whose model call fails (a provider, billing or auth error) ends at once as `failed`, and the note holds the error text (one line, secrets masked, cut to 300 characters). The daemon looks at the OpenCode session every few seconds: a run with outcome `failed` ends the worker. Only recognized provider errors demote the model; local and unknown failures do not. Codex reports a failed turn as a hand-back. Claude Code and Pi workers have no such check yet: a model error there shows only as the idle time-out.
- **Complete or partial hand-back.** `agent_handback` takes `complete`, a boolean that defaults to true. A worker that did only part of its task calls it with `complete: false`, and the run is counted as partial. The brief tells every worker to set it honestly. A Codex worker has no tools, so its brief asks it to end its report with `complete: yes` or `complete: no`; the daemon reads that last line. A Codex turn that failed or was interrupted counts as failed, whatever the line says.
- **Follow-up messages.** A worker ends when it hands back. A message from its owner to the same session ID starts it again on the same harness session, with the same model, project and worktree, and delivers the message. That is `agent_handback` for OpenCode, Claude Code and Pi, and the end of a turn for Codex. OpenCode, Claude Code and Codex can open the saved session again (a new private server on the same folders, `claude --bg --resume <id>`, or `thread/resume`). A Pi worker cannot be started again: Pi's rpc mode cannot open an existing session, and the refusal says so; read the old session with `agent-net show`, or open its session file in Pi. Only the owner can start a worker again; another sender gets a clear refusal. A Codex worker takes messages from its owner and from the person at the machine only: the daemon turns each into a turn, one after the other, and refuses other agents.
- **Ending:** a hand-back (any status) ends the worker after the notice, like a Claude Code subagent. Messages sent while a Codex or OpenCode worker is running wait for the current turn, and the worker ends after the last queued turn hands back. The idle time and the longest run time are safety nets: no message to or from it for 60 minutes while its session is not busy and its tokens do not grow; 4 hours since the start; its owner ended or stayed offline for 10 minutes; `agent_stop` or `agent-net stop`. For Claude Code the daemon runs `claude stop <id>` first, then kills the process group if the process is still there. The stopped session stays in `claude agents`, with its conversation, until you `claude rm <id>`.
- **Unfinished work is kept.** When a worker ends with no hand-back (`stopped`, `failed`, `timed out`, `owner_gone`, or lost while the daemon was down) and its worktree has uncommitted changes, the daemon runs `git add -A` and commits them on the worker's branch before the worktree is cleaned up, with the message `agent-net: unfinished work of worker <id> (<reason>)`. The author is the repository's own `user.name` and `user.email`; where the repository has none, it is `agent-net <agent-net@localhost>`. Hooks and signing are off for this commit. The notice says `unfinished changes were saved as a commit on branch <branch>`. A worker that was lost while the daemon was down gets its commit when the new daemon finds it lost, at start. A worktree with no changes is removed as before. A worker that handed back is not touched when it ends: its changes stay uncommitted in the kept worktree, and are committed only if someone continues that worker.
- **Continue (`continue`).** `agent_start` (and `worker_start`) accept `continue`: the session ID or worker ID of an ended worker that the same caller started. The gateway sends the request to the machine that ran the old worker, and says so clearly when that machine is offline. That daemon checks the owner against its record of the worker, and starts a new worker there: same project, harness and model unless the caller gives others, a new worktree on a new branch made from the old worker's branch (if the branch is gone because the old worker left no changes, from the project's current commit), and the old worker's full first task (the record keeps up to 64 KiB of it in `daemon.db`), after this line: `You continue the work of an earlier worker that ended (<reason>). Its unfinished changes are already in your worktree as the last commit. Review them critically, then finish the task.` An optional `task` is added as `Also: ...`. `project`, `machine`, `base` and `isolation` must be empty (a mismatching `project` is refused). Only the owner can continue a worker; errors say if the worker is unknown, still running, or not yours. The model of a continued worker is passed to the harness as given (it is not matched against the model list), with its `:effort` or `:thinking` suffix if it has one. It works while the record of the old worker exists: until the longest run time after the worker started (see the worker list below). A worker that worked in the project folder (isolation `none`) is continued in the folder, and a Pi worker still needs its worktree.
- **Daemon restart.** The daemon reconnects to surviving workers and keeps their session IDs. It resumes a Codex turn and restores the OpenCode server connection. If it cannot restore a worker, it stops the worker, saves unfinished work on its branch, and sends its owner a `lost after daemon restart` notice.
- **The worker list.** `agent-net workers` and the TUI show only workers that run. A worker that ended leaves the list, and its record is kept (ended) until the longest run time: a hand-back record is what starts the session again, and every ended record also tells the daemon that the harness session is a worker's, so the plugin of another OpenCode server may not register it as a normal session. A record that is offline and started longer ago than `max_age` is cleaned: on the machine at daemon start and every 10 minutes, and on the gateway (`worker.cleanup`), so an old worker does not stay listed for weeks. The cleanup never touches the record of a worker the machine still runs or has adopted: the daemon sends the session IDs it runs with `worker.cleanup` (and runs the cleanup only after it has taken its workers back), and the gateway keeps those. A worker that comes back after a daemon restart registers under the session ID it had, even if its record was lost. A worker that the machine says it does not run leaves the list at once.
- **Context and tokens.** Each worker carries `context_tokens` (what the model saw on its last step: input plus cache reads and cache writes), `context_limit` (the model's context window) and `tokens` (input plus output plus reasoning for the run, without cache reads). The daemon refreshes them on its worker tick while the worker runs, and with the final notice. OpenCode reads the newest assistant message and `limit.context` from `/api/model` on the worker's private server. Claude Code reads the last assistant usage in the transcript and takes the limit from a small table by model family (the 5 series, Opus 4.7 and later, Fable 5 and Mythos 5 have 1M; other Claude models have 200k). Codex reads `last` and `modelContextWindow` from `thread/tokenUsage/updated`. Pi reads the worker's JSONL session file under `~/.pi/agent/sessions` (or `PI_CODING_AGENT_SESSION_DIR`): the last assistant message gives the context and the entries give the run total. It takes the model's context window from `pi --list-models`, once per worker. The figures are in `agent-net workers --json` (also with `--recent`), on the worker's TUI detail pane as `context 84k / 1M (8%)  tokens 345k`, and on the Claude Code band after the run time as `ctx 84k/1M 8%` (the percentage is yellow at 70 percent and over, red at 90 percent and over). A harness that has not reported a figure leaves it out.
- **Defaults for a short request (W17).** "Use agent-net to implement X with a new OpenCode worker, use deepseek 4.1" is enough.
  - **Worktree:** when `isolation` is empty and the project is a git repository, the worker works in its own worktree. `isolation: none` (`--isolation none`) opts out, for read-only or non-code tasks. A folder that is not a repository gets no worktree. Pi always needs one. The `agent_start` tool accepts `base`, the name of an existing local branch, to start the worktree from that branch instead of the main working tree's HEAD. It requires worktree isolation.
  - **Project rules:** the target daemon reads `.agent-net/worker.md` from the project's main working tree, including uncommitted changes, so local rules take effect without a commit. It adds up to 8 KiB of cleaned text after the fixed brief and before the task. The file is optional.
  - **Project:** an agent that leaves `project` empty gets the project of its own session. If the project is on several machines, the caller's machine wins when it has the harness, else the one machine that has it. The command line must name a project.
  - **Model:** a loose name is resolved against the model list of the machine that runs the worker, for every harness that reported a list (OpenCode, Pi, Codex, and Claude's alias and full-name table). A harness with no list passes the name through. A suffix after the last colon that is a reasoning or thinking level (Codex's `:low`, `:medium`, `:high`, `:xhigh`, `:max`, Pi's thinking levels) is not part of the name: only the part before it is resolved, and the suffix stays on the result. An exact id wins. Else the name is split into words (case does not matter; `4.1` matches `v4.1`) and every model id that contains all words is a candidate. Candidates are ranked by: a worker that finished successfully (hand-back status done) on that machine and harness in the last 7 days; no worker failure in the last 24 hours (a model that failed a worker with a model error goes last, and the other models of its provider go after the other providers; an exact id still wins); sessions on that machine that used the model in the last 30 days; sessions that used any model of the same provider in that time; the order of `model_providers` in `node.json`; not `-free`; not a dated snapshot (`:0813`); the shortest name. A provider that is exhausted, or above 95 percent of a quota window, is skipped for a loose name; a provider ahead of pace ranks below providers on pace. The usage counts are all sessions of that harness on that machine that the gateway saw in the last 30 days, the user's own sessions included, not only workers. If one candidate is strictly best, it is used and the result says `model: luna 6 -> gpt-6-luna` (or `model: deepseek 4.1 -> opencode-go/deepseek-v4.1-flash`). When a matching model was ranked last for a recent failure, the note says so. If the best are equal, the start fails and lists up to five. If nothing matches, the start is refused before a worker is launched: `no codex model matches "luna 6" on research; closest: gpt-6-luna, gpt-6.1-luna` (at most five closest names, ranked by shared words and digits).
  - **Checklist:** every worker notice ends with a line from agent-net: read the diff in the worktree, run the project's tests there, report what you checked, merge only if your user asked. When the notice carries a report, the last line adds: `If you learned something that would change how to pick or brief this model next time, record it with agent_learn.`
  - `model_providers` in `node.json` is a list of provider prefixes to prefer, first is best: `"model_providers": ["opencode-go", "opencode"]`. Restart the daemon after a change.
- **Limits and load (decision 0018):** a worker starts only when the machine has room. The daemon keeps 1-minute and 5-minute averages of whole-machine CPU busy percent and of available memory. Before the rules are applied, the expected cost of every worker that started in the last 5 minutes (RAM and CPU) is added to the projected load, because the averages cannot show it yet. A start is let through when: fewer than `max` workers run (default 12, a safety net); the 5-minute available memory minus the expected RAM of the worker (the median of the average RSS of the last 20 workers of that harness and model, at most 25 percent of the machine's RAM, 600 MB with fewer than 2 workers in the history), minus the RAM of the workers that started in the last 5 minutes, minus the reserve (the larger of 1.5 GB and 10 percent of RAM) is above zero; the 5-minute CPU average plus the expected CPU of those workers is below `cpu_target` (default 75) and the 1-minute average, with the same addition, is below 95; and the last start that ran on the machine was at least `start_gap` ago (default 6 seconds). A start that failed, or a worker that ended within 30 seconds, does not hold the gap: it did not add load the averages could show. Running workers are never stopped for load. On Linux, available memory is `MemAvailable`; on macOS it is total RAM minus app, wired and compressed memory.
  Settings are in `node.json`: `"workers": {"max": 12, "cpu_target": 75, "mem_reserve": "2GB", "queue_timeout": "2h", "start_gap": "6s", "idle": "60m", "max_age": "4h", "owner_grace": "10m"}`. All are optional; `mem_reserve` defaults to the larger of 1.5 GB and 10 percent of RAM, and `start_gap` defaults to 6 seconds (`"0s"` or a negative duration turns the gap off). Restart the daemon after a change.
- **The queue.** With no room, `agent_start` and `agent-net start` answer `status: queued` with a `queue_id` (`q_...`), the place in the queue and the reason, for example `queued #2 on research: CPU 5-min average 82% (target 75%)`. The queue is per machine, first come first served, and is kept in the daemon database across restarts. The daemon looks at it every 15 seconds. When a start begins, the caller gets a worker notice (`status: started`, the session ID and the queue wait) and the task is sent. A start still queued after `queue_timeout` (2 hours), or one that fails when its turn comes, ends with a notice with `status: not started`. If the caller's session is offline for `owner_grace`, the queued start is dropped. `agent_stop` and `agent-net stop` take the `queue_id` to cancel (the owner of the start or the person at the command line only). When the project is on several machines and each reports its room, the start goes to the one with the most room, and the result says why (`machine_note`).
- **Room in the owner views.** Each machine reports `workers N running, M queued - room: ok|cpu|mem|cap|gap|measuring` (with a middle dot) in its usage report. `agent-net workers` prints one line per machine that has workers or a queue above the table, then a table of queued starts (queue id, position, machine, project, harness, model, owner, queued time, reason); `--json` has them as `queued`; the TUI shows it on the machine line when the machine has workers, a queue, or no room. In the gateway, `node.usage` carries it as `workers` (`running`, `queued`, `room`, `headroom`, `reason`). New frames: `worker.queued_start` (machine to gateway) and `worker.task` (gateway to the owner's machine); `worker.kill` carries `requester_session` and `as_owner` for a queued start.
- **Owner:** `agent-net workers` and the TUI show who started each worker. A worker started from the command line or the TUI is owned by `you (on <host>)`.
- **Events:** `worker.started`, `worker.stopped` (with `reason=handback|idle|max_age|owner_gone|stopped|process_exited|lost|model_failed`) `worker.queued`, `worker.queue_cancelled` and `worker.refused`, in the gateway log, the events table and the TUI feed. `worker.cleaned` is in the gateway log.

### The playbook and the track record

The gateway learns which harness and model do well from the runs it already records. When a machine reports `worker.stopped`, the gateway adds one row to `worker_runs` (node, harness, model, outcome, start, end, run time). The outcome is `complete`, `partial` (`complete: false`), `failed` (a blocked or failed hand-back, a process that ended by itself, or a run that errored), `timed_out` (the idle or longest-run limit) or `stopped` (a stop on request, a worker lost at a daemon restart, an owner that went away, or a start that never ran the task). A stopped run is neither a success nor a failure: it is counted apart and never makes a model look bad. The gateway keeps the rows for 30 days (`--keep-worker-runs`) and counts them per harness and model.

The line it shows, in `agent_nodes` for each machine and in every `agent_start` (and `agent-net start`) result for the chosen harness and model, is:

```
terra · pi: 4 runs: 1 complete, 3 partial (median 9 min) · cannot resume
deepseek-v4.1-flash · opencode: 14 runs: 13 complete (median 26 min) · resumes
gpt-6-luna · codex: 3 runs: 2 complete, 1 stopped (median 6 min) · resumes
```

`default` stands for a worker that ran without a resolved model name. The last part says whether the harness can open an ended session again: OpenCode, Claude Code and Codex can, Pi cannot. Before the first run the line says `no runs yet`. The start result carries it as `track` (`harness`, `model`, `runs`, `complete`, `partial`, `failed`, `timed_out`, `stopped`, `median_run_seconds`, `resumable`), and `agent_nodes` as `tracks` on each machine.

The owner can write a **playbook note** about a model (and optionally one harness) with `agent-net playbook set`, or the operator methods `playbook_set` and `playbook_get` (owner gate only; the CTO will use the same methods later). The note appears in the `playbooks` list of the start result and in `agent_nodes`. Automatic notes from findings are built the same way; see [findings and the automatic playbook](#findings-and-the-automatic-playbook). The gateway stores notes in `playbook`, one row per subject and harness, at most 1 KiB for an owner note.

**The operator interface.** The daemon's local socket serves these methods to the command line and the TUI, and the phone app will use the same ones: `info`, `nodes`, `list`, `projects`, `events`, `message_status`, `workers` (reads) and `owner_send`, `worker_start`, `worker_stop` (actions). The owner-only methods include `playbook_get` and `playbook_set` (see [the playbook](#the-playbook-and-the-track-record)). The types are in `internal/protocol/operator.go`; `worker_handback` is the worker's own call. Agent connections may use `worker_start`, `worker_stop`, `worker_handback`, `agent_learn` and `workers`. `owner_send` is for the command line only; `agent_learn` is for agent sessions only.

## The TUI screen

`agent-net tui` has six tabs: `1` Sessions, `2` Messages, `3` Quotas, `4` Map, `5` Tools and `6` Graph. On each tab, `tab` moves focus between the tab and CTO chat. In the CTO box, replies use Markdown; pgup/pgdn or ctrl+u/ctrl+d scroll the conversation, enter sends and esc returns to the list. When the input is empty, `o` opens the full interface, `n` starts a new conversation after confirmation, `u` undoes the last graph change, and `m` edits the harness and model. Messages groups the feed into conversations. Use up/down to select a conversation, left/right to select a message, `v` for its full body, `e` to show system events, and `l` to cycle the event filter. Use `/` to filter the list.

The Graph tab keeps its graph, camera, selection and traffic in a headless state. Actions update that state and return effects for the tab to perform. A scene describes what to show independently of the terminal, and a renderer draws it. The current renderer uses Braille; a kitty pixel renderer can consume the same scene later.

`agent-net tui` needs a terminal. It talks to the local daemon through the operator interface, as an owner's CLI (`hello` with `client: cli`). Besides the reads and the actions below it uses the owner-only methods `usage`, `counts`, `usage_top`, `session_text` and `session_enter` (see [CPU and memory](#cpu-and-memory) and [security.md](security.md#session-text-and-resource-figures)). It shows, from top to bottom:

- **Tabs.** Press `1` for Sessions, `2` for Messages, `3` for Quotas, `4` for Map, `5` for Tools or `6` for Graph. The Quotas tab merges readings by provider and plan across machines and shows each window, pace, reset time, reading age and running workers. Providers not set up are folded together; press `s` to show or hide them. Its Workers panel lists registered profiles, scores, fit and current picks at each difficulty, and worker run outcomes. The letters T, S, N, H and V mean trivial, simple, normal, hard and very complex. Enable the Quota integration and agent-net installs quota-axi, or uses the copy you installed (see [subscription quotas](#subscription-quotas)).
- **Graph** on tab 6 shows projects, architects, their relationships and recent message traffic. Press `+` or `-` to zoom, arrows or `h`, `j`, `k`, `l` to pan, `0` to fit, and `[` or `]` to select a node. `o` arranges nodes in rings around the selection, enter opens it in Map, space pauses traffic and `?` shows the keys. Tab focuses the CTO box. The graph uses Braille characters now; pixel graphics are planned for terminals that support them.
- **Header.** `12 online · 3 busy · 1 needs you · workers 4 running, 1 queued`: the gateway's online count from `counts`, the sessions that report `busy`, the sessions that wait for you (in the warning colour, and left out when none wait), and the workers with the queued starts from the admission queue. Offline sessions are left out: they are mostly old ghosts. An older gateway has no counts: the header then says `N sessions, M online` and the same figures after it. A warning on the title points to tab 3 when a subscription window is above 80% or exhausted. The header also shows the inbox count, the learned count, the active filters (`filter: active, /cogforge`), and the time of the last refresh.
- **Needs you,** a section above the machines when at least one session waits for you (decision [0016](decisions/0016-needs-you.md)). Each row has a warning mark, the session name, its harness, its machine when that is not this one, how long it has waited, and what it asks: `permission: Bash(git push origin main)`, or the question in quotes. Oldest wait first. At most five rows; more are counted in one line. The section follows the filter and the offline switch, and it is hidden when nobody waits. Its rows are the same sessions as in the table, so enter, `e` and `m` work on them. A waiting session also says `needs you` in STATUS, in the warning colour. The two sources: the Claude Code `needs-hook` hooks, and the OpenCode plugin (a permission request or a form); any other agent can say it with `agent_announce` and `waiting`.
- **Machines,** one line each: online state, name, agent-net version, CPU now with a bar and the 5 minute average in brackets, memory used and total with a bar, 1 minute load, and the installed harnesses when the line has room. A machine with the user's shared OpenCode service shows a short dim note here (`opencode service 2.1G 9 procs`). This machine comes first and is marked `(here)`. The name is the gateway's name for the machine, and `(here)` is matched by node ID, not by hostname. A figure that is missing or older than 45 seconds reads `?`.
- **Sessions,** grouped by machine under a heading: NAME, AGENT (the harness), PROJECT, MODEL (shortened: no provider, no `claude-` prefix), STATUS (`idle`, `busy`, `shell`, `needs you`, `dorm` for dormant, `off` for offline), TRUST (`-`, `ask` or `auto` for the node wildcard rule, with `+N` for sender-specific edges; always shown), POLICY (only when some session has a [delegation policy](#delegation-policy): `own`, `sub` or `wrk`), CPU, RAM and TASK (the task the session announced; a worker without one shows the first line of its task; otherwise the harness title if it differs from NAME, else blank). Sessions stay in the order they were first seen, oldest first, so a new session appears at the bottom of its machine and nothing re-sorts a row. A session that is idle with no report for more than an hour is dim. Offline sessions are dimmed too. On a narrow terminal TASK, MODEL, PROJECT and POLICY go first; TRUST always stays. A tree with more than 20 processes, 200% CPU or 4G of memory shows its numbers in red. A process tree that several sessions share shows `shared` in their rows and has no row of its own. A worker's private OpenCode server is its own tree, so its CPU and memory are on the worker's row.
- **Selected session.** The first line has the name, harness, status and the session's CPU, RAM and process count. Below, on the left: session and worker IDs and PID, folder, branch, model and mode (these last two moved here from the table), function (the project description from `AGENTS.md`; "none: add a `# Project: <one line>` line to AGENTS.md" when there is none), remote, worktree (when the session runs in a linked worktree), the project source (`seen in a session`, `registered`, or `seen in a session and registered`), task, expertise, a `trust` line when peer trust is on, `policy` and `main` lines when it has a delegation policy (with a note for each part its harness cannot do), and its recent messages. For a worker it starts with who started it and then `context 84k / 1M (8%)  tokens 345k`, with only the parts the harness has reported. On the right: the heaviest 5 processes by CPU and by memory (PID, CPU, RAM, command line with secrets masked), fetched with `usage_top` when the session is selected and again every 5 seconds. Press `d` to replace the session list with these details; press it again to return. The CTO box stays visible.
- **Workers** show in the sessions table right under the session that started them, indented with `├─` (or `└─` for the last one), in order of start time, like a sub-agent. A worker's NAME is `<project> worker <n>` (decision [0024](decisions/0024-session-names.md)), and its TASK is the first line of its FIRST task, which the daemon records and the gateway passes in the agent list; a later request from its owner does not replace it. When that task is missing, TASK is the harness title, or blank. The full worker ID is on the detail pane's `id` line. A worker on another machine than its caller still sits under the caller and has `@host` after its name. A worker whose caller is not on screen (you started it, or the caller is offline or hidden by the filter) stays in its own machine group with `↳` before the name. Selection keys follow the order on screen. The detail says the worker runs in the background. The title counts them.
- **Messages** on tab 2 shows one line per message on the network: the hour and minute, the two session names, the type, the first line of the body when this machine has a session in the conversation, and one delivery symbol at the end (`…` pending, `✓` delivered, `↩` replied, `✗` failed or expired). A reply that directly follows the message it answers is indented with `└`. Names are session names only; a short `@host` is added only when two sessions in view share a name. The body line is fetched once, in the background, through the same `message_status` call the conversation view uses, and cached by message ID. A message between two other machines shows the type and no body in the feed; the owner can open it to request the body from a participant machine. New session lines are hidden by default, and worker lines (started, stopped or refused) are dim; `l` shows every lifecycle line, then hides all of them, then returns to the default. Machines connecting or disconnecting and refused sends also have a line. Messages from you show as `you`; messages sent by the CTO show as `CTO` in purple. The panel title shows the current event filter.
- **A result line** under the title says what the last action did: plain when it worked, `!` and red when it did not. The next key clears it.
- **Conversation** (enter or `c` on a session). The whole screen shows the prompts, replies, tool calls and tool results of the session, newest last, each with its time and a role label (`user`, `assistant`, `tool`, `result`). Text is wrapped and shown as the daemon sent it, with its masking marks. The view follows new entries once a second; scroll up and it pauses (`G` follows again); page up past the top loads older entries by cursor; esc goes back.
- **Help** (`?`). Lists the keys and what the columns mean. The key line at the bottom shows the main keys and drops the least useful when it does not fit.

The screen refreshes once a second. CPU, memory and counts are asked every 5 seconds, because daemons report every 10 seconds. If the daemon is not running, or cannot reach the gateway, a banner says so, the last data stays on screen, and it retries every second. A daemon or gateway from before CPU and memory shows `?` and is not asked again.

| Key | What it does |
|---|---|
| up, down, `k`, `j`, `g`, `G`, page up, page down | Move the selection (or the message cursor, when the messages have the focus). |
| enter, `c` | Open the conversation of the selected session. With the messages in focus, open the conversation of the end on this machine (or the message body when there is no such end). |
| `v` | With the messages in focus, open the body of the message. |
| `l` | Cycle the session and worker lines in the feed: worker lines (the default), all of them, none. |
| `/` | Filter the sessions: type words, all must appear in some field of a session (name, harness, machine, project, branch, model, mode, status, function, task, folder, ID). Enter keeps the filter, Esc clears it. |
| `o` | Show or hide offline and dormant sessions. |
| `h`, `H` | In Sessions and Map, hide or unhide the selected session or project node; `H` toggles showing hidden ones as dim rows. |
| `a` | Active only: hide sessions that are idle and have had no report for more than an hour. Each machine heading says how many it hides, and the header lists the active filters. |
| `1` to `6` | Open Sessions, Messages, Quotas, Map, Tools or Graph. |
| tab, `C` | Focus the CTO box; tab or Esc returns focus to the sessions. |
| `d` | Toggle selected session details in place of the session list. |
| `?` | Show or hide the help. |
| esc | Leave the conversation, the help or a message body, or clear the filter. |
| In the conversation: up, down, page up, page down, `g`, `G`, `f` | Scroll; `g` goes to the oldest loaded entry and loads older ones; `G`, end or `f` follow new entries. |
| `r` | Refresh now. |
| `m` | Send a message to the selected session as yourself. Type, Enter sends, Esc cancels. The reply shows in the messages. |
| `s` | Start a worker. A form takes Project, Harness, Model and Task. Project completes from the project list (a name on two machines is offered as `name@machine`), Model from the model list of that project's machine. Tab completes, then moves on; up and down or Enter move between fields; Enter on the Task starts it; Esc cancels. If the selected session's project is known, the form starts on the Task with it filled in. |
| `x` | In Sessions, stop the selected worker after a `y/n` question. Any other session is refused with a message. |
| `t` | Cycle the selected node's wildcard request rule through `normal`, `ask` and `auto`. |
| `N` | Name the machine of the selected session ([machine names](#machine-names)). A one-line prompt starts with the name shown now. Enter saves, an empty name clears it, Esc cancels. Every machine shows the new name. |
| `p` | Set the [delegation policy](#delegation-policy) of the selected session or its project. A form takes Delegate (left and right: none, off, native, workers), Coder, Main, Workers (the [permission level](#workers) of workers started in the project: default sandboxed, sandboxed, no-network, read-only; choosing one sets the scope to project) and Scope (session, project). Each move to another field shows what the loose model names resolve to; Enter on Scope saves; Esc cancels. |
| `e` | Enter the selected session: the TUI hands the terminal to it and comes back when you leave. See [Enter a session](#enter-a-session). |
| `q`, ctrl+c | Quit. |

The feed quotes the first line only for messages involving a session on this machine. The owner can open another message from the command line or TUI: the daemon checks the owner gate and asks the gateway to relay the request to a participant machine. That machine can answer only for messages involving its own sessions. If all participant machines are offline, the screen names a holding machine and says it is offline. Messages that have expired remain unavailable. The feed asks for each local body once, keeps the first line, and does not ask again. Text from the network is stripped of control characters before it is drawn, so an agent name or a message cannot move the cursor or change the screen.

**Event feed.** The screen reads the gateway's events through the local daemon method `events` (`{"after_id": N, "limit": N}`) and the gateway frame `query.events`. Any enrolled machine can ask. It returns the events of kinds `msg.*`, `session.*`, `node.*` and `worker.*`, oldest first, as metadata: the event kind and time, the machine, the session, the message ID, and for messages the two session IDs, the type, the ID of the message it answers, and whether the CTO sent it. Never a message body. Machine addresses are left out, details are cut to 200 characters, and MCP, enrollment and retention events are not included. With `after_id` 0 you get the newest `limit` events (default 100, at most 500). An older daemon or gateway answers that the method is unknown, and the screen then shows the sessions without the feed.

### Enter a session

`e` in the TUI asks the daemon (`session_enter`) how to take over the terminal for the selected session, runs the answer, and draws the TUI again when you leave. The daemon of the machine that holds the session works out the command. The TUI builds none. Press `ctrl+]` to return to the TUI; the session keeps running. This also works when the session uses enhanced keyboard reporting. The TUI closes the attach client. Your own input in the session counts as yours, so approvals you give are yours.

| Session | What `e` runs |
|---|---|
| Claude Code background session (`kind` bg, which includes Claude workers) | `claude attach <id>`. The left arrow returns to the agent view, ctrl+z drops to the shell, the session keeps running. |
| Claude Code interactive session | Cannot be attached twice. If its process runs in a herdr pane, `herdr agent attach <pane>` (see below). Else, if its process runs in a tmux pane, the TUI attaches a new client at that pane, including inside the same tmux server. Otherwise: "open its window on <machine>". |
| Pi | The same herdr and tmux rules. A Pi worker has no screen, so there is nothing to enter. |
| OpenCode | `opencode --session <id>`, in the session's folder, through OpenCode's own background service. |
| OpenCode worker | `opencode --server <its private url> --session <id>`, with its password in `OPENCODE_SERVER_PASSWORD`. Only from the machine that runs it. |
| Codex | `codex resume <thread>`, in the thread's folder. It joins the shared Codex app server, so you get the thread that is open there. With a `CODEX_HOME` other than `~/.codex` the plan is `env CODEX_HOME=<it> codex resume <thread>`. |
| Codex worker | `codex resume <thread> --remote ws://<its private address> --remote-auth-token-env AGENT_NET_CODEX_TOKEN`. Only from the machine that runs it. The daemon drives the same thread, so do not type while it works. |

**herdr.** A session whose process (or a parent) has `HERDR_ENV=1` and `HERDR_PANE_ID` in its environment is entered with `herdr agent attach <pane>`. The same plan is used when the TUI is outside herdr, inside the same herdr, or inside another one: herdr's `agent focus` does not move a client that is already open, so the TUI attaches instead. The daemon sends `HERDR_SOCKET_PATH` of the session's herdr in the command (`env -u HERDR_SESSION HERDR_SOCKET_PATH=<socket> herdr agent attach <pane>`), so it also works through `ssh -t`. If the pane moved or closed, the daemon looks for the pane that holds the process, and says so when none does. The herdr program is found from the pane's `HERDR_BIN_PATH`, `PATH`, then `~/.local/bin/herdr`.

- Herdr detach keys also work: `ctrl+b q`, or `ctrl+b ctrl+b q` when the TUI runs inside herdr.
- A pane takes one direct attach. If another `herdr agent attach` is open on the same pane, the new one fails with "already has an attached client". agent-net never uses `--takeover`: close the other attach first. Your normal herdr window is not affected.
- On macOS the session's environment is read with `sysctl kern.procargs2` for processes of your user. This also finds tmux servers other than the default one.

When the plan has nothing to run, the answer has only a note, and the TUI shows it in the result line.

**A session on another machine.** The question goes through the gateway to that machine. By default the TUI runs the command there through `ssh -t -o ConnectTimeout=5 -- <target> <command>`. To change the target, or to turn ssh off, set in `node.json` on the machine where you use the TUI, and restart the daemon:

```json
"enter_ssh": false,
"enter_ssh_hosts": {"build-box": "me@build-box.example"}
```

`enter_ssh` is on by default. `"enter_ssh": false` turns it off, and then `e` shows the command to run there instead. `enter_ssh_hosts` is optional: it maps the machine's name (as `agent-net nodes` shows it, so its [display name](#machine-names) when it has one) to the ssh target, which can be an alias from `~/.ssh/config`. Without an entry the name itself is used. The target must be a plain host name or `user@host`. ssh runs in your terminal, with your own keys and ssh config, so it may ask for a password, a key passphrase or a Tailscale SSH check. If ssh exits with status 255 (it could not connect or log in), the TUI shows the command to run on the machine, with a hint to set `enter_ssh_hosts`. The secrets of a worker's private server never leave the machine: workers of OpenCode and Codex on another machine can only be entered from a TUI on that machine.

## Peer trust and hints

Decision: [0013](decisions/0013-peer-trust-and-hints.md). Security: [security.md](security.md#peer-trust).

**Owner instruction markers.** For a Claude Code owner prompt, the gateway considers the first subsequent send within 10 minutes. If the prompt names the recipient session or project, ignoring case and matching whole names, the message carries up to 200 characters of the prompt. A non-matching first send consumes the prompt. The Messages tab shows `you→` and the excerpt. Claude Code, Codex, Pi and OpenCode append a note after the message body with the excerpt and say it is not approval for risky actions.

**Request rules.** The gateway chooses an edge rule from the sender's project node to the recipient's project node. A specific sender edge wins over the wildcard edge from `*`. With no edge the rule is `normal`: deliver the request under the usual peer guidance. `auto` delivers it with the peer trust note. `ask` holds it for your decision. `never` refuses it with `refused by your user's rule`. The TUI `t` key cycles the wildcard rule through `normal`, `ask` and `auto`; the TRUST column also counts sender-specific edges. `session_trust` remains an owner-only compatibility method that sets the wildcard rule to `auto` or `normal`. At upgrade, trusted sessions with a project get a wildcard `auto` edge, without overwriting an existing edge.

A held request appears in Needs you with its sender, recipient and first line. Select it and press `a` to approve once, `A` to approve and set its sender edge to `auto`, `r` to decline once, or `R` to decline and set that edge to `never`. The owner-only operator methods `held_list` and `held_answer` expose the same actions. Approval delivers the request with an agent-net approval note. A decline sends the sender `your user declined this request`. A held request expires at its normal message expiry and the sender receives a notice.

**Expertise.** `agent_announce` takes `expertise`, one short line. `agent_list` and `agent_get` show it, `list --json` has `profile.expertise`, `agent-net list --wide` has an EXPERTISE column, and the `text` filter searches it.

**Hints.** The local method `hint` takes `{"text": "...", "failed": true}` (and, from the command line, `harness` and `native_id` of the session) and answers `{"suggestions": [{"session_id", "name", "hostname", "why"}], "text": "<one line>"}`, or an empty object. The Claude Code hook (`agent-net hint`), the Pi extension and the OpenCode plugin call it after a failed tool call. How each shows the line: Claude Code adds it as `additionalContext` of the hook output, Pi appends it to the tool result, OpenCode adds it to the system prompt of the next request of the session. See the decision for the matching rules and the limits (at most 2 suggestions; the same suggestion once per 30 minutes per session; one hint per 45 seconds per session).

The Claude Code hook output, when there is a suggestion (one line of JSON on stdout, exit 0):

```
{"hookSpecificOutput":{"hookEventName":"PostToolUseFailure","additionalContext":"agent-net: another session may own or see this. ..."}}
```

## Needs you

A session can wait for you: it asks permission for a tool, asks a question, or wants your input. The TUI lists those sessions above the machines (decision [0016](decisions/0016-needs-you.md)). Every machine's TUI sees them, so one screen answers "what needs me" across the network.

The waiting state is three fields on the session: `kind` (`permission`, `question` or `input`), `text` (one line, cut to 200 characters, control characters removed) and `since` (Unix seconds, set when it began). The daemon sends it with the session to the gateway, which stores it like the status, and every machine reads it from the agent list. An empty waiting clears the state.

It is cleared when the session works again (the daemon clears it when the status becomes `busy`), and when you answer: a Claude Code prompt, an OpenCode prompt, a permission or form that is settled, or any announce without `waiting`.

Where it comes from, best first:

- **Claude Code:** the `needs-hook` hooks, added by `agent-net setup --claude-needs-hook` (see [what setup changes](#what-setup-changes-on-your-machine)). `PermissionRequest` fires at once and gives the tool, so the line reads `permission: Bash(git push origin main)`. `Notification` adds idle and MCP elicitation waits. `UserPromptSubmit` clears it. The hooks talk to the local daemon only, give up after 300 ms and print nothing.
- **OpenCode:** the plugin reports a `permission.asked` as a permission with the action and its resources, and a `form.created` as a question with its title. `permission.replied`, `form.replied`, `form.cancelled` and a new prompt clear it.
- **Every harness:** `agent_announce` takes an optional `waiting` string, the question in one line. The guidance and the tool descriptions ask every agent to set it when it stops for your decision.

The local daemon method is `waiting` (`{"kind", "text"}` or `{"clear": true}`). A plugin is bound to its session; a command line names `harness` and `native_id`, and is silent when the session is unknown. It is not an owner-only method: a harness hook must be able to call it for its own session, like `hint`.

## Delegation policy

Decision: [0014](decisions/0014-delegation-policy.md). Security: [security.md](security.md#delegation-policy).

A per-session or per-project setting that says who does the coding. Set it with `p` in the TUI. Values: `delegate` (`off`, `native`, `workers`), `coder_model` and `main_model` (loose names, resolved against the model list of the session's machine and harness), scope (`session`, `project`). The session's own value wins over its project's. The operator method is `session_policy` (`{"session_id": "...", "policy": {"delegate": "native", "coder_model": "deepseek 4.1", "main_model": "sol 6.1"}, "scope": "session", "dry_run": false}`), served only to the owner's command line or TUI. It answers `{"session_id", "saved", "effective"}`; `effective` is the policy the session ends up with, with `coder_resolved`, `main_resolved` and the errors of a name that did not resolve. A dry run saves nothing. An empty policy clears the value at that scope. `list --json` has `delegation` (`scope` says where the effective value comes from).

The gateway stores the policy in `sessions.delegation` and the project defaults in `project_policies` (migration 10), and logs a `session.policy` event and line when one is saved.

**What the session gets.** The local method `policy` (every turn; a session asks for its own, the command line asks by `harness` and `native_id`) answers `{"text", "mode", "agent_id", "coder", "main", "charter"}` or an empty object. The daemon asks the gateway (`policy.get`) and caches the answer for 2 seconds. `charter` is the node's charter context of [charters and component relationships](#charters-and-component-relationships); it is there even when the session has no delegation policy. `text` is one line that starts with `[agent-net]`:

- `native`: `[agent-net] Your user's standing policy for this session, set in agent-net: delegate all code changes to the `agent-net-coder-<model>` subagent (model <model>). You plan, write the brief, review its diff and run the tests. Do not edit code yourself unless the subagent fails twice.`
- `workers`: the same, but to start a worker with `agent_start` with the coder model.
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

The gateway keeps a graph of components and architects: a node per project (and a node for an architect that owns no code), a charter for each node, and directed edges (`depends`: consumer to provider; `governs`: architect to component, with mode `advisor`, `arbiter` or `owner`; `deploys`: deployer to each app it deploys). A session speaks for the node of its project, as the PROJECT column resolves it. The graph is visible to every enrolled machine.

**Writing a charter.** The project's own agent reads its charter on every turn, so budget about 1200 characters (roughly 300 tokens) for the whole charter. Neighbors read only the first 160 characters of `accepts` and `refuses` at most, so put the most important item first. The owner reads `purpose` on the Map card: use one sentence up to 160 characters, or 2-3 lines at 80 columns. For `accepts`, `refuses` and `escalate`, use at most 6 categories, each a short phrase separated by semicolons or newlines. Use names and pointers for `feature_requests`, `consumers` and `needs`; use `notes` only for rules nothing else covers. Never put secrets, IP addresses, tokens or paths in a charter. `charter_set` and `agent-net charter set` return advisory warnings about the budget and sensitive details; those warnings do not stop a write. The existing 2 KiB value limit still applies. Put detail in the project's `AGENTS.md`. The shared source for the guidance is `internal/protocol/charter_guide.go`.

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

**Maturity and its defaults.** A node's `maturity` level sets five controls. A node may override any one of them in a node setting; the owner sets it with `node_settings_set`, and the override is stored beside the charter.

| | `early` | `growing` | `mature` | `critical` |
|---|---|---|---|---|
| Tests before merge (`tests_before_merge`) | `optional` | `suite` | `suite-new-tests` | `suite-new-tests-review` |
| Merge authority (`merge_authority`) | `full` | `full` | `delegated` | `ask` |
| Worker sandbox (`worker_sandbox`, decision 0020) | `sandboxed` | `sandboxed` | `sandboxed` | `no-network` |
| Requests from consumers (`judge_consumers`) | `free` | `charter` | `strict` | `escalate` |
| Deploy rule (`deploy_rule`) | `brief` | `brief` | `ask` | `owner` |

An empty or missing maturity uses the `growing` controls, but no maturity rules are written into the node's text. The text the session and its workers get is plain words, for example `maturity mature: run the suite and add tests for new behavior; merge needs your owner's or a delegate's approval; ask before deploying`, followed by the consumer rule. A node with no maturity gets no rules text.

The worker sandbox level from decision 0020 follows the node: the owner's project policy wins when it sets a level, else the node's `worker_sandbox` default applies. A node with no maturity and no override keeps `sandboxed`.

**Merge authority.** `full` lets the agent merge and push to the default branch. `delegated` needs an approval from the owner or a delegate: an architect with mode `owner` or `arbiter` over the node. `ask` needs the owner's written approval. `MergeApprovalFor` returns the authority, whether approval is required, whether it must be written, the delegates and the text. The owner's inbox that records written approvals is step 3b and is not in this build: `RequestWrittenApproval` is a stub that says so. Only an approval note that agent-net itself writes counts, and it covers one action.

**Deploys.** On a `deploys` edge the deployer applies the target's deploy rule, not its own. The deployer's per-turn context names each target and its rule, for example `You deploy shop; its maturity critical: ...; ask the owner before deploying`.

**Every turn.** The daemon reads the node of the session's project and puts a short charter text into the same places as the delegation policy: the Claude Code `UserPromptSubmit` hook (`additionalContext`), the OpenCode `context` hook, and the Pi `before_agent_start` system prompt. Codex has no turn context today: the policy is not applied there either, but a Codex worker gets the charter in its brief. The text holds `maturity`, `accepts`, `refuses`, `escalate`, the maturity rules and consumer rule, the edges that matter to the node (who governs it and in which mode, what it depends on, what depends on it, for an architect what it governs, and for a deployer each target's deploy rule), and the decide rule. It starts with `[agent-net]`. The daemon caches the graph per node for 2 seconds, and keeps the last answer for 10 minutes when the gateway is gone.

**Connected charters.** The per-turn text also describes directly connected nodes with charters, in either edge direction and for any edge kind. Each line has the form `<project>: accepts <short accepts>; refuses <short refuses>; ask it for those`. The project name and each charter value are cut to 40 characters, so a line is at most about 160 characters. There are at most eight lines and the block is at most 1500 bytes. Edges with a non-default request rule come first, then `governs`, `deploys` and `depends`; ties use the project name. The wildcard source `*`, the node itself and neighbors without a charter are skipped. A node without its own charter can still get these lines. The same lines go into worker briefs, including Codex worker briefs.

**Instruction signals.** The Claude Code `UserPromptSubmit` policy hook sends the first 300 characters of the latest owner prompt to the gateway for that session. The gateway keeps that latest prompt in `signal_prompts` for a 10-minute matching window. When the session sends a new request or notification in that window and the excerpt names another known agent or project, the gateway records a row in `charter_signals`. Reply outcomes are linked through the existing `graph_outcomes` table. The match is a time window, not proof that the send belongs to the same turn. A later prompt replaces the earlier one, including when it is empty.

| Signal data | What the gateway records |
|---|---|
| Owner instruction | The prompt excerpt, sender and recipient project nodes, message ID and time. |
| Reply outcome | The request and reply IDs, outcome (`accept`, `reject`, `redirect` or `escalate`), reason and redirect target when supplied. The outcome is linked to the request's signal when there is one; outcomes without an instruction signal can also be evidence. |

Signal records are kept for 90 days, independently of the message retention setting. The latest prompt is no longer used after 10 minutes; the retention timer removes expired prompt rows. Signals are read only through the owner authority path, including the private CTO tool. The gateway trusts enrolled daemons to enforce their local owner checks. See [security.md](security.md#instruction-signals) for what the gateway stores and who can read it.

**Charter suggestions.** The CTO's read tool `charter_suggestions` returns suggested diffs grouped by node: field, old value, proposed value and message IDs, with prompt excerpts and reply outcomes as evidence. Its plain rules recognize an owner instruction that says a task is his or its job, or a redirect whose reason names a task and destination. They suggest adding the task to the destination's `accepts` and `<destination> for <task>` to the sender's `needs`. For example, GitHub token work is expressed as `GitHub secrets`. A rejection such as `already in main` is evidence but does not itself suggest a change. These rules do not understand every wording. The tool considers at most 40 recent evidence entries and keeps its JSON output below 20 KB, so it may omit entries or suggestions. The CTO offers the diffs to the owner and uses `charter_set` to apply them only after the owner says yes. No signal changes a charter by itself.

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
| Claude Code workers plugin | If `claude` is found, follows the network answer or `--claude-workers-mod` / `--no-claude-workers-mod`. On yes it writes a local marketplace and the plugin in `~/.local/share/agent-net/claude-plugins/` from the files in this binary, then runs `claude plugin marketplace add <folder>` and `claude plugin install agent-net-workers@agent-net -s user`. Claude Code reads the plugin from that folder. A later setup run, or the daemon at start, rewrites the files if they differ from the binary, so the plugin matches the version that is installed. If the `claude` CLI fails, the step says so and the rest of setup continues. Records the answer in `~/.config/agent-net/claude-workers-mod.json`. | Runs `claude plugin uninstall agent-net-workers@agent-net -s user` and `claude plugin marketplace remove agent-net`, deletes the folder and the record. |
| Quota-axi private copy | If the Quota integration is on and no quota-axi is on `PATH`, in `~/.local/bin`, in `node.json` or in the macOS Node folders, installs the pinned `quota-axi` with npm in `~/.local/share/agent-net/quota-axi/`. Needs Node 22.19 or newer and npm. A failed install is retried at most once a day. The daemon does the same in the background when it applies a changed network answer. | Removes `~/.local/share/agent-net/quota-axi`. Turning the item off does the same. |
| Codex MCP server | If `codex` is found or `~/.codex` (`$CODEX_HOME`) exists, adds a block between `# agent-net:begin` and `# agent-net:end` at the end of `config.toml` in it: the server `agent-net` with `args = ["mcp", "--codex"]`. It follows the network answer or `--accept-codex-tools` / `--no-accept-codex-tools`; yes adds `default_tools_approval_mode = "approve"` to that block (for this server only), no leaves it out. The answer is kept in `~/.config/agent-net/codex-tools.json`. A `[mcp_servers.agent-net]` that is not ours is left alone. Codex threads started before this do not have the tools. | Removes the block and the record; the rest of `config.toml` is kept. |
| OpenCode plugin | If `~/.config/opencode` (or `$XDG_CONFIG_HOME/opencode`) exists, writes `plugins/agent-net.ts` in it. A symlink to a file agent-net did not write is left alone. A symlink to an older agent-net copy is replaced with a real file holding this version; the old target is kept. | Deletes the file. A symlink to a file agent-net did not write is left alone. A symlink to an agent-net copy is removed, and its target is kept. |
| Pi extension | If `~/.pi` exists, writes `~/.pi/agent/extensions/agent-net.ts`. A symlink to a file agent-net did not write is left alone. A symlink to an older agent-net copy is replaced with a real file holding this version; the old target is kept. | Deletes the file. A symlink to a file agent-net did not write is left alone. A symlink to an agent-net copy is removed, and its target is kept. |
| Skill and MCP management state | Created by the daemon when the owner applies a tool assignment or removes a native MCP entry. Setup writes none of it. Files and folders are listed in [Files](#files). | Deletes `skill-manifest.json`, `skill-backups`, `mcp-backups`, `skill-journal` and leftover temporary files in the daemon state folder, and releases the Git exclude lines the manifest owns. It keeps installed skill copies and every native MCP config file (including any edit the daemon made): restore those by hand or with `tools_undo` before you uninstall. Backups are deleted because they can hold secrets. The gateway side (`skills.git`, the inventory and audit tables in `gateway.db`) is kept with the gateway data. |
| Guidance text | Adds a block between `<!-- agent-net:begin -->` and `<!-- agent-net:end -->` to `~/.claude/CLAUDE.md` (if `~/.claude` exists) `~/.pi/agent/AGENTS.md` (if `~/.pi` exists) `~/.config/opencode/AGENTS.md` (if `~/.config/opencode` exists) and `~/.codex/AGENTS.md` (if the Codex home exists). Text outside the markers is never changed. An older unmarked `## agent-net: working with other agents` section is replaced by the block. | Removes the block. |

The Pi extension, the OpenCode plugin, the Claude workers plugin folder and the guidance blocks are also written by the daemon, on every start, with the same rules as setup (the harness folder must exist, a symlink to a foreign file is left alone, guidance changes only between the markers). The Claude workers folder is refreshed only when setup already wrote it, so the daemon never installs the plugin on its own. It rewrites a file only if it differs from the content embedded in the binary, and logs one line per file it changes (`agent files: pi extension updated`) in the daemon log. A symlink that points at an older agent-net plugin is replaced with a real file, so a stale link cannot keep an outdated extension loaded; the old target is kept and named in the log line. So these files follow the binary after an upgrade or a manual copy, without a setup run. See [upgrade.md](upgrade.md#agent-files-on-a-machine).

Setup does not use `sudo`. On Linux it may print a hint to run `sudo loginctl enable-linger <user>`, which keeps the user services running when you are logged out. You run that yourself.

Setup does not change any other Claude Code or Pi setting (the one exception is the peer-message setting above, and only with your yes), and it does not touch other MCP servers.

## Owner inbox and written approvals

Agents use `agent_request_approval(action, recommendation)` for one exact action, or `agent_propose_idea(text, recommendation)` for an idea. The daemon binds each request to the calling session; the gateway derives its project from that session. Codex workers put the exact action and their recommendation in their report when they need approval.

Press `i` in the TUI to open the inbox. Items are grouped by kind and ordered oldest first within each kind. Select an item and press enter to see its context. Press `a` to approve, `r` to refuse, or `t` to enter a one-line answer. The header counts open items, and open approvals appear under Needs you. Answers are stored together with a message queued for the requesting session. An approval covers only the exact action named in the item.

The owner-only local methods are `inbox_list` and `inbox_answer` with `id` and `answer` (`approve`, `refuse`, or text). The daemon applies the same operator gate as other owner-only methods. Rejections and escalations of agent requests are also recorded in this inbox, with the request, reason, recommendation and conversation. Answering an item or resolving it from Needs you closes it in both views. Existing outcomes are added to the inbox when the gateway upgrades.

For a node with `ask` merge authority, request approval for the exact merge using `agent_request_approval`. A deployer follows the target node's deploy rule: for a critical target, request written approval for the exact deploy using the same tool. Codex workers include the action and recommendation in their report.
### Request outcomes

`agent_reply` accepts optional `outcome`: `accept`, `reject`, `redirect` or `escalate`. Reject, redirect and escalate need `reason`; redirect also needs `redirect_to` (a node name), and escalate needs `recommendation`. Each text field is limited to 2048 bytes, and the node name to the project name limit. Control characters are removed. Outcomes apply to replies to requests. The gateway records the request's source and target project names, request and reply IDs, outcome, reason, redirect, recommendation and time. Projects unknown to the sessions appear as empty names.

A rejection or escalation of another agent's request appears as a separate Needs you entry until the owner resolves it. The entry shows the request's first line, outcome, reason and recommendation. Select it and press `c` for the receiving session's conversation or `R` for the requesting session's conversation. Press `s` to let the decision stand or `o` to overrule it. Letting it stand sends the receiving session a notice that its decision stands. Overrule sends the original request again from the owner with the agent-net note `Nicolas overrules: do it.` The operator method is `outcome_overrule` with `reply_id` and `action` (`let_stand` or `overrule`).

The daemon marks its private Claude CTO folder and Claude worker worktrees trusted in `~/.claude.json` (or `$CLAUDE_CONFIG_DIR/.claude.json`). It removes the worktree trust setting when the worktree is deleted. Uninstall removes the CTO folder's trust setting and preserves other project settings.

The owner-only `cto_enter` method starts the local CTO runtime if needed and returns an enter plan for its full harness UI. In the CTO pane, press `o` to open it and ctrl+] to return. Claude starts a background session on first entry if needed. Entry errors appear in the TUI. The Claude CTO requires Claude Code with `--bg` and `--strict-mcp-config`; update Claude Code if either flag is missing.

The daemon service records the login shell's PATH in its environment, so tools
installed outside the system directories are available to workers. If the shell
cannot supply it, setup adds common Homebrew and local binary directories to the
current PATH. A daemon upgrade adds PATH to older service files.
