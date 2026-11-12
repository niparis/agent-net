# 0149 - The chief is the owner's front door; the Desk is his decision queue

Date: 2026-11-12
Status: Approved by the owner on 2026-11-12 (reshape brief v2 with both sets of
feedback, through the CTO). Not built. Build on the owner's go, in the order
of [the plan](../design/owner-front-door-plan.md).
Amends [0090](0090-asks-and-the-desk.md) and
[0114](0114-chief-of-staff.md). Routes are in [0151](0151-automatic-message-routes.md).
Picker overrides are in [0150](0150-cto-or-chief-confirms-picker-overrides.md).

## The problem

The owner gets asks from three places (CTO chat, chief chat, the Desk), and
they do not know about each other. The same question reaches him twice. Some
Desk items cannot be answered on the Desk: a "check" item has no options. The
chief has forwarded an agent's premise without checking it: the finget
PYPI_TOKEN ask rested on history, not evidence. A 15-minute timer sends any
ask the chief has not sorted to the owner anyway. The CTO relays agent chatter
to the owner. No automatic process reaches the CTO, so system-health problems
surface only when the owner notices them.

## Decision

### Roles

- **The owner** sets direction and makes the calls only he can make: money,
  secrets, security and permissions, client-facing releases, product direction,
  and destructive or irreversible actions. He does not sort, chase or relay.
- **The chief of staff** owns the owner's attention. It is the single front
  door for anything that wants the owner. It verifies facts and challenges the
  premise. It sends the asker back with specific questions rather than
  investigating itself. It resolves what standing orders and judgments cover.
  It turns the rest into a ready decision, tracks it to "carried out", and
  writes the morning brief.
- **The CTO** owns how things are built: architecture, the network and graph,
  tools and MCP, models, the picker, quotas and cost, and system health. It
  does not relay agent chatter to the owner. An owner-level decision goes
  through the chief like any lead's. Fixes to agent-net go to the agent-net
  lead as feature requests; the CTO does not start workers in agent-net.
- **The Desk** is the owner's decision queue and judgment log. The chief
  curates it.

### Who agents go to

An agent goes to its lead. An agent without a lead goes to the chief, which
routes technical items to the CTO. Leads go to the CTO for "how" and to the
chief for "whether the owner, who, when". Nobody goes to the owner directly.
The chief routes technical asks to the CTO; the CTO decides within its
authority or returns a ready item. On disagreement the CTO wins on method and
the chief on the owner's time. If it stays unresolved, one Desk item holds
both views.

### The Desk is the one shared list

1. It uses the existing inbox items and the owner judgment store
   (standing_orders). There is no second object.
2. Each item has an **owner** (chief or CTO) and a **state**: verifying,
   with_cto, ready, answered, carried_out, closed.
3. **One decision per item.** An ask with several questions is split at intake.
4. **Duplicates** are checked at intake against all open items. The key is
   the item's source (change, session or ask ref) plus its kind. When there is
   no source, the fallback is the same asker and the same question text within
   24 hours. A duplicate joins the open item; it does not make a new one.
5. **Owner-bound items** (next = owner) must have a one-line question, the
   evidence, a "verified by chief" line (or "unverified" with the reason), a
   recommendation, options, and "discuss". The gateway refuses an owner-bound
   item without options. Items bound for the chief may be raw inputs ("check",
   waiting sessions): the chief turns them into decisions or resolves them. A
   "check" item never reaches the owner.
6. **"Discuss"** opens the thread of the item's owner (chief or CTO) and wakes
   that session if it is offline.
7. The gateway's generated recommendation "Open the session and ask what is
   left, or message it." (internal/gateway/check.go) is removed. An item that
   has nothing better to recommend goes to the chief, not the owner.
8. A **waiting-session ask** is created only after the session has waited a
   minimum time (a debounce), so a short wait for the user makes no ask.

### Answers, scope and standing rules

1. Every answer carries a **scope**: "this time only", or "always for this kind
   (scope)". The owner sets the scope on the answer; the chief never infers it.
2. A scoped answer becomes a **standing rule** in the judgment store (kind
   rule). Each rule shows on the Desk with undo. Every time the chief applies a
   rule, the application cites the rule id. The morning brief counts the
   applications per rule. It shows plainly the scope of rules in the owner-only
   categories: money, secrets, security and client-facing.
3. **Decisions made in live chat** with the chief or the CTO are recorded
   explicitly. The chief or the CTO records each one with the judgment tool in
   the same turn, with its scope. The morning brief lists the day's recorded
   decisions, so a missed one is visible. There is no automatic reading worker.
4. The chief carries out each answer and reports completion. The item moves to
   carried_out.

### Timer, claims and the safety net

1. The 15-minute auto-send to the owner is removed (today `ChiefMaxWait` and
   the line "An ask you leave for 15 minutes goes to the owner" in the batch
   text, internal/gateway/chief.go).
2. The chief **claims** an ask (state verifying), which stops any clock.
3. **Claim age:** a claimed ask idle past the stuck-work limit pings the chief,
   never the owner. The morning brief shows it as "still open, N hours".
4. **Safety net when the chief is down.** It is based on presence plus missed
   wakes. A wake counts as missed when the chief has not claimed or sorted the
   batch within the wake interval (10 minutes). Security, outage and money
   items go to the owner after one missed wake. All other owner-bound items go
   after two. While the chief is live and answering, nothing goes to the owner
   by timer. If the chief's harness cannot be woken, the rule uses presence
   plus unanswered batches instead.

### Measures

The chief's verification quality is model behaviour, not code, so it is
measured, not enforced. The brief and the Desk header show:

- owner touches per day;
- "didn't need me" and "should have asked me" marks;
- items covered by a standing order that still reached the owner;
- duplicates that reached the owner;
- reversals of chief-verified items after the evidence changed;
- time from ask to carried out.

## Consequences

- The owner sees only decisions he can take on the spot, each once.
- The chief must be live and verify well. A chief that is down is caught by
  the safety net, and one that verifies badly shows in the reversal count.
- Migration: today's owner-bound items without options become chief-bound.
  None is refused or dropped.
- The chief needs read-only verify access (repo, gh run logs, gateway state).
  It requests this with agent_permission_propose. A known gap: that tool
  refuses the chief's folder because it is not a registered repo. The chief
  reports it to the agent-net lead.
