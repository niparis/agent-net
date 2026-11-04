# 0062 - New projects from the CTO

**Date:** 2026-11-04
**Status:** Accepted.

## Context

The owner wants to name a new project in the CTO conversation and open its
main session from Sessions. Placement should follow the existing layout and
answers the owner has already given.

## Decision

The CTO uses the projects layout scan to choose a machine, root and group.
It uses the group's machine when a group matches. Otherwise it uses the
machine running the owner's TUI. It asks one short question if the scan and
learned answers do not decide. The layout scan is provided by the separate
projects_layout change.

The CTO stores placement answers in its private daemon state and reads them
when choosing later placements. It proposes the folder, machine and agent,
then asks for approval in a one-line dialog in the CTO conversation. When the
harness has no dialog, the proposal stays open in the Inbox. A correction
requires a fresh proposal. The folder is made, git is initialized and
the project registered only after approval.

When the owner supplies a remote, project creation clones it instead of
initializing an empty repository. The confirmation names the remote. Only
`git@host:owner/repo.git` and `https://host/owner/repo.git` forms are accepted.
The destination must not exist. The clone is registered and started under
the same rules as a new project.

The default main agent is Claude with claude-opus-5-5, unless the owner names
another agent or has given a different default. An approved Claude start runs
in the background and opens no view, locally or remotely. The owner selects
it in Sessions and presses Enter. A start from the TUI's new-session form is
unchanged. Codex, OpenCode and Pi starts are unchanged by this decision.
