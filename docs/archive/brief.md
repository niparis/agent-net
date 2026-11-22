# Agent Network Gateway
## Technical Architecture & Implementation Plan

**Version:** 1.0 - Draft
**Date:** October 28, 2026
**Deployment:** Self-hosted, private home LAN
**Status:** Proposed architecture; documented integrations researched, end-to-end compatibility testing pending

> Original brief as written by the project owner, persisted verbatim on 2026-10-28.
> The only edit: em dashes replaced with plain dashes. For the review of this brief, see [review.md](review.md).

---

# 1. Problem Statement

## 1.1 Current situation

Multiple AI coding agents operate independently across several virtual machines on a home LAN.

The environment includes:

- Claude Code
- OpenAI Codex
- Pi Coding Agent
- OpenCode v2
- Grok Build / Grokbot

There may be several instances of the same harness running simultaneously, often working on different repositories or projects.

Although these agents are individually capable, they are isolated from each other.

**Agent isolation**

An agent typically does not know which other agents are running, what they are working on, what capabilities they have, or how to contact them.

Collaboration requires manual intervention: inspecting terminals, copying messages between sessions, or explicitly orchestrating multiple agents from outside their native environments.

**Fragmented MCP configuration**

Each harness maintains its own MCP server configuration. External tools must be configured and maintained across different VMs and agent runtimes.

There is no single place to discover, manage, share, or govern access to MCP services.

**Fragmented skills**

Agent skills are distributed across different local directories and repositories.

A skill created or installed on one VM is not automatically available to agents on another machine. Maintaining common skills requires manual copying, separate installations, or custom Git synchronization.

**Missing coordination infrastructure**

There is no common service for agent discovery, asynchronous messaging, message history, delivery acknowledgments, or shared capability discovery.

## 1.2 Desired state

Create a lightweight, self-hosted **Agent Network Gateway** that connects existing independent coding agents into a discoverable, collaborative network.

Agents should be able to:

1. Join the network automatically when their sessions start.
2. Announce their own names, functions, current projects, and capabilities.
3. Discover other running agents and understand their advertised roles.
4. Send messages to peers, either at the user's request or autonomously.
5. Receive messages within their existing sessions, without requiring a new conversation.
6. Discover and invoke shared MCP tools.
7. Search, install, update, and publish shared skills.
8. Continue operating within their native harnesses, permissions, context windows, and tool environments.

**Core principle: The network connects agents. It does not replace or centrally orchestrate them.**

---

# 2. Design Principles

### 2.1 Harness independence

Claude Code, Codex, Pi, OpenCode, and Grok retain their native execution environments.

The gateway must not depend on any one LLM provider, SDK, or agent framework.

Harness-specific behavior belongs in replaceable adapters.

### 2.2 Self-registration, not predefined agent manifests

Agents are not assigned centrally maintained names, roles, or capability manifests.

A running agent introduces itself and can update that description as its work changes.

The gateway stores the resulting information but does not dictate the agent's function.

Repository-level instructions such as `AGENTS.md` or `CLAUDE.md` may explain how agents should participate in the network.

These instructions are optional behavioral guidance, not required registration records.

### 2.3 MCP-first interoperability

MCP is the primary interface through which agents interact with the gateway.

The gateway acts as:

- An **MCP server** exposing agent-network and skills operations.
- An **MCP client** connecting to upstream MCP servers.
- A **tool registry and proxy** for shared MCP capabilities.

MCP is not assumed to provide all agent-session lifecycle functionality.

### 2.4 Native session continuity

When agent A sends a message to agent B, the system should deliver it to agent B's existing running session.

It must avoid silently starting a separate session or losing conversation context.

If native delivery is unavailable, the system should report that limitation and retain the message for later retrieval.

### 2.5 Autonomous, but controlled

Agents may independently decide to discover peers and communicate.

However, autonomous messaging must respect identity, authorization, rate limits, and existing harness permissions.

Being contacted by another agent must not grant additional privileges.

### 2.6 Local-first deployment

The system should operate entirely on the home LAN for local agents.

Cloud services are not required for the core infrastructure.

External cloud-based agents may be connected through separately controlled integrations.

### 2.7 Progressive complexity

The initial system should be small enough to deploy and maintain personally.

Avoid introducing Kubernetes, a distributed database, Temporal, or a heavyweight message broker before there is a demonstrated need.

---

# 3. Scope

## 3.1 In scope

| Domain | Required capabilities |
|---|---|
| Agent network | Registration, identity, presence, discovery |
| Messaging | Direct messages, replies, persistent inboxes |
| Session integration | Live-session delivery, busy handling, wake-up |
| MCP federation | Server registry, tool search, tool execution |
| Skills | Search, publish, version, install, synchronize |
| Security | Authentication, authorization, isolation |
| Operations | Logs, health, diagnostics, administration |

## 3.2 Explicitly out of scope for the first release

- Building a replacement coding-agent harness.
- Centralized planning or decomposition of all agent tasks.
- Unified memory or shared conversation context across harnesses.
- Centralized LLM model routing.
- Automatically distributing repository source code between VMs.
- A general remote shell or arbitrary command-execution service.
- Internet-facing public agent discovery.
- Full A2A task interoperability.
- Highly available, multi-node gateway infrastructure.

These capabilities may become future extensions, but are not prerequisites.

---

# 4. High-Level Architecture

The system contains a central gateway and lightweight adapters on each VM.

**Logical architecture**

    Agent sessions across VMs
    Claude / Codex / Pi / OpenCode / Grok
                    |
             MCP clients
                    |
         Local session adapters
                    |
            Secure LAN link
                    |
    +------------------------------------+
    |        Agent Network Gateway       |
    |                                    |
    |  1. Agent Registry                 |
    |  2. Messaging & Inbox              |
    |  3. MCP Registry & Proxy           |
    |  4. Skills Registry                |
    |  5. Identity & Policy              |
    |  6. Events & Observability         |
    +------------------------------------+
          |                     |
      MCP Servers          Skills Storage
      GitHub, tools        Git + artifacts
      Custom services     Versioned bundles

An agent connects to one gateway MCP endpoint for network operations.

Native harness adapters deliver inbound events into the correct local sessions.

The skills subsystem has a separate distribution interface because local skill files and supporting assets cannot always be represented adequately by MCP tool responses alone.

## 4.1 Main components

### A. Agent Registry

Maintains the currently known agent sessions and their self-declared identities.

Responsibilities:

- Agent session registration.
- Agent names and descriptions.
- Capabilities, projects, and current tasks.
- Presence tracking.
- Search and filtering.
- Session lookup for messaging.

### B. Message Service

Provides asynchronous communication between registered agents.

Responsibilities:

- Message persistence.
- Routing by agent/session ID.
- Reply correlation.
- Delivery tracking.
- Offline inboxes.
- Retry control and duplicate prevention.

### C. MCP Federation

Provides one entry point to shared MCP servers.

Responsibilities:

- Upstream MCP server registration.
- Tool discovery and schema indexing.
- Tool search.
- Authenticated invocation.
- Credential isolation.
- Per-agent tool visibility.

### D. Skills Registry

Provides a centrally managed library of Agent Skills.

Responsibilities:

- Catalog search.
- Version tracking.
- Bundle distribution.
- Publishing and review.
- Global and project-specific installation.
- Local cache synchronization.
- Rollback.

### E. Local Session Adapter

Runs on each VM or within the harness.

Responsibilities:

- Detecting session lifecycle events.
- Registering physical session presence.
- Sending heartbeats.
- Receiving gateway messages.
- Delivering messages into the appropriate harness session.
- Managing local skill synchronization.
- Reporting delivery and execution status.

### F. Identity, Policy & Operations

Shared infrastructure providing authentication, access control, audit logging, health checks, and operational visibility.

---

# 5. Agent Identity and Self-Registration

## 5.1 Two-stage registration

**Stage 1 - automatic technical registration**

When a supported agent session starts, its adapter registers the session with the gateway.

This registration does not require an LLM call.

The adapter provides:

- Unique session identifier.
- Harness type and version.
- VM/node identifier.
- Native session reference.
- Available inbound delivery mechanism.
- Session lifecycle state.
- Last heartbeat timestamp.

The gateway generates or validates a network-level session identity.

**Stage 2 - autonomous semantic announcement**

When the LLM becomes active, it calls `agent_announce` through MCP.

It supplies:

- A descriptive name.
- Its current function.
- Its project or repository.
- Capabilities it believes it can provide.
- Optional current task description.
- Optional availability preference.

Example announcement:

    agent_id: generated by gateway
    name: Housing Backend
    function: Implementing backend services
    harness: codex
    project: housing
    capabilities:
      - python
      - fastapi
      - postgresql
    status: working

The agent does not invent its trusted session ID. That comes from the authenticated adapter registration.

## 5.2 Identity model

Use three separate identifiers:

| Identifier | Meaning |
|---|---|
| `node_id` | Stable identity of a VM |
| `session_id` | Unique running harness session |
| `agent_name` | Human-readable, self-declared name |

`session_id` is the canonical routing identity.

Agent names may change and need not be globally unique.

An agent may optionally adopt a stable alias across session restarts, but a new session must still receive a new session ID.

## 5.3 Presence and liveness

Presence is owned by the adapter, not the LLM.

Suggested states:

- **Online / idle:** Session can accept a new message.
- **Online / busy:** Session is actively processing work.
- **Unavailable:** Session exists but cannot currently receive messages.
- **Offline:** No valid heartbeat or confirmed session termination.

A separate self-declared activity field can indicate that the agent is researching, reviewing, implementing, or waiting.

These two dimensions should not be conflated.

Heartbeat expiry marks a session offline. An explicit shutdown event can do so immediately.

## 5.4 Registration lifecycle

1. Harness starts.
2. Local adapter detects the session.
3. Adapter registers the technical session identity.
4. Gateway advertises an online, initially undescribed agent.
5. LLM announces its name, capabilities, and purpose.
6. Gateway updates its searchable directory.
7. Adapter maintains heartbeats.
8. LLM updates its description when useful.
9. Shutdown or lease expiry removes the session from the live directory.

The historical identity and message records may be retained after a session goes offline.

---

# 6. Agent Discovery

## 6.1 Discovery interface

The primary MCP tools are:

| Tool | Behavior |
|---|---|
| `agent_list` | List reachable agents |
| `agent_get` | Retrieve one agent's full details |
| `agent_announce` | Register/update self-description |

`agent_list` should support filters for:

- Harness.
- Capability.
- Project.
- Current availability.
- Node.
- Free-text function description.

The response should distinguish verified technical attributes from self-declared capabilities.

## 6.2 Expected behavior

Claude Code working on an infrastructure project might call:

`agent_list(capability="terraform", online_only=true)`

The gateway returns matching live sessions, their current descriptions, and their availability.

Claude can then choose a recipient.

Agent discovery must remain useful even when agents are busy.

## 6.3 Future discovery improvements

Potential later features:

- Semantic capability matching.
- Agent-reported specialization history.
- Recent successful collaboration signals.
- User-defined preferred agents.
- Discovery of agents by repository or Git worktree.

These are optional enhancements rather than requirements for the first release.

---

# 7. Agent Messaging

## 7.1 Core requirements

Messages must support:

- Agent-to-agent communication.
- User-requested communication.
- Autonomous agent communication.
- Existing-session delivery.
- Asynchronous replies.
- Offline persistence.
- Busy-recipient queuing.
- Delivery acknowledgments.
- Conversation and request correlation.
- Message inspection and audit.

## 7.2 MCP interface

| Tool | Responsibility |
|---|---|
| `agent_send` | Send a new message |
| `agent_messages` | Query inbox and conversation history |
| `agent_message_status` | Inspect delivery/execution status |
| `agent_reply` | Reply to an existing conversation |

An explicit `agent_reply` is convenient, though it could internally use `agent_send` with a conversation identifier.

A send should normally return immediately with a message ID.

**A message being accepted by the gateway does not imply that the recipient LLM has read, understood, or completed it.**

## 7.3 Message envelope

Each message should carry:

- `message_id`
- `conversation_id`
- `sender_session_id`
- `recipient_session_id`
- `parent_message_id`, if replying
- `message_type`
- `body`
- `created_at`
- `expires_at`
- `priority`
- `idempotency_key`
- Delivery and processing state

Suggested message types:

- `request`
- `reply`
- `notification`
- `status`
- `error`

Text is sufficient initially. Larger artifacts should be referenced by a separately stored attachment ID or approved URL, rather than embedded into messages.

## 7.4 Delivery states

Use explicit states:

1. **Stored:** Gateway has durably recorded the message.
2. **Dispatched:** Sent to the recipient's adapter.
3. **Accepted:** Adapter has accepted responsibility for delivery.
4. **Delivered:** Entered the intended harness session or its native queue.
5. **Processing:** Recipient explicitly acknowledged that work has started.
6. **Replied:** A correlated response was received.
7. **Failed / expired:** Delivery or processing could not proceed.

Not every harness exposes sufficient signals for every state. The gateway must report the strongest status it can actually verify.

## 7.5 Busy-agent behavior

Default delivery policy:

- Idle recipient: deliver and trigger processing when supported.
- Busy recipient: append as a follow-up after the current turn.
- Unavailable recipient: store for later delivery or retrieval.
- Offline recipient: retain only if the message's delivery policy permits it.

Do not interrupt or steer an active turn by default.

## 7.6 Reliability

The gateway should provide durable storage, deduplication, and controlled retries.

Use at-least-once delivery semantics with idempotency safeguards rather than promise universal exactly-once execution.

An uncertain delivery outcome must not cause blind retry of a potentially side-effecting operation.

## 7.7 Autonomous communication

Agents may contact peers without an explicit user command when collaboration is useful.

Examples:

- Requesting a code review.
- Asking another agent about an API change.
- Sharing a breaking change with a dependent agent.
- Requesting a second opinion on an architecture.
- Coordinating work across repositories.

Rate limits and delegation budgets should prevent circular conversations and runaway messaging.

---

# 8. Harness Integration Strategy

The gateway should expose a common contract while allowing each harness to use its best native integration.

The following describes documented technical integration paths, not completed end-to-end tests.

| Harness | MCP connectivity | Preferred inbound integration | Important constraint |
|---|---|---|---|
| Claude Code | Native | Cross-session delivery, OCS or MCP Channels | Channels are experimental and session-dependent |
| Codex | Native | App Server API; OCS fallback | Arbitrary CLI sessions may not be externally controllable |
| Pi | Native | Pi extension | Extension needed for live injection |
| OpenCode v2 | Native | v2 session/plugin interface | Native adapter must be built |
| Grok Build | Native | OCS in supported leader mode | Not all Grok CLI session modes support direct wake-up |
| Official Grok Bot | Connected MCP integration | Webhook/routine bridge | Not equivalent to a local controllable CLI session |

## 8.1 Claude Code

Use native MCP for outbound gateway calls.

Possible inbound transports:

- Native cross-session messaging.
- OCS.
- A custom MCP Channel where supported.

Claude's Channels feature can deliver events into an existing running session, but it remains a research-preview capability.

For the initial implementation, prefer the most reliable supported mechanism demonstrated on the actual VM.

## 8.2 Codex

Use native MCP for outbound gateway calls.

For inbound messages, the preferred controlled integration is Codex App Server.

Relevant operations include:

- `thread/resume`
- `turn/start`
- `turn/steer`

This supports addressing an existing Codex conversation when the gateway adapter controls or can access the relevant App Server lifecycle.

However, arbitrary independently running Codex CLI sessions are not guaranteed to accept external injection.

OCS may provide an alternative for supported Desktop or terminal configurations.

**Acceptance testing must explicitly distinguish Codex App Server, Desktop, and ordinary CLI sessions.**

## 8.3 Pi Coding Agent

Use Pi's native MCP client.

Implement a lightweight Pi extension for inbound communication.

Pi exposes message-injection APIs with idle triggering and busy-turn delivery modes.

This is one of the most direct integration paths.

## 8.4 OpenCode v2

Use its native MCP client for outbound operations.

Implement an OpenCode v2 plugin/adapter that:

- Detects session lifecycle.
- Registers sessions.
- Receives incoming network messages.
- Uses native session prompting.
- Preserves existing session context.
- Queues incoming messages appropriately.
- Reports delivery state.

OpenCode's native v2 session APIs provide the necessary foundation.

## 8.5 Grok Build

For Grok Build CLI, evaluate OCS's supported `grok --leader` integration.

Verify that it:

- Registers a live session.
- Receives messages after initialization.
- Preserves the existing session.
- Safely queues messages when busy.

Do not assume the same behavior for ordinary Grok web, mobile, or standalone CLI sessions.

## 8.6 Grok Bot

If this refers to the official persistent cloud-based Grok Bot product, treat it separately.

A webhook-triggered routine can accept externally initiated work. An MCP bridge may allow the Bot to access network capabilities if the gateway is securely reachable.

However, full arbitrary messaging into a specific existing conversation is not established by the documented routine interface.

Cloud-based access also requires a deliberate network boundary rather than direct dependence on a private LAN address.

If Grokbot is instead a custom process running locally with the Grok API, build a normal local adapter.

---

# 9. MCP Gateway and Tool Federation

## 9.1 Objective

All participating agents should connect to one primary MCP gateway rather than maintain independent copies of shared MCP server configurations.

Example endpoint:

`https://agent-gateway.lan/mcp`

Agents should still be free to maintain harness-specific local tools where needed.

## 9.2 Gateway responsibilities

The gateway acts as an MCP server toward agents and an MCP client toward upstream servers.

It maintains a tool registry containing:

- Source server identity.
- Tool name.
- Description.
- Input schema.
- Availability.
- Access permissions.
- Credential requirements.

## 9.3 Tool discovery

Expose two main operations:

| Tool | Purpose |
|---|---|
| `tools_search` | Search the federated tool catalog |
| `tools_call` | Invoke an authorized upstream tool |

The gateway may additionally expose selected, stable upstream tools as native MCP tools.

Avoid publishing hundreds of rarely used tools into every agent's prompt.

## 9.4 MCP server onboarding

Upstream MCP servers may be:

- Registered by an administrator.
- Discovered from configured local endpoints.
- Proposed by authenticated agents.
- Connected automatically when trusted policy permits it.

Unlike agent registration, adding MCP servers may introduce executable capabilities, external data access, and credentials.

Therefore, **MCP server discovery should not automatically grant tool access**.

## 9.5 Security boundaries

Enforce permissions on both:

- Tool discovery.
- Tool invocation.

Use the authenticated requesting session as the authorization principal.

Self-declared roles or capabilities must not be used as proof of authority.

Where possible, preserve per-user or per-agent credentials rather than routing everything through one administrator-level account.

## 9.6 MCP protocol compatibility

The gateway should negotiate supported MCP versions and be tested with every target client.

The July 2026 MCP specification differs materially from older session-oriented transport behavior.

Do not assume that ordinary MCP tools provide an unsolicited persistent message channel into a sleeping agent.

The messaging subsystem and session adapters remain separate, even though agents control them through MCP tools.

---

# 10. Centralized Skills Registry

## 10.1 Objective

Eliminate manual skill copying and maintain one shared source of truth across machines and compatible harnesses.

A skill may contain:

- `SKILL.md`
- Scripts
- References
- Templates
- Supporting assets

The baseline packaging format should follow the open Agent Skills specification.

## 10.2 Distribution architecture

Use a central, versioned skills repository with two access paths.

**Path A - MCP**

Agents search the catalog, inspect skill metadata, and request installations or publications.

**Path B - Local distribution**

A VM-level synchronization service places approved skill bundles into a managed local cache.

Native harness skill loaders discover and load the synchronized files.

OpenCode v2 additionally supports native HTTP skills catalogs, so it can consume a compatible gateway-hosted catalog directly without a separate custom download mechanism.

## 10.3 Proposed MCP operations

| Tool | Purpose |
|---|---|
| `skills_search` | Search available skills |
| `skills_get` | Inspect metadata and versions |
| `skills_install` | Request installation on the calling VM |
| `skills_list_installed` | List locally available skills |
| `skills_publish` | Submit a new or updated skill |
| `skills_status` | Inspect distribution and activation status |

Publishing must distinguish an agent's proposed content from an approved and distributable version.

## 10.4 Storage model

Use a Git-backed canonical store.

Logical structure:

    skills/
      global/
        code-review/
          SKILL.md
          references/
          scripts/
      projects/
        housing/
          housing-diagnostics/
            SKILL.md
      releases/
      catalog/

The gateway can index the contents and distribute immutable, versioned bundles.

The Git structure is illustrative; the implementation may keep versions in Git and distribute generated archives or directory trees.

## 10.5 Global versus project skills

**Global skills**

Available to all authorized local agents, regardless of repository.

**Project skills**

Available only within a particular project or installed into that repository's native skill location.

Project-local definitions should retain the harness's expected precedence where possible.

## 10.6 Skill lifecycle

1. An agent creates or modifies a skill.
2. It submits a publication request.
3. The registry validates the package structure.
4. Executable contents and sensitive instructions undergo review.
5. The approved version is committed and published.
6. Relevant VMs receive the new version.
7. The harness discovers the skill natively.
8. Previous versions remain available for rollback.

Initially, publication can require human approval. Trusted automated publication policies can be added later.

## 10.7 Synchronization strategy

A VM agent should:

- Poll or subscribe for catalog changes.
- Download approved versions.
- Verify checksums.
- Install into a managed cache.
- Maintain native harness directory mappings.
- Report installation status.
- Roll back failed updates.

Use atomic version switches so a harness never reads a partially downloaded skill.

Installation and actual runtime activation are different states. Some harnesses need a reload or new model step before updated skills become visible.

## 10.8 Skill compatibility

Do not assume a skill is executable everywhere simply because its Markdown format is compatible.

Supporting scripts may depend on:

- Operating system.
- Shell.
- Python/Node versions.
- Native binaries.
- Project files.
- Network access.
- Harness-specific tools.

The registry should retain compatibility requirements and report incompatibilities rather than silently install unusable skills.

---

# 11. Local VM Adapter Design

A small process, provisionally called `agent-netd`, should run on each VM.

It is the bridge between the centralized gateway and local harness sessions.

## 11.1 Core responsibilities

- Discover managed harness sessions.
- Maintain session-to-adapter mappings.
- Send heartbeats.
- Receive incoming messages.
- Dispatch messages through native harness interfaces.
- Report delivery outcomes.
- Synchronize skills.
- Provide local diagnostics.

## 11.2 Adapter interfaces

Define a harness adapter contract with operations such as:

- `discover_sessions()`
- `attach(session_id)`
- `deliver(session_id, message)`
- `get_state(session_id)`
- `detach(session_id)`

Each implementation translates these operations into harness-native APIs.

The gateway itself should not contain Claude-specific or Codex-specific session logic.

## 11.3 Network transport

Prefer a single authenticated outbound connection from each VM adapter to the gateway for receiving events and issuing acknowledgments.

A persistent WebSocket or reconnecting event stream can provide this control plane.

This avoids exposing separate inbound HTTP ports for every harness process.

The adapter can communicate locally with harness plugins through IPC, Unix sockets, named pipes, or another supported local mechanism.

## 11.4 OCS integration

OCS is worth reusing for supported native session wake-up.

However, keep it behind a transport abstraction.

The gateway's messaging protocol should remain authoritative for message IDs, delivery state, authorization, and persistence.

OCS should not become the canonical agent registry or the sole message database.

This allows replacing it later without redesigning the gateway.

---

# 12. Security Model

The system runs on a trusted home network, but agents should not be treated as fully trusted actors.

An agent may access repositories, credentials, local shells, and connected tools.

## 12.1 Node authentication

Every VM adapter receives a unique credential or device identity.

Use mutually authenticated connections or securely provisioned node credentials.

The gateway validates node identity before accepting session registrations.

## 12.2 Session identity

The adapter binds a real local harness session to its gateway identity.

Agents can edit their self-descriptions, but must not be able to impersonate other sessions by submitting arbitrary IDs.

## 12.3 Messaging authorization

Apply policies governing:

- Which nodes can communicate.
- Which sessions are visible.
- Which agents can contact which other agents.
- Whether unsolicited messages are accepted.
- Rate limits and message sizes.

Default policies may be permissive within the trusted LAN, but must remain explicit and configurable.

## 12.4 Tool authorization

Tool permissions are separate from messaging permissions.

An agent must never acquire additional tool access solely by declaring a new name or role.

The gateway enforces authorization independently of the model.

## 12.5 Skill security

Treat downloaded skills as potentially executable software.

Use:

- Approved publishers.
- Version pinning.
- Checksums.
- Review of executable scripts.
- Basic dependency checks.
- Rollback support.

Do not automatically promote all agent-created skills into the global library.

## 12.6 Prompt-injection boundaries

Messages from peers and skill content must not automatically become higher-priority instructions than the user's original task or the harness's security policy.

Peer communications should retain provenance and be treated as untrusted input unless explicitly authorized.

## 12.7 Network exposure

Bind services to the private network and use firewall restrictions.

For remote access, prefer WireGuard or Tailscale instead of opening the gateway directly to the internet.

Cloud Grok Bot integration should use a separately authenticated public-facing bridge if required.

---

# 13. Data Model

The initial system can use SQLite.

Suggested entities:

| Entity | Important fields |
|---|---|
| `nodes` | ID, hostname, OS, auth identity, last_seen |
| `sessions` | ID, node, harness, native_ref, state, lease |
| `agent_profiles` | Session, name, function, project, capabilities |
| `conversations` | ID, participants, created_at |
| `messages` | ID, conversation, sender, recipient, body |
| `deliveries` | Message, state, attempts, timestamps, receipt |
| `mcp_servers` | ID, endpoint, auth reference, status |
| `mcp_tools` | Server, tool name, schema, permissions |
| `skills` | ID, name, description, scope |
| `skill_versions` | Skill ID, version, digest, source |
| `skill_installs` | Version, VM, path, activation state |
| `audit_events` | Actor, action, target, outcome, timestamp |

Use immutable event records for meaningful lifecycle transitions.

Database migrations should be versioned from the beginning.

---

# 14. Technology Stack

## 14.1 Recommended initial implementation

| Component | Technology |
|---|---|
| Gateway application | Python |
| HTTP service | FastAPI |
| MCP server | Official Python MCP SDK |
| MCP upstream clients | Python MCP SDK |
| Database | SQLite, WAL mode |
| Persistence | Local disk with backups |
| Skills source | Git |
| Skills distribution | HTTPS catalog + bundles |
| Local adapter daemon | Python or Go |
| Harness plugins | Native SDK/language |
| Event transport | WebSocket |
| Metrics | Prometheus-compatible |
| Logging | Structured JSON |
| Deployment | Docker Compose or systemd |

For simplicity, I'd implement the gateway and VM daemon in Python initially, while keeping the adapters that require native extension APIs in their corresponding language.

## 14.2 Initial deployment

One small, always-on gateway host is sufficient.

Suggested components:

- Gateway service
- SQLite database
- Git-backed skill storage
- Reverse proxy with TLS
- Optional lightweight administration UI

Each participating VM runs one `agent-netd` instance plus the necessary harness-specific integration.

No distributed infrastructure is required at this stage.

## 14.3 Future scalability options

If SQLite or the simple messaging implementation becomes insufficient:

- PostgreSQL for concurrent storage.
- NATS JetStream for durable event distribution.
- A2A adapters for structured task exchange.
- Multiple gateway instances for availability.
- External observability infrastructure.

These are evolution paths, not first-release requirements.

---

# 15. User Experience

The user should not need to manage registrations manually.

## 15.1 Normal agent startup

An agent starts in an existing terminal.

The adapter detects it, registers it, and maintains its presence.

The LLM announces its role when it becomes active.

No separate administrative action is required.

## 15.2 Agent collaboration

A user tells Claude:

"Ask the Codex agent working on the backend to review our API design."

Claude:

1. Searches the directory.
2. Finds the correct session.
3. Sends a review request.
4. Receives a message ID.
5. Continues other work or checks status.
6. Receives the eventual reply in the existing session.

## 15.3 Autonomous collaboration

While working, Claude discovers that another agent is handling a related component.

It independently sends a targeted question or notification.

The recipient replies, and the exchange remains correlated in the message history.

## 15.4 Skill reuse

An agent encounters a task for which it lacks a relevant skill.

It searches the shared catalog, selects a skill, requests installation, and uses the native skill mechanism after activation.

The same skill becomes available on other compatible VMs according to distribution policy.

## 15.5 Administration

A simple operational interface should eventually show:

- Connected VMs.
- Running agents and current functions.
- Active conversations.
- Pending and failed messages.
- Federated MCP servers.
- Skills and installed versions.
- Authentication and delivery errors.

A full web dashboard is not necessary for the first implementation. CLI diagnostics are acceptable initially.

---

# 16. Implementation Plan

Build vertical slices that prove complete user behavior, rather than implementing all backend components first.

## Phase 0 - Compatibility spike

**Objective:** Confirm the practical delivery mechanisms before committing to a transport architecture.

Tasks:

- Test native MCP connectivity from each harness.
- Confirm technical session identification.
- Verify native message injection into an idle running session.
- Verify behavior when the recipient is busy.
- Verify session continuity.
- Evaluate OCS across two VMs.
- Verify OpenCode v2 plugin delivery.
- Identify the exact Grokbot product and integration surface.

**Exit criterion:** A documented adapter strategy for each harness, with unsupported configurations explicitly identified.

This is a prerequisite, not an optional exploration.

## Phase 1 - Minimum agent network

**Objective:** Claude can discover and message at least one other live agent.

Build:

- Gateway service.
- SQLite persistence.
- Node authentication.
- Automatic technical registration.
- Agent self-announcement.
- `agent_list` and `agent_get`.
- `agent_send` and `agent_messages`.
- Two working harness adapters.

Recommended first pair: Claude Code and Pi, because Pi exposes a direct message-injection interface.

**Exit criterion:** Two agents on separate VMs discover each other, communicate, and reply in their existing sessions.

## Phase 2 - Reliable messaging

**Objective:** The network remains dependable under normal session churn.

Build:

- Persistent inboxes.
- Delivery receipts.
- Busy-recipient queues.
- Idempotent message handling.
- Offline state and expiry.
- Connection recovery.
- Rate limits.
- Basic message inspection.

**Exit criterion:** Messages are neither silently lost nor blindly duplicated during disconnects, busy turns, or restarts.

## Phase 3 - Full harness coverage

**Objective:** Support all locally controllable harnesses.

Build or complete:

- Codex adapter.
- OpenCode v2 plugin.
- Grok Build integration.
- Claude inbound channel alternative if appropriate.
- Grok Bot bridge where supported.

**Exit criterion:** Each supported harness can register, discover, send, and receive using its documented compatible session mode.

Unsupported modes remain visible as explicit limitations.

## Phase 4 - MCP federation

**Objective:** Consolidate shared tools.

Build:

- Upstream MCP server registration.
- Tool schema discovery.
- Search and invocation.
- Per-agent permissions.
- Credential handling.
- Failure isolation.

**Exit criterion:** At least two different harnesses invoke the same authorized upstream MCP service through the gateway.

## Phase 5 - Skills Registry

**Objective:** Eliminate manual skill copying.

Build:

- Git-backed catalog.
- Skill search and metadata.
- Versioned bundles.
- Publication approval workflow.
- Per-VM synchronization.
- Native skill installation mappings.
- OpenCode v2 HTTP catalog support.
- Installation and activation status.
- Rollback.

**Exit criterion:** Publish a skill on VM A; an authorized agent on VM B discovers, installs, and uses it without manually transferring files.

## Phase 6 - Operational hardening

**Objective:** Make the system reliable for everyday use.

Build:

- Health diagnostics.
- Structured logs.
- Operational metrics.
- Backup/restore.
- Versioned configuration.
- Upgrade process.
- Optional lightweight dashboard.
- Integration test suite.

**Exit criterion:** Normal network interruptions and service restarts recover predictably without manual database repair or lost accepted messages.

---

# 17. Acceptance Test Matrix

The following test categories apply to every supported local harness.

| Test | Expected result |
|---|---|
| Session starts | Technical registration appears automatically |
| Agent first runs | Self-description appears |
| Agent changes task | Description can be updated |
| Agent discovers peer | Correct profile and status returned |
| User requests message | Peer receives it |
| Agent autonomously sends | Peer receives it within policy |
| Recipient idle | Message triggers processing when supported |
| Recipient busy | Message queues without corrupting current turn |
| Recipient offline | Message persists or expires according to policy |
| Recipient replies | Response reaches correct originating conversation |
| Two sessions share a VM | Identities and inboxes remain separate |
| Adapter disconnects | Status eventually changes appropriately |
| Gateway restarts | Durable state is recovered |
| Untrusted peer requests tool | Gateway enforces authorization |
| Skill published | New version appears in catalog |
| Skill distributed | Correct files appear on target VM |
| Skill updated | Compatible harness loads approved version |
| Skill rolled back | Previous version becomes available |
| MCP tool invoked | Correct upstream tool executes under permitted identity |

Additional harness-specific tests are mandatory for session wake-up and busy-turn behavior.

A passing generic messaging test is not sufficient proof that each native session integration works.

---

# 18. Observability and Quality Requirements

Suggested initial operational targets, to be validated through testing:

| Measure | Initial target |
|---|---|
| Agent discovery after registration | Under 5 seconds |
| Detect disconnected adapter | Under 30 seconds |
| Gateway message persistence | Before send acknowledgment |
| Delivery on healthy LAN | Under 2 seconds to adapter |
| Duplicate execution | Prevented by idempotency and delivery policies |
| Skill integrity | Verified digest on installation |
| Message loss on restart | No loss of durably accepted messages |
| Tool access violations | Rejected and recorded |

The gateway should distinguish transport performance from LLM response time.

A message may reach an agent immediately while the underlying model takes much longer to respond.

Useful metrics include:

- Connected nodes and sessions.
- Message enqueue-to-delivery latency.
- Recipient processing latency.
- Undelivered messages.
- Delivery failures.
- Tool-call errors.
- Skill synchronization failures.
- Gateway availability.

---

# 19. Main Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Harness APIs change | Versioned adapters and integration tests |
| Codex terminal cannot be externally controlled | App Server mode or restricted fallback |
| Experimental Claude features change | Keep alternative transport adapters |
| Busy agent receives unexpected input | Queue rather than interrupt by default |
| Agents message each other indefinitely | Rate limits, loop detection, conversation budgets |
| An agent impersonates another | Authenticate adapters and bind session identities |
| A skill contains unsafe code | Review, verify and version published artifacts |
| A tool exposes sensitive credentials | Server-side authorization and credential isolation |
| MCP clients support different protocol versions | Explicit compatibility negotiation and testing |
| Grok Bot remains cloud-only | Isolated webhook bridge and limited capability claims |
| Gateway becomes a single point of failure | Durable storage, backups, clear recovery behavior |

---

# 20. Recommended Project Structure

A possible repository layout:

    agent-network/
      gateway/
        api/
        mcp/
        registry/
        messaging/
        federation/
        skills/
        auth/
        storage/
      daemon/
        core/
        transports/
        skills_sync/
      adapters/
        claude/
        codex/
        pi/
        opencode/
        grok/
      skills-repository/
        global/
        projects/
      tests/
        unit/
        integration/
        harness/
        lan/
      deploy/
        docker/
        systemd/
      docs/
        architecture/
        protocols/
        operations/
        decisions/

Keep the shared data contracts independent from adapter implementations.

The central gateway should not import harness-specific runtime code.

---

# 21. Key Architecture Decisions

**Decision 1 - One gateway, multiple capability domains**

The Agent Registry, Messaging Service, MCP Federation, and Skills Registry live behind one logical gateway.

They remain separate internal modules.

**Decision 2 - Agents self-describe**

No centrally maintained agent-role manifest.

Technical session metadata comes from the adapter; semantic identity comes from the agent.

**Decision 3 - Local adapters handle native session delivery**

The gateway does not directly manipulate terminal processes or agent context files.

Each adapter uses supported native harness mechanisms.

**Decision 4 - MCP is the control interface, not the entire delivery transport**

MCP exposes operations to agents.

A separate reliable messaging mechanism handles inbound delivery and wake-up.

**Decision 5 - Skills use native harness loading**

MCP supports discovery and management.

Local distribution or supported HTTP catalogs provide the actual skill files.

**Decision 6 - OCS is optional infrastructure**

Reuse its proven transports where useful, but do not make it the sole source of identity, message history, or policy.

**Decision 7 - Reliable messaging precedes advanced orchestration**

Do not introduce A2A or Temporal until the core agent-to-agent network works reliably.

**Decision 8 - Security is enforced outside the LLM**

Self-described capabilities and incoming peer messages do not create authorization.

---

# 22. Future Extensions

Potential extensions once the initial system is stable:

**Capability-based delegation**

Send a request to a capability rather than a named agent, allowing the gateway to suggest or select a suitable peer.

**Groups and broadcast**

Create project-based discussion channels and filtered broadcasts.

**A2A interoperability**

Expose agent capabilities through A2A-compatible interfaces for external structured task coordination.

**Shared artifacts**

Attach versioned reports, patches, or design documents to agent conversations.

**Task tracking**

Track delegated work across agents, including dependencies, progress, and completion.

**Shared knowledge**

Introduce search over collaboration history, published artifacts, and selected project knowledge.

**Advanced policy**

Add project isolation, trust groups, workload budgets, and fine-grained delegation permissions.

**Dashboard**

Build a live view of connected agents, their activities, conversations, tools, and skills.

These extensions should preserve the decentralized character of the participating agents.

---

# 23. Recommended Starting Point

The highest-value first implementation is:

**Claude Code <-> Gateway <-> Pi, across two VMs**, with automatic registration, discovery, messages, and replies.

Then add OpenCode v2, Codex, and Grok integration.

MCP federation and the Skills Registry can follow once the core session-aware messaging architecture is proven. A basic Git-based skills synchronization process can be deployed earlier to eliminate manual copying immediately.

The first release does not need a sophisticated web UI, distributed infrastructure, or autonomous agent orchestration.

It needs to make five things work reliably:

1. An agent appears automatically when it starts.
2. It can describe itself dynamically.
3. Other agents can find it.
4. They can send messages to its existing session and receive replies.
5. They can share MCP tools and skills without manual replication.

**The intended result is a self-organizing, harness-agnostic network of autonomous coding agents, with a common gateway for communication, tools, and reusable capabilities.**

---

# 24. Reference Documentation

- [Claude Code - Cross-session messaging](https://code.claude.com/docs/en/cross-session-messaging)
- [Claude Code - MCP Channels](https://code.claude.com/docs/en/channels)
- [Codex - App Server](https://learn.chatgpt.com/docs/app-server)
- [Pi - Extensions](https://pi.dev/docs/latest/extensions)
- [Pi - MCP](https://pi.dev/docs/latest/mcp)
- [OpenCode v2 - Server plugins](https://opencode.ai/v2/docs/build/plugins/server/)
- [OpenCode v2 - Skills and HTTP catalogs](https://opencode.ai/v2/docs/skills)
- [Grok Build - Skills](https://docs.x.ai/build/features/skills-plugins-marketplaces)
- [Grok Bot - Skills and routines](https://docs.x.ai/grok-bot/skills-routines-and-automations)
- [Open Cross-session](https://github.com/leeguooooo/open-cross-session)
- [Agent Skills specification](https://agentskills.io/specification)
- [MCP specification](https://modelcontextprotocol.io/specification/2026-07-28)
