# 0028 - Shared TUI hidden items

**Date:** 2026-10-31
**Status:** Accepted
**Context:** [0004 TUI and event feed](0004-tui-and-event-feed.md), [0022 component graph](0022-component-graph-and-cto.md), [reference](../reference.md#the-tui-screen), [security](../security.md#shared-tui-hidden-items).

## Decision

The owner can hide sessions and project nodes from the TUI without deleting
them. The gateway stores this preference in the existing `owner_settings` table
under `hidden_items`, so all machines display the same list. Session entries
use session IDs and survive a session returning online. The gateway removes an
ended session's entry after 30 days offline. Node entries use project names and
remain until unhidden.

`hidden_get` and `hidden_set` are owner-only operator methods. The daemon
refuses agent clients and command lines running inside an agent session's
process tree. Gateway node tokens remain trusted for writes, as with other
owner settings.

On Sessions, `h` hides or unhides the selected session and `H` toggles showing
hidden sessions as dim, tagged rows. The existing `x` stops a selected worker,
so it remains unchanged. On Map, `h` hides or unhides the selected node and `H`
toggles showing hidden nodes as dim, tagged rows. Each tab shows its hidden
item count while the preference is non-empty.

Hidden items affect the TUI display only. They do not remove gateway records or
limit access to sessions, messages or graph data.
