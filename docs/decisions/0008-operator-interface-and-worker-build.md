# 0008 - The operator interface, the owner as a sender, and how workers are built

**Date:** 2026-10-29
**Status:** Built on branch `worktree-agent-a022fe03a8ec212a7` for OpenCode; Claude Code, Pi and Codex workers were added after. Proposed until the owner has read it.
**Context:** [0007](0007-usage-and-session-text.md) made the owner-only data methods and their gate. [0006](0006-phone-app.md) P1 says the TUI and the phone must use one interface, and [0002](0002-workers.md) is accepted. This text records how both were built, and the choices that 0002 left open.

## O1 - One operator interface, in the daemon's local API

The methods a person uses are one set, with request and answer types in `internal/protocol/operator.go`. They are plain JSON, with nothing that belongs to a terminal.

| Method | Takes | Answers | Kind |
|---|---|---|---|
| `info` | - | `OperatorInfo` | read |
| `nodes` | - | `NodesInfo` | read |
| `list` | `QueryAgents` | `AgentsResult` | read |
| `projects` | `QueryProjects` | `ProjectsResult` | read |
| `events` | `QueryEvents` | `EventsResult` | read |
| `message_status` | `QueryMessage` | `MessageResult` (the body) | read |
| `workers` | - | `WorkersResult` | read |
| `usage` | - | `UsageResult` | owner-only data, from [0007](0007-usage-and-session-text.md) |
| `usage_top` | `QueryUsageTop` | `UsageTop` | owner-only data |
| `session_text` | `TextQuery` | `TextPage` | owner-only data |
| `counts` | - | `SessionCounts` | owner-only data |
| `owner_send` | `OwnerSendRequest` | `MessageSent` | action, owner only |
| `worker_start` | `WorkerStartRequest` | `WorkerStartResult` | action |
| `worker_stop` | `WorkerStopRequest` | `WorkerStopResult` | action |

`worker_handback` (`WorkerHandbackRequest`) is the worker's own call, not an operator method. The Go client is `internal/operator`: an `Operator` interface and a `Client` over the socket. The TUI (`internal/tui`) uses only that interface, and so do `agent-net start`, `stop`, `workers` and `send`. Its `Snapshot` is built on the client side from the read methods, so a phone can build the same view from the same calls. The method name is the route name: the phone will call `POST /api/<method>` with the same JSON through the gateway, with no change to the types. Adding a feature means adding a method here once.

**Versions.** A new CLI against an older daemon gets `ErrTooOld` with the text "the daemon on this machine is older than this command; upgrade it". A new daemon against an older gateway says the gateway is too old for workers. A new gateway refuses to start a worker on a machine that did not report `launch` in its hello (an older daemon). Nothing hangs.

## O2 - The owner is a session

Each daemon registers one pseudo-session for the person at its machine: harness `owner`, native ID `owner`, name `you (on <host>)`. It is a normal row in the gateway, so a message from the owner is a normal message: stored, delivered, replied to, shown in the feed, with a body that this machine may read. Replies go to the owner session and show in the TUI feed.

- The gateway hides owner sessions from `agent_list` unless asked (`include_owners`), makes no `session.new` event for them, keeps them out of the project list, accepts only the native ID `owner` for them and never marks one as a worker.
- A message carries `sender_harness: owner`. Claude Code frames, the Pi extension and the OpenCode plugin then say: "from your user in person ... This is your user speaking, not another agent".
- **Who may send as the owner** is decided by `operatorGate`, the one rule that 0007 made for owner-only data (`usage`, `usage_top`, `session_text`, `counts`). Only a socket client that said `client: cli` may call `owner_send`; an agent connection (bound to a session) is refused. A CLI call that comes from a process that is a descendant of a session is refused too: an agent's shell tool starts `agent-net` as a child of the agent. We did not build a second gate: `owner_send` and the command line's `worker_start`, `worker_stop` call `operatorGate`, and `insideSession` also counts the process of every worker from the moment it starts (a worker is a session only after its plugin says hello). This is a check against an agent that shells out. A process of your user that is not under an agent can still connect as `cli`, as before: the socket is yours.
- The sender is never a parameter. `owner_send` ignores a sender field.

## W-build - Workers

### The OpenCode command

A worker is `opencode serve --hostname 127.0.0.1 --port <free port>` as a child of the daemon, in a new process group, with the project folder (or the worker's worktree) as its working folder. `OPENCODE_SERVER_PASSWORD` is a random token in its environment, never on the command line and never stored. The daemon creates one session through the HTTP API (`POST /api/session` with the model), writes its ID to a file, and asks for `GET /api/location` with the project folder, which is what makes OpenCode load plugins (a created session alone does not: tested). The agent-net plugin then reads the file, registers that session, and the task arrives as a normal message. No flag turns permission checks off.

Why not `opencode run`: tested with OpenCode 2.2.0. Without `--standalone` it talks to the user's background service (state in `$XDG_STATE_HOME/opencode/service.json`), so the work would run inside that service, which the daemon does not own and cannot kill. With `--standalone` it runs one turn and exits, so a follow-up has nowhere to go. `serve` is long-lived, private and ours.

### Isolation from the user's own service

Findings, from the 2.2.0 binary and a throwaway `HOME` and `XDG_*`:

- OpenCode 2.2.0 keeps its data (sessions, and the credentials in a `credential` table) in one SQLite file, `$XDG_DATA_HOME/opencode/opencode.db`. `auth.json` in that folder is only imported once from older versions.
- Every command has `--standalone`, so a second OpenCode process on the same database is a supported mode. A plain `serve` does not write `service.json`.
- A separate data folder would hide the user's credentials. Copying them to a worker's folder would copy secrets, and a refreshed OAuth token in the copy could invalidate the user's own login.

So the worker shares the data folder (it needs the credentials and the user's sessions are theirs to keep) and isolates what could clash:

1. **State folder.** `XDG_STATE_HOME` is a private folder in the daemon's state (`workers/<id>/state`), deleted when the worker ends. The worker never reads or writes the service's `service.json`.
2. **Port and password.** It listens on `127.0.0.1` only, with its own random password. It does not use the service's port.
3. **Plugin discovery.** The OpenCode plugin registers the user's newest sessions at load. In a worker that would register sessions that the user's own service also registers. The daemon sets `AGENT_NET_WORKER`, and the plugin then skips discovery. It registers only the session named in `AGENT_NET_WORKER_SESSION_FILE`, which the daemon writes after it created the session. (A session title cannot carry the worker ID: `POST /api/session` ignores `title`.)
4. **Who may be the worker's session.** The daemon binds a worker's session to the worker's process group. A connection for that session from any other process (the user's service sees the worker's session in the shared database) is refused with "belongs to a worker". A worker has one session; a second one is refused.
5. **The worker's sessions stay in the user's database,** (OpenCode names them from their first prompt). They show in the user's OpenCode session list after the worker ends. This is a visible side effect, and it is useful for reading what a worker did.

What is not isolated: the two processes write one SQLite file (WAL, with a busy timeout, like any two OpenCode commands), and a worker started by a newer OpenCode binary than the service runs may migrate the database. That is the same as running `opencode` by hand.

### The rest of the build

- **Where it is decided.** The caller's daemon sends `worker.start` to the gateway. The gateway resolves the project from the merged projects list, picks the machine, checks that it is online, accepts workers (`launch` in its hello), has the harness, and lists the model. It then sends `worker.launch` to that machine's daemon, which checks the folder, the limit and the harness, starts the process and answers when the plugin has registered the session. The caller's daemon then sends the task as a message from the caller.
- **Opt out.** `agent-net setup --no-launch` writes `no_launch` in `node.json`; the daemon reports it in hello and in the inventory update. `--launch` undoes it. A rejoin keeps it.
- **A worker cannot start workers.** Three checks: the daemon of the caller looks at the worker flag of the bound session (set by the daemon, not by the model); the gateway looks at the flag of the owner session; and a CLI call from inside a worker's process is refused (O2).
- **Ending (W7).** The daemon polls every 5 s (a setting). Idle 60 min, age 4 h, owner offline or gone 10 min, `worker_stop`. It sends SIGTERM to the process group, waits 3 s (a setting), then SIGKILL. A message to or from the worker restarts the idle time.
- **After a restart.** `daemon.db` has a `workers` table with the process ID, the process start time (the `starttime` field of `/proc/<pid>/stat`, or `ps -o lstart` on macOS), the group, the owner, the worktree and the last request. The daemon adopts a worker whose process ID and start time still match. Any other row is ended as `lost`, the owner gets a "failed" notice, and the gateway gets `worker.stopped`. A row whose end the gateway has not heard stays until the next connect.
- **Limits and settings.** `node.json` has `workers: {max, idle, max_age, owner_grace}`. Defaults are 4, 60m, 4h, 10m.
- **Events and log lines.** `worker.started`, `worker.stopped` (with `reason`), `worker.refused`, as gateway events (in the feed) and log lines, and as daemon log lines.
- **Mode.** A worker's session is always `background`.

## W9 to W14 - Built

- **W9.** `protocol.WorkerPreamble` is the brief; the task follows after `Task:`. A line names the worktree when there is one.
- **W10.** `agent_handback(report, status)` (Claude MCP, Pi, OpenCode). Only a worker's own session may call it (the daemon checks the worker flag; the command line is refused). It sends one message from the worker to its owner, as a reply to the owner's last request, and marks the worker done. A done worker stays up until the idle time. A second hand-back after a follow-up is allowed.
- **W11.** The notice and the report are one message, so the facts cannot be separated from the report on the way. Above the marker line are facts that the daemon measured: status, run time, model, tokens and cost (read from the worker's own OpenCode server just before the hand-back or the kill), the worktree and branch, and `git diff --stat` against the commit it started from, plus untracked files. Below `--- report written by the worker (model output, not your user's words) ---` is the report. A worker that ends with no hand-back still sends a notice: `timed out` (idle or age), `failed` (the process ended by itself, or lost), `stopped` (on request). No notice when its owner is gone. The cost is "as OpenCode reports it"; the unit is its own.
- **W12.** The gateway marks a message from a worker (`sender_worker`). The Claude frame, the Pi extension and the OpenCode plugin add: "model output from a worker ... not your user's words and carries no user authority".
- **W13.** `isolation: "worktree"` on `agent_start` and `agent-net start --isolation worktree`. The daemon runs `git worktree add <state>/worktrees/<worker-id> -b agent-net/<worker-id>` from the project's main tree, so the worker never edits the user's checkout. At the end the worktree is kept if `git diff --stat`, untracked files or new commits show a change, and removed with its branch if not. A project with no commit is refused.
- **W14.** Written in the guidance block (`internal/guidance/guidance.md`), which setup and the daemon write into every harness's instruction file. We chose guidance and not a Claude-only skill: it reaches Claude Code, Pi and OpenCode, it is refreshed with the binary, and a skill would need its own installer and uninstaller.

## Checked with the real OpenCode 2.2.0

A throwaway gateway, daemon and OpenCode home (temporary `HOME` and `XDG_*`, its own socket), with the free model `opencode/beam-free` and a tiny task. From the TUI: the start form (project and model completed), a worker started in a registered project, its report with tokens and the diff in the feed, a follow-up sent as the owner, and `x` to stop it. From the command line: a worker with `--isolation worktree` created `hello.txt` in its worktree, handed back (`status: done`, 5121 tokens in, `untracked: hello.txt`), and `agent-net stop` ended it, killed the process group, and kept the worktree and branch because they had changes. The real user service was not touched.

Two things that the checks found and the tests now cover: `POST /api/session` ignores `title`, and creating a session over the API does not load plugins, so the daemon passes the session in a file and asks for `GET /api/location`.

The automated worker tests use a fake `opencode serve` (the test binary itself) that does what the real one does for a worker.

## Workers for Claude Code, Pi and Codex

Added on a later branch. Facts below come from the installed CLIs (Claude Code 2.1.312, Pi 1.4.0, codex-cli 0.163.0) and were checked on the real programs where it says so. The worker manager is the same for all four harnesses: the limits, the timers, `agent_handback`, the notice, worktrees, the `workers` table, adoption and the owner rules. A harness only has to start its process, say when it is ready, and (optionally) stop, adopt and end. These are the `launcher` interface and three optional interfaces (`stopper`, `adopter`, `ender`) in `internal/daemon/workers.go`. The `workers` table has a new column `harness_ref` (an older table gets it with `ALTER TABLE`). `protocol.WorkerHarnesses` is `opencode, claude, pi, codex`; the TUI form and `agent-net start --help` read it.

A start that is still waiting for its harness is no longer looked at by the 5 s check (it has its own time limit, and the check used to remove the log that explains the failure).

### Claude Code (W16): `claude --bg`

Checked on the real CLI with throwaway folders and the cheapest model (`haiku`):

- **The id.** `claude --bg` prints `backgrounded - <id> - <name>` and `claude stop <id>`, and returns at once. The id is the first 8 characters of the session ID. The session writes `~/.claude/sessions/<pid>.json` with `kind: "bg"`, the same `sessionId`, **`jobId` equal to the id**, and `messagingSocketPath`. The Claude adapter finds it from that file, and the transcript is `~/.claude/projects/<folder>/<sessionId>.jsonl`, so the mapping holds. `claude agents --json --all` lists the same id with the `pid`, and a stopped session without a pid.
- **The process.** The session is not a child of the daemon. It is a child of Claude's background service (`claude bg-pty-host`) and the leader of its own process group. **The environment of the `claude --bg` command is not passed on to the session** (a test variable set on the command was absent in the session and in its MCP server). So the worker's process and group come from the pid in the session file, and the daemon keeps the id and the pid in `workers` (`harness_ref` is the id).
- **Settings.** `--settings '<json>'` reaches the session. `"crossSessionInbound":"accept"` lets the agent-net messages in. `"env":{...}` sets variables for the session and for its MCP servers: the daemon puts `AGENT_NET_SOCKET` and `AGENT_NET_WORKER` there. This is the only way to give the session an environment. The user's `settings.json` is not touched.
- **The prompt, the model, the folder.** `--model haiku` works. The working folder is the folder of the command (`cmd.Dir`), also for a worktree. The first prompt is not an argument: the task is sent as a normal message to the inbox of the session, as for any Claude session. The session received it and answered (checked).
- **MCP.** The user-scope `agent-net` MCP server loads in the session as its child, in under a second. But the installed `agent-net mcp` can be older than the daemon and lack `agent_handback` (it was: v0.3.10). `--mcp-config '{"mcpServers":{"agent-net":{"command":"<daemon binary>","args":["mcp"]}}}'` replaces the server of that name for this session only (checked: only the given binary ran). The daemon passes its own executable, so the tools are those of the daemon.
- **Deferred tools.** Claude Code 2.1.312 runs with tool search on (`ToolSearch:optimistic mode=tst`): MCP tools are not in the tool list of a turn until the model loads them with `ToolSearch`. In two real runs a haiku worker said "I don't have an `agent_handback` tool" and answered in plain text, which is not delivered. The brief for a Claude worker now ends with: the tool is `mcp__agent-net__agent_handback`, and if it is not in the tool list, load it with `ToolSearch` (`select:mcp__agent-net__agent_handback`). **This brief change was not run on the real CLI** (the run budget was spent): see "Not done".
- **Timing.** A turn that starts before Claude has connected its MCP servers has none of their tools. They connected within 1 s in the real runs. The start waits for the MCP server of the session to say hello, and 3 s more (3 s, `WorkerSettings.MCPSettle` in the daemon; it is not in `node.json`).
- **Stop (W7).** `claude stop <id>` ends the session and removes its session file (checked). The daemon runs it, waits for the process to go, and kills the process group if it did not (also the processes the session started). `claude rm <id>` is **not** run: the stopped session stays in `claude agents` with its conversation, as the OpenCode sessions stay in OpenCode's list. The owner can read it, or remove it.
- **Enter it (W16).** `claude attach <id>` works on the worker (it is a background session), so the owner can watch it or answer a permission prompt it waits on.
- **Permissions (W6).** No flag. The session uses the user's and the project's settings (auto mode in the checked runs). A step that needs approval waits in the session until the owner attaches. The daemon does not detect it: there is no `blocked` hand-back for Claude workers yet.
- **Binding.** The worker flag is put on the session that the adapter discovers, by the session ID that the launcher read from the session file (`SetSessions` marks it). The mode is `background`. The MCP connection of that session is checked as before (its parent process is the Claude process of a session file with that ID).
- **Stats.** Tokens and the model come from the transcript (the usage of each assistant message, once per message ID). Claude Code writes no cost, so the notice has none.
- **Needs.** The folder must be trusted by Claude (`Workspace not trusted` is the start error otherwise; the daemon passes it on). A worktree of a trusted repository is trusted. A new folder, or a repository nested in a trusted one, is not.

### Pi: `pi --mode rpc`

- **Mode.** `pi --help`: `--mode <text|json|rpc>`; `docs/rpc.md` in the package: RPC mode is a long-lived child that reads JSON commands on stdin and ends when stdin closes. The same file says `--model` takes `provider/id` with an optional `:thinking`, and that `--session-id <id>` uses that ID. `ctx.mode` is `"rpc"` in it, which the Pi extension already reports as a `background` session (`ExtensionMode` in `dist/core/extensions/types.d.ts`).
- **Command.** `pi --mode rpc --session-id <uuid> --name "worker <project>" [--model M]`, in a new process group, in the project folder. The daemon picks the UUID, so the worker is bound to its session before Pi starts and no other process may connect as it (as for OpenCode). Pi's extension loads in this mode (the daemon test and the real run), registers the session, and has `agent_handback` and `agent_reply`.
- **Standard input.** It must never close. The daemon makes a named pipe in the worker folder, opens it for reading and writing, and gives it to Pi. Pi holds its own copy, so it is its own writer and never sees the end of the file, also after the daemon restarts (adoption). `/dev/null` would end Pi at once.
- **Model list.** `pi --list-models` prints a table (provider, model, context, ...), with no machine-readable form, so the model is not checked by the gateway. A model Pi does not know ends the process, and the start fails with Pi's output.
- **Permissions (W6).** Pi has no permission prompts: its tools run without asking. There is nothing to approve or block, and a Pi worker can run any command in the project folder. This is Pi's design, and it is written in `docs/security.md`: use `isolation: worktree` for tasks that change code.
- **Enter it (W16).** Not possible while it runs: rpc mode has no screen. The session file stays in Pi's session folder, and `agent-net show <session>` reads the conversation through the extension.
- **Stats.** None: the notice has no tokens or cost (Pi has `get_session_stats` over rpc, but we send no command).

### Codex: `codex app-server`, driven by the daemon

- **Modes checked.** `codex exec` runs one prompt and ends (`exec resume` would start a new process for each follow-up). `codex queue` queues a message for an existing session of a running server. `codex app-server` is the long-lived mode: `--listen stdio://|unix://|ws://IP:PORT`, with `--ws-auth capability-token --ws-token-file` for WebSocket. `codex app-server generate-ts` writes the protocol types. We use the app server: it is the one mode that gives a process the daemon owns, structured events, tokens, and approval requests as requests that we answer.
- **Transport.** A WebSocket on `127.0.0.1:<free port>` with a capability token (32 random bytes, hex) in a file of the worker folder (mode 0600), sent as `Authorization: Bearer <token>`. Stdio would end the server when the daemon restarts; the WebSocket survives, and the daemon connects again (`harness_ref` is the address; the token file is in the worker folder).
- **Protocol (from the generated types, and checked on the real server).** `initialize`, then `initialized`; `thread/start {cwd, model}` answers the thread; `thread/resume {threadId}` rejoins it; `turn/start {threadId, input:[{type:"text",text}]}`. Notifications: `item/completed` (the final answer is an `agentMessage` with `phase: "final_answer"`), `thread/tokenUsage/updated` (`total` with input, cached input, output, reasoning), `turn/completed` (`status` is `completed`, `failed` or `interrupted`; `error.message`). Server requests: `item/commandExecution/requestApproval`, `item/fileChange/requestApproval`, `item/permissions/requestApproval`, `item/tool/requestUserInput`, `mcpServer/elicitation/request`, and the older `execCommandApproval` and `applyPatchApproval`. `model/list` exists and lists the models of the account.
- **Option (b), MCP, not taken.** `codex mcp add` and `-c mcp_servers.<name>.command=...` support stdio servers, and the thread could have been given the agent-net MCP server through per-thread config. We did not, because the daemon already has to register the thread as a session and to deliver the messages (a Codex session has no inbox), and a hand-back that the daemon makes at the end of a turn cannot be forgotten or sent in plain text. The cost: no `agent_reply` for a question in the middle of the work, and the brief for Codex does not name `agent_handback` (`WorkerPreambleCodex`).
- **Session and delivery.** The daemon registers the thread as a session of harness `codex` (name `codex worker <project>`, mode `background`, with the worker flag, status `busy` during a turn). A message to it goes to the `codex` delivery (`codexDelivery`), which accepts messages from the owner of the worker and from the person at the machine, and fails the message for any other sender. The text of the message is the turn. A person's follow-up is framed "Message from your user, in person". Turns run one after the other (a message that arrives during a turn is queued).
- **Hand-back.** At the end of each turn the daemon calls the same hand-back as `agent_handback`, to the sender of the message that started the turn: status `done` with the final answer; `failed` for a failed or interrupted turn (with the reason); `blocked` when an approval was asked.
- **Permissions (W6).** No approval policy, no sandbox, no flag: `thread/start` has only `cwd` and `model`, so the project's and the user's Codex configuration decide. Every approval request is **declined** (`decline` or `denied`; the other requests get an error), and the turn's hand-back is `blocked` with the W15 fields: the action, the reason, `approve_in` (the thread ID, and `codex resume <id>` in the project folder), and an `approval_text`.
- **Follow-up while the daemon is down.** Adoption reconnects, `thread/resume`s, and registers the session again. The state of a turn that was running during the restart is lost: its hand-back does not come, and the worker ends at its idle time.
- **Model list.** `model/list` exists, but only on a running app server, so the inventory does not have it. The gateway accepts any model string for Codex; a name Codex refuses fails the first turn with its error.
- **Enter it (W16).** `codex resume <thread id>` in the project folder opens the thread (the thread is not ephemeral). Two clients on one thread is Codex's own business.
- **Stats.** Tokens and the model, from the thread. No cost.

### Real runs

Done with `scripts/smoke-worker.sh <claude|pi|codex>` (a throwaway gateway and daemon, a tiny task, the cheapest model; it spends money, so it is not part of the tests). Run it from your own terminal: the owner gate refuses `start` from under an agent.

- **Codex (gpt-6-luna):** the whole path works. Start, thread, turn, automatic hand-back (`status: done`, "tokens: 3088 in, 5 out, 11520 cache read", report "OK"), `agent-net stop`, session offline. The run used a private `CODEX_HOME` with a link to the real `auth.json`.
- **Claude Code (haiku), three runs:** the start works (`claude --bg`, session file, `jobId`, worker flag, worktree of a trusted repository), the inbound message is accepted (`crossSessionInbound`) and the model answers, `claude stop` and `claude rm` work. The hand-back did not happen: the worker had no `agent_handback` in its tool list because Claude defers MCP tools (see above), and answered in plain text. The fix (a line in the brief) came after the third run, so it is unproven on the real CLI.
- **Pi:** the extension of this build loaded in `pi --mode rpc` (through a private `PI_CODING_AGENT_DIR`), registered the session that the daemon chose, received the task, and started a turn. The model call failed: on this machine Pi's `openai` login is a ChatGPT account, and every model I tried (`gpt-4.1-nano`, `gpt-5.4-mini`, `gpt-5.5`, also in `pi -p`) is answered with `model_not_found ... not supported when using Codex with a ChatGPT account`. So no Pi hand-back was seen on the real program.
- **A failed Pi turn is silent.** Pi's rpc events go to stdout, which the daemon drops (they are large and nobody reads them), so a worker whose model call fails does not hand back `failed`. It ends at the idle time with `timed out`. Reading the events (`turn_end` with `stopReason: error`, `agent_settled`) would fix it; they would have to go to a file, not to a pipe that the daemon holds, because Pi dies on a broken pipe.

## Not done

- **Claude Code workers through `-p`.** Not built, on purpose: they are `claude --bg` sessions (W16), see above.
- **A real Claude hand-back.** The real runs showed the start, the discovery of the session, the inbound message, the answer, `claude stop` and `claude rm`. They did not show a successful `agent_handback`, because the first two workers did not load the deferred MCP tool. The brief now tells them how; that change is covered by a test of the text only.
- **Claude and OpenCode approvals.** A Claude worker waits on a permission prompt until the owner attaches; an OpenCode worker waits for an answer. Neither hands back `blocked`. Codex does.
- **Permission requests.** A worker that meets an `ask` permission waits for an answer that nobody gives; it ends at the idle time with a "timed out" notice. W6 says it should report. OpenCode's permission events are not yet turned into a hand-back.
- **Token and cost after a daemon restart.** The worker's server password is kept in memory only, so an adopted worker's notice has no tokens or cost.
- **A TUI worktree field,** and the phone app.
- **Windows.** Unchanged.
