# Can a trained model pick workers better than the binned table?

Date: 2026-11-18. Snapshot: `gateway-2026-11-18.db` (last event 2026-11-18 03:10 UTC).
Research only. Nothing in the live picker changed.

Every number here comes from `results/tables.md`, which the study writes. To
rerun: `uv run python -m picker_ml.study /path/to/snapshot.db` (see README.md).

## The answer

**Drop the learned quality model for now. Revisit it after about 3,000 more
tasks, and only if part of them are picked at random.**

- On the most recent 22% of tasks, the trained model predicts the graded score
  about as well as the table, not clearly better. With option A's weights the
  gain in RMSE is 0.004, 95% interval -0.018 to +0.012.
- The bigger finding: **no method beats "D alone" by a clear margin.** A forecast
  that uses only the judge's D bucket and ignores the model does as well as the
  table and the trained model, or better. With option A's weights its RMSE is
  0.329, against 0.338 for the table and 0.334 for the trained model.
- So at today's volume, a model's past record in a D bucket does not predict its
  next tasks. A model's quality in one week correlates only +0.29 with the next
  week (p = 0.19, 12 cells). Telling two models apart by 0.10 of score needs
  about 150 tasks per model per band. Most cells have 5 to 40.
- This explains the owner's complaint. When the quality numbers do not
  separate models, cost decides, so the cheapest model wins. A better estimator
  cannot fix that. More evidence can, and randomized picks give it fastest.
- One part is worth keeping: the **cost model**. It beats the table's cost
  estimate. The error of log cost falls by 0.081 (95% interval 0.012 to
  0.147). On the test tasks it underestimates total spend by 21%; the table
  underestimates by 35%.

## Against option A

Option A has two parts (decision 0173 draft): a reweighted score (Part 1), and
a quality penalty `exp(alpha x (best - this))` on the table's cells (Part 2).

- **Part 1 (the reweighting) is fine to keep.** It removes the double counting.
  This study gives no reason against it.
- **Part 2 needs quality gaps between cells that hold up on later tasks. This
  data says they do not, yet.** With option A labels, the table's per-model
  cells predict the next tasks worse than D alone (RMSE 0.338 against 0.329). A
  penalty that multiplies the gaps, at alpha 6 to 8 as 0173 suggests, would
  multiply noise.
- **The trained model is no better a quality signal for Part 2.** It does not
  beat D alone either. Using it instead of the table would add complexity and
  give no evidence of better picks.

## Data and labels

- 1,491 chains (a run plus its continuations; stopped and infra runs left out).
  1,176 have a judge D and a model key. This is the picker's task set; the 0173
  replay also counts 1,176. 25 model keys. Tasks started from 2026-11-02 to
  2026-11-18.
- **The loader matches the live picker.** The study rebuilt today's table at
  each of the 51 recorded picks. For 1,014 of 1,020 candidate rows, the cell
  score is within 0.01 of the recorded one. For 1,012, the task count is the
  same.
- **Scores.** Today's score (`protocol.PickerTaskScore`) and option A's score:
  fix round 0; failed test -0.2; failed accept -0.2; failed review -0.3; defect
  -0.25 per point of severity, counted once; an incomplete task scores 0 unless
  its change merged. The auto defect for a failed merger test is skipped, as the
  gateway does. Shape: today 699 at 1, 183 at 0, the rest at 0.25, 0.5 and 0.75.
  Option A: 829 at 1, 97 at 0, 115 at 0.8, and a thin spread in between.
- **P(clean)** = complete, no failed accept, review or test, no defect (the
  gateway's `Dirty`, negated). 70.4% of tasks are clean.
- **Cost** = the session's recorded tokens at OpenRouter prices, priced with the
  same rules as `protocol.PriceBook`. 1,170 tasks have a cost.

### Label lag

| event | n | before task end | p50 days | p95 | p99 | max |
|---|---|---|---|---|---|---|
| defect | 101 | 15 | 0.07 | 1.04 | 3.07 | 5.34 |
| review | 48 | 45 | -0.01 | 0.01 | 0.05 | 0.08 |
| accept | 155 | 131 | 0.00 | 0.00 | 0.35 | 0.96 |
| test | 127 | 93 | 0.00 | 0.10 | 0.84 | 0.95 |

Rules:

- **Training labels are as of the split time.** A training task counts only the
  failed steps, defects and merges recorded before the cut. A task still running
  at the cut is left out (5 tasks). Seven training labels changed after the cut.
  A model trained at the cut could not have known them, so they stay hidden.
- **Test labels are as of the snapshot.** A test task is *mature* if it ended
  at least W = 1.0 day before the snapshot. W is the 95th percentile delay of
  the 147 events that came after their task ended, rounded up to half a day.
  207 test tasks are mature. 52 are flagged immature and left out of the main
  numbers. The tables still show "all test" columns with them included, and
  they do not change any conclusion.

### Label measurement differs by project

| group | tasks | opened a change | clean | option A |
|---|---|---|---|---|
| agent-net | 639 | 0.895 | 0.584 | 0.796 |
| other projects | 363 | 0.931 | 0.884 | 0.923 |
| project unknown (before 11-04) | 174 | 0.092 | 0.770 | 0.820 |

Tasks in agent-net are clean 58% of the time, and tasks in other projects 88%.
Most of this gap is probably stricter checks (the merger runs full packages, and
reviews and defects are filed often), not harder work. It biases the table: a
model used mostly in agent-net looks worse there. The trained model has a
project effect that absorbs this. The table cannot.

### Where the brief is stored

Message bodies were removed from the snapshot. What is left:

- `worker_starts.done`: the structured done checks. They are stored only since
  2026-11-12 (180 tasks with a D). The training period has almost none.
- `worker_assessments.reasons`: the judge's six written reasons. They exist for
  every judged task. Their length stands in for brief complexity.
- `delivery_changes.title` and `done` are written after the start, so the study
  does not use them.
- "Continues an earlier worker" (a start with a task ID): 1 task. "touches":
  0 tasks with a D. Neither can be used.

## Method

- **Time split.** The 259 most recent tasks by start (22%), from 2026-11-12
  00:55 to 2026-11-18 02:54, are the test set. Training uses the 912 tasks that
  started and ended before 2026-11-12 00:55. Tuning uses a second split inside
  training: the last 20% of training is the validation set, with its own cut
  and its own label rule.
- **Today's table.** A copy of `PickerTable.Cell`: buckets of D with width 1/3,
  cells shrunk to the bucket mean with k = 5, built only from training labels.
- **Trained model (pooled).** A GLM with Gaussian priors, fitted by maximum a
  posteriori.
  - Model effects are nested: family, then base model (family and tier, any
    version), then model key. Each level has its own intercept and its own
    slope on D (model x D). Everything is shrunk to zero.
  - A new key gets its parent's effect. gpt-6.2-astra is a new class with no
    parent, and `scores.json` has no score for it, so it gets the family prior
    only.
  - Fixed effects: D and D squared; R, S, C, U, V, K; judge confidence and
    bottleneck; judge reasons length; done checks (count, length, paths);
    benchmark score and effort. Project is a group effect.
  - Recency weight: `exp(-age / tau)`.
  - Links: fractional logit for the graded score. Papke and Wooldridge's
    quasi-likelihood gives a consistent mean for any outcome in [0, 1], with
    its lumps at 0 and 1. Logistic for P(clean). Log-normal for cost.
  - Tuning picked lambda and tau on validation. tau = 7 days won for every
    target.
- **Checks on the shape.** A two-part model, P(score > 0) x E[score | score > 0].
  An ordinal model would only add structure that the two-part model shows the
  data does not need.
- **Checks on nonlinearity.** One gradient boosting model.
- **Intervals.** Paired bootstrap over test tasks: 2,000 resamples, a fixed
  seed, 95% percentile intervals.

### Leakage

Every feature is known before the first run starts. The judge result was
written before the run started for 1,279 of 1,284 judged sessions. The other 5
are within seconds. `results/tables.md` lists when each feature becomes known.

These are left out because they are known only after the start: run seconds,
tokens, fix rounds, continuations, struggle counters, files and lines changed,
the worker's own complete flag, ratings, brief defects and change titles. Every
delivery step and every defect is also left out, because they are the label.
Start time sets the split and the recency weight only. It is never an input.

## Quality (primary target)

Mature test tasks (n = 207). The difference columns are method minus table.
Negative is better than the table.

| score | method | MAE | RMSE | calib err | dRMSE vs table [95%] |
|---|---|---|---|---|---|
| today | train mean | 0.339 | 0.402 | 0.097 | +0.001 [-0.012, +0.015] |
| today | D bucket only | 0.320 | 0.390 | 0.068 | -0.011 [-0.022, -0.000] |
| today | **table (today)** | 0.312 | 0.401 | 0.103 | - |
| today | pooled fractional logit | 0.315 | 0.387 | 0.068 | -0.014 [-0.029, +0.001] |
| today | pooled two-part | 0.317 | 0.384 | 0.051 | -0.017 [-0.033, -0.000] |
| today | pooled, no model information | 0.320 | 0.388 | 0.073 | -0.013 [-0.031, +0.006] |
| today | gradient boosting | 0.321 | 0.398 | 0.084 | -0.003 [-0.017, +0.010] |
| today | bound: same-period model form (cheats) | 0.317 | 0.383 | 0.081 | -0.018 [-0.037, -0.000] |
| option A | train mean | 0.240 | 0.335 | 0.081 | -0.004 [-0.013, +0.006] |
| option A | D bucket only | 0.226 | 0.329 | 0.075 | -0.009 [-0.018, +0.001] |
| option A | **table (today)** | 0.216 | 0.338 | 0.083 | - |
| option A | pooled fractional logit | 0.226 | 0.334 | 0.090 | -0.004 [-0.018, +0.012] |
| option A | pooled two-part | 0.224 | 0.332 | 0.063 | -0.007 [-0.020, +0.008] |
| option A | pooled, no model information | 0.232 | 0.333 | 0.079 | -0.005 [-0.022, +0.013] |
| option A | gradient boosting | 0.236 | 0.335 | 0.050 | -0.003 [-0.014, +0.009] |
| option A | bound: same-period model form (cheats) | 0.230 | 0.317 | 0.097 | -0.021 [-0.042, -0.002] |

How to read it:

- **RMSE is the score to read.** RMSE rewards the right mean, and the mean is
  what the decision rule uses. MAE rewards the median, which is 1 here, so
  MAE favors any method that predicts high. The table has the best MAE because
  it predicts high, not because it is right.
- **"Bound" is not a real method.** It adds each model's mean residual on the
  *other* test tasks of the same period (leave one out, shrunk with k = 5). It
  shows what knowing each model's current form is worth at most: about 0.02 of
  RMSE, or 3 to 8% of MSE against D alone. The real methods get none of that gain with option
  A's labels, and part of it with today's labels.
- **Calibration.**
  - The table is overconfident. In 9 of 10 option A deciles it predicts above
    what happens. In deciles 5 to 9 it says 0.91 to 0.94, and 0.81 happens on average.
  - The pooled model is lower everywhere, but it is not better ordered.
  - The test period was worse than training for every model. The realized
    option A score was 0.81, while the table predicted 0.89 and the pooled
    model 0.85.
- **By model on the test tasks.** The table predicted 0.90 for Sonnet high; it
  scored 0.83 (n 54). It predicted 0.94 for Haiku high; it scored 0.86 (n 40).
  It predicted 0.88 for DeepSeek Alibaba; it scored 0.86 (n 64). These gaps are
  within 1 to 2 standard errors.

## P(clean) (secondary target)

| method | log loss | Brier | AUC | calib err | dLogLoss vs table [95%] |
|---|---|---|---|---|---|
| train mean | 0.639 | 0.223 | 0.500 | 0.111 | -0.019 [-0.069, +0.030] |
| D bucket only | 0.612 | 0.212 | 0.648 | 0.115 | -0.046 [-0.088, -0.008] |
| **table (today)** | 0.659 | 0.230 | 0.596 | 0.149 | - |
| pooled logistic | 0.647 | 0.222 | 0.616 | 0.117 | -0.011 [-0.071, +0.048] |
| pooled, no model information | 0.632 | 0.215 | 0.656 | 0.114 | -0.027 [-0.095, +0.045] |
| gradient boosting | 0.713 | 0.244 | 0.561 | 0.163 | +0.055 [-0.011, +0.119] |
| bound: same-period model form (cheats) | 0.614 | 0.211 | 0.582 | 0.142 | -0.045 [-0.099, +0.003] |

- The table's p is worse than a constant: log loss 0.659, against 0.639 for
  the training mean.
- D alone is clearly better than the table: -0.046 [-0.088, -0.008].
- In the deciles where the table says 0.74 to 0.92, the clean rate was 0.33 to
  0.90. The table's `p_clean` is the number that drives the rescue term in
  today's expected cost, so this matters for today's rule too.

## Run cost

| method | MAE of log cost | median abs % error | MAE $ | sum predicted / sum actual |
|---|---|---|---|---|
| table (today) | 0.672 | 53% | $1.09 | 0.65 |
| pooled log-normal (mean) | 0.592 | 48% | $0.93 | 0.79 |
| pooled log-normal (median) | 0.544 | 40% | $0.95 | 0.56 |

- Pooled minus table on MAE of log cost: -0.081 [-0.147, -0.012]. This is the
  only clear win of the trained model.
- Both methods underestimate spend, because test-period tasks cost more than
  training tasks.

## Why model identity does not help yet

- **The table's per-model cells are a stable signal only for some models.**
  The study cut the data in two halves (before and after 2026-11-09 01:53). It
  compared each model-and-label cell's residual in the two halves (12 cells
  with at least 8 tasks in each half). The correlation is +0.29, weighted
  +0.31, permutation p = 0.19.
  - Some cells keep their sign: Sonnet high on normal tasks, and DeepSeek
    OpenCode on simple and hard tasks.
  - Sol low falls from +0.02 to -0.34 (n 34, then 9).
- **The sample size is too small for the gaps.** The per-task sd of option A
  is 0.31. To tell a 0.10 gap at 95% confidence and 80% power takes about 149
  tasks per model per difficulty band, and 594 for a 0.05 gap. Today a typical
  cell holds 5 to 40 tasks.
- **Each validation ablation that removes model information improves the
  fit.** Removing the model hierarchy, the model x D slopes and the benchmark
  gives better validation numbers for every target:
  - option A RMSE: 0.208 to 0.199
  - today's RMSE: 0.288 to 0.280
  - clean log loss: 0.520 to 0.498

## Ablations

The study refits the pooled model with the tuned lambda and tau and one
feature group changed. Decisions use validation; test is shown for honesty.
Lower is better. The A and today columns are RMSE; the clean column is log
loss.

| variant | A val | A test | today val | today test | clean val | clean test |
|---|---|---|---|---|---|---|
| default | 0.208 | 0.334 | 0.288 | 0.387 | 0.520 | 0.647 |
| without D | 0.209 | 0.334 | 0.288 | 0.387 | 0.520 | 0.646 |
| without judge R S C U V K | 0.208 | 0.334 | 0.287 | 0.392 | 0.521 | 0.653 |
| without judge confidence, bottleneck, judge model | 0.209 | 0.334 | 0.284 | 0.388 | 0.519 | 0.649 |
| without brief text | 0.207 | 0.334 | 0.282 | 0.387 | 0.521 | 0.647 |
| without benchmark and effort | 0.209 | 0.334 | 0.287 | 0.387 | 0.517 | 0.645 |
| without model hierarchy | 0.201 | 0.333 | 0.282 | 0.389 | 0.505 | 0.633 |
| without model x D slopes | 0.208 | 0.334 | 0.287 | 0.386 | 0.513 | 0.646 |
| without project | 0.215 | 0.329 | 0.293 | 0.382 | 0.622 | 0.645 |
| with provider and harness | 0.214 | 0.333 | 0.291 | 0.386 | 0.518 | 0.651 |
| with context (machine, concurrent workers) | 0.208 | 0.335 | 0.287 | 0.390 | 0.523 | 0.667 |
| no recency weight | 0.211 | 0.334 | 0.289 | 0.389 | 0.539 | 0.645 |
| no model information at all | **0.199** | 0.333 | **0.280** | 0.388 | **0.498** | 0.632 |
| model key only (no family or base) | 0.215 | 0.336 | 0.292 | 0.388 | 0.505 | 0.653 |
| D + model only | 0.217 | 0.329 | 0.286 | 0.387 | 0.627 | 0.647 |

- **Context (machine, concurrent workers) is dropped.** It does not improve
  held-out error, as the brief expected. Sandbox mode is the same for every
  start, so it carries no information.
- **Provider and harness are dropped.** They make validation worse.
- **Recency weighting stays.** Without it, clean log loss on validation rises
  from 0.520 to 0.539.
- **Project carries much of the signal**, mostly through label measurement (see
  above). It is in the model so that the model comparison is fair.
- **Judge dimensions and brief text add almost nothing beyond D.** The brief
  text is mostly missing before 2026-11-12, so its effect cannot be judged yet.
- **Gradient boosting is no better than the linear pooled model.** There is no
  sign of a nonlinearity the GLM misses.

## Counterfactual picks on the test tasks

- **Rule.** For each mature test task, pick the key with the lowest
  `cost + (1 - quality) x F`. Quality is E[option A score] or P(clean). F is
  the dollar cost of a failure: $0.5, $1, $2, $5 or $10.
- **Roster.** The 16 keys with at least 5 training tasks that ran in the week
  before the cut.
- **Evaluation.** Each policy is scored by both the pooled model and the table,
  so no policy is judged only by its own model.

| policy | F | top picks | E[q A] pooled | E[clean] pooled | E[cost] pooled | E[q A] table |
|---|---|---|---|---|---|---|
| logged (what ran) | - | deepseek alibaba 64, sonnet 54, haiku high 40, opus 20 | 0.850 | 0.628 | $1.33 | 0.892 |
| pooled, quality A | 1 | deepseek alibaba 136, luna 57, deepseek opencode 14 | 0.846 | 0.540 | $0.17 | 0.854 |
| pooled, quality A | 5 | deepseek alibaba 168, deepseek opencode 39 | 0.874 | 0.537 | $0.22 | 0.865 |
| pooled, P(clean) | 5 | haiku high 183, deepseek opencode 24 | 0.834 | 0.665 | $0.24 | 0.866 |
| table, quality A | 5 | deepseek alibaba 86, deepseek opencode 69, haiku high 35, luna 17 | 0.852 | 0.593 | $0.26 | 0.891 |
| table, P(clean) | 10 | sol medium 46, terra medium 41, haiku high 35, terra high 29 | 0.784 | 0.634 | $0.62 | 0.906 |

The realized logged values were: option A 0.812, clean 0.671, cost $1.68 per
task. `results/tables.md` has every F.

### What off-policy data can and cannot support

- **Every task ran on one model.** Nobody knows what another model would have
  done on the same task. Any estimate for a new rule is the model's
  extrapolation.
- **Most picks were not random.** Until 2026-11-17, callers chose models by
  hand. They likely sent tasks they worried about to stronger models, and the
  judge's D captures only part of that worry. The 51 logged picks with
  probabilities are mostly in the immature window, and the explore share was
  0.1, so inverse propensity weighting has almost nothing to work with.
- **The two evaluators disagree.** The pooled model says its own F = 5 policy
  raises quality from 0.850 to 0.874 and cuts cost by about 80%. The table says
  the same policy lowers quality from 0.892 to 0.865. The data cannot say which
  is right.
- **The agreement subset is biased.** For the tasks where a policy's pick
  equals what actually ran, realized option A is 0.81 to 1.00, by policy. Those tasks are
  a selected subset, so this number is not evidence either way.
- **What the data supports:** checks of prediction and calibration on what
  actually ran, and cost forecasts.
- **What it does not support:** a claim that any new rule raises quality, or by
  how much. That claim needs randomized picks with logged probabilities: a
  real exploration share, with the probability stored, as `pick_candidates`
  already does.

## The D 3.20 case (s_c1ce54688a069800)

- **Training.** The model was trained on the 1,166 tasks that ended before this
  start, with labels as of the start. Judge: R3 S3 C4 U3 V3 K3.
- **The table rebuilds exactly.** It gives the recorded cells: DeepSeek Alibaba
  0.605 (n 27) and Sol 0.767 (n 17).

| candidate | recorded cell | recorded p | recorded $/run | pooled q today | pooled q A | pooled p clean | pooled $/run |
|---|---|---|---|---|---|---|---|
| deepseek alibaba high (chosen) | 0.605 | 0.411 | 0.47 | 0.601 | 0.694 | 0.407 | 0.44 |
| sol codex | 0.767 | 0.734 | 1.04 | 0.505 | 0.601 | 0.433 | 2.19 |
| opus high claude | 0.743 | 0.691 | 7.03 | 0.719 | 0.814 | 0.612 | 7.39 |
| sonnet claude | 0.693 | 0.633 | 2.58 | 0.617 | 0.735 | 0.540 | 3.20 |
| haiku high claude | 0.673 | 0.272 | 0.32 | 0.594 | 0.724 | 0.508 | 0.43 |
| luna codex | 0.673 | 0.272 | 0.32 | 0.461 | 0.625 | 0.475 | 0.27 |
| terra codex | 0.686 | 0.711 | 0.69 | 0.497 | 0.489 | 0.411 | 0.75 |
| gpt-6.2-astra:high (new class, no record) | - | - | - | 0.488 | 0.570 | 0.430 | 0.86 |

The model's picks for this task:

| F | by quality A | by P(clean) |
|---|---|---|
| $0.5 to $1 | luna codex | luna codex |
| $2 | haiku high claude | luna codex |
| $5 to $10 | haiku high claude | haiku high claude |

- **The trained model does not side with Sol.** It moves Sol down toward the
  rest of the OpenAI family. Terra, with many tasks, scores lower, and Sol's 17
  tasks in this bucket are not enough to hold it apart. It also lowers the
  level for every candidate, because this is an agent-net task with a long done
  list.
- **Haiku high wins once a failure costs $2 or more.** It is about as good as
  DeepSeek Alibaba and Sonnet, and cheap.
- **Neither verdict is reliable.** The table's 0.16 gap between Sol and DeepSeek
  Alibaba is within noise. So is the trained model's reversal. The section
  above shows that neither one predicts the next tasks better than D alone.
- **gpt-6.2-astra** gets only the OpenAI family prior and its effort. There is
  no parent and no benchmark score in `scores.json`, so it lands near the
  family average.

## Recommendation

1. **Do not adopt the trained quality model.** It does not beat the table by a
   margin the data can show. Neither does the table beat D alone.
2. **Adopt it later only on a pre-registered test.** Run it after about
   3,000 more tasks. Adopt it only if it beats both the table and D alone on a
   held-out time split, with the bootstrap interval excluding zero.
   - Where 3,000 comes from: telling models apart by 0.10 at 95% and 80% power
     needs about 150 tasks per model per band. Six serious candidates across
     three bands is about 2,700 tasks. Showing a 2% gain in MSE on held-out
     data needs about 3,800 test tasks.
   - At 30 to 70 tasks a day, this is 6 to 14 weeks.
   - The learning curve gives no sign that more of the same kind of data closes
     the gap by itself. From 228 to 912 training tasks, the pooled model's gain
     over the table with option A moved between -0.008 and +0.004, with no
     trend.
3. **Make the data comparable first.** Randomize part of the picks among
   allowed models within a D band, and store the probabilities. Without that,
   3,000 more hand-chosen or cost-chosen tasks still cannot say which model is
   better. They would mostly add tasks for the cheap model.
4. **Option A.** Keep Part 1. Do not tune alpha for Part 2 on this data: the
   quality gaps it multiplies do not hold up out of time.
5. **Cost.** A pooled log-normal cost model is a candidate for the cost side
   of the rule. Replay it with `tools/pickerreplay` before any change.
6. **Today's p_clean is badly calibrated.** It is worse than a constant on the
   test tasks. Any rule that leans on `(1 - p) x rescue` inherits that error.
   A D-band clean rate would be more accurate today.

## Limits

- One snapshot covers 16 days. The test window is 6 days, and 3 of them
  (2026-11-13 to 2026-11-16) had few tasks.
- The test set holds 207 mature tasks. Intervals are wide, and the tables give
  them.
- Labels depend on how strictly each project checks work. The model includes
  project; the table does not.
- Cost comes from each session's final usage total. Six tasks have no usage.
- The benchmark lookup is a simplified copy of
  `LookupModelScoreWithDefaultEffort`: same names, nearest effort.
