# Spike: Codex CLI

codex-cli 0.163.0 TUI (plain `codex`, no `--remote`), Linux. A shared app-server daemon 0.165.0 is running (`codex app-server --listen unix:// --managed-daemon`). Target: a throwaway session in tmux.

## Finding the thread

- A thread exists only after the first prompt.
- The rollout file appears at `~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<thread uuid>.jsonl`. `~/.codex/session_index.jsonl` holds names.
- OCS proves a CLI session is live by finding the process that holds the rollout file open (`src/codex-sessions.ts`). Port this for discovery.

## Results

| Test | Result |
|---|---|
| Idle target, `codex queue --thread <uuid> --message <text>` | `Queued message <id> for thread <uuid>`, exit 0. The TUI started a new turn at once in the existing thread and answered in about 3 s. |
| Busy target (25 s shell command running) | Queued. Delivered as a follow-up right after the running turn ended, then answered. Did not disturb the running turn. |

## Gaps

- **No sender label.** The queued text shows as if the user typed it. The daemon must put the sender and reply instructions into the text itself, for example a `[agent-net message from X, id Y. Reply with the agent_reply tool]` header.
- **No native reply path.** Codex has nothing like Claude's `SendMessage`. Replies must go through our MCP tool (`agent_reply`), so the per-session MCP process is required for Codex.
- **MCP process identity, not tested.** The TUI had no child processes in this test. Step 2 must check what a Codex MCP child gets in its environment. If nothing, use the parent PID and the rollout file it holds open.
- **Desktop, not tested.** Needs the MacBook. Does it share the app-server daemon? If not, port the OCS Desktop IPC path.
- **JSON-RPC to the daemon control socket** (`~/.codex/app-server-control/app-server-control.sock`) was not tried. `codex queue` is enough for v1. Direct JSON-RPC would give turn events (idle/busy) without polling.
