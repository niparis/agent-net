# 0108 - Remove the overqualified rule from the worker picker

Date: 2026-11-09
Status: Approved by the owner on 2026-11-09 ("1. Ship B' then do 2-3"). Amends
[0105](0105-worker-picker-learns-from-results.md).

## Context

Step 4 of the cell rule in 0105 left out an allowed model when its benchmark
ceiling was more than one bucket above the task's D and a weaker allowed model
existed. A model with 3 tasks or more escaped when its cell score was within
`picker.overqualified_margin` (0.25) of the best. A model with fewer than 3 tasks
had no escape.

Two live failures:

- `claude-opus-5-5` (AA 51.7, ceiling D 5.0) was cut in the 4.00 column, the
  hardest one with real work. With no tasks there it could never earn a record.
- At D 1.85, sonnet high (0.74, n3) passed the exception and was chosen. Haiku
  xhigh (n1), sol high (n2) and terra medium (n2) were cut only because their
  cells were thin. Strong models with a mediocre record got in, and thin ones
  were cut.

The B-prime rotation (0105 step 5) already tilts work toward weaker models with
`picker.weaker_bias`, and the qualifying rule (cell score at least 0.9 x best,
with 3 tasks or more) keeps low scorers out. The hard cut is no longer needed.

## Decision

- Remove the over-qualification step from `PickerBucketVerdicts`. A model with 3
  tasks or more is allowed unless the top of its interval is under
  `picker.allowed_fraction` x best. A model with fewer is allowed when its
  benchmark ceiling is above the bucket's start.
- Keep "above its benchmark": a thin model whose ceiling is below the task stays
  out.
- Remove the setting `picker.overqualified_margin`. Row 72 of the settings ledger
  keeps `picker.allowed_fraction`. No stored value of the removed key existed in
  the live gateway.
- Remove the "overqualified" reason from pick notes, the Quality sub-tab verdict
  text and the docs. The Quality sub-tab settings line no longer shows a margin.

## Consequences

- The explore share (10 percent) now spreads over every thin cell the benchmark
  allows, so on easy work each strong model gets about 0.5 to 1 percent of the
  picks instead of none. The weaker bias still weighs the cheaper thin models
  higher inside that share. The backtest is in
  [picker-backtest-2026-11-09.md](../picker-backtest-2026-11-09.md).
- In columns with no records, such as D 4.33 and above, every model the
  benchmark allows can be picked and can earn a record.
