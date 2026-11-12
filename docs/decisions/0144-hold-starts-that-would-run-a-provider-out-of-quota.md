# 0144 - Hold a start that would run its provider out of quota

**Date:** 2026-11-12
**Status:** Built. The owner asked for it: "i think we need to adjust the number
of parallel jobs with a provider to the speed of quota exhaustion", then "the
picker improvement is still worth it".

## Context

On 2026-11-12 about five Codex workers ran at once and all hit the account
limit within minutes of each other. An Antigravity worker hit its limit after
that. The work was saved, but each task lost 5 to 20 minutes.

The picker already reads the quota windows and paces by them (decisions 0022 and
0105). That did not help here:

- quota-axi does not see the Codex account limit in time, so the picker had no
  reading to pace by.
- The pace rule looks at what has been used. It does not look at how fast the
  workers that run now are using it.

## Decision

Before an automatic start, the picker projects the burn of the workers that run
now on each provider. If one more worker would use more quota than the
provider has left before its reset, the provider is left out of this pick.

There is no fixed cap per provider and no setting. Every number is measured.

### What is measured

All of it is per quota provider (the same names the quota windows use), across
every machine, because the subscription belongs to the owner.

- **Burn rate.** For the workers running now on the provider: the tokens they
  have used so far, divided by the seconds they have run. This is the rate of
  one worker. Tokens are the run's input, output and reasoning tokens, which is
  what worker notices show. A set of workers that has run less than 60 seconds
  in total says nothing yet.
- **Expected life of a worker.** The mean run time of the finished runs of that
  provider in the last 31 days. With no finished run, the mean age of the running
  ones.
- **Size of the quota, in tokens.** Two sources, in this order:
  1. A usable reading with a window that serves the model. The tokens that the
     provider's workers used since the window started, divided by the percent
     used, give the tokens per percent. The window start is its reset time
     minus its length (five hours, a day, a week or a month). A window under 1
     percent used is too small to size.
  2. A budget learned from a quota failure (below). It stands in when no reading
     shows a limit, which is the Codex case.

### The projection

For each window, one more worker is held back when

    rate x (running workers + 1) x min(expected life, time to the reset)
        > tokens left in the window

Tokens left is `(100 - percent used) x tokens per percent` from a reading, or
`budget - tokens used in the trailing window` from a learned budget. With no
reading the reset time is not known, so the horizon is the expected life only.

A worker is expected to run for its expected life, not until the reset. That is
why one worker is never held back just because the reset is far away.

The first worker of a provider is never held: with none running there is no
rate. The projection is one more reason to leave a provider out, next to the
existing rules (exhausted window, above 90 percent used, the worker floor).

### Learning the budget from a failure

When a worker run ends with an account quota failure that names a reset delay
(today: the agy "Individual quota reached ... Resets in 134h28m52s" text, and any
other provider that the failure parser learns), the gateway records the budget
of that provider. The window length is the shortest of five hours, a day, a week
and 31 days that holds the delay. The budget is the tokens that the provider's
runs used in that window before the failure, the failing run included. The
newest failure of a window length replaces the older one. It is stored in
`quota_budgets` (provider, window seconds, tokens).

The budget is a lower bound: it counts only the tokens of agent-net's workers,
and the owner's own sessions drew on the same quota. A reading that sizes the
window from the percent used has the same bias. Both make the check cautious,
not loose.

### What the pick does

- The held provider's models are left out before the picker's rules run, so the
  pick goes to another provider that qualifies. The pick note (the "why" line
  and the start notice) says `held back for quota: <provider>: N running
  worker(s) have used X tokens a minute each; one more would need about Y tokens
  in the next Z, and the window has about W left (...)`.
- When no other model qualifies, the refusal carries the same sentence and the
  usual reason. The caller can start again when a running worker ends; the next
  start measures again.
- A model that the caller names explicitly is not touched. Only automatic
  picks use this check.

## Not done

- There is no gateway queue for starts. The daemon queue (decision 0018) holds
  starts for machine room only. A start that is held for quota is refused with
  the reason, and the caller starts it again. A queue that waits for the
  measurement to clear is a separate step.
- The worker preview does not apply the check, because it has no running
  workers to measure.

## Where it is

- `internal/protocol/quota_burn.go`: the projection (`QuotaBurnHold`) and the
  budget rule (`LearnQuotaBudget`). Pure functions with tests.
- `internal/protocol/chooser.go`: `ChooseWorkerModel` leaves held providers out
  and adds the note.
- `internal/gateway/quota_burn.go`: reads the running workers and the finished
  runs, hands them to the picker with the start's quota reading, and stores
  learned budgets.
- `internal/gateway/workers.go`: the start calls `withQuotaBurn`; a worker that
  stops with a quota failure calls `learnQuotaBudget`.
