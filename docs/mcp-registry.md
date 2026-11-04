# MCP server registry

Register an MCP server once. Every agent session on every machine can then use its tools.

The gateway keeps the registry. The machine that hosts a server runs it: for a command, its daemon starts and restarts the process; for a URL, its daemon connects as an MCP client. A call goes from the agent's session to its own daemon, to the gateway, to the hosting daemon, to the server, and back. Secrets stay on the hosting machine.

The registry passes through tools, resources and prompts, over stdio, streamable HTTP and SSE, with bearer or custom header auth. OAuth logins and per-host scoping come later (see [OAuth logins](#oauth-logins-not-built)). A server can be limited to some projects: see [scoped MCP servers](reference.md#scoped-mcp-servers).

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

Only the owner can add or remove a shared server, through the TUI or CLI. A CTO can propose an add or remove with `mcp_change`; the owner must approve it. Agent sessions can list and test servers. The daemon also rejects add or remove requests from native plugins.

`add` connects to the server and lists its tools before it saves anything. If that fails, nothing is saved and the error says why.

## How agents see the tools

A registered tool appears as `<server>__<tool>` with the server's own description and input schema. The description starts with `[<server> on <host>]`. Server names are lowercase letters, numbers and single hyphens, at most 24 characters, and unique. (In Claude Code the full tool name also carries its usual prefix, for example `mcp__agent-net__files__read_file`.)

When the registry changes, or a server reports a new tool list, the gateway tells every daemon and each daemon tells its sessions.

| Harness | What happens | Tested behavior |
|---|---|---|
| Claude Code | `agent-net mcp` updates the registered server tools on its MCP server. The SDK sends `notifications/tools/list_changed` (after a short debounce) and Claude Code re-reads the list. | Owner changes through the TUI or CLI appear in the running session, no restart. |
| Pi | The extension registers tools with `pi.registerTool` on connect, on the daemon's `mcp_changed` push, and every 15 s. Pi cannot unregister, so a removed tool is registered again as `hidden`. | Works in the running session, no restart. Pi reads its tool list when a run starts, so a tool added during a run is usable from the next prompt, not later in the same run. |
| Codex | One `agent-net mcp --codex` process serves all threads and has no project, so it lists global servers only. It updates its list when the daemon pushes `mcp_changed`. | Tested with a fake app server: a global server is listed and callable from every thread; after a scope change to projects it is dropped from the list. Whether the real Codex re-reads the list in a running session is not tested. |
| OpenCode | The plugin does not read the registry yet, so OpenCode sessions get no federated tools. | The daemon pushes `mcp_changed` and answers `mcp_tools` filtered for the project (tested with a fake plugin). |

If the daemon is down, tools that are already registered stay in place, and calls fail with "agent-net daemon not reachable".

## How agents see resources and prompts

agent-net's own MCP server (`agent-net mcp`, and `agent-net mcp --codex`) also passes through the resources, resource templates and prompts of the registered servers. In Claude Code they are what `ListMcpResources` and `ReadMcpResource` show for the server `agent-net`, and the prompts are slash commands.

| A server's | Shows to the agent as | Example |
|---|---|---|
| resource `URI` | `agent-net://<server>/<URI>`, name `<server>__<name>` | `agent-net://files/file:///srv/data/notes.md` |
| template `URI-TEMPLATE` | `agent-net://<server>/<URI-TEMPLATE>` | `agent-net://files/file:///srv/data/{path}` |
| prompt `NAME` | `<server>__<NAME>`, with the same arguments | `files__summarize` (in Claude Code `/mcp__agent-net__files__summarize`) |

The description starts with `[<server> on <host>]`, as for tools. Two servers with the same URIs or prompt names never clash: the server name is part of each one. A read of `agent-net://files/file:///x` asks the server `files` for `file:///x`, and the answer carries the agent-net URI again. A resource link or embedded resource in a prompt gets the agent-net URI too, so the agent can read it through agent-net. Binary resources come as blobs with the server's mime type.

- **Fresh lists.** agent-net does not store resources or prompts. Each list asks every server the session may see, at the same time. The host reuses a server's list for 10 seconds.
- **Changes.** When a server sends `notifications/resources/list_changed` or `notifications/prompts/list_changed`, its host drops the cached lists at once and tells the gateway (at most once a second per server). The gateway sends `mcp.changed` to every daemon, and `agent-net mcp` sends `list_changed` for resources and prompts to its client. A registry change (add, remove, enable, scope, health) does the same. A server that sends no notifications is read again after the 10 seconds.
- **Offline or failing servers** are left out of a list; the other servers still list. A read or prompt from such a server fails with the reason, for example `host nas offline`.
- **Access** is checked at the gateway on every list, read and prompt, with the rules of a tool call: the session's project must see the server now, and the server must be enabled. The owner's command line has the owner's authority. The tool allow-list of an imported server limits tools only; resources and prompts are reads.
- **Codex** gets the global servers only, as for tools: its resource requests carry no thread.
- **Pi and OpenCode** use agent-net's plugins, not its MCP server, so they do not get resources or prompts.

## Limits

- Timeout per call: 60 s by default, set per server with `--timeout` (at most 10 minutes). The host enforces it and the error says which tool timed out.
- Result size: 512 KiB. A larger result is cut and ends with a note saying so. Arguments are limited to 256 KiB.
- Resource reads and prompts: 512 KiB, measured as the JSON answer (a blob counts in base64). A larger one is refused with an error that names the resource or prompt, its size and the limit; it is never cut. They use the server's timeout. Prompt arguments are limited to 256 KiB, a URI or prompt name to 8 KiB.
- Lists of resources, templates or prompts: at most 1,000 items and 512 KiB per server; the rest is left out. One list of all servers holds at most 2 MiB; a server past that is left out. Each host has 15 s to list.
- A stdio server that exits is restarted with a growing delay (1 s up to 1 min). A server that stays up for 30 s resets the delay. Its stderr goes to the daemon log.
- Health: the host pings each server every 60 s and reports changes. `list` shows the last report. The tool list is cached in the gateway, so tools stay listed while the host is offline, and calls to them say the host is offline.

## Auth

A header is given as a source, never a value:

- `--header-env 'Authorization=VAR'` reads the environment variable `VAR` of the hosting daemon.
- `--header-file 'Authorization=PATH'` reads the file at `PATH` on the hosting machine (`~` works). Trailing whitespace is trimmed.

For an `Authorization` header without a scheme (no space in the value), the host adds `Bearer `. Any other value is sent as it is.

Env vars for a stdio server are given as names with `--env NAME`. The host passes those variables, plus a small base set (`PATH`, `HOME`, `USER`, `LANG`, `TMPDIR` and a few more), to the process. The daemon's other variables do not reach it.

The daemon reads variables from its own environment. If you run the daemon as a service, put the variables in the service's environment, not only in your shell.

## OAuth logins (not built)

A server that needs an OAuth login (for example a hosted MCP server that sends `401` with `WWW-Authenticate` and OAuth metadata) cannot be added yet. Today the host only sends a fixed header value. A login would need:

- **A login flow on the host.** The host is an MCP client, so it would discover the authorization server (protected resource metadata, RFC 9728, and authorization server metadata, RFC 8414), register itself (dynamic client registration, RFC 7591, or a client ID the owner gives) and run the authorization code flow with PKCE. The redirect needs a browser and a callback: a loopback port on the host, or a device code shown in the TUI where the server supports it.
- **The owner in the loop.** The TUI would show the login link or code for a server that needs one, and the owner signs in. No agent can start or finish a login.
- **Token storage on the host.** Access and refresh tokens stay on the hosting machine, in a mode 0600 file under the state folder, never in the registry, the gateway or a log. The registry entry would only say "OAuth".
- **Refresh and failure.** The host refreshes the access token before it expires, retries a call once after a refresh on `401`, and reports health `error` with "login needed" when the refresh fails, so the TUI can ask the owner to sign in again.
- **Redaction** of both tokens, like header values today, and an uninstall step that deletes the token files.

## Security

- **Only enrolled nodes** can call the registry. It uses the same node token as every other gateway call.
- **The registry holds no secrets.** An entry has the command, the args, the env var names, the URL, and for each header its name and the env var name or file path. Args and the URL are stored and shown to everyone, so keep secrets out of them. A URL with credentials (`user:pass@`) is refused. `NAME=value` for an env var is refused without echoing the value.
- **Secrets stay on the host.** Header values and env values are read on the hosting machine at connect time. They are never sent to the gateway or to another daemon.
- **Redaction.** The hosting daemon removes every secret value it read from error text, stderr lines in the log, and tool results (including the `Bearer ` form and the bare token) before anything leaves the host. Gateway and daemon logs and the audit log never get a value, because none reaches them. A wrong token gives a plain error such as "the server rejected the credentials (HTTP 401)".
- **A daemon runs only its own servers.** The gateway tells each node which servers it hosts, and the daemon ignores any entry that names another node. A stdio server added by the owner is hosted on the machine selected in the CLI or TUI.
- **Who added what.** Each entry records the node, the session and the harness that added it, and `list` shows it. The gateway's audit log records adds, removes, enables, disables and refused adds.
- **A registered server is code that runs as your user** on the hosting machine, and its tools run for any agent on any enrolled machine. Treat `mcp add` like `ssh`: add only what you would run yourself. The daemon blocks agent clients from adding or removing servers. The gateway trusts enrolled daemons, so a compromised machine can still impersonate its owner to register servers on itself and can call tools of servers hosted elsewhere.
- **Tool results are text from a server.** A hostile server can try prompt injection through results. The same advice as for messages applies: your permission rules are the barrier. Resources and prompts are text from the server too; a prompt is written by the server, not by you.
- **Resources and prompts get the same checks as tools.** The gateway checks the caller's project and the enabled flag on every list, read and prompt. The host redacts secrets from them and refuses a read or prompt larger than 512 KiB.
