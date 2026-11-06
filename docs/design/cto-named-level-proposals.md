# CTO proposals at a named level

Date: 2026-11-06

Status: design, docs only. This builds modularity step 6.

## Scope and decisions

The CTO changes a setting at a named level. The owner can see what changes,
where today's value comes from, and which contexts use the new value. The CTO
may change advice alone. Every rule change needs the owner.

This extends [modularity.md](modularity.md), "How a recipe is changed", and
[settings-layer.md](settings-layer.md). It uses the proposal runner,
[approval dialog](cto-approve-in-chat.md), and Inbox. It does not add a second
approval system or a recipe import format.

The first release supports network, repo, and agent targets. The teams design
now exists in [teams-relationships.md](teams-relationships.md), but team code
does not. This feature does not depend on team code. Add team targets only
after the teams build lands its step 2, including membership and settings.
That later step uses the team resolver's context and context version; it does
not duplicate membership lookup here. Relationships are not a prerequisite.

The main choices are:

- Name both level and target. `repo shop` and `agent shop` are different.
- Resolve a name once to a typed reference and identity token. A stored
  proposal never resolves its target again by name.
- Build the plan with the same resolver and transaction planner used to write.
- Show all inherited effects before the first release that can apply a plan.
- Keep run values readable, but do not offer run targets or run edits in the
  first release. Run values remain part of the start request.

## What exists today

These references describe the code before this feature. Older designs may
describe work that has since shipped.

`settings_change` enters a settings router
(`internal/daemon/cto_tools.go:196`). The router separates advice writes from
rule writes at `internal/daemon/settings.go:240-258`. The daemon validates
addresses at `internal/daemon/settings.go:204`; the gateway checks actor
against setting kind at `internal/gateway/settings.go:296`. The gateway's
write planner is `internal/gateway/settings.go:234` onward.

Writes address network or repo using `level` and string `scope`. Other stored
levels and keyed items are refused at `internal/daemon/settings.go:204` and
`internal/gateway/settings.go:299`. The resolver context is
`internal/settings/settings.go:612-617`; its effective result is at
`:618-623`. It resolves network, repo, agent, session, and run values at
`:631-735`. There is no team field in that resolver yet.

The CTO reads effective values and sources through `agent_settings_read`,
using the ordinary public projection (`internal/daemon/cto_catalog_reads.go:37`).
The request currently selects a key and repo
(`internal/protocol/settings.go:51`). The projection includes definition,
explicit value, and history, but omits the resolver's raised flag
(`internal/settingsview/views.go:18-30`). Reads may display machine overrides
(`internal/daemon/worker_machine_settings.go:99-111`). Such overrides come
from local `node.json`, which the gateway plan cannot inspect.

Proposals keep the exact call, reason, and outcome
(`internal/daemon/proposals.go:282-299`). Creation canonicalizes, validates,
rejects duplicates, and caps open proposals at 20 at `:324-366`; expiry is
`:319-321`. Approval claims the open row, validates again, and runs as
`owner (CTO proposal p_N)` (`internal/daemon/proposals.go:503`). Settings
validation uses gateway dry validation (`internal/daemon/proposals_tools.go:85-94`).
The dialog currently prints tool, action, arguments, and reason
(`internal/daemon/cto_tools.go:55`). Named impact is missing.

## Naming the level and target

The public read and change calls use one address form. The selector examples
below describe the proposed interface.

| Level | Selector | Owner-facing name |
|---|---|---|
| network | `{"level":"network"}` | `network` |
| repo | `{"level":"repo","name":"shop"}` | `repo shop` |
| agent | `{"level":"agent","name":"shop"}` | `agent shop` |

Network is a singleton and takes no name. Other names are looked up only in
the selected kind. A name in the wrong kind is an error. No tool guesses a
repo because an agent has the same name, or chooses the caller's repo by
default. Do not accept a raw ID in a name field.

The first release requires a unique display name within each kind. The same
name may exist once as a repo and once as an agent. Add a uniqueness check
when display names are created or renamed, and refuse duplicates. Do not use
an opaque selection handle: the typed level disambiguates kinds, and
within-kind uniqueness makes a name sufficient. Unknown targets do not
create proposals or change advice.

Every call has exactly one address form. A new call may provide `target`, or
the legacy `level` and `scope`, but never both. Refuse `target` together with
either non-empty `level` or `scope`. Preview, commit check, and executor use
the stored resolved reference. An explicit compatibility reader handles
proposals created before upgrade; it reads their old network/repo
`level`/`scope` form and never reinterprets it as a new target.

Internally, resolution produces a typed reference containing kind, storage
ID, display name at proposal time, and an immutable identity token. Store one
token per durable target in a gateway identity table keyed by typed kind and
ID; store the network token in that table's singleton network row. Seed
tokens for existing network, repo, and agent records in an additive
migration. A rename leaves the token unchanged. Deletion removes the row;
recreation, even with the same name or storage ID, creates a fresh random
token. The proposal stores the typed reference and token. At commit, compare
the current token before building or applying the plan. A missing or changed
token makes the proposal stale. The executor uses that stored typed
reference, never the name.

A stored address includes key, level, typed target, and item when supported.
This release continues to reject keyed items. The target kind must match the
level. IDs are storage identity, not user-facing names.

Example:

```json
{"area":"values_write","changes":[
  {"key":"delivery.pipeline.merge.approver",
   "target":{"level":"repo","name":"shop"},"value":"owner"}
],"reason":"Shared releases now need the owner's decision."}
```

The definition must allow repo. Supporting a selector does not widen a
key's allowed levels. The binary remains authoritative for kind, type,
allowed levels, slots, bounds, and live status.

## Reading before changing

Extend `agent_settings_read`, rather than restoring the retired `settings`
tool. It accepts the same selector and key filters. A repo read includes its
resolved context. An agent working in several repos must name the work repo
when that context affects the key. It never picks a repo from the agent's
name. Run values remain readable when a read supplies the saved run context;
they cannot be edited here.

Each returned setting includes:

- Key, plain description, kind, allowed levels, and live status.
- Effective value, source level, typed source reference, and readable label.
- Explicit value at the requested address, or "not set here".
- Snapshot revision, recipe digest, and context revision.
- Raised-to-bound flag, inherited bound where applicable, and visible history.

The resolver remains the source of inheritance. Keep today's source text for
older clients while adding structured fields. A source distinguishes
`standard recipe`, `network`, `repo shop`, and `agent reviewer`; never parse
source text to recover an identity. Set rules report each contributing bound
when no single ancestor accounts for the result.

Owner-only preferences remain unreadable and unwritable by the CTO. Public
reads match ordinary agents' reads. Settings hold no secrets, paths, or
private conversation evidence. A machine is not an inheritance level. A
local machine override is shown separately as display-only information. It
does not enter the gateway plan or commit comparison because the gateway
cannot see `node.json`.

## Showing the difference

Extend the existing settings validation/write boundary. The gateway plans
the same transaction it would commit, then returns the plan without writing.
Do not implement child moves again in a display helper. Today's dry
validation calls the write transaction and rolls back
(`internal/gateway/settings.go:202`). Extend that shared planning boundary.

The plan contains exact writes and deletes, old explicit values, before and
after effective values and sources, child rewrites, and affected contexts.
It records the recipe digest and context revision used. The gateway returns
the typed plan wire; the daemon renders it in plain words for the CTO, dialog,
Inbox, and Settings view. The plan wire has a version, the complete plan,
explicit expectations for each changed address (including "no row"), and a
digest over its canonical contents. The daemon stores and sends this exact
wire with the proposal. The gateway recomputes the plan inside the mutation
transaction and compares it with the supplied plan and expectations before
writing. A missing plan, unsupported plan version, or missing expectation on
a new proposal is refused. The gateway does not accept a daemon-supplied
larger or narrower impact list as authoritative.

For each key show:

1. "Change merge approval for repo shop. Rule."
2. Current effective value and source, and explicit value or "not set here".
3. Requested value and effective value afterward with its source.
4. Totals for affected repos and agent work contexts.
5. Named effects and the CTO's reason, clearly marked as the reason.

For example, `delivery.pipeline.merge.approver` has enum values `none`,
`delegate`, and `owner`, with default `none` and levels `[network, repo]`
(`internal/settings/standard.yaml:54-62`). For an illustrative network
default of `none`, a repo proposal could render:

```text
Change merge approval for repo shop. Rule.
Now: none, from network default. Not set here.
Set here: owner. Effective afterward: owner, from repo shop.

repo shop: none -> owner; a row is written.
repo handbook: none -> owner; inherited, no row is written.
agent reviewer in repo shop: none -> owner, from repo shop.

Future work contexts use the new value. Existing runs keep their saved inputs;
bounds apply at the next turn or start.
CTO reason: Shared releases now need the owner's decision.
```

List each affected repo and agent work context once per key. Agent entries
describe contexts, not extra copies of repo settings. Include standalone
agent contexts when the key reaches them. Distinguish the complete known
list from future matching contexts. A parent proposal is not frozen to
today's members forever.

Advice overrides may shield children. Show "unchanged, own advice" where that
explains an omission. A stored write that leaves the effective value
unchanged still matters: show "pins today's inherited value here". A
source-only change also appears. Deleting means "inherit again", not setting
null, empty text, or an empty set. For text, show exact old and new wording;
do not add a line-level text diff. Plain descriptions accompany enum tokens,
and the exact value remains available. Large plans have totals, pages, and a
complete named detail list. No clipped list may imply that only visible
targets change.

### Descendants and child changes

Use one gateway query, `settingDescendantContextsTx`, in both planner and
write transaction. Given a changed typed address, key definition, and
snapshot, it returns every existing durable context reached by that address
in the resolver's allowed inheritance chain. In the first release these are
network, repo, and agent contexts. For a network key whose levels include
repo and agent, return all matching repo contexts and agent contexts, with
the repo context supplied when that agent works in a repo. For a repo
address, return its own context and matching agents working in that repo.
For an agent address, return its matching agent contexts. Use the resolver for
effective values; do not infer contexts from names or ownership. Sessions and
runs are transient inputs to resolution, not persisted descendants or rewrite
targets. The same query supplies the planner's affected-context list and the
write planner's durable child rows.

At every durable lower level a key lists, a stricter parent adjusts stored
children in the same transaction and never refuses because a child is looser.
Today the applicable durable lower levels are repo and agent. For example,
`workers.auto_continue` is a live rule at `[network, repo, agent, run]`
(`internal/settings/standard.yaml:652-661`). A network change must inspect
and tighten both repo and agent rows. Add a test with a network change and an
existing agent row. When the later team step lands, extend the same query
through team context and membership version; do not ship a team selector
before then.

Child history has cause `parent`. Inherited and maturity-derived effects
appear even when no child row is rewritten. Sets use the existing per-type
combination, not an assignment that restores removed items. Relaxing a parent
leaves rewritten child rows alone, and the preview says so. A direct stored
child write that loosens a bound is refused. Transient run inputs are raised
to their bound and flagged. Parent changes never rewrite saved run inputs or
claim to undo work already done.

Protected rule text is different. `stricter: owner` has no prose ordering: an
approved child wording replaces parent wording. A parent text change leaves
it alone. This is decision 0056, represented by
`internal/settings/settings.go:260`. Never ask the model to judge strictness.

## Approval and concurrent changes

Store the resolved call and reviewed plan together. Keep expected explicit
values for changed addresses; missing means no row, not the effective default
(`internal/protocol/settings.go:5`, `internal/gateway/settings.go:308`).
Bind the plan digest to all material inputs and outputs: affected contexts,
explicit expectations, target identity tokens, membership or context
revision, recipe digest, effective values and sources, and child moves. A
membership or context revision change invalidates the plan even when the new
member already has the proposed value. A recipe change invalidates it too.
The machine override is display-only and is excluded.

At approval, the gateway compares the current target token before planning,
then recomputes the full plan inside the mutation transaction and compares
its canonical digest and explicit expectations with the stored plan. If any
material field differs, apply nothing. Show "Settings changed; review the
updated difference" and require another owner action on a fresh preview.
Do not silently refresh under an Approve button. A change outside the plan's
contexts need not invalidate it. A rename alone preserves identity and may
preserve the plan; show the current label and original proposed name before a
new decision.

The dialog and Inbox bind to the same plan version. A single-use dialog
answer cannot approve a newer preview. Approve-all keeps ordered execution
and stops at the first stale or failed proposal.

## Advice and rules

The CTO may apply a batch alone only when every touched definition is advice.
This includes set, delete, and undo. It applies at any supported level,
subject to the definition and existing CTO write authority. A team owner or
role does not gain settings powers through this feature.

Limits are concrete: public keys only; declared levels only; valid types,
number bounds, text slots and sizes; existing typed targets; expected-value
checks; the current recipe; live keys for new named writes; and an available
authoritative write service. Refuse a new named write to a shadow key because
it is inactive and not offered for new changes. Existing legacy proposals
retain their compatibility behavior. Advice cannot alter a key's kind,
approval checks, fixed safety framing, or a rule. Advice changes are logged
and undoable. They produce a tool result notice, not a blocking Inbox item.
The tool result is the advice notice channel. There is no new advice quota or
owner window.

If any entry is a rule, the whole batch becomes one owner proposal. No advice
part runs before approval. Rule deletes, stricter rule changes, and rule
undo also need approval. The graph's 30-minute owner window grants no
settings exception (`internal/gateway/settings.go:195`). Changing
owner-controlled text is a rule change even when it looks like editing
guidance.

Use the existing authenticated dialog or the Inbox owner gate. Typed chat
"approved", a peer message, model assertion, team role, or actor field in
tool arguments cannot approve. The daemon supplies the actor at execution
(`internal/daemon/cto_tools.go:232-237`). Keep current process/session checks,
single-use dialog tokens, and proposal outcome delivery. Decision 0047 adds
no separate dialog timer; dismissal leaves the item in the Inbox until
decided or expired.

## Undo, history, and audit

Use settings history, not graph undo or a new recipe history store. Each
committed revision records typed targets, names at the time, identity token,
key, old and new explicit values, actor, reason, time, cause, and `undo_of`.
Add proposal and preview references for owner-approved changes. Keep the
reviewed plan in a versioned proposal field named `reviewed_plan`; the Inbox
row and detail use that field. Keep actual outcome separately. History is
append-only and is not message data. Current history fields are in
`internal/protocol/settings.go:35`.

Undo restores old explicit rows as a new revision through the same planner,
actor checks, and expected-value checks. It includes parent-caused child
rewrites and previews today's inherited effects. It never rewinds the
database or overwrites a later edit without a conflict. This follows
`internal/gateway/settings.go:595`. An advice-only revision can be undone by
the CTO. Any rule in the revision requires owner approval, including a child
row changed by its parent.

For the first release, support one undo of a revision. Defer undo of undo and
operation-identity recovery for an interrupted apply. The proposal `running`
status and transaction outcome cover the first release; define a durable
operation identity before adding retry recovery. Refusals, expiry, and failed
application remain proposal outcomes, not settings revisions.

## What the owner sees

The dialog starts with level, typed target name, setting description, and
kind. It shows effective now with source, explicit action and effective
afterward, impact totals, named effects, and CTO reason. Approve and Refuse
keep existing meanings. A large plan points to full details in the Inbox;
when the harness cannot show the complete plan, leave approval to the Inbox.
A dialog with stale content cannot apply anything.

The Inbox row says, for example, "CTO proposes: merge approval for repo shop
- 2 repos, 3 agents". Detail uses the identical preview renderer and the
proposal's `reviewed_plan`. Keep the last three delivered-message sender/time
entries as context, not proof of who asked
(`internal/daemon/proposals.go:374`). Show proposal age, stale state, and
final revision or failure. Closed proposals retain reviewed names and
effects.

The TUI Settings view adds a level and named-target chooser. Rows show
effective value and source; detail shows explicit value, inherited bound,
kind, allowed levels, full text, and history. "Inherit again" is distinct
from editing a value. History groups parent and child entries under one
revision, with its proposal and undo action. Existing source and history
rendering is at `internal/tui/settings.go:139` and `:525`.

Owner edits continue through the existing owner gate and planner, but do not
gain a new preview flow in this work. Pending CTO changes link to their Inbox
item rather than appearing effective. Public advice result notices link to
Settings history. Machine controls remain separate from the level chooser.
Offline views label cached values; approval and writes fail without the
gateway. Existing running work may continue under settings-layer rules.

## Build steps

Sizes are rough changed-line estimates including tests and docs. Split
implementation internally as needed, but do not release an applying step
without its required display and checks. Use failing tests first, temporary
stores, and fake harnesses. No default, existing authority, or effective
value changes merely on upgrade.

1. **Named reads and unique typed targets, about 700-900 lines.** Add the
   network, repo, and agent selector and structured read fields. Add the read
   MCP schema, catalog description, and public parity test. Enforce unique
   display names within kind. Add additive identity-token storage and seed
   existing targets. Test same-name repo/agent, duplicate names, rename token
   stability, and delete/recreate token change. No write behavior changes.

2. **Shared impact planner, about 900-1,100 lines.** Add
   `settingDescendantContextsTx`, plan response type, recipe digest, and
   context revision. Return a read-only plan from the existing gateway
   validation boundary. Include repo and agent contexts and complete child
   effects. Test advice shielding, maturity, sets, protected text, parent
   relaxation, deterministic order, and network `workers.auto_continue`
   tightening an agent row. Existing writes keep their present behavior.

3. **Reviewed-plan storage, wire, owner surface, and first apply, about
   1,100-1,400 lines.** Add proposal schema migration and versioned
   `reviewed_plan`; add target, identity token, typed history fields and
   additive migration/backfill while keeping old rows readable. Add the
   target write MCP schema, catalog text, and public parity test. Add plan
   digest and explicit expectations to the settings write wire. The gateway
   checks target token before recomputing and comparing the full plan in the
   write transaction. Add the legacy proposal reader and a test that stores
   an old network/repo proposal, upgrades, validates, renders, and applies
   it. Refuse mixed target and legacy address fields. Include the plain
   renderer, dialog binding, and Inbox binding in this same release as the
   first apply. Test stale identity, changed membership/context, changed
   recipe, changed affected contexts, stale dialog answer, missing plan, and
   old/new rows together. Old proposals keep their old compatibility path.

4. **Settings view, history, and single undo, about 800-1,000 lines.** Add
   target chooser, history grouping, inherited deletion, stale-plan details,
   and one undo through the shared planner. Owner edits keep today's direct
   path. Update reference/security docs if storage or checks require it, and
   record an architecture decision if the implementation changes an
   architectural choice. Test source labels, parent revision grouping,
   advice undo, and rule owner gate.

5. **Team target extension, about 300-500 lines, after teams build step 2.**
   Add team to the typed selector and resolver context only after membership
   and settings exist. Use the team resolver's context and version in
   `settingDescendantContextsTx`, planner, and commit comparison. Add team
   identity tokens and migration/backfill with the team schema, then enable
   team levels only for definitions that declare them. Test membership
   changes, moved descendants, and fresh review after a context-version
   change. This is a later dependent step, not a prerequisite for shipping
   network, repo, and agent.

## Deferred choices

- Run targets and edits are out of the first release. Keep run values
  readable; edit them through the worker start form.
- Do not preview every owner edit. The request is CTO proposals; owner edits
  retain their current gate and planner.
- Do not add undo of undo or interrupted-apply operation identity yet.
- Do not add a line-level text diff. Exact old and new wording is sufficient.
- Do not add relationships as a prerequisite. Their design and build order
  are independent of settings proposals.
- Do not build a second approval protocol, recipe import format, or opaque
  target handle. The existing owner gate and typed unique names suffice.

## Changes after review

- **B1:** Removed team from the first release and current settings levels.
  Team support is a named later step after teams build step 2; it uses the
  resolver context and version.
- **B2:** Defined per-target immutable tokens, storage, rename/delete behavior,
  proposal storage, commit comparison before planning, and typed-reference
  execution.
- **B3:** Bound commit to the full canonical plan: contexts, expectations,
  tokens, membership/context revision, recipe digest, values, sources, and
  child moves.
- **B4:** Made target and legacy level/scope mutually exclusive and assigned
  old calls to a compatibility reader.
- **B5:** Put renderer, dialog binding, and Inbox binding in the same step as
  first apply.
- **B6:** Named `settingDescendantContextsTx`, defined repo and agent
  descendants, and required a network `workers.auto_continue` test with an
  agent row.
- **S1:** Corrected citations for advice/rule routing, address validation,
  gateway actor validation, proposal creation, dialog actor, resolver fields,
  and tool schemas.
- **S2:** Replaced invented values with the real `none` to `owner` example
  and identified an unset value as `network default`.
- **S3:** Assigned read and write MCP schemas, catalog text, and parity tests
  to steps 1 and 3.
- **S4:** Defined the versioned plan wire, digest, explicit expectations,
  daemon storage, and gateway comparison and missing-plan behavior.
- **S5:** Assigned additive identity, proposal, and typed-history migration
  and backfill to steps 1 and 3, with old-row compatibility tests.
- **S6:** Assigned the old proposal reader and validate/render/apply test to
  step 3.
- **M1:** Added recipe hash and context revision to reads; machine overrides
  are display-only and excluded from commit comparison.
- **M2:** Named the tool result as the advice notice channel and
  `reviewed_plan` as the Inbox proposal field.
- **M3:** New named writes to shadow keys are refused; legacy proposals retain
  compatibility behavior.
- **Simplicity:** Dropped run targets, opaque handles, owner-edit previews,
  line diffs, and relationships prerequisites. Deferred undo of undo and
  interrupted-apply identity. Kept a typed reference and full plan binding
  because they enforce the owner-approved target and exact reviewed impact.
