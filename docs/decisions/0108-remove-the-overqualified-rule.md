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
- The explore share is up only (owner rule, 2026-11-09, after reading the first
  backtest). `picker.explore_share` may send a task only to a thin cell (fewer
  than 3 tasks in the bucket) for which the task is a step up. A thin cell
  qualifies when either (a) it has a proven record in a lower bucket (3 tasks or
  more, cell score at least `picker.allowed_fraction` x the best cell there) and
  none in this bucket or above, or (b) it has no proven record anywhere and its
  benchmark level (`WorkerMaximumLevel`) is exactly the task's level. Every other
  thin cell stays allowed but is left out of exploration, and the note says
  `left out (explore is up only)`. When no thin cell qualifies, the explore
  credit does not build up. The qualifying rotation and "above its benchmark"
  are unchanged. When no model qualifies and the allowed set rotates as a
  fallback, thin cells still take part in that rotation.
- Remove the "overqualified" reason from pick notes, the Quality sub-tab verdict
  text and the docs. The Quality sub-tab settings line no longer shows a margin.

## Consequences

- Without the up-only rule, the 10 percent explore share spread over every thin
  cell the benchmark allowed, so strong models such as opus, sol high and terra
  max got about 0.7 percent each of the picks on trivial work. With it, trivial
  and simple work explores only among cheap models or models stretching up from
  a lower proven level. The three-way comparison is in
  [picker-backtest-2026-11-09.md](../picker-backtest-2026-11-09.md).
- In the 4.00 column opus, argon and sol high are still explored, because
  very_complex is their own level. Models proven at normal stretch up to hard.
- In columns with no records, such as D 4.33 and above, every model the
  benchmark allows at that level can be picked and can earn a record.
- A cheap model with no proven record and a benchmark above the task, such as
  luna at D 1.00, is not explored there. A later change to rule (b) can allow it.
