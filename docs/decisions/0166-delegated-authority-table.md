# 0166 - Delegated authority: one never list, one scope table

Date: 2026-11-14
Status: Approved by the owner on 2026-11-14 ("go T5"). This is hand-back 1 of
the build: the list, the method, the Claude hook and the text. The agent-net
holds, Codex and OpenCode follow.

## Problem

The owner, 2026-11-14: "It's not about checking and forbidding. It's about
delegating my authority (conditionally of course) so that a headless session
can work without its guardrails being constantly triggered."

Scopes were stored on role nodes, and nothing read them. "Only the owner may"
lived in five places: the CTO hook's list, the CTO's irreversible list, the
proposal classes, a gateway refusal and the CTO's instructions.

## Decision

- **One never list**, in `internal/daemon/authority/never.go`. It holds the
  owner-only CTO actions and the shell command patterns for deleting data,
  spending money and using secrets. The five old places read it and their own
  copies are gone.
- **One scope table**, in `internal/daemon/authority/table.go`. It says what
  each scope kind allows. The decision method and the text both read it.
- **`tool_decide`**, a local daemon method. It answers allow or no decision from
  the scopes of the session's role node. It never answers block. An unknown
  session, no scope, a never-list match, a compound command or an unreachable
  gateway is no decision, and then the harness behaves as before.
- **The scopes travel with `policy.get`.** The daemon caches them like the turn
  policy. A scope write tells the daemons to drop their caches, so a removed
  scope stops allowing at once.
- **A Claude hook**, `agent-net tool-decide`, for `Bash` and agent-net tools. The
  CTO's launch hook is the same command; its own `cto-hook` and list are gone.
  Setup adds the hook without a question, because it can only remove a prompt
  the owner already delegated.
- **A "Delegated authority" line** in the charter text of every session whose
  node holds a scope.

## What this does not do

- It does not change the agent-net holds (worker starts that name a model,
  permission proposals, owner asks). That is the next hand-back.
- It does not answer Codex or OpenCode permission requests yet.
- The CTO's hook now depends on its role node holding `admin`. A migration seeds
  that scope for the node with the role `cto`.
- An agent-net tool is allowed by the scope kind alone. The area is checked for
  shell commands only, because a tool call carries no folder.
- The deploy row allows the repo's `scripts/deploy*.sh` only. A repo's deploy
  rule is prose, not a list of commands.

## Checks

`internal/daemon/authority` has table-driven tests for every row, the compound
and never-list cases, and the one list. The daemon test proves a removed scope
stops allowing without waiting for a cache to run out, and that a cached answer
is fast.
