# Repo and agent split design: independent review

Date: 2026-11-05. Reviewer: an independent model, read only. Reviewed
`docs/design/repo-agent-split.md` on branch `agent-net/w_1d8300272f2481e2`. I
checked every cited line against the code and grepped `internal/gateway`,
`internal/daemon`, `internal/mcpserver` and `internal/tui` for `Repo.Project`,
project names used as node names, `graph_charter`, `graph_edges`, `merger` and
delivery `owner_node`. I did not edit the design and did not run the full suite.

## Citation check

Most cited facts are correct: schema and protocol ranges, graph kinds, implicit
nodes, settings scope, delegation, merger validation, the migration lock and the
TUI lines match. Decisions 0036 and 0063 are read correctly. Four citations are
wrong or loose:

- `repo-agent-split.md:247,417` cites "Where a setting lives" and "How a recipe
  is changed" in `settings-layer.md`; both are in `modularity.md:107,178`.
- `repo-agent-split.md:86` cites `protocol/graph.go:74-87` for allowed edge
  kinds; that range is the `Edge` struct. The set is in `store_graph.go:733`.
- `repo-agent-split.md:75` says "every session"; the graph attaches online
  sessions only, five per node (`graph_view.go:150,175`).

## Blockers

### B1. The design never says which charter and settings a running session reads

Evidence: design `225-227,248,276-281,296-301,313-320`;
`internal/daemon/policy.go:65-73`; `internal/daemon/graphread.go:78-102`;
`internal/gateway/agent_charter.go:46-76`;
`internal/gateway/graphread.go:129-136,145-167`;
`internal/gateway/delivery_changes.go:139-153`;
`internal/gateway/store_graph.go:936-968`.

Today a session reads the charter and settings of its `Repo.Project` node. If
3b moves settings to an opaque Repo ID while the session reports a display name,
`chartedProject` returns empty, `applyAgentCharters` gives it an empty charter,
and `deliveryPipeline` falls back to network values.
The claim "same effective settings and behavior" cannot hold. The design gives
the Agent a charter but never says which charter the session reads. Define one
resolver and its order. Keep the Repo name as a compatibility key until 3e.

### B2. Step 3c cannot ship alone before step 3d

Evidence: design `469-482`; `internal/gateway/graphread.go:39-56,61-74,104-127`;
`internal/daemon/messages_write.go:269-291`; `internal/gateway/check.go:82-105`.

Step 3c converts governance and delivery edges to Agent and Repo IDs, but
sessions join Agents only in 3d, and trust and request rules still map a session
to `Repo.Project`. Between the releases `nodeName` returns a repo name, no edge
has that source, `RelationFor` and `RequestRule` return empty or `normal`, and
`maySendGroup` stops granting advisor sends. The design allows separate IDs even
when names match, so this is likely. Make the first Agent IDs equal the old
project names, or move session attachment and routing into 3c.

### B3. Delivery backfill and the frozen merger triggers key on project names

Evidence: design `261-274,326-333,357-362,469-475`;
`internal/gateway/delivery_changes.go:97-153`;
`internal/gateway/delivery_tasks.go:30-70`; `internal/gateway/store.go:801-804`;
`internal/gateway/merger.go:148-190`.

`backfillDeliveryTasks` runs after every migration, groups active changes by
`c.Project` and calls `deliveryPipeline(c.Project)`, which resolves the repo by
exact node name. It includes `merged` changes, not only `open`. If 3c writes
Repo IDs to `delivery_changes.project` or `graph_edges.target`, the next restart
re-queues active changes with the network pipeline. The shipped triggers
`merger_removed` and `merger_replaced` also join on `OLD.project` and
`graph_edges.target=OLD.project`, so they stop matching. The design does not
mention these paths, the `merged` state or the trigger bodies. Name the field
that stays a name and the field that becomes an ID, and migrate the triggers.
Test: open a change, queue a node-owned step, register a merger, run the
migration and `OpenStore`, then assert the route.

### B4. Mixed daemon versions are not detectable

Evidence: design `357-362`; `internal/daemon/compat_test.go:18-80`;
`internal/gateway/server.go:761`; `internal/gateway/nodes.go:18`;
`internal/daemon/link.go:317-381`.

The gateway accepts an old daemon and does not gate on version. Old daemons
keep sending project names in `session.sync`, `policy.get`, `graph.read`,
`change.submit` and step reports. The design says the last release writes the
new schema "after all daemons are compatible" but not how the gateway knows;
`nodes.version` from `hello` is already stored. Specify the check and the
mixed-fleet behaviour, and translate both shapes until every node is new. Test:
extend `TestOldDaemonAgainstNewGateway` to submit after cutover.

## Should fix

### S1. The seam list misses the daemon and the trust path

The ten seams are mostly gateway. These also treat a project as a node:

- `internal/gateway/graphread.go:39-56,104-127`: `projectOfSession` and
  `nodeName` map a session to a project node, and `RequestRule` matches edges by
  that name. This is the delivery trust path, more important than
  `check.go:82-105`, which the design cites.
- `internal/gateway/delivery_changes.go:97-135`, `toolread.go:212-215`,
  `store_policy.go:16-24`, `agent_charter.go:40-76`: step eligibility, pipeline
  lookup, tools, delegation and charter use the project name.
- `internal/daemon/messages_write.go:269-291,307-322`,
  `internal/daemon/delivery_tools.go:128-136,519-530`,
  `internal/gateway/server.go:1725-1727`, `internal/tui/session_form.go:283-390`,
  `internal/tui/tools_agents.go:35-54`, `model.go:2090-2116`: group sends,
  delivery tools, Inbox nodes, starts and selections use `Repo.Project`.

### S2. CTO, owner and worker session attachment is not defined

Evidence: design `220-233,287-294,432-434`;
`internal/gateway/graph_view.go:127-142,165-169`;
`internal/protocol/operator.go:271-283`.

The CTO node is synthesized, not stored, and the owner pseudo-session has no
project. The migration creates Agents only for project components and explicit
architect nodes, so "each non-worker, lasting session attaches to an agent_id"
cannot be met for the CTO and owner. Workers are excluded, but the mapping still
assigns their sessions to the repo Agent, which contradicts "associated with the
run". Merge works because `SetMerger` requires a session of the project on the
machine (`merger.go:31-38`). Create a CTO Agent that owns nothing, decide
whether owner sessions are exempt, and state the worker rule.

### S3. Typed edge endpoints have no wire format, and duplicate names are ambiguous

Evidence: design `371-372,404-409,454-475`; `internal/protocol/graph.go:74-87`;
`internal/gateway/store.go:191-193`; `graph_view.go:35-37,44-45`;
`graphread.go:17-37`.

`Edge` has only `From`, `To` and `Kind` strings. The design says to use typed
endpoints and allows a Repo and its Agent to share a display name, but the
filtered reads select by one name, so `GraphFor`, `graphView` and the TUI cannot
tell them apart. Define the ID format and namespacing, or add endpoint kinds,
and make the read take a kind.

### S4. The migration is incomplete and not reversible

Evidence: design `311-362`;
`internal/gateway/project_identity_migration.go:147-159`;
`internal/gateway/store.go:166-168,216,320-341,618-633,692,747,760,819-1245`.

The project identity precedent updates `project_policies`, `inbox_items.node`,
`learned_suggestions`, `learned_views`, `learned_message_flow`,
`charter_signals`, `findings` and `playbook`. The split list covers
registrations, delivery, mergers and sessions but not these. Otherwise those
reads point at a missing node. `project_policies` is cited as a seam but never
mapped.

`schema_version` is forward only. The old binary will not run the new migration
and does not know the new tables. "Retain legacy tables" works only if every
new write also updates them, which the design requires only in the optional
compatibility release. The append-only `setting_history` and `graph_history`
rows written after cutover refer to IDs the old binary cannot resolve. Say that
rollback means restoring a backup, or specify dual-write and a tested downgrade.

### S5. "One primary owner" is not encoded, and the first step can be smaller

Evidence: design `205-216`; `internal/gateway/store_graph.go:774-784`.

`owns(agent_id, repo_id)` as written allows many owners. "A repo can have one
primary owner" is policy text, not a constraint. The code already rejects a
second delivery owner for one step (`setEdgeTx`), so one owner is the right
first step. Use a single `repos.owner_agent_id` key, or add `UNIQUE(repo_id)` to
`owns` and a `primary` flag for later. Say what happens when a repo has no
owner or the owner is deleted.

The model is not too complex, but one step is too broad. 3c retypes every edge
while 3d attaches sessions; keeping old names until 3e removes most of B2 and B3.

### S6. Owner-visible changes the design does not mention

Evidence: `internal/gateway/graph_view.go:35-37,44-45`;
`internal/tui/map_card.go:430-438`; `internal/tui/graph_tab.go:446`;
`internal/tui/model.go:2090-2116`; `internal/protocol/graph_public.go:17-21`;
`internal/gateway/cto_graph.go:34-44`.

- The Graph shows two nodes per project with the same name, and selection,
  filtering and `GraphFor` are by name. The card renders `Kind == "architect"`
  and prints the kind text; new kinds need a label and style.
- `node_add` and `node_remove` still create and remove architects. The design
  does not say how the owner creates a Repo with no checkout or deletes one.
- `agent_graph_read` pages at 20 or 50 and now lists both records, so the CTO
  can read the wrong charter until 3e. Sessions still show a `PROJECT` column
  and the start form filters by project, with no stated replacement.

## Minor

- M1. The design calls the home of a lead a "Repo"; decision 0036 calls it a
  "Project". State the rename, or keep 0036's term. One name per concept.
- M2. Step 3e cannot keep current behavior visible because it removes the
  compatibility assumptions; say that 3a to 3d keep behaviour.

## Which step is riskiest

Step 3c. It rewrites the edges that live delivery, trust and graph undo read,
while sessions still identify by repo name until 3d, and the merger triggers
are frozen in a shipped migration. The restart test in B3 proves it, plus the
mixed-version submit in B4. Step 3a is additive and safe. Step 3b is safe only
with B1. Step 3d is the second risk because starts, merger validation and
delivery tools compare project names.

**Verdict.** Build after fixes. The Repo and Agent split is the right shape, and
one primary owner is the right first step because it preserves today's one-lead
behaviour. The design is not yet safe on a live gateway: fix B1 to B4 before
building 3a. If the owner wants a smaller first release, build 3a and 3b with
stable names and keep 3c for later. A redesign is not needed.
