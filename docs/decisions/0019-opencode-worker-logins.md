# 0019: OpenCode workers get a copy of all the owner's OpenCode logins

Status: accepted, to revisit (2026-10-30)

## Context

Each OpenCode worker runs its own `opencode serve`. Until now all of them shared OpenCode's data folder (`~/.local/share/opencode`) with the owner's own OpenCode service. That folder holds one SQLite database for every session. Two problems came from it:

- **Write collisions.** With several OpenCode servers writing at once, a worker's run failed with `effect/sql/SqlError: Failed to execute statement`.
- **Ghost sessions.** The owner's OpenCode service saw the workers' sessions in the shared database, and its agent-net plugin listed them as ordinary sessions after the workers ended.

The fix is a private data folder per worker. But OpenCode 2.2 keeps its provider logins in that same database, so a worker with an empty one cannot call any model. The logins have to be copied in: at each worker start, `opencode auth export --standalone` from the owner's data and `opencode auth import --standalone` into the worker's private database (mode 0600). The copy goes through memory, never through a file.

## The risk

Logins are of two kinds:

- **API keys and the OpenCode Console login** are fixed values. A copy is harmless.
- **OAuth logins** (for example OpenAI and Google) hold a short-lived token and a refresh token. Many providers, OpenAI included, accept each refresh token only once. When a worker's copy refreshes first, the provider can invalidate the refresh token that the owner's own OpenCode still holds. The owner's OpenCode is then signed out of that provider and the owner must sign in again.

This is a reliability risk, not a leak: the copies stay on the owner's machine, readable only by the owner, and are deleted with the worker's folder. It comes from holding one OAuth login in two places. Pi and Codex workers are not affected: they keep logins in a separate file and use the owner's login directly, with no copy.

## Options

1. Copy only API keys and the OpenCode Console login. No sign-out risk. OpenCode workers cannot use OAuth providers; OpenAI models go to Pi or Codex workers instead, and agent-net should hide OAuth-only models when it picks a model for an OpenCode worker.
2. Copy all logins. OpenCode workers can use every provider the owner can. Accepts the sign-out risk for OAuth providers.
3. Keep the shared data folder. No copy, but the write collisions and ghost sessions stay.

## Decision

Option 2, chosen by the owner for now. Workers can use every provider.

## Revisit when

- the owner's OpenCode is signed out of an OAuth provider while OpenCode workers ran: then switch to option 1;
- OpenCode stores logins outside its session database, or offers a shared credential store: then stop copying.
