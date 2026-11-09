# 0105 - The worker picker learns from results

Date: 2026-11-08
Status: Approved by the owner on 2026-11-08 ("go with the new worker selection
process"), built in two parts. This file is part 1: the picker and the read API.
Part 2 is the Quality subtab of Quotas. Amends [0026](0026-worker-difficulty-and-scores.md).
The number was the next free one on main when this was written. The merger may
renumber the file. The code and settings name this decision as "decision 0105";
if the file is renumbered, change those mentions too.

## Context

The picker chose by fixed levels (decision 0026). A model had a benchmark score,
the score gave it a maximum level, and a task could go to a model at its maximum
level or one below. Nothing the model did later changed that.

The live data showed the cost:

- 804 judged tasks join to their worker runs. There are 84 defects and about
  1,700 delivery step results.
- Flash's normal work averages 0.53 fix rounds, against 0.17 on simple work. The
  fixed levels never moved it.
- terra:max at D 4.0 scores 0.40 (interval 0.26 to 0.53) over 19 tasks, and still
  got very_complex work.
- The ridge fit of quality (decision 0022 and later) only nudged the rotation
  weight, and only when the owner turned `quality_weighting` on. The owner never
  did. It is not set in `owner_settings`.

The owner chose the replacement step by step on 2026-11-08, each time from tables
built on the live data: D buckets of one third, the whole data set, effort kept
separate, defects added by severity, and 90 percent intervals.

## Decision

The picker reads a table of results. A model that does worse on harder work
drifts to easier work. A model with a strong record takes harder work.

### Model key

Model plus effort, kept separate. `gpt-6.1-sol:low` and `gpt-6.1-sol:medium` are
two keys. A Codex run with no recorded effort is `<model>:default`. The provider
prefix (`opencode-go/`) stays in the key and is removed only to display it.

### Task score, 0 to 1

A run and its continuations are one task (owner decision, 2026-11-08: "continuation
should have 0 penalty, not even a small one"). The link is the task root of a
`worker_runs` row: `task_id` when the continue started a new session (it holds
the root session), otherwise `session_id`, because a continue to the same
worker session adds another row with the same session. This is the root the
rework counters use. On the live data 256 sessions have more than one row and
21 rows carry a `task_id`; no two sessions share a recorded worktree branch, so
a branch is not used as a link.

The model comes from the first counted run and D from the first valid judge
result of the root session (or of the first session of the chain that has one).
The outcome comes from the last counted run. Fix rounds add up across the runs.
Defects and failed delivery steps are read per session and count once, because
a session is in one chain only. A continuation costs nothing: there is no
continue penalty and no setting for one. A partial run that was never continued
is the last run of its chain and scores 0. Runs that ended `stopped` or `infra`
do not count.

The score of a task:

- 0 if the run did not end complete.
- Otherwise `1 - 0.25 x fix rounds - 0.25 x failed accept, review and test steps
  - 0.25 x (defect count + sum of defect severities)`, and never below 0. A
  severity 4 defect costs 1.25.

Fix rounds come from `worker_runs.fix_count`. Defects come from
`worker_defects`. A failed step is a `delivery_evidence` row with result
`fail` and step `accept`, `review` or `test`, on a change whose
`origin.session_id` is the task's session. There is no time cut. A defect
counts whenever it arrives, so the table is rebuilt when a run, a judge result, a
rating, a defect, a score or profile change, a `picker.*` setting or a failed
step is recorded (the triggers of `refreshQuality`, plus failed steps).

### Cells

Buckets are `picker.bucket_width` wide (one third), starting at D 1.0. For each
model key and bucket:

- `score = (sum of task scores + k x bucket mean) / (n + k)`, with k = 5. The
  bucket mean is over all models.
- `variance = (n x cell variance + k x bucket variance) / (n + k)`, never below
  half the variance of all tasks. A thin bucket cannot look certain.
- 90 percent interval = `score +- 1.645 x sqrt(variance / (n + k))`, clipped to
  [0, 1].
- An empty cell takes the bucket mean, with the floored variance and n = 0.

The table is built on those writes and cached. A pick reads the cache only: no
SQL per pick.

### Benchmark prior

A model with fewer than 3 tasks in a bucket has no record there. The benchmark
score sets a ceiling for it: the top of the D range of the highest level the score
meets. The ranges are the judge's label cut points: trivial below 1.5, simple
below 2.25, normal below 3.0, hard below 3.75, very_complex above. A hard model is
assumed fine up to D 3.75. Where the record is thin, the model is allowed when the
bucket starts below its ceiling.

### Pick rule

For a task with judge D (held inside its label's range, so a bottleneck that lifts
the label keeps the pick at the label's floor; a failed or low confidence judge
uses the middle of the level's range):

1. Candidates are the available profiles with quota not blocked. Quota, protected
   provider and worker floor logic is unchanged.
2. In D's bucket, `best` is the highest cell score among candidates with at least
   3 tasks there. With none, it is the bucket mean.
3. A candidate with 3 tasks or more is allowed unless the top of its interval is
   below `picker.allowed_fraction` (0.9) x best. A candidate with fewer is allowed
   by the benchmark prior.
4. Removed by [0112](0112-remove-the-overqualified-rule.md). This step left out an
   allowed candidate whose benchmark ceiling was more than one bucket above D when
   a weaker allowed candidate existed (owner decision, 2026-11-08, for thin cells).
   The number 4 is kept so the later steps keep theirs.
5. The qualifying set (owner decision, 2026-11-08: "that rotation is important. i
   like B'") contains the allowed candidates with at least 3 tasks in the
   bucket and a cell score (not the top of the interval) of at least
   `picker.allowed_fraction` (0.9) x best. The pick rotates among them with the
   smooth weighted round robin. A candidate weighs its quota pace factor (the
   existing one, with the five-point penalty per running worker) times
   `(highest benchmark among the allowed / its benchmark) ^ picker.weaker_bias`
   (default 1). A lower benchmark weighs more, so weaker proven models get more
   picks and stronger ones are not starved. A candidate with no benchmark has
   factor 1. The old cell-score, track record and uncertain-boost terms are not
   in this weight.
6. `picker.explore_share` (default 0.10): about 1 pick in 10 goes to an allowed
   thin cell (fewer than 3 tasks, allowed by its benchmark). The share is
   counted, not random: each pick adds the share to a credit kept in the
   rotation state, and a pick whose credit reaches 1 takes 1 and rotates
   among the thin cells with their own weights. The state survives a restart.
7. Fallbacks, in order: when nothing qualifies (no candidate has 3 tasks in the
   bucket), the allowed set rotates as before this change, with the cell score,
   the uncertain boost and the track terms in the weight. When no candidate is
   allowed, the level rule picks.

Removed: the fixed band (max level or one below) as the rule of eligibility, and
the "poor track" completion-only rule. Both stay inside the fallback only.
Explicit model requests are unchanged.

### Quality weighting is off for good

The cell table replaces the ridge fit as an input to the pick. The fit stays as
the analysis the Quality cards show. `quality_weighting` is unused: `quality_get`
always answers disabled, and `quality_set` is accepted and changes nothing, so an
older client keeps working.

### Pick note

`model_note` names the bucket and the chosen cell, then the qualifying set with
each rotation weight, then the thin cells open to the explore share, then every
candidate that was left out with its reason (below the qualifying score, below
the interval, above its benchmark, no record and no benchmark),
and the quota exclusions. A pick made by the explore share says
"exploration pick". The pick reads the cached table only.

### Read API

`picker_get` (owner only, backed by `GET /v1/picker`) returns, for every model key
and every bucket, the score, `lo`, `hi` and `n`, plus the bucket means, the
settings in use and each registered profile's benchmark ceiling. A model with no
task yet appears with empty cells. The TUI does not poll it; part 2 reads it.

### Settings

`picker.bucket_width` (1/3), `picker.k` (5), `picker.allowed_fraction` (0.9),
`picker.uncertain_boost` (1.5),
`picker.uncertain_n` (5), `picker.weaker_bias` (1.0) and `picker.explore_share`
(0.10). The uncertain boost applies only in the fallback rotation. They are rules of the network. The CTO can change them.
Settings that the cell rule replaced (`picker.fit_bands`, `picker.max_level_gap`,
`picker.overqualified_scale`, `picker.overqualified_margin` (removed by 0112), `picker.poor_track_threshold`,
`picker.quality_weighting`) are gone from the ledger or marked as code.

## Consequences

- Cold start. With no data at all every candidate has the same cell score. The
  weakest benchmark stays and stronger ones are left out where they are more than
  one bucket above the task, so the first picks go to the weakest adequate model
  and to models near their ceiling, then follow quota pace. A lone strong model on
  the roster is still used.
- A model with fewer than 3 tasks in a bucket is allowed up to its benchmark
  ceiling and, near that ceiling, gets the uncertain boost. That is how it earns a
  record. A strong model does not get tries on easy work.
- Part 2 moved the forecast: the Quotas Workers panel now marks the gateway's
  own preview of the current picks (`QualityPicks`), which uses the cell rule at
  the middle of each level's D range, instead of simulating the level rule. The
  Quality subtab shows the same cells the picker reads, with the verdicts of the
  same protocol function.
- Continued runs count as one task, with no continuation penalty.
- Runs that were stopped or hit an infrastructure failure are not in the table.
  A model behind an unreliable provider is not judged by those runs. Its timed
  out and failed runs still count as 0.
