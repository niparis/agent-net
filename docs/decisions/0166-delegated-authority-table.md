# 0166 - Delegated authority: one never list, one scope table

Date: 2026-11-14
Status: Approved by the owner on 2026-11-14 ("go T5"). Hand-back 1 built the
list, the method, the Claude hook and the text. Hand-back 2 built the agent-net
holds and the Codex and OpenCode answers.

## Problem

The owner, 2026-11-14: "It's not about checking and forbidding. It's about
delegating my authority (conditionally of course) so that a headless session
can work without its guardrails being constantly triggered."

Scopes were stored on role nodes, and nothing read them. "Only the owner may"
lived in five places: the CTO hook's list, the CTO's irreversible list, the
proposal classes, a gateway refusal and the CTO's instructions.

## Decision

- **One never list**, in `internal/authority/never.go`. It holds the
  owner-only CTO actions and the shell command patterns for deleting data,
  spending money and using secrets. The five old places read it and their own
  copies are gone.
- **One scope table**, in `internal/authority/table.go`. It says what
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

## The holds and the other harnesses

- **Worker start that names a model** passes when the caller's role node holds
  `admin` over the project. **A permission proposal** is approved by the
  requester's own `permissions` scope over every target repo, never for folder
  trust, a whole-tool rule or a never-list command. **An owner ask** goes to a
  live session of a node whose `asks` scope covers the asker's repo, before the
  chief. The order is the owner's words, a scope, the chief, the Desk.
- The CTO's own permission proposals are never approved by the scope path, even
  though its node holds permissions over everything: only its verified owner
  words (decision 0165) approve them.
- **Codex** command approvals are accepted once when `tool_decide` says allow.
- **OpenCode 2.3** has no hook that can answer allow. The plugin forwards a
  shell permission request, and on allow the daemon replies `once` through
  `POST /api/session/{sessionID}/permission/{requestID}/reply`. The route and
  its body were read from the OpenAPI document of a real 2.3.0 service. The
  daemon test runs against a fake service with that route.
- **Pi** never asks permission and needs no code.

## What this does not do

- A holder session has no tool to answer a permission ask: the permission hold
  reads the requester's own scope, and the ask hold lets the holder close the
  ask. If the owner wants a lead to approve its team's proposals, that needs an
  answer tool and is a separate decision.
- The ask hold has no timeout: a holder that stays silent keeps the ask. The
  owner can still answer it on the Desk.
- A permission request that OpenCode raises for something other than one shell
  command, or with a pattern list, is not forwarded.

- The CTO's hook now depends on its role node holding `admin`. A migration seeds
  that scope for the node with the role `cto`.
- An agent-net tool is allowed by the scope kind alone. The area is checked for
  shell commands only, because a tool call carries no folder.
- The deploy row allows the repo's `scripts/deploy*.sh` only. A repo's deploy
  rule is prose, not a list of commands.

## Checks

`internal/authority` has table-driven tests for every row, the compound
and never-list cases, and the one list. The daemon test proves a removed scope
stops allowing without waiting for a cache to run out, and that a cached answer
is fast.
