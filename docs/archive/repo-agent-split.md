# Repo and agent split

Date: 2026-11-05
Status: version 2 for owner review. Documentation only. Nothing is built.

This is build step 3 in [modularity.md](modularity.md). It follows the [settings layer](settings-layer.md): delivery
already resolves through that layer. The split separates the software being worked on from the lasting identity of the
agent that leads it (`internal/gateway/delivery_changes.go:137-153`). It does not add teams or redesign the
relationships module.

Every statement about the current implementation below names the code that supports it. The target model is a proposal,
not a claim about current code.

## The decision in one page

Today, `project`, `graph node` and `lead agent` are used as if they were one identity. A project name identifies the
graph component; its charter and settings describe the project, while sessions discovered in that project appear on the
same graph node and are treated as the agents for that work. The model cannot say that two lasting agents lead or act
for one repo without reusing the repo node as their identity.

After the split:

- A **Repo** identifies the software and its checkouts. It owns purpose, maturity, delivery and repo-level settings.
- An **Agent** identifies who acts. It owns a charter, a free-text role and zero or more repos. The first migration
  creates one agent per existing project and gives that agent ownership of that repo.
- A **session** belongs to one agent. Its repo context is separate and names the repo it is working in. That lets a
  session's identity and its current work context differ.
- Repo stores its one owning Agent directly; this is not a team mechanism or a general relationships module.
- Delivery stays repo-wide. Existing named delivery owners become Agent IDs; caller and merger keep their defined
  special meanings.
- Existing projects migrate to one Repo and one owning Agent with the same visible name and the same effective settings
  and behavior.

```text
Machine --checkout--> Repo <--owns-- Agent <--belongs to-- Session
                          ^                           |
                          +------ working context ----+
```

The session's working context can name a repo without implying ownership. An owner session or expert can therefore act
in a repo it does not own.

## 1. Today's model, checked against code

### Node kinds and identity

The protocol calls a graph node a component or architect. A `Node` has one name and kind, a `Charter`, node settings,
delegation, checkout locations, sessions and a session count. There is no independent Repo or Agent object in this
contract (`internal/protocol/graph.go:12-23,45-61`).

The gateway's `graphKind` returns `component` when the name exists in the project registry or is the `repo_project` of a
session. It returns `architect` for graph nodes created with that kind (`internal/gateway/store_graph.go:27-47, 66-91`).
Graph reads synthesize implicit component nodes from registered projects, sessions and edge endpoints
(`internal/gateway/store_graph.go:855-883`). So an observed or registered project is already a graph node even before an
owner makes it explicit.

The persistent schema makes the coupling visible. `projects` rows contain `(node_id, path, name, remote, default_branch,
machine metadata)`; the registered checkout is machine-scoped (`internal/gateway/store.go:147-152`). The graph schema
keys nodes by name, charter by `(node, attr)`, and edges by `(source, target, kind)`
(`internal/gateway/store.go:191-195`). Session rows carry `repo_project`, root, remote, branch and worktree, not an
agent identity (`internal/gateway/store.go:134-138`).

The map attaches registered locations to component nodes by project name and attaches online sessions to the node named
by their `Repo.Project` (up to five session rows per node); the CTO session is attached to the fixed CTO node
(`internal/gateway/graph_view.go:144-182`). The graph's component is therefore both repo-shaped and agent-shaped in the
owner's main configuration view.

### What is attached to the node today

| Current data | Current key and meaning |
|---|---|
| Charter | `graph_charter(node, attr)`. The protocol charter contains purpose, maturity, accepts, refuses, feature requests, consumers, escalation, needs and notes (`internal/protocol/graph.go:12-23`; `internal/gateway/store.go:192-194`). |
| Dependency and governance edges | `graph_edges(source, target, kind, mode, rule)`. The schema and allowed kinds/modes are `internal/gateway/store.go:193` and `internal/gateway/store_graph.go:732-744`. |
| Delivery assignments | More edge kinds in the same `graph_edges` table. The repo is the target; its source names a component/architect or a special owner (`internal/protocol/delivery_pipeline.go:54-120,142-157`; `internal/gateway/store_graph.go:622-655,722-785`). |
| Maturity defaults | Read from the component's charter, then used to resolve repo settings (`internal/gateway/store_graph.go:936-968`; `internal/gateway/store_graph.go:328-356`). |
| Settings | Canonical settings rows use `level='repo'` and the component name as `scope`. The level set already includes `agent`, but the graph does not yet provide agent identity (`internal/gateway/store.go:618-622`; `internal/gateway/store_graph.go:246-290`). |
| Delegation | Legacy project policy is keyed by `(machine node_id, project)`, then read into that component's public `Delegation` list (`internal/gateway/store.go:165-168`; `internal/gateway/store_graph.go:969-995`). |
| Locations | Registered machine paths become `GraphLocation` values on the component node (`internal/gateway/graph_view.go:144-164`). |
| Sessions | The graph view lists online sessions under the component named by their discovered repo (`internal/gateway/graph_view.go:165-182`). |
| Delivery changes and merger | Changes key on `project`; merger registrations key on `(project, machine)` and a session ID (`internal/gateway/store.go:657-669,759-765`). |

### Every current project-equals-node-to-agent assumption in the split seam

These are the direct code paths the first split must replace or adapt. This is the seam inventory for this design, not a
claim that every project-named field throughout the system should disappear. Machine references, file locations, worker
requests and git evidence may still need an explicit Repo ID.

1. **Node classification.** A project with a registered folder or session is `component`; a standalone named node is
   `architect` (`internal/gateway/store_graph.go:27-47,855-883`). This makes graph kind decide whether an identity is a
   repo or agent.
2. **Charter identity.** `SetCharter` accepts a `node`; storing its first attribute inserts a graph node of the inferred
   kind and stores the charter under that node (`internal/gateway/store_graph.go:177-243`). Graph reads put each charter
   attribute back on the same `Node` (`internal/gateway/store_graph.go:889-935`).
3. **Edges and endpoints.** Edge validation resolves each endpoint through `graphKind`; governance is
   architect-to-component and delivery assignments must target components (`internal/gateway/store_graph.go:722-785`).
   Edge keys store names rather than independent repo and agent IDs (`internal/gateway/store.go:193`).
4. **Per-repo settings identity.** Repo setting `scope` is the project name. The graph adapter reads those scopes into
   node settings and calculates maturity defaults from that node's charter
   (`internal/gateway/store_graph.go:246-290,936-968`). The modularity design says software-building settings live at
   repo level and behavior settings at agent level (`docs/design/modularity.md:107-114`).
5. **Session-to-node mapping.** The graph view picks `a.Repo.Project` as the node; `agentNode` uses `Repo.Project` then
   profile project for session behavior (`internal/gateway/graph_view.go:165-182`; `internal/gateway/check.go:82-105`).
   A project is acting as a proxy for which agent a session is.
6. **Charter-facing agent behavior.** `agent-net` reads charter through the public graph, whose rows are graph nodes and
   edges; the CTO graph mutators accept a `Node` field for `charter_set`
   (`internal/protocol/graph_public.go:8-14,39-59`; `internal/gateway/cto_graph.go:26-33`). Today a repo's charter is
   also the acting lead's instructions.
7. **Delivery change identity.** A change stores the project name. Its unique open-branch constraint and lookup are per
   project (`internal/gateway/store.go:653-669`). Settings and delivery routing use that project value as the repo key.
8. **Step owner identity.** The protocol says a named step owner is a graph node, either component or architect; the
   delivery edge connects its source to the project target (`internal/protocol/delivery_pipeline.go:112-120, 142-148`).
   Runtime tasks capture `owner_node` for a change and revision (`internal/gateway/store.go:697-709`).
9. **Merger identity.** The merger registration is `(project, node_id) -> session_id`; validation requires the session's
   project to equal the target project (`internal/gateway/merger.go:11-39`). It is already a session selected for the
   repo, not the identity of the lead agent.
10. **Registration identity.** A daemon adds paths under its machine node ID; records include local path, repo name,
    remote and default branch (`internal/gateway/store_projects.go:13-16,18-37,40-90`). The graph combines registrations
    across machines under the same project name (`internal/gateway/store_graph.go:855-883`).
11. **Trust, policy and agent data.** `projectOfSession` and `nodeName` map a session to `Repo.Project`; `RequestRule`
    and `RelationFor` match graph edges by those names (`internal/gateway/graphread.go:39-75,104-127`).
    `applyAgentCharters` loads the project node's charter and repo settings into the session's agent view
    (`internal/gateway/agent_charter.go:8-10, 40-75,85-106`). Project delegation is keyed by project name
    (`internal/gateway/store_policy.go:93-100,152-166`).
12. **Daemon and owner flows.** Group-send authorization checks a `governs` edge whose source is `Repo.Project`
    (`internal/daemon/messages_write.go:265-290`). Delivery submit and merge repo selection use `Repo.Project`
    (`internal/daemon/delivery_tools.go:123-145, 520-540`). Inbox items store the calling session's project as `node`
    (`internal/gateway/server.go:1723-1727`). The session form filters and starts by project name
    (`internal/tui/session_form.go:334-395`), and session rows show `PROJECT`
    (`internal/tui/view_main.go:960-969,1292-1305`).
13. **Other session joins.** Tool reads fall back from profile project to `Repo.Project`
    (`internal/gateway/toolread.go:211-215`). Project delegation resolves from `Repo.Project` or the profile and is
    keyed by machine and lower-case name (`internal/gateway/store_policy.go:14-24, 151-172`). Agent-skill selection and
    Graph selection also look up project names (`internal/tui/tools_agents.go:35-55`;
    `internal/tui/model.go:2087-2112`). Delivery pipeline settings resolve their Repo by the project name
    (`internal/gateway/delivery_changes.go:137-153`).

The sharp distinction is that a checkout is already a machine-specific record, but its repo name becomes the graph
component identity. The named graph component then doubles as the lead agent. Decision 0036 already names Project as the
home of a lead and explicitly says it is not a Graph node, while also calling Agent the node. This design brings the
implementation in line with that decision (`docs/decisions/0036-primitives.md:13-19`). This design uses Repo for the
primitive Decision 0036 calls Project; the stored key remains the existing project name.

## 2. Target data model

### Repo

A Repo is the durable identity of software across machines and paths. It has:

- stable repo ID and display name;
- zero or more checkout records `(machine, path, remote, default branch)`;
- purpose, accepts, refuses, feature requests, consumers, escalation and needs, as repo-level charter data;
- maturity and deploy rules;
- delivery pipeline and delivery step assignments;
- repo-level settings, including settings previously scoped to its component name;
- any repo-local delegation or policy that concerns how work in this software is done.

Repo ID must not be a machine path. A single Repo may have several paths, one per machine, and may have no current
checkout. Registration continues to say which machine has a folder suitable for starting or routing work. A checkout
does not create a second Repo.

### Agent

An Agent is a lasting actor identity. It has:

- stable agent ID and display name;
- a charter for the actor's own goals and boundaries;
- a free-text role such as “lead”, “reviewer”, “security specialist” or a phrase the owner chooses;
- ownership relations to zero or more repos;
- agent-level settings, with the levels and tightening rules already defined by the settings layer.

Role is descriptive data. It does not select a new graph kind, permission set or code path. The first implementation may
retain shipped role suggestions, but it stores text and accepts other text. The owner controls what an agent may do
through explicit authority and settings, not by parsing the role string.

An agent may own no repo. An owner or a cross-repo reviewer can be an agent without ownership. An agent may own multiple
repos without duplicating its lasting identity. Ownership does not itself grant merge, deploy, or other authority; those
continue to use explicit settings, approval and delivery policy.

### Stable keys, names and Graph references

During this build, the Repo ID and its first Agent ID both equal the existing project name. They are in separate typed
namespaces. An architect Agent keeps its old node name. Do not mint opaque IDs or rename project keys between steps.
This keeps current lookup keys valid while consumers move incrementally.

At the final Graph step, wire endpoints are typed references, for example `{"kind":"repo","id":"agent-net"}` and
`{"kind":"agent","id":"agent-net"}`. The same string can identify both records because the type is part of the key.
Graph reads and selection take both kind and ID; display-name-only filters are removed. Current `Edge` has string `From`
and `To` fields (`internal/protocol/graph.go:74-87`), and `GraphView` filters by one project name
(`internal/gateway/graph_view.go:31-53`), so the typed reference is a final-step wire change, not a mid-build rename.

The owner always sees the display name plus a `repo` or `agent` type label. Never expose raw IDs. Keep existing names
where possible; if display names collide, show the type label and allow the owner to rename the display name without
changing either key.

### Ownership

The `owns(agent, repo)` relationship is represented by nullable `repos.owner_agent_id`, a foreign key to `agents.id`.
This encodes exactly one owner per Repo; do not add a many-to-many table yet. An Agent may own zero or more Repos.
Ownership records responsibility but grants no merge, deploy or other authority.

A Repo with no owner remains valid. Its sessions do not get an implicit Agent; the owner must select one to start a
lasting agent session, and named delivery assignments wait for an explicit eligible Agent. The special `caller` route
still works. Deleting an Agent sets `owner_agent_id` to null. Keep a tombstone while sessions or run history refer to
it; do not transfer ownership automatically. The owner chooses a replacement.

### Sessions and their work context

A lasting session stores `agent_id` and has at most one current `repo_id` context. Starting in a Repo selects its owner
Agent by default; the owner may pick another Agent. A session may work in a Repo it does not own. Repo context selects
the software boundary and delivery setup; it never changes session identity.

The CTO is an Agent with no Repo owner link and retains its existing network scope. The current CTO graph node is
synthesized for display, not stored as a project record (`internal/gateway/graph_view.go:127-142`). Owner
pseudo-sessions are exempt from Agent attachment because they represent the human, not a persistent Agent. A worker
belongs to its Run, not to the Repo owner; the Run records `caller_agent_id`. Its Repo context comes from the Run. A
merger is a session assignment for a Repo and machine, not an Agent role; the session attaches to its selected Agent as
usual.

### One context resolver for sessions and runs

All policy and prompt construction goes through one resolver keyed by the session or Run. It returns two separately
labelled charters and effective settings, never one merged charter:

1. Resolve identity. A lasting session uses `session.agent_id`; a worker uses its Run identity and recorded
   `caller_agent_id`; CTO resolves to the CTO Agent; the owner pseudo-session has no Agent charter.
2. Resolve Repo. A lasting session uses its one `repo_id`; a worker uses the Run's Repo. An owner/CTO with no Repo gets
   no Repo charter or Repo settings.
3. Return the Repo charter first as the software's purpose, accepted work, limits and maturity. Return the Agent charter
   separately as that actor's own goals and limits. The Agent charter never copies the Repo purpose. For a worker,
   include the caller Agent charter as caller context and the Run brief as the worker's task; do not identify the worker
   as the caller. Trust checks use typed endpoints: `governs` uses Agent IDs, while software dependencies use Repo IDs.
   This preserves each edge's target meaning.
4. Resolve settings in this order: network, team when teams exist, Repo, Agent, then session or Run. Apply the settings
   layer's rule that lower levels only tighten rules; advice may be replaced. Repo maturity supplies Repo defaults.
   Agent settings do not replace Repo delivery settings. This is the settings-layer chain `network -> team -> repo ->
   agent -> session or run` (`docs/design/settings-layer.md:149-166`).

Today the daemon asks `policy.get` and attaches a graph charter and effective node settings to the answer
(`internal/daemon/policy.go:63-74`; `internal/gateway/agent_charter.go:40-75,85-106`). `charterContext` reads the
charter for the session's project node (`internal/daemon/graphread.go:78-85`). Delivery separately resolves pipeline
settings from the project node (`internal/gateway/delivery_changes.go:137-153`). These paths must call the same resolver
after the split. During steps 3a-3d, Repo and Agent IDs for an existing project both equal its old project name, so
current project-keyed lookups continue to find the Repo settings and charter.

## 3. What moves where

| Current concept | Target home | Notes |
|---|---|---|
| Repo purpose, maturity, accepts, refuses, feature requests, consumers, escalation, needs | Repo | These describe the software and its boundary. Preserve their current text and history. |
| Agent's own goals and conduct | Agent charter | New identity-specific charter. Do not copy repo purpose into every agent charter after the initial mapping. |
| `depends` | Repo-to-Repo when it describes software dependency | Keep the existing rule and meaning. Add explicit endpoint kinds in 3e; retain old string keys before then. |
| `accepts` and `refuses` | Repo charter text | They describe which requests fit this software. They are not the delivery `accepts` step assignment. |
| `governs` advisor/arbiter/owner | Agent-to-Agent | It describes how one actor relates to another. Existing architect nodes become agents. |
| `deploys` relation | Agent-to-Repo delivery assignment | The deployer acts; the repo is deployed. Preserve its present meaning. |
| Delivery step edges (`accepts`, `reviews`, `tests`, `approves`, `merges`, `releases`, `delivery_deploys`, `verifies`) | Agent-to-Repo assignment | Add typed endpoint refs in 3e. Keep source and target strings unchanged through 3a-3d. `accepts` remains distinct from charter accepts/refuses. |
| Step owners `caller`, `merger`, `owner`, `github`, `delegate`, `none` | Special delivery owner values | Retain their protocol meaning. A named node owner now resolves to an Agent ID. |
| Maturity, deploy rules, delivery pipeline | Repo | Software-building and shipping settings belong at repo level (`docs/design/modularity.md:107-114`); delivery is the gateway-resolved pipeline (`internal/protocol/delivery_pipeline.go:11-22`). |
| Settings about how one lasting actor behaves | Agent | Use `level='agent'` and Agent ID scope. No current agent identity should be guessed from the repo name after migration. |
| Repo construction and shipment behavior | Repo | Keep current settings at repo scope. Session/run values remain at their existing scopes. |
| Per-machine project registration | Repo checkout | Keep machine ID, path, remote, default branch, and registration metadata. The project-name key remains the Repo ID. |
| Session identity | Agent | Attach sessions explicitly to an Agent. |
| Session work context | Repo | Store separately from Agent identity. |

This split does not implement a general `relationships` module. It retains today's edge behavior and performs only the
endpoint translations needed for repo/agent identity. In particular it does not add reporting lines, path ownership, or
team membership.

## 4. Migrating live gateway data

### Required mapping

For each existing project component `P`, create Repo `P` and Agent `P` in separate typed namespaces, then set
`repos.owner_agent_id = P`. Both IDs are exactly the old project name. Existing architect nodes become Agents with their
existing names. No display name is used as an untyped foreign key.

Keep these key values unchanged in every step: `projects.name`, `sessions.repo_project`, `delivery_changes.project`,
`project_mergers.project`, `graph_edges.source` and `graph_edges.target`. `delivery_changes.project` is the Repo ID even
though its historical column is named `project`. Agent ID and Repo ID can carry the same string because typed tables and
final typed graph references distinguish them. This preserves pipeline lookups, step backfill, project merger
validation, and the frozen merger trigger predicates.

`OpenStore` runs delivery step backfill after schema migrations; backfill resolves each active change's pipeline from
its `project` key (`internal/gateway/store.go:793-804`; `internal/gateway/delivery_tasks.go:30-70`). It must continue to
use the same project-name Repo key, including for merged changes. Existing merger triggers match `OLD.project` against
delivery rows and graph edge targets (`internal/gateway/merger.go:145-188`), so their project key and predicates stay
intact. If graph edges gain typed columns, append a new migration that refreshes their endpoint types in each
trigger-written row; do not edit the shipped migration text.

Standalone architect nodes become Agent records. Their name, role default “architect”, charter and outgoing or incoming
actor relations are preserved. If an old edge targets a component because it describes software dependency, map the
endpoint to Repo. If it governs an architect/component relation, map the architect endpoint to Agent and the component
endpoint according to the table above. Delivery assignment edges always target Repo and their named source becomes
Agent.

### Data to preserve and classify

- Copy repo-facing charter fields from the component to Repo. The migration should preserve immutable history or add a
  translation table from old graph history IDs to the new Repo/Agent history. Do not silently concatenate distinct
  values.
- Keep repo settings and setting history at `level='repo'`; the old scope is already the Repo ID. Settings levels
  include `agent` (`internal/gateway/store.go:618-631`). Populate the Agent charter separately; existing project
  charters remain Repo charters.
- Preserve legacy string edges through 3a-3d. In the final migration, classify both endpoints by edge meaning and add
  typed refs. A `depends` endpoint is a Repo; a `governs` endpoint is an Agent; delivery targets are Repos and named
  delivery sources are Agents. Preserve history and `mode`/`rule`.
- Keep checkout paths and project names unchanged; the names become Repo IDs. Keep machine ID, remote, branch, registrar
  and timestamps.
- Keep delivery revisions, evidence and tasks on their existing project key; keep machine IDs and commit hashes
  unchanged. Preserve merger records on `(Repo ID, machine) -> session`.
- Add session `agent_id` and `repo_id` fields without changing their existing project-name values. Keep harness,
  machine, native ID, status, profile, worker relation, conversation and session history.

The split migration must also classify every existing name-bearing table, including all of these:

| Table | Migration rule |
|---|---|
| `project_policies` | Repo-scoped delegation policy; retain machine ID and project-name Repo key (`internal/gateway/store.go:165-168`). |
| `inbox_items.node` | Add target kind; classify the referenced Repo or Agent from the originating action, preserving session and answer (`internal/gateway/store.go:215-219`). |
| `learned_suggestions.node`, `learned_views.node` | Repo for project charter suggestions; Agent for architect charter suggestions. Preserve suggestion/history IDs; type edge-suggestion endpoints in `phrase` at 3e (`internal/gateway/store.go:322-341`). |
| `learned_message_flow.source/target`, `charter_signals.source/target` | These describe message participants; map to Agent refs, retaining CTO/owner special identities (`internal/gateway/store.go:319-332`). |
| `findings.subject`, `playbook.subject` | Respect `subject_kind`; translate only project/agent subjects, preserve model subjects and reporter/session values (`internal/gateway/store.go:246-259`). |
| `graph_history.node`, `setting_history.scope`, `delivery_step_tasks.owner_node` | Add/derive the target kind while keeping the key string; preserve history, revision and special delivery-owner values (`internal/gateway/store.go:623-629,699-709`; graph history schema: `internal/gateway/store.go:191-195`). |

Before cutover, validate every row has one typed destination. If a legacy row cannot be classified without guessing,
stop the migration and report its key for owner resolution. Keep message IDs, session IDs and machine IDs in their own
namespaces; they are not Repo or Agent IDs.

### Reversibility and loss

Schema migrations are append-only; the lock rejects changing, moving or removing a shipped migration
(`internal/gateway/migrations_lock_test.go:14-46`). Build additive schema first. The final data cutover is one
transaction after the daemon-version gate below. Never edit frozen migration text; append a new migration for schema and
trigger changes.

Rollback is deliberately brutal. There is no downgrade migration, retained downgrade data or dual-write for rollback.
Take a gateway backup immediately before the final migration. To roll back, stop the gateway and restore that backup.
All gateway writes after the backup are lost. Schedule the cutover so the owner accepts that loss window; if the backup
is unavailable or invalid, do not start the migration.

The existing project-identity migration is a precedent for transactional backfills and conflict checks, not downgrade
support (`internal/gateway/project_identity_migration.go:13-16,88-129`).

### Daemon-version gate

The gateway records each daemon's version from `hello` in `nodes.version` (`internal/gateway/server.go:756-763`;
`internal/gateway/nodes.go:13-19`). The final cutover is blocked until every enrolled node has reported the minimum
release that supports typed Repo/Agent references. A blank, unknown or older version blocks it; an offline node still
counts. The owner must upgrade or remove that node before retrying. Until then the gateway stays on the legacy schema
and all build steps retain the project-name protocol.

After cutover, reject `hello` and state-changing old-protocol frames from any node below the minimum with an `upgrade
required` response. Do not accept its `session.sync`, policy, graph, delivery submit or step report; mark it unavailable
for routing and show the required upgrade in the owner UI. The compatibility test for old daemons
(`internal/daemon/compat_test.go:18-80`) must cover both the pre-gate success case and post-cutover refusal.

## 5. What the owner sees

### Graph: configuration home

Keep Graph as the configuration home. Show linked Repo and Agent records with ownership visible. The Repo card owns
charter, checkouts, maturity, repo settings, delivery pipeline and delivery owners. The Agent card owns its charter,
free-text role and owned repos. Edges identify each end's type.

Graph already serves as the owner-facing configuration map, shows node locations and online sessions, and includes
charter history (`internal/gateway/graph_view.go:17-23,55-83,144-182`). The minimal UI change is to show the owner link
and split the existing card's fields into Repo and Agent sections. Keep the same navigation, graph canvas and card
workflow. Add the repo's delivery settings to this existing configuration surface rather than inventing a separate
delivery area. Public Graph reads page their results (`internal/protocol/graph_public.go:23-30,62-70`); the CTO view
must page typed Repo and Agent refs and never conflate same-name records. The owner can create a Repo without a
registered checkout and can archive a Repo after open delivery changes and merger routing are cleared. Existing
`node_add` and `node_remove` only create or remove architects (`internal/gateway/cto_graph.go:34-44`); replace them with
typed Repo and Agent actions. Keep the Graph canvas and give Repo and Agent distinct labels and styles. `GraphFor` and
owner graph selection must filter by `(kind, id)`, not a shared name (`internal/gateway/graphread.go:15-36`;
`internal/gateway/graph_view.go:31-53`).

Repo registration remains machine-aware. A Repo card shows its known paths by machine and lets the owner distinguish
“the repo exists” from “this machine has a registered checkout”. This is necessary because registrations are already per
machine (`internal/gateway/store.go:147-152`).

### Sessions

Keep existing Sessions rows and actions. Rename `PROJECT` to `REPO` and add Agent identity; show both in details. The
start form picks a Repo and defaults to its owner Agent, which the owner may change. A session working in another Repo
keeps its own Agent identity.

Merger remains a session type/assignment associated with a Repo and machine, not an Agent role. The existing Sessions
table merger indication remains valid; the Repo's delivery setup shows which session is selected. The current owner
table has a TYPE column (`internal/tui/view_main.go:963,1260-1295`), and Graph shows delivery owners
(`internal/tui/graph_tab.go:399-405,641-663`). Decision 0063 establishes that the mapping is per project and machine and
only an owner or approved proposal may change it (`docs/decisions/0063-merger-session.md`, “Decision”).

### CTO tools and proposals

The CTO reads the Repo charter, Agent charter, ownership and typed graph endpoints. Today `charter_set` accepts a node
name (`internal/gateway/cto_graph.go:26-33`). In the target model it must name the target kind and stable ID or use
separate tools such as `repo_charter_set` and `agent_charter_set`. The CTO must never infer which charter to change from
a shared display name.

Proposals name the level and typed identity being changed. Examples are “set delivery tests for Repo agent-net” and
“change role for Agent lead-agent-net”. The owner sees names, type labels and the effective change, never raw IDs.
Existing rule approvals continue to be owner-gated. Agent role text alone does not make an action minor or grant
authority. This is consistent with the settings design in which the CTO proposes a named-level change and the owner
approves rules (`docs/design/modularity.md:178`).

Do not redesign the proposal protocol in this step. Adapt its target identity and display; leave general named-level
proposal support to build step 6 in `modularity.md`.

## 6. Owner decisions (2026-11-05)

1. **One owner per Repo.** Store it in nullable `repos.owner_agent_id`. A Repo may have no owner; starts then require
   explicit Agent selection. If its owner is deleted, clear the field and keep a tombstone for referenced sessions/runs.
   No automatic transfer.
2. **Agent charter.** It holds the Agent's own goals and limits. It never copies the Repo's purpose.
3. **Session start.** Default to the Repo owner Agent; the owner may select another Agent.
4. **One context.** A session has one Repo context at a time.
5. **Display.** Show names with a `repo` or `agent` type label. Never show raw IDs.
6. **Rollback.** Rollback means stopping the gateway and restoring the backup taken immediately before migration. There
   is no downgrade machinery or rollback dual-write; post-backup writes are lost.
7. **Architect.** Existing architect nodes become Agents with role `architect`. Do not create Teams in this step.
8. **Terminology.** Decision 0036 calls a lead's home Project; this design calls that primitive Repo. The old
   project-name key stays unchanged.

## 7. Build steps

Each step ships alone and preserves today's effective behavior. Each is about 1,000 changed lines including tests. Steps
3a-3d retain the old project-name keys and string protocol, so the current Graph and sessions continue to work.

### Step 3a: additive Repo and Agent records

Add `repos`, `agents` and nullable `repos.owner_agent_id`; backfill IDs using existing project names in separate typed
namespaces. The Repo ID and first Agent ID are equal to the old project name. Convert each architect to an Agent. Keep
old graph component reads as a compatibility projection. No changes to `projects.name`, `sessions.repo_project`,
`graph_edges` or delivery keys.

### Step 3b: charter and context resolver

Move existing component charter to Repo charter. Start the lead Agent charter empty unless there is explicit actor-only
data; do not copy Repo purpose. Add the resolver above and send both labelled contexts to sessions. Keep Repo settings
at the same `repo` level and same project-name scope. Preserve current effective rules, defaults and sources.

### Step 3c: typed edge metadata, same keys

Add endpoint-kind fields to graph edges and classify them. Keep the existing `source` and `target` strings and every
graph read's legacy projection; old `nodeName`, relation and group-send paths therefore keep matching. Continue to
resolve delivery by Repo name. Add an appended migration that replaces the frozen merger triggers so trigger-written
delivery edges include endpoint kinds. Verify startup backfill for both `open` and `merged` changes, including a queued
node-owned step and a registered merger.

### Step 3d: explicit session and Run identity

Attach lasting sessions to `agent_id`; keep `repo_id` as their one Repo context, with both keys still project names. The
start form defaults to the owner Agent and lets the owner choose another. Update trust, charter, policy, Inbox, worker,
delivery and merger paths to call the resolver. A worker session belongs to its Run; store `caller_agent_id` on that
Run. Preserve all old string protocol fields as adapters. No change to effective routing or authority.

### Step 3e: typed Graph and owner surfaces

After the version gate, change Graph wire endpoints and filters to typed `(kind, id)` refs; the key strings themselves
remain the stable Repo/Agent IDs. Update Graph and CTO reads/writes, proposals, session selection, public Graph paging
and TUI styles. Show `repo` and `agent` labels with names only. Add typed Repo create/archive and Agent create/tombstone
actions; a Repo may be created without a checkout. Keep delivery configuration on the Repo card. Drop legacy API
adapters only after every enrolled node passes the gate.

This design does not decide team membership, team ownership, a general relationships module, ownership of repo paths, a
task tracker or new delivery steps.

## Changes from version 1

- **B1:** Defined the single session/Run resolver, charter order, settings order, worker caller context and Repo/Agent
  behavior.
- **B2:** Kept old project-name keys and string protocol through 3a-3d; typed edge metadata does not change the trust
  lookup key mid-build.
- **B3:** Kept `delivery_changes.project`, edge strings, merger project keys and backfill keys stable; covered open and
  merged backfill and trigger edits.
- **B4:** Gated final cutover on every enrolled node's stored `hello` version; old daemons are refused after cutover and
  shown as unavailable.
- **S1:** Added gateway trust/policy, daemon message/delivery, Inbox, TUI selection, tools, delegation and pipeline
  paths to the seam list.
- **S2:** Defined the CTO Agent, owner-session exemption, Run-owned workers, caller Agent and merger session attachment.
- **S3:** Defined type-specific name keys, typed Graph refs and `(kind, id)` filtering; owner surfaces show names and
  type labels, never raw IDs.
- **S4:** Added all named reference tables and explicit pre-migration backup restore rollback. No downgrade data or
  rollback dual-write remains.
- **S5:** Encoded one owner in `repos.owner_agent_id`; defined null and deletion behavior. Split session attachment from
  edge work to keep keys stable.
- **S6:** Added typed create/archive actions, typed Graph cards/paging, CTO targets and Repo/Agent labels in Sessions
  and start selection.
- **M1:** Stated that this design renames Decision 0036's Project primitive to Repo while preserving its existing
  project-name key.
- **M2:** Stated that 3a-3d preserve behavior and 3e changes surfaces without changing effective routing or authority.
- **Citation fixes:** Replaced the mislabeled section links with `docs/design/modularity.md:107-114,178` and
  `docs/design/settings-layer.md:149-166`; cited edge kinds at `internal/gateway/store_graph.go:732-744` and
  schema at `internal/gateway/store.go:193`; corrected graph membership to online sessions, up to five per node
  (`internal/gateway/graph_view.go:150,165-182`).
