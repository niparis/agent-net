# 0088 - Sessions draft charters from AGENTS.md

Date: 2026-11-08
Status: Accepted by the owner (part 6 of the org design, "Charter v2 B")

This builds on 0086. Part A gave charters four levels. Most repos and agents
still have empty or thin charters. Part B fills the gaps without a model call
of its own: the session that knows the repo drafts the text, and the owner
reviews it.

## The request

agent-net never calls a model to write a charter. When a repo or an agent has
no own value for a field, the gateway asks a live session of that repo.

- The ask rides on the normal per-turn text. The daemon sends `policy.get`
  with `turn: true` and `agents_hash`, the hash of the AGENTS.md (else
  CLAUDE.md) it read in the repo root on its machine. The answer may carry
  `draft_request`. The daemon adds it to that turn's charter text and keeps it
  out of its policy cache, so a stale answer never repeats it.
- Asked fields, repo level: purpose, maturity, refuses. Agent level: accepts,
  refuses, escalate, feature_requests, rules. Only fields with no own value at
  that level are named. Inherited values do not count.
- Once per repo and once per agent for one hash. The table
  `charter_draft_asks(level, scope, source_hash)` holds the last hash. A new
  request needs a different hash, which means AGENTS.md changed. No AGENTS.md
  means no request. Workers, the owner, the CTO and bots are never asked.
- The text the session gets starts `[agent-net] Charter draft request.` It
  says to read AGENTS.md (and README.md if needed), lists the empty fields with
  the charter guide's meaning and size for each, and says to call
  `agent_charter_draft` once per field. It adds that a draft is a suggestion,
  that nothing changes until the owner accepts it, that a draft never grants
  or removes authority, and that agent-net asks once.

## The draft tool

`agent_charter_draft {level, attr, value, team?}` is open to agent sessions and
refused for the command line, like `agent_learn`. The scope is never a
parameter: it is the caller's own repo, the caller's own agent, or a team the
caller leads. The tool exists because ordinary sessions have no charter write
tool; only the owner and the CTO do. It writes nothing to a charter.

- The value goes through the existing `CleanCharterValue` (2 KiB, control
  characters removed, maturity is an enum). Empty values are refused.
- A field that already has an own value is not drafted over.
- A new draft for the same field replaces the pending one. A value the owner
  rejected is not stored again; the tool answers `rejected`.
- Drafts live in `charter_drafts(level, scope, attr, value, source, source_hash,
  status, drafted_by, decided_by)`. Status is pending, accepted, rejected or
  superseded. A pending draft whose field gets a value some other way becomes
  superseded and leaves the lists.

## Accept and reject

A pending draft is a suggestion. It is not part of any effective charter.

- It appears in the owner's learned list (`learned_list`) as a major
  suggestion with `origin: draft`, `level`, `scope` and `by: AGENTS.md`. Its
  ID is the negative of the draft ID, so the existing `learned_answer` path
  routes it. This reuses the Inbox row and its accept and reject keys.
- The charter card shows a section "Drafted from AGENTS.md" with each draft.
  `y` accepts the selected one, `x` rejects it, `,` and `.` choose. The
  approved key map is unchanged: the card gained `y` and `x` only.
- Accept calls the normal `SetLevelCharter` as `owner` or `cto`, so the value
  lands at the draft's own level (repo, agent or team) and shows in the
  effective charter with that level as its source. The draft row keeps
  `source` and `decided_by`.
- The CTO can accept by writing the value with `charter_set`, which it
  already has. A CTO tool that lists and decides drafts is not built; the
  owner's list and card are the path now.

## Team charters

The team lead session drafts the team purpose and team refuses from its
members' charters. A lead is the session whose agent owns the team (and holds
`lead_session` when set).

- The ask names only empty team fields, lists the members, and tells the lead
  to read their charters with `agent_graph_read`. Team refuses is a safety
  limit that every member inherits, so the text says to list only what the
  members already refuse.
- It is asked when at least one member has a purpose or refuses of its own.
  Its hash is taken from the members' own purpose and refuses values, so it
  is asked once for that set and again only when those values change.
- Source of a team draft is "member charters". Accept writes `teams.purpose`
  or `charter_values` through the same team write as before.

## Escalate default

0086 already supplies "ask the team lead" as the default escalate text for a
session in an active team. That is the "your lead" default of the design
table. Nothing changed for it.

## Staff charters

Staff charters (chief of staff) come with part 5 of the plan. They are not
built here.

## Limits

No new limit, check or prompt. Drafts never touch settings. Rules text is free
text and never grants or removes authority. The only checks are the existing
charter value cleaning, the per-level field lists for drafting, and the rule
that a draft goes to a field that has no value.

## Migration

One new migration at the end creates `charter_drafts` and
`charter_draft_asks`. All statements are `IF NOT EXISTS`, so replay is safe.
The `charter levels` replay test now rewinds to that migration by marker,
not by position.
