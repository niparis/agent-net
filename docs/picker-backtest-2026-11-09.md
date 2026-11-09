# Picker backtest: without the overqualified rule (2026-11-09)

This compares three versions of the picker:

1. **With the rule**: B-prime with the overqualified rule (commit `aee0ad986`, the
   base of this branch).
2. **Without it, explore anywhere**: the rule removed, and the explore share going
   to any thin cell the benchmark allows (the first commit of this branch).
3. **Up-only explore**: the rule removed, and the explore share going only to a
   thin cell for which the task is a step up (owner rule, "explore is up only").
   This is the version this branch ends with. See decision
   [0112](decisions/0112-remove-the-overqualified-rule.md).

The earlier B-prime backtest was `docs/picker-backtest-2026-11-08.md` on the
B-prime branch.

## Method

- Data: a read-only copy of the live gateway database, made with
  `sqlite3 .backup` on 2026-11-09 at about 01:22 UTC (1323 worker runs, the last
  ended at 01:21). The live database was not written. All three versions ran on
  copies of this one file, so the numbers compare.
- Tasks: the last 20 distinct sessions by the end of their last run. D is the
  first valid judge result of the session, the one the table scores the task
  under. All tasks have a judge result.
- Candidates: the 17 registered worker profiles, all treated as available. No
  quota, no running-worker penalty.
- Picks: the real `ChooseWorkerModel` run 1000 times from a fresh rotation for
  each task and each column. A share is the part of the 1000 picks a profile got.
  The task rows use the task's label. The column rows use the middle of the
  bucket and the label of that D.
- All versions ran the same test (`TestPickerBacktest` in
  `internal/gateway/picker_backtest_test.go`). To repeat it: copy the database
  with `sqlite3 .backup`, then run
  `AGENT_NET_PICKER_BACKTEST_DB=<copy> go test ./internal/gateway -run '^TestPickerBacktest$' -v`.

## The up-only rule

The explore share (`picker.explore_share`, 10 percent) may send a task only to a
thin cell (fewer than 3 tasks in the bucket) for which the task is a step up. A
thin cell qualifies when either:

- (a) it has a proven record in a lower bucket (3 tasks or more and a cell score
  of at least 0.9 x the best cell there) and none in this bucket or above; or
- (b) it has no proven record anywhere and its benchmark level
  (`WorkerMaximumLevel`) is exactly the task's level.

Any other thin cell stays allowed but is left out of exploration, and the note
says `left out (explore is up only)`. The main qualifying rotation and "above its
benchmark" are unchanged.

## Result in short

- **Trivial and simple work explores only among cheap models or models stretching
  up.** At D 1.00 nobody is a step up, so no exploration happens and the 10
  percent stays with the proven models (haiku high 52, deepseek 48). With
  explore anywhere, 15 other models, including opus agy, sol high and terra max,
  shared that 10 percent. At 1.67 only terra-low is explored, as with the rule.
  Columns 1.33 and 2.00 to 2.67 are the same as with the rule.
- **The 4.00 column still explores opus, argon and sol high.** Very_complex is
  their own level, so each gets about 3.3 percent. With the rule, opus and argon
  were out of this column. Columns 4.33 and 4.67 do not change: nobody has a
  record and the four models the benchmark allows get 25 percent each.
- **Models proven at normal stretch up to hard.** At 3.00, sol low (5 percent)
  and terra medium (5 percent) are explored. At 3.33, argon agy and sonnet xhigh
  get 5 percent each. At 3.67, models proven lower (sonnet, terra, sol) and the
  very_complex models (opus, argon, sol high) share the 10 percent, 1.5 to 1.9
  percent each.
- **Where the rule explores less than before:** at D 1.00 luna codex (n2) and
  terra-low (n2) were explored (luna 10 percent) and now are not. They have no
  proven record and their benchmark level is above trivial, so a trivial task is
  a step down for them. At D 3.00 sonnet agy and haiku xhigh are no longer
  explored: they have no proven record and a benchmark level above hard, so a
  hard task is below their own level. If the owner wants cheap models like luna
  tried on the lowest work, that is a change to rule (b).
- The main rotation is the same as with the rule in every column. The explore
  share is no longer spread over many strong, expensive models on easy work, so
  the cost note in the first version of this document no longer applies.

### Per task (last 20 tasks)

| # | D | Label | Actual model | With the rule | Without it, explore anywhere | Up-only explore | Up-only vs with the rule |
|---|---|---|---|---|---|---|---|
| 1 | 1.85 | simple | claude-sonnet-5-5:high | luna codex 36%, haiku high claude 28%, deepseek opencode 26%, terra-low 10% | luna codex 36%, haiku high claude 28%, deepseek opencode 26% + 12 explore (10%) | luna codex 36%, haiku high claude 28%, deepseek opencode 26%, terra-low 10% | none |
| 2 | 2.00 | simple | claude-haiku-5-5:high | haiku high claude 24%, deepseek opencode 22%, sol low codex 20%, terra medium codex 19%, sol codex 15% | haiku high claude 22%, deepseek opencode 20%, sol low codex 18%, terra medium codex 17%, sol codex 14% + 7 explore (10%) | haiku high claude 24%, deepseek opencode 22%, sol low codex 20%, terra medium codex 19%, sol codex 15% | none |
| 3 | 2.40 | normal | claude-sonnet-5-5:high | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18% + 6 explore (10%) | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | none |
| 4 | 1.00 | trivial | claude-haiku-5-5:high | haiku high claude 47%, deepseek opencode 43%, luna codex 10% | haiku high claude 47%, deepseek opencode 43% + 15 explore (10%) | haiku high claude 52%, deepseek opencode 48% | no longer picked: luna codex |
| 5 | 3.60 | hard | claude-sonnet-5-5:xhigh | sonnet claude 90%, sonnet xhigh claude 10% | sonnet claude 90% + 4 explore (10%) | sonnet claude 90%, argon agy 5%, sonnet xhigh claude 5% | now picked: argon agy |
| 6 | 3.00 | hard | opencode-go/deepseek-v4.1-flash | deepseek opencode 38%, sol codex 27%, argon agy 25% + 4 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25% + 6 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25%, sol low codex 5% + 1 explore (5%) | no longer picked: haiku xhigh claude, sonnet agy |
| 7 | 2.85 | normal | gemini-3.8-flash-medium | deepseek opencode 21%, sol low codex 20%, terra medium codex 18%, sonnet claude 16%, terra codex 16%, sonnet agy 10% | deepseek opencode 21%, sol low codex 20%, terra medium codex 18%, sonnet claude 16%, terra codex 16% + 6 explore (10%) | deepseek opencode 21%, sol low codex 20%, terra medium codex 18%, sonnet claude 16%, terra codex 16%, sonnet agy 10% | none |
| 8 | 2.55 | normal | claude-haiku-5-5:xhigh | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18% + 6 explore (10%) | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | none |
| 9 | 1.95 | simple | claude-haiku-5-5:high | luna codex 36%, haiku high claude 28%, deepseek opencode 26%, terra-low 10% | luna codex 36%, haiku high claude 28%, deepseek opencode 26% + 12 explore (10%) | luna codex 36%, haiku high claude 28%, deepseek opencode 26%, terra-low 10% | none |
| 10 | 3.00 | hard | claude-sonnet-5-5:high | deepseek opencode 38%, sol codex 27%, argon agy 25% + 4 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25% + 6 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25%, sol low codex 5% + 1 explore (5%) | no longer picked: haiku xhigh claude, sonnet agy |
| 11 | 1.45 | trivial | opencode-go/deepseek-v4.1-flash | luna codex 40%, terra-low 32%, deepseek opencode 28% | luna codex 36%, terra-low 28%, deepseek opencode 26% + 14 explore (10%) | luna codex 40%, terra-low 32%, deepseek opencode 28% | none |
| 12 | 2.45 | normal | claude-haiku-5-5:xhigh | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18% + 6 explore (10%) | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | none |
| 13 | 2.15 | simple | claude-sonnet-5-5:high | haiku high claude 24%, deepseek opencode 22%, sol low codex 20%, terra medium codex 19%, sol codex 15% | haiku high claude 22%, deepseek opencode 20%, sol low codex 18%, terra medium codex 17%, sol codex 14% + 7 explore (10%) | haiku high claude 24%, deepseek opencode 22%, sol low codex 20%, terra medium codex 19%, sol codex 15% | none |
| 14 | 3.00 | hard | claude-haiku-5-5:xhigh | deepseek opencode 38%, sol codex 27%, argon agy 25% + 4 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25% + 6 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25%, sol low codex 5% + 1 explore (5%) | no longer picked: haiku xhigh claude, sonnet agy |
| 15 | 2.50 | normal | gpt-6.1-terra:medium | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18% + 6 explore (10%) | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | none |
| 16 | 3.00 | hard | claude-haiku-5-5:xhigh | deepseek opencode 38%, sol codex 27%, argon agy 25% + 4 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25% + 6 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25%, sol low codex 5% + 1 explore (5%) | no longer picked: haiku xhigh claude, sonnet agy |
| 17 | 3.60 | hard | gpt-6.1-terra:high | sonnet claude 90%, sonnet xhigh claude 10% | sonnet claude 90% + 4 explore (10%) | sonnet claude 90%, argon agy 5%, sonnet xhigh claude 5% | now picked: argon agy |
| 18 | 2.15 | simple | claude-sonnet-5-5:high | haiku high claude 24%, deepseek opencode 22%, sol low codex 20%, terra medium codex 19%, sol codex 15% | haiku high claude 22%, deepseek opencode 20%, sol low codex 18%, terra medium codex 17%, sol codex 14% + 7 explore (10%) | haiku high claude 24%, deepseek opencode 22%, sol low codex 20%, terra medium codex 19%, sol codex 15% | none |
| 19 | 2.50 | normal | gemini-3.8-flash-medium | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18% + 6 explore (10%) | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | none |
| 20 | 3.15 | hard | opencode-go/deepseek-v4.1-flash | deepseek opencode 38%, sol codex 27%, argon agy 25% + 4 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25% + 6 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25%, sol low codex 5% + 1 explore (5%) | no longer picked: haiku xhigh claude, sonnet agy |

### Per column (D 1.00 to 4.67)

Models that can be picked in each column, with the share of 1000 picks from a fresh rotation (no quota). "N explore" means N models that each get under 5 percent.

| Column | Label | With the rule | Without it, explore anywhere | Up-only explore | Up-only vs with the rule |
|---|---|---|---|---|---|
| 1.00 | trivial | haiku high claude 47%, deepseek opencode 43%, luna codex 10% | haiku high claude 47%, deepseek opencode 43% + 15 explore (10%) | haiku high claude 52%, deepseek opencode 48% | no longer picked: luna codex |
| 1.33 | trivial | luna codex 40%, terra-low 32%, deepseek opencode 28% | luna codex 36%, terra-low 28%, deepseek opencode 26% + 14 explore (10%) | luna codex 40%, terra-low 32%, deepseek opencode 28% | none |
| 1.67 | simple | luna codex 36%, haiku high claude 28%, deepseek opencode 26%, terra-low 10% | luna codex 36%, haiku high claude 28%, deepseek opencode 26% + 12 explore (10%) | luna codex 36%, haiku high claude 28%, deepseek opencode 26%, terra-low 10% | none |
| 2.00 | simple | haiku high claude 24%, deepseek opencode 22%, sol low codex 20%, terra medium codex 19%, sol codex 15% | haiku high claude 22%, deepseek opencode 20%, sol low codex 18%, terra medium codex 17%, sol codex 14% + 7 explore (10%) | haiku high claude 24%, deepseek opencode 22%, sol low codex 20%, terra medium codex 19%, sol codex 15% | none |
| 2.33 | normal | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18% + 6 explore (10%) | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | none |
| 2.67 | normal | deepseek opencode 21%, sol low codex 20%, terra medium codex 18%, sonnet claude 16%, terra codex 16%, sonnet agy 10% | deepseek opencode 21%, sol low codex 20%, terra medium codex 18%, sonnet claude 16%, terra codex 16% + 6 explore (10%) | deepseek opencode 21%, sol low codex 20%, terra medium codex 18%, sonnet claude 16%, terra codex 16%, sonnet agy 10% | none |
| 3.00 | hard | deepseek opencode 38%, sol codex 27%, argon agy 25% + 4 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25% + 6 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25%, sol low codex 5% + 1 explore (5%) | no longer picked: haiku xhigh claude, sonnet agy |
| 3.33 | hard | sonnet claude 90%, sonnet xhigh claude 10% | sonnet claude 90% + 4 explore (10%) | sonnet claude 90%, argon agy 5%, sonnet xhigh claude 5% | now picked: argon agy |
| 3.67 | very_complex | terra max codex 90% + 4 explore (10%) | terra max codex 90% + 7 explore (10%) | terra max codex 90% + 6 explore (10%) | now picked: argon agy, opus agy, sol high codex; no longer picked: sonnet xhigh claude |
| 4.00 | very_complex | terra max codex 90%, sol high codex 10% | terra max codex 90% + 3 explore (10%) | terra max codex 90% + 3 explore (10%) | now picked: argon agy, opus agy |
| 4.33 | very_complex | argon agy 25%, opus agy 25%, sol high codex 25%, terra max codex 25% | argon agy 25%, opus agy 25%, sol high codex 25%, terra max codex 25% | argon agy 25%, opus agy 25%, sol high codex 25%, terra max codex 25% | none |
| 4.67 | very_complex | argon agy 25%, opus agy 25%, sol high codex 25%, terra max codex 25% | argon agy 25%, opus agy 25%, sol high codex 25%, terra max codex 25% | argon agy 25%, opus agy 25%, sol high codex 25%, terra max codex 25% | none |

## Notes

- "Picked" here means picked at least once in 1000 picks with no quota. In the
  real picker, quota and the running-worker penalty remove some of these.
- When no thin cell is a step up, the explore share is not used for that pick:
  the credit does not build up, and the proven models keep the 10 percent.
- Rows in the per-task table with the same D and label give the same result.
  The tasks are listed as they happened, newest first.
