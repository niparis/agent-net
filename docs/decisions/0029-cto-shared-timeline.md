# 0029 - One CTO timeline across harnesses

**Date:** 2026-10-31
**Status:** Accepted
**Context:** [0022 component graph and CTO](0022-component-graph-and-cto.md), [reference](../reference.md#cto-settings-and-conversation).

## Decision

The CTO keeps a separate native conversation for each harness. The owner sees
one chronological timeline across those conversations. A switch records its
time, destination harness and model in the daemon's local state database.

When a harness is left, the daemon stores its messages and action lines as a
snapshot. The active harness is read from its native session; inactive
harnesses are read from their latest snapshots. This avoids starting an
inactive harness just to refresh the TUI. The timeline includes a divider at
each switch and survives daemon and TUI restarts.

The daemon returns up to 500 timeline entries at a time. The TUI requests the
older page when the owner scrolls past the oldest loaded entry. This keeps the
normal poll small while preserving access to older history.

`cto_new` still starts a new conversation only for the selected harness. It
does not clear another harness's conversation or recorded switch dividers.
