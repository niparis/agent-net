# 0116 - Defect hints and rework from a bad brief

Date: 2026-11-09
Status: Built.

## Context

The rule is known and still missed. A survey of the tenebrae work on
2026-11-09 found two defects in a feed-sync worker that nobody filed. The lead
went straight to a fix worker. The same happened to a mislabel in a pull
request: a later pull request fixed it and no defect was recorded. The fix
always happens; the filing is the step that is forgotten.

The gateway already records a defect when a fix names `fixes=<change id>`
(decision 0103), but nothing suggests the link. The hand-back carries the diff
and the change record, so the files are already there when the caller reads
the notice.

A second kind of rework has no honest home. A stale-base brief caused a
conflicting pull request, and the briefing session's mistake could only be
counted against the worker, which did nothing wrong.

## Decision

1. **Start hint.** When a worker starts with no `fixes` and its title or task
   says fix, bug, defect, regression or broken, the result carries one line:
   `If this fixes earlier work, pass fixes=<change id> so agent-net counts the
   defect. Recent changes in <project>: <up to 3 ids, titles, workers, merged
   dates>`. Recent means merged in that project in the last seven days.
2. **Hand-back hint.** When the worker's diff touches a file that a merged
   change by another worker in the same project changed in the last seven
   days, and the start named no `fixes`, the notice carries one line naming
   that change and worker. The daemon asks the gateway for up to five recent
   changes and makes one git call per candidate for its file list. A change
   made by the same worker is never named.
3. **`brief_defect`.** `agent_worker_rate` takes `kind: "brief_defect"` with a
   severity from 1 to 4 and a note. It is filed by the caller about its own
   brief, or by a lead above the briefing session about a member's brief,
   and charged to the briefing session. It is stored in
   `worker_brief_defects` with the briefing session, the worker run it caused
   and the note. It never enters `worker_defects`, so the picker's quality
   data and the worker's score do not change. The worker log detail shows the
   count on the caller line, and the calibration data keeps one count per
   caller.
4. **Checklist line.** The worker notice checklist says: `Found a defect in
   earlier work? Set fixes=<change> on the fix, or file defect_found_later.
   Rework from your own brief: brief_defect.`

There is no new setting, no new approval and no gate. Every hint is text. A
gateway or git failure, an older gateway or a repository with no delivery
change leaves the notice as it is.

## Consequences

- The hand-back hint costs one gateway read and up to six git calls at a
  hand-back: one for the worker's diff and one per recent change, at most five.
  A large project pays this once per hand-back, not per message.
- The hint can name a change the worker did not fix. It is a question in the
  notice, not a record: only `fixes` or a manual defect writes a row.
- A lead that briefed a member directly is that worker's caller, so the
  existing caller check covers the lead case. A lead that wants to report a
  brief written by someone else must ask the briefing session to file it.
- The picker improves only when a fix names `fixes` or someone files
  `defect_found_later`. `brief_defect` deliberately does not move a worker's
  score.
