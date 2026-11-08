# 0094 - CTO connected-app administration

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

OAuth consent, a Tailscale login and phone pairing remain owner actions.
One-click asks for those flows and the remaining operator gaps are separate
work. This decision does not change folder trust or standing orders.
