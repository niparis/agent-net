# 0129 - Messages wait at the gateway while a session is busy

**Date:** 2026-11-09
**Status:** Accepted. Parts a to d are built. Withdraw and the owner and CTO
withdraw method are designed here and not built yet.

## Context

The gateway pushed a message as soon as the recipient was online. Each
harness then queued it inside its own session: Claude through its inbox
socket, OpenCode behind the running turn, Pi as a follow-up, Codex in its
thread queue.

On 2026-11-09 an OpenCode session on the Mac (homelab-agent 1) had 13
messages queued inside it. Some had been superseded. It would have acted on
stale orders. Two other reports put OpenCode delivery 10 to 20 minutes late,
which fits a message queued behind a long turn: the gateway saw the session
online, pushed, and the plugin held the prompt until the turn ended.

The gateway already knows each session's status from `session.upsert`: Claude
reports idle or busy, OpenCode reports `execution.started` and
`execution.succeeded`, failed or interrupted, Pi reports `agent_start` and
`agent_settled`, and Codex reports `turn/started` and `turn/completed`.

## Decision

**a. The gateway holds, the session does not queue.** A message to an online,
non-worker session whose status is busy stays at the gateway with state
`stored` and a new marker `held_for_busy`. It is not dispatched. Workers keep
their daemon turn queue. Offline sessions keep the queue and wake path.

**b. One batch, newest first.** When the session reports that it is no longer
busy, the gateway sends everything held for it as one message. The text starts
with `N messages arrived while you were busy. Act on the newest; read the
older ones as context.` and then lists each original message with its ID. A
single held message goes as it is. The batch message carries the originals in
its `batch` field, so the Claude adapter records each original for reply
matching, and every original keeps its ID for replies and acks. The batch's
own row is what the delivery report moves; its originals move with it.

**c. A stuck session is not starved.** If a message has waited 30 minutes
(`BusyHoldLimit`) and the session still reports busy, the gateway sends the
held messages anyway and logs `msg.released_stuck`.

**d. Inbox limit.** `messages.inbox_limit` (default 5, levels network, repo
and agent) caps the messages waiting at the gateway for one recipient.
Messages in a batch count once, as the batch. At the limit a send is refused
with `recipient has N waiting, oldest from you at <time>; withdraw one or
wait`. The owner's messages, and the CTO's messages sent with
`as_owner: true`, skip the limit. Any sender may mark a message `urgent`: it
goes ahead of older held messages but still counts toward the limit. Zero
turns the limit off.

**Visibility.** A send result and `agent_message_status` carry
`queue_position`, `queue_waiting`, `busy_for_seconds` and the one-line
`queue` receipt (`queued behind N, session busy for X min`). The TUI STATUS
cell adds `gw N · in M`: held at the gateway, and dispatched but not delivered
to the harness.

**Withdraw (designed, not built).** A sender may withdraw its own message
while it is held, stored, or dispatched and not yet delivered. The owner and
the CTO may withdraw any undelivered message to a session, including every
message older than a named one. A withdrawn message gets the state
`withdrawn` and stays visible to its sender. A message the harness has
accepted cannot be withdrawn, and the error says so. Withdrawing an original
in a batch removes it from the batch before the batch goes.

## Consequences

The in-session queue is normally zero or one. A busy session sees one
message with the newest order first, and its sender sees where it waits and
why. The gateway does more work per send: one status read, and one count for
the limit. The two new columns and the indexes are in
`busyHoldMigration`.

The hold assumes the harness reports busy reliably. A harness that leaves
`busy` set loses at most 30 minutes of immediacy, never a message.
