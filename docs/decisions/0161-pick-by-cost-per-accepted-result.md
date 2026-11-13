# 0161 - The picker takes the lowest cost per accepted result

Date: 2026-11-13
Status: Approved by the owner on 2026-11-13.

## Problem

Decision [0105](0105-worker-picker-learns-from-results.md) qualified a model
only when its cell score was at least 0.9 x the best score in the bucket. The
best model sets the bar for everyone, so the strongest model wins nearly every
task. A D 2.65 task went to Opus high (0.86, 13 tasks). Sonnet (0.71, 25 tasks)
and DeepSeek (0.59, 7 tasks) were left out for "score below 0.9×best". On the
last 15 runs at OpenRouter API prices the cost was $57.72, of which Opus was
$45.26, Sonnet $12.31, Haiku $0.08 and DeepSeek $0.07. Cache reads were 40 to
65 percent of each run's cost.

The owner's words: "with these rules we're going to use opus for everything".
And: "So i'm good with this rework. Can we bundle it with adding the cost per task
to the worker screen ? (worker log to be exact)".

## Decision

1. This replaces the relative 0.9 x best qualifying rule of decision 0105. Among
   the allowed candidates with at least 3 tasks in the bucket, the picker takes
   the one with the lowest expected cost:
   `expected = run cost + (1 - p) x rescue`.
   - Run cost is the model's average run cost in the bucket at API prices. A run's
     tokens are cumulative per task, so its cost already holds its fix rounds. A
     model with no run in the bucket costs its price times the tokens of an
     average run there.
   - p is the share of the model's tasks in the bucket that ended clean: complete,
     with no failed accept, review or test step and no defect found later. A fix
     round alone does not count against p, because the cost has it. p is shrunk
     to the bucket mean with the same k as the score, and takes the lower end of
     its interval while the cell has fewer than `picker.uncertain_n` tasks.
   - Rescue is the average run cost of the next dearer model in the bucket. The
     ladder is ordered by price per token (prompt plus completion) from the price
     book. The dearest model's rescue is itself.
   The cell score stays in use for the allowed test, the exploration rules and the
   notes.
2. Unchanged: quota floors and pace (a lagging provider counts as cheaper, its expected cost divided by the pace factor), the
   "above its benchmark" rule, the exploration share and "explore is up only",
   the allowed test of the interval (the top of a cell's interval against
   `picker.allowed_fraction` x best, which the Quality sub-tab shows), and the
   difficulty judge. The weaker bias no longer changes a pick that the cost rule
   makes. It still weighs the exploration pool.
3. Prices come from `https://openrouter.ai/api/v1/models`, a public list that
   needs no key. The gateway fetches it once a day and keeps the last good copy
   in `model_prices`. A failed fetch keeps the old prices and logs one line. The
   request carries nothing about the owner.
4. Our model ids are matched to OpenRouter ids by a search on the name: no effort
   suffix, no provider prefix, no date, and a version with a dot
   (`claude-opus-5-5:high` is `anthropic/claude-opus-5.5`). `:batch` and
   `~...-latest` entries are skipped. A model with no match is shown as "price
   unknown" and takes the middle price of the list until its mapping is fixed.
5. The cost of a run is its tokens by kind times the price of that kind: input at
   the prompt price, output and reasoning at the completion price, cache reads
   and writes at their own prices, and at the prompt price when a cache price is
   missing. The tokens by kind are kept per worker session in `worker_usage`: the
   daemon's report when it sends one, else the `tokens:` line of the run's
   stored notice.
6. The worker log and the worker notice show `cost: $X.XX at API prices` next to
   the tokens. The cost OpenCode reports stays on its own line.

## Left out on purpose

Cash factors per provider, plan fees and any new picker setting. They may come
later, as a separate decision.

## Consequences

- The picker note names each candidate's run cost, p and expected cost.
- A failed merger test step counts once in the task score. Agent-net also records
  an automatic severity 1 defect for it (decision 0103), which cost a quarter for
  the defect and a quarter for its severity on top of the quarter for the failed
  step. The score now skips that automatic defect and keeps the failed step. A
  manual defect and a failed verify still count.
- A task that ended before the daemon reported usage by kind takes its tokens
  from the `tokens:` line of its stored notice, so it still counts in a cell's
  average cost. A task with neither has no cost. It does not count in a cell's
  average cost; the price times the average run of the bucket stands in.
- `TestPickerCostBacktestReport` re-runs the rule on a copy of a real gateway
  database and prints a per-bucket summary (candidates, run cost, p, rescue, expected cost and the winner), then one row per finished task. It reads the price list from a saved OpenRouter
  JSON (`AGENT_NET_PICKER_BACKTEST_PRICES`), takes the tokens of old runs from the
  `tokens:` line of their stored notices, and applies today's worker floors to a
  quota reading (`AGENT_NET_PICKER_BACKTEST_USED` or `..._QUOTAS`).
