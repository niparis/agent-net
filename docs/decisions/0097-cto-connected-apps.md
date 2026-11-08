# 0097 - CTO owner operations and connected-app consent

Date: 2026-11-08
Status: Accepted on the owner's instruction through the CTO.

## The problem

The CTO can administer shared MCP servers, but cannot grant one to a bot,
rename or revoke a connected app, or switch internet access. The owner has to
repeat the work in the TUI. The owner asked for CTO paths for owner actions.

## Decision

Extend the existing private `mcp_change` tool with `apps_list`, `app_grant`,
`app_ungrant`, `app_rename`, `app_revoke`, `public_status`, `public_on` and
`public_off`. Keep the existing process-tree check and explicit action classes.
Run mutations immediately through the existing owner implementations.

Listing app metadata and public-access status is an explicit exception to the
public-read rule in decision 0035. Neither result includes app credentials.
It lets the CTO identify the app and the grants it must change without asking
the owner to copy the TUI inventory. Ordinary agent reads do not change.

Every call keeps the existing CTO audit line. Every successful mutation makes
a CTO notice in the Inbox and Desk log. Grant, name and public-switch notices
include undo using the previous value, including a no-op. Revoking an app
invalidates its credentials and cannot be undone. Reconnection needs new
owner consent. The existing server allow-list, bot grant validation and call
limits still apply.

Extend `settings_change` with `area: operator`, an explicit method enum and
`arguments` using the existing owner API fields. This covers owner reads and
remaining session, preference, inventory, Team, delivery, merger and backup
actions. These methods reuse owner handlers, attribution and validation. Each
call is audited; mutations make Inbox/Desk notices with existing inverse or
history undo where supported. Append-only effects have no invented delete undo.
Ordinary agent reads stay scoped to their caller. This private CTO route
supersedes decision 0035 only for the explicit owner operations.

OAuth consent, Tailscale login and phone pairing remain owner actions. The CTO
opens live-code app/phone asks with `mcp_change app_ask`; the existing owner
approval path runs on `y`. The CTO cannot approve those asks. Changing an ask's
permissions replaces its ID. Completion, refusal and expiry close it. Public
access setup opens an HTTPS login ask and retries the requested switch after
the owner's browser consent, closing the ask when enabled.

Session entry asks one connected owner TUI to run its existing entry path with
that TUI's herdr context. The relay carries identifiers only. Invites are
created on the gateway even when the CTO is hosted on another machine. Missing
private CTO identity may be restored on its existing node, without moving home.
Folder trust and standing orders are decision 0091 and are not changed here.

Every owner action in the TUI or in an owner-only operator call has a CTO
route. A daemon test walks the full list of owner operator methods and fails
when one has no route. Only three kinds have no route, each for a reason: the
owner's consent (`apps.approve`), the answer to a CTO proposal
(`proposal_answer`, `proposal_list`, `proposal_start_ack`) and the owner's
declaration that they are driving the TUI (`cto_graph_window`). The CTO
opens the consent as a one-click ask instead. Adding an owner method without a
CTO route breaks that test.

The CTO tool-list schema test budget increases from 32 KiB to 36 KiB. This
accommodates these actions alongside the parallel trust and standing-orders
tools without cutting useful descriptions. It does not change tool authority.
