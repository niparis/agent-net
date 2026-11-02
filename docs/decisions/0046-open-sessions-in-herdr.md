# 0046 - Open sessions in herdr

**Date:** 2026-11-01
**Status:** Accepted.

## Context

The owner wants every session opened by agent-net to use herdr. Taking over the
TUI terminal hides the TUI and turns off mouse tracking in Claude attaches.
Decision 0032 made Enter open a session; decision 0039 added offline resume.
Decision 0035 made the CTO a normal session.

## Decision

Open sessions in herdr for Enter, starts, resumed sessions, and the CTO. Focus
an existing pane when one holds the session. Otherwise split beside a TUI
already in herdr, or create a titled tab on the default server. Start a
headless server when needed. The TUI keeps running and reports the open in its
result line. A herdr Claude attach keeps mouse tracking on. The daemon keeps
private worker connection values out of command arguments and pane input.

For another machine, use its herdr pane and a local herdr remote client when
available. Otherwise run the SSH plan in the local herdr pane. The SSH setting
and the boundary around private worker credentials still apply.

When herdr is unavailable or its server cannot start, retain the prior enter
and start behavior and give an installation hint. Ctrl+] still returns from a
terminal handover. An interactive session outside herdr and tmux cannot be
attached twice; its existing window remains the way to enter it. A headless
server needs an attached herdr client to display its panes.

Claude, OpenCode and Codex workers open a view on their own machine after
they register, including when a worker is resumed. A Pi worker uses RPC mode,
which has no interactive terminal to attach. Its live conversation remains
available through agent-net, but it cannot be opened as a Pi terminal session.

When an approved Codex session needs the terminal fallback, only the TUI that
approved it runs the command. This prevents multiple open TUIs from starting
the same session more than once.

This changes the terminal handover in decisions 0032 and 0039 when herdr is
available. Their selection and resume rules remain in force.

## Correction 2026-11-02

v0.4.72 limited herdr to a TUI that runs in herdr, so that Enter would not open
a pane the owner cannot see. It also stopped using herdr's remote client for a
session on another machine when the TUI ran in a plain terminal, and plain ssh
cannot carry the clipboard: image paste failed. The remote client runs in the
TUI's own terminal, so it is used again whenever this machine has herdr.
AGENTS.md states the rules.
