# 0141 - One way to view a session

**Date:** 2026-11-11
**Status:** Accepted; implementation in progress.

## Context

Enter, start, resume, the CTO and sign-in use separate paths to show a session.
Some paths split the owner's terminal and leave little room for the session.
The owner approved one full-screen view and asked that the old paths be deleted.

## Decision

Use one view plan for every entry point. The session runs independently of the
view. Use the harness attach client to show it: Claude uses `claude attach`,
OpenCode uses `opencode --server ... --session ...`, and Codex uses
`codex resume --remote ...`. In a plain terminal the TUI hands over the
terminal, and ctrl+] returns. In herdr, open a full tab.

For another machine, use `herdr --remote <host>` when local herdr is available.
This carries clipboard and image paste. Never substitute ssh in that case.
herdr also provides tabs and hosts Pi, which has no attach client. Keep Pi in
its headless herdr server and show it full screen.

Delete pane splitting and the per-entry view paths and helpers replaced by the
shared view plan. This decision does not change session startup or add a way
to move a running session to the background; those are separate parts of the
redesign.
