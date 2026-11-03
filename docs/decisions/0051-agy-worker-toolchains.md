# 0051 - Expose toolchains to Antigravity workers

**Date:** 2026-11-03
**Status:** Accepted by the owner.
**Context:** [0049](0049-antigravity-harness.md).

## Problem

Antigravity's terminal sandbox hides parts of home, including a Go installation
under `~/.local/go`. A worker can inherit a PATH that names Go while the binary
is invisible inside the sandbox. Adding a folder with `--add-dir` exposes it,
but also makes it writable.

## Decision

Accept writable toolchain folders for Antigravity workers. Find Go on the
harness search path and run `go env GOROOT GOMODCACHE` with the worker's
environment and a five second limit. Pass each existing folder as a separate
`--add-dir`. Refuse home itself and its parents, including symlink aliases.
A missing Go binary or a failed probe does not prevent the worker from starting.

List the grants in the daemon's worker start log and in the permissions line
shown to the worker and its owner. Keep the probes together so other toolchains
can be added later. Only Go is supported now.

Codex workers keep their current sandbox rules.

2026-11-03: Go is also looked for in standard install folders when it is not on the harness search path. On research it lives in ~/.local/go/bin, which only the project worker notes add to PATH, so the first release added no folders.
