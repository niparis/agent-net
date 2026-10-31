# 0032 - Enter enters the session; conversation view removed

**Date:** 2026-10-31
**Status:** Accepted.

## Context

Enter and `c` opened a read-only view of a session's prompts, replies and
tool calls. The key `e` could enter the session itself. The owner decided that
a read-only view has no point when the session can be entered.

## Decision

On the Sessions tab, Enter runs the same plan as `e`: `session_enter`, the
terminal hand-over, and ctrl+] or the plan's own way back. This covers the
table rows and the Needs you rows. Enter on a waiting session enters it and
opens no compose form: the owner answers inside the session. `e` stays as an
alias. `m` still opens the compose form to send a message without entering.
When the plan has no argv, Enter shows the plan's note in the result line, as
`e` does.

The conversation view is removed: the `c` key, its model state and rendering,
its tests and goldens, and the TUI's `session_text` call. The Messages tab keeps
its conversation threads, which are a different feature. The operator
`session_text` method stays because the CTO parity tools and the phone view use
it.
