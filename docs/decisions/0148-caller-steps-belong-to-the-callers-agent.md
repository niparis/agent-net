# 0148 - Caller steps belong to the caller's agent

**Date:** 2026-11-12
**Status:** Accepted.

## Context

The owner, 2026-11-12: "it looks like we are forgetting completed work". 188
changes were open. About 145 outside agent-net waited at accept, for 1.5 to 7
days. Two causes:

1. Steps owned by the caller belonged to one session, the one that started the
   worker. When that session was offline or waiting on the owner, nobody could
   act, and the reminders went to a session that was not running.
2. When every step of a pipeline is owned by the caller (every project except
   agent-net and homelab-agent), `queueSteps` made no task for the caller's
   steps. No task meant no step message, no reminder and no stuck notice. The
   leads of finget and subtext never heard of 30 and 34 changes.

## Decision

**The owner of a caller step is the caller's agent.** The gateway reads the
agent of the change's caller session when it checks (no data migration).

- Any live session of that agent may claim and report the step. The task
  message goes to one live session of the agent: the caller if it is live, else
  the agent's most recently seen session. A session without an agent keeps the
  old rule: only the caller session.
- A caller step may be reported again after it completed, as it could when it
  had no task. An open claim still holds.
- When no session of the agent is live and a step has waited past
  `delivery.step_stuck`, the gateway sends the agent one message with the list
  of its waiting steps to its best offline session. The message wakes the
  session the way any message does. If there is no session to wake, the wake
  was not started, or nobody of the agent is online when the wake window
  (`wakeTimeout`, 2 minutes) ends, the steps are offered to the live session of
  the repo's lead. Only then may the lead claim and report them. The offer ends
  when a session of the agent is live again. The state is one row per agent in
  `caller_step_escalations`.
- A session of another agent, or the lead before the offer, is still refused.
- Finished work that waited more than 24 hours with no live owner raises one
  Desk line per project: `finget: 30 finished changes waiting for accept,
  oldest 3 days, lead offline`. The line is updated in place and closes when
  the count reaches zero. A line the owner dismissed stays quiet for a day.
  The CTO reads it with the other asks.
- `queueSteps` makes a task for every ready caller step, whether or not the
  pipeline has another owner. A task made at gateway start for work that
  finished earlier is dated from the change's last update, so its waiting time
  is honest.
- The worker notice says when the caller owns accept: "You own the accept step
  of ch_X. Report it: agent_step_report change=ch_X step=accept result=pass
  commit=<head>, or result=close with a one-line reason if nothing will be
  merged." Leads read the checklist as "check the work" and never reported.

## Consequences

- Sessions of one repo share one agent, so a merger session of the same agent
  may now report the caller's steps. This is the intended reach: the agent, not
  the session.
- The first gateway start after this change makes tasks for the backlog, and
  the reminders and stuck notices follow. They are batched per session.
- No new setting. The stuck limit, the wake window and the 24 hours are the
  existing timer and constants.
