# 0082. Show the Graph by team and live state

Date: 2026-11-07
Status: Built.

The Graph overview groups members in team boxes, with a separate unassigned
box. The star marks the team lead. Repeated links between two boxes share one
line and a count by relationship kind. Box widths follow their content and the
terminal size. The selected member stays in view when the overview is taller
than the terminal.

A repo and its same-name owner or lead share one display node, including when
that agent also serves other repos. Other merges require a one-to-one pair
from ownership, graph links and session associations. Their records remain
separate on the gateway. Cards retain the agent identity and relationships.
A merged node uses the repo's team, or the agent's team if only the agent is a
member. It appears in one box. Unrelated namesakes have dim repo or agent
suffixes so their labels remain distinct.

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
