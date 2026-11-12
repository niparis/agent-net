# 0146 - Worker briefs are structured fields, and the project sets the bar

**Date:** 2026-11-12
**Status:** Part (a) built. Parts (b) and (c) are separate changes.

## Context

The owner approved the design on 2026-11-12 ("ok i think we've reached a
design i like lets go"), after asking how a tech lead would know to follow it.
Decision 0145 retires the six old brief format sections: an analysis of 1,399
past briefs found they do not predict outcomes. The problem was not the
wording of a free-text `task`. Each caller wrote the brief differently. The
caller's guessed fix read as the task, "done" was usually "these tests pass",
and the grading line did not match what the reviewer checked.

Two kinds of rework came from this. A guessed fix was wrong in two fix-round
briefs ("run fresh migrations in one transaction" and "queue the start"). Two
fix-round briefs led Claude workers to commit in another worker's worktree.
The analysis also found that a narrow test command ended bad 44.5% of the
time, against 24.6% for the wider check, and that an unclear branch reference
gave 4 times the defects on 42 briefs.

The structure must hold for every caller in every project by construction,
not by memory. Every lead starts workers through `agent_worker_start`, so the
tool and the daemon are the place to make it hold.

## Decision

### (a) The brief is named fields

`agent_worker_start` takes these fields:

- `problem` (required for a new start): what is wrong or wanted, in the
  requester's words, with who asked.
- `evidence`: a list of facts the caller already has, one per line.
- `done` (required with `problem`): the outcomes the worker can check itself,
  one line each.
- `hypothesis` (optional): the caller's guess. The brief renders it as
  "Hypothesis (yours to drop if wrong)", so it does not read as an order.
- `scope` (optional): what not to touch and the known traps.
- `task` (optional): the old free-text field, now extra notes only.

The daemon refuses a call with no brief at all, a call that has any
structured field but no `problem`, and one that has `problem` but no `done`.
The message names the missing field. A call with only `task` and none of the
new fields keeps working during the change: it renders as the old task text,
so existing callers and older machines do not break. This is the migration
path, not a second format. The tool description and the guidance written to
agent instruction files say the same fields, so a caller does not have to
remember them.

The daemon builds the brief text from the fields in one fixed order under the
existing `Task:` header: Problem, Evidence, Done, Hypothesis, Scope, Notes.
Every worker sees the same shape from every caller and in every project,
whatever the caller wrote. A continue renders the new brief as the "Also:"
part after the old worker's first task. A fix round is a continue, so it gets
the same shape. When a continue does not repeat `done`, the new start inherits
the old start's list.

The `done` list is stored with the start in the gateway. At a complete
hand-back the gateway carries it onto the delivery change, so
`agent_changes_find` shows it. The accept and review step tasks repeat it as
"definition of done (check each): ...". Grading equals the brief. A queued
start and a continued worker use the same text; the machine that sends the
task renders the same fields.

### (b) Each project has a quality bar

The gateway stores a short list of rules per repository. The project's lead
drafts the first list from its AGENTS.md, through the same draft request flow
as charter drafts (decision 0088), and the owner accepts or rejects it. Every
worker brief header for that project carries the bar, and every accept and
review task for that project repeats it. The bar is advice about quality, not
a new approval gate.

### (c) The bar grows from the work

A failed accept or review finding must cite a bar rule or propose a new one.
When the same proposed rule appears on two changes, the gateway sends the
project's lead a draft request to add it. When the merger's suite fails a test
that the hand-back check did not run, the gateway records the changed-path to
failing-test pair. On a repeat it sends the lead a draft to widen the check or
add a bar rule. No rule enters the bar without the owner's answer.

## Consequences

- A caller can still send a free-text `task`. It is notes, not the whole
  brief, and the worker still gets the fixed header.
- The gateway fills `done` from the start when an older daemon's hand-back
  does not send it. A submit from a session, with no worker start, has none.
- `done` is a new column on `worker_starts` and on `delivery_changes`, added
  by a replay-safe migration. Older rows read as an empty list.
- Nothing in this part checks the content of `problem`, `evidence`, `done`,
  `hypothesis` or `scope` beyond the two required fields. The overlap check is
  unchanged. Decision 0145 retires the old six format checks in a parallel
  change; until that lands, they still run on this branch. This change does
  not add to them.
- Parts (b) and (c) add storage, draft requests and injection points only. They
  build on this shape and do not change it.