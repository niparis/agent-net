# 0006 - Phone app (planned)

**Date:** 2026-10-29
**Status:** Planned by the owner. Build after workers (0002) and the next TUI work.
**Context:** the owner wants to browse and use their agents from their phone, with Tailscale installed on the phone. Related: [0002](0002-workers.md), [0004](0004-tui-and-event-feed.md).

## P1 - The phone shows the same data and offers the same functions as the TUI

This is the main requirement. The phone is not a smaller product. Everything the TUI can show, the phone can show, and every action the TUI can do, such as sending a message, the phone can do too.

To keep the two from drifting apart (built: see [0008](0008-operator-interface-and-worker-build.md), O1), the TUI and the phone use one shared operator interface. That interface covers the data (machines, sessions, projects, the message feed, message bodies) and the actions (send a message, start a worker, stop a worker, and future ones). A new feature is added there once, and both front ends get it. The TUI must not grow actions that call the daemon in a way the phone cannot reuse.

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

## P6 - iPhone, without the App Store

The web app is a PWA. On iPhone it is added from Safari with Share, then "Add to Home Screen". It then opens full screen with its own icon. Needs and limits:

- **Certificate:** iOS installs web apps and allows notifications only over a certificate it trusts. That is why P3 uses the `tailscale serve` certificate.
- **Push notifications:** home screen web apps get them since iOS 16.4. The gateway sends Web Push through Apple's push service. A notification carries only a short line, such as "worker on tenebrae finished". Message and conversation text never go into it.
- **When open:** the app shows live data only while it is open. It does not run in the background.
- **Off Tailscale:** the app shows "not reachable".
- **Not used:** TestFlight and sideloading, because builds expire.

## P7 - Stack

- **Pages:** Go `html/template`, served by the gateway and built from the same operator methods as the TUI (P1).
- **Actions:** [htmx](https://htmx.org). Forms and buttons call the operator methods and swap in the changed part of the page.
- **Live data:** Server-Sent Events carry the session list and the message feed.
- **PWA:** a web app manifest and a small service worker for the home screen icon and push. This is the only hand-written JavaScript.
- **Build:** the files are embedded in the binary with `go:embed`. There is no Node build step, and the release binaries stay pure Go.
- **Not used:** Flutter. Its web output draws on a canvas, so it loads heavy and does not feel native in Safari.
- **If the client side ever needs heavy logic:** use Svelte, built to static files and embedded the same way.

## P8 - Streaming: watch a worker, Q&A with agents

The owner will want to see what a worker is doing as it happens, and to have question-and-answer threads with any agent.

- **Live view.** The htmx SSE extension holds one connection open. The gateway sends each new conversation entry as a small HTML fragment, which the page appends. Entries are prompts, replies, tool calls (for example "ran `pytest`") and short tool results. The data comes from the conversation method with follow mode and a cursor, the same one the TUI uses (P1).
- **How fine it is depends on the agent:**
  - Claude Code writes its transcript one finished step at a time, so its view updates step by step.
  - OpenCode sends partial-text events, so its replies can stream word by word into the same entry.
- **Q&A.** A text box at the bottom sends the question with the "send as owner" action (P4). The agent gets it in its own conversation, marked as from its user. Its answer arrives through the same live connection. The screen reads as a chat thread with tool calls in between.
- **iPhone background.** Safari pauses the connection when the app goes to the background. On return, the browser reconnects with `Last-Event-ID`, and the gateway sends what was missed from that cursor.
- **Long threads.** The latest part loads first, and older entries load when the owner scrolls up.
- **Limit.** For a screen that needs heavy client-side logic, such as editing diffs in the browser, that one screen uses Svelte (P7).
