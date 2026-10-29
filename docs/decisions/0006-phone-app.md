# 0006 - Phone app (planned)

**Date:** 2026-10-29
**Status:** Planned by the owner. Build after workers (0002) and the next TUI work.
**Context:** the owner wants to browse and use their agents from their phone, with Tailscale installed on the phone. Related: [0002](0002-workers.md), [0004](0004-tui-and-event-feed.md).

## P1 - The phone shows the same data and offers the same functions as the TUI

This is the main requirement. The phone is not a smaller product. Everything the TUI can show, the phone can show, and every action the TUI can do, such as sending a message, the phone can do too.

To keep the two from drifting apart, the TUI and the phone use one shared operator interface. That interface covers the data (machines, sessions, projects, the message feed, message bodies) and the actions (send a message, start a worker, stop a worker, and future ones). A new feature is added there once, and both front ends get it. The TUI must not grow actions that call the daemon in a way the phone cannot reuse.

## P2 - A web app served by the gateway, not a native app

The gateway serves a small web app that can be added to the phone's home screen. Reasons:

- no app store;
- one codebase;
- it ships with `agent-net upgrade`.

A native app is not planned.

## P3 - Reached through Tailscale only

- **Certificate:** `tailscale serve` puts the web app behind a real `*.ts.net` certificate. Phones do not trust the gateway's own CA.
- **Who is calling:** the gateway learns the caller from the Tailscale identity headers. It accepts only the owner's Tailscale login, and it refuses requests that did not come through Tailscale serve.
- **No secrets on the phone:** no passwords and no tokens are stored there.

## P4 - The phone acts as the owner

A message sent from the phone comes from the owner in person, not from an agent session. Agents are told that such a message is from their user. This is why P3 must be strict: the phone is as powerful as the owner's keyboard.

## P5 - Order

1. Workers (0002) and the TUI actions, built on the shared operator interface of P1.
2. The phone view and actions, on that same interface.
