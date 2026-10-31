# MCP server registry

Register an MCP server once. Every agent session on every machine can then use its tools.

The gateway keeps the registry. The machine that hosts a server runs it: for a command, its daemon starts and restarts the process; for a URL, its daemon connects as an MCP client. A call goes from the agent's session to its own daemon, to the gateway, to the hosting daemon, to the server, and back. Secrets stay on the hosting machine.

Version 1 covers tools only (no resources or prompts), stdio, streamable HTTP and SSE, and bearer or custom header auth. OAuth and per-host scoping come later. A server can be limited to some projects: see [scoped MCP servers](reference.md#scoped-mcp-servers).

## Add a server

On the command line (the machine you run this on hosts the server):

```
# A stdio server. Everything after -- is the command.
agent-net mcp add files --env GITHUB_TOKEN -- npx -y @modelcontextprotocol/server-filesystem /srv/data

# Host it on another machine (host name or node ID). CLI only.
agent-net mcp add files --on nas -- /usr/local/bin/files-mcp

# A remote server. The header value is read on this machine, at connect time.
agent-net mcp add homelab --url https://nas.lan/mcp --header-env 'Authorization=HOMELAB_TOKEN'
agent-net mcp add homelab --url https://nas.lan/mcp --header-file 'Authorization=~/.secrets/homelab' --timeout 120s
agent-net mcp add legacy --url https://old.lan/sse --transport sse
```

Other commands:

```
agent-net mcp list              # host, transport, short target, auth source, enabled, health, tool count, who added it
agent-net mcp list --wide       # same, with the full command line in TARGET
agent-net mcp test NAME         # the host connects now, lists tools, reports health
agent-net mcp call NAME TOOL '{"arg":"value"}'   # one call, for checks
agent-net mcp enable NAME
agent-net mcp disable NAME
agent-net mcp remove NAME
```

`agent-net mcp` with no subcommand is still the stdio MCP server that Claude Code starts. Nothing about that changed.

An agent can do the same with the tools `agent_mcp_add`, `agent_mcp_list`, `agent_mcp_test` and `agent_mcp_remove`. A server an agent adds is always hosted on that agent's own machine, and its auth can only name an env var or a file path on that machine. Every result says the change applies to every agent on every machine. The guidance text tells agents to ask you before adding or removing a server.

`add` connects to the server and lists its tools before it saves anything. If that fails, nothing is saved and the error says why.

## How agents see the tools

A registered tool appears as `<server>__<tool>` with the server's own description and input schema. The description starts with `[<server> on <host>]`. Server names are lowercase letters, numbers and single hyphens, at most 24 characters, and unique. (In Claude Code the full tool name also carries its usual prefix, for example `mcp__agent-net__files__read_file`.)

When the registry changes, or a server reports a new tool list, the gateway tells every daemon and each daemon tells its sessions.

| Harness | What happens | Tested behavior |
|---|---|---|
| Claude Code | `agent-net mcp` adds and removes tools on its MCP server. The SDK sends `notifications/tools/list_changed` (after a short debounce) and Claude Code re-reads the list. | Works in the running session, no restart. A tool added with `agent_mcp_add` was found and called in the same conversation, and was gone after `agent_mcp_remove`. |
| Pi | The extension registers tools with `pi.registerTool` on connect, on the daemon's `mcp_changed` push, and every 15 s. Pi cannot unregister, so a removed tool is registered again as `hidden`. | Works in the running session, no restart. Pi reads its tool list when a run starts, so a tool added during a run is usable from the next prompt, not later in the same run. |

If the daemon is down, tools that are already registered stay in place, and calls fail with "agent-net daemon not reachable".

## Limits

- Timeout per call: 60 s by default, set per server with `--timeout` (at most 10 minutes). The host enforces it and the error says which tool timed out.
- Result size: 512 KiB. A larger result is cut and ends with a note saying so. Arguments are limited to 256 KiB.
- A stdio server that exits is restarted with a growing delay (1 s up to 1 min). A server that stays up for 30 s resets the delay. Its stderr goes to the daemon log.
- Health: the host pings each server every 60 s and reports changes. `list` shows the last report. The tool list is cached in the gateway, so tools stay listed while the host is offline, and calls to them say the host is offline.

## Auth

A header is given as a source, never a value:

- `--header-env 'Authorization=VAR'` reads the environment variable `VAR` of the hosting daemon.
- `--header-file 'Authorization=PATH'` reads the file at `PATH` on the hosting machine (`~` works). Trailing whitespace is trimmed.

For an `Authorization` header without a scheme (no space in the value), the host adds `Bearer `. Any other value is sent as it is.

Env vars for a stdio server are given as names with `--env NAME`. The host passes those variables, plus a small base set (`PATH`, `HOME`, `USER`, `LANG`, `TMPDIR` and a few more), to the process. The daemon's other variables do not reach it.

The daemon reads variables from its own environment. If you run the daemon as a service, put the variables in the service's environment, not only in your shell.

## Security

- **Only enrolled nodes** can call the registry. It uses the same node token as every other gateway call.
- **The registry holds no secrets.** An entry has the command, the args, the env var names, the URL, and for each header its name and the env var name or file path. Args and the URL are stored and shown to everyone, so keep secrets out of them. A URL with credentials (`user:pass@`) is refused. `NAME=value` for an env var is refused without echoing the value.
- **Secrets stay on the host.** Header values and env values are read on the hosting machine at connect time. They are never sent to the gateway or to another daemon.
- **Redaction.** The hosting daemon removes every secret value it read from error text, stderr lines in the log, and tool results (including the `Bearer ` form and the bare token) before anything leaves the host. Gateway and daemon logs and the audit log never get a value, because none reaches them. A wrong token gives a plain error such as "the server rejected the credentials (HTTP 401)".
- **A daemon runs only its own servers.** The gateway tells each node which servers it hosts, and the daemon ignores any entry that names another node. A stdio server added by an agent is hosted on that agent's machine, whatever the agent asks for.
- **Who added what.** Each entry records the node, the session and the harness that added it, and `list` shows it. The gateway's audit log records adds, removes, enables, disables and refused adds.
- **A registered server is code that runs as your user** on the hosting machine, and its tools run for any agent on any enrolled machine. Treat `mcp add` like `ssh`: add only what you would run yourself. An agent can add a server, so approve those calls in your agent's permission settings, or rely on the guidance text only if you trust the agent. A compromised enrolled machine can add servers that run on itself, and can call tools of servers hosted elsewhere.
- **Tool results are text from a server.** A hostile server can try prompt injection through results. The same advice as for messages applies: your permission rules are the barrier.
