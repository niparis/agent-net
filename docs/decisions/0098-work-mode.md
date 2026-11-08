# 0098 - Work mode: a session learns who writes code from agent-net

Date: 2026-11-08
Status: Built. The owner asked for it in session tenebrae on 2026-11-08.

## Context

The guidance agent-net writes into each harness instruction file says: hand a
task to a worker only when the owner asks. The owner wants the forge team to
work another way: sessions design, architect and review, and agent-net workers
write all code, with no owner ask per task.

The forge lead had to send 7 orders by hand and edit 8 AGENTS.md files. Nested
repos do not load the parent AGENTS.md, so every member needed its own pointer.
The owner asked how new sessions can know how to work with agent-net without an
explicit instruction. The answer has to come from data agent-net gives each
session on every turn.

## Decision

A new charter attribute, `work_mode`, with two values:

- `workers_code`: the session designs, architects and reviews. Workers write all
  code. No owner ask per task.
- `on_request`: the default, and what an unset value means. A session hands a
  task to a worker only when the owner asks.

It is set at global, Team, Repo or Agent level and inherits like the rest of the
charter. The nearest set value wins, and the charter sources say which level it
came from.

The per-turn charter context prints one line only when the effective mode is
`workers_code`. A normal prompt costs no extra tokens.

The guidance line now reads: "Hand a task to a worker only when I ask, or when
your effective work mode says workers write code."

The owner sets it on the charter form. The CTO sets it without a proposal, like
advice: with `charter_set` at any level, or with `node_settings_set` at the repo
level. The Graph card shows it after the rules. `agent_graph_read` and
`agent_session_get` return it in `charter.work_mode`.

## Why a charter attribute and not a node setting

The node settings store has two writable levels today: network and repo. It has
no Team level, and the write path refuses the agent level. A Team level would
have meant a new level in the resolver, the write checks, the sync and every
reader. The charter already has global, Team, Repo and Agent levels, the
precedence, the sources, the TUI form and the CTO write tool. A charter
attribute is the smallest thing that lets a Team carry the value and its members
inherit it. `node_settings_set` still takes `work_mode` as a door to the repo
level, so the CTO tool the owner knows keeps working.

## Consequences

- No limit and no gate is added. The mode is guidance, like the other charter
  text. A session in `workers_code` can still be told otherwise by the owner.
- An old gateway ignores the attribute; an old daemon prints no line.
- A future Team level in node settings can move the value; the attribute name
  and the two values stay.
