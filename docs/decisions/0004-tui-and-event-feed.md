# 0004 - The TUI, session mode and model, and the event feed

**Date:** 2026-10-29
**Status:** Proposed, built on branch `worktree-agent-a97bcf0f23c6359d2`
**Context:** a live, read-only view of every agent session (`agent-net tui`) that runs on any machine, plus two new facts per session.

> **Update 2026-10-29.** The TUI now also acts: send a message as the owner (`m`), start a worker (`s`), stop a worker (`x`). It still talks only to the local daemon, now through the operator interface of [0008](0008-operator-interface-and-worker-build.md), which the phone will share. D1 below describes the first version, which only read. The feed kinds now include `worker.*`.

## D1 - The TUI reads through the local daemon only

The screen is a CLI client of the local daemon (`hello` with `client: cli`) and calls only `info`, `nodes`, `list`, `events` and `message_status`. It never uses the admin API, which is for the gateway machine only. This way it works on every machine with no new credentials, and the daemon's rule that a CLI client is read-only is the limit on what the screen can do.

## D2 - The event feed is a polled cursor, not a push stream

A new gateway frame `query.events {after_id, limit}` returns events after a cursor, oldest first, and the daemon passes it on as the local method `events`. The screen asks once a second with the last ID it saw. We did not add a push stream: the local socket and the node link are request and answer, a stream would need subscriptions, flow control and a cleanup when the screen dies, and one cheap indexed query a second per open screen is enough. An older daemon ("not available to cli clients") or gateway ("unknown frame type") makes the screen show the sessions without the feed.

## D3 - What the feed contains

Metadata only: kinds `msg.*`, `session.*` and `node.*`; time, machine, session, message ID; for a message event also the sender, the recipient, the type and the parent message (so a reply can mark its parent replied, which has no event of its own). No bodies, no MCP or enrollment events, no machine addresses, details cut to 200 characters. Every enrolled machine can read it. That widens what a node sees (before: only the messages of its own sessions), and docs/security.md says so. Bodies stay limited to the two machines in the conversation; the screen shows a body only through the existing `message_status` call and explains the refusal otherwise.

## D4 - MODE and MODEL are reported by the harness side

MODE is `foreground`, `background` or `unknown`; MODEL is a string. Both are part of the session the daemon reports, so a change is pushed like any other change. Claude Code: `kind` of the session file (only `interactive` is foreground) and the `model` field of the last assistant entry of the transcript, read from the end of the file. Pi: `ctx.mode` and `ctx.model`. OpenCode: model from the session and its events; mode is `unknown`, because OpenCode 2.2.0 has no reliable "client attached" signal and a guess would be wrong more often than not. A new gateway migration (the one after the project registry's) adds the two columns; a shipped migration is never edited. Daemons and plugins that do not send them leave them empty, shown as `-`.

## D5 - Bubble Tea, with the model apart from the drawing

`internal/tui` keeps the state and the update logic (model.go, feed.go) apart from the drawing (view.go) and from the daemon (source.go, behind a small `Source` interface), so the screen is tested with fake data, golden files at two widths, and a fake daemon socket. Libraries (pinned): bubbletea v1.3.10, bubbles v1.0.0, lipgloss v1.1.0, all pure Go. Text from the network is stripped of control characters before drawing.
