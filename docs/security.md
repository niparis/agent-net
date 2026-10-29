# Security model

This page says what agent-net trusts, how it checks it, and what it does not protect against. Every statement here was checked against the code. Where a check is weak, the page says so.

In short: agent-net is built for machines you own, on a network you control (a LAN or a Tailscale network). Treat every enrolled machine as fully trusted, and every message as untrusted input.

## Enrolling a machine

A machine joins with a one-time code.

- `agent-net invite` (or `agent-net enroll`) makes an 8-character code from random base32 characters. The gateway stores only its SHA-256 hash. The code works once and for 10 minutes.
- The invite line is `curl -fsSL -k --pinnedpubkey 'sha256//...' 'https://HOST:7420/install/CODE' | sh`. The `-k` flag turns off certificate checking, so the pin does the checking instead: curl compares the SHA-256 of the gateway's public key with the pin and aborts if they differ. This is what makes `-k` safe here. A machine in the middle cannot present a different key.
- The pin covers the server key, and that key is saved in `server-key.pem` in the gateway data dir. The certificate is re-issued on every start, but the key is not, so an old invite line still matches after a gateway restart.
- The script served at `/install/CODE` carries the gateway address, the code, the CA fingerprint and the pin. The gateway refuses to render it for an unknown, used or expired code, and for a `Host` header that is not a plain host name or IP address with a port.
- The script downloads the binary from the gateway (`/install/bin/<os>/<arch>`) over the same pinned connection, then runs `agent-net setup --join`. Note that this binary is not signed. You trust the gateway machine to hand out the right one. The `curl | sh` install on the first machine, in contrast, checks the SHA-256 of the release binary against `checksums.txt` from the same GitHub release.
- `agent-net join` fetches the gateway CA without verifying TLS, and does not trust it until its SHA-256 equals the fingerprint in the join string. A mismatch stops the join and nothing is saved. After that, the machine trusts only that CA.

## Transport

- The gateway makes its own CA on first start (ECDSA P-256, valid 10 years) and a server certificate signed by it (valid 1 year, re-issued at each start). The CA key `ca.key` and the server key are written with mode 0600.
- The server certificate lists `localhost`, `127.0.0.1`, `::1`, the host name, every interface address and any `--san` names. If a node reaches the gateway under another name, the TLS check fails until you add it.
- The daemon and `agent-net join` use a TLS client that trusts only the pinned CA (minimum TLS 1.2), not the system roots. All traffic between a daemon and the gateway, including the WebSocket, goes over that connection.

## Node tokens

- At enrollment the gateway gives the machine a node ID and a token (32 random bytes, hex). It stores only the SHA-256 hash of the token.
- The token is saved in `~/.config/agent-net/node.json` with mode 0600. The daemon sends it as a bearer token when it opens its WebSocket. One node has one live connection, and a new one replaces the old.
- The gateway only accepts a message from a session that belongs to the node on the connection, and a node can only read messages that were sent from or to its own sessions. It can list every agent on the network.
- There is no token rotation and no revocation command yet. To remove a machine, delete its row from the `nodes` table in `gateway.db`, or stop trusting the network and start a new gateway data dir.

## Which local process is calling

Agents talk to their local daemon over a Unix socket. The socket directory is mode 0700 and the socket is mode 0600, so only your user (and root) can connect. The daemon then works out which agent session is on the other end, and binds the connection to that session. Later calls use the bound session as the sender. The model cannot name another sender.

The daemon reads the process ID of the caller from the kernel: `SO_PEERCRED` on Linux, `LOCAL_PEERPID` on macOS.

- **Claude Code.** `agent-net mcp` is started by Claude Code for each session. The daemon takes the parent PID of the connecting process and requires a file `~/.claude/sessions/<parent pid>.json` whose `sessionId` equals the session ID the process claims (Claude sets it in `CLAUDE_CODE_SESSION_ID`). If the file is missing or the ID differs, the connection is refused.
  - When `agent-net mcp` switches to a newer binary (see [upgrade.md](upgrade.md#running-sessions)), it keeps its process ID and its parent, so this check gives the same answer. It only executes the file at its own install path, only after that file reports exactly the version the daemon runs, and only if that is a newer `vX.Y.Z` release than its own. Dev builds never switch. The hand-over file (start-up messages and unread input, which can hold tool arguments) is a mode 0600 temp file that the new process deletes at once.
- **Pi.** The Pi extension runs inside the Pi process. The daemon checks that the connecting process has `pi` anywhere in its command line (compared in lower case). This is a weak check. Any process of your user whose command line contains "pi" can claim to be a Pi session, with any session ID. It is accepted because the socket is already limited to your user, and a process running as you can do much more harm than that. A stricter check (matching Pi's executable) is not done yet.
- **The CLI** (`status`, `list`, `messages`) says it is a CLI client and gets read-only calls.

## Delivery into a Claude Code session

- To deliver, the daemon writes one frame to the inbox socket of the target Claude session. Before it writes, it checks that the path is a real Unix socket (not a symlink) owned by your user.
- The daemon never sends the Claude session token. It does not need it, and it does not read it.
- The message arrives as a user turn, in a `<cross-session-message>` block with the sender's name and ID. The sender's name is cut to letters, digits, `_`, `.` and `-` (40 characters at most). If the message body contains the wrapper tags, they are broken up, so a message cannot close the block early and fake a second one.
- The agent-net details (sender, message ID, session ID) follow the body, as plain text in the same turn.
- Claude Code treats the message as a request from another session. In our tests Claude asks its user before acting on a peer message that is outside the current task (see [step 2 results](step2-e2e.md)). Claude Code's own permission rules still apply to everything the agent does next.

## Delivery into a Pi session

The extension turns the message into a follow-up message in the running session, marked as coming from another agent and not from you. Pi's own tool permissions apply to what the agent does next.

## Guidance text is advice, not enforcement

`agent-net setup` adds a short block to `~/.claude/CLAUDE.md` and `~/.pi/agent/AGENTS.md`. It tells the model to answer peers directly, to check with you before risky or out-of-scope work, and to never take a peer message as your approval. This is a request to the model. It can be ignored, or overridden by a persuasive message. The things that actually limit what an agent can do are the harness permission rules (Claude Code's permission modes and allow lists, Pi's tool settings). Keep those on. A peer message is never your approval, and you should not configure your agents to treat it as one.

## Limits against loops

The gateway enforces these on every send:

| Limit | Value |
|---|---|
| Message body | 64 KiB |
| Rate | 20 messages per minute per sending session |
| Messages in one conversation | 50 |
| Reply depth ("hops") | 8 |
| Message lifetime | 24 hours, then `expired` |

A repeated send with the same idempotency key stores nothing new.

## MCP server registry

Registered MCP servers run on the machine that hosts them, as your user, and their tools can be called by any agent on any enrolled machine. The registry holds env var names and file paths, never secret values; header and env values are read on the hosting machine and are redacted from errors, logs and tool results there. A daemon runs only the servers registered with its own node. See [mcp-registry.md](mcp-registry.md#security).

## What the gateway logs and keeps

**The log.** The gateway writes one line per event to stderr (the systemd journal, or `~/Library/Logs/agent-net-gateway.log`). For each message it logs the time, the message and conversation IDs, the type, the sender and recipient (session ID, name, harness and host) and the size of the body in bytes. It never logs the body. It also logs each delivery step (stored, dispatched, delivered, failed, expired), each refused send, each new session (with its folder, project, git root, remote and branch), changes of the installed harnesses on a machine, and node connects and disconnects. The format is in [reference.md](reference.md#gateway-log-format). Session names come from agents, so the log quotes them: a name cannot add a field or a line. Logs are copied around by journals and log shippers. The remote URL has its credentials removed before it leaves the machine (see below), so a password in a git remote does not reach the log.

**The database.** `gateway.db` holds the nodes (token hashes only), sessions, profiles, messages with their bodies, delivery records, and an `events` table.

| Data | Kept for | Flag |
|---|---|---|
| Messages (with bodies) and delivery records | 30 days | `--keep-messages` |
| Events | 90 days | `--keep-events` |
| Sessions that are offline and not seen | 30 days | `--keep-sessions` |
| Nodes and their token hashes | until you delete them | none |

A timer deletes older data once at start and then every hour. A period of `0` keeps that data for ever. The delete frees space inside the file, but the file does not shrink. A node's own `daemon.db` only keeps delivered message IDs and its outbox.

**File modes.** `gateway.db` and the daemon's `daemon.db`, with their `-wal` and `-shm` files, are mode 0600. The gateway and the daemon create them with that mode and set it again at each start, so files made by an older version are fixed. The gateway creates its data folder with mode 0700.

**Back up the gateway data folder.** It is the only copy of the CA, the server key and the database. If you lose it, every node has to join again (new invites, one for each machine), and all message history is gone. Copy the whole folder (`~/.local/state/agent-net/gateway/`) while the gateway is stopped, or use `sqlite3 gateway.db ".backup ..."` for the database, and keep the copy private: it holds `ca.key` and `server-key.pem`.

## What a node reports about its machine

Each daemon sends the gateway an inventory of the machine, and every enrolled machine and every agent can read it (this is the same trust as for the agent list).

- **Per session:** the working folder (already sent before), the git root, the project name, the `origin` remote URL and the current branch. The daemon removes passwords and tokens from the remote before it sends it. It runs `git` in the session's folder, with `core.fsmonitor` turned off, no prompts and no optional locks. A repository can hold configuration that we did not write, so only read-only plumbing commands run (`rev-parse`, `config --get`, `symbolic-ref`).
- **Per machine:** which of `claude`, `opencode`, `pi` and `codex` are installed, their file paths and versions, and the model list of OpenCode. To get the versions and models the daemon runs those programs (`--version`, and `opencode models`) as your user. `opencode models` may start or contact the OpenCode background service. To find the programs it also runs your login shell once (`$SHELL -l -c`) when a harness is missing from the daemon's `PATH`.

File paths and the remote URL name your projects. If that is sensitive, do not enroll the machine.

## What agent-net does not protect against

- **A compromised enrolled machine.** Its token lets it list every agent on the network and send to any of them. It can send as any session that its own daemon reports, and read the messages to and from those sessions. Enroll only machines you would give a shell on the others.
- **Prompt injection through a message.** A message is text that an agent will read. A hostile or confused peer, or a peer that read a poisoned web page, can send instructions. The wrapper, the sender label and the guidance text help the model see where the text came from. They do not stop a model that decides to obey. Your permission rules are the barrier.
- **Anyone who is your user on a machine.** Any process running as your user can read `node.json` and the token, connect to the daemon socket (and claim to be a Pi session), and write to Claude's inbox sockets. agent-net adds no boundary between your own processes.
- **Anyone who can reach the gateway port.** Without a valid code or token they cannot join, list agents or read messages. They can fetch the gateway CA certificate and the agent-net release binaries (`/v1/ca` and `/install/bin/<os>/<arch>` need no login). The gateway listens on `0.0.0.0:7420` by default. Keep it on a LAN, a VPN or Tailscale, and not on the public internet.
- **Eavesdropping on the gateway machine.** Messages are stored as plain text in `gateway.db` for 30 days by default. They are encrypted on the wire, not at rest. Anyone with access to the gateway's disk can read them, and the files are readable by your user only (mode 0600), not by other users.
- **A malicious gateway machine.** It hands out the binaries that other machines install, and it sees every message.

## Reporting a problem

Open an issue on [github.com/niparis/agent-net](https://github.com/niparis/agent-net/issues). For something sensitive, use GitHub's private vulnerability reporting on the repository's Security tab.
