# 0126 - The CTO keeps mouse tracking on

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
