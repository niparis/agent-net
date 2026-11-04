# Delivery pipeline: steps done by nodes

**Date:** 2026-11-04
**Status:** First draft for the owner. Nothing built.
**Context:** [modularity.md](modularity.md) (objective, recipes, the "merge gate" gap), [settings-layer.md](settings-layer.md) (settings, levels, rules and advice).

## The problem

The owner's words: changing the way a repo merges should mean "another node comes online and takes over the work of reviewing, testing, merging". Today it cannot.

Settings steps 3 to 7 made the delivery rules data: `delivery.merge_authority`, `delivery.tests_before_merge`, `delivery.deploy_rule`. But a rule only changes what agents are **told**. The work is still done by hand by the caller session. For agent-net itself, the orchestrator session:

1. reads each worker's report and diff;
2. starts the independent review worker and reads its findings;
3. sends fixes back to the author;
4. merges in its own merge worktree;
5. builds, vets, pushes a release branch;
6. runs `release.sh`: waits for CI, pushes main, tags, waits for the release workflow, installs;
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
| Releases | git tags and the repo's own release command | Each repo already has one (agent-net: `release.sh`, a tag, a workflow). |
| CI | GitHub Actions when the repo is on GitHub; otherwise a test step run by a node | agent-net must not build a CI runner. |
| Hard enforcement | GitHub's branch protection and required checks, when the repo is on GitHub | The only gate an agent with write access cannot step around. |
| Which node owns which step, per repo | agent-net | Git has no idea of nodes, roles or machines. |
| Routing the change to that node, hand-over when the owner changes | agent-net | Cross-machine, cross-session. |
| The state of each change | agent-net (gateway database) | One view across machines; survives restarts. |
| Evidence (who passed which step on which commit) | agent-net, keyed by repo and commit hash; optionally posted to GitHub as checks on the commit | Must not change the commit (so not trailers or amended messages). |
| Approvals, notices to the owner, model choice for step workers | agent-net | Already built (proposals, Inbox, picker). |
| Detecting work that skipped the pipeline | agent-net watches the default branch | The fallback when the repo is not on GitHub. |

What agent-net does **not** build: a CI runner, a review UI, a merge queue for repos where GitHub already has one.

**The limit, stated plainly.** On a repo that lives only on our machines (no GitHub copy), nothing can stop an agent with write access from pushing to main. agent-net makes the pipeline the easy path and makes a skip visible: a commit on the default branch with no recorded evidence becomes an Inbox item for the owner at once. Where the repo is on GitHub, agent-net posts its evidence as checks on the commit, and GitHub's branch protection refuses a merge without them.

## Concepts

### Change

One unit of delivery: a repo, a branch, a head commit, where it came from (worker run, agent session, or a person), and its caller. Created by agent-net when a worker hands back with `complete: yes`, or by an agent with a tool call (`agent_change_submit`) for work done outside a worker.

A new commit on the branch makes a new **revision** of the change. Evidence belongs to a revision, so a fix after a review invalidates that review unless the step says otherwise.

### Step

A kind of work in the pipeline, from a fixed list that agent-net knows how to route:

| Step | Done by | Evidence it leaves |
|---|---|---|
| `check` | the caller (today's "read the report and diff") | accepted or sent back, with a note |
| `review` | a node, which may start review workers | pass, or findings by severity |
| `test` | a node, or GitHub's CI | pass or fail, the command or CI run link, the commit, and evidence fields (below) |
| `approve` | an approver (below): the owner, a delegate, or a node | approved or refused, by whom, for the exact commit |
| `merge` | a node, in its own worktree | the merged commit on the target branch |
| `release` | a node, running the repo's release command | the tag, the release run link |
| `deploy` | a node, running the repo's deploy command | where it was deployed, the commit, the run link |
| `verify` | a node | what was checked live, pass or fail |

A recipe may leave steps out (a solo repo: `test`, `merge`). The list grows only when a recipe needs a step the list cannot express.

`approve` is separate from `merge` on purpose: who **decides** a merge and who **does** it are different questions. The same holds for deploy: a `deploy` step may require an `approve` step of its own (`approve.for: deploy`). Release builds and publishes a version; deploy puts a version into service on a machine. A repo may have either, both or neither.

### Evidence fields

A step reports pass or fail, plus named fields the pipeline may require. A field is a fact agent-net can check or the step owner states, recorded for the exact commit:

| Field | Set by | Checked how |
|---|---|---|
| `tests_added` | the test step | agent-net compares the change's diff for new or changed test files (per language: `_test.go`, `test_*.py`, `*.test.ts`); the owner can override with a reason |
| `suite` | the test step | which suite ran (changed packages, full suite, CI) |
| `findings_open` | the review step | count of blocker findings not yet fixed; merge requires 0 |
| `independent` | agent-net | the step's session differs from the author, or its model family differs |

A rule then reads `merge requires test(suite=full, tests_added=yes), review(findings_open=0)`.

### Step owner

Per repo, each step has an owner: a **role**, which the repo's settings map to a node. Default for every step: the caller, which is today's behaviour. An owner may be:

- a node (an agent session with that role in its charter);
- `github` (only for `test`, and for `merge` with GitHub's merge queue: agent-net reads the result for the commit; no session works);
- `owner` (the person; the step becomes an Inbox approval).

For `approve`, the approver may also be:

- `none`: the step is left out (today's merge authority "full");
- `delegate`: whoever governs this node in the graph, through a "governs" edge in an allowed mode (`delivery.delegated_approver_modes`, today arbiter or owner), or the owner. This is today's "delegated", resolved from the graph at the moment the step starts, so a new architect takes over without a pipeline change.

### Rules on steps

Rules are settings of kind `rule` (owner approval to change, only tighten going down the levels):

- **requires**: a step does not start until the listed steps have passed **on the same commit**. Example: `merge requires review, test`.
- **independent**: the evidence must come from a different session than the author, or from a different model family (`review.independent: family`).
- **on_fail**: where a failed step sends the change (back to the author worker as a fix, or to the caller).
- **after_merge**: steps that run on the merged commit (`test` again after a multi-worker merge, `release`, `verify`).

### Pipeline

The ordered steps with their rules, per repo. It lives in the recipe (`delivery.pipeline`) and resolves through the settings layer like any other setting, with its source shown.

### One source of truth: the pipeline replaces the step 6 delivery settings

Settings step 6 made three delivery rules into settings: `delivery.merge_authority`, `delivery.tests_before_merge` and `delivery.deploy_rule`. If they stayed next to the pipeline, two places would say how a repo merges, and they would drift. So the pipeline is the one source, and those three become **shorthand**:

- Setting a shorthand value writes the matching part of the pipeline, in the same change, with history.
- Reading a shorthand shows the value the pipeline implies, or "custom" when the pipeline says something the shorthand cannot.
- The rule text agents see (charters, the note on each delivered message, worker briefs, the checklist) is rendered from the pipeline, not from the shorthand.

The mapping:

| Shorthand | Value | Pipeline |
|---|---|---|
| `merge_authority` | full | no `approve` step before merge |
| | delegated | `approve` before merge, approver `delegate` |
| | ask | `approve` before merge, approver `owner` |
| `tests_before_merge` | optional | merge does not require `test` |
| | suite | merge requires `test(suite)` |
| | suite-new-tests | merge requires `test(suite, tests_added=yes)` |
| | suite-new-tests-review | merge requires `test(suite, tests_added=yes)` and `review(findings_open=0)` |
| `deploy_rule` | brief | `deploy` with no approval (deploy as the brief says) |
| | ask | `deploy` requires `approve(for: deploy)`, approver `delegate` |
| | owner | `deploy` requires `approve(for: deploy)`, approver `owner` |

The maturity defaults (prototype, beta, production, critical) keep working: each maturity level names a pipeline, which is what the shorthand values resolve to today.

`charter.judge_consumers` and `charter.autonomy_mode` are not delivery. They decide how requests come in and whether an idle agent is woken, and stay settings of their own.

The worker settings (branch prefix, commit trailers, cleanup, saving unfinished work, the brief texts) describe how the worker step runs, and do not change.

## How a change moves

1. A worker hands back with `complete: yes`. The daemon records the head commit and creates the change (revision 1).
2. agent-net finds the first step whose requirements hold and sends a **step task** to its owner, in the same form as a worker notice: the change, the commit, the diff stat, the evidence so far, what to report.
3. The owner does the work. It may start workers (a reviewer node starts an independent review worker on another model family; the picker still chooses models).
4. The owner reports with `agent_step_report`: pass, fail with findings, or "needs the owner". agent-net checks the commit in the report is still the head of the change.
5. Pass: the next step starts. Fail: the change goes where `on_fail` says. A fix commit makes a new revision; steps with `requires` on it run again.
6. `merge` checks, at the moment it runs, that every required step passed on the exact commit it merges, with the required evidence fields. The merge tool refuses otherwise. This check is agent-net's, so it holds even when the merge node's model is wrong. `deploy` checks its own requirements the same way.
7. After merge, `after_merge` steps run on the merged commit.
8. The caller gets one notice at the end (merged, released, or stopped and why), not one per step, unless it owns a step.

## Hand-over

The owner changes a step owner (a rule change: from the TUI, or a CTO proposal approved by the owner). Then:

- Changes not yet at that step go to the new owner.
- A step in progress stays with its current owner until it reports, unless the owner moves it explicitly ("hand over now"). Then the old owner gets a stop notice and the new one gets the step task with the evidence so far.
- The old owner stops receiving step tasks at once. Its charter text is updated by the same change.
- If the new owner is offline, step tasks wait in the queue and the owner sees "waiting for merger node (offline)" in the TUI.

## What the owner sees

- A **Delivery** view per repo: each open change, its revision, the step it is in, who owns that step, the evidence so far, and how long it has waited.
- Each step owner's queue.
- Inbox items for steps owned by the owner, for skipped-pipeline commits, and for an owner who stays offline.
- The CTO reads all of it and proposes pipeline and owner changes.

## Recipes

**The owner's way today** (this must reproduce today exactly):

| Step | Owner | Rules |
|---|---|---|
| check | caller | |
| review | caller | independent: family (steps 5 to 7 tonight); optional for small changes |
| approve | none | merge authority "full" for agent-net |
| merge | caller | requires check, review when present |
| test | github (CI on the release branch) | |
| release | caller | requires test on the exact commit; runs `release.sh` |
| verify | caller | after release |

voicemechs (deploy rule "brief", the one real node setting the step 6 cutover found) adds `deploy` with no approval.

**The owner's way with a merger node:** the same, with review, merge, test, release and verify owned by `merger`. The orchestrator keeps only `check`.

**A friend's way (pull requests):**

| Step | Owner | Rules |
|---|---|---|
| review | owner (a person, on the pull request) | |
| test | github | required check |
| merge | github (merge queue) or a merger node | requires review, test |
| release | github (workflow on tag) | |

**Solo, minimal:** test by the worker's own node, merge by the caller. No review.

## Build order

Each step ships alone. Step 1 keeps today's behaviour exactly.

1. **Changes and evidence, recorded only.** The gateway records a change at every hand-back, and evidence when today's flow does its steps (the caller reports `check`, reviews and merges through new tools). Nothing is routed or refused. The Delivery view shows it. This proves the model on real traffic.
2. **Step owners and routing.** `delivery.pipeline` and step owners as settings, default "caller". Step tasks go to the owner. With all owners set to "caller", behaviour is today's. In the same step, the three shorthand settings start writing and reading the pipeline, and agents' rule text is rendered from it; the step 6 fixtures must stay byte-identical.
3. **The merge and deploy checks.** The merge and deploy tools refuse without the required evidence and approvals on the exact commit, including the evidence fields (`tests_added`, `findings_open`). The default-branch watcher flags commits with no evidence.
4. **GitHub integration.** `test` owned by `github` reads CI results; evidence posted as checks on the commit so branch protection can enforce it.
5. **Hand-over** with in-flight changes, and the "waiting for an offline owner" state.
6. **Recipes:** load, choose and export a named pipeline per repo. Write the friend's way and list what it still cannot express.

The first real use: a merger node for agent-net, after step 5.

## Open questions for the owner

1. **Where the merge runs.** The merger node needs a checkout with push rights. On research only, or on whichever machine the merger runs? (Recommendation: the merger's own machine, its own worktree, like today's merge worktree.)
2. **Small changes.** Should a review be required for every change, or may the recipe set a size or difficulty below which review is skipped? (Recommendation: a rule using the judge's difficulty label, default "review from hard up".)
3. **Batching.** Today several workers' branches are merged and released together. Is a release a step on one change, or on a batch? (Recommendation: merge per change, release per batch, a batch being "everything merged since the last release".)
4. **Who may submit a change** outside a worker (an agent's own commits)? (Recommendation: any agent in the repo, through `agent_change_submit`.)
5. **GitHub only?** Support only GitHub at first, not GitLab or Gitea? (Recommendation: yes; the design does not depend on GitHub, so others can be added.)
