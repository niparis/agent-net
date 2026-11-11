# 0132 Struggle detector

Date: 2026-11-11

## The problem

A persistent agent can work around a broken instruction or a broken
environment and then report success. The owner has seen git
`packed-refs.lock` errors, sandbox refusals, a worker re-running the same
test, and a worker that said `complete: yes` with three CI failures found
later. The report is model output; the tool trace is the evidence.

The talk "Your AI Agent Is Hiding Its Mistakes From You" (Warp) makes the
same point: agents probe a port for minutes because a skill is stale, and
only the tool trace shows it.

## The decision

At run end, the daemon reads the run's own tool record once and counts the
struggle signs. It makes no model call, reads no new data from the harness
while the worker is running, and does not change what the worker does.

The signals are:

- failed tool calls: a tool error, a non-zero exit, a permission or sandbox
  refusal;
- the same command run 3 or more times;
- retries of a command that had already failed;
- tool calls before the first edit, as the exploration length;
- tool results that name a known environment problem: `packed-refs.lock`,
  `permission denied`, `operation not permitted`, `command not found`,
  `address already in use`, `context deadline exceeded`.

The pattern tables live in one place: `struggleEnvErrors`,
`struggleEditTools` and `struggleWriteMarkers` in
`internal/daemon/struggle.go`.

## Trace sources

| Harness | Source | How |
|---|---|---|
| Claude Code | session transcript | `~/.claude/projects/<folder>/<session>.jsonl`, tool_use and tool_result blocks |
| Codex | rollout file | `sessions` or `archived_sessions` under `CODEX_HOME`, matching `-<thread>.jsonl`, `function_call` and `function_call_output` items |
| OpenCode | messages API | `GET /api/session/<id>/message`, tool parts with a state |
| Pi | none | the adapter reads usage from its session file, not tool calls: `no trace` |
| Antigravity | none | the adapter reads step usage from its stream, not tool calls: `no trace` |

No trace is reported as `no trace`. The daemon does not guess zeros.

## Cost

The trace is read once, at run end, streaming one line at a time. A trace
longer than 20 MB is read from its end only, at most the last 20 MB, and the
line says it was capped. No full trace is loaded into memory.

## Storage

The counts are kept on the worker record in the daemon's database, in one
`struggle` JSON column, and on the run in `worker_runs`: `struggle_failed`,
`struggle_repeats`, `struggle_retries`, `struggle_exploration`,
`struggle_env`, `struggle_top`, `struggle_trace`, `struggle_capped`. The
gateway adds the columns with the column-check pattern, so replaying the
migration is safe, and `migrations.lock` is updated.

The worker notice and the worker part of the session show them too. A change
origin carries the counts at a worker hand-back, so the accept task has them
before the run row reaches the gateway; the run row is the fallback.

## Where it shows

- The worker notice gets one line: `struggle: N failed tool calls, M repeats;
  top: <two worst, short>` when there are 3 or more failed tool calls or any
  command ran 3 or more times. Below that, nothing.
- The caller's accept step task gets the same line.
- `agent_workers_find` with `detail: full` gets the full counts, or
  `no trace`.

Thresholds are code constants, not settings: `StruggleFailedThreshold` (3),
`StruggleRepeatRuns` (3) and `StruggleTop` (2) in
`internal/protocol/struggle.go`. The owner asked for a low bar, not a knob.