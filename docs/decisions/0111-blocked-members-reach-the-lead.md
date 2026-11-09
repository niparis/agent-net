# 0111 - A blocked member reaches its team lead

Date: 2026-11-09
Status: Reported by the tenebrae lead on 2026-11-09, as the owner asked leads
to report systemic team problems. Built on the branch of worker
w_4dbebcc73f2e171c. Not yet released.
Builds on [0090](0090-asks-and-the-desk.md), [0094](0094-standing-loops.md) and
[0100](0100-team-questions-go-to-the-lead.md).

## The problem

The tenebrae team has `asks_to=lead`. Its member tenebrae-strats set its
announced task to "blocked on the owner: PR #130 ... merge was denied in this
session" at about 00:10 UTC on 2026-11-09. It made no `agent_owner_ask`, so the
routing of decision 0100 never ran. The lead heard nothing for about an hour,
until the owner told it. The lead then merged the PR in two minutes.

Two more things hid the state. The same member's task said "T2b worker
running" for an hour after that worker had ended. And the lead's loop tick
(decision 0094) lists only the lead's own changes, so a member's state never
showed there. The same denied-merge pattern hit four other member repos on
2026-11-08.

## Decision

1. **A blocked announce tells the lead once.** When a session in a team with
   `asks_to=lead` announces a task that reads as blocked or waiting, the
   gateway sends the team lead one notification from `agent-net`: who, the
   announce text and since when. The lead lookup is the one of
   `RedirectOwnerAsk` (`askerTeam`, `leadAskSession`).
   - An announce reads as blocked or waiting when it starts with `blocked`
     (`parseCheckAnswer`), has the phrase `blocked on` or `waiting on` as whole
     words that a `not` or `was` before them does not turn around, or set a wait
     (the `waiting` field of `agent_announce`).
   - One message for each distinct text. The gateway keeps a short fingerprint
     of the last text it told the lead about (`member_blocked_notices`, one row
     per session, never the text). A repeat announce of the same text sends
     nothing; a changed text sends again. An announce that is not blocked removes
     the row and sends nothing, so a later block is a new block.
   - No lead session: one Desk log line instead, once per text.
2. **The lead's loop tick shows its members.** For a lead of a team with
   `asks_to=lead`, the tick adds a `Members:` part, only when it has something
   to say: the online members whose announce reads blocked or waiting (or that
   set an explicit wait), with how long, and the workers they started that ended
   since the last tick or whose open change has no passed `accept` step. It is
   at most four lines for each list. In `on_change` mode the part counts as a
   change. An unreviewed worker stays in every tick until its change is accepted;
   that is the point, and it also means a lead with an old unreviewed change does
   not get quiet ticks until someone accepts, closes or stops it.
3. **A stale worker line gets a note at read time.** In the session views the
   CTO and the TUI read, a task that names a worker session id which has ended
   gets `(worker <id> ended <date> <time> UTC)` after it, in
   `Profile.TaskNote`. The stored announce is not changed. The idle check, the
   searches and the task hash read the announce, not the note.

## Why this shape

- The message is a notification, not a Desk ask. The member asked nothing, and
  an ask that waits on the lead would need an owner-side close path. The lead
  already has the tick and the Desk log.
- The check uses the words the idle check already accepts, plus two phrases
  and the explicit wait. A word list will miss some wording. It is meant to
  catch the common ones; a missed one costs what it cost before.
- A fingerprint table, not the message history, decides "again": messages are
  pruned, and a pruned message would let a repeat announce send a second one.
- The note is a separate field, not an edit of the task. The task is the
  member's own word and feeds the idle check.

## Not built

Watching GitHub for open pull requests. It is out of scope here.

## Tests

`member_blocked_test.go` (the message, the repeat, the changed text, the clear,
the Desk line, the cases that send nothing), `member_loop_test.go` (the tick, an
unreviewed worker, `on_change`, a team that asks the owner) and
`member_worker_note_test.go` (the note).
