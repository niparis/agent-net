# 0197 - One rule for worker sandboxes: the write fence

Date: 2026-11-27
Status: Decided by the owner on 2026-11-27, in the agent-net lead's chat. After the CTO's friction analysis he first said "I'm thinking we kill the sandbox system", then agreed with the lead's proposal: "sandbox >> i'm ok with your suggestion", "get going".

## Context

The CTO read 1,358 worker friction reports (2026-11-16 to 2026-11-27). About 550 came from the worker sandboxes:

- Unix sockets blocked (64 reports). Daemon tests could not run in a worker.
- Local ports and Chromium (about 130). Playwright, dev servers and screenshots failed.
- The Docker socket (38).
- The shared `.git` read-only: `packed-refs.lock` failed, `push -u` could not save the upstream, `switch -c` stopped halfway, `worktree add` was refused (101).
- `/tmp` and macOS `mktemp` under `/var/folders` refused (152).
- A read-only `GOCACHE`, uv, `~/.cache` and the Go checksum database (31).

There were four sandboxes, each with its own limits: agent-net's OS wrapper for Pi and OpenCode (bubblewrap on Linux, `sandbox-exec` on macOS), Claude Code's Bash sandbox for Claude workers, Codex's sandbox and agy's `--sandbox`. The wrapper also hid folders (`$XDG_RUNTIME_DIR`, the host `/tmp`, the Docker socket), kept the network switch, and on macOS denied every Unix socket except the agent-net socket and the DNS resolver.

## Decision

Keep one rule for every worker harness: **a worker may write only in its worktree, its own temp folder, its caches and the repository's git folder, but not the git config and hooks.** Everything else is allowed: the network, every Unix socket, listening on loopback, and reads anywhere. Nothing is hidden.

The writable folders, the same for every harness:

- the worktree;
- the repository's git folder (the common `.git`: objects, refs, `packed-refs`, logs, the worktree's own `worktrees/<id>` and new entries under `worktrees/`), so `git commit`, `git switch -c` and `git worktree add` work. Its `config`, `config.worktree`, `hooks`, `info`, `modules`, the folder that `core.hooksPath` names, and the folders of the other linked worktrees stay read-only (see [the git trade-off](#the-git-trade-off));
- the worker's own temp folder. Under the OS wrapper `TMPDIR`, `TMP`, `TEMP` and `GOTMPDIR` all name `tmp` in the worker folder; `/tmp` itself stays read-only there;
- the worker's own package caches (`cache` in the worker folder: uv, npm, pip, and now cargo through `CARGO_HOME`);
- the shared Go build cache (now also set as `GOCACHE` for every worker), the shared Go temp folder and the Go module cache, which holds the checksum database cache;
- for the OS wrapper, the agent-net socket folder.

How each harness applies it:

- Pi and OpenCode, Linux: `bwrap --unshare-all --share-net --ro-bind / / --dev /dev --proc /proc`, then one `--bind` for each folder above, then one `--ro-bind` for each read-only git path over them. No `--tmpfs`, no private `/tmp`, no `/dev/null` masks.
- Pi and OpenCode, macOS: a Seatbelt profile that allows everything except file writes, allows writes only in the same folders, and then denies writes to the read-only git paths (the later rule wins).
- Claude: `sandbox.filesystem.allowWrite` names the same folders and `sandbox.filesystem.denyWrite` the read-only git paths. `sandbox.network` turns on `allowAllUnixSockets`, `allowLocalBinding` and `allowMachLookup: ["*"]`.
- Codex: the same folders as writable roots of its `workspace-write` sandbox, network on. Codex has no read-only list inside a writable root, so in place of the whole git folder it gets only the folders a commit writes: `objects`, `refs`, `logs` and the worktree's own `worktrees/<id>`.
- agy: the OS wrapper, as for Pi and OpenCode, and agy runs without its own `--sandbox` inside it. agy's own folders (`~/.gemini/antigravity-cli` and its login folder) are its writable private state. The folders above stay `--add-dir` folders, with the same narrow git folders as Codex, for agy's file tools. Why: in the real Mac check of v0.5.171 agy's terminal sandbox could not run the owner's Nix shell, and on the Linux machine it could not exec the shell at all, so the model reran every command with `BypassSandbox` and nothing held the fence. agy 1.5.18 has no setting that stops that bypass. A project with the wrapper turned off keeps agy's `--sandbox`, and its permissions line says the fence is not enforced.

Each harness keeps its own permission mode. The owner's own sessions, the CTO's sandbox and the picker do not change.

## The git trade-off

The worker can write the repository's git folder. It can move or delete any branch, not only its own. That is visible in `git reflog` and can be undone.

It cannot write the files that make git run code. Git runs hooks from `.git/hooks` or the folder `core.hooksPath` names, and the config can name commands: `core.fsmonitor` runs on an ordinary `git status`, and filter drivers, `core.sshCommand` and credential helpers run on other commands. A linked worktree's `commondir` file says which git folder, and so which config, git uses there. That code runs with no sandbox the next time the owner, the merger or the daemon runs git in the main checkout or in another worktree. So, under the wrappers that have a read-only list (bubblewrap, Seatbelt, Claude Code), these stay read-only inside the writable git folder: `config`, `config.worktree`, `hooks`, `info`, `modules`, the `core.hooksPath` folder, and the folders of the other linked worktrees that exist when the worker starts. The daemon makes `hooks` and `info` when they are missing, so a worker cannot make them. Codex and agy have no read-only list, so they get only the folders a commit writes.

Three costs follow:

- `git push -u` cannot save the upstream in the config. Workers push with a plain `git push`: the daemon sets `branch.<branch>.remote` and `branch.<branch>.merge` for the worker's branch at the start, and on hand-back it pushes the worker's branch itself when a delivery step on another machine needs it.
- Under Codex and agy, `git worktree add` and a change to `packed-refs` (deleting a packed branch) fail: those write in the root of the git folder or in `worktrees/`.
- A linked worktree made after the worker started is not in its read-only list under bubblewrap and Claude Code, whose mounts are fixed at the start.

A worker's own git still runs with `core.hooksPath` set to agent-net's commit hooks, `gc.auto=0` and `maintenance.auto=false`, so a worker never repacks the shared folder. The worker's own worktree, with its `.git` file, is the worker's: git that the daemon runs there reads what the worker wrote. This decision does not change that.

## What agent-net cannot change

- Codex keeps `/tmp` writable, by its own rules.
- Claude Code's Bash sandbox keeps a fixed list of files read-only in the working folder (`.bashrc`, `.gitconfig`, `.mcp.json`, `.claude/agents` and others). Where one is missing, it makes an empty file as a mount point. agent-net did not make these files, and no Claude Code setting turns them off. So `worker_sandbox_placeholders.go` stays: without it, the leftover commit would add the empty files to the worker's branch. A Claude worker's git now gets `core.excludesFile` set to a file in its worker folder, with the owner's own excludes and these names, so they no longer show in the worker's `git status`, `git add -A` or `scripts/test-changed.sh`. A real folder with one of these names still shows.
- On Linux, Claude Code runs each shell command in its own network namespace. A port that a command opens on loopback is reachable inside that command, not from a later command.
- agy cannot move its own folders, so an agy worker in the wrapper writes the owner's `~/.gemini/antigravity-cli` and login folder. A worker could change agy's settings or helper binaries there, which the owner's next agy session uses outside the wrapper.

## Measured

Dedicated sandbox files (`ossandbox*.go`, `claude_sandbox.go`, `worker_codex_sandbox.go`, `worker_sandbox_placeholders.go`):

| | Before | After |
|---|---|---|
| Production lines | 985 | 868 |
| Test lines (`ossandbox*_test.go`, `claude_sandbox_test.go`, `worker_sandbox_placeholders_test.go`) | 1,227 | 1,186 |

Over the whole change in `internal/`: production +171 and -250 lines, tests +302 and -267 lines. The test lines include a new probe that the bubblewrap and `sandbox-exec` tests share. It commits, runs `git switch -c`, writes `$TMPDIR`, opens a Unix socket there, listens on `127.0.0.1`, and fails to write a home folder outside the worktree.

The second round, after the lead's review, made the git config and hooks read-only: production +120 and -52 lines, tests +110 and -27 lines in `internal/` (the dedicated sandbox files are now 913 production lines and 1,204 test lines). The probe now also runs `git worktree add`, and fails to write `.git/hooks/pre-commit`, `.git/config` and another linked worktree's `commondir`.

Removed: the hidden folders (`$XDG_RUNTIME_DIR`, the host `/tmp`), the Docker socket mask, the read-only `.git` entry of the worktree, the "visible" binds that hidden folders needed, the network switch, the Seatbelt Unix socket rules and read denies, the separate socket path inside the sandbox, and the narrow list of git folders for workers. The narrow list stays only for Codex role sessions, which this decision does not cover.
