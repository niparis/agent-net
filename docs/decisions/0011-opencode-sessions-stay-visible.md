# 0011 - OpenCode sessions stay visible

**Date:** 2026-10-30
**Status:** Built
**Context:** The owner saw every OpenCode session offline or dormant, and said the removal was far too aggressive. Earlier, dormant (60 minutes quiet) was added to collapse old sessions that the OpenCode service knows but no window shows (three rows for one project).

## What was wrong

- **A plugin reload dropped sessions.** OpenCode hot-loads the plugin file. An agent-net upgrade rewrites it, so the old instance is disposed and releases all its links. The new instance registered only sessions updated in the last 60 minutes. Every session quiet for longer went offline (status stays `dormant` or `idle`, ONLINE false) until someone typed in it. Reproduced end to end with a throwaway gateway, daemon and `opencode serve`: after the file was rewritten, the dormant session turned offline.
- **A daemon restart was not a cause.** The same run restarted the daemon: the link reconnected and the session came back online.
- **60 minutes hid open sessions.** OpenCode 2.2.0 has no signal for "a window has this session open" (see [opencode.md](../opencode.md)), so a session left open over a meeting went dormant and left the TUI.

## D1 - A reload keeps every session

The shared hub remembers the sessions that a disposed instance released (`released`, with their last activity). The next instance registers them again, however old. `session.deleted` and `session.archived` forget them.

## D2 - Discovery looks back 7 days

At plugin load the newest 50 top-level sessions updated in the last 7 days register (before: 60 minutes). This also repairs sessions lost by the old plugin version on the upgrade to this one.

## D3 - Dormant after 24 hours

Dormant means no activity for 24 hours (before: 60 minutes). The extra collapse of history is kept, but a session that someone used today stays in the TUI. Sessions between 1 and 7 days old register as dormant and are hidden by default (`o` in the TUI, `--dormant` in `agent-net list`). A better signal would be a client list from OpenCode; if it appears, dormant should use it.
