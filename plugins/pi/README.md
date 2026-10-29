# agent-net extension for Pi

Connects a Pi session to the local agent-net daemon. Pi can then list other agent sessions, message them, and receive their messages in the running session.

- Connects to the daemon's Unix socket on `session_start` and sends `hello` for this Pi session.
- Reconnects with backoff (1 s, doubling to 30 s) if the daemon is down. Pi works normally without it, and the tools answer "agent-net daemon not reachable".
- Sends `status` `busy` on `agent_start` and `idle` on `agent_settled`.
- Injects a pushed message with `pi.sendMessage` (follow-up, triggers a turn), then confirms with `delivered`. The text says it is from another agent, not the user, and to answer with `agent_reply`.
- Tools: `agent_list`, `agent_nodes`, `agent_get`, `agent_announce`, `agent_send`, `agent_reply`, `agent_messages`, `agent_message_status`, and the MCP registry tools `agent_mcp_add`, `agent_mcp_list`, `agent_mcp_test`, `agent_mcp_remove`.
- Registered MCP servers: their tools are registered at run time as `<server>__<tool>`, on connect, on the daemon's `mcp_changed` push and every 15 s. Pi cannot unregister a tool, so a removed one is registered again as `hidden`.

## Install

```sh
mkdir -p ~/.pi/agent/extensions
ln -s "$PWD/plugins/pi/agent-net.ts" ~/.pi/agent/extensions/agent-net.ts
```

## Try it without installing

```sh
pi -e plugins/pi/agent-net.ts
```

## Socket path

`$XDG_RUNTIME_DIR/agent-net/daemon.sock`, or `/tmp/agent-net-<uid>/daemon.sock` if `XDG_RUNTIME_DIR` is unset. Set `AGENT_NET_SOCKET` to use another path.

## Testing

Type-check against the globally installed Pi types:

```sh
plugins/pi/typecheck.sh
```

The script finds Pi with `npm root -g`, writes a temporary tsconfig that points at it, and runs `tsc --noEmit`.

A fake daemon for manual tests:

```sh
node plugins/pi/test/fake-daemon.mjs /tmp/fake.sock --deliver-after-ms 5000
AGENT_NET_SOCKET=/tmp/fake.sock pi --model <model> -e plugins/pi/agent-net.ts
```

It logs every request and pushes one `deliver` after `hello`.
