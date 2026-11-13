# 0162 - One path chooses every model: rank, reorder, gate

Date: 2026-11-13
Status: Approved by the owner on 2026-11-13, with all six choices as
recommended ("fully approved").

## Problem

On 2026-11-13 a worker start had no difficulty. The difficulty judge chose
DeepSeek on OpenCode Go, whose monthly limit was used up. In the same start the
worker picker had already skipped that provider ("opencode-go has 0% left ...
under your 2% worker floor"). Two routes chose a model with different checks.

The owner's words: "i suspect we have a more fundamental problem: multiple
routes to model selection. And each route seems to be end to end and slightly
custom."

A read of origin/main (73a7a1aa77) found eleven routes that choose a harness,
provider, model or effort. Only the automatic worker start applies every check.

| Route | Ranks by | Checks it applies |
|---|---|---|
| R1 automatic worker start (`gateway/workers.go` `workerStart`) | cells, cost, explore, rotation | floor, exhausted and 90% block, burn hold, cooling, auth, installed, admission |
| R2 worker start with a named model (`resolveWorkerModel`, `chooseWorkerHarness`) | the named model, quota ranking | exhausted, auth, installed; floor except owner starts; no burn hold, no cooling |
| R3 worker continue (`daemon/worker_continue.go`) | the same model | admission only |
| R4 resume and auto-continue (`resumeEndedWorker`, `worker_auto_continue.go`) | the same model | exhausted only, or nothing |
| R5 difficulty judge (`daemon/difficulty_assessor.go` `pickJudge`) | R1 at normal, on the node's own quota reading | no floor, no burn hold; its own retry with another provider |
| R6 difficulty analyzer (`difficultyModel`, `cheapestDifficultyProfile`) | config, else the cheapest Codex profile | nothing |
| R7 CTO (`daemon/cto.go` `ctoModel`, `cto_claude.go`) | a setting, else a fixed list in code | installed only |
| R8 merger (`daemon/proposals_tools.go`) | `claude-opus-5-5` in code | installed only |
| R9 chief (`chief.model`) | a setting that nothing starts a session with | chief health only |
| R10 phrasing worker | R1 | as R1 |
| R11 previews and the backtest (`quality_preview.go`, `picker_cost_backtest.go`) | R1's chooser with other inputs | their own subset, so a preview can differ from a real start |

Other findings: three quota readings are in use (`ownerQuotas`, `nodeQuotas`,
`withQuotaBurn`); `sessions.limited_until` never takes part in a model choice; a
queued start is not checked again when it leaves the queue; when a chosen model
fails at launch, no route takes the next one, except the judge with its own
retry.

## Decision

### Scope

Every session start that launches a model goes through this path: workers (new,
continue, resume), the difficulty judge, the CTO, the chief of staff, the
mergers, and sessions the owner starts. The CTO, chief and merger are roles of
the one session start (decision 0156), not separate launch paths. The path does
not run inside a running session and does not route messages.

### Layers

Ranking is policy. Whether a candidate can run now is mechanism. They live in
different functions.

1. **Rankers** return an ordered list of candidates (harness, provider, model,
   effort). A ranker never checks quota, auth or installation.
   - Cost ranker: the cell rule of decision 0161, returning the whole order. The
     level rule stays as its fallback order.
   - Fixed-list ranker: an ordered list from a setting or from the caller (a
     model the owner names; the CTO, chief, merger and judge settings).
   - Same-model ranker: a list of one, for continue and resume.
2. **Modifiers** reorder a list and never remove a candidate: pace order,
   protected providers last, rotation among equals.
3. **The gate** returns the first candidate that can run now, with a reason for
   each one it skipped. It reads one fact snapshot: `ownerQuotas` with burn,
   harnesses per machine, auth blocks, model health, `limited_until`.
4. **Granted.** Only the gate can make a `Granted` value (its own package,
   unexported fields). The worker launch and the session start require one. A new
   route cannot skip the gate: it would not compile.

### Locked and optional steps

Locked checks have no switch. They are not in the caller's options, so no
caller can turn them off:

- harness installed and signed in on the machine;
- provider auth not blocked;
- provider not exhausted (0% left, or refused);
- a provider limit that a live session reported (`limited_until`).

Optional steps are switches in the caller's policy: the worker floor, the 90%
used block, the burn hold, skipping models that cool after failures (as a last
resort), pace order, protected providers last, rotation.

### Policy per caller

One table, `PolicyFor(purpose)`, in one file, sets each kind of caller's
optional steps. A test pins it. A caller does not change it at run time. The
one run-time exception: a start the owner makes himself turns the floor off, as
today.

| Caller | Ranker | Floor | 90% | Burn | Cooling | Pace | Protected last | Rotation |
|---|---|---|---|---|---|---|---|---|
| Worker, automatic | cost | on | on | on | on | on | on | on |
| Worker, model named by an agent | fixed | on | on | on | on | off | off | off |
| Worker or session, model named by the owner | fixed | off | off | off | off | off | off | off |
| Continue or resume | same | off | off | off | off | off | off | off |
| Difficulty judge | fixed list | on | on | on | on | on | on | off |
| CTO, chief of staff, merger | fixed list | off | off | off | on | off | off | off |
| Preview, backtest | the row of the caller it previews, with "decide, do not start" |

Continue and resume keep everything off because the task already runs on that
model; a locked check still refuses an exhausted provider, and the caller then
starts a new worker. The CTO, chief and merger keep cooling on so a failing
model drops to the next one in their list.

### Behaviour changes on purpose

- A queued start passes the gate again when it leaves the queue.
- When a chosen candidate fails at launch, the route asks for the next
  candidate in the same list. The failure is recorded as today.
- The CTO, chief and merger model settings become ordered lists. The models
  hard-coded in Go code and in the CTO text go.
- Previews and the backtest call the same function with "decide, do not start".
- The skip reasons are the one note format for every route.

### Deleted

The judge's own picker and retry (`pickJudge`, `judgeWithRetry`); the
difficulty analyzer (`difficultyModel`, `cheapestDifficultyProfile`); the CTO's
model choice and fixed list (`ctoModel`, the `claude_model` default); the
merger default model and its `harness='claude'` checks; the quota checks inside
`resolveWorkerModelRankedQuota` and `ConfirmOverride`; the preview's own chooser
inputs; `nodeQuotas` as a choice input.

A test pins the gate: no Go file outside the gate's package calls the floor,
quota refusal, burn hold or 90% block functions, and nothing outside it can
build a `Granted`.

## Owner choices (all approved as recommended)

1. Locked list as above.
2. Policy fixed per caller in one table; the owner's own start is the only
   run-time exception.
3. The policy table values above, including the floor off for the CTO, chief
   and merger.
4. Continue or resume on an exhausted provider stays refused.
5. CTO, chief and merger settings become ordered lists.
6. This comes before org T4.

User documentation: `docs/model-choice.md`, written with this decision and
shipped with the last build task, so it never describes code that is not out.
