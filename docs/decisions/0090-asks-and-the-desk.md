# 0090 - Asks and the Desk

Date: 2026-11-08
Status: Accepted by the owner. Part A1 (store, sources, auto-close, log, RPC) is
built. Part A2 (the Desk tab) and part B (the chief of staff) are not.

## The problem

The owner's queue was built in the TUI from six sources: Inbox items, held
requests, outcomes, learned suggestions and charter drafts, CTO proposals, and
sessions that report a wait. Delivery steps that need the owner were a seventh
view on another tab. Each source had its own list call, its own close rule and
its own keys. Notices (the CTO applied a change, a new app was approved) sat in
the same queue as real questions, so the queue was long. The Inbox refresh made
four gateway calls and returned the whole history of items, closed ones too.

## Decision

1. **One store of asks.** Everything that waits on a person is an ask. The
   store is the `inbox_items` table, extended in place with `from_kind`,
   `source_ref`, `about_kind`, `about_id`, `next_who`, `why_you` and `detail`
   (migration 146). The table keeps its name because forty places write to it
   and close it; they keep working and every close path already writes the
   reason. The protocol type is `protocol.Ask`.
2. **An ask has the shape of a delivery step.** `from` (a waiting session, or a
   source: cto, gateway, disk, apps), `about` (a change, project or session),
   `question`, `options`, `recommendation`, `next` (who must act now: owner, a
   session id, or chief), `state`, `close_reason`, `why_you`. The state is
   read from `status`: open (or "with someone" when `next` is not the owner),
   answered, closed. The reason is kept in `answer`.
3. **`next` leaves room for the chief of staff.** Every ask in part A has
   `next = owner`. Part B may set `next = chief`; the ask then reads as "with
   someone" and the Desk shows it as in flight, not as needing the owner.
4. **Each source writes its asks.**
   - `agent_owner_ask`, approvals and questions: as before, now about the
     project.
   - Held requests: an ask from the sender, about the receiving session,
     opened in the same transaction that holds the message.
   - Escalations and rejections: asks as before.
   - Delivery steps that report `needs_owner`: an ask about the change
     (`step:<change>:<revision>:<step>`). The approve step keeps its written
     approval ask, now also about its change.
   - Learned suggestions (major, ready) and charter drafts: asks with the
     options accept and reject, opened by the reconcile.
   - Sessions that report a wait: an ask from the session. An explicit wait
     (from `agent_owner_ask`) has its own ask and makes no second one.
   - "Blocked on", "stopped with work left" and disk low stay `check` asks.
     They close when their reason goes away (see below).
   - CTO proposals live in the daemon's own database. The gateway has
     `ask.open`, `ask.close` and `ask.sync` for them. `ask.sync` sends the whole
     list of open proposals of a machine and closes the rest, so a call lost
     while offline cannot leave a stale ask. The daemon sends it after a
     proposal is made or decided, and every minute when the list changed.
5. **Notices go to a log, not the queue.** "CTO applied", "CTO sent" and "new
   app approved" are lines in `desk_log`, and are not asks. A CTO line keeps
   the way to undo it in its options. Until the Desk replaces the Inbox tab
   (part A2), the gateway also writes the old Inbox row for these notices, so
   the old screen and its undo keep working; `asks.list` leaves them out. The
   gateway also logs what it did by itself: every automatic close of an ask,
   reminders, idle checks and retired notices. The log keeps the newest 2000
   lines.
6. **Asks close by themselves.** When the asker answers or withdraws it
   (as before). When the change it is about is done or stopped
   (`change_done`). When the asker's session has ended (`session_ended`): for
   questions, approvals, waits, stalls and checks, not for held requests and
   outcomes, which are work between two sessions. When the source is decided
   (`resolved`), or a held request expires (`expired`). A new activity of the
   session closes a stall (`activity_or_end`). Automatic closes go to the log;
   a decision by the owner does not.
7. **One list.** `asks.list` returns the open asks in one query, oldest first.
   `desk.log` returns the newest log lines. The daemon forwards them as the
   operator methods `asks_list` and `desk_log`.
8. **Reconcile.** Every source acts at the moment it changes. A set-based
   `reconcileAsks` runs at start and on the gateway's minute tick as the safety
   net, and it is the only path for what changes with time: a held request that
   expires, a learned suggestion that becomes ready, a change that finishes.

## Upgrade

Open Inbox items need no move: they are asks already. The migration fills the
new columns for them (about the change for delivery approvals, about the
project for questions, approvals and stalls; the source for CTO, app and disk
checks). Open CTO and "Connected" notices are copied to the log once and stay
as old Inbox rows. No row is deleted or closed by the migration. Held requests
and learned suggestions that were pending get their asks at start from the
reconcile. The old `inbox.list` leaves out the kinds that this decision added
(held, learned, draft, proposal, step, waiting), so the old screen shows what
it showed before.

## Part A2 and part B

A2: the Desk on the Inbox tab slot (tab 7), the Delivery tab (tab 8) removed, the TUI
reading `asks_list`, the Sessions "Needs you" band and every count from the
same list, and the log
section with the CTO undo. B: the chief of staff, a separate role that sorts
asks (`next = chief`, `why_you`) under its own charter.

## Cost

`BenchmarkOwnerQueueLegacy` (the four calls of the old refresh) against
`BenchmarkOwnerQueueDesk` (`asks.list`), on a gateway with 60 sessions, 600
Inbox rows, 20 held requests, 10 escalations and 200 learned suggestions. See
the numbers in the change that built it.
