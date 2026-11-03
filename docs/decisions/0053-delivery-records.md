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
response write remains uncertain. Remove the blind second push of a Pi first task
when its ack is missing. A known refusal remains eligible for a later gateway
push; uncertainty does not authorize a retry.

W4.2 will add retry scheduling with backoff. W4.3 will add final failure and
expiry notices to the sender. Neither is part of this decision's implementation.
