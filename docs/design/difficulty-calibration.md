# Difficulty calibration from rework

Status: approved by the owner on 2026-11-01, with the recommended answers to the open questions. Built in the parts of section 10.

Parts 1, 2, 4 and 5 are built. Part 3 steering is not built. Part 5 fits
ratings and weighted rework in the gateway, with a penalty of 10 equivalent
tasks per effect. Effort effects sum to zero within each model. The gateway
rebuilds the cached analysis at startup and when ratings, runs, profiles or
score overrides change, including run retention. No request or TUI redraw
fits data. Quotas shows effects, counts and predictions at normal difficulty.
Rotation weighting is owner controlled through
`agent-net scores quality on|off`, defaults to off, and needs at least 5
ratings. Its multiplier is
`1 + (prediction - average) / 4`, bounded to 0.5 through 1.5. Weak ratings
count half; models without a strong rating cannot get a prediction above
the fitted average. Residuals over 0.75 points with at least 5 ratings flag a
combination for review. No interactions or difficulty steering were added.

Builds on decision [0026](../decisions/0026-worker-difficulty-and-scores.md)
(difficulty, scores, rotation) and [0036](../decisions/0036-primitives.md)
(task as a contract between a caller and a doer).

## The problem

Every agent that asks for a worker, the orchestrator included, labels tasks
as harder than they are. The asker pays nothing for a label that is too high
and pays a redo for a label that is too low, so rounding up is safe for the
asker and expensive for the owner: hard tasks go to the strongest models and
use the quota the owner wants to protect. Rules in each project cannot fix a
bias that comes from the incentive. The network can, because it sees every
task and what happened to it.

## The idea

1. Agents ask for a difficulty, not a model.
2. agent-net measures rework per task, per caller and per difficulty.
3. It aims for about one round of rework. Zero rework, every time, means the
   model is stronger than the task needs. Two or more means it is too weak.
   One round means the cheapest model that can do the job.
4. It corrects each caller's labels from that measure, and says so when it
   starts the worker.

## 1. Difficulty, not model

`agent_start` already takes a difficulty. A model named by an agent becomes a
hint: the chooser may use it, and the start result says when it did not. Only
the owner (the CLI, the TUI, or an approved CTO proposal) forces a model.
Otherwise every caller could step around the loop by naming one.

## 2. Measuring rework: counters

Each worker record gets counters. They come from messages that agent-net
already routes. The caller marks nothing.

| Counter | Counts when | Means |
|---|---|---|
| `continue` | the caller sends the worker a new message after a hand-back with `complete: no` | the worker stopped early |
| `fix` | the caller sends the worker a new message after a hand-back with `complete: yes` | the worker finished, but not right |
| `midrun_count` | after the first brief, a caller request arrives with no end notice since the previous caller request | the caller sent a mid-run follow-up |

A worker that goes idle without a hand-back is the same signal as
`complete: no`: it stopped early without saying so, and the caller's next
message counts as a continue.

The daemon's own automatic continues are not rework. When a worker stops early
(a partial hand-back or idle without a hand-back), agent-net sends it up to two
continues itself before the caller is told. These cost the caller nothing, so
they are counted apart (`auto_continue`) and never enter `continue`, `fix` or
the weighted rework. They stay visible in the track record line as
`2 auto-continues`, as a sign that a model needed a nudge and a caller did not
have to give it.

The weighted rework of a task is `fix + 0.33 × continue`, plus the severity
points of late defect reports. A fix counts more because it costs review
attention: the worker delivered the wrong thing, and someone had to notice. A
continue is cheaper: the work so far was right, and a resumable harness picks
it up. The owner set the continue weight to 0.33 on 2026-11-01; a fix stays 1.

This split matters for real models. Today terra on Codex stops early (many
continues, few fixes), and DeepSeek on OpenCode finishes but is sometimes
wrong (few continues, some fixes). Counting both the same would treat a
model that needs a nudge like one that builds the wrong thing.

**A task can span worker sessions and turns.** A follow-up after a hand-back
can start another run in the same session. `agent_start continue=<session>`
starts a new session for the same task. Each gateway run has a `task_id`:
the root session ID, or an empty string when its own session is the root.
The calibration and quality tables add the counters of every run in the task.
They take the caller, model, harness and difficulty from the root's first run,
and the end time from the last. A continued start adds one continue when the
old run was partial, or one fix for every other outcome, including stopped.
The task stays out only if all its runs were stopped. A stopped run within a
task otherwise contributes its counters, and defects reported on any session
belong to the same task. Some older runs have unknown counters, so their stored
zero may not describe all of the rework.

`midrun_count` excludes the initial brief. A done, partial, failed, timed out
or idle-without-hand-back notice marks the next caller request as post-notice;
another request before a new notice counts as mid-run. This counter does not
change the existing continue and fix counts and has weight zero for now.
Automatic continues are not caller requests. The gateway backfills links and
mid-run counts once from retained messages and stores a completion marker in
the same transaction. Expired messages cannot be recovered.

**A defect found later.** The counters only see a follow-up the caller sends.
A defect found hours later, after the hand-back or after merge, would be lost.
So the caller can report it with `agent_rate` and `kind:
"defect_found_later"`: one line on what is wrong, a required severity from 1
to 4, no score, at any time within the gateway's worker run retention, also
after a rating and more than once. Each report is one defect and adds its
severity in points of rework to that worker's task. The owner can file one
from the Messages tab, with `f` on a worker conversation, where the prompt
asks for the note and then the severity.

Not counted: reverts after landing and fix-up lines found in git. They are
better signals but need more machinery.

## 3. The target: a median of about one

For each caller and difficulty, take the weighted rework of the caller's last
7 tasks at that difficulty (with at least 5). Use the median, not the mean:
counts are small whole numbers, and one bad task should not swing the result.
The runs of all sessions in a task are added together before the task enters
the window.

| Median | Reading | Action |
|---|---|---|
| below 0.5 | the model was stronger than needed | step this caller's label down one tier |
| 0.5 to 1.5 | right | no change |
| above 1.5 | too weak | step this caller's label up one tier |

The tiers are the existing difficulties: trivial, simple, normal, hard,
very_complex. The step is an offset per caller and difficulty, kept by the
gateway. It moves one tier at a time, and only after a new window of tasks,
so it cannot swing back and forth.

A new caller starts with the network's offset for that difficulty (see
section 6), and its own data takes over as it comes in.

## 4. Explain it; do not hide it

The worker still starts at once. A dialog would cost the caller a turn on
every start. The start result explains the choice:

> You asked *hard*. Your last 7 *hard* tasks needed a median rework of 0, so
> this one starts at *normal* (deepseek-v4.1-flash) and steps up if it is sent
> back. To force *hard*, start again with `reason`.

A caller may override with `reason`. The reason is logged, and the outcome
of the overridden task counts in the caller's record like any other. An
override is possible, but not free.

## 5. A lower target for risky work

Risk comes from the charter's maturity, which every node already has (early,
growing, mature, critical). No new labels and no per-project rules.

For a task in a `critical` node:

- the target is a median of 0.5 instead of 1;
- a floor tier: the loop never moves the task below `hard`.

A bug in critical code that passes review never shows up as rework, and it
costs far more than one round. The floor keeps the cheapest models away from
it no matter what the counters say.

## 6. The weak spot, and its guards

The loop sees only the rework that a caller notices. A caller that does not
review well sends nothing back. Then the loop reads zero rework, steps the
caller down, the cheaper model delivers worse work, the weak caller still
notices nothing, and the loop drifts down. The measure is only as good as the
reviewer.

Guards:

- **Only a strong caller can lower a tier.** Any caller's rework can step its
  offset up. Only a caller running on a model at or above a score threshold
  (the same score table as decision 0026, for example Opus, Astra or sol) can
  step it down.
- **Weak callers use the network's offsets.** The per-difficulty offset
  learned from strong callers' tasks applies to callers below the threshold.
  The loop learns from the agents that can tell good work from bad.
- **Spot checks, off by default.** On request, a strong model reviews a
  sample of tasks that a weak caller accepted. It costs strong-model quota, so
  only the owner turns it on.

## 7. Choosing the model

The calibration changes only the difficulty that goes into the chooser. The
chooser uses the score minimum and the owner's max-level rule: "no model
should be used for more than 1 level less than its max". A profile's max is
the highest difficulty whose minimum its score meets, including owner
overrides. Automatic picks may use it only at that level or exactly one below.
A score of 51 with today's minimums has max `very_complex`, so it can run
`hard` and `very_complex`, never `normal`, `simple` or `trivial`.

Excluded models have no rotation weight and show `x` in the Quotas Workers
forecast. Among eligible profiles, unprotected providers come first, then
quota pace and weighted rotation. If no profile qualifies, the start fails
with the level, registered profiles and their max levels, and guidance to
register a profile for that level or pass `model` explicitly. An explicit
model overrides eligibility; the start note says when it does. Automatic
notes name models excluded by the max-level rule. An empty roster fails too.
Recent failure and success marks change weight only on the same node and
harness. A model's own track record is read by the same two counters, so a model whose
tasks need too many fixes at a difficulty loses rotation weight there. A
model that only needs continues keeps most of its weight on resumable
harnesses, and loses it on harnesses that cannot resume (Pi today).

## 7b. Asking the caller: a satisfaction sample

Counters see how often work came back, not how good it was. So, now and then,
agent-net asks the caller directly.

- **When.** After a worker's final hand-back, for every 5th finished worker
  of each caller. Also for the first 3 finished tasks of any model and effort
  that has fewer than 5 ratings, so new models get rated quickly.
- **What.** One short request from agent-net, answered with one tool call
  (`agent_rate`): a score from 1 to 5 for the result, and one line on why.
  Example: "Rate worker 4 (deepseek-v4.1-flash, normal, 23 min): 1 to 5 for
  the result, and one line." No answer within a day counts as no rating, not
  as a low one.
- **Stored.** Per rating: the model, the effort, the harness, the difficulty
  asked and used, the caller and its model, the score, the line, the time.
- **Used.** Through the analysis in section 7c, not as one median per
  combination. The chooser uses the predicted quality like the track record:
  it changes rotation weight, within limits, and never the score minimum.
  The benchmark score decides what a model may do; the ratings decide how
  often it is picked among the adequate ones.
- **The same guard as section 6.** Ratings from strong callers count fully.
  Ratings from weak callers count half, and alone they cannot raise a model's
  quality score. A weak reviewer is generous for the same reason it sends
  nothing back.
- **Overlap.** `agent_learn` stays for free-text findings that should change
  how a model is briefed. `agent_rate` is the structured number. A low rating
  with a reason may also become a finding, by the caller's choice.

## 7c. Analysis: store each task, read the effects together

Split by every combination (model, harness, effort), each cell gets two or
three ratings, which says nothing. So agent-net stores every rating and
every rework count raw, per task, and analyses them all together as a sum of
effects:

```
quality ≈ average + model + harness + effort + difficulty
```

The effects are fitted over all tasks at once. Each task then informs its
model, its harness and its effort together, instead of filling one small
cell. This answers three questions directly:

- **How does the model act?** The model effect, for example "DeepSeek -0.2,
  sol +0.6", with the number of tasks behind it.
- **How much does the harness matter?** The spread of the harness effects. If
  Codex, OpenCode and Pi are all within ±0.1, the harness hardly matters. If
  Pi is at -0.5 for every model, the problem is the harness, not the model.
- **How much does effort matter?** Measured within one model family, because
  "high" means different things for different vendors: "sol medium against
  sol high: +0.1, not worth the cost".

Two properties make this better than plain medians:

- **It separates things that come together.** DeepSeek runs almost always on
  OpenCode, so a plain median cannot tell a DeepSeek effect from an OpenCode
  effect. The sum of effects can, wherever there is some overlap.
- **It predicts a combination nobody tried yet**, from the effects it is made
  of.

Kept honest:

- **Small samples are pulled toward zero** (shrinkage). A model with three
  ratings does not jump to +1.5; its effect stays near the average until
  enough tasks support it. One parameter sets how strong the pull is.
- **Every effect shows its count**: "+0.6 (14 tasks)", "-0.3 (3 tasks, weak)".
- **The same fit runs on the rework counters.** That shows, for example,
  that early stops are partly a harness matter (Codex can resume, Pi cannot)
  and wrong results a model matter.
- **No interactions at first.** Some combinations are special (early stops
  matter only on a harness that cannot resume). The sum misses them. Start
  without, and flag a combination whose real results stay far from the
  prediction.

The fit runs in the gateway on source changes, and its result is cached in
memory. Reads only serve the cache. Nothing is stored beyond the raw
records and the owner setting. The real limit is data: at 5 to 20 rated tasks a day, model effects
take a week or two to settle, harness and effort effects longer, and the
screen says so instead of pretending to know.

The per-caller rework medians of section 3 stay as they are: they correct a
caller's labels, which is a different question from how good a model is.

## 8. What the owner sees

The Quotas tab gets one small table, one row per caller:

```
CALLER          LABEL BIAS   HARD        NORMAL      OVERRIDES
agent-net       -1 tier      0 · 7 tasks 1 · 9 tasks 1
homelab-agent    0           1 · 5 tasks -           0
```

Each cell shows the median weighted rework and the number of tasks behind
it. A caller with a lasting negative bias is an over-labeler. A caller marked
weak shows `network` in place of its own numbers.

Under it, the three answers of section 7c, each effect with its count:

```
MODEL                QUALITY        REWORK        HARNESS     QUALITY
gpt-6.1-sol          +0.6 (14)      -0.3 (14)     codex       +0.1 (31)
deepseek-v4.1-flash  -0.2 (22)      +0.2 (22)     opencode    -0.1 (25)
gpt-6.1-terra        +0.1 (9)       +0.6 (9)      pi          -0.4 (4, weak)

EFFORT (within family)
sol    medium -> high   +0.1 (6, weak)
```

## 9. State

- Rework and mid-run counters on each worker record (daemon and gateway).
- The ratings, raw, one row per rating (gateway).
- One offset per caller and difficulty, plus the network offsets (gateway
  owner settings, next to the rotation state).
- The override reasons (gateway events).

Nothing reads task content. This fits decision 0036: agent-net knows the
content only of work that crosses a boundary, and here it only counts it.

## 10. Build plan

1. The two counters on the worker record, counted from routed messages, and
   shown in the worker track record. Read only; nothing changes behavior.
2. The per-caller medians and offsets, the network offsets, and the
   strong-caller guard, with the Quotas table. Still read only: the table
   shows what the loop would do.
3. Apply the offsets in the chooser, with the explanation in the start
   result, `reason` overrides, the critical floor and target, and a model
   named by an agent as a hint.
4. The satisfaction sample (section 7b): `agent_rate` and the sampling.
5. The analysis (section 7c): the sum-of-effects fit with shrinkage over
   ratings and rework, the three answers in the Quotas tab, then the
   predicted quality as rotation weight.
6. Optional, later: restarts, reverts and git fix-up lines as extra signals;
   spot checks.

Parts 1 and 2 change no behavior, so the owner can watch the numbers for a
few days before part 3 starts steering.

## Open questions for the owner

1. **The weights.** Decided by the owner on 2026-11-01: a fix counts 1, a
   continue 0.33, and a late defect adds its severity from 1 to 4. The scale: 1 cosmetic or test-only, caught before release; 2 a visible annoyance or a latent performance problem that had not hit the owner yet; 3 a broken feature or a real performance regression in a release; 4 an outage, data loss or a security problem.
2. **The window.** The last 7 tasks, with at least 5. Recommendation: keep
   it; at today's pace that is about a day of work per caller.
3. **The strong-caller threshold.** Recommendation: a score of 45 or more,
   which today means sol, Opus and Astra, and not terra, DeepSeek or Sonnet.
4. **A named model from an agent becomes a hint.** This changes today's
   behavior for agents that name models. Recommendation: yes.
5. **How often to ask.** Every 5th finished worker per caller, plus the first
   3 tasks of a little-rated model. Recommendation: keep it, and watch
   whether callers find it a burden.
6. **The analysis.** A sum of effects with shrinkage, no interactions at
   first. Recommendation: yes; it is small, and plain medians per
   combination would stay empty for weeks.

Worker starts can also report `touches`. When it includes `db` (schema, migrations or stored data) or `hot-path` (code run on every request, refresh, redraw or message), automatic choice uses the next difficulty level, capped at `very_complex`. The requested difficulty remains the calibration input; the worker and run also store the effective level and touched areas. Empty touches or other areas leave choice unchanged.
