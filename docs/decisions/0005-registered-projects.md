# 0005 - Registered projects

**Date:** 2026-10-29
**Status:** Proposed. The owner has asked for the feature; this text records how it is built and has not been reviewed yet.
**Context:** [0002](0002-workers.md) says a worker starts only in a known project, and W3 says "known" means seen in a session. [0003](0003-gateway-record-and-inventory.md) D3 says there is no projects table, and "if the launch feature needs projects that outlive their sessions, add a table then." This is that table. The owner wants to tell an agent "register all repos in ~/code to the agent net", so a project can be known before any session ran there.

## P1 - A registry table, merged with the seen projects

The gateway has a `projects` table (gateway migration 5): machine, path of the main tree, name, remote, default branch, who added it, when it was added, when it was last checked. The key is machine plus path.

`agent-net projects` (and `query.projects`) merge two sources, one row per repository per machine, with a SOURCE of `registered`, `seen` or `both`. Seen projects are still built from the stored sessions as in 0003 D3, and the session code is not changed. Because the registry is a separate table, a registered project does not go away when its sessions are deleted by retention.

For 0002 W3: the gateway resolves "project X" from this merged list. A registered project counts as known. A registered project is not a promise that the folder is still there; the daemon that starts a worker must check that.

## P2 - Registration runs on the machine that holds the folder

Paths are local. Only the daemon of that machine can see the folder, so it does the checks (the path exists and is in a git working tree), resolves the path to its main tree with `internal/repoinfo` (the rule of 0003 D2: a linked worktree belongs to its main tree, a submodule is its own project), and strips credentials from the remote. It then sends the checked entry in a new `project.add` request. The gateway stores it for the node of the connection, and checks only the shape.

The request has no field for another machine. `agent_project_add` cannot name one, the daemon ignores extra fields, and the CLI registers on the machine it runs on. This is the rule `agent_mcp_add` already has for the host of an MCP server. We chose it over letting the owner name a machine because the owner can run the CLI on that machine, and a path on a machine the daemon cannot see cannot be checked.

## P3 - Scan, with limits

`projects scan` walks a folder to a depth (default 3, at most 8), stops at a repository, does not follow symbolic links, skips hidden folders, `node_modules` and `vendor`, and looks at 200 repositories per call at most. It reports the overflow. A linked worktree it finds is skipped with its main tree named. A dry run lists what would happen.

The limits are there because an agent can call the scan. A bounded scan cannot turn "register my repos" into thousands of rows.

## P4 - Agents add and list; the owner removes

Agents get `agent_project_add` and `agent_project_list` in the Claude Code MCP server, the Pi extension and the OpenCode plugin. Removing is CLI only (`agent-net projects remove`), and the daemon refuses the method from agents. We chose this because adding a project widens where workers may start, and removing one is only a cleanup. There is no case where an agent needs to remove a project that outweighs the risk of an agent dropping projects the owner registered. The guidance text asks agents to register projects only when the owner asks.

`projects remove` can take `--on MACHINE` for an entry of a machine that is gone. It is the one way to name another machine, and only the CLI has it.

## P5 - Log and events

`project.added` and `project.removed` are events in the events table and lines in the gateway log, in the key=value format of 0003 D1. There is one line per project, with the session and harness that did it (`cli` for the CLI).

## P6 - Versions

A new daemon against an older gateway gets `unknown frame type` and reports that the gateway is too old. A new CLI against an older daemon reports that the daemon is too old. An older gateway lists projects without SOURCE, and the CLI shows those as `seen`. Nothing hangs.

## Not done

- Re-checking registered projects on a timer (the "last checked" time is set when a path is registered again).
- Removing a registration when the folder is gone.
- Registering a path from another machine.
