# 0057 - Delivery pipeline: git holds the work, agent-net routes the steps

Date: 2026-11-04

## Context

The step 6 delivery settings (`delivery.merge_authority`,
`delivery.tests_before_merge`, `delivery.deploy_rule`) only change what agents
are told. No code checks that a review, a test or an approval happened before a
merge, and no setting can move a step to another node. The design is
[docs/design/delivery-pipeline.md](../design/delivery-pipeline.md), checked
against the code on 2026-11-04. The owner approved building it on 2026-11-04.

## Decision

**Git holds the work.** A change is a project, the machine that holds the
branch, a branch and its head commit. A revision is the head at one complete
hand-back or one submit, not every commit. `continue` moves a change to a new
branch; a new head commit makes a new revision. agent-net does not build a CI
runner, a review UI or a merge queue.

**agent-net decides who does each step and keeps the proof.** The steps are
`accept`, `review`, `test`, `approve`, `merge`, `release`, `deploy` and
`verify` (`accept` is the old "check": that word already names the idle check
and an Inbox kind). Each step has an owner: the caller, a graph node, `github`
(test and merge only), the owner, or for `approve` also `delegate` or `none`.
The default deploy owner is the node with a `deploys` edge to the project.

**Evidence is keyed by the exact commit.** Each result names the change, the
revision and a full commit hash: the head before merge, the merged commit after
it. Evidence of an older revision does not count. Evidence comes only from an
authenticated step report, an answered Inbox item or GitHub, never from a
message, and it never changes the commit. A step may run once before merge and
once after it, so today's CI run on the merged commit is an after-merge `test`
that `release` requires.

**Independence compares the model maker** (anthropic, openai, google,
deepseek, and so on), so a Sonnet review of an Opus change is not independent.

**The pipeline replaces the three step 6 settings, which become shorthand.**
Each shorthand value maps to a part of the pipeline, as the design's table
says. Reading a shorthand gives the value the pipeline implies, or `custom`.
Each maturity level's defaults resolve to a pipeline through that mapping.
`tests_before_merge` `suite` means merge requires a test with any suite named;
deploy rule `brief` adds no deploy step. The pipeline of today's behaviour is
the owner's way: every step owned by the caller, no approvals, and CI, release
and verify after merge. Its shorthand reads `suite`, `full`, `brief`.

**The pipeline is a protocol type; the gateway part decides its storage.** The
settings layer refuses an unordered rule at several levels, so the pipeline
cannot be one rule setting (design open question 1).

The shared contract is `internal/protocol/delivery_pipeline.go`: the types,
the rules (`Ready`, `Status`, `NextSteps`, `Accepts`), the shorthand mapping,
and the frames `change.submit`, `change.get`, `change.list`, `step.report` and
`step.task`. The tools are `agent_change_submit`, `agent_step_report` and
`agent_changes_find`. Until the daemon part lands they check their arguments
and answer "not built yet".

**Step tasks coalesce per recipient session.** When a step becomes ready for an
owner who already has an undelivered or recent (last 30 s) step-task notice or
who just reported the step before it, tasks coalesce into a pending batch sent
after a short quiet window (10 s with no new task for that recipient, capped at
25 lines per message). A single isolated task keeps the full format with diff
stat and evidence.

**Two consecutive steps of one owner are one task.** When a step becomes ready
and the step after it in the pipeline has the same owner and needs only that
step, the gateway queues both and names them in one message,
`steps: release, verify (after merge)`. Steps left out of the change's pipeline
do not break the pair, and it never crosses the merge. This happens when the
first step is first queued, so the pair is one notice; a step that was already
handed out keeps its own task. The message tells the owner to claim and report
each step in order. Each step keeps its own task, evidence and report on the
exact commit, and reporting the first does not send another message for the
second.

## Not decided here

The design's open questions stay the owner's: where step owners are stored,
whether the daemon commits at hand-back, branches on other machines, a direct
owner approval under `delegated`, and who approves a deploy under `ask`. The
CTO gets `agent_changes_find` once its tool list has room under its 32 KiB
schema budget.
