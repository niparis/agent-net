# 0128 - The CTO keeps mouse tracking on

**Date:** 2026-11-09
**Status:** Accepted.

## Context

The owner could not read long CTO replies. Scrolling in the CTO session worked
for a while, then stopped, with no CTO start or stop in the daemon log. Every
other Claude session scrolled fine.

The CTO is a Claude Code background session. Claude Code runs it in the
fullscreen view, which keeps its own scrollback. The wheel scrolls that view
only when the session asks the terminal for mouse events.

Since 2026-11-01 the CTO's launch settings had `CLAUDE_CODE_DISABLE_MOUSE=1`
(decision 0046 also assumed the attach client could switch it back on). These
checks ran with Claude Code 2.1.321, a temporary HOME, a dummy API key and a
closed local base URL, so no model call was made:

- With `CLAUDE_CODE_DISABLE_MOUSE=1` in the settings `env`, the session never
  turns on mouse tracking. Without it, the session turns on modes 1000, 1002,
  1003 and 1006. The `tui` setting does not change this.
- The variable is read inside the session. A `claude attach` client with the
  variable set still receives the mouse modes. The environment of the
  `claude --bg` command does not reach the session when Claude's background
  service is already running. The service's own environment does reach every
  session it starts later. A service started by a launch that had the variable
  set therefore turns the wheel off for sessions that have no setting of their
  own.
- A settings `env` value of `0` (or empty) turns mouse tracking on, even when
  the service's environment has `1`.
- With tracking off and a long conversation open, wheel events that a terminal
  turns into arrow keys do nothing in the normal view. The same arrow keys
  scroll the transcript view (ctrl+o). That is why scrolling seemed to work
  by itself and then stop.

## Decision

The CTO's launch settings set `CLAUDE_CODE_DISABLE_MOUSE=0`. The CTO launch
command drops the variable from its environment, so it cannot turn the wheel
off for sessions that Claude's service starts later. The CTO's attach plan
carries no mouse variable. No new setting is added.

Workers and owner sessions keep `CLAUDE_CODE_DISABLE_MOUSE=1`. The same checks
show that for them the variable decides nothing on the attach side, and that
the wheel works in them only when the service's environment is clean. That is a
separate question for the owner.

A CTO that is already running keeps its old environment. It is started again
the next time it starts, for example when its model changes or after a stop.

## Note (2026-11-09)

The owner chose mouse tracking on for every Claude session agent-net starts,
not only the CTO: "scrolling is now quite hard - it's basically scrolling the
history window" in a normal session, while the CTO scrolled fine.

Managed sessions and Claude workers now put `CLAUDE_CODE_DISABLE_MOUSE=0` in
their launch settings, the same way the CTO does. The launch command drops the
variable from its environment, and the attach plan no longer carries it. The
wheel scrolls the conversation in every session. Selecting text then needs
Shift while dragging (Option on some terminals), because a plain drag is a
mouse event for the session.

## Note (2026-11-11) - one mechanism at the user level

The owner asked for one mechanism, not a fix per window or per machine:

1. agent-net owns the wheel choice: the daemon writes
   `CLAUDE_CODE_DISABLE_MOUSE=0` to `env` in the user's Claude settings
   (`~/.claude/settings.json`, or `$CLAUDE_CONFIG_DIR/settings.json`). It
   merges only that key, keeps every other key and the file mode, writes
   through a temp file and a rename, and writes again when the value drifts.
   Setup writes it on a new machine. Uninstall removes it, and only while its
   value is still `"0"`. A file that is not valid JSON is never overwritten; it
   is reported.
2. The per-launch `0` that the v0.5.16 change added to managed sessions and
   workers is gone: `claudeMouseOn`, the worker settings entry and the launch
   command filter. The user setting is the one mechanism. The CTO keeps its
   own launch settings exception, which take priority.
3. The daemon reports a live Claude session whose inherited environment still
   turns the wheel off, at most once per session per day, in the owner TUI and
   the Desk log: "<session> on <machine>: wheel off (mouse tracking disabled);
   it scrolls after its next restart". The check reads the process environment
   from `/proc/<pid>/environ` on Linux and `sysctl kern.procargs2` on macOS,
   and compares the file's write time with the process start time, because
   Claude Code reads the user setting only at start. It is a report, not a gate.
4. A Claude background service (`claude daemon run`) that carries `1` is
   reported once. The daemon stops it with the supported
   `claude daemon stop --any` only when Claude's fresh roster lists no live
   session, so the next launch starts a clean service. Otherwise it waits and
   reports once. It never kills or restarts a service with live sessions.

The wheel behaves the same in every session however it was started, and the
owner has nothing to do per window; a running session picks the setting up at
its next restart.
