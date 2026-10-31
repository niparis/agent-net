# 0035 - The CTO is a standard agent session with owner-only tools

**Date:** 2026-10-31
**Status:** Proposed
**Context:** [0022 component graph and CTO](0022-component-graph-and-cto.md), [0029 one CTO timeline](0029-cto-shared-timeline.md), [0034 the CTO box is read-only](0034-cto-box-is-read-only.md). Design: [docs/design/cto-as-agent.md](../design/cto-as-agent.md).

## Context

Decision 0022 made the CTO a private runtime: "It is not a network peer: no session can message it, so no other agent can talk it into changing rights." That rule has costs. The CTO sends as the owner session, and replies reach it only as a quoted forward. It is not in `agent_list`, the Sessions tab or the Graph. It has its own send, events and enter methods. Its 82 tool schemas cost about 11,930 tokens in every model call, and some list results cost several thousand tokens.

The owner approved the plan: the CTO becomes a normal session with special powers attached.

## Decision

1. **A normal session.** The daemon starts the CTO on the claude or opencode harness, as today. It has one stable agent-net session ID across harness switches, the reserved name `CTO`, and a fixed `cto` node on the Graph. It is listed and entered like any session. Agents can message it, and replies reach it through normal delivery.
2. **Owner-only tools, bound by process.** Only the session the daemon started for the CTO gets the owner-only tool set, checked by its process tree as today. The CTO has no shell, file-write or scheduling tools.
3. **The turn rule.** An owner-only tool works only in a turn that the owner's words started (typed in the CTO session, or a message from the owner's TUI), and only while no input from another source has entered that turn. In any other turn the daemon refuses the call and records it as a proposal in Needs you, which the owner approves or declines with one key. The daemon then runs the stored call itself.
4. **How the daemon knows.** The daemon delivers every agent message itself and delivers one input at a time. It keeps a ledger of inputs and their sources. Claude Code reports each turn start through a daemon hook; OpenCode messages are matched by the IDs the daemon created. At each owner-only call the daemon checks the conversation itself (the Claude transcript or the OpenCode message list). A mixed turn, or any doubt, counts as not the owner's.
5. **Tool classes.** Every tool and action has a fixed class: public read and talk work in any turn; owner read and owner write work only in owner turns. A new send is an owner write; a reply in the conversation that started the turn is talk.
6. **CTO messages.** The CTO sends from its own session. The gateway accepts the CTO flag only from the recorded CTO session, with an owner-turn bit from its daemon. A send in an owner turn carries the CTO note and the owner's words of that turn. A reply in an agent's turn carries a plain peer note.
7. **Progressive disclosure.** The CTO tool set becomes 26 tools: an overview, one-line paged lists, details by ID, and one change tool per area with an action field. Long guidance moves to detail views.

## Consequences

- This supersedes 0022's isolation sentence. The rest of 0022 stands: the CTO is still the only way for the owner to change the graph by talking, and its sends are still the owner's word within each receiver's charter.
- 0029 stands: the timeline is the CTO session's text. 0034 stands: the box is read-only and Enter opens the CTO, now through `session_enter`.
- `cto_send`, `cto_events` and `cto_enter`, the owner-session CTO sends and the reply forward are removed. `cto_new` and `cto_settings` stay.
- The owner sees refusals in agent turns as proposals. Some work that used to need the owner to type now needs one key.
- Residual risks: the CTO can repeat in a reply what it saw in an owner turn; data it reads in an owner turn can still try to steer it, as today; and the trust boundary stays the owner's operating-system user.
