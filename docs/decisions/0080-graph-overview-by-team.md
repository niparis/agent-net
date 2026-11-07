# 0080. Show the Graph by team and live state

Date: 2026-11-07
Status: Built.

The Graph overview groups members in team boxes, with a separate unassigned
box. The star marks the team lead. Repeated links between two boxes share one
line and a count by relationship kind. Box widths follow their content and the
terminal size. The selected member stays in view when the overview is taller
than the terminal.

A repo and agent share one display node only when their ownership, graph links
and session associations establish a one-to-one pair. Their records remain
separate on the gateway. Cards retain the agent identity and the full set of
relationships. Shared agents and repos with several agents stay separate.

Dormant identities fold into one selectable chip. Dormant means no online
session and no session activity within 30 days, an archived record, or a
name matching tmp, tmp.*, or *-scratch-test. The owner can reveal them with d.
The owner can remove one with x, or select the chip and remove all with X.
One confirmation precedes the existing repo_archive and agent_tombstone calls.
Successful records leave the local view immediately; errors appear in a notice.
The gateway retains archived records and their history.

Repeated edges covering all or nearly all repos become a mark beside the
source or target instead of many lines. Cards still list the relationships.
The f key focuses on a node and its direct neighbours, or zooms into a team's
members and their direct outside neighbours. Outside nodes are dim. Escape
returns one level. Colours follow session state: needs you, busy, idle, offline.
These display choices add no authority, session limits or request checks.
