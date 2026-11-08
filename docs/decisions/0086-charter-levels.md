# 0086 - Charters inherit through global, team, repo and agent

Date: 2026-11-08
Status: Accepted by the owner

This supersedes the charter parts of 0009 and 0035 where they disagree.
Part A implements storage and readers. Part B will draft a charter from AGENTS.md.

## Fields

Purpose describes the repo or team. Maturity is a repo enum: experimental,
early, growing or mature. A separate maturity_note holds its optional explanation.
Accepts, refuses, escalate and feature_requests describe the agent's work.
Rules is free text for taste and house rules at any level. Code never interprets it.
Repo and team refuses are safety limits which agents inherit.

An absent field inherits: global -> team -> repo -> agent. Refuses is the union,
with every contributing value and source retained. If an Agent and its Repo
belong to different Teams, both Teams retain their safety refusals. Other fields take the nearest
value. An active team supplies the default escalate text "ask the team lead".
No charter is valid and adds no limit, approval or prompt.

## Storage

Keep graph_charter and agent_charter, their keys and their history. Add
charter_values(level, scope, attr, value, updated_at, updated_by) for global and
team values. Its key is (level, scope, attr); global scope is empty. Team purpose
stays in teams.purpose so existing team tools and undo keep working. This adds
one small table without copying the existing repo and agent values. No column
is added, so the replay guard is CREATE TABLE IF NOT EXISTS rather than an ALTER
TABLE column check. The append-only migration lock covers the new migration.

The effective charter carries sources per field: level, scope and value. For
refuses, sources includes every contributor even when equal values are displayed
only once. A session resolves with its current repo and agent. A standalone graph Agent inherits its sole owned Repo when unique. With
multiple owned Repos it uses its direct Team and Agent values; the session view
has the explicit Repo context. A repo context uses its Team; without repo membership it falls back to
the Agent's direct Team. Team membership is not a grant of tool authority.

## Migration and compatibility

The migration takes the first word of old maturity text as its enum. Unknown
words, including the removed critical level, become early. Text after " - "
becomes maturity_note. Consumers, needs and notes move into rules under
"Consumers:", "Needs:" and "Notes:" headings, in that order. Existing rules stay.
The dropped attributes are then deleted and cannot be written. Replaying the
migration does not duplicate the text. No backup is made by this migration;
the release step backs up the database.

The wire structs retain the old empty fields for inexpensive client
compatibility. Existing repo-level work boundaries survive; the owner can move
them to Agent scope. Historical maturity parsing still supports old recipe
fixtures and clients; new charter writes accept only enum tokens. Explicit
settings remain independent of the charter. Rules text grants no permissions.

New writes with an explicit level use the fields for that level. Legacy
unscoped writes and typed Agent clients keep their inexpensive compatibility
path. The removed attributes are rejected on every path. Existing learned
boundary text uses rules instead of producing dropped charter attributes;
this text never sets a delivery or tool permission.
