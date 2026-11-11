# 0131: Chief of staff pauses individual nudge kinds

The owner said, through the CTO on 11 November 2026:

> pause and resume each mechanism separately, per team or per agent.
> Every pause and resume is recorded, shown to the owner in the Inbox and
> undoable. The chief can set it without asking the owner.
> A paused mechanism doesn't send. It must not queue its messages.
> Owner messages and real peer requests are never affected.

The owner also said:

> keep every automatic nudge mechanism as it is ... with their current defaults.
> Don't move them behind a model or a digest.

The chief gets three tools: agent_nudge_pause, agent_nudge_resume and
agent_nudge_pauses_find. They use the same chief session check as sorting asks.
A pause names exactly one kind and one team or agent. It follows agent identity
across every session and current agent or repo membership of a team. The kinds are
idle_check, autonomy_continue, reply_reminder, loop_tick, step_reminder,
stuck_step, undelivered, waiting_notice, wake_failed and member_blocked.

Preview reads the target sessions and counts this kind's messages in the last
24 hours. It writes nothing. Apply stores the pause without asking the owner.
Apply and resume each write a Desk log entry with an owner-only Undo receipt.
Undo of a pause ends it; Undo of a resume restores its original expiry. Receipts
are consumed once, and a later operation makes an older receipt stale.

A pause may have a reason and an absolute expiry, or provider_reset. The latter
ends when no session of the target has limited_until in the future. It also
ends after seven days at the latest. This cap exists only so that a pause never
stays forever by accident. Ordinary pauses have no cap. Ended rows are retained
with ended_at and ended_by; time and provider expiry are checked between ticks
and recorded by maintenance.

Each builder checks the recipient before creating a message or changing its
reminder flags, dedupe keys or counters. The indexed lookup uses the kind and
recipient's agent or team. There is no queue of suppressed messages and resume
does not send anything. Each builder continues on its normal clock afterwards.
Step reminder pauses leave first task notices alone, including in batches.
Owner messages, peer requests and replies, worker notices, step results, and
the chief's brief wake and ask batches keep their current paths.

This adds no settings, model decision, digest, budget or approval step. All ten
mechanisms keep their current behavior and defaults when there is no pause.

Applying a pause also expires matching nudges already queued at the gateway.
Delivery rechecks pauses before sending or waking a session, so a saved queue
snapshot cannot replay an expired nudge after resume.
