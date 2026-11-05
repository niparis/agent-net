# 0065 - Wake retired Claude background sessions for messages

Claude Code retires unattached idle background sessions. The conversation is
kept, but the gateway sees the session offline and cannot deliver its work.

The gateway selects offline Claude recipients when it queues messages and
when their machine reconnects. For a ready unclaimed step owned by the merger
or caller, it wakes only that recorded session; node-owned steps wait for an
online session. Task notifications are created after the session is online,
so a backlog is batched. The gateway records a per-session 10-minute attempt limit and protects queued messages from expiry during the
bounded attempt. The owning daemon checks the local Claude background list,
requires one matching record in state `done` and the original folder, refuses
workers and launches the existing background resume command. It opens no view.
Records already stopped, removed, ambiguous or unreadable are not woken.
The owner accepts that a stop made directly in Claude between the check and
the resume may be undone by the wake; the owner can stop the session again.
If Claude creates a copy, the gateway rebinds the original session ID to its native ID
before discovery, only for the matching in-flight wake and owning machine.

The gateway owns the queue, expiry and cross-machine connection. The daemon
owns the local harness and can check the record immediately before launch.
A failed attempt is logged once; a later message or reconnect may try again
after the limit. A successful registration uses normal message delivery.
