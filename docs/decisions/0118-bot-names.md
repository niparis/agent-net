# 0118 - Names for the bots of one app

Date: 2026-11-09
Status: Accepted by the owner (Option B, 2026-11-09). Built.

## The problem

The owner runs several Grok bots (CTO, Trade, Shopping, Devops) through one
connected bot app. All of them were the same agent-net session. A message the
CTO bot sent showed as from Trade, and replies to any of them landed in one
shared inbox.

## Decision

1. **A bot may act under a name.** Every bot tool takes an optional string
   argument `as`: 1 to 32 characters, letters, digits, space, `-` and `_`.
   Names are compared without case ("CTO" and "cto" are one name), and runs of
   spaces count as one space. A bot can also set the name once for a whole MCP
   connection, so it need not repeat it on every call:
   - the HTTP header `X-Agent-Net-As: <name>` on the requests to `/mcp`, or
   - a client name written `as:<name>` in the MCP `initialize` `clientInfo`.

   A per-call `as` wins over the header, and the header wins over `clientInfo`.
   A `clientInfo` name without the `as:` marker (for example "Cursor") is an
   ordinary client name and is ignored. Otherwise every existing bot would move
   to a new, empty inbox the day this ships.
2. **Each name is its own bot session and agent node.** The first call with a
   name creates them. The session is a `sessions` row (harness `bot`, node the
   app ID, native ID `as:<name in lower case>`) named `<name> (<app name>)`. Its
   agent node is an `agents` row with role `bot` and the ID `<app ID>/<name in
   lower case>`, so it is a child of the app's node by its ID. This follows
   decision 0092: a bot never leads, the `calls` edges are derived (only the
   app's node has them, the named node shows the same `can call` list), and
   nothing is deleted.
3. **A call without a name keeps the shared session** of the app, as before.
4. **Messages and inboxes are per name.** A message sent with a name comes from
   that name's session. Replies go to the name that was addressed.
   `agent_messages_find`, `agent_message_get` and `agent_message_reply` see only
   the calling name's messages. The other tools (sessions, graph, settings) read
   the same data for every name.
5. **Rename and revoke.** Renaming the app changes the `(<app name>)` suffix of
   every named session and node. Revoking the app archives every named node with
   the tombstone flag and takes it out of its Teams, like the app's own node.
   Triggers on the `apps` table do both, so every code path is covered. No
   named session is deleted.
6. **Connected apps lists the names** under the bot's row.

The migration adds two triggers and no table or column.

## The name is not a security boundary

All the names of an app share one token, one set of grants and one set of call
and message limits. Any caller that holds the app's token can pass any name,
including the name of another bot of the same app, and read that name's mail.
The name tells the owner and the other agents who sent a message. It does not
keep one bot out of another bot's inbox. This is accepted because all the bots
of one app are the owner's. To keep two bots apart for real, connect them as two
apps.

There is no limit on the number of names beyond the name format and the app's
call limit. The owner can see every name in Connected apps.

## Rejected

- **One app per bot.** Each connection needs an owner approval, and the owner
  chose one shared app.
- **A new `parent` column on `agents`.** Positional inserts into `agents` in
  older migrations would break. The ID prefix carries the parent link.
- **A name from every `clientInfo`.** It moves existing bots to a new inbox.
- **A secret per name.** It would make the name a boundary, which the owner did
  not ask for.
