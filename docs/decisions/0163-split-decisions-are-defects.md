# 0163 - Find and fix decisions made in two places

Date: 2026-11-13
Status: Proposed (revision 2), waiting for the owner's approval. Revision 1 (a
decision register and a CI check) was not approved: the owner wants no
deterministic step and no list of decisions he has to keep approved.

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

The owner's limits for the answer:

- no deterministic step: a script cannot judge whether two pieces of code make
  the same decision;
- no LLM on every change: too expensive;
- no list of decisions to keep approved: there are too many, and he cannot keep
  track of them.

## Decision

Two parts, both done by the agents that already run, with judgment, not rules.

### 1. Instructions in agent-net's guidance

Every session gets these through agent-net's guidance, so they reach every
harness and every project.

- **Leads, before a brief.** Before you brief a change that decides something
  (which model, who leads, whether a tool may run, where a message goes),
  search for code that already decides it. Brief the worker to use or extend
  that code. Never brief a second copy beside it.
- **Workers, while coding.** Before you add a check, a lookup or a choice,
  search for a function that already does it, and reuse or extend it. When you
  find two places that already do the same thing, do not fix them (it is out of
  scope): name both in your hand-back under "Duplicates seen".
- **Reviewers, in the review step.** For each piece of decision logic in the
  diff, search the code base for another place that makes the same decision.
  Report it as a finding with both locations.
- **Defect fixes.** Say whether the defect came from two parts of the system
  that disagree, and name them.

These reuse work that already happens: the worker already reads the code
around its change, the reviewer already reads the diff, and the fixer already
investigates the defect. The added cost is a search and a sentence.

### 2. One investigation a week per project

Once a week agent-net gives each project lead one task:

1. **Gather.** Read the week's "Duplicates seen" lines from hand-backs, the
   duplication findings from reviews, and the defects marked as two parts that
   disagree.
2. **Pick one area.** The one with the most reports, or, in a quiet week, the
   next area in a rotation the lead keeps.
3. **Investigate.** List every route that makes the decision in that area, with
   a table of which checks each route applies.
4. **Propose, only with a real find.** When the investigation finds a real
   duplicate, with the routes and the evidence, the project lead writes a design
   that gives the decision one owner in the code. Like any design, it goes to the
   owner for approval before a worker starts. A week with no real find sends
   nothing: no "nothing found" report, no low-value item. (CTO input,
   2026-11-13.)

One investigation costs about 130,000 to 160,000 tokens (measured on
2026-11-13 for the model routes and the lead routes): cents on a low-cost model,
a few dollars on the strongest.

### 3. Fixing

An approved proposal is built like any feature: tasks first, then sessions
(the lead guidance "Plan before you start workers"), with a first part that
changes no behaviour. When it ships, the decision has one owner in the code.
Nothing is added to a list.

### Generic

The instructions and the weekly task name no agent-net decision. They work on
any project agent-net builds.

## Build

Each piece needs the owner's go.

1. The instruction text: the lead, worker, reviewer and defect lines, and the
   "Duplicates seen" line in the hand-back. One small worker, text and goldens
   only.
2. The weekly task to each project lead. Its own short design first: how it is
   scheduled, what it reads, and where its proposal goes.
