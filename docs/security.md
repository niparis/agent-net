# Security

The network-wide CTO harness, model preference and auto-compaction rules are
owner settings. The daemon's `cto_settings` method uses the owner gate and
refuses agent sessions. Values are bounded: a threshold from 10 to 100 percent,
a floor up to 10 million tokens, and cache windows up to one day. Like the
integration settings, the gateway endpoint requires an enrolled node token;
that token is an owner credential and must not be shared with agents. The
`cto_send`, `cto_enter`, `cto_new`, `cto_events`, and `cto_graph_history` methods use the
same gate. The daemon keeps CTO action lines in its private state database;
these can include message and task text.

Model score overrides, difficulty minimums and the worker provider allowlist are owner settings. The daemon's
`scores_get`, `scores_set`, `difficulty_get`, `difficulty_set`, `worker_providers_get` and `worker_providers_set` methods use the
owner gate and refuse agent sessions. Their gateway endpoints require an
enrolled node token, which is an owner credential. Scores and minimums are
limited to 0 through 100. The private CTO `score_set` tool uses the same
settings when the owner instructs it.

Worker profiles are stored at the gateway. The owner CLI, the private CTO tools and bound non-worker agent sessions may list, add or remove them. The daemon and gateway both reject worker sessions, and the gateway verifies that the requesting session belongs to the sending node. It resolves each added model against the advertised model list of a machine and records the requester and time. The gateway does not verify that an agent received an owner's instruction, so agents must follow their normal instruction and approval rules. Registered profiles remain subject to the owner-controlled worker provider filter.

The OpenCode CTO server binds only to localhost and requires a random password.
Its data, state, configuration and cache are private to the daemon. The OpenCode
agent-net plugin is disabled in that process. The private configuration copies
the owner's OpenCode provider and other general settings and non-agent-net plugins.
It excludes the owner's agents, MCP servers, permissions and instructions to keep
the CTO's tools private. The Claude Code CTO uses the
owner's Claude login and a background session in a private working directory.
Its session receives `--strict-mcp-config` and a private MCP configuration that
contains only `agent-net cto-mcp`, not the ordinary agent-net MCP server. The
daemon excludes this working directory from Claude session discovery, so the
CTO is not registered with the gateway and cannot receive peer messages.

The private MCP server exposes the owner operations listed in
[the CTO parity table](reference.md), including network and conversation reads,
worker and registry actions, graph changes and settings. These operations reuse
the daemon's owner implementations; the larger tool list does not grant a new
authority path. The daemon accepts the channel only from the active CTO
runtime's process tree and checks the peer on every call. For Claude, it
identifies the background session from Claude's session file, including its
job, name, private working directory and PID file name. It checks the root
process start token as well as its ancestry. The owner gate also checks the CTO
process tree, so a command it runs cannot use the owner CLI. Switching harnesses
stops the old runtime and revokes its channel. The CTO brief requires the owner's
explicit words in the chat before it stops a worker, declines a held request,
removes an MCP server or changes integrations. This is an instruction to the
model, not a separate technical confirmation gate.

CTO feed, worker and project pages default to 20 entries and accept at most 50.
The cursor reads the next page. These output limits do not change the owner gate.

The CTO can send interface requests to local TUI connections that subscribed
and passed the owner gate. The requests select a view, selection and filter or
show a notice; they do not run an operator action in the TUI.

The gateway trusts enrolled daemons to attribute CTO graph writes and sends.
Those writes and sends are marked `by=cto`. A CTO write or send checks that
the gateway supports attribution on the same connection; it is refused on an
older gateway. A CTO worker start also checks the target daemon, so a queued
task keeps its attribution. CTO sends are not queued across a gateway outage.
The gateway labels the sender as `cto`, so an older receiver treats it as a
peer rather than as the owner speaking in person. The gateway accepts a CTO sender
flag only on an owner session. Delivery adapters write the CTO authority note
after the message body and neutralize agent-net markers in the body. A peer
cannot create the note by putting text in its message. Text the CTO reads is
untrusted data; its system brief says not to follow instructions in it.

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
- **Codex.** Codex starts one `agent-net mcp --codex` process per thread, as a child of its shared app server, and does not put the thread in its environment. The process takes the thread from `_meta.threadId` of each tool call (Codex sets it; the model cannot) and says hello once per thread. The daemon accepts a hello only when (1) the process's parent is the app server that the daemon is connected to, whose process ID the daemon reads from the control socket with `SO_PEERCRED` or `LOCAL_PEERPID` (no process name or command line is trusted), and (2) the thread is one the daemon found by listing the server's open threads. What this does not stop: a Codex model that starts `agent-net mcp --codex` through its shell tool, so that the app server is its direct parent, and writes the protocol by hand with another open thread's ID. That needs a command outside the sandbox's process wrapper, and an agent that can run commands as you can also read your files and talk to your socket. It was not closed because a check that the named thread has a tool call running needs the daemon to subscribe to threads, which would also make it a target for approval requests. See [decision 0012](decisions/0012-codex-peers.md).
- **The CLI** (`status`, `list`, `messages`) says it is a CLI client and gets read-only calls, plus the MCP registry and project registry commands.

## Sending as the owner, and who may start workers

A message from the person at a machine is sent from that machine's **owner session** (harness `owner`, shown as `you (on <host>)`). Receiving agents are told it is "from your user in person". So who may send as the owner matters.

- **Only the command line or the TUI.** The daemon accepts `owner_send` only from a socket client that said `client: cli`. An agent connection (Claude, Pi, OpenCode) is bound to a session and is refused. This is the same gate as for the owner-only data (`usage`, `session_text`, and the others). The owner session cannot be created by a hello: only the daemon makes it, and the gateway accepts only the native ID `owner` for that harness.
- **Not from inside an agent.** A command-line call is also refused when its process is a descendant of a session or worker process of this machine, so an agent's shell tool cannot run `agent-net send` or `agent-net start` as you. The daemon reads the caller's process ID from the socket and walks its parents. This stops an agent that shells out. It does not stop a process of your user that is not under an agent, which could always use the socket (the socket is yours).
- **What the refusal tells an agent.** The refusal text is fixed. It names the agent-net tool to use and says that a missing tool means old tools, so the agent asks its user to reconnect. It does not give the agent any way around the check. The daemon also records a session's `tools_version` and `tools_old` from the `hello` of its `agent-net mcp` process. They come from the process itself and are only shown (TUI, `agent_get`); nothing is allowed or refused because of them. A process can claim any version. The only effect of a false one is a wrong warning or no warning.
- **The sender is never a parameter.** `owner_send` ignores any sender field, and `send` and `reply` use the bound session.
- **Workers.** Any agent may call `agent_start` and `agent_stop` (on its own workers); the person at the command line may stop any worker. A worker cannot start workers: the daemon checks the worker flag of the calling session, which the daemon set from its own table; the gateway checks it again; and a command-line call from inside a worker is refused as above. A machine refuses workers with `setup --no-launch`; the gateway sees that from the machine's hello and refuses early. A machine admits a worker by its recent CPU and memory load and keeps a queue of starts that wait (decision 0018). The hard cap is 12 workers on a machine (a setting), the queue is as long as agents make it, and a start ends after 2 hours in the queue. Only the owner of a queued start, or the person at the command line, can cancel it. When a queued start begins, the machine that runs it asks the gateway to pass the task to the owner's machine (`worker.queued_start`, then `worker.task`). The gateway checks that the worker is a session of the sending machine and names that owner. A machine could still make the owner's machine send a task to its own workers, which it could receive from anyone anyway; it cannot make it send to any other session. `worker.cleanup` lists the sessions the machine runs, so the gateway never deletes those records; a machine can only keep its own records that way.
- **A worker's session is only its own process.** The daemon binds a worker's harness session to the worker's process group. A connection for that session from any other process is refused, so the user's own OpenCode service cannot register or hijack it. A worker has one session.
- **A worker's server** (`opencode serve`) listens on `127.0.0.1` only, with a random password in its environment. Another local user of the machine could reach the port but not the API without the password. The password is stored in a mode 0600 file in the private worker state folder so the daemon can reconnect after a restart. It is not written to the database or the command line.
- **Permissions.** A worker runs with the project's own permission settings. agent-net never passes a flag that turns them off, for any harness. What a worker does when a step needs approval depends on the harness:
  - *OpenCode:* it waits for an answer that nobody gives, and ends at the idle time.
  - *Claude Code:* it is a background session, so it waits on the prompt. You can `claude attach <id>` to answer it. agent-net does not approve for it.
  - *Codex:* the daemon answers every approval request with "decline" and hands back with status `blocked`, with the command, the reason and where to approve it. Codex's own approval policy and sandbox (the project's and your `config.toml`) are not changed by agent-net.
  - *Pi:* Pi has no permission prompts. A Pi worker can run any command its tools allow in the project folder, so the daemon refuses a Pi worker unless it has `isolation: worktree`: it never works in your own checkout.
- **Claude Code workers** start through `claude --bg`, so the session belongs to Claude's background service, not to the daemon. The per-worker settings go on the command line (`--settings`): `crossSessionInbound: "accept"` and the `env` of the session (`AGENT_NET_SOCKET`, `AGENT_NET_WORKER`). Your own `settings.json` is not changed. The worker is bound to the session ID in its session file, and the daemon's process checks use the process ID from that file. Ending is `claude stop`, then the kill of the process group. The stopped session stays in `claude agents` with its conversation until you `claude rm` it.
- **Pi workers** run `pi --mode rpc` with a named pipe as standard input that the daemon keeps open, so Pi does not end when the daemon restarts. The daemon chooses the Pi session ID (`--session-id`) before Pi starts and binds it to the worker's process group.
- **Codex workers** stay on their own private app server (not the shared one; see [decision 0012](decisions/0012-codex-peers.md#c4---codex-workers-stay-on-their-private-server)) and run `codex app-server` on `ws://127.0.0.1:<port>`, guarded by a random capability token. The token is in a private file (mode 0600) in the worker folder and never on the command line. Another local user could reach the port, but not the API without the token. The daemon takes messages for a Codex worker from the session that started it and from the person at the machine (the owner), and refuses other agents.
- **Reports are model output.** The gateway marks messages from a worker. Claude Code, Pi and OpenCode frame them as the worker's own words, not the user's: no instruction or approval in a report carries your authority. This limits prompt injection through a report; it does not stop a model that decides to obey.
- **Worktrees.** With `isolation: worktree` a worker edits its own git worktree under the daemon's state folder, not your checkout. The branch `agent-net/<worker id>` is made in your project.
- **The feed** now also carries `worker.started`, `worker.stopped` and `worker.refused` (project, harness, model, reason and owner session ID, cut to 200 characters), readable by every enrolled machine, like the other feed events.

## Delivery into a Claude Code session

- To deliver, the daemon writes one frame to the inbox socket of the target Claude session. Before it writes, it checks that the path is a real Unix socket (not a symlink) owned by your user.
- The daemon never sends the Claude session token. It does not need it, and it does not read it.
- The message arrives as a user turn, in a `<cross-session-message>` block with the sender's name and ID. The sender's name is cut to letters, digits, `_`, `.` and `-` (40 characters at most). If the message body contains the wrapper tags, they are broken up, so a message cannot close the block early and fake a second one. The marker `[agent-net` is broken up in the body too, so a body cannot imitate agent-net's own details.
- The agent-net details (sender, message ID, session ID) follow the body, as plain text in the same turn.
- Claude Code treats the message as a request from another session. In our tests Claude asks its user before acting on a peer message that is outside the current task (see [step 2 results](step2-e2e.md)). Claude Code's own permission rules still apply to everything the agent does next.

### Accepting peer messages in Claude Code

By default Claude Code holds a cross-session message for your review when the sender's permission mode class differs from the receiver's (for example a prompting session sends to one in bypass mode). Setting `"crossSessionInbound": "accept"` in the user settings turns that check off for all cross-session messages. Then a message from any agent on your agent-net network reaches the session without review, even when the sender runs in a looser permission mode than the receiver. This means a compromised or careless agent on another machine can put text in front of a strict session without you seeing it first.

What stays the same: a peer message is still not your approval. Claude Code's permission rules and prompts still apply to what the agent does next. Claude Code also keeps its stricter sources: a managed (organization) setting or a repository setting of `hold` or `refuse` wins over your `accept`. The accepted values are `accept`, `hold` and `refuse`.

Setup never turns this on by itself. It follows the answer to the one integrations question, or `--accept-peer-messages` (a local answer for that machine). It never changes a value that is already set. It records that it set the value, and `agent-net uninstall` removes the key only if agent-net set it and it is still `"accept"`. See [Integrations](setup.md#integrations).

## Delivery into a Pi session

The extension turns the message into a follow-up message in the running session, marked as coming from another agent and not from you. Pi's own tool permissions apply to what the agent does next.

## Delivery into a Codex thread, and what the daemon can do to Codex

The daemon is a client of the shared Codex app server's control socket (mode 0600 in a 0700 folder, so only your user and root reach it; there is no token). It follows the symlink at `$CODEX_HOME/app-server-control/app-server-control.sock` to its target and requires a socket owned by your user. It only reads thread lists and thread metadata and adds messages to a thread's queue. It never starts, resumes or interrupts an interactive thread, so it is never a subscriber: it receives no approval requests and keeps no thread loaded. A queued message appears in the window as a message from the user; the text says in plain words that it is from another agent and carries no approval. Codex's own approval policy and sandbox still decide what the agent does next. Messages from a worker add the "model output, not your user's words" line.

Setup can set `default_tools_approval_mode = "approve"` for the `agent-net` MCP server only. That lets a Codex thread call the agent-net tools (list, send, reply, start workers) with no prompt, including under approval policy `never`, where Codex would otherwise refuse them. Setup follows the answer to the one integrations question, or `--accept-codex-tools` (a local answer for that machine).

## Integrations answered once for all machines

Your answers to the integrations question ([decision 0021](decisions/0021-network-integrations.md)) are stored at the gateway, and every daemon applies them: it adds or removes the Claude Code hooks and plugin, the Claude peer-message setting and the Codex tool approval on its own machine, at start and every minute.

- **Only you can change them** from a command line or the TUI: `integrations_set` is behind the owner gate, like `session_trust` and `session_policy`.
- **A machine you lose control of can change them for all machines.** The gateway takes the answers from any enrolled machine's token, the same trust as for trust and policy settings. A compromised machine could turn on, for example, accepted peer messages in Claude Code on your other machines. Remove a lost machine at the gateway at once (see [node tokens](#node-tokens)).
- **A machine can say no for itself.** A local `--no-...` answer at `setup` wins over the network answer on that machine, and the daemon never undoes it.
- **Quota-axi reads local logins.** The quota item only lets the daemon run quota-axi on each machine; see [subscription quotas](#subscription-quotas).

## Delegation policy

A delegation policy (key `p` in the TUI, [decision 0014](decisions/0014-delegation-policy.md)) is text that agent-net adds to a session's context on every turn: hand code changes to a subagent or a worker, or do them yourself. It is your standing instruction to the session, so who can set it matters.

- **Only you can set it.** The method `session_policy` is behind the owner gate, like `session_trust`: it works from your command line or the TUI, and is refused for an agent client and for a command line that runs inside an agent session's process tree (a program that detaches itself from its parent is not caught). No MCP, Pi or OpenCode tool sets it. A session can read its own policy (the local method `policy`), nothing else. The gateway stores it and logs a `session.policy` event with the session ID, scope, mode and model names. A machine that is enrolled can send the frame, so the gateway trusts the daemons of your machines, as it does for every other frame.
- **A peer cannot imitate it.** The text starts with the marker `[agent-net`, which is broken in every message body ([peer trust](#peer-trust), the marker rule). The model names in it are checked at the gateway and again in the daemon: letters, digits and `. _ : / @ + -` and spaces only, at most 100 characters. No bracket, newline, quote or backtick can get in, so a model name cannot carry an instruction.
- **What it costs.** A policy can make a session start workers (the limits in [who may start workers](#sending-as-the-owner-and-who-may-start-workers) apply: the daemon's limits, the worktree default) or edit less itself. It cannot give a session more permission than its harness already gives it. The session can still ignore it, as with all text.
- **Claude Code files.** With the policy hook (your yes in `setup`), the daemon writes `~/.claude/agents/agent-net-coder-<model>.md` for a `native` policy. Only files with that name prefix and agent-net's marker are changed or removed; a file of yours with the same name is left alone. `uninstall` removes ours. A subagent in that folder can run with the tools of the session: the file does not set `tools`, so it inherits them (Claude Code's default).
- **What the gateway keeps.** A session's own policy lives in its session record and goes when the gateway deletes the old offline session (the retention flags). A project default is a small row in `project_policies` that the retention rules do not delete; clear it in the TUI (`p`, mode none, scope project).
- **The hook adds text, not authority.** The `UserPromptSubmit` hook only prints the context for the prompt. It never blocks a prompt, never changes it, and prints nothing when it has no policy or the daemon does not answer within 700 ms.

## Merge authority

A node's maturity sets its merge authority (decision [0022](decisions/0022-component-graph-and-cto.md)): `full` lets the agent merge and push to the default branch; `delegated` needs an approval from the owner or a delegate (an architect with mode `owner` or `arbiter` over the node); `ask` needs the owner's written approval. The owner can override it per node with `node_settings_set`, behind the owner gate.

This is **guidance plus approval objects, not a git lock**. `MergeApprovalFor` returns the authority, whether approval is required, whether it must be written, the delegates and the text the session gets. Nothing in agent-net refuses a push or blocks a merge in git, and an agent can ignore the text. The approval note that counts is the one agent-net writes for one exact action. The owner's inbox that records written approvals is step 3b of decision 0022; `RequestWrittenApproval` is a stub in this build and says the inbox is not there, so a session under `ask` must ask its owner directly and treat only an agent-net approval note as approval.

A critical repository should also have branch protection on its default branch: require a pull request, require review, and stop force-pushes. Branch protection is enforced by the repository host; agent-net's merge authority is not.

## What a worker can do: the sandbox level

A worker is an agent that nobody watches, so agent-net does not let it ask for approval: the daemon declines every approval, and the worker runs in a sandbox instead ([decision 0020](decisions/0020-worker-sandbox.md)). You set the level per project in the TUI (`p`, line Workers). Only you can: the method is `session_policy`, behind the owner gate, and an agent cannot widen the sandbox of the workers it starts. When the project policy sets no level, the node's maturity default applies ([decision 0022](decisions/0022-component-graph-and-cto.md)): `early`, `growing` and `mature` give `sandboxed`, `critical` gives `no-network`, and a node with no maturity keeps `sandboxed`. The owner can override it with `node_settings_set worker_sandbox`. Today only Codex workers are sandboxed. Claude Code, OpenCode and Pi workers record the level and behave as before (a worker uses the project's own permission settings).

| Level | Writes | Reads | Network |
|---|---|---|---|
| `sandboxed` (default) | Only its worktree, the temp folders, and `objects`, `refs`, `logs` and `worktrees/<name>` of the main repository's git dir | Anything your user can read | on |
| `no-network` | The same | The same | off |
| `read-only` | Nothing | The same | on |

- **Reads are not limited.** A sandboxed worker can read any file your user can read: `~/.ssh`, `~/.aws`, browser profiles, other projects. Treat what a worker can read as what it can leak.
- **With the network on, it can send what it reads.** `sandboxed` and `read-only` allow network access, so a worker that reads a secret (or a prompt-injected worker) can send it out. `no-network` blocks this. Use it for projects with secrets next to the code, or for tasks that need no network. The cost: no package installs, no `git fetch`, no web calls.
- **Writes are limited, but a commit is a write to your repository.** The worker can create objects, move refs (branches and tags) and write reflogs in the main repository, and change its own worktree's index and HEAD. It cannot change `config` or `hooks` of the main repository (Codex keeps `.git` read-only, and agent-net grants only the folders above), so a worker cannot plant a hook that runs outside the sandbox the next time you use git. It can still make commits on branches other than its own, and delete refs. The branch `agent-net/<worker-id>` is its own; read the diff before you merge.
- **`--isolation none` cannot commit.** A worker in your own checkout has no write access to `.git`. Writes to the working tree itself are allowed.
- **No approvals means no way to ask.** A step the sandbox refuses fails inside the worker. It may report that it is blocked, or work around it. The report is model output: check it.
- **The agent-net tools are off for a Codex worker.** Its private app server runs with `mcp_servers.agent-net.enabled=false` when your Codex config defines the server, so it cannot list, message or start anything on the network. A server defined only in a project's `.codex/config.toml` is not turned off. Other MCP servers stay available to it, with their own authority.
- **The sandbox is Codex's.** Its strength is the strength of Codex's Linux (bubblewrap/seccomp) and macOS (Seatbelt) sandbox on that machine. agent-net chooses the policy and does not test the isolation itself.

## Peer trust

Requests use rules on graph edges. `normal` uses the normal peer guidance, `auto` adds the trust note, `ask` holds the request for the owner, and `never` refuses it. The sender-specific edge takes precedence over the wildcard edge into the recipient node. The `t` key cycles the wildcard through `normal`, `ask` and `auto`.

The risk of `auto` is that text relayed by a peer can steer a session without the owner seeing it first. The trust note is guidance to the model, not a replacement for harness permission rules. It still asks for owner approval before deleting data, force-pushing, rewriting history, spending money, sharing secrets, changing credentials or permissions, or deploying beyond the brief.

Only the owner can change edge rules or answer a held request through the local operator interface. `edge_set`, `session_trust`, `held_list` and `held_answer` are behind the owner gate. The gateway trusts enrolled daemons, so a compromised enrolled machine can send these gateway frames. Remove a lost machine at the gateway.

The gateway supplies the trust and approval flags; senders cannot set them in a message. Adapters write the notes after the body. The marker rule breaks `[agent-net` in a body so that it cannot imitate agent-net framing. Plain text can still claim approval, so only the agent-net note proves it. An approval applies to the held request. A decline or expiry generates a reply to the sender. Rules and held messages are stored at the gateway; a session removal does not remove a graph edge.

## Hints about who can help

After a failed tool call, agent-net can add one line telling the model which other online session may own the failing service (decision [0013](decisions/0013-peer-trust-and-hints.md#part-b---hints-about-who-can-help)). The matching runs in your daemon, on tokens, with no model call. What leaves the machine: nothing new. The daemon asks the gateway for the list of online sessions, which it already knows, and compares the error text with it locally. The error text is not sent to the gateway.

- The hint is advice. The model may ignore it, and it only suggests that the model asks with `agent_send`. A suggestion names a session by its name and ID and says why it matched (a project, repository, folder or host name found in the error, or words of the session's own announced `expertise`, function and task). Session names and expertise are written by the sessions themselves: a session could choose them to attract hints. The worst result is a message to that session, which is still a normal message under all the limits.
- The Claude Code hook is added to `~/.claude/settings.json` only with your consent, and removed by `agent-net uninstall`. The command reads the hook input on stdin, talks to the local daemon only, gives up after 280 ms and prints nothing on any error. The hook runs inside the Claude Code session's process tree, so it uses the `hint` method with the session ID Claude gives it. That method can only use up the rate limit of that session; it is not an owner-only method.
- Limits: at most two suggestions; the same suggestion for the same session at most once per 30 minutes; any hint for a session at most once per 45 seconds; never the session itself, an offline session, the owner, a worker or a session of the same project.
- The `expertise` line is stored at the gateway with the profile, cut to 200 characters, with control characters removed.

## Needs you

The TUI's "Needs you" section shows what a session waits for: a permission, a question or input (decision [0016](decisions/0016-needs-you.md)). The state is `kind`, `text` and `since` on the session. The daemon sends it to the gateway with the session, and every enrolled machine's TUI can read it: the same audience as the agent list, not only the session's own machine.

- **What leaves the machine.** The text comes from the harness: a Claude Code permission request (the tool name and its command, path or URL) or notification, an OpenCode permission or form title, or the agent's own `waiting` line. It is cut to 200 characters, turned into one line and stripped of control characters at the daemon and again at the gateway. A permission request can name a file, a command or a URL, so treat the line as metadata that every enrolled machine can see. Do not put secrets in a question you ask an agent to announce.
- **Who may set it.** A plugin is bound to its session by the local socket. A command line hook names `harness` and `native_id`, and the daemon silently ignores an unknown session. Like `hint`, the `waiting` method is not owner-only: it must work inside a harness turn. Any process of your user can call it for a session the daemon knows, which is no more than that process can already do.
- **What it changes.** Nothing but a row and a STATUS cell. It is not a message, carries no authority, and is cleared when the session becomes busy or you answer. A session can lie about waiting, just as it can lie in its announced task; the worst result is a row you look at and dismiss.
- **The hooks.** The Claude Code hooks are added only with your consent (the one integrations question, or `--claude-needs-hook`), removed by `agent-net uninstall`, talk to the local daemon only, give up after 300 ms and print nothing on any error. They run inside the session's process tree and use the session ID Claude gives them.

## Guidance text is advice, not enforcement

`agent-net setup` adds a short block to `~/.claude/CLAUDE.md` and `~/.pi/agent/AGENTS.md`. It tells the model to answer peers directly, to check with you before risky or out-of-scope work, and to never take a peer message as your approval, unless the note that agent-net itself adds says you allowed this session to act on peer requests (see [peer trust](#peer-trust)). It also tells the model to relay a request you asked it to send, and let the receiver decide, and to ask a peer early when an error comes from something another session owns. This is a request to the model. It can be ignored, or overridden by a persuasive message. The things that actually limit what an agent can do are the harness permission rules (Claude Code's permission modes and allow lists, Pi's tool settings). Keep those on. A peer message is never your approval, except through peer trust, which only you switch on, per session. Do not configure your agents to treat peer messages as approval in any other way.

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

## Machine names

The owner can give a machine a display name ([decision 0015](decisions/0015-machine-display-name.md), key `N` in the TUI, `agent-net nodes rename`). It is the name every machine, every agent and every hint sees for that machine, so who sets it and what it may hold matter.

- **Only you can set it.** The method `node_rename` is behind the owner gate, like `session_policy`: your command line or the TUI, refused for an agent client and for a command line inside an agent session's process tree. No agent tool sets it. A machine that is enrolled can send the `node.rename` frame, so the gateway trusts the daemons of your machines here, as for every other frame.
- **The limit.** 1 to 63 characters, letters, digits, `-`, `_` and `.`, starting with a letter or digit, checked at the gateway (and in the TUI and the command line before they send). No space, quote, bracket, `@`, `:`, slash or control character can get in, so the name is safe in a line of text for the model, in a log line and as an ssh target. A name must be unique among the machines, ignoring case, so a name cannot make two machines look like one.
- **What it does not change.** The name is a label. Machine identity is the node ID and token. The reported host name stays stored, and a machine's own daemon cannot set a display name by saying a different host name in `hello`.

## What a node reports about its machine

Each daemon sends the gateway an inventory of the machine, and every enrolled machine and every agent can read it (this is the same trust as for the agent list).

- **Per session:** the working folder (already sent before), the git root, the project name, the `origin` remote URL and the current branch. The daemon removes passwords and tokens from the remote before it sends it. It runs `git` in the session's folder, with `core.fsmonitor` turned off, no prompts and no optional locks. A repository can hold configuration that we did not write, so only read-only plumbing commands run (`rev-parse`, `config --get`, `symbolic-ref`).
- **Per session, the project description:** the daemon reads the first 32 KiB of the project's `AGENTS.md` (else `CLAUDE.md`) and sends one sentence of at most 120 characters, with markdown removed. The gateway cleans it again and cuts it to 120 characters. Everything else in the file stays on the machine. The text is whatever the file says, so anyone who can write the file chooses what other agents read in the FUNCTION column; treat it as data, like any peer text.
- **Per machine:** which of `claude`, `opencode`, `pi` and `codex` are installed, their file paths and versions, and their model lists. When the quota integration is on, also the subscription window figures described under [subscription quotas](#subscription-quotas). To get the versions and models the daemon runs those programs as your user (`--version`, `opencode models`, `pi --list-models`, and `codex app-server` with `model/list`). Claude Code has no list command, so its list is a small table in the daemon. `opencode models` may start or contact the OpenCode background service; the Codex app server reads the user's Codex configuration but starts no turn. To find the programs it also runs your login shell once (`$SHELL -l -c`) when a harness is missing from the daemon's `PATH`.

- **Per session:** whether it is in the foreground (a person has it open) or the background, and the model it runs. For Claude Code the daemon reads the `model` field of the newest assistant entry in the session's transcript file. It reads nothing else from the transcript, and sends and logs no transcript text.

File paths and the remote URL name your projects. If that is sensitive, do not enroll the machine.

## Subscription quotas

agent-net reads quotas through [quota-axi](https://github.com/kunchenguid/quota-axi) instead of reading provider logins itself. That keeps one program responsible for the credential formats, and agent-net gets only the figures it needs.

- **How it is installed.** When the Quota integration is on and you have no quota-axi, setup or the daemon runs `npm install --prefix ~/.local/share/agent-net/quota-axi --no-fund --no-audit quota-axi@0.1.69` in the background, with a 3 minute limit. The version is pinned in the binary; updating it is part of an agent-net release, and npm checks the package integrity from the registry. agent-net does not install Node. Without Node 22.19 or newer and npm, it installs nothing and says so once.
- **Which copy runs.** Your copy always wins: the path in `node.json` `quota_axi`, `PATH`, `~/.local/bin`, or on macOS the nvm folders, `/opt/homebrew/bin`, `/usr/local/bin` and the global npm prefix. The daemon runs that copy with its own Node folder first on `PATH`. Otherwise it runs the private copy.
- **What runs.** The daemon runs `quota-axi --json --max-age 5m --no-credential-refresh` every 5 minutes, with a 90 second limit. The integration is on by default; the owner can turn it off in the integrations question or with `--no-quota-axi`, which also removes the private copy. agent-net never runs quota-axi's `update` command.
- **What quota-axi reads.** quota-axi reads the local logins of your model providers (files, environment variables, and on macOS or Windows a native secure store when you approved it), and asks the providers' own usage endpoints. Those are the same sources the provider CLIs use. The risk is quota-axi's, not a new one: it already runs as your user on the machine that holds the logins.
- **No refresh, no identity.** `--no-credential-refresh` tells quota-axi never to run a vendor CLI's refresh command, so agent-net cannot make a login rotate (the risk in decision [0019](decisions/0019-opencode-worker-logins.md)). agent-net never passes `--full`, the audit tier that carries account identity and source attempts. It passes no `--provider`, so quota-axi reads what is set up on the machine.
- **What leaves the machine.** Only provider id, plan name (when quota-axi reports one), and per window: id, label, kind, percent used, reset time, pace and exhausted. No token, credential file path, account id, email, or account key is kept or sent. The reading travels to the gateway with the usage report and is kept in memory there, readable by every enrolled machine like the agent list.
- **What it changes.** The figures choose a model for a loose name, rank a provider ahead of pace lower, and refuse a start whose provider is exhausted. A reading quota-axi marks stale is shown but never decides anything.
- **Failures.** A failed or slow run changes nothing: the last good reading stays, and the daemon logs the error.

## What every node can read: the event feed

Since the TUI, every enrolled machine can read the network's event metadata, with `agent-net tui` or the daemon method `events` (gateway frame `query.events`). This is new: before, a machine could only read the messages of its own sessions. The feed has the kind and time of each message, delivery, session and connection event, which machine and session it concerns, the two session IDs and the type of each message, and the ID of the message it answers. It does not have message bodies, MCP events, enrollment events or machine addresses. Details are cut to 200 characters, and the delivery detail comes from the receiving machine (for example `plugin refused`). A message body is still given only to the two machines in the conversation (`message_status`), and the TUI uses exactly that call. The feed is open to every enrolled machine, which already could list all agents; a compromised machine now also sees who talks to whom and when. The events are kept for 90 days.

## Session text and resource figures

Decision [0007](decisions/0007-usage-and-session-text.md). Two kinds of data are for the owner only.

**The text of a session** (the TUI, later the phone): prompts, replies, tool calls and results.

- It crosses the network only when the owner asks for it. The request comes from the local CLI or TUI. For a session on another machine it goes through the gateway, which relays it to the machine that holds the session and relays the answer back. The gateway does not store it, count it or log it, and writes no event for it. The log line for a failed relay says only that it failed.
- **Agent connections can never call it.** The daemon allows `session_text`, `session_enter`, `usage`, `usage_top` and `counts` only on a connection that said `client: cli` in hello. Claude MCP, Pi and OpenCode connections get an error. The daemon also refuses a CLI whose calling process is a local session's process or a child of one, so an agent that runs `agent-net tui` in its shell tool is stopped. A process that detaches itself from its parent (a double fork) is not caught, and any process of your user can read `~/.claude/projects` directly: this is a barrier against an agent that simply runs the command, not against hostile code of your own user.
- Before the text leaves the machine that holds it, secrets are masked: `sk-...` keys, GitHub tokens, AWS key IDs, Slack and Google keys, JWTs, bearer and `Authorization` headers, `password`/`token`/`secret`/`key` values after `=` or `:`, passwords in URLs, and private key blocks. The daemon that asks masks again. This catches common shapes only; a secret in an odd shape is not masked. Each entry is cut to 2,000 characters (tool results 600) and control characters are removed.
- Nothing of it is written to the gateway log, the events table or the daemon's database.
- The gateway cannot tell an owner's CLI from a program on that machine that says it is one. A compromised enrolled machine can therefore ask for the text of any online session on the network. This widens the trust note under [what agent-net does not protect against](#what-agent-net-does-not-protect-against).

**Entering a session** (`session_enter`, the TUI key `e`): same gate and same relay as the session text. The answer is a command for the owner's own terminal, so the TUI runs only what the local daemon returns. The secrets of a worker's private OpenCode or Codex server go in the answer only for a session on the asking machine; a machine answering a relayed question refuses to include them, and the asking daemon drops any `env` it gets from the gateway. For a session on another machine the TUI runs `ssh -t -o ConnectTimeout=5` by default, in your terminal and with your own keys and ssh config; `"enter_ssh": false` in `node.json` turns it off. agent-net stores no key and gains no access through ssh: it runs the command the local daemon planned, and the login is yours. When ssh cannot connect (exit status 255), the TUI shows the command to run on that machine instead. The ssh target is the host name the gateway lists for the session (or your `enter_ssh_hosts` entry), never a name inside the other machine's answer, and a target that starts with `-` or has spaces or shell characters is refused. A compromised enrolled machine can still make `e` run a command of its choice on itself, through your ssh login, when you enter one of its sessions: enter sessions only on machines you trust.

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

### Request outcome decisions

Only the owner can use `outcome_overrule`: the daemon applies the owner gate used for `session_text`, refusing agent clients and command lines inside an agent session's process tree. As with other owner-gated methods, the gateway trusts enrolled daemons. An enrolled machine can send the gateway frame, so keep enrollment credentials private.

An overruled request has a gateway-stored marker separate from its body. Adapters write `[agent-net] Nicolas overrules: do it.` only when that marker is present. The marker cannot be supplied to `agent_send` or `agent_reply`. Message bodies have the `[agent-net` marker broken before delivery, so a peer cannot imitate the framed note. The owner's gate is a barrier against a command running inside a session, not a defense against a program that detaches from its process tree.

Outcome reasons and recommendations are visible to enrolled machines through the owner inbox. They are limited to 2048 bytes each and cleaned of controls. Do not put secrets in them. Unresolved rejections and escalations keep their request and reply messages and both session records past the normal retention period; normal retention applies after the owner answers.

## Graph foundation

Graph operator methods use the owner gate, including history and graph reads. They refuse agent clients and CLI processes inside an agent session. As with session policy, an enrolled machine can send the gateway frames, so the gateway trusts enrolled daemons. Charter values are at most 2048 UTF-8 bytes after control characters other than newline are removed. Charters and edge history must not contain secrets.

## Playbook notes

`playbook_set` and `playbook_get` use the same owner gate: your command line or the TUI, never an agent client and never a CLI inside an agent session. The CTO can also call them through its process-tree-checked private channel. A note is at most 1024 bytes after control characters other than newline and tab are removed, and it appears in every `agent_start` result for that model. It is not a secret store: the note reaches any agent that starts a worker with that model, so do not put credentials in it.

`agent_learn` is the other direction: it is open to agent sessions, because they are the ones that notice things, and refused for the command line. A finding is at most 500 bytes, names a subject and may carry up to 20 session or worker IDs. A session may record at most 3 findings in 24 hours; the fourth is refused. Findings are not messages and never appear in Needs you. They are stored in the gateway's `findings` table with the reporting session and a status. Automatic notes built from them are not secrets either, and they reach every agent at the moment of choice.

## Written approvals and the inbox

The daemon accepts `inbox_list` and `inbox_answer` only through the owner operator gate. Agent tools can create requests only for their bound session; the gateway checks that the session belongs to the authenticated daemon and derives the project from it. As with other owner operations, a compromised enrolled machine is within the trust boundary.

An approval is for one exact action. The gateway stores its approval note separately from message text and delivery adapters write it after the body with the `[agent-net]` marker. A message body claiming that Nicolas approved something does not count. The marker in message bodies is neutralized under the rule in decision [0013](decisions/0013-peer-trust-and-hints.md). The inbox item and answer remain in the gateway database; an answer and its queued delivery are committed together.

Rejection and escalation outcomes and their inbox items are committed together. Resolving an outcome in Needs you closes its inbox item; an inbox answer also resolves the corresponding outcome. Maturity guidance tells agents to use the approval tool for an exact merge or critical deploy, so a message body claiming approval does not stand in for a written note.

The full CTO UI attaches as a client of the private OpenCode server or Claude background session. CTO tools still run in the CTO process tree and pass the same private-tool process checks.

### Phone owner identity

The phone owner setting records the Tailscale login once in the gateway database.
The phone authentication check accepts only requests from 127.0.0.1 with exactly
one `Tailscale-User-Login` header equal to that setting. This relies on
Tailscale Serve to authenticate the caller and supply the identity header. A
local process can connect to a loopback listener and forge this header; the
check does not protect the gateway from other processes on its host. The host
and its local users must be trusted. Do not expose the HTTP listener directly
on a network or put an untrusted proxy in front of it.

The tools inventory scanner bounds a skill bundle to 10,000 entries and
64 MiB of regular file data. Pi source discovery is limited to 10,000
directories and 32 path components. It rejects special files, absolute or escaping
symlinks, and files that change while being read. MCP and settings files are
limited to 1 MiB. Inventory reports are bounded to 10,000 entries and 256 KiB
of field text. Oversized sources and reports fail the scan rather than silently
truncating it.

MCP fingerprints omit environment and header values, URL queries, and unknown
argument values. They retain URL paths, environment variable names, safe config
arguments, and numeric `--port` values.
The local inventory scan and read methods use the owner gate. The gateway stores
observations from authenticated daemons and keeps the previous records marked
stale after a failed scan. Discovery does not send skill contents or raw MCP configuration to the gateway.

Inventory reads are paged to at most 240 KiB of encoded report data per response.


### Managed skill store and backups

Only an owner connection can request `tools_import`, `tools_versions`,
`tools_export` or `gateway_backup` through the daemon. The gateway trusts its
enrolled daemons, as it does for other operator requests. Before importing, the
owner must review the source files for secrets. Import sends the skill contents
to the gateway. It does not run them or modify their source. The gateway checks
the observed hash and rejects traversal, escaping links, special files and
bundles over 2 MiB or 2,048 entries. A source that changed since its scan is
rejected. Unknown and unsafe content is not made safe by hashing it.

The gateway data directory contains `skills.git`, a private bare Git repository.
Its directory is mode 0700. Imported file contents, including any secrets in
those files, are retained in Git history. SQLite stores the item ID, content
hash, commit and source location. The gateway account and anyone who can read
its data directory or a backup can read these contents. Git history does not
erase a secret when a later version removes it. Nothing is pushed to a remote.

Gateway backups contain the database, skill repository, CA private key and
server key. The backup directory and copied files are owner-only. Protect the
backup as carefully as the gateway data. Backup serializes imports with the
SQLite snapshot and repository copy. Restore writes only to a new directory and
checks SQLite, Git objects and every saved skill hash before making it available.


### Tool assignment plans

`tools_plan`, `tools_apply` and `tools_history` require the owner gate. The
private CTO process-tree gate may use these methods; its brief requires the
owner's explicit words in the chat before applying. The daemon attributes
calls as owner or CTO and the gateway checks that the request belongs to the
sending machine's online owner session. As with other gateway operations, the
gateway trusts its enrolled daemons.

Plans contain observed source and destination paths, target machines and
conflicts. They do not copy or remove files. The gateway binds each plan to the
observed inventory, project registry, item versions and desired revisions. It
rechecks the target paths and refuses a changed plan. Offline paths cannot be
inspected and are reported as unresolved. A successful apply records desired
state as pending; it does not claim that any target was changed. Subsequent
reconciliation must revalidate paths and hashes before file operations.

The gateway records plans and apply attempts with the owner or CTO actor,
targets and result. Charter needs are untrusted advisory text, not authorization
or assignment. Plan and audit records may include machine and filesystem paths;
they do not include skill contents or native MCP credential values.
