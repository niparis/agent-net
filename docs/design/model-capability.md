# Model capability from rework

**Date:** 2026-11-02. **Status:** plan stored, on hold until the owner says go. Nothing built.

## 1. Goal

The picker matches a model to a task on one scale. The task has a raw
difficulty D (1 to 5, from the judge). Each model has a capability C on the
same scale. C starts from the model's benchmark score and moves with the
orchestrator time ("my time") its past tasks cost. A model that costs more time
than average for its tasks gets a lower C, so it is pushed toward easier tasks.

## 2. The rules

**Capability.**

```
C = prior(score) + adjust
prior:  score -> D through the difficulty minimums
        (minimum of simple -> 1.5, normal -> 2.25, hard -> 3.0,
         very_complex -> 3.75; straight lines between, same slope beyond)
adjust: clamp(-extra x 0.0625, -0.75, +0.75)     4 extra min per task = -0.25
extra:  sum(my time - level average) / (n + 5)   pulled toward 0 while n is small
```

The data for `extra` is the model's family x effort (for example Terra x high)
when that has 20 or more tasks, otherwise the family. The window is the same 30
days the Quality panel uses. The level average is the mean my time of all tasks
with the same label.

**My time per task:** fix 10 min, manual nudge 2 min, late bug 20, 45, 90 or
180 min by severity (today 10, 30, 60, 180). Automatic nudges are not counted.

**Task difficulty.** D is the judge's D. When the judge failed, D is the middle
of the label: trivial 1.1, simple 1.9, normal 2.6, hard 3.4, very_complex 4.1.

**Weight.**

```
fit    = 1 - |C - D| / 0.5      a model 0.5 or more away gets no weight
weight = fit x quota pace x recent result x star weighting (if turned on)
```

If no model is within 0.5, the closest model is used alone, and the start note
says so.

**Removed:** the "max level or one below" rule, the over-qualification penalty
`exp(-(score - weakest) / 8)`, and the track bonus `1 + 0.2 x complete rate`.

**Kept:** unprotected providers first; a model with fewer than half complete
among five or more runs is a last resort; exhausted providers and windows above
90% are skipped; five quota points off per running worker; the smooth weighted
round robin per label; unscored models excluded; an explicit model skips the
picker.

## 3. Expected result with today's data

Backtest: `scripts/dev/capability-backtest.py`, run on a copy of the gateway
database. Pace neutral.

| Model | Score | Prior C | Change | C |
|---|---|---|---|---|
| luna:xhigh | 23.3 | 1.75 | +0.22 | 1.97 |
| deepseek | 32.5 | 2.44 | -0.07 | 2.37 |
| terra:high | 42.3 | 3.17 | -0.07 | 3.10 |
| sol:medium | 46.1 | 3.46 | +0.15 | 3.61 |
| terra:max | 51.0 | 3.83 | -0.09 | 3.73 |

| D | Odds (%) |
|---|---|
| 2.0 | luna 79, deepseek 21 |
| 2.3 | deepseek 72, luna 28 |
| 2.6 | deepseek 100 |
| 2.7 | deepseek 64, terra:high 36 |
| 2.8 | terra:high 73, deepseek 27 |
| 3.2 | terra:high 81, sol 19 |
| 3.4 | sol 44, terra:high 30, terra:max 25 |
| 3.6 | sol 57, terra:max 43 |
| 4.0 | terra:max 68, sol 32 |

## 4. Review time (recorded, not used yet)

"My time" today is an estimate from counts (a fix is 10 minutes). Review time
is measured: the real time the orchestrator spends on a worker's result.

- **Where it is measured.** The agent-net Claude plugin sees every tool call of
  the orchestrator session. A call counts for a worker when its input names the
  worker's worktree ID (`w_...`), branch or session ID: reading the diff,
  running its tests, merging it, messaging it.
- **What is counted.** For such a call: the time from the previous event in the
  session (the end of the previous tool call, or the owner's prompt) to the end
  of this call, capped at 5 minutes. This is the thinking plus the call itself.
  Idle time between turns is never counted.
- **Path.** The plugin adds the seconds up in memory and sends them once a
  minute with a hidden command, `agent-net review add <worker> <seconds>`. The
  daemon checks that the caller is the session that started the worker, as it
  does for ratings. The gateway stores rows in a new table
  `worker_review (session_id, seconds, at)`.
- **Shown.** Quality details gets a column REVIEW / TASK (minutes) per model
  and level. It does not enter my time or C.
- **Decision in two weeks:** if it separates the models, add it to my time.
- **Limit:** Claude orchestrators only. The plugin never blocks or slows a tool
  call: it only reads the input and the clock, and the send runs in the
  background.

`agent-net workers -json` must include the worktree ID; the plugin already
reads that list every few seconds.

## 5. Changes by package

| Package | Change |
|---|---|
| `internal/protocol` | Defect minutes. `ChooseWorkerModel` takes D and per-candidate C; new fit weight; closest fallback; removed rules. `QualityCapability` type and a 7-day task count on `QualityFamily`. |
| `internal/gateway` | Capability table built in the quality refresh, from the existing tables. The start path reads D from the assessment. The Quality preview uses the label middles. Review table, migration and store. |
| `internal/daemon`, `cmd/agent-net` | The `review add` command and its caller check. `worktree` in `workers -json`. |
| `plugins/claude/agent-net-workers` | The review-time hook. |
| `internal/tui` | Cards ordered by tasks in the last 7 days. Details: a Capability table and the REVIEW / TASK column. |
| docs | `reference.md` (picker, my time, review command, new table), `security.md` (who may add review time), `task-difficulty.md` decision. |

No owner setting is added.

## 6. Build steps

1. **Picker and capability** (protocol, gateway, TUI, docs). One worker.
   Tests first: the chooser odds above as fixed cases (terra:max gets no weight
   at D 3.2, terra:high none at D 2.0, terra:max alone at D 4.5).
2. **Review time** (plugin, CLI, daemon, gateway, TUI column). A second worker,
   in parallel with step 1. Only the TUI details screen overlaps; I merge it.
3. One release with both. Rerun the backtest after it.

## 7. Risks and open points

- **Thin roster around D 2.6 to 2.9.** Between DeepSeek (2.37) and Terra high
  (3.10) only one model is in range at a time, so quota pace cannot spread
  work there. The owner adds Terra at default effort (prior about 2.86) with
  the CTO.
- **Judges running in parallel are slow.** One judge takes about 11 seconds;
  three at once took over 2 minutes each in a test on 2026-11-02. The start
  path gives the judge 30 seconds, so starting several workers at once can
  fall back to normal. To investigate before step 1 ships, because D matters
  more once it picks the model directly.
- **Old tasks have labels chosen by callers**, often raised by `touches`, so the
  level averages compare unlike tasks. It fades as judged tasks replace them.
- **Rotation state stays per label.** With a continuous D, two tasks with the
  same label can have different candidates; the round robin only balances the
  ones it sees. Acceptable for now.
- **Rework strength** stays at 4 minutes = 0.25 until two weeks of judged tasks
  exist. Then rerun the backtest and decide whether to double it.
