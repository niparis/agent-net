# Model capability review

**Date:** 2026-11-04. **Status:** review only. No product code changed.
**Data:** a read-only copy of the gateway database, 2026-10-30 19:07 to
2026-11-04 20:09, 541 tasks on two machines.
**Script:** `scripts/dev/worker-data-review.py` regenerates every table here.
**Plan under review:** `docs/design/model-capability.md`.

## 1. Summary

The core of the stored plan survives. The judge's D is a real scale, and
matching a model to D is better than the old level rule. Two parts do not
survive the data.

First, the rework adjustment is noise. The split-half reliability of a model's
rework residual is 0.18, while the same test on ratings is 0.73. Rebuilding the
plan on today's data moves `gpt-6.1-sol:medium` from C 3.61 to 3.03 in two
days, a swing of 0.58, and most of that swing is one noisy term.

Second, the level cut-off spends capability without buying outcomes. Tasks just
below and just above 3.00 have the same completion, rating and defect counts,
but the model used jumps by about 12 benchmark points across that line.

The verdict is **change**, not confirm and not replace. Keep C on the D scale,
keep the fit weight, drop the rework term, recalibrate the priors against
observed ratings, widen the window, and add a small risk guard.

## 2. What the data is

| Item | Count |
| :--- | ---: |
| worker_runs rows | 747 |
| tasks (distinct sessions) | 541 |
| assessment rows | 327 |
| assessed tasks (latest row per session) | 246 |
| assessed tasks with a usable D | 214 |
| ratings | 203 |
| rating offers | 309 |
| late defects | 37 |
| delivery changes | 39 |
| delivery evidence rows | 126 |
| tasks with a delivery change | 37 |

Rating coverage is 203 of 541 tasks (38%). The caller answered 203 of 309
rating offers (66%). A rating exists only for tasks the caller chose to rate,
so it is not a random sample.

How a task is counted: `worker_runs` writes one row each time a run is
reported, so the same session appears up to nine times with the same start
time. The script folds those rows into one task per session. Outcome, model,
duration and level come from the last row. Rework counters are summed because
they are per-report increments.

Two limits matter for every number below.

- `run_seconds` is wall clock from start to hand-back. It includes queue and
  quota waits. The longest agy task shows 184 minutes of wall clock, while the
  caller's note says 38 minutes of work. Time tables are directionally useful,
  not a measure of model speed.
- No table stores tokens, cost or active model minutes. One question in the
  brief (tokens) cannot be answered from this database.

The rating is close to the ceiling: 113 of 203 ratings are 5 (56%), 69 are 4,
and only 21 are 3 or lower. That limits how much any model comparison can say.

## 3. Outcomes

### 3.1 By model and effort

Models with at least 5 tasks. Rework is continues plus fix rounds per task.
Sev is the mean severity of late defects.

| Harness | Model | Effort | Tasks | Done % | Part % | Fail % | Stop % | Rating | Rated | 5s % | Rework | Fixes | Defects | Sev | Med min | P90 min |
| :--- | :--- | :--- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| opencode | deepseek-v4.1-flash | default | 161 | 91 | 0 | 2 | 7 | 4.53 | 43 | 63 | 0.26 | 0.25 | 11 | 1.8 | 22 | 59 |
| codex | terra:high | high | 95 | 74 | 20 | 1 | 5 | 3.50 | 24 | 29 | 0.77 | 0.40 | 5 | 2.2 | 17 | 67 |
| codex | luna:xhigh | xhigh | 52 | 94 | 2 | 0 | 4 | 4.44 | 16 | 44 | 0.38 | 0.37 | 1 | 1.0 | 16 | 38 |
| codex | sol:medium | medium | 48 | 88 | 6 | 2 | 4 | 4.18 | 17 | 29 | 0.46 | 0.40 | 9 | 2.2 | 18 | 44 |
| codex | terra:max | max | 33 | 85 | 0 | 9 | 6 | 4.41 | 27 | 59 | 0.61 | 0.58 | 6 | 1.2 | 16 | 89 |
| codex | terra | default | 25 | 64 | 28 | 8 | 0 | - | 0 | - | 1.84 | 0.96 | 0 | - | 33 | 100 |
| agy | gemini-3.8-flash | medium | 18 | 83 | 0 | 6 | 11 | 4.36 | 11 | 36 | 0.67 | 0.67 | 0 | - | 20 | 37 |
| codex | sol:low | low | 14 | 86 | 14 | 0 | 0 | 4.18 | 11 | 36 | 0.21 | 0.14 | 1 | 2.0 | 11 | 46 |
| claude | opus-5-5:high | high | 14 | 93 | 0 | 7 | 0 | 4.86 | 14 | 86 | 0.07 | 0.07 | 0 | - | 27 | 37 |
| codex | sol:high | high | 12 | 83 | 17 | 0 | 0 | 4.50 | 2 | 50 | 0.42 | 0.25 | 0 | - | 17 | 29 |
| claude | sonnet-5-5:high | high | 11 | 91 | 0 | 9 | 0 | 4.91 | 11 | 91 | 0.55 | 0.55 | 2 | 1.0 | 4 | 64 |
| codex | terra:medium | medium | 10 | 100 | 0 | 0 | 0 | 4.50 | 8 | 50 | 0.40 | 0.30 | 0 | - | 5 | 17 |
| claude | opus-5-5:xhigh | xhigh | 9 | 89 | 0 | 0 | 11 | 5.00 | 8 | 100 | 0.00 | 0.00 | 1 | 3.0 | 32 | 62 |
| claude | opus | default | 7 | 100 | 0 | 0 | 0 | - | 0 | - | 0.14 | 0.14 | 0 | - | 5 | 9 |
| agy | gemini-4-argon | high | 7 | 43 | 14 | 29 | 14 | 4.80 | 5 | 80 | 0.00 | 0.00 | 0 | - | 16 | 28 |
| opencode | deepseek-flash-free | default | 5 | 40 | 0 | 60 | 0 | - | 0 | - | 0.00 | 0.00 | 0 | - | 14 | 16 |
| pi | gpt-6.1-terra | default | 5 | 40 | 60 | 0 | 0 | - | 0 | - | 0.00 | 0.00 | 0 | - | 26 | 32 |

Fail includes failed, timed out and infra. Stop is a caller stop.

The main story in this table:

- DeepSeek is the workhorse: 161 tasks, 91% complete, rating 4.53, low rework.
- Terra high is the outlier: 74% complete, 20% partial, rating 3.50, the lowest
  of any model with more than a handful of ratings. It is also the model the
  current picker uses most at hard.
- Luna is small and reliable in this window: 94% complete, rating 4.44, 1 late
  defect.
- Sol medium has 9 defects on 48 tasks (19%), the highest defect rate of the
  large models. Terra high has 5 on 95 (5%).
- Claude models rate highest, but they are protected for the owner's
  orchestrator and are not normal picker candidates.

### 3.2 By harness

| Harness | Tasks | Done % | Part % | Fail % | Rating | Rated | 5s % | Rework | Fixes | Defects | Sev | Med min | P90 min |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| agy | 31 | 61 | 6 | 23 | 4.44 | 18 | 50 | 0.39 | 0.39 | 0 | - | 15 | 36 |
| claude | 42 | 93 | 0 | 5 | 4.91 | 34 | 91 | 0.19 | 0.19 | 3 | 1.7 | 17 | 53 |
| codex | 294 | 81 | 12 | 3 | 4.17 | 108 | 43 | 0.67 | 0.44 | 23 | 1.9 | 17 | 61 |
| opencode | 169 | 88 | 0 | 5 | 4.53 | 43 | 63 | 0.25 | 0.24 | 11 | 1.8 | 21 | 55 |
| pi | 5 | 40 | 60 | 0 | - | 0 | - | 0.00 | 0.00 | 0 | - | 26 | 32 |

agy fails most (23%), but that number is 7 of 31 and includes auth and quota
failures, not only model work. Codex has the most rework and the most defects,
mostly because it carries the terra high and sol medium traffic.

### 3.3 By level

| Level | Tasks | Done % | Part % | Rating | Rated | Rework | Defects | Sev | Med min |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| trivial | 8 | 75 | 12 | 4.75 | 4 | 0.12 | 0 | - | 3 |
| simple | 45 | 82 | 4 | 4.65 | 20 | 0.38 | 2 | 1.0 | 7 |
| normal | 321 | 85 | 7 | 4.42 | 95 | 0.45 | 18 | 1.9 | 19 |
| hard | 123 | 79 | 10 | 4.21 | 66 | 0.60 | 13 | 2.1 | 19 |
| very_complex | 21 | 86 | 5 | 4.56 | 18 | 0.67 | 4 | 1.5 | 17 |

The level is the judge's label when the judge finished, otherwise the level the
task was started at. The trend is weak and not monotonic: very_complex does
better than hard. Hard has the most rework and the lowest rating.

### 3.4 By D bucket

Judged tasks only, 0.25 wide.

| D bucket | Tasks | Done % | Part % | Rating | Rated | Rework | Defects | Sev | Med min |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1.00-1.25 | 2 | 50 | 50 | - | 0 | 0.50 | 0 | - | 3 |
| 1.25-1.50 | 1 | 100 | 0 | 5.00 | 1 | 0.00 | 0 | - | 1 |
| 1.50-1.75 | 6 | 100 | 0 | 4.75 | 4 | 0.67 | 0 | - | 3 |
| 1.75-2.00 | 7 | 71 | 0 | 4.75 | 4 | 0.57 | 0 | - | 3 |
| 2.00-2.25 | 13 | 92 | 0 | 4.71 | 7 | 0.38 | 0 | - | 5 |
| 2.25-2.50 | 30 | 87 | 3 | 4.64 | 11 | 0.33 | 1 | 2.0 | 9 |
| 2.50-2.75 | 33 | 82 | 6 | 4.33 | 21 | 0.18 | 1 | 3.0 | 15 |
| 2.75-3.00 | 40 | 85 | 5 | 4.60 | 25 | 0.35 | 4 | 1.5 | 12 |
| 3.00-3.25 | 33 | 85 | 3 | 4.59 | 22 | 0.58 | 3 | 1.7 | 13 |
| 3.25-3.50 | 22 | 68 | 14 | 4.44 | 16 | 0.27 | 2 | 1.5 | 12 |
| 3.50-3.75 | 16 | 88 | 6 | 4.13 | 15 | 0.75 | 6 | 2.2 | 39 |
| 3.75-4.00 | 7 | 86 | 0 | 4.80 | 5 | 1.00 | 1 | 1.0 | 41 |
| 4.00-4.25 | 4 | 75 | 25 | 3.67 | 3 | 1.00 | 1 | 1.0 | 76 |

Defects cluster in 3.50-3.75 (6 defects in 16 tasks), and the rating there is
the lowest of the large buckets (4.13). The 3.25-3.50 bucket has the worst
completion (68%). D is not a clean monotonic risk scale.

## 4. Is the judge's D predictive?

Judged tasks: 214. Ratings on judged tasks: 134.

### 4.1 Correlations

Pearson r and Spearman rho. Rating uses rated tasks; the rest use all judged
tasks.

| Score | Rating (r/rho) | Rework (r/rho) | Minutes (r/rho) | Defects (r/rho) | Incomplete (r/rho) |
| :--- | ---: | ---: | ---: | ---: | ---: |
| D | -0.17 / -0.09 | +0.10 / +0.10 | +0.34 / +0.35 | +0.19 / +0.20 | +0.07 / +0.08 |
| Reasoning | -0.02 / +0.01 | +0.10 / +0.10 | +0.23 / +0.34 | +0.11 / +0.10 | +0.02 / +0.04 |
| Scope | -0.13 / -0.04 | +0.07 / +0.12 | +0.24 / +0.30 | +0.09 / +0.06 | -0.01 / -0.01 |
| Coupling | -0.14 / -0.08 | +0.06 / +0.08 | +0.22 / +0.30 | +0.14 / +0.14 | -0.01 / -0.01 |
| Uncertainty | -0.09 / -0.07 | -0.07 / -0.10 | +0.17 / +0.26 | +0.11 / +0.12 | -0.03 / -0.02 |
| Verification | -0.04 / +0.04 | +0.10 / +0.08 | +0.20 / +0.20 | +0.01 / -0.01 | +0.05 / +0.05 |
| Risk | -0.24 / -0.19 | +0.13 / +0.09 | +0.31 / +0.20 | +0.24 / +0.29 | +0.19 / +0.19 |

D's main signal is time (r 0.34). Its signal for rework is weak (r 0.10), for
defects weak (r 0.19), for rating weak (r -0.17), and for incomplete close to
none (r 0.07). The judge is a cost estimate more than a quality estimate.

### 4.2 The six sub-scores together

Least squares with all six scores and an intercept. Standardized betas, so the
columns compare.

| Outcome | N | R2 | R | S | C | U | V | K |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Rating | 134 | 0.07 | +0.05 | -0.01 | -0.03 | -0.08 | +0.04 | -0.23 |
| Rework | 214 | 0.05 | +0.14 | +0.03 | -0.07 | -0.17 | +0.07 | +0.13 |
| Minutes | 214 | 0.13 | +0.10 | +0.09 | -0.05 | +0.04 | +0.08 | +0.25 |
| Defects | 214 | 0.07 | +0.05 | -0.06 | +0.05 | +0.06 | -0.07 | +0.23 |
| Incomplete | 214 | 0.05 | +0.05 | -0.04 | -0.13 | -0.07 | +0.04 | +0.26 |

Which sub-scores matter:

- **Risk (K)** is the only score that moves every quality outcome in the right
  direction. It is the strongest predictor of defects (+0.23), incomplete work
  (+0.26) and rating (-0.23).
- **Reasoning (R)** drives time (+0.10) and rework (+0.14).
- **Uncertainty (U)** does not help. Its rework sign is negative (-0.17), which
  means tasks the judge found uncertain were finished with less rework in this
  window. It adds noise to a single scale.
- **Verification (V)** and **coupling (C)** add little once the others are in.
- R2 is low everywhere (0.05 to 0.13). No combination of the six scores
  explains much of the outcome variance. That is expected for six days of
  noisy, near-ceiling ratings, but it argues against fine-grained weights.

The plan puts D on one axis and ignores the split. The data says cost and
reliability are two axes: R and S move time, K moves defects and rating. A
single C can still rank cost, but it should not be sold as a quality rank.

### 4.3 Under-qualified picks

The plan's benchmark prior gives each model a capability on the D scale. Gap is
prior minus D. A gap below -0.25 is a model picked clearly below the task.

| Gap quartile | Tasks | Done % | Rating | Rated | Rework | Defects | Med min |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| -1.51 to -0.16 | 51 | 92 | 4.34 | 35 | 0.71 | 2 | 16 |
| -0.15 to +0.16 | 52 | 81 | 4.21 | 28 | 0.50 | 9 | 13 |
| +0.16 to +0.61 | 52 | 88 | 4.58 | 36 | 0.29 | 4 | 10 |
| +0.62 to +2.02 | 52 | 81 | 4.82 | 33 | 0.29 | 4 | 12 |

Picked clearly below the task: 41 of 207 (20%). Gap vs rating r +0.22, gap vs
rework r -0.23, gap vs minutes r -0.20.

Two readings:

- Under-qualification costs: the bottom quartile has 0.71 rework and rating
  4.34, against 0.29 and 4.82 in the top quartile. Matching matters.
- Over-qualification has diminishing returns: the top two quartiles have the
  same rework (0.29) and the same defect count (4). Paying for capability
  beyond about +0.6 buys nothing measurable.

That shape supports a soft weight that reaches zero around 0.6 to 0.75, not a
hard 0.5 cliff.

## 5. Does the level cut-off hurt?

### 5.1 Outcomes at the boundaries

Judged tasks within 0.12 of each label boundary.

| Boundary | Side | Tasks | Done % | Part % | Rating | Rated | Rework | Defects | Med min |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 2.25 | below | 2 | 100 | 0 | 4.50 | 2 | 0.00 | 0 | 16 |
| 2.25 | above | 20 | 90 | 0 | 4.86 | 7 | 0.15 | 0 | 7 |
| 3.00 | below | 11 | 91 | 0 | 4.71 | 7 | 0.09 | 1 | 10 |
| 3.00 | above | 21 | 86 | 0 | 4.86 | 14 | 0.38 | 1 | 13 |
| 3.75 | below | 3 | 100 | 0 | 4.33 | 3 | 0.00 | 0 | 32 |
| 3.75 | above | 5 | 80 | 0 | 4.67 | 3 | 0.60 | 1 | 34 |

At 3.00 there is no gain above the line. Completion is 5 points lower, rework
is 0.29 higher, defects are equal, and the rating difference is within noise on
7 and 14 ratings. At 3.75 the above-line tasks have more rework and lower
completion. The samples are small, but nothing here says the cut-off helps.

### 5.2 What the cut-off does to the pick

| Level | Tasks with a score | Mean score | Min | Max | Mean gap | Gap below -0.25 % |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: |
| trivial | 8 | 26.3 | 23.3 | 41.1 | +1.11 | 0 |
| simple | 45 | 31.6 | 23.3 | 50.2 | +0.63 | 12 |
| normal | 297 | 35.8 | 23.3 | 57.0 | +0.21 | 24 |
| hard | 119 | 44.4 | 23.3 | 57.0 | +0.11 | 16 |
| very_complex | 20 | 50.0 | 32.5 | 57.0 | -0.22 | 27 |

The mean benchmark score of the model used jumps by 8.6 points from normal to
hard. The pair the owner saw at the boundary differs by 12 (30 against 42.3).
D moves 0.05 across that line. The picker pays a large capability step for a
tiny change in the task.

The normal row also shows the cost of the rule: 24% of normal tasks were run by
a model more than 0.25 below the task, and 27% at very_complex. That is where
under-qualification comes from.

### 5.3 The four observations from today

| Observation | What the data shows |
| :--- | :--- |
| D 2.90 picked normal and a 30 score, D 3.00 picked hard and 42.3 | Confirmed. On 11-04, 2.95 got gemini-3.8-flash (30) and 3.00 got terra high (42.3). Both completed. The whole 0.05 step changes the model class. |
| Independent reviews found blockers in normal changes | Confirmed but small. 12 changes were reviewed. 13 reviews failed, 5 changes then passed at a new commit. The 2 reviewed normal changes had 16 and 7 open findings. 8 of 9 merged normal changes had no review. |
| An agy gemini flash worker took about 3 hours and 8 rounds on a normal task | Confirmed. The longest agy task ran 184 minutes of wall clock, 7 report rows, 8 fixes, D 3.20, rating 4. The caller's note says 38 minutes of work, so most of the wall clock was waiting. |
| Terra high handed back incomplete early on a multi-item brief | Confirmed. 19 of 95 terra high tasks ended partial (20%), the highest rate of the large models. Partial runs are shorter (median 10 min against 19 min complete) and rating 3.50. |

## 6. Reviews

### 6.1 Reviewer families

`delivery_evidence` rows with step review. All 25 are marked independent by
family.

| Reviewer family | Pass | Fail | Real fix | Withdrawn | Unresolved | Reviews |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: |
| deepseek | 6 | 6 | 1 | 0 | 2 | 12 |
| google | 5 | 2 | 2 | 0 | 0 | 7 |
| openai | 1 | 5 | 2 | 0 | 0 | 6 |

Definition: a fail is a real fix when the next review of the same change passes
at a new commit. It is withdrawn when the next review passes at the same
commit. The table counts only the last fail in a chain, so the 5 real fixes are
5 changes, not 5 findings.

- No fail was withdrawn. The reviewers did not raise false alarms that were
  later reversed.
- deepseek raised the most fails (6) and was the reviewer on the two normal
  changes with findings. Its real-fix count is low because 3 of its fails were
  followed by another fail before the change passed.
- openai found 5 fails in 6 reviews. That is a high hit rate, but the sample is
  6.

### 6.2 Changes, review and state

| Level | Changes | Reviewed | Merged | Open | Done/stopped |
| :--- | ---: | ---: | ---: | ---: | ---: |
| simple | 6 | 0 | 3 | 3 | 0 |
| normal | 18 | 2 | 9 | 7 | 2 |
| hard | 10 | 7 | 7 | 3 | 0 |
| very_complex | 3 | 3 | 3 | 0 | 0 |
| (unset) | 2 | 0 | 2 | 0 | 0 |

Normal is the hole: 18 changes, 2 reviewed, 8 of 9 merged without a review.
Hard and very_complex are mostly reviewed.

Late defects joined to a delivery change: only 4. Reviewed changes: 3 (severity
1, 1, 2). Unreviewed: 1 (severity 1). The delivery change table is younger than
most defects, so this cannot answer "did review prevent defects". It can only
say the join is too thin to test.

What it can say: independent review finds real blockers. 5 of 12 reviewed
changes needed a new commit after a fail. The two reviewed normal changes had
23 findings open across their reviews. Normal work is not free of defects, so
skipping review there is a gap in the process, not evidence that review is
unnecessary.

## 7. The picker

### 7.1 How often each model ran

| Model | Tasks | Share % |
| :--- | ---: | ---: |
| deepseek-v4.1-flash | 161 | 30 |
| terra:high | 95 | 18 |
| luna:xhigh | 52 | 10 |
| sol:medium | 48 | 9 |
| terra:max | 33 | 6 |
| terra | 25 | 5 |
| gemini-3.8-flash | 18 | 3 |
| sol:low | 14 | 3 |
| opus-5-5:high | 14 | 3 |
| sol:high | 12 | 2 |
| sonnet-5-5:high | 11 | 2 |
| terra:medium | 10 | 2 |

These are all runs, including explicit picks by the caller. Only 80 of 246
assessments carry a picker note, so the automatic share cannot be separated
from the whole.

Automatic picks recorded in the note, by level:

| Level | Model | Automatic picks |
| :--- | :--- | ---: |
| hard | terra:high | 9 |
| hard | sol:medium | 4 |
| hard | terra:max | 2 |
| normal | gemini-3.8-flash | 8 |
| normal | deepseek | 7 |
| normal | sol:low | 4 |
| normal | terra:high | 2 |
| normal | terra:medium | 2 |
| normal | sol:medium | 1 |
| simple | deepseek | 1 |
| simple | gemini-3.8-flash | 1 |
| simple | terra:low | 1 |

The automatic picker at normal already spreads over DeepSeek, gemini flash and
sol low. At hard it leans on terra high, which is the model with the worst
completion and rating in the roster.

### 7.2 Quota pace

33 picker notes carry a pace figure. Mean signed pace is -0.3 points (slightly
behind). 17 picks were made while the provider was ahead of pace, 13 while
behind, 3 exactly on pace. Rotation weight: n 42, min 0.07, median 0.74, max
2.00.

| Provider pace | Tasks | Done % | Rating | Rated | Rework | Med min |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: |
| ahead | 14 | 79 | 3.88 | 8 | 0.00 | 10 |
| behind | 13 | 85 | 4.45 | 11 | 0.00 | 7 |

The ahead-of-pace picks rated lower, but that is 8 ratings against 11. It is
too small to call a quota effect. Rework is zero on both sides, which means
these 27 tasks had no fix rounds at all. The main quota effect in the data is
indirect: the picker rotates to a weaker model when a provider is ahead, and
the weaker model shows up in the outcome tables.

### 7.3 Was a better pick available?

Rated tasks where the model had a same-level mean: 53. A different model had a
mean at least 0.5 higher on 7 of them (13%), mean gap 0.89.

All 7 are gemini-3.8-flash on normal tasks, where gemini-4-argon had a higher
mean. Argon has only 4 normal ratings, and it fails 29% of its tasks overall,
so this is not a safe alternative. With protected Claude models excluded, the
result is the same: 7 of 52, all to argon.

So the picker did not miss a clearly better unprotected model in this window.
The stronger finding is the reverse: it under-qualified 20% of judged tasks,
and those tasks cost more rework.

## 8. The stored plan on today's data

### 8.1 Capability drift

The script rebuilds C the same way as `scripts/dev/capability-backtest.py`:
my time per task, residual against the level average, `extra = sum / (n + 5)`,
`adjust = clamp(-extra / 16, -0.75, +0.75)`.

| Model | Score | Prior C | Tasks | Extra min | Adjust | C today | C in plan | Drift |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| luna:xhigh | 23.3 | 1.75 | 50 | -1.0 | +0.06 | 1.81 | 1.97 | -0.16 |
| deepseek-v4.1-flash | 32.5 | 2.44 | 144 | -0.5 | +0.03 | 2.47 | 2.37 | +0.10 |
| terra:high | 42.3 | 3.17 | 89 | -0.7 | +0.04 | 3.21 | 3.10 | +0.11 |
| sol:medium | 46.1 | 3.46 | 48 | +6.8 | -0.42 | 3.03 | 3.61 | -0.58 |
| terra:max | 51.0 | 3.83 | 31 | -1.1 | +0.07 | 3.89 | 3.73 | +0.16 |

The stored table is two days old and one model moved by 0.58. The swing comes
from 6.8 extra minutes on 48 tasks, which is a handful of fix rounds. At D 2.9
this makes sol medium the closest model (0.1 away) while DeepSeek is 0.43 away,
so the plan would pick sol medium over DeepSeek at normal. The observed ratings
at 2.7 to 3.3 go the other way: DeepSeek 4.88 (n 8), sol medium 4.17 (n 6).

### 8.2 Is the rework adjustment measuring a trait?

Split-half reliability: split each model and effort group by time, compare the
first half with the second half, correlate the group means across groups.

| Signal | Groups | Split-half r |
| :--- | ---: | ---: |
| Rework residual (min) | 11 | 0.18 |
| Rating | 9 | 0.73 |

The rework residual is mostly noise. Sol medium goes from -3.8 minutes in the
first half to +18.7 in the second. Terra high goes from -3.7 to +2.2. A model
that "costs more time than average" in one half does not do so in the next.
Ratings are stable: terra high stays at 3.5 then 3.8, opus at 5.0 then 4.7.

This is the strongest single argument against the plan as stored. The
adjustment is the part meant to make C move with evidence. The evidence it uses
does not repeat.

## 9. Verdict

**Change the plan.** Do not replace it.

What to keep:

- D from the judge on a continuous scale.
- A capability C for each model on the same scale.
- A weight that falls with distance between C and D.
- The closest-model fallback.
- The removal of the "max level or one level below" rule.

What to change:

| Parameter | Stored plan | Proposed | Evidence |
| :--- | :--- | :--- | :--- |
| Rework term | `-extra/16`, cap 0.75 | 0 in the first release | Split-half r 0.18; C drift 0.58 in 2 days; sol medium would beat DeepSeek at D 2.9 against a 0.7 rating gap |
| Outcome term | none | add a rating adjustment, shrunk `n/(n+20)`, cap 0.4, only at 20+ rated tasks | Split-half r 0.73; ratings separate terra high (3.50) from DeepSeek (4.53) |
| Prior | benchmark score only | keep the score prior but correct it with the observed gap | terra high prior 3.17 against rating 3.50 and 20% partial; DeepSeek 2.44 against 4.53 |
| Fit window | 0.5 hard cliff | 0.75, straight line to zero | 20% of tasks under-qualified; top two gap quartiles have the same rework and defects, so beyond +0.6 buys little |
| Review rule | not in the picker | independent review for normal and above | 8 of 9 merged normal changes had no review; the 2 reviewed normal changes had 23 findings |
| Risk guard | none | when K is 4 or 5, prefer a model with a low defect rate or require review | K is the strongest predictor of defects (+0.23), incomplete (+0.26) and rating (-0.23) |

Why not confirm: the stored expected table already changed, and the term that
was meant to carry evidence is not stable. Why not replace: the prior and the
fit weighting do work. Under-qualified tasks cost 0.42 more rework and 0.48
rating points, which is the problem the plan exists to solve. The judge's D is
a usable scale for cost.

Why not keep the rework term at a smaller weight: the data cannot tell a small
true effect from zero with 6 days and 214 judged tasks. Shipping a term at 0
and turning it on when the split-half test passes is the honest choice. The
plan's own checkpoint says "rerun after two weeks"; this is that rerun.

## 10. What is missing, and how to collect it

| Missing data | Why it matters | How to collect it |
| :--- | :--- | :--- |
| Tokens and cost per task | The plan matches on time, not price; a cheap model that needs two rounds may still win | Read the usage block each harness already prints and store `tokens_in`, `tokens_out`, `cost` on `worker_runs` |
| Active model minutes | Wall clock includes quota waits; the 184 min task had 38 min of work | Record the first and last tool call of the worker, or a heartbeat from the harness adapter, and store active seconds |
| Review minutes | The plan measures them but nothing stores them yet | Ship the plugin hook and the `worker_review` table already described in the plan |
| Rating coverage | 38% of tasks, self-selected; the caller rates when asked | Ask on every hand-back, or rate a fixed random sample; add a "no opinion" option to separate unrated from neutral |
| Automatic versus explicit start | The picker's share and its outcomes are mixed with caller choices | Add a `start_source` column (`picker`, `explicit`, `retry`) to `worker_runs` |
| Worker to change link | Only 4 defects join a delivery change | Store `change_id` on `worker_runs`, or the worker session on every change at creation |
| Quota snapshots | Pace is only in the picker note, one point in time | Store provider used percent, elapsed percent and pace at each start |
| A second rater | One caller rating, near the ceiling | Rate a sample twice, once by another session, and report the agreement |

Until the first four exist, do not tune the fit width or the weights further.
The next useful sample is two weeks and about 300 rated tasks, with the
split-half test as the gate.

## 11. Reproduce

```
cp ~/.local/state/agent-net/gateway/gateway.db* /tmp/gw/
python3 scripts/dev/worker-data-review.py /tmp/gw/gateway.db
```

The script opens the copy with `mode=ro`, uses only the standard library, and
prints every table above. It never reads the live database and never writes.
