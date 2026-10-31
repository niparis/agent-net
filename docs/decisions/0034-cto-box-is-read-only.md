# 0034 - The CTO box is read-only; Enter opens the CTO

**Date:** 2026-10-31
**Status:** Accepted.

## Context

The CTO box at the bottom of every tab was a second chat window: `tab` focused
it, letters went into its input, Enter sent them to the CTO, and `o` opened the
CTO's full harness UI. The owner asked for one rule across the TUI: Enter means
"go into it". A text input in the box teaches the opposite, because Enter there
sent a message instead of opening the CTO.

## Decision

`tab` and `C` still focus the CTO box. The box is read-only: it draws the
latest CTO output lines and one dim hint line, `enter: open the CTO · tab:
back`. Enter runs the same plan `o` ran before: `cto_enter`, the terminal
hand-over, and ctrl+] or the plan's own way back. `tab` and Esc return focus to
the tab.

The box no longer has a text input. The `o` key, the compose and send code, the
optimistic send errors and the hints for sending and for `o` are removed. What
feeds the box stays: the one-second poll, CTO events, action lines, graph
changes, scrolling and older pages. The existing `n` (new conversation after
confirmation), `u` (undo the latest graph change) and `m` (harness and model
form) shortcuts stay, because they manage the CTO rather than compose a
message. The key line and `?` help list the new Enter and tab keys.

On the Map, `c` focuses the CTO box. It no longer fills in "about PROJECT",
because there is no input to fill.

## Consequences

- Enter has one meaning on the CTO box: open the CTO. Messages are written
  inside the full CTO UI, the same way the owner answers a session.
- The box cannot send a message by accident, and it no longer shows a prompt
  that looks editable.
- The full CTO UI is the only place that runs the CTO conversation. The poll
  still keeps the box current while the owner works in another tab.
