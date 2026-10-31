# 0035 - The CTO is a standard agent session that proposes owner changes

**Date:** 2026-10-31
**Status:** Accepted (option A, 2026-10-31)
**Context:** [0022 component graph and CTO](0022-component-graph-and-cto.md), [0029 one CTO timeline](0029-cto-shared-timeline.md), [0034 the CTO box is read-only](0034-cto-box-is-read-only.md). Design: [docs/design/cto-as-agent.md](../design/cto-as-agent.md). Spike: `docs/design/cto-as-agent-spike.md` on branch `agent-net/w_3eefacd613258158`.

## Context

Decision 0022 made the CTO a private runtime: "It is not a network peer: no session can message it, so no other agent can talk it into changing rights." That rule has costs. The CTO sends as the owner session, and replies reach it only as a quoted forward. It is not in `agent_list`, the Sessions tab or the Graph. It has its own send, events and enter methods. Its 82 tool schemas cost about 11,930 tokens in every model call, and some list results cost several thousand tokens.

A first draft kept owner powers in the CTO and allowed them only in a turn that the owner's words started. The spike showed that this cannot be proven with today's Claude Code and OpenCode: turn provenance, transcript timing and links from a tool call to its input are not available or not reliable. The owner chose option A: the CTO proposes, the owner approves, the daemon runs.

## Decision

1. **A normal session.** The daemon starts the CTO on the claude or opencode harness, as today. It has one stable agent-net session ID across harness switches, the reserved name `CTO`, and a fixed `cto` node on the Graph. It is listed and entered like any session. Agents can message it, and replies reach it through normal delivery.
2. **Changes are proposals.** The CTO never runs an owner-only change. Graph and charter changes, worker start and stop, settings, tools and MCP changes, `mcp_call`, answers in Needs you, sends as the owner and `cto_new` are stored as proposals in Needs you. The owner approves or declines each with one key, through the owner gate. The daemon validates the stored call again and runs it itself with the actor `owner (CTO proposal p_N)`. The CTO gets a one-line outcome note without result data.
3. **Reads are public only.** The CTO's tools return only what an ordinary session reads through its own agent-net tools, plus the CTO's own conversations and proposals. Owner-only data (Needs you contents, other sessions' text, the owner's inbox, owner settings) reaches the CTO only when the owner types it or hands an item over with a TUI key. A private mode without agent messages was rejected: Claude's own inbox bypasses the daemon, and data read in that mode stays in the conversation afterwards.
4. **No turn rule.** Rules 2 and 3 hold in every turn, whoever started it, so the daemon does not need to know a turn's source.
5. **No built-in tools.** Claude starts with `--tools ""` and `--strict-mcp-config` with only `agent-net cto-mcp`. OpenCode's `cto` agent has plural `permissions` deny rules for shell, edit, write, read, search, web and subagents, and no delegation. No owner plugins. The daemon checks the effective tool set at start and fails closed.
6. **CTO messages.** The CTO sends from its own session. A plain send or reply is sent at once with a peer note. A send as the owner is a proposal; after approval the daemon sends the exact text with the CTO note "from the CTO, on the owner's instructions". Only the proposal runner sets the CTO flag, and the gateway accepts it only from the recorded CTO session.
7. **Progressive disclosure.** The CTO tool set becomes 26 tools: an overview, one-line paged lists, details by ID, and one change tool per area with an action field. Long guidance moves to detail views.

## Consequences

- This supersedes 0022's isolation sentence. The rest of 0022 stands: the CTO is still the way for the owner to change the graph by talking, now through proposals.
- 0029 stands: the timeline is the CTO session's text. 0034 stands: the box is read-only and Enter opens the CTO, now through `session_enter`.
- Removed from today's private CTO: running owner-only changes directly, owner-only reads, all built-in tools and owner plugins, sends as the owner session, the reply forward (after 30 days), `cto_send`, `cto_events`, `cto_enter` and the CTO's `session_enter` and `cto_graph_history` tools. `cto_new` and `cto_settings` stay as owner methods.
- Every change costs the owner one key, even when the owner asked for it in the CTO session.
- The private CTO can adopt rules 2 and 3 before it becomes messageable. It must not become messageable before they ship.
- Residual risks: the CTO can repeat what the owner typed or handed to it; the owner can approve a proposal without reading it; the trust boundary stays the owner's operating-system user.
- The owner's-words grant of the authority draft is a separate, later decision.
