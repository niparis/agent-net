# Owner front door: build plan

Status: design approved by the owner on 2026-11-12. Nothing is built.
Build starts only on the owner's go ("we have too many workers"). Each step is
one change, built by a worker, accepted by the agent-net lead and merged by
the merger.

Decisions: [0149](../decisions/0149-owner-front-door.md) (roles, Desk,
answers, safety net, measures), [0150](../decisions/0150-cto-or-chief-confirms-picker-overrides.md)
(picker overrides), [0151](../decisions/0151-automatic-message-routes.md)
(routes).

## Step (a): the Desk item shape

- Inbox item gains `owner` (chief or cto) and `state` (verifying, with_cto,
  ready, answered, carried_out, closed). One migration, appended at the end.
- Intake: one decision per item (a multi-question ask is split); the duplicate
  check uses source plus kind, with a fallback to the same asker and the same
  text within 24 hours.
- Owner-bound items need question, evidence, a verified line, a
  recommendation, options and "discuss". The gateway refuses an owner-bound item
  without options, with an error that names the missing field.
- An answer carries a scope. Scoped answers become kind=rule judgments, shown on
  the Desk with undo. Each application cites the rule id.
- The chief and the CTO record chat decisions with the judgment tool, with a
  scope.
- Remove the generated "Open the session and ask what is left" recommendation.
- Waiting-session asks get a debounce.
- "Discuss" wakes the item owner's session.
- Picker overrides: the CTO and the chief may confirm (0150).
- Tests: refusal of an owner-bound item without options; a split; both
  duplicate keys; a scoped answer that creates a rule and its undo; a rule
  citation.

## Step (b): routes and the CTO intake queue

- One route table in code that matches 0151. A test lists every ask and
  message kind and fails on a kind with no route.
- The CTO intake queue: a table keyed by (kind, subject) with severity, first
  seen, last seen and count. A read the CTO calls at the start of each owner
  conversation. A wake only for the 0151 wake list.
- New detector: K = 3 failed test runs in a row on the merger's train tip.
- New feeds: harness update failure, compaction failure, quota floor or
  exhausted, struggle across workers, MCP repair failure, machine silent for
  N = 15 minutes.
- The morning brief gains one system-health line and the list of the day's
  recorded decisions.
- Tests: deduplication; the wake rule for each wake case; nothing reaches the
  owner except sign-in and OAuth.

## Step (c): the timer and the safety net

- Remove `ChiefMaxWait` and the 15-minute line in the batch text.
- Claiming an ask (verifying) stops any clock. A claimed ask idle past the
  stuck-work limit pings the chief and shows in the brief as "still open, N
  hours".
- The safety net: presence plus missed wakes (10-minute wake interval).
  Security, outage and money items go to the owner after one missed wake, the
  rest after two. A harness that cannot be woken uses presence plus unanswered
  batches.
- Tests: a live chief never sends an item to the owner by timer; an offline
  chief sends a security item after one missed wake and others after two.

## Step (d): migration

- One migration re-routes the open items. Owner-bound items without options
  become chief-bound; none is refused or dropped. Open items gain an owner and
  a state.
- The 13 ask kinds follow the 0151 table.
- Test: a copy of a real gateway DB migrates with no item lost (count before =
  count after).

## Open

- How the chief's verification quality is judged beyond the measures in
  0149. That is model behaviour; the measures show it, they do not force it.
- The chief's read-only verify access needs agent_permission_propose to accept
  the chief's folder. The chief reports this gap to the agent-net lead.

## Checked against docs/principles.md

- 4 (near the loop) and 6 (owner attention): the owner sees only decisions,
  once.
- 5 (owner keeps his calls): money, secrets, security, client-facing,
  destructive. These stay owner calls, and their standing rules show their
  scope plainly.
- 7 (agents can do what the owner can): every chief Desk action keeps a CTO
  equivalent.
- 8 (no unasked limits): the one new refusal, an owner-bound item without
  options, is the owner's own request.
- 9 (evidence): every owner item carries evidence and a verified line.
- 12 (measure): the measures in 0149 and the CTO digest.
- 16 (performance): the CTO intake is a queue read at conversation start, not
  one message per event.
