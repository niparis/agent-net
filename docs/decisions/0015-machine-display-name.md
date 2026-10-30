# 0015 - A display name for a machine, set once by the owner

**Date:** 2026-10-30
**Status:** Built on branch `worktree-agent-a14c599b1e4ce39ce`. Proposed until the owner has read it.
**Context:** The name of a machine was the host name its daemon reports in `hello`, and the gateway overwrote it at every hello. On macOS that name can be long (`Johns-MacBook-Pro-950`). The owner wants a short name such as `laptop-1`, set once, without doing anything on that machine.

Base: [0008](0008-operator-interface-and-worker-build.md) (the owner gate, one operator interface for the TUI and the phone), [0013](0013-peer-trust-and-hints.md) and [0014](0014-delegation-policy.md) (the pattern for an owner-only setting kept on the gateway).

## Decision

1. **The gateway stores two names per node:** `reported_hostname` (what the daemon said in `hello`) and `display_name` (what the owner set, empty by default). The existing column `nodes.hostname` now holds the name to show: the display name when set, else the reported name. It is rewritten in one place (`SetNodeHostname` at hello, `SetNodeDisplayName` at a rename). Every query that reads `n.hostname` (agents, nodes, projects, MCP servers, events, workers, notices, the log) therefore shows the display name without a change. Migration 12 adds the two columns and copies `hostname` into `reported_hostname`.
2. **Operator method `node_rename`** (`{node_id, name}`), owner only, behind the same gate as `session_trust` and `session_policy`. The daemon sends the gateway frame `node.rename`. An empty name clears the display name. CLI form: `agent-net nodes rename <node-id|name> [new name]`. TUI: key `N` on a session of the machine, one-line prompt, enter saves, empty clears, esc cancels. Both go through `operator.RenameNode`, as the phone will.
3. **The daemon learns its shown name.** The answer to `hello` carries `hostname` (the name the gateway lists), and the gateway pushes `node.name` to the renamed machine when it is online. The daemon keeps it in `shownHost` and uses `d.hostName()` where it used `cfg.Hostname`: the `Self` host of the hint engine (it compares with the host names of the candidates the gateway lists, so a session on the same machine is still "same host"), the host in enter plans, worker notices, project add results, usage and `info`. `cfg.Hostname` is still what goes into `hello`. An older daemon ignores the `node.name` frame and keeps using its own host name for itself; every listing still shows the display name.
4. **Rules:** 1 to 63 characters, letters, digits, `-`, `_`, `.`, starting with a letter or digit (`protocol.CleanNodeName`). It may be an ssh target key in `enter_ssh_hosts`, which is looked up by the name the gateway lists, so `enter_ssh_hosts` follows the display name with no code change: the owner uses the display name as the key. Unique among nodes ignoring case, also against the name another machine is listed under. A later `hello` of another machine that reports a name equal to a display name is accepted, because the gateway cannot refuse a host name; lookups by name then match both.
5. **Not done:** the owner session name (`you (on <host>)`) is fixed when the daemon starts and keeps the reported host name until a restart. Old events keep the name that was in their text when they were written.

## Why not store the name on the machine

A `node.json` setting needs a step on each machine and a restart, and a long macOS name would show until then. The owner has all machines in front of them in the TUI, so the gateway is the place to set it once.
