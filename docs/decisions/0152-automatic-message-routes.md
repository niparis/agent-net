# 0152 - Where each automatic message goes

Date: 2026-11-12
Status: Approved by the owner on 2026-11-12 (reshape brief v2). Not built:
step (b) of [the plan](../design/owner-front-door-plan.md).
Follows [0150](0150-owner-front-door.md).

## Context

On v0.5.29 (main d893eff0) the gateway and the daemon send these automatic
messages. Routine messages go to the session that must act. Thirteen Desk ask
kinds go to the chief when one is live, and to the owner after 15 minutes. Five
go straight to the owner: machine went silent, sign-in, picker override, the
Claude mouse notice, and phone paired. Nothing automatic reaches the CTO.
Struggle, change signals, quota floor or exhaustion, harness update failure,
compaction failure, CPU and memory pressure are logs and state only. The
code read behind this is in the agent-net lead's fact check for the owner
(2026-11-12).

## Decision

### Agent only (unchanged)

| Kind | Receiver |
|---|---|
| step task, step task reminders, step result, step closed, merged outside, no work recorded | the session that must act (or the change caller) |
| reminder for an open request | the session that owes the answer |
| idle check, autonomy continue, loop tick | that session, or the repo lead for a loop |
| worker started, worker notice (ended, failed, lost, idle), auto-continue | the worker's owner, or the worker |
| undelivered, could not be woken, delivered to a waiting session | the sender |
| tools disconnected (turn line), native MCP removed | that session |
| member blocked or waiting | the team lead |
| charter draft request and project rules request (turn text) | the lead session |

### Chief (everything that wants the owner)

| Kind | Note |
|---|---|
| approval, step needs_owner | becomes a decision item |
| check: blocked, or woken twice without progress | raw input; never reaches the owner as is |
| loop paused | |
| waiting session | created only after the debounce (0150) |
| rejection, escalation | |
| session stall | |
| held request | |
| charter draft asks, learned suggestions | or the team lead, as today |
| agent start expiry, sandbox settings cutover, low disk | |
| stuck work whose blocker is a person (the owner, or an approval) | other stuck steps follow decision 0148: owner, then project lead, then the CTO when there is no lead |
| work failing because of quota (worker failed or model_failed, steps stuck because no provider is available) | the chief gets the work moving or re-routed; the CTO fixes the cause |
| ask batch, morning brief wake, nudge pause and resume | as today |

### CTO (new): an intake queue

Alerts go into a CTO intake queue. The CTO reads the queue at the start of
each owner conversation. Entries are deduplicated by (kind, subject), where the
subject is the machine, harness or project. Each entry has a severity, a first
seen time, a last seen time and a count.

**What "wakes the CTO" means.** A CTO turn starts only from the owner, so
there is no CTO turn without him. A wake is a P1 item in the chief's queue,
"CTO alert". The chief routes it to the lead that owns the cause (the
agent-net lead for agent-net), with the CTO informed. It goes to the owner
only for money, secrets or security. The CTO acts on it at the next owner
conversation, or at once if the owner opens one.

**One event, one thread.** Quota exhaustion reaches the CTO (the cause) and
the chief (the stuck work). It is one thread: the chief's stuck-work item
links the CTO queue entry for the same provider, and closing the cause closes
both.

| Kind | Subject | Wakes the CTO when |
|---|---|---|
| machine offline or silent | machine | silent for N = 15 minutes; a machine that reported sleep never alerts (decision 0137 tells asleep from silent) |
| harness update failure | machine and harness | the alert repeats for over an hour |
| compaction failure | machine and harness | the alert repeats for over an hour |
| MCP repair failure | machine and server | the alert repeats for over an hour |
| struggle pattern across workers (not per worker) | model or project | failures across many workers |
| merge queue failing (new detector) | project | K = 3 failed test runs in a row on the merger's train tip, counted per train |
| quota floor reached, provider exhausted | provider | the alert repeats for over an hour |
| picker override (decision 0151) | the start | never wakes; it goes to the chief first, and the CTO answers it from the queue only if the chief has not |
| Claude mouse notice | machine | never; one digest line |

Everything that does not wake the CTO goes to its digest. The chief's morning
brief carries one system-health line from it.

### Owner directly

Only sign-in and OAuth asks, as one-click asks. Money ("buy more, or change
the plan") goes through the chief to the Desk. The "phone paired" notice
becomes a digest line for the CTO.

## Consequences

- No automatic message goes to the owner except sign-in and OAuth.
- The CTO needs a queue read at conversation start. An alert never starts a
  CTO conversation; a wake is a P1 item for the chief.
- A new detector is needed for the merge queue (K failed runs in a row).
- Every kind not listed here keeps its current route until this table is
  updated, and a test lists every kind so that a new kind fails until it has a
  route.
