# 0111 - The chief of staff (part B1)

Date: 2026-11-09
Status: Built. Part B of [0090](0090-asks-and-the-desk.md) is the gateway and
daemon side here. The Desk screens that show the sorts and undo them are the
next part.

## The problem

Every ask waits for the owner, so the owner's queue grows with questions that
another agent could sort: duplicates, answered threads, questions for one
session that another session should answer. The owner wants a second pair of
hands that sorts the queue, and never answers for him.

## Decision

1. **A standard agent session.** The chief is an ordinary agent session with a
   charter and the tools of an agent. Its only special code is the routing of
   asks, the batch message and the sort. Its model is a setting, `chief.model`,
   with the default `codex · openai · gpt-6.1-sol · medium`.
2. **One chief, set by the owner or the CTO.** The chief is the session named
   in the owner setting `chief_session` (the same kind of row as `cto_session`).
   The owner or the CTO sets it with the gateway method `chief.set` (the
   operator method `chief_set`). A session must be a live agent session: not a
   worker, the owner, the CTO or a bot. Nothing else is needed: the chief is
   live when it is registered, online and not archived.
3. **Routing.** A new ask goes to the chief (`next = chief`) when a chief is
   live and the ask names no other next. The chief's own asks go to the owner.
   A team lead's route (decision 0100) is kept as it is.
4. **Batches.** The gateway sends the chief one message for the asks that arrive
   close together: it waits for a quiet window of 5 seconds after the newest
   ask, and never waits more than 30 seconds after the oldest. A message holds up
   to 25 asks: the id, the kind, the asker, what the ask is about, the question,
   the options, the recommendation and up to five related open asks (same
   session or same subject). A sent ask is marked, so it is never sent twice.
5. **Timeout and fallback.** An ask the chief has not sorted within 15 minutes
   (`protocol.ChiefTimeout`, a constant, not a setting) goes to the owner
   (`next = owner`). An ask waiting on a chief that is not live goes to the owner
   at once. Each one writes a Desk log line that says why. With no live chief,
   asks go to the owner as before.
6. **The sort tool, `agent_ask_sort`.** The chief calls it with one of four
   results:
   - `close`: the ask is closed; its answer reads `chief: <reason>`.
   - `merge`: the ask is closed into another open ask (`into`); the other ask's
     detail gets the line `merged: <id>`.
   - `send`: the ask waits on a session (`to`), which gets the ask as a message
     from `agent-net` that names the chief. The ask shows as "with someone".
   - `owner`: the ask goes to the owner with a `priority` (1 to 3) and a
     `why_you` line.
   Every sort needs a `reason`. Nothing is deleted. The sort writes one row of
   `ask_sorts` with the ask as it was before (`prev_next`, `prev_status`,
   `prev_answer`, `prev_answered_at`), so a later undo can put it back, and one
   Desk log line.
7. **Sort only.** The chief never answers for the owner. A sort that is not
   from the live chief, or that acts on an ask not waiting for the chief, is
   refused.
8. **History.** `ask_history` (gateway `ask.history`) returns an ask's sorts,
   oldest first, for the CTO and the TUI.

## Not in this part

- The chief session is started by hand, or by the CTO, with `session_start` and
  the model that `chief_get` returns. The daemon does not start it by itself.
- The charter and the first message of the chief are written by the orchestrator.
- `send` asks that never move are shown as "with someone" until their session
  answers (no timeout yet).

## Part 2: the Desk

The owner sees what the chief sent him, can see and undo what the chief
handled, gets a digest when he comes back, and can correct the chief with two
keys. The reference is in [reference](../reference.md#the-chief-on-the-desk-decision-0111-part-2).

1. **Undo.** `ask.undo` (owner or CTO) restores the ask from its newest sort's
   `prev_*` fields. It refuses an ask that changed since the sort, and a sort
   that is undone already. A `next` of the chief becomes the owner: the chief's
   one batch is not sent again, so putting the ask back with the chief would
   leave it waiting until the 15 minutes end. Priority and why_you are cleared.
   The undo is a new `ask_sorts` row (result `undo`) and a log line. Nothing is
   deleted.
2. **Marks.** `ask.mark` (owner or CTO) stores `didnt_need_me` or
   `should_have_asked`, with one optional line of why. The mark is a correction
   with the ask's project as area and the ask id as ref. The next batch message
   lists the newest five, so the chief reads them with each batch. The judgment
   store (`judgment_add`) is not on this base, so the marks go to the table
   `chief_corrections` for now. The TODO in `internal/gateway/chief.go` marks
   the switch to `judgment_add` when it lands.
3. **The Desk.** The header names the chief and whether it is online, and counts
   the sorts of the last 24 hours. Needs you is the next=owner list, and every
   count of "needs you" reads it. Each row shows priority and why_you. Handled
   for you lists the sorts of the last 24 hours with undo (`u`) and the mark
   `m`. `n` marks an ask on Needs you.
4. **The digest.** It opens when the owner's first key comes after an hour
   without a key (the TUI keeps the time of the last key). Its four counts come
   from the newest 500 log lines, read once, so later lines do not change it.
   The counts read the log text that the gateway writes. `x` hides it.
5. **CTO.** `chief_sorts` is a CTO read. `ask_undo` and `ask_mark` are CTO
   changes, so the CTO can act for the owner.

Choices made here, not in the brief:

- The digest counts the newest 500 lines, not all of them. A longer absence
  is counted up to that number.
- "Sorted N asks today" counts the sorts of the last 24 hours, not the local
  day.
- The reason and the session of a handled sort are on its row when they fit;
  the panel always shows them.

## Why this shape

A separate session type would need its own code paths. A registered session and
the existing routing, batch and log paths carry it, as the merger does for
delivery steps. The setting for the model is read where the chief is started, so
the owner can change it without a release.
