# Owner front door: build plan

Status: design approved by the owner on 2026-11-12. Nothing is built.
Build starts only on the owner's go ("we have too many workers"). Each step is
one change, built by a worker, accepted by the agent-net lead and merged by
the merger.

Decisions: [0150](../decisions/0150-owner-front-door.md) (roles, Desk,
answers, safety net, measures), [0151](../decisions/0151-cto-or-chief-confirms-picker-overrides.md)
(picker overrides), [0152](../decisions/0152-automatic-message-routes.md)
(routes).

## Step (a): the Desk item shape

- Inbox item gains `owner` (chief or cto) and `state` (verifying, with_cto,
  ready, answered, carried_out, closed). One migration, appended at the end.
- Inbox item gains `category` (security, outage, money, normal), set at intake
  from the kind where the kind fixes it, else from the asker; the chief may
  correct it.
- Intake: one decision per item (a multi-question ask is split); the duplicate
  check uses source plus kind, with a fallback to the same asker and the same
  normalized text (case and whitespace folded, ids removed) within 24 hours.
  Every asker that joined gets the answer when it is carried out.
- Owner-bound items need question, evidence, a verified line, a
  recommendation, options and "discuss". The gateway refuses an owner-bound item
  without options, with an error that names the missing field.
- An answer carries a scope: area (repo, team or network) plus action. Scoped
  answers become kind=rule judgments, shown on the Desk with undo. A rule
  applies only on an exact match of both parts; in money, secrets, security
  and client-facing a rule names a repo or team, never the network. Each
  application cites the rule id.
- New tool `agent_owner_judgment_record` for the chief and the CTO, with a
  scope field, to record chat decisions.
- Desk controls on an answered item: "didn't need me" and "should have asked
  me".
- Remove the generated "Open the session and ask what is left" recommendation.
- Waiting-session asks get a debounce.
- "Discuss" on a chief item wakes the chief; on a CTO item it opens the
  owner's CTO chat (no wake).
- Picker overrides go to the chief first; it confirms under 0151's rule (same
  or cheaper tier, quota allows, provider not protected, never the caller's own
  family for a review of its own work); the CTO answers from its queue if the
  chief has not.
- Tests: refusal of an owner-bound item without options; a split; both
  duplicate keys, including a normalized-text match; every joined asker
  answered; a scoped answer that creates a rule, an exact-match application,
  a near miss that does not apply, and the undo; a network-wide rule refused
  in an owner-only category; the chief's confirm rule (cheaper tier confirms,
  own family for own review routes to the CTO).

## Step (b): routes and the CTO intake queue

- One route table in code that matches 0152. A test lists every ask and
  message kind and fails on a kind with no route.
- The CTO intake queue: a table keyed by (kind, subject) with severity, first
  seen, last seen and count. A read the CTO calls at the start of each owner
  conversation. A wake, only for the 0152 wake list, is a P1 "CTO alert" item
  in the chief's queue: the chief routes it to the lead that owns the cause,
  with the CTO informed, and to the owner only for money, secrets or security.
- Quota exhaustion is one thread: the chief's stuck-work item links the CTO
  queue entry for the provider.
- A machine that reported sleep never raises the silent alert (decision
  0137).
- New detector: K = 3 failed test runs in a row on the merger's train tip.
- New feeds: harness update failure, compaction failure, quota floor or
  exhausted, struggle across workers, MCP repair failure, machine silent for
  N = 15 minutes.
- The morning brief gains one system-health line and the list of the day's
  recorded decisions.
- Tests: deduplication; the wake rule for each wake case, each producing one
  P1 chief item and no CTO turn; a sleeping machine raises nothing; one quota
  event gives one thread; nothing reaches the owner except sign-in and OAuth.

## Step (c): the timer and the safety net

- Remove `ChiefMaxWait` and the 15-minute line in the batch text.
- Claiming an ask (verifying) stops any clock. A claimed ask idle past the
  stuck-work limit pings the chief and shows in the brief as "still open, N
  hours". A with_cto item idle past the same limit goes back to the chief.
- The safety net: presence plus missed wakes (10-minute wake interval).
  Items with category security, outage or money go to the owner after one
  missed wake, the rest after two. A harness that cannot be woken uses presence plus unanswered
  batches.
- Tests: a live chief never sends an item to the owner by timer; an offline
  chief sends a security item after one missed wake and others after two.

## Step (c2): no owner questions in session chats

Added 2026-11-12 (0150, "No agent asks the owner in its own chat").

- Guidance: change the owner-ask lines in internal/guidance (instruction-file
  text and its goldens, `-update`) and the MCP server instructions
  (internal/mcpserver/mcpserver.go) to "owner questions go to the chief with
  agent_owner_ask; do not end a turn waiting on the owner".
- Gateway: agent_owner_ask from an agent session creates a chief-bound item
  (owner chief, state verifying). syncWaitingAsk (internal/gateway/asks.go)
  creates the waiting-session item chief-bound, with no debounce unless the
  owner typed in that session in the last 30 minutes.
- Trigger: an open agent_owner_ask, or a final message that is a question to
  the owner (a harness-reported question prompt, or a line marked "Owner
  question:" as the guidance asks). Never a plain turn end.
- Daemon: the Claude Stop hook (policy-hook) returns a continue reason "Send
  this question to the chief with agent_owner_ask and carry on" once per
  question, when the trigger fires and the owner has not typed in that
  session in 30 minutes (owner keystrokes there; attach or view does not
  count). Other harnesses get the line at the start of the next turn. No loop.
- Tests:
  - an agent session's agent_owner_ask creates an item with owner chief and
    never an owner-bound item without options;
  - a turn ending on an owner question, with no owner typing in 30 minutes,
    gets a chief-bound item at once, and the Stop hook returns the continue
    reason exactly once (a second stop on the same question passes);
  - an idle agent whose turn ends with no question gets no ask and no
    continue;
  - the same question with the owner typing 5 minutes ago creates no redirect
    and no hook block (in the loop); the owner only viewing does not count;
  - the CTO's and the chief's own sessions are never redirected;
  - guidance goldens contain no "ask your owner" line.

## Step (d): migration

- One migration re-routes the open items. Owner-bound items without options
  become chief-bound; none is refused or dropped. Open items gain an owner and
  a state.
- The 13 ask kinds follow the 0152 table.
- Test: a copy of a real gateway DB migrates with no item lost (count before =
  count after).

## Open

- How the chief's verification quality is judged beyond the measures in
  0150. That is model behaviour; the measures show it, they do not force it.
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
- 12 (measure): the measures in 0150 and the CTO digest.
- 16 (performance): the CTO intake is a queue read at conversation start, not
  one message per event.
