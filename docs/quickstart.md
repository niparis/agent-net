# Quick start

Your first day with agent-net, step by step.

One idea runs through all of it: **you talk to the CTO, and the CTO runs the network.** The CTO is your main assistant session. It adds machines, sets up projects, starts and stops sessions, changes settings and answers "where are we?". You use the TUI to watch, and to open a session when you want to look inside it.

## Two kinds of sessions

A session is one AI coding agent at work: Claude Code, Codex, OpenCode, Pi or agy. agent-net works with two kinds:

- **Your own sessions.** You start them in your terminal, the way you always do (`claude`, `codex`, `opencode` and so on). agent-net finds them by itself and gives them its tools, so they can send and receive messages and start workers. They live as long as your terminal. Use them when you want to work hands-on with one agent.
- **agent-net sessions.** agent-net starts them in the background, on any machine: the CTO, the chief of staff, a lead you ask the CTO to start, and every worker. They need no terminal and keep working while you are away. A message to one that stopped wakes it in the same conversation.

Both kinds show up in the Sessions tab and talk to each other the same way. Press Enter on any session to look inside it.

## 1. Install on your main machine

```sh
curl -fsSL https://niparis.github.io/agent-net/install | sh
```

The script checks the release signature, installs `agent-net` in `~/.local/bin` and sets this machine up as the gateway. If `~/.local/bin` is not on your `PATH`, it prints the line to add. Details and options: [setup.md](setup.md).

## 2. Open agent-net and meet the CTO

```sh
agent-net
```

The TUI opens on the Sessions tab: your machine and the agent sessions running on it. Press `c` to open the CTO. From now on, most things start with a sentence to the CTO.

## 3. Add your other machines (optional)

On the gateway machine, run:

```sh
agent-net invite
```

It prints one install line, valid for 10 minutes and one machine. If the gateway has a Tailscale address, it prints a second line for that address. Paste the line in a shell on the new machine. The new machine shows up in the Sessions tab, and its sessions can now talk to every other session.

Repeat for each machine. Details: [setup.md](setup.md).

## 4. Set up your projects and teams

Tell the CTO what you work on and how it should be run. These are real first-day asks, lightly edited. Each one sets up something you will not have to manage again:

- *"Start a new project named shop."* **Why:** agent-net only works with projects it knows. A project gets a repo, a place on a machine, and sessions that belong to it.
- *"Create a new project on the worker VM named shop-api, in the same folder as shop. Also create a team with shop, this new project and the finance project."* **Why:** a team groups the projects that change together, so one lead can coordinate them.
- *"I want a second team, the homelab team, with all the sessions we have and a lead."* **Why:** a lead is the one agent you talk to for a team or a repo. It plans, starts workers, checks their work and answers for it.
- *"Help me set up a charter and edges for shop."* **Why:** the charter says what the agent does, accepts and refuses. Edges say who may ask whom for what, so agents can work together without asking you.
- *"Set up the edge between shop and homelab so that homelab deploys shop without my confirmation."* **Why:** every confirmation you give once by rule is one you never give again.
- *"Set up a merger agent for shop."* **Why:** the merger tests, merges and releases changes, so nothing reaches main unchecked and you do not merge by hand.
- *"What workers could I create with agy?"*, then *"Create a new worker type: claude, sonnet, high effort."* **Why:** worker types are the menu the picker chooses from for every task.
- *"I want to replace my homelab lead with a Claude Sonnet session. What is the best way to go about it?"* **Why:** roles stay; the agent program and model behind them can change.

If you already run many sessions and repos, the same asks work on what you have: the CTO places existing sessions into projects and teams.

## 5. Design the work with the lead

The way to work with a lead is to design with it, not to pass it a one-line order. Open the lead's session (Enter on the Sessions tab) and think the work through together:

- Say what you want and why. The lead knows its repo: it reads the code, proposes a plan, points out what will break and asks what it needs to know.
- Push back and refine until the plan is right. A good lead writes the plan down in the repo, so it survives a restart and other agents can read it.
- Agree on what "done" means, then let it run.

From there the lead works on its own. It splits the plan into tasks and starts **workers**: short-lived sessions that each do one task in their own branch and sandbox, then hand back. agent-net picks the agent program, model and machine for each worker. The lead reviews what comes back, and each change goes through its steps (accept, review, test, merge, release, verify) until it ships. You get short outcomes, and you come back to the lead when a real decision is needed.

## 6. Day to day with the CTO

Real asks from daily use, lightly edited:

- **Status, without starting work:** *"How are we doing on the storage migration? Just give me a status update, don't do more work."* / *"Has our chief of staff done anything yet?"*
- **What runs where:** *"Can you tell me all the agents and sessions we have on the worker VM?"* / *"What's eating my CPU on the worker VM?"* / *"On the worker VM, which sessions can we put to sleep?"*
- **Machine trouble, handled by the agents:** *"The laptop is running out of space. Message all the live and recent sessions there, ask them to reclaim disk space, and give me a report with each session and its answer."*
- **Models and workers:** *"Can we change homelab agent 3 to use agy with Gemini Flash?"* / *"Protect Claude so it stops being used for workers. No, protect it at the provider level."* / *"Pause the argon worker: its price is about to go up. Don't delete it."*
- **Settings:** *"Change the merger's idle limit to 30 minutes."* / *"Put the research loop on hold."*
- **When something is wrong:** *"The research project is stuck. Analyse why."* / *"I can't find my homelab session!"*, then *"Report it to agent-net: it's a problem that it disappears from my list."*
- **Clearing things for you:** *"Approve all the pending drafts for me."* / *"In my inbox I have 10 really old messages from one project. Get rid of them."*

### How to talk to the CTO

- **Say it once, and expect it done.** In the owner's words: *"When I speak you must do. When you can't do, you ask agent-net to build. Very simple."*
- **Do not approve twice.** If you already asked for something, the CTO acts on it. If it asks you to authorise what you just said, tell it: that is a defect, not your job.
- **Make it the default.** When you repeat a choice, ask the CTO to make it the default or a standing rule, so no one asks again.

## 7. What stays with you

Some things only you can do, and agent-net asks you for them:

- Browser and OAuth sign-ins, passwords and `sudo` prompts.
- Physical work on a machine, and network approvals such as a Tailscale route.
- Deleting data, spending money and secrets. These always wait for your own words.

## 8. When something goes wrong

- Ask the CTO *"why?"* first. It can read the machine logs and the history of every message and worker.
- `agent-net nodes` shows every machine, its version and whether it is online.
- A message to a session that is down waits, and expires after 24 hours by default. If it matters, ask the CTO to make sure it arrives.
- Common problems and fixes: [troubleshooting.md](troubleshooting.md).

## Where to go next

- [tour.md](tour.md): every TUI screen and what you can do on it.
- [setup.md](setup.md): install, add, update and remove machines in detail.
- [principles.md](principles.md): the ideas behind these choices.
