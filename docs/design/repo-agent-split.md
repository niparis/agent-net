# Repo and agent split

Date: 2026-11-05
Status: design for owner review. Documentation only. Nothing is built.

This is build step 3 in [modularity.md](modularity.md). It follows the
[settings layer](settings-layer.md): delivery already resolves through that
layer. The split separates the software being worked on from the lasting
identity of the agent that leads it. It does not add teams or redesign the
relationships module.

Every statement about the current implementation below names the code that
supports it. The target model is a proposal, not a claim about current code.

## The decision in one page

Today, `project`, `graph node` and `lead agent` are used as if they were one
identity. A project name identifies the graph component; its charter and
settings describe the project, while sessions discovered in that project
appear on the same graph node and are treated as the agents for that work.
The model cannot say that two lasting agents lead or act for one repo without
reusing the repo node as their identity.

After the split:

- A **Repo** identifies the software and its checkouts. It owns purpose,
  maturity, delivery and repo-level settings.
- An **Agent** identifies who acts. It owns a charter, a free-text role and
  zero or more repos. The first migration creates one agent per existing
  project and gives that agent ownership of that repo.
- A **session** belongs to one agent. Its repo context is separate and names
  the repo it is working in. That lets a session's identity and its current
  work context differ.
- The `owns` relation connects agents to repos. It is a direct relation needed
  by this split, not a team mechanism or a general relationships module.
- Delivery stays repo-wide. Existing named delivery owners become Agent IDs;
  caller and merger keep their defined special meanings.
- Existing projects migrate to one Repo and one owning Agent with the same
  visible name and the same effective settings and behavior.

```text
Machine --checkout--> Repo <--owns-- Agent <--belongs to-- Session
                          ^                           |
                          +------ working context ----+
```

The session's working context can name a repo without implying ownership.
An owner session or expert can therefore act in a repo it does not own.

## 1. Today's model, checked against code

### Node kinds and identity

The protocol calls a graph node a component or architect. A `Node` has one
name and kind, a `Charter`, node settings, delegation, checkout locations,
sessions and a session count. There is no independent Repo or Agent object in
this contract (`internal/protocol/graph.go:12-23,45-61`).

The gateway's `graphKind` returns `component` when the name exists in the
project registry or is the `repo_project` of a session. It returns `architect`
for graph nodes created with that kind (`internal/gateway/store_graph.go:27-47,
66-91`). Graph reads synthesize implicit component nodes from registered
projects, sessions and edge endpoints (`internal/gateway/store_graph.go:855-883`).
So an observed or registered project is already a graph node even before an
owner makes it explicit.

The persistent schema makes the coupling visible. `projects` rows contain
`(node_id, path, name, remote, default_branch, machine metadata)`; the
registered checkout is machine-scoped (`internal/gateway/store.go:147-152`).
The graph schema keys nodes by name, charter by `(node, attr)`, and edges by
`(source, target, kind)` (`internal/gateway/store.go:191-195`). Session rows
carry `repo_project`, root, remote, branch and worktree, not an agent identity
(`internal/gateway/store.go:134-138`).

The map attaches registered locations to component nodes by project name and
attaches every session to the node named by its `Repo.Project`; the CTO session
is attached to the fixed CTO node (`internal/gateway/graph_view.go:144-182`).
The graph's component is therefore both repo-shaped and agent-shaped in the
owner's main configuration view.

### What is attached to the node today

| Current data | Current key and meaning |
|---|---|
| Charter | `graph_charter(node, attr)`. The protocol charter contains purpose, maturity, accepts, refuses, feature requests, consumers, escalation, needs and notes (`internal/protocol/graph.go:12-23`; `internal/gateway/store.go:192-194`). |
| Dependency and governance edges | `graph_edges(source, target, kind, mode, rule)`. The graph supports `depends`, `governs` and `deploys`, with `governs` modes advisor, arbiter or owner (`internal/protocol/graph.go:74-87`; `internal/gateway/store_graph.go:722-785`). |
| Delivery assignments | More edge kinds in the same `graph_edges` table. The repo is the target; its source names a component/architect or a special owner (`internal/protocol/delivery_pipeline.go:54-120,142-157`; `internal/gateway/store_graph.go:622-655,722-785`). |
| Maturity defaults | Read from the component's charter, then used to resolve repo settings (`internal/gateway/store_graph.go:936-968`; `internal/gateway/store_graph.go:328-356`). |
| Settings | Canonical settings rows use `level='repo'` and the component name as `scope`. The level set already includes `agent`, but the graph does not yet provide agent identity (`internal/gateway/store.go:618-622`; `internal/gateway/store_graph.go:246-290`). |
| Delegation | Legacy project policy is keyed by `(machine node_id, project)`, then read into that component's public `Delegation` list (`internal/gateway/store.go:165-168`; `internal/gateway/store_graph.go:969-995`). |
| Locations | Registered machine paths become `GraphLocation` values on the component node (`internal/gateway/graph_view.go:144-164`). |
| Sessions | The graph view lists online sessions under the component named by their discovered repo (`internal/gateway/graph_view.go:165-182`). |
| Delivery changes and merger | Changes key on `project`; merger registrations key on `(project, machine)` and a session ID (`internal/gateway/store.go:657-669,759-765`). |

### Every current project-equals-node-to-agent assumption in the split seam

These are the direct code paths the first split must replace or adapt. This is
the seam inventory for this design, not a claim that every project-named field
throughout the system should disappear. Machine references, file locations,
worker requests and git evidence may still need an explicit Repo ID.

1. **Node classification.** A project with a registered folder or session is
   `component`; a standalone named node is `architect`
   (`internal/gateway/store_graph.go:27-47,855-883`). This makes graph kind
   decide whether an identity is a repo or agent.
2. **Charter identity.** `SetCharter` accepts a `node`; storing its first
   attribute inserts a graph node of the inferred kind and stores the charter
   under that node (`internal/gateway/store_graph.go:177-243`). Graph reads put
   each charter attribute back on the same `Node`
   (`internal/gateway/store_graph.go:889-935`).
3. **Edges and endpoints.** Edge validation resolves each endpoint through
   `graphKind`; governance is architect-to-component and delivery assignments
   must target components (`internal/gateway/store_graph.go:722-785`).
   Edge keys store names rather than independent repo and agent IDs
   (`internal/gateway/store.go:193`).
4. **Per-repo settings identity.** Repo setting `scope` is the project name.
   The graph adapter reads those scopes into node settings and calculates
   maturity defaults from that node's charter
   (`internal/gateway/store_graph.go:246-290,936-968`). The resolver contract
   describes the current repo identity the same way
   (`docs/design/settings-layer.md`, section “Levels”, says the repo identity
   today is the graph node name).
5. **Session-to-node mapping.** The graph view picks `a.Repo.Project` as the
   node; `agentNode` uses `Repo.Project` then profile project for session
   behavior (`internal/gateway/graph_view.go:165-182`;
   `internal/gateway/check.go:82-105`). A project is acting as a proxy for
   which agent a session is.
6. **Charter-facing agent behavior.** `agent-net` reads charter through the
   public graph, whose rows are graph nodes and edges; the CTO graph mutators
   accept a `Node` field for `charter_set`
   (`internal/protocol/graph_public.go:8-14,39-59`;
   `internal/gateway/cto_graph.go:26-33`). Today a repo's charter is also the
   acting lead's instructions.
7. **Delivery change identity.** A change stores the project name. Its unique
   open-branch constraint and lookup are per project
   (`internal/gateway/store.go:653-669`). Settings and delivery routing use
   that project value as the repo key.
8. **Step owner identity.** The protocol says a named step owner is a graph
   node, either component or architect; the delivery edge connects its source
   to the project target (`internal/protocol/delivery_pipeline.go:112-120,
   142-148`). Runtime tasks capture `owner_node` for a change and revision
   (`internal/gateway/store.go:697-709`).
9. **Merger identity.** The merger registration is `(project, node_id) ->
   session_id`; validation requires the session's project to equal the target
   project (`internal/gateway/merger.go:11-39`). It is already a session
   selected for the repo, not the identity of the lead agent.
10. **Registration identity.** A daemon adds paths under its machine node ID;
    records include local path, repo name, remote and default branch
    (`internal/gateway/store_projects.go:13-16,18-37,40-90`). The graph
    combines registrations across machines under the same project name
    (`internal/gateway/store_graph.go:855-883`).

The sharp distinction is that a checkout is already a machine-specific record,
but its repo name becomes the graph component identity. The named graph
component then doubles as the lead agent. Decision 0036 already names Project
as the home of a lead and explicitly says it is not a Graph node, while also
calling Agent the node. This design brings the implementation in line with
that decision (`docs/decisions/0036-primitives.md`, “People and agents”).

## 2. Target data model

### Repo

A Repo is the durable identity of software across machines and paths. It has:

- stable repo ID and display name;
- zero or more checkout records `(machine, path, remote, default branch)`;
- purpose, accepts, refuses, feature requests, consumers, escalation and
  needs, as repo-level charter data;
- maturity and deploy rules;
- delivery pipeline and delivery step assignments;
- repo-level settings, including settings previously scoped to its component
  name;
- any repo-local delegation or policy that concerns how work in this software
  is done.

Repo ID must not be a machine path. A single Repo may have several paths, one
per machine, and may have no current checkout. Registration continues to say
which machine has a folder suitable for starting or routing work. A checkout
does not create a second Repo.

### Agent

An Agent is a lasting actor identity. It has:

- stable agent ID and display name;
- a charter for the actor's own goals and boundaries;
- a free-text role such as “lead”, “reviewer”, “security specialist” or a
  phrase the owner chooses;
- ownership relations to zero or more repos;
- agent-level settings, with the levels and tightening rules already defined
  by the settings layer.

Role is descriptive data. It does not select a new graph kind, permission set
or code path. The first implementation may retain shipped role suggestions,
but it stores text and accepts other text. The owner controls what an agent may
do through explicit authority and settings, not by parsing the role string.

An agent may own no repo. An owner or a cross-repo reviewer can be an agent
without ownership. An agent may own multiple repos, which records shared
responsibility without duplicating its lasting identity. Ownership does not
itself grant merge, deploy, or other authority; those continue to use explicit
settings, approval and delivery policy.

### Owns relation

`owns(agent_id, repo_id)` is the minimal explicit link. A repo can have one
primary owner in this step. An agent can own zero or more repos. Do not use
`owns` for ordinary work assignment: a session may work in a repo it does not
own. Do not add team ownership or path ownership here. If later work needs
multiple co-owners, path ownership, or teams, that will require a separate
decision and a migration.

The primary-owner constraint keeps today's single-lead behavior representable.
The new model can still host agents that own nothing. If the owner wants
multiple first-class owners on one repo, that is an open question below.

### Sessions and their work context

Each non-worker, lasting session attaches to an `agent_id`. A session remains
one running process of one harness, as decision 0036 defines. Agent identity
comes from an explicit selection or from the existing project's migrated
owner mapping. It must no longer be inferred from the repo name.

A session can have a current `repo_id` work context. It selects charter and
delivery context without changing the session's identity. The owner, CTO,
reviewers and other agents may work in a repo they do not own when permitted.

A worker run is still a task-specific identity as described by the modularity
plan. Its caller is an Agent, its work context is a Repo, and its resulting
worker session is associated with the run, not permanently made the repo
owner. This design does not redesign runs or sessions beyond what is needed to
express that distinction.

## 3. What moves where

| Current concept | Target home | Notes |
|---|---|---|
| Repo purpose, maturity, accepts, refuses, feature requests, consumers, escalation, needs | Repo | These describe the software and its boundary. Preserve their current text and history. |
| Agent's own goals and conduct | Agent charter | New identity-specific charter. Do not copy repo purpose into every agent charter after the initial mapping. |
| `depends` | Repo-to-Repo when it describes software dependency | Keep the existing rule and meaning. This step only changes endpoint IDs where an endpoint is a repo. |
| `accepts` and `refuses` | Repo charter text | They describe which requests fit this software. They are not the delivery `accepts` step assignment. |
| `governs` advisor/arbiter/owner | Agent-to-Agent | It describes how one actor relates to another. Existing architect nodes become agents. |
| `deploys` relation | Agent-to-Repo delivery assignment | The deployer acts; the repo is deployed. Preserve its present meaning. |
| Delivery step edges (`accepts`, `reviews`, `tests`, `approves`, `merges`, `releases`, `delivery_deploys`, `verifies`) | Agent-to-Repo assignment | Edge source becomes Agent ID; target becomes Repo ID. The `accepts` step name remains distinct from charter accepts/refuses. |
| Step owners `caller`, `merger`, `owner`, `github`, `delegate`, `none` | Special delivery owner values | Retain their protocol meaning. A named node owner now resolves to an Agent ID. |
| Maturity, deploy rules, delivery pipeline | Repo | The settings layer already treats delivery as repo-level and reads the delivery pipeline (`docs/design/settings-layer.md`, “Where a setting lives” and “Levels”; `internal/protocol/delivery_pipeline.go:11-22`). |
| Settings about how one lasting actor behaves | Agent | Use `level='agent'` and Agent ID scope. No current agent identity should be guessed from the repo name after migration. |
| Repo construction and shipment behavior | Repo | Keep current settings at repo scope. Session/run values remain at their existing scopes. |
| Per-machine project registration | Repo checkout | Re-key local registration to Repo ID and keep machine ID, path, remote, default branch, and registration metadata. |
| Session identity | Agent | Attach sessions explicitly to an Agent. |
| Session work context | Repo | Store separately from Agent identity. |

This split does not implement a general `relationships` module. It retains
today's edge behavior and performs only the endpoint translations needed for
repo/agent identity. In particular it does not add reporting lines, path
ownership, or team membership.

### Delivery and settings compatibility

Delivery changes continue to identify the Repo, branch, machine and commits.
The pipeline's evidence remains keyed to a revision and exact commit. The
pipeline type already says that its storage is the gateway's part and that a
repo's pipeline is resolved from settings (`internal/protocol/delivery_pipeline.go:11-22`).
The project field on delivery change rows will become a Repo ID or be renamed
to `repo_id`; new code and serialized contracts should use one canonical term.

For a named step owner, use the owner Agent ID. At routing time, find an
eligible session attached to that agent and working in the Repo. Preserve the
existing special `caller` route to the caller's session and `merger` route to
the merger registered for that Repo and machine. Do not turn merger into the
Repo's Agent owner: it remains an operational session assignment. Today
merger lookup is explicitly project-plus-machine and checks the session's
repo (`internal/gateway/merger.go:11-39`).

Step assignment is a repo property. Repo settings stay `level='repo'` and use
Repo ID scope. Agent settings use `level='agent'` and Agent ID scope. The
network default, team level reserved for later, setting history, source
reporting and resolver chain remain as designed in the settings layer. During
the split, repo maturity must not migrate to an agent just because the old
component also represented its lead.

## 4. Migrating live gateway data

### Required mapping

For each existing project component `P`, create:

1. one Repo `R` preserving `P`'s name as its display name and carrying all of
   its current registered checkout records;
2. one Agent `A` preserving `P`'s name as the initial lead name;
3. one `owns(A, R)` relation;
4. a session-agent association assigning sessions currently mapped to `P` to
   `A`, while retaining their Repo context `R`.

The IDs may be separate internal IDs even if the display names match. Preserve
all effective behavior: same charter in force for repo work, same maturity,
same repo settings and delivery pipeline, same graph rules after endpoint
translation, same project registrations, same merger selection, and same
session routing. The owner-facing graph can initially render the repo and its
owner together so existing users see no behavior change.

Standalone architect nodes become Agent records. Their name, role default
“architect”, charter and outgoing or incoming actor relations are preserved.
If an old edge targets a component because it describes software dependency,
map the endpoint to Repo. If it governs an architect/component relation,
map the architect endpoint to Agent and the component endpoint according to
the table above. Delivery assignment edges always target Repo and their named
source becomes Agent.

### Data to move without changing its effective value

- Copy repo-facing charter fields from the component to Repo. The migration
  should preserve immutable history or add a translation table from old graph
  history IDs to the new Repo/Agent history. Do not silently concatenate
  distinct values.
- Keep settings rows and setting history at `level='repo'`, changing only the
  scope from old project name to Repo ID. Since settings already support the
  `agent` level, do not invent a separate storage mechanism for agent settings
  (`internal/gateway/store.go:618-631`).
- Translate graph edge endpoints using their meaning, not a global string
  replacement. A source that names an existing architect is an Agent; a
  component target in delivery is a Repo. Preserve mode, rule and history.
- Re-key each machine checkout under Repo ID. Preserve path, remote, branch,
  who registered it, timestamps and graph inclusion state.
- Keep delivery changes, revisions, evidence and tasks attached to the same
  logical Repo. Keep machine IDs and exact commit hashes unchanged.
- Keep `(repo, machine) -> merger session` registrations, with repo changed
  to Repo ID. Verify each registered session is attached to the migrated lead
  Agent and retains the same Repo work context.
- Move session rows to their Agent ID and keep current Repo ID separately.
  Keep harness, machine, native session ID, status, profile, worker relation,
  conversation and session history unchanged.

### Reversibility and loss

Migrations are append-only: shipped migrations stay byte-for-byte in place,
and a new migration is appended. The migration lock test hashes each migration
in order and rejects changed, moved or removed entries
(`internal/gateway/migrations_lock_test.go:14-46`). The project identity
migration precedent performs its updates and schema version change in one
transaction, checks conflicting controls, and preserves history where it can
(`internal/gateway/project_identity_migration.go:13-16,88-129`).

The split migration should be atomic and retain a cutover map from each old
node/project string to both new IDs. Retain the old-to-new mapping, original
row keys and original edge payloads as downgrade data. A rollback can then
restore the single-node representation while no post-cutover edits have
created conflicts.

After new data is written, rollback can lose facts that one node cannot
represent: distinct Repo and Agent charters, multiple owners, agent settings,
and sessions with independent identity and work context. Keep a translation
map and backups. Refuse rollback on conflicting charter values or export the
divergent values; never choose a winner silently.

Existing open delivery changes must remain actionable during and after
cutover. The safest deployment sequence is a compatibility release that reads
both IDs and writes both references, then the transactional backfill, then a
release that writes only the new schema after all daemons are compatible. If
the migration runs with no mixed-version window, the one transaction can
rewrite all gateway references and retain legacy tables for downgrade.

## 5. What the owner sees

### Graph: configuration home

Keep Graph as the configuration home. Show linked Repo and Agent records with
ownership visible. The Repo card owns charter, checkouts, maturity, repo
settings, delivery pipeline and delivery owners. The Agent card owns its
charter, free-text role and owned repos. Edges identify each end's type.

Graph already serves as the owner-facing configuration map, shows node
locations and online sessions, and includes charter history
(`internal/gateway/graph_view.go:17-23,55-83,144-182`). The minimal UI change
is to show the owner link and split the existing card's fields into Repo and
Agent sections. Keep the same navigation, graph canvas and card workflow. Add
the repo's delivery settings to this existing configuration surface rather
than inventing a separate delivery area.

Repo registration remains machine-aware. A Repo card shows its known paths by
machine and lets the owner distinguish “the repo exists” from “this machine
has a registered checkout”. This is necessary because registrations are
already per machine (`internal/gateway/store.go:147-152`).

### Sessions

Keep existing Sessions rows and actions. Add Agent identity and Repo context
to details; lists may show them separately or together. Starting continues to
pick a machine and checkout and defaults the Agent to the Repo owner. A session
working in another repo keeps its own Agent identity.

Merger remains a session type/assignment associated with a Repo and machine,
not an Agent role. The existing Sessions table merger indication remains
valid; the Repo's delivery setup shows which session is selected. The current
owner table has a TYPE column (`internal/tui/view_main.go:963,1260-1295`), and
Graph shows delivery owners (`internal/tui/graph_tab.go:399-405,641-663`). Decision
0063 establishes that the mapping is per project and machine and only an
owner or approved proposal may change it (`docs/decisions/0063-merger-session.md`,
“Decision”).

### CTO tools and proposals

The CTO reads the Repo charter, Agent charter, ownership and typed graph
endpoints. Today `charter_set` accepts a node name
(`internal/gateway/cto_graph.go:26-33`). In the target model it must name the
target kind and stable ID or use separate tools such as `repo_charter_set` and
`agent_charter_set`.
The CTO must never infer which charter to change from a shared display name.

Proposals name the level and identity being changed. Examples are “set
delivery tests for Repo agent-net” and “change role for Agent lead-agent-net”.
An owner approval must show both IDs, display names and the effective change.
Existing rule approvals continue to be owner-gated. Agent role text alone
does not make an action minor or grant authority. This is consistent with the
settings design in which the CTO proposes a named-level change and the owner
approves rules (`docs/design/settings-layer.md`, sections “The owner's
decisions” and “How a recipe is changed”).

Do not redesign the proposal protocol in this step. Adapt its target identity
and display; leave general named-level proposal support to build step 6 in
`modularity.md`.

## 6. Open questions for the owner

1. **Can one Repo have multiple primary owners?** Recommendation: no for this
   step. Keep one `owns` relation per Repo; allow agents to own many repos or
   none. Revisit co-owners when a real repo needs them.
2. **How complete is an Agent charter?** Recommendation: define purpose,
   boundaries and notes independently, but migrate only existing actor
   information. Do not duplicate project purpose by default.
3. **How does an owner start a session as an Agent?** Recommendation: default
   to the Repo owner and allow explicit selection. Worker starts inherit the
   caller Agent; require an eligible session when live routing is needed.
4. **Can a session work in several repos at once?** Recommendation: one
   current Repo context per session. A run may later name a separate context.
5. **Which IDs are visible?** Recommendation: opaque stable IDs internally,
   display names plus type labels in cards and proposals. Keep existing repo
   names and paths.
6. **How long to retain downgrade support?** Recommendation: one release
   cycle; refuse automatic rollback when new facts cannot fit one node.
7. **What becomes of an architect node?** Recommendation: an Agent with role
   `architect`, retaining its charter and relations. Do not create a Team yet.

## 7. Build steps

Each step can ship alone. Each keeps the current behavior visible to the owner
and to sessions. Size is an estimate of implementation and tests, around
1,000 changed lines per step, not a hard budget. Do not combine all steps into
one release.

### Step 3a: introduce identities and compatibility reads

Add Repo and Agent protocol records, stable IDs, the `owns` relation, explicit
session-to-agent identity and Repo work context. Keep old graph component
reads and project-named API fields as compatibility views over one Repo and
its migrated lead Agent. No user workflow changes. Add migration and rollback
data. This foundation can ship without moving every consumer in the same
patch.

### Step 3b: move charter and settings ownership

Move repo-facing charter values and repo-scoped settings onto Repo IDs. Add
Agent charter and role storage. Translate charter histories without losing
actor, time or original value. Keep current setting keys, levels, defaults,
source reporting and effective results. Agent-level settings become usable
only for settings already declared at that level.

### Step 3c: move edges and delivery ownership

Use typed Repo and Agent endpoints. Convert `depends` to Repo-to-Repo,
governance to Agent-to-Agent, deployment and delivery ownership to
Agent-to-Repo. Keep special delivery owners and exact delivery evidence
unchanged. Preserve graph history and undo behavior through a translation
map. Update open changes and queued step tasks in the same migration.

### Step 3d: move registrations and session attachment

Make each registered checkout belong to a Repo ID, not a graph component
name. Attach each session to an Agent and separately record its Repo context.
Update project discovery, starts, worker briefs, merger validation, status and
graph context. Old start requests still select the Repo's primary Agent.

### Step 3e: owner view and remove compatibility assumptions

Show Repo and Agent in Graph and Sessions, expose ownership and role, and move
delivery configuration onto the Repo card. Update CTO reads and proposals to
name typed identities. Keep the same Graph canvas and navigation. Remove
legacy project-equals-node assumptions only after the compatibility period
and after all stored references have a typed replacement.

This design does not decide team membership, team ownership, a general
relationships module, ownership of repo paths, a task tracker or new delivery
steps.
