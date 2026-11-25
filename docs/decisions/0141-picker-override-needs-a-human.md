# 0141 - An agent cannot override the worker picker without a human

**Date:** 2026-11-12
**Status:** Accepted.

## Context

The difficulty judge and the picker choose a worker's harness and model from
task difficulty, quota pace and track record. Agents kept pinning `harness` and
`model` on every start from rules they made up, and so overrode both. The only
brake was a soft `reminder` in the start result, and callers ignored it.

The owner's decision, 2026-11-12: human confirmation if you want to override
the model picker. It holds for every caller on every machine.

## Decision

The selection fields are `harness`, `provider`, `model`, `effort`, `not_family`
and `machine`. An agent's start that sets any of them goes ahead only when a
human confirmed it:

1. The start carries `owner_words`, a quote of the owner's request. The daemon
   finds it in what the owner typed in that session in the last 3 hours (from
   the Claude Code prompt hook, in memory only). Only a prompt whose transcript
   entry marks a human origin is kept, and every pasted block and
   system-reminder block is removed from it before it is stored or matched. The
   opening and closing tags may carry the same id; everything from the first
   opening tag to the last closing tag is dropped, including text between two
   blocks. If the entry is still missing when the five second wait ends, the
   daemon reads the transcript again when the words are needed, so an entry
   written later still counts. An entry that never appears, an unknown origin
   and a queued command without a human origin close waits as before but are
   not kept as words.
2. Otherwise the daemon holds the start and puts one ask on the Desk, with the
   caller's `reason`. `confirm` starts it with the fields, `picker` without.
   The caller is answered `accepted` at once and gets the worker notice later.

The owner's command line and TUI and the CTO are not checked. The CTO may
answer `picker` but not `confirm`. The soft reminder and its setting are gone.

## Consequences

- Harnesses without the prompt hook always use the Desk ask.
- The daemon holds a held start in memory. A restart ends it with a notice.
- The match proves the quote is in the owner's prompt, not that it names the
  fields. This is the same trust as the owner-instruction note.
- Details: [reference](../reference.md) and [security](../security.md).
