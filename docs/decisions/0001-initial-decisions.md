# 0001 - Initial decisions

**Date:** 2026-10-28
**Status:** Accepted by the owner
**Context:** [brief.md](../brief.md), [review.md](../review.md)

## D0 - Do not use Reason Network

Reason Network covers much of Phase 1, but it is a paid product with very strict limits. We build our own. Agents on this project must not use the `reason` CLI or the `reason-network` MCP tools.

## D1 - One server

Registry, messaging, skills registry, MCP federation and identity are modules of one gateway process. Each machine runs one `agent-netd`, which does session delivery and skills sync. No set of separate tools.

## D2 - Full skills registry

Build the registry described in brief section 10, as a gateway module. Git is the canonical store. `agent-netd` syncs approved versions into each harness's native skill folder.

## D3 - Codex: CLI and Desktop

Codex 0.165 runs a shared managed app-server daemon on a local Unix socket, and has `codex queue` and session hooks. The adapter uses those, so a plain `codex` CLI session is reachable without `--remote`. Desktop is in scope too: through the same daemon if it shares it, otherwise through the OCS Desktop path. The step 1 spike confirms both.

## D4 - LAN first, Tailscale later

v1 node auth: one-time enrollment code, then a per-node token over TLS with a pinned gateway CA. Node auth sits behind one interface so Tailscale identity can be added later.

## D5 - Session identity through a local stdio MCP process

Each harness session starts a stdio MCP process that talks to the local `agent-netd`. The gateway gets session identity from the daemon, never from the model. Replaces the shared HTTPS endpoint in brief section 9.1.

## D6 - MCP federation serves the owner's own MCP servers

Federation targets the MCP servers the owner writes. GitHub MCP is out: agents use the `gh` CLI, which works better.

## D7 - Grok Bot is a cloud product with a webhook-only way in

Checked on 2026-10-28 against [xAI's routines page](https://docs.x.ai/grok-bot/skills-routines-and-automations). Routines run in xAI's cloud. A routine webhook (`POST` with `Authorization: Bearer <key>`) **starts a new run**. It does not add to an existing conversation, and the page documents no API to message one. Limits: 50 routines per Bot, 300 runs per hour in total, 200 per routine. The page does not mention custom MCP servers. Third-party guides say a Bot can register a remote MCP server from chat, but that is not confirmed by xAI.

Consequence: Grok Bot cannot be a normal network member. At most, it is a "send-only target": the gateway can trigger a routine run, and the Bot can call back through a public-facing bridge if remote MCP works. It needs an internet-facing endpoint, which v1 does not have. Deferred until after step 7.

## D8 - OCS is reference material only

We do not fork OCS or depend on it. A clone lives outside this repo at `~/code/niparis/open-cross-session` (last reviewed at `478ffac`, v0.9.6). We read its harness code and tests and port the logic to Go. Its MIT copyright notice goes next to any ported harness code. From time to time we diff its harness files (`src/claude-*`, `src/codex-*`, `src/pi-*`, `src/grok.ts`, `src/wake*`) against the last reviewed commit. A change there usually means a harness protocol changed.

## D9 - Go for everything except in-process plugins

One Go binary, `agent-net`, with subcommands: `gateway`, `daemon` (one per machine), `mcp` (per-session stdio MCP process), and CLI inspection commands. MCP through the official `github.com/modelcontextprotocol/go-sdk` (v1.7.0+, supports 2026-07-28). SQLite through `modernc.org/sqlite` (pure Go, so macOS builds from Linux are easy). Git through the `git` CLI. The Pi extension and the OpenCode plugin are small TypeScript layers, because they must run inside those apps. They talk to the daemon over a local socket. This replaces the Python/FastAPI stack in brief section 14.

## D10 - Codex Desktop deferred

Owner decision, 2026-10-28: skip Codex Desktop for now. v1 supports the Codex CLI only, through the shared app-server daemon and `codex queue` (see [spike/codex.md](../spike/codex.md)). Desktop comes back after step 5, probably through the OCS Desktop IPC path.
