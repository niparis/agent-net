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
- **Task:** Work to be done.
- **Message:** An ask, reply or notification.
- **Report:** The result of a task.
- **Workspace:** A folder or worktree an agent may write in.

### Shared resources

- **Tools:** Skills and MCP tools shared by agents.
- **Knowledge:** Playbooks and findings shared by agents.
- **Budget:** Quotas and protected providers.
- **Needs you:** The queue for the owner's attention.

## Exposure

The owner sees agents with a kind badge, projects and Needs you. The Graph shows charter, edge and authority. Sessions, workspaces, budget and knowledge stay underneath and appear when they need attention.

## Display names

Screens may use names other than the fixed internal terms. The custom CTO title is the first case. A future objective, not yet built, is themes that rename primitives on screen only. A Rome theme might call the CTO Imperator and an agent Centurion. A Matrix theme might call the CTO Oracle. Agents always see the fixed terms.
