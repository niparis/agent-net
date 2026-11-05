# Delivery pipeline: steps done by nodes

**Date:** 2026-11-04
**Status:** The pipeline settings, shorthand cutover, owner controls, node task queue and Inbox approvals are built. See [Checked against the code](#checked-against-the-code) for implementation details.
**Context:** [modularity.md](modularity.md) (objective, recipes, the "merge gate" gap), [settings-layer.md](settings-layer.md) (settings, levels, rules and advice).

## The problem

The owner's words: changing the way a repo merges should mean "another node comes online and takes over the work of reviewing, testing, merging". Today it cannot.

Settings steps 3 to 7 made the delivery rules data: `delivery.merge_authority`, `delivery.tests_before_merge`, `delivery.deploy_rule`. But a rule only changes what agents are **told**. No code refuses a merge: `MergeApprovalFor` works out who may approve one, but only tests call it. The work is still done by hand by the caller session. For agent-net itself, the orchestrator session:

1. reads each worker's report and diff;
2. starts the independent review worker and reads its findings;
3. sends fixes back to the author;
4. merges into a release branch in a worktree of its mirror (`main` does not move yet);
5. builds, vets, pushes the release branch;
6. follows [RELEASING.md](../../RELEASING.md): waits for CI on that exact commit, fast-forwards `main` to it, tags, waits for the release workflow, installs;
7. checks the release live.

agent-net does not know these steps exist. No setting can move one of them to another node, and nothing checks that a step happened before the next.

## The goal

A repo's delivery is a **pipeline** of **steps**. Each step is **owned by a node**. When a worker hands back, agent-net moves the change through the steps itself. Changing the recipe changes who does the work. Every step leaves **evidence tied to one commit**, and a step that needs evidence does not start without it.

The test of success:

- The owner's way (above) is one recipe and behaves exactly as today.
- A merger node comes online with a charter "I review and merge for agent-net". The owner sets it as the owner of those steps. From the next hand-back, the orchestrator no longer sees those steps; the merger does them.
- A friend's way (pull requests, GitHub CI, a human reviewer) is another recipe for the same binary.

## Git and agent-net: who does what

Rule of thumb: **git holds the work; agent-net decides who does each step and keeps the proof.**

| Concern | Owner | Why |
|---|---|---|
| The work itself | git: a branch and its head commit | Already the unit everyone uses. A commit hash names exactly what was reviewed and tested. |
| Integration (merge, fast-forward) | git, run by the merge step's node | No reason to reimplement. |
| Releases | git tags and the repo's own release steps | Each repo already has them (agent-net: [RELEASING.md](../../RELEASING.md), a `v*` tag, `.github/workflows/release.yml`). |
| CI | GitHub Actions when the repo is on GitHub; otherwise a test step run by a node | agent-net must not build a CI runner. |
| Hard enforcement | GitHub's branch protection and required checks, when the repo is on GitHub | The only gate an agent with write access cannot step around. |
| Which node owns which step, per repo | agent-net | Git has no idea of nodes, roles or machines. |
| Routing the change to that node, hand-over when the owner changes | agent-net | Cross-machine, cross-session. |
| The state of each change | agent-net (gateway database) | One view across machines; survives restarts. |
| Evidence (who passed which step on which commit) | agent-net, keyed by project and commit hash; optionally posted to GitHub as checks on the commit | Must not change the commit (so not trailers or amended messages). The worker trailers the commit hook already adds (`Agent-Net-Worker`, `-Model`, `-Harness`, `-Difficulty`, `-Caller`) name the author; evidence does not repeat them. |
| Approvals, notices to the owner, model choice for step workers | agent-net | Partly built: Inbox approval items (`agent_owner_ask` kind approval), CTO proposals for rule changes, the picker. Today an approval is free text sent back to the asking session; nothing ties it to a commit or checks it. |
| Detecting work that skipped the pipeline | agent-net watches the default branch | The fallback when the repo is not on GitHub. |

What agent-net does **not** build: a CI runner, a review UI, a merge queue for repos where GitHub already has one.

**The limit, stated plainly.** On a repo that lives only on our machines (no GitHub copy), nothing can stop an agent with write access from pushing to main. agent-net makes the pipeline the easy path and makes a skip visible: a commit on the default branch with no recorded evidence becomes an Inbox item for the owner at once. Where the repo is on GitHub, agent-net posts its evidence as checks on the commit, and GitHub's branch protection refuses a merge without them.

**Statuses and merge commits.** Branch protection checks the required statuses on the commit that becomes the new tip of the protected branch. Evidence is keyed by the change's head commit, so statuses posted on the head satisfy branch protection only when the target is fast-forwarded to that head. A merge commit is a new commit with no statuses, and a push of it to a protected branch is refused. So a protected target uses `ff-only` merges, or the merge step posts the statuses on the merged commit (after CI ran on it) before the target branch moves. `delivery.Merge` supports both modes; the recipe chooses.

## Concepts

### Change

One unit of delivery: a project (its identity from decision 0031, the basename of the `origin` remote), a branch, a head commit, the machine that holds the branch, where it came from (worker run, agent session, or a person), and its caller (the session that started the worker). Created by agent-net when a worker hands back with status `done` and complete, after any automatic continue. "Complete" is the `complete` field of `agent_handback` (absent means yes); a Codex worker has no tools, so for Codex it is the last `complete: yes` or `complete: no` line of its report (absent means yes). Or created by an agent with a tool call (`agent_change_submit`) for work done outside a worker.

A **revision** is the branch head at one complete hand-back, or at one submit. A worker makes many commits in a run; only the head at hand-back is a revision. Evidence belongs to a revision, so a fix after a review invalidates that review unless the step says otherwise. A fix arrives in one of three ways today, and each must become a new revision of the same change:

- the caller messages the worker after its hand-back: the same session starts again on the same branch;
- the caller uses `continue`: a new worker on a **new branch** made from the old one, so the change moves to the new branch;
- the caller starts a new worker with `base` set to the old branch: the same as `continue`, but nothing records the link, so the start must name the change.

### Step

A kind of work in the pipeline, from a fixed list that agent-net knows how to route:

| Step | Done by | Evidence it leaves |
|---|---|---|
| `accept` | the caller (today's "read the report and diff") | accepted or sent back, with a note |
| `review` | a node, which may start review workers | pass, or findings by severity |
| `test` | a node, or GitHub's CI | pass or fail, the command or CI run link, the commit, and evidence fields (below) |
| `approve` | an approver (below): the owner, a delegate, or a node | approved or refused, by whom, for the exact commit |
| `merge` | a node, in a worktree of its mirror | the merged commit on the target branch |
| `release` | a node, running the repo's release steps | the tag, the release run link |
| `deploy` | a node, running the repo's deploy command | where it was deployed, the commit, the run link |
| `verify` | a node | what was checked live, pass or fail |

The first draft called `accept` "check". That word is taken twice: the idle check that asks an idle agent whether it is done (decision 0036) and the Inbox kind `check`.

A recipe may leave steps out (a solo repo: `test`, `merge`). The list grows only when a recipe needs a step the list cannot express.

`approve` is separate from `merge` on purpose: who **decides** a merge and who **does** it are different questions. The same holds for deploy: a `deploy` step may require an `approve` step of its own (`approve.for: deploy`). Release builds and publishes a version; deploy puts a version into service on a machine. A repo may have either, both or neither.

### Evidence fields

A step reports pass or fail, plus named fields the pipeline may require. A field is a fact agent-net can check or the step owner states, recorded for the exact commit:

| Field | Set by | Checked how |
|---|---|---|
| `tests_added` | the test step | agent-net looks in the change's diff for new or changed test files (per language: `_test.go`, `test_*.py`, `*.test.ts`). Required only when the diff changes code outside tests: today's rule asks for tests "for new behavior", so a docs-only change passes. The owner can override with a reason. |
| `suite` | the test step | which suite ran (changed packages, full suite, CI) |
| `findings_open` | the review step | count of blocker findings not yet fixed; merge requires 0 |
| `independent` | agent-net | the step's session differs from the author's (the `Agent-Net-Worker` trailer), or its model family differs (open question 6) |

A rule then reads `merge requires test(suite=full, tests_added=yes), review(findings_open=0)`.

### Step owner

Per repo, each step has an owner. Default for every step: the caller, which is today's behaviour. An owner may be:

- a graph node (a component or an architect). A charter has no role field: the owner's assignment makes a node the owner of a step, and the node's charter says what it accepts. One of the node's sessions does the work, never a worker. A Codex worker has no agent-net tools, so a step owner that starts a review worker reads its report and reports the step itself;
- `github` (only for `test`, and for `merge` with GitHub's merge queue: agent-net reads the result for the commit; no session works);
- `owner` (the person; the step becomes an Inbox approval).

For `deploy`, the default owner is the node with a `deploys` edge to the repo, where there is one. The graph already says that node deploys the repo under the repo's deploy rule.

For `approve`, the approver may also be:

- `none`: the step is left out (today's merge authority "full");
- `delegate`: whoever governs this node in the graph, through a `governs` edge in an allowed mode (`delivery.delegated_approver_modes`, default owner and arbiter; the third mode, advisor, is left out), or the owner. Governs edges go only from an architect to a component. This is today's "delegated", resolved from the graph at the moment the step starts, so a new architect takes over without a pipeline change.

Evidence is never a message. A trust note, a relayed owner instruction (its note says "On its own this does not approve a merge") or a CTO note does not count. Evidence comes from an authenticated step report, an answered Inbox item, or GitHub. The pipeline keeps today's fallbacks: a session that agent-net does not know gets merge authority `ask`, and a repo with no graph node gets the network value.

### Rules on steps

Rules are settings of kind `rule`: only the owner, or a CTO proposal the owner approved, may change them. A repo value may only tighten a network value the owner has set; with no network value, it replaces the default.

- **requires**: a step does not start until the listed steps have passed **on the same commit**. Example: `merge requires review, test`.
- **independent**: the evidence must come from a different session than the author, or from a different model family (`review.independent: family`).
- **on_fail**: where a failed step sends the change (back to the author worker as a fix, or to the caller).
- **after_merge**: steps that run on the merged commit (`test` again after a multi-worker merge, `release`, `verify`).

**The settings layer cannot hold these yet.** A rule set at both network and repo level must have an order (enum, bool, number or set), so that the stricter value can win; the loader refuses an "unordered rule". A list of steps with owner names has no such order. See open question 1.

### Pipeline

The ordered steps with their rules, per repo. It lives in the recipe (`delivery.pipeline`) and resolves through the settings layer like any other setting, with its source shown, once open question 1 is settled.

### One source of truth: the pipeline replaces the step 6 delivery settings

Settings step 6 moved five maturity controls into settings: `delivery.merge_authority`, `delivery.tests_before_merge`, `delivery.deploy_rule`, `workers.sandbox_level` and `charter.judge_consumers`. The first three are delivery. If they stayed next to the pipeline, two places would say how a repo merges, and they would drift. So the pipeline is the one source, and those three become **shorthand**:

- Setting a shorthand value writes the matching part of the pipeline, in the same change, with history.
- Reading a shorthand shows the value the pipeline implies, or "custom" when the pipeline says something the shorthand cannot.
- The rule text agents see is rendered from the pipeline, not from the shorthand. Today that text is: the rules line in each node's context and in worker briefs (`RulesText`), the merge rule the gateway puts on every delivered message (read into the trust note and the owner-instruction note), and the deploy rule a deployer gets. The notice checklist (`delivery.caller_review_and_merge`) is advice, which the CTO may change alone; only its merge part would come from the pipeline.

The mapping. Today's texts are the defaults in `internal/settings/standard.yaml`.

| Shorthand | Value | Today's text, in short | Pipeline |
|---|---|---|---|
| `merge_authority` | full | "merge and push to the default branch" | no `approve` step. The merge step owner still merges only when its user asked, as today's checklist says. |
| | delegated | the owner approves directly or with an approval note from `agent_owner_ask` kind approval; a delegate approves in a message from an architect with a governs edge in an allowed mode | `approve` before merge, approver `delegate`. The owner approves in the Inbox (or in the session: open question 4); a delegate with a step report from its own session, not with a message. |
| | ask | the owner's written approval for the exact action, with `agent_owner_ask` kind approval or in a Codex worker report | `approve` before merge, approver `owner`, written only: an answered Inbox approval item for this change and commit. agent-net raises the item itself, so no worker has to ask. |
| `tests_before_merge` | optional | "tests optional" | merge does not require `test` |
| | suite | "run the suite" | merge requires `test` with any `suite` value (the text does not say which suite) |
| | suite-new-tests | "run the suite and add tests for new behavior" | merge requires `test(tests_added=yes)` |
| | suite-new-tests-review | "run the suite, add tests for new behavior and get a review" | merge requires `test(tests_added=yes)` and `review(findings_open=0)`. `findings_open=0` is stricter than today's text. |
| `deploy_rule` | brief | "deploy as the brief says" | no `approve(for: deploy)`. A `deploy` step exists only when the recipe has one. |
| | ask | "ask before deploying" (names no approver) | `deploy` requires `approve(for: deploy)`, approver `delegate` (open question 5) |
| | owner | "ask the owner", written, as merge `ask` | `deploy` requires `approve(for: deploy)`, approver `owner`, written only |

The maturity defaults keep working: each maturity level names a pipeline, which is what the shorthand values resolve to today. An explicit repo value wins over the maturity default.

| Maturity | `tests_before_merge` | `merge_authority` | `deploy_rule` |
|---|---|---|---|
| early | optional | full | brief |
| growing, or not set | suite | full | brief |
| mature | suite-new-tests | delegated | ask |
| critical | suite-new-tests-review | ask | owner |

`charter.judge_consumers`, `charter.autonomy_mode` and `workers.sandbox_level` are not delivery. They decide how requests come in, whether an idle agent is woken, and what a worker may touch, and stay settings of their own.

The worker settings (branch prefix, commit trailers, cleanup, saving unfinished work, the brief texts) describe how the worker step runs, and do not change.

## How a change moves

1. A worker hands back, complete. Every harness reaches one daemon function for this (`handbackToNote`). There, after the commit it makes for an Antigravity worker, the daemon records the change: project, machine, branch, base, the head commit, and whether the worktree still has uncommitted changes. This is revision 1. Today the head often does not hold the work: see [What the head is at hand-back](#what-the-head-is-at-hand-back).
2. agent-net finds the first step whose requirements hold and sends a **step task** to its owner, in the same form as a worker notice: the change, the commit, the diff stat, the evidence so far, what to report. The sender is agent-net itself, as for the idle check, so edge request rules (`ask`, `never`) do not hold it and the receiver's consumer rule does not judge it.
3. The owner does the work. It may start workers (a reviewer node starts an independent review worker on another model family; the picker still chooses models).
4. The owner reports with `agent_step_report` from its own session: pass, fail with findings, "needs the owner", or "close without merge" (its user does not want the change). agent-net checks the commit in the report is still the head of the change.
5. Pass: the next step starts. The pre-merge test waits for accept to pass on the same commit; a new revision needs accept again. Tasks already ready or claimed before this rule remain available. Fail: the change goes where `on_fail` says. Sending it back to the author worker works only through the caller today: only the session that started a worker may start it again or continue it, and only that session's messages count as fix rework. So `on_fail: author` sends the findings to the caller, or the step owner starts its own fix worker with `base` on the change's machine. A fix makes a new revision; steps with `requires` on it run again.
6. `merge` checks, at the moment it runs, that every required step passed on the change's head, with the required evidence fields, and that the merge keeps that head: a fast-forward to it, or a merge commit with it as a parent. A squash or a rebase makes new commits with no evidence, so the merge tool refuses them. This check is agent-net's, so it holds even when the merge node's model is wrong. `deploy` checks its own requirements the same way.
7. After merge, `after_merge` steps run on the merged commit. A merge commit is a new commit: tests that must cover the merged result (CI in the owner's way) are `after_merge` steps, and `release` requires them on that exact commit.
8. The caller gets one notice at the end (merged, released, or stopped and why), not one per step, unless it owns a step.

## Hand-over

The owner changes a step owner (a rule change: from the TUI, or a CTO proposal approved by the owner). Then:

- Changes not yet at that step go to the new owner.
- A step in progress stays with its current owner until it reports, unless the owner moves it explicitly ("hand over now"). Then the old owner gets a stop notice and the new one gets the step task with the evidence so far.
- The old owner stops receiving step tasks at once. The rules line in its node context, which is rendered from settings, changes with the same change; the charter itself is not edited.
- If the new owner is offline, step tasks wait in the queue and the owner sees "waiting for merger node (offline)" in the TUI. This queue is new: see [Routing to sessions](#routing-to-sessions).

## What the owner sees

- A **Delivery** view per repo: each open change, its revision, the step it is in, who owns that step, the evidence so far, and how long it has waited.
- Each step owner's queue.
- Inbox items for steps owned by the owner, for skipped-pipeline commits, and for an owner who stays offline.
- The CTO reads all of it and proposes pipeline and owner changes.

## Gaps the code shows

### Independence can be attributed to the wrong worker

A caller can report a step it did itself with `done_by` set to a worker it
started. The gateway then treats that worker as the doer and may mark the
evidence independent. Step 3 must verify who did the work before using that
field to satisfy an independence requirement.

### What the head is at hand-back

- Claude Code, OpenCode, Pi and Codex workers commit only if the task asks ("Commit your work there if the task asks for it."). The daemon does not commit at their hand-back. The head is then the base commit or the worker's last commit, and the work may sit uncommitted in the worktree. The notice hides this: its diff stat is `git diff --stat <base>` of the working tree, plus untracked files.
- An Antigravity worker does not commit. The daemon commits all its changes at hand-back, with the title as the subject, unless a merge or rebase is in progress (then it leaves the worktree as it is).
- A worker that ends without a hand-back (idle, too old, exited, stopped, caller gone) gets its changes saved in an "unfinished work" commit when `delivery.save_unfinished` is on. It is not a change.
- `continue` commits a handed-back worker's leftover changes under the subject "agent-net: unfinished work of worker ... (handback)", even when the work was complete. With `delivery.save_unfinished` off it does not commit them, and the new branch starts without the work.
- A Codex worker hands back at the end of every turn and keeps running, and its notice goes to whoever sent that turn. Each complete turn is a hand-back, so one Codex run can make several revisions.

So a change cannot be "the head at hand-back" until the head holds the work. See open question 2.

### Routing to sessions

- A message goes to one session, never to a node, and expires after 24 hours. A step task for a node needs a new queue in the gateway, kept until a session of that node takes it. A node can have several sessions: the step task goes to its online sessions that are not workers, and the first one to claim it (or report on it) takes it.
- The caller is a session, not a node. When the caller ends, its running workers are ended after a grace time with no notice, and no other session may resume or continue them. A step owned by "caller" must fall back to the caller's node (another session of the same project), then to the owner.
- An offline step owner may have no session at all. Starting one is `session_start`, which is a CTO proposal today. The pipeline does not start sessions by itself: it shows "waiting for X (offline)" and, after a set time, adds an Inbox item.
- A step owner that sits idle with an open step task: reuse the idle check and the node's autonomy rule (decision 0036) instead of a new reminder.

### Branches on other machines

A worker's branch lives in the project repository on the worker's machine, and workers do not push. A reviewer or merger on another machine cannot read it. Either the step runs on that machine (a reviewer can start its worker there with `machine` and `base`), or the branch is pushed to the shared remote. See open question 3.

### Merges in a mirror worktree

The orchestrator's merge worktree is a worktree of agent-net's own mirror (a bare repository only the daemon writes), so worker-writable config is not read for checkout, merge or push. The change branch is fetched from the worker repository by name. The merge check therefore works on commit hashes, not on worktrees: the change's head must be present where the merge runs, and the merge must keep it (fast-forward or merge commit). The same rule keeps cleanup working. Cleanup removes a worker's worktree and branch only when the branch head is reachable from the default branch, so a squash merge would leave the branch forever, and a change closed without merge keeps its branch, as today.

### The default-branch watcher

Worker commits reach the default branch as parents of merge commits, or in a row after a fast-forward. The watcher walks the first-parent line of the default branch from the last commit it knows. Each commit on it must be a merged commit that a merge step recorded, or lie under one (the commits of a fast-forwarded change). A commit past the last recorded one is a skip. The watcher uses the default branch the gateway already gives cleanup.

### Approvals today are free text to one session

`agent_owner_ask` kind approval stores the action as text. When the owner approves, agent-net sends the asking session a message with the approval note. The item names no change and no commit, and if that session has ended the note reaches nobody. The `approve` step reuses the item and the note, adds the change and the commit to the item, and records the answer on the change, where the merge check reads it.

## Recipes

**The owner's way today** (this must reproduce today exactly). The node context of agent-net sessions has no rules line today, so agent-net has no maturity, merge authority `full` and deploy rule `brief` (or `ask`); `tests_before_merge` is `suite` unless the repo sets it.

| Step | Owner | Rules |
|---|---|---|
| accept | caller | reads the report and diff, runs its checks in the worktree (today's checklist) |
| test | caller | `suite` is what the caller ran; part of accept today |
| review | caller | independent: family; optional for small changes (decision 2) |
| approve | none | merge authority "full" for agent-net |
| merge | caller | into the release branch in a worktree of the caller's mirror, not `main`; requires accept and test, and review when present |
| test | github | `after_merge`: CI on the merged commit, full suite |
| release | caller | requires the CI test on that exact commit; then RELEASING.md: fast-forward `main` to it, tag, release workflow, install |
| verify | caller | after release |

voicemechs (deploy rule "brief", the one real node setting the step 6 cutover found) adds `deploy` with no approval. This is a fact of the real setup; this check did not read it.

**The owner's way with a merger node:** the same, with merge, both tests and release owned by `merger`. The orchestrator keeps `accept`, review and verify. The merger works on the machine that holds the branches, unless open question 3 says otherwise.

**A friend's way (pull requests):**

| Step | Owner | Rules |
|---|---|---|
| review | owner (a person, on the pull request) | |
| test | github | required check |
| merge | github (merge queue) or a merger node | requires review, test |
| release | caller or merger | requires full CI on the exact merged commit |

**Solo, minimal:** test by the worker's own node, merge by the caller. No review.

## Build order

Each step ships alone. Step 1 keeps today's behaviour exactly.

1. **Changes and evidence, recorded only.** The daemon records a change at every complete hand-back, with the head commit and whether the worktree is clean, and the gateway stores it. Evidence is recorded when today's flow does its steps (the caller reports `accept`, reviews and merges through new tools). Nothing is routed or refused. The Delivery view shows it. This proves the model on real traffic.
2. **Step owners and routing.** `delivery.pipeline` and step owners, default "caller", stored as open question 1 says. Step tasks go to the owner; with all owners set to "caller" they go to the caller's session, as notices do today, and behaviour is today's. In the same step, the three shorthand settings start writing and reading the pipeline, and agents' rule text is rendered from it; the step 6 fixture (`internal/protocol/testdata/delivery-baseline.json`, checked by `TestDeliveryBaselineFixture`) must stay byte-identical.
3. **The merge and deploy checks.** Status: merge reports already require the configured evidence. Review is now also required when the judge's risk score is at or above `delivery.pipeline.review.required_risk` (default 4), or the daemon finds sensitive paths at hand-back. Risk and sensitive-path fields are optional for older peers. Merge reports now also carry an optional daemon check that `merged_commit` is a commit and keeps the change head, using `git merge-base --is-ancestor`. The gateway refuses negative proof and requires proof from daemons at version 0.4.131 or newer, including dev builds. Older daemons without proof keep the existing behavior. Missing diffs on session submissions require review, and sensitive directory names count as well as filenames. Deploy checks and the default-branch watcher are still pending.

    The merge and deploy tools refuse without the required evidence and approvals on the exact commit, including the evidence fields (`tests_added`, `findings_open`), and refuse a merge that does not keep the change's head. The default-branch watcher flags commits with no evidence.
4. **GitHub integration.** `test` owned by `github` reads CI results; evidence posted as checks on the commit so branch protection can enforce it.
5. **Hand-over** with in-flight changes, the gateway queue for step tasks to a node, the fallback when the caller has ended, and the "waiting for an offline owner" state.
6. **Recipes:** load, choose and export a named pipeline per repo. Write the friend's way and list what it still cannot express.

The first real use: a merger node for agent-net, after step 5.

## Decisions

These were open questions in the first draft; each has an obvious answer, so they are settled here. Each is a setting where it may vary later.

1. **Where the merge runs:** on the merge step owner's own machine, in a worktree of agent-net's own mirror. The change's branch must be on that machine (open question 3).
2. **Small changes:** review is required from difficulty "hard" up (hard and very_complex), using the judge's label for the run, or from risk 4 up, or for sensitive paths. `delivery.pipeline.review.required_risk` may lower the risk threshold, with lower values stricter. Risk means `WorkerAssessment.Risk`, shown as K in the judge note; R there means reasoning. Sensitive paths are `internal/gateway/store.go`, `testdata/migrations.lock`, path components containing auth, token or security (case insensitive), `internal/daemon/files*.go`, `install.sh` and `.github/workflows/`. Missing diff evidence requires review. Existing triggers are kept on later submissions. A judge that failed or had low confidence counts as "normal", as the picker does. A change submitted with `agent_change_submit` has no label and counts as "normal". A rule (`review.required_from`), so a repo can require it always.
3. **Batching:** merge each change on its own; release a batch, which is everything merged since the last release.
4. **Changes from outside a worker:** any agent in the repo may submit one with `agent_change_submit`.
5. **GitHub only at first:** the design does not depend on GitHub, so other hosts can be added when a recipe needs one.

## More decisions (from the check against the code)

The owner took each recommendation on 2026-11-04.

7. **Where step owners and the pipeline live:** step owners are graph edges, like `deploys` today (for example a `merges` edge from the merger node to the repo); the step rules are ordered settings: `requires` lists as sets (more is stricter), approvers as an enum ordered none, delegate, owner. No new setting type.
8. **Commit at hand-back:** the daemon commits leftover changes at every complete hand-back, as it does for Antigravity, with the trailers and the title as the subject.
9. **Worker branches on other machines:** steps run on the machine that holds the branch, until a merger on another machine is wanted; then the daemon pushes the change branch to `origin` at hand-back, only for repos whose pipeline has a step owner on another machine.
10. **A direct owner approval under `delegated`** counts, recorded as "approved by the owner, reported by session X"; `ask` stays written only.
11. **Who approves a deploy under `ask`:** approver `delegate` (the owner or a governing architect).
12. **"Model family" for `independent`:** the model maker (Anthropic, OpenAI, Google, DeepSeek and so on), so a Sonnet review of an Opus change is not independent.

## Checked against the code

An independent check on 2026-11-04 of the draft against the code at commit `56466382`. Paths are in this repository.

**Confirmed:**

- The three delivery settings, their values, defaults and maturity defaults: `internal/settings/standard.yaml:13-51`. Only the owner, or a CTO proposal the owner approved, changes a rule: `internal/gateway/settings.go:109-114`.
- `delegated` approvers are the owner plus architects with a `governs` edge in a mode from `delivery.delegated_approver_modes` (default owner, arbiter): `internal/protocol/maturity.go:252-272` and `:332-379`, `internal/settings/standard.yaml:234-241`. Governs edges are architect to component, modes advisor, arbiter, owner: `internal/gateway/store_graph.go:622-646`.
- The merge rule rides on every delivered message, resolved from the receiver's project, `ask` for an unknown session: `internal/gateway/graphread.go:145-164`, `internal/gateway/server.go:1947-1953`. Trust and owner-instruction notes carry it: `internal/protocol/trust.go:29-70` and `:100-109`.
- The worker trailers and the commit hook: `internal/daemon/worker_commit.go:14-59`, `internal/settings/standard.yaml:242-281`.

**Corrected:**

1. Maturity levels are early, growing, mature, critical, not "prototype, beta, production, critical": `internal/protocol/maturity.go:17-22`, `internal/protocol/graph.go:90-101`. Added the defaults table.
2. `release.sh` is not in the repository. The release steps are in `RELEASING.md`.
3. Nothing enforces a merge rule today: `MergeApprovalFor` is called only by tests (`internal/protocol/delivery_fixture_test.go:28`, `internal/protocol/maturity_test.go:134`). Approvals are free text sent to the asking session: `internal/gateway/store_inbox.go:18-19` and `:143-148`.
4. The mapping table: `full` keeps "merge only if your user asked" (`internal/settings/standard.yaml:191-199`); `delegated` lets the owner approve directly (`:102-110`); `ask` is written only and covers the Codex worker report path (`:111-118`, `internal/protocol/maturity.go:370-374`); `suite` names no suite (`:70-77`); `findings_open=0` is stricter than "get a review" (`:86-93`); deploy `ask` names no approver (`:127-134`); deploy `brief` is the default for every repo below mature, so it cannot add a deploy step by itself (`:43-52`).
5. The owner's way: the merge goes to a release branch, CI tests the merged commit, then `main` fast-forwards to it (`RELEASING.md` steps 1 to 4). The draft had merge before test with no `after_merge`, which the `suite` shorthand would turn into "merge requires test".
6. A step owner is a graph node. A charter has no role field: `internal/protocol/graph.go:13-23`. A Codex worker has no agent-net tools, so it cannot report a step: `internal/protocol/workers.go:527-536`. The deploy step owner defaults from the `deploys` edge: `internal/protocol/graph_context.go:120-128` and `:167-183`.
7. `check` is now `accept`: `check` is the idle check (`internal/gateway/check.go:14-24`) and an Inbox kind (`internal/gateway/store_inbox.go:28`).
8. A revision is the head at a complete hand-back, not every commit. "Complete" per harness: `internal/protocol/operator.go:406-412`, `internal/protocol/rework.go:17-36`, `internal/daemon/worker_codex.go:944-955`. The three fix paths: `internal/daemon/workers.go:1061-1078`, `internal/daemon/worker_continue.go:11-15`, `internal/protocol/operator.go:322-328`.
9. The head at hand-back: no daemon commit for most harnesses (`internal/daemon/worker_notice.go:576-607`), the diff stat includes uncommitted changes (`:209-235`), unfinished work is saved only without a hand-back (`internal/daemon/workers.go:919-925`), `continue` commits it later (`internal/daemon/worker_continue.go:66-70`).
10. Routing: messages go to one session (`internal/protocol/protocol.go:400-412`) and expire after 24 hours (`:50`); a caller that ends takes its workers with it (`internal/daemon/workers.go:1323-1336`); only the starter may resume or continue a worker (`internal/daemon/workers.go:1075`, `internal/daemon/worker_continue.go:31-33`) or count as fix rework (`internal/daemon/worker_notice.go:682-686`).
11. The merge check and cleanup: cleanup frees a branch only when its head is on the default branch (`internal/daemon/cleanup.go:105-106` and `:208-237`), so the merge must keep the change's head.
12. The settings layer refuses an unordered rule at more than one level: `internal/settings/settings.go:137-157`. A repo value tightens only a network value that is set: `:634-645` and `:667-683`.
13. Decision 2: the levels are trivial, simple, normal, hard, very_complex (`internal/protocol/calibration.go:34`); a failed or low-confidence judge counts as normal (`internal/protocol/worker_assessment.go:68-76`).
14. Build step 2 names the step 6 fixture: `internal/protocol/delivery_fixture_test.go`, `internal/protocol/testdata/delivery-baseline.json`.

**Not checked:** the real setup (the actual values set for agent-net and voicemechs, the orchestrator's own scripts). The agent-net values above come from the node context its sessions receive, which has no rules line.
