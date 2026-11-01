# 0036 - Primitives

**Date:** 2026-11-01
**Status:** Accepted

## Decision

These terms describe the agent network. They are the fixed terms used in code, docs and what agents see.

### People and agents

- **Owner:** The person who owns the network. The owner is the only source of authority, including authority given to delegates.
- **Agent:** A lasting identity with a charter. The agent is the node on the Graph. Two questions define its kind: does it have a home project, and is it lasting or for one task?
  - A lasting agent with a home is a **project lead**.
  - A lasting agent without a home is an **advisor**, an architect for a domain. The CTO is the advisor whose scope is the whole network. The CTO has one special right: it speaks with the owner's authority only through proposals the owner approves.
  - A one-task agent with a home is a **worker**. An agent requests it and it returns a report. Workers write code today, but a task can be anything.
  - A one-task agent without a home is a **ghost** or experiment. An open question is whether a ghost is simply a worker whose caller is the owner.
- **Session:** One running process of one harness for an agent. It is born, dies and can be resumed. One session is the agent. Two or more live sessions of one agent are clones: possible, but not recommended. Machine, harness and model are session attributes, not primitives.
- **Project:** The home of a lead, such as a repo or folder. It is not a Graph node.

### Rules and work

- **Charter:** The goals and behavior of any agent.
- **Edge:** How two agents deal with each other.
- **Authority:** What an agent may do without asking the owner, including merges, pushes, proposals and held requests.
- **Autonomy:** Whether an agent may wake itself or plan its own work. It is separate from authority.
- **Task:** Work to be done.
- **Message:** An ask, reply or notification.
- **Report:** The result of a task.
- **Workspace:** A folder or worktree an agent may write in.

### Shared resources

- **Tools:** Skills and MCP tools shared by agents.
- **Knowledge:** Playbooks and findings shared by agents.
- **Budget:** Quotas and protected providers.
- **Needs you:** The queue for the owner's attention.

## Tasks and levels of work

A task is a contract between a caller and a doer. It names the caller, doer, done condition and result. Requests between agents, worker briefs and Needs you items are tasks.

agent-net is the project manager, but it does not track task content. Each agent keeps its own tasks and objectives. agent-net knows the content only of work that crosses a boundary between agents or between an agent and the owner. Work inside one session stays in the harness and the repo.

Work has three levels: the objective is the charter's purpose; the roadmap lives in the repo and the charter may point to it; tasks are the work to be done. agent-net reads none of this content from the repo. It knows boundary work only as described above.

## State and pending work

Pending says whether an agent has work left. It is derived from open work, such as a reply owed, a reply awaited, a worker not yet handed back or a non-empty announced task. An idle check can ask the agent whether it is done. No answer means unknown, never no.

Agent state is activity from the session plus pending work for the agent. An idle agent with nothing pending is free. An idle agent with work pending is waiting or stalled.

## Autonomy and guard rails

Authority gates actions such as a merge or push. Autonomy gates decisions about work, such as waking itself or planning its next work. When an agent is idle with work pending, autonomy determines whether agent-net wakes it or creates a Needs you row.

Edge, autonomy and authority together answer who may create work for whom and what needs approval. Autonomy must allow waking before an automatic wake-up can happen. A daily cap limits automatic wake-ups per agent. Two wake-ups in a row with no new message, task or commit stop automatic wake-ups and tell the owner.

An open conflict remained: the session-start design lets only the owner or the CTO start sessions, while autonomy means agent-net wakes agents itself. The owner decided on 2026-11-01: agent-net may wake an existing agent by resuming it when autonomy is `wake`; creating a new agent or a clone stays with the owner, with the CTO able to propose it for approval. Waking does not start a new session in slice 4. See [the session-start design](../design/start-sessions.md#open-questions-for-the-owner).

Slice 4 builds autonomy as one node setting with two values, `off` (the default) and `wake`. Every agent can read it in the public graph and in its agent profile because it is a rule, but only the owner changes it: on the Graph agent card with `a`, or through an owner-approved CTO proposal. All node settings, including this one, are major in the CTO minor/major rule. The gateway stores only the daily wake count, the no-progress streak and the progress markers; it never stores task content.

## Idle checks

agent-net decides when a check is worth it. The agent answers whether its work is done. The CTO gets only cases that need judgment. The CTO is not a poller: polling costs money and context, and the CTO reads only public data.

The idle check replaces a declared "more to do" bit at each turn end. Check an agent when it has been idle for about 20 minutes, has something open and has not been checked since its last activity. Open work means an unanswered request, a worker not handed back or a non-empty announced task.

Send one short message asking the agent to answer with `agent_announce`: done, more or blocked on X. Then act on the answer:

- **Done:** Do nothing until new activity.
- **More:** Continue if autonomy allows. Otherwise, create a Needs you row: "X stopped with work left".
- **Blocked on X:** Route it to X or the owner. Send it to the CTO only if it needs a decision.

Send at most one check per idle period. Only new activity resets that limit. Two "more" answers with no progress between them stop the checks and tell the owner once. A daily cap applies per agent. A check wakes the agent's own model, which may use a protected provider. Keep checks rare to control that cost.

## Build in slices

Build each slice so it can be measured and removed:

1. Show read-only "waiting on B" and "owes reply" in STATUS from conversations.
2. Send B one reminder when B goes idle with an unanswered request.
3. Add the idle check.
4. Add autonomy with two values, off and wake, and a small daily cap. (Built as a node setting; see the owner answer above.)

If slice 1 shows that agents rarely stall, stop there.

## Minimal state

There is no task table. Tasks are conversations, workers and Needs you items. Pending is derived, and autonomy is one node setting beside the charter. The only new stored facts are whether the agent has been checked since its last activity, and the small wake counters the guard rails need.

## Exposure

The owner sees agents with a kind badge, projects and Needs you. The Graph shows charter, edge and authority. Sessions, workspaces, budget and knowledge stay underneath and appear when they need attention.

## Display names

Screens may use names other than the fixed internal terms. The custom CTO title is the first case. A future objective, not yet built, is themes that rename primitives on screen only. A Rome theme might call the CTO Imperator and an agent Centurion. A Matrix theme might call the CTO Oracle. Agents always see the fixed terms.
