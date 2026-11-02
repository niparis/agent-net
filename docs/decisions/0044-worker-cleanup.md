# 0044 - Worker cleanup

**Date:** 2026-11-01
**Status:** Built on a worktree branch. Project folder cleanup was removed later; only worker worktree cleanup remains.
**Context:** Worker worktrees and declared build folders can accumulate after work ends.

## Decision

Every 30 minutes the daemon checks registered projects. It removes an ended worker's managed worktree and branch when all branch commits are reachable from the project's default branch and the worktree has no uncommitted changes or untracked files. Ignored files are removed with the worktree. It keeps running or queued workers and branches used by starts in progress. Older worktrees without an end record are eligible as soon as they are found unused. It leaves branches without an end record or managed worktree alone because their origin cannot be established.

A project opts in to folder cleanup with lines in the first 8 KiB of `.agent-net/worker.md` of the form `cleanup: path age`, for example `cleanup: build/cache 48h`. Paths may be absolute or relative to the project root and cannot contain whitespace; ages are Go durations. The project root, its parents, its Git metadata and the daemon worktree directory cannot be cleanup targets. With no declarations, no project folder is trimmed.

The daemon removes an entry only when it and all of its descendants are older than the specified age. It keeps an entry when it finds an open file; an inconclusive process check does not block removal. Cleanup roots with symlinks are rejected. Hard links are kept. Cleanup logs the bytes removed.
