# CTO proposals at a named level

Date: 2026-11-06
Status: design, docs only. This builds modularity step 6.

## Scope and decisions

The CTO changes a setting at a named level. The owner can see what changes,
where today's value comes from, and who will use the new value. Advice may be
changed by the CTO alone. Every rule change needs the owner.

This extends [modularity.md](modularity.md), "How a recipe is changed", and
[settings-layer.md](settings-layer.md). It uses the existing proposal runner,
[approval dialog](cto-approve-in-chat.md) and Inbox. It does not introduce a
second approval system or a recipe import format.

Prerequisites are the settings layer, the [repo and agent split](repo-agent-split.md),
teams, and relationships. Team is an existing settings level in this design.
The parallel `docs/design/teams-relationships.md` design supplies team identity
and membership. This document does not choose team structure or authority.

The main choices are:

- Name both the level and the target. Show `repo shop` and `agent shop` as
  different things, even when their names match.
- Resolve names to typed references once. Keep those references in the stored
  proposal. Renaming a target never redirects an approval to another object.
- Build the difference with the settings resolver and write planner. The CTO's
  reason is separate from the daemon's account of the change.
- Show inherited effects as well as changed rows. A parent change can move
  children. One approval covers that complete, reviewed result.
- Keep run values in the run request. Do not turn a run into a durable setting.

## What exists today

These references describe the code read for this design, before step 6.
Older design documents sometimes describe work that has since shipped.

`settings_change` already enters a settings-specific router
(`internal/daemon/cto_tools.go:196`). For `values_write`, the daemon classifies
keys from the embedded definitions. For `values_undo`, it checks revision
history. Advice executes as `cto`; any rule takes the proposal path
(`internal/daemon/settings.go:158`, `internal/daemon/settings.go:227`).
The gateway checks actor against kind again
(`internal/gateway/settings.go:183`, `internal/gateway/settings.go:295`).

Writes currently address network or repo with `level` and a string `scope`.
Other stored levels and keyed items are refused
(`internal/daemon/settings.go:184`, `internal/gateway/settings.go:299`).
The resolver has repo and agent context plus transient session/run maps, but
no team field yet (`internal/settings/settings.go:612`). Its result already
contains value, source, revision and a raised-to-bound flag
(`internal/settings/settings.go:618`). It resolves defaults, maturity, stored
values and transient overrides (`internal/settings/settings.go:631`).

The CTO already reads effective values and sources through
`agent_settings_read`, using the ordinary public projection
(`internal/daemon/cto_catalog_reads.go:37`). Its request selects a key and repo
only (`internal/protocol/settings.go:51`). The projection includes definition,
explicit value and history, but drops the resolver's raised flag
(`internal/settingsview/views.go:18`). Reads refresh the snapshot and request
history from the gateway; this path fails if history is unavailable
(`internal/daemon/settings.go:136`). Machine overrides may replace the displayed
value and source (`internal/daemon/worker_machine_settings.go:99`).

Proposals keep the exact call, reason and outcome
(`internal/daemon/proposals.go:282`). Creation canonicalizes arguments,
validates, rejects duplicates and caps open proposals at 20; open proposals
expire after 24 hours (`internal/daemon/proposals.go:319`). Approval claims the
open row, validates again and runs as `owner (CTO proposal p_N)`
(`internal/daemon/proposals.go:503`). Settings validation already uses the
gateway's dry validation path (`internal/daemon/proposals_tools.go:85`).
The dialog currently prints tool, action, arguments and reason
(`internal/daemon/cto_tools.go:55`). Named impact is the missing part.

## Naming the level and target

The public read and change tools take a target selector. The examples below
are proposed input, not claims about today's schema.

| Level | Selector | Owner-facing name |
|---|---|---|
| network | `{"level":"network"}` | `network` |
| team | `{"level":"team","name":"studio"}` | `team studio` |
| repo | `{"level":"repo","name":"shop"}` | `repo shop` |
| agent | `{"level":"agent","name":"shop"}` | `agent shop` |
| run | `{"level":"run","name":"shop / check login"}` | `run shop / check login` |

Network has one target and needs no name. Other names are looked up only in
the selected kind. A name in the wrong kind is an error. No tool guesses a repo
because an agent has the same name, or chooses the caller's repo by default.
Do not accept a raw ID in a name field as an escape hatch.

Names need not be globally unique. Lookup returns typed, named choices when
it finds several matches. Run choices include the repo, agent, task name and
start time. The CTO selects a returned choice, using its opaque selection
handle. This handle is for tools, never a label the owner must copy or read.
Unknown or ambiguous targets do not create proposals or change advice.

Internally the selector resolves to `(kind, id)`. Repo and agent refs come from
the split, where both can have the same ID string in separate namespaces.
A stored address includes key, level, typed target and item when supported.
The target's kind must match the level. Team and run refs use their own kinds.
Network remains a singleton. IDs are storage identity, not user-facing names.

A proposal stores the typed ref, the name seen when proposed and the target's
identity version. Details show the current name and, after a rename, "called
shop when proposed". Deletion makes the proposal unapplicable. Deleting and
recreating a namesake cannot revive it. Legacy proposals retain the existing
string-scope meaning through an explicit compatibility reader.

Example:

```json
{"area":"values_write","changes":[
  {"key":"delivery.pipeline.merge.approver",
   "target":{"level":"team","name":"studio"},"value":"owner"}
],"reason":"Shared releases now need the owner's decision."}
```

This works only if the definition allows team. Step 6 does not widen the
levels of a key just because the selector supports them. The binary still owns
kind, type, allowed levels, slots, bounds and whether a key is live.

## Reading before changing

Extend `agent_settings_read`, rather than restoring the retired `settings`
tool. It accepts the same named selector as a write, plus key filters. A run
read supplies its saved repo, agent and transient values to the resolver.
A read at repo level stops there; an agent read includes its resolved context.
For an agent working in several repos, the request must name the work repo
when that context affects the key. It never picks one from the agent's name.

Each returned setting includes:

- Key, plain description, kind, allowed levels and live status.
- Effective value, source level and typed source ref, plus a readable label.
- Explicit value at the requested address, or "not set here".
- Snapshot revision, recipe hash, context revision and raised-to-bound flag.
- The inherited bound where one applies, and history visible to this reader.

The source distinguishes `standard recipe`, `network`, `team studio`,
`repo shop`, `repo shop, maturity critical`, `agent reviewer` and the named run.
Do not parse a source string to find an identity. Keep today's `source` text
for older clients while adding structured fields. For set rules, report each
contributing bound when no single ancestor accounts for the result.

The resolver remains the one source of inheritance. Delivery follows the work
repo; behavior follows the acting agent. Allowed levels determine which path
can contribute. Use the team context provided by the team/settings layer.
Do not infer team membership from ownership or combine two teams in this tool.
Session remains a transient resolver input, not a sixth durable target here.

Owner-only preferences remain unreadable and unwritable by the CTO. Public
reads must match ordinary agents' reads. Settings hold no secrets, paths or
private conversation evidence. A machine is not an inheritance level. A local
machine override is shown separately; a network proposal must not claim it
will replace that local value.

## Showing the difference

Add a preview to the existing settings validation/write path. The gateway
plans the same transaction it would commit, then returns the plan without
writing. Do not implement child moves again in a display helper. Today's dry
validation already calls the write transaction and rolls back
(`internal/gateway/settings.go:202`). Extend that shared planning boundary.

The plan contains the exact writes and deletes, old explicit values, before
and after effective values and sources, child rewrites, and affected contexts.
It records the recipe and context used. The daemon renders that plan in plain
words for the CTO, dialog, Inbox and Settings view.

For each key show:

1. "Change merge approval for team studio. Rule."
2. "Now: automatic, from network. Not set at team studio."
3. "Set here: owner. Effective afterward: owner, from team studio."
4. "Changes 2 repos and 3 agents in those work contexts."
5. Named effects, followed by the CTO's reason clearly marked as such.

Example, assuming this ordered rule permits the listed levels:

```text
Change merge approval for team studio. Rule.
Now: automatic, from network. Not set here.
Set here: owner. Afterward: owner, from team studio.

repo shop: automatic -> owner; its own value is rewritten.
repo handbook: automatic -> owner; inherited, no row is written.
agent shop in repo shop: automatic -> owner, from team studio.
agent reviewer in repo handbook: automatic -> owner, from team studio.
agent release in repo shop: automatic -> owner, from team studio.
repo archive: unchanged, already requires owner approval.

Future runs in these contexts use the new value.
Existing runs keep their saved inputs; bounds apply at the next turn or start.
CTO reason: Shared releases now need the owner's decision.
```

Agent entries describe work contexts, not extra copies of repo settings. List
every affected repo and agent context once per key. Include agents outside a
repo when the changed key reaches them. Distinguish the complete known list
from the statement that future matching repos, agents and runs will inherit.
A parent proposal is not frozen to today's members forever.

Advice overrides may shield children. Show "unchanged, own advice" where that
explains an omission. A stored write that leaves the effective value unchanged
still matters: show "pins today's inherited value here". A source-only change
also appears. Deleting means "inherit again", not setting null, empty text or
an empty set.

For text show the exact old and new wording with added and removed lines.
Plain descriptions accompany enum tokens; the exact value remains available.
Large plans have totals, pages and a complete named detail list. No clipped
list may imply that only the visible targets change.

### Parents and children

Keep the settings-layer decision: an ordered parent rule that tightens adjusts
stored children in the same transaction. It does not fail because children
were looser. Child history has cause `parent`. Inherited and maturity-derived
effects are listed even when there is no child row to rewrite. Sets use the
existing per-type combination, not an assignment that restores removed items.
Today's network-to-repo move is in `internal/gateway/settings.go:415`.
Use that same rule over the descendants supplied by the settings layer.

Relaxing a parent leaves rewritten child rows alone. The preview must say so.
A direct stored child write that loosens a bound is refused. Transient run
inputs are instead raised to their bound and flagged. Parent changes never
rewrite those saved inputs or claim to undo work a running worker has done.

Protected rule text is different. `stricter: owner` has no prose ordering:
an approved child wording replaces its parent wording. A parent text change
leaves it alone. This is decision 0056, represented by
`internal/settings/settings.go:260`. Never ask the model to judge strictness.

### Approval and concurrent changes

Store the resolved call and its preview together. Keep expected explicit
values for changed addresses; missing means no row, not the effective default
(`internal/protocol/settings.go:5`, `internal/gateway/settings.go:308`).
Also bind approval to the relevant effective sources, inherited bounds,
child moves, target identity, membership and recipe. Expectations on the
explicit row alone cannot detect a changed parent or a new affected child.

At approval, recompute the plan inside the gateway mutation transaction and
compare its material result with the reviewed plan. If targets, values,
sources or child moves differ, apply nothing. Show "Settings changed; review
the updated difference" and require another owner action on a fresh preview.
Do not silently refresh the plan underneath an Approve button. An unrelated
setting change need not invalidate a plan. A rename alone can keep identity
and effects, while showing the new label before the next decision.

Dialog and Inbox use the same plan version. A single-use dialog answer cannot
approve a newer preview. Approve-all keeps today's ordered execution and
stops at the first stale or failed proposal. It does not weaken these checks.

## Advice and rules

The CTO may apply a batch alone only when every touched definition is advice.
This includes set, delete and undo. It applies at any supported level, subject
to the definition and the CTO's existing settings write authority. A team owner
or role does not gain settings powers through this feature.

The limits are concrete: public keys only; declared levels only; valid types,
number bounds, text slots and sizes; existing typed targets; expected-value
checks; the current recipe; and an available authoritative write service.
Only live keys are offered for new named changes. Shadow definitions remain
visible as inactive and keep their existing compatibility behavior. Advice
cannot alter a key's kind, approval checks, fixed safety framing or a rule.
Advice changes are logged and undoable. They produce a result notice, not a
blocking Inbox item. There is no new advice quota or owner window.

If any entry is a rule, the whole batch becomes one owner proposal. No advice
part runs before approval. Rule deletes, stricter rule changes and rule undo
all need approval too. The graph's 30-minute owner window grants no settings
exception (`internal/gateway/settings.go:195`). Changing owner-controlled text
is a rule change even when it looks like editing guidance.

Use the existing authenticated dialog or the Inbox owner gate. Typed chat
"approved", a peer message, a model assertion, a team role or an actor field in
tool arguments cannot approve. The daemon supplies the actor at execution
(`internal/daemon/cto_tools.go:232`). Keep current process/session checks,
single-use dialog tokens and proposal outcome delivery. Decision 0047 adds no
separate dialog timer; dismissal leaves the item in the Inbox until decided or
expired. Older timeout language in the dialog design is not the current rule.

### Run targets

A run selector names one worker start, never every future run of an agent.
Its setting values remain part of that start request. It must name an actual
pending start with a stable identity and the repo and agent chosen for it.
Show the inherited value and its source, requested run value, effective run
value and any bound adjustment.

For a start already waiting for approval, replace it with a newly reviewed
start proposal containing the run values. Supersede the old proposal so two
approvals cannot start it twice. Approving a settings edit does not approve
worker creation. Advice alone may edit an unapproved draft, but starting it
still uses today's worker-start approval and checks.

A started or finished run is readable, not editable here. Reject the write
with "This run has started; use these settings for a new run". Do not add hot
changes, restarts, cancellations or a durable run override table in step 6.

## Undo, history and audit

Use settings history, not graph undo or a new recipe history store. Each
committed revision records typed targets, names at the time, key, old and new
explicit values, actor, reason, time, cause and `undo_of`. Add proposal and
preview references for owner-approved changes. Keep the reviewed impact and
the actual outcome on the proposal; do not rely on later resolution to explain
what the owner approved. History stays append-only and is not message data.
Today's history fields are in `internal/protocol/settings.go:35`.

Undo restores the old explicit rows as a new revision through the same write
planner, actor checks and expected-value checks. It includes parent-caused
child rewrites in that revision, and previews today's inherited effects.
It never rewinds the database or overwrites a later edit without a conflict.
This follows `internal/gateway/settings.go:595`.
An advice-only revision can be undone by the CTO. Any rule in the revision
requires owner approval, including a child row changed by its parent.

If later edits conflict, undo applies nothing. Offer a separate proposal for
the remaining desired change, not a partial undo disguised as a full restore.
Undo can itself be undone. Undoing a transient draft edit restores only the
pending request and its audit entry; it cannot undo a run that already started.
Refusals, expiry and failed application remain proposal outcomes, not invented
settings revisions. Retrying an interrupted apply must find its prior commit
by operation identity instead of writing a second revision.

## What the owner sees

The dialog starts with level, typed target name, setting description and kind.
It shows effective now with source, explicit action and effective afterward,
impact totals, the named effects and the CTO reason. Approve and Refuse keep
the existing meanings. A large plan points to full details in the Inbox; when
the harness cannot show the complete plan, leave approval to the Inbox.
A dialog with stale content cannot apply anything.

The Inbox row says, for example, "CTO proposes: merge approval for team studio
- 2 repos, 3 agents". Detail uses the identical preview renderer. Keep the last
three delivered-message sender/time entries as context, not proof of who asked
(`internal/daemon/proposals.go:374`). Show proposal age, stale state and final
revision or failure. Closed proposals retain the reviewed names and effects.

The TUI Settings view adds a level and named-target chooser. Rows show the
effective value and source; detail shows explicit value, inherited bound,
kind, allowed levels, full text and history. "Inherit again" is distinct from
editing a value. History groups parent and child entries under one revision,
with its proposal and undo action. Existing source and history rendering lives
at `internal/tui/settings.go:139` and `internal/tui/settings.go:525`.

Owner edits use the same preview and planner, through the existing owner gate.
They do not create a CTO proposal. Pending CTO changes link to their Inbox item
rather than appearing as already effective. Public advice result notices link
to Settings history. Machine controls remain separate from the level chooser.
Offline views label cached values; approval and writes fail without the gateway.
Existing running work may continue under the settings layer's offline rules.

## Build steps

Each step aims for about 1,000 changed lines, including tests and docs. Split a
step if it exceeds that budget. Use failing tests first, temporary stores and
fake harnesses. Keep old network/repo requests and open proposals readable.
No default, existing authority or effective value changes merely on upgrade.

1. **Named reads and typed addresses, about 900-1,100 lines.** Add the selector
   adapter over identities from the split and teams. Extend public views with
   structured sources, bound flags and context. Keep legacy wire adapters.
   Tests cover repo/agent namesakes, ambiguous runs, hidden keys, public-read
   parity and unchanged network/repo resolution. No write behavior changes.
2. **Shared impact planner, about 900-1,100 lines.** Return a read-only plan
   from existing gateway validation. Include inherited effects, child moves,
   text differences and relevant context checks. Test advice shielding,
   maturity, sets, protected text, parent relaxation and deterministic ordering.
   Existing writes still use the same transaction and produce the same rows.
3. **Store and apply reviewed plans, about 900-1,100 lines.** Add typed calls,
   preview versions and the gateway comparison at commit. Route advice alone
   and mixed/rule batches as today. Tests cover concurrent parent and membership
   changes, deleted targets, unrelated revisions, duplicate approval, undo
   conflicts and recovery after commit. Preserve old proposal execution paths.
4. **Dialog and Inbox, about 900-1,100 lines.** Use one plain renderer, totals
   and full named details. Bind dialog answers to preview version. Keep Inbox
   fallback, expiry, dedupe, cap and approve-all semantics. Test unavailable
   elicitation, stale answers, remote owner decisions and long text/large plans.
5. **Settings view and transient run requests, about 900-1,100 lines.** Add the
   target chooser, history links, inherited deletion and pending-start editing.
   Test source labels, parent revision grouping, started-run refusal and
   superseded-start approval. Update reference/security docs and record the
   decision. Default scenes and worker starts retain today's behavior.

## Open questions and recommendations

1. **Ambiguous names:** require globally unique display names, expose IDs, or
   return typed choices? Recommend typed choices with opaque tool handles.
   Owners keep readable names; same-named repos and agents remain separate.
2. **Changed impact while waiting:** apply to current members, reject every
   revision change, or compare material plans? Recommend material comparison
   inside commit. New affected members require review; unrelated edits do not.
3. **Large dialogs:** truncate effects, add another dialog protocol, or use
   Inbox detail? Recommend the existing Inbox when the full plan cannot fit.
   Never ask the owner to approve a hidden tail of changes.
4. **Run changes after start:** add hot editing or limit writes to pending
   starts? Recommend pending starts only. A new run can carry new values;
   editing an existing process is a separate mechanism.
5. **Source detail:** return one label or a full explanation tree? Recommend a
   structured winning source plus contributing bounds and raised flag. Add a
   deeper trace only for a demonstrated explanation gap.
6. **Team context contract:** duplicate membership lookup in proposals or use
   the team/settings resolver? Recommend the resolver. Its result must include
   a context version usable by the commit check. Confirm that interface with
   the parallel team design without defining teams here.
7. **Old pending proposals:** require resubmission or preserve exact calls?
   Recommend compatibility reading and execution for old network/repo calls.
   Label them as legacy previews; never reinterpret their scope as an agent.
   New named calls always use the reviewed-plan checks above.
