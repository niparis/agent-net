# Task difficulty from evidence

**Status:** Proposal, for the owner's approval. Nothing is built.
**Date:** 2026-11-02.

## 1. The problem

The worker picker chooses a model from one label: `trivial`, `simple`,
`normal`, `hard` or `very_complex`. Today the caller (the session that starts
the worker) picks that label, and `touches` raises it one level for `db` and
`hot-path`. On 2026-11-02 this went wrong in two ways:

- **The caller mixes up difficulty and size.** The Settings tab changed 255
  files, most of them golden screens from one repeated change. It was labeled
  `hard`; it was `normal`.
- **`touches` raises the level automatically.** Several plain tasks became
  `very_complex` only because they had a migration ("touches db: chose as
  very_complex"). `very_complex` has one eligible model, so it won every start.

The caller also has a reason to pick a label that gets the model it wants, so
any label the caller gives can be gamed.

## 2. The idea

Three steps, none of them done by the caller:

1. **An analyzer** reads the task text and the repository and reports
   **evidence**: facts with file references, no ratings.
2. **A judge** scores six dimensions from 1 to 5 against a fixed rubric, using
   only that evidence.
3. **A function** turns the six scores into the label.

The picker uses the label as today. The analyzer and the judge are cheap
models run by agent-net. Most of the intelligence is needed to find the facts,
little to score them.

## 3. The six dimensions

Scores 2 and 4 are in-between cases.

| Dimension | Evidence the analyzer must give | 1 | 3 | 5 |
|---|---|---|---|---|
| Reasoning | The closest existing code it can copy, how the task differs, new logic needed | Mechanical, a copied pattern | Some new logic | New or subtle reasoning |
| Scope | Components touched, the distinct kinds of change, which edits repeat | Local | Several distinct changes | Many independent changes |
| Coupling | Contracts, shared state and invariants affected | Isolated | Crosses established boundaries | Strongly interconnected |
| Uncertainty | Open questions, unverified assumptions, what was inspected | Fully understood | Some unknowns | Basic unknowns |
| Verification | Existing tests, how to reproduce and check the behavior | Fast deterministic tests | Integration tests needed | Hard to reproduce or check |
| Risk | Data changes, security, compatibility, reversibility | Easily reversed | Real side effects | Severe or irreversible |

Rules for the analyzer:

- **Count distinct changes, not files.** 20 files changed by one known pattern
  is scope 2, not 5.
- **Count real coupling, not technologies.** Touching the database, the API
  and the UI is not coupling by itself.
- **Every claim has evidence:** a file and line range, or what was inspected
  when it claims something is absent. Facts and guesses are marked apart.
- **A confidence for the whole profile.** Poor inspection gives low
  confidence, not high uncertainty.

## 4. The evidence profile

The analyzer returns JSON, checked against a schema:

```
{
  "reasoning":    {"closest_pattern": "...", "new_logic": "...", "evidence": ["path:lines"]},
  "scope":        {"components": [...], "distinct_changes": [...], "repeated_changes": [...]},
  "coupling":     {"contracts": [...], "invariants": [...], "evidence": [...]},
  "uncertainty":  {"open_questions": [...], "inspected": [...]},
  "verification": {"tests": [...], "needed_checks": [...], "gaps": [...]},
  "risk":         {"failures": [...], "reversibility": "...", "evidence": [...]},
  "confidence":   "low | medium | high"
}
```

## 5. The judge

The judge gets the rubric and the profile, never the repository. It returns
six scores with one line each on which evidence set the score, or
`INSUFFICIENT_EVIDENCE` with the dimension and what to inspect. On that answer
agent-net runs the analyzer once more for that dimension only. If the second
profile is still insufficient, or confidence is low, the start goes ahead at
`normal` and the start note says why.

## 6. From scores to the label

All six dimensions count the same way; risk is not a special case.

```
D = 0.20 Reasoning + 0.10 Scope + 0.20 Coupling
  + 0.15 Uncertainty + 0.15 Verification + 0.20 Risk
```

| D | Label |
|---|---|
| below 1.5 | trivial |
| 1.5 to 2.24 | simple |
| 2.25 to 2.99 | normal |
| 3.0 to 3.74 | hard |
| 3.75 and above | very_complex |

**Bottleneck rule:** a score of 5 on any dimension gives at least `hard`, so
one severe problem cannot disappear into the average.

The weights and the cut points are starting values, to be tuned from results
(section 8).

`touches` no longer changes the label. It still asks for the checks it asks
for today (for example a migration note), and the analyzer reads it as a hint
for the risk evidence.

## 7. Where it runs

- `agent_worker_start` keeps its fields, but `difficulty` is no longer given
  by the caller. A caller may still name a `model`, which skips the picker as
  today.
- Before the start, agent-net runs the analyzer as a short, read-only worker
  in the project (isolation `none`), on a cheap model, with the task text.
  Then it runs the judge (a cheap model, no tools), then the function.
- The start note shows the six scores, D, the label and any bottleneck, so the
  caller and the owner see why.
- Time and cost are capped: the analyzer has a time limit (for example 3
  minutes); over it, the start goes ahead at `normal` with a note.

## 8. Learning from results

The profile, the six scores, D and the label are stored with the worker's
record, next to the existing outcome counters (continues, fixes, rating,
defects found later). After a few dozen tasks this shows where the error is:
the evidence (the analyzer missed coupling, or counted repeated edits as
distinct), the rubric (scores that do not match outcomes), or the weights and
cut points (which can change without running any analysis again).

## 9. Build steps

1. Schema, rubric text and the scoring function with tests (simple).
2. The analyzer run: a read-only worker brief, output checked against the
   schema, a time limit (normal).
3. The judge call and the `INSUFFICIENT_EVIDENCE` retry (simple).
4. `agent_worker_start` and the picker use the label; `touches` stops raising
   it; the start note shows the scores (normal).
5. Store profiles and scores with worker records; show them in the worker's
   detail in the TUI (normal).
6. Backtest: run the analyzer and judge on the tasks of 2026-11-02 and
   compare with their outcomes (simple).
