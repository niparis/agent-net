# 0003 - The gateway record, repo info and harness inventory

**Date:** 2026-10-29
**Status:** Accepted by the owner
**Context:** the plan to start a new agent session on the right machine ("start an OpenCode session in project X with this task and model"). The launch feature is described in [0002](0002-workers.md). This change builds the records it needs: workers resolve "project X" to a machine and a folder through the projects list in D3.

## D1 - One log line per event, in key=value form

The gateway logs to stderr, as before, but every line is now `ts=... event=... key=value ...`. One format for messages, delivery steps, new sessions, inventory changes and errors. The line for a stored message has both ends (ID, name, harness, host) and the size of the body. It never has the body: the body is in the database, and logs get copied to other places.

## D2 - The daemon works out the project of a session

The daemon runs `git` in the session's folder. It does this and not the gateway, because only the daemon can see the folder. The result (git root, project, remote without credentials, branch) is part of the session it already reports. It is cached per folder, has a time limit, and never fails a session.

A linked git worktree is not a project of its own. The daemon takes the main working tree from `git rev-parse --git-common-dir` (its parent, when that directory is named `.git`). The project name is the base name of that main tree, and `root` is its path. The worktree the session is in is kept in its own field, `worktree`. We do this because workers resolve "project tenebrae" to a folder, and worktree folders with names like `agent-a76c...` would fill the list. Where the common directory has another shape (a bare repository, a submodule, a separate git directory), the working tree itself is the root; a submodule is its own project.

`agent-net projects` shows one project per repository per machine. The path is the main working tree, and the linked worktrees where sessions ran are listed in the WORKTREES column, so no folder is lost.

## D3 - The projects list is built from stored sessions

There is no separate projects table. `agent-net projects` groups the stored sessions by machine and folder. A project exists as long as its sessions do, so it goes away when its last offline session is deleted (30 days by default). This keeps one source of truth. If the launch feature needs projects that outlive their sessions, add a table then.

## D4 - Harness inventory is sent in hello, with an update message

A daemon sends the installed harnesses in `hello` when it has them, and in a `node.harnesses` request when detection ends later or runs again (every 6 hours). `null` means "not reported" (an older daemon) and keeps what the gateway has. An empty list means "none installed". An older gateway ignores the extra hello field and answers "unknown frame type" to the update, which the daemon ignores.

The inventory is per machine, not per session, so agents read it with a new tool `agent_nodes`, not through `agent_list`. `agent_list` is about sessions. OpenCode is the only harness with a command that lists models (`opencode models`), so only it reports models.

## D5 - Retention is a timer in the gateway, set by flags

Message rows (with bodies) and delivery rows go after 30 days, events after 90 days, offline sessions not seen for 30 days. The periods are flags on `agent-net gateway`; `0` keeps for ever. Setup does not pass them, because the defaults are the policy. Whole message rows are deleted, not only the bodies: the events table keeps the trail (who, when, size) for 90 days.

When a session goes offline its `last_seen` is set to that time. Before this, `last_seen` was the last update, so a session that had run for 40 days would have been deleted the moment it went offline.

## D6 - Database files are private

`gateway.db` and `daemon.db`, with `-wal` and `-shm`, are mode 0600. They are created with that mode and fixed at each start.
