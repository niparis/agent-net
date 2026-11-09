# Picker backtest: without the overqualified rule (2026-11-09)

This compares B-prime with the overqualified rule (commit `aee0ad986`, the base of
this branch) and B-prime without it (this branch, decision
[0108](decisions/0108-remove-the-overqualified-rule.md)). The earlier B-prime
backtest was `docs/picker-backtest-2026-11-08.md` on the B-prime branch.

## Method

- Data: a read-only copy of the live gateway database, made with
  `sqlite3 .backup` on 2026-11-09 at about 01:22 UTC (1323 worker runs, the last
  ended at 01:21). The live database was not written.
- Tasks: the last 20 distinct sessions by the end of their last run. D is the
  first valid judge result of the session, the one the table scores the task
  under. All tasks have a judge result.
- Candidates: the 17 registered worker profiles, all treated as available. No
  quota, no running-worker penalty.
- Picks: the real `ChooseWorkerModel` run 1000 times from a fresh rotation for
  each task and each column. A share is the part of the 1000 picks a profile got.
  The task rows use the task's label. The column rows use the middle of the
  bucket and the label of that D.
- Both versions ran the same test (`TestPickerBacktest` in
  `internal/gateway/picker_backtest_test.go`) on the same copy of the database
  (two copies, one per version). To repeat it: copy the database with
  `sqlite3 .backup`, then run
  `AGENT_NET_PICKER_BACKTEST_DB=<copy> go test ./internal/gateway -run '^TestPickerBacktest$' -v`.

## Result in short

- The proven models stay the same in every column, and the weaker bias still
  gives weaker proven models more of the work. Their shares fall by up to 10
  points in columns that had no thin cell open before (for example luna 40 to 36
  percent at 1.33), because the explore share now always has a target there.
- The explore share stays at 10 percent. With the rule it went to the one or two
  thin cells that were not cut (luna at 1.00, terra-low at 1.67, sonnet agy at
  2.33 and 2.67, sonnet xhigh at 3.33, sol high at 4.00). Without it, it spreads
  over every thin cell the benchmark allows: 14 or 15 more models on trivial
  work, 12 at D 1.67, 6 or 7 in the middle, 3 or 4 at the top. On trivial work
  each of them gets about 0.7 percent.
- Opus agy is now pickable in every column. With the rule it was out of the 1.00
  to 4.00 columns, including the 4.00 column (hardest column with real work). It
  now gets 3.3 percent there, as one of three thin cells that share the explore
  share (argon agy, opus agy, sol high). It cannot win the main rotation until it
  has 3 tasks in the column, by design, but it can now earn them. In 4.33 and
  4.67 nobody has a record, and the four models allowed by their benchmark get
  25 percent each, as before.
- Task rows (last 20 tasks): the tasks at D 3.00 to 3.15 now also try opus agy and
  sol high; at D 3.60 they try argon, opus and sol high; at D 1.0 to 2.15 they
  try 7 to 14 more models, each under 3 percent.
- Cost to watch: on trivial work about 10 percent of the picks go to thin cells
  in total, and most of those models are strong and expensive (opus agy, sol
  high, terra max). That is the explore share working as designed. If it is too
  much, lower `picker.explore_share` or raise `picker.weaker_bias`. Neither was
  changed here.

### Per task (last 20 tasks)

| # | D | Label | Actual model | B-prime with the rule | B-prime without it | Candidates that changed |
|---|---|---|---|---|---|---|
| 1 | 1.85 | simple | claude-sonnet-5-5:high | luna codex 36%, haiku high claude 28%, deepseek opencode 26%, terra-low 10% | luna codex 36%, haiku high claude 28%, deepseek opencode 26% + 12 explore (10%) | now picked: argon agy, flash agy, haiku xhigh claude, opus agy, sol codex, sol high codex, sonnet agy, sonnet xhigh claude, terra codex, terra max codex, terra medium codex |
| 2 | 2.00 | simple | claude-haiku-5-5:high | haiku high claude 24%, deepseek opencode 22%, sol low codex 20%, terra medium codex 19%, sol codex 15% | haiku high claude 22%, deepseek opencode 20%, sol low codex 18%, terra medium codex 17%, sol codex 14% + 7 explore (10%) | now picked: argon agy, opus agy, sol high codex, sonnet agy, sonnet xhigh claude, terra codex, terra max codex |
| 3 | 2.40 | normal | claude-sonnet-5-5:high | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18% + 6 explore (10%) | now picked: argon agy, opus agy, sol high codex, sonnet xhigh claude, terra max codex |
| 4 | 1.00 | trivial | claude-haiku-5-5:high | haiku high claude 47%, deepseek opencode 43%, luna codex 10% | haiku high claude 47%, deepseek opencode 43% + 15 explore (10%) | now picked: argon agy, flash agy, haiku xhigh claude, opus agy, sol codex, sol high codex, sol low codex, sonnet agy, sonnet claude, sonnet xhigh claude, terra codex, terra max codex, terra medium codex, terra-low |
| 5 | 3.60 | hard | claude-sonnet-5-5:xhigh | sonnet claude 90%, sonnet xhigh claude 10% | sonnet claude 90% + 4 explore (10%) | now picked: argon agy, opus agy, sol high codex |
| 6 | 3.00 | hard | opencode-go/deepseek-v4.1-flash | deepseek opencode 38%, sol codex 27%, argon agy 25% + 4 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25% + 6 explore (10%) | now picked: opus agy, sol high codex |
| 7 | 2.85 | normal | gemini-3.8-flash-medium | deepseek opencode 21%, sol low codex 20%, terra medium codex 18%, sonnet claude 16%, terra codex 16%, sonnet agy 10% | deepseek opencode 21%, sol low codex 20%, terra medium codex 18%, sonnet claude 16%, terra codex 16% + 6 explore (10%) | now picked: argon agy, opus agy, sol high codex, sonnet xhigh claude, terra max codex |
| 8 | 2.55 | normal | claude-haiku-5-5:xhigh | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18% + 6 explore (10%) | now picked: argon agy, opus agy, sol high codex, sonnet xhigh claude, terra max codex |
| 9 | 1.95 | simple | claude-haiku-5-5:high | luna codex 36%, haiku high claude 28%, deepseek opencode 26%, terra-low 10% | luna codex 36%, haiku high claude 28%, deepseek opencode 26% + 12 explore (10%) | now picked: argon agy, flash agy, haiku xhigh claude, opus agy, sol codex, sol high codex, sonnet agy, sonnet xhigh claude, terra codex, terra max codex, terra medium codex |
| 10 | 3.00 | hard | claude-sonnet-5-5:high | deepseek opencode 38%, sol codex 27%, argon agy 25% + 4 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25% + 6 explore (10%) | now picked: opus agy, sol high codex |
| 11 | 1.45 | trivial | opencode-go/deepseek-v4.1-flash | luna codex 40%, terra-low 32%, deepseek opencode 28% | luna codex 36%, terra-low 28%, deepseek opencode 26% + 14 explore (10%) | now picked: argon agy, flash agy, haiku high claude, haiku xhigh claude, opus agy, sol codex, sol high codex, sol low codex, sonnet agy, sonnet claude, sonnet xhigh claude, terra codex, terra max codex, terra medium codex |
| 12 | 2.45 | normal | claude-haiku-5-5:xhigh | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18% + 6 explore (10%) | now picked: argon agy, opus agy, sol high codex, sonnet xhigh claude, terra max codex |
| 13 | 2.15 | simple | claude-sonnet-5-5:high | haiku high claude 24%, deepseek opencode 22%, sol low codex 20%, terra medium codex 19%, sol codex 15% | haiku high claude 22%, deepseek opencode 20%, sol low codex 18%, terra medium codex 17%, sol codex 14% + 7 explore (10%) | now picked: argon agy, opus agy, sol high codex, sonnet agy, sonnet xhigh claude, terra codex, terra max codex |
| 14 | 3.00 | hard | claude-haiku-5-5:xhigh | deepseek opencode 38%, sol codex 27%, argon agy 25% + 4 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25% + 6 explore (10%) | now picked: opus agy, sol high codex |
| 15 | 2.50 | normal | gpt-6.1-terra:medium | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18% + 6 explore (10%) | now picked: argon agy, opus agy, sol high codex, sonnet xhigh claude, terra max codex |
| 16 | 3.00 | hard | claude-haiku-5-5:xhigh | deepseek opencode 38%, sol codex 27%, argon agy 25% + 4 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25% + 6 explore (10%) | now picked: opus agy, sol high codex |
| 17 | 3.60 | hard | gpt-6.1-terra:high | sonnet claude 90%, sonnet xhigh claude 10% | sonnet claude 90% + 4 explore (10%) | now picked: argon agy, opus agy, sol high codex |
| 18 | 2.15 | simple | claude-sonnet-5-5:high | haiku high claude 24%, deepseek opencode 22%, sol low codex 20%, terra medium codex 19%, sol codex 15% | haiku high claude 22%, deepseek opencode 20%, sol low codex 18%, terra medium codex 17%, sol codex 14% + 7 explore (10%) | now picked: argon agy, opus agy, sol high codex, sonnet agy, sonnet xhigh claude, terra codex, terra max codex |
| 19 | 2.50 | normal | gemini-3.8-flash-medium | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18% + 6 explore (10%) | now picked: argon agy, opus agy, sol high codex, sonnet xhigh claude, terra max codex |
| 20 | 3.15 | hard | opencode-go/deepseek-v4.1-flash | deepseek opencode 38%, sol codex 27%, argon agy 25% + 4 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25% + 6 explore (10%) | now picked: opus agy, sol high codex |

### Per column (D 1.00 to 4.67)

Models that can be picked in each column, with the share of 1000 picks from a fresh rotation (no quota). "Explore" means a model that gets under 5 percent.

| Column | Label | Pickable with the rule | Pickable without it | Newly pickable |
|---|---|---|---|---|
| 1.00 | trivial | haiku high claude 47%, deepseek opencode 43%, luna codex 10% | haiku high claude 47%, deepseek opencode 43% + 15 explore (10%) | argon agy, flash agy, haiku xhigh claude, opus agy, sol codex, sol high codex, sol low codex, sonnet agy, sonnet claude, sonnet xhigh claude, terra codex, terra max codex, terra medium codex, terra-low |
| 1.33 | trivial | luna codex 40%, terra-low 32%, deepseek opencode 28% | luna codex 36%, terra-low 28%, deepseek opencode 26% + 14 explore (10%) | argon agy, flash agy, haiku high claude, haiku xhigh claude, opus agy, sol codex, sol high codex, sol low codex, sonnet agy, sonnet claude, sonnet xhigh claude, terra codex, terra max codex, terra medium codex |
| 1.67 | simple | luna codex 36%, haiku high claude 28%, deepseek opencode 26%, terra-low 10% | luna codex 36%, haiku high claude 28%, deepseek opencode 26% + 12 explore (10%) | argon agy, flash agy, haiku xhigh claude, opus agy, sol codex, sol high codex, sonnet agy, sonnet xhigh claude, terra codex, terra max codex, terra medium codex |
| 2.00 | simple | haiku high claude 24%, deepseek opencode 22%, sol low codex 20%, terra medium codex 19%, sol codex 15% | haiku high claude 22%, deepseek opencode 20%, sol low codex 18%, terra medium codex 17%, sol codex 14% + 7 explore (10%) | argon agy, opus agy, sol high codex, sonnet agy, sonnet xhigh claude, terra codex, terra max codex |
| 2.33 | normal | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18%, sonnet agy 10% | flash agy 27%, deepseek opencode 25%, sonnet claude 20%, sol codex 18% + 6 explore (10%) | argon agy, opus agy, sol high codex, sonnet xhigh claude, terra max codex |
| 2.67 | normal | deepseek opencode 21%, sol low codex 20%, terra medium codex 18%, sonnet claude 16%, terra codex 16%, sonnet agy 10% | deepseek opencode 21%, sol low codex 20%, terra medium codex 18%, sonnet claude 16%, terra codex 16% + 6 explore (10%) | argon agy, opus agy, sol high codex, sonnet xhigh claude, terra max codex |
| 3.00 | hard | deepseek opencode 38%, sol codex 27%, argon agy 25% + 4 explore (10%) | deepseek opencode 38%, sol codex 27%, argon agy 25% + 6 explore (10%) | opus agy, sol high codex |
| 3.33 | hard | sonnet claude 90%, sonnet xhigh claude 10% | sonnet claude 90% + 4 explore (10%) | argon agy, opus agy, sol high codex |
| 3.67 | very_complex | terra max codex 90% + 4 explore (10%) | terra max codex 90% + 7 explore (10%) | argon agy, opus agy, sol high codex |
| 4.00 | very_complex | terra max codex 90%, sol high codex 10% | terra max codex 90% + 3 explore (10%) | argon agy, opus agy |
| 4.33 | very_complex | argon agy 25%, opus agy 25%, sol high codex 25%, terra max codex 25% | argon agy 25%, opus agy 25%, sol high codex 25%, terra max codex 25% | none |
| 4.67 | very_complex | argon agy 25%, opus agy 25%, sol high codex 25%, terra max codex 25% | argon agy 25%, opus agy 25%, sol high codex 25%, terra max codex 25% | none |

## Notes

- "Pickable" here means picked at least once in 1000 picks with no quota. In the
  real picker, quota and the running-worker penalty remove some of these.
- Rows in the per-task table with the same D and label give the same result.
  The tasks are listed as they happened, newest first.
