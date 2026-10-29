# 0008 - The TUI shows CPU, memory and conversations, and is the only reader

**Date:** 2026-10-29
**Status:** Built on branch `worktree-agent-aebf0f2ff28c64d2e`
**Context:** [0004](0004-tui-and-event-feed.md) made the TUI the live view. [0007](0007-usage-and-session-text.md) added the owner-only methods `usage`, `usage_top`, `session_text` and `counts`.

## D1 - One reader: the TUI

The owner wants one screen, not a screen and two commands. `agent-net top` and `agent-net show` are removed. The daemon methods stay, with their owner-only gate and their tests, because the TUI uses them and the phone app will later (0006, P1 and P8).

## D2 - The TUI asks for owner data in three rhythms

- `usage` and `counts` every 5 seconds, with the snapshot. Daemons report every 10 seconds, so a faster rhythm adds nothing. An older daemon or gateway turns this off after the first refusal and the screen shows `?`.
- `usage_top` for the selected session only, kept for 5 seconds.
- `session_text` only when the person opens a conversation. It follows once a second with the `newer` cursor and pages back with the `older` cursor, one request at a time.

## D3 - Data handling is in the model, drawing is in the view

`usage.go` (freshness, trees, figures) and `conv.go` (entries, cursors, paging, follow, wrapped lines) hold the logic. `view_*.go` only styles it. The phone view can reuse the conversation logic. The text is shown as the daemon sent it, with the control characters removed.

## D4 - Machine headings use the gateway's name

A session reports its own hostname, which can differ from the name the gateway has for the machine (a Mac says `Name.local`). Headings and message lines use the gateway's node name, and `(here)` is matched by node ID.

## D5 - Colour means one thing

Green is idle or healthy, amber is busy or a loaded machine, blue is a shell, red is trouble or a heavy tree (over 20 processes, 200% CPU of one core, or 4G), dim is offline, unknown or secondary. Every figure also reads without colour.
