# 0022 - A graph of components with charters, and a CTO in the TUI

**Date:** 2026-10-30
**Status:** Draft. Not built. For the owner to correct before work starts.
**Context:** Today every session is an island with a few separate switches: peer trust (0013), the delegation policy (0014), the worker sandbox level (0020), and merge authority (discussed, not built). Nothing says what a repository is for, what it should refuse, how careful its agents must be, or which other repositories and agents have a say over it. The owner wants agents to react to requests from other agents the way a team would: a generic API should be able to refuse a client-specific feature and give the reason; some services matter more than others; a session that owns no code can manage the architecture of a few repositories. The owner wants to set all of this by talking, not by clicking, and to see it.

Base: [0008](0008-operator-interface-and-worker-build.md) (the owner gate and operator interface), [0013](0013-peer-trust-and-hints.md), [0014](0014-delegation-policy.md), [0016](0016-needs-you.md), [0019](0019-opencode-worker-logins.md), [0020](0020-worker-sandbox.md).

## The model in one paragraph

agent-net keeps a **graph of components**. A node is a repository or service (a **component**), or an **architect** that owns no code. Each node has a **charter**: a fixed set of attributes with free-text values, including a **maturity** level. **Edges** link nodes: one component depends on another, and an architect governs some components, each edge with its own **mode** (advisor, arbiter or owner). Sessions come and go; a session **speaks for** the node of the project it works in. The owner is above every node and edge. The owner changes the graph only by talking to a **CTO**: an agent that agent-net runs inside the TUI, with a map view beside the chat.

## Nodes

- **Component.** One per project (a repository or a service). It is the existing project of the project registry, on any machine. The node is the same on every machine: `cogforge` on the Mac and on the VM is one node.
- **Architect.** A node with no code of its own, for example "WBG architect" or "trading architect". A session becomes the architect's voice when the owner starts it as one (an agent-net session with a role, see "Sessions and nodes"). There can be several architects, each governing a few nodes.

## The charter

A charter has fixed attribute names, so the TUI can lay it out and agents can read it, and free-text values, so the owner writes plain language.

| Attribute | What it says | Example |
|---|---|---|
| `purpose` | What the node is for | Generic document and evidence API for any client. |
| `maturity` | A level and a sentence (see below) | `early` - prototype, speed over safety; tests optional. |
| `accepts` | Requests it takes on | Generic endpoints, performance work, bug fixes from any consumer. |
| `refuses` | Requests it turns down, and why | Client-specific endpoints or business logic for one consumer. |
| `consumers` | Who depends on it (also an edge; this is the human note) | tenebrae, wbg-housing |
| `escalate` | When to ask the owner or its architect | Breaking API changes: ask Nicolas. |
| `needs` | Skills and MCP servers a session needs to be effective here | MCP: tenebrae-library; skill: api-design |
| `notes` | Anything else the owner wants every session of the node to know | Deploys go through homelab, never by hand. |

An architect's charter uses the same attributes: its `purpose` is the domain it looks after, and its `accepts`/`refuses` say what it decides and what it leaves to the components.

Values are bounded (2 KiB per attribute), cleaned (no control characters) and versioned: every change keeps the old value, who changed it and when.

### Maturity

Maturity is a level plus free text. The level sets **defaults** for controls that exist today as separate switches. Each default can still be overridden on the node.

| | `early` | `growing` | `mature` | `critical` |
|---|---|---|---|---|
| Tests before merge | optional | run the suite | suite plus tests for new behavior | suite, new tests and a review |
| Merge authority | full: merge and push | full | delegated: the owner or a delegate approves | ask the owner |
| Worker sandbox (0020) | sandboxed | sandboxed | sandboxed | no-network |
| Requests from consumers | accept freely | judge by the charter | judge strictly | escalate changes to the owner |
| Deploys | as the brief says | as the brief says | ask before deploying | ask the owner |

The free text goes to the agents as written, so they know the intent ("speed over safety"); the level drives the settings.

## Edges

| Edge | From | To | Meaning |
|---|---|---|---|
| depends on | consumer | provider | The consumer may send requests; the provider judges them against its charter. |
| governs | architect | component | The architect has a say, by mode: |
| | | | `advisor`: its opinion goes with the request; the component decides. |
| | | | `arbiter`: it settles a dispute between nodes it governs (a contested refusal). |
| | | | `owner`: its decision stands; the component follows it. |
| owns | session | its workers | Exists today (0002). Not stored in the graph. |

An edge may also carry a **request rule**, which replaces the per-session trust toggle (0013) with a per-pair setting:

- `normal` (default): requests are delivered; the receiver judges by its charter and asks the owner when its charter says so.
- `auto`: requests on this edge are acted on as if the owner had asked (the trust note of 0013, with the same limits).
- `ask`: requests on this edge wait for the owner (Needs you) before delivery.

The owner is above all edges. No edge mode overrides the owner.

## Sessions and nodes

- A session speaks for the node of its project: the main repository of its folder, as the PROJECT column already resolves it.
- Several sessions may speak for one node at once (a Claude session on the Mac, an OpenCode session on the VM). They share the charter.
- An architect node is voiced by a session the owner starts with a role: `agent_start` (or the CTO) with `role: architect:<name>`, or an existing session the owner assigns to it. Its folder can be anywhere (for example a notes repository).
- Workers belong to the node of their project. A worker's brief carries the node's charter, maturity and rules.

## Where it lives and how agents get it

- **In the gateway**, in three tables: nodes, charter attributes (with history), edges. One copy for the network. Not in AGENTS.md: a file in a branch differs between checkouts, any agent can edit it, and a session sees it only if it reads it.
- **Given to the node's sessions on every turn**, through the same paths as the delegation policy: the Claude Code prompt hook, the OpenCode context hook, the Pi `before_agent_start` hook, and for Codex the turn context. The text is short: maturity, accepts, refuses, escalate, and the edges that matter to this node.
- **Shown to others before they ask:** `agent_list` and `agent_get` show a node's `purpose`, `maturity`, `accepts` and `refuses`, so a consumer writes a request that fits, or does not send it.
- **Workers:** the charter section is added to the worker brief, after the project rules for workers.

## How a request flows

1. tenebrae (a consumer) sends cogforge (a provider) a request. If the edge rule is `ask`, it waits for the owner first; if `auto`, it carries the trust note.
2. cogforge's session judges it against its charter and answers with one of four **outcomes**, given as a field of `agent_reply`:
   - `accept`: it does the work.
   - `reject`: with a reason ("client-specific; belongs in tenebrae's adapter").
   - `redirect`: to another node ("this belongs in homelab").
   - `escalate`: the charter does not say; it goes to the governing architect, or to the owner.
3. agent-net records the outcome on the edge (the map view shows the latest decisions).
4. If an architect governs the edge as `arbiter` or `owner`, a contested `reject` or an `escalate` goes to it first, and the owner sees its ruling. An `advisor` only adds its opinion.
5. A `reject` or an `escalate` that reaches the owner appears in **Needs you** with the request, the reason and both conversations one key away. The owner lets it stand, or overrules it: cogforge gets the request again with a note only agent-net can write: "Nicolas overrules: do it."

## The CTO

The CTO is the only way to change the graph: charters, maturity, edges, modes and request rules. It is an agent that agent-net runs for the owner, with a chat pane in the TUI.

### What it does

- Changes the graph when the owner asks, and shows each change as a diff with undo:
  ```
  you   cogforge is mature now, and the trading architect should only advise on it
  cto   Done:
          cogforge.maturity  early -> mature  (merge: full -> delegated, tests: required)
          trading-architect -> cogforge  owner -> advisor
        u to undo, or tell me what to change
  ```
- Answers questions about the graph and its history: "why did cogforge reject tenebrae this morning?", "which nodes does the WBG architect own?".
- Drafts a charter for a new node from its code and README, for the owner to correct by talking.
- Sends tasks to any node and starts workers on any project, on the owner's behalf (see "Its authority").
- Reads the feed, Needs you and the workers list, so it can say what is going on and what needs the owner.

### The harness: OpenCode

| | Driven by agent-net today | Any model | Conversation kept | Private tools |
|---|---|---|---|---|
| OpenCode | yes (workers: private server, HTTP API) | yes, any provider with a key or login | yes | yes, through a private MCP server |
| Codex | yes | OpenAI only | yes | yes |
| Claude Code | yes | Anthropic only | yes | yes |
| Pi | yes | yes | no (cannot resume) | yes |

OpenCode is the only one with all four. The CTO runs as a private `opencode serve` with its own data folder (0019 for logins), like a worker, but it is not a worker: it has no task, no hand-back and no idle end. It keeps one long conversation; the owner can start a new one.

- **Model:** a setting of the owner's, with a strong default, because the CTO directs other agents. The owner can name a cheaper model for small edits, or switch per request ("think hard about this one").
- **Tools:** a private MCP server that only the CTO's server loads: read the graph, propose and apply changes, undo, read history, send as the owner's CTO, start workers, read the feed and Needs you. These tools are not in the network's MCP registry and no other session can reach them.

### Its authority

- The CTO's authority comes from the owner typing in the TUI, the owner's own channel, behind the owner gate. It is not a network peer: no session can message it, so no other agent can talk it into changing rights.
- What it sends carries a note only agent-net can write: "from the CTO, on Nicolas's instruction". Receivers treat it as the owner's word, within their own charter's limits (a `critical` node still asks before a deploy; merge authority and the sandbox still apply).
- It acts only in a conversation with the owner. Every message it sends, worker it starts and graph change it makes is shown in the chat and in the feed.
- Text it reads (charters, repository files, other agents' messages in the feed) is data, not instructions. Its own brief says so.

### Sessions do not change the graph

A session that wants a charter change (for example "add api-design to my needs") says so to the owner, and the owner tells the CTO. One place to change things, one history, and no rights changed from the network.

## The TUI

- **Map view** (key to be chosen, for example `M`): architects with the nodes they govern and the mode in brackets; the selected node's charter; its edges in and out with their request rules; its latest decisions (accepted, rejected, overruled). Read only.
- **CTO pane** (for example `C`): the chat, beside the map. Changes appear in the map as the owner talks. Undo is one key on the last change and a sentence for older ones.
- **Needs you:** rejections and escalations that reach the owner, and requests held by an `ask` rule, with "let it stand", "overrule", "approve" and "reject" by key or by telling the CTO.
- **The phone (0006) later:** the same chat and the same Needs you.

## What it replaces

| Today | Becomes |
|---|---|
| Peer trust per session (0013, key `t`) | Request rules on edges; "trusted" becomes `auto` on every edge into the node. |
| Delegation policy per session or project (0014, key `p`) | Part of the node's rules, set through the CTO; per-session overrides stay. |
| Worker sandbox level per project (0020) | A maturity default, overridable on the node. |
| Merge authority (not built) | A maturity default, overridable on the node. |
| Project description from AGENTS.md (`# Project:`) | The charter's `purpose`; the AGENTS.md line stays as a fallback for nodes without a charter. |

Existing settings are migrated into the graph so nothing changes for the owner on the day it ships.

## Security

- **Who can change rights:** only the owner, through the CTO in the TUI (the owner gate). Sessions and peers cannot.
- **The CTO is a powerful agent.** It can message any node and start workers as the owner's CTO. Limits: it acts only while the owner talks to it, everything is shown, and receivers keep their charter limits. A prompt injection through text it reads is the main risk; its brief treats read text as data, and its own changes to rights are listed as diffs the owner sees at once.
- **Notes only agent-net can write:** "your user approved this request", "Nicolas overrules", "from the CTO, on Nicolas's instruction". The marker rule of 0013 applies: a message body cannot imitate them.
- **The graph is visible to every enrolled machine**, like the agent list. Charters should not hold secrets; the CTO is told not to put any there.

## Build order

1. **Charters and maturity, read only.** Gateway tables, the charter in session context and in `agent_list`, the map view. The owner edits charters with a CLI command until the CTO exists. Request outcomes (`accept`, `reject`, `redirect`, `escalate`) and Needs you for rejections.
2. **The CTO.** OpenCode harness, private MCP tools, the TUI chat pane, diffs and undo.
3. **Edges and modes.** depends-on, governs with advisor/arbiter/owner, request rules (replacing trust), architect roles.
4. **Maturity defaults wired to the controls.** Merge authority (new), sandbox, delegation, request judging.
5. **The phone** (0006), with the same chat and Needs you.

## Open questions

1. **Maturity levels:** are four levels right, and are the defaults in the table the right ones?
2. **Architect sessions:** must an architect always have a running session, or can the CTO answer for an architect that has none?
3. **Several sessions for one node:** when two sessions speak for cogforge at once, which one judges an incoming request? (Proposal: the one the sender addressed; the outcome is recorded for the node.)
4. **Overrule versus architect owner:** when an architect is `owner` of a node and the owner overrules the architect, does the architect get told? (Proposal: yes, as a notification.)
5. **CTO conversation length:** one long conversation will fill its context. Start a new one per day, or summarize?
6. **Cost:** a strong CTO model reads the graph and history on every request. Is a daily budget wanted?
