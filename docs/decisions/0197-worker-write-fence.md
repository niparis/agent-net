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

Keep one rule for every worker harness: **a worker may write only in its worktree, its own temp folder, its caches and the repository's git folder.** Everything else is allowed: the network, every Unix socket, listening on loopback, and reads anywhere. Nothing is hidden.

The writable folders, the same for every harness:

- the worktree;
- the repository's whole git folder (the common `.git`: objects, refs, `packed-refs`, logs, config and the worktree's own `worktrees/<id>`), so `git commit`, `git switch -c`, `git push -u` and `git worktree add` work;
- the worker's own temp folder. Under the OS wrapper `TMPDIR`, `TMP`, `TEMP` and `GOTMPDIR` all name `tmp` in the worker folder; `/tmp` itself stays read-only there;
- the worker's own package caches (`cache` in the worker folder: uv, npm, pip, and now cargo through `CARGO_HOME`);
- the shared Go build cache (now also set as `GOCACHE` for every worker), the shared Go temp folder and the Go module cache, which holds the checksum database cache;
- for the OS wrapper, the agent-net socket folder.

How each harness applies it:

- Pi and OpenCode, Linux: `bwrap --unshare-all --share-net --ro-bind / / --dev /dev --proc /proc`, then one `--bind` for each folder above. No `--tmpfs`, no private `/tmp`, no `/dev/null` masks.
- Pi and OpenCode, macOS: a Seatbelt profile that allows everything except file writes, and allows writes only in the same folders.
- Claude: `sandbox.filesystem.allowWrite` names the same folders, and `sandbox.network` turns on `allowAllUnixSockets`, `allowLocalBinding` and `allowMachLookup: ["*"]`.
- Codex: the same folders as writable roots of its `workspace-write` sandbox, network on.
- agy: the same folders as `--add-dir`.

Each harness keeps its own permission mode. The owner's own sessions, the CTO's sandbox and the picker do not change.

## The git trade-off

The worker can now write the repository's whole git folder. It can move or delete any branch, not only its own. That is visible in `git reflog` and can be undone.

It can also change the repository's `config` and `hooks`. `git push -u` writes the config, and `git worktree add` writes `worktrees/`, so the owner's yes to those commands needs it. A changed `core.hooksPath` or a new hook would run in the owner's own, unsandboxed git commands. That is a real way out of the fence, and it is part of this choice. A worker's own git still runs with `core.hooksPath` set to agent-net's commit hooks, `gc.auto=0` and `maintenance.auto=false`, so a worker never repacks the shared folder.

## What agent-net cannot change

- Codex and agy keep `/tmp` writable, by their own rules.
- Claude Code's Bash sandbox keeps a fixed list of files read-only in the working folder (`.bashrc`, `.gitconfig`, `.mcp.json`, `.claude/agents` and others). Where one is missing, it makes an empty file as a mount point. agent-net did not make these files, and no Claude Code setting turns them off. So `worker_sandbox_placeholders.go` stays: without it, the leftover commit would add the empty files to the worker's branch. A Claude worker's git now gets `core.excludesFile` set to a file in its worker folder, with the owner's own excludes and these names, so they no longer show in the worker's `git status`, `git add -A` or `scripts/test-changed.sh`. A real folder with one of these names still shows.
- On Linux, Claude Code runs each shell command in its own network namespace. A port that a command opens on loopback is reachable inside that command, not from a later command.
- agy hides parts of the home folder in its own sandbox.

## Measured

Dedicated sandbox files (`ossandbox*.go`, `claude_sandbox.go`, `worker_codex_sandbox.go`, `worker_sandbox_placeholders.go`):

| | Before | After |
|---|---|---|
| Production lines | 985 | 868 |
| Test lines (`ossandbox*_test.go`, `claude_sandbox_test.go`, `worker_sandbox_placeholders_test.go`) | 1,227 | 1,186 |

Over the whole change in `internal/`: production +171 and -250 lines, tests +302 and -267 lines. The test lines include a new probe that the bubblewrap and `sandbox-exec` tests share. It commits, runs `git switch -c`, writes `$TMPDIR`, opens a Unix socket there, listens on `127.0.0.1`, and fails to write a home folder outside the worktree.

Removed: the hidden folders (`$XDG_RUNTIME_DIR`, the host `/tmp`), the Docker socket mask, the read-only `.git` entry of the worktree, the "visible" binds that hidden folders needed, the network switch, the Seatbelt Unix socket rules and read denies, the separate socket path inside the sandbox, and the narrow list of git folders for workers. The narrow list stays only for Codex role sessions, which this decision does not cover.
