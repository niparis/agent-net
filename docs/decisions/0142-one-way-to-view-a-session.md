# 0142 - One way to view a session

**Date:** 2026-11-11
**Status:** Accepted; implementation in progress. "In herdr, open a full tab"
is replaced by decision 0154: the TUI hands over its own herdr pane, and the
daemon hosts only in its own herdr.

## Context

Enter, start, resume, the CTO and sign-in use separate paths to show a session.
Some paths split the owner's terminal and leave little room for the session.
The owner approved one full-screen view and asked that the old paths be deleted.

## Decision

Use one view plan for every entry point: session enter, the CTO, a sign-in ask,
and the plan a start returns. The session runs independently of the view. Use
the harness client to show it: Claude uses `claude attach` for a background job
and `claude --resume <id>` for an interactive session with no job ID, OpenCode
uses `opencode --server ... --session ...`, and Codex uses
`codex resume --remote ...`. Sign-in runs its allowlisted login command. In a
plain terminal the TUI hands over the terminal, and ctrl+] returns. In herdr,
open a full tab. A worker never opens a view by itself.

For another machine, use `herdr --remote <host>` when local herdr is available.
This carries clipboard and image paste. Never substitute ssh in that case.
When herdr is not available, the same command runs in the TUI terminal. herdr
also provides tabs and hosts Pi, which has no attach client. Keep Pi in its
headless herdr server and show it full screen.

Delete pane splitting and the per-entry view paths and helpers replaced by the
shared view plan. This decision does not change session startup or add a way
to move a running session to the background; those are separate parts of the
redesign.

An OpenCode session discovered from a standalone CLI may have no owner service
record. In that case the view uses `opencode --session <id>`; sessions started
through the owner service use its address and password for attachment.
