# 0077. Group sessions by owner attention

Date: 2026-11-07

Sessions groups are Needs you, Working and Idle. Machine becomes a column.
This replaces the Mine and machine groups from decision 0072. Owner use and
pins still inform the finder; pinned sessions stay visible in Idle.

Build caller trees first. Workers belong to their caller. A merger belongs to
its project's explicit online repo lead, then its starter if known, otherwise
it stays at the top level. A tree takes its most urgent member's group. Text
filters keep the whole matching tree so the caller stays with its children.

The active view folds unpinned idle trees into a count. The existing v key
shows all idle and ended sessions. z toggles the selected tree, including
when a child is selected. Z toggles all visible trees. Collapse state lasts
for the TUI run and collapsed tasks show the descendant and busy counts.
These keys leave the cell editing controls unchanged.

The first group header carries the Inbox decision count. CTO and merger
roles appear as dim name tags instead of the TYPE column from decision 0063.
