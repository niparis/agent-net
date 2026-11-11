# 0137: A restart keeps the session ID

The CTO asked for this on 11 November 2026, after a live case: an orchestrator
session was quit and restarted, the restart got a new session ID, and three
running workers, their hand-backs, the caller steps of its open change and its
inbox were left on the old ID. The old record also kept the name and was
renamed to "agent-net 3" when the new one took its name.

A session ID is the key that names the work around a session. A restart, and
the owner's resume with Enter, therefore keeps it:

- When the harness resumes the same conversation under a new native ID (a
  Claude Code background copy), the daemon asks the gateway to rebind the
  record to the new handle (`session.resume_rebind`). It does this while
  session discovery is still locked, before the copy can register as a new
  session.
- When a new conversation is unavoidable, a new session record is created by
  discovery. The daemon then asks the gateway to move the old work to it
  (`session.replace`), in one transaction: the caller role of running and
  queued workers, the caller of open delivery changes and their claimed steps,
  undelivered messages and open requests, open Inbox items, the agent and repo
  binding, the lead places, and the name. The daemon also moves the caller of
  its own worker records and its queue, because a hand-back and a step task
  use the caller ID from there.

The old record is marked replaced with `sessions.replaced_by`. It is not
renamed: it keeps the conversation's name, stays offline, and shows
`replaced by <new name>`. Only a live session holds a name (decision 0024), so
the new record holds it and the old one is history. A later discovery report
of the old conversation does not rename it.

Per harness:

- Claude Code: the resume can be a copy with a new native ID, so it uses the
  rebind, and the move is the fallback when a record for the copy already
  exists.
- Codex: `thread/resume` keeps the thread ID, so the record comes back online
  with the same ID. A new thread uses the move.
- OpenCode: `--session` keeps the session ID, so the record comes back online.
  A new session uses the move.
- Pi: it cannot reopen an old session, so resume and restart refuse it and no
  move applies.