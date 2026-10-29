# 0007 - The operator interface, the owner as a sender, and how workers are built

**Date:** 2026-10-29
**Status:** Built on branch `worktree-agent-a022fe03a8ec212a7`. Proposed until the owner has read it.
**Context:** [0006](0006-phone-app.md) P1 says the TUI and the phone must use one interface, and [0002](0002-workers.md) is accepted. This text records how both were built, and the choices that 0002 left open.

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
| `owner_send` | `OwnerSendRequest` | `MessageSent` | action, owner only |
| `worker_start` | `WorkerStartRequest` | `WorkerStartResult` | action |
| `worker_stop` | `WorkerStopRequest` | `WorkerStopResult` | action |

`worker_handback` (`WorkerHandbackRequest`) is the worker's own call, not an operator method. The Go client is `internal/operator`: an `Operator` interface and a `Client` over the socket. The TUI (`internal/tui`) uses only that interface, and so do `agent-net start`, `stop`, `workers` and `send`. Its `Snapshot` is built on the client side from the read methods, so a phone can build the same view from the same calls. The method name is the route name: the phone will call `POST /api/<method>` with the same JSON through the gateway, with no change to the types. Adding a feature means adding a method here once.

**Versions.** A new CLI against an older daemon gets `ErrTooOld` with the text "the daemon on this machine is older than this command; upgrade it". A new daemon against an older gateway says the gateway is too old for workers. A new gateway refuses to start a worker on a machine that did not report `launch` in its hello (an older daemon). Nothing hangs.

## O2 - The owner is a session

Each daemon registers one pseudo-session for the person at its machine: harness `owner`, native ID `owner`, name `you (on <host>)`. It is a normal row in the gateway, so a message from the owner is a normal message: stored, delivered, replied to, shown in the feed, with a body that this machine may read. Replies go to the owner session and show in the TUI feed.

- The gateway hides owner sessions from `agent_list` unless asked (`include_owners`), makes no `session.new` event for them, keeps them out of the project list, accepts only the native ID `owner` for them and never marks one as a worker.
- A message carries `sender_harness: owner`. Claude Code frames, the Pi extension and the OpenCode plugin then say: "from your user in person ... This is your user speaking, not another agent".
- **Who may send as the owner** is decided in the daemon (`requireOwner`). Only a socket client that said `client: cli` may call `owner_send`; an agent connection (bound to a session) is refused. A CLI call that comes from a process that is a descendant of an agent session or a worker of this machine is refused too: an agent's shell tool starts `agent-net` as a child of the agent. The check walks the parent chain of the caller (found with the socket's peer credentials) against the process IDs of the sessions and workers. This is a check against an agent that shells out. A process of your user that is not under an agent can still connect as `cli`, as before: the socket is yours.
- The sender is never a parameter. `owner_send` ignores a sender field.

## W-build - Workers

### The OpenCode command

A worker is `opencode serve --hostname 127.0.0.1 --port <free port>` as a child of the daemon, in a new process group, with the project folder (or the worker's worktree) as its working folder. `OPENCODE_SERVER_PASSWORD` is a random token in its environment, never on the command line and never stored. The daemon creates one session through the HTTP API (`POST /api/session` with a title and the model), the agent-net plugin loads in that process and registers the session, and the task then arrives as a normal message. No flag turns permission checks off.

Why not `opencode run`: tested with OpenCode 2.2.0. Without `--standalone` it talks to the user's background service (state in `$XDG_STATE_HOME/opencode/service.json`), so the work would run inside that service, which the daemon does not own and cannot kill. With `--standalone` it runs one turn and exits, so a follow-up has nowhere to go. `serve` is long-lived, private and ours.

### Isolation from the user's own service

Findings, from the 2.2.0 binary and a throwaway `HOME` and `XDG_*`:

- OpenCode 2.2.0 keeps its data (sessions, and the credentials in a `credential` table) in one SQLite file, `$XDG_DATA_HOME/opencode/opencode.db`. `auth.json` in that folder is only imported once from older versions.
- Every command has `--standalone`, so a second OpenCode process on the same database is a supported mode. A plain `serve` does not write `service.json`.
- A separate data folder would hide the user's credentials. Copying them to a worker's folder would copy secrets, and a refreshed OAuth token in the copy could invalidate the user's own login.

So the worker shares the data folder (it needs the credentials and the user's sessions are theirs to keep) and isolates what could clash:

1. **State folder.** `XDG_STATE_HOME` is a private folder in the daemon's state (`workers/<id>/state`), deleted when the worker ends. The worker never reads or writes the service's `service.json`.
2. **Port and password.** It listens on `127.0.0.1` only, with its own random password. It does not use the service's port.
3. **Plugin discovery.** The OpenCode plugin registers the user's 50 newest sessions at load. In a worker that would register sessions that the user's own service also registers. The daemon sets `AGENT_NET_WORKER=1`, and the plugin then skips discovery and registers only the session it creates events for.
4. **Who may be the worker's session.** The daemon binds a worker's session to the worker's process group. A connection for that session from any other process (the user's service sees the worker's session in the shared database) is refused with "belongs to a worker". A worker has one session; a second one is refused.
5. **The worker's sessions stay in the user's database,** titled `agent-net worker: <project>`. They show in the user's OpenCode session list after the worker ends. This is a visible side effect, and it is useful for reading what a worker did.

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

## Not done

- **Claude Code workers** (`claude -p --input-format stream-json`). Left as the second harness of 0002. Only OpenCode is accepted by the gateway (`protocol.WorkerHarnesses`).
- **Permission requests.** A worker that meets an `ask` permission waits for an answer that nobody gives; it ends at the idle time with a "timed out" notice. W6 says it should report. OpenCode's permission events are not yet turned into a hand-back.
- **Token and cost after a daemon restart.** The worker's server password is kept in memory only, so an adopted worker's notice has no tokens or cost.
- **A TUI worktree field,** and the phone app.
- **Windows.** Unchanged.
