# Settings layer

Date: 2026-11-03
Status: second design for owner review. Documentation only. Nothing is wired yet.

This replaces the first version. It follows [modularity.md](modularity.md), the
149 opinions in [modularity-inventory.md](modularity-inventory.md#2-built-in-opinions),
and the [independent review](settings-layer-review.md). Row numbers below refer
to that inventory. Its current values and source locations remain the extraction
baseline. A row may become several keys. Extracting a setting must preserve its
consumers, fallback behavior and evaluation order.

## Decisions in force

The owner's four decisions of 2026-11-03 are final:

1. The CTO changes advice directly. Rules need an owner-approved proposal.
   Every change has an author, history and undo.
2. Guidance moves first. Delivery moves immediately after it, in the first half
   of the build: worker tests, CI, review, merge and release.
3. A stricter parent moves conflicting children to the new parent value in the
   same transaction. Tell the owner which children changed. Never refuse the
   parent change because of those children. Lower rule overrides stay few and
   plain.
4. One readable YAML file holds every setting's key, description, type, kind,
   levels and default. It ships inside the binary and IS the standard recipe.
   Go holds per-type validation and stricter checks, plus context defaults that
   cannot be data. There is no second Go definition table.

This design chooses a new gateway table and explicit tightening orders. These
are recommendations for approval with this design, rather than reasons to stop
writing it. There is no product change in this step.

## Definitions and values

Use `internal/settings/standard.yaml` in the first code step. Embed its exact
bytes. It contains the format version, definition version, settings and an
inventory disposition list for all 149 rows. Each setting has:

| Field | Meaning |
|---|---|
| `key`, `description`, `inventory_rows` | Stable name, plain explanation and source rows |
| `type` | Scalar or declared list, record or keyed collection schema |
| `kind` | `advice` or `rule`; permission follows this field, never prose wording |
| `home`, `chain`, `overrides` | Binding level, ordered chain and explicit permitted override levels |
| `default` | Literal value, or a named context default with declared inputs and binding point |
| `validation`, `tightening` | Data constraints and comparison mode; no lower overrides unless an order is declared |
| `visibility` | `public`, `owner`, or `internal`, defined below |
| `slots` | Named, typed slots for text, if any |

For example, this illustrates the format, not the full standard recipe:

```yaml
format_version: 1
definition_version: 1
settings:
  - key: workers.sandbox_level
    description: Minimum permissions restriction for a worker.
    inventory_rows: [52]
    type: {name: enum, values: [sandboxed, no-network, read-only]}
    kind: rule
    home: network
    chain: repo_run
    overrides: [repo, run]
    default: sandboxed
    contextual_defaults:
      - at: repo
        function: maturity_worker_sandbox
        inputs: [repo.maturity]
    validation: {}
    tightening: {mode: enum_order, order: [sandboxed, no-network, read-only]}
    visibility: public
```

The maturity preset matrix itself is data in this file (row 44). The named
function selects the right preset at repo, rather than hiding another copy of
the matrix in Go. The file also lists joint constraints and text consumers.
No executable expressions, arbitrary scripts or general template language are
allowed. A recipe variant uses this same format with different values. Runtime
import, export and sharing are later work; definitions remain binary-owned.

Supported values are `bool`, signed 64-bit `int`, finite binary64 `float`,
`duration`, `string`, `text`, `enum`, `list`, `record` and `keyed`. Duration is
nonnegative integer nanoseconds on the wire, with human units in YAML. Lists
declare element types and ordered or set semantics. Records have named typed
fields and reject unknown fields. A keyed collection has a declared key type
and record or scalar value schema. It is not an untyped map.

Rows 31, 69 and 82 need keyed records. Provider cache windows use provider IDs;
model scores use model-pattern plus effort; findings and notes use subject IDs.
Row 82's stored findings remain operational records; only thresholds and note
policy move to settings. Rows 120 and 126 use registry IDs for skills and MCP
servers. A definition declares a family once, rather than creating new setting
names whenever a server, skill or model is added. Each collection entry is a
separate override and history address. New registry objects need no release.
Network seed entries for model scores move from `scores.json` into the standard
recipe at cutover. Pattern matching and picker arithmetic stay code.

For skills, distinguish the installed/allowed ceiling from the new-project
selection default. Row 120's `new_projects=false` seeds an unconfigured repo;
it is not a network ban. An explicit repo selection may enable an installed,
allowed skill. A network ban or uninstall cannot be loosened below it. Encode
these as separate typed leaves with separate binding and comparison semantics.
MCP attachments likewise separate the registry enabled ceiling from default
attachment. An explicit empty attachment set revokes attachment; deleting that
override means inherit and may restore it, so deletion needs the same rule
approval and bound checks as any other write.

Encode database and snapshot values as tagged JSON, for example
`{"type":"float","value":0.33}`, `{"type":"text","value":""}` and
`{"type":"list","value":[]}`. A record's fields and list elements follow its
declared schema recursively. A keyed entry encodes one typed value, not a whole
opaque collection. Use sorted object keys and round-trip binary64 decimals.
Reject NaN, infinity, duplicate YAML/JSON keys, overflow and unknown tags.
Normalize negative zero. Integers and durations never pass through float64.
Empty text, list or record is an explicit value if its schema allows it.
No row means absent/inherit. JSON null is not absence and is not a setting value.
History uses SQL NULL only to represent an absent before or after row.

## Levels, identity and context

Machine is a capability, not a recipe level. Harness is also context, not an
inheritance level. Session is the short-lived instance of an agent; run is one
worker attempt. The definition selects one chain:

| Chain | Traversal | Use |
|---|---|---|
| `repo_run` | network -> repo's team -> repo -> run | Tests, delivery, sandbox and isolation |
| `agent_session` | network -> agent's team -> agent -> session | Agent behavior and today's session delegation |
| `repo_agent` | network -> repo's team -> repo -> acting agent -> session or run | Repo obligations and advice that an agent may refine |
| `network` | network | Shared money, calibration, CTO and fixed shared policy |

The last branch is one terminal session or run, never both. A definition may
permit fewer levels than its chain contains. `home` says where its own default
binds; it does not forbid an explicitly declared broader minimum or default.
Network defaults for repo and agent settings are available before those scopes
exist. `overrides: []` means no lower overrides, not an unchangeable network
value. Read-only constants are not settings.

Rows 6, 42, 45, 128 and 131 use `repo_agent`. Row 64 may use that chain to allow
repo, agent and run restriction. Delivery otherwise comes from the work's repo,
not the calling agent's home repo. Agent behavior otherwise comes from the
acting agent. When a visiting agent belongs to a different team, `repo_agent`
uses the work repo's team; it does not silently merge two team branches. The
agent's explicit restriction applies only if the definition permits it.

```go
type Input struct {
    NodeID, SessionID, Harness, Project string
    TeamID, RepoID, AgentID, RunID string // optional assertions, checked
}
type DefaultInputs struct {
    Maturity, Harness string
    RAMBytes int64
}
type ScopeLookup interface {
    Lookup(Input) (ScopeContext, error)
}
```

`ScopeContext` contains the validated chain identities, scope facts and machine
capabilities, all at the snapshot revision. Callers can supply the IDs they
know. They need not invent TeamID or AgentID. Caller identity comes from the
existing authenticated channel, not from these fields. Unknown or conflicting
assertions are errors. Optional missing scopes are skipped. A local session
before `session.sync` may have only harness and project; it can still use local
defaults. It cannot use missing identity as authority for a gateway mutation.

The first lookup adapter uses graph node IDs, known projects and sessions.
Existing component nodes provide a repo identity and a legacy agent alias.
These are separate typed scope addresses even when their strings coincide.
It never creates real teams or agents. Reserved levels return unsupported on
writes until their models exist; fixture lookups exercise them in tests.
Repos known only through projects or sessions are valid implicit repos. No
registration step is added. Resolve aliases from `(node_id, project)` once to
stable IDs; preserve the mapping in snapshots and migration history.

Network's only scope ID is `network`. Other scope IDs are opaque stable IDs,
case-sensitive, compared with SQLite BINARY collation. Display names and paths
are not IDs. Do not lowercase graph IDs. Legacy lowercased project policy keys
need an explicit mapping to the same repo; ambiguous case collisions stop that
key's cutover for owner repair. Never guess or merge them.

## Resolution, defaults and tightening

Use the same pure resolver library in gateway and daemon, over an immutable
snapshot. `Resolve(input, keys)` resolves only a requested set locally; it is
not a per-key RPC. Return each value, definition version, snapshot revision,
source level and ID, default function if any, and any capability restriction.
Source distinguishes `network default`, `repo R maturity default`, an explicit
value, and `machine local capability`. Explanations include the full binding
trace when requested. Filter visibility before returning values or traces.

Context defaults bind at their home level during resolution. Traverse broad
to narrow, carrying the current effective rule. At each level:

1. Apply that level's default only if no explicit value for that level exists.
   A context default uses only declared facts available at that level. It cannot
   read a future child. Literal fallback defaults are bound once, not reset at
   every level.
2. A rule default is constrained by the already effective ancestor. For an
   ordered value take the stricter of the two. For set rules combine constraints
   according to the declared order. Advice defaults replace the fallback only
   where the definition says they bind.
3. Apply any explicit value there. A rule must meet the inherited bound and the
   level's own mandatory constraints. Advice may replace freely.
4. Continue through the permitted children. Absence inherits. Check declared
   joint constraints on the resolved family before returning it.

Thus critical maturity supplies `no-network` at repo for row 52. A team value of
`sandboxed` is legal before that repo is visited. A run value of `sandboxed`
cannot loosen the repo's `no-network`. A network `read-only` minimum remains
`read-only` even in an early repo. Host RAM defaults bind in the capability
calculation using that host's RAM, not a remote caller's RAM. Harness fallback
binds to the actual session or run harness. Default functions are deterministic,
side-effect free and tested for every declared input. Time, network and disk
reads are not default inputs.

Keep rule overrides limited to demonstrated needs: sandbox/isolation at repo
and run, escalation at agent, and the specific cap or allowlist restrictions in
the coverage ledger. Do not grant every setting all levels. The loader rejects
an override level without a declared order. Widening an allowed level requires
a definition change and review.

| Type | Permitted stricter checks |
|---|---|
| Bool | Explicit tighter value; no universal true/false order |
| Int, float, duration | Declared min or max order, range and units; sentinel order explicit |
| Enum | Data-defined order or partial-order pairs; equality always allowed |
| Set list | Subset for allowlists, superset for obligations or forbidden actions |
| Ordered list | Equality unless a declared per-type sequence order exists |
| Text or string | Equality for rules; freely replace advice |
| Record | Declared field orders and data-defined joint constraints |
| Keyed | Compare each entry with its inherited entry; collection membership has its own declared order |

A sentinel such as retention `0 = forever` is not numerically less than one day.
Its definition declares the meaning and order. Most network-only numeric
calibration settings need no lower-level stricter order. Defaults, writes and
snapshots share the same per-type validation.

Row 42 splits repo purpose/accepts/refuses advice from an escalation rule.
Escalation is a set of required cases, with stable case IDs and text. An agent
may add cases, never replace or remove inherited cases. Arbitrary extra prose
is rendered as an added case; no algorithm claims to prove prose stricter.
Changes to existing required cases need the owner at their home. Row 128's
merge authority uses the order `full -> delegated -> ask`; its wording is
separate advice derived from that rule. These two policy fields become rules
for settings approval and inheritance. This does not pretend a git merge gate
already exists. Current consumers still receive text until a separate
mechanism enforces it. The same distinction applies to future CI and release
gates. Never accept advice as proof of an enforced check.

A parent tightening, deletion exposing a tighter inherited value, maturity
change, membership change or relevant registry change recomputes all affected
chains in the gateway transaction. If a child's explicit rule fails the new
bound, replace that child with the new effective parent value. This also works
for incomparable child values: copy the parent exactly. Do not reject the
parent because of descendant conflicts. Record every adjusted child in the
same revision and send one owner notice listing before, after and cause.
Do not delete the override, since later parent relaxation must not silently
loosen that child's newly recorded restriction. Invalid types, unknown scopes
and unauthorized writes can still be rejected. Relaxing a parent leaves its
stricter children unchanged. Undo uses these same rules and shows any new
adjustments; it cannot bypass today's bounds.

Joint behavior needs explicit treatment, rather than blind scalar splitting:

- Rows 64-65: continuation is enabled only when the toggle is true AND the cap
  is greater than zero. Zero dominates true; this is a data-declared dependency,
  shown in the explanation. It is not a conflicting permission to continue.
- Row 51: preserve today's missing-worktree continuation and read-only
  `isolation:none` cases in the legacy default policy. An explicit tighter
  worktree requirement blocks a new continuation without that worktree. The
  default cannot be described as an unconditional worktree requirement while
  still promising parity. Encode the legacy conditional cases as a record;
  making them unconditional is a later policy change.
- Row 129: preserve two named legacy outputs, approval text `ask` and delivered
  authority `full`. Mark this inconsistency in effective explanations. Do not
  make an existing extraction choose one silently. A future gate needs one
  owner-approved value before it can enforce uncharted merges.

Corrupt legacy node rows keep today's ignore-invalid behavior during migration:
quarantine and report them, resolve the existing maturity default and log that
source. Never store them as valid typed overrides. New typed writes reject
invalid values. A malformed incoming snapshot is rejected as a unit; keep the
last good snapshot. Corruption is not grounds to stop ordinary local turns.

## Machine capabilities and existing local choices

Keep `node.json`, `integrations-local.json`, daemon `owner_preferences`, and
process/setup flags. They are not new rows in the inheritance ladder.

| Existing state | Treatment at cutover |
|---|---|
| Rows 53-59, 61: node worker caps, timers, CPU and RAM | Preserve explicit local values and distinguish absence. Compute host admission and supervision inputs from network defaults plus the local capability record. |
| Row 98: `model_providers` | Preserve local provider preference order as a host routing choice; recipe supplies the network fallback. |
| Rows 101, 117, 118: integrations, quota tool and updates | Keep machine install/support choices and local switches; recipe provides network intent. Missing tools or disabled local support never become installed by resolution. |
| Row 116: notification preference | Keep the daemon-local owner toggle; combine with network cooldown defaults. |
| Other node fields | Keep hostname, entry options, no-launch, paths and credentials outside settings. |

Initially the compatibility adapter reproduces today's local replacement
semantics, including a longer local timer, and labels that provenance. These
are declared local capability inputs in the recipe, not hidden recipe
children. A local replacement replaces a shipped fallback only. Explicit recipe
bounds, including timer caps, constrain the composed result using the declared
order. A tighter network value can therefore shorten the effective local timer
without rewriting node.json; show both the local input and binding bound. New explicit recipe security restrictions such as sandbox, denied
providers and owner-only actions remain binding; a capability cannot grant
permission. Later moving an existing host preference into a shared recipe is
an explicit migration, not deleting its current override. Snapshot resolution
and host capability composition are separate stages, both visible to the
owner. Private paths and node identities are removed from public explanations.

## Gateway tables, history and revisions

Use new tables. `owner_settings` keeps operational state such as `cto_session`
and `worker_rotation`. `graph_node_settings` and `project_policies` remain
authoritative only for keys not yet migrated. The following is the exact first
schema, with foreign keys enabled. All times are UTC RFC3339 text. JSON is
validated in the library as well as by SQLite.

```sql
CREATE TABLE setting_revision (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    revision INTEGER NOT NULL CHECK (revision >= 0)
);
INSERT INTO setting_revision VALUES (1, 0);

CREATE TABLE setting_history (
    revision INTEGER NOT NULL CHECK (revision > 0),
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    key TEXT COLLATE BINARY NOT NULL,
    level TEXT NOT NULL CHECK (level IN
        ('network','team','repo','agent','session','run')),
    scope_id TEXT COLLATE BINARY NOT NULL,
    item_id TEXT COLLATE BINARY NOT NULL DEFAULT '',
    before_value TEXT CHECK (before_value IS NULL OR json_valid(before_value)),
    after_value TEXT CHECK (after_value IS NULL OR json_valid(after_value)),
    definition_version INTEGER NOT NULL CHECK (definition_version > 0),
    actor TEXT NOT NULL,
    approval_id TEXT,
    cause TEXT NOT NULL,
    undo_of_revision INTEGER,
    changed_at TEXT NOT NULL,
    PRIMARY KEY (revision, ordinal),
    CHECK (level <> 'network' OR scope_id = 'network'),
    CHECK (before_value IS NOT NULL OR after_value IS NOT NULL)
);

CREATE TABLE setting_values (
    key TEXT COLLATE BINARY NOT NULL,
    level TEXT NOT NULL CHECK (level IN
        ('network','team','repo','agent','session','run')),
    scope_id TEXT COLLATE BINARY NOT NULL,
    item_id TEXT COLLATE BINARY NOT NULL DEFAULT '',
    value TEXT NOT NULL CHECK (json_valid(value)),
    definition_version INTEGER NOT NULL CHECK (definition_version > 0),
    revision INTEGER NOT NULL,
    history_ordinal INTEGER NOT NULL,
    PRIMARY KEY (key, level, scope_id, item_id),
    FOREIGN KEY (revision, history_ordinal)
        REFERENCES setting_history(revision, ordinal),
    CHECK (level <> 'network' OR scope_id = 'network')
);

CREATE TRIGGER setting_history_no_update BEFORE UPDATE ON setting_history
BEGIN SELECT RAISE(ABORT, 'setting history is append-only'); END;
CREATE TRIGGER setting_history_no_delete BEFORE DELETE ON setting_history
BEGIN SELECT RAISE(ABORT, 'setting history is append-only'); END;
```

`item_id` is empty for ordinary values. For keyed values it is a canonical JSON
array of identity components, for example `["model-pattern","medium"]`.
It uses immutable registry IDs for skills and servers. A keyed entry with an
empty list of allowed tools is distinct from no entry. No wildcard item ID is
implied. Ordered pattern precedence is explicit data, never SQLite row order.
The library validates that each address matches its definition and registry.

Each successful state-changing transaction increments the one global revision
once. Its edits and induced child changes share that revision, with unique
ordinals. A no-op consumes no revision. History holds author, approval,
before/after, reason and undo link; the value's history row supplies its update
time and actor. Never prune this history with message retention. Schema version,
definition version and global state revision are separate numbers.

Membership, maturity, scope aliases and registry policy changes must enter the
same serialized revision path, even when their actual rows live in existing
tables. A fact-only change increments the revision without a value history row;
the existing graph/registry audit records that fact. Its induced setting edits
still get history. Snapshot construction reads facts, values and revision in
one SQLite read transaction. It cannot combine new membership with old rules.
Undo a migrated graph setting through this history, not by reviving its old
`graph_node_settings` row. `GraphUndo`, `RemoveArchitect`, node setters and
project/session policy writers must join the same transaction at their cutover.
Graph history links to the settings revision so a mixed graph undo is atomic.

## Writes, approval and visibility

The CTO's current `settings` tool is in `internal/mcpserver/cto.go`, not
`cto_v2.go`. `internal/daemon/cto_reads.go` currently exposes only worker
profiles there. Adding this resolver does not silently widen that read.

Each definition has `visibility: public`, `owner` or `internal`. Public means
readable by ordinary agents and the CTO through the same filtered projection.
Owner means owner surfaces only. Internal means resolver/enforcer use only,
with owner diagnostics through an explicit safe projection. Unknown definitions
are invisible. Existing public charter and rule fields stay public under
[decision 0041](../decisions/0041-charters-and-rules-are-public.md). New keys
start owner-only unless their review establishes public safety. Neither typed
records nor source traces may smuggle hidden fields into a public response.
Credentials, filesystem locations, owner preference details and raw learned
evidence are not public recipe values. History has the same visibility plus
actor redaction. Public value access does not grant access to approval payloads
or evidence. Extend CTO parity tests when each key becomes readable.

All changes use a gateway write command: key/address, delete-or-value,
`expected_revision`, actor, reason and optional proposal ID. The CTO may submit
advice directly, with no owner prompt. An ordinary agent cannot write advice.
Rules always require an authenticated owner decision. A definition's kind,
visibility and allowed levels cannot be changed through a value write. A mixed
advice/rule batch requires approval for the whole batch.

Existing daemon proposals remain a UI/workflow store, not proof that the gateway
may trust `approved:true`. The gateway owns an approval record bound to the
exact canonical command digest, expected revision, owner identity, expiry and
single-use decision token. The existing owner gate authenticates registration
and approval of that record. The daemon's runner submits its proposal ID, not
a self-asserted approval flag. Inside one gateway `BEGIN IMMEDIATE` transaction:

1. Authenticate the actor and recheck proposal identity, payload digest, owner
   decision, expiry and unused status for rule writes.
2. Compare `expected_revision` with `setting_revision`. Reject stale commands
   for a fresh diff, including advice and undo. Prior daemon validation is only
   a preview. A retry of an already consumed matching proposal returns its
   recorded result; it does not apply twice.
3. Validate scope, types, kind, capability bounds, rule order and joint
   constraints. Recompute descendants and move conflicting children as above.
4. Write values, append history, mark the approval consumed with its resulting
   revision, advance revision and enqueue the owner notification atomically.
5. Commit before publishing a snapshot. Reconnect replay covers a lost push.

The approval ledger belongs to the existing proposal mechanism's gateway
extension, not a second source of settings. It is wired in the write-path step,
not the first storage build. Undo is a new inverse command through this same
path. Advice undo can be direct CTO work; rule undo needs the owner. Undoing a
parent transaction includes its child edits but revalidates against current
facts. The TUI and CTO show the before/after, scope, source, adjustments and
revision. No owner surface or legacy endpoint bypasses the gateway check.

## Snapshots and offline operation

The gateway pushes a versioned snapshot after hello and after each committed
change, like `mcp.sync`. A snapshot includes schema/definition versions,
standard-recipe digest, global revision, relevant values and scope facts, and a
content digest. Each daemon receives only what it needs to enforce and render
its scopes. Gateway secrets and unrelated private settings are excluded.
A gateway-only setting is resolved at the gateway. Public client reads are
filtered again at their authenticated boundary.

The daemon stores the complete accepted snapshot in `daemon.db`:

```sql
CREATE TABLE settings_snapshot (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    revision INTEGER NOT NULL CHECK (revision >= 0),
    definition_version INTEGER NOT NULL,
    recipe_digest TEXT NOT NULL,
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    payload_digest TEXT NOT NULL,
    received_at TEXT NOT NULL
);
```

Verify the whole payload against the supported definitions and digest before
atomically replacing this row. Swap the in-memory immutable pointer only after
commit. Ignore older revisions. Equal revision with a different digest is an
error requiring resync. A changed definition version or projection gets a full
snapshot and advances the global revision before publication; it never reuses
an equal revision with different contents. Deltas
are unnecessary in the first build. Reconnect requests a full snapshot. Keep
one read transaction/pointer for a turn or worker tick so it cannot mix
revisions. Report the observed revision in diagnostics and worker facts.

| Operation | Gateway unavailable or incompatible snapshot |
|---|---|
| Local turns, hooks and per-turn advice | Last good persisted snapshot; shipped defaults before first sync |
| Existing worker supervision, timeouts, cleanup and local caller checks | Same local snapshot and host capabilities; never pause timeouts for an RPC |
| Judge failure | Preserve row 89's normal fallback; settings transport does not make the judge mandatory |
| Picker/start requiring gateway, cross-node send, proposals and shared writes | Fail the gateway-dependent operation; no local authorization guess |
| Daemon startup, hello, session.sync and upgrade discovery | Remain available; do not gate them on settings compatibility |

A stale offline snapshot may miss a new parent restriction. That is an explicit
availability decision, not a claim of instantaneous offline enforcement. On
reconnect, new local decisions use the new revision. Migration of each worker
limit must state whether active workers adopt it; preserve today's semantics
until then. Gateway mutations always use current gateway facts, even if the
requester's local preview is older. No per-key network request is added to the
2-second turn hooks or 5-second supervision tick.

Keep hello open for older daemons so session.sync can finish and auto-upgrade
can run. Advertise settings support as an additive capability. Unknown settings
versions disable affected gateway mutations with an upgrade explanation, not
the connection. Legacy HTTP/WS endpoints translate migrated values to the old
shape from the new authority. Old binaries still enforcing constants cannot
honor arbitrary new local restrictions. Gate those affected starts or edits on
capability support, or retain the legacy effective value until upgraded. Do not
claim the compatibility bridge changes an old binary's local enforcement.

## Guidance and text rendering

User-global `~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md` and equivalent managed
files carry only network advice. Team, repo and agent advice is rendered into
per-turn context and worker briefs for that validated context. A scoped advice
change never rewrites a user-global file. Default network wording stays byte
compatible. Shared symlinks still promote harness variants to Combined.
Managed native-coder files also cannot hold one repo's advice globally: retain
the network base there and pass scoped instructions in the delegation prompt.

Use named text slots with declared types, such as `{reply_tool}`, `{agent_id}`,
`{coder_model}`, `{merge_rule}`, `{delegate_names}`, `{owner_prompt_excerpt}`,
`{harness}`, `{permissions}`, `{worktree}`, `{diff}` and `{rate_ask}`. Allow
literal text and these slots only. Reject missing or unknown slots on write;
escape runtime text for its destination and neutralize authority markers before
insertion. Slots cannot create authority flags. Code chooses conditional blocks
and supplies typed data; the file supplies wording and slot declarations.
Empty maturity context still renders the same empty RulesText or merge text.

Capture independent old-output fixtures before migrating each consumer. Cover
Block Claude/Other/Combined, Apply/Remove, repeated SyncGuidance, shared
symlinks, legacy heading recognition and edited advice. Uninstall matches the
managed markers, including Antigravity, rather than comparing default bytes.
Preserve the heading, marker grammar and final newline; a later marker change
needs an explicit compatibility parser. Never remove unrelated user text.

Test PolicyText across delegation modes and unsupported harnesses; RulesText,
MergeApprovalFor and authority notes across unset/critical maturity, live
delegates and prompt excerpts; worker briefs/notices across permissions,
worktrees, diff, report framing and rating asks. Test tool descriptions and
Go/TypeScript plugin copies against the same slot contract and independent
fixtures. Generate or pass resolved text to adapters where possible. Do not
leave separate policy literals that silently defeat an override. Text parity
proves rendering, not agent obedience or enforcement.

## Inventory coverage

Every row below has a destination. Unless qualified, its definition retains
the inventory key, current value, kind, home and allowed levels. Split compound
rows into typed leaves under that key, with each leaf linked to its row. The
standard recipe will carry these links and dispositions, including code-only
rows. A completeness test must find exactly rows 1 through 149, with no holes
or unexplained extra opinions.

`Data` means values move to the readable file and override store. `Mixed` means
values move but the stated mechanism stays code. `Code` means the row describes
a mechanism or external data, not a freely replaceable recipe scalar. A rule's
security floor can remain fixed even when an additional recipe restriction is
allowed. The 12 fixed security checks listed separately in the inventory stay
code and are outside the 149.

| Row | Inventory key | Destination | Treatment |
|---|---|---|---|
| 1 | `guidance.sync_agent_files` | Data | Network sync toggle; marker parser remains code. |
| 2 | `guidance.intro_text` | Data | Orientation text. |
| 3 | `guidance.peer_help_direct` | Data | Help text with reply_tool slot. |
| 4 | `guidance.decide_not_defer` | Data | Decision text; share with graph context. |
| 5 | `guidance.owner_check_triggers` | Data | Owner-check advice; does not replace caller gates. |
| 6 | `guidance.trusted_free_actions` | Data | repo_agent advice; only render with authenticated trust note. |
| 7 | `guidance.always_ask_actions` | Data | Ask-first advice; no new action detector. |
| 8 | `guidance.cto_note_trust` | Data | Wording only; note provenance stays fixed. |
| 9 | `guidance.peer_contact_triggers` | Data | Contact advice. |
| 10 | `guidance.ask_peer_after_tries` | Data | Retry threshold and contact wording. |
| 11 | `guidance.relay_unedited` | Data | Relay wording; authenticated approval flags stay fixed. |
| 12 | `guidance.message_style` | Data | Message style text. |
| 13 | `guidance.announce_task` | Data | Announcement wording; no new enforcement. |
| 14 | `guidance.mcp_and_secret_hygiene` | Mixed | Hygiene advice moves; MCP owner gate and secret indirection stay code. |
| 15 | `guidance.register_projects_when_asked` | Data | Agent advice; scoped delegation prompt, not repo text in global files. |
| 16 | `guidance.start_workers_when_asked` | Data | Agent worker-start advice; does not create an autonomy gate. |
| 17 | `guidance.worker_brief_style` | Data | Brief-writing advice. |
| 18 | `guidance.wait_for_worker_notice` | Data | Wait-for-notice advice. |
| 19 | `guidance.report_late_defects` | Data | Late-defect advice; rating schema remains typed. |
| 20 | `guidance.worker_brief_precedence` | Data | Worker precedence text and run context. |
| 21 | `delegation.mode` | Data | Delegation enum; preserve session overrides and project policy aliases. |
| 22 | `delegation.native_unsupported_fallback` | Mixed | Fallback selection is data; harness support detection is code. |
| 23 | `delegation.max_failures_before_self_edit` | Data | Failure count plus wording; preserve session behavior. |
| 24 | `delegation.coder_subagent_prompt` | Data | Coder prompt and model slots; global file has network base only. |
| 25 | `cto.brief_text` | Data | CTO brief text. |
| 26 | `cto.protected_provider_advice` | Data | Protected-provider advice. |
| 27 | `cto.repoint_unavailable_profile` | Data | Profile-repointing advice. |
| 28 | `cto.charter_budget_advice` | Data | Charter budget advice; preserve existing 160/40 discrepancy visibly. |
| 29 | `cto.allowed_harnesses` | Data | Default harness and allowed harness list. |
| 30 | `cto.title` | Mixed | Title/default and length policy move; printable validation and fixed agent-facing CTO identity stay code. |
| 31 | `cto.compaction_policy` | Data | Typed compaction record and provider-keyed cache windows. |
| 32 | `cto.tool_classes` | Mixed | Tool class policy data; authenticated roles, as_owner approval floor and unknown-tool protection stay code. |
| 33 | `cto.allowed_actions` | Mixed | Action selection is data within fixed implemented routes and owner-only floor; cannot invent methods. |
| 34 | `cto.graph_minor_rules` | Mixed | Classification cases are data; classifier is code, with settings advice/rule authority overriding legacy heuristics. |
| 35 | `cto.restriction_words` | Data | Restriction-word set; matching procedure stays code. |
| 36 | `cto.owner_window_minutes` | Data | Window duration and permitted operation set; window is not a substitute for a rule proposal. |
| 37 | `cto.proposal_expire_hours` | Data | Proposal expiry duration. |
| 38 | `cto.proposal_max_open` | Mixed | Open cap and context count move; duplicate detection remains code. |
| 39 | `cto.learned_minor_daily_cap` | Mixed | Cap and eligible fields move; counting and learning algorithm stay code. |
| 40 | `cto.learned_signal_windows` | Data | Typed signal lengths and horizons; evidence remains private state. |
| 41 | `charter.agent_ownership_and_role` | Code | Repo/agent split, ownership and free-text roles require an entity mechanism; not a setting that changes identity. |
| 42 | `charter.scope_and_escalation` | Mixed | Repo charter advice plus additive escalation rule on repo_agent; ownership/schema stays code. |
| 43 | `repo.maturity` | Data | Repo maturity enum, unset fallback preserved; fact changes use revision transaction. |
| 44 | `maturity.level_presets` | Data | Typed preset matrix; bind its outputs at repo, not network. |
| 45 | `charter.judge_consumers` | Data | repo_agent consumer advice enum and wording. |
| 46 | `charter.autonomy_mode` | Data | Agent autonomy enum; existing wake mechanism unchanged. |
| 47 | `charter.size_budgets` | Mixed | Warning budgets move; attribute schema and leak detectors stay code. |
| 48 | `charter.context_template` | Data | Context layout with typed slots and conditional blocks. |
| 49 | `charter.neighbor_summary_limits` | Mixed | Count/byte limits and priority list move; truncation procedure stays code. |
| 50 | `relationships.edge_semantics` | Mixed | Relationship wording and declared policy parameters move; directed graph edges and authority computation stay in graph code. |
| 51 | `workers.isolation_minimum` | Mixed | Legacy isolation case record, tighter repo/run requirement; worktree creation and harness requirements stay code. |
| 52 | `workers.sandbox_level` | Mixed | Permission enum, repo maturity binding and session/project compatibility; harness enforcement support stays code. |
| 53 | `workers.max_age_minutes` | Data | Duration; preserve explicit node.json capability timer. |
| 54 | `workers.idle_timeout_minutes` | Data | Duration; preserve explicit node.json capability timer. |
| 55 | `workers.caller_gone_grace_minutes` | Data | Duration; preserve caller-owned semantics and local override. |
| 56 | `workers.queue_timeout_minutes` | Data | Duration; preserve explicit machine queue timeout. |
| 57 | `workers.max_per_machine` | Data | Network fallback and existing local hardware cap; no machine level. |
| 58 | `workers.cpu_admission_limits` | Data | Float CPU targets; local host capability record preserved. |
| 59 | `workers.memory_reserve` | Mixed | Reserve bytes and RAM fraction move; RAM-based default calculation stays Go. |
| 60 | `workers.cost_model_defaults` | Mixed | RAM, float core cost and window move; resource learning stays code. |
| 61 | `workers.start_gap_seconds` | Data | Duration with local start-gap capability. |
| 62 | `workers.machine_placement_policy` | Mixed | Placement strategy name/parameters are data; placement and ambiguous-load refusal algorithm stay code. |
| 63 | `workers.run_model` | Mixed | Run model/harness selection data; compatibility, provider and budget checks stay code. |
| 64 | `workers.auto_continue` | Data | repo_agent continuation toggle, terminal run restriction; effective enablement also requires positive row 65 cap. |
| 65 | `workers.auto_continue_max` | Data | Nonnegative cap, lower is tighter; zero disables continuation. |
| 66 | `workers.continue_prompt_text` | Data | Manual and automatic continuation text. |
| 67 | `workers.brief_preamble` | Data | Harness brief variants with slots. |
| 68 | `picker.difficulty_minimums` | Data | Named tiers and float minimums in a typed collection; network calibration. |
| 69 | `picker.model_scores` | Mixed | Model-pattern/effort keyed score records and default effort move; pattern matching stays code. |
| 70 | `picker.fit_bands` | Data | Ordered numeric fit bands and labels. |
| 71 | `picker.max_level_gap` | Mixed | Allowed level gap moves; eligibility procedure stays code. |
| 72 | `picker.overqualified_scale` | Mixed | Float decay scale moves; exponential formula stays code. |
| 73 | `picker.rotation_window` | Mixed | Rotation window moves; recent picks remain operational state. |
| 74 | `picker.quota_max_percent` | Data | Quota ceiling. |
| 75 | `picker.running_worker_penalty` | Data | Per-worker quota penalty. |
| 76 | `picker.quota_pace_curve` | Mixed | Float denominator and clamp move; exp(room/scale) formula stays code. |
| 77 | `picker.track_weights` | Mixed | Float bonuses, outcome multipliers and lookback move; track observations and arithmetic stay code. |
| 78 | `picker.poor_track_threshold` | Data | Minimum sample and completion-share threshold. |
| 79 | `picker.quality_weighting` | Mixed | Toggle and float clamp move; multiplier calculation stays code. |
| 80 | `picker.rating_sampling` | Mixed | Sampling cadence and scale parameters move; valid wire rating/defect bounds and payload protection stay code. |
| 81 | `picker.quality_estimator_priors` | Mixed | Float priors, thresholds and cost records move; estimator and observations stay code. |
| 82 | `picker.playbook_promotion` | Mixed | Limits, horizons and promotion thresholds move; subject-keyed findings/notes and promotion procedure remain module data/code. |
| 83 | `picker.calibration_thresholds` | Mixed | Float weights, bounds and sample windows move; calibration formula/report remains code. |
| 84 | `judge.ignore_caller_difficulty` | Data | Caller-label policy; preserve judge start path. |
| 85 | `judge.primary_model` | Data | Primary judge harness/model record. |
| 86 | `judge.retry_model` | Data | Fallback record, effort and retry budget. |
| 87 | `judge.timeout_seconds` | Data | Start, standalone and lookup durations. |
| 88 | `judge.analyzer_enabled` | Mixed | Analyzer toggle/selection policy data; analyzer mechanism stays code. |
| 89 | `judge.fallback_level` | Data | Fallback label; judge outage still uses normal by default. |
| 90 | `judge.rubric_text` | Data | Rubric and anchor text. |
| 91 | `judge.analyzer_prompt` | Data | Analyzer prompt text. |
| 92 | `judge.weights_and_cut_points` | Mixed | Float dimension weights and cuts move; weighted scoring formula stays code. |
| 93 | `judge.bottleneck_floor` | Mixed | Bottleneck trigger/floor parameters move; score application stays code. |
| 94 | `providers.protected_list` | Mixed | Protected provider set moves; fallback procedure stays code. |
| 95 | `providers.protected_reason_text` | Data | Protected explanation text. |
| 96 | `providers.pool_mapping` | Mixed | Typed harness/model pool mapping moves; provider identification and matching algorithm stay code. |
| 97 | `providers.worker_allowlist` | Mixed | Provider allowlist and loose/explicit policies move; live quota facts and spend guards stay code. |
| 98 | `providers.loose_match_order` | Data | Network order fallback plus current node.json local routing preference. |
| 99 | `quotas.refuse_when_exhausted` | Data | Exhausted-quota refusal policy; live quota observation is state. |
| 100 | `quotas.high_warning_percent` | Data | Warning threshold and ordered display ranks. |
| 101 | `quotas.poll_interval_minutes` | Mixed | Poll cadence and integration intent move; local tool/install choice stays capability. |
| 102 | `approvals.owner_only_actions` | Mixed | Additive owner-only action restrictions move; fixed owner gate cannot be relaxed. |
| 103 | `intake.allowed_sources` | Mixed | Allowed intake-source set moves; only existing session/owner mechanisms accepted, tracker intake not built. |
| 104 | `approvals.worker_max_nesting_depth` | Mixed | Nesting limit data within implemented roles; worker tool-role restriction stays code. |
| 105 | `approvals.worker_control_principals` | Mixed | Principal restrictions move; caller authentication and principal types stay code, no tracker principal yet. |
| 106 | `approvals.edge_request_rule` | Mixed | Default edge gate/order policy moves; (source,target,kind) and wildcard edge records stay graph authority. |
| 107 | `inbox.outcome_escalation_rules` | Mixed | Additional escalation cases move; required reason/recommendation schema and outcome handling stay code. |
| 108 | `inbox.ideas_backlog_enabled` | Data | Idea submission toggle; no new tracker backlog. |
| 109 | `inbox.allowed_kinds_and_limits` | Mixed | Allowed kinds and lower option/summary limits move; wire schema and hard bounds stay code. |
| 110 | `approvals.single_action_scope` | Code | Single-action approval binding is an integrity mechanism; recipe cannot make an approval reusable. |
| 111 | `inbox.idle_check_schedule` | Mixed | Delay/cap move; pending-work calculation stays code. |
| 112 | `inbox.autonomy_wake_daily_cap` | Mixed | Wake cap and progress parameters move; no-progress detection stays code. |
| 113 | `inbox.request_reminder_grace_minutes` | Mixed | Grace, max age and suppression policy move; reminder delivery/idempotence stay code. |
| 114 | `approvals.message_loop_and_rate_limits` | Mixed | Workflow hop/rate/TTL caps move; hard payload/buffer security ceilings stay code. |
| 115 | `network.retention_periods` | Data | Retention durations with explicit forever sentinel; migrate CLI precedence deliberately. |
| 116 | `inbox.notification_cooldown_seconds` | Mixed | Network cooldown moves; daemon-local notification preference stays local. |
| 117 | `approvals.harness_integrations` | Mixed | Typed integration intent moves; local overrides, installation and caller gates stay code/capability. |
| 118 | `network.harness_auto_update` | Mixed | Update schedule/toggle move; machine availability and update mechanism stay code. |
| 119 | `approvals.connected_app_token_ttl` | Mixed | Token TTL/renewal durations move; authentication and one-use OAuth mechanism stay code. |
| 120 | `skills.enabled_by_default` | Mixed | Registry-skill keyed new-repo/default choices and repo enablement move; placement plans and installed ceiling stay module state. |
| 121 | `skills.placement_strategy` | Mixed | Placement policy choices move; user/repo projection planner stays code, honoring decision 0048. |
| 122 | `skills.project_roots` | Mixed | Harness root data moves; foreign checkout/shadow/path guards stay code. |
| 123 | `skills.preserve_git_tracked` | Code | Never overwrite/delete tracked project skills is a filesystem safety floor, not a recipe opt-out. |
| 124 | `skills.worktree_copy_policy` | Mixed | Allowed copy categories move, lower levels may remove categories; ownership checks and copying stay code. |
| 125 | `skills.backup_retention` | Mixed | Backup count/age move; unfinished-journal protection stays code. |
| 126 | `skills.mcp_server_attachment` | Mixed | Registry-server keyed attachment allowlists/timeouts move; server registry, secrets indirection and transport stay code. |
| 127 | `delivery.tests_before_merge` | Data | Current test advice and maturity binding move first with delivery; enforced CI/tests require a later mechanism. |
| 128 | `delivery.merge_gate` | Mixed | Ordered merge authority rule on repo_agent; wording separate, git merge evidence gate still unbuilt. |
| 129 | `delivery.uncharted_merge_policy` | Data | Two explicitly named legacy fallback outputs until owner resolves drift; never claim one enforced gate. |
| 130 | `delivery.delegated_approver_modes` | Mixed | Eligible governance modes and approval text move; graph edge storage/lookup stays code. |
| 131 | `delivery.caller_review_and_merge` | Data | repo_agent review advice, split from merge rule; no claim that text verifies review. |
| 132 | `delivery.deploy_rule` | Mixed | Deploy policy and rendered conditions move; deployment enforcement is not created by extraction. |
| 133 | `delivery.worker_branch_prefix` | Mixed | Branch prefix moves; safe ref validation and unique naming stay code. |
| 134 | `delivery.worker_branch_prep` | Data | Commit/rebase/push/PR instructions; default remains commit only if asked. |
| 135 | `delivery.worker_commit_and_trailers` | Mixed | Auto-commit choice and trailer field policy move; git operations and provenance computation stay code. |
| 136 | `delivery.repo_worker_rules` | Mixed | Path, heading skip list and size policy move; .agent-net/worker.md remains repo-owned disk advice, not copied into gateway settings. |
| 137 | `delivery.worktree_cleanup_policy` | Mixed | Cleanup schedule/policy selector move; clean/merged verification and safe deletion remain code. |
| 138 | `notices.untrusted_peer_framing` | Mixed | Untrusted framing text moves; neutralization/provenance stay fixed. |
| 139 | `notices.trusted_request_framing` | Data | Trusted framing text with receiver merge-rule slot. |
| 140 | `notices.owner_prompt_excerpt` | Mixed | Excerpt cap/wording move; authorized excerpt selection and redaction stay code. |
| 141 | `notices.owner_overrule_text` | Data | Owner-overrule wording; approval identity remains fixed. |
| 142 | `notices.reply_no_ack_text` | Data | No-ack reply wording. |
| 143 | `notices.held_request_replies` | Data | Held/refused/declined/expired reply variants. |
| 144 | `notices.worker_start_text` | Data | Start/queue result and owner_line wording with slots. |
| 145 | `notices.worker_completion_format` | Mixed | Completion layout/checklist/learning text move; computed facts, untrusted-report boundary and marker grammar stay code. |
| 146 | `notices.worker_rate_prompt_text` | Data | Rating prompt with typed worker/scale slots. |
| 147 | `notices.worker_idle_minutes` | Data | Idle notice duration and harness set. |
| 148 | `internal/gateway/check.go:22,25,110-112` | Mixed | Question/wake wording moves; valid announce response protocol stays code. |
| 149 | `notices.error_hint_limits` | Data | Hint count and cooldown durations; matching algorithm stays code. |

The wholly code-only rows are 41, 110 and 123. Mixed rows keep the mechanisms
named above, including algorithms in 34, 62, 76 and 96, score/learning formulas,
graph edges in 50 and 106, and disk advice in 136. These are not gaps hidden
behind a generic string setting. Floats and keyed configuration are data;
observations, identities, graph edges and file contents are not all settings.
Registry removal must not silently turn an absent collection entry into a grant.
Retain tombstoned identity references for history and remove active attachments
through an approved registry transaction.

## Cutover and build order

After the storage-only step below, move guidance first, then delivery rows
127-137 and their duplicate guidance/notice text (especially 131 and 145).
This includes the worker-test instructions missing from a merge-only view of
row 127. Model worker tests, required CI, independent review, merge and release
as separate concepts. Preserve existing advice first. CI/review/release rules
that have no enforcing caller are explicit mechanism gaps; do not enable a
recipe entry and claim enforcement. Build their mechanisms separately once
the gap list calls for them. Delivery extraction belongs in the first half of
the build, ahead of picker, judge and quota extraction.

Then migrate other families in small cuts. Each cut includes independent
before/after fixtures, contextual cases, data conversion, legacy handler
translation, owner-visible provenance and rollback. Preserve today's judge
fallback, invalid-node-row fallback, conditional defaults, machine choices,
existing tool-plan approval and security ceilings. Do not import hypothetical
tracker permissions or change the current branch/rebase advice by default.

For each migrated key, one gateway transaction validates and copies
`owner_settings`, `graph_node_settings` or `project_policies` values with their
scope mapping and provenance. Switch every writer, undo path and reader of that
key together. Legacy endpoints become adapters over `setting_values`; they do
not keep independent authorities. Leave unmigrated keys alone. Copy graph undo
metadata before old rows become read-only. Audit the cutover itself.

Rollback must preserve edits made after cutover. Keep old rows as a backup of
the cutover, not a runtime fallback. A supported rollback command exports the
latest typed values and history, then transactionally projects every migrated
key into its old stores before running a compatible older binary. Reject a
rollback conversion that cannot represent a new scope or value, with the exact
keys; retain the new binary and recommend a forward repair. Do not let an old
binary silently open a newer incompatible schema or revive stale pre-cutover
rows. There is no promise of arbitrary binary downgrade. Test one supported
rollback before each family switches. Temporary compatibility projection during
that operation is distinct from indefinite dual-write.

Changes to actual setup writes later update reference docs and uninstall code
together. Changes to actual security gates later update security docs. This
design-only rewrite changes none of them.

## Review disposition

All seven sections of the independent review are answered here. The four owner
decisions above take precedence over the first version and review alternatives.

| Review section | Decision and reason |
|---|---|
| 1. Express all rows | Accept float, keyed schemas, repo_agent and session context. Adapt edges to stay graph records and machine to stay capability. Algorithms, structural changes and disk advice keep their mechanisms; the ledger identifies each. |
| 2. Resolution and tightening | Accept home-bound defaults, structured escalation and ordered merge authority. Add joint behavior, fact-change revalidation and fallback parity. Replace descendant rejection with the owner's transactional child adjustment. |
| 3. Storage and cutover | Accept new tables, GraphUndo integration, legacy adapters and open hello. Add an explicit supported rollback conversion; no silent downgrade or permanent dual authority. |
| 4. Offline/API | Accept pushed snapshots in daemon.db and local batch reads. Reject per-key daemon RPCs and blanket fail-closed because they would interrupt ordinary local work. |
| 5. Guidance | Accept network-only globals, scoped turn/brief injection, typed slots, marker-based uninstall and Go/TS parity beyond Block fixtures. |
| 6. Step 3 detail | Accept exact schema, tagged encoding, global revision, lookup contract, visibility and gateway-atomic writes. Approval wiring remains later; its transaction contract is specified now. |
| 7. Top five | Accept the priorities, with machine/edge adaptations and the owner's child-adjustment rule as stated below. |

For the review's top five changes, in order:

1. Add float, typed keyed records, repo -> agent -> session/run chains and
   SessionID/Harness/NodeID input; leave machine outside inheritance and edge
   records in the graph.
2. Bind maturity at repo, revalidate maturity/membership edits, split escalation
   and merge authority from advice, and move conflicting children rather than
   refuse their parent.
3. Push atomic, persisted snapshots; local turns and supervision use them or
   shipped defaults, gateway mutations need the gateway, and hello stays open.
4. Add definition visibility, append-only history, graph undo parity and an
   owner approval bound to expected_revision inside the gateway transaction.
5. Restrict global files to network advice; render scoped text per turn/brief,
   use named slots, remove by markers and test Go plus TypeScript consumers.

## Remaining owner choices

The four recorded decisions need no further confirmation. Recommend approving
the new-table storage and the explicit orders described here. They solve the
review without changing current product behavior.

One behavior choice remains before an enforced merge gate: row 129's uncharted
repo fallback says both ask and full today. Recommend `ask` for both consumers
when that mechanism is built. Until the owner chooses, preserve and disclose
the two legacy outputs. The settings build is not blocked by that choice.
The row 28 wording discrepancy also remains visible; recommend deriving its
future number from the actual neighbor budget rather than keeping a second
number in prose. That correction needs its own reviewed text change at cutover.
No new decision is requested about algorithms, keyed records, scopes or offline
behavior; those are concrete recommendations in this design.

## Step 3 scope

This is the next code step in the settings work, not the later repo/agent split
numbered in modularity.md. Build exactly:

1. The gateway revision, values and append-only history tables above, and the
   daemon snapshot table, with transactional migrations in temporary databases.
2. The embedded readable default file loader and typed value codec. Put every
   definition, default, slot contract, visibility and inventory disposition in
   that one standard recipe. Validate all 149 row links and fixed-code reasons.
3. The pure resolver, explicit per-type stricter checks, level-bound default
   functions, scope-lookup interface, current-scope adapter and fixture lookup
   for future team/agent/session/run chains. Return source and revision.
4. The internal transactional write primitive for validation, revision compare,
   history, parent adjustment and undo. Its future approval requirement is an
   explicit input from a trusted gateway authorization boundary. Do not expose
   an unapproved rule-writing endpoint. No CTO/proposal caller yet.
5. Full snapshot assembly, additive link capability/frame, push on sync and
   committed revision, and daemon.db acceptance with an immutable local reader.
   Exercise this through fake gateway/daemon links; no policy caller consumes
   it yet. Hello and upgrade startup remain compatible.
6. Failing tests first for loader/encoding, exact inventory coverage, default
   parity, scope lookup, repo_agent chains, home-bound critical sandbox,
   tightening and child adjustment, coupled values, revision races, history
   immutability, undo, visibility, atomic snapshot consistency, reconnect,
   invalid/newer snapshots, offline restart and pre-sync defaults. Test current
   host-choice preservation and old-hello compatibility. No paid calls or real
   user configuration.

There are no guidance renderers, picker, judge, worker, TUI, CTO or plugin
settings callers in this step. No old settings data is cut over, no approval UI
is wired, and no teams, agents, merge gate or recipe importer are built.
Snapshot transport is infrastructure only. Later callers adopt it family by
family with parity tests.

This proves that the standard recipe can describe every extracted value, that
resolution and child adjustment are deterministic, that a revision is a
consistent view of facts and values, and that local reads survive disconnects
without changing today's runtime behavior. It does not prove new enforcement,
agent obedience or a tracker-driven workflow.
