# Codex

Codex threads that you have open join the network as peers: they show in `agent-net list` and the TUI, they can receive messages, and they can send. The design and the facts behind it are in [decisions/0012-codex-peers.md](decisions/0012-codex-peers.md). Codex workers are a separate thing (see [reference.md](reference.md#workers)).

## How it works

agent-net discovers threads on the shared local Codex app server. It does not discover an embedded server inside a terminal process. The daemon does not start the shared server.

### Starting a terminal session

On Linux with codex-cli 0.167.0, a normal `codex` launch uses the shared server when eligible, starting it if needed. `-m`, `-s`, and an initial prompt do not themselves prevent this. A `-c model_reasoning_effort=...` or `-c sandbox_workspace_write.network_access=...` override instead selects an embedded server, whether or not the shared server is already running. Most other `-c` overrides do the same; Codex allows certain boolean feature and TUI overrides during implicit reuse, subject to server compatibility. `--no-daemon`, `--oss`, `--profile`, `--strict-config`, `--dangerously-bypass-hook-trust`, a custom config loader, workload identity, and `CODEX_EXEC_SERVER_URL` also exclude implicit shared-server reuse. An implicit connection can fall back to an embedded server if connecting fails. These embedded threads are invisible to agent-net and their MCP calls are refused.

To keep per-launch overrides and require the shared server, start it first and attach explicitly. For the chief of staff:

```sh
codex app-server daemon start
exec codex --remote unix:// -m gpt-6.1-sol -c model_reasoning_effort=medium -s workspace-write -c sandbox_workspace_write.network_access=true "$PROMPT"
```

`--remote unix://` requires the shared server to be running and fails instead of falling back to an embedded server. The server and the terminal must use the same `CODEX_HOME` as agent-net. The explicit attachment forwards the model, reasoning effort, sandbox mode and network override to the new thread. agent-net-started terminal sessions pin the shared server when its adapter is connected; if it is not yet connected, Codex uses its normal automatic startup.

- **Finding threads.** The daemon connects to the server's control socket, `$CODEX_HOME/app-server-control/app-server-control.sock` (default `~/.codex/...`), and lists the threads the server has loaded every 5 seconds and on each change. A loaded thread is one that is open in a window, or was closed a short time ago: the server drops a thread that has no window and no activity. Sub-agent threads and scratch (ephemeral) threads are left out. If the server is not running, there are no Codex sessions, and the daemon tries again every 3 seconds. The daemon never starts it.
- **Status and mode.** `busy` while a turn runs, else `idle`. MODE is `unknown` for threads from the terminal and the IDE, because the daemon cannot see whether a window is still attached; `background` for threads that a program drives (`codex exec`, SDK clients). There is no dormant state: a loaded thread stays listed, however long it sits idle.
- **Receiving.** A message goes into the thread's queue (`thread/queue/add`, the same call as `codex queue`). On an idle thread it starts a turn; on a busy one it waits for the running turn. The thread shows it as a message from the user, with the body first and a footer that says it is from another agent, not from the user, and how to answer (`agent_message_reply` with the message ID).
- **Replying.** Only with `agent_message_reply`. agent-net does not copy the last message of the turn back to the sender, because that message is written for the person at the window. (A Codex worker is different: its last message is its report.)
- **Sending.** `agent-net mcp --codex` is the MCP server of each thread. Codex starts one process per thread and tells it nothing about the thread; every tool call carries the thread in `_meta.threadId`, which Codex sets and the model cannot. The process opens one daemon connection per thread. The daemon accepts it only if the process's parent is the Codex app server it is connected to, and the thread is one it found. It requests a fresh discovery and briefly waits for a new thread to register before refusing the first call. See [security.md](security.md#which-local-process-is-calling).
- **Entering.** Enter (or `e`) in the TUI runs `codex resume <thread>` in the thread's folder.

## Setup

`agent-net setup` adds the server to `~/.codex/config.toml` (or `$CODEX_HOME`) as a marked block, and the guidance text to `~/.codex/AGENTS.md`. It follows the answer to the one integrations question, or `--accept-codex-tools` / `--no-accept-codex-tools` (a local answer for that machine). The answer is whether Codex may call the agent-net tools without asking each time (`default_tools_approval_mode = "approve"`, for this server only). Without it, Codex asks before every call, and refuses calls when its approval policy is `never`. You can also change the line in `config.toml` by hand. See [reference.md](reference.md#what-setup-changes-on-your-machine).

The block also names the daemon's socket in `env`. Codex starts `agent-net mcp --codex` with only a few variables of its own, so without that line the server dials the fallback socket path, finds no daemon, and every tool call says "agent-net daemon not reachable at ...". A block written by an older setup has no `env` line: run `agent-net setup` once to update it.

Codex reads its config when a thread starts: a thread that was open before setup does not have the tools until you start a new one. Receiving needs no setup.

## Limits

- No process figures (CPU, RAM) for Codex sessions: the window's process is not known.
- A thread without the agent-net tools can read a message but cannot answer it; its person can.
- The terminal startup rules above were checked on codex-cli 0.167.0. The shared server of the machine can be newer.

## Tests

`go test ./internal/daemon` runs a fake app server. With `AGENT_NET_CODEX_E2E=1` and `codex` on `PATH`, `TestCodexRealAppServer` starts a real `codex app-server` in a throwaway `CODEX_HOME` and `HOME`, with a fake model on a loopback port, and checks discovery, the queue, the embedded-terminal refusal, and an interactive terminal's first `agent_announce` through the real MCP server. It uses no model account and never opens `~/.codex`.
