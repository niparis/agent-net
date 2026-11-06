# CTO named-level proposals: independent review

Date: 2026-11-06. Reviewer: an independent model, read only. I read
`docs/design/cto-named-level-proposals.md` against `AGENTS.md`, `modularity.md`,
`settings-layer.md`, `cto-approve-in-chat.md`, `repo-agent-split.md`, decisions 0040, 0047,
0056 and 0062, and the Go it cites. I checked every cited line with `grep -n` and did not edit
the design or the code. This is a docs review, so no tests were run.

## What exists today

Most of the current-state summary is right: the router at `cto_tools.go:196`, the resolver
context at `settings.go:612-617`, the effective fields at `:618-623`, the ordinary projection at
`cto_catalog_reads.go:37`, the dropped raised flag at `views.go:30`, the proposal struct at
`proposals.go:290-299` and `:374`, and the dialog at `cto_tools.go:55`.

The substantive error is the team level. The design says "Team is an existing settings level in
this design" (line 18) and names a parallel design at line 19. That file is not in the tree,
there is no team table, no `Team` field in the resolver, and no key lists `team`. See B1.

## Blocking

### B1. The team level has no code and no design

Section: "Scope and decisions" `17-20`; "Naming the level and target" `82-87`, `121-123`;
example `114-119`.
Problem: teams are a prerequisite and the first non-network level, but
`docs/design/teams-relationships.md` does not exist. There is no `teams` table
(`internal/gateway/store.go:769-775` creates only `agents` and `repos`), no `Team` field in
`settings.Where` (`internal/settings/settings.go:612-617`), and no key in
`internal/settings/standard.yaml` lists `team`; the levels are `[network]`, `[network, repo]`,
`[network, repo, agent]`, `[network, repo, agent, run]` and `[network, repo, run]`.
`settings-layer.md:155-157` still says team does not exist. The example at `team studio` cannot
be built or tested in step 6.
Fix: write the team design and land team inheritance before step 6, or cut team and ship
network, repo and agent. Remove the claim that a parallel design exists, and name the key that
would gain the team level, with its definition review.

### B2. The namesake guarantee has no mechanism

Section: "Naming the level and target" `106-110`; "Approval and concurrent changes" `238-252`.
Problem: the proposal stores "the target's identity version", but no such field exists. `repos`
has `id`, `display_name`, `created_at`, `updated_at` and `owner_agent_id`
(`internal/gateway/store.go:772`), and the split keeps the Repo ID equal to the project name
(`repo-agent-split.md:181-183`). A repo deleted and recreated with the same name gets the same
ID, and `repos` has no tombstone (unlike `agents`, `store.go:771`). The recomputed plan would be
identical, so the commit check would apply the old approval to the new object. "Deleting and
recreating a namesake cannot revive it" (`107-109`) has no support.
Fix: define an immutable identity token (a UUID, or `created_at` plus a tiebreaker), store it in
the proposal, bump it on delete and recreate, and leave it alone on rename. Compare it at commit
before the plan. The executor uses the stored typed reference, never the stored name.

### B3. The commit comparison is narrower than the plan the owner sees

Section: "Approval and concurrent changes" `238-252`; "Showing the difference" `168-178`.
Problem: the plan includes affected contexts, totals and named effects, and the text says to bind
approval to "target identity, membership and recipe" (`242-244`). The commit check then compares
only "targets, values, sources or child moves" (`247-248`). A repo or agent that joins the team
after the preview and already holds the target value is not a child move and not a value change.
The owner approved "2 repos, 3 agents" and the write applies to 3 repos, 4 agents. A membership
change and a recipe change have the same gap.
Fix: compare the full plan: affected contexts, explicit expectations, identity tokens,
membership or context revision, and the recipe digest. Any difference aborts with "Settings
changed; review the updated difference".

### B4. Two address forms can disagree

Section: "Naming the level and target" `100-110`; "Advice and rules" `265-272`.
Problem: old proposals carry `level` and `scope` (`internal/protocol/settings.go:6-14`); new
calls carry `target`. The design does not say the two are mutually exclusive, or which one the
validator, preview and executor use. A call with both can be previewed as one target and
executed as another. The same risk exists if the executor re-resolves the name instead of using
the stored typed reference.
Fix: one address form per call. Refuse `target` with a non-empty `level` or `scope`. The
compatibility reader applies only to proposals created before the upgrade. The preview, the
commit check and the executor all use the stored resolved reference.

### B5. Step 3 applies changes before any step shows the difference

Section: "Build steps" `378-386`.
Problem: step 3 stores and applies reviewed plans and adds the commit check. Step 4 adds the
plain renderer. A release at step 3 can rewrite child rows the dialog never showed. Today's
dialog prints tool, action, arguments and reason (`internal/daemon/cto_tools.go:55`), not
inherited changes. "No clipped list may imply that only the visible targets change" (`214-215`)
is not met at step 3.
Fix: move the renderer and the dialog and Inbox binding into step 3, or merge steps 3 and 4.
Keep step 4 for paging, totals and the Inbox fallback. A step that first applies a plan must
also show that plan.

### B6. The child-move engine only handles repos

Section: "Parents and children" `217-235`.
Problem: `writeSettingsTx` moves children only for `level == "repo"` and only for keys whose
levels include repo (`internal/gateway/settings.go:415-480`). It enumerates repos from graph
nodes, projects and sessions (`:438-461`). There is no agent-level child move and no team
descendant query. But `workers.auto_continue` is a live rule with levels
`[network, repo, agent, run]` (`internal/settings/standard.yaml:652-661`), and the resolver
applies agent values (`internal/settings/settings.go:731-735`). A network change to that rule
must tighten repo and agent rows. As written, "a stricter parent adjusts children" fails for
agent rows, and the team case has no descendant list.
Fix: name the descendant query (team members, repos, repo agents, with session and run as
transient inputs) and use it in the planner and the write. Move children for every durable level
the key lists. Add a test for a network `workers.auto_continue` change with an agent row.

## Should fix

### S1. Citation check

The cited facts mostly hold. These are wrong or loose. The first three matter because a builder
will open them first.

| Claim in the design | Cited line | What is there |
|---|---|---|
| "Advice executes as `cto`; any rule takes the proposal path" (`42-43`) | `internal/daemon/settings.go:158`, `:227` | `:158` is inside `settingsRevisionHasRule`; `:227` is the end of `validateSettingsWrite`. The split is `:240-258`. |
| "Other stored levels and keyed items are refused" (`48-49`) | `internal/daemon/settings.go:184` | `:184` is `recipe := settings.Standard()`. The address check is `:204`. |
| "The gateway checks actor against kind" (`44-45`) | `internal/gateway/settings.go:295` | `:295` is blank. The check is `:296` (`actorAllowed`). |
| "Creation canonicalizes, validates, rejects duplicates and caps at 20" (`68-69`) | `internal/daemon/proposals.go:319` | `:319` is `expire`. The rest is `:324-366`; expiry is `:319-321`. |

Loose line drift, where the cited line is a function or case and the fact is a few lines below:
`proposals_tools.go:85` (dry call at `:94`), `cto_tools.go:232` (actor at `:237`),
`worker_machine_settings.go:99` (override loop at `:104-111`), `views.go:18` (field set at
`:30`) and `proposals.go:282` (reason and result at `:290-299`).

The other citations hold, including `internal/gateway/settings.go:415` and `:595`,
`internal/protocol/settings.go:35`, `internal/daemon/proposals.go:374` and
`internal/tui/settings.go:139,525`.
Fix: correct `settings.go:240-258`, `:204`, `gateway/settings.go:296` and `proposals.go:324-366`.
The loose ones can stay if they name a function.

### S2. The main example uses values that do not exist

Section: "Showing the difference" `181-198`.
Problem: `delivery.pipeline.merge.approver` has values `[none, delegate, owner]`, default `none`,
and levels `[network, repo]` (`internal/settings/standard.yaml:54-62`). The example says
"automatic" six times, and "from network" when nothing is stored. An unset value resolves with
source "network default" (`internal/settings/settings.go:673`).
Fix: use the real values, for example "none -> owner", and say "network default" for an unset
value. If it is illustrative, label it and do not use a real key with invented values.

### S3. The MCP schema and catalog changes are missing from the steps

Section: "Build steps" `368-372`, `378-382`.
Problem: `settingsChangesField()` requires `key`, `level` and `scope`
(`internal/mcpserver/cto.go:49-51`). `CTOArguments` validates stored calls against the same
schema. A `target` selector needs a schema change, a catalog change
(`internal/toolcatalog/tools.go:17,46`) and the public parity test. No step names this work.
Fix: add the read schema to step 1 and the write schema to step 3, with the catalog text and the
parity test.

### S4. The plan wire is not defined

Section: "Showing the difference" `162-171`; "Approval and concurrent changes" `238-252`.
Problem: `settings.validate` returns only an error (`internal/gateway/server.go:1568-1573`).
`settings.write` carries only changes, reason and actor (`internal/protocol/settings.go:15-19`).
The commit comparison needs the reviewed plan, or a digest, in the write frame. The design does
not name the field or who compares it.
Fix: define the new field (a plan digest plus explicit expectations), who computes it, and what
the gateway does when it is missing. A daemon cannot send a wider plan than the one it stored.

### S5. The schema migration is not assigned to a step

Section: "Undo, history and audit" `308-313`.
Problem: typed history needs a target kind and the name at the time; the identity token needs a
column; the proposal needs the stored plan and its version. None exist today. No step mentions
an additive migration or a backfill.
Fix: put the migration in step 2 or 3, keep old rows readable, and test an old row and a new row
together.

### S6. Legacy proposal compatibility is not assigned to a step

Section: "Naming the level and target" `109-110`; "Build steps" `382`.
Problem: the design promises "an explicit compatibility reader" for old string-scope proposals,
and step 3 says "Preserve old proposal execution paths". No step owns the reader and no test is
named.
Fix: assign the reader and its test to step 3. The test must store an old network/repo proposal,
upgrade, then validate, render and apply it.

## Minor

- M1. The read items at `139` need recipe hash and context revision fields
  (`internal/settings/settings.go:618-623` has neither), and the machine override at `157-158` is
  display only; the gateway plan cannot see `node.json`, so it must not enter the commit compare.
- M2. Name the advice notice channel at `271-272` and the proposal field that holds the plan for
  the Inbox row at `341-343`. Today the tool result is the notice, and the row has no plan field.
- M3. "Only live keys are offered" (`268`) is about the offer, not the write. The write path does
  not check `live` (`internal/gateway/settings.go:291-300`). Say whether a named write to a
  shadow key is refused or allowed.

## Simplicity: what to drop

The owner asked for four things: a named level, a plain-words difference, advice alone, and
rules with the owner. Keep these with less machinery.

1. **Drop run targets for now.** Run is the one level that is not a stored setting. It needs the
   start-request path, a supersede status and a double-start rule, and it adds the
   approved-but-not-started hole: `worker_starts` is still `open` until the session starts
   (`internal/gateway/store.go:690-694`), and the runner has no superseded status
   (`internal/daemon/proposals.go:520-526,588`). Read-only run values still answer "what will
   this run use". Add editing with the start form.
2. **Drop the opaque selection handle if display names are unique in a kind.**
   `repos.display_name` and `agents.display_name` have no unique index
   (`internal/gateway/store.go:771-772`). Add one, refuse a duplicate, and a `repo shop` or
   `agent shop` selector is enough. The same-named repo and agent already separate by level.
3. **Defer the owner-edit preview** (`354-357`). The owner edits settings directly today. A
   preview for every owner edit is a new two-step flow and not part of the CTO ask. Use the
   preview for CTO proposals first.
4. **Defer undo of undo and the interrupted-apply operation identity** (`325-330`). One undo and
   the proposal `running` status cover the first release.
5. **Drop the line-level text diff** (`212-213`). Exact old and new wording is enough. Added and
   removed lines are a display feature, not a safety feature.
6. **Drop `relationships` from the prerequisites** (`17-18`). The feature uses teams, the split
   and proposals, not a relationship rule.

Keep the named level, the typed reference, the plain-words plan, the advice and rule split, the
owner gate and the stale check.

## Build steps

The broad order is right, but four pieces are missing and one is mis-ordered.

- **Missing:** the MCP schema and catalog work (S3), the plan wire (S4), the migration (S5) and
  the legacy reader (S6). Each is a build item with tests.
- **Mis-ordered:** the renderer must ship with or before the first apply (B5). Move it into step
  3, or merge 3 and 4.
- **Too big:** step 3 carries typed calls, preview versions, the commit comparison, idempotency,
  undo conflicts and recovery. Step 5 adds the TUI chooser, history grouping, inherit-again,
  pending-start editing, supersede, docs and a decision. Split step 5 into 5a (Settings view)
  and 5b (run requests), or drop run.
- **Step 1** is read only and keeps behavior. It needs the team decision and the read schema.
- **Step 2** needs the descendant query and the plan response type. With team and agent
  descendants it is bigger than 1,000 lines.
- **Step 3** changes behavior for new proposals (a stale plan now fails). That is intended, but
  "keep today's behavior" does not hold for it. Say that old proposals keep the old path.
- **Step 4** is display and binding. It keeps behavior once the renderer moves.
- **Step 5** adds the TUI and, as written, a new run-edit behavior. Split it or drop run.

## Verdict

**Ready to build after the blocking fixes. No redesign of the core is needed.** The shape is
right: one typed address, one plan built by the same write transaction, one owner approval,
advice alone, rules only through the owner.

The text cannot ship as written. B1 (team has no code and no design), B2 (the namesake guarantee
has no mechanism), B3 (the commit check is narrower than the plan), B4 (two address forms can
disagree), B5 (step 3 applies before it shows) and B6 (child moves only cover repos) must be
fixed first. Drop run targets for the first release, add the missing MCP, wire and migration work
to the steps, and keep the renderer with the first apply. With that, the design is buildable in
five steps of about 1,000 lines each.
