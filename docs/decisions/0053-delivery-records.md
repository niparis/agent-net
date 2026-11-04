# 0053 - Durable delivery records

Date: 2026-11-03

A crash after a harness write but before saving its receipt used to permit a
second injection. Failed writes could also be reported as delivered. W4.1
makes delivery intent durable and keeps uncertainty visible to the sender.

Extend the existing `delivered` table with a `state` column, defaulting to
`delivered`. This is simpler than a second table: old rows keep their meaning,
and the existing expiry plus seven-day pruning covers every state. No message
text is added. The migration is safe to run again.

Save `injecting` before calling the harness. Refuse the call if reading or
writing the record fails. Save `delivered` after acceptance, `failed` after a
known failure, and `unconfirmed` after an uncertain write. The extra internal
state distinguishes a safe later attempt from one that could duplicate a turn.
A delivered record never moves back. In-memory queues remain dispatched and
clear their intent until the actual harness call. Queue duplicates are kept
out, and queued worker delivery waits for the dispatch record before writing.

A surviving `injecting` record reports failed with
`may have arrived: the daemon stopped while delivering`, without another write.
An unconfirmed write reports failed with a may-have-arrived detail and also
blocks another write. A confirmed duplicate still reports delivered with
`duplicate`. Use the existing failed protocol state: both the gateway and TUI
already display it and its detail. No gateway or protocol change is needed.

Confirmation stays at each harness's existing acceptance boundary: the Pi or
OpenCode plugin ack, Codex queue accept, Antigravity hook reply or herdr
acceptance, and Claude inbox socket write. Acceptance does not prove that the
agent processed the turn. For an Antigravity hook, save intent while preparing
the reply and confirm only after the local response socket write. A failed
response write remains uncertain. Pi's first worker task can be pushed once
more within the same delivery when its turn-start ack is missing. The extension
remembers the last 200 IDs passed to `sendMessage` and dedupes repeated pushes.
A repeated first task is acknowledged only after its turn starts; other repeated
IDs get the same successful ack without another injection. The durable intent
stays in place until both pushes finish. A known refusal remains eligible for a
later gateway push; uncertainty does not otherwise authorize a retry.

W4.2 will add retry scheduling with backoff. W4.3 will add final failure and
expiry notices to the sender. Neither is part of this decision's implementation.

## W4.2 update (2026-11-03)

The gateway stores retry counts and due times separately from delivery records.
It retries an allowlist of known temporary failures at 30 seconds, 2 minutes,
10 minutes, and every 30 minutes until expiry. Unknown failures and details
saying the message may have arrived are not retried. Success and expiry clear
the schedule. Held requests keep their existing path. W4.3 remains responsible
for final failure and expiry notices.

## W4.3 update (2026-11-04)

A message that fails for good gets one notice to its sender from agent-net
itself: a failure that is not retried, or an expiry with no confirmed delivery.
The notice is a normal stored notification with the idempotency key
`undelivered:<message id>`. The unique sender and key pair makes it exactly
once, also across restarts, with no new table. It is written in the same
transaction as the final state. A detail saying the message may have arrived,
or an expiry while still dispatched, gives "may not have arrived" instead of
"was not delivered". There is no notice about agent-net's own messages (no
loops), and none to the owner or a connected app, which already see the state.
Held requests keep their own expiry reply.
