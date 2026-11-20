# Folding graph_nodes away (schema reduction, part 10)

Date: 2026-11-20. Design note that [decision 0179](../decisions/0179-gateway-schema-reduction.md) asks for before part 10 is built.

File and line numbers are from part 3's branch `agent-net/w_83b39aeda2f5388f` at `dd902815b8`. They move when parts 3 and 4 merge. All paths are under `internal/gateway/`.

## Recommendation

Drop `graph_nodes` and add no column. Both things it holds are already stored elsewhere:

- **"This component is explicit"** becomes "this repo has a charter value or an edge".
- **"This name is a legacy architect"** becomes `agents.role='architect' AND tombstone=0`.

Part 10 needs part 3 and part 4 merged first. Part 4 puts repo charters in `charter_values`, which the first rule reads.

The architect kind stays. Removing it is a separate change (see below).

## What graph_nodes holds that repos does not

`graph_nodes(name, kind, created_at)`. Live rows, read-only on 2026-11-20:

| Kind | Rows | Meaning |
|---|---|---|
| `component` | 14 | A node the graph shows even with no project folder. A row is added when a charter value is first set (`store_graph.go:307`). |
| `architect` | 0 | A named agent with no project, from before the typed graph. |

Measured on the live store:

- 14 of 14 component rows have a charter value, and no charter belongs to a node outside the table. So the explicit set equals the set of nodes with a charter.
- 14 of 14 have a `repos` row. `repos.created_at` replaces `created_at`.
- 1 of the 14 has no project and no live session. It stays visible through its charter.
- No agent has the role `architect`. `graph_history` has no row of type `node`.

One behaviour changes: a repo whose whole charter is cleared, with no edge and no project folder, stops showing in the graph. Today its row keeps it. An earlier migration already removes such rows as junk (`store.go:301`), and `graph_view.go:136` already treats a charter value as explicit, so this follows the existing intent.

The other option is a `repos.declared` column. It keeps that one case, and adds a flag that must be kept in step with charters and edges. That is a decision in two places (0163), so it is not recommended.

## The six triggers

| Trigger | On | What it guards | After the fold |
|---|---|---|---|
| `repo_agent_project_insert` | `projects` | A new project name gets an agent and a repo, unless a non-component node (an architect) already has that name | Same trigger, guard becomes `NOT EXISTS (SELECT 1 FROM agents WHERE id=NEW.name AND role='architect' AND tombstone=0)` |
| `repo_agent_project_update` | `projects` | Same, on a rename | Same change |
| `repo_agent_session_insert` | `sessions` | Same, for a new `repo_project` | Same change |
| `repo_agent_session_update` | `sessions` | Same, on a change of `repo_project` | Same change |
| `repo_agent_architect_insert` | `graph_nodes` | Adding an architect creates or revives its agent with role `architect` | Dropped. `AddArchitect` writes the agent row itself, and so does undo |
| `repo_agent_architect_delete` | `graph_nodes` | Removing an architect clears the repos it owns and tombstones its agent | Dropped. `RemoveArchitect` does both, and so does undo. Part 3's `repo_lead_tombstone` still refuses while the agent leads a repo |

Part 3 already rewrites the first four (`repo_fold_migration.go:46-61`). Part 10 rewrites them once more.

## Every reader and writer in production code

Runtime:

| Site | What it does | Becomes |
|---|---|---|
| `store_graph.go:34` `graphKind` | Kind of a name | `architect` from `agents.role`, else the existing project and session check |
| `store_graph.go:76`, `:246`, `:486` | Is this name an architect? Routes a charter write, read or history to the agent | One helper on `agents.role` |
| `store_graph.go:110`, `:201` | `AddArchitect`, `RemoveArchitect`, only when the typed graph is off | Write `agents` and `repos` directly |
| `store_graph.go:307` | Marks a component explicit on a charter write | Removed |
| `store_graph.go:1009`, `:1024`, `:1031` | Undo of a node add or remove | Same as add and remove |
| `store_graph.go:1434-1438` `graphNodes` | Lists explicit and implicit nodes | Explicit rows from `charter_values` (level `repo`) and agents with role `architect`; `created_at` from `repos` or `agents` |
| `store_graph.go:1482` | Splits repo and architect charters | Uses the helper |
| `graph_lifecycle.go:53`, `:61` | Keeps a new repo id apart from an architect of the same name | Reads `agents` |
| `graph_view.go:88` | History of architect charters | Uses the helper |
| `graph_view.go:136` | Names visible to a session | Drop the first `SELECT`; the charter and edge parts already cover it |
| `learned.go:197` | Known graph names | Add charter scopes and architect agents |
| `settings.go:629` | Repos for a settings check | Charter scopes of level `repo` |
| `worker_sandbox_settings.go:28-32` | Project names; also called by shipped migration steps | See risks |
| `graph_cutover_auto.go:281`, `:285` | The typed-graph cutover, on a store not cut over yet | Reads `agents.role` |

Migration only, left as they are: the migration list in `store.go` (lines 226, 301-310, 809-840, 950-962, 1595-1611), the runner at `store.go:2066` and `:3350`, `project_identity_migration.go:63-144`, `repo_fold_migration.go`.

Topic lists: `graphNodesTables` (`store_graph.go:1400`) names `graph_nodes`. It feeds `graphTables`, `visibleTables`, `delegationTables` and the Graph topic.

## Is the architect kind still used?

Yes, as a legacy kind.

- With the typed graph on, which is the case on the live store, `node_add` and `node_remove` already run `agent.create` and `agent.tombstone` (`store_graph.go:93`, `:127`). They write `graph_nodes` only on a store that is not cut over.
- The TUI still draws the kind in five places: `tui/graph_labels.go:35`, `tui/graphview/scene.go:167`, `tui/map.go:277`, `tui/map_card.go:610`, `tui/session_form.go:379`. Also `setup/onboarding.go:336` and `charter_levels.go:178`.

Part 10 keeps `Node.Kind == "architect"` on the wire, computed from `agents.role`. Nothing changes for the TUI, the CTO or the docs.

Removing the kind later would mean: the five TUI sites show an agent like any other; `node_add` and `node_remove` become plain names for `agent_create` and `agent_tombstone`, or go away from `mcpserver/cto_parity.go:39-40`, `cto_ops.go:184` and the never list; and `docs/reference.md` lines 548, 655 and 950 change. That changes what the owner sees, so it is not part of a schema step.

## Migration steps, in order

One new migration at the end of the list, every statement safe to replay:

1. If `graph_nodes` exists: for each architect row, make sure its agent has role `architect` and `tombstone=0` (the trigger did this; the step repeats it). Count component rows with no charter value and no edge, and write the count to the event log. They lose only the explicit flag.
2. `DROP TRIGGER IF EXISTS` and recreate the four project and session triggers with the new guard.
3. `DROP TRIGGER IF EXISTS repo_agent_architect_insert` and `repo_agent_architect_delete`.
4. `DROP TABLE IF EXISTS graph_nodes`.

In the same change: the readers above, `graphNodesTables`, and the tests.

## Risks

**Shipped steps call live helpers on old stores.** `workerProjectNamesTx` runs inside `cutoverWorkerSandboxTx` (`store_policy.go:53`, `:141`), and seed steps reach `graphKind` through `setCharterTx`. On a store that is upgrading from an old version, these run before the fold, when `graph_nodes` holds the real data and `charter_values` has no repo level yet.

Part 3 answers this with a schema probe (`repoSchemaOf`, `org_read.go:38`): helpers ask which layout the store has. Part 4 answers a different half with `ensureOldCharterTablesTx`: before a replay, the old tables are made again so old SQL text still runs.

Pick part 3's way, with one rule: **if the table `graph_nodes` exists, the store has the old layout.** The few helpers that shipped steps call keep their old query behind that one test. Runtime readers do not test; they use the new form.

**Rewind replay.** A folded store has no `graph_nodes`, and shipped steps name it in their SQL (`DELETE FROM graph_nodes`, the `split_components` inserts, `CREATE TRIGGER ... ON graph_nodes`). Before a replay, make the table again and fill it from the new sources: one component row per repo charter scope, one architect row per agent with that role. Then the rule above holds during the replay and the old steps see true data, not an empty table. The fold step at the end drops the table and the two triggers again. Part 4 remakes its tables empty; here that would make `cutoverWorkerSandboxTx` see fewer project names, so fill it.

**Topic lists.** Replace `graph_nodes` in `graphNodesTables` with what the new queries read: `agents.role`, `agents.tombstone` and `repos.created_at` as column keys. `charter_values` is already there through `charterTables`. `TestCachedReadsDeclareEveryTableTheyRead` checks this.

**An agent created with role `architect` after the cutover** is treated as a legacy architect. For charter routing that is correct. Confirm what `agent.create` accepts as a role (`graph_lifecycle.go:40-47`) and add a test for the name guard.

**Wire results.** `protocol.Node` keeps `Name`, `Kind` and `CreatedAt`. An explicit component with no `repos` row gets `CreatedAt` 0, which implicit nodes already have.

**No way back without the backup**, as for every part.

## Effect on the counts

One table and three columns go. No column is added.
