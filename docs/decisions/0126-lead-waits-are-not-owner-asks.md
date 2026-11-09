# 0126 - A wait on the lead is not an owner ask

Date: 2026-11-09
Status: Reported by the tenebrae lead on 2026-11-09, and also hit by agent-net 2
the same day. Built on the branch of worker w_5338e6e0fefb6ed2. Not yet released.
Builds on [0100](0100-team-questions-go-to-the-lead.md),
[0111](0111-blocked-members-reach-the-lead.md) and
[0114](0114-chief-of-staff.md).

## The problem

A member of team forge announced "waiting: tenebrae confirming X". tenebrae is
the lead of the team. The wait was ordinary: the member needed an answer from
its lead. Three things made it noise for the owner:

1. The wait became a Desk ask (`syncWaitingAsk`). The chief of staff sorted it
   and sent it back to the lead as a message from `agent-net`. Two asks
   (i_539e23320d316ea1, then i_33c7220b61d032fe) came from one wait.
2. The lead could not answer. `agent_message_reply` to a message whose sender is
   `agent-net` failed with `unknown recipient session "agent-net"`, because
   `agent-net` is not a session. agent-net 2 got the same error on a stall ask
   the same day (m_79b78bcc790c81c8). The ask stayed open.
3. In 15 minutes one member's wait made 2 owner asks and 3 "blocked or waiting"
   notices. The announce notice of decision 0111 followed the whole task line,
   so a member that reworded its task while it waited sent a new notice for the
   same wait. The lead's loop tick listed the same wait every tick.

## Decision

1. **A wait that names the session's own lead makes no owner ask.** When the
   text of a wait names the session's team lead or its repo lead, the wait goes
   to that lead as the notice of decision 0111, and no Desk ask is made (an
   existing one closes). The lead is named when its Agent id, its Agent display
   name, the id of one of its sessions or its pinned session stands in the text
   as a whole word, ignoring case. This is for any team, whatever its
   `asks_to`; a repo lead counts too. The notice reads `Team forge:` or
   `Repo demo:` in front. Waits that name the owner (the word `owner`, also when
   the lead is named too) or nobody keep their old path. A wait a harness event
   reported (a permission prompt, `blocking`) is the owner's whatever its text
   says. An explicit wait (`agent_owner_ask`) has its own redirect (0100).
2. **A reply to a message of agent-net about an ask answers the ask.** The
   chief's "send" sort writes the ask id into the message key
   (`chief_ask:<ask id>:<random>`). A reply to that message, from the session it
   went to, is stored as history (state acknowledged, never delivered) and
   closes the ask. A body is the reason, the same as the chief's `close`:
   status `closed`, answer `<session name>: <body>`. An ack (`ack: true`) closes
   it with `<session name> acked`. One Desk log line says it. This is the
   smallest change that makes `agent_message_reply` work and closes the ask: it
   adds no tool and no table, and the ask never reaches the asker as an owner
   answer, because the asker is not waiting on one.
   - An ask the owner alone decides (`route_fixed`) is never closed this way.
     The reply fails with: `ask <id> is a decision only the owner can answer; a
     reply cannot close it. If it needs the owner, tell them with
     agent_owner_ask.` The ask stays open.
   - A message of agent-net that is not about an ask (a notice, a loop tick, a
     step task) gets a clear error: `agent-net sent <id> as a notice and does not
     read replies to it`.
   - A session that did not get the message cannot answer it.
3. **A wait is told once for one text.** The announce notice keys on the text of
   the wait when the announce has one, not on the task line around it. A member
   that rewords its task while it waits sends nothing new; a changed wait text
   sends again. The lead's loop tick lists a blocked member once for one text:
   the gateway keeps the fingerprint of what the last sent tick listed
   (`member_blocked_ticks`, one row per session, never the text) and drops the
   row when the member is not blocked any more. A tick that was skipped records
   nothing, so the wait is still listed in the next tick that goes out.

## Why this shape

- No limit and no gate. Nothing is refused that was allowed before, except the
  reply to an owner-only ask, which could not be answered before either.
- The name match is plain and visible. A wait that names the lead by a word it
  does not know will still make an ask, as before. A false match costs a notice
  to the lead instead of an ask to the owner, and the lead can still ask the
  owner.
- The ask id rides in the message key, so no column and no text parsing.

## Tests

`lead_wait_test.go`: a member that waits on its team lead (asks_to lead and
owner) or its repo lead makes no ask and one message; waits that name the owner
or nobody still make an ask; a reply, an ack, a stranger and a `route_fixed` ask
for agent-net messages; one wait over several announces and several ticks.
