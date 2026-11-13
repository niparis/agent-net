# 0162 - One path chooses every model: rank, reorder, gate

Date: 2026-11-13
Status: Direction approved by the owner on 2026-11-13 ("yes write the decision
file"). Three choices at the end wait for his answer.

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

Other findings:

- Three quota readings are in use: `ownerQuotas`, `nodeQuotas` and
  `withQuotaBurn`. The judge, the chief and the preview use the node reading that
  `ownerQuotas` was written to replace.
- `sessions.limited_until`, a provider limit that a live session reported, never
  takes part in a model choice.
- A queued start is not checked again when it leaves the queue.
- When a chosen model fails at launch, no route takes the next one, except the
  judge with its own retry.

## Decision

Every model choice goes through one path with three layers. Ranking is policy.
Whether a candidate can run now is mechanism. They live in different functions.

1. **Rankers** return an ordered list of candidates (harness, provider, model,
   effort). A ranker never checks quota, auth or installation.
   - Cost ranker: the cell rule of decision 0161, returning the whole order
     instead of one winner. The level rule stays as its fallback order.
   - Fixed-list ranker: an ordered list from a setting or from the caller. It
     serves a model the owner names, and the CTO, chief, merger and judge
     settings.
   - Same-model ranker: a list of one, for continue and resume.
2. **Modifiers** reorder a list and never remove a candidate.
   - Pace: a provider behind its quota pace moves up, one ahead moves down. Today
     this divides the expected cost inside the chooser; it moves out.
   - Protected providers last.
   - Rotation among equal candidates.
3. **The gate** takes a list and returns the first candidate that can run now,
   with a reason for each one it skipped. It is the only code that applies these
   checks:
   - harness installed and signed in on the machine;
   - provider auth not blocked;
   - provider not exhausted and under the 90% block;
   - the provider limit that a live session reported (`limited_until`);
   - the worker floor (an owner's own start stays exempt, as today);
   - the burn hold;
   - model cooling after infrastructure failures, as a last resort only.
   It reads one quota reading, `ownerQuotas` with burn.

Every route calls this path:

- Workers (R1 to R4, R10) through the worker start.
- agent-net's own model calls: the judge (R5) asks the gateway for its choice.
  The judge's private retry goes. The analyzer (R6), which the owner put on ice,
  is deleted.
- The CTO, the chief and the merger (R7 to R9) are roles of the one session start
  (decision 0156), not separate launch paths. Each role's model setting is an
  ordered list that goes through the fixed-list ranker and the gate. The models
  hard-coded in Go code and in the CTO text go.
- Previews and the backtest (R11) call the same function with "decide, do not
  start". The backtest passes past data as its inputs. No preview has its own
  chooser.

Two behaviours change on purpose:

- A queued start passes the gate again when it leaves the queue.
- When a chosen candidate fails at launch, the route asks the gate for the next
  candidate in the same list. Its failure is recorded as today, so the gate skips
  it while it cools.

The skip reasons are the one note format for every route, so the CTO, the judge
and a worker explain a choice the same way.

A test pins the gate: no Go file outside it calls the floor, quota refusal, burn
hold or 90% block functions, or reads the quota tables to decide a model.

## Build

One worker, three hand-backs, each releasable:

1. Pull the gate out of R1 with no change in behaviour. The existing chooser and
   picker tests, and the 0161 backtest, must give the same results.
2. The cost ranker returns a list; pace and rotation become modifiers. The
   backtest winners must not change.
3. Move R2 to R11 to the path one at a time and delete their own checks. Add the
   gate pin test, the re-check when a start leaves the queue, and the fallback to
   the next candidate. Report the net line count.

## Choices for the owner

1. Continue or resume when the same model's provider is exhausted. Today it is
   refused and the caller starts a new worker. A different model would be a new
   session without the old context. Recommendation: keep the refusal.
2. CTO, chief and merger model settings become ordered lists, so the gate has a
   fallback. Recommendation: yes.
3. Order of work: this before org T4. Recommendation: yes. The two picker fixes
   in flight (live cost data, the OpenCode effort flag) stay valid under it.
