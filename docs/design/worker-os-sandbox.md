# OS sandbox for workers (W7)

Status: built for Pi and OpenCode workers on Linux ([decision 0059](../decisions/0059-worker-os-sandbox.md)). Claude workers are refused when the switch is on (see below). macOS is designed here and not built. Plan: [hardening-plan.md](hardening-plan.md) W7. Earlier research: [worker-sandbox.md](worker-sandbox.md). Levels: [decision 0020](../decisions/0020-worker-sandbox.md).

## What it is

An optional wrapper around the whole harness process of a worker. Every child of the harness (shell commands, language servers, MCP servers) inherits it. The owner switches it on per project, on the machine that runs the workers. Off by default.

It does not change how the level (`sandboxed`, `no-network`, `read-only`) is chosen or stored. The level still comes from the project policy and the settings layer. The switch only says whether the OS wrapper is used.

## Which tool

| OS | Tool | Choice |
|---|---|---|
| Linux | bubblewrap (`bwrap`) | Used. Mount namespaces give a read-only view of the host with a few writable binds, and can hide whole folders of control sockets. A private PID namespace hides host processes. A private network namespace turns the network off. Packaged by every major distribution; Ubuntu 24.04 ships 0.9.0. |
| Linux | Landlock | Not used now. It restricts paths but cannot hide a socket file that is readable, cannot block abstract Unix sockets before ABI 6, and ABI 4 cannot block UDP. It must be set in a single-threaded helper just before `exec`. A possible second layer later. |
| macOS | `sandbox-exec` (Seatbelt) | Designed below, not built. Deprecated by Apple, still present. |

## Which harness

| Harness | With the switch on |
|---|---|
| Pi | Wrapped. `pi --mode rpc` is a direct child of the daemon. |
| OpenCode | Wrapped. `opencode serve` is a direct child of the daemon. |
| Codex | Not wrapped. Codex keeps its own sandbox (bubblewrap on Linux, Seatbelt on macOS) for each command. |
| Antigravity | Not wrapped. agy keeps its own `--sandbox` flag. |
| Claude Code | Start refused. See "Claude workers". |

## The Linux wrapper

The daemon runs `bwrap <policy> -- <harness> <args>` instead of the harness. The policy, in order:

- `--unshare-all`, then `--share-net` unless the network is off. This gives new user, IPC, PID, UTS, cgroup and network namespaces.
- `--ro-bind / /`, `--dev /dev`, `--proc /proc`: the whole host is visible and read-only. A new `/proc` shows only the sandbox's own processes. A new `/dev` has no host terminals.
- A private `/tmp`: the folder `tmp` in the worker's private folder, bound at `/tmp`. It is on disk, not in memory, and it is removed with the worker's folder. `TMPDIR` is `/tmp`.
- `$XDG_RUNTIME_DIR` (for example `/run/user/1000`) is hidden by an empty tmpfs. This hides the systemd user bus (`systemd-run --user` would start a process outside the sandbox), the user's Podman socket, keyring and agent sockets.
- The agent-net socket is bound back at its own path, so the harness's agent-net plugin or extension works.
- `/run/docker.sock` is hidden when it exists. Membership in the docker group gives root.
- Writable: the worker's folder (`sandboxed` and `no-network` only), and the narrow git folders a commit needs (the worktree's own git folder and the common `objects`, `refs` and `logs`), as for Codex. The worktree's `.git` file or folder is bound read-only again, so a worker cannot point git at a folder it controls or plant a hook.
- Writable: the harness's private state (below).
- `--chdir` the worker folder, `--setenv TMPDIR /tmp`. `HOME` is unchanged, so the harness finds its configuration, read-only.

Two flags that bubblewrap users often add are left out on purpose:

- **No `--die-with-parent`.** A worker outlives the daemon: the daemon finds it again after a restart or an upgrade. With this flag, every daemon restart would kill every sandboxed worker. The daemon still stops a worker with SIGTERM and then SIGKILL to its process group. The harness stays in the process group of `bwrap`, and when the sandbox's PID 1 ends, the kernel ends every process in the sandbox.
- **No `--new-session`.** The daemon already starts `bwrap` in a session of its own, with no controlling terminal. That is the protection `--new-session` gives (against `TIOCSTI` input injection). A second session would take the harness out of the process group, and the daemon's group stop would no longer reach it.

### What each harness writes

| Harness | Writable private state | Read-only |
|---|---|---|
| Pi | A private agent folder `pi-agent` in the worker's folder, set as `PI_CODING_AGENT_DIR`. The daemon copies `auth.json` and `models-store.json` into it at each start. Pi's session folder (`PI_CODING_AGENT_SESSION_DIR`, default `~/.pi/agent/sessions`) is bound writable, because the daemon reads the worker's usage there. | Every other entry of the owner's agent folder (`extensions`, `settings.json`, `models.json`, `skills`, `npm`, `git`, `SYSTEM.md` and so on) is bound read-only into the private folder. |
| OpenCode | The worker's own `data` and `state` folders (`XDG_DATA_HOME`, `XDG_STATE_HOME`), which the daemon already makes private and fills with a copy of the credentials. | `~/.config/opencode` and `~/.cache/opencode`. |

Why Pi needs a private folder: Pi takes a lock by creating a folder next to `auth.json` and `settings.json`, so its folder must be writable. But Pi runs an `auth.json` key value that starts with `!` as a shell command, and loads code from `extensions`, `npm`, `git` and `settings.json`. A worker that could write the owner's real agent folder could plant a command that runs, unsandboxed, in the owner's next Pi session. The private folder gives Pi its locks and its own credentials, and keeps the owner's files read-only. Token refreshes stay in the worker's copy, as they already do for OpenCode workers.

Both were checked with the real harness inside this policy, with a temporary home and no model call: `opencode serve` 2.2.5 started, created a session and loaded a project; `pi --mode rpc` 1.4 started and answered `get_state`.

### Levels

| Level | Worker folder and git folders | Harness private state | Network |
|---|---|---|---|
| `sandboxed` | writable | writable | on |
| `no-network` | start refused (below) | | |
| `read-only` | read-only | writable | on |

`read-only` keeps the harness's private state writable. Without it the harness cannot start. This is the clarification that [worker-sandbox.md](worker-sandbox.md) asked the owner to accept: no writes to the project or to shared host files.

`no-network` is refused for a wrapped harness. The wrapper covers the whole harness, so a private network namespace also cuts the harness's own connection to its model, and the worker could do nothing. A model relay is a separate design. Use a Codex worker for `no-network`, whose sandbox covers commands only, or turn the switch off for that project.

## When the tool is missing

With the switch on, the daemon checks before each start of a Pi or OpenCode worker:

1. the OS is Linux;
2. `bwrap` is on the daemon's `PATH`;
3. a probe runs: `bwrap --unshare-all --ro-bind / / --dev /dev --proc /proc -- <bwrap> --version`. This fails when user namespaces are denied (for example by an AppArmor rule or a container).

If a check fails, the start is refused with the reason and the fix, for example "bubblewrap is not installed: install it (Debian and Ubuntu: `sudo apt install bubblewrap`), or remove the project from `workers.os_sandbox` in node.json". The worker never starts unsandboxed. A queued start is checked again when it leaves the queue.

## The switch

In `node.json` on the machine that runs the workers:

```json
"workers": {"os_sandbox": ["project-a", "project-b"]}
```

`"*"` (as the value or as an item) means every project. Names match the project name without regard to case. The daemon reads node.json when it starts: restart it after a change. A sandboxed worker cannot change node.json: `~/.config/agent-net` is read-only inside the sandbox.

Why node.json and not the project policy: it is a fact about one machine (bubblewrap is installed and user namespaces work there), and it must stay apart from the level, which the settings layer owns.

A worker that was started sandboxed records it in its private folder (`os-sandbox`). Its follow-ups and a start after a daemon restart stay sandboxed even if the switch is turned off later. A follow-up is also sandboxed when the switch is on now.

The permissions line of the start notice, the brief and the completion notice says so:

```
permissions: sandboxed (writes only in its worktree, network on) (sandboxed by bubblewrap)
```

Without the wrapper, Pi and OpenCode workers keep "(enforced for Codex only for now)". Claude workers enforce the level with Claude Code's own settings, below.

## Claude workers

A Claude worker is `claude --bg`. The session runs inside Claude Code's own background service (`claude daemon run`, which starts a `bg-pty-host` and a pre-started spare process), not as a child of agent-net. A wrapper around `claude --bg` would sandbox only the short client that hands the job over. So with the switch on, a Claude worker start, and a Claude follow-up, are refused with this reason. A Claude worker that already runs keeps running.

The follow-up for Claude, built for the write boundary: Claude Code has its own sandbox for its Bash tool (bubblewrap on Linux, Seatbelt on macOS). Claude Code 2.1.322 has the settings `sandbox.enabled`, `sandbox.filesystem.allowWrite`, `sandbox.network.allowedDomains`, `sandbox.allowUnsandboxedCommands` and `sandbox.failIfUnavailable`, and `permissions.blockWritesOutsideWorkingDirectories` for its file tools. The worker's `--settings` reach a `--bg` session, so the daemon sets them per worker, as it sets the level for Codex: the file-tool rule refuses writes outside the worktree in every mode, and the Bash sandbox keeps commands to the worktree, the git folders a commit writes (not hooks or config), the Go module cache, the shared Go caches and the temp folders. `read-only` denies the worktree too; `no-network` leaves the sandbox's hosts empty. When bubblewrap or socat is missing, Claude Code warns and runs the commands unsandboxed, while the file-tool rule still applies. The OS wrapper itself still refuses a Claude start. The other way, a Claude process started directly by the daemon inside bubblewrap, loses `claude attach` and needs a terminal holder that outlives the daemon, and Claude writes `~/.claude.json` and `~/.claude/settings.json`, which can define hooks and MCP servers that run unsandboxed in the owner's sessions.

## macOS design (not built)

Run `/usr/bin/sandbox-exec -p <profile> -- <harness> <args>`. The profile is generated per worker:

```
(version 1)
(allow default)
(deny file-write*)
(allow file-write* (subpath "<worker folder>") (subpath "<git folders>") (subpath "<private state>")
                   (subpath "<private tmp>") (literal "/dev/null") (regex #"^/dev/tty"))
(deny network-outbound (remote unix-socket))
(allow network-outbound (remote unix-socket (path-literal "<agent-net socket>")))
```

Paths are resolved (`/private/var`, symlinks) and quoted before they go into the profile. `no-network` is refused for the same reason as on Linux. Open points before it is built: `(allow default)` allows Mach service lookups, so a worker could ask `launchd` or another app (Terminal through Apple Events) to run a process outside the sandbox; a deny list of Mach services is needed and must be tested on each supported macOS version. Seatbelt syntax is not a stable public API. Check that `sandbox-exec` exists and a probe profile runs before each start, and refuse otherwise.

## The threat it covers

A worker that is prompt-injected or careless, running as the owner's user:

- cannot write outside its worktree, the narrow git folders and its private state: no `rm -rf ~`, no change to shell start files, to other projects, or to the main repository's `config` and `hooks`;
- cannot plant code in the owner's harness configuration (Pi extensions and auth commands, OpenCode plugins and cache) that would run later outside the sandbox;
- cannot reach the control sockets in `/tmp` and `$XDG_RUNTIME_DIR`: the systemd user manager, the Claude Code and Codex background services, tmux, ssh-agent;
- cannot see, signal or trace the owner's other processes;
- with `read-only`, cannot write the project at all.

## What it does not cover

- **Reads.** The worker can read every file the owner can read: `~/.ssh`, `~/.aws`, browser profiles, other projects. Treat what a worker can read as what it can leak.
- **Exfiltration and local services.** With the network on, it can send what it reads, and it can reach any service on localhost or the LAN. An ssh server on this machine that accepts a key the worker can read is a way out. So is any local HTTP API that runs commands.
- **Abstract Unix sockets** of the host stay reachable while the network namespace is shared (for example an X11 display).
- **Other sockets on the filesystem** outside `/tmp`, `$XDG_RUNTIME_DIR` and `/run/docker.sock` stay reachable, because a read-only mount does not stop `connect()`.
- **Its own state.** The worker can change its own session history and its private copy of the credentials.
- **Kernel and bubblewrap flaws.** A namespace sandbox is as strong as the kernel.

MCP servers the harness starts run in the sandbox too, so one that needs a hidden socket or a write outside it fails. Seen on the research machine: the owner's OpenCode config starts the GitHub MCP server with `docker run`, which cannot reach the hidden Docker socket. That is the intended trade: the Docker socket gives root.
- **Claude workers** are refused, not sandboxed. Codex and Antigravity keep their own sandboxes, unchanged.
