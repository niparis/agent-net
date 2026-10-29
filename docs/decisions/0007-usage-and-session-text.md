# 0007 - CPU and memory, session text, and session counts

**Date:** 2026-10-29
**Status:** Built on branch `worktree-agent-abd453ab6f8b7e31d`
**Context:** the owner wants to see what each machine and each session costs, to follow what happens inside a session, and a header count that does not confuse. Related: [0004](0004-tui-and-event-feed.md), [0006](0006-phone-app.md).

## D1 - CPU and memory are sampled by the daemon and held in memory by the gateway

The daemon reads the machine every 5 seconds and keeps a 5 minute window. Linux reads `/proc` directly. macOS runs `ps`, `sysctl` and `vm_stat`, because the binaries are built without cgo; we did not take gopsutil. CPU is a difference of CPU time between two readings, not the lifetime figure of `ps`. A session's figure is its whole process tree: the agent, background shells, MCP servers, dev servers.

Every 10 seconds the daemon sends a small summary (frame `node.usage`): machine figures and one entry per process tree. The gateway keeps only the newest report per machine, in memory. It is a live reading, so it has no table and no migration (migration 7 stays free). A gateway or daemon from before this feature answers `unknown frame type`; the daemon stops sending on that connection and readers show "unknown".

The top 5 processes of a tree are not in the report. They are fetched on request (`usage_top`), with the first 80 characters of each command line and secrets masked.

OpenCode runs many sessions in one service process. That tree is reported once, marked shared, with the list of its sessions. It is never split.

## D2 - Session text is owner data, asked for on demand

The method `session_text` returns the recent entries of one session (prompt, reply, tool call, tool result), newest last, with a cursor to page back and a cursor to follow. Claude Code is read from the transcript file, from the end. Pi gives its history from `ctx.sessionManager.getBranch()`. OpenCode gives `ctx.session.context({sessionID})`.

Rules:

- Only the owner's CLI or TUI (a `hello` with `client: cli`) may call `session_text`, `usage`, `usage_top` and `counts`. Every bound agent connection (Claude MCP, Pi, OpenCode) is refused. A CLI that runs inside an agent session is refused too: the daemon walks the parents of the calling process and refuses it when one is a local session's process. This stops an agent that runs `agent-net tui` from its shell tool. It does not stop a program that detaches itself on purpose; against a process of your own user nothing in agent-net is a wall (see security.md).
- The text is masked (keys, tokens, bearer headers, `key=value` secrets, private keys, URL passwords) and cut to 2,000 characters per entry (tool results 600) on the machine that holds the session, and masked again by the daemon that asks.
- Another machine's owner asks through the gateway (`query.session_text`, relayed as `node.session_text`). The gateway relays and keeps nothing: no store, no log line with content, no event.
- Nothing of it goes into logs or the events table.

## D3 - Counts for the header

`query.counts` returns online sessions, sessions that went offline in the last 24 hours, and all offline sessions. The gateway keeps offline sessions for 30 days, so the total is mostly old restarts. The header should say "12 online, 5 offline in the last 24 h" and leave the total out, or show it dimmed.

## D4 - A plugin that sends no mode gets "unknown"

A Pi or OpenCode plugin that sends no mode is reported as `unknown`, not as empty. An empty mode is kept for a machine on an older agent-net.
