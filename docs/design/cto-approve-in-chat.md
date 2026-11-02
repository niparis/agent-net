# Approve CTO proposals in the CTO chat

**Date:** 2026-11-02. **Status:** plan, for the owner's review. Nothing built.

## 1. Goal

When the CTO proposes an owner-only change, the owner approves or refuses it
where the conversation happens: in the CTO chat. The Inbox (tab 7) stays as a
second way, and stays the only way when the dialog is not available.

Today the CTO answers "it is waiting in the Inbox, press A there", and a typed
"approved" does not count, because agent-net cannot tell the owner's words from
the model's.

## 2. The mechanism: an MCP elicitation

MCP has a request for exactly this: the server asks the client to show a form
to the person, and the client returns the person's answer. The model does not
see the form and cannot answer it. agent-net's CTO tools come from its own MCP
server, so that server can ask.

1. The CTO calls a tool that makes a proposal (for example
   `worker_profile_add`). The daemon stores the proposal as today.
2. Before the tool returns, agent-net's MCP server sends an elicitation to the
   CTO's harness: the proposal in plain words, the diff, and two buttons,
   Approve and Refuse.
3. The harness shows the dialog. The owner clicks.
4. Approve: the MCP server tells the daemon, which applies the proposal exactly
   as an Inbox approval does (same checks, same outcome note, same undo).
   Refuse: the proposal is refused. Dismissed or no answer in 10 minutes: the
   proposal stays in the Inbox.
5. The tool result tells the CTO what happened, so its reply is one line:
   "Added terra-low." or "Refused." or "Waiting in the Inbox (7)."

## 3. Which harnesses

| CTO harness | Elicitation | What happens |
|---|---|---|
| Claude Code | yes | dialog in the chat |
| Codex | yes | dialog in the chat |
| OpenCode | to check | dialog if supported, otherwise the Inbox |

The MCP server reads the client's declared capabilities at connection. Without
elicitation it keeps today's behavior.

## 4. Security

- **Who may approve this way:** only the CTO's own agent-net MCP server, for a
  proposal the CTO made in the same session. The daemon accepts an
  "approved in dialog" call only from that server's connection (the existing
  process check that binds an MCP server to its session), and only for a
  proposal that is open and belongs to that CTO session.
- **What the dialog shows:** the same text the Inbox shows, built by agent-net
  from the stored proposal, never from the model's words.
- **What it does not cover:** a process running as the owner can already
  approve through the TUI's socket; this adds no new reach. The accepted limit
  in `docs/security.md` (a local agent can escape the process check) applies
  unchanged.
- **Unchanged:** major graph changes outside the 30-minute driving window,
  and every non-graph owner-only change, still need the owner. The dialog is
  the owner acting, so it satisfies that.

## 5. Also in this change

- The CTO's text names the Inbox, not "Needs you" (done in v0.4.92).
- The CTO's replies after a proposal are one line, because the tool result
  already says what happened.

## 6. Build steps

1. Check OpenCode's MCP client for elicitation support (a test against the real
   harness).
2. MCP server: ask for approval after each proposal, when the client supports
   it; a 10-minute limit; the answer goes to the daemon.
3. Daemon: the "approved in dialog" call with its checks, reusing the Inbox
   approval path. Tests: approve, refuse, dismiss, timeout, a proposal from
   another session, a client without elicitation.
4. Docs: `docs/reference.md` (CTO proposals), `docs/security.md`, a decision
   record.
5. A real test with the Claude Code CTO on the owner's Mac.

One worker, then one release.
