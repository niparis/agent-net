# Modular agent-net: sessions and work items

**Date:** 2026-11-03
**Status:** Draft for the owner, nothing built
**Naming:** "playbook" already means model notes in the code, and "owner" on a worker means the calling session. This note calls the layer of opinions the **operating model**, and one complete way of working (modules plus operating model) a **recipe**.
**Companion:** [modularity-inventory.md](modularity-inventory.md), the list of every feature and every built-in opinion, with file and line.

## The goal

agent-net should support different ways of working without code changes:

- **The owner's way:** long-running sessions, one tech lead per repo, a CTO, and the human at feature level (Ideas).
- **A friend's way (Conductor):** a tracker-driven daemon. A work item reaches a state, a workflow picks it up, an agent runs in a worktree, and an evidence-gated merge closes it. No long-running orchestrator.
- **Other ways** nobody has thought of yet.

The CTO can read and propose changes to the setup. The owner approves, as for any owner-only change.

## Four layers

| Layer | What it is | Turned off? | Examples |
|---|---|---|---|
| Foundation | what agent-net is | never | sessions, identity and caller checks, messages, gateway, security, herdr, harness detection and updates, **work items and runs** (new) |
| Modules | capabilities | per network, machine or project | workers and the picker, difficulty judge, quality, skills, MCP sharing, quotas, CTO graph and charters, Inbox approvals, Ideas, triggers, merge gate |
| Operating model | a way of working, as data | edited, not switched | rules for people and agents, workflows, defaults, escalation levels, north stars |
| Surfaces | where the human looks and acts | they show what is on | TUI, phone app, bots, CTO chat, CLI |

Surfaces have no logic of their own: if a module is off, no surface shows it.

## The finding: two different centres

agent-net today is built around **sessions**. Work starts because an agent or the owner decides. A worker belongs to the session that started it, and its report goes back to that session.

Conductor is built around **work items**. A ticket moves through states; a daemon leases it, routes it to a workflow, runs it, and applies the result. Nothing needs to be running in between.

To support both, agent-net needs **work items as a foundation object next to sessions**, and **runs that belong to either one**.

### Work item

A unit of work the human cares about.

- **Fields:** an id, a title, a level (idea, feature, task), a state (parked, ready, active, needs-you, done, closed), labels, a project, links (depends on, part of), and a conversation (one chat per item, as in the Ideas screen).
- **Sources:** the owner, an agent, the CTO, or a trigger: a tracker such as Plane, an alert, or a schedule.
- **Optional mirror:** an external tracker. Plane owns the state and agent-net mirrors it, or agent-net owns it. This is set per project.
- **One owner at a time.** A work item has exactly one current owner: a session, a run or the human.
- **Handoff is not a message.** Passing an item to a new owner (lead to reviewer, worker back to lead) is one database step: the old owner's hold closes, the new one's opens, and the history keeps both. A message about the item can still be sent, but it never moves ownership. A message can be lost or ignored; a handoff cannot.
- **Waiting names what it waits for.** An item in needs-you or blocked records the thing it waits for: an owner decision, another item, a CI run. When that thing resolves, agent-net wakes the owner. Nobody polls, in the same way as worker notices today.

A run ends in one of a small fixed set of exits: done, handoff, waiting, failed. A workflow step lists which exits it allows.

### Run

One agent working on one thing in one worktree. That is today's worker, with two changes:

- **The owner of a run** is a session (today), a work item (new), or both: a tech lead runs a worker for a feature.
- **Reports** go to the owner. For a work item, the report becomes the item's state change and a line in its conversation, not a message to a session.

## Operating model

Data, per network with per-project overrides. Versioned, and changed through owner-approved proposals.

- **Rules:** who may start runs; whether a tech lead may edit code; the level the human works at (feature or task); when to escalate and how to bundle escalations; protected providers; compaction advice.
- **Workflows:** route by labels or level to an agent, model, effort, prompts and completion effects. This is Conductor's workflow folder, as data.
- **Defaults:** picker thresholds, auto-continue, judge timeout, isolation.
- **North stars:** per project, what the agents there may build on their own.

The guidance text in every agent's instructions is generated from the operating model, not written in the code.

### Advice and rules are different things

An agent reads text and may not follow it. Only agent-net itself can make something always true. So every entry in the operating model is one of two kinds:

| Kind | Who makes it hold | Examples | Who may change it |
|---|---|---|---|
| **Advice** | the agent, by reading it | bundle escalations, keep memory files, "tech lead may fix trivial things", which tests a worker runs before handing back | the CTO within limits, the owner |
| **Rule** | the daemon or gateway, whatever the agent does | who may start runs, protected providers, merge only when CI and a review agree, owner-only actions | the owner only |

The test: if it must hold every time, it is a rule, and the guidance text only explains it. A rule written only as advice is a bug. This also answers the open question about the CTO: it may change advice, never rules.

## Lessons from running the owner's way (2026-11-03)

The orchestrator of this repo played tech lead for a full day: 6 to 8 workers in parallel, about 10 releases. What that taught, for the owner's preset:

- **One orchestrator per repo is right, but it is a bottleneck.** Merging, conflict resolution and CI rounds held the work up, not the thinking. Three releases failed CI because one worker's tests asserted what another worker had just changed. The merge gate module and a "test every touched package after a multi-worker merge" rule in the operating model address this.
- **"The tech lead never writes code" is too strict.** A handful of one-line fixes (a retry cap, a judge timeout, a column width) were faster done directly than briefed to a worker, which takes about 10 minutes and a full brief. Operating model rule: "tech lead never writes features; may fix trivial things (a few lines, tested)".
- **Compaction alone loses detail.** Memory files (feedback, project facts, decisions) carried what mattered across compactions; the summary alone would not have. Operating model advice: long-running sessions keep memory files, and compaction is a supplement to them.
- **Asking other agents works.** The Mac session found the agy quota cause, and a Mac worker found the CTO dialog error in 2 minutes, both on machines the orchestrator cannot read. This is foundation and should stay so.
- **Where opinion is baked in today:** the guidance text (for example "start workers only when the owner asks", "announce your task"), the protected provider and "saved for your orchestrator" (which assume an Opus orchestrator), the picker's level rules and auto-continue, and one CTO per network. All of these move to the operating model.

## Removing the integration bottleneck

Options considered for the work after a worker finishes (merging, conflicts, CI, release):

| Option | How | For | Against | Verdict |
|---|---|---|---|---|
| A. Merge gate | a finished run merges itself when CI passed, a review ran and the agent's report says merge | no manual merging; proven in Conductor | needs CI and a review step per repo; weak tests let bad merges through | **yes** |
| B. Worker hands back a ready-to-merge branch | before handing back, the worker rebases on main, resolves conflicts and runs the tests it touched | the agent that knows the change resolves its conflicts; the lead's context stays small | more worker time; two finishing at once must rebase again | **yes, with independent checks** |
| C. Release train | batch finished work, one CI run per batch (what happened on 2026-11-03) | fewer CI runs | one bad change blocks the batch; cross-worker clashes surface late | fallback only |
| D. Integrator agent | a separate cheaper agent merges, runs CI and releases | frees the lead | one more agent; must understand conflicts or it escalates a lot | when volume is high (more than about 6 workers) |
| E. Sub-leads per repo | several leads by area, **one of them the lead** that receives all communication | more thinking in parallel | needs reporting lines, which agent-net does not model yet | where a project is big enough |

"Plan smaller, independent tasks" was also listed and dropped: it is a habit, not something agent-net can provide.

**Quality control stays independent of the writer.** B moves the plumbing, not the judgement. The worker prepares the branch; it never approves it. Before anything merges:

- CI runs on the branch: the worker's own word is not evidence;
- a reviewer reads the final diff against main: the lead, a separate reviewer agent, or a bot review;
- the merge gate merges only when CI and the review agree.

The remaining risk is a conflict resolved "correctly" but wrongly. The reviewer sees the final diff, so it is caught at review.

**Reporting lines** make E work. A new graph edge, "reports to", states who the lead is. Messages for a repo go to its lead, and sub-leads take work from the lead. A project's operating model decides whether it has sub-leads. The same edge fits the work-item preset: a workflow reports to whoever owns the board.

**Recommendation:** B plus A, with D when volume calls for it and E where a project is large enough. All four are operating-model settings, so each project picks its own.

## Modules that come from Conductor

- **Triggers:** tracker sources (Plane first) and alert sources (webhooks). They create work items and never start runs themselves.
- **Merge gate:** a run's end effect. It merges only when both sides agree:
  - **Policy:** CI passed, a review ran, no review flag on the item.
  - **Evidence:** the agent's own report says merge.

  Missing evidence is a refusal. The owner's way benefits too: today the tech lead merges by hand.
- **Ideas:** the feature-level backlog for the human, one chat per idea, and "Do now" starts a product-owner run.

## Recipes

A **recipe** is the blueprint for one way of working. It says which modules are on, with which settings, and how the work moves from an idea to a release. It is one versioned file. Modules are the parts; the recipe is how they are put together.

The idea comes from OpenRig's team definitions (starter, workshop, factory), widened: OpenRig's team files list agents, while a recipe also carries the delivery strategy and the rules.

### What a recipe holds

| Section | What it decides | Example (the owner's recipe) |
|---|---|---|
| **Modules** | which modules are on, and their settings | workers and picker on; judge on, 60 s; quality weighting on; Ideas on; triggers off |
| **Roles** | which long-running sessions exist, their reporting lines, harness and model hints | one CTO per network; one tech lead per repo on an Anthropic model; no sub-leads |
| **Intake** | where work comes from, and the level the human works at | the owner and Ideas; the human works at feature level and never sees tasks |
| **Routing** | which work goes to which kind of run | the difficulty judge picks the level; the picker picks the model; non-Anthropic first |
| **Delivery** | how a change gets tested, reviewed, merged and released | see below |
| **Escalation** | what reaches the human, and how it is bundled | owner-only actions; decisions with a recommendation; bundled at the end of a turn |
| **Advice** | the guidance text agents read | "announce your task", "decide, do not defer" |

### Delivery strategy

The integration lessons of 2026-11-03 belong here, as data, so each project picks its own. Each line is marked as a rule (agent-net enforces it) or advice (agents follow it).

| Step | Choices | The owner's recipe for agent-net |
|---|---|---|
| Worker tests | none; changed packages; full suite | changed packages only, never the full suite (advice) |
| Branch prep | lead merges; worker rebases on main and resolves conflicts before hand-back | worker rebases (advice) |
| CI | none; on the branch; on the merged batch | on the branch, required (rule) |
| Review | none; the lead; a reviewer agent; a bot; never the writer | the lead reads the diff (rule: the writer never approves its own run) |
| Merge | by hand; merge gate | merge gate when CI and review agree, else the lead (rule) |
| Batching | per change; release train | per change, train as fallback |
| After a multi-run merge | nothing; test every touched package | test every touched package (rule, CI does it) |
| Release | none; on tag; automatic after CI on main | automatic: CI on main, then tag (rule) |

Two other examples:

- **A docs repo:** no CI, the lead reviews and merges, no release.
- **Conductor:** the worker rebases; CI on the branch; a bot review; the merge gate closes the item; release is left to the project.

### The recipes that ship

- **Owner:** sessions at the centre; tech leads; work items only at feature level; the human never sees tasks; merges by the tech lead or the merge gate.
- **Conductor:** work items at the centre; no tech leads; triggers from Plane and alerts; workflows route items to runs; the merge gate closes them; the human sees needs-you items and the runs page.
- **Minimal:** messages and sessions only.

### How a recipe is used

- **Scope.** One recipe per network. A project may pick another recipe or override single sections, most often Delivery.
- **Setup asks for one.** Today's behaviour is the Owner recipe, so nothing changes for an existing network.
- **Switching or editing is a proposal.** The CTO may propose; the owner approves. The proposal shows the difference in plain words: which modules turn on or off, which rules change, which guidance lines change.
- **Sharing.** A recipe can be exported as a file and imported from a commit-pinned Git path. A recipe is instructions for agents, so an imported one is treated like code from a stranger: the owner reads the difference and approves it, and it can never relax a Foundation security check.
- **Measuring.** Runs already carry ratings, rework and late defects. Grouped by recipe, these show which way of working gives better results for the same kind of work, with numbers instead of opinion. OpenRig has no such measure.

## How to get there

Each step ships alone and keeps today's behaviour as the default.

1. **Inventory, done in draft:** see the companion file. The owner marks anything misclassified.
2. **The recipe file, holding today's behaviour as the Owner recipe:**
   - start with the guidance text and the hard-coded rules listed in the inventory, each marked advice or rule;
   - the code reads values from the recipe, not constants;
   - the CTO gets read access and owner-gated change proposals;
   - nothing changes in behaviour.
3. **Module switches:** each module declares its tools, tabs, hooks and guidance lines; the recipe's Modules section switches them, and switching one off removes them all.
4. **Work items and runs owned by work items,** with handoff and waiting: the foundation change. The Ideas screen is the first surface on it.
5. **Delivery section and merge gate:** the merge gate reads its conditions from the recipe.
6. **Triggers:** Plane first.
7. **Recipes in setup:** Owner, Conductor and Minimal; per-project overrides; export and import; results grouped by recipe.

## Open questions for the owner

- **Where state lives** when an external tracker exists: agent-net or the tracker? Conductor says the tracker owns item state and its own database owns run state. That works well and could be the rule.
- **One recipe per network with per-project overrides** (this note's proposal), or fully per project?
- **Whether the CTO may change advice within limits** without asking, in the same way as the 30-minute graph window. Rules always need the owner.
