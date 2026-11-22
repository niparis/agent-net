# Teams and relationships design: independent review

Date: 2026-11-06. Reviewer: an independent model, read only. Reviewed
`docs/design/teams-relationships.md` at `0c42511e` against the code and the
earlier designs. I did not edit the design or the code.

The shape is right for modularity steps 4 and 5. The present-behavior section is
not yet accurate, the team settings level does not exist, and cross-cutting
delivery cannot work as described. I count 9 blocking, 13 should-fix and
6 minor findings.

## Blocking

### B1. Relationships today: present endpoints are the target endpoints, not today's
`Relationships today` says `governs` is Agent to Agent and delivery is Agent to
Repo. Today `setEdgeTx` requires a `governs` source of kind `architect` and a
target of `component`, and a delivery source of `component` or `architect` with
a `component` target; `graphKind` does not consult `agents`, so an Agent that
owns no Repo cannot be an endpoint. Evidence:
`internal/gateway/store_graph.go:771-777,27-47`. Fix: describe today as architect
node to component, and make Agent and Team endpoint kinds a stated prerequisite
in step 1 (or 3e), with tests.

### B2. Required team work: cross-cutting delivery cannot work with the current engine
The design says the existing engine chooses a session for the assigned Agent.
It does not. Dispatch and claim match the session's Repo (`sessionRepoSQL`) to
the owner node, not its `agent_id`, and claim rejects a session on another
machine. An Agent whose ID is not a Repo name never gets the step, and a remote
reviewer cannot report. Evidence:
`internal/gateway/delivery_tasks.go:405-412,1225,1244`;
`internal/gateway/delivery_changes.go:120-138`. Fix: add an Agent-based recipient
and claim path, define precedence when a session matches both, state the machine
rule, and test both machines.

### B3. Settings and delivery: the team settings level does not exist
`Settings and delivery` resolves through the Repo's Team and `Data and live
migration` says settings use "the existing team scope". There is no team scope.
`Where` has no Team field, `Resolve` never reads a team level, `writeSettingsTx`
rejects any level other than `network` or `repo`, and no key lists `team`. A
team could store nothing and no member would inherit anything. Evidence:
`internal/settings/settings.go:612-617,631-746`;
`internal/gateway/settings.go:299`; `internal/settings/standard.yaml:11-47`.
Fix: step 2 adds the level end to end and names the keys that gain it, which for
this design are the delivery keys at team level in `modularity.md:133`.

### B4. Settings and delivery: "rules only tighten" and "leaving does not loosen" are not supported
The resolver clamps a child rule only when a network row was explicitly stored;
a shipped default is not a bound. The write path checks a looser Repo rule only
against an explicit network row. Nothing rewrites child rows when a parent
changes: `Raised` is computed at read time and thrown away. A team parent
therefore cannot keep a Repo value raised after the team is left. Evidence:
`internal/settings/settings.go:674-695,714-729`;
`internal/gateway/settings.go:396-413`. Fix: specify how a raised value is
persisted and reconciled, and capture the effective value and source for every
delivery key so no-team resolution cannot change.

### B5. Module boundary: a team relationship rule can widen trust
The module returns `auto`, which sets `RecipientTrust` and the owner-trust note.
The design says only that an unknown rule must not be "more permissive", but it
never defines the order or says who may select a team rule. A Team owner is an
ordinary Agent, so if it may select its team's rule it can turn requests to its
Repos into owner-approved requests. Evidence:
`internal/gateway/graphread.go:168-170`; `internal/gateway/server.go:2197-2209`;
`internal/protocol/trust.go:29-34`; `internal/gateway/settings.go:195-200`. Fix:
only the network owner selects the network rule. A team selection may only
tighten, with an explicit rank (`never`, `ask`, `normal`, `auto`) and a test that
it cannot produce `auto` where the network rule would not. Unknown versions
reject the change; they do not fall back.

### B6. Required team work: a team default can silently drop or hand off an approval
The design says a team assignment is a default and an explicit special owner is
an override, but it does not forbid a team default of `@none` or `@delegate`.
`@none` leaves an approve step out and `@delegate` hands it to the node's
governor, so a team default of `@none` removes approval from every member Repo
with no explicit assignment. Evidence:
`internal/protocol/delivery_pipeline.go:343-357,1006,1226`;
`internal/protocol/delivery_pipeline_settings.go:130-135`. Fix: team defaults may
name only an Agent (or a Repo), never a special owner. A Repo may still choose
one explicitly, and the source is shown. Test that a team default cannot remove
or delegate an approval.

### B7. Data and live migration: downgrade is unsafe, and the cited precedent says the opposite
The design says to retain old data "as the split design does" and that an old
gateway may open the database, then documents downgrade as unsafe. The split
design says there is no downgrade data and rollback means restoring a backup. An
old gateway can open a newer database because `migrate` does nothing when the
stored version is higher, then ignores team settings, so a team rule that
tightened review disappears. Evidence:
`docs/design/repo-agent-split.md:328-341,417`; `internal/gateway/store.go:939-948`.
Fix: align with the split: no downgrade, restore a backup. Add a schema guard in
the release before Teams that refuses a database version it does not know.

### B8. Data and live migration: the old-client claims contradict step 3e
Teams are modularity steps 4 and 5, after the Repo/Agent split. Step 3e's cutover
rejects old daemons and drops legacy adapters. The design says old clients keep
their existing projection and old daemons stay safe, which cannot both be true:
before 3e there are no typed refs, after 3e old clients are refused. Team nodes
and `member_of` edges would also appear in `Graph()` and the legacy node union
unless filtered. Evidence: `docs/design/repo-agent-split.md:343-352,456-462`;
`internal/gateway/store_graph.go:858-862,1002`. Fix: state the position. If Teams
ship after 3e, replace the old-client section with the version gate. Keep team
data out of every legacy projection and test it.

### B9. Build steps: step 5 leaves two second interpretations behind
The design routes the request gate, the relation line and the delivery owner
lookup through the module. `held.go` matches edges again when the owner answers
a held request with "always", and can create a `depends` edge. `remind.go`
matches `governs` against Repo names rather than Agent identities and uses its
own wildcard order. Extracting only the named paths leaves both behind. Evidence:
`internal/gateway/held.go:50-88`; `internal/gateway/remind.go:14-32`. Fix:
include both in the module contract and parity fixtures, or state why they stay
outside and how they stay consistent.

## Should fix

### S1. Relationships today: peer trust is a project-wide edge, not only a session flag
For a scoped session, `SetPeerTrust` writes a wildcard `* -> repo depends` edge
with rule `auto` (or `normal` when off), so trust is effectively per project and
two sessions in one Repo share it. Non-request messages take trust from the
session flag, not the rule. Evidence: `internal/gateway/store.go:1965-1974`;
`internal/gateway/server.go:2210-2211`. Fix: describe both paths and their
granularity, and test on, off, and two sessions in one Repo.

### S2. Relationships today: the wildcard is depends-only, and the tie order is alphabetical
The design says `governs` and `deploys` match "including wildcard source".
`setEdgeTx` allows a wildcard only for `depends`. The tie order is the literal
kind string, so `deploys` beats `depends` beats `governs` when an Agent and a Repo
share a name, which the split allows. Evidence:
`internal/gateway/store_graph.go:764-766`; `internal/gateway/graphread.go:172-175`;
`docs/design/repo-agent-split.md:278-279`. Fix: state the real order, and test a
same-name pair.

### S3. Relationships today: the relation line has no wildcard and follows Graph order
`RelationFor` matches only exact edges and returns the first match in `Graph()`
order (`source`, `target`, `kind`). A wildcard edge gates but does not frame.
Evidence: `internal/gateway/graphread.go:84-93`;
`internal/gateway/store_graph.go:1002`. Fix: state both limits and test a wildcard
edge and a pair with both `depends` and `governs`.

### S4. Relationships today: worker and CTO identity fallbacks are missing
A worker's Agent identity becomes its Repo (`if worker || agent == "" { agent = project }`),
and the CTO maps to `CTONodeName` for both endpoints. These decide whether a
`governs` edge matches and what the relation line says. Evidence:
`internal/gateway/graphread.go:134-145`. Fix: list both in `Relationships today`
and add parity cases for a worker, an unscoped session and the CTO.

### S5. Relationships today: owner-in-person requests are gated today
Only `m.FromCTO` is exempt at the send path. An owner session has `FromCTO=false`,
so a wildcard `ask` or `never` edge can hold or reject an owner's request,
although decision 0022 says the owner is above all edges. Evidence:
`internal/gateway/store.go:2497-2504,2622`. Fix: say that only the registered CTO
is exempt today, and have the owner decide whether owner-in-person should be
exempt in the new rule.

### S6. Required team work: the team precedence ignores the pipeline's existing guards
The design says Repo assignment wins, then the Team, then the pipeline owner. The
overlay also applies `tests` to both phases, treats `deploys` as a fallback behind
`delivery_deploys`, and ignores an approve assignment when the current owner is
the owner-ask or a delegate, except that a delegate can be replaced by the owner.
A team level changes those cases unless they are defined. Evidence:
`internal/protocol/delivery_pipeline_settings.go:90-135`. Fix: write the
precedence as a matrix for every step reference and phase, and test each case.

### S7. Required team work: team-level required review cannot be expressed
"A separate pipeline requirement makes it mandatory", but the requirement keys
(`delivery.pipeline.merge.requires` and the review keys) list only `network` and
`repo`. A team cannot require review, so "assign it to a delivery step for a whole
team when the work is required" cannot come from team configuration alone.
Evidence: `internal/settings/standard.yaml:41-62`; `docs/design/modularity.md:133`.
Fix: name the key that becomes team-level and add it in step 2, or state that the
owner sets the requirement per Repo and the team assignment only picks the owner.

### S8. Module boundary: free-text framing from a rule can imitate agent-net
The interface returns "relation framing" while the gateway "writes it". If a
future rule returns a string, it can include the `[agent-net` marker or an owner
note. The marker rule exists to stop a sender doing exactly that. Evidence:
`internal/protocol/trust.go:72-82`;
`docs/decisions/0013-peer-trust-and-hints.md:43-47`. Fix: return a typed relation
(from, to, kind, mode, no-relation) and render it in the gateway, or format and
neutralize it there. Test a rule that returns the marker.

### S9. Module boundary: step 5's "separate operation" breaks the pure pipeline callers
`PipelineFromSettings` is pure and is used by the worker notice, maturity text,
the settings view and the gateway. The design moves the delivery owner lookup
into a module operation that needs a graph/settings transaction, which those
callers cannot use, so they would keep a second overlay. Evidence:
`internal/protocol/delivery_pipeline_settings.go:9-11`;
`internal/protocol/workers.go:953-959`; `internal/protocol/maturity.go:179-205`.
Fix: keep one resolver. Have the gateway resolve team defaults into
`snapshot.DeliveryAssignments`, and give the module only the gate and framing.

### S10. Build steps: steps 2, 3 and 5 are too big for about 1,000 lines each
Step 2 adds team storage, the resolver level, write validation, the raised-value
model, reconciliation, sources, undo and no-team parity. Step 3 adds endpoint
kinds, Agent and machine eligibility, team defaults, precedence, reconciliation
and UI. Step 5 must also absorb `held.go`, `remind.go` and the pure pipeline
callers. Evidence: `docs/design/teams-relationships.md:283-300`;
`internal/gateway/settings.go:294-414`;
`internal/gateway/delivery_tasks.go:405-412`. Fix: split 2 into storage/read and
writes/reconciliation, split 3 into endpoint/eligibility and team defaults, and
split 5 into gate/framing and delivery lookup.

### S11. Build steps: step 4 is mostly already built
Expertise is stored and searched, and `Purpose` and `Charter` are already in the
full session read. The missing work is team context, honest availability and wake,
and search over charter text. Evidence: `internal/gateway/toolread.go:278-289,400-407`;
`internal/gateway/store.go:2226`; `internal/protocol/toolread.go:92-97`. Fix: narrow
step 4 and cite the existing expertise path. Test wake only on the harnesses that
support it.

### S12. Required team work: team-move reconciliation reuses a one-project handler with a sharp edge
The owner-change handler expires queued messages for one project and step,
updates unclaimed tasks, and marks unclaimed tasks completed when the new owner
is not a node. A team move affects several Repos and steps, and clearing a team
default should fall back to the pipeline owner, not to completed. Evidence:
`internal/gateway/store_graph.go:699-710`. Fix: define the recompute for Agent,
special owner, cleared and moved, and test claimed and unclaimed tasks.

### S13. Relationships today: legacy `deploys` misses the delivery-edge checks
The design calls `deploys` a delivery assignment and cites `store_graph.go:732`
for the owner gate. The gate is line 736, and `deploys` does pass it, so only
the owner writes it. But `deploys` is excluded from the delivery branch at 732,
so it misses the one-owner uniqueness and component-target checks, and the
pipeline overlay treats it as a lower-priority fallback behind `delivery_deploys`.
Evidence: `internal/gateway/store_graph.go:732-738,774-784`;
`internal/protocol/delivery_pipeline_settings.go:108-118`. Fix: state that
`deploys` is owner-gated but skips those checks and is a fallback. Preserve that
or close it as a separate owner-approved change, with a test for both edges.

## Minor

- **M1. Citation line errors.** `graphread.go:151` to `168-170`; `store.go:2617`
  to `2622`; `store_graph.go:732` to `736`; `delivery_pipeline.go:142` to
  `149-157`; `delivery_pipeline.go:11` to `482-493`; `repo-agent-split.md:247`
  to `251-256` or `456`; `repo-agent-split.md:274` to `328-341`;
  `toolread.go:244` to `259-260`; `session_wake.go:17` to `51,66`.
- **M2. Step 6 can be dropped.** The owner asked for today's rule as the first
  form. No second rule has a concrete recipe. Defer selection and proposals.
- **M3. Team deletion does not say what happens to team settings rows and
  history.** `Data and live migration` covers membership and assignments only.
- **M4. Team owner tombstone and deletion are undefined.** A direct reference
  like Repo ownership does not say what happens when the owning Agent is removed.
- **M5. Open questions 2, 3 and 6 still change security and migration.** Mark
  them as owner decisions needed before step 3, not as open questions.
- **M6. One name per concept.** The design uses `Repo`, `component` and `node`
  for the same thing, and `Agent` for a graph node and a record.

## Verdict

**Ready to build after the blocking fixes.** The model is right and the no-teams
default is the right safety property. Do not start step 2 until B3 and B4 are
resolved, or step 3 until B1, B2 and B6 are resolved. B7 and B8 need a position
before migration work. A redesign is not needed, but the six steps are not yet
buildable: split 2, 3 and 5, and drop 6 until a second rule has a concrete recipe.
