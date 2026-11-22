# Quick start

Your first day with agent-net, step by step.

One idea runs through all of it: **you talk to the CTO, and the CTO runs the network.** The CTO is your main assistant session. It adds machines, sets up projects, starts and stops sessions, changes settings and answers "where are we?". You use the TUI to watch, to open a session when you want to look inside it, and to answer what is waiting for you on the Desk.

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

Ask the CTO: *"Add my laptop."* It prints one install line, valid for 10 minutes and one machine. Paste it in a shell on that machine. The new machine shows up in the Sessions tab, and its sessions can now talk to every other session.

A common setup is your laptop plus a VM that does the heavy work. Add the VM the same way, and make it the default home for workers: *"Add a VM, and run workers there by default."* Your laptop then stays free, and workers keep running when it sleeps.

## 4. Bring in your projects

Tell the CTO which repositories you work on, and how you want them run. Typical first-day asks:

- *"Add the repos in ~/code/shop and ~/code/blog."*
- *"Make a lead for the shop repo."* A lead is the agent in charge of one repo: it plans, starts workers and checks their work.
- *"Put shop and blog in one team."* Teams are optional. A repo without a team reports straight to the CTO.
- *"Only the shop lead may merge to main."*
- *"Trust these folders"*, or *"Let workers run the tests without asking."*
- *"Connect the shared GitHub MCP server."* Some connections need a browser sign-in: that part is yours.

If you already have a mess of sessions and repos, ask the CTO to reorganize it: *"Look at my sessions and propose a team structure."*

## 5. Give work to a lead

Ask a lead for something real, through the CTO (*"Tell the shop lead to fix the checkout bug"*) or by opening the lead's session with Enter on the Sessions tab and asking it directly.

The lead starts one or more **workers**: short-lived sessions that do one task in their own branch and sandbox, then hand back. agent-net picks the agent program, model and machine for each worker. The lead reviews the result, and the change goes through its steps (accept, review, test, merge, release, verify) until it ships.

You see the outcome as a short report. If you want the details, open the session.

## 6. Day to day: what people ask the CTO

- *"Where are we on the checkout rework? Just a status, no more work."*
- *"Go"*, or *"yes to all"*, to move a plan to its next step.
- *"Change the plan: keep the cache in the shop repo, and update the plan doc."*
- *"Use a cheaper model for the blog lead."*
- *"Keep workers off my personal subscription."*
- *"Merge PR 42."*
- *"The build on the Linux box is broken. Get it fixed by whoever owns that machine."*
- *"The blog session is down. Make sure it gets my message."*
- *"Clean up the stray and duplicate sessions."*
- *"Stand the blog team down for now"*, then later *"restart it"*.
- *"Why did that release fail? I want the cause, not a workaround."*
- *"Tell the shop lead its last report was too long."*

## 7. What stays with you

Some things only you can do, and agent-net asks you for them:

- **The Desk** (tab 6): the questions the chief of staff could not answer from your past decisions. Your answers are remembered, so the same question does not come back.
- Browser and OAuth sign-ins, passwords and `sudo` prompts.
- Physical work on a machine, and network approvals such as a Tailscale route.
- Deleting data, spending money and secrets. These always wait for your own words.

## 8. Say it once

When you notice you are repeating yourself, turn it into a rule: *"From now on, workers never run on my laptop."* The CTO stores it as a standing order, and every agent follows it. If an agent asks you to approve or time something you already decided, tell the CTO. That is a defect, not your job.

## 9. When something goes wrong

- Ask the CTO *"why?"* first. It can read the machine logs and the history of every message and worker.
- `agent-net nodes` shows every machine, its version and whether it is online.
- A message to a session that is down waits, and expires after 24 hours by default. If it matters, ask the CTO to make sure it arrives.
- Common problems and fixes: [troubleshooting.md](troubleshooting.md).

## Where to go next

- [tour.md](tour.md): every TUI screen and what you can do on it.
- [setup.md](setup.md): install, add, update and remove machines in detail.
- [principles.md](principles.md): the ideas behind these choices.
