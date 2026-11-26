# 0189 - A continue meets the picker's rules of today

Date: 2026-11-25
Status: Asked for by the owner on 2026-11-25, through the CTO: "we still dont
get codex workers". Amends [0162](0162-one-model-choice-path.md) and
[0188](0188-owner-paused-models.md).

## Problem

A continue of an ended worker started a new worker on the model of the old
run. The gateway checked only that the model could run: quota and, since 0188,
the pause. It never met the picker's rules of the day. After the ceiling rule
went on (2026-11-25 16:03Z), 9 of 9 continues kept their model, and 6 of them
were hard. A hard correction stayed on the model that did the first part, so a
model the picker now prefers, such as a Codex one, never got that work.

A continue already starts a fresh session of the harness: a new worktree on a
new branch made from the old branch, with the old worker's full first task. It
never carries the conversation on. Keeping the model kept nothing that a new
model would lose.

## Decision

1. When a caller continues an ended worker and names no model and no harness,
   the gateway checks the old model with the rules of today, at the level stored
   with the worker (the judge's label of the run it continues). When the caller
   adds work to the brief, the changed brief is judged once on that machine
   before the pick, and the check uses that new level and D (amended
   2026-11-26). The checks are those of an automatic start: the pause, the
   protected providers, the cell and ceiling rules, the floors, the quota and
   cooling. The code is `Gateway.repickContinue` in
   `internal/gateway/continue_repick.go`.
2. A model that still qualifies stays. The check asks the gate for the old
   model first, so a model the rules allow is not moved by the rotation's next
   step.
3. A model that no longer qualifies is replaced: the picker chooses again from
   the normal roster at that level, as for an automatic start. The rotation
   moves, the pick's rows are recorded with the new run, and the start's note
   says `continue moved from <old> to <new> at <level> (old model: <why>)`.
4. A protected provider is saved for the orchestrator. A continue moves off
   it when another model can run, and stays on it when none can.
5. A paused model is re-picked the same way. This replaces point 5 of 0188 for
   a continue with a stored level: it is no longer refused when another model
   qualifies.
6. When no other model qualifies, the old model stays if it may run, and the
   note says `continue kept <old>: no other model qualifies at <level> (old
   model: <why>)`. If it may not run, for example because it is paused, the
   continue is refused as before, and the refusal adds the same line. A
   machine with no room queues the continue as before.
7. These keep the old behaviour, with no re-pick:
   - a continue that names a model or a harness, in the owner's words or not
     (the locked checks of 0162 still apply to it);
   - a worker with no stored level (no judge result was recorded);
   - a network with no worker profiles, where no automatic start runs either;
   - a message to a running worker and the daemon's automatic continue: both
     go to the same session, so its model does not change. A continue of a
     running worker is refused by its machine ("still running"), as before;
   - a resume, which carries the harness's conversation on.

## What a move across harnesses keeps

The new worker gets the old worker's work on a new branch made from the old
branch (leftover changes are committed on the old branch first), the delivery
change ID, the task ID, the title and the full first task, after the continue
line. These are the daemon's record of the old worker, not the harness's, so
they carry over for every harness pair.

What is lost is the same for every pair, and the same as a continue on the old
model: the old session's conversation (its turns, tool results and reasoning).
Per harness, the new session also starts with the new harness's own state:

- Claude: the MCP server choice comes from the main checkout, as for any
  continue; the old session's approvals do not carry over.
- Codex: the old thread is not resumed. A message to the ended Codex worker
  (a resume) still carries its thread on.
- OpenCode and agy: a new session or conversation ID; nothing else is held by
  the harness.

A failed start of the new model loses nothing: the daemon's record of the old
worker, its worktree and its branch stay as they were, and the next continue
picks them up.

## Not changed

The exploration rules, the price rule, the scores, the floors and the settings
stay as they are. The level is the one stored with the worker. When the caller
adds work to the brief, the machine that ran the worker judges the changed
brief once before the gateway picks, and the gateway re-picks at that new level
and D; the new run records the same result. (Amended 2026-11-26 for the owner's
word through the uadd3 lead: before this, the machine judged the changed brief
only for the record, and the gateway re-picked at the stored level.)

## Open point

A first start where the owner named the model is not remembered as such. A
continue of it that names no model meets the rules like any other. The owner
can name the model on the continue to keep it. Closed by
[0191](0191-picks-keep-their-quota-read-and-the-owners-choice-holds.md): the
owner's choice is now kept and holds for a continue that names none.
