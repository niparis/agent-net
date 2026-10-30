# Reference

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
| `agent-net list` | Agents on the network, with their PROJECT (found from the agent's folder). `--all` includes offline ones. `--dormant` includes dormant ones (reachable, but quiet for 24 hours; OpenCode only). `--json` shows them always. `--harness`, `--text` filter. `--wide` adds branch, remote, folder (the session's folder), node and worktree. MODE (`foreground`, `background`, `unknown`, or `-` from an older machine) and MODEL show how the session runs. TRUST is `yes` for a session with [peer trust](#peer-trust-and-hints) on. POLICY is the [delegation policy](#delegation-policy) (`native:deepseek/deepseek-v4.1-flash(project)`), or `-`. `--wide` adds EXPERTISE. `--json`. |
| `agent-net nodes` | Machines with their agent-net version and installed harnesses (HARNESSES column). `--wide` lists each harness with version, path and model count, and one extra row for every other copy of a harness (marked "other copy"); the primary copy is marked when there is more than one. `--json` has everything, including the model lists. |
| `agent-net nodes rename NODE [NAME]` | Give a machine a display name that every machine shows, or clear it (no `NAME`). `NODE` is a node ID or a name the machine is listed under. Same as the `N` key in the TUI. See [machine names](#machine-names). |
| `agent-net projects` | Known projects, one per repository per machine: name, machine, path (the main working tree), git remote, SOURCE (`registered`, `seen` or `both`), which harnesses ran there, the linked worktrees where sessions ran, online sessions and last seen. `--name N` shows one project. `--json`. See [the project registry](#the-project-registry). |
| `agent-net projects add PATH...` | Register repositories on this machine. `--dry-run` shows what would happen. `--json`. |
| `agent-net projects scan DIR` | Register every repository under a folder on this machine. `--depth N` (default 3, at most 8), `--dry-run`, `--json`. |
| `agent-net projects remove NAME\|PATH` | Remove a registration. A name that fits two projects on this machine is refused: give the path. `--on MACHINE` removes the entry of another machine (host name or node ID), for example one that is gone. |
| `agent-net events` | On the gateway machine: the gateway's events table, newest last. `--node` (ID or host name), `--session`, `--kind` (exact, or a group like `msg`), `--since` (like `2h` or `2026-10-29T12:00:00Z`), `--limit N` (default 100), `--follow` (or `-f`), `--json`, `--data DIR`. |
| `agent-net tui` | A live terminal view of every session on the network, with three actions: message a session as yourself, start a worker, stop a worker. It talks to the local daemon through the operator interface, so it runs on any machine. See [the screen](#the-tui-screen). |
| `agent-net start --project NAME [--harness opencode|claude|pi|codex] [--model M] [--machine HOST] [--isolation worktree|none] "TASK"` | Start a worker: a new agent session in a known project, on the machine that holds it. It prints the worker's session ID. The task is sent to it as a message from you; its report comes back to you (see the TUI, or `agent-net messages`). In a git repository the worker gets its own git worktree on the branch `agent-net/<worker-id>` by default; `--isolation none` works in the project folder. `--model` takes a loose name such as `"deepseek 4.1"`. See [workers](#workers). |
| `agent-net stop SESSION` | Stop a worker. The process group is killed. |
| `agent-net workers` | The workers on the network, with machine, project, harness, model, owner, state and start time. `--json` also has the task's first line and the usage figures (`context_tokens`, `context_limit`, `tokens`). `--owner-claude UUID` keeps only the workers owned by this machine's Claude Code session with that native id (the transcript file's name). `--recent` also lists workers that ended in the last two minutes, with `ended_at`, `end_status` and `end_note`; without it the list and the table are unchanged. |
| `agent-net send SESSION TEXT...` | Send a message as yourself (the owner of this machine). `SESSION` is a session ID or a unique agent name. |
| `agent-net messages` | Recent messages and their states. `--session ID`, `--limit N` (default 20), `--json`. |
| `agent-net hint` | Not for you to run: the Claude Code hook that [setup can add](#what-setup-changes-on-your-machine). It reads the hook input on stdin and prints hook JSON only when it has a suggestion. See [peer trust and hints](#peer-trust-and-hints). |
| `agent-net policy-hook` | Not for you to run: the Claude Code `UserPromptSubmit` hook that [setup can add](#what-setup-changes-on-your-machine). It reads the hook input on stdin and prints hook JSON only when you set a delegation policy for the session. See [delegation policy](#delegation-policy). |
| `agent-net version` | Print the version. |

## Ports

| Port | Used by |
|---|---|
| TCP 7420 | The gateway. HTTPS and WebSocket, on `0.0.0.0` by default. Every daemon connects to it. Open it to your other machines and to nothing else. |

The daemon and the agents talk over Unix sockets, not network ports.

## Files

| What | Default path | Notes |
|---|---|---|
| Node credentials | `~/.config/agent-net/node.json` | Gateway URL, node ID, token, CA fingerprint. Mode 0600. Optional `"hostname"` pins the machine name (see below). |
| Pinned CA | `~/.config/agent-net/ca.pem` | The gateway's CA, trusted after the fingerprint check. |
| Daemon state | `~/.local/state/agent-net/daemon/daemon.db` | SQLite. Delivered message IDs (for duplicates), the outbox for sends made while the gateway is away, and the `workers` table (process ID, start time, owner, worktree, task line) that lets the daemon take its workers back after a restart. Mode 0600, like its `-wal` and `-shm` files. |
| Worker folders | `~/.local/state/agent-net/daemon/workers/<worker id>/` | The worker's private files: for OpenCode its state folder and session file, for Pi the named pipe used as its standard input, for Codex the capability token of its app server (mode 0600). Also the output log of the harness. Deleted when the worker ends. |
| Worker worktrees | `~/.local/state/agent-net/daemon/worktrees/<worker id>/` | The default in a git repository (not with `--isolation none`). Kept when the worker changed something, removed when not. The branch is `agent-net/<worker id>` in the project. |
| Daemon socket | `$XDG_RUNTIME_DIR/agent-net/daemon.sock`, else `/tmp/agent-net-<uid>/daemon.sock` | Directory 0700, socket 0600. macOS has no `XDG_RUNTIME_DIR`, so it uses the `/tmp` path. |
| Claude hint hook record | `~/.config/agent-net/claude-hint-hook.json` | Whether you were asked, and whether agent-net added the hook (and to which settings file). |
| Reply sockets | `<socket directory>/r/<session id>.sock` | One per sending session, used to catch Claude Code's native replies. |
| Gateway data | `~/.local/state/agent-net/gateway/` | `ca.pem`, `ca.key`, `server-key.pem`, `gateway.db` (SQLite, with `-wal` and `-shm` files). **Back this folder up.** Without it every machine has to join again. See [security.md](security.md#what-the-gateway-keeps). |
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
| `session.new` | The first time the gateway sees a session. | `session`, `node`, `host`, `harness`, `name`, `pid`, `cwd`, `project`, `git_root`, `worktree`, `remote`, `branch` |
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
| `daemon` | `--gateway URL` | from `node.json` |
| | `--config-dir DIR` | `~/.config/agent-net` |
| `setup` | `--join STRING` | none. A join string from `invite` or `enroll`. |
| | `--no-launch` | off. This machine refuses workers. Saved as `no_launch` in `node.json`. It stays until `--launch`. |
| | `--launch` | off. Accept workers again. |
| | `--gateway` | off. Also install and start the gateway here. |
| | `--no-service` | off |
| | `--accept-peer-messages` | off. Set Claude Code `crossSessionInbound` to `accept` without asking, if it is not set. |
| | `--no-accept-peer-messages` | off. Do not set it and do not ask. |
| | `--claude-hint-hook` | off. Add the Claude Code hook that suggests which session to ask after a failed tool call, without asking. |
| | `--no-claude-hint-hook` | off. Do not add it and do not ask. |
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

`install.sh` takes `--gateway`, `--client`, `--binary-only`, `--accept-peer-messages`, `--no-accept-peer-messages` and `--version vX.Y.Z`. The last two peer-message flags are passed to `setup`. See [setup.md](setup.md).

## Machine names

The name of a machine is the host name its daemon reports in `hello`. On macOS that can be long (`Johns-MacBook-Pro-950`). To get a short name, give the machine a display name once, from any machine: press `N` in the TUI, or run `agent-net nodes rename NODE NAME`. Nothing needs to be done on the machine itself. The gateway keeps the display name in its database, apart from the reported host name, and a new `hello` does not change it. An empty name clears it, and the machine is listed under the reported host name again.

- **Where it shows.** Wherever the gateway gives out the name of a machine: `list` and `nodes` (the HOST column and the `hostname` field; `nodes --json` also has `display_name` and `reported_hostname`), the TUI, worker start results and notices, the `hostname` of hints, enter plans, the events and the gateway log. Every daemon also learns its own display name at `hello` (and at once when it is renamed), so a machine uses the same name for itself.
- **The rules.** 1 to 63 characters: letters, digits, `-`, `_` and `.`, starting with a letter or digit. Letter case is kept, but two machines cannot have names that differ only in case, and a name cannot be the name another machine is listed under.
- **The operator method** is `node_rename` (`{"node_id": "...", "name": "laptop-1"}`, an empty `name` clears it). It answers `{"node_id", "hostname", "display_name", "reported_hostname"}`. It is served only to the owner's command line or TUI, like `session_policy`, and refused for an agent client and for a command line inside an agent session's process tree. No MCP, Pi or OpenCode tool sets it.
- **`enter_ssh_hosts`.** Its keys are the names the gateway lists, so after a rename use the display name as the key (`{"laptop-1": "me@laptop-1.example"}`). Matching is exact. The name is also the ssh target when the entry is missing, which is why the rules above are those of a host name.

## What each machine reports

Each daemon sends two kinds of facts about its machine to the gateway. Agents and the CLI can read them.

**Repo info for each session.** When the daemon finds a session (any harness), it runs `git` in the session's folder (with a 3 second limit for all commands) and sends:

- `root`: the main working tree of the repository. Empty if the folder is not in a git repository. A linked git worktree has the root of the main tree it belongs to, found with `git rev-parse --git-common-dir`.
- `worktree`: the working tree the session's folder is in. It is the same as `root`, except in a linked git worktree.
- `project`: the base name of `root`, or of the folder if there is no git repository. So a worktree is not a project of its own: the sessions of `tenebrae` and of its worktrees `agent-a76c...` are all in project `tenebrae`.
- The default name of an OpenCode, Pi or Codex session is the project too, so a session in a worktree is not named after the worktree folder. A name of the form `<folder>: <title>` from a plugin has its folder part swapped for the project. A name you chose is kept.
- `remote`: the `origin` URL. Passwords and tokens are removed. For `http`, `https`, `ftp` and `git` URLs the whole user part goes, because a token is often the user name. For `ssh` URLs only the password goes. A query or fragment is removed. `git@host:owner/repo` is kept as it is.
- `branch`: the current branch, or `detached@<short hash>`.
- `description`: the one-line description of the project, read from `AGENTS.md` (else `CLAUDE.md`) in the main working tree, or in the session's folder when there is no repository. See "FUNCTION and the task" below.

**FUNCTION and the task.** FUNCTION in `agent-net list` and the `function` field of an agent's profile is the project description above. In the TUI it is on the detail pane of a session, not in the main table, where TASK has its place. The model does not write it, and `agent_announce` cannot set it. With no `AGENTS.md` or `CLAUDE.md`, or no description line in it, FUNCTION is empty: `agent-net list` shows `-`. The daemon re-checks the file (one `stat`) at every update of the session, so a change shows within moments.

The description is found like this (code: `internal/repoinfo/description.go`). Details and reasons: [decision 0009](decisions/0009-function-from-agents-md.md).

1. Start at the first `# Project: <name>` heading (the standard template), else at the first `# ` heading, else at the top of the file. Ignore front matter and code blocks.
2. In the template form, take the first non-empty line after the heading. Otherwise take the first plain paragraph. Stop at the next heading.
3. Skip lists, tables, quotes, HTML, rules, `@file` imports and notes to agents (a paragraph that starts like "Notes for coding agents ...").
4. Strip markdown, keep the first sentence without its final full stop, and cut to 120 characters.

What the agent writes is the **task**. At the start of every session, and when the task changes, the agent must call `agent_announce` with its task in one short line (the guidance, the MCP server instructions, the tool description and the Pi and OpenCode system prompts all say so). `name` renames the session only when the agent passes it; `function` is ignored. Until a session has announced, every agent-net tool result ends with "Reminder: call agent_announce with your current task." The task shows in `agent_list`, `agent_get`, the TUI and as TASK in `agent-net list --wide`.

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

**Installed harnesses.** At start and then every 6 hours the daemon looks for `claude`, `opencode`, `pi` and `codex`. It reports the path and the version (the first line of `<program> --version`, 5 second limit) of each one it finds. For OpenCode it also runs `opencode models` (20 second limit), which prints one `provider/model` per line, and reports that list. No other harness has such a command in use, so only OpenCode reports models.

A daemon started by launchd or systemd has a short `PATH`, and it does not read `~/.bashrc` or `~/.zshrc`. So it searches, in this order: its own `PATH`, then `~/.local/bin`, `~/.bun/bin`, `~/.npm-global/bin`, `~/.opencode/bin`, the folders of Node version managers, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin`, `/bin`, and last the `PATH` of your login shell (`$SHELL -l -c`, or `/bin/zsh` on macOS if `$SHELL` is not set, 3 second limit). The login shell is never interactive: it reads your login startup files, not the interactive ones. The programs it runs get the search path as their `PATH`, so a script that starts with `#!/usr/bin/env node` works.

The version manager folders are: nvm (`$NVM_DIR` if set, and `~/.nvm/versions/node/*/bin`), fnm (`$FNM_DIR`, `~/.local/share/fnm`, `~/.fnm`, `~/Library/Application Support/fnm`, each with `node-versions/*/installation/bin`), volta (`~/.volta/bin`), asdf and mise shims (`~/.asdf/shims`, `~/.local/share/mise/shims`, `~/.mise/shims`), pnpm global (`$PNPM_HOME`, `~/.local/share/pnpm`, `~/Library/pnpm`) and bun (`~/.bun/bin`). With several Node versions, the one named by nvm's default alias (`~/.nvm/alias/default`) or fnm's default alias comes first. If the alias does not resolve, the newest version comes first.

A harness can be installed more than once, for example OpenCode v1 from its install script and v2 from npm. The daemon finds every copy and drops copies that are the same file (same real path after symlinks). It reports one entry per harness. The `path`, `version` and `models` fields describe the primary copy. This is the copy that comes first in your login shell's `PATH`, and then `primary` is `true`. If the login shell does not find the harness, the primary copy is the first one in the search order and `primary` is absent: the daemon cannot tell which one you run. The other copies are in `others`, each with `path` and `version`. Models are listed only for the primary copy. Both fields are new after v0.3.5 and are left out when empty, so v0.3.5 daemons, gateways and clients still work with them.

`agent-net nodes` shows the result. A machine on an older agent-net shows `unknown`. A machine that reports an empty list shows `none`. Agents get the same data from the `agent_nodes` tool (Claude Code and Pi).

## CPU and memory

Each daemon samples its machine every 5 seconds, keeps 5 minutes, and reports a summary to the gateway every 10 seconds. The gateway keeps the newest report per machine in memory only (it is gone after a gateway restart and back within 10 seconds).

- **Machine:** CPU as a percentage of all cores (0 to 100), memory used and total, load average (1, 5, 15 minutes), process count, each with the 5 minute average.
- **Session:** the process tree under the session's process: CPU as a percentage of one core (100 is one full core, a tree can show more), the sum of resident memory, the number of processes, each with the 5 minute average. A tree seen only once has no CPU figure yet (`...`).
- **Which process:** Claude Code, the session's PID. Pi, the process that connects the extension. OpenCode, the one service process that hosts all sessions: it is listed once as "shared by N sessions" and is never split. If one session starts another (a Claude Code session inside another's shell), the inner tree is also counted in the outer one.
- **Linux:** `/proc/<pid>/stat` (user, system and waited-for children time) and `/proc/stat`. CPU is the difference between two readings. The sampler costs about 5 ms per reading with 450 processes, about 0.1% of one core.
- **macOS:** `ps -A -o pid=,ppid=,rss=,time=,comm=` for processes, `sysctl` for cores, memory size and load, `vm_stat` for memory in use (app + wired + compressed, like Activity Monitor). Load and memory are read every 4th sample. CPU time from `ps` has a resolution of 10 ms and misses processes that start and end between two readings, so tree CPU can read low for a tree that spawns many short jobs. Machine CPU is the sum of live processes' CPU time over the cores, with the same limit. This was written and tested against fixtures of the `ps`, `sysctl` and `vm_stat` output; it has not been run on a Mac.
- **Top lists:** only on request, not in the report. Each process has its PID, short name, and the first 80 characters of its command line with tokens, `--password=`/`--token` values and `KEY=value` secrets masked.
- **Daemon methods** (owner only, from `hello` with `client: cli`): `usage` (this machine live, and all machines from the gateway), `usage_top {session_id}`, `session_text {session_id, limit, before, after, sidechain}`, `session_enter {session_id, tmux_socket, herdr_socket}` (answers `{argv, dir, env, host, note}`, see [Enter a session](#enter-a-session)), `counts`. Gateway frames: `node.usage`, `query.usage`, `query.counts`, `query.usage_top`, `query.session_text`, `query.session_enter` (and `node.usage_top`, `node.session_text`, `node.session_enter` to the machine that holds the session).
- **Counts:** `counts` returns `online`, `offline_24h` and `offline_total`. The gateway keeps offline sessions for 30 days, so a header that says "134 sessions, 12 online" is mostly old restarts. Show "12 online, 5 offline in the last 24 h".

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

## Workers

A worker is an agent session that agent-net starts for you, with nobody watching it. See [decision 0002](decisions/0002-workers.md) for the rules and [0008](decisions/0008-operator-interface-and-worker-build.md) for how they are built.

- **Who starts one:** you (`agent-net start`, the TUI) or an agent (`agent_start`). A worker cannot start workers. The harness is `opencode` (the default), `claude`, `pi` or `codex`. A caller can say "implement this with a Claude, Pi or Codex subagent".
- **Where:** the project is looked up by name in `agent-net projects` (registered or seen). If it is on two machines, name one with `--machine`. The machine must be online, accept workers (not set up with `--no-launch`), and have the harness (`agent-net nodes --wide`). Only OpenCode reports a model list, and the gateway checks the model against it. For the other harnesses any model string is passed on, and a model the harness does not know fails at start with the harness's own message. The gateway says which check failed.
- **How, per harness.** All run in the project folder (or the worker's worktree), and none gets a flag that turns permission checks off: a worker uses the project's own permission settings.

| Harness | Command | The model | The task and the report | Enter it later |
|---|---|---|---|---|
| OpenCode | `opencode serve` on `127.0.0.1`, random password | `provider/model`, checked against the machine's list | A normal message. The worker ends with `agent_handback`; the owner's next message starts the same session again. | `opencode --session <id>` |
| Claude Code | `claude --bg --name worker-<project>-<id> --settings <json> --mcp-config <json>` (a background session of Claude's own service, not a child of the daemon) | An alias (`haiku`, `sonnet`, `opus`) or a full name, after `--model` | A normal message, delivered to the session's inbox. The worker ends with `agent_handback`, through the agent-net MCP server of this daemon's binary; the owner's next message runs `claude --bg --resume <session-id>`. | `claude attach <id>` (the id is the `jobId` in the session file) |
| Pi (needs `--isolation worktree`, or the daemon refuses it) | `pi --mode rpc --session-id <uuid> --name "worker <project>"`, standard input a named pipe that never closes | `provider/id[:thinking]`, after `--model` | A normal message, through the Pi extension, which loads in this mode. The worker ends with `agent_handback`, and it cannot be started again (Pi's rpc mode cannot open an existing session). | Not possible: Pi's rpc mode has no screen. Read it with `agent-net show`, or resume its session file in Pi after the worker ended. |
| Codex | `codex app-server --listen ws://127.0.0.1:<port> --ws-auth capability-token --ws-token-file <file>` | A name, in `thread/start` | The daemon runs each message from the owner or the person as a turn. The last message of the turn is the report, handed back by the daemon; the owner's next message starts a new app server and `thread/resume`s it. | `codex resume <thread id>` in the project folder |

  Claude Code needs the folder to be trusted (run `claude` in it once, or use a worktree of a trusted repository). The Pi extension and the OpenCode plugin must be installed on the machine (`agent-net setup`). The Claude session carries `crossSessionInbound: "accept"` and `AGENT_NET_SOCKET` in its per-session settings (`--settings`), so inbound agent-net messages are accepted; your `settings.json` is not changed.
- **The task** is sent as a message from the caller, after the fixed brief. For Claude Code, Pi and OpenCode the worker ends its work with `agent_handback(report)`. For Codex the daemon does it when a turn ends: status `done` for a finished turn, `failed` for a failed or interrupted one, and `blocked` when Codex asked for an approval (the daemon declines every approval, and the report names the command, the reason and the Codex thread to open). The caller gets one message with the facts (status, run time, model, tokens, the context at the end, the cost if the harness reports it, the worktree and branch, `git diff --stat`) and the report below them. Tokens come from OpenCode, from the Claude transcript, from Codex, and from Pi's rpc statistics. Cost comes from OpenCode only. The context at the end is the tokens of the last assistant message or turn (input plus cache reads and cache writes) and the model's context window. A worker that ends without a hand-back sends the same notice with the status `timed out`, `failed` or `stopped`. A worker that nobody messages for the idle time (60 minutes by default) ends as `timed out`; one that runs longer than 4 hours ends as `timed out` too. An OpenCode worker whose model call fails (a provider, billing or auth error) ends at once as `failed`, and the note holds the error text (one line, secrets masked, cut to 300 characters). The daemon looks at the OpenCode session every few seconds: a run with outcome `failed` ends the worker. Codex reports a failed turn as a hand-back. Claude Code and Pi workers have no such check yet: a model error there shows only as the idle time-out.
- **Follow-up messages.** A worker ends when it hands back. A message from its owner to the same session ID starts it again on the same harness session, with the same model, project and worktree, and delivers the message. That is `agent_handback` for OpenCode, Claude Code and Pi, and the end of a turn for Codex. OpenCode, Claude Code and Codex can open the saved session again (a new private server on the same folders, `claude --bg --resume <id>`, or `thread/resume`). A Pi worker cannot be started again: Pi's rpc mode cannot open an existing session, and the refusal says so; read the old session with `agent-net show`, or open its session file in Pi. Only the owner can start a worker again; another sender gets a clear refusal. A Codex worker takes messages from its owner and from the person at the machine only: the daemon turns each into a turn, one after the other, and refuses other agents.
- **Ending:** a hand-back (any status) ends the worker right after the notice, like a Claude Code subagent. The idle time and the longest run time are safety nets: no message to or from it for 60 minutes; 4 hours since the start; its owner ended or stayed offline for 10 minutes; `agent_stop` or `agent-net stop`. For Claude Code the daemon runs `claude stop <id>` first, then kills the process group if the process is still there. The stopped session stays in `claude agents`, with its conversation, until you `claude rm <id>`.
- **The worker list.** `agent-net workers` and the TUI show only workers that run. A worker that ended leaves the list, and its record is kept (ended) until the longest run time: a hand-back record is what starts the session again, and every ended record also tells the daemon that the harness session is a worker's, so the plugin of another OpenCode server may not register it as a normal session. A record that is offline and started longer ago than `max_age` is cleaned: on the machine at daemon start and every 10 minutes, and on the gateway (`worker.cleanup`), so an old worker does not stay listed for weeks. A worker that the machine says it does not run leaves the list at once.
- **Context and tokens.** Each worker carries `context_tokens` (what the model saw on its last step: input plus cache reads and cache writes), `context_limit` (the model's context window) and `tokens` (input plus output plus reasoning for the run, without cache reads). The daemon refreshes them on its worker tick while the worker runs, and with the final notice. OpenCode reads the newest assistant message and `limit.context` from `/api/model` on the worker's private server. Claude Code reads the last assistant usage in the transcript and takes the limit from a small table by model family (the 5 series, Opus 4.7 and later, Fable 5 and Mythos 5 have 1M; other Claude models have 200k). Codex reads `last` and `modelContextWindow` from `thread/tokenUsage/updated`. Pi sends `get_session_stats` to its rpc mode and reads the tokens and `contextUsage`. The figures are in `agent-net workers --json` (also with `--recent`), on the worker's TUI detail pane as `context 84k / 1M (8%)  tokens 345k`, and on the Claude Code band after the run time as `ctx 84k/1M 8%` (the percentage is yellow at 70 percent and over, red at 90 percent and over). A harness that has not reported a figure leaves it out.
- **Defaults for a short request (W17).** "Use agent-net to implement X with a new OpenCode worker, use deepseek 4.1" is enough.
  - **Worktree:** when `isolation` is empty and the project is a git repository, the worker works in its own worktree. `isolation: none` (`--isolation none`) opts out, for read-only or non-code tasks. A folder that is not a repository gets no worktree. Pi always needs one.
  - **Project:** an agent that leaves `project` empty gets the project of its own session. If the project is on several machines, the caller's machine wins when it has the harness, else the one machine that has it. The command line must name a project.
  - **Model:** a loose name is resolved against the model list of the machine that runs the worker (OpenCode has one; Claude aliases and harnesses with no list pass through). An exact id wins. Else the name is split into words (case does not matter; `4.1` matches `v4.1`) and every model id that contains all words is a candidate. Candidates are ranked by: no worker failure in the last 24 hours (a model that failed a worker with a model error goes last, and the other models of its provider go after the other providers; an exact id still wins); sessions on that machine that used the model in the last 30 days; sessions that used any model of the same provider in that time; the order of `model_providers` in `node.json`; not `-free`; not a dated snapshot (`:0813`); the shortest name. The usage counts are all sessions of that harness on that machine that the gateway saw in the last 30 days, the user's own sessions included, not only workers. If one candidate is strictly best, it is used and the result says `model: deepseek 4.1 -> opencode-go/deepseek-v4.1-flash`. When a matching model was ranked last for a recent failure, the note says so. If the best are equal, the start fails and lists up to five.
  - **Checklist:** every worker notice ends with a line from agent-net: read the diff in the worktree, run the project's tests there, report what you checked, merge only if your user asked.
  - `model_providers` in `node.json` is a list of provider prefixes to prefer, first is best: `"model_providers": ["opencode-go", "opencode"]`. Restart the daemon after a change.
- **Limits:** 4 workers per machine. Settings are in `node.json`: `"workers": {"max": 4, "idle": "60m", "max_age": "4h", "owner_grace": "10m"}`. Restart the daemon after a change.
- **Owner:** `agent-net workers` and the TUI show who started each worker. A worker started from the command line or the TUI is owned by `you (on <host>)`.
- **Events:** `worker.started`, `worker.stopped` (with `reason=handback|idle|max_age|owner_gone|stopped|process_exited|lost|model_failed`) and `worker.refused`, in the gateway log, the events table and the TUI feed. `worker.cleaned` is in the gateway log.

**The operator interface.** The daemon's local socket serves these methods to the command line and the TUI, and the phone app will use the same ones: `info`, `nodes`, `list`, `projects`, `events`, `message_status`, `workers` (reads) and `owner_send`, `worker_start`, `worker_stop` (actions). The types are in `internal/protocol/operator.go`; `worker_handback` is the worker's own call. Agent connections may use `worker_start`, `worker_stop`, `worker_handback` and `workers`. `owner_send` is for the command line only.

## The TUI screen

`agent-net tui` needs a terminal. It talks to the local daemon through the operator interface, as an owner's CLI (`hello` with `client: cli`). Besides the reads and the actions below it uses the owner-only methods `usage`, `counts`, `usage_top`, `session_text` and `session_enter` (see [CPU and memory](#cpu-and-memory) and [security.md](security.md#session-text-and-resource-figures)). It shows, from top to bottom:

- **Header.** `12 online · 5 offline today`, from `counts`. The total of old offline sessions is left out. An older gateway has no counts: the header then says `N sessions, M online`. Also the active filters, when there are any (`filter: active, /cogforge`), and the time of the last refresh.
- **Machines,** one line each: online state, name, agent-net version, CPU now with a bar and the 5 minute average in brackets, memory used and total with a bar, 1 minute load, and the installed harnesses when the line has room. A machine with the user's shared OpenCode service shows a short dim note here (`opencode service 2.1G 9 procs`). This machine comes first and is marked `(here)`. The name is the gateway's name for the machine, and `(here)` is matched by node ID, not by hostname. A figure that is missing or older than 45 seconds reads `?`.
- **Sessions,** grouped by machine under a heading: NAME, AGENT (the harness), PROJECT, MODEL (shortened: no provider, no `claude-` prefix), STATUS (`idle`, `busy`, `shell`, `dorm` for dormant, `off` for offline), TRUST (`peer` when peer trust is on, a dim `-` otherwise; always shown), POLICY (only when some session has a [delegation policy](#delegation-policy): `own`, `sub` or `wrk`), CPU, RAM and TASK (the task the session announced; when it never announced, the harness title if it differs from NAME, else blank). Sessions stay in the order they were first seen, oldest first, so a new session appears at the bottom of its machine and nothing re-sorts a row. A session that is idle with no report for more than an hour is dim. Offline sessions are dimmed too. On a narrow terminal TASK, MODEL, PROJECT and POLICY go first; TRUST always stays. A tree with more than 20 processes, 200% CPU or 4G of memory shows its numbers in red. A process tree that several sessions share shows `shared` in their rows and has no row of its own. A worker's private OpenCode server is its own tree, so its CPU and memory are on the worker's row.
- **Selected session.** The first line has the name, harness, status and the session's CPU, RAM and process count. Below, on the left: session and worker IDs and PID, folder, branch, model and mode (these last two moved here from the table), function (the project description from `AGENTS.md`; "none: add a `# Project: <one line>` line to AGENTS.md" when there is none), remote, worktree (when the session runs in a linked worktree), the project source (`seen in a session`, `registered`, or `seen in a session and registered`), task, expertise, a `trust` line when peer trust is on, `policy` and `main` lines when it has a delegation policy (with a note for each part its harness cannot do), and its recent messages. For a worker it starts with who started it and then `context 84k / 1M (8%)  tokens 345k`, with only the parts the harness has reported. On the right: the heaviest 5 processes by CPU and by memory (PID, CPU, RAM, command line with secrets masked), fetched with `usage_top` when the session is selected and again every 5 seconds. On a small terminal the lists get as many rows as fit; `d` gives the pane the room of the message feed.
- **Workers** show in the sessions table right under the session that started them, indented with `├─` (or `└─` for the last one), in order of start time, like a sub-agent. A worker's NAME is the first line of its FIRST task, which the daemon records and the gateway passes in the agent list, so every machine shows the same title; a later request from its owner does not replace it. When that task is missing, the row says `worker <harness> <short worker id>`. The full worker ID is on the detail pane's `id` line. A worker on another machine than its caller still sits under the caller and has `@host` after its name. A worker whose caller is not on screen (you started it, or the caller is offline or hidden by the filter) stays in its own machine group with `↳` before the name. Selection keys follow the order on screen. The detail says the worker runs in the background. The title counts them.
- **Messages (live).** One line per message on the network: the hour and minute, the two session names, the type, the first line of the body when this machine has a session in the conversation, and one delivery symbol at the end (`…` pending, `✓` delivered, `↩` replied, `✗` failed or expired). A reply that directly follows the message it answers is indented with `↳`. Names are session names only; a short `@host` is added only when two sessions in view share a name. The body line is fetched once, in the background, through the same `message_status` call the conversation view uses, and cached by message ID. A message between two other machines shows the type and no body. New session lines are hidden by default, and worker lines (started, stopped or refused) are dim; `l` shows every lifecycle line, then hides all of them, then returns to the default. Machines connecting or disconnecting and refused sends also have a line. Messages from you show as `you`.
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
| `a` | Active only: hide sessions that are idle and have had no report for more than an hour. Each machine heading says how many it hides, and the header lists the active filters. |
| tab | Move the focus between the sessions and the messages. |
| `d` | Give the detail pane the room of the message feed, or give it back. |
| `?` | Show or hide the help. |
| esc | Leave the conversation, the help or a message body, or clear the filter. |
| In the conversation: up, down, page up, page down, `g`, `G`, `f` | Scroll; `g` goes to the oldest loaded entry and loads older ones; `G`, end or `f` follow new entries. |
| `r` | Refresh now. |
| `m` | Send a message to the selected session as yourself. Type, Enter sends, Esc cancels. The reply shows in the messages. |
| `s` | Start a worker. A form takes Project, Harness, Model and Task. Project completes from the project list (a name on two machines is offered as `name@machine`), Model from the model list of that project's machine. Tab completes, then moves on; up and down or Enter move between fields; Enter on the Task starts it; Esc cancels. If the selected session's project is known, the form starts on the Task with it filled in. |
| `x` | Stop the selected worker, after a `y/n` question. Any other session is refused with a message. |
| `t` | Turn [peer trust](#peer-trust-and-hints) on or off for the selected session. Turning it on asks `y/n`; turning it off is immediate. |
| `N` | Name the machine of the selected session ([machine names](#machine-names)). A one-line prompt starts with the name shown now. Enter saves, an empty name clears it, Esc cancels. Every machine shows the new name. |
| `p` | Set the [delegation policy](#delegation-policy) of the selected session or its project. A form takes Delegate (left and right: none, off, native, workers), Coder, Main and Scope (session, project). Each move to another field shows what the loose model names resolve to; Enter on Scope saves; Esc cancels. |
| `e` | Enter the selected session: the TUI hands the terminal to it and comes back when you leave. See [Enter a session](#enter-a-session). |
| `q`, ctrl+c | Quit. |

A message body, including the first line the feed quotes, can only be shown for a message that involves a session on this machine. The gateway refuses it to other machines. The feed asks for that one body once, keeps the first line, and does not ask again. Text from the network is stripped of control characters before it is drawn, so an agent name or a message cannot move the cursor or change the screen.

**Event feed.** The screen reads the gateway's events through the local daemon method `events` (`{"after_id": N, "limit": N}`) and the gateway frame `query.events`. Any enrolled machine can ask. It returns the events of kinds `msg.*`, `session.*`, `node.*` and `worker.*`, oldest first, as metadata: the event kind and time, the machine, the session, the message ID, and for messages the two session IDs, the type and the ID of the message it answers. Never a message body. Machine addresses are left out, details are cut to 200 characters, and MCP, enrollment and retention events are not included. With `after_id` 0 you get the newest `limit` events (default 100, at most 500). An older daemon or gateway answers that the method is unknown, and the screen then shows the sessions without the feed.

### Enter a session

`e` in the TUI asks the daemon (`session_enter`) how to take over the terminal for the selected session, runs the answer, and draws the TUI again when you leave. The daemon of the machine that holds the session works out the command. The TUI builds none. Your own input in the session counts as yours, so approvals you give are yours.

| Session | What `e` runs |
|---|---|
| Claude Code background session (`kind` bg, which includes Claude workers) | `claude attach <id>`. The left arrow returns to the agent view, ctrl+z drops to the shell, the session keeps running. |
| Claude Code interactive session | Cannot be attached twice. If its process runs in a herdr pane, `herdr agent attach <pane>` (see below). Else, if its process runs in a tmux pane, the TUI switches its tmux client to that pane (when the TUI is in the same tmux server) or attaches a new client at that pane. Otherwise: "open its window on <machine>". |
| Pi | The same herdr and tmux rules. A Pi worker has no screen, so there is nothing to enter. |
| OpenCode | `opencode --session <id>`, in the session's folder, through OpenCode's own background service. |
| OpenCode worker | `opencode --server <its private url> --session <id>`, with its password in `OPENCODE_SERVER_PASSWORD`. Only from the machine that runs it. |
| Codex | `codex resume <thread>`, in the thread's folder. It joins the shared Codex app server, so you get the thread that is open there. With a `CODEX_HOME` other than `~/.codex` the plan is `env CODEX_HOME=<it> codex resume <thread>`. |
| Codex worker | `codex resume <thread> --remote ws://<its private address> --remote-auth-token-env AGENT_NET_CODEX_TOKEN`. Only from the machine that runs it. The daemon drives the same thread, so do not type while it works. |

**herdr.** A session whose process (or a parent) has `HERDR_ENV=1` and `HERDR_PANE_ID` in its environment is entered with `herdr agent attach <pane>`. The same plan is used when the TUI is outside herdr, inside the same herdr, or inside another one: herdr's `agent focus` does not move a client that is already open, so the TUI attaches instead. The daemon sends `HERDR_SOCKET_PATH` of the session's herdr in the command (`env -u HERDR_SESSION HERDR_SOCKET_PATH=<socket> herdr agent attach <pane>`), so it also works through `ssh -t`. If the pane moved or closed, the daemon looks for the pane that holds the process, and says so when none does. The herdr program is found from the pane's `HERDR_BIN_PATH`, `PATH`, then `~/.local/bin/herdr`.

- Press `ctrl+b q` to come back to the TUI. If the TUI itself runs in a herdr pane, press `ctrl+b ctrl+b q` (the first `ctrl+b` goes to the outer herdr). The agent keeps running.
- A pane takes one direct attach. If another `herdr agent attach` is open on the same pane, the new one fails with "already has an attached client". agent-net never uses `--takeover`: close the other attach first. Your normal herdr window is not affected.
- On macOS the session's environment is read with `sysctl kern.procargs2` for processes of your user. This also finds tmux servers other than the default one.

When the plan has nothing to run, the answer has only a note, and the TUI shows it in the result line.

**A session on another machine.** The question goes through the gateway to that machine. By default the TUI shows the command to run there. To let `e` run it through `ssh -t`, set in `node.json` on the machine where you use the TUI, and restart the daemon:

```json
"enter_ssh": true,
"enter_ssh_hosts": {"build-box": "me@build-box.example"}
```

`enter_ssh` is off by default. `enter_ssh_hosts` is optional: it maps the machine's name (as `agent-net nodes` shows it, so its [display name](#machine-names) when it has one) to the ssh target, which can be an alias from `~/.ssh/config`. Without an entry the name itself is used. The target must be a plain host name or `user@host`. ssh must already work without a prompt for anything but a key passphrase or a Tailscale SSH check. The secrets of a worker's private server never leave the machine: workers of OpenCode and Codex on another machine can only be entered from a TUI on that machine.

## Peer trust and hints

Decision: [0013](decisions/0013-peer-trust-and-hints.md). Security: [security.md](security.md#peer-trust).

**Peer trust.** A per-session setting, off by default. Turn it on or off with `t` in the TUI. The operator method is `session_trust` (`{"session_id": "...", "on": true}`), served only to the owner's command line or TUI (the owner gate of `session_text`). The daemon sends the gateway frame `session.trust`; the gateway stores the flag in `sessions.peer_trust` and logs `session.trust` (event and log line). The gateway puts `recipient_peer_trust` on the message it delivers, and the Claude Code adapter, the Codex adapter, the Pi extension and the OpenCode plugin write the note. `agent-net list` shows it in TRUST, `list --json` has `peer_trust`. A worker is not trusted unless you turn it on.

**Expertise.** `agent_announce` takes `expertise`, one short line. `agent_list` and `agent_get` show it, `list --json` has `profile.expertise`, `agent-net list --wide` has an EXPERTISE column, and the `text` filter searches it.

**Hints.** The local method `hint` takes `{"text": "...", "failed": true}` (and, from the command line, `harness` and `native_id` of the session) and answers `{"suggestions": [{"session_id", "name", "hostname", "why"}], "text": "<one line>"}`, or an empty object. The Claude Code hook (`agent-net hint`), the Pi extension and the OpenCode plugin call it after a failed tool call. How each shows the line: Claude Code adds it as `additionalContext` of the hook output, Pi appends it to the tool result, OpenCode adds it to the system prompt of the next request of the session. See the decision for the matching rules and the limits (at most 2 suggestions; the same suggestion once per 30 minutes per session; one hint per 45 seconds per session).

The Claude Code hook output, when there is a suggestion (one line of JSON on stdout, exit 0):

```
{"hookSpecificOutput":{"hookEventName":"PostToolUseFailure","additionalContext":"agent-net: another session may own or see this. ..."}}
```

## Delegation policy

Decision: [0014](decisions/0014-delegation-policy.md). Security: [security.md](security.md#delegation-policy).

A per-session or per-project setting that says who does the coding. Set it with `p` in the TUI. Values: `delegate` (`off`, `native`, `workers`), `coder_model` and `main_model` (loose names, resolved against the model list of the session's machine and harness), scope (`session`, `project`). The session's own value wins over its project's. The operator method is `session_policy` (`{"session_id": "...", "policy": {"delegate": "native", "coder_model": "deepseek 4.1", "main_model": "sol 6.1"}, "scope": "session", "dry_run": false}`), served only to the owner's command line or TUI. It answers `{"session_id", "saved", "effective"}`; `effective` is the policy the session ends up with, with `coder_resolved`, `main_resolved` and the errors of a name that did not resolve. A dry run saves nothing. An empty policy clears the value at that scope. `list --json` has `delegation` (`scope` says where the effective value comes from).

The gateway stores the policy in `sessions.delegation` and the project defaults in `project_policies` (migration 10), and logs a `session.policy` event and line when one is saved.

**What the session gets.** The local method `policy` (every turn; a session asks for its own, the command line asks by `harness` and `native_id`) answers `{"text", "mode", "agent_id", "coder", "main"}` or an empty object. The daemon asks the gateway (`policy.get`) and caches the answer for 2 seconds. `text` is one line that starts with `[agent-net]`:

- `native`: `[agent-net] Your user's standing policy for this session, set in agent-net: delegate all code changes to the `agent-net-coder-<model>` subagent (model <model>). You plan, write the brief, review its diff and run the tests. Do not edit code yourself unless the subagent fails twice.`
- `workers`: the same, but to start a worker with `agent_start` with the coder model.
- `off`: `... do the code changes yourself. Do not start subagents or workers for them.`

| Harness | Where the text goes | `native` | Main model |
|---|---|---|---|
| OpenCode | A system part on every model request of a root session (`context` hook). A subagent's own session gets none. | The plugin defines `agent-net-coder-<model>` (mode subagent, the resolved model) at run time. | `session.switchModel`, once per value. |
| Claude Code | The `UserPromptSubmit` hook (`agent-net policy-hook`), as `additionalContext`. Needs the hook: `agent-net setup --claude-policy-hook`. | `~/.claude/agents/agent-net-coder-<model>.md` with a `model:` line, written by the daemon when the hook asks. | Not possible from outside. |
| Pi | The system prompt, each turn (`before_agent_start`). | Pi has no subagents: the `workers` text is used. | `setModel`, once per value, for an exact `provider/id`. |
| Codex | Not applied. | | |

The Claude Code hook output (one line of JSON on stdout, exit 0), only when the session has a policy with text:

```
{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"[agent-net] Your user's standing policy ..."}}
```

## What setup changes on your machine

`agent-net setup` (run by the install line, by the invite line, or by hand) changes only the items below. Each step prints `ok`, `ok (unchanged)`, `updated` or `skipped (reason)`. The two service steps always restart the service, so they print `ok (restarted)` (unit file unchanged), `updated (restarted)` (unit file changed) or `ok (installed, started)` (new unit). Every step is safe to run again.

| Item | What setup does | What `agent-net uninstall` does |
|---|---|---|
| Binary `~/.local/bin/agent-net` | Put there by `install.sh` or the invite script, not by `setup` itself. | Keeps it. Delete it by hand. |
| Daemon service | Writes the systemd user unit (Linux) or launchd agent (macOS), enables it and restarts it. The unit has `KillMode=process` and the plist has `AbandonProcessGroup`, so a restart stops only the daemon and keeps the workers (the next daemon adopts them). Workers also run in their own session. A daemon that starts under systemd adds `KillMode=process` to an older unit and reloads systemd, without a restart. | Stops it and deletes the unit or plist. Then stops every worker that is still running (the `workers` step), because nothing else would. |
| Gateway service (only with `--gateway`) | Same, listening on `0.0.0.0:7420`. | Stops it and deletes the unit or plist. |
| Gateway binaries (only with `--gateway`) | Copies this binary into `~/.local/share/agent-net/dist`. | Keeps them. |
| Gateway data | Created by the gateway on first start. | Keeps it, even with `--purge` (it prints a reminder). Delete the directory by hand to start over. |
| Join | Writes `~/.config/agent-net/node.json` and `ca.pem`. Skipped if this machine already joined the same gateway. | Keeps them. `--purge` deletes `~/.config/agent-net`. |
| Daemon state | Created by the daemon. | Keeps it. `--purge` deletes `~/.local/state/agent-net/daemon`. |
| Claude Code | If `claude` is found, runs `claude mcp add -s user agent-net -- <binary> mcp`. This adds an entry to your user-level MCP servers in `~/.claude.json`. Skipped if the entry exists. | Runs `claude mcp remove -s user agent-net`. |
| Claude Code peer messages | If `claude` is found and `crossSessionInbound` is not set in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`), asks on the terminal, or follows `--accept-peer-messages` / `--no-accept-peer-messages`. On yes it adds `"crossSessionInbound": "accept"` and keeps every other key (file rewritten with 2-space indent, through a temp file and a rename). Records the answer in `~/.config/agent-net/claude-peer-messages.json`. An existing value is never changed. A file that is not valid JSON is not touched. The daemon and upgrades never change it. | Removes the key, only if agent-net set it and the value is still `"accept"`. Deletes the record. |
| Claude Code hint hook | If `claude` is found, asks once on the terminal (default No), or follows `--claude-hint-hook` / `--no-claude-hint-hook`. On yes it adds two entries to `hooks` in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`): `PostToolUseFailure` (all tools) and `PostToolUse` with matcher `Bash`, each running `<binary> hint` with a timeout of 2 seconds. Every other key and every other hook is kept. A later setup run updates the command if the binary moved. Records the answer in `~/.config/agent-net/claude-hint-hook.json`. A file that is not valid JSON is not touched. | Removes our two entries (and an empty `hooks` object), only if agent-net added them. Deletes the record. |
| Claude Code policy hook | If `claude` is found, asks once on the terminal (default No), or follows `--claude-policy-hook` / `--no-claude-policy-hook`. On yes it adds one entry to `hooks` in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`): `UserPromptSubmit`, running `<binary> policy-hook` with a timeout of 3 seconds. Every other key and hook is kept. A later setup run updates the command if the binary moved. Records the answer in `~/.config/agent-net/claude-policy-hook.json`; the daemon reads it to tell the gateway the hook is installed. When you set a `native` [delegation policy](#delegation-policy) for a Claude Code session, the daemon also writes `~/.claude/agents/agent-net-coder-<model>.md` (a marker comment inside; a file with that name that is not ours is left alone). | Removes our entry (and an empty `hooks` object), only if agent-net added it, deletes the record, and deletes every `agent-net-coder-*.md` file in `~/.claude/agents/` that has our marker. |
| Codex MCP server | If `codex` is found or `~/.codex` (`$CODEX_HOME`) exists, adds a block between `# agent-net:begin` and `# agent-net:end` at the end of `config.toml` in it: the server `agent-net` with `args = ["mcp", "--codex"]`. It asks once on the terminal whether Codex may call the agent-net tools without asking each time; yes adds `default_tools_approval_mode = "approve"` to that block (for this server only). `--accept-codex-tools` and `--no-accept-codex-tools` answer without a question; with no terminal the block has no approval line. The answer is kept in `~/.config/agent-net/codex-tools.json`. A `[mcp_servers.agent-net]` that is not ours is left alone. Codex threads started before this do not have the tools. | Removes the block and the record; the rest of `config.toml` is kept. |
| OpenCode plugin | If `~/.config/opencode` (or `$XDG_CONFIG_HOME/opencode`) exists, writes `plugins/agent-net.ts` in it. A symlink there is left alone. | Deletes the file. A symlink is left alone. |
| Pi extension | If `~/.pi` exists, writes `~/.pi/agent/extensions/agent-net.ts`. A symlink there is left alone. | Deletes the file. A symlink is left alone. |
| Guidance text | Adds a block between `<!-- agent-net:begin -->` and `<!-- agent-net:end -->` to `~/.claude/CLAUDE.md` (if `~/.claude` exists) `~/.pi/agent/AGENTS.md` (if `~/.pi` exists) `~/.config/opencode/AGENTS.md` (if `~/.config/opencode` exists) and `~/.codex/AGENTS.md` (if the Codex home exists). Text outside the markers is never changed. An older unmarked `## agent-net: working with other agents` section is replaced by the block. | Removes the block. |

The Pi extension, the OpenCode plugin and the guidance blocks are also written by the daemon, on every start, with the same rules as setup (the harness folder must exist, a symlink is left alone, guidance changes only between the markers). It rewrites a file only if it differs from the content embedded in the binary, and logs one line per file it changes (`agent files: pi extension updated`) in the daemon log. So these files follow the binary after an upgrade or a manual copy, without a setup run. See [upgrade.md](upgrade.md#agent-files-on-a-machine).

Setup does not use `sudo`. On Linux it may print a hint to run `sudo loginctl enable-linger <user>`, which keeps the user services running when you are logged out. You run that yourself.

Setup does not change any other Claude Code or Pi setting (the one exception is the peer-message setting above, and only with your yes), and it does not touch other MCP servers.
