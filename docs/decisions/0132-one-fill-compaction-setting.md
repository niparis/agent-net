# One fill-based compaction setting for persistent sessions

Date: 2026-11-11.

The owner ordered one auto-compaction control for every persistent non-worker
session on every harness.

`sessions.compaction_threshold_percent` is the primary key: an integer from 1 to
100, default 80, at the network, team, repo and agent levels, live. A worker
session is never compacted. A session resolves the repo, the repo's agent and
that agent's team, so the owner can change the value for any persistent session.

The trigger is context fill at or above the threshold, or idle for the
provider's prompt cache window minus 30 seconds, whichever comes first. Neither
runs while a client is attached or mid-turn. Each idle stretch gets one attempt;
a busy turn resets the guard.

The CTO's own loop reads the same key with its agent level. The schema migration
sets `50` at the CTO's agent level when no value is set there, so the CTO keeps
its old behaviour. The old `threshold_percent`, `cache_expiry_enabled` and
`cache_window_seconds` fields in the CTO settings are removed.
`sessions.cache_window_seconds` replaces `sessions.idle_compaction_after`.
`sessions.idle_compaction` and `sessions.idle_compaction_min_tokens` remain, and
their descriptions say they control only the idle trigger.

Claude compacts through `/compact` typed into its own attach client. OpenCode
compacts through its compaction API; the CTO loop and persistent OpenCode
sessions share that call. Codex compacts through the shared app server's
`thread/compact/start`. Pi runs its own compaction when the daemon pushes
`compact` to the extension. A harness with no compaction command is reported on
the session row and not attempted.

Each session row carries `last_compacted_at` and `last_compacted_reason`, where
the reason is `fill N%` or `idle`. The Sessions row shows both, and
`agent_sessions_find` with `detail: full` returns both.
