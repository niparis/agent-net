# 0163 - A decision made in two places is a defect: find, fix, lock

Date: 2026-11-13
Status: Approach approved by the owner on 2026-11-13 ("I think that's a great
approach btw i would like to codify it"). This file codifies it. Building the
pieces in "Build" needs a design per piece and the owner's go.

## Problem

On 2026-11-13 two defects had the same shape:

- The difficulty judge launched on a provider that the worker picker had
  already skipped as exhausted. Eleven routes chose models, each with its own
  checks (decision [0162](0162-one-model-choice-path.md)).
- The stored reporting tree went stale after a lead change, because 27 places
  still worked out leads from the old tables (org task T4).

In both cases two parts of the system made the same decision with different
rules, and disagreed.

Why it happens here:

- **No agent owns a decision across its callers.** About 225 commits a day,
  each by a worker that sees one brief. No session holds a whole domain in mind.
  This is the root cause.
- **Rules arrive as patches to one route.** The worker floor was added to the
  worker start because the complaint was about workers. The judge, CTO and
  merger routes never got it.
- **Routes split when a new caller arrives.** The judge needed a model, so it
  borrowed half of the worker start and wrote the rest. Each step was the
  smallest diff.
- **Briefs and reviews reward the local fix.** A review checks the diff against
  the brief, so a copy elsewhere is invisible. The lead's own brief did this on
  2026-11-13.
- **Adding a route costs nothing; removing one earns nothing.**

Rules in docs, reviewer checklists, clone linters and periodic audits do not
hold this off: docs go stale at this rate, a diff-scoped reviewer cannot see a
copy elsewhere, the routes are not textual clones, and an audit finds the
problem after the copies exist.

The owner asked for a process that both prevents and fixes these, for any
decision and any project, not one fix for model choice.

## Decision

A decision made in more than one place is a defect class: a **split decision**.
It has one record, one way to prevent it, three ways to find it, one way to fix
it, and two numbers that show whether the process works.

A **decision** is a question the system answers for many callers: which model a
session runs on, who leads a node, whether a tool call may run, which session
gets a message, whether a provider has quota left.

### 1. The decision register

Each project keeps one register, as data in its repo. For each decision:

- its name and the question it answers;
- its **owner**: the one package or function allowed to make it;
- its **owned facts**: the tables, columns, settings and functions that only
  the owner may use to make it;
- its status: `unified`, `split` (with the routes found) or `unknown`;
- the decision file that designed it.

Everything below reads or writes the register. Nothing depends on an agent
remembering it.

### 2. Prevent

- **One CI check, driven by the register.** It fails when code outside a
  decision's owner uses one of that decision's owned facts. It is one generic
  check for every decision, not a test per domain, so adding a decision to the
  register is enough to protect it. A violation fails the build, so it does not
  decay.
- **The shortcut does not compile where the language allows it.** A unified
  decision returns a value that only its owner can make (for model choice, the
  `Granted` value), and the action it guards requires that value.
- **Briefs and reviews carry the register.** When a brief or a change touches a
  decision's owned facts, agent-net adds that decision's owner and entry point
  to the worker brief and to the review task, so a worker learns the path
  before the CI check has to stop it.

### 3. Find

Three signals put a candidate in one queue that the owner can see:

- **Incidents.** Every fixed defect gets one triage question: "did two parts of
  the system disagree?" A yes names the decision and opens a candidate. This is
  the strongest signal, because it comes from real failures.
- **Spread.** A weekly count of how many places read each table, setting and
  state flag to make a choice. A fact that gains readers and has no register
  entry becomes a candidate.
- **Review.** A change that adds a reader of a fact that already has several
  readers is flagged as a candidate, not refused.

### 4. Fix

Every candidate gets the same five steps:

1. **Inventory.** Every route that makes the decision, with a matrix of which
   checks each one applies.
2. **Design.** Separate policy (what we would like) from mechanism (what can
   run now). Name the one owner. Decide which checks are locked for every
   caller and which each kind of caller chooses, in one table.
3. **Owner approval** of the design.
4. **Build** in parts, by one worker session. The first part changes no
   behaviour, and the existing tests pin that.
5. **Lock.** Add the decision to the register as `unified` with its owned
   facts, so the CI check protects it, and make the shortcut fail to compile
   where the language allows it.

Decision 0162 (model choice) is the first run of these steps. Org leads (T4) is
the second.

### 5. Measure

Two numbers per week, per project:

- split and unknown decisions in the register, which should fall;
- defects triaged as "two parts disagreed", which should fall to zero.

If the first falls and the second does not, the register is missing
decisions.

### Generic

The register format, the CI check, the weekly count, the triage question and
the five fix steps are agent-net features that work on any project agent-net
builds. Nothing in them names agent-net's own decisions.

## Build

Each piece below needs its own design and the owner's go before a worker
starts.

1. The register format and seed entries for three decisions: model choice
   (after 0162 ships), leads (after T4), permissions.
2. The generic CI check driven by the register.
3. The triage question on fixed defects, and the candidate queue.
4. The weekly spread count.
5. The register in briefs and review tasks.
6. The five fix steps as a playbook agent-net runs for a candidate.
