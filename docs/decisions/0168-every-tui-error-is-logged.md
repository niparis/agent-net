# 0168 - Every error the TUI shows is in the daemon log

**Date:** 2026-11-14
**Status:** Accepted.

## Context

The owner saw a failed Enter on his screen and asked that it be logged: "but
you have to log these, it's pretty critical". The TUI shows an error line for
many failures, and most of them were only on screen. A failed call was already
reported by the operator client (`event=client_error`), but a refusal the
screen made on its own, a plan note, and a command that failed in the
terminal never reached the daemon.

## Decision

One TUI function, `fail`, shows an error line to the owner and reports it
through the existing `client_error` report with `method=screen`, the screen,
the text the owner read, and the session when there is one. The daemon writes
`event=client_error` with `text=` and `session=`, cleaned and bounded like an
Enter failure (decision 0167). Every place that shows an error line goes
through `fail`, or its two variants: `failFor` for an error that belongs to a
named session, and `failErr` for an error a Source call returned.

A failed call is already reported by the operator client. Its error carries a
mark (`operator.ReportedError`), and `failErr` shows the line without reporting
it again, so one failure makes one line. An Enter that part 1 already reported
with `event=enter_failed` is shown without a second report for the same reason.

A scan test reads the TUI's Go files and fails when an error notice is made
outside the one function, so new code cannot bypass the report.

## Consequences

The gateway's client error counters gain rows for method `screen`. That is
intended: the owner wants screen errors counted with the other client-side
failures.

The line is best effort. If the daemon cannot be reached, the report is
dropped, like the other client reports. A report needs no new request type and
no new poll.

## Revisit when

- the daemon log is read by someone who does not want screen errors in the
  client error counters; or
- a screen error should carry more than the line and the session, for example
  the request that led to it.