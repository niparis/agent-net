# 0188 - A model the owner scores 0 is paused

Date: 2026-11-25
Status: Asked for by the owner on 2026-11-25: he paused a model with a score of
0, and the picker still used it. Amends [0162](0162-one-model-choice-path.md)
and [0105](0105-worker-picker-learns-from-results.md).

## Problem

The owner gave a model a score of 0 to stop its use. The score only lowered the
model's benchmark. The cell rule of 0105 allows a model by its record whatever
its score, and at the trivial level a score of 0 meets the minimum, so the
picker kept choosing it: by record, by exploration, in the allowed draw and in
the level rule. A role list, a continue and a resume did not read the scores at
all.

## Decision

1. An owner score of exactly 0 pauses the model. Only a score the owner set
   counts. A model with no owner score is not paused, and neither is a
   benchmark score of 0. One function decides it:
   `protocol.WorkerModelPaused`.
2. `Gateway.Choose`, the one choice path of 0162, leaves a paused model out
   before the ranker. No rule, draw or exploration can then choose it. Every
   caller of `Choose` gets this: the automatic start, a model an agent names,
   the difficulty judge, the role lists, continue, resume, the preview and the
   backtest. This is the one exception to "the ranker removes no candidate" in
   0162.
3. The paused model shows as a skip with the reason "paused by the owner". The
   start's note and a refusal name it. The pick rows of a cell pick get a row
   for it with that reason.
4. The owner can still name a paused model for one start: on his command line
   or TUI, or in his own words that an agent quotes and its daemon finds
   (`owner_words`). The daemon marks such a start with `owner_selected`. The
   start then runs as before the pause. A start of the CTO is not the owner's
   own words.
5. A continue or a resume of a worker whose model is paused is refused with the
   reason. The refusal comes before the machine is asked, so the worker keeps
   its record, its worktree, its branch and its change. A continue that names
   another model goes ahead. The gateway checks the model of the worker's last
   run: that is the model the machine starts again. The session's own model is
   what the harness said, and OpenCode says none.
6. A worker whose model is paused gets no automatic continue. Its partial
   hand-back or idle notice goes to the caller at once. The daemon reads the
   owner's scores from the gateway for this; a failed read is no pause.

## Not changed

The scores, the settings, the minimums, the quota rules, the floors, the
protected providers and the exploration share stay as they are. A model that
is not paused is chosen as before.
