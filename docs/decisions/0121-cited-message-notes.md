# 0121 - Notes for cited messages

Date: 2026-11-09
Status: Built. The merger renumbers if needed.

## The problem

A lead approves a deploy. The member asks the operator to deploy and cites the
approval by its message ID. The operator cannot read that message, so text the
member pastes is the only proof, and the operator asked the lead again. That
cost a round trip. (Live case: m_c0f041a063cd1359, 2026-11-09.)

## Decision

1. When the body of a message names a message ID (`m_` and 16 hex characters),
   agent-net adds a note next to the message with the cited message's sender,
   recipient, time and body. The note is agent-net's text, written by the
   gateway, never part of the body. It reads:
   `[agent-net] cited m_...: from <name> (session s_...) to <name> at <time> UTC, request: "<body>"`.
   A cited message from the owner reads `from your user in person`.
2. **Visibility does not change.** The note is added only when the sender of the
   new message was the sender or the recipient of the cited message. For any
   other ID nothing is added, and nothing tells whether the ID exists. The
   sender could paste the text anyway. agent-net only vouches that it is
   genuine.
3. Only the first 3 distinct IDs of a body are looked at. A cited body is shown
   up to 1500 characters, then `(cut, N more characters)`.
4. The gateway builds the notes in one place and sends them in `cited_notes` on
   the delivered message. The Claude Code adapter, the Codex adapter, the Pi
   extension and the OpenCode plugin write them after the other agent-net
   notes. `agent_message_get` shows them in `notes`.
5. No new setting and no new gate.

## Why in the gateway

Only the gateway can see both messages and the graph of sessions. A daemon
would need to read a message it may not be allowed to read. The gateway already
computes the other delivery notes (relation, authority) at the same step.

## Limits

- A cited body that itself cites a message gets no second level of notes.
- The note shows what was written. It does not say the cited text was an
  approval. The receiver still reads it and decides.
