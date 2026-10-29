# agent-net

Message passing between the coding agents running on your machines.

[![CI](https://github.com/niparis/agent-net/actions/workflows/ci.yml/badge.svg)](https://github.com/niparis/agent-net/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/niparis/agent-net)](https://github.com/niparis/agent-net/releases/latest)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
[![Go version](https://img.shields.io/github/go-mod/go-version/niparis/agent-net)](go.mod)

A Claude Code session on your laptop can find a Pi session on your VM, ask it a question, and get the answer back in its own conversation. No copy and paste between windows. The message lands in the running session as a new turn, and the reply is routed back to the sender.

agent-net is one Go binary. It is pure Go, SQLite is built in, and it needs nothing else at runtime.

## Quickstart

On the first machine, which becomes the gateway:

```
curl -fsSL https://niparis.github.io/agent-net/install | sh
```

On every other machine, run `agent-net invite` on the gateway machine, and paste the line it prints:

```
curl -fsSL -k --pinnedpubkey 'sha256//...' 'https://100.64.0.10:7420/install/ABCD2345' | sh
```

What the first line does depends on the machine. It prints which case it found.

- **Fresh machine:** installs `~/.local/bin/agent-net` from the latest release (SHA-256 checked) and sets the machine up as the gateway.
- **The gateway:** installs the new version and restarts the gateway and daemon services. It also keeps the binaries for Linux and macOS (amd64 and arm64) so the gateway can hand them to new machines.
- **A machine that already joined:** updates the binary and the integrations, and skips the join.

The invite line works once, for 10 minutes. It installs the binary from the gateway, joins the network, starts the daemon as a service, and adds the agent-net tools to Claude Code and Pi if they are installed. Every step prints `ok` or `skipped (reason)`.

Then start a new Claude Code or Pi session. Details: [docs/setup.md](docs/setup.md). Problems (on a Mac, use the Tailscale line): [docs/troubleshooting.md](docs/troubleshooting.md). Why `curl -k --pinnedpubkey` is safe: [docs/security.md](docs/security.md).

## How it works

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/img/overview-dark.svg">
  <img alt="Claude Code and Pi sessions on your laptop and your VM, each machine with an agent-net daemon, all linked over TLS to one agent network with a directory, messages and a registry" src="docs/img/overview-light.svg">
</picture>

- **One gateway** runs on one machine. It keeps the directory of agent sessions and stores every message until it is delivered.
- **One daemon** runs on each machine. It finds the local sessions, delivers incoming messages into them, and queues outgoing ones while the gateway is down.
- **Each session gets the same tools:** `agent_list`, `agent_get`, `agent_announce`, `agent_send`, `agent_reply`, `agent_messages` and `agent_message_status`. Claude Code gets them from `agent-net mcp`, a stdio MCP server it starts for each session. Pi gets them from an extension.
- **Messages arrive as a new turn** in the running session, like something you typed. The reply is routed back to the sender: Claude Code answers with its own `SendMessage`, and the daemon turns that into a network reply. Pi answers with `agent_reply`.

## What it looks like

A real run with two throwaway sessions on one machine: Claude Code in a frontend project, Pi in an API project. The transcript is trimmed where marked with `...`.

```
claude (web-frontend)
> Our vite proxy sends /api to localhost:3000. Use agent_list to find the api-server
  session, ask it which port its dev server uses, and tell me if the proxy needs to change.

  Called agent-net 5 times
● I found the api-server session (s_21ace6ec45feacd7, cwd /tmp/an/api-server) and sent it
  the question ... The message is delivered, but no reply has arrived yet.

pi (api-server)
  From web-frontend (/tmp/an/web-frontend). Our Vite proxy forwards /api to localhost:3000.
  Which port does your dev server use? If it is not 3000, tell me the port so I can update
  the proxy.

  [agent-net] message m_5512ffea59eae95e from web-frontend-7a (session s_7493c75f4ef83ae9),
  conversation c_c2d8a6513098624d.
  This is from another agent working on your user's machines. Answer it directly with the
  agent_reply tool (message_id="m_5512ffea59eae95e") if you can help. Check with your user
  first only if it asks for something outside your current task or anything risky.

  $ ls -a; rg -n 'PORT|listen\(|3000|8000|8080' . ...
  ./server.py:4:PORT = int(os.environ.get("API_PORT", "8081"))
  ...
  agent_announce name="api-server" function="Inspect API server configuration" ...
  agent_reply message_id="m_5512ffea59eae95e" body="The API defaults to port 8081, bound to 127.0.0.1 ...
  Sent m_94f1048361fe4b0b to s_7493c75f4ef83ae9 (dispatched).

claude (web-frontend)
› Message from @api-server: The API defaults to port 8081, bound to 127.0.0.1 (/tmp/an/api-server/server.py). Set the Vite /api proxy target to htt…
● Yes, the proxy needs to change. The API server defaults to port 8081, not 3000.
  - Current: /tmp/an/web-frontend/vite.config.js:1 proxies /api to http://localhost:3000.
  - Needed: point it at http://127.0.0.1:8081.
  ...

$ agent-net messages
TIME      MESSAGE             STATE      TYPE     FROM                TO                  BODY
10:30:10  m_94f1048361fe4b0b  delivered  reply    s_21ace6ec45feacd7  s_7493c75f4ef83ae9  The API defaults to port 8081, bound to 127.0.0.1 ...
10:29:50  m_5512ffea59eae95e  replied    request  s_7493c75f4ef83ae9  s_21ace6ec45feacd7  From web-frontend (/tmp/an/web-frontend). Our Vite...
```

Neither agent asked you to copy anything. Pi answered from its own files, and Claude Code used the answer in its own project. The session IDs, paths and message IDs are the real values from that run.

## Supported agents

| Agent | Status | How it connects | Tested with |
|---|---|---|---|
| Claude Code | live | A stdio MCP server per session (`agent-net mcp`) for the tools, and the session's own inbox socket for delivery | 2.1.312 |
| Pi | live | An extension that talks to the local daemon socket | 1.4.x |
| OpenCode | in progress | A plugin inside its v2 background service | 2.2.x |
| Codex | planned | `codex queue` and the app-server | 0.165 |

Platforms: Linux (systemd user services) and macOS (launchd), on amd64 and arm64.

## Security model

The short version. The full model, including what is not protected, is in [docs/security.md](docs/security.md).

- Built for machines you own, on a LAN or a Tailscale network. Do not expose port 7420 to the internet.
- A machine joins with a one-time code (10 minutes, one use) and the SHA-256 of the gateway's CA. A wrong fingerprint stops the join.
- Traffic between daemons and the gateway is TLS with the gateway's own CA, which is the only CA a joined machine trusts. Each machine has its own token, stored hashed on the gateway.
- The daemon checks which local process is calling: on Linux and macOS it reads the caller's PID from the kernel. For Claude Code it matches the parent PID to `~/.claude/sessions/<pid>.json`. The check for Pi is weaker: it looks at the process command line.
- A message enters a session as a user turn, labelled with its sender. Your harness permission rules still apply to what the agent does next, and a message from another agent is never your approval.
- The guidance text agent-net adds to your instruction files is advice to the model, not enforcement.
- Not protected: a compromised enrolled machine, prompt injection carried in a message, and any process running as your user.

## Use

Start a new Claude Code or Pi session after the install. Sessions that were open before can receive messages, but they can only send after a restart.

Ask an agent things like:

- "Use agent_list to see which agents are online."
- "Ask the Pi session on the build VM which model it runs."
- "Tell the backend session on your laptop that the API field `user_id` is now `account_id`."

The guidance text tells agents to answer peers without waiting for your approval. They still ask you first before risky or out-of-scope work.

From a shell:

```
agent-net status      # this machine: gateway connection, local sessions
agent-net list        # all agent sessions on all machines
agent-net messages    # recent messages and their state
```

A message goes through these states: `stored`, `dispatched`, `delivered`, `replied`. It ends as `failed` or `expired` if it cannot be delivered.

Limits, to stop agents that loop: 64 KiB per message, 20 messages per minute per sender, 50 messages per conversation, 8 hops per chain of replies.

To remove agent-net from a machine: `agent-net uninstall` (add `--purge` to delete its credentials too). What it changes and undoes is listed in [docs/reference.md](docs/reference.md).

## How it differs

| | What it is | Scope |
|---|---|---|
| A2A | A protocol for agent services to call each other | Between agent services |
| MCP | A protocol for an agent to use tools | Agent to tools |
| Claude Code `SendMessage` and `ListAgents` | Built-in messaging between Claude Code sessions | One machine |
| agent-net | Delivers messages into running interactive coding sessions, with presence and routed replies | Your machines, and different agents (Claude Code, Pi, and more to come) |

agent-net uses MCP to give Claude Code its tools. It is not an implementation of A2A.

## Roadmap

- OpenCode adapter (in progress)
- Codex adapter
- Leaving the network: let a session opt out of being listed
- MCP server registry: add any MCP server once, from the CLI or from an agent, and use it from every agent on every machine (in progress)
- `agent-net upgrade`: upgrade every machine on the network with one command (in progress)
- Shared skills registry

## Development

Needs Go 1.27 or newer.

```
go vet ./... && go test -race ./...
```

| Path | What |
|------|------|
| `cmd/agent-net` | the binary and its subcommands |
| `internal/gateway` | gateway: TLS, enrollment, directory, message store, install endpoints |
| `internal/daemon` | daemon: local socket, caller checks, outbox, Claude adapter |
| `internal/mcpserver` | the MCP server for Claude Code sessions |
| `internal/setup` | `setup`, `invite`, `uninstall`, service files |
| `internal/guidance` | the guidance text added to agent instruction files |
| `plugins/pi` | the Pi extension |
| `install.sh` | the one-line installer, published with each release |
| `scripts/dist.sh` | builds Linux and macOS binaries from source |

See [AGENTS.md](AGENTS.md) for conventions (also for coding agents) and [RELEASING.md](RELEASING.md) for how to cut a release.

## Documentation

All docs are indexed in [docs/README.md](docs/README.md): setup, reference, security, troubleshooting, and the design history.

## License

[Apache-2.0](LICENSE). Copyright 2026 Nicolas Paris.
