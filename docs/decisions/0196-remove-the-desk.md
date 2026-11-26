# 0196 - Remove the Desk; what needs the owner goes to the chief

Date: 2026-11-26
Status: Decided by the owner on 2026-11-26, in the agent-net lead's chat: "i want to completely remove it. If something needs me it's sent to the chief". The parts below wait for his go, because a feature freeze is in force.

## Context

The owner, on the Desk: "i hate it so mcuh".

In the 7 days to 2026-11-26, 134 Desk items were put to the owner, about 19 a day. He answered or approved about 10. 27 were approvals that were replaced before he saw them, and most of the rest closed by themselves. 33 were open for him on that day. They came from the chief of staff (30), agent-net's own checks (27), the homelab lead (22) and the agent-net lead (9).

The Desk is two things today:

- A screen and a list for the owner: the TUI Desk and inbox screens, the phone Desk page, and the rule that an item with `next_who` = owner waits for him.
- The store of asks between agents: `inbox_items`, `agent_owner_ask`, `agent_ask_sort`, `agent_ask_intake`, held worker starts, permission proposals, sign-in asks and delivery steps that need the owner.

## Revised the same day: no ask machinery either

The owner, after reading the first plan: "there's a lot of uselerss junk going to my desk. Can we also cutr the machienry behind it ? why cant agents just use messages to send their asks to chief".

So the ask store goes too. An ask is a message: an agent that needs the owner sends a request to the chief of staff with `agent_message_send`, and the request stays owed until the chief replies, as every request does today. The rules that replace the points below:

- No `inbox_items` table, no `agent_owner_ask`, `agent_ask_sort` or `agent_ask_intake`, no ask holder, intake, routing, limits, flush, follow-ups or Desk log.
- The gateway's own automatic items (about 20 producers: brief checks, stalls, owed replies, loops, tree check, waiting work, follow-ups and others) stop. Each one becomes a log line, or one message to the lead that owns the thing, never an item for the owner.
- A model override needs the owner's words. When the caller has none, the start is refused with one line: ask the chief, then start again quoting what the owner typed. Since v0.5.154 his words count from any session, so his reply in the chief's chat is enough. There is no held start.
- A permission proposal or a sign-in that needs the owner is a request to the chief. The chief asks him, and the change is applied with his quoted words.

Points 1, 2, 3 and 5 below still hold. Point 4 (keep `inbox_items`) is replaced by this section.

## Decision (first version)

1. The owner has no Desk. The TUI Desk and inbox screens, their tab, the Desk counts on the Sessions screen and the phone Desk page are removed.
2. Nothing waits for the owner in a list. Each item that today is put to the owner (`next_who` = owner) is sent to the chief of staff as one message instead. The chief decides: it answers what it can, and it asks the owner in its own chat only what only he can decide.
3. The owner answers in the chief's chat. The chief records his answer with the existing answer path, quoting his words. Since v0.5.154, words he typed in any session count as owner words, so a held start or a permission proposal can be confirmed from his reply in the chief's chat.
4. Asks between agents stay. `inbox_items` stays as the store of asks and of their answers, without an owner view. Agent-to-agent asks, the chief's sorting and the CTO's tools keep working.
5. agent-net's own automatic checks (tree self-check, waiting work, brief checks) go to the log or to the lead who owns them, never to the owner.

## What this removes

The TUI Desk and inbox screens (internal/tui/inbox.go, desk.go, desk_chief.go and about 30 goldens), the phone Desk page, the owner routing and owner-only limits in the gateway (parts of store_inbox.go, inbox_limit.go, desk_rules.go, desk_flush.go), and the owner-facing text in docs. Rough size: 3,000 to 4,000 lines out. The ask store and the agent tools stay.

## Size

Measured on main on 2026-11-26: the files named for asks, the Desk, intake, the holder and the chief hold about 13,500 production lines and 13,300 test lines, plus 31 TUI goldens. About 20 other gateway files raise Desk items. Part of the chief code stays (the chief session itself), so the cut is less than the total.

## Parts

1. Stop the noise: the gateway's automatic items stop, each becomes a log line or a message to its owning lead. Nothing reaches the owner from agent-net itself. No screen change.
2. Asks become messages: `agent_owner_ask` and the other ask tools are removed. Agents send requests to the chief. Overrides need the owner's quoted words, and there is no held start. Permissions and sign-in go to the chief and are applied with his words.
3. Delete: the TUI Desk and inbox screens, the phone page, the ask store and its code, the CTO's ask tools, the guidance and docs text, with an appended migration that drops the tables.

Part 1 changes no screen, so the owner sees the noise stop before anything is deleted.
