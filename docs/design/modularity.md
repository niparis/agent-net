# Modular agent-net: sessions and work items

**Date:** 2026-11-03
**Status:** Draft for the owner, nothing built
**Naming:** "playbook" already means model notes in the code, and "owner" on a worker means the calling session. This note calls the layer of opinions the **operating model**.
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

## Lessons from running the owner's way (2026-11-03)

The orchestrator of this repo played tech lead for a full day: 6 to 8 workers in parallel, about 10 releases. What that taught, for the owner's preset:

- **One orchestrator per repo is right, but it is a bottleneck.** Merging, conflict resolution and CI rounds held the work up, not the thinking. Three releases failed CI because one worker's tests asserted what another worker had just changed. The merge gate module and a "test every touched package after a multi-worker merge" rule in the operating model address this.
- **"The tech lead never writes code" is too strict.** A handful of one-line fixes (a retry cap, a judge timeout, a column width) were faster done directly than briefed to a worker, which takes about 10 minutes and a full brief. Operating model rule: "tech lead never writes features; may fix trivial things (a few lines, tested)".
- **Compaction alone loses detail.** Memory files (feedback, project facts, decisions) carried what mattered across compactions; the summary alone would not have. Operating model advice: long-running sessions keep memory files, and compaction is a supplement to them.
- **Asking other agents works.** The Mac session found the agy quota cause, and a Mac worker found the CTO dialog error in 2 minutes, both on machines the orchestrator cannot read. This is foundation and should stay so.
- **Where opinion is baked in today:** the guidance text (for example "start workers only when the owner asks", "announce your task"), the protected provider and "saved for your orchestrator" (which assume an Opus orchestrator), the picker's level rules and auto-continue, and one CTO per network. All of these move to the operating model.

## Modules that come from Conductor

- **Triggers:** tracker sources (Plane first) and alert sources (webhooks). They create work items and never start runs themselves.
- **Merge gate:** a run's end effect. It merges only when both sides agree:
  - **Policy:** CI passed, a review ran, no review flag on the item.
  - **Evidence:** the agent's own report says merge.

  Missing evidence is a refusal. The owner's way benefits too: today the tech lead merges by hand.
- **Ideas:** the feature-level backlog for the human, one chat per idea, and "Do now" starts a product-owner run.

## Presets

- **Owner:** sessions at the centre; tech leads; work items only at feature level; the human never sees tasks; merges by the tech lead or the merge gate.
- **Conductor:** work items at the centre; no tech leads; triggers from Plane and alerts; workflows route items to runs; the merge gate closes them; the human sees needs-you items and the runs page.
- **Minimal:** messages and sessions only.

A preset is a operating model plus a set of switched-on modules. Setup asks for one. The CTO can propose switching.

## How to get there

Each step ships alone and keeps today's behaviour as the default.

1. **Inventory, done in draft:** see the companion file. The owner marks anything misclassified.
2. **Operating model as data:**
   - start with the guidance text and the hard-coded rules listed in the inventory;
   - the code reads values, not constants;
   - the CTO gets read access and owner-gated change proposals;
   - nothing changes in behaviour.
3. **Module switches:** each module declares its tools, tabs, hooks and guidance lines; switching it off removes them all.
4. **Work items and runs owned by work items:** the foundation change. The Ideas screen is the first surface on it.
5. **Merge gate.**
6. **Triggers:** Plane first.
7. **Presets in setup.**

## Open questions for the owner

- **Where state lives** when an external tracker exists: agent-net or the tracker? Conductor says the tracker owns item state and its own database owns run state. That works well and could be the rule.
- **One operating model per network with per-project overrides,** or fully per project?
- **Whether the CTO may change the operating model within limits** without asking, in the same way as the 30-minute graph window, or always with owner approval.
