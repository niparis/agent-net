# 0173 - How the worker picker weighs quality

Date: 2026-11-18

**Status: Proposed. Not approved. Nothing in the live picker changed.**

The owner asked for a redesign of how the worker picker weighs quality, with a
decision draft and replay results before any change. This file and
`tools/pickerreplay` are that draft and that replay. The picker keeps picking
exactly as it does today until the owner approves a change.

## The problem in the owner's words

> "our current methodology will take the cheapest model all the time even if
> it's too dumb to perform well"

> "our quality score is more granular which is good, but the calculation is
> arbitrary and bad"

The replay below finds three separate causes:

1. The allowed set compares the top of a cell's interval, `Cell.Hi`, with
   `0.9 x best quality`. That number is arbitrary, and a strong model sets the
   bar for every other model.
2. A failure costs only `(1 - p) x rescue`, and the rescue is the run cost of
   the next dearer model on a ladder ordered by price per token. The jump from
   one rung to the next is not a cost per accepted result, so expected costs
   jump oddly: Terra costs $0.69 per run but $2.05 expected, Sol $1.04 per run
   but $1.06 expected.
3. The task score `PickerTaskScore` charges a flat 0.25 for every event, counts
   a fix round twice (its tokens and its score), counts a defect twice (the
   defect and its severity), gives every incomplete task 0, and leaves 0.75 for
   a failed review.

## The case the owner named

Bucket D 3.00 to 3.33, pick for session `s_c1ce54688a069800` (D 3.20):

| profile | quality | clean p | run cost | expected cost | adjusted cost |
|---|---|---|---|---|---|
| deepseek alibaba high | 0.61 | 0.41 | $0.47 | $0.65 | $0.65 |
| sol codex | 0.77 | 0.73 | $1.04 | $1.06 | $1.70 |

DeepSeek wins on expected cost. Its cell passes the `Hi >= 0.9 x best` test, so
the stronger model is not even a candidate on cost. The "adjusted cost" divides
by the quota pace factor, which the owner keeps as it is.

## Today's method

- A task is one run and its continuations. It gets a D from the judge and a
  score from `protocol.PickerTaskScore`.
- Tasks are grouped by model key and D bucket. Each cell is shrunk toward the
  bucket mean with `k = 5`.
- A cell with at least 3 tasks is allowed when `Hi >= 0.9 x best score` in the
  bucket. A thin cell may be allowed by its benchmark ceiling.
- Among allowed cells with at least 3 tasks, the pick is the lowest
  `expected = run cost + (1 - p) x rescue`, divided by the quota pace factor.
  `p` is the shrunk share of tasks that ended clean.
- An exploration share of 0.1 lets a thin cell be tried when it is a step up.

## Option A: reweighted score plus an exponential quality penalty

Part 1 changes the task score:

- a fix round costs nothing in the score (its tokens already cost in the run)
- a failed test costs 0.2 and a failed accept 0.2
- a failed review costs 0.3
- a later defect costs 0.25, 0.5, 0.75 or 1.0 by severity, counted once
- an incomplete task scores 0 unless its change merged, in which case the
  penalties above still apply

Part 2 drops the `Hi >= 0.9 x best` gate and ranks every proven cell by

    adjusted expected cost x exp(alpha x (best quality - this quality))

`alpha = 0` is the pure cost rule with no quality term. Larger alpha pays more
for quality. The CTO's draft proposed alpha 3.

The replay applies Part 1 and Part 2 together, at alpha 0, 1, 2, 3, 4, 5, 6, 8,
10 and 12. It keeps today's quota pace factor and ignores the exploration share
(so every pick is the deterministic proven pick).

## Option B

see research/picker-ml/RESULTS.md (separate study)

## Replay check first

The tool rebuilds the picker table from the snapshot as it stood at each pick:
only runs that had ended, judge results that had arrived, and defects, failed
steps and merges that had been recorded by then. It then runs today's rule on
the recorded candidate roster.

- Match: **44 of 51 recorded picks (86%)**.
- Rebuilt cell score within 0.01 of the recorded cell: **623 of 623 (100%)**.
- Rebuilt clean share within 0.01: **409 of 466 (88%)**.
- Rebuilt run cost within $0.01: **966 of 1020 (95%)**.

The 7 mismatches are explained, and none needs a rule difference:

- 3 picks chose a model whose rebuilt cell has fewer than 3 tasks, so it is not
  a proven candidate here. The live table counted tasks the snapshot copy can no
  longer rebuild exactly at that moment, mainly a session whose continuation
  arrived later.
- 4 picks differ on cost. The live picker could price runs from the `tokens:`
  line of stored notices; the snapshot removed message bodies, and
  `worker_usage` is a final running total. A cell's clean share also moves when
  a defect is recorded after the pick.

The rule itself reproduces: every cell score matches, and 95 percent of costs
match to a cent. The replay numbers below are therefore the same rule, applied
to one consistent table.

## What each method picks over the 51 recorded picks

Mean quality is today's score, for every row, so the rows compare. Mean clean p
and expected cost are the chosen model's values at that pick.

| method | picks | mean quality | mean clean p | mean expected cost | mean adjusted cost |
|---|---|---|---|---|---|
| recorded | 51 | 0.74 | 0.70 | $1.76 | $1.44 |
| today | 51 | 0.75 | 0.71 | $1.14 | $1.08 |
| A alpha 0 | 51 | 0.71 | 0.71 | $1.18 | $1.05 |
| A alpha 1 | 51 | 0.71 | 0.71 | $1.19 | $1.05 |
| A alpha 2 | 51 | 0.72 | 0.71 | $1.19 | $1.06 |
| A alpha 3 | 51 | 0.72 | 0.72 | $1.22 | $1.06 |
| A alpha 4 | 51 | 0.73 | 0.72 | $1.23 | $1.06 |
| A alpha 5 | 51 | 0.74 | 0.73 | $1.23 | $1.07 |
| A alpha 6 | 51 | 0.75 | 0.73 | $1.30 | $1.10 |
| A alpha 8 | 51 | 0.76 | 0.76 | $1.36 | $1.13 |
| A alpha 10 | 51 | 0.76 | 0.76 | $1.41 | $1.13 |
| A alpha 12 | 51 | 0.76 | 0.76 | $1.44 | $1.14 |

"Recorded" is what the live picker actually did. "Today" is the same rule on
the rebuilt table. It is cheaper than the recorded picks because the rebuild
counts the four cost-drift picks differently; this is the 86 percent match, not
a change in the rule.

The data says three things the CTO's draft did not assume:

- **Option A at alpha 3 is worse on quality than today.** It lowers mean quality
  from 0.75 to 0.72 and raises expected cost by 7 percent.
- **Option A's reweighted score compresses the quality gap.** DeepSeek Alibaba
  rises from 0.61 to 0.74 and Sol from 0.77 to 0.86, so the gap falls from 0.16
  to 0.12. A fixed alpha 3 weighs less than the CTO's math assumed.
- **Most of the clean-p gain needs a large alpha.** Clean p reaches 0.76 only at
  alpha 8, for 19 percent more expected cost. Alpha 8 also starts picking
  DeepSeek OpenCode, whose rescue is distorted by cause 2.

Models picked, count per model:

- recorded: deepseek alibaba high 17, haiku high claude 12, haiku xhigh claude 9, deepseek opencode 4, sonnet claude 4, sonnet agy 2, terra max codex 2, opus medium claude 1
- today: deepseek alibaba high 22, haiku high claude 13, haiku xhigh claude 9, sonnet claude 5, terra max codex 2
- A alpha 0: haiku xhigh claude 20, haiku high claude 13, deepseek alibaba high 11, sonnet claude 5, terra max codex 2
- A alpha 1: haiku xhigh claude 20, haiku high claude 13, deepseek alibaba high 10, sonnet claude 5, terra max codex 2, deepseek opencode 1
- A alpha 2: haiku xhigh claude 19, haiku high claude 13, deepseek alibaba high 11, sonnet claude 5, terra max codex 2, deepseek opencode 1
- A alpha 3: haiku xhigh claude 17, haiku high claude 13, deepseek alibaba high 11, sonnet claude 5, deepseek opencode 3, terra max codex 2
- A alpha 4: haiku xhigh claude 15, haiku high claude 13, deepseek alibaba high 12, sonnet claude 5, deepseek opencode 4, terra max codex 2
- A alpha 5: deepseek alibaba high 15, haiku high claude 13, haiku xhigh claude 11, deepseek opencode 5, sonnet claude 5, terra max codex 2
- A alpha 6: deepseek alibaba high 15, haiku high claude 13, haiku xhigh claude 9, sonnet claude 7, deepseek opencode 5, terra max codex 2
- A alpha 8: deepseek alibaba high 11, deepseek opencode 11, haiku high claude 10, haiku xhigh claude 9, sonnet claude 6, sol codex 2, terra max codex 2
- A alpha 10: deepseek opencode 14, haiku high claude 10, haiku xhigh claude 9, deepseek alibaba high 8, sonnet claude 6, sol codex 2, terra max codex 2
- A alpha 12: deepseek opencode 16, haiku high claude 10, haiku xhigh claude 9, deepseek alibaba high 6, sonnet claude 6, sol codex 2, terra max codex 2

## The D 3.20 case under each option

| method | winner | quality | clean p | expected cost | adjusted | rank |
|---|---|---|---|---|---|---|
| today | deepseek alibaba high | 0.61 | 0.41 | $0.65 | $0.65 | - |
| A alpha 3 | deepseek alibaba high | 0.74 | 0.41 | $0.65 | $0.65 | 0.98 |
| A alpha 6 | deepseek alibaba high | 0.74 | 0.41 | $0.65 | $0.65 | 1.46 |
| A alpha 8 | sol codex | 0.86 | 0.73 | $1.06 | $1.70 | 1.81 |

The two profiles under today and under A at alpha 3 and 8:

| method | profile | quality | clean p | run cost | expected cost | adjusted | rank |
|---|---|---|---|---|---|---|---|
| today | deepseek alibaba high | 0.61 | 0.41 | $0.47 | $0.65 | $0.65 | - |
| today | sol codex | 0.77 | 0.73 | $1.04 | $1.06 | $1.70 | - |
| A alpha 3 | deepseek alibaba high | 0.74 | 0.41 | $0.47 | $0.65 | $0.65 | 0.98 |
| A alpha 3 | sol codex | 0.86 | 0.73 | $1.04 | $1.06 | $1.70 | 1.74 |
| A alpha 8 | deepseek alibaba high | 0.74 | 0.41 | $0.47 | $0.65 | $0.65 | 1.91 |
| A alpha 8 | sol codex | 0.86 | 0.73 | $1.04 | $1.06 | $1.70 | 1.81 |

At alpha 3 the CTO's expectation holds on the unadjusted numbers, about $1.05
against $1.06, but not after the pace factor: Sol is ahead of its quota pace,
so it counts as $1.70. Alpha 8 is the first setting that flips this case.

## Limits of this replay

- 51 recorded picks, all on one day. The table changes as the day's tasks land,
  which is why 7 picks do not match.
- The snapshot removed message bodies, so some run costs come from the final
  usage total rather than the notice the picker read then.
- The replay applies the recorded quota pace factor. It does not replay the
  exploration share; each pick is the deterministic proven pick.
- The 1,176 tasks here are chains with a D. The CTO counted 1,171 recorded
  tasks; the difference is chains the replay groups slightly differently.

## Recommendation

1. **Do not adopt option A at alpha 3.** On this data it lowers mean quality and
   makes the owner's case worse, not better, because the new score compresses
   the gap and the pace factor keeps Sol at $1.70.
2. **Fix cause 2 first, on its own.** Order the rescue ladder by expected cost
   per accepted result, or cap the rescue jump, instead of using the next rung's
   run cost on a price-per-token ladder. Cause 2 distorts today's picker and
   every alpha of option A. It is the smallest change with the widest
   effect, and it can be replayed alone.
3. **Then decide on quality.** Option A's Part 1 (the reweighted score) is worth
   keeping: it removes the double counting the owner called arbitrary. If the
   owner then wants a quality term, the replay points to alpha 6 to 8, not 3,
   and the draft should be re-run after the rescue fix. The honest trade is
   about 5 points of clean p for 19 percent more expected cost at alpha 8.
4. If the owner prefers no quality term at all, dropping the 0.9 gate alone
   (alpha 0) is defensible on cost but lowers quality. The data does not support
   that as an improvement.

None of this changes the picker today. The next step is the owner's decision on
cause 2 and on whether a quality term is worth its cost.

## How to rerun

The tool is its own Go module, so it adds nothing to the agent-net binary:

    go -C tools/pickerreplay run . -db /path/to/gateway-snapshot.db

Optional flags: `-alphas 0,3,6,8`. The snapshot must be a copy. The tool opens
it read only and never calls a gateway store, because a
store open migrates the file. It is not wired into the daemon or the gateway.
The tool's tests are run by CI with `go -C tools/pickerreplay test`.
## Replay of the opt-in rule

This replay uses build 4168d838a0 and the read-only snapshot named in the
request. The setting remains off by default. The earlier tables in this decision
are the original draft replay; this section uses the accepted opt-in build.
There are 51 picks. The current rule matches 44 recorded picks; the limitations
and seven mismatches described above still apply.

The deterministic replay keeps the recorded candidate roster and quota pace
factor. It uses the live V2 score, inherited parent cells, interval gate and
cost per accepted result. It does not simulate exploration. The recorded roster
limits which new profiles can appear: a profile absent from a pick's roster
cannot be selected for that pick. Alpha multiplies pace-adjusted cost by
`exp(alpha x (best quality - quality))`. Floor sets use alpha zero:

- A: hard 0.70, normal 0.60; no simple floor.
- B: hard 0.75, normal 0.65, simple 0.55.
- C: hard 0.60, normal 0.50; no simple floor.

The judge's D levels are trivial [1.00, 1.50), simple [1.50, 2.25), normal
[2.25, 3.00), hard [3.00, 3.75), and very_complex [3.75, 5.00]. Floors not
listed for a level are zero. Candidates below the floor are excluded from the
qualifying set. If none qualifies and meets the floor, the replay uses today's
choice and counts a fallback. Thus fallback picks can be below the floor.

Mean quality uses today's score and table for every row. Clean p is the
selected cell's estimated clean share, not an observed outcome of the replay.
Expected cost is the selected candidate's predicted cost: V2 uses cost per
accepted result; today and floor fallbacks use today's rescue rule. Averages
for the floor rows therefore mix these rules. Adjusted cost also includes the
recorded quota pace factor. Case quality is the selected rule's cell score;
the summary's quality uses today's score. Where a V2-only child has no record
in today's table, its comparison quality is today's bucket prior.

### What each method picks

| method | picks | no pick | mean quality (today's score) | mean clean p | mean expected cost | mean adjusted cost | fallbacks |
|---|---|---|---|---|---|---|---|
| today | 51 | 0 | 0.75 | 0.71 | $1.14 | $1.08 | 0 |
| V2 alpha 0 | 51 | 0 | 0.76 | 0.74 | $0.72 | $0.47 | 0 |
| V2 alpha 2 | 51 | 0 | 0.76 | 0.75 | $0.89 | $0.48 | 0 |
| V2 alpha 4 | 51 | 0 | 0.76 | 0.75 | $0.98 | $0.51 | 0 |
| V2 alpha 6 | 51 | 0 | 0.76 | 0.75 | $0.98 | $0.51 | 0 |
| V2 alpha 8 | 51 | 0 | 0.76 | 0.76 | $0.99 | $0.51 | 0 |
| V2 alpha 10 | 51 | 0 | 0.76 | 0.77 | $0.99 | $0.51 | 0 |
| floor A | 51 | 0 | 0.76 | 0.75 | $1.22 | $1.08 | 6 |
| floor B | 51 | 0 | 0.75 | 0.76 | $1.22 | $1.06 | 9 |
| floor C | 51 | 0 | 0.76 | 0.75 | $1.22 | $1.02 | 5 |

Models picked by difficulty level:

- today / trivial: deepseek alibaba high 2
- today / simple: deepseek alibaba high 3, haiku high claude 11
- today / normal: deepseek alibaba high 14, haiku high claude 2, haiku xhigh claude 9
- today / hard: deepseek alibaba high 3, sonnet claude 5, terra max codex 1
- today / very_complex: terra max codex 1
- V2 alpha 0 / trivial: deepseek alibaba high 2
- V2 alpha 0 / simple: deepseek alibaba high 1, deepseek opencode 5, haiku high claude 8
- V2 alpha 0 / normal: deepseek opencode 22, haiku high claude 1, haiku xhigh claude 2
- V2 alpha 0 / hard: deepseek opencode 3, sonnet claude 2, terra codex 3, terra max codex 1
- V2 alpha 0 / very_complex: terra max codex 1
- V2 alpha 2 / trivial: deepseek alibaba high 1, deepseek opencode 1
- V2 alpha 2 / simple: deepseek alibaba high 1, deepseek opencode 5, haiku high claude 8
- V2 alpha 2 / normal: deepseek opencode 22, haiku high claude 1, haiku xhigh claude 2
- V2 alpha 2 / hard: deepseek opencode 3, sonnet claude 4, terra codex 1, terra max codex 1
- V2 alpha 2 / very_complex: terra max codex 1
- V2 alpha 4 / trivial: deepseek alibaba high 1, deepseek opencode 1
- V2 alpha 4 / simple: deepseek alibaba high 1, deepseek opencode 7, haiku high claude 6
- V2 alpha 4 / normal: deepseek opencode 21, haiku high claude 1, haiku xhigh claude 3
- V2 alpha 4 / hard: deepseek opencode 3, sonnet claude 5, terra max codex 1
- V2 alpha 4 / very_complex: terra max codex 1
- V2 alpha 6 / trivial: deepseek alibaba high 1, deepseek opencode 1
- V2 alpha 6 / simple: deepseek alibaba high 1, deepseek opencode 7, haiku high claude 6
- V2 alpha 6 / normal: deepseek opencode 21, haiku high claude 1, haiku xhigh claude 3
- V2 alpha 6 / hard: deepseek opencode 3, sonnet claude 5, terra max codex 1
- V2 alpha 6 / very_complex: terra max codex 1
- V2 alpha 8 / trivial: deepseek alibaba high 1, deepseek opencode 1
- V2 alpha 8 / simple: deepseek alibaba high 1, deepseek opencode 8, haiku high claude 5
- V2 alpha 8 / normal: deepseek opencode 19, haiku high claude 1, haiku xhigh claude 5
- V2 alpha 8 / hard: deepseek opencode 3, sonnet claude 5, terra max codex 1
- V2 alpha 8 / very_complex: terra max codex 1
- V2 alpha 10 / trivial: deepseek alibaba high 1, deepseek opencode 1
- V2 alpha 10 / simple: deepseek alibaba high 1, deepseek opencode 9, haiku high claude 4
- V2 alpha 10 / normal: deepseek opencode 18, haiku high claude 1, haiku xhigh claude 6
- V2 alpha 10 / hard: deepseek opencode 3, sonnet claude 5, terra max codex 1
- V2 alpha 10 / very_complex: terra max codex 1
- floor A / trivial: deepseek alibaba high 2
- floor A / simple: deepseek alibaba high 1, deepseek opencode 5, haiku high claude 8
- floor A / normal: deepseek opencode 22, haiku high claude 1, haiku xhigh claude 2
- floor A / hard: sonnet claude 5, terra codex 3, terra max codex 1
- floor A / very_complex: terra max codex 1
- floor B / trivial: deepseek alibaba high 2
- floor B / simple: deepseek alibaba high 1, deepseek opencode 5, haiku high claude 8
- floor B / normal: deepseek opencode 14, haiku high claude 1, haiku xhigh claude 10
- floor B / hard: deepseek alibaba high 3, sonnet claude 5, terra max codex 1
- floor B / very_complex: terra max codex 1
- floor C / trivial: deepseek alibaba high 2
- floor C / simple: deepseek alibaba high 1, deepseek opencode 5, haiku high claude 8
- floor C / normal: deepseek opencode 22, haiku high claude 1, haiku xhigh claude 2
- floor C / hard: deepseek opencode 3, sonnet claude 5, terra max codex 1
- floor C / very_complex: terra max codex 1

### Case D 3.20

Session s_c1ce54688a069800, bucket 3.00-3.33, recorded pick: deepseek alibaba high.
A rank of 0 means not applicable for today or a fallback.

| method | winner | quality | clean p | run cost | expected cost | adjusted | rank |
|---|---|---|---|---|---|---|---|
| today | deepseek alibaba high | 0.61 | 0.41 | $0.47 | $0.65 | $0.65 | 0.00 |
| V2 alpha 0 | deepseek opencode | 0.80 | 0.68 | $0.40 | $0.58 | $0.15 | 0.15 |
| V2 alpha 2 | deepseek opencode | 0.80 | 0.68 | $0.40 | $0.58 | $0.15 | 0.17 |
| V2 alpha 4 | deepseek opencode | 0.80 | 0.68 | $0.40 | $0.58 | $0.15 | 0.19 |
| V2 alpha 6 | deepseek opencode | 0.80 | 0.68 | $0.40 | $0.58 | $0.15 | 0.22 |
| V2 alpha 8 | deepseek opencode | 0.80 | 0.68 | $0.40 | $0.58 | $0.15 | 0.26 |
| V2 alpha 10 | deepseek opencode | 0.80 | 0.68 | $0.40 | $0.58 | $0.15 | 0.29 |
| floor A | terra codex | 0.79 | 0.71 | $0.69 | $0.98 | $1.57 | 1.57 |
| floor B (fallback) | deepseek alibaba high | 0.61 | 0.41 | $0.47 | $0.65 | $0.65 | 0.00 |
| floor C | deepseek opencode | 0.80 | 0.68 | $0.40 | $0.58 | $0.15 | 0.15 |


The [picker-ml study](https://github.com/niparis/agent-net/blob/e994482e67171659fd42815fd7a55be3cac6e68d/research/picker-ml/RESULTS.md)
found that per-model quality gaps did not hold up on later tasks: D alone
predicted as well as the model-specific table. Alpha and floors therefore act
on noisy estimated gaps. The linked historical copy is used because the study
is not present on this branch.
