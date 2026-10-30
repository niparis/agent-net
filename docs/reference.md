# Reference

Ports, files, environment variables, flags, and what setup changes on a machine. Paths are for the default user setup. `~` is your home directory.

## Commands

| Command | What it does |
|---|---|
| `agent-net gateway` | Run the gateway server. |
| `agent-net daemon` | Run the per-machine daemon. |
| `agent-net mcp` | Stdio MCP server for one Claude Code session. Claude Code starts it, you do not. |
| `agent-net setup` | Set this machine up. See [what setup changes](#what-setup-changes-on-your-machine). |
| `agent-net invite` | On the gateway machine: print the one-line installer for a new machine. |
| `agent-net enroll` | On the gateway machine: print a raw one-time join string. |
| `agent-net join STRING` | Join this machine to a gateway with a join string. |
| `agent-net uninstall` | Remove services and integrations. |
| `agent-net status` | Daemon connection, node ID and local sessions. `--json` for JSON. |
| `agent-net list` | Agents on the network, with their PROJECT (found from the agent's folder). `--all` includes offline ones. `--dormant` includes dormant ones (reachable, but quiet for 24 hours; OpenCode only). `--json` shows them always. `--harness`, `--text` filter. `--wide` adds branch, remote, folder (the session's folder), node and worktree. MODE (`foreground`, `background`, `unknown`, or `-` from an older machine) and MODEL show how the session runs. `--json`. |
| `agent-net nodes` | Machines with their agent-net version and installed harnesses (HARNESSES column). `--wide` lists each harness with version, path and model count, and one extra row for every other copy of a harness (marked "other copy"); the primary copy is marked when there is more than one. `--json` has everything, including the model lists. |
| `agent-net projects` | Known projects, one per repository per machine: name, machine, path (the main working tree), git remote, SOURCE (`registered`, `seen` or `both`), which harnesses ran there, the linked worktrees where sessions ran, online sessions and last seen. `--name N` shows one project. `--json`. See [the project registry](#the-project-registry). |
| `agent-net projects add PATH...` | Register repositories on this machine. `--dry-run` shows what would happen. `--json`. |
| `agent-net projects scan DIR` | Register every repository under a folder on this machine. `--depth N` (default 3, at most 8), `--dry-run`, `--json`. |
| `agent-net projects remove NAME\|PATH` | Remove a registration. A name that fits two projects on this machine is refused: give the path. `--on MACHINE` removes the entry of another machine (host name or node ID), for example one that is gone. |
| `agent-net events` | On the gateway machine: the gateway's events table, newest last. `--node` (ID or host name), `--session`, `--kind` (exact, or a group like `msg`), `--since` (like `2h` or `2026-10-29T12:00:00Z`), `--limit N` (default 100), `--follow` (or `-f`), `--json`, `--data DIR`. |
| `agent-net tui` | A live terminal view of every session on the network, with three actions: message a session as yourself, start a worker, stop a worker. It talks to the local daemon through the operator interface, so it runs on any machine. See [the screen](#the-tui-screen). |
| `agent-net start --project NAME [--harness opencode|claude|pi|codex] [--model M] [--machine HOST] [--isolation worktree] "TASK"` | Start a worker: a new agent session in a known project, on the machine that holds it. It prints the worker's session ID. The task is sent to it as a message from you; its report comes back to you (see the TUI, or `agent-net messages`). `--isolation worktree` gives it its own git worktree on the branch `agent-net/<worker-id>`. See [workers](#workers). |
| `agent-net stop SESSION` | Stop a worker. The process group is killed. |
| `agent-net workers` | The workers on the network, with machine, project, harness, model, owner, state and start time. `--json`. |
| `agent-net send SESSION TEXT...` | Send a message as yourself (the owner of this machine). `SESSION` is a session ID or a unique agent name. |
| `agent-net messages` | Recent messages and their states. `--session ID`, `--limit N` (default 20), `--json`. |
| `agent-net version` | Print the version. |

## Ports

| Port | Used by |
|---|---|
| TCP 7420 | The gateway. HTTPS and WebSocket, on `0.0.0.0` by default. Every daemon connects to it. Open it to your other machines and to nothing else. |

The daemon and the agents talk over Unix sockets, not network ports.

## Files

| What | Default path | Notes |
|---|---|---|
| Node credentials | `~/.config/agent-net/node.json` | Gateway URL, node ID, token, CA fingerprint. Mode 0600. |
| Pinned CA | `~/.config/agent-net/ca.pem` | The gateway's CA, trusted after the fingerprint check. |
| Daemon state | `~/.local/state/agent-net/daemon/daemon.db` | SQLite. Delivered message IDs (for duplicates), the outbox for sends made while the gateway is away, and the `workers` table (process ID, start time, owner, worktree) that lets the daemon take its workers back after a restart. Mode 0600, like its `-wal` and `-shm` files. |
| Worker folders | `~/.local/state/agent-net/daemon/workers/<worker id>/` | The worker's private files: for OpenCode its state folder and session file, for Pi the named pipe used as its standard input, for Codex the capability token of its app server (mode 0600). Also the output log of the harness. Deleted when the worker ends. |
| Worker worktrees | `~/.local/state/agent-net/daemon/worktrees/<worker id>/` | With `--isolation worktree`. Kept when the worker changed something, removed when not. The branch is `agent-net/<worker id>` in the project. |
| Daemon socket | `$XDG_RUNTIME_DIR/agent-net/daemon.sock`, else `/tmp/agent-net-<uid>/daemon.sock` | Directory 0700, socket 0600. macOS has no `XDG_RUNTIME_DIR`, so it uses the `/tmp` path. |
| Reply sockets | `<socket directory>/r/<session id>.sock` | One per sending session, used to catch Claude Code's native replies. |
| Gateway data | `~/.local/state/agent-net/gateway/` | `ca.pem`, `ca.key`, `server-key.pem`, `gateway.db` (SQLite, with `-wal` and `-shm` files). **Back this folder up.** Without it every machine has to join again. See [security.md](security.md#what-the-gateway-keeps). |
| Gateway binaries | `~/.local/share/agent-net/dist/agent-net-<os>-<arch>` | The four binaries the gateway hands out to new machines. |
| Binary | `~/.local/bin/agent-net` | |
| Claude Code sessions (read) | `~/.claude/sessions/` | The daemon reads one file per live session. It never writes here. |
| Linux services | `~/.config/systemd/user/agent-net-daemon.service`, `agent-net-gateway.service` | systemd user units. |
| macOS services | `~/Library/LaunchAgents/com.agent-net.daemon.plist`, `com.agent-net.gateway.plist` | launchd agents. |
| Linux logs | `journalctl --user -u agent-net-daemon` (and `-u agent-net-gateway`) | |
| macOS logs | `~/Library/Logs/agent-net-daemon.log`, `agent-net-gateway.log` | |

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
| | `--data DIR`, `--dist DIR`, `--port P` | gateway data, dist dir, `7420` (with `--gateway`) |
| `invite` | `--host H` | the IPv4 address of the interface with the default route. A second line is printed for a Tailscale address if there is one. |
| | `--port P`, `--data DIR` | `7420`, gateway data |
| `enroll` | `--host H`, `--port P`, `--data DIR` | this host's name, `7420`, gateway data |
| `join` | `--config-dir DIR` | `~/.config/agent-net` |
| `uninstall` | `--purge` | off. Also remove credentials and daemon state. |
| | `--no-service` | off |

`setup --gateway` does not pass the `--keep-*` flags, so the gateway service uses the defaults. To change them, add the flags to the `ExecStart` line of the systemd unit (or to `ProgramArguments` in the launchd plist) and restart the service. Setup keeps your edit only until the next `setup` run, which rewrites the unit.

`install.sh` takes `--gateway`, `--client`, `--binary-only`, `--accept-peer-messages`, `--no-accept-peer-messages` and `--version vX.Y.Z`. The last two peer-message flags are passed to `setup`. See [setup.md](setup.md).

## What each machine reports

Each daemon sends two kinds of facts about its machine to the gateway. Agents and the CLI can read them.

**Repo info for each session.** When the daemon finds a session (any harness), it runs `git` in the session's folder (with a 3 second limit for all commands) and sends:

- `root`: the main working tree of the repository. Empty if the folder is not in a git repository. A linked git worktree has the root of the main tree it belongs to, found with `git rev-parse --git-common-dir`.
- `worktree`: the working tree the session's folder is in. It is the same as `root`, except in a linked git worktree.
- `project`: the base name of `root`, or of the folder if there is no git repository. So a worktree is not a project of its own: the sessions of `tenebrae` and of its worktrees `agent-a76c...` are all in project `tenebrae`.
- `remote`: the `origin` URL. Passwords and tokens are removed. For `http`, `https`, `ftp` and `git` URLs the whole user part goes, because a token is often the user name. For `ssh` URLs only the password goes. A query or fragment is removed. `git@host:owner/repo` is kept as it is.
- `branch`: the current branch, or `detached@<short hash>`.
- `description`: the one-line description of the project, read from `AGENTS.md` (else `CLAUDE.md`) in the main working tree, or in the session's folder when there is no repository. See "FUNCTION and the task" below.

**FUNCTION and the task.** FUNCTION in `agent-net list`, the TUI and the `function` field of an agent's profile is the project description above. The model does not write it, and `agent_announce` cannot set it. With no `AGENTS.md` or `CLAUDE.md`, or no description line in it, FUNCTION is empty: `agent-net list` shows `-`. The daemon re-checks the file (one `stat`) at every update of the session, so a change shows within moments.

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
| OpenCode | `opencode serve` on `127.0.0.1`, random password | `provider/model`, checked against the machine's list | A normal message. The worker ends with `agent_handback`. | `opencode --session <id>` |
| Claude Code | `claude --bg --name worker-<project>-<id> --settings <json> --mcp-config <json>` (a background session of Claude's own service, not a child of the daemon) | An alias (`haiku`, `sonnet`, `opus`) or a full name, after `--model` | A normal message, delivered to the session's inbox. The worker ends with `agent_handback`, through the agent-net MCP server of this daemon's binary. | `claude attach <id>` (the id is the `jobId` in the session file) |
| Pi (needs `--isolation worktree`, or the daemon refuses it) | `pi --mode rpc --session-id <uuid> --name "worker <project>"`, standard input a named pipe that never closes | `provider/id[:thinking]`, after `--model` | A normal message, through the Pi extension, which loads in this mode. The worker ends with `agent_handback`. | Not possible: Pi's rpc mode has no screen. Read it with `agent-net show`, or resume its session file in Pi after the worker ended. |
| Codex | `codex app-server --listen ws://127.0.0.1:<port> --ws-auth capability-token --ws-token-file <file>` | A name, in `thread/start` | The daemon runs each message from the owner or the person as a turn. The last message of the turn is the report, handed back by the daemon. | `codex resume <thread id>` in the project folder |

  Claude Code needs the folder to be trusted (run `claude` in it once, or use a worktree of a trusted repository). The Pi extension and the OpenCode plugin must be installed on the machine (`agent-net setup`). The Claude session carries `crossSessionInbound: "accept"` and `AGENT_NET_SOCKET` in its per-session settings (`--settings`), so inbound agent-net messages are accepted; your `settings.json` is not changed.
- **The task** is sent as a message from the caller, after the fixed brief. For Claude Code, Pi and OpenCode the worker ends its work with `agent_handback(report)`. For Codex the daemon does it when a turn ends: status `done` for a finished turn, `failed` for a failed or interrupted one, and `blocked` when Codex asked for an approval (the daemon declines every approval, and the report names the command, the reason and the Codex thread to open). The caller gets one message with the facts (status, run time, model, tokens, the cost if the harness reports it, the worktree and branch, `git diff --stat`) and the report below them. Tokens come from OpenCode, from the Claude transcript, and from Codex. Cost comes from OpenCode only. Pi reports neither. A worker that ends without a hand-back sends the same notice with the status `timed out`, `failed` or `stopped`.
- **Follow-up messages.** OpenCode, Claude Code and Pi workers take messages like any session, and keep their conversation. A Codex worker takes them from its owner and from the person at the machine only: the daemon turns each into a turn, one after the other, and refuses other agents.
- **Ending:** no message to or from it for 60 minutes; 4 hours since the start; its owner ended or stayed offline for 10 minutes; `agent_stop` or `agent-net stop`. A worker that handed back stays up for follow-ups until the idle time. For Claude Code the daemon runs `claude stop <id>` first, then kills the process group if the process is still there. The stopped session stays in `claude agents`, with its conversation, until you `claude rm <id>`.
- **Limits:** 4 workers per machine. Settings are in `node.json`: `"workers": {"max": 4, "idle": "60m", "max_age": "4h", "owner_grace": "10m"}`. Restart the daemon after a change.
- **Owner:** `agent-net workers` and the TUI show who started each worker. A worker started from the command line or the TUI is owned by `you (on <host>)`.
- **Events:** `worker.started`, `worker.stopped` (with `reason=idle|max_age|owner_gone|stopped|process_exited|lost`) and `worker.refused`, in the gateway log, the events table and the TUI feed.

**The operator interface.** The daemon's local socket serves these methods to the command line and the TUI, and the phone app will use the same ones: `info`, `nodes`, `list`, `projects`, `events`, `message_status`, `workers` (reads) and `owner_send`, `worker_start`, `worker_stop` (actions). The types are in `internal/protocol/operator.go`; `worker_handback` is the worker's own call. Agent connections may use `worker_start`, `worker_stop`, `worker_handback` and `workers`. `owner_send` is for the command line only.

## The TUI screen

`agent-net tui` needs a terminal. It talks to the local daemon through the operator interface, as an owner's CLI (`hello` with `client: cli`). Besides the reads and the actions below it uses the owner-only methods `usage`, `counts`, `usage_top`, `session_text` and `session_enter` (see [CPU and memory](#cpu-and-memory) and [security.md](security.md#session-text-and-resource-figures)). It shows, from top to bottom:

- **Header.** `12 online · 5 offline today`, from `counts`. The total of old offline sessions is left out. An older gateway has no counts: the header then says `N sessions, M online`. Also the filter, and the time of the last refresh.
- **Machines,** one line each: online state, name, agent-net version, CPU now with a bar and the 5 minute average in brackets, memory used and total with a bar, 1 minute load, and the installed harnesses when the line has room. This machine comes first and is marked `(here)`. The name is the gateway's name for the machine, and `(here)` is matched by node ID, not by hostname. A figure that is missing or older than 45 seconds reads `?`.
- **Sessions,** grouped by machine under a heading: NAME, AGENT (the harness), PROJECT, BRANCH, MODEL (shortened: no provider, no `claude-` prefix), MODE (`fg`, `bg`; a dim `?` for unknown; blank for an older machine), STATUS (`idle`, `busy`, `shell`, `dorm` for dormant, `off` for offline), CPU, RAM and PROC (the session's process tree), and FUNCTION (the project description from `AGENTS.md`; "no AGENTS.md" when there is none). Offline sessions are dimmed. On a narrow terminal FUNCTION, BRANCH, MODE, MODEL and PROJECT go first; the usage columns stay. A tree with more than 20 processes, 200% CPU or 4G of memory shows its numbers in red. An OpenCode service process that several sessions share shows `shared` in their rows and is listed once under the machine's sessions, never split.
- **Selected session.** The first line has the name, harness, status and the session's CPU, RAM and process count. Below, on the left: session ID and PID, folder, branch, model and mode, remote, worktree (when the session runs in a linked worktree), project with its source (`registered`, `seen` or `both`), task, and its recent messages. On the right: the heaviest 5 processes by CPU and by memory (PID, CPU, RAM, command line with secrets masked), fetched with `usage_top` when the session is selected and again every 5 seconds. On a small terminal the lists get as many rows as fit; `d` gives the pane the room of the message feed.
- **Messages (live).** One line per message on the network, with both ends, the type and the delivery state. Other lines: machines connecting or disconnecting, new sessions, refused sends, workers starting, stopping or refused. Messages from you show as `you (on <host>)`.
- **Workers** show in the sessions table with `[w]` before the name and MODE `bg`. The selected worker's detail has a `worker` line: who started it, when, and its project. The title counts them.
- **A result line** under the title says what the last action did: plain when it worked, `!` and red when it did not. The next key clears it.
- **Conversation** (enter or `c` on a session). The whole screen shows the prompts, replies, tool calls and tool results of the session, newest last, each with its time and a role label (`user`, `assistant`, `tool`, `result`). Text is wrapped and shown as the daemon sent it, with its masking marks. The view follows new entries once a second; scroll up and it pauses (`G` follows again); page up past the top loads older entries by cursor; esc goes back.
- **Help** (`?`). Lists the keys and what the columns mean. The key line at the bottom shows the main keys and drops the least useful when it does not fit.

The screen refreshes once a second. CPU, memory and counts are asked every 5 seconds, because daemons report every 10 seconds. If the daemon is not running, or cannot reach the gateway, a banner says so, the last data stays on screen, and it retries every second. A daemon or gateway from before CPU and memory shows `?` and is not asked again.

| Key | What it does |
|---|---|
| up, down, `k`, `j`, `g`, `G`, page up, page down | Move the selection (or the message cursor, when the messages have the focus). |
| enter, `c` | Open the conversation of the selected session. With the messages in focus, enter opens the body of the message. |
| `/` | Filter the sessions: type words, all must appear in some field of a session (name, harness, machine, project, branch, model, mode, status, function, task, folder, ID). Enter keeps the filter, Esc clears it. |
| `o` | Show or hide offline and dormant sessions. |
| tab | Move the focus between the sessions and the messages. |
| `d` | Give the detail pane the room of the message feed, or give it back. |
| `?` | Show or hide the help. |
| esc | Leave the conversation, the help or a message body, or clear the filter. |
| In the conversation: up, down, page up, page down, `g`, `G`, `f` | Scroll; `g` goes to the oldest loaded entry and loads older ones; `G`, end or `f` follow new entries. |
| `r` | Refresh now. |
| `m` | Send a message to the selected session as yourself. Type, Enter sends, Esc cancels. The reply shows in the messages. |
| `s` | Start a worker. A form takes Project, Harness, Model and Task. Project completes from the project list (a name on two machines is offered as `name@machine`), Model from the model list of that project's machine. Tab completes, then moves on; up and down or Enter move between fields; Enter on the Task starts it; Esc cancels. If the selected session's project is known, the form starts on the Task with it filled in. |
| `x` | Stop the selected worker, after a `y/n` question. Any other session is refused with a message. |
| `e` | Enter the selected session: the TUI hands the terminal to it and comes back when you leave. See [Enter a session](#enter-a-session). |
| `q`, ctrl+c | Quit. |

A message body can only be shown for a message that involves a session on this machine. The gateway refuses it to other machines. Text from the network is stripped of control characters before it is drawn, so an agent name or a message cannot move the cursor or change the screen.

**Event feed.** The screen reads the gateway's events through the local daemon method `events` (`{"after_id": N, "limit": N}`) and the gateway frame `query.events`. Any enrolled machine can ask. It returns the events of kinds `msg.*`, `session.*` and `node.*`, oldest first, as metadata: the event kind and time, the machine, the session, the message ID, and for messages the two session IDs, the type and the ID of the message it answers. Never a message body. Machine addresses are left out, details are cut to 200 characters, and MCP, enrollment and retention events are not included. With `after_id` 0 you get the newest `limit` events (default 100, at most 500). An older daemon or gateway answers that the method is unknown, and the screen then shows the sessions without the feed.

### Enter a session

`e` in the TUI asks the daemon (`session_enter`) how to take over the terminal for the selected session, runs the answer, and draws the TUI again when you leave. The daemon of the machine that holds the session works out the command. The TUI builds none. Your own input in the session counts as yours, so approvals you give are yours.

| Session | What `e` runs |
|---|---|
| Claude Code background session (`kind` bg, which includes Claude workers) | `claude attach <id>`. The left arrow returns to the agent view, ctrl+z drops to the shell, the session keeps running. |
| Claude Code interactive session | Cannot be attached twice. If its process runs in a herdr pane, `herdr agent attach <pane>` (see below). Else, if its process runs in a tmux pane, the TUI switches its tmux client to that pane (when the TUI is in the same tmux server) or attaches a new client at that pane. Otherwise: "open its window on <machine>". |
| Pi | The same herdr and tmux rules. A Pi worker has no screen, so there is nothing to enter. |
| OpenCode | `opencode --session <id>`, in the session's folder, through OpenCode's own background service. |
| OpenCode worker | `opencode --server <its private url> --session <id>`, with its password in `OPENCODE_SERVER_PASSWORD`. Only from the machine that runs it. |
| Codex worker | `codex resume <thread> --remote ws://<its private address> --remote-auth-token-env AGENT_NET_CODEX_TOKEN`. Only from the machine that runs it. The daemon drives the same thread, so do not type while it works. Other Codex sessions are not known to agent-net and cannot be entered. |

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

`enter_ssh` is off by default. `enter_ssh_hosts` is optional: it maps the machine's name (as `agent-net nodes` shows it) to the ssh target, which can be an alias from `~/.ssh/config`. Without an entry the name itself is used. The target must be a plain host name or `user@host`. ssh must already work without a prompt for anything but a key passphrase or a Tailscale SSH check. The secrets of a worker's private server never leave the machine: workers of OpenCode and Codex on another machine can only be entered from a TUI on that machine.

## What setup changes on your machine

`agent-net setup` (run by the install line, by the invite line, or by hand) changes only the items below. Each step prints `ok`, `ok (unchanged)`, `updated` or `skipped (reason)`. The two service steps always restart the service, so they print `ok (restarted)` (unit file unchanged), `updated (restarted)` (unit file changed) or `ok (installed, started)` (new unit). Every step is safe to run again.

| Item | What setup does | What `agent-net uninstall` does |
|---|---|---|
| Binary `~/.local/bin/agent-net` | Put there by `install.sh` or the invite script, not by `setup` itself. | Keeps it. Delete it by hand. |
| Daemon service | Writes the systemd user unit (Linux) or launchd agent (macOS), enables it and restarts it. | Stops it and deletes the unit or plist. |
| Gateway service (only with `--gateway`) | Same, listening on `0.0.0.0:7420`. | Stops it and deletes the unit or plist. |
| Gateway binaries (only with `--gateway`) | Copies this binary into `~/.local/share/agent-net/dist`. | Keeps them. |
| Gateway data | Created by the gateway on first start. | Keeps it, even with `--purge` (it prints a reminder). Delete the directory by hand to start over. |
| Join | Writes `~/.config/agent-net/node.json` and `ca.pem`. Skipped if this machine already joined the same gateway. | Keeps them. `--purge` deletes `~/.config/agent-net`. |
| Daemon state | Created by the daemon. | Keeps it. `--purge` deletes `~/.local/state/agent-net/daemon`. |
| Claude Code | If `claude` is found, runs `claude mcp add -s user agent-net -- <binary> mcp`. This adds an entry to your user-level MCP servers in `~/.claude.json`. Skipped if the entry exists. | Runs `claude mcp remove -s user agent-net`. |
| Claude Code peer messages | If `claude` is found and `crossSessionInbound` is not set in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`), asks on the terminal, or follows `--accept-peer-messages` / `--no-accept-peer-messages`. On yes it adds `"crossSessionInbound": "accept"` and keeps every other key (file rewritten with 2-space indent, through a temp file and a rename). Records the answer in `~/.config/agent-net/claude-peer-messages.json`. An existing value is never changed. A file that is not valid JSON is not touched. The daemon and upgrades never change it. | Removes the key, only if agent-net set it and the value is still `"accept"`. Deletes the record. |
| OpenCode plugin | If `~/.config/opencode` (or `$XDG_CONFIG_HOME/opencode`) exists, writes `plugins/agent-net.ts` in it. A symlink there is left alone. | Deletes the file. A symlink is left alone. |
| Pi extension | If `~/.pi` exists, writes `~/.pi/agent/extensions/agent-net.ts`. A symlink there is left alone. | Deletes the file. A symlink is left alone. |
| Guidance text | Adds a block between `<!-- agent-net:begin -->` and `<!-- agent-net:end -->` to `~/.claude/CLAUDE.md` (if `~/.claude` exists) `~/.pi/agent/AGENTS.md` (if `~/.pi` exists) and `~/.config/opencode/AGENTS.md` (if `~/.config/opencode` exists). Text outside the markers is never changed. An older unmarked `## agent-net: working with other agents` section is replaced by the block. | Removes the block. |

The Pi extension, the OpenCode plugin and the guidance blocks are also written by the daemon, on every start, with the same rules as setup (the harness folder must exist, a symlink is left alone, guidance changes only between the markers). It rewrites a file only if it differs from the content embedded in the binary, and logs one line per file it changes (`agent files: pi extension updated`) in the daemon log. So these files follow the binary after an upgrade or a manual copy, without a setup run. See [upgrade.md](upgrade.md#agent-files-on-a-machine).

Setup does not use `sudo`. On Linux it may print a hint to run `sudo loginctl enable-linger <user>`, which keeps the user services running when you are logged out. You run that yourself.

Setup does not change any other Claude Code or Pi setting (the one exception is the peer-message setting above, and only with your yes), and it does not touch other MCP servers.
