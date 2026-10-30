# 0022 - A graph of components with charters, and a CTO in the TUI

**Date:** 2026-10-30
**Status:** Built (steps 1 to 5), 2026-10-30. Step 6, the phone, belongs to decision 0006 and is not part of this decision's build. See "As built" at the end.
**Context:** Today every session is an island with a few separate switches: peer trust (0013), the delegation policy (0014), the worker sandbox level (0020), and merge authority (discussed, not built). Nothing says what a repository is for, what it should refuse, how careful its agents must be, or which other repositories and agents have a say over it. The owner wants agents to react to requests from other agents the way a team would: a generic API should be able to refuse a client-specific feature and give the reason; some services matter more than others; a session that owns no code can manage the architecture of a few repositories. The owner wants to set all of this by talking, not by clicking, and to see it.

Base: [0008](0008-operator-interface-and-worker-build.md) (the owner gate and operator interface), [0013](0013-peer-trust-and-hints.md), [0014](0014-delegation-policy.md), [0016](0016-needs-you.md), [0019](0019-opencode-worker-logins.md), [0020](0020-worker-sandbox.md).

## The model in one paragraph

agent-net keeps a **graph of components**. A node is a repository or service (a **component**), or an **architect** that owns no code. Each node has a **charter**: a fixed set of attributes with free-text values, including a **maturity** level. **Edges** link nodes: one component depends on another, and an architect governs some components, each edge with its own **mode** (advisor, arbiter or owner). Sessions come and go; a session **speaks for** the node of the project it works in. The owner is above every node and edge. The owner changes the graph only by talking to a **CTO**: an agent that agent-net runs inside the TUI, with a map view beside the chat.

## Nodes

2026-10-30: Implicit components require a Git root outside temporary folders and the home folder itself; owner-facing graph reads hide empty inactive projects after seven days, with an all option.

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
| `feature_requests` | What to do with a good idea that is not in scope yet | Small generic additions: just do them. New endpoints: propose to Nicolas with a recommendation. Client-specific: reject. |
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
| deploys | deployer (for example homelab) | each app it deploys | The deployer applies the **target's** deploy rule, not its own: it deploys an `early` app freely and stops for a `critical` one until it holds the owner's written approval (see "Written approval"). |
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

## Every message says who it comes from, in the receiver's terms

The receiver must know, at the moment a request arrives, whether the sender is its boss, its consumer or a stranger. agent-net adds a line only it can write (the marker rule of 0013) to every delivered request, from the graph:

```
from tenebrae, a consumer of yours: judge its request by your charter
from trading-architect, your owner: follow its decision
from wbg-architect, your advisor: weigh its opinion, you decide
from finance, no relation to you: your charter and normal rules apply
from the CTO, on Nicolas's instruction
```

## Decide, do not defer

Agents fail in two ways: they do whatever is asked without thinking, or they bounce everything to the owner. The brief of every node's sessions says:

- Judge each request against your charter and decide yourself: accept, reject with a reason, or redirect.
- Escalate only when your charter says so (`escalate`, maturity, `feature_requests`) or truly does not cover the case.
- Every escalation carries your recommendation: "I would reject this because ...; agree?", never a bare question. The owner then answers with one word.

The map view later shows escalations and rejections per node, so the owner can see when a charter is too timid or too loose.

## Written approval

Some actions need the owner's written approval: a deploy of a `critical` app, a merge under `ask` merge authority, anything a charter's `escalate` names. A chat line saying "Nicolas approved" proves nothing, since any agent can write it. So:

1. The agent asks through agent-net with the exact action: "deploy cogforge abc123 to prod", and its recommendation.
2. The request appears in the owner's inbox and Needs you, and on the phone later.
3. The owner approves or refuses, by key or by telling the CTO.
4. The agent receives an approval note only agent-net can write, tied to that action: "Nicolas approved: deploy cogforge abc123 to prod, 14:02". An approval is for one action; a different commit needs a new one.

## The owner's inbox

Needs you shows what waits right now. The inbox is the lasting list of things for the owner, the place tickets used to be:

- **Approvals:** actions waiting for written approval (deploys, merges).
- **Feature ideas:** ideas agents proposed under `feature_requests` and did not act on.
- **Questions:** things an agent was not comfortable deciding, each with its recommendation.
- **Rejections and escalations** that reached the owner.

Each item keeps its context (the request, the conversations, the recommendation) and stays until the owner answers it, at any time; the answer goes back to the agent. The TUI shows the inbox, the CTO can read it and answer for the owner on instruction, and the phone later sends a push for approvals.

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

## Quotas

Workers spend the owner's subscriptions. agent-net reads how much is left and plans with it. It does not measure quotas itself: it reads [quota-axi](https://github.com/kunchenguid/quota-axi), a CLI that reports the quota windows of the owner's subscriptions (Claude, Codex, OpenCode Go, DeepSeek, Cursor, OpenRouter and more) from the logins on the machine.

- **Reading.** When quota-axi is installed on a machine (an integration of 0021: the owner turns it on once for all machines; agent-net does not install npm packages by itself), its daemon runs `quota-axi --json --max-age 5m --no-credential-refresh` every few minutes and sends the gateway one line per provider and window: percent used, time to reset, pace (`on pace`, `ahead`, `exhausted`). `--no-credential-refresh`: agent-net never makes quota-axi refresh a login (decision 0019's risk). No account identity is sent (`--full` is not used).
- **Model choice.** A provider that is exhausted, or above 95% of any window, is skipped for loose model names; an exact model id still works, with a warning in the start result. A provider ahead of pace ranks below providers on pace.
- **Admission.** A start whose model's provider is exhausted is refused with the reason and the reset time, instead of failing at the first model call.
- **Shown** in one line of the TUI header (`claude 59%w · codex 90%w · opencode-go 76%w · cursor exhausted`), in `agent_nodes` for agents, and to the CTO, which plans with it ("codex is at 90% of its week: the next workers go to opencode-go").

## Knowledge: what the network learns about models, harnesses and projects

Which model and harness to use for which work, and the quirks of each project, are learned by trying. Today that knowledge stays in one session's memory. agent-net keeps it for every agent, in three layers.

1. **Facts, collected automatically: the track record.** agent-net already records every worker's harness, model, run time, outcome and tokens. A worker also reports `complete: false` in `agent_handback` when it hands back partial work, so a lazy model is counted. A run that ended because its owner stopped it, the worker was lost at a daemon restart or its owner went away is counted as `stopped`, apart from success and failure: that says nothing about the model and must not make its record look bad. Per harness and model:
   ```
   terra · pi       4 runs: 1 complete, 3 partial (median 9 min) · cannot resume
   terra · codex    3 runs: 0 complete, 3 partial (median 3 min) · resumes
   deepseek-v4.1-flash · opencode   14 runs: 13 complete (median 26 min)
   gpt-6-luna · codex   3 runs: 2 complete, 1 stopped (median 6 min) · resumes
   ```
2. **Findings, reported by agents.** The agents that review workers' results notice things. A tool `agent_learn(subject, finding, evidence)` records one:
   ```
   subject:  model gpt-6.1-terra, harness pi
   finding:  Hands back partial work after about 10 min even when told not to.
             Delivers one layer when the brief has a hard checklist.
   evidence: the worker sessions it saw
   ```
   A subject is a model, a harness, a model on a harness, a project ("cogforge tests need the TEI service up"), an MCP server or a machine. The worker notice's checklist ends with "Did you learn something about this model, harness or project? Report it with agent_learn", and the guidance says so. A finding is a proposal: it changes nothing until it is promoted.
3. **The playbook, built automatically.** The owner wants to know what the network learned, not to decide on each finding. The gateway promotes a pattern to a playbook note on its own when it has enough support: at least 3 findings on the same subject from at least 2 different sessions within 30 days, or a clearly bad track record (at least 4 decided runs, stopped runs aside, and under half complete). The note is the latest finding's text, its support count and the track record line, with no model call. A note whose support is all older than 60 days goes stale and is no longer shown. The owner (or the CTO on the owner's word) can write, pin or remove a note at any time; the owner never has to.

**Quiet by design.** Knowledge must not become theater that interrupts the team:

- An agent reports a finding only when it would change how someone picks or briefs that model, harness or project next time. Never routine results. At most 3 findings per session per day.
- Findings are never sent as messages and never appear in Needs you.
- Playbook notes appear only at the moment of choice: in the `agent_start` result and in `agent_nodes`. Not in every turn's context.
- The owner sees what was learned when he looks: a "Learned" list in the TUI (key `L`) with each note's support, a quiet count in the header ("learned 2 new") that never pops up, and the CTO answering "what have we learned this week?".

**Storage**, in the gateway, one copy for the network:

- findings: subject, text, evidence (worker and session ids), reporter, time, status (`new`, `promoted`, `dismissed`, `stale`). A finding never promoted goes stale after 90 days.
- playbook: subject, note, how it was made (automatic, or written by the owner or the CTO), time, and the findings and track record behind it. Versioned with history and undo, like charters.

**Delivery:**

- `agent_start` results show the playbook note and the track record line for the chosen model and harness; `agent_nodes` shows them for every model a machine offers.
- The CTO sees all of it when it plans work.

## The TUI

2026-10-30: The Sessions tab keeps the CTO chat below the session list; Messages and Quotas have separate tabs.

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

1. **Charters and maturity, read only.** Gateway tables, the charter in session context and in `agent_list`, the map view. The owner edits charters with a CLI command until the CTO exists. Request outcomes (`accept`, `reject`, `redirect`, `escalate`) with a recommendation, the "decide, do not defer" brief, the relationship line on every request, and Needs you for rejections.
2. **The CTO.** OpenCode harness, private MCP tools, the TUI chat pane, diffs and undo.
3. **Edges and modes.** depends-on, governs with advisor/arbiter/owner, deploys (the target's rule applies), request rules (replacing trust), architect roles, written approvals and the owner's inbox.
4. **Maturity defaults wired to the controls.** Merge authority (new), sandbox, delegation, request judging.
5. **Quotas and knowledge.** quota-axi reading, model choice and admission by quota; `complete: false` hand-backs and the track record; `agent_learn` findings; the automatic, quiet playbook.
6. **The phone** (0006), with the same chat and Needs you.

## Open questions

1. **Maturity levels:** decided: the four levels and the defaults in the table stay; a node overrides any default.
2. **Architect sessions:** decided for now: the CTO answers for an architect that has no running session.
3. **Several sessions for one node:** decided for now: the addressed session judges the request, and the outcome is recorded for the node.
4. **Overrule versus architect owner:** decided for now: an overruled architect gets a notification.
5. **CTO conversation length:** decided: one conversation, compacted automatically, like Claude Code. The daemon asks OpenCode to compact it when its context passes 70% of the model's limit, and when it is large and idle shortly before the provider's prompt cache expires (5 minutes by default, a setting per provider), so the next message never rereads a big cold history. The chat shows one line per compaction.
6. **Cost:** decided: no budget.

## As built (2026-10-30)

All of steps 1 to 5 are on main, with tests. What exists, by step:

1. **Graph and charters.** Gateway tables for nodes, charters (nine attributes, maturity included), edges (`depends`, `governs` with a mode, `deploys`) and their history with undo; owner-only operator methods and the CLI (`agent-net charter`, `graph`, `node`, `edge`); charter fields in `agent_list` and `agent_get` with the AGENTS.md description as fallback; the charter, the relationship line and the "decide, do not defer" rule in every session's turn context and in worker briefs; the read-only map (`M`); request outcomes (`accept`, `reject`, `redirect`, `escalate`) on `agent_reply`, recorded on the node edge, with rejections and escalations in Needs you and the owner's overrule.
2. **The CTO.** A private OpenCode server with its own data folder, one conversation that survives daemon restarts, a model setting, private MCP tools reachable only from its own process tree (graph reads and writes, agents, workers, Needs you, the feed, sending, starting workers), the "from the CTO, on Nicolas's instruction" note, and the chat pane (`C`) with tool lines, undo (`u`) and a new conversation (`n`).
The CTO can also run in a Claude Code background session using the owner's
Claude login and Claude's automatic compaction. The owner selects the harness;
each harness keeps its own conversation. Claude receives only the private CTO
MCP configuration, and the daemon checks its session's process tree.

3. **Edges at work.** Request rules on edges (`normal`, `auto`, `ask`, `never`; the most specific rule wins; trusted sessions migrated to `* -> node: auto`); held requests in Needs you with `a`, `A`, `r`, `R`; written approvals and the owner's inbox (`i`), with answers delivered in the same transaction and approval notes only the gateway writes; outcomes and merge-authority requests create inbox items.
4. **Maturity defaults.** Defaults per level for tests, merge authority, worker sandbox, consumer judging and deploys; per-node overrides with history; the rules in plain words in context and briefs; a deployer gets the target's deploy rule.
5. **Quotas and knowledge.** quota-axi reading (an integration item), model choice and admission by quota, the header line and `agent_nodes`; `complete: false` hand-backs, the track record per harness and model, `agent_learn` findings with a daily limit, the automatic playbook (promoted by support, stale after 60 days, owner override), delivered only in start results and `agent_nodes`, and the quiet `L` list.

Open questions 1 and 6 are decided. Auto-compaction of the CTO conversation (question 5) is built: OpenCode 2.2.0's `POST /api/session/{id}/compact`, the 70 percent threshold after a reply, the 40k floor before the provider's prompt cache window (5 minutes by default), one compaction per idle period, the owner settings, and the chat line. Merge authority is guidance plus approval objects, not a git lock: use branch protection on critical repositories.

