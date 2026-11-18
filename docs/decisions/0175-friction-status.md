# 0175 - Read status of friction reports

Date: 2026-11-18

The owner asked to store the read status of the friction reports. Until now
the CTO read the same reports again on every look, and nothing said which ones
were handled. `friction_find` also returned at most 200 rows and had no cursor,
so some reports could not be read at all.

This decision keeps the reports as they are (decision [0170](0170-friction-reports.md))
and adds a state to each one.

## The states

A report holds one state: `new`, `read`, `triaged`, `fixed`, `wontfix` or
`infra`. A report nobody marked is `new`.

## Storage

The gateway adds two tables at the end of its migrations:

- `friction_status` holds the current mark of each report: the state, who set
  it, when, a link and a note.
- `friction_status_history` holds every mark a report received. Rows are added
  and never changed.

The reports in `friction` do not change. An old database reads its reports as
`new` after the upgrade.

## Who sets a state

The owner's command line and TUI, and the CTO, set a state with `friction_mark`.
That is the operator tier, so an agent session is refused, workers included.

Leads are not in this first version. The daemon has no lead-only gate that a
lead's tool list could use, so a lead gate needs its own design first.

## Groups

Reports that say the same thing share a group id. The id is a short hash of the
area and the text, with paths, ids and numbers taken out. One mark by group
closes every report in it.

The group id is computed when a report is read. Nothing is stored for it, so
there is no backfill. The id depends on the normalizing rules. A change to those
rules gives new ids, and an id an owner wrote down stops matching.

## The read

`friction_find` filters by project, area, status, text, group and `report_id`, and
since a time. It returns newest first. A page holds 20 reports by default and at
most 200. When more follow, the reply names `next_cursor`, and the next call
passes it back as `cursor`. The total, the unread count and the per-project
counts cover every report the filter holds, not only the page.

## The mark

`friction_mark` picks reports in one of three ways: a list of ids, a group id,
or a filter on project, area and a text match. It takes exactly one way. With
`dry_run` it counts the matches and writes nothing, so a filter can be checked
first.

A mark sets the state, the link and the note. A link is a change id such as
`ch_1234` or a web address. A mark without a link or a note clears them, and
the history keeps what they were.

## Not changed

Friction rows still never score a worker, and they still stay out of the picker
and the defect counts. Decision 0170 holds.
