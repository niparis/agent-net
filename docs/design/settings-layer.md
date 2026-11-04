# Settings layer

Date: 2026-11-03
Status: final design for owner review. Documentation only. Nothing is built.

This is the third version. It replaces the second version, which answered the
[independent review](settings-layer-review.md). Every claim about today's code
was checked against the files it names. It serves [modularity.md](modularity.md):
agent-net keeps the mechanisms, a way of working is data, and the CTO controls
that data. Row numbers refer to the 149 opinions in
[modularity-inventory.md](modularity-inventory.md#2-built-in-opinions).

## The owner's decisions

Final, 2026-11-03:

1. The CTO changes advice on its own. Only rules need the owner. Every change
   is logged and can be undone.
2. After the guidance text, the delivery opinions (worker tests, CI, review,
   merge, release) move in the first half of the build.
3. A stricter parent moves its children to the new value and tells the owner.
   It never refuses. Lower-level rule overrides stay few and plain, because
   limits are hard to reason about.
4. One readable file in the binary holds every setting's key, description,
   type, kind, levels and default. That file is the standard recipe. Go holds
   only the per-type "is this stricter" checks and the defaults that depend on
   context.

## The design on one page

```
standard.yaml (in the binary)      gateway.db                     daemon.db (each machine)
definitions and standard values    values someone set, with       last snapshot
                                   history and undo                    |
                                        |   settings.sync frame        v
                                        +-------------------------> resolver: value + source

session and run values arrive with the session or with the worker start
```

- **The file** says what each setting is and what its standard value is. It is
  the owner's way of working, written down.
- **The gateway** stores only values that someone set: a network value that
  differs from the file, or a value for one repo. Each change has an author, a
  reason and an undo.
- **Each daemon** gets every stored value in one snapshot when it connects and
  after each change. It keeps the last one in `daemon.db` and works offline
  from it.
- **One resolver**, the same Go package in the gateway and the daemon, returns
  the effective value for a place and says where it came from.

A different way of working, such as a friend's tracker-driven one, is the same
keys with other values (appendix A). It is applied as network and repo values
through the normal write path, so each value is logged and undoable.

## Advice and rules

| | Advice | Rule |
|---|---|---|
| What it is | Words an agent reads | A choice agent-net enforces, or one only the owner may change today |
| Who changes it | The CTO alone, or the owner | The owner, in the TUI or by approving a CTO proposal |
| Lower levels | Replace it freely | Same value or stricter only |
| Examples | guidance text, notice wording, prompts | sandbox level, merge authority, worker time limits, picker numbers |

The kind says who may change a setting and whether a lower level may loosen
it. It does not claim that agent-net enforces it. Merge authority is a rule:
only the owner may change it today (the CTO's `node_settings_set` is always a
major change, `internal/gateway/cto_graph.go:58`), although agents only read
it as text. Appendix A says for each setting what enforces it today. A rule
that only text carries is a missing mechanism for the
[gap list](modularity.md#two-lists-to-complete), not a reason to call it advice.

## The recipe file

The file is `internal/settings/standard.yaml`, embedded with `go:embed`. It is
YAML because long texts read well as block scalars and the repo already uses
`go.yaml.in/yaml/v3`. The loader accepts known fields only, refuses duplicate
keys and checks every value against its declared type, so YAML's loose typing
cannot slip a wrong value through.

A real entry, with today's values:

```yaml
format: 1
settings:
  - key: workers.sandbox_level
    rows: [52]
    description: How far a worker's permissions are limited.
    kind: rule
    type: enum
    values: [sandboxed, no-network, read-only]   # a rule lists loosest first
    levels: [network, repo]
    default: sandboxed
    by_maturity: {critical: no-network}
    live: false
```

| Field | Meaning |
|---|---|
| `key` | Stable name. Never reused for another meaning. |
| `rows` | The inventory rows it comes from. |
| `description` | One plain sentence, shown in the TUI and to the CTO. |
| `kind` | `advice` or `rule`. |
| `type` | `bool`, `int`, `float`, `duration`, `string`, `text`, `enum` or `set`. `keyed` comes later. |
| `values` | For `enum`: the allowed values. For a rule, loosest first. |
| `stricter` | For a rule with a lower level: `lower` or `higher` for numbers and durations, `true` or `false` for a bool, `fewer` or `more` for a set. Owner-controlled rule text uses `owner`; its repo wording is an owner-approved replacement, not an ordered bound. |
| `min`, `max` | Bounds for numbers and durations. |
| `levels` | Where a value may be set. `network` is always allowed. |
| `default` | The standard value. |
| `by_maturity` | The standard value for repos of a maturity, only where it differs from `default`. |
| `default_go` | A named Go function, for the few defaults that depend on the machine. |
| `slots` | For `text`: the `{{name}}` slots it may use. |
| `visibility` | `public` (the default) or `owner`. |
| `live` | `true` once code reads the setting. |

The file also holds a `ledger` with every inventory row, 1 to 149, exactly once:

```yaml
ledger:
  - {row: 52, keys: [workers.sandbox_level], step: 7}
  - {row: 123, code: "never overwriting git-tracked skills is a safety floor"}
```

Three rules keep the file honest:

- **A value in the file is a value in force.** A setting becomes `live: true`
  in the same change that makes code read it and deletes the Go constant.
  Until then a parity test keeps its `default` equal to that constant. There is
  no second Go table of definitions.
- **No logic in the file.** No expressions, scripts or conditions. The only
  context the file knows is `by_maturity`, `default_go` and text slots.
- **Definitions belong to the binary.** A stored value can change a value,
  never a key's kind, type or levels. Changing those is a code change.

**Values.** The database and the snapshot hold plain JSON, read against the
definition: `true`, `12`, `0.75`, `"30m"` for a duration, `"sandboxed"`,
`["a","b"]`. Integers are decoded without passing through float64. NaN,
infinity and out-of-range numbers are refused. No row means "not set here".
An empty text `""` or an empty set `[]` is a real value.

**Keyed settings** (rows 31, 69, 120, 126: cache windows per provider, scores
per model, skills, MCP servers) hold one entry per item ID. Each entry is its
own row with `item` set, so it has its own history and its own override. A new
model, skill or server needs no release. This type is built in the step that
first moves one of these rows.

## Levels

One chain for every setting:

```
network -> team -> repo -> agent -> session or run
```

A setting uses only the levels it lists. Team and agent do not exist yet. No
value can be stored there until the modularity steps build them. Until then the
chain is `network -> repo -> session or run`.

| Level | Identity today | Where its values live |
|---|---|---|
| network | the literal `network` | gateway `setting_values` |
| team | later, with teams | gateway `setting_values` |
| repo | the graph node name, which is the project name (`internal/gateway/store_graph.go:602-608`); case-sensitive | gateway `setting_values` |
| agent | later, with the repo and agent split | gateway `setting_values` |
| session | the agent-net session ID | with the session (today `sessions.delegation`) |
| run | the worker ID | in the worker start request (today `auto_continue`, `isolation`, model) |

- Work in a repo uses that repo's team. An agent outside any repo, such as the
  CTO or an architect, uses its own team. So two teams never mix.
- A worker that agent Y starts in repo X reads repo X, then agent Y, then the
  run, as modularity.md says.
- Session and run values are not stored as settings. They arrive with the
  session or the start, and the resolver checks them against the levels above.
  A parent change never rewrites them. It applies from the next turn or start.
- A machine is not a level. See "Machine values".

## Resolving a value

The resolver walks from network down to the most specific level that the
setting lists and that the place has:

1. Start with the stored network value, else the file `default`.
2. At the repo, take the explicit repo value. If there is none and the repo has
   a maturity with a `by_maturity` entry, take that.
3. At each lower level, take the explicit value given there.
4. For an ordered rule, each step down keeps the stricter of the value from
   above and the value found here. Advice takes the value found here. A rule
   text marked `stricter: owner` takes the owner-approved repo text; arbitrary
   prose has no strictness order and cannot be set at session or run level.

The maturity value is the repo's default, not a bound. An explicit repo value
replaces it, even when looser, exactly as a node setting replaces it today
(`NodeDefaults`, `internal/protocol/maturity.go:122`). Only the levels above
bind.

The answer carries the value, its source ("network default", "network", "repo
homelab", "repo homelab, maturity critical", "run"), the snapshot revision, and
a flag when a lower value was raised to its bound.

**Example, row 52.** Network has nothing stored, so the file gives `sandboxed`.

| Case | From above | At the repo | Result |
|---|---|---|---|
| critical repo, nothing set | sandboxed | maturity gives no-network | no-network, "repo homelab, maturity critical" |
| owner set the repo to sandboxed | sandboxed | explicit sandboxed | sandboxed, as today |
| owner set network to read-only | read-only | maturity gives no-network | read-only, "network" |

A run value works the same way. If the owner sets network
`workers.auto_continue` to `false`, a start that asks for `true` gets `false`,
flagged as raised to its bound.

**Defaults that cannot be data.** `default_go` names a pure Go function of
declared machine facts. Today only row 59 needs one: the memory reserve is the
larger of 1536 MiB and 10% of RAM. It runs on the machine that uses the value,
with that machine's RAM. Such a function reads no clock, network or disk, and
its tests cover every input.

**Broken stored data.** A stored value that fails its definition, from an old
binary or a hand edit, is ignored and logged, and the level above applies. That
is today's rule for node settings (`internal/protocol/maturity.go:119-127`).

## Tightening: few overrides, children move

A rule lists a lower level only where the owner really varies it today. For
build steps 3 to 7:

| Setting | Lower levels | Loosest to strictest |
|---|---|---|
| `delivery.tests_before_merge` | repo | optional, suite, suite-new-tests, suite-new-tests-review |
| `delivery.merge_authority` | repo | full, delegated, ask |
| `delivery.deploy_rule` | repo | brief, ask, owner |
| `charter.judge_consumers` | repo | free, charter, strict, escalate |
| `charter.autonomy_mode` | repo | wake, off |
| `workers.sandbox_level` | repo | sandboxed, no-network, read-only |
| `workers.auto_continue` | run | true, false |

Every other rule in these steps is network only. A wider level is a definition
change with its own review.

**A stored lower value looser than its bound is refused** with a plain
message, for example "network sets read-only; a repo may only use read-only".
That is a child write, not a parent change, so decision 3 does not apply to it.
A session or run value is not refused: it is raised to its bound and flagged,
so a worker start never fails over a setting.

**A parent that tightens moves its children.** Example: the owner sets network
`delivery.merge_authority` to `delegated`. Repo homelab has an explicit `full`.
In the same transaction the gateway rewrites homelab's value to `delegated` and
records it with cause `parent`. The result, which the TUI shows the owner, lists
every repo whose effective value changes:

```
network merge_authority: full -> delegated
also changed: repo homelab full -> delegated (its own value, rewritten)
also changed: repo docs-site full -> delegated (maturity default early)
unchanged: repo vault (maturity critical: ask is stricter)
```

The child gets the stricter of its value and the new parent value. For an
ordered value that is the parent value. For a set it is the combination, so an
allowlist child never gains an item it had removed.

**Relaxing leaves children alone.** If the owner later sets network back to
`full`, homelab stays `delegated`, because its row now says so. The owner can
delete that value to make homelab inherit again.

**Undo** of a revision writes its old values back as a new revision, through
the same checks, so it can itself move children and be undone.

Other changes that can tighten:

- A release that makes a rule's file default stricter: the gateway runs the
  same child move once at start, with actor `agent-net release <version>`, and
  records it.
- A maturity change only changes defaults, so it rewrites no stored row. The
  effective values follow, and graph history already records the change.

Rules are always the owner's, so the owner is the one who sees the list. No new
notice channel is needed.

## Changing values

Step 5 adds one gateway frame, `settings.write`:

```json
{"changes": [{"key": "delivery.merge_authority", "level": "repo",
              "scope": "homelab", "value": "ask", "expect": "full"}],
 "reason": "homelab now runs the backups", "by": "owner"}
```

- `"delete": true` instead of `value` removes the value, so the place inherits.
- `expect` is the value the writer saw at that address; it is left out when
  nothing was set. The gateway applies the batch only if every address still
  holds its expected value. Unrelated changes in between do not make a waiting
  proposal stale; a real conflict fails it with the current value.
- One transaction does everything: check the actor against the kind, check each
  `expect`, validate type, level and bound, write, move children, append
  history, bump the revision. The gateway store has one connection
  (`internal/gateway/store.go:623`), so writes are serialized. After the commit
  the gateway pushes the new snapshot. A batch that changes nothing bumps
  nothing.

**Who may write.** The gateway trusts the daemon's actor label, exactly as it
does for graph writes today (`graphActor`, `internal/gateway/server.go:1935`):

| Label | Who | May write |
|---|---|---|
| `cto`, `CTO (minor)`, `CTO (owner window)` | the CTO acting alone (the owner window covers graph changes only) | advice |
| `owner`, `owner (CTO proposal p_N)` | the owner, behind the daemon's owner gate | advice and rules |

The daemon turns a CTO write that touches any rule into a proposal, as it
does for a major graph change today. Ordinary agents cannot write settings.

The second version added a separate approval ledger in the gateway. This
version drops it. The gateway cannot authenticate the owner except through the
daemon's owner gate, so a ledger adds a mechanism without adding protection.
The daemon already stores the exact proposed call (`cto_proposals`,
`internal/daemon/proposals.go`), and `expect` makes a stale approval fail
cleanly.

**Undo** is the frame `settings.undo` with a revision. A revision with advice
only may be undone by the CTO. A revision that touched a rule needs the owner.

**History is kept for ever.** Message retention never prunes it.

**Visibility.** Every setting, its effective value, its source and its history
are readable by every agent and the CTO. This follows the owner's rule, given
twice, that every charter and every rule is readable by the whole team
([decision 0041](../decisions/0041-charters-and-rules-are-public.md)). Settings
never hold secrets, tokens, machine paths or learned evidence; those stay where
they are. `visibility: owner` is only for personal owner preferences. The
CTO's `settings` read serves only worker profiles today
(`internal/daemon/cto_reads.go:97-108`, schema at
`internal/mcpserver/cto.go:72`). Step 5 adds effective values there and extends
the parity test `internal/mcpserver/cto_public_test.go`.

## Gateway tables

```sql
CREATE TABLE setting_revision (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    revision INTEGER NOT NULL CHECK (revision >= 0)
);
INSERT INTO setting_revision VALUES (1, 0);

CREATE TABLE setting_values (
    key TEXT NOT NULL,
    level TEXT NOT NULL CHECK (level IN ('network', 'team', 'repo', 'agent')),
    scope TEXT NOT NULL,             -- 'network', a repo node name, later a team or agent ID
    item TEXT NOT NULL DEFAULT '',   -- keyed settings only
    value TEXT NOT NULL CHECK (json_valid(value)),
    revision INTEGER NOT NULL,       -- the revision that wrote it
    PRIMARY KEY (key, level, scope, item),
    CHECK (level <> 'network' OR scope = 'network')
);

CREATE TABLE setting_history (
    revision INTEGER NOT NULL,
    seq INTEGER NOT NULL,
    key TEXT NOT NULL,
    level TEXT NOT NULL,
    scope TEXT NOT NULL,
    item TEXT NOT NULL DEFAULT '',
    old_value TEXT,                  -- NULL: was not set
    new_value TEXT,                  -- NULL: removed
    actor TEXT NOT NULL,
    reason TEXT NOT NULL,
    cause TEXT NOT NULL CHECK (cause IN ('write', 'parent', 'undo', 'release', 'cutover')),
    undo_of INTEGER,
    at INTEGER NOT NULL,             -- Unix nanoseconds, like the other gateway tables
    PRIMARY KEY (revision, seq),
    CHECK (old_value IS NOT NULL OR new_value IS NOT NULL)
);
CREATE TRIGGER setting_history_no_update BEFORE UPDATE ON setting_history
BEGIN SELECT RAISE(ABORT, 'setting history is append-only'); END;
CREATE TRIGGER setting_history_no_delete BEFORE DELETE ON setting_history
BEGIN SELECT RAISE(ABORT, 'setting history is append-only'); END;
```

- SQLite compares text byte for byte by default, so scopes are case-sensitive
  like graph node names. No ID is lowercased.
- The revision goes up once per transaction that changes something. A maturity
  change also bumps it, in the charter's own transaction, with no settings
  history row: graph history records it. The writers of a maturity are
  `setCharterTx` (used by `SetCharter` and the learned changes), `GraphUndo`
  and `RemoveArchitect`, all in `internal/gateway/store_graph.go`. So a
  snapshot never pairs a new maturity with old values.
- `owner_settings`, `graph_node_settings` and `project_policies` keep serving
  every key that has not moved. `owner_settings` keeps its operational state
  (`cto_session`, `worker_rotation`, `onboarding`, backfill flags) for good.

## Snapshot and offline work

The gateway sends the frame `settings.sync` after `session.sync`, in the same
place it sends `mcp.sync` (`internal/gateway/server.go:661-663`), and to every
connected daemon after each committed revision:

```json
{"revision": 42,
 "recipe": "sha256 of the gateway's standard.yaml",
 "values": [{"key": "delivery.merge_authority", "level": "repo",
             "scope": "homelab", "item": "", "value": "ask"}],
 "maturity": {"homelab": "critical", "agent-net": "growing"}}
```

Every daemon gets every stored value. Settings hold no secrets, they are
public by rule, and the set is small. Visibility applies where an agent or the
CTO reads. The gateway builds the snapshot in one read transaction.

The daemon keeps one row in `daemon.db`:

```sql
CREATE TABLE IF NOT EXISTS settings_snapshot (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    revision INTEGER NOT NULL,
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    received_at INTEGER NOT NULL
);
```

- **Accept**: decode the frame, check each value against this binary's own
  file, drop and log any key it does not know or any value that fails, save
  the row, then swap an in-memory pointer. A frame that is not valid JSON is
  dropped whole and the last snapshot stays.
- **Order**: a pushed snapshot with a lower revision than the one held is
  ignored. The first snapshot after each connect replaces whatever is held, so
  a gateway restored from a backup wins.
- **One view per unit of work**: a turn or a worker tick takes the pointer
  once, so it never mixes two revisions.
- **Before the first snapshot** the daemon uses the file defaults.

**Older daemons ignore the frame.** The daemon's read loop has no default case
(`internal/daemon/link.go:76-192`), and `mcp.sync` already works this way. An
older machine keeps its built-in values until auto-upgrade replaces it. No
change to `hello`, no version negotiation. The TUI's settings view names the
machines that run an older version.

| Work | When the gateway is down |
|---|---|
| Turns, hooks, guidance files, worker briefs | last snapshot, else file defaults |
| Worker supervision, time limits, cleanup | the same; never waits for the gateway |
| Settings writes, proposals, worker starts and cross-machine messages | fail, as today |
| Daemon start, `hello`, `session.sync`, upgrade | unchanged; never wait for settings |

A machine that is offline during a change misses it until it reconnects. That
is the price of working offline. Today's policy cache already serves answers
up to 10 minutes old (`internal/daemon/policy.go:25`). Settings reads add no
network call to the 2-second turn hook (`policy.go:63`) or to a worker tick:
they read the local snapshot.

The gateway uses the same resolver over a snapshot of its own tables, rebuilt
at each revision.

## Machine values

A machine says what it can do. It is not a level. `node.json`,
`integrations-local.json`, the daemon's `owner_preferences` and the gateway's
CLI flags stay where they are.

| Today | When its row moves |
|---|---|
| `node.json` workers (rows 53-59, 61): `max`, `idle`, `max_age`, `owner_grace`, `cpu_target`, `mem_reserve`, `queue_timeout`, `start_gap` (`internal/daemon/join.go:57-75`) | The network value comes from the file or a stored network value. A `node.json` value replaces it on that machine, as today. The source says "machine node.json". |
| `node.json` `model_providers` (row 98) | The same: the local order wins on that machine. |
| `integrations-local.json` (rows 101, 117) | The local switch wins; the network value is the intent. |
| `owner_preferences` notifications (row 116) | Stays local. The cooldown becomes a network value. |
| Retention flags (row 115, `cmd/agent-net/main.go:245-248`) | A given flag wins, as today. The network value replaces the built-in default. |

## Guidance and text

Text slots use today's syntax, `{{name}}`. `internal/guidance/guidance.go:34`
already fills `{{reply}}` per harness variant. A text setting declares its
slots. The loader refuses a value with an unknown slot or without a required
one. Go fills each slot with a typed value and decides which blocks to show.
The file supplies only words. Before insertion a slot value is escaped for its
place and agent-net markers in it are neutralized (`NeutralizeMarkers`,
`internal/protocol/trust.go:62`), so a slot cannot forge authority.

**Example, row 3**, with today's text:

```yaml
  - key: guidance.peer_help_direct
    rows: [3]
    description: Tells sessions to answer peers directly.
    kind: advice
    type: text
    levels: [network]
    slots: [reply]
    default: >-
      - **When another agent messages you, help it.** Answering a peer's
      question or request is part of your job. Reply directly {{reply}}. Do not
      wait for me to approve a normal answer.
```

`reply` becomes "(Claude: `SendMessage` to the sender; others:
`agent_message_reply` with the message ID)" in `~/.claude/CLAUDE.md`, and the
Other or Combined wording elsewhere, exactly as today.

Where texts go:

- **User-level files** (`~/.claude/CLAUDE.md`; the Codex, OpenCode and Pi
  `AGENTS.md`; Antigravity's `rules/AGENTS.md`) hold network values only. Every
  repo on the machine shares them, so a repo value there would leak into other
  repos.
- **Repo advice**, and later team and agent advice, reaches an agent in the
  per-turn context, the path the charter already takes (`policyOf`,
  `internal/daemon/policy.go:54`), and in worker briefs. Today that path exists
  for Claude with the policy hook, OpenCode and Pi
  (`SupportFor`, `internal/protocol/delegation.go:227`). Codex and Antigravity
  sessions see scoped advice only in worker briefs until they get such a path.
- In step 4 every guidance key lists `network` only. A key gets a lower level
  only once its per-turn rendering is built and tested.

Keep the markers, the heading and the final newline. When guidance becomes
changeable in step 5, two things change with it: a snapshot that changes a
guidance value makes the daemon rewrite the managed blocks (`SyncGuidance`,
`internal/agentfiles/agentfiles.go:262`), as it does at start; and Antigravity
uninstall must match the markers instead of the default bytes
(`bytes.Equal` at `internal/agentfiles/antigravity.go:162`), or it leaves the
file behind.

Step 4 proves byte-identical output for `Block` in the Claude, Other and
Combined variants, `Apply` and `Remove`, shared symlinks promoted to Combined,
the legacy heading migration and the Antigravity file. Each later text family
adds its own old-output fixtures first: `PolicyText`, `RulesText`,
`MergeApprovalFor`, trust notes, worker briefs and notices, and the TypeScript
copies in the plugins that the protocol tests already compare. Text parity
proves rendering, not that an agent obeys.

## Inventory ledger

The file's `ledger` lists rows 1 to 149, each exactly once; a test checks it.
Unless the table below says otherwise, each row keeps the inventory's key,
kind and value, and its levels are those code supports today (decision 3).
Formulas, matching, detectors and algorithms stay Go; only their numbers and
words move. The 12 security checks listed after the inventory table are not
among the 149 and stay code.

| Rows | Change from the inventory | Why |
|---|---|---|
| 45, 127, 128, 132 | advice becomes rule | Only the owner changes these node settings today; agents treat them as binding. |
| 43 | Maturity stays a charter field in `graph_charter`. Settings read it as a fact. | It is edited with the charter today. |
| 44 | The preset matrix becomes `by_maturity` on each of the five keys. | Each key shows its own column; no separate table. |
| 46 | Autonomy: levels network and repo, as stored today. | It moves to agent with the split. |
| 41, 110, 123 | Stay code. | The repo and agent split is an entity change; an approval binds one action; tracked skills are never overwritten. |
| 50, 106 | Edges stay graph records. Only wording and the default rule move. | Edges are relationships, not values. |
| 136 | `.agent-net/worker.md` stays the repo's own file. | It is in the repo, tracked in git. |
| 82 | Findings and notes stay data. Only limits and thresholds move. | Observations are not settings. |
| 53-59, 61, 98, 101, 115-118 | Network value in the file; the machine or flag wins locally. | A machine is not a level. |
| 28 | Fix the text drift when the CTO brief moves: it says 160 characters, the code cuts at 40. | A text bug, not a choice. |
| 129 | One value. See owner choice 1. | Today two answers. |
| 148 | Key `notices.idle_check_and_wake_text`. | The second version listed a file path as the key. |
| 128 | Key `delivery.merge_authority`, not `delivery.merge_gate`. | It holds today's node setting `merge_authority`; "merge gate" stays the name of the future mechanism. |
| duration rows | A key drops its unit: `workers.idle_timeout_minutes` becomes `workers.idle_timeout`. | The type carries the unit, as in `node.json` ("60m"). |

## Build order

| Step | What | Rows |
|---|---|---|
| 1 | Opinion list | done |
| 2 | This design | this document |
| 3 | Settings core: tables, file and loader, resolver, snapshot push. No callers. | none live |
| 4 | Guidance text read from the file, byte-identical | 1-20 |
| 5 | Changing values: write and undo frames, CTO read and change, TUI view with source and history, guidance file refresh | none new |
| 6 | Delivery: the node settings except sandbox leave `graph_node_settings`; delivery texts and constants | 45, 46, 127-137 |
| 7 | Workers: sandbox with project policies, isolation, time limits and caps with `node.json`, auto-continue, brief | 51-61, 64-67 |
| later | CTO, picker, judge, quotas and providers, approvals and Inbox, notices, skills and MCP (keyed), delegation | the rest |

Step 5 comes before delivery because moving a key means switching its writers,
and `node.settings.set` needs the write path. The modularity steps (repo and
agent split, teams, relationships) add the agent and team levels when they
land. No settings step waits for them.

**Delivery and decision 2.** Step 6 moves what code holds today: worker tests
(`tests_before_merge`, which reaches workers in `RulesText`), review (the
caller checklist, rows 131 and 145), merge authority, deploy rule, branch
preparation and the worker branch, commit and cleanup constants. CI and
release have no code today, so there is nothing to move. Each gets a key with
its mechanism, when the gap list builds it, so the file never shows a rule
that nothing enforces. The owner's "test changed packages only" lives in this
repo's `.agent-net/worker.md` and stays there.

**Each moving step** does, for its keys:

1. Capture today's output as fixtures, before any change.
2. Set `live: true` and delete the Go constant.
3. Copy the old rows (`owner_settings`, `graph_node_settings`,
   `project_policies`) into `setting_values` in one transaction, with cause
   `cutover`, and switch every writer, reader and undo path of those keys at
   once. Old handlers such as `node.settings.get` become adapters over the
   resolver, so an older TUI or daemon still gets the right answer.
4. Leave the old rows untouched, as a backup that is never read again.

Special cases:

- **Graph undo.** `GraphUndo` of a node-setting row written before the cutover
  is refused with a message naming the setting. Node removal (`RemoveArchitect`,
  `internal/gateway/store_graph.go:94`) also removes that node's setting values,
  with history, in its transaction.
- **Project policies** are keyed by machine and lower-case project
  (`internal/gateway/store_policy.go:18-24`). At step 7 the `workers` value
  becomes the repo's value. The strictest policy and legacy node fallback win,
  and the Inbox names machines that become stricter with their old values. A lower-case project maps to its node the way `Graph()` does
  (`store_graph.go:643,720`). Two nodes with the same lower-case name stop that
  key's cutover for the owner to fix. The delegation fields move with the
  repo and agent split.
- **Rollback.** An older binary opens the newer database without complaint
  (`migrate`, `internal/gateway/store.go:650`, only runs missing migrations)
  and reads the old rows, which the cutover left as they were. A downgrade
  therefore loses only the settings changes made after the cutover. There is no
  downgrade tool. Changes made in old rows during a downgrade are not picked up
  again after the next upgrade.

When a step changes what setup writes to a machine, it updates
`docs/reference.md` and the uninstall code. When it changes a limit or a
security check, it updates `docs/security.md`. This document changes neither.

## Step 3 scope

One worker, one run. Build exactly this:

1. **Package `internal/settings`.**
   - `standard.yaml`, embedded, with format 1, the full ledger (rows 1 to 149),
     and five shadow definitions (`live: false`) with today's values:
     `workers.sandbox_level`, `delivery.merge_authority`,
     `workers.auto_continue`, `workers.auto_continue_max` and
     `guidance.peer_help_direct`.
   - The loader: known fields only, duplicate keys refused, every `default` and
     `by_maturity` value checked against its type, `stricter` required for a
     rule with a lower level, slots checked, ledger complete.
   - The value codec for `bool`, `int`, `float`, `duration`, `string`, `text`,
     `enum` and `set`, as plain JSON.
   - `Stricter` per type.
   - `Snapshot` and
     `Resolve(key string, w Where) (Effective, error)`, with
     `Where{Repo string; Session, Run map[string]json.RawMessage; Host Host}`
     and `Host{RAMBytes int64}`. `Effective` holds value, source, revision and
     the raised flag.
2. **Gateway store.** A migration with the three tables. Store functions
   `WriteSettings(changes, actor, reason)`, `UndoSettings(revision, actor)` and
   `SettingsSnapshot()`. A write runs the whole transaction above: actor class
   against kind, `expect`, validation, child moves, history, revision. The
   maturity bump in the three maturity writers named above. No frame for
   writes.
3. **Snapshot push.** `settings.sync` after `session.sync` and after a revision
   bump, through a hook that tests and the maturity bump call.
4. **Daemon.** The `settings_snapshot` table, accept and swap as above, and an
   accessor that returns the current immutable snapshot. Nothing reads it yet.
5. **Tests, each written to fail first:**
   - Loader: refuses an unknown field, a duplicate key, a wrongly typed
     default, a rule with a lower level and no `stricter`, an undeclared slot,
     a missing or repeated ledger row.
   - Codec: round trip for each type; an integer above 2^53 stays exact; NaN is
     refused; `""` and `[]` are values, a missing row is not.
   - Parity: each shadow default equals today's constant
     (`protocol.DefaultsForMaturity` for every maturity, including unset and
     unknown; `AutoContinueEnabled(nil)`; `DefaultAutoContinues`; the
     `guidance.Block` line for all three variants).
   - Resolver: the three sandbox cases in the table above; a run value of
     `workers.auto_continue` looser than its bound is raised and flagged;
     advice is replaced freely; a broken stored row is ignored.
   - Writes: a network change that tightens moves an explicit repo value and
     lists the repo; relaxing leaves it; a looser child write is refused; an
     `expect` mismatch writes nothing; a no-op bumps nothing; the CTO actor may
     write advice and not a rule; undo restores and can itself be undone; an
     update or delete of history aborts.
   - Snapshot: built in one read transaction; a maturity change bumps the
     revision and appears in it; it is pushed after `session.sync` on a fake
     link; the daemon accepts it, keeps it across a restart, ignores a lower
     revision, takes the first snapshot after a reconnect, drops unknown keys,
     and uses file defaults with no snapshot; a daemon that does not know the
     frame ignores it.

   All tests use temporary directories and the existing fake links. No paid
   calls, no real setup.

Not in step 3: any caller, the write and undo frames, CTO or TUI changes,
guidance changes, any cutover, team or agent levels, keyed settings.

**What it proves:** the file can describe real settings of each hard kind
(ordered rule with a maturity default, run-level bool, number, text with a
slot); resolution and child moves are deterministic; a revision is one
consistent view of values and maturity; a daemon works offline from a stored
snapshot. Nothing in the product behaves differently. It does not prove any
enforcement, that agents obey a text, or that a tracker workflow works.

## Owner choices

The four decisions above need nothing more. These remain, each with a
recommendation. None blocks step 3.

1. **Merge rule for a repo with no graph node (row 129).** Today
   `MergeApprovalFor` says "ask the owner" and the trust note says `full`
   (`internal/protocol/maturity.go:286`, `internal/gateway/graphread.go:144`).
   Recommendation: one answer from the resolver, the network value (today
   `full`), the same as a repo with no maturity. Agents already act on `full`
   through the trust note, and "nothing set means the network value" is one
   rule the owner can keep in mind. To make uncharted repos safer, set the
   network value to `ask`; that changes every repo without its own value.
2. **The five maturity-driven node settings are rules** (rows 45, 127, 128,
   132, and 52 already). Recommendation: yes. As advice, the CTO could change a
   repo's merge authority from `ask` to `full` alone, which it cannot do today.
3. **A `node.json` value keeps winning on its own machine**, even against a
   stricter network rule. Recommendation: yes. It is today's behavior, these
   are hardware and local choices, and one simple rule is easier to reason
   about. The TUI shows the local value as the source.
4. **YAML for the file.** Decision 4 allows YAML or TOML. Recommendation: YAML.
   Long texts read better, and the typed loader removes YAML's usual traps.

## Changes from the second version

| Change | Why |
|---|---|
| One level chain instead of four | Team and agent do not exist yet; one chain with per-setting levels covers every case in the review. |
| Session and run values are inputs, not stored rows | They live with the session and the start today; nothing to clean up when they end. |
| `by_maturity` field instead of a Go preset function | The matrix becomes readable data on each key. |
| Approval ledger dropped; per-address `expect` instead of a global expected revision | The ledger added no protection over the daemon's owner gate; a global revision would make every waiting proposal stale. |
| Public by default instead of owner-only for new keys | The owner's rule and decision 0041. |
| Plain JSON values instead of tagged values | The definition already gives the type. |
| No capability negotiation | Older daemons already ignore unknown frames. |
| Joint-constraint mechanism dropped | Rows 64 and 65 are two plain rules; code reads both. |
| Only five shadow definitions in step 3; each family adds its own when it goes live | A value in the file is a value in force; one worker can build it. |
| Inventory table cut to the rows that differ | The ledger in the file holds all 149. |

Errors in the second version found against the code:

- The snapshot was to be pushed "after hello". `mcp.sync` is sent after
  `session.sync` (`internal/gateway/server.go:661-663`).
- Text slots used `{reply_tool}`. Today's syntax is `{{reply}}`
  (`internal/guidance/guidance.go:34`).
- History times were RFC3339 text. Gateway tables store Unix nanoseconds.
- Row 43 listed maturity as a data setting while also treating it as a fact.
  It is a charter field in `graph_charter`
  (`internal/gateway/store_policy.go:90`), edited with the charter, and stays
  one.
- New keys were to start owner-only. That contradicts decision 0041.
- Row 148's key was a file path instead of `notices.idle_check_and_wake_text`.
- It left open whether an explicit repo value may be looser than its own
  maturity default. Today it may: a node setting replaces the maturity default
  (`NodeDefaults`), and the project policy's `workers` value replaces the
  node's sandbox (`WorkerPermissions`, `internal/gateway/store_policy.go:71`).
  This version says so and keeps it.
- `project_policies` is per machine and project, so it can hold two values for
  one repo. The second version mapped it to one repo without a rule for that.

## Appendix A: two recipes

Definitions belong to the binary, so a recipe lists only keys and values. The
owner's recipe is the standard file. The tracker-driven recipe is a friend's
way (work starts from a tracker item, nobody messages peers, merging is decided
by policy) and is applied as network values through `settings.write`; its
owner approves the rule values, as with any rule change. In that excerpt
`default` stands for the network value written. A network value may be looser
than the file's, because the file is not a bound. A rule still takes the
stricter of the network value and the repo's maturity column.

**The owner's standard recipe** (excerpt of `standard.yaml`, today's values):

```yaml
settings:
  - key: guidance.peer_help_direct
    default: >-
      - **When another agent messages you, help it.** Answering a peer's
      question or request is part of your job. Reply directly {{reply}}. Do not
      wait for me to approve a normal answer.
  - key: guidance.start_workers_when_asked
    default: >-
      - **Hand a task to a worker only when I ask.** ...   # today's line, guidance.md:16
  - key: delivery.tests_before_merge
    default: suite
    by_maturity: {early: optional, mature: suite-new-tests, critical: suite-new-tests-review}
  - key: delivery.merge_authority
    default: full
    by_maturity: {mature: delegated, critical: ask}
  - key: delivery.worker_branch_prep
    default: Commit your work there if the task asks for it.
  - key: workers.sandbox_level
    default: sandboxed
    by_maturity: {critical: no-network}
  - key: workers.auto_continue
    default: true
  - key: workers.auto_continue_max
    default: 2
  - key: workers.idle_timeout
    default: 60m
```

**A tracker-driven recipe** (same keys, other values):

```yaml
settings:
  - key: guidance.peer_help_direct
    default: >-
      - **Other agents rarely message you.** If one does, answer it {{reply}}
      in one line and go back to your tracker item.
  - key: guidance.start_workers_when_asked
    default: >-
      - **Start one worker per tracker item assigned to your repo.** Put the
      item's link in the brief.
  - key: delivery.tests_before_merge
    default: suite-new-tests-review
  - key: delivery.merge_authority
    default: ask
  - key: delivery.worker_branch_prep
    default: Rebase your branch on main and push it before you hand back.
  - key: workers.sandbox_level
    default: no-network
  - key: workers.auto_continue
    default: true
  - key: workers.auto_continue_max
    default: 4
  - key: workers.idle_timeout
    default: 30m
```

Where today's code enforces each of these:

| Setting | Kind | Enforced today by | Note for the tracker way |
|---|---|---|---|
| `guidance.peer_help_direct` (row 3) | advice | Nothing: text in `internal/guidance/guidance.md:5`, written into user files by `SyncGuidance` | Fits as text. |
| `guidance.start_workers_when_asked` (row 16) | advice | Nothing: text at `guidance.md:16`. The gateway accepts a start from any non-worker session (`internal/gateway/workers.go:328-336`) | Gap: a start from a tracker item needs intake (row 103). |
| `delivery.tests_before_merge` (row 127) | rule | Text only: `TestText` and `RulesText` (`internal/protocol/maturity.go:198,254`) reach sessions per turn and workers in their brief. Nothing runs tests. | Gap: required CI. |
| `delivery.merge_authority` (row 128) | rule | Text only: `MergeText` and `MergeApprovalFor` (`maturity.go:217,277`), the receiver's rule in the trust note (`internal/gateway/graphread.go:130`). The written approval note exists; no git gate. | Gap: a merge gate decided by CI and review. `ask` is the safe value until then. |
| `delivery.worker_branch_prep` (row 134) | advice | Nothing: worker brief text (`internal/protocol/workers.go:590`), left out for Antigravity. | Fits as text. |
| `workers.sandbox_level` (row 52) | rule | `WorkerPermissions` (`internal/gateway/store_policy.go:71`) picks the level; Codex applies it in its sandbox and Antigravity applies read-only (`internal/protocol/workers.go:230,495`); other harnesses get it as brief text. | Fits. |
| `workers.auto_continue` (row 64) | rule | The daemon (`internal/daemon/worker_auto_continue.go:17-20`); absent means true (`internal/protocol/workers.go:78`). | Fits. |
| `workers.auto_continue_max` (row 65) | rule | The daemon, same check, with `DefaultAutoContinues` (`internal/protocol/workers.go:55`). | Fits. |
| `workers.idle_timeout` (row 54) | rule | The daemon's supervision (`internal/daemon/workers.go:92-94`), with a `node.json` override (`internal/daemon/join.go:60`). | Fits. |

Both recipes fit the same keys. Every row the tracker way cannot express marks
a missing mechanism: tracker intake, CI as a rule, and the merge gate. These
are the gap list's work, not the settings layer's.
