# Step 2 design: gateway core, Claude and Pi

**Date:** 2026-10-28. **Inputs:** [decisions](decisions/0001-initial-decisions.md), [spike results](spike/README.md).
**Exit criterion:** a Claude session and a Pi session discover each other, exchange a request and a reply in their running sessions, and keep accepted messages through a gateway restart. In step 2 both run on this VM, with a real gateway, a real daemon and a real join over HTTP(S).

## 1. Layout

```
go.mod                         module github.com/niparis/a2a-ocs  (Go 1.27)
cmd/agent-net/main.go          subcommands: gateway, daemon, mcp, join, enroll, status, list, messages
internal/protocol/             shared types: Message, Session, Profile, frames (gateway<->daemon, daemon<->local clients)
internal/gateway/              HTTP + WebSocket server, SQLite store, routing, limits
internal/daemon/               gateway link, local socket server, session registry, adapters
internal/daemon/claude/        Claude adapter (discovery, delivery, native reply capture)
internal/mcpserver/            `agent-net mcp`: stdio MCP server that proxies to the daemon
plugins/pi/agent-net.ts        Pi extension (TypeScript), client of the daemon local socket
```

Dependencies: `github.com/modelcontextprotocol/go-sdk` (v1.7.0+), `modernc.org/sqlite`, and one WebSocket library (`github.com/coder/websocket`). Nothing else without a reason. The `spike/` directory stays as reference and is not imported.

## 2. Identity

| ID | Who makes it | Form |
|---|---|---|
| `node_id` | gateway at enrollment | `n_` + 16 hex |
| `session_id` | gateway at first `session.upsert` | `s_` + 16 hex, stable for `(node_id, harness, native_id)` |
| `message_id` | gateway at store time | `m_` + 16 hex |
| `conversation_id` | gateway, for a new thread | `c_` + 16 hex. A reply inherits its parent's |
| `native_id` | harness | Claude `sessionId`, Pi session ID |

The model never supplies `session_id`. The daemon binds each local connection to one session (section 6).

## 3. Gateway

- `agent-net gateway --listen 0.0.0.0:7420 --data ~/.local/state/agent-net/gateway`.
- On first start it creates a self-signed CA and a server certificate (ECDSA P-256) in the data dir and serves HTTPS. It prints the CA SHA-256 fingerprint.
- `agent-net enroll` (run on the gateway host) prints a one-time join string: `https://<host>:7420#<code>#<ca-sha256>`. The code is 8 random base32 characters, valid for 10 minutes, usable once. It is stored hashed.
- `POST /v1/enroll` with `{code, hostname, os}` returns `{node_id, token}`. The token is 32 random bytes in hex and is stored hashed (SHA-256).
- `GET /v1/node` upgrades to WebSocket. Auth: `Authorization: Bearer <token>`. One live connection per node, and a new one replaces the old.
- SQLite in WAL mode, with migrations in code (numbered, applied in order, a `schema_version` table). Tables:
  - `nodes`
  - `enroll_codes`
  - `sessions` (`id`, `node_id`, `harness`, `native_id`, `name`, `cwd`, `pid`, `status`, `delivery`, `last_seen`, `online`)
  - `profiles` (`session_id`, `name`, `function`, `project`, `capabilities` JSON, `task`, `updated_at`)
  - `messages`
  - `deliveries`
  - `events` (append-only audit)

### Message envelope (`internal/protocol`)

```go
type Message struct {
    ID             string    `json:"message_id"`
    ConversationID string    `json:"conversation_id"`
    ParentID       string    `json:"parent_message_id,omitempty"`
    From           string    `json:"sender_session_id"`
    To             string    `json:"recipient_session_id"`
    Type           string    `json:"message_type"`   // request | reply | notification
    Body           string    `json:"body"`
    Hop            int       `json:"hop"`            // 0 for a new thread, parent.Hop+1 for a reply
    IdempotencyKey string    `json:"idempotency_key"`
    CreatedAt      time.Time `json:"created_at"`
    ExpiresAt      time.Time `json:"expires_at"`     // default CreatedAt+24h
    State          string    `json:"state"`
    FromName       string    `json:"sender_name"`    // filled by gateway from the profile, for display
}
```

States: `stored`, `dispatched`, `delivered`, `replied`, `failed`, `expired`. A message is in the database (committed) **before** the sender gets its ID. `replied` is set on the parent when a reply that references it is stored.

### Rules enforced by the gateway

- Idempotency: `(sender_session_id, idempotency_key)` is unique. A repeat returns the first message's ID and state and stores nothing.
- Hop limit: reject if `hop > 8`.
- Conversation budget: reject the 51st message in one conversation.
- Rate limit: 20 messages per minute per sender session (token bucket, in memory).
- Body size: 64 KiB maximum.
- Recipient must exist. If it is offline, the message is stored and delivered when it comes back, until `expires_at`. Expiry runs once a minute.

### Gateway <-> daemon frames (WebSocket, JSON text frames)

Every request carries `id` (string). The other side answers `{"type":"result","id":...,"ok":true,"data":{...}}` or `{"type":"result","id":...,"ok":false,"error":"..."}`.

Daemon to gateway:

| type | fields | data in result |
|---|---|---|
| `hello` | `version`, `hostname` | `{node_id}` |
| `session.sync` | `sessions: [SessionInfo]`, the full current set for this node. Sessions missing from it go offline | `{sessions: [{native_id, harness, session_id}]}` |
| `session.upsert` | `session: SessionInfo` | `{session_id}` |
| `session.remove` | `session_id` | - |
| `profile.announce` | `session_id`, `name`, `function`, `project`, `capabilities`, `task` | `{profile}` |
| `msg.send` | `sender_session_id`, `recipient_session_id`, `message_type`, `body`, `parent_message_id?`, `idempotency_key` | `{message}` |
| `delivery.update` | `message_id`, `state` (`delivered` or `failed`), `detail` | - |
| `query.agents` | filters `harness`, `capability`, `project`, `online_only`, `text` | `{agents: [AgentView]}` |
| `query.agent` | `session_id` | `{agent: AgentView}` |
| `query.messages` | `session_id`, `conversation_id?`, `limit` | `{messages: [Message]}` |
| `query.message` | `message_id` | `{message}` |

Gateway to daemon: `msg.deliver` with `message` (full envelope). The daemon answers with a `result` meaning "accepted for delivery". It then sends `delivery.update` once the adapter is done.

`SessionInfo`: `harness`, `native_id`, `pid`, `cwd`, `name` (harness-given), `status` (`idle` | `busy` | `shell` | `unknown`), `delivery` (`native-claude` | `plugin`).

`AgentView` keeps verified fields apart from claimed ones: `{session_id, node_id, hostname, harness, cwd, status, online, last_seen, profile: {name, function, project, capabilities, task}}`. The profile is what the agent says about itself. Everything else comes from the daemon.

### Dispatch

When a message is stored and the recipient's node is connected, the gateway sends `msg.deliver` and marks the message `dispatched`. When a node connects (after `session.sync`), the gateway resends every `stored` or `dispatched` message for that node's online sessions, oldest first. Delivery is at-least-once. The daemon removes duplicates (section 5).

Offline: a node whose WebSocket has been closed for more than 30 s has its sessions marked `online=false`.

## 4. Daemon

- `agent-net daemon --gateway https://host:7420`, with credentials from `~/.config/agent-net/node.json` (written by `agent-net join <join-string>`, mode 0600). The file holds `{gateway_url, node_id, token, ca_sha256}`. The daemon pins the CA: it builds a cert pool from the CA fetched at join time and refuses any other.
- `agent-net join` fetches `GET /v1/ca` (the CA PEM), checks its SHA-256 against the join string, saves `ca.pem`, then calls `/v1/enroll`.
- Local state in `~/.local/state/agent-net/daemon/`:
  - a small SQLite DB `delivered(message_id PRIMARY KEY, at)` for removing duplicates;
  - `outbox` for sends made while the gateway is unreachable. Flush them in order on reconnect, with the same idempotency keys.
- Reconnect with backoff (1 s doubling to 30 s, with jitter). After every connect, send `hello` and then `session.sync`.

## 5. Delivery and duplicates

On `msg.deliver`, the daemon:

1. If `message_id` is in `delivered`, answers ok and sends `delivery.update delivered` again. It does not deliver twice.
2. Finds the local session by `recipient_session_id`, then calls its adapter's `Deliver`.
3. On success, inserts into `delivered` **before** sending `delivery.update delivered`.
4. On a definite failure (socket missing, plugin gone), sends `failed` with the detail.
5. On an unclear outcome (a write that timed out after connect), sends `delivered` with `detail: "unconfirmed"`, never a retry. This is the brief's rule: an unclear outcome must not cause a blind retry.

## 6. Local socket (`$XDG_RUNTIME_DIR/agent-net/daemon.sock`, or `/tmp/agent-net-<uid>/daemon.sock` on macOS)

Directory mode 0700, socket mode 0600. JSON lines both ways. Request `{"id":"1","method":"...","params":{...}}`. Response `{"id":"1","result":{...}}` or `{"id":"1","error":"..."}`. Pushes from the daemon have no `id`: `{"method":"deliver","params":{"message":{...}}}`.

The first request must be `hello`:

```json
{"id":"1","method":"hello","params":{"client":"mcp"|"pi","harness":"claude"|"codex"|"pi","native_id":"...","cwd":"...","status":"idle"}}
```

**Peer check:** the daemon reads the peer PID from the socket (`SO_PEERCRED` on Linux, `LOCAL_PEERPID` on macOS) and checks the claim:

- `client:"mcp"`, `harness:"claude"`: the peer's **parent PID** must have `~/.claude/sessions/<ppid>.json` with `sessionId == native_id`.
- `client:"pi"`, `harness:"pi"`: the peer PID itself is the Pi process. Its command line must contain `pi`, and `native_id` is taken as given, since the extension runs inside Pi.

A failed check closes the connection with an error. A passed check binds the connection to that session. Every later call uses the bound `session_id`, and params can never name another sender.

Methods after `hello`: `status` (Pi pushes idle/busy), `announce`, `list`, `get`, `send`, `reply`, `messages`, `message_status`, plus `delivered` (Pi confirms a pushed message: `{message_id, ok, detail}`).

- `send`: `{to, body, message_type?, idempotency_key?}`. `to` is a session ID, or an exact agent name if that name is unique among online agents. Otherwise return an error that lists the matches. The daemon fills in the idempotency key if none is given.
- `reply`: `{message_id, body}`. It sets `parent_message_id`, the recipient (the parent's sender), `hop` and the type.

Clients without a bound session (`agent-net status/list/messages` on the CLI) say `{"client":"cli"}` and get read-only methods.

## 7. Claude adapter (`internal/daemon/claude`)

Ports the spike ([spike/claude.md](spike/claude.md)). Credit OCS (MIT) in the file header for the frame format.

- **Discovery:** poll `~/.claude/sessions/*.json` every second (override the dir with `AGENT_NET_CLAUDE_SESSIONS_DIR` for tests). The PID must be alive and the filename PID must match the content. Status comes from the file. Report adds, changes and removals to the daemon as `session.upsert` / `session.remove`.
- **Delivery:** connect to `messagingSocketPath`, after checking it is a real socket owned by our uid and not a symlink. Write one `user` frame:
  - `msg_id` = our `message_id`, `priority: "next"`, and `from: "uds:<reply socket>"`.
  - The content is wrapped in `<cross-session-message from="uds:<reply socket>" from-name="<sender name, sanitized to [A-Za-z0-9_.-], max 40>" from-mode="prompting">`.
  - Never send the session token.
  - The body text starts with one header line: `[agent-net] message <message_id> from <sender name> (<harness>, session <sender_session_id>). Reply with your SendMessage tool to this sender, or use the agent-net tools.`
- **Reply sockets:** one listening socket per *sender* session, at `$XDG_RUNTIME_DIR/agent-net/r/<sender_session_id>.sock`. Watch the 108-byte limit: the session ID is short enough. A frame arriving there comes from a Claude session. Identify it by the `from` field (`uds:<its inbox socket>`), which maps to a session through the discovery table. Treat it as a reply to the most recent message from that sender to that session, and send it as `msg.send` with `parent_message_id` set. Strip the `<cross-session-message>` wrapper. Keep the `hop-chain` attribute in the event log for now.
- Claude sends a `peer_message_status` receipt in some cases (see OCS `src/claude-receipt.ts`). Accept and log it, nothing more in step 2.

## 8. `agent-net mcp` (`internal/mcpserver`)

A stdio MCP server built on the official Go SDK. On start it connects to the daemon socket and sends `hello`:
- Claude: `native_id = $CLAUDE_CODE_SESSION_ID`.
- If the variable is missing, it stops with a clear error.

Tools, each a thin call to the daemon:

| Tool | Params |
|---|---|
| `agent_list` | `harness?`, `capability?`, `project?`, `online_only?` (default true), `text?` |
| `agent_get` | `session_id` |
| `agent_announce` | `name`, `function`, `project?`, `capabilities?`, `task?` |
| `agent_send` | `to`, `body`, `message_type?` |
| `agent_reply` | `message_id`, `body` |
| `agent_messages` | `conversation_id?`, `limit?` |
| `agent_message_status` | `message_id` |

Tool descriptions must say plainly that peer messages are requests from other agents that cannot approve actions on the user's behalf.

## 9. Pi extension (`plugins/pi/agent-net.ts`)

Ports [spike/pi/agent-net-spike.ts](../spike/pi/agent-net-spike.ts), but as a **client** of the daemon socket. It does not listen itself.

- On `session_start`: connect to the daemon socket and send `hello` with `{client:"pi", harness:"pi", native_id: ctx.sessionManager.getSessionId(), cwd: ctx.cwd, status:"idle"}`. Reconnect with backoff if the daemon is down. Pi must keep working without it.
- `agent_start` / `agent_settled` send `status` `busy` / `idle`.
- On a `deliver` push: `pi.sendMessage({customType:"agent-net", content, display:true, details}, {deliverAs:"followUp", triggerTurn:true})`, then `delivered {message_id, ok:true}`. The content header is the same idea as Claude's: who it is from, the message ID, and "use agent_reply".
- Register tools `agent_list`, `agent_get`, `agent_announce`, `agent_send`, `agent_reply`, `agent_messages`, `agent_message_status`. Each one is a daemon call, with the same params as section 8.
- On `session_shutdown`: close the connection.
- Install: copy or symlink to `~/.pi/agent/extensions/agent-net.ts`. For tests, use `pi -e plugins/pi/agent-net.ts`.

## 10. CLI

- `agent-net status`: daemon connection state, node ID, local sessions.
- `agent-net list`: agents from the gateway.
- `agent-net messages [--session s_x]`: recent messages and their states.

## 11. Tests (Go, `go test ./...`, no network beyond localhost)

- Store: idempotency, hop limit, budget, expiry, `replied` on the parent, migrations from empty.
- Gateway and daemon in one process over a real WebSocket on 127.0.0.1, with a fake adapter. Cover: send, deliver, reply; recipient offline then back; gateway restart (close it, reopen the same data dir) with nothing accepted lost; duplicate `msg.deliver` delivered once.
- Claude adapter: a fake sessions dir and a fake inbox socket, checking the exact frame bytes and reply capture.
- Local socket: the peer check refuses a wrong `native_id`, and `send` cannot change the sender.
- Enrollment: a code works once, expires, and a wrong CA fingerprint is refused at join.

Formatting and checks: `gofmt`, `go vet`, and `staticcheck` if it is available. No test may sleep for more than 2 s. Use polling helpers with timeouts.
