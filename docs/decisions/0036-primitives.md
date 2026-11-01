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

agent-net knows the content only of work that crosses a boundary between agents or between an agent and the owner. Work inside one session stays in the harness and the repo. agent-net is not a task tracker.

Work has three levels: the objective is the charter's purpose; the roadmap lives in the repo and the charter may point to it; tasks are the work to be done. agent-net reads none of this content from the repo. It knows boundary work only as described above.

## State and pending work

Pending says whether an agent has work left. It comes from open tasks, such as a reply owed, a reply awaited or a worker not yet handed back, plus one declared bit at each turn end. No answer means unknown, never no.

Agent state is activity from the session plus pending work for the agent. An idle agent with nothing pending is free. An idle agent with work pending is waiting or stalled.

## Autonomy and guard rails

Authority gates actions such as a merge or push. Autonomy gates decisions about work, such as waking itself or planning its next work. When an agent is idle with work pending, autonomy determines whether agent-net wakes it or creates a Needs you row.

Edge, autonomy and authority together answer who may create work for whom and what needs approval. Autonomy must allow waking before an automatic wake-up can happen. A daily cap limits automatic wake-ups per agent. Two wake-ups in a row with no new message, task or commit stop automatic wake-ups and tell the owner.

An open conflict remains: the session-start design lets only the owner or the CTO start sessions, while autonomy means agent-net wakes agents itself. This needs a decision.

## Exposure

The owner sees agents with a kind badge, projects and Needs you. The Graph shows charter, edge and authority. Sessions, workspaces, budget and knowledge stay underneath and appear when they need attention.

## Display names

Screens may use names other than the fixed internal terms. The custom CTO title is the first case. A future objective, not yet built, is themes that rename primitives on screen only. A Rome theme might call the CTO Imperator and an agent Centurion. A Matrix theme might call the CTO Oracle. Agents always see the fixed terms.
