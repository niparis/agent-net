# 0190 - Owner scores explore, and the order behind a refused pick

Date: 2026-11-25
Status: Approved by the owner on 2026-11-25 through the CTO ("we still dont
get codex workers" and "or even gemini"). Amends
[0112](0112-remove-the-overqualified-rule.md) and
[0105](0105-worker-picker-learns-from-results.md). Changes the order behind the
pick of [0162](0162-one-model-choice-path.md).

## Context

Two small failures in the cell rule kept Codex and Gemini workers from starting.

- **C.** Explore is up only (0112) read the benchmark level and required
  exactly the task's level. A model the owner scored himself, with no record,
  such as Gemini at 60 or 55, was left out at normal and hard even when its
  score was above the task. It could never earn a record, so it never got a
  first task.
- **D.** On 2026-11-25 18:03Z at very_complex the rule chose a Codex model, the
  owner's floor refused it at the gate, and the order behind the pick offered
  Hydrogen (score 41, left out as "explore is up only") before Neon high (score
  60, in the exploration pool). The gate took Hydrogen and Neon was not taken.

## Decision

- **C.** A candidate whose score is the owner's and above 0, with no proven
  record in any bucket, joins the exploration pool at normal and hard when its
  benchmark level is at least the task's level. A model with a proven record
  keeps rule (a) of 0112, and a benchmark-only score keeps rule (b), the exact
  level. An owner score of 0 is the pause (0188) and does not join. The explore
  share does not change.
- **D.** Behind the pick the gate tries the other qualified models by the same
  cost rule, then the exploration pool of this task, then the remaining allowed
  models, then a thin cell left out with "explore is up only", then the blocked
  models. The fallback draw keeps the order it already had.
- The pick note and the rows keep saying why the next candidate ran: the note
  describes the pick the ranker made, and a row the gate skipped carries the
  gate's reason.

## Consequences

- An owner-scored Codex or Gemini model can earn a first record at normal and
  hard without changing how often the exploration share is used.
- When the gate refuses the cost winner, a model in the exploration pool is
  tried before a thin model the rule had left out of exploration.
- A benchmark-only model and every task at trivial, simple or very_complex
  keep the rule of 0112 for their exploration.
