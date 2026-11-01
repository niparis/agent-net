# 0044 - Worker disk admission and cache cleanup

**Date:** 2026-11-01
**Status:** Built on a worktree branch.
**Context:** Concurrent worker builds can fill a machine's disk even when CPU and memory admission allow more workers.

## Decision

Worker starts wait when the worktree filesystem or a configured build cache filesystem has less than the configured free-space floor. The default floor is 15 GiB. The existing queue retries starts as space returns.

The daemon checks configured Go build cache and temporary roots. On Linux the defaults are `/tmp/agent-net-gocache` and `/tmp/agent-net-gotmp`. The owner can replace either list in `node.json`. Cache paths in project instructions must also be listed there. The daemon does not infer deletion authority from project prose.

Every 30 minutes and on low disk space, the daemon removes build cache files older than two hours. It removes top-level temporary entries when every file and directory inside is older than two hours. An open-file check can keep an entry, but an inconclusive check does not prevent removal. Cleanup is confined to the configured roots. Root paths with symlinks are rejected, and the daemon logs cleanup results.

On low disk space, the daemon sends a Needs-you check containing free space and up to three large folders. The gateway limits these checks to one per machine per hour, including after a restart. Folder sizes exclude inaccessible paths and other filesystems.
