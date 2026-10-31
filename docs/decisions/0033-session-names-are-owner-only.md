# 0033 - Only the owner names a session

**Date:** 2026-10-31
**Status:** Accepted by the owner
**Context:** [0024](0024-session-names.md) gave every session the name of its project, but rule 5 let an agent replace that name through `agent_announce`. A name set that way says nothing about the project, can collide with another live session, and lets an agent choose words that attract hints or look like another project. The owner decided that agents may no longer rename themselves: a session's name comes from its repository, and only the owner may change it.

## Decision

1. **An announce sets no name.** `agent_announce` takes a task, a project, capability tags and expertise. The `name` parameter is removed from the Claude Code MCP server, the Pi extension and the OpenCode plugin. The gateway ignores a `name` an older client still sends, so an old daemon cannot rename a session either.
2. **The automatic name follows the repository identity.** It is the project identity of [0031](0031-project-identity-and-display-name.md), which is the last part of the `origin` remote, or the main tree folder, or the session folder outside a repository. Rules 2 to 4 and 6 of [0024](0024-session-names.md) still apply: live duplicates get the lowest free number, a worker is `<project> worker <n>`, and a harness title only fills TASK.
3. **Only the owner renames.** The owner renames a session with the CTO tool `session_rename` (`session_id`, `name`) or the operator method of the same name. The method is behind the owner gate: an agent connection is refused, and so is a command line inside an agent session's process tree. No agent tool sets a session name.
4. **An empty name restores the automatic name.** It is the lowest free name for the session's project and worker state at that moment. If another live session took the old automatic name while this one had a name of its own, the session gets the next free number rather than a duplicate.
5. **A name the owner set survives.** Reports only write the automatic name, which is stored apart from the owner's name; every listing, hint and message uses the owner's name while it is set. A rename to a name another live session holds is refused with a clear error, because `agent_send` and `agent-net send` match exact names among online agents.
6. **Upgrade clears announce-set names.** A gateway migration clears every name that `agent_announce` had stored. Each session shows its automatic name again at its next report, so an old hand-set name does not outlive the rule. A name the owner sets after the upgrade is untouched.

This decision replaces rule 5 of [0024](0024-session-names.md). Its rules 1 to 4 and 6 remain in force.

## Consequences

- The list reads as projects: `agent-net`, `agent-net 2`, `agent-net worker 1`, plus any name the owner chose.
- The owner can still give a session a memorable name, and that name is stable across reports.
- A session cannot choose a name to attract hints or to look like another project.
- `agent_send` with an exact name keeps working, because automatic names are unique among live sessions and the owner cannot take a live session's name.
- The CTO renames only on the owner's words. The operator method is also open to the owner's command line and TUI.
