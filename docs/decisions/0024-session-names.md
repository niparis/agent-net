# 0024 - Session names are project names

**Date:** 2026-10-30
**Status:** Accepted by the owner. Rule 5 is replaced by [0032](0032-session-names-are-owner-only.md): only the owner renames a session, and an announce sets no name.
**Context:** The session list showed names the harnesses made up: `tenebrae-53`, `a2a-ocs-4f`, a Codex thread title (`Plan portal apps and project de...`), `wbg-arch: Open the server`, `Repo: Legion & Legends (brow...`. They say nothing about the project, they change when a title changes, and they make an exact name hard to use. The owner asked for one rule for every harness.

## Decision

1. **The name is the project name.** It is the base name of the main working tree folder, the project of [0003](0003-gateway-record-and-inventory.md). A linked worktree uses the main tree, so a session in `agent-a76c...` is `tenebrae`. Not in git: the folder name. The git remote plays no part.
2. **Duplicates get a number.** The first live session keeps the name. The next live session with the same name gets ` 2`, then ` 3`: the lowest free number. A session keeps its number while it lives. Numbers count live sessions only, so a name becomes free when its session goes offline. A session that comes back after another took its name gets the next free number.
3. **A worker is `<project> worker <n>`**, with `n` the lowest free number among the live workers of that project.
4. **A harness title is not the name.** A Codex thread name, a Claude session title and an OpenCode session title go to the TASK column when the session announced no task. A title derived from the folder (Claude's `nameSource: "derived"`) is dropped. A worker's TASK is the first line of its task.
5. **A name set by hand wins.** A name the agent passes to `agent_announce` still replaces the automatic name for the rest of the session. The TUI `N` key names a machine, not a session, and is unchanged. Agent-net gives no other way to rename a session.
6. **Exact names still work.** Because automatic names are unique among online agents, `agent_send` and `agent-net send` can use the name. A name set by hand can still collide with another name; use a session ID then.

The gateway owns this rule. It gives a session its name when it first stores it, and checks the name again on every report: a live session keeps a name that still fits its project and is free, and otherwise gets the lowest free one. The daemon sends the harness title in `SessionInfo.Name`, and the gateway stores it in the new `sessions.title` column. The store is the only place that knows every live session on the network, so numbering there is the same on every machine.

## Consequences

- The list reads as projects: `agent-net`, `agent-net 2`, `agent-net worker 1`.
- A name can change once, when the gateway is upgraded: an old automatic name is replaced at the session's next report. A name set by hand is untouched.
- The harness title is still visible in TASK, and `agent-net list --json` keeps `title` apart from `profile.name`.
- Claude sessions whose name is derived from the folder no longer fill TASK with `folder-4f`.
- A session that is offline does not hold a name. A new live session may take it; the old session gets a number when it returns.
- The gateway numbers names from the project it was given. A daemon that sends no repository info still gets the folder name.
