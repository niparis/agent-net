# 0177 - Owed replies

Date: 2026-11-20

## Context

PN50 Stage 2 stalled for about 80 minutes (04:22 to 05:40 UTC, 2026-11-20). The homelab lead sent request `m_0f9947fe3eff6b6b` to `homelab-agent 2` (OpenCode). At 04:20:37, the recipient sent a reply that started with "Progress, not a hand-back". In agent-net, any reply closed a request by moving its state to `replied`. When the recipient ended its turn at 04:22 without a final report, the request was already marked answered. Nothing was open, so the session stall check never triggered. An earlier 50-minute wait was caused by the same pattern.

## Design

### 1. Progress replies

`agent_message_reply` takes `progress: true`. A progress reply is delivered to the sender and counts as activity for the recipient, but leaves the parent request or order open in state `delivered` (or `dispatched`/`stored`). A reply without `progress: true` is final, as before, and marks the request `replied`.

An acknowledgement (`ack: true`) closes the request with no message sent. It cannot be combined with `progress: true`.

### 2. Automatic reminder nudge

An open request or order with no final reply, whose recipient has been idle for `messages.owed_reply_nudge_after` (default 15m) since its last activity, receives one automatic reminder nudge from agent-net:

`You still owe <sender name> a final reply to <message id> (<first line>). Reply with the result, or hand back.`

The check skips:
- A recipient that is busy running a turn.
- A recipient that waits on the owner (an explicit wait or an open Desk ask).
- A recipient that waits on others (a running worker it started, or a claimed delivery step).
- A withdrawn or expired request.

### 3. Chief ask

If the request or order still has no final reply `messages.owed_reply_ask_after` (default 10m) after the reminder nudge, agent-net raises one ask routed to the chief of staff (`next_who chief`).

The ask names the full chain:
- Who waits on whom.
- Since when (the request creation time).
- The message id and its first line.
- The nudge time.
- The sender's lead, if any.

The chief of staff chases the lead and escalates to the owner only if chasing fails.

### Invariants

- Exactly one nudge and one ask are raised per request.
- A final reply before the nudge or ask stops the chain.
- No model tokens are used for detection.

## Addition 2026-11-20: follow_up_at

### Context

On 2026-11-20 the chief of staff relayed "the lead reviews, then it flips; nothing comes to you" and set no follow-up. The chief has no timer: it acts only when a message arrives. The work stalled for 80 minutes. The owner asked for the lead's and the chief's views, with the final call to the lead.

### Design

`agent_ask_sort` (results `send` and `close`) and `agent_message_send` take an optional `follow_up_at`: an RFC 3339 time or a positive duration such as `45m`. The gateway stores it in the `follow_ups` table. On its minute tick it looks at each follow-up once, when it falls due. If the expected event has not happened by then, it raises one ask routed to the chief (`next_who chief`, source `follow-up:<id>`) that names what was expected and since when.

The expected event is:

- For a send: a message from the recipient in the same conversation. A progress reply counts, because the follow-up asks whether the recipient is alive on this thread, and part 1 already chases a missing final reply.
- For a sort that names a change (`change`, or the change the ask is about): a move of that change. New evidence on any step, or a change update after the follow-up was set, is a move.
- For a close or send with neither: any later message in the ask's conversation that is not from agent-net. If the ask has no conversation, any later message from or to the asker.

### Invariants

- No `follow_up_at` means today's behaviour exactly.
- Exactly one ask is raised per follow-up. A follow-up that is met is never raised.
- Part 1's owed-reply rules (nudge, ask, skips) do not change.
- No model tokens are used for detection.
- Follow-ups are never limits or approval gates: they only raise an ask the chief can close.
