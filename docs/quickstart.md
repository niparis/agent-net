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

## 4. Bring in your projects

Tell the CTO which repositories you work on, and how you want them run. Each ask below sets up one thing, so that later you can hand work over and stop watching it:

- *"Add the repos in ~/code/shop and ~/code/blog."* **Why:** agent-net only knows about projects you add. Once a repo is added, its sessions show up together, and workers can be started in it on any machine.
- *"Make a lead for the shop repo."* **Why:** you want one agent to talk to per repo, not ten. The lead plans the work, starts workers, checks what they hand back and answers for the repo. You give it a goal and get one outcome back.
- *"Put shop and blog in one team."* **Why:** related repos often change together. A team lead coordinates them, so a change in one does not break the other. Teams are optional: a repo without a team reports straight to the CTO.
- *"Only the shop lead may merge to main."* **Why:** many agents work in parallel. One named agent decides what reaches main, so nothing ships that nobody checked.
- *"Trust these folders"*, or *"Let workers run the tests without asking."* **Why:** every permission prompt stops a worker until you answer. Granting the safe, repeated actions once lets work run while you are away.
- *"Connect the shared GitHub MCP server."* **Why:** one connection, set up once, that every agent can use, instead of a sign-in per session. Some connections need a browser sign-in: that part is yours.

If you already have a mess of sessions and repos, ask the CTO to reorganize it: *"Look at my sessions and propose a team structure."* **Why:** the same setup, built from what you already run.

## 5. Design the work with the lead

The way to work with a lead is to design with it, not to pass it a one-line order. Open the lead's session (Enter on the Sessions tab) and think the work through together:

- Say what you want and why. The lead knows its repo: it reads the code, proposes a plan, points out what will break and asks what it needs to know.
- Push back and refine until the plan is right. A good lead writes the plan down in the repo, so it survives a restart and other agents can read it.
- Agree on what "done" means, then let it run.

From there the lead works on its own. It splits the plan into tasks and starts **workers**: short-lived sessions that each do one task in their own branch and sandbox, then hand back. agent-net picks the agent program, model and machine for each worker. The lead reviews what comes back, and each change goes through its steps (accept, review, test, merge, release, verify) until it ships. You get short outcomes, and you come back to the lead when a real decision is needed.

## 6. Day to day: what people ask the CTO

The CTO changes how the network is set up. Typical asks:

- *"Start a new project for the mobile app: a repo, a lead and a charter."*
- *"The blog lead should also own the docs site, and it must never touch the database schema."* (changes a charter)
- *"Let the shop lead merge and release on its own once the tests pass."* (changes who may merge)
- *"Move the blog into the web team."*
- *"Where are we on the checkout rework? Just a status, no more work."*
- *"Add Gemini Neon as a new worker profile, and advise me on the right effort level."* (registers a new kind of worker that the picker can then choose)
- *"Use a cheaper model for the blog lead."*
- *"Keep workers off my personal subscription."*
- *"The build on the Linux box is broken. Get it fixed by whoever owns that machine."*
- *"Clean up the stray and duplicate sessions."*
- *"Stand the blog team down for now"*, then later *"restart it"*.
- *"Why did that release fail? I want the cause, not a workaround."*

## 7. What stays with you

Some things only you can do, and agent-net asks you for them:

- Browser and OAuth sign-ins, passwords and `sudo` prompts.
- Physical work on a machine, and network approvals such as a Tailscale route.
- Deleting data, spending money and secrets. These always wait for your own words.

## 8. Say it once

When you notice you are repeating yourself, turn it into a rule: *"From now on, give me one-line outcomes, not long reports."* The CTO stores it as a standing order, and every agent follows it. If an agent asks you to approve or time something you already decided, tell the CTO. That is a defect, not your job.

## 9. When something goes wrong

- Ask the CTO *"why?"* first. It can read the machine logs and the history of every message and worker.
- `agent-net nodes` shows every machine, its version and whether it is online.
- A message to a session that is down waits, and expires after 24 hours by default. If it matters, ask the CTO to make sure it arrives.
- Common problems and fixes: [troubleshooting.md](troubleshooting.md).

## Where to go next

- [tour.md](tour.md): every TUI screen and what you can do on it.
- [setup.md](setup.md): install, add, update and remove machines in detail.
- [principles.md](principles.md): the ideas behind these choices.
