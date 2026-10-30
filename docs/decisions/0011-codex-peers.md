# 0011 - Interactive Codex sessions are peers

**Date:** 2026-10-30
**Status:** Built on branch `worktree-agent-a6b5a98a502fdac19`. The owner approved the goal. Proposed until the owner has read it.
**Context:** Until now Codex was only a worker ([0002](0002-workers.md), [0008](0008-operator-interface-and-worker-build.md)): the daemon started a private `codex app-server` and drove it. A Codex window that the owner opened was invisible to agent-net, could not receive a message and could not send one. This text makes those windows full peers, like Claude Code, Pi and OpenCode sessions.

## Facts, and how they were checked

Checked on codex-cli 0.163.0 with an isolated `CODEX_HOME`, `HOME` and `XDG_*` folders, a throwaway app server, and a fake model server on a loopback port (`wire_api = "responses"`), so no paid model was used and the owner's `~/.codex` and shared daemon were never opened. The owner's daemon runs 0.166.0, so every point below must also hold there; this is listed as unverified at the end.

- **The shared daemon.** `codex app-server --listen unix://` is the shared daemon (`daemon_auto_start`). Its control socket is `$CODEX_HOME/app-server-control/app-server-control.sock` (`CODEX_HOME` is `~/.codex` by default; source: `app_server_control_socket_path` in openai/codex). That path is a symlink to `/tmp/codex-daemon-<uid>/<hash>`, because a socket path is limited to about 108 bytes. The protocol on the socket is a WebSocket upgrade at `ws://localhost/` and then the same JSON-RPC as `app-server`. There is no token: the socket has mode 0600 in a 0700 folder. Agent-net follows the symlink and dials the target.
- **Schema.** `codex app-server generate-ts --experimental` gives the types. The methods used: `initialize`, `thread/loaded/list`, `thread/read` (`includeTurns: false`), `thread/queue/add`, and the notifications `thread/started`, `thread/status/changed`, `thread/name/updated`.
- **Which threads are open.** `thread/loaded/list` returns the threads that the server holds in memory. The server unloads a thread when it has no subscriber and has been inactive for a delay (`UnloadingState` in `thread_lifecycle.rs`). A window that shows a thread subscribes to it. So "loaded" means "open in a window, or closed a short time ago". `thread/list` is history and is not used: it returns every old thread. A thread started with `thread/start` and no turn yet is in the loaded list but not in `thread/list`.
- **No subscribing.** A second client that never started or resumed a thread still receives `thread/started` and `thread/status/changed` (`active` / `idle`) for every thread. It receives no item or turn events and no approval requests. Agent-net must never `thread/resume` an interactive thread: that would subscribe it (the thread would never unload) and could make it a target of approval requests that it must not answer.
- **Queue.** `thread/queue/add {threadId, input, clientUserMessageId}` on an idle thread starts a turn at once; the text becomes a `userMessage` item with `clientId` equal to `clientUserMessageId`, so the window shows it as the user's message. On a busy thread it waits behind the running turn (this is what `codex queue --thread` does; the CLI source calls the same method). `thread/queue/list` shows what waits.
- **MCP servers.** Codex starts each stdio MCP server once per thread, when the thread starts, as a direct child of the app server process. The server gets no thread ID in its environment. Every `tools/call` carries `params._meta.threadId` and `sessionId`, set by Codex (source: `with_mcp_tool_call_ids_meta` in `mcp_tool_call.rs`; seen in a real call). The model cannot set `_meta`.
- **Tool approval.** With `approval_policy = "never"` an MCP tool call fails with "MCP tool call requires approval, but approval policy is never", unless the server has `default_tools_approval_mode = "approve"` in `config.toml` (tested). Config changes apply to threads started after the change.
- **Hooks.** Hook events exist (`HookEventName`: `sessionStart`, `userPromptSubmit`, `stop`, ...). They are not used: the app-server interface gives everything needed, and a hook runs a command in the user's name for every event.

## C1 - Discovery: the daemon is a read-only client of the shared daemon

The Codex adapter connects to the control socket, says `initialize` (client name `agent-net`, experimental API on, because the queue methods need it), and keeps the connection, with a retry every few seconds when the daemon is not running. If Codex has no daemon, there are no Codex peers, and nothing is started: agent-net never starts the shared daemon.

Every 5 seconds, and on each `thread/started`, `thread/name/updated` and `thread/status/changed`, it lists the loaded threads and reads the new ones. A loaded thread is a session when it is not ephemeral, has no parent thread (a sub-agent of another thread is not a peer) and is not one of our workers. Session fields: name (the thread name, else the folder name), folder, model, status (`busy` while the thread is `active`, else `idle`; `systemError` is `unknown`), harness `codex`, delivery `codex-app-server`. Mode is `unknown` for threads from the terminal and the IDE, because the daemon cannot see whether a window is still attached, and `background` for threads from `exec` or other app-server clients. The set goes through `SetSessions("codex", ...)`, so the gateway, `agent-net list`, the TUI, FUNCTION from `AGENTS.md` and the conversation view work as for any harness.

**No dormant rule.** OpenCode sessions needed one because its service keeps every session. Here the server drops a quiet thread with no window by itself, so a thread that is loaded stays visible, however long its window sits idle. A thread whose window closed leaves the list after the server's unload delay.

## C2 - Receive: queue, framed like the other harnesses

`Deliver` calls `thread/queue/add` with the message as one text input and the agent-net message ID as `clientUserMessageId`. The text has the body first, then the same footer as the Pi extension: sender name, session, conversation, "from another agent working on your user's machines", and how to answer (`agent_reply` with the message ID). A message from the person at a machine says so. A message from a worker carries the "model output, not your user's words" footer. Delivery is reported as delivered when the server accepted the queue entry; the daemon cannot see more without subscribing.

**Reply capture: not automatic.** A Codex worker's last message becomes its report, because nobody is in that session and the owner asked for the result. An interactive thread is different: the last message of the turn is written for the person at the window, and may hold things that were not meant for the sender. So the reply is an explicit act, `agent_reply`, as in the other harnesses. It would also need a subscription to the thread, which we avoid. The cost: a Codex thread that has no agent-net tool (setup declined, or a thread that started before setup) cannot answer a peer. It can still read the message, and its person can answer.

## C3 - Send: `agent-net mcp --codex`, and who is calling

Setup registers `agent-net mcp --codex` as the MCP server `agent-net` in `$CODEX_HOME/config.toml` (default `~/.codex/config.toml`) as a marked block (`# agent-net:begin` / `# agent-net:end`) at the end of the file, only when the owner's Codex is installed (the folder or the `codex` binary exists) and the file does not already define `[mcp_servers.agent-net]`. Setup asks first, on the terminal, as for Claude peer messages (`--accept-codex-tools` / `--no-accept-codex-tools` skip the question): "Let Codex call agent-net tools without asking each time?" Yes writes `default_tools_approval_mode = "approve"`. No (or no terminal) registers the server without it; Codex then asks on each call, and fails the call under `approval_policy = "never"`. Uninstall removes the block. It also writes the guidance block in `$CODEX_HOME/AGENTS.md` (variant "other": reply with `agent_reply`).

**Which thread is calling.** The shim cannot take the thread from its environment, and the process ID does not help: all threads share one parent. The shim reads `_meta.threadId` of each `tools/call` and opens one daemon connection per thread, each with a hello that names that thread. The daemon accepts the hello only when:

1. the shim's parent process is the Codex app server that the adapter is connected to. The adapter reads that process ID from the control socket itself (`SO_PEERCRED` on Linux, `LOCAL_PEERPID` on macOS), so no name or command line is trusted; and
2. the thread is a session that the adapter has discovered.

A shim that no thread has called yet says hello with no thread, and gets only the calls that need no session (the registry tool list). Everything after is the per-connection binding that Claude, Pi and OpenCode already use, so the sender of a message is never a parameter.

What this does not stop: a Codex model that starts `agent-net mcp --codex` through its shell tool, where the shell is exec-ed directly by the app server, and writes the JSON-RPC itself with another thread's ID. That needs a shell tool call that skips the sandbox's process wrapper, then hand-written protocol. It is the same trust level as any agent that runs commands as the owner: it could also read `~/.config/agent-net` or ask for the owner's socket. We did not add a check that the target thread has a tool call running now, because that needs the daemon to subscribe to threads. This is recorded in `docs/security.md`.

## C4 - Codex workers stay on their private server

The question was whether to start workers on the shared daemon, so that `codex agents` shows them. They stay private:

- **Ending.** Decision 0002 W7 ends a worker by killing the process group the daemon owns. A thread on the shared daemon has no process to kill; only an interrupt and an archive, and the thread stays loaded until the server's unload delay.
- **The daemon is not ours.** The managed daemon updates itself and restarts ("may interrupt running work"), which would end workers in the middle of a task, and we would not own the restart.
- **Approvals.** A worker declines every approval request (W15). On the shared daemon a window that resumes the thread would also be asked, and which client wins is not verified.
- **Version.** The owner's CLI and daemon can differ (0.163.0 and 0.166.0 on the machine of this work). A private server is the binary we found.

So worker threads are not in the shared loaded list and do not clash with discovery. The adapter still ignores any thread that a worker owns, and `SetSessions` does not delete the worker's own session record. `e` on a worker works as before.

## C5 - Enter

`e` on a Codex session that is not a worker plans `codex resume <thread>` in the thread's folder. With a `CODEX_HOME` that is not `~/.codex` the plan carries `CODEX_HOME` in `Env`. `codex resume` joins the shared daemon (`--help`: interactive windows run on it), so the owner gets the same thread in a window. Other machines: as before, planned by that machine and wrapped in `ssh -t` only with `enter_ssh`.

## What is not done, and why

- **No thread event stream.** Message state beyond "delivered", and a check in C3 that the named thread has a tool call running, need `thread/resume`, which subscribes. A later decision can add it for threads that have agent-net tools, if the approval routing is verified on the owner's daemon version.
- **`session_text` (the conversation view of a Codex session)** is not built. It needs `thread/turns/list` and `thread/items/list`; they are read-only and do not subscribe, so this is a follow-up and not a blocker.
- **Process columns (CPU, RAM)** are empty for Codex peers: the window's process is not known to the daemon (PID 0).

## Unverified

- Everything above on the owner's 0.166.0 daemon (the isolated runs used 0.163.0).
- That `codex resume <thread>` in a real terminal opens the loaded thread (checked with `--help` and the CLI source only; a real terminal run needs a login).
- A real model turn that calls `agent_reply` (it needs a login; the isolated runs used the fake model server, which is enough to prove the MCP path, `_meta.threadId` and the queue).
- Windows and macOS (the code compiles for macOS; the socket peer lookup on macOS is the same call as for the other harnesses).
