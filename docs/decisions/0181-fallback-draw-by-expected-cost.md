# 0181 - When no candidate is proven, the fallback draw favours the lowest expected cost

**Date:** 2026-11-21
**Status:** Accepted by the owner (yes, 2026-11-21).

## Context

Decision 0105 and the picker notes in `docs/model-choice.md` describe the
picker's fallback. When no candidate has three tasks in the bucket, nothing is
proven. The picker then draws among the allowed candidates with the rotation
weight of the 11-08 rule: quota pace, benchmark and record. That weight favours
the weaker, cheaper models to save the strong ones' quota.

At the hardest tasks that favours the weakest model. On 2026-11-21 a pick at
difficulty 3.70 (run k_495655dd93a53f0d) had no qualifying candidate. The draw
gave a medium model 30 percent, with an expected cost including rework of 89.
Two cheaper candidates, at 36, got 20 percent each. The Claude models, at 92 to
300, got 4 to 9 percent each.

## Decision

**a. The fallback draw follows the expected cost.** When no candidate qualifies,
the cell score in each allowed candidate's rotation weight is replaced by
`1 / expected cost`, where expected cost is the run cost plus the rework term
of decision 0161. The quota pace stays in the weight, so the weight is the pace
factor over the expected cost, which is the same as `1 / cost after pace`. A
cheaper candidate is drawn more often. The function is
`protocol.FallbackDrawWeight`.

**b. Every allowed candidate keeps a chance.** A cost of zero or an unbounded
cost is held inside fixed bounds, so no allowed candidate gets a zero weight.
Unproven models still build a record.

**c. The rest of the rotation weight stays.** The uncertain boost, the track
record, the recent success and the recent early quit apply as before.

**d. A proven candidate changes nothing.** When at least one candidate
qualifies, the rotation and the cost pick are exactly as before.

**e. The note says so.** When nothing qualifies, the start note says
`none qualified; fallback draw by expected cost` where it said
`no other qualifying`.

## Consequences

- The rule is simple: one over the cost, with no tuning knob. A steeper rule,
  such as one over the square of the cost, would favour the cheapest model more
  strongly. That is a later choice, not part of this decision.
- The fallback no longer uses the cell score as a weight. In a fallback no cell
  has three tasks, so the score is a guess from the benchmark. Rework enters the
  cost through the clean share.
- The replay tool (`tools/pickerreplay`, flag `-fallback`) reads the recorded
  fallback picks and shows how the draw would have changed.
