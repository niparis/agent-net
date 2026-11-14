# 0167 - Enter takes over a stale herdr attach

**Date:** 2026-11-14
**Status:** Accepted.

## Context

The owner pressed Enter on a Pi session on his Mac, and the TUI said the
terminal attach failed because the terminal "already has an attached
client".

Pi runs in a headless herdr server of its own (decision 0140), and Enter
attaches a terminal of that server. herdr allows one direct attach per
terminal. A client left behind by an earlier Enter, or by a window the owner
closed, made the next attach fail. Decision 0002 had agent-net never use
`--takeover`, so the owner had to find and close the other attach himself.
He could not: the reason existed only on the TUI screen, because the failing
command runs in his terminal and the daemon never sees its output.

## Decision

The attach plan for a herdr-hosted session terminal uses `--takeover`. When a
terminal already has a client, Enter detaches it and attaches the newest
client. The session the owner asked for opens, like re-attaching a terminal
multiplexer. This covers Pi and any harness whose view is a herdr terminal.

The owner's normal herdr TUI client is not a direct attach, so it is not
touched. Claude, OpenCode and Codex attaches keep their own commands and
behavior; this decision does not change how those sessions are entered.

Every failed session Enter is written to the daemon log. The daemon logs a
plan it refuses, and the TUI reports a command that failed in the owner's
terminal with the command's last output. The lead reads both with
`agent_machine_log`.

## Consequences

A view the owner deliberately left attached in another window ends when he
presses Enter somewhere else. That is intended: the newest Enter wins. herdr
tells the old client it was taken over.

## Revisit when

- herdr offers a non-disruptive way to show a session already attached in
  another client; or
- the owner asks to keep several live views of one session at the same time.