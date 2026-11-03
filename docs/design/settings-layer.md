# Settings layer

Date: 2026-11-03
Status: design for owner review. No behavior changes are proposed in this step.

This design follows [modularity.md](modularity.md) and the 149 rows in [modularity-inventory.md](modularity-inventory.md). The inventory is the migration checklist, not a promise that every row maps to one scalar. Some rows describe several values or conditional behavior. Split those into separately typed keys, with the inventory row linked to each key. Preserve their present interactions. Advice is what an agent is told. A rule is a check the program enforces. Moving advice into a setting does not make it enforced.

## Model

A definition has a stable key, type, kind, home level, lowest override level, default and validation. Types are bool, int, duration, string, text, list and enum. Durations have explicit units; lists declare their element type and whether order matters. A rule also declares a tightening relation. Defaults capture today's actual value, including conditional defaults where necessary, rather than a desired future policy. A definition may use a named default function over explicit context when a constant cannot reproduce today. Such functions must be deterministic and tested. Do not make arbitrary code or templates into recipe values.

Levels are network, team, repo, agent and run. Only network and repo exist today. Team and agent arrive with the repo/agent split; run overrides are reserved for the later run model. A home is the level where a setting belongs, not necessarily where its default is stored. The binary can supply a default for a repo-home setting. `none` as the lowest override means no stored override; otherwise the definition explicitly lists valid levels between home and that limit. This avoids treating agent and repo as interchangeable. Network-only settings can still have owner-approved network values when their definition permits it; `none` means a fixed shipped value.

## Resolution and tightening

For a request with optional team, repo, agent and run IDs, validate that the IDs belong together. A repo and an agent each have at most one team. Resolve only the chain applicable to the definition: network -> team -> repo -> run for delivery, or network -> team -> agent for agent behavior. A definition must name its chain, so a worker in repo X started by agent Y gets delivery from X and behavior from Y. Never silently combine two branches. Skip absent levels. Reject inconsistent context, unknown keys, invalid types and unsupported levels. An empty value is a value; absence means inherit.

Start with the shipped default, then visit allowed levels from broad to specific. The most specific allowed value wins. Advice can replace its parent freely. Each rule candidate must be equal to or tighter than the effective parent at that point. Check this on write and again on read, because an ancestor may later change. An invalid persisted override is an error, not permission to fall back to a looser value. Ancestor updates must validate all affected descendants in the same transaction, or reject the update and identify the conflicts. This also prevents removing an override from exposing a value that violates a remaining descendant.

Each rule definition supplies its own relation, with equality allowed:

- Bool: explicitly declare which value is tighter, such as false for auto-continue. There is no universal bool order.
- Int and duration: declare lower or higher as tighter and the valid range, units and any special sentinel semantics. Compare normalized values, not strings. A sentinel must have an explicit position; otherwise reject it.
- Enum: declare a partial order or transition table, such as sandboxed -> no-network -> read-only. Incomparable values cannot override one another.
- List: declare set semantics and whether subset is tighter (allowlist) or superset is tighter (required or forbidden actions). Canonicalize elements, reject duplicates, and compare membership. Ordered lists need a definition-specific relation; do not assume a prefix is tighter.
- String and text: equality only unless a definition supplies a well-defined, tested domain-specific relation. Free-form prose cannot prove a stricter rule.

Compound policies become separate keys with independent relations, or a named, tested policy comparator where splitting would lose an invariant. A definition without a sound comparator cannot permit lower rule overrides. Defaults and overrides use the same validation. No comparator implies a security check can be changed.

Return the value and its source level and identity, including `network default` for a shipped default or, for example, `repo agent-net` for an override. Also return definition version. Do not expose hidden node fields or secrets through this API.

Examples, using inventory keys and today's values:

1. `guidance.message_style` is advice text, defaulting to the current instruction to put the point first, include context, and avoid acknowledgements. A repo may replace it; if agent A has a permitted agent override, that text wins with source `agent A`. With no override the exact shipped text wins with source `network default`.
2. `workers.auto_continue_max` is an int rule, default 2, lower is tighter. A repo value of 1 wins over 2; a run value of 0 wins over 1. A run value of 2 is rejected against its effective parent 1.
3. `approvals.owner_only_actions` is a list rule. Its default is today's owner-only action set. Adding an action at a permitted lower level tightens it; removing one is rejected. The winning complete set carries that level's source. The existing owner caller gate remains fixed, independent of this setting.

## Storage and versions

The gateway database is the authority for shared overrides. Today `owner_settings` stores assorted network state, while `graph_node_settings` stores per-node setting strings and provenance. Neither should become a second authority for migrated keys. Introduce one `setting_values` table keyed by definition key, level and stable scope ID, with typed encoded value, definition/schema version, revision, updated time and actor. Network has a canonical scope ID. Keep operational state such as the registered CTO session in `owner_settings`. Leave unmigrated node settings in their current table. For each migrated key, atomically copy and validate existing values, switch all readers and writers, and retain a rollback plan. Do not dual-write indefinitely. Preserve absent-versus-explicit values and current invalid-value behavior during migration; do not silently broaden access.

Definitions ship in the binary from one Go table, including defaults, types, scopes and comparators. Generate the opinion inventory view from that table and maintain row links to source behavior, so a check can detect drift. Compound rows and fixed checks still need explicit inventory annotations. Definition and database schema versions are separate. Migrations are transactional and versioned; unknown versions or keys fail closed for enforcement. Older daemons must not interpret a newer incompatible definition: negotiate supported versions and reject incompatible operations, rather than use a stale permissive default. A default change requires an explicit reviewed migration and tests for existing installations.

A future versioned recipe file maps named levels and scope IDs to these same keys and typed values. Import would validate the entire document and propose a diff through the same approval path, then apply atomically. It is not a second runtime store. File format, import, export and sharing are outside this step.

## Readers, writers and surfaces

Use one shared resolver contract, implemented against a consistent gateway snapshot and used by gateway and daemon callers:

```go
type Context struct { TeamID, RepoID, AgentID, RunID string }
type Source struct { Level, ScopeID string; Default bool }
type Resolved struct { Value Value; Source Source; DefinitionVersion int }
type Resolver interface {
    Resolve(ctx context.Context, key string, scope Context) (Resolved, error)
    ResolveAll(ctx context.Context, scope Context) (map[string]Resolved, error)
}
```

`Value` is a tagged, validated value, not an unchecked `any`. The gateway owns persistence and validation; a daemon calls the gateway resolver rather than independently interpreting rows. If a required rule cannot be resolved, the enforcing operation fails closed. A versioned snapshot may be cached only with explicit freshness and invalidation rules; no stale cache may relax enforcement. Preserve existing operation when the gateway is unavailable until each migration has a tested equivalent failure policy.

Changes to this layer go through owner-approved proposals, including advice. A proposal names key, level, scope ID, old and proposed values, effective source and revision, and a plain-language effect. Revalidate scope, type, tightening and revision at approval and apply atomically. Reject a stale proposal for renewed review. Deletion means inherit and gets the same validation. Existing owner and node setting paths remain in place until migrated; each migrated writer must use this path.

Extend the existing CTO `settings` read and `settings_change` proposal interface in `internal/mcpserver/cto_v2.go` with scoped effective values and sources. Route the proposed operation through the existing dispatcher and `proposalRunner` in `internal/daemon/proposals.go`: validate on submission, put it in the owner's Inbox, and execute only on approval, reporting the outcome to the CTO. Apply existing CTO read-scope filtering. The TUI can add a settings view near its existing settings and project views, with a scope selector, effective value and source; its Inbox proposal shows the before/after and scope. Neither surface writes around approval.

## Guidance

Move the advice lines in `internal/guidance/guidance.md` into text definitions first. Keep their order, punctuation, whitespace, markers, heading and final newline. Render the same block using the resolved text and retain the existing Claude, other and combined reply substitutions. Shared fragments used by trust notes or protocol text need a single definition with explicit consumers, without changing their context or authority. The synchronization switch is a rule, not a guidance sentence. Keep current rendering until the resolver can supply the same defaults.

Before switching, capture today's `Block` bytes for every variant as independent fixtures. Test the new default renderer byte for byte against them, including markers and newlines, and test `Apply` and `Remove` compatibility. A fixture must not be generated from the new renderer during the test. Test each migrated conditional text path as well. These tests prove no default text change, not that agents obey the advice.

## Migration

Each step ships independently, with the old behavior as the default and tests comparing old and new results across meaningful contexts. First add definitions, resolver and source reporting without changing enforcement. Move guidance advice first: it is visible, has deterministic byte-level comparisons, and exercises the model without changing a gate. Then move other advice and notices, preserving templates and conditional text. Next migrate existing repo and network settings, including maturity and skills, one family at a time with transactional data migration. Then move worker, picker, judge, quota, approval and delivery opinions in small coherent groups, preserving evaluation order, defaults, edge cases and enforcement. Migrate only after defining a sound comparator and testing inherited and rejected overrides. Track each of the 149 rows, including split keys and rows that remain fixed, against its consumer and parity tests. Do not silently resolve current inconsistencies, such as the uncharted merge fallback in inventory row 129, as part of extraction.

The 12 security checks and protective limits listed separately in the inventory never become recipe settings. Their gates and bounds remain fixed in code. Existing behavior that is only advice stays advice until a separately designed enforcement change.

## Boundaries and owner choices

This does not build teams, agents, their ownership or membership, relationships, recipe files, a merge gate, or new intake mechanisms. Reserved scopes and explicit chains leave room for those changes without inventing membership now. Named-level CTO changes beyond currently existing scopes wait for those models. The first owner recipe is represented by shipped defaults and existing data, not an imported file.

- **Owner choice: storage.** Recommend a new gateway override table and gradual cutover from existing tables. Alternative: extend `owner_settings` and `graph_node_settings`. One table gives one typed scope and revision model; reuse avoids a migration but leaves two authorities and different semantics.
- **Owner choice: tightening.** Recommend per-definition partial orders and rejecting incomparable values. Alternative: generic comparisons by type. Explicit orders handle allowlists, denylists and sentinels without guessing safety.
- **Owner choice: approval.** Recommend owner approval for every setting write, including advice. Alternative: permit CTO advice changes within limits. A single path is auditable and matches this step's write constraint; delegation can be designed later.
- **Owner choice: ancestor changes.** Recommend rejecting changes that conflict with descendants and showing the conflicts. Alternative: automatically remove or clamp descendants. Rejection preserves owner intent and avoids silent changes.
- **Owner choice: defaults and inventory.** Recommend one Go definition table with a generated inventory view and reviewed conditional default functions. Alternative: a separate recipe or inventory file as source. One source makes drift testable while preserving binary defaults before file recipes exist.
- **Owner choice: rollout.** Recommend guidance first and parity before each cutover. Alternative: migrate all settings at once. Small changes make discrepancies and rollback local.
