# Modular agent-net

**Date:** 2026-11-03
**Status:** Draft for the owner, nothing built. Second version: the first version drifted into new features; this one keeps to the objective.
**Companion:** [modularity-inventory.md](modularity-inventory.md), every feature and every built-in opinion, with file and line.

## The objective

In the owner's words: "how can the harness be adjusted to support my way and his way and other ways, and how can I control that from my CTO".

- **agent-net provides mechanisms.** Sessions, messages, workers, the picker, the judge, skills, the graph, proposals and approvals.
- **A way of working is an opinion on top.** Who may start workers, whether a lead writes code, which provider is protected, what agents are told, how work is tested and merged.
- **Opinions are data, not code.** A way of working is written as a **recipe**. Changing it needs no release.
- **The CTO controls it.** The CTO reads the recipe in force and proposes changes; the owner approves.
- **Surfaces reflect it.** The TUI, phone and bots show what the recipe uses. They are not modules.

**The test of success:** the owner's way and a friend's way (Conductor) can both be written as recipes for the same binary. Where one cannot be written, that marks a missing mechanism. Only then is something built.

## Principles

1. **Mechanism, not opinion, in the code.** A value that someone may want different moves to the recipe.
2. **Advice and rules are different.** Advice is text an agent reads and may not follow ("keep memory files"). A rule is a check agent-net enforces ("merge only when CI and a review agree"). If it must hold every time, it is a rule; a rule written only as advice is a bug.
3. **Rules only tighten going down the levels; advice may change freely.**
4. **A setting lives at the level where it really varies,** and says how far down it may be overridden.
5. **Every effective value says where it came from** ("merge gate on: team wbg"), in the TUI and for the CTO.
6. **One name per concept.** No special kinds where a setting does the job.

## The concepts

### Network

The whole harness, one owner, one CTO. Foundation lives here: gateway, identity, caller checks, messages, security notes, herdr, harness detection and updates.

### Team

A group of repos and agents with a shared purpose, for example all the wbg repos.

- A repo or an agent is a **member of** at most one team (a new edge kind). One parent keeps inheritance clear.
- A team has a purpose and settings that its members inherit.
- A team is not an architect. An architect is an agent that owns a team (below).

### Repo

The software being built: code in one place, its git remote, the machines it is checked out on. The repo holds the properties of the software, so that several agents in one repo cannot drift apart:

- purpose and north star;
- accepts and refuses (which feature requests fit);
- escalation: which changes must go to the owner;
- maturity and deploy rules;
- delivery: tests, CI, review, merge, release;
- skills placement.

### Agent

Who acts. One name for every kind: tech lead, worker, architect, product manager, expert, reviewer, CTO.

- **Owns:** a repo, a part of a repo (a set of paths), a team, the network, or nothing. An agent has no purpose of its own: it takes the purpose of what it owns.
- **Inside one repo, parts do not overlap.** The repo's lead owns whatever no one else owns, so every file has exactly one owner.
- **Role:** free text, with shipped defaults (lead, worker, reviewer, architect, product manager, expert). A role feeds wording, not logic. A fixed list would go against modularity.
- **Autonomy:** may write code, may start workers. "Non-coding" is a setting, not a kind.
- **Escalation:** the repo's escalation binds every agent there; an agent may add stricter escalation for itself, never looser.
- **Relationships:** see below.

The owner's style stays one agent per repo. The split matters when it is not 1-1, and it removes the architect as a special node kind: an architect is an agent that owns a team.

Examples:

| Agent | Owns | Role | May write code |
|---|---|---|---|
| Tech lead | a repo | lead | trivial fixes only (advice) |
| Worker | one task in one repo, for one run | worker | yes |
| Architect | a team | architect | no |
| Product manager | a team's feature backlog | product manager | no |
| Expert (for example a library agent) | nothing, or a knowledge source | expert | no |
| Security reviewer | nothing; may review a team's repos | reviewer | no |
| CTO | the network | CTO | no |

### Run

One worker start: one agent working on one task in one worktree. Today's worker. A task has one active run at a time; a retry or a continuation is another run of the same task.

### Machine

Not a level. A machine says what it **can** do: installed harnesses, toolchains, whether it accepts workers, harness updates. It never takes part in inheritance.

## Relationships, as a module

The graph edges today (`depends`, `governs` with modes advisor, arbiter or owner, `deploys`) answer one question: how should a receiving agent treat a message from a sender, as an **order**, a **suggestion** or **information**? Today the answer is worked out at delivery and written into the trust note.

As a module it is a rule from (sender, receiver, the edges between them) to order, suggestion or information.

- The owner's rule is today's: owner, then CTO, then an owner-mode governor, then peers.
- Reporting lines ("reports to") are one more edge type in this module, not a separate system.
- A way of working without messages between agents (Conductor) may need almost none of it.

## Levels and inheritance

```
network
  team
    repo chain:   repo  -> run
    agent chain:  agent -> session
```

A worker started by agent Y in repo X takes delivery settings from X and behaviour settings from Y.

### Where a setting lives

1. Touches a shared resource: **network** (subscriptions, quotas, protected providers).
2. Measurement or calibration: **network** (model scores, judge, cut points, quality data). Splitting it would make the data meaningless.
3. Security: **set at network; lower levels may only tighten** (owner-only actions, approval path, caller checks, sandbox minimum).
4. Coordination across repos: **team** (who leads, cross-repo features, intake).
5. How the software is built and shipped: **repo**, with a network default.
6. How one agent behaves: **agent**.

### Classification

| Setting or module | Home | Overridable at | Why |
|---|---|---|---|
| Gateway, identity, messages, caller checks, herdr, harness updates | network (foundation) | nowhere | what agent-net is |
| Quotas, protected providers | network | nowhere | shared money |
| Worker profiles | network | nowhere (a team allowlist is out of scope) | shared |
| Model scores, judge, cut points, quality data, picker weights | network | nowhere | calibration |
| Owner-only actions, approval path | network | lower levels tighten only | security |
| CTO | network, one per owner | - | the owner's single control point |
| Shared MCP servers | registry at network | which ones attach: team, repo, agent | registry shared, use local |
| Relationships rule | network | team | how messages are read |
| Escalation and bundling of what reaches the owner | network default | team, repo, agent (tighten) | how much reaches the owner |
| Intake (owner, Ideas, tracker) | team | repo | a tracker project usually maps to a team |
| Ideas (feature backlog) | optional module, team | - | today it lives inside an agent session |
| Purpose, accepts, refuses | repo; team for a team's own purpose | - | properties of the software |
| Maturity, deploy rules | repo | - | how careful to be with this code |
| Delivery: tests, CI, review, merge gate, release | network default | team, repo | varies by repo |
| Release train (repos that release together) | team | - | only meaningful across repos |
| Skills on or off | network default | team, repo | already per project |
| Worker isolation, sandbox | network minimum | repo, run (tighten) | security minimum |
| Owns, role, may write code, may start workers | agent | - | who acts |
| Advice text (guidance) | network | team, repo, agent | advice changes freely |
| Model for one task | run | - | only inside what is allowed above |

### Resolution rules

1. Each setting declares its home level and how far down it may be overridden.
2. The most specific allowed level wins.
3. Rules only tighten going down; advice may change freely.
4. A repo or an agent is a member of at most one team.
5. Every effective value carries its source.
6. A CTO proposal names the level it changes: "merge gate for team wbg" and "merge gate for wbg-housing" are different proposals.

## Recipes

A recipe is one versioned file: which modules are on, and the settings at each level. The owner's way is the first recipe and reproduces today's behaviour exactly.

Sections:

| Section | Decides | The owner's recipe |
|---|---|---|
| Modules | which are on, with their settings | workers and picker, judge, quality weighting, skills, CTO graph, Inbox |
| Teams | teams, members, purposes | none yet |
| Repos | purpose, accepts, refuses, escalation, maturity, delivery | today's charters and defaults |
| Agents | owns, role, autonomy | one lead per repo, the CTO |
| Relationships | the rule and the edges | today's edges and modes |
| Intake | where work comes from | the owner and agents |
| Advice | the guidance text | today's guidance lines, as data |

**Delivery example**, the owner's recipe for agent-net, each line marked:

| Step | Setting | Kind |
|---|---|---|
| Worker tests | changed packages only | advice |
| Branch prep | the worker rebases on main before handing back | advice |
| CI | on the branch, required | rule |
| Review | the lead reads the diff; the writer never approves its own run | rule |
| Merge | merge gate when CI and review agree, otherwise the lead | rule |
| After a multi-worker merge | test every touched package | rule |
| Release | automatic after CI on main | rule |

**How a recipe is changed.** The CTO reads the recipe and the effective values with their sources, and proposes a change at a named level. The proposal shows the difference in plain words. The owner approves through the existing proposal path. The CTO may change advice within limits; rules always need the owner.

## Two lists to complete

1. **The opinion list.** Every opinion now in the code, from the inventory, marked advice or rule, with the recipe setting that replaces it. Writing the owner's recipe from it must reproduce today exactly. The largest groups are the guidance text, the CTO brief, the maturity defaults, the picker numbers, worker limits and auto-continue, and the protected provider.
2. **The gap list.** The friend's way written as a recipe, with every line that today's mechanisms cannot express. Known so far:
   - work that starts from a tracker item, not from a session (intake as a module);
   - a run that reports to an item in a tracker, not to a session;
   - merging decided by policy and evidence, not by a lead (the merge gate).

   The rest needs the friend's way in more detail.

## Build order

Each step ships alone and keeps today's behaviour.

1. **The opinion list** (inventory done in draft).
2. **Settings as data, owner recipe only.** The code reads values instead of constants; each value marked advice or rule; effective values show their source; the CTO can read them.
3. **Repo and agent split, with ownership and free-text roles.** The architect becomes an agent that owns a team.
4. **Teams** with the "member of" edge and inheritance.
5. **Relationships as a module,** today's rule as its first form.
6. **CTO proposals at a named level.**
7. **Gap list for the friend's way,** then only the mechanisms it needs (intake, merge gate, run reporting).

## Dropped from the first version, and why

- **Work items as a foundation object.** At task level a work item and a run are nearly 1-1. Only an item that exists before any run, or above runs (a feature), is new. For a tracker-driven way the tracker can own the item; agent-net keeps a reference on the run.
- **Module on/off switches as a goal.** Turning things off is a side effect of recipes, not the objective.
- **Handoff and waiting as foundation objects.** Taken from OpenRig without a problem that needed them. Revisit if the gap list asks for them.
- **"Measure by recipe".** With one owner and one recipe there is nothing to compare.
- **Recipe import and sharing.** No one needs it yet; it adds a security surface.
- **Merge-bottleneck options.** The merge gate and a rebased hand-back are good features in their own right. They are delivery settings once built, but building them is not modularity work.

## Open questions

1. **Decided 2026-11-06:** One owner per setup, chosen in the recipe per network or team. Internal tracking of agent-net's own changes and steps is the default. With a third-party tracker, the tracker owns the item and its state. agent-net keeps only the item link on the run and posts results one way (merged, released, failed); it never reads tracker state to decide anything. There is no two-way sync. Build this only if the step 7 gap list needs it.
2. Should a part of a repo ever have its own maturity (a stable core and an experimental module)? Proposed: not now.
3. **Decided 2026-11-06:** A cross-cutting agent can own nothing and works through relationships. Advertise its charter and expertise for discovery and ad-hoc help; assign it to a delivery step for a whole team when the work is required. See [teams and relationships](teams-relationships.md).
