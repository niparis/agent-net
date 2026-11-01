# 0044 - Worker cleanup

**Date:** 2026-11-01
**Status:** Built on a worktree branch.
**Context:** Worker worktrees and declared build folders can accumulate after work ends.

## Decision

Every 30 minutes the daemon checks registered projects. It removes an ended worker's managed worktree and branch only after 24 hours, when all branch commits are reachable from other kept branches and the worktree has no uncommitted changes or untracked files. Ignored files are removed with the worktree. It keeps running or queued workers and branches used by starts in progress. For older worktrees without an end record, it waits 24 hours after first observing them unused. It leaves branches without an end record or managed worktree alone because their origin cannot be established.

A project opts in to folder cleanup with lines in the first 8 KiB of `.agent-net/worker.md` of the form `cleanup: path age`, for example `cleanup: build/cache 48h`. Paths may be absolute or relative to the project root and cannot contain whitespace; ages are Go durations. The project root, its parents, its Git metadata and the daemon worktree directory cannot be cleanup targets. With no declarations, no project folder is trimmed.

The daemon removes an entry only when it and all of its descendants are older than the specified age. It keeps an entry when it finds an open file; an inconclusive process check does not block removal. Cleanup roots with symlinks are rejected. Hard links are kept. Cleanup logs the bytes removed.
