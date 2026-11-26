# How agent-net chooses a model

This page is the deep dive on model selection. The README gives the short
version. Here you find what the picker decides, how it decides, what each step
rests on, what you can change, and what is still open.

It covers worker starts (new, continued and resumed), the difficulty judge, and
the CTO, chief of staff and merger sessions. Sessions you start yourself are not
on this path yet (see the limits below). The picker does not run inside a
running session, and it does not route messages.

Where: [decision 0162](decisions/0162-one-model-choice-path.md) (one path for
every start).

## The problem

Each start needs four answers: harness, provider, model and effort. There are
three simple ways to pick them. Each one fails in a known way.

- **By hand, every time.** You would need the quota left on each subscription,
  the sign-in state on each machine, and how well each model did on work like
  this. Nobody keeps that in mind across machines and models.
- **Always the best model.** Quality is high, and so is cost. The best model
  also sets the bar for every other model, so it wins almost every job. Under
  the old rule, Opus took $45 of a $58 bill for fifteen runs at API prices. The
  owner's words: "with these rules we're going to use opus for everything."
- **Always the cheapest model.** A cheap model that is too weak for a hard job
  costs more in fix rounds, reruns and defects than it saved.

The picker takes a fourth path. For each start, it picks the candidate with the
lowest expected cost of one clean result, among the candidates with a proven
record at this difficulty. Other candidates get an occasional try (section 4).
Quota, sign-in state and your protections decide which candidates may run now.
Every finished task updates the record.

Where: [decision 0161](decisions/0161-pick-by-cost-per-accepted-result.md)
(the cost rule), decision 0162 (the path).

## One pick, end to end

```
brief
  |
  1. judge        six scores -> D -> level (trivial ... very_complex)
  |
  2. candidates   the worker profiles you registered
  |
  3. rank         cost rule: lowest expected cost of one clean result
  |               (a fixed list for the judge and the role sessions)
  |
  4. reorder      pace; protected providers last
  |
  5. gate         first candidate that can run now; a reason for each skip
  |
  6. start        launch; if the machine refuses, the gate's next candidate
```

The ranker and the modifiers never check quota or sign-in. Only the gate can
grant a start.

### 1. The judge

Before a worker starts, a small judge model reads the brief. It scores six
dimensions from 1 to 5. Each score above 1 needs a named piece of evidence in
the brief.

| Dimension | Weight in D |
|---|---|
| Reasoning | 0.20 |
| Scope | 0.10 |
| Coupling | 0.20 |
| Uncertainty | 0.15 |
| Verification | 0.15 |
| Risk | 0.20 |

D is the weighted sum, from 1 to 5. The level follows from D: below 1.5 is
trivial, 1.5 to 2.25 is simple, 2.25 to 3.0 is normal, 3.0 to 3.75 is hard, and
3.75 or more is very complex. A 5 in any dimension makes the level at least
hard.

If the judge fails, times out, or reports low confidence, the level is normal
and the start goes ahead. The note says why.

If the first judge fails, the daemon asks the gateway once more for a judge on
another provider. The judge is itself a start, and it goes through the gate with
the judge's policy (see section 6).

Where: `internal/difficulty/difficulty.go` (weights, cut points, `Label`),
`internal/daemon/difficulty_assessor.go` (`judgeInOrder`),
`internal/protocol/worker_assessment.go` (`WorkerLevel`).

### 2. Candidates

A candidate is a worker profile you registered: a harness, a provider, a model
and an effort. Each candidate gets a benchmark score from the model scores the
gateway stores, and its record. The README says the benchmark scores come from
Artificial Analysis. A candidate whose harness is not installed on the machine is still
listed. The gate skips it with a reason.

Where: `internal/gateway/workers.go` (`workerModelCandidates`),
`internal/gateway/scores.go` (`model_scores`, `difficulty_minimums`),
`internal/protocol/picker.go` (`WorkerBenchmarkCeiling`).

### 3. Rank: the cost rule

Automatic worker starts use the cost rule. It works inside one difficulty
bucket. Buckets are a third of a point wide: 1.00, 1.33, 1.67 and so on.

For each candidate in the bucket:

- **Cell score.** The mean task score, pulled toward the bucket mean with a
  weight of five tasks. A task scores 1 minus 0.25 for each fix round, each
  failed test, accept or review step, each defect, and each point of defect
  severity. A task that did not end complete scores 0.
- **Allowed.** A candidate with at least three tasks in the bucket is allowed
  when the top of its 90% interval reaches 0.9 times the best cell in the
  bucket. A candidate with fewer tasks is allowed by its benchmark, when the
  benchmark ceiling is above the bucket. A candidate whose ceiling is at or
  below the bucket is left out with the reason "above its benchmark".
- **Expected cost.** `expected = run cost + (1 - p) x rescue`.
  - *Run cost:* the average real run cost in this bucket, at API prices. With
    no runs there, its price times the tokens of an average run.
  - *p:* the share of its tasks in the bucket that ended clean: complete, with
    no failed accept, review or test step, and no defect found later. p is
    pulled toward the bucket mean. While the cell has fewer than five tasks, p
    takes the lower end of its interval.
  - *Rescue:* the run cost of the next dearer model in the bucket. A failed run
    is paid again on that model.

The pick is the allowed candidate with at least three tasks and the lowest
expected cost after pace (section 5). A tie goes to the higher cell score, then
to the name.

When no candidate has three tasks in the bucket, nothing is proven. The picker
then makes a fallback draw among the allowed candidates (decision 0181). A
candidate's weight is 1 divided by its expected cost, the cost in the list
above, in place of the cell score of the rotation. A cheaper run is drawn more
often.
  - *Pace:* the quota pace factor applies as before, so a provider behind its
    pace gets more weight. The weight is the pace factor over the expected cost,
    which is the same as 1 over the cost after pace in section 5.
  - *Uncertain boost:* the weight is times `picker.uncertain_boost` (1.5) for a
    thin cell that is not far above the work.
  - *Record and recent marks:* the track record, the recent success (times 1.2)
    and the recent early quit (halved) apply as in the rotation.
  - *Floor:* a cost of zero or an unbounded cost reads as the bounds of
    `FallbackDrawWeight`, so every allowed candidate keeps a chance to build a
    record.

The draw is a weighted rotation, so each candidate's share of the picks follows
its weight over time. The start note says `none qualified; fallback draw by
expected cost`. When no candidate is allowed, the start fails with the level
and the reason no profile was allowed; there is no second rule.

Where: `internal/protocol/picker.go` (cell, `ExpectedCost`),
`internal/protocol/picker_choose.go` (`PickerBucketVerdicts`,
`pickOrderByCells`, `expectedCosts`, `FallbackDrawWeight`),
`internal/protocol/chooser.go` (`rotateRanked`, `rotationWeight`), decisions
0161 and 0181.

### 4. Explore

Only proven candidates are chosen by cost. A thin candidate would never get a
try, so the picker sends some work its way. While proven candidates qualify,
about one pick in ten goes to an allowed thin candidate, but only when this task
is a step up for it. A step up means the candidate has a proven record in a
lower bucket and none here or above, or it has no proven record and its
benchmark level is this task's level. A model whose score you set yourself,
above 0, also steps up at normal and hard when it has no proven record and its
benchmark level is at least the task's level. A score of 0 is your pause, and a
benchmark-only score keeps the exact-level rule.

The explore share is `picker.explore_share` (default 0.1). It counts picks, so
it is not random.

Where: `internal/protocol/picker_choose.go` (`exploreIsUp`, the explore credit),
decision 0112.

### 5. Reorder

Modifiers move candidates. They never remove one.

- **Pace.** Each quota window has a pace: the share of the window that has
  passed, against the share used. A provider behind pace has room. The cost
  rule divides its expected cost by e for each 20 points of room, between 0.1
  and 4 times. A provider ahead of pace costs more on paper. Each worker
  already running on a provider takes 5 points off its room, so a burst of
  starts spreads out.
- **Protected last.** Providers you protect go to the end of the list. They run
  only when no other candidate can run now. The note says they were saved for
  your orchestrator.
- **Fixed lists** keep the order you wrote. The judge is the one fixed list that
  is paced and has protected providers last.

Behind the pick, the gate tries the other qualified models by the same cost
rule, then the exploration pool of this task, then the remaining allowed
models, then a thin model left out with `explore is up only`, then the blocked
models. So when the gate refuses the winner, a model in the exploration pool is
tried before a thin model the rule had left out.

Where: `internal/gateway/choose.go` (`paceOrder`, `protectedLastOrder`),
`internal/protocol/chooser.go` (`quotaPaceFactor`, `WorkerQuotaPace`).

### 6. The gate

The gate is the one place that decides whether a candidate may run now. It
walks the list in order and returns the first candidate that passes every
check. For each candidate it skips, it writes one reason.

Some checks are locked. No caller can switch them off:

- the harness is installed and signed in on the machine;
- the provider's sign-in has not failed;
- the provider is not exhausted (0% left, or it refused a start);
- no live session has reported a provider limit that is still running.

Some checks are optional. Each kind of start turns on the ones it needs (see
the table below):

- **Floor.** A share of each provider that you keep for yourself. You set it
  per provider, from 0 to 95 percent, where 0 is off. A start is skipped when
  any window of the provider has less left than the floor. With no quota reading,
  the floor cannot apply. The note says so.
- **90% block.** A provider with a window above 90% used is skipped for this
  start.
- **Burn hold.** The gate projects how fast the running workers use each window.
  It holds back a start that would empty the window before it resets. It never
  holds the first worker of a provider, because there is no rate yet.

The checks each kind of start uses are one table in code, and a test pins it:

| Start | Ranker | Floor | 90% | Burn | Pace | Protected last |
|---|---|---|---|---|---|---|
| Worker, chosen by agent-net | cost rule | yes | yes | yes | yes | yes |
| Worker, model named by an agent | the agent's list | yes | yes | yes | no | no |
| Worker, model named by you | your list | no | no | no | no | no |
| Continue or resume a worker | same model | no | no | no | no | no |
| Continue whose old model no longer qualifies | cost rule | yes | yes | yes | yes | yes |
| Difficulty judge | worker profiles | yes | yes | yes | yes | yes |
| CTO, chief of staff, merger | role list | no | no | no | no | no |

A preview or backtest uses the same checks as the row it previews. It decides
and starts nothing, so the preview shows the choice a real start would make.

A resumed worker keeps its model, so its conversation carries on. The locked
checks still apply. If its provider is exhausted, the resume is refused, and
you or the agent that started it starts a new worker on the same branch.

A continue starts a new session on a new branch made from the old one, so it
carries no conversation. When it names no model, the gateway checks the old
model with the rules of today at the level stored with the worker, as an
automatic start would. A model that still qualifies stays. One that no longer
qualifies (paused, protected, left out by the cell or ceiling rule, under its
floor, out of quota) is replaced by the picker's choice at that level, and the
note says `continue moved from <old> to <new>`. When no other model qualifies,
the old one stays if it may run. A continue that names a model keeps it
(decision [0189](decisions/0189-continue-meets-todays-picker.md)). What you
named at the worker's first start holds for every continue that names none.
Your newest word wins: if you pause that model after you named it, the
continue meets the pause as for any other worker. If it was already paused
when you named it, it keeps running (decision
[0191](decisions/0191-picks-keep-their-quota-read-and-the-owners-choice-holds.md)).

The CTO, chief of staff and merger have a role list. Their session resumes on
the model it already runs. If a locked check refuses that model, the next entry
of the list starts a new conversation. A refused role start gives one line of
reason. Pi is never used for a role.

Where: `internal/model/model.go` (`Gate`, `lockedSkip`, `optionalSkip`),
`internal/model/policy.go` (`PolicyFor`), `internal/protocol/worker_floors.go`
(`CheckWorkerFloor`), `internal/protocol/chooser.go` (`QuotaRoomForProvider`),
`internal/protocol/quota_burn.go` (`QuotaBurnHold`),
`internal/gateway/role_picker.go`.

### 7. The start, its note, and the refusal

After the gate grants a candidate, the machine launches it. If the machine
refuses the model, the gate's next candidate in the same list is tried. The
failed launch is written into the note.

A queued start passes the gate again when it leaves the queue. It tries the
model it chose first, and takes another only if the gate now refuses that one.

If no candidate can run, the start is refused before any model is called. The
refusal names the first three skip reasons and a count of the rest.

A worker start returns the track record for the chosen model and harness on
that machine, and the playbook notes that match the model, harness, project and
machine.

Where: `internal/gateway/workers.go` (the launch loop, `workerRecheck`),
`internal/gateway/choose.go` (`Choose`, `Next`), `internal/gateway/track.go`
(`choiceNotes`).

## Reading the note

A worker start carries two notes: a short "why" line, and a full note with the
pick and the skips. The TUI worker log shows the full note.

The short "why" line says why the first candidate ranked first. This is a real
pick from a notice:

```
difficulty normal (D 2.70); haiku xhigh claude 0.60 (n27); 2 other qualifying;
left out: haiku high claude (above its benchmark); sonnet xhigh claude, opus
high claude (explore is up only); deepseek opencode (opencode-go monthly used
up); terra codex (codex session used up)
```

Read it left to right. The level and D come first. Then the chosen model, its
cell score and its task count (`n27`). Then how many other candidates qualify.
Then each candidate that was left out, with its reason.

The full note adds the pick for the bucket: each qualifying candidate's cell,
its run cost, its p and its expected cost. A fixture from the test suite has
this shape:

```
bucket 2.67-3.00: deepseek 1.00 [0.90-1.00] n13, lowest expected cost;
qualifying: opus 0.86 [0.78-0.94] n31 $1.40/run, p 0.55, $2.03 expected,
deepseek 1.00 [0.90-1.00] n13 $0.07/run, p 0.46, $0.11 expected
```

Every candidate the gate skipped gets its own clause, `skipped <candidate>:
<reason>`. These are the reasons you will see:

- `<provider> is exhausted (<window>; <reset>)`: the provider has no quota left.
- `<provider> has 14% left in its week window, under your 20% worker floor`: the floor.
- `held back for quota: ...`: the burn hold.
- `harness claude is not signed in on <machine>`: the locked sign-in check.

## How results feed back

- **Finished worker runs** are recorded with their outcome, fix rounds, failed
  steps, defects, and whether the work merged. A run that was stopped, or ended
  on infrastructure, does not count.
- **Defects** count when you file them by hand, or when agent-net records one by
  itself. It records a failed verify after merge, a failed test before merge
  (not a flake), and a fix of a shipped change. A failed merger test step counts
  once.
- **Caller ratings** are stored with each run. The picker does not read them.
  The ratings code says so, and a test pins the rule.
- **Brief defects** (a caller finding that its own brief was poor) are kept apart.
  They never enter the picker.
- **Playbook notes** are written per model, harness, project and machine. Agent
  findings can promote a note when enough findings support it, or when the
  track record is clearly bad. A note you wrote or pinned is never changed.
- **Prices** come from OpenRouter's public model list, which needs no key. The
  gateway fetches it once a day and keeps the last good copy. A failed fetch
  keeps the old prices. A model whose name does not match a listed price takes
  the middle price until its mapping is fixed.

Where: `internal/gateway/picker.go` (`pickerTaskRecords`, `pickerTasksFrom`),
`internal/gateway/ratings.go`, `internal/gateway/defect_auto.go`,
`internal/gateway/learn.go` (`evaluatePlaybook`), `internal/gateway/prices.go`,
decisions [0103](decisions/0103-agent-net-records-late-defects.md) and
[0105](decisions/0105-worker-picker-learns-from-results.md).

## What you control

- **Worker profiles.** They decide which models may take work at all. Add one
  with `agent-net workers profiles add <harness> <model> [effort]`, and remove
  one with `remove <name>`.
- **Worker floor.** Per provider, in the TUI Quotas tab: select the subscription
  and press `f`. 0 turns it off.
- **Protected providers.** In the TUI Quotas tab: select the subscription and
  press `p`.
- **Role lists.** `cto.models`, `chief.model` and `merger.models` are ordered
  lists, one candidate per line: harness, provider, model and effort. The first
  one that can run now is used. The CTO can change its list with its settings
  tools, and can undo a change.
- **Picker settings.** `picker.explore_share` (0.1), `picker.allowed_fraction`
  (0.9), `picker.k` (5), `picker.bucket_width`, `picker.uncertain_n` and
  `picker.uncertain_boost`, and `picker.weaker_bias`. The switch from decision
  0173 in code and off is `picker.ceiling_rule`.
- **Playbook notes.** Write a note for a model, harness, project or machine, and
  pin it so that no later finding changes it.
- **Pause a model.** Give it a score of 0 in the model scores. No automatic
  start, role list, continue, resume or automatic continue uses it, and the
  note says "paused by the owner". A model with no score is not paused
  (decision 0188).
- **Your own choice.** If you name a harness, model or effort, the start runs on
  that model, even a paused one. For a worker you name, only the locked checks
  apply.

## What agents may not do

- **Choose the model.** An agent cannot set harness, provider, model, effort,
  `not_family` or machine on a worker start, unless one of two things holds. The
  first is owner words: a quote that appears in what you typed in that session
  in the last three hours. The second is a reason. A reason holds the start
  until the Desk answers it. The chief of staff answers first, the CTO may
  answer too, and with no chief the owner answers. The answers are `confirm`,
  `picker` or `dismiss`. Without a match or a reason, the start is refused.
- **Exception.** A caller whose role node holds admin scope over the project
  has the choice delegated (decision 0166). Its start goes ahead without owner
  words.
- **Change floors or protection.** Those are owner settings.
- **Grant a model to itself.** Only the gate makes a grant. Its type has no
  exported fields, so a route cannot build one.

Where: `internal/daemon/worker_override.go` (`checkOverride`),
`internal/gateway/start_override.go`, `internal/protocol/worker_override.go`,
decisions [0141](decisions/0141-picker-override-needs-a-human.md) and
[0166](decisions/0166-delegated-authority-table.md).

## Limits and open points

These are true of the code today. Some differ from decision 0162 or the README.

- **Costs are API prices.** The cost rule ranks by what the same tokens would
  cost at OpenRouter's public prices. It does not count subscription fees, plan
  limits or cash factors. Those were left out on purpose (decision 0161).
- **Thin models get little work.** A model with fewer than three tasks in a
  bucket is tried only by explore picks. A new model learns slowly.
- **Ratings do not steer the picker.** Only task outcomes, fix rounds, failed
  steps and defects do.
- **Plain sessions do not use the gate.** Decision 0162 puts the sessions you
  start yourself on this path. The code does not yet: a session you start with a
  named model goes straight to its harness. Workers, the judge, and the CTO,
  chief of staff and merger do use the gate.
- **The CTO's OpenCode conversation still has its own model choice.** When no
  OpenCode model is saved, `ctoModel` in `internal/daemon/cto.go` picks from a
  fixed list of three models in code. Decision 0162 says this goes.
- **The judge has a retry.** Decision 0162 lists the old judge retry as deleted.
  A retry on another provider still exists in `judgeInOrder`.
- **Rotation is narrower than the policy table suggests.** `PolicyFor` has a
  rotation column, but nothing reads it. Rotation runs only when no candidate
  qualifies, and on explore picks. A cost pick does not rotate: within a bucket,
  the same proven model wins until its numbers change. The README's "rotates so
  no model takes every job" does not describe a cost pick.
- **Decision 0173 is a proposal.** It changes how the picker weighs quality. Its
  ceiling switch is in code and off.
- **The judge reads only the brief.** A vague brief gives a low-confidence
  score, which means the start runs at normal.

## Where to look

- The TUI Quotas tab (tab 3) shows floors and protection. The Workers log shows
  each start's note. The Quality sub-tab (tab 3, then Tab twice) shows the picker
  table: each model's score per level, its task count, and whether it is allowed.
- The decisions behind this page are
  [0105](decisions/0105-worker-picker-learns-from-results.md),
  [0112](decisions/0112-remove-the-overqualified-rule.md),
  [0161](decisions/0161-pick-by-cost-per-accepted-result.md),
  [0162](decisions/0162-one-model-choice-path.md) and
  [0173](decisions/0173-picker-quality.md).
- The backtest of the explore rule is in
  [picker-backtest-2026-11-09.md](picker-backtest-2026-11-09.md).
