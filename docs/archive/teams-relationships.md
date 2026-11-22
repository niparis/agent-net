# Teams and relationships

Date: 2026-11-06
Status: proposal for owner review. Documentation only; nothing in this design is built.

This covers steps 4 and 5 of [modularity.md](modularity.md). It builds on the
[settings layer](settings-layer.md) and the [Repo/Agent split](repo-agent-split.md),
including step 3e's typed graph references. Code citations describe the present
implementation, not the proposed result. Each build step below ships separately
and preserves the behavior of a network with no teams.

## What a Team is for

A Team is a label for filtering and reach. The owner can show one Team's
members on the Graph, and the CTO can reach that group in one place. Teams
enforce nothing: they change no request rule, setting, delivery default,
permission or authority. Membership and ownership do not grant trust, work or
access. The later sections of this proposal describe ideas that are not part of
this step. Until an owner decision makes them real, a Team does nothing beyond
labelling its members.

## Choices

- A Team is a named group of Agents and Repos with a shared purpose. An Agent
  may own it. Ownership and membership are distinct: owning a team does not
  imply membership, ownership of its repos, or authority over its members.
- An Agent or Repo has at most one direct team. Membership gives settings a
  single parent; it does not grant message trust or assign work.
- The relationship module owns the interpretation of relationship edges. Its
  first implementation reproduces today's request gates, relationship text and
  held-request and reminder behavior. Delivery uses one pipeline resolver.
  It does not replace caller authentication, owner approval or the delivery
  state machine.
- A reviewer that owns nothing can advertise expertise for optional help, and
  can be assigned a delivery step for every Repo in a Team when work is
  required. Discovery is not assignment, and an assignment does not itself
  add a step or make a review required.
- A Team assignment is a default for its member Repos. The owner must decide
  whether a Repo may override a Team reviewer before delivery defaults ship.

## Team and membership

A Team has a stable ID, display name, purpose, optional owning Agent and
settings. Its purpose describes the shared work, not a new charter for each
repo. An architect is an ordinary Agent that owns a Team. Its role is free
text. The owner may leave a Team without an owner. Owning a Team does not make
its Agent the owner of its Repos; those retain their own owners from step 3.
A Team is not a session and cannot receive a message.

A typed `member_of` edge points from an Agent or Repo to a Team. The source
may have zero or one such edge. Multiple teams, nested teams and team-to-team
membership are out of scope. Validate the endpoint kinds and uniqueness in the
same transaction as the write. Names are display only; typed IDs are the keys.
Moving a member replaces the edge atomically, with history and an expected old
value. Removing membership leaves the member and its own settings intact,
subject to the settings reconciliation below. Deleting a Team requires moving
or removing its members and resolving assignments first; do not silently
cascade them. Archive can retain the identity and history but cannot leave
active members or assignments pointing at an inactive Team. Delete Team settings
rows with history in the same transaction; retain immutable history and refuse
unsafe undo. An owning Agent cannot be deleted or tombstoned until the owner
clears or transfers Team ownership explicitly.

Membership is not inferred from a repo owner or session location. An Agent and
its Repo may belong to different teams, or one may be unassigned. Show that
fact instead of silently changing either membership. When working in a Repo,
the Repo's Team is the team context; an Agent outside a Repo uses its own Team.
This is the existing settings design's single-chain rule
(`docs/design/settings-layer.md:149`, `docs/design/settings-layer.md:168`).
A session's current repo context, not every repo its Agent owns, chooses the
chain. No two teams' settings are combined.

## Settings and delivery

Team scope is new work. Add Team to `Where`, snapshot storage and resolution,
settings addresses, write validation, history, undo and revision propagation.
For a Repo use network, its Team, Repo, acting Agent and session or run, only
where the key permits each level. Without a Repo use the Agent's Team. Never
combine both Teams. Show effective value, source and raised status.

Add Team to the delivery rule keys `delivery.tests_before_merge`,
`delivery.merge_authority`, `delivery.deploy_rule`,
`delivery.pipeline.review.required_risk`, `delivery.pipeline.review.required_from`,
`delivery.pipeline.review.independent`, `delivery.pipeline.review.requires`,
`delivery.pipeline.merge.requires`, `delivery.pipeline.merge.approver`, and
`delivery.pipeline.deploy.approver`. CI and release have no enforced keys yet;
add Team when those mechanisms exist. Team review requirements can use
`delivery.pipeline.merge.requires` with `review` in custom tests mode, and the
review keys set its details. An assignment alone does not require review.

Preserve existing no-Team semantics, including that a shipped default is not a
network bound. Before cutover capture value and source for every delivery key
for populated and empty networks, and compare after every step. For rules, an
explicit network row bounds Team and Repo; an explicit Team row bounds its
children. On parent tightening or membership move, atomically persist each
explicit child that is too loose at the stricter value, with history, source,
revision and an impact preview. Validate writes against effective explicit
bounds. On leaving or relaxing a parent, keep those persisted raised values;
only an explicit authorized edit may relax them. Undo is a fresh validated
change and cannot bypass current bounds. Reconcile membership and values in one
transaction with expected old values. Runtime overrides are clamped and flagged,
not persisted. Advice replaces freely. Test all of these and no-Team parity.

## Relationships today

These are current endpoints, before typed 3e references. `setEdgeTx` requires
`governs` from an architect node to a component; delivery edges go from a
component or architect to a component. `graphKind` does not consult Agents,
so an Agent owning no Repo cannot be an endpoint
(`internal/gateway/store_graph.go:27`, `internal/gateway/store_graph.go:771`).
`depends` links consumer to provider. `governs` carries advisor, arbiter or
owner mode. These are guidance and request rules, not sandbox authority.
Delivery assignments map step references to owners, not evidence
(`internal/protocol/delivery_pipeline.go:149`,
`internal/protocol/delivery_pipeline.go:482`).

Legacy `deploys` is owner-gated but misses delivery-edge uniqueness and
component-target checks. It is a fallback behind `delivery_deploys`
(`internal/gateway/store_graph.go:732`, `internal/gateway/store_graph.go:736`,
`internal/protocol/delivery_pipeline_settings.go:108`). Preserve this until a
separate owner-approved change; test both edges together.

Request gating matches `governs` by Agent identity and `depends` and `deploys`
by Repo identity. Only `depends` permits a wildcard source. Exact source wins,
then literal kind order: `deploys`, `depends`, `governs`. No match is normal;
an unscoped recipient with peer trust gets auto
(`internal/gateway/graphread.go:168`). Workers and sessions without an Agent
use their Repo as Agent identity; CTO maps both identities to `CTONodeName`.
For scoped sessions peer trust writes a wildcard depends edge for the Repo:
on is auto, off is normal, shared by its sessions. Non-request messages instead
use the session flag (`internal/gateway/store.go:1965`,
`internal/gateway/server.go:2210`). Only the registered CTO is exempt from the
send gate today, not the owner in person (`internal/gateway/store.go:2622`).

The request relation line is separate: exact edges only, first in Graph order
(source, target, kind). A wildcard can gate without framing. Same or unknown
identities can yield no line. Preserve normal, auto, ask and never, existing
approval, reply and notification handling. Parity fixtures cover trust on and
off, two sessions in one Repo, same-name Agent and Repo ties, wildcard framing,
depends plus governs, workers, unscoped sessions, CTO and owner in person.

## Module boundary

The network owner alone selects the network rule. The first release ships only
`current`; defer rule selection and proposals until a second concrete recipe.
Any future Team selection may only tighten the network result, ranked from
strictest to loosest: `never < ask < normal < auto`. Test every pair, including
that Team selection can never yield auto when the network would not. Reject an
unknown rule version change atomically; retain the last valid snapshot. The owner
must decide the future selection policy before Team delivery defaults ship.

The module receives authenticated identities, typed refs, recipient Team context,
message type, edges, peer trust and provenance from a consistent snapshot. It
returns a disposition and a typed relation (from, to, kind, mode or no relation),
with rule and evidence. The gateway validates and renders it, neutralizes marker
text, enforces the atomic send gate and dispatch recheck, and retains owner and
caller checks. A malicious marker in a rule result cannot become gateway framing.

Include held-request always decisions and reminder eligibility in the contract:
preserve their current distinct matching and edge creation behavior in parity
fixtures before switching, including their wildcard and identity differences.
Keep the gateway's transaction and reminder recheck. Delivery lookup stays out
of this module: the gateway resolves Team defaults into
`snapshot.DeliveryAssignments`; `PipelineFromSettings` remains the one pure
pipeline overlay for gateway, settings view, worker notices and maturity text.
Fixtures cover all those callers, modes, message types and concurrent changes.

## Cross-cutting work

The owner decided on 2026-11-06 that a cross-cutting Agent may own nothing,
works through relationships and must be advertised properly on the network.
A security reviewer can be a Team member without owning the Team or a Repo.

### Optional help

The Agent has its own charter, including scope, accepts, refuses and escalation,
and searchable expertise. Advertise these with the Agent identity, its Team,
role, session availability and wake capability. `agent_sessions_find` should
find matching sessions by expertise and charter as well as the existing
project and function fields; `agent_graph_read` supplies the full charter and
relationships before a request. Use the existing session ID for a message.
The requester asks; the reviewer judges under its charter and the actual
request rule. Membership or advertising is not authorization to act elsewhere.

Show whether a session is online, can wake on a message, or is unavailable.
Do not promise wake for every harness: current discovery already describes
some sessions as waking on message (`internal/gateway/toolread.go:259`), and
wake depends on supported session and daemon state
(`internal/daemon/session_wake.go:51`). A wake failure or offline session is
visible to the sender; a message is not proof of completed work. If no session
can be reached, the requester can choose another expert or ask the owner.

### Required team work

An owner may assign an Agent, including one that owns no Repo, to a Team step.
Team defaults accept only an Agent, never `@none`, `@delegate`, or another
special owner. A Repo may explicitly select a special owner; show its source.
Test that a Team default cannot remove or delegate approval. The owner decision
below controls Repo override before this feature ships.

Subject to the owner decision on Repo overrides, resolve the pipeline first.
For each exact reference use the Repo assignment,
then Team default, then pipeline owner, subject to the existing guards:

| Reference | Phases and precedence |
|---|---|
| accept, review, merge, release, verify | Exact phase; Repo, Team, pipeline. |
| test | Each phase uses its exact assignment first; after-merge falls back to the before-merge test assignment, then pipeline. |
| approve for merge or deploy | Exact phase and target; an empty target means merge. Apply an assignment only when the pipeline owner is none, or replace a delegate with the owner in person. Otherwise retain the pipeline owner. |
| deploy | Exact phase; `delivery_deploys` wins over legacy `deploys`, then pipeline. Apply Repo before Team within each matching kind. |

Test every reference and phase, including both deploy edges and approval guards.
The gateway supplies resolved assignments and their source to the pure overlay.
A Team assignment does not create a step by itself.

Add typed Agent recipients to dispatch and claim. When an assignment names an
Agent, match session Agent ID first, even if its Repo also matches a different
assignment; Repo matching applies only to Repo recipients. A non-worker eligible
session on either the change machine or another machine can claim and report
for its assigned Agent. Check identity, independence, claim, revision, commit and
evidence on both machines. No eligible session leaves the step pending; show
availability, never silently pass or substitute the caller.

On assignment or membership change, preview and recompute each affected Repo
and step atomically. Expire queued messages for changed unclaimed tasks and
route to the recomputed Agent; a special owner follows its existing semantics.
Clearing a default falls back through the matrix, never automatically completes
the task. Preserve completed evidence. Claimed tasks await the owner decision
below; do not ship reassignment until decided. Test claimed and unclaimed tasks,
cleared assignments, special owners, moves, revision and remote reports.

## Data and live migration

Teams ship after split step 3e. That cutover rejects old daemons and drops legacy
adapters; do not promise old clients a Team projection. Add typed Agent and Team
endpoint validation as a prerequisite in 3e or Team step 1, including an Agent
with no Repo and invalid kind tests. Keep Team records and `member_of` edges out
of every legacy graph projection, including `Graph()` and the legacy node union;
test each projection. New clients use typed IDs and the 3e version gate.

Add Team records, history, typed membership, settings and assignments with
uniqueness and expected-value checks. Start empty; do not infer membership.
Migrate populated databases transactionally and compare no-Team behavior. Before
Teams ship, release a schema version guard: a gateway refuses any database
version newer than it knows. Test opening a future version and a known version.
There is no downgrade data or downgrade path. Rollback restores a backup from
before upgrade, as in the split design (`docs/design/repo-agent-split.md:328`).

## Owner and CTO surfaces

Graph shows Team nodes, purpose, owning Agent, members and typed edges. Show
membership separately from governance and delivery assignment. Selecting a
Team lists its Repo and Agent members, effective settings with source, team
step owners and affected repos. Selecting a Repo shows inherited and explicit
step owners and any raised rules. Sessions shows Agent identity, working Repo,
team context, expertise and actual availability; an Agent with no owned Repo
remains discoverable. Team itself is never shown as a live session.

CTO reads expose typed IDs, membership, ownership, effective settings and
sources, relationship rule/version, assignments and affected pending tasks.
Changes and proposals name the level and typed target, expected old value,
plain-language diff and impact, including child adjustments. They use existing
owner gates: delivery and other rules require owner authorization; advice
follows the settings layer's permitted CTO path. A proposal to move a Repo
shows changed inheritance and delivery routing before approval. Reject stale
expected values atomically and retain history and undo, with the same safety
checks as a fresh change. No peer message counts as owner approval.

## Build steps

Each estimate includes tests; split further if needed without partial cutover.
Write failing tests first. Owner decisions below block Team delivery defaults.

0. **Schema guard** (about 200 lines): ship in a release before Teams; reject
   unknown database versions and test known and future versions.
1. **Team records and typed endpoints** (about 1,000 lines): schema, membership,
   ownership, endpoint kinds, projections, history and parity.
2a. **Team settings storage and reads** (about 800 lines): scope, definitions,
   snapshots, resolver and source fixtures for every delivery key.
2b. **Settings writes and reconciliation** (about 1,000 lines): validation,
   previews, persisted raises, membership moves, history, undo and parity.
3a. **Agent delivery eligibility** (about 900 lines): typed recipients, claim,
   remote reports, precedence, revision and machine tests.
3b. **Team delivery defaults** (about 1,000 lines): matrix, source, preview,
   pending-task reconciliation, UI and all phase and special-owner cases.
4. **Discovery gaps** (about 500 lines): Team context, charter search and
   honest availability and supported wake. Expertise is already stored and
   searched (`internal/gateway/toolread.go:278`); full session reads already
   include purpose and charter. Test wake only on supported harnesses.
5a. **Gate and framing module** (about 1,000 lines): fixtures first, typed
   relation rendering, held decisions, reminders and atomic gate parity.
5b. **Delivery lookup** (about 700 lines): resolve Team defaults into the
   snapshot, preserve the pure overlay and parity for every caller.

No second rule has a concrete recipe, so selection and proposals are deferred.

## Owner decisions needed

These choices remain open. Decide before step 3b; preserve current behavior
until a decision is implemented and tested.

1. Should the owner in person be exempt from relationship gates, as decision
   0022 says? Recommendation: yes, with authenticated owner provenance; today
   only the registered CTO is exempt.
2. May a Repo override a Team reviewer? Recommendation: yes, show the source;
   a separate Team rule can require independent review.
3. What happens to claimed tasks after reassignment? Recommendation: retain the
   claim and old owner until completion or explicit release, validate reports
   against the original claim and revision, and reroute only unclaimed tasks.
4. What rule selection can a Team make? Recommendation: only registered,
   versioned rules that tighten the network result, with owner authorization;
   defer selection until a concrete second recipe exists.

## Changes after review

| Finding | Resolution |
|---|---|
| B1 | Correct current endpoints; typed Agent and Team endpoint prerequisite and tests. |
| B2 | Agent recipients, identity precedence and cross-machine claims and reports. |
| B3 | New Team scope, storage, resolver, validation and named delivery keys. |
| B4 | Persist raised children, reconcile atomically and capture all delivery values and sources. |
| B5 | Owner network selection, strict rank, tightening tests and version rejection. |
| B6 | Team defaults accept Agents only; explicit Repo special owners show source. |
| B7 | Pre-Team schema guard; rollback restores backup. |
| B8 | Ship after 3e version gate; filter and test all legacy projections. |
| B9 | Held decisions and reminders join the contract and parity fixtures. |
| S1 | Scoped peer trust and non-request session flag described and tested. |
| S2 | Depends-only wildcard and alphabetical tie order documented and tested. |
| S3 | Exact relation matching and Graph order fixtures. |
| S4 | Worker, unscoped and CTO identity cases in parity fixtures. |
| S5 | Current CTO exemption stated; owner-in-person decision requested. |
| S6 | Step and phase matrix preserves pipeline guards. |
| S7 | Team merge requirement and review keys make required review expressible. |
| S8 | Typed relation and gateway rendering with marker test. |
| S9 | One pure pipeline overlay consumes resolved snapshot assignments. |
| S10 | Steps 2, 3 and 5 split with sizes. |
| S11 | Discovery narrowed to missing context, search and supported wake. |
| S12 | Per-Repo recomputation and claimed task decision, with tests. |
| S13 | Legacy deploy behavior preserved and both edges tested. |
| M1 | Corrected citations and removed obsolete references. |
| M2 | Deferred selection and proposals pending a concrete second rule. |
| M3 | Delete settings transactionally and retain history. |
| M4 | Clear or transfer ownership before Agent deletion or tombstone. |
| M5 | Owner decisions and recommendations replace open questions. |
| M6 | Repo, Agent and Team name the target concepts; current node kinds are identified as historical. |
