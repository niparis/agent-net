# 0062 - Partial skill inventory and tool plans

**Date:** 2026-11-04
**Context:** [0027](0027-skills-and-mcp-management.md),
[0048](0048-skills-on-off-per-project.md).

One skill above the scan's 64 MiB file-data limit made every skill switch on
that machine fail. Inventory already kept fresh observations alongside failed
ones, but planning rejected the entire partial report. It also uploaded the
locally merged stale report, which the gateway refuses as a fresh scan.

A fresh partial scan may support a skill plan when its failed items have other
names and disjoint source folders. The same name, overlapping folder or entry
alias remains a conflict. A failed source root with unknown names can hide any
skill, so it remains a conflict. Whole-machine failures, report limits, missing
reports, reports older than 15 minutes and scans without successful items
remain conflicts. Planning requires acknowledgement of the fresh scan upload;
it never sends a locally merged stale report as a fresh scan. Apply repeats the
checks, and absence verification uses the same rules.

Keep the actual failure reason when retaining a previous observation as stale.
Conflict lines name the skill, project or source folder and the rule that
failed. Tools shows `cannot manage: <reason>` before the owner tries a switch.
No scan, import, path, secret or ownership limit is relaxed.
