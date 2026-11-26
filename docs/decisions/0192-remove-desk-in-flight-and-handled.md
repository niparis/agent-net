# 0192 - Remove the Desk's In flight and Handled for you

Date: 2026-11-26
Status: Approved by the owner on 2026-11-26 ("I want to completely remove the
following features: Desk 'In flight' and Desk 'handled for you'", "no no we
delete the data. that whole feature was a failure", "dont forget to delete the
tests", "have you also removed the database logic? all the internal logic?").
Removes what [0090](0090-asks-and-the-desk.md) planned and what
[0114](0114-chief-of-staff.md) and [0122](0122-web-console.md) built for those
two lists. Those three decisions stay as history and are not edited.

## Context

The Desk showed three lists: Needs you, In flight and Handled for you. In
flight listed open delivery changes. Handled for you listed the chief of
staff's sorts of the last day and had its own history-hiding feature: preview,
apply and undo, with a receipt in `desk_log` and a `hidden_at` column on
`ask_sorts`. The owner never used either list and asked for every part of them
to go, including the stored data. The work came in three parts: the TUI in
part 1, the web console in part 2, and the gateway, tools, tests and data in
part 3.

## Decision

- The screens are gone. The Desk shows the Needs you list and the log. The web
  console and the phone show the same. The Changes screen still shows every
  change and its steps, and `agent_changes_find` still reads one change.
- The gateway read `chief.sorts` and the operator method `chief_sorts` are
  gone, with their protocol types. The CTO no longer lists them.
- The history-hiding feature is gone: `chief_hide.go`, the `hide` result of
  `agent_ask_sort`, its preview, apply and undo, its filter fields and the
  `hidden_count` of the list stats.
- The data is deleted, not kept. A new gateway migration at the end deletes
  the `hide_receipt` rows from `desk_log` and drops `ask_sorts.hidden_at`.
  The existing `ask_sorts` rows and the other `desk_log` rows stay.
- Other Desk features stay: sort history, undo, stats, chief health,
  `ask.mark` with the two marks, and `desk_log`. The mark `should_have_asked`
  is still stored when the owner marks an ask the chief already sorted.
- The tests of the removed lists and the hiding feature are removed. A test of
  the new migration runs it on a store that holds receipts and the column and
  checks that only those are gone.

## Why

The owner asked for it. Two lists he never used cost a gateway read on every
Desk refresh, a gateway operation, protocol types and a reversible-write
feature with its own receipts. Deleting the data too keeps the schema honest:
no column and no log rows remain for a feature nobody can reach.
