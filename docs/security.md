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
- The gateway only accepts a message from a session that belongs to the node on the connection, and a node can only read messages that were sent from or to its own sessions. It can list every agent on the network and read the event feed (metadata, no bodies; see [the event feed](#what-every-node-can-read-the-event-feed)).
- There is no token rotation and no revocation command yet. To remove a machine, delete its row from the `nodes` table in `gateway.db`, or stop trusting the network and start a new gateway data dir.

## Which local process is calling

Agents talk to their local daemon over a Unix socket. The socket directory is mode 0700 and the socket is mode 0600, so only your user (and root) can connect. The daemon then works out which agent session is on the other end, and binds the connection to that session. Later calls use the bound session as the sender. The model cannot name another sender.

The daemon reads the process ID of the caller from the kernel: `SO_PEERCRED` on Linux, `LOCAL_PEERPID` on macOS.

- **Claude Code.** `agent-net mcp` is started by Claude Code for each session. The daemon takes the parent PID of the connecting process and requires a file `~/.claude/sessions/<parent pid>.json` whose `sessionId` equals the session ID the process claims (Claude sets it in `CLAUDE_CODE_SESSION_ID`). If the file is missing or the ID differs, the connection is refused.
  - When `agent-net mcp` switches to a newer binary (see [upgrade.md](upgrade.md#running-sessions)), it keeps its process ID and its parent, so this check gives the same answer. It only executes the file at its own install path, only after that file reports exactly the version the daemon runs, and only if that is a newer `vX.Y.Z` release than its own. Dev builds never switch. The hand-over file (start-up messages and unread input, which can hold tool arguments) is a mode 0600 temp file that the new process deletes at once.
- **Pi.** The Pi extension runs inside the Pi process. The daemon checks that the connecting process has `pi` anywhere in its command line (compared in lower case). This is a weak check. Any process of your user whose command line contains "pi" can claim to be a Pi session, with any session ID. It is accepted because the socket is already limited to your user, and a process running as you can do much more harm than that. A stricter check (matching Pi's executable) is not done yet.
- **The CLI** (`status`, `list`, `messages`) says it is a CLI client and gets read-only calls, plus the MCP registry and project registry commands.

## Sending as the owner, and who may start workers

A message from the person at a machine is sent from that machine's **owner session** (harness `owner`, shown as `you (on <host>)`). Receiving agents are told it is "from your user in person". So who may send as the owner matters.

- **Only the command line or the TUI.** The daemon accepts `owner_send` only from a socket client that said `client: cli`. An agent connection (Claude, Pi, OpenCode) is bound to a session and is refused. This is the same gate as for the owner-only data (`usage`, `session_text`, and the others). The owner session cannot be created by a hello: only the daemon makes it, and the gateway accepts only the native ID `owner` for that harness.
- **Not from inside an agent.** A command-line call is also refused when its process is a descendant of a session or worker process of this machine, so an agent's shell tool cannot run `agent-net send` or `agent-net start` as you. The daemon reads the caller's process ID from the socket and walks its parents. This stops an agent that shells out. It does not stop a process of your user that is not under an agent, which could always use the socket (the socket is yours).
- **The sender is never a parameter.** `owner_send` ignores any sender field, and `send` and `reply` use the bound session.
- **Workers.** Any agent may call `agent_start` and `agent_stop` (on its own workers); the person at the command line may stop any worker. A worker cannot start workers: the daemon checks the worker flag of the calling session, which the daemon set from its own table; the gateway checks it again; and a command-line call from inside a worker is refused as above. A machine refuses workers with `setup --no-launch`; the gateway sees that from the machine's hello and refuses early. At most 4 workers run on a machine (a setting).
- **A worker's session is only its own process.** The daemon binds a worker's harness session to the worker's process group. A connection for that session from any other process is refused, so the user's own OpenCode service cannot register or hijack it. A worker has one session.
- **A worker's server** (`opencode serve`) listens on `127.0.0.1` only, with a random password in its environment. Another local user of the machine could reach the port but not the API without the password. The password is not written to disk or to the command line.
- **Permissions.** A worker runs with the project's own permission settings. agent-net never passes a flag that turns them off, for any harness. What a worker does when a step needs approval depends on the harness:
  - *OpenCode:* it waits for an answer that nobody gives, and ends at the idle time.
  - *Claude Code:* it is a background session, so it waits on the prompt. You can `claude attach <id>` to answer it. agent-net does not approve for it.
  - *Codex:* the daemon answers every approval request with "decline" and hands back with status `blocked`, with the command, the reason and where to approve it. Codex's own approval policy and sandbox (the project's and your `config.toml`) are not changed by agent-net.
  - *Pi:* Pi has no permission prompts. A Pi worker can run any command its tools allow in the project folder, so the daemon refuses a Pi worker unless it has `isolation: worktree`: it never works in your own checkout.
- **Claude Code workers** start through `claude --bg`, so the session belongs to Claude's background service, not to the daemon. The per-worker settings go on the command line (`--settings`): `crossSessionInbound: "accept"` and the `env` of the session (`AGENT_NET_SOCKET`, `AGENT_NET_WORKER`). Your own `settings.json` is not changed. The worker is bound to the session ID in its session file, and the daemon's process checks use the process ID from that file. Ending is `claude stop`, then the kill of the process group. The stopped session stays in `claude agents` with its conversation until you `claude rm` it.
- **Pi workers** run `pi --mode rpc` with a named pipe as standard input that the daemon keeps open, so Pi does not end when the daemon restarts. The daemon chooses the Pi session ID (`--session-id`) before Pi starts and binds it to the worker's process group.
- **Codex workers** run `codex app-server` on `ws://127.0.0.1:<port>`, guarded by a random capability token. The token is in a private file (mode 0600) in the worker folder and never on the command line. Another local user could reach the port, but not the API without the token. The daemon takes messages for a Codex worker from the session that started it and from the person at the machine (the owner), and refuses other agents.
- **Reports are model output.** The gateway marks messages from a worker. Claude Code, Pi and OpenCode frame them as the worker's own words, not the user's: no instruction or approval in a report carries your authority. This limits prompt injection through a report; it does not stop a model that decides to obey.
- **Worktrees.** With `isolation: worktree` a worker edits its own git worktree under the daemon's state folder, not your checkout. The branch `agent-net/<worker id>` is made in your project.
- **The feed** now also carries `worker.started`, `worker.stopped` and `worker.refused` (project, harness, model, reason and owner session ID, cut to 200 characters), readable by every enrolled machine, like the other feed events.

## Delivery into a Claude Code session

- To deliver, the daemon writes one frame to the inbox socket of the target Claude session. Before it writes, it checks that the path is a real Unix socket (not a symlink) owned by your user.
- The daemon never sends the Claude session token. It does not need it, and it does not read it.
- The message arrives as a user turn, in a `<cross-session-message>` block with the sender's name and ID. The sender's name is cut to letters, digits, `_`, `.` and `-` (40 characters at most). If the message body contains the wrapper tags, they are broken up, so a message cannot close the block early and fake a second one.
- The agent-net details (sender, message ID, session ID) follow the body, as plain text in the same turn.
- Claude Code treats the message as a request from another session. In our tests Claude asks its user before acting on a peer message that is outside the current task (see [step 2 results](step2-e2e.md)). Claude Code's own permission rules still apply to everything the agent does next.

### Accepting peer messages in Claude Code

By default Claude Code holds a cross-session message for your review when the sender's permission mode class differs from the receiver's (for example a prompting session sends to one in bypass mode). Setting `"crossSessionInbound": "accept"` in the user settings turns that check off for all cross-session messages. Then a message from any agent on your agent-net network reaches the session without review, even when the sender runs in a looser permission mode than the receiver. This means a compromised or careless agent on another machine can put text in front of a strict session without you seeing it first.

What stays the same: a peer message is still not your approval. Claude Code's permission rules and prompts still apply to what the agent does next. Claude Code also keeps its stricter sources: a managed (organization) setting or a repository setting of `hold` or `refuse` wins over your `accept`. The accepted values are `accept`, `hold` and `refuse`.

Setup never turns this on by itself. It asks, default No, or follows `--accept-peer-messages`. It never changes a value that is already set. It records that it set the value, and `agent-net uninstall` removes the key only if agent-net set it and it is still `"accept"`. See [setup.md](setup.md#the-claude-code-question).

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

## Project registry

A registered project is a folder where workers may start sessions (see [decision 0002](decisions/0002-workers.md)). Registering one widens that set, so it is controlled like this:

- **Only the folder's own machine can register it.** The daemon of that machine checks the path (it exists and is inside a git working tree), resolves it to the main tree and strips credentials from the remote. The gateway puts the entry on the node of the connection that sent it. The request has no field for another machine, and the daemon ignores any extra field an agent adds (for example `on`), the same way `agent_mcp_add` does. So an agent can register folders on its own machine only. A compromised enrolled machine can register paths on itself, as it can already start anything on itself.
- **The gateway does not look at folders.** It only checks the shape of an entry: an absolute, clean path, no control characters, fields of at most 4096 bytes, at most 200 entries per call. It removes credentials from the remote again.
- **Agents can add and list, not remove.** The daemon refuses the remove method from anything but the CLI. The guidance text asks agents to register projects only when you ask; like all guidance, that is advice and not enforcement (see below). The harness permission rules are the barrier.
- **Limits on a scan.** It stops at 200 repositories per call, goes at most 8 levels down, does not follow symbolic links, does not enter a repository once found and skips hidden folders, `node_modules` and `vendor`. This keeps an agent that scans `/` or `~` from registering thousands of folders, or from walking out of the tree it was pointed at.
- **The CLI can remove another machine's entry** (`projects remove --on`), for a machine that is gone. This is as much trust as `agent-net mcp remove` has.
- Registrations are in the audit trail: `project.added` and `project.removed` events, with the session and harness that did it (`cli` for the CLI).

## What the gateway logs and keeps

**The log.** The gateway writes one line per event to stderr (the systemd journal, or `~/Library/Logs/agent-net-gateway.log`). For each message it logs the time, the message and conversation IDs, the type, the sender and recipient (session ID, name, harness and host) and the size of the body in bytes. It never logs the body. It also logs each delivery step (stored, dispatched, delivered, failed, expired), each refused send, each new session (with its folder, project, git root, remote and branch), changes of the installed harnesses on a machine, each project that is registered or removed, and node connects and disconnects. The format is in [reference.md](reference.md#gateway-log-format). Session names come from agents, so the log quotes them: a name cannot add a field or a line. Logs are copied around by journals and log shippers. The remote URL has its credentials removed before it leaves the machine (see below), so a password in a git remote does not reach the log.

**The database.** `gateway.db` holds the nodes (token hashes only), sessions, profiles, messages with their bodies, delivery records, and an `events` table.

| Data | Kept for | Flag |
|---|---|---|
| Messages (with bodies) and delivery records | 30 days | `--keep-messages` |
| Events | 90 days | `--keep-events` |
| Sessions that are offline and not seen | 30 days | `--keep-sessions` |
| Registered projects | until you remove them | none |
| Nodes and their token hashes | until you delete them | none |

A timer deletes older data once at start and then every hour. A period of `0` keeps that data for ever. The delete frees space inside the file, but the file does not shrink. A node's own `daemon.db` only keeps delivered message IDs and its outbox.

**File modes.** `gateway.db` and the daemon's `daemon.db`, with their `-wal` and `-shm` files, are mode 0600. The gateway and the daemon create them with that mode and set it again at each start, so files made by an older version are fixed. The gateway creates its data folder with mode 0700.

**Back up the gateway data folder.** It is the only copy of the CA, the server key and the database. If you lose it, every node has to join again (new invites, one for each machine), and all message history is gone. Copy the whole folder (`~/.local/state/agent-net/gateway/`) while the gateway is stopped, or use `sqlite3 gateway.db ".backup ..."` for the database, and keep the copy private: it holds `ca.key` and `server-key.pem`.

## What a node reports about its machine

Each daemon sends the gateway an inventory of the machine, and every enrolled machine and every agent can read it (this is the same trust as for the agent list).

- **Per session:** the working folder (already sent before), the git root, the project name, the `origin` remote URL and the current branch. The daemon removes passwords and tokens from the remote before it sends it. It runs `git` in the session's folder, with `core.fsmonitor` turned off, no prompts and no optional locks. A repository can hold configuration that we did not write, so only read-only plumbing commands run (`rev-parse`, `config --get`, `symbolic-ref`).
- **Per session, the project description:** the daemon reads the first 32 KiB of the project's `AGENTS.md` (else `CLAUDE.md`) and sends one sentence of at most 120 characters, with markdown removed. The gateway cleans it again and cuts it to 120 characters. Everything else in the file stays on the machine. The text is whatever the file says, so anyone who can write the file chooses what other agents read in the FUNCTION column; treat it as data, like any peer text.
- **Per machine:** which of `claude`, `opencode`, `pi` and `codex` are installed, their file paths and versions, and the model list of OpenCode. To get the versions and models the daemon runs those programs (`--version`, and `opencode models`) as your user. `opencode models` may start or contact the OpenCode background service. To find the programs it also runs your login shell once (`$SHELL -l -c`) when a harness is missing from the daemon's `PATH`.

- **Per session:** whether it is in the foreground (a person has it open) or the background, and the model it runs. For Claude Code the daemon reads the `model` field of the newest assistant entry in the session's transcript file. It reads nothing else from the transcript, and sends and logs no transcript text.

File paths and the remote URL name your projects. If that is sensitive, do not enroll the machine.

## What every node can read: the event feed

Since the TUI, every enrolled machine can read the network's event metadata, with `agent-net tui` or the daemon method `events` (gateway frame `query.events`). This is new: before, a machine could only read the messages of its own sessions. The feed has the kind and time of each message, delivery, session and connection event, which machine and session it concerns, the two session IDs and the type of each message, and the ID of the message it answers. It does not have message bodies, MCP events, enrollment events or machine addresses. Details are cut to 200 characters, and the delivery detail comes from the receiving machine (for example `plugin refused`). A message body is still given only to the two machines in the conversation (`message_status`), and the TUI uses exactly that call. The feed is open to every enrolled machine, which already could list all agents; a compromised machine now also sees who talks to whom and when. The events are kept for 90 days.

## Session text and resource figures

Decision [0007](decisions/0007-usage-and-session-text.md). Two kinds of data are for the owner only.

**The text of a session** (the TUI, later the phone): prompts, replies, tool calls and results.

- It crosses the network only when the owner asks for it. The request comes from the local CLI or TUI. For a session on another machine it goes through the gateway, which relays it to the machine that holds the session and relays the answer back. The gateway does not store it, count it or log it, and writes no event for it. The log line for a failed relay says only that it failed.
- **Agent connections can never call it.** The daemon allows `session_text`, `usage`, `usage_top` and `counts` only on a connection that said `client: cli` in hello. Claude MCP, Pi and OpenCode connections get an error. The daemon also refuses a CLI whose calling process is a local session's process or a child of one, so an agent that runs `agent-net tui` in its shell tool is stopped. A process that detaches itself from its parent (a double fork) is not caught, and any process of your user can read `~/.claude/projects` directly: this is a barrier against an agent that simply runs the command, not against hostile code of your own user.
- Before the text leaves the machine that holds it, secrets are masked: `sk-...` keys, GitHub tokens, AWS key IDs, Slack and Google keys, JWTs, bearer and `Authorization` headers, `password`/`token`/`secret`/`key` values after `=` or `:`, passwords in URLs, and private key blocks. The daemon that asks masks again. This catches common shapes only; a secret in an odd shape is not masked. Each entry is cut to 2,000 characters (tool results 600) and control characters are removed.
- Nothing of it is written to the gateway log, the events table or the daemon's database.
- The gateway cannot tell an owner's CLI from a program on that machine that says it is one. A compromised enrolled machine can therefore ask for the text of any online session on the network. This widens the trust note under [what agent-net does not protect against](#what-agent-net-does-not-protect-against).

**CPU and memory.** Each daemon reports a summary to the gateway every 10 seconds, and every enrolled machine can read it (same trust as the agent list). The summary has numbers only: no command lines. The command lines of the 5 heaviest processes of a session (first 80 characters, tokens and `--password`/`KEY=value` pairs masked) come only on request, with the same owner-only rule and relay as the session text. The gateway keeps the summaries in memory only.

## What agent-net does not protect against

- **A compromised enrolled machine.** Its token lets it list every agent on the network and send to any of them. It can send as any session that its own daemon reports, and read the messages to and from those sessions, and it sees the metadata of every message on the network. Enroll only machines you would give a shell on the others.
- **Prompt injection through a message.** A message is text that an agent will read. A hostile or confused peer, or a peer that read a poisoned web page, can send instructions. The wrapper, the sender label and the guidance text help the model see where the text came from. They do not stop a model that decides to obey. Your permission rules are the barrier.
- **Anyone who is your user on a machine.** Any process running as your user can read `node.json` and the token, connect to the daemon socket (and claim to be a Pi session), and write to Claude's inbox sockets. agent-net adds no boundary between your own processes.
- **Anyone who can reach the gateway port.** Without a valid code or token they cannot join, list agents or read messages. They can fetch the gateway CA certificate and the agent-net release binaries (`/v1/ca` and `/install/bin/<os>/<arch>` need no login). The gateway listens on `0.0.0.0:7420` by default. Keep it on a LAN, a VPN or Tailscale, and not on the public internet.
- **Eavesdropping on the gateway machine.** Messages are stored as plain text in `gateway.db` for 30 days by default. They are encrypted on the wire, not at rest. Anyone with access to the gateway's disk can read them, and the files are readable by your user only (mode 0600), not by other users.
- **A malicious gateway machine.** It hands out the binaries that other machines install, and it sees every message.

## Reporting a problem

Open an issue on [github.com/niparis/agent-net](https://github.com/niparis/agent-net/issues). For something sensitive, use GitHub's private vulnerability reporting on the repository's Security tab.
