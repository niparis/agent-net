# 0091 - Bots as agents

Date: 2026-11-08
Status: Accepted by the owner through the CTO. Built.

## The problem

A connected bot app (for example "cursor", which calls the shared MCP server
ingest-forge) was outside the Graph. It could not join a Team, it had no charter
or place, and its MCP grants showed only in Connected apps. The owner wants
roles made from standard parts, so a bot should be a normal node, not a second
structure.

## Decision

1. **A bot app has one agent node.** The node is a row of the existing `agents`
   table with role `bot`. Its ID is the app ID and its display name is the app
   name. No new table or column. Three triggers on `apps` keep it in step, so
   every code path that approves, renames or revokes an app is covered:
   - approving an app of kind `bot` creates the node (owner apps get none),
   - a rename of the app renames the node (`apps.rename`),
   - revoking the app archives the node with the tombstone flag, the way agents
     are archived, and removes it from its Team. Nothing is deleted.
2. **Backfill once.** Migration 147 gives every active bot app a node and links
   the bot's sessions. On the live gateway that is `app_494a4ad58773c780`
   ("cursor"). Revoked apps get no node.
3. **The session links to the node.** The bot's session sets `sessions.agent_id`
   to the app ID, so its charter and place resolve with the normal code. Its
   messages still route as before: the routing identity of a bot session stays
   empty, so the node changes no request rule.
4. **Teams.** A bot node is an `agent` member of a Team through the existing
   membership operations. A bot can never be a Team or Repo lead: creating a
   Team with a bot owner, `lead_set` with a bot or a bot session, and an undo
   that would restore one are refused. The bot is not offered when a session
   picks its agent, and the role `bot` cannot be given with `agent.create`. The
   owner cannot tombstone a bot node by hand: it is archived when the app is
   revoked.
5. **`calls` edges are derived.** Each granted shared MCP server becomes a
   read-only `calls` edge from the bot node to the Repo that hosts the server:
   the one project of the server's assignment, else the Repo with the server's
   name. If neither exists the edge is not drawn, because the Graph has no
   machine nodes. The edges are built when the Graph is read, with one query
   for all apps and one each for the registry and assignments, whatever the
   number of bots. They are never stored. `edge_set`, `edge_remove` and undo
   refuse the kind. The grant (`g` in Connected apps) is the only way to change
   them.
6. **The Graph shows it.** A bot has a `⚙` before its name in the Team frame and
   on the canvas. Its card shows CLIENT, CONNECTED, LAST USED, `can call: ...`,
   its charter and one dim line: a bot reads its charter only if its own
   instructions tell it to; its grants and app limits are what bind it. The
   card has no repo choices.

## What does not change

A bot gets no new permission. Its grants, call limit and `/mcp` access are the
ones described in `docs/security.md`. A charter does not bind a bot; the card
says so.

## Rejected

- A separate `bots` table or a new column on `apps` for the node: the `agents`
  table already has the role column, and a parallel structure would need its own
  Team, charter and archive code.
- Storing `calls` edges: they would drift from the grants, and every grant
  change would need a second write.
- A machine node as the fallback target of `calls`: the Graph has none.
