# 0040 - CTO applies minor graph changes; owner window for major

**Date:** 2026-11-01
**Status:** Accepted by the owner; implemented
**Amends:** [0035 option A](0035-cto-is-a-standard-agent.md), rule 2

## Reason

The owner called one key per routine charter or edge change "a massive issue".
The CTO should maintain the graph without making every wording improvement a
blocking owner decision.

## Decision

Minor CTO graph changes apply at once: charter wording, accepts, needs and
consumers items, new architect nodes and normal depends edges. The gateway
reuses the learned-change classifier and its restrictive-wording rule.
Restrictive or narrowing phrases, refuses, escalate, maturity, deploy rules,
request rules (auto, ask, never), governs and deploys edges, node removal, all
node settings and anything touching a critical node are major. Undo is
classified by the reverse change. The gateway repeats the minor check inside
the mutation transaction before commit.

The owner can press `w` on the focused CTO box to open an "I'm driving" window
for 30 minutes. During it, major graph changes also apply directly. The same
key closes it early. The box shows the time left. The deadline exists only in
the CTO host daemon's memory, expires at 30 minutes and closes on restart.
Reading or toggling it requires the owner gate and a TUI subscription. An
agent caller, the CTO process and a plain CLI connection are refused. It is
not a CTO tool or setting and the CTO cannot forge or extend it.

Immediate changes use actor `CTO (minor)` or `CTO (owner window)`, write an
owner notice in the CTO box and normal graph undo history, and create no
blocking Needs you row. The existing `u` key undoes the latest CTO graph change.
Major changes outside the window remain proposals. Workers, tools, MCP,
settings, trust, policy and sends as owner always remain proposals.

The CTO brief describes these limits and points the owner to `A` in Needs you
or `w` on the CTO box. An approval typed in chat never counts.

## Security reasoning

Request rules and trust decide who can make an agent act without asking.
They stay gated outside the window. A normal dependency edge and charter
wording do not grant that permission. Node settings, including deploy rules,
also stay major. Changing or removing an edge with a non-normal rule is major.
A critical node, including either end of an edge, always makes a change major.

The window is an explicit, bounded delegation to the CTO in every turn. A
peer message can influence it during the window. The owner sees its countdown,
gets a notice for every change, and can undo. It grants no general owner
powers and does not change the read rule or the restrictions on built-in tools.
Enrolled daemons remain trusted; the operating-system user remains the trust
boundary. A gateway without support is refused before immediate mutations.
