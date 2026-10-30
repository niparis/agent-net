# 0020 - Unattended workers get a sandbox, not approval prompts

**Date:** 2026-10-30
**Status:** Built for Codex on this branch. The owner approved the design. Amends [0002](0002-workers.md) W6. The other harnesses follow later.
**Context:** A Codex worker could do nothing. Decision 0002 W6 left the approval policy and the sandbox to the user's Codex config. Codex trusts folders by path, and a worker's git worktree (under the daemon state folder) is a new, untrusted folder, so Codex asked approval for every command. The daemon declines every approval, because nobody watches a worker. The worker also tried the agent-net tools (`agent_announce`, `agent_handback`), which the user's Codex config loads, and Codex asked approval for those too.

Base: [0002](0002-workers.md) (workers), [0008](0008-operator-interface-and-worker-build.md) (the Codex worker, the owner gate), [0014](0014-delegation-policy.md) (the per-project store and the TUI form that this reuses).

## The change

W6 said: a worker uses the project's own permission settings. That cannot work for a worker nobody watches. The new rule: **an unattended worker never needs an approval. It runs in a sandbox that the owner chooses per project.**

| Level | Writes | Network |
|---|---|---|
| `sandboxed` (default) | The worker's worktree, the temp folders, and the folders of the main repository that a commit writes to (below). | on |
| `no-network` | The same. | off |
| `read-only` | None. For review and investigation tasks. | on |

The default applies when the owner has set nothing.

## Where the level lives

In the project policy of [0014](0014-delegation-policy.md): the table `project_policies` (key: machine and lower-case project name), field `workers` of the policy JSON. No migration: the policy is a JSON text. The owner sets it with key `p` in the TUI: a new line **Workers** with the choices default (sandboxed), sandboxed, no-network, read-only. Choosing a level sets the scope to project, and the gateway refuses a level at session scope ("the workers level is set per project"). Saving at project scope replaces the whole project policy, so the form is filled from the project policy when the session follows it (a session with its own policy shows its own, and the form then starts with the default level).

The gate is the same as for the delegation policy: the operator method `session_policy` is served only to the owner's command line or TUI. An agent session, and a command line inside an agent session's process tree, are refused. No agent tool sets the level, so an agent cannot widen the sandbox of the workers it starts.

The gateway looks the level up when it handles `worker.start`, and sends it in `worker.launch` (`permissions`). It answers the start with it (`WorkerStartAnswer.Permissions`, `WorkerStartResult.Permissions`). The daemon keeps it with the worker (daemon database column `workers.permissions`, so a follow-up that starts the thread again uses the level the worker started with, not a later change) and reports it in the worker part of the session (`WorkerInfo.Permissions`). The TUI detail pane of a worker shows it on a `sandbox` line, with "(not enforced yet: Codex only)" for the other harnesses.

## Codex: the protocol fields

Checked on codex-cli 0.163.0 (`codex app-server generate-json-schema`), and with a real `codex app-server` with a temporary `CODEX_HOME`, without a model call.

- `thread/start` (and `thread/resume`): `approvalPolicy: "never"`, `sandbox: "workspace-write"` or `"read-only"`, and for the write levels `config: {"sandbox_workspace_write": {"writable_roots": [...], "network_access": <bool>}}`. The answer repeats the effective `approvalPolicy` and `sandbox` (`workspaceWrite` with `writableRoots` and `networkAccess`, or `readOnly`).
- `turn/start`, on every turn: `approvalPolicy: "never"` and `sandboxPolicy`: `{"type": "workspaceWrite", "writableRoots": [...], "networkAccess": <bool>}` or `{"type": "readOnly", "networkAccess": true}`. The thread value would be enough for the write levels, but `thread/start` `sandbox` is a plain mode with no network field, so the read-only level with network on needs the turn policy. Sending both on every turn also stops the sandbox from drifting.
- The temp folders (`/tmp` and `$TMPDIR`) are writable by default in `workspaceWrite`, and so is the worker's folder (`cwd`).
- The daemon still declines any approval request that arrives. It should not happen now.

### What a commit in a worktree writes

A linked worktree keeps its objects, refs and logs in the main repository's git dir, and its index and HEAD in `<git dir>/worktrees/<name>`. Codex keeps any `.git` folder read-only, even inside a writable root, and making the whole git dir a root does not help (checked: `git commit` still fails with "Read-only file system"). So the writable roots are the narrow folders:

- `<git dir>/worktrees/<name>` (what `git rev-parse --git-dir` says in the worktree)
- `<common git dir>/objects`, `refs` and `logs`

With these roots a commit works in a worktree (checked with a real Codex sandbox through `command/exec`), and writes to `config`, `hooks` and anything outside stay refused. That is better than the whole git dir: a worker cannot plant a hook or change the config of the main repository, which would run outside the sandbox the next time the owner uses git. The cost: `git gc`, `git pack-refs` and a branch rename fail, and a worker with `--isolation none` (working in the owner's own checkout) cannot commit, because its index is directly in `.git`. The default is a worktree.

## Codex workers do not use agent-net tools

- The brief for a Codex worker (`WorkerPreambleCodex`) does not name `agent_handback` or `agent_reply`: the end of the turn is the report, and the daemon hands it back. It was already like this; a test now holds it.
- The agent-net MCP server is turned off in the worker's private app server: `-c mcp_servers.agent-net.enabled=false` on the `codex app-server` command line (checked: the server is not started, other servers are). Other MCP servers stay. Codex stops with "invalid transport" when that key names a server that the config does not define, so the daemon adds the flag only when `$CODEX_HOME/config.toml` (default `~/.codex`) has a `[mcp_servers.agent-net]` table. A definition only in a project's `.codex/config.toml` is not seen, and the tools stay loaded there.

## The other harnesses

Claude Code, OpenCode and Pi workers are **not changed** by this decision. They read the level and record it on the worker (shown in the TUI as not enforced yet), so the follow-up can apply it per harness: Claude Code with its permission mode and settings, OpenCode with its permission config, Pi (which has no permission prompts) by what its extension allows. Until then their behavior is the one of 0002.

## Alternatives not taken

- **Approve for the worker.** A worker nobody watches would approve its own requests. No.
- **`danger-full-access` or approval policy `never` with no sandbox.** Fast, and it removes the one protection the owner has against a worker that reads a hostile page or file.
- **Trust the worktree in the user's Codex config.** It writes to the user's `~/.codex`, one entry per worktree, and trust is not a sandbox.
- **The whole git dir as a writable root.** Does not work (`.git` stays read-only), and would allow hooks if it did.
- **A sandbox level per worker or per start.** The caller is an agent. The owner sets limits, not the agent.

## Checked and not checked

Checked: the schema fields above; `thread/start` with these params on a real `codex app-server` (temporary `CODEX_HOME`, no model call), including the effective sandbox in its answer; `command/exec` under the same writable roots: a commit in a worktree works, a write to `config`, `hooks` or outside the roots fails, a write under `readOnly` fails; the `-c` override with and without the server defined.

Not checked: a full worker run with a model (no paid calls). In particular that `turn/start` `sandboxPolicy` `readOnly` with `networkAccess: true` gives network to the read-only level (the thread-level read-only mode has network off); that a model's own commands run under the same policy as `command/exec`.
