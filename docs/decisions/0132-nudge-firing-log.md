# 0132: Record every automatic nudge and its useful rate

The owner said, through the CTO on 11 November 2026:

> Record every automatic nudge: kind, recipient, time, and whether work
> followed within a set window (a tool call, announce, step evidence or
> message that isn't an ack). Show the useful rate per kind x agent/team,
> readable by the chief (so it pauses by numbers) and on the Desk/TUI.
> Don't change any nudge behaviour.

The caller measured about 17 automatic wakes in 18 hours, of which about 5
led to work. The chief could pause a kind or an agent already (decision 0131)
but had no numbers to decide with.

The ten kinds are the ones the pause control knows: idle_check,
autonomy_continue, reply_reminder, loop_tick, step_reminder, stuck_step,
undelivered, waiting_notice, wake_failed and member_blocked.

Each builder writes one nudge_fires row in the same transaction as its
message: kind, recipient session, the session's agent and team at fire time,
message id, and fired_at. A batched envelope has one row per original nudge
message, so the busy hold's parts stay separate.

Work followed when the first of these comes from the recipient within 30
minutes after fired_at:

- an agent-net tool call (the daemon reports it in a small frame, limited to
  one per session per second; an ack is a message, not a tool call);
- an announce;
- step evidence reported by that session;
- a message it sends that is not an ack.

The 30 minute window is a named code constant, not a setting. The owner asked
for "a set window".

The tool touch marks matching fires at once with an indexed update. The other
signals are marked lazily when the stats are read: unfollowed rows in the
requested window are checked once, and a row whose window has closed is not
checked again. Each check is a small indexed lookup on the sender time, the
evidence session, and primary keys. It never scans the message table.

The readout is agent_nudge_stats {since?, kind?, team?, agent?}. It returns
fires, followed and the useful rate per kind x agent and per kind x team, over
the last 24 hours by default, with the most fires first. The chief is a normal
agent session, so it gets the tool through the agent role. The CTO tool list
is at its schema budget, so the CTO does not get it there; it reads the same
numbers in the TUI.

The TUI shows the small table in a new Nudges section of Settings, below the
working rules. It is the least crowded existing place: the Desk Log is the
event stream, and the Quotas panel is the picker's. The table shows the total
and the five kinds with the most fires.

The pause preview carries "useful 2 of 14 in 24h" from the same rows. It only
reads them: preview still writes nothing.

No nudge is sent, held, delayed or suppressed differently. No setting, model
call, digest or approval step is added.