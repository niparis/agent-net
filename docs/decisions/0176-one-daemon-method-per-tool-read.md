# 0176 - Ask and read tools are decided in the daemon

Date: 2026-11-20

Claude Code and Codex reach agent-net through the MCP server. Pi and OpenCode
send the generated catalog straight to the daemon. Until now some tools did
more work in the MCP server than in the daemon, so the two paths gave different
answers ([principle 18](../principles.md): each decision is made in one place).

## What was different

- `agent_owner_ask` with `kind: question`: MCP made a Desk ask. Pi and OpenCode
  sent an explicit wait, which makes no Desk ask.
- `agent_messages_find`, `agent_message_get`, `agent_workers_find` and
  `agent_mcp_servers_find`: the MCP server filtered, paged and fetched in Go. A
  plugin got the raw daemon answer, with no filter, no page and no cursor. Pi
  and OpenCode also added `online_only` to workers by themselves.

## What we do

The daemon has one method for each of these tools. Its params are the tool
input and its answer is the tool output.

| Tool | Daemon method |
|---|---|
| `agent_owner_ask` | `owner_ask` (picks `ask_question`, `request_approval` or `propose_idea` from `kind`) |
| `agent_messages_find` | `messages_find` |
| `agent_message_get` | `message_get` |
| `agent_workers_find` | `workers_find` |
| `agent_mcp_servers_find` | `servers_find` |

The method name `mcp_servers_find` was not used: the daemon sends every
`mcp_` method to the MCP registry.

The MCP handlers forward the tool input to the method and do nothing else. The
plugins do the same, with no mapping for these tools. The code that filters and
pages stays in `internal/mcpserver` (`ServeRead`) because that package already
holds the catalog output shapes. The daemon calls it with the session bound to
the connection, so the caller is not read from the environment any more.

A plugin that leaves out `detail` gets `concise`, as the MCP schema does, so a
cursor made through one path works through the other.

The old `messages`, `message_status`, `list` and `mcp_list` methods stay as
they were. The TUI, the CLI and the CTO read them.

## Left as it was

The CTO has its own copy of these reads in `internal/daemon/cto_catalog_reads.go`.
It is a third copy of the same logic. Moving it onto `ServeRead` would change
its output and its cursors, so it is a separate task.
