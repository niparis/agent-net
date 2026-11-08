# 0094 - The CTO stops, resumes and restarts sessions

Date: 2026-11-08
Status: Accepted by the owner, through the CTO.

## The problem

The owner asked the CTO to restart the tenebrae lead, so it reloads its
agent-net tools. A fresh Claude Code process reads `~/.claude.json` again. The
CTO could not do it: `session_change` had rename, trust, policy and starts, but
no stop or resume. The owner could stop a session in the TUI and press Enter on
an offline session to resume it.

## The decision

`session_change` gets three actions: `stop`, `resume` and `restart`, each with
a `session_id`. They are added to the existing action enum, not to a new tool,
because the CTO tool schema is near its size budget.

- `stop` calls the owner's `session_stop` path with no change, so its guard
  against signalling the wrong process stays.
- `resume` calls the owner's `session_resume` path with no herdr location, so it
  runs in the background and opens no view (decision 0046). The owner opens the
  session with Enter.
- `restart` stops the session, polls the gateway until it is reported ended
  (30 second limit), then resumes it. An offline session is only resumed.

All three work on any machine through the gateway session relay. They act on
owner and agent sessions only: workers are refused (`worker_change` stops them)
and so is the CTO's own session. Pi cannot reopen an old session, so resume and
restart refuse it, and restart refuses before it stops anything.

They are immediate changes with an Inbox notice and an audit line. The undo of
`stop` is `session_resume`. `resume` and `restart` have no undo.

No new limit was added beyond these refusals.
