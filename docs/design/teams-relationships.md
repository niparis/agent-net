# Teams and relationships

Date: 2026-11-06
Status: proposal for owner review. Documentation only; nothing in this design is built.

This covers steps 4 and 5 of [modularity.md](modularity.md). It builds on the
[settings layer](settings-layer.md) and the [Repo/Agent split](repo-agent-split.md),
including step 3e's typed graph references. Code citations describe the present
implementation, not the proposed result. Each build step below ships separately
and preserves the behavior of a network with no teams.

## Choices

- A Team is a named group of Agents and Repos with a shared purpose. An Agent
  may own it. Ownership and membership are distinct: owning a team does not
  imply membership, ownership of its repos, or authority over its members.
- An Agent or Repo has at most one direct team. Membership gives settings a
  single parent; it does not grant message trust or assign work.
- The relationship module owns the interpretation of relationship edges. Its
  first implementation reproduces today's request gates, relationship text and
  delivery assignment behavior. It does not replace caller authentication,
  owner approval or the delivery state machine.
- A reviewer that owns nothing can advertise expertise for optional help, and
  can be assigned a delivery step for every Repo in a Team when work is
  required. Discovery is not assignment, and an assignment does not itself
  add a step or make a review required.
- A team assignment is a default for its member repos. A repo assignment wins
  for that exact step and phase. A required team rule can prevent a repo from
  opting out, through the settings layer's normal tightening rules.

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
active members or assignments pointing at an inactive Team.

Membership is not inferred from a repo owner or session location. An Agent and
its Repo may belong to different teams, or one may be unassigned. Show that
fact instead of silently changing either membership. When working in a Repo,
the Repo's Team is the team context; an Agent outside a Repo uses its own Team.
This is the existing settings design's single-chain rule
(`docs/design/settings-layer.md:149`, `docs/design/settings-layer.md:168`).
A session's current repo context, not every repo its Agent owns, chooses the
chain. No two teams' settings are combined.

## Settings and delivery

For work in a Repo, resolve each applicable key through network, the Repo's
Team, Repo, acting Agent and session or run, restricted to the levels declared
by that key. For work without a Repo use network, the Agent's Team, Agent and
session or run. A worker uses the Repo chain for delivery and the acting Agent
level for applicable behavior settings. The session or run is an input, not a
persisted team member. Show the effective value, source and any adjustment.
An Agent's own Team does not insert a second Team into a Repo's chain.

Advice can change freely at an allowed lower level. A rule can only tighten.
When a parent rule becomes stricter, adjust explicit child values that are now
too loose and report the changes; never refuse the parent change merely because
of children (`docs/design/settings-layer.md:21`). Membership changes can have
the same effect: preview and atomically reconcile explicit Repo and applicable
Agent values against the new chain, record history and report raised values.
Leaving a team does not automatically loosen a value already raised. Runtime
session and run overrides are resolved against the new bound and flagged when
raised. A move must not partially change membership or settings on failure.

A team may supply delivery settings only for keys whose definitions permit the
team level. The ordered pipeline has its own representation and validation
(`docs/decisions/0057-delivery-pipeline.md`); do not pretend the whole pipeline
is an ordered settings value. Resolve a valid pipeline first, then resolve each
step assignment. A Team's required review must be represented by a supported,
enforced delivery rule, not by the mere presence of a reviewer edge. Until
such a team-level requirement exists, preview must say that an assignment
alone does not require review.

## Relationships today

The original graph decision is `docs/decisions/0022-component-graph-and-cto.md`;
peer trust and its limits are in `docs/decisions/0013-peer-trust-and-hints.md`.
The split assigns typed endpoints in `docs/design/repo-agent-split.md:247`.
The following is the behavior to capture in parity tests before extraction.

| Edge | Meaning and present effect |
|---|---|
| `depends` | Consumer Repo to provider Repo. A directed request receives a consumer relation line. Its request rule can gate that request. It does not forbid other messages. |
| `governs` | Governor Agent to governed Agent, with `advisor`, `arbiter` or `owner` mode. The directed request line names the mode. Its request rule can gate requests. Eligible modes also select delegated delivery approvers. Mode text is guidance, not a sandbox. |
| `deploys` | Legacy deployer to Repo relation and deploy assignment. Its request rule participates in gating; it also supplies a deploy owner and target deploy guidance. Preserve legacy endpoint matching during typed migration. |
| Delivery assignment | Agent to Repo, or a special owner. `accepts`, `reviews`, `tests`, `tests_after_merge`, `approves`, `approves_deploy`, `merges`, `releases`, `delivery_deploys`, `verifies`, and legacy `deploys` assign an exact step and phase. Assignment does not by itself grant peer trust. |
| `member_of` | Proposed Agent or Repo to Team. It selects inheritance and team defaults; it has no request gate or authority by itself. |

The mapping of assignment kinds to steps and phases is in
`internal/protocol/delivery_pipeline.go:54` and
`internal/protocol/delivery_pipeline.go:77`. Special owners include caller,
merger, owner, GitHub, delegate and none, with eligibility depending on step
(`internal/protocol/delivery_pipeline.go:142`). The gateway resolves the
pipeline from settings and assignments (`internal/gateway/delivery_changes.go:143`).
Delivery tasks and evidence remain owned by the delivery system; a relationship
is not evidence that a step passed. Reports are tied to a change revision and
commit (`internal/protocol/delivery_pipeline.go:11`).

There are separate request outcomes and relation text. For the gate, the
current lookup matches directed `governs` against Agent identities and
`depends` or `deploys` against Repo identities, including wildcard source;
exact source precedes wildcard, then kind orders ties. No match is `normal`.
An unscoped recipient with peer trust gets `auto`
(`internal/gateway/graphread.go:151`). The values are:

- `normal`: deliver under the receiver's ordinary rules.
- `auto`: deliver with the gateway's peer-trust note and its existing limits.
- `ask`: hold for the owner before delivery.
- `never`: reject the request.

The atomic send path holds or rejects non-CTO requests
(`internal/gateway/store.go:2617`); dispatch checks `ask` again and supplies
trust on `auto` (`internal/gateway/server.go:2195`). Preserve the existing CTO,
bot, owner approval, worker, reply and notification handling rather than
applying one generic gate to every message. In particular, a reply is not an
approval, and gateway framing cannot be supplied by the sender.

The relationship line is separate: on requests only, it names a matching
directed `depends` or `governs` edge, or says no relation; same or unknown
identities can yield no line (`internal/gateway/graphread.go:61`,
`internal/gateway/graphread.go:109`, `internal/protocol/graph_context.go:192`).
It does not mean that the edge is required to send. Do not collapse this into
the request gate, or assume that a governance mode alone creates `auto` trust.
The modularity plan's order/suggestion/information shorthand is an intent for
wording, not a complete description of the present gate.

## Module boundary

The gateway selects a named, versioned relationship rule at network level,
with a team selection where allowed. For a request to a Repo session, use the
recipient Repo's Team; without a Repo, use the recipient Agent's Team. No team
means network default. The shipped `current` rule is the only implementation
in the first release. Selection is configuration, not executable code from a
recipe. Unknown or unavailable rule versions fail closed for changed
configuration and keep the last valid snapshot; never silently select a more
permissive rule.

The interface takes authenticated sender and recipient session identities,
typed Agent and Repo refs, recipient team context, message type, relevant
edges and their modes and rules, peer-trust and owner/CTO provenance. It
returns a request disposition (`normal`, `auto`, `ask`, `never`) and relation
framing, with the rule and evidence/source for inspection. Delivery asks a
separate operation for the eligible assignment and delegated approver for an
exact Repo and step reference. The gateway still enforces the disposition in
the atomic write and rechecks at dispatch, writes the framing itself, and
applies existing approval and security checks. The delivery engine still
validates pipeline, eligibility, tasks and evidence. Give both operations a
consistent graph/settings snapshot or transaction, and define deterministic
ordering for every ambiguity. A rule cannot impersonate the owner, bypass
caller checks or widen a security minimum.

A future implementation can change edge interpretation and presentation, but
must implement this contract and pass security invariants. Adding `reports_to`
would be an explicit new typed edge and rule version, not an implicit effect
of team membership. No general plugin loading or user-supplied executable
rules is needed for these steps. Parity tests compare existing results across
all edge modes, wildcard ties, missing identities, peer trust, message types,
CTO provenance and delivery owners before routing is switched.

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
some sessions as waking on message (`internal/gateway/toolread.go:244`), and
wake depends on supported session and daemon state
(`internal/daemon/session_wake.go:17`). A wake failure or offline session is
visible to the sender; a message is not proof of completed work. If no session
can be reached, the requester can choose another expert or ask the owner.

### Required team work

The owner assigns the reviewer Agent to the Team's `reviews` step reference.
For every member Repo, resolve the Repo's explicit assignment first, then the
Team assignment, then the existing pipeline/default owner. A Repo with no
explicit assignment inherits even if the Agent owns nothing and is not a
member. The UI names the source. An explicit special owner is an override,
not an absent assignment; clearing it exposes the team default. Only the
owner changes these delivery assignments, as today
(`internal/gateway/store_graph.go:732`).

A team assignment applies when that step is present and ready; a separate
pipeline requirement makes it mandatory. The existing delivery engine chooses
an eligible non-worker session for the assigned Agent, queues and wakes it when
supported, and checks claims, independence and evidence against the exact
revision. If no eligible session is available, keep the step pending and show
who is offline; never silently use the caller or mark it passed. A team
assignment is not a broadcast and should not create one persistent edge per
Repo. On membership or assignment change, recompute unclaimed pending tasks in
one transaction using the existing owner-change behavior
(`internal/gateway/store_graph.go:700`); preserve claimed work and completed
evidence, and revalidate any report against its revision and owner rules.
Preview affected repos and pending tasks. Explicitly test a move while a task
is claimed, and make any required reassignment visible rather than dropping it.

## Data and live migration

Add a Team table keyed by stable ID, with name, purpose, nullable owner Agent,
active state and history. Add typed membership edges to the 3e graph, with a
unique active source across Teams. Add team delivery assignment edges keyed by
Team and exact step reference, with the same owner validation as Repo
assignments. Settings use the existing team scope and revision/history model.
Keep edge kind, typed endpoint, mode and rule in graph history and undo. Team
ownership is a direct reference like Repo ownership, not `governs`.

Migrate additively: create empty tables and indexes in a new migration, leave
existing Repos, Agents, IDs, edge strings, settings, session mappings, delivery
changes, tasks and evidence untouched. Existing networks have no Teams and
resolve identically. Do not infer teams from names, existing architect edges or
ownership. Backfill only new typed metadata where 3e requires it, with checks
for ambiguous legacy data and no silent guessing. Use a transaction for each
cutover and retain old data as the split design does
(`docs/design/repo-agent-split.md:274`).

Old clients must continue to see their existing graph projection and delivery
routing. Version new frames and snapshots so old clients can ignore unknown
fields; reject writes that would erase team data through an old representation.
An old gateway binary may open the database but cannot honor team inheritance
or team assignments. Document downgrade as unsafe once teams are active; do
not claim that the preserved old rows reflect later changes. Provide a preview
and an explicit owner-directed removal of team-dependent configuration before
any downgrade. Never automatically delete live data.

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

Each step targets roughly 1,000 changed lines, including tests. Split a step
further if needed; do not ship a partial cutover. Write failing tests first.

1. **Team records and graph reads.** Add empty schema, typed membership and
   owner references, validation, history and read-only Graph/CTO views. Exercise
   migration from a populated gateway, uniqueness, kind checks, old clients and
   no-team parity. Nothing inherits yet.
2. **Membership writes and settings.** Add owner-gated edits, preview, atomic
   move and reconciliation, sources in Graph and session context. Test advice,
   stricter parent adjustment, explicit child values, mixed-team Agent/Repo,
   concurrent edits, undo and no-team parity.
3. **Team delivery assignments.** Add team defaults and Repo precedence,
   preview and pending-task reconciliation. Test exact phases, special owners,
   no required step, offline reviewer, claimed tasks, revision evidence and
   member moves. Keep existing Repo assignments and no-team routing unchanged.
4. **Advertising.** Expose ownership-free Agents' charter, expertise, team and
   honest availability through discovery and Sessions. Test discovery, supported
   wake, offline and no-session cases without changing message authority.
5. **Extract current relationship rule.** Capture parity fixtures first; route
   request gate, relation framing and delivery owner lookup through the module
   contract on the same transaction/snapshot. Test all current edge cases,
   concurrent changes and security boundaries. Only `current` is selectable.
6. **Named selection and proposals.** Add network/team selection, versioned
   reads, owner-gated proposal and history. Test missing versions, stale
   proposals and default parity. A second rule is built only for a concrete
   recipe; this step does not invent one.

## Open questions

1. **Should an Agent and its working Repo be in different Teams?** Recommend
   yes. Use only the Repo's Team for that work and show the mismatch. Forbid
   it only if a real workflow needs the restriction.
2. **May a Repo override a team reviewer?** Recommend yes for the assignment,
   with the explicit source shown. A separate tighter team rule can require an
   independent review; assignment alone cannot enforce a named reviewer.
3. **What happens to claimed tasks when assignment changes?** Recommend keep
   the claim and show the old owner until it completes or is explicitly
   released; reassign only unclaimed tasks. Confirm the report eligibility rule
   in the implementation design before changing live routing.
4. **Should team membership imply trust or governance?** Recommend no. Keep
   these explicit edges and owner-controlled request rules.
5. **How is an offline ownership-free Agent resumed?** Recommend reuse the
   supported session wake path and report unavailable otherwise. Do not build
   a general agent launcher until a recipe needs one.
6. **Can a team rule choose any new relationship implementation?** Recommend
   only registered, versioned implementations and only where permitted by the
   settings definition; unknown versions fail closed. Define an ordering before
   permitting any override that can loosen a network security rule.
