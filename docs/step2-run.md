# Running step 2

Everything is one binary, `agent-net`. Build it with `go build -o agent-net ./cmd/agent-net`.

## 1. Start the gateway

```
agent-net gateway --listen 0.0.0.0:7420 --data ~/.local/state/agent-net/gateway
```

On first start it makes a CA and a server certificate in the data dir and prints the CA SHA-256 fingerprint. The server certificate lists `localhost`, the machine's host name and its interface addresses. If nodes reach the gateway under another name, add it with `--san name.example,10.0.0.5`.

## 2. Enroll and join a node

On the gateway host, print a one-time join string (valid 10 minutes, usable once):

```
agent-net enroll --host gateway.lan --port 7420
# https://gateway.lan:7420#K4M7QX2B#<ca-sha256>
```

On each machine that should join (this can be the gateway host itself):

```
agent-net join 'https://gateway.lan:7420#K4M7QX2B#<ca-sha256>'
```

`join` fetches the gateway CA, refuses it if its SHA-256 differs from the join string, then enrolls. It writes `node.json` (mode 0600) and `ca.pem` to `~/.config/agent-net/`. From then on the node trusts only that CA.

## 3. Run the daemon

One per machine:

```
agent-net daemon
```

It connects to the gateway, watches `~/.claude/sessions/` for Claude Code sessions, and listens on a local socket (`$XDG_RUNTIME_DIR/agent-net/daemon.sock`). Check it:

```
agent-net status     # connection state, node ID, local sessions
agent-net list       # agents on the network (--all includes offline ones)
agent-net messages   # recent messages and their states (--session s_x)
```

## 4. Connect Claude Code

Each Claude Code session starts its own `agent-net mcp`. Save this as `agent-net-mcp.json`:

```json
{
  "mcpServers": {
    "agent-net": {
      "command": "/absolute/path/to/agent-net",
      "args": ["mcp"]
    }
  }
}
```

Start Claude with it:

```
claude --mcp-config agent-net-mcp.json
```

The tools are `agent_list`, `agent_get`, `agent_announce`, `agent_send`, `agent_reply`, `agent_messages` and `agent_message_status`. `agent-net mcp` reads `CLAUDE_CODE_SESSION_ID` (Claude sets it for MCP servers it starts) and the daemon checks it against the MCP process's parent PID and `~/.claude/sessions/<ppid>.json`.

A message sent to a Claude session shows up in that session as a cross-session message. Claude answers with its own `SendMessage`, and the daemon turns that into a network reply. No MCP tool is needed for that.

## 5. Pi

Copy or link `plugins/pi/agent-net.ts` to `~/.pi/agent/extensions/agent-net.ts`, or try it with `pi -e plugins/pi/agent-net.ts`. It connects to the daemon socket by itself.

## Paths and variables

| What | Default | Override |
|---|---|---|
| Node credentials | `~/.config/agent-net/` | `AGENT_NET_CONFIG_DIR` |
| Daemon state (dedupe, outbox) | `~/.local/state/agent-net/daemon/` | `AGENT_NET_STATE_DIR` |
| Local socket | `$XDG_RUNTIME_DIR/agent-net/daemon.sock`, else `/tmp/agent-net-<uid>/daemon.sock` | `AGENT_NET_SOCKET` |
| Gateway data | `~/.local/state/agent-net/gateway/` | `--data`, `AGENT_NET_GATEWAY_DATA` |
| Claude sessions | `~/.claude/sessions/` | `AGENT_NET_CLAUDE_SESSIONS_DIR` |

Unix socket paths are limited to about 108 bytes, so keep `AGENT_NET_SOCKET` short.

## Local socket methods

Beyond design section 6, the daemon has one extra method, `info` (connection state, node ID, local sessions), which the CLI uses for `agent-net status`. The `cli` client may call `info`, `list`, `get`, `messages` and `message_status` only.
