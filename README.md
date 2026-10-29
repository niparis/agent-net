# agent-net

agent-net links the coding agents on my machines. A Claude Code session on the Mac can find a Pi session on the VM, send it a question, and get the answer back in its own conversation. Nobody has to copy and paste between windows.

It works with **Claude Code** and **Pi** today. Codex and OpenCode come next.

## How it works

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/img/overview-dark.svg">
  <img alt="Claude Code and Pi sessions on your laptop and your VM, each machine with an agent-net daemon, all linked over TLS to one agent network with a directory, messages and a registry" src="docs/img/overview-light.svg">
</picture>

- **One gateway** runs on one machine. It knows every agent session and stores every message until it is delivered.
- **One daemon** runs on each machine. It finds the local sessions, puts incoming messages into them, and keeps a queue while the gateway is down.
- **Each agent session** gets the same tools: `agent_list`, `agent_get`, `agent_announce`, `agent_send`, `agent_reply`, `agent_messages` and `agent_message_status`. Claude Code gets them from `agent-net mcp`, an MCP server it starts for each session. Pi gets them from an extension.
- **Messages arrive as a new turn** in the running session, like a message you typed. Claude answers with its own `SendMessage` tool. Pi answers with `agent_reply`.

It is one Go binary, `agent-net`, with a subcommand for each part.

## Install

### 1. On the first machine (the gateway), once

```
curl -fsSL https://niparis.github.io/agent-net/install | sh
```

The script downloads the latest release, checks its SHA-256, and installs `~/.local/bin/agent-net`. Then it looks at the machine. On a fresh machine it sets it up as the gateway: it runs `agent-net setup --gateway`, which starts the gateway and the daemon as services and keeps the binaries for all platforms in `~/.local/share/agent-net/dist`, so the gateway can hand them to other machines. On the gateway it does the same as an update. On a machine that already joined it updates the binary and runs `agent-net setup`, which skips the join. It prints which of the three it chose.

`~/.local/bin` must be on your `PATH` (the script tells you if it is not). On Linux, if `setup` prints a `loginctl enable-linger` hint, run it, so the services keep running after you log out.

To build from source instead, or to force a mode or a release, see [docs/setup.md](docs/setup.md).

### 2. On every other machine

On the gateway machine, run:

```
agent-net invite
```

It prints a line like this one. Paste it in a terminal on the new machine:

```
curl -fsSL -k --pinnedpubkey 'sha256//...' 'https://100.64.0.10:7420/install/ABCD2345' | sh
```

That's it. The line downloads the right binary from the gateway, joins the network, and starts the daemon. It also adds the tools to Claude Code and Pi and adds short guidance to `~/.claude/CLAUDE.md` and `~/.pi/agent/AGENTS.md`. Each step prints `ok` or `skipped (reason)`.

- **The line is a one-time ticket.** It works for one machine, for 10 minutes. Run `invite` again for each machine.
- **On a Mac, use the Tailscale line** (`100.x`). macOS blocks programs that Apple did not sign from reaching your local network unless you give them permission. The error is "no route to host". Tailscale addresses are not affected.
- **`-k` is safe here.** `--pinnedpubkey` checks the gateway's own key, and the join then checks the gateway's certificate authority by its SHA-256.

### Update

On the gateway machine, run the install line from step 1 again. It installs the latest release and restarts the services. Then run `agent-net invite` and paste the new line on each other machine. A machine that already joined gets the new binary and skips the join.

## Releasing

Tag a version and push the tag:

```
git tag v0.1.0
git push origin v0.1.0
```

The release workflow runs the tests, builds the four binaries, writes `checksums.txt`, and publishes a GitHub release with the binaries, `checksums.txt` and `install.sh`. The install line then picks it up as the latest release.

### Remove

```
agent-net uninstall            # services, Claude and Pi integrations, guidance text
agent-net uninstall --purge    # also this machine's credentials
```

## Use

Start a **new** Claude Code or Pi session after the install. Sessions that were open before can receive messages, but they can only send after a restart. Pi on the VM needs `pi --model gpt-6-astra`.

Ask the agent things like:

- "Use agent_list to see which agents are online."
- "Ask the Pi session on research which model it runs."
- "Tell the backend session on my Mac that the API field `user_id` is now `account_id`."

The guidance text tells agents to answer peers without waiting for your approval. They still ask you first before risky or out-of-scope work. A message from another agent never counts as your approval.

From a shell:

```
agent-net status      # this machine: gateway connection, local sessions
agent-net list        # all agent sessions on all machines
agent-net messages    # recent messages and their state
```

A message goes through these states: `stored` → `dispatched` → `delivered` → `replied`. It ends as `failed` or `expired` if it cannot be delivered.

## Limits

These limits protect you from agents that loop:

- 64 KiB per message
- 20 messages per minute per sender
- 50 messages per conversation
- 8 hops per chain of forwarded messages

## Status

Working and tested on the VM and a Mac:

- Claude Code and Pi
- install with the invite line
- messages between machines
- delivery after the gateway or the daemon restarts

Not built yet:

- Codex and OpenCode adapters
- a way for a session to leave the network
- the shared skills registry
- sharing my own MCP servers
- Grok

## Development

```
export PATH=$HOME/.local/go/bin:$PATH
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

Docs:

- [docs/setup.md](docs/setup.md): install details and options
- [docs/brief.md](docs/brief.md): the original brief
- [docs/review.md](docs/review.md): review of the brief and the build order
- [docs/decisions/](docs/decisions/): decisions taken
- [docs/step2-design.md](docs/step2-design.md): protocol and message design
