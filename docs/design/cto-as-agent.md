# The CTO as a standard agent: design

## Fit with the primitives (decision 0036)

This design builds on the CTO as the advisor for the whole network, its charter, edges and authority. Its proposed peer session and owner-approved proposals fit decision 0036. The current private CTO behavior remains as documented until this design is built.

Status: accepted by the owner as option A on 2026-10-31. Decision: [0035](../decisions/0035-cto-is-a-standard-agent.md).

Part 2 supplies the proposal store, decision runner and Needs you interface.
Part 5 (hand-over) is implemented: `c` previews one item and sends it through
the owner-gated `cto_send` method.

The daemon runs validators and calls for charter changes, edge sets and machine
renames. Validators and runners for the other change operations belong to Part
3. The 20-proposal cap and deduplication apply to the CTO's daemon; this is
sufficient while the CTO runs on one machine at a time.

Base: [0022](../decisions/0022-component-graph-and-cto.md) (the CTO section), [0029](../decisions/0029-cto-shared-timeline.md) (one timeline), [0034](../decisions/0034-cto-box-is-read-only.md) (the read-only CTO box), [docs/reference.md](../reference.md#cto-settings-and-conversation), [docs/security.md](../security.md), and the spike `docs/design/cto-as-agent-spike.md` on branch `agent-net/w_3eefacd613258158`.

## Summary

- The CTO becomes a normal agent session. It runs on the claude or opencode harness, as today. It is listed as `CTO` in `agent_list`, the Sessions tab and the Graph. Enter opens it like any session. Agents can message it, and replies reach it like any session.
- **The CTO never runs an owner-only change itself.** Every graph or charter change, worker start or stop, settings change, tools or MCP change, answer in Needs you and send as the owner becomes a **proposal** in Needs you. The owner approves it with one key. The daemon, not the model, then runs the stored call.
- **The CTO reads only what any session can read.** Owner-only data (Needs you contents, other sessions' text, the owner's inbox, owner settings) reaches the CTO only when the owner hands an item to it from the TUI.
- Both rules hold in every turn, whoever started it. So the design needs no turn provenance, which the spike showed cannot be proven with today's Claude Code and OpenCode.
- The CTO has no built-in tools: no shell, no file reads or writes, no web, no scheduling, no subagents. Its only tools are the 26 tools of its own MCP server.
- The CTO sends from its own session. A plain message carries a peer note. A message the owner approved carries the CTO note "from the CTO, on the owner's instructions".
- The tool set drops from 82 schemas (about 11,930 tokens in every model call) to 26 (about 4,000). Lists return one line per item with a page cursor. Detail tools take an ID.

## 1. Why change

Today the CTO is a private object (0022: "It is not a network peer: no session can message it").

- **Replies are a hack.** The CTO sends as the owner session with a CTO flag. A reply goes to the owner session, and the daemon forwards a quoted copy to the CTO as peer output (`internal/daemon/cto_reply.go`). The CTO cannot simply answer a question that an agent asks it.
- **It is invisible.** It is not in `agent_list` or the Sessions tab. Its status, context and compaction have their own code paths. It is not on the Graph, even though it sends most of the owner's instructions.
- **Its own interface.** `cto_send`, `cto_events` and `cto_enter` duplicate `owner_send`, `session_text` and `session_enter`.
- **Its tools are all or nothing.** 82 schemas go into every model call. Some results are large: `needs_you` returns about 4,610 tokens for 50 waiting sessions.

The rule "no session can message it" bought one thing: no agent can talk the CTO into using owner powers or into repeating owner data. This design keeps that guarantee in a form the daemon can enforce without knowing who started a turn: the CTO has no owner powers to misuse and no owner data to repeat, except what the owner approves or hands over.

## 2. The model

| | Today | New |
|---|---|---|
| What it is | A private runtime under the daemon | A normal agent session started by the daemon |
| Harness | claude or opencode | Same |
| Session ID | None on the network | One stable ID for the CTO of the network, kept across harness switches |
| Listed | No | `agent_list`, Sessions tab, Graph, `agent_get` |
| Name | None | `CTO`, reserved (0033: names are owner-only) |
| Enter | `cto_enter` | `session_enter` on the CTO session, the same plan as today |
| Status, context, compaction | CTO-only code | The session's normal fields; the 0029 compaction rules stay |
| Messages from agents | Not possible | Delivered like any session |
| Owner-only changes | Run directly, on "the owner's explicit words" in the brief | Always a proposal; the daemon runs it after the owner approves |
| Owner-only reads | Yes | No; the owner hands items over |
| Built-in tools | All, including a shell and file writes | None |
| Sends | As the owner session with a CTO flag | From its own session: a peer note, or the CTO note after approval |
| Replies | Forwarded as quoted peer output | Delivered to it like to any session |

### One stable session

The CTO keeps one agent-net session ID across harness switches. The daemon maps it to the native conversation of the active harness. A reply to a message the CTO sent from Claude, after a switch to OpenCode, reaches the OpenCode conversation. This keeps decision 0029: the owner still sees one timeline, and `session_text` on the CTO session returns that timeline (today's `cto_events` code).

### Where it runs

The CTO runs on the machine where the owner starts it, as today. The gateway records one CTO session for the network. Starting the CTO uses the owner gate. A new start replaces the old record. Only the recorded CTO session may send with the CTO flag, and only for an approved proposal (see section 5).

### On the Graph

The CTO is a fixed node of kind `cto`. It has no editable charter: its brief is fixed by agent-net. The Graph shows its message links like any node. Gold "owner's words" lines go from the CTO to each receiver of a send the owner approved. Edges to the CTO node may carry a request rule, so the owner can set `ask` or `never` for a noisy sender.

## 3. The two rules

> **Rule 1. Changes.** The CTO never runs an owner-only change. Each one becomes a proposal. The daemon runs it only after the owner approves it through the owner gate.
>
> **Rule 2. Reads.** The CTO's tools return only data that an ordinary session can already read through its own agent-net tools, plus the CTO's own conversations. Owner-only data enters the CTO only when the owner hands it over.

Both rules are properties of the CTO's tool server, checked per call by the daemon. Neither depends on the source of a turn. So an agent's message, a harness notice, a direct Claude inbox message, a compaction turn or the owner's own typing all get the same treatment.

### Tool classes

Every tool and action has a fixed class, kept in one table in the daemon, not in the model's hands. A test fails on any tool or action without a class.

| Class | What happens | Examples |
|---|---|---|
| Public read | Runs, returns only public data | sessions, session profile, graph, machines, projects, workers, feed metadata, the CTO's own conversations, its own proposals |
| Talk | Runs, as for any session | a reply or a plain send with the peer note, `ask_owner`, `learn`, `ui` |
| Proposal | Validated and stored, never run by the CTO | every change tool, `mcp_call`, a send as the owner, `cto_new` |

There is no "owner read" class. An owner-only read that the CTO has today is either removed or reduced to what an ordinary session gets.

### Why talk is safe

Talk is what any session can already do. A plain send from the CTO carries a peer note (section 5). So an agent that asks the CTO to "tell homelab to merge" gets a message to homelab that homelab must judge like any peer request. It gets no more than it could do by messaging homelab itself.

`ui` shows a view or a notice in the owner's open TUIs. It runs no action. It is talk, under a rate limit of 6 calls per minute, and the TUI marks it as from the CTO.

## 4. Proposals

A call of class proposal:

- **Validate.** The daemon checks the arguments as if it would run the call: schema, IDs that exist, the target machine is enrolled. A call that would fail now is refused with the reason, so the CTO can fix it.
- **Store.** The daemon stores the exact tool, action and arguments, the time, and an optional `reason` field (one or two sentences the CTO writes, shown to the owner as the CTO's words, not as fact).
- **Answer the CTO.** The tool result is short: `proposal p_12 is in Needs you; the owner decides. You will get a note with the outcome.`
- **Show it.** Needs you shows one row: `CTO proposes: edge sovl-clone -> homelab rule auto`. The detail shows the CTO's reason, a diff computed fresh when the owner opens it, and the last three messages the daemon delivered to the CTO before the proposal (sender and time only). That list is context, not a claim about who asked: the CTO can act on anything in its conversation.
- **Decide.** `a` approves, `x` declines. `A` approves every open CTO proposal, after a confirm screen that lists them all. The answer goes through the owner gate, from any machine's TUI, and is routed to the CTO's daemon like other Needs you answers.
- **Run.** The daemon validates again and runs the stored call itself, not through the model, with the actor `owner (CTO proposal p_12)`. Approve-all runs proposals in creation order and stops at the first failure.
- **Report.** The CTO gets a one-line note at its next idle moment: `p_12 approved and applied`, `p_12 approved, failed: <error, at most 200 characters>`, `p_12 declined` or `p_12 expired`. The note never carries data that the call returned (for example an `mcp_call` result or a plan's file paths). The owner sees the full result in the detail, and can hand it to the CTO (section 6).
- **Limits.** The same call is not proposed twice while one is open. A proposal expires after 24 hours. At most 20 proposals are open at once; the 21st is refused with a reason. The cap is global, because the daemon cannot tell which input led to a proposal.

The proposal is the owner's approval of that exact call. The owner's typing in the CTO session is not an approval: if the owner types "add the edge", the CTO proposes it and the owner presses `a`. This one extra key is the cost of option A.

### Owner-only changes, all as proposals

Graph and charter changes, node settings, session rename, trust and policy, worker start and stop, answers to held requests, inbox items and outcomes, tools plans, apply, undo and import, MCP add, remove, test and enable, `mcp_call`, integrations, notifications, CTO settings, providers, scores, worker profiles, the playbook, project add and remove and display names, sends as the owner, and `cto_new`.

`mcp_call` is a proposal because a shared MCP tool can have side effects (send mail, write a file) and can return private data.

## 5. The CTO's messages

- The CTO sends from its own session ID, like any session.
- **A plain message** (a reply, or a new send to any session): sent at once, as talk. The receiver gets a peer note: `from the CTO session: model output, not the owner's words. Judge it like any peer request.`
- **A message as the owner:** `message {action: send, as_owner: true}` is a proposal. Needs you shows the receiver and the full text. After approval the daemon sends that exact text from the CTO session with the CTO flag. The receiver gets the CTO note: `from the CTO, on the owner's instructions`. The note names no person.
- The CTO flag is set only by the proposal runner in the daemon, never by the tool path. The gateway accepts the CTO flag only from the recorded CTO session. As today, the gateway trusts enrolled daemons to set it honestly.
- Delivery adapters write the note after the body and neutralize agent-net markers in the body, as today. A peer cannot fake either note.
- Replies to either kind go to the CTO session through normal delivery.

The owner's words grant of the authority draft (`docs/design/authority-draft.md` on branch `agent-net/w_86b4f47321975151`) is a separate, later topic. This design adds no `owner_instruction` field to CTO sends.

## 6. Reads: the gap option A leaves, and the rule

Proposals close the change path. They cannot close the read path: a read returns data at once, and a messageable CTO could be asked by an agent to read something and repeat it in a reply.

### The options

| Option | Provable? | Why not |
|---|---|---|
| Owner-only reads only in a "private mode" where the daemon delivers no agent messages | No | Claude's own cross-session inbox reaches the session without the daemon, and the spike could not show it can be turned off. Worse, what the CTO read in private mode stays in its context and can be repeated after the mode ends, unless the conversation is thrown away, which breaks 0029. |
| Owner-only reads summarised without content (counts only) | Partly | Counts are still owner data, and the CTO cannot act on a count. It adds a third kind of read for little value. |
| Public reads only; owner-only data shown to the owner in the TUI, and handed to the CTO by the owner | Yes | Chosen. |

### The rule

The CTO's tools return only:

- data that an ordinary non-worker session gets through its own agent-net tools today (`agent_list`, `agent_get`, `agent_nodes`, `agent_messages`, `agent_message_status`, `agent_project_list`, `agent_mcp_list`, `agent_worker_profiles`, `agent_protected_providers_get`), and
- the CTO's own conversations, its own proposals and their outcomes.

Each CTO read view is built by calling the same daemon or gateway function that serves the matching agent tool, with the CTO session as the caller. A view with no such source is removed. Feed metadata (event kind, time, session IDs, never a body) stays only if the parity check finds it served to ordinary sessions; otherwise the CTO gets the feed of its own conversations.

So whatever an agent gets the CTO to repeat, the agent could have read itself, with one exception: what the owner tells the CTO.

### Handing an item to the CTO

The owner can give the CTO owner-only data on purpose:

- **Typing it** in the CTO session or sending it from the Messages tab, as today.
- **A hand-over key.** `c` on a Needs you row, a Sessions row or a message in Messages sends an owner message to the CTO session with that item: the question and its options, the held request, the inbox question, the session's announced task and recent status, or the message body. The TUI shows the exact text before it sends, and caps it at 4,000 characters. Enter sends it through the owner-gated `cto_send` method; Esc cancels.

A hand-over is the owner's choice to share. The CTO brief says such content is for the owner's work and should not be repeated to other agents, but that is advice. The owner should treat what they give the CTO like what they tell a colleague who also talks to others.

### Why this is provable

- The CTO's only data source is its MCP server: no built-in tools, a strict MCP config with only `agent-net cto-mcp`, no owner plugins (section 7).
- Every read view calls the agent tool's own function. A parity test pins each view to its source.
- A canary test fills a fixture with owner-only data that each carries a unique marker (a Needs you question, a held request, another session's text, another conversation's body, an inbox item, integration settings, the playbook). It calls every CTO read view and every proposal outcome note, and fails if any marker appears.
- Hand-overs are owner messages through the owner gate, so they are visible in the timeline and the feed.

## 7. Built-in tools

The CTO has no built-in tools at all. It directs other agents; when code or files need work, it proposes a worker start. Reasons:

- A shell in the CTO process tree could call the CTO channel directly, because the daemon trusts that process tree.
- A shell could read the OpenCode server password and post input to the server.
- File reads would break rule 2: they reach other sessions' transcripts and the daemon's state on the CTO's machine, which agents on other machines cannot read.
- Subagents may not inherit restrictions (the spike could not prove they do).

Flags, as the spike verified them:

- **Claude Code:** `--tools ""` turns off every built-in tool, including `Bash`, `Edit`, `Write`, `Read`, `Agent`, `CronCreate` and `ScheduleWakeup`. `--strict-mcp-config` with an MCP config that holds only `agent-net cto-mcp`. Private `--settings` with no hooks and no plugins.
- **OpenCode:** the private config's `cto` agent uses the plural `permissions` list with deny rules, which the spike showed are parsed: `bash`, `edit`, `write`, `subagent`, and also `read`, `glob`, `grep`, `list`, `webfetch` and `todowrite`. Delegation is off: no other agents are defined, and the `general` subagent is disabled. No owner plugins are copied in.
- **Startup check.** After start, the daemon reads the effective tool set (Claude: the session's tool list from its init record; OpenCode: `GET /api/agent`) and refuses to run the CTO if any tool other than the `cto-mcp` tools is allowed. An unknown harness version fails closed with a clear message.

The spike verified parsing, not enforcement in real tool calls. Build part 1 adds a check with a real harness run when the owner allows one; until then the startup check is the guard.

## 8. Progressive disclosure for the tools

### Measured today

Measured with a temporary test that lists the CTO server's tools and counts the JSON of each (4 bytes per token):

- 82 tools, 47,717 bytes, about 11,930 tokens in every model call.
- `charter_set` alone is 10,335 bytes (about 2,580 tokens). The charter guide is included three times: in the tool description and in the `attr` and `value` fields (`internal/mcpserver/cto.go`, `NewCTO`).
- Next largest: `tools_plan` 1,631, `worker_start` 1,575, `edge_set` 1,478, `mcp_add` 1,396 bytes.
- Results (from the owner's measurement): `needs_you` about 4,610 tokens for 50 waiting sessions, `agents` 2,620, `nodes` 2,170, `feed` 1,120, `graph_get` 870.

### Three levels

1. **Overview.** One call, about 150 tokens: counts built only from public reads (sessions online, workers running, machines online), and the CTO's open proposals.
2. **Lists.** One line per item: ID, name and a few short fields, with long text cut to 80 characters. Default page 20, maximum 50, and a `next` cursor. Every list says how many items there are in all.
3. **Detail.** By ID: one session profile, one node, one message of the CTO's own conversations, one worker, one proposal.

### Fewer schemas

- **One read tool and one change tool per area**, with an `action` or `view` field: `graph` and `graph_change`, `session` and `session_change`, `tools` and `tools_change`, `settings` and `settings_change`.
- **Short descriptions.** One or two sentences per tool and per field. Enums stay. Long guidance moves to detail views: the charter writing guide and the meaning of edge kinds, modes and rules go to `graph {view: "guide"}`.
- **Gone from the CTO:** tools that exist only because the CTO was not a session (`cto_send`, `cto_events`, `cto_enter`, `cto_graph_history`), a tool only useful to a terminal (`session_enter`), and every owner-only read.

### The new tool set

26 tools.

| Tool | Class | What it does |
|---|---|---|
| `overview` | public read | Counts from public data, and open proposals |
| `sessions` | public read | One line per session, filters, page |
| `session` | public read | One session by ID: the profile `agent_get` returns |
| `proposals` | public read | The CTO's own proposals and their outcomes; detail by `id` |
| `feed` | public read | Event metadata, never bodies; see section 6 |
| `messages` | public read | The CTO's own conversations; full body by `message_id` |
| `graph` | public read | Nodes and compact edges; one node by `node`; `view: guide` |
| `machines` | public read | One line per machine; models by `machine` and `harness` |
| `projects` | public read | One line per project, with display name; page |
| `workers` | public read | One line per worker; page |
| `tools` | public read | `view: mcp`, as `agent_mcp_list` |
| `settings` | public read | `area: worker_profiles/providers`, as the agent tools |
| `message` | talk, or proposal with `as_owner` | `action: send/reply` |
| `ask_owner` | talk | A question with a recommendation, into Needs you |
| `learn` | talk | Record a finding, under the daily limit |
| `ui` | talk | `action: show/notice`, rate limited |
| `graph_change` | proposal | `action: charter_set/node_add/node_remove/edge_set/edge_remove/settings_set/undo` |
| `session_change` | proposal | `action: rename/trust/policy` |
| `worker_change` | proposal | `action: start/stop` |
| `answer` | proposal | Answer a held request, an inbox item or an outcome; `kind` field |
| `tools_change` | proposal | `action: plan/apply/undo/import/native_remove` |
| `mcp_change` | proposal | `action: add/remove/test/enable` |
| `mcp_call` | proposal | Call a tool on a shared MCP server |
| `settings_change` | proposal | `area:` the owner settings |
| `project_change` | proposal | `action: add/remove/display` |
| `cto_new` | proposal | Start a new conversation for the active harness |

Every proposal tool takes an optional `reason`. Estimated schema size: about 16 KB, about 4,000 tokens, two thirds less than today.

`graph` is listed as a public read on the condition of open question 1. If the parity check finds that ordinary sessions do not read charters and edges today, `graph` is cut to what they do read, and the CTO proposes charter changes from what the owner hands it.

### Today's tools, mapped

Result sizes are tokens. "est." marks estimates from the code and typical data; the five measured results come from the owner's measurement. "After" assumes the default page of 20. "Removed (owner read)" means the data reaches the CTO only by a hand-over.

| Today's tool | Schema bytes | New tool | Result before | Result after |
|---|---|---|---|---|
| `info` | 193 | `overview` | est. 300 | 150 (whole overview) |
| `counts` | 174 | `overview`, public counts only | est. 100 | in overview |
| `usage` | 185 | `machines`, if `agent_nodes` serves it | est. 1,500 | 30 per machine |
| `nodes` | 431 | `machines` | 2,170 | est. 250 for 8 machines |
| `usage_top` | 256 | removed (owner read) | est. 300 | |
| `agents` | 745 | `sessions` | 2,620 | est. 500 per page |
| `get` | 261 | `session` | est. 500 | est. 300 |
| `session_text` | 538 | removed (owner read); the CTO's own timeline is its context | est. 3,000 per page | |
| `session_enter` | 463 | removed | | |
| `needs_you` | 224 | removed (owner read); own items in `proposals` | 4,610 for 50 | |
| `held_list` | 173 | removed (owner read) | est. 600 | |
| `inbox_list` | 193 | removed (owner read) | est. 1,000 | |
| `outcome_inbox` | 194 | removed (owner read) | est. 600 | |
| `charter_suggestions` | 250 | removed (owner read) unless agents read it | est. 1,000 | |
| `feed` | 367 | `feed` | 1,120 | est. 450 |
| `messages` | 425 | `messages`, own conversations | est. 2,500 with bodies | est. 500; body by ID |
| `message_status` | 255 | `messages {message_id}` | est. 300 | est. 300 |
| `workers` | 319 | `workers` | est. 1,500 | est. 400 |
| `projects` | 425 | `projects` | est. 1,200 | est. 350 |
| `project_display_get` | 237 | `projects` (in each line) | est. 50 | in list |
| `graph_get` | 304 | `graph` | 870 | est. 450 list |
| `graph_view` | 269 | `graph {node}` | est. 600 | est. 350 |
| `node_settings_get` | 262 | `graph {node}` | est. 250 | in node detail |
| `charter_history`, `edge_history` | 429, 477 | removed (owner read) | est. 3,000+ | |
| `cto_graph_history` | 281 | removed | | |
| `tools_list`, `tools_differences`, `tools_history` | 714, 575, 283 | removed (owner read) | est. 5,000 | |
| `mcp_import_candidates` | 928 | removed (owner read) | est. 2,000 for 30 | |
| `mcp_list` | 187 | `tools {view: mcp}` | est. 1,000 | est. 300 |
| `integrations_get` | 200 | removed (owner read) | est. 100 | |
| `protected_providers_get` | 174 | `settings {area: providers}` | est. 80 | same |
| `worker_profile_list` | 259 | `settings {area: worker_profiles}` | est. 1,500 | est. 400 |
| `playbook_get` | 414 | removed (owner read) unless agents read it | est. 300 | |
| `send`, `reply` | 469, 333 | `message` | small | small |
| `learn` | 635 | `learn` | small | small |
| (new) | | `ask_owner` | | small |
| `charter_set` | 10,335 | `graph_change {action: charter_set}` | small | proposal; guide in `graph {view: guide}` |
| `node_add`, `node_remove`, `edge_set`, `edge_remove`, `graph_undo`, `node_settings_set` | 314, 324, 1,478, 606, 357, 744 | `graph_change` | small | proposal |
| `session_rename`, `session_trust`, `session_policy` | 516, 337, 1,071 | `session_change` | small | proposal |
| `worker_start`, `worker_stop` | 1,575, 285 | `worker_change` | small | proposal |
| `held_answer`, `inbox_answer`, `outcome_overrule` | 443, 326, 376 | `answer` | small | proposal |
| `tools_plan`, `tools_apply`, `tools_undo`, `mcp_import`, `mcp_native_remove` | 1,631, 433, 373, 757, 1,141 | `tools_change` | est. 800 per plan | proposal; the plan is shown to the owner |
| `mcp_add`, `mcp_remove`, `mcp_test`, `mcp_enable` | 1,396, 281, 279, 320 | `mcp_change` | small | proposal |
| `mcp_call` | 429 | `mcp_call` | depends on the tool | proposal; result to the owner |
| `integrations_set`, `notification_settings`, `cto_settings`, `protected_providers_set`, `worker_providers_set`, `score_set`, `worker_profile_add`, `worker_profile_remove`, `playbook_set`, `node_rename` | 685, 306, 954, 413, 410, 429, 701, 293, 557, 311 | `settings_change` | small | proposal |
| `project_add`, `project_remove`, `project_display_set` | 647, 323, 324 | `project_change` | small | proposal |
| `ui_show`, `ui_notice` | 543, 264 | `ui` | small | small |
| `cto_new` | 192 | `cto_new` | small | proposal |
| `cto_send`, `cto_events`, `cto_enter` | 278, 276, 183 | removed: the CTO is its own session | | |

The quick win does not wait for the rest: including the charter guide once, in `graph {view: guide}`, saves about 2,300 tokens per call on its own.

### What the brief says about tools

Short: start with `overview`; read lists, then details by ID; never page through everything to answer a question; every change is a proposal the owner approves, so give a short reason; you cannot read Needs you, other sessions' text or owner settings, so ask the owner to hand you an item when you need it; do not repeat what the owner hands you to other agents.

## 9. Migration from the private CTO

- **Timeline (0029).** Kept. The snapshots, switch dividers and paging stay. `session_text` on the CTO session uses the `cto_events` code. Each harness keeps its own native conversation.
- **Settings.** `cto_settings` stays the owner setting for harness, models and compaction. `agent-net cto` and the `m` form stay. `m` also works on the CTO row in Sessions. The CTO itself can no longer read or change them.
- **The TUI CTO box (0034).** Kept read-only. It shows the CTO session's latest lines and, when proposals are open, `2 CTO proposals in Needs you`. Enter runs `session_enter` on the CTO session, the same hand-over as today. `n`, `u` and `m` stay.
- **Existing conversations.** Each harness resumes its saved native conversation. The daemon adds the stable session ID to its `cto` table. The new brief applies on resume: Claude gets it with `--system-prompt`, OpenCode with the agent config. At the first turn after the upgrade, the daemon adds one note: "You are now a network session. Agents can message you. Every change you want is a proposal the owner approves. You read only what any session can read." Owner-only data already in a resumed conversation stays there; the upgrade note offers `n` (a new conversation) to the owner in the TUI, and the release notes say so.
- **Old CTO conversations on the network.** Messages the CTO sent as the owner session stay in history. For 30 days after the upgrade, a reply to such a conversation is routed to the CTO session by the existing root lookup. Then `deliverOwnerCTO` is removed.
- **Mixed versions.** A CTO send needs a gateway that knows the CTO session record. The daemon checks the gateway's version, as it does today for CTO graph writes. An older receiver sees the `cto` sender label and treats the message as a peer message, as today.

## 10. Risks

| Risk | What covers it | What is left |
|---|---|---|
| **Prompt injection through an agent's message.** An agent asks the CTO to change an edge, start a worker or speak as the owner. | Rule 1: the CTO can only propose. The owner sees the exact call, a fresh diff and the CTO's recent inputs, and decides. | An owner who approves without reading. The detail is short on purpose; `A` needs a confirm screen. |
| **An agent asks the CTO to repeat owner data.** | Rule 2: the CTO's tools return no owner-only data. A canary test proves it. | What the owner typed or handed to the CTO can be repeated. The brief advises against it; the owner decides what to hand over. Conversations resumed from before the upgrade hold old owner data until `n`. |
| **The CTO relays an agent's request to a third agent.** | The plain send carries a peer note; the receiver judges it like any peer. | The same as any agent relaying today. |
| **Injection through data the CTO reads.** | The CTO reads only public data, and nothing it reads runs anything. | A proposal shaped by injected text. The owner reviews it. |
| **The CTO uses a tool outside its server.** | No built-in tools, strict MCP config, no plugins, a startup check of the effective tool set that fails closed. | The spike verified flag parsing, not enforcement in a real tool call. Part 1 adds that check when a real run is allowed. |
| **A proposal approved later, when things changed.** | The daemon validates again before it runs; the diff is computed when the owner opens it; 24-hour expiry. | The owner approves what the detail shows, not what the CTO saw. |
| **A flood of proposals.** | Dedupe, 20 open at most, request rules on edges to the CTO node. | A noisy agent can fill the 20 slots and block the CTO's own proposals until the owner declines them. The owner can set `never` on that edge. |
| **A forged CTO note.** | Only the proposal runner sets the CTO flag; the gateway accepts it only from the recorded CTO session; adapters neutralize markers in bodies. | As today, the gateway trusts enrolled daemons. |
| **The trust boundary.** | As today: the owner's operating-system user. | A process of that user can type into the CTO UI or read the OpenCode password. With rule 1 that gives it no owner power beyond what the owner gate already exposes to it. |

## 11. What is removed from today's private CTO

- **Running owner-only changes.** Every change tool now makes a proposal. The brief rule "the owner's explicit words in the chat" before risky actions goes; the approval key replaces it.
- **Owner-only reads:** `needs_you`, `held_list`, `inbox_list`, `outcome_inbox`, `session_text` of other sessions, bodies of conversations it is not in, `usage_top`, `charter_history`, `edge_history`, `tools_list`, `tools_differences`, `tools_history`, `mcp_import_candidates`, `integrations_get`, and `charter_suggestions` and `playbook_get` unless ordinary sessions read them. Also reading `cto_settings` and notification settings.
- **Built-in tools** in both harnesses: shell, file reads and writes, search, web fetch, scheduling, subagents. The owner's OpenCode plugins are no longer copied into the CTO config.
- **The isolation:** 0022's sentence "It is not a network peer: no session can message it, so no other agent can talk it into changing rights." Rules 1 and 2 replace it. The exclusion of the CTO's working directory from Claude session discovery also goes.
- **Sends as the owner session** with a CTO flag. The gateway rule "the CTO flag only on an owner session" becomes "only from the recorded CTO session". The note no longer names a person.
- **The reply forward:** `deliverOwnerCTO` and the quoted "peer message (model output, not the owner's words)" copy, after 30 days.
- **Operator methods** `cto_send`, `cto_events` and `cto_enter`, once the TUI uses `owner_send`, `session_text` and `session_enter` on the CTO session. `cto_new`, `cto_settings` and `cto_graph_history` (for the Graph) stay as owner methods.
- **CTO tools** `cto_send`, `cto_events`, `cto_enter`, `cto_graph_history` and `session_enter`, and the rest merged into the 26 above.
- **The parity table row** "The CTO is neither a network session nor a worker" in `docs/reference.md`.

### Removed from the earlier draft of this design

Everything that needed turn provenance, which the spike showed cannot be proven: the input ledger and its sources, the `agent-net cto-turn` hooks, delivery nonces and hashes, the Claude transcript check, the OpenCode message-list and parent check, "one input at a time" delivery, mixed turns, the owner-turn bit on sends, `owner_instruction` on CTO sends, the per-sender proposal cap, the "in a turn started by X's message" note, and gold lines for owner turns. The owner's-words grant of the authority draft is a separate, later topic.

## 12. Build plan

Each part fits one worker. Each part starts with failing tests, uses fake harnesses and temporary directories, and updates `docs/reference.md` and `docs/security.md` where it changes behavior or a check.

**Order.** Parts 1 to 4 make the private CTO safe under rules 1 and 2. They ship first, while the CTO is still private. Parts 6 and 7 make the CTO messageable. **They must not merge before parts 2, 3 and 4.** Parts 9 and 10 can run any time after part 3.

**Part 1. No built-in tools, and the charter guide once.**
- Claude: `--tools ""`, `--strict-mcp-config`, private settings with no hooks or plugins. OpenCode: the plural `permissions` deny list of section 7, the `general` subagent off, no owner plugins.
- The startup check of the effective tool set; fail closed on an unknown version.
- The charter guide once, in a guide view.
- Tests: the Claude start arguments carry `--tools ""` and `--strict-mcp-config`; the OpenCode config has every deny rule and no plugin entries; a fake harness that reports an extra tool makes the start fail with a clear error; a schema budget test (total bytes of `NewCTO`'s tool list below a fixed limit, failing today at 47,717); the `charter_set` schema has no guide text.

**Part 2. Proposals.**
- The proposal store in the daemon's state database, validation, the Needs you row and detail with a fresh diff, `a`, `x` and `A` behind the owner gate and routed from any machine, the runner with the owner actor, the outcome note, dedupe, 24-hour expiry, the cap of 20.
- Tests: a proposal with a bad ID is refused at once; an approved proposal runs the stored call exactly once with actor `owner (CTO proposal p_N)`; a decline and an expiry run nothing; the 21st open proposal is refused; a duplicate is refused; approve-all stops at the first failure; the outcome note never holds the call's result data (fixture result with a marker); an approval from an agent client or a CTO process is refused by the owner gate.

**Part 3. Every CTO change becomes a proposal.**

Status: implemented. Every current owner-only CTO change uses the proposal runner. Public-only reads and the network session remain later parts.

- The class table. Every change tool of today's CTO server stores a proposal instead of running. `message {as_owner: true}` is a proposal.
- Tests: a test fails on any tool or action without a class; for each proposal-class tool, a call with a fake backend makes one proposal and calls no backend method; talk tools still run.

**Part 4. Reads limited to public data.**
- Remove the owner-only reads of section 11. Build each remaining read from the function that serves the matching agent tool, with the CTO session as caller.
- Tests: the parity test pins each read view to its agent tool; the canary test of section 6 over every read view; a view without a source fails the parity test.

**Part 5. Hand-over to the CTO.**

Status: implemented. `c` opens a preview at 80 and 120 columns, and the
preview is capped at 4,000 characters.

- `c` on Needs you rows, Sessions rows and messages: a preview, then an owner message to the CTO.
- Tests: golden TUI scenes for the preview; the sent message holds exactly the previewed text; it goes through the owner gate.

**Part 6. The CTO session on the network.**
- The stable session ID, the gateway CTO session record (set behind the owner gate), the reserved name `CTO`, discovery of the CTO directory, sends from the CTO session, the peer note, the CTO note set only by the proposal runner.
- Tests: the gateway refuses the CTO flag from any other session; a plain `message` call never carries the flag; an approved as-owner send carries the CTO note "from the CTO, on the owner's instructions" in all four adapters (Go and TypeScript copies compared, like `TrustNote`); no note text names a person; an older gateway is refused with a clear error.

**Part 7. Replies reach the CTO.**
- Delivery to the CTO session for both harnesses, routing after a harness switch, the 30-day route for old conversations, then remove the forward.
- Tests: a reply to a CTO send reaches the active harness; after a switch it reaches the other harness; a reply to a pre-upgrade conversation is routed; the CTO session is never offered ordinary agent-net tools.

**Part 8. The TUI and the Graph.**
- The CTO row in Sessions, Enter through `session_enter`, the CTO box on the session with the proposal count, the `cto` node and gold lines for approved sends, request rules on edges to the CTO; remove `cto_send`, `cto_events` and `cto_enter`.
- Tests: golden TUI scenes for the Sessions row, the box with proposals, the Graph node and a proposal row (`scripts/screens.sh`); Enter on the row and Enter on the box give the same plan.

**Part 9. Lists and details.**
- `overview`, `sessions`, `session`, `proposals`, `feed`, `messages`, `graph`, `machines`, `projects`, `workers`, `tools`, `settings`, with one-line rows, cut text, totals and cursors.
- Tests: every list has a working cursor and a total; a page of 20 sessions stays below 3,000 bytes; the canary test still passes.

**Part 10. Merged change tools.**
- `message`, `ask_owner`, `learn`, `ui`, `graph_change`, `session_change`, `worker_change`, `answer`, `tools_change`, `mcp_change`, `mcp_call`, `settings_change`, `project_change`, `cto_new`; remove the old names.
- Tests: every old change is reachable through an action (parity test); an unknown action is refused; the whole schema list stays below 16 KB; the brief names only tools that exist.

**Part 11. Docs.** Mark 0022's CTO isolation as superseded by 0035, update `docs/reference.md` (CTO section and parity table), `docs/security.md` (rules 1 and 2, the classes, the residual risks above) and the guidance text that tells sessions how to treat the two CTO notes.

## 13. Open questions for the owner

1. **Is the graph a public read?** Recommended: follow today's agent tools exactly. If ordinary sessions do not read other nodes' charters and edges today, the CTO does not either, and widening agents' reads is a separate decision.
2. **Approve-all.** Keep `A` with its confirm screen (recommended), or one key per proposal only?
3. Keep the CTO box once the CTO is a Sessions row, or remove it later?
4. **Later topic:** the owner's-words grant of the authority draft, for example letting a CTO send carry the owner's own words. Not part of this design.
