# Review of the Agent Network Gateway brief

**Date:** 2026-10-28
**Reviews:** [brief.md](brief.md) v1.0
**Method:** Read the brief. Checked its integration claims against the current docs for Claude Code, Codex App Server, Pi, OpenCode v2 and OCS. Checked what already runs on this machine (`ListAgents`, `reason network who`, installed CLIs).

## 1. Verdict

The brief is well thought out. It gets the hard parts right: adapters own presence, MCP is the control interface and not the delivery transport, peer messages carry no authority, and "accepted by gateway" is not "read by the agent". Phase 0 as a hard gate is correct.

It has four real problems:

1. **It does not explain how an MCP call identifies its session.** This is the main design gap (section 4).
2. **It does not list existing tools it is replacing.** Reason Network already covers much of Phase 1 but is rejected (paid, strict limits). The brief should record that, so nobody re-opens it (section 3).
3. **Some integration paths are weaker or different than the brief says**: Claude Channels, Codex CLI, OCS coverage (section 2).
4. **Scope.** Six phases, five harnesses, MCP federation and a skills registry with an approval workflow is a lot for a one-person system. Two of the subsystems have a much simpler direct path (section 5).

## 2. Integration claims, checked against the docs

| Harness | Brief says | What the docs say | Effect on the plan |
|---|---|---|---|
| **Claude Code** | Inbound by cross-session delivery, OCS or Channels | Each session binds a per-session inbox Unix socket. Its path and a token are exported to hooks as `CLAUDE_CODE_MESSAGING_SOCKET` / `CLAUDE_CODE_MESSAGING_TOKEN`, including in `SessionStart`. A busy session reads a message between tool calls. An idle session starts a new turn. Same-machine messages never leave the machine. | **Best path: a `SessionStart` hook sends the socket path to the local daemon, and the daemon posts to the socket.** No Channels and no OCS needed. Phase 0 must still pin down: (a) the JSON line format for an external poster (the page documents only the auth line), and (b) inbound controls. A message from a non-child process asserts no permission class, so a session in bypass mode *holds* it, and held messages drop after 5 minutes. Set `crossSessionInbound: "accept"` on the agent VMs. |
| Claude Code | (not mentioned) | Native cross-machine messaging already works through Remote Control, routed through Anthropic servers. | Claude-to-Claude across VMs needs no gateway. The gateway's value for Claude is reaching *other* harnesses. |
| Claude Channels | A candidate inbound transport | Research preview. Custom channels need `--dangerously-load-development-channels`. Claude Code **does not register a channel server that negotiates MCP revision 2026-07-28**. | **Drop Channels.** A gateway that speaks the July 2026 spec cannot also be a channel. |
| **Codex** | App Server; plain CLI sessions maybe not controllable | Checked on this VM (codex-cli 0.163, daemon 0.165). Codex runs a **shared managed app-server daemon** (`codex app-server --listen unix:// --managed-daemon`), with its control socket at `~/.codex/app-server-control/app-server-control.sock`. `codex agents` browses every session on that daemon. `codex queue --thread <id> --message <text>` queues a message into an existing session. Codex has hooks in `~/.codex/hooks.json` (`SessionStart`, `UserPromptSubmit`, `PostToolUse`, `Stop`, `SessionEnd`). The public app-server page still marks the API experimental. | **A plain `codex` CLI is reachable, with no `--remote` needed.** Adapter: hooks register and unregister the session, and delivery goes through `codex queue` or JSON-RPC on the control socket (`turn/start` when idle, queue when busy). **Desktop**: the spike must check whether it shares the same daemon. If not, use the OCS Desktop path. The spike must also check whether `codex queue` wakes an idle session or waits for the next prompt. |
| **Pi** | Extension with idle trigger and busy modes | Confirmed: `pi.sendMessage(msg, {deliverAs: "steer" \| "followUp" \| "nextTurn", triggerTurn})`, plus `session_start` / `session_shutdown` / `agent_settled` events. MCP is the `builtin:mcp` extension. | Easiest harness, as the brief says. Two details: the default is `steer` (the brief's policy is follow-up, so pass `followUp`). Use `sendMessage` and **not** `sendUserMessage`, which makes the text look like the user typed it and loses provenance (brief 12.6). A Pi extension can register its own tools, so Pi does not need MCP for network operations. |
| **OpenCode v2** | Plugin with native session prompting | Confirmed: `ctx.session.prompt({sessionID, text})` with `delivery: "steer" \| "queue"`, `ctx.event.subscribe()`. The plugin page does not list session lifecycle event names. | Feasible. **OCS does not support OpenCode**, so this adapter is fully yours. |
| **Grok Build** | OCS with `grok --leader` | OCS lists `grok --leader` and says Grok web and mobile are not connected. | Only path is OCS. Low priority. |
| **Grok Bot** | Webhook bridge | Not identified. | Keep out of scope until you name the exact product. |
| **OCS** | Reusable transport behind an abstraction | One static binary, no server. Append-only JSONL under `~/.ocs`. LAN mode is opt-in with an 8-hour pairing trust. "Nothing watches for sessions coming online." 27 stars, MIT. No OpenCode. Wake-note bodies are cut at 4 KB. | Small project. Its value is that it has already **worked out the wire formats** (Claude inbox, Codex Desktop IPC, Grok leader). **Read its source to learn them. Do not run it as a dependency in v1**, except possibly for Grok. |

## 3. Rejected alternative: Reason Network

**Decision (owner, 2026-10-28): do not use Reason Network.** It is a paid product with very strict limits. Agents in this project must not use the `reason` CLI or the `reason-network` MCP tools.

For the record, it already does much of Phase 1. `reason network who` lists Claude Code and Cursor sessions on this VM and Pi and Claude Code sessions on the MacBook, with busy/idle state and an auto-generated task line. It is cloud-hosted, which also breaks principle 2.6 (local-first).

Two ideas worth copying from it:

- **Free self-description.** Each session gets a one-line task summary with no extra model call. `agent_announce` can start pre-filled the same way (section 6).
- **Presence that includes a "shell" state.** This is the harness at a prompt with no live model loop, which is different from idle.

## 4. Design gap: how an MCP call knows its session

Section 9.1 has every harness call one HTTPS endpoint, `https://agent-gateway.lan/mcp`. Section 5.1 says the trusted session ID comes from the adapter, not the LLM. These two do not fit together. When Claude calls `agent_send` over HTTPS, the gateway sees only an HTTP request. It cannot tell which of the five Claude sessions on that VM sent it. Putting a session token in each harness's MCP config does not help either, because that config is shared by every session of that harness.

**Recommendation: a local stdio MCP shim per session, not a shared HTTPS endpoint.**

```
harness session --stdio--> agent-net mcp (child process) --Unix socket--> agent-netd --WSS--> gateway
```

- The harness starts the shim as a stdio MCP server, so there is one shim per session.
- The shim finds its session from its parent process (PID, plus `CLAUDE_CODE_MESSAGING_SOCKET` if Claude passes it to MCP children; Phase 0 must check this). It asks `agent-netd`, which already registered that session through the harness hook.
- Node credentials stay in `agent-netd`. No harness config holds a gateway secret.
- When the gateway is down, the shim can still answer with a clear error, and `agent-netd` can queue outbound sends.
- Pi and OpenCode can skip the shim and register the same tools directly in their extension or plugin.

The 2026-07-28 MCP revision is stateless, which makes a stdio shim even more natural than one long-lived shared HTTP session.

## 5. Scope: simpler paths

**Skills registry (brief section 10).** The review suggested plain Git sync (a timer and symlinks) in place of the full registry. **The owner chose the full registry, as one module of the gateway** (decision D2). Keep it simple inside: Git stays the store, so versions, rollback and hashes come from Git, and `agent-netd` does the sync. The registry adds search, the approval step, compatibility metadata and install status on top.

**MCP federation (brief section 9).**
- It works only for servers that do not need local files: API-backed ones such as GitHub or Linear. Many MCP servers (filesystem, browser, language servers, anything that reads the repo) have to run on the agent's own VM. Centralizing them breaks them.
- `tools_search` + `tools_call` hide real tool schemas from the model, so tool use gets worse. Claude Code already loads tools on demand (deferred tool search). Other harnesses may do the same.
- Existing MCP aggregators and gateways are worth evaluating before building one.
- Recommendation: federate remote API servers only, preferably with an existing aggregator. Defer it until messaging is solid.

**Node authentication (brief 12.1).** Owner decision D4: plain LAN first, Tailscale later. LAN: each node enrolls once with a one-time code printed by the gateway, then holds a per-node token and talks to the gateway over TLS (gateway CA certificate pinned in the daemon). Keep node auth behind one interface, so Tailscale identity (`tailscale whois` on the peer address) plugs in later without changing anything else.

**Delivery states (brief 7.4).** Seven states is more than most harnesses can report. Keep the column, but expect only `stored`, `dispatched`, `delivered` and `replied` (plus `failed` / `expired`) in practice. `processing` needs an explicit agent action that models will often skip.

## 6. Smaller points

- **Busy policy is per harness, not universal.** Claude reads messages between tool calls, which is effectively steering. You cannot change that. Pi and OpenCode let you choose. The brief's "do not steer by default" (7.5) should become "use the least disruptive mode the harness offers, and record which one was used".
- **Loop control must live in the gateway.** Claude throttles loops on its own side. Pi, Codex and OpenCode do not. Add a hop count and a per-conversation message budget to the envelope.
- **Self-description is free on Claude.** `ListAgents` and Claude's own session naming already derive a session name without an LLM call. `agent_announce` can start pre-filled from the session's first prompt or title, so the network is useful before the model calls the tool.
- **"VMs" includes a Mac.** The MacBook already hosts Pi and Claude sessions, so `agent-netd` must run on macOS (launchd), not only on systemd Linux.
- **Session discovery is push, not scan.** Every harness has start/stop hooks: Claude `SessionStart` / `SessionEnd`, Pi `session_start` / `session_shutdown`, OpenCode plugin events, Codex app-server thread events. `discover_sessions()` should mostly be "hooks call the daemon", with scanning only to recover after a daemon restart.
- **Agent cost.** An idle-triggered message starts a paid turn in the recipient. Rate limits protect your budget as well as stability.

## 7. Build order (revised after owner answers)

**Shape (decision D1): one server.** Everything is a module of a single gateway process: registry, messaging, skills registry, MCP federation, identity. Each machine runs one `agent-netd`, which does session delivery *and* skills sync. Each harness session gets one small stdio MCP process that talks to `agent-netd`. No separate tools.

| Step | What | Exit criterion |
|---|---|---|
| **0. Decisions** | Done: [docs/decisions/0001-initial-decisions.md](decisions/0001-initial-decisions.md). | Recorded. |
| **1. Spike** | Throwaway scripts, no gateway. Claude: post to the inbox socket from a foreign process (idle, busy, bypass mode). Pi: extension injecting from a local socket. Codex CLI: `codex queue` and the daemon control socket (idle, busy). Codex Desktop: same daemon or not. OpenCode: `session.prompt`. Stdio MCP process: can it identify its parent session? | One page per harness in `docs/spike/`: works / does not work / caveats. |
| **2. Gateway core + Claude and Pi** | Gateway (FastAPI, SQLite, store before ack, idempotency keys), LAN node enrollment, `agent-netd`, stdio MCP process, Claude and Pi adapters. This VM <-> MacBook. | Claude here and Pi on the Mac discover each other, exchange a request and a reply in their running sessions, and keep accepted messages through a gateway restart. |
| **3. Reliability** | Reconnect, expiry, rate limits, hop budget, `agent-net` CLI for inspecting messages. | The brief's Phase 2 exit criterion, tested by killing processes. |
| **4. Skills registry** | Gateway module: Git-backed store, search, publish with approval, versions, rollback. `agent-netd` syncs into each harness's skill folder, with atomic switch. OpenCode HTTP catalog. | A skill published on one machine is found, installed and used on the other with no manual copying. Rollback works. |
| **5. Codex and OpenCode** | Codex adapter (hooks + shared daemon, CLI and Desktop). OpenCode plugin. Grok via OCS last, if still wanted. | Acceptance matrix rows pass per harness. |
| **6. MCP federation** | Gateway module for API-backed upstream servers. | Two harnesses call the same upstream tool under their own identity. |
| **7. Tailscale + hardening** | Tailscale node identity, backups, metrics, upgrade process. | The brief's Phase 6 exit criterion. |

Why skills come before Codex and OpenCode: skills reuse the daemon built in step 2 and need only a folder mapping per harness, while the new harness adapters carry the most unknowns.

## 8. Open questions for the owner

1. ~~Build vs Reason Network?~~ Settled: Reason is rejected (paid, strict limits).
2. ~~Codex launch mode?~~ Settled (D3): plain CLI via the shared daemon, and Desktop.
3. ~~Skills approach?~~ Settled (D2): full registry, as a gateway module.
4. ~~Which MCP servers?~~ Settled (D6): the owner's own MCP servers. No GitHub MCP; agents use `gh`.
5. ~~Network?~~ Settled (D4): LAN first, then Tailscale.
6. ~~Which Grokbot?~~ Settled (D7): xAI Grok Bot, cloud, webhook starts new runs only. Deferred.
7. ~~Remove Reason hooks?~~ Done 2026-10-28 on this VM (Claude, Codex, OpenCode, skill). Backups in `~/.reason-removal-backup-20261028/`. The MacBook still has it.
8. OCS: fork and use the harness modules as a library (option B, section 9)? TypeScript throughout?

## 9. OCS: fork or not

Looked at the source on 2026-10-28 (`leeguooooo/open-cross-session`, v0.9.6, released 2026-10-25).

**Facts**

- TypeScript on Bun, MIT. About 17,000 lines of source and 10,000 lines of tests. It started on 2026-09-01, so it is two months old, with frequent releases and mainly one author. The design docs are in Chinese.
- **Architecture: no server.** Each machine keeps its own append-only JSONL logs. Machines pair peer-to-peer over the LAN (Ed25519, X25519, AES-GCM). Both ends store a message separately. This is the opposite of D1 (one server holds the directory, the messages and the policy).
- **The valuable part is the harness code.** About 5,000 lines, in a few groups:
  - Claude: `claude-inject.ts`, `claude-receipt.ts`, `claude-address.ts`
  - Codex Desktop: `codex-ipc.ts`, `codex-ipc-win.ts`
  - Codex CLI: `codex-queue.ts`, `codex-steer.ts`, `codex-defer.ts`, `codex-sessions.ts`
  - Pi: `pi-extension.ts`, `pi-sessions.ts`
  - Grok: `grok.ts`
  - Hermes: `hermes.ts`
  - Shared delivery logic: `wake*.ts`, `idle.ts`

  This code holds exactly what we still have to learn: how to post into a Claude inbox and read its receipt, how Codex Desktop's private IPC works, how to prove a Codex CLI session is alive, and Grok leader IPC. Its rules match ours: store before waking, never resend after an unknown outcome, and keep the true sender in the message.
- It has no OpenCode support, no central registry, no skills and no MCP federation.

**Options**

| Option | Gain | Cost |
|---|---|---|
| A. Fork and use it as the base | Fastest first demo | We would replace its core: the store, the LAN layer, the roster and the CLI (about 60% of the code). We would spend the project fighting a design built around having no server. |
| **B. Fork it, keep only the harness code, use that as a library in `agent-netd`** | Reuses the hardest-won knowledge. Upstream fixes to private protocols (Codex Desktop IPC is probed at runtime and will break) can still be pulled from upstream. | The daemon has to be TypeScript/Bun. We keep a thin layer between OCS's delivery result types and our delivery states. |
| C. Read it, write our own | Clean code in any language | We rediscover every edge case OCS already handles, and get no upstream fixes. |

**Recommendation: B.** Fork to your GitHub account. Our code calls only the harness modules, behind our adapter interface (`deliver`, `get_state`, ...), with the fork pinned by commit. Pull from upstream on purpose, never automatically.

**Effect on the stack.** B makes `agent-netd` TypeScript/Bun. Pi extensions and OpenCode plugins are TypeScript too. The question is whether the gateway stays Python (as the brief says) or also moves to TypeScript, which would mean **one language across the system**, with the official TypeScript MCP SDK. One language is simpler to maintain and lets the gateway and daemon share the message and envelope types directly. Recommendation: TypeScript throughout, unless you have a strong reason to keep Python.
