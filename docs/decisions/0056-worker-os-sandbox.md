# 0056 - An optional OS sandbox for Pi and OpenCode workers

Date: 2026-11-04

Amends [0020](0020-worker-sandbox.md), which left the other harnesses for later. Design: [worker-os-sandbox.md](../design/worker-os-sandbox.md). Plan: [hardening-plan.md](../design/hardening-plan.md) W7.

## Decision

- On Linux, a Pi or OpenCode worker of a project listed in `workers.os_sandbox` of `node.json` runs inside bubblewrap. The wrapper applies the level of 0020; it does not choose it. Off by default.
- The switch is per machine and per project, in `node.json`, apart from the level, which the settings layer owns.
- Fail closed: when the switch is on and the wrapper cannot run (no bubblewrap, user namespaces off, macOS), the start is refused with the reason. A worker never runs without the sandbox while the switch is on.
- A Claude worker is refused while the switch is on: `claude --bg` runs the session in Claude Code's own background service, which no wrapper started by the daemon can reach.
- `no-network` is refused for a wrapped harness: the wrapper covers the whole harness, so it would also cut the harness's connection to its model.
- `read-only` keeps the harness's private state writable, so the harness can run. Nothing in the project or in shared host files is writable.
- Pi runs with a private agent folder: a copy of its credentials, and the owner's other entries bound read-only. The owner's real folder is never writable from the sandbox, because Pi runs `!` values of `auth.json` as shell commands and loads code from it.
- No `--die-with-parent` and no `--new-session`: a worker outlives a daemon restart, and the daemon stops it through bubblewrap's process group.
- Codex and Antigravity keep their own sandboxes and are not wrapped.

## Not done here

- macOS (`sandbox-exec`): designed, not built.
- Claude workers: the plan is Claude Code's own sandbox settings in the worker's `--settings`, checked first with a real worker run.
- A model relay that would let a wrapped worker run at `no-network`.
