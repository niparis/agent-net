# Evidence: how a change leaves a record

This page is for a technical reader who has read the [README](../README.md). It explains how one change moves from a worker's hand-back to a verified release, what each step must record, and how that record feeds back into the swarm.

The short version: no step passes on a message. Each pass is a report, tied to one revision and one exact commit, from a named session. The gateway checks the shape of each report and keeps every row. Later steps and the picker read those rows.

Decisions cited here: [0057](decisions/0057-delivery-pipeline.md) (delivery pipeline), [0083](decisions/0083-review-independence-default-none.md) (review independence), [0164](decisions/0164-merge-queue-is-the-single-test-gate.md) (merge queue CI is the test gate), [0165](decisions/0165-cto-owner-words-are-approval.md) (owner words), [0166](decisions/0166-delegated-authority-table.md) (delegated authority), [0170](decisions/0170-friction-reports.md) and [0175](decisions/0175-friction-status.md) (friction reports).

## What a change is

A change is one unit of delivery. It has a project, the machine that holds its branch, a branch, a target branch and a head commit. The gateway gives it an ID that starts with `ch_`.

A change starts in one of two ways. A worker's complete hand-back submits it. A session can also submit work it did itself with `agent_change_submit`. Git holds the code. agent-net holds the steps and the proof.

Four rules keep this simple:

- A branch holds at most one open change. A submit for a branch that has an open change adds to that change.
- A submit with a new head commit is a new revision of the same change.
- A change cannot move onto a branch that another open change holds. That submit is refused.
- A merged change takes no new head. Its state refuses one, so new work is a new change.

**Where:** `Change` and `Revision` in `internal/protocol/delivery_pipeline.go`; `SubmitChange` in `internal/gateway/delivery_changes.go`; `Change.WithHead` in the same protocol file; [0057](decisions/0057-delivery-pipeline.md).

## What a revision is

A revision is the head of a change at one complete hand-back or one submit. The first revision is 1. A new head commit makes the next revision. A move to another branch with the same commit does not.

Evidence counts only for the revision it was reported on, and only on its exact commit. Before merge, that commit is the head. After merge, it is the merged commit. Evidence on an older revision or an older commit is kept in the record, but no later step reads it.

**Where:** `WithHead` (the revision bumps only when the commit changes) and `latest()` in `internal/protocol/delivery_pipeline.go`; `insertRevision` in `internal/gateway/delivery_changes.go`.

## The steps and their owners

A repo's pipeline names which steps run, who owns each one, and what must pass before the next one starts. The steps are `accept`, `review`, `test`, `approve`, `merge`, `release`, `deploy` and `verify`. Some run before the merge, and some run after it on the merged commit. A pipeline uses only the steps it needs.

| Step | Usual owner | What the report carries | What it proves |
|---|---|---|---|
| accept | caller | pass or fail, with a note | The caller read the report and diff and says the work does what the brief asked. |
| review | caller, or a node | pass; `findings_open` when the pipeline requires it | A second read. Required from difficulty hard, or when a sensitive path changed. |
| test | merger (train mode) or caller | `suite` (changed or full), `run`; `tested_commit` in train mode | Tests ran on this exact commit. |
| approve for merge | none, a delegate, or the owner | the approver's pass | A human or a delegate allows the merge. Owner `none` leaves the step out. |
| merge | merger or caller | pass with `merged_commit` | The change landed on the target branch. The change becomes merged. |
| test after merge | merger or caller | `suite` full on the merged commit | CI ran on the commit that landed. |
| release | merger or caller | `tag` | The release exists. Needs the after-merge full test on the same commit. |
| deploy | the node with a `deploys` edge | `target` | Only when the pipeline has a deploy step. |
| verify | caller or owner | pass with a note on what was checked; `severity` on a fail | The result holds on the real setup after release. |

The gateway also refuses reports that break the rules, before they are stored:

- a commit that is not the head (or the merged commit, after merge);
- a revision that is not the current one;
- a step that is not in the pipeline, or that comes at the wrong time (before merge on a merged change, or after merge on an open one);
- a report from a session that does not own the step;
- a merge pass with no `merged_commit`;
- a pass that lacks a field a later step needs, so the reporter fixes it at once and not at the merge.

For a merge pass, the daemon on the machine must also confirm that the merged commit contains the change head. The gateway refuses the merge if it does not.

**Where:** the steps and fields in `internal/protocol/delivery_pipeline.go` (`StepRef`, the `Field*` constants, `Pipeline.Accepts`, `checkPassFields`); `ReportStep` in `internal/gateway/delivery_changes.go`; [0057](decisions/0057-delivery-pipeline.md); [0164](decisions/0164-merge-queue-is-the-single-test-gate.md).

## Who reports, and how evidence is stored

Evidence comes from three places: an authenticated step report from a session on the same machine as the change, an owner's answer to an Inbox approval, or a result read from GitHub. A message never counts.

When a session reports a step that a worker it started did, the worker is the author of the evidence (`by`) and the reporting session is `reported_by`. Each row keeps the model and model family of the session that did the work.

Every report is appended. The newest row for a step on a revision and commit is the one that counts. The session that reported a step can report it again on the same commit, to fix a field or a result. That holds only while no later step has relied on the first report. The older row stays.

Step reports can also carry friction lines, which the gateway stores with the change ([0170](decisions/0170-friction-reports.md)).

**Where:** `ReportStep` (the session node check, `by` and `reported_by`); `insertEvidence`; `reopenOwnReport`; `Evidence` in `internal/protocol/delivery_pipeline.go`.

## How independence is set

The reporter does not set the `independent` field. The gateway does, from who did the work:

- an owner's answer or a GitHub result is `family`;
- the same session that wrote the change is `none`;
- another session of the same model maker is `session`;
- a session of another model maker is `family`.

Model makers are families such as anthropic, openai and google. Sonnet and Opus are one family.

A step can require `session` or `family`. A pass that is not independent enough is ignored, and the step note says why. By default, review needs no independence ([0083](decisions/0083-review-independence-default-none.md)). The owner can turn it back on.

**Where:** `IndependenceOf` and `ModelFamily` in `internal/protocol/delivery_pipeline.go`; `latest()` in the same file; [0083](decisions/0083-review-independence-default-none.md).

## Reminders, stuck work and waiting

Each ready step creates a task for its owner. Tasks for one session can batch into one message.

- A ready step is reminded every 2 hours by default (`delivery.step_reminder`).
- A verify step is reminded at 30 minutes, 2 hours, 8 hours, then daily, counted from the time it became ready.
- A ready step that no one claims for 12 hours (`delivery.step_stuck`) sends a notice to the caller. A caller step that waits past that limit with no live session of the caller's agent wakes the agent once. If that fails, or the agent does not come online, the repo lead may act on it.
- An owner can report `waiting` with a reason. A waiting step sends no reminders. Any new evidence on the change ends the wait. Otherwise the wait expires after 24 hours.

Nothing here counts as a pass. A step with no report stays ready, waiting or stuck until someone reports it.

**Where:** `stepReminderCutoff` and `verifyReminderAges` in `internal/gateway/delivery_tasks.go`; `checkStuckStepTasks` in the same file; `escalateCallerSteps` in `internal/gateway/caller_agent.go`; `reportStepWaiting` in `internal/gateway/delivery_waiting.go`; [0057](decisions/0057-delivery-pipeline.md), the 2026-11-11 section.

## Close and supersede

A `close` result sets the change to `stopped`. The caller, or the owner, may close at any step, with a note, and that includes a step after the merge. Closing ends the change's open asks and step tasks, and it notifies the sessions that held a task.

A change that replaces another is a convention, not a link. The old change is closed with `close`, and the new change is submitted. The code does not record that one replaced the other.

**Where:** the `close` branch of `ReportStep` and `closeStepTasks` in `internal/gateway/delivery_changes.go` and `internal/gateway/delivery_tasks.go`; the branch check in `SubmitChange`.

## From evidence to defects, ratings and the picker

The gateway records some defects by itself. It knows each change's author, so it does not wait for an agent to remember ([defect_auto.go](../internal/gateway/defect_auto.go)).

| Event | Defect recorded | Severity | Counted |
|---|---|---|---|
| verify fails after merge | verify | 3, or the `severity` field of the report | once per revision |
| test fails before merge, not marked as a flake | test | 1 | once per revision |
| a later submit sets `fixes=` on a shipped change | fix | 2, or `fixes_severity` | once per fixed change |

A failed accept or review is normal iteration. It records no defect. The picker still counts it as a failed step, once per task chain.

Callers can also record defects by hand, with `agent_worker_rate`:

- `defect_found_later` with severity 1 to 4, for problems the steps could not see;
- `brief_defect`, charged to the caller, not the worker. The gateway records one when a brief is refused twice and then started anyway, at severity 2.

A hand-filed defect for a worker in the last hour hides the automatic one for that worker.

Caller ratings (1 to 5) are stored with the run's facts and shown on the Quality tab. The picker's score does not read them. It reads defects, failed steps and the judge's difficulty result. A worker task and its continuations count as one task.

**Where:** `internal/gateway/defect_auto.go`; `pickerStepCounts` and `pickerTaskRecordsUsing` in `internal/gateway/picker.go`; `internal/gateway/ratings.go` (the comment on read-only use); `internal/gateway/brief_check.go` (`recordBriefDefectFromBrief`); [0161](decisions/0161-pick-by-cost-per-accepted-result.md) and [0173](decisions/0173-picker-quality.md) for the picker.

## What the owner sees

The Needs you section of the Desk holds the approvals and decisions that the pipeline sends to the owner. Each of those is an Inbox item, and answering it records the owner's evidence. A change's steps and what is waiting are on the Changes screen and in `agent_changes_find`.

The owner can also report a verify step himself, on a merged change. The gateway takes that as a direct owner report.

**Where:** `answerDeliveryApproval` in `internal/gateway/delivery_approval.go`; `ownerVerify` in `ReportStep`.

## Why this makes a swarm safe to supervise by exception

A swarm is safe to leave alone when three things hold.

- Every step has one owner. A change cannot pass a step without a report on its exact commit. A missing step shows as waiting, so it is never assumed done.
- A failure leaves a record: a defect, a severity, a worker and a model. The next picks and the next reviews see it.
- The owner sees only what needs him: approvals, decisions and failed verifies. Routine passes stay in the record, where he can read them.

The owner does not have to trust a worker's summary. He can read the rows, and each row names its commit and its reporter.

**Where:** the sections above; [0057](decisions/0057-delivery-pipeline.md) and [0164](decisions/0164-merge-queue-is-the-single-test-gate.md) for the step and test rules.

## Worked example 1: a change that shipped

Change `ch_cd3247148058b46d` fixed a bug where a nested message batch left inner messages dispatched forever. It touched migrations, so it needed a review. All times are UTC, from the gateway record.

The pipeline for the agent-net project, as recorded on this change, is: accept (caller), test before merge (merger), review (caller, from difficulty hard), approve for merge (none), merge (merger, after accept, review, a test and the approval), test after merge (merger), release after merge (merger, after a full test on the same commit) and verify after merge (caller, after release).

| Time | Step | Result and commit | Who | What the record says |
|---|---|---|---|---|
| 2026-11-19 18:49 | revision 1 | head `39bcd403` | worker | The worker's hand-back created the change. |
| 18:49 | accept | pass on `39bcd403` | agent-net lead (caller) | The diff does what the brief asked. A read-only check on a copy of the real gateway database counted the stuck rows. |
| 18:49 | review | pass on `39bcd403` | agent-net lead (caller) | Required: "touches migrations". No defects found. |
| 19:02 | test before merge | pass on `39bcd403`, tested commit `657aa1ef` | merger | Train `merge-train-0675`, merged onto main. Format, vet and the gateway tests for the changed code passed. CI runs once on the train tip. |
| 19:21 | merge | pass, merged commit `657aa1ef` | merger | CI run 44594334777 was green on the train tip. Main was fast-forwarded. The change became merged. |
| 19:21 | test after merge | pass on `657aa1ef`, full suite | merger | The same CI run, on the commit that landed. |
| 19:33 | release | pass, tag `v0.5.106` | merger | The release workflow succeeded and the three machines were upgraded. |
| 19:33 | verify | pass on `657aa1ef` | agent-net lead (caller) | Read-only check on the real gateway database at v0.5.106. The three stuck messages were delivered and no session had a stored or dispatched message. The note also says the forward path is not yet seen live. |

The change ended in state `done`. Each row is a separate report, and each step counted only on the commit it names.

**Where:** read with `agent_changes_find` (change `ch_cd3247148058b46d`); the rules are in `ReportStep` and `Pipeline.Accepts`, as above.

## Worked example 2: a change that passed tests and failed verify

Change `ch_01128b1ba413e8aa` made the skill suggester ignore common words. It was simple, so review was left out. It passed every step up to verify.

| Time | Step | Result and commit | Who | What the record says |
|---|---|---|---|---|
| 2026-11-20 01:54 | revision 1 | head `10f1baa7` | worker | The hand-back created the change. |
| 01:54 | accept | pass on `10f1baa7` | agent-net lead (caller) | Read the diff. The tests cover each done line. Review was left out because the difficulty was simple. |
| 01:56 | test before merge | pass on `10f1baa7`, tested commit `bb209ecb` | merger | Train `merge-train-0677`. Format, vet and the gateway skill tests passed. |
| 02:15 | merge | pass, merged commit `bb209ecb` | merger | CI run 44657251599 was green on the train tip. The change became merged. |
| 02:15 | test after merge | pass on `bb209ecb`, full suite | merger | The same CI run. |
| 02:25 | release | pass, tag `v0.5.108` | merger | Release workflow succeeded. The gateway had no migration to apply. |
| 02:25 | verify | **fail**, severity 2, on `bb209ecb` | agent-net lead (caller) | Live on v0.5.108, the suggester still returned eight skills on generic words. The note blamed the context text, which included the inherited charter and the bar rules. That cause was wrong, as the fix change below shows. |

Three things happened next, all from the record.

- The failed verify was filed as a defect on the worker, at severity 2. The gateway read severity from the report's `severity` field. The Desk log got a line for it.
- The change stayed in state `merged`, not `done`. Verify is the last step, and it failed.
- The fix is a new change, `ch_afd4fb1773bb1d9c`, submitted with `fixes=ch_01128b1ba413e8aa`. The fix rule above counts it once against the old change.

The fix change has its own record. Its first revision failed accept, and its second passed. Its verify was closed, not passed.

| Time (UTC) | Step | Result and commit | Who | What the record says |
|---|---|---|---|---|
| 2026-11-20 02:29 | revision 1 | head `a36b0cfe` | worker | The worker's hand-back created the change. |
| 02:31 | accept | **fail** on `a36b0cfe` | agent-net lead (caller) | The lead ran the new suggester on a copy of the real gateway database, read-only. Voicemechs still got eight bad picks. The noise came from the repo's own charter process fields (owner, deploys, freeze, never, test), not from the inherited text. A failed accept is normal iteration, so it records no defect. |
| 02:34 | revision 2 | head `6fb13443` | worker | The second hand-back. The suggester now matches on the repo's purpose and description only. |
| 02:35 | accept | pass on `6fb13443` | agent-net lead (caller) | On the same copy of the database, voicemechs got 4 picks, down from 8. The note says picks on one word are still loose, which is the limit of word matching. |
| 02:35 | review | skipped | agent-net lead (caller) | Left out: the difficulty was simple, so review was not required. |
| 02:37 | test before merge | pass on `6fb13443`, tested commit `0600cc30` | merger | Train `merge-train-0678` on main `bb209ecb`. Format, vet, the rule check and the gateway skill tests with race passed. |
| 03:10 | merge | pass, merged commit `0600cc30` | merger | CI run 44662132659 was green after one rerun of a daemon shard that had flaked. The change became merged. |
| 03:10 | test after merge | pass on `0600cc30`, full suite | merger | The same CI run, on the commit that landed. |
| 03:19 | release | pass, tag `v0.5.109` | merger | The release workflow succeeded. The gateway, Mac and research nodes were on v0.5.109. The schema had no migration. |
| 03:19 | verify | **closed** on `0600cc30` | agent-net lead (caller) | Not a pass. The owner decided, through the CTO, to remove the skills suggest action altogether, so there was nothing left to check live. The change state became `stopped`, not `done`. |

The close is the record's answer for a verify that will not run. It is not a pass. The `v0.5.109` code kept the suggest action until the removal change ships.

The lesson: evidence on the real setup caught two wrong conclusions that passing tests had not. The first was that the change worked, because its tests passed. Verify on the real setup showed it did not. The second was the cause, the inherited text. The accept of the first revision, run on a copy of the real database, showed the noise came from the repo's own charter fields.

**Where:** read with `agent_changes_find` (changes `ch_01128b1ba413e8aa` and `ch_afd4fb1773bb1d9c`); the defect rules are in `internal/gateway/defect_auto.go`; the close rule is in `ReportStep` in `internal/gateway/delivery_changes.go`.

## Limits

- **Evidence is what the reporter says.** The gateway checks the shape of a report: its commit, revision, step order, required fields and the merge proof. It does not run tests or read a database. A verify pass rests on the caller's note and on what that note says was checked.
- **Silence is not evidence.** A step with no report stays ready, waiting or stuck. Only a report passes it.
- **Independence is about who did the work, not how well they did it.** Review independence is off by default ([0083](decisions/0083-review-independence-default-none.md)). A pipeline has to ask for it.
- **Newest evidence wins.** Older rows stay in the record. A reporter can change a result on the same commit, and the older row is no longer read.
- **Ratings do not move the picker.** The picker reads defects, failed steps and judge results.
- **Pipelines are per project.** The default in code is not what every repo runs. The first example above shows the agent-net project's pipeline, which differs from the default.
- **Supersede is a convention.** Nothing in the record links a replacement change to the one it replaced.
- **Merge proof depends on the daemon.** The gateway asks the daemon to confirm that the merged commit contains the head. It refuses the merge when the answer is no.

**Where:** `ReportStep` (`MergeContainsHead`, `deliveryMergeProofRequired`) in `internal/gateway/delivery_changes.go`; the sections above for each limit.

## Where to look

- `agent_changes_find` with a change ID reads one change in full: its revisions, its steps and its evidence.
- `agent_step_report` and `agent_step_claim` are the calls a step owner makes.
- The Quality tab shows the same record in the TUI.
