# Onboarding design

## Fit with the primitives (decision 0036)

This design teaches agents, sessions, workers, charters and edges. A project is the home of a lead, not its Graph node; an agent is the node. Its description of the private CTO reflects the design at the time and conflicts with the proposed peer CTO in decision 0035; decision 0036 names the CTO the network-wide advisor.

**Date:** 2026-10-31
**Status:** Design for the owner to review. Nothing here is built.
**Replaces:** the rejected draft on branch `agent-net/w_b660bc1794986523`. That draft was a setup checklist. This one starts from what agent-net is for.

## What onboarding must teach

agent-net is one place for all your coding agents. Onboarding teaches six things, in this order, because each one builds on the one before:

1. **You see all your agents in one place.** Every Claude Code, Codex, Pi and OpenCode session on every machine, live: what each works on, which are busy, which wait for you (Needs you). You set nothing up: agent-net finds them. Enter steps into any session, on any machine.
2. **Your agents talk to each other.** An agent stuck on something another project owns asks that project's agent, instead of guessing or interrupting you. You can watch these conversations and message any agent yourself.
3. **The CTO is your one voice.** You say what you want in plain words. It sets rules, starts workers and answers "what is going on". Keys are the fallback.
4. **The graph tells agents who to ask and what they may do.** Each agent has a charter (what it does, accepts, refuses, maturity). Edges say who depends on whom and how much trust a request gets. agent-net learns edges from real traffic and asks you only about major changes.
5. **Workers are cheap help that protects your main plan.** An agent hands a task to a worker on another model. The chooser picks one that is good enough and spends unprotected subscriptions first, so the model that runs your main sessions keeps its limits. Quotas shows where you stand.
6. **Your tools stay the same on every machine.** Skills and MCP servers match across machines, and you fix differences from one screen.

The sequence is: setup, then the essential getting-started info, then the TUI, then progressive learning inside the TUI. The only commands the owner is ever told are `agent-net setup` and the command that opens the TUI.

## How this was researched

- Read README, `docs/setup.md`, `docs/reference.md`, `docs/security.md` and all 32 files in `docs/decisions/`.
- Rendered every golden scene with `scripts/screens.sh` and looked at the Sessions, Needs you, Messages, Quotas, Graph, Tools, help and CTO scenes, and the goldens in `internal/tui/testdata/`.
- Built the binary and ran `agent-net setup --gateway --no-service --all` in a sandbox: a temporary `HOME` with `~/.pi`, `~/.config/opencode` and `~/.codex` folders, no `claude` on `PATH`, all five `AGENT_NET_*` variables pointed at temporary folders, and a private gateway on `127.0.0.1:17420`. The daemon step was read from `internal/setup/setup.go`, because `--no-service` skips it.

### What setup prints today (sandbox run, verbatim)

```
gateway service       skipped (no service)
gateway binaries      ok
join                  ok (node n_c8fa9b823b7e4bc4)
workers               accepted (refuse them with --no-launch)
daemon service        skipped (no service)
Connect your agents fully? Recommended:
  - Claude: accept messages from your other agents
  ...
Turn all on? [Y/n/c]
integrations:         7 of 7 on (saved for your network)
claude code           skipped (claude not found)
claude peer messages  skipped (claude not found)
codex mcp             ok (calls need no approval)
                      hint: Codex threads that were open before this need to be started again to get the agent-net tools
claude hint hook      skipped (claude not found)
...
quota-axi             ok (installed 0.1.69)
                      claude: not found
                      codex: not found
                      ... (19 lines, one per provider)
pi extension          ok
opencode plugin       ok
                      hint: OpenCode 2.2 loads new plugin files by itself; if the agent-net tools are missing in a session, restart its service when no session is busy: opencode service restart
claude guidance       ok
agent guidance        ok
opencode guidance     ok
codex guidance        ok
daemon status         skipped (no service)
```

With a real service the last line is `daemon status  connected to https://... as node n_...`. On a machine with Claude and no protected provider, setup also prints (`internal/setup/protected.go`): `... to protect it, run: agent-net workers protect anthropic`.

### Problems found while reading (fix them along the way)

| Problem | Where | Fix |
|---|---|---|
| Setup never says what agent-net is for or what it found. It ends on `daemon status`. | `internal/setup/setup.go` | Part A. |
| The quota step prints one `not found` line per provider: 19 lines of noise on a fresh machine. | `internal/setup/quota.go` | One line: `quota-axi  ok (0.1.69) - logged in: claude, codex` or `- no provider logins found yet`. |
| Setup tells the owner to run `agent-net workers protect anthropic`, a second command. | `internal/setup/protected.go` | Ask once in setup (Part D) or teach it in the TUI (step 5). Remove the line. |
| `install.sh` tells the owner to run `agent-net invite`, a third command, and there is no way to add a machine from the TUI. | `install.sh` lines 176 and 185 | New CTO tool `invite` (Part C, step 1b). Keep the command for scripts, stop telling the owner about it. |
| The name "Nicolas" is compiled into notes every owner's agents see: `CTONote`, `OverruleNote`, `WrittenApprovalNote("Nicolas", ...)`, the Pi and OpenCode plugins, and the guidance block. | `internal/protocol/trust.go:16`, `internal/protocol/outcome.go:87`, `internal/gateway/store_inbox.go:125`, `plugins/pi/agent-net.ts:96,106`, `plugins/opencode/agent-net.ts:99,109`, `internal/guidance/guidance.md:8` | Part B. |
| TUI help says `I  integrations for all machines: six checkboxes`. There are seven. | `internal/tui/help.go` | Say seven, or do not count. |
| In help, `n  desktop notifications: on (toggle)` sits above the `Sessions` heading, outside any section. | `help_120x40.golden` | Move it under a `Settings` or `Everywhere` heading. |
| The README has the "From a shell" block twice, with two different descriptions of the TUI. | `README.md` lines 205 to 225 | Keep one. |
| In a PTY recording, setup writes a terminal background query (`ESC ]11;?`) before its first line. Cause not checked; probably a colour library probing the terminal. | sandbox run | Setup output should use plain 16-colour codes and never query the terminal (Part A colours). |
| Bare `agent-net` prints usage and exits 2. | `cmd/agent-net` | Open the TUI on a terminal (Part A). |

The rejected draft's facts on these points were checked and are correct: the hardcoded name, the six-versus-seven help line, the stray help line, the protect hint, the invite line in `install.sh`, bare `agent-net` exiting 2, and the TUI colour roles in `internal/tui/theme.go`.

## Summary of the recommendations

1. **Setup ends with a short "What you have now" block** built from what setup and the daemon just found: machines, sessions by harness, projects, and the one thing to do first. Six numbered lines, one per point above, each with a real number or name from this machine. Then `Open agent-net now? [Y/n]`.
2. **Bare `agent-net` opens the TUI** (new). The owner learns two commands: `agent-net setup` and `agent-net`.
3. **The CTO title is asked once, in setup on the first machine**, default `CTO`. Stored on the gateway. Changed later in the CTO `m` form or by telling the CTO.
4. **Agents keep seeing the fixed word "CTO"**, and the owner's name leaves the binary: notes say "on your user's instruction". The chosen title is for the owner's screens only.
5. **Progressive learning is one line** in the CTO box, where the dim hint `write to the CTO: ...` is today. One step at a time, never a popup, never blocking, `>` skips it, done steps never come back.
6. **Seven steps follow the six points.** Each step teaches the why in one sentence, names a real session or project of the owner, and asks for one action. Most steps finish by watching the owner's real state, not by a click.
7. **Setup asks three things on the first machine** (integrations, CTO title, protect the orchestrator's subscription) and one thing on later machines (nothing, unless no network answer exists). Everything else waits for the TUI.

## Part A. The end of setup

### What it shows

After the step lines, three blocks:

1. **One result line.** `agent-net is running on <machine>.` in green, or `Setup did not finish: <step> failed.` in red.
2. **What you have now.** Six lines, one per point, each made concrete with this owner's data. A line whose data is empty says what will fill it, in one clause.
3. **Do this first.** At most two items the owner must do, with the exact action. Then the open question.

The step lines stay as they are, so docs and logs that quote them stay true. Only the quota lines shrink (see the problems table).

### Where the numbers come from

Setup already holds the daemon socket at its last step (`daemonStatus`). After it connects, setup calls the same owner-gated operator methods the TUI uses. No new gateway frame is needed.

| Line | Source | Today |
|---|---|---|
| Machines | `nodes` | exists |
| Sessions by harness, busy, waiting | `list` (online only) | exists |
| Harnesses found here | setup's own steps (claude, codex, pi, opencode) | exists |
| Projects seen, projects with no charter | `projects`, `graph_get` | exists |
| Logged-in providers, protected providers | the quota step's auth check, `protected_providers_get` | exists |
| Workers | `workers` | exists |
| Tool differences | `tools_differences` (CTO read tool, same computation as the Tools tab) | exists, needs an operator method for setup (new) |

The daemon needs a moment to discover sessions. Setup waits up to 3 seconds for the first `list` that is not empty, then prints what it has. Claude Code sessions and Codex threads appear at once (the daemon reads `~/.claude/sessions` and the Codex control socket). Pi and OpenCode sessions appear only after their extension or plugin loads, so setup counts them as "need a restart" when the harness was found but no session is listed.

### Mockup: first machine, 80 columns, with sessions found

Colour key, not printed: `{G}` green, `{Y}` yellow, `{R}` red, `{C}` cyan, `{F}` faint, `{B}` bold. Annotations are in the right margin.

```
daemon status         connected to https://192.168.1.10:7420 as node n_3f2a…

agent-net is running on laptop.                                    {G}{B}

What you have now                                                  {B}
1 See   4 sessions on laptop: 3 claude, 1 codex. 2 busy.           "1 See" {C}
        Your other machines appear when you add them.              {F}
2 Talk  Your agents can now ask each other. New sessions get the
        tools; open ones get them after a restart.                 {F}
3 CTO   Your CTO runs the network for you. Ask it in plain words.
4 Graph 8 projects seen. Their agents have no charter yet: the CTO drafts
        them with you.                                             {F}
5 Work  Workers use claude, codex. Your claude plan is protected.
6 Tools Skills and MCP servers are checked on every machine.

Do this first                                                      {Y}{B}
! Restart 2 pi sessions so they get the agent-net tools.           "!" {Y}

Open agent-net now? [Y/n]                                          "[Y/n]" {C}
```

That is 20 lines after `daemon status`. With the step lines above it, the owner scrolls back for detail, but the essentials fit one 80x24 screen.

Rules:

- **Each line has a label of one short word** (`See`, `Talk`, `CTO`, `Graph`, `Work`, `Tools`) in cyan, so the owner can scan the left edge. The same six words name the TUI steps in Part C, so the owner meets them twice.
- **Real names and numbers only.** No line describes a feature in the abstract when data exists. With data missing, the line says what fills it ("Your other machines appear when you add them").
- **Line 5 states the protection fact**: protected or not, by provider name. It is the one thing that can cost the owner money before they open the TUI.
- **"Do this first" lists only actions**, at most two, each one line: restart sessions that lack the tools, the `loginctl enable-linger` command when it applies, a failed step. Information never goes there.
- **The open question** appears only when stdin or `/dev/tty` is a terminal. Enter or `y` runs the TUI in place (`exec`). Without a terminal the line reads `Open agent-net: run agent-net` and setup exits 0.

### Mockup: first machine, nothing found yet

```
agent-net is running on laptop.

What you have now
1 See   No agent sessions yet. Start claude, codex, pi or opencode in
        a project: it shows up here by itself.
2 Talk  Your agents will be able to ask each other.
3 CTO   Your CTO runs the network for you. Ask it in plain words.
4 Graph No projects yet. Each project you work in becomes one.
5 Work  Workers can use: codex. Nothing to protect yet.
6 Tools Skills and MCP servers are checked on every machine.

Open agent-net now? [Y/n]
```

### Mockup: a machine that joins (invite line), 80 columns

The owner is often in an ssh shell on a VM, and the TUI may already be open on the laptop. So the block is shorter and the default answer is no.

```
agent-net is running on build-vm, with 1 other machine.            {G}{B}

What you have now                                                  {B}
1 See   2 sessions here: 1 pi, 1 opencode. They show on laptop too.
2 Talk  They can now ask your agents on laptop, and the reverse.
5 Work  build-vm takes workers. Harnesses: pi, opencode.

Do this first                                                      {Y}{B}
! Restart 2 sessions (pi, opencode) so they get the agent-net tools.

Open agent-net here? [y/N]                                         "[y/N]" {C}
```

Lines 3, 4 and 6 are left out: they are network-wide and were shown on the first machine. Line numbers stay the same, so the labels keep their meaning.

### Mockup: a failed run

```
daemon service        failed (systemctl --user start: exit 1)      {R}

Setup did not finish: daemon service failed.                       {R}{B}

Do this first                                                      {Y}{B}
! See why: journalctl --user -u agent-net-daemon                   cmd {C}
  Then run agent-net setup again.
```

No "What you have now" and no question: nothing works yet.

### Colours

Setup output is plain lines, often over ssh, sometimes piped to a log. It uses the 16 basic ANSI colours only, so the terminal's own theme keeps them readable on dark and light backgrounds.

| Role | Used for | Code |
|---|---|---|
| good | the result line when it worked | 32 green, with 1 bold |
| label | the six labels, keys, commands to copy, `[Y/n]` | 36 cyan |
| attention | "Do this first", the `!` marks | 33 yellow |
| error | a failed step, the "did not finish" line | 31 red |
| detail | second lines of a point | 2 faint |
| heading | "What you have now" | 1 bold |

- Colour is never the only signal: every role also has a word, a mark (`!`) or a position.
- No colour when `NO_COLOR` is set and not empty, when `TERM=dumb`, or when stdout is not a terminal.
- No background colours, no 256-colour or true-colour codes, no terminal queries.
- The step lines above keep their current plain form, except a failed step, which turns red.

Rejected:

- **A checklist end screen** ("Next steps: 1. open, 2. restart, 3. add a machine"), as in the first draft. It tells the owner what to do but not why agent-net is worth it.
- **Print the six points as fixed marketing text.** The owner would skip it. Real names and numbers make it worth reading.
- **Show nothing and rely on the TUI.** A failed or skipped TUI start leaves the owner with step lines only.
- **Match the TUI hex colours.** True colour is not reliable over ssh, and setup cannot know the background without a query.

### Bare `agent-net` opens the TUI (new)

On a terminal, `agent-net` with no arguments runs `agent-net tui`. Without a terminal it prints usage as today. This gives the owner exactly two commands. The README Quickstart and `docs/setup.md` say "then run `agent-net`".

Rejected: **a new `agent-net open` command.** One more word to learn for no gain.

## Part B. The CTO title

### Where it is asked

In setup, only when all of these hold:

- this run sets up the gateway on a fresh machine (`setup --gateway`, which the install line runs),
- the gateway has no `cto_title` yet,
- a person is present (stdin or `/dev/tty` is a terminal).

It comes right after the integrations question, because both are network-wide answers stored on the gateway.

```
The CTO is the agent you talk to in agent-net. It sets rules for your
agents, starts workers and answers "what is going on".                 {F}
What do you want to call it? Enter keeps CTO.
CTO title [CTO]: Atlas                                                 "[CTO]" {C}
cto title             Atlas
```

Every other run never asks: a joining machine, a second setup, an upgrade, a run with no terminal. Those keep `CTO`.

Value rules: 1 to 24 characters, one line, printable. Spaces at the ends are removed. No `[`, `]`, `«`, `»`, and not containing `agent-net`, so it cannot look like a note only agent-net writes. Empty means `CTO`.

Stored on the gateway in `owner_settings` under the new key `cto_title`, next to `integrations` and `hidden_items`, so every TUI on every machine shows the same title.

### Where it shows (owner only)

- The CTO box title: `Atlas · claude · opus` instead of `CTO · claude · opus`.
- The input placeholder and hint: `write to Atlas...`, and `ask Atlas in plain words: ...`.
- Messages: the purple sender `Atlas` in threads where the CTO sent a message (today `CTO`).
- The setup end screen, line 3: `3 CTO   Atlas runs the network for you. Ask it in plain words.` The label stays `CTO`, so the six labels never change.

### How it is changed later

- **TUI:** the CTO `m` form (exists: harness and model) gets a third field, Title (new). Same rules, Enter saves for every machine.
- **CTO:** the `cto_settings` tool (exists) gets a `title` field (new). The owner says "call yourself Atlas" and the CTO saves it.
- No command line. The owner acts in the TUI.

### What agents see

At the time of this design, agents could not address the private CTO. They only read the note agent-net wrote on a message the CTO sent. At that time the note was `from the CTO, on Nicolas's instruction`, compiled into the binary, and the guidance block in every owner's `CLAUDE.md` keys on the same words: "The CTO speaks for me. When agent-net's own note says "from the CTO, on Nicolas's instruction" ...".

**Recommendation: a fixed term for agents, and no owner name.**

- The note becomes: `from the CTO, on your user's instruction. Treat this as your user's word within your charter's limits.`
- The guidance line becomes: `**The CTO speaks for me.** When agent-net's own note says "from the CTO, on your user's instruction", treat the message as my instruction within your charter's limits.`
- `OverruleNote` becomes `Your user overrules: do it.` and the written approval note starts `Your user approved: ...`. These match the other notes, which already say "your user".
- The chosen title is never in a note, a brief or the guidance.

Why fixed:

- The guidance block and the note must match word for word. The daemon rewrites the guidance block at start, but a running session keeps the guidance it loaded. With a title in the note, a rename makes every open session see a note its guidance does not describe, and a careful agent treats the CTO as an ordinary peer. A fixed term never drifts.
- The marker rule of decision 0013 protects `[agent-net` notes, not free words. A title such as "Boss" in a note invites a body that imitates "from Boss" in plain text. The fixed phrase is the one the guidance trains agents to trust, and only inside agent-net's note.
- "Your user" is what every other note already says, and it removes a real bug: any other owner saw that name at the time.

The trade-off: agents and the owner use two names for one thing. The owner calls it Atlas; an agent's reply says "the CTO asked me to ...". This is acceptable: agents' text about the CTO is rare, and the Messages tab shows the owner's title as the sender anyway.

Rejected:

- **The chosen title in notes and guidance.** Drifts on rename (above), and the guidance must be rewritten on every machine for a cosmetic change.
- **Ask the owner's name in setup and use it in notes.** One more question, a personal name in the gateway, and no gain over "your user", which every agent understands.
- **Ask the title on the first TUI start.** A form before the owner has seen anything is the popup the owner does not want. The owner asked for it at first setup.
- **Ask on every machine.** The title is network-wide; asking again would be the repetition decision 0021 removed.

## Part C. Progressive learning in the TUI

### The element

There is exactly one onboarding element: **a step line in the CTO box**, in place of the dim hint line that is there today (`write to the CTO: it can set charters, start workers, answer what the network is doing`). The CTO box is visible on every tab, so the step line is too.

```
╭─CTO─────────────────────────────────────────────────────────────────────╮
│                                                                          │
│ 1/7 See  All your sessions, live. Enter steps into tenebrae.   > skip      │  "1/7 See" {C}, "> skip" {F}
│ > write to the CTO...                                                    │
╰──────────────────────────────────────────────────────────────────────────╯
```

Rules:

- **At most one step is visible**, and it is always this one line. No popup, no modal, no overlay, no second banner. The CTO conversation, when it has text, scrolls above it as today.
- **Never blocking.** Every key works as before. The line changes nothing about focus.
- **One key dismisses:** `>` skips the current step (new; `>` is not bound in the TUI today). A skipped step is never shown again.
- **Done steps never come back.** When all steps are done or skipped, the line returns to the normal CTO hint for good.
- **The line names the owner's real things:** a session name, a project, a provider. Never a placeholder.
- **The label matches the setup end screen** (`See`, `Talk`, `CTO`, `Graph`, `Work`, `Tools`), so the owner recognises it.
- **Colours** use the existing TUI roles from `internal/tui/theme.go` (adaptive light and dark values): the step number and label in the accent role, the text in the normal text role, `> skip` in the faint role. A step that answers something waiting (Needs you) uses the warning role for the session name, like the Needs you section.
- At 80 columns the line drops `> skip` first, then shortens the text with `…`. Help (`?`) lists `>  skip the onboarding step`.

Rejected:

- **A popup or tour on first start.** The owner said no; it also blocks the live view the TUI exists for.
- **A checklist panel or tab.** A second element, and a list of chores rather than a reason.
- **Hints in the bottom key line.** It is already full and drops keys on narrow terminals.
- **Hints in the header.** The header carries the live counts; a sentence there pushes them off.
- **Notes in each tab's empty state only.** Good as a complement (see below), but tabs the owner never opens would never teach.

### The steps

Seven steps, in the order of the six points. Step 1b exists only while the network has one machine. Each step is shown only when the previous one is done or skipped, and only when its condition holds. A step whose condition does not hold is passed over silently and checked again later; it is not marked skipped.

| # | Label | Shown when | Line (exact words; `<x>` is real data) | Done when |
|---|---|---|---|---|
| 1 | See | at least 1 online session | `All your agents, live, on every machine. Enter steps into <session>.` | The owner entered any session from this TUI (a `session_enter` plan ran and returned). |
| 1 | See | no online session | `No sessions yet. Start claude, codex, pi or opencode in a project: it appears here.` | The first online session appears (live fact). |
| 1b | See | only 1 machine, step 1 done | `agent-net shows every machine. Ask the CTO to add one: "add a machine".` | A second machine is listed in `nodes` (live fact). |
| 2 | Talk | step 1 done | `Agents ask each other instead of you. m sends <session> a message as you.` | The Messages tab was opened while it held an agent-to-agent thread, or the owner sent a message with `m` (TUI action). |
| 3 | CTO | step 2 done | `Say what you want in plain words. Try: "what is going on?"` | The first message sent to the CTO from this TUI (`cto_send`). |
| 4 | Graph | step 3 done, at least one agent with a live session has no charter | `The agent for <project> has no charter, so peers guess what it accepts. Ask the CTO: "draft a charter for the agent for <project>".` | That agent, or any agent with a live session, has a charter (live fact from `graph_get`). |
| 5 | Work | step 4 done or passed, Claude, Codex or another orchestrator provider is logged in, none protected | `Workers spend the cheapest good-enough plan. Protect <provider> so your main sessions keep their limit: 3, then p.` | `protected_providers_get` says configured (live fact). |
| 5 | Work | protection configured, no worker ever started on the network | `Hand a task to a worker: tell any agent "do this with a worker", or press s.` | A worker exists or existed (`workers` with `--recent`, or a `worker.started` event in the feed). |
| 6 | Tools | 2 or more machines, `tools_differences` lists at least one item | `Your machines differ in <n> skills or MCP servers. 5 fixes them from one screen.` | The owner applied a fix or accepted a difference on Tools (TUI action), or the count reaches 0 (live fact). |

Notes on the choices:

- **Step 1 teaches Enter, not a tab.** The owner confirmed that entering any session on any machine is the core of point 1. Needs you needs no step of its own: when a session waits, the existing Needs you section and the warning count are the lesson. While a Needs you row exists and step 1 is current, the line names the waiting session instead: `<session> waits for you. Enter answers it inside the session.` Same done rule.
- **Step 2 is done by watching or by doing.** Agents talking is something the owner sees, not something they set up. The step finishes the first time the owner looks at a real agent-to-agent thread, or uses `m`.
- **Step 3 makes the CTO the main path** for steps 4, 1b and 5. From step 3 on, every line offers the CTO first and a key second.
- **Step 4 asks for one charter, not all.** One drafted charter shows what a charter does. The CTO brief already tells it to ask the project's own agent to draft the charter and show a diff. Learned edges teach themselves: the header already shows `learned N`, and Graph marks `● N learned`.
- **Step 5 protects before it spends.** The protect line appears only when a provider worth protecting is logged in and nothing is protected. If setup already protected it (Part D), the step goes straight to the worker line.
- **Step 6 waits for a real difference.** With one machine, or no difference, there is nothing to teach yet, so the step stays hidden until there is.
- **Steps that touch the network** (charters, protection, workers, a second machine) finish from live facts, so they finish everywhere at once, even when the CTO or another machine did the work.

### Empty states (no new element)

Tabs that are empty today get one line of reason in their own body, written once and always shown when empty. This is not an onboarding element: it is what an empty screen should say anyway.

- Messages, no thread: `No conversations yet. When an agent asks another one something, the thread shows here.`
- Graph, no charter on any agent: `No charters yet. Ask the CTO to draft one for a project you work in.`
- Quotas, nothing protected: today's line `no provider protected · select a subscription and press p` stays.
- Tools, one machine: `One machine: nothing to compare yet.`

### What is tracked and where

Two kinds of completion:

1. **Facts** (a session exists, a charter exists, protection is configured, a worker existed, a second machine, a difference count). They are read live from the data the TUI already polls (`list`, `nodes`, `graph_get`, `protected_providers_get`, `workers`, `events`, the Tools differences). They are never stored, so they cannot go stale.
2. **Owner actions in this TUI** (entered a session, opened a Messages thread or used `m`, sent to the CTO, fixed or accepted a Tools difference) and **skips**. These are stored.

Storage, per machine, in the daemon's local database `daemon.db`, next to the existing `owner_preferences` table (which already keeps the per-machine desktop notification switch):

```sql
CREATE TABLE IF NOT EXISTS onboarding (
  step TEXT PRIMARY KEY,      -- 'see', 'see-machine', 'talk', 'cto', 'map', 'work-protect', 'work-worker', 'tools'
  state TEXT NOT NULL,        -- 'done' or 'skipped'
  at INTEGER NOT NULL         -- Unix seconds
);
```

Two new owner-gated operator methods, `onboarding_get` (returns the rows) and `onboarding_set` (`{step, state}`), behind the same `operatorGate` as `hidden_get` and `notification_settings`. Agents cannot read or write them. They are operator methods, not TUI-local files, so the phone app (decision 0006, P1) can show the same step later.

Why per machine: the owner asked for it, and the TUI on a laptop and the TUI on a VM are used in different ways (an ssh shell on a VM rarely enters sessions). Facts are network-wide anyway, so the network-level steps finish everywhere without sharing the table.

Reset: no key. The owner can ask the CTO "show me the getting-started steps again", which calls `onboarding_set` with an empty state for every step through a new CTO tool `onboarding_reset` (new). This is an open question below.

Rejected:

- **Store progress on the gateway.** The owner asked for per machine; a gateway row would also finish a step on the VM because the owner did it on the laptop.
- **A file in `~/.config/agent-net`.** The daemon database already holds per-machine owner preferences, and an operator method keeps the phone in step.
- **Mark a step done when its line was shown.** Seeing is not doing. The owner learns by doing.

## Part D. What setup asks, and what waits

Setup asks only what must be right before the owner opens the TUI, or what the owner asked to answer at setup. Ranked:

| Rank | Question | Where | Why in setup |
|---|---|---|---|
| 1 | Integrations: `Connect your agents fully? [Y/n/c]` (exists) | first machine; later machines apply the network answer without asking | Without them, Needs you, hints, peer messages and quotas do not work, and the TUI looks empty. Already one question for seven items (decision 0021). |
| 2 | `Keep workers off your <provider> plan, so your main sessions keep their limit? [Y/n]` (new question; replaces the `agent-net workers protect anthropic` line) | first machine, only when Claude Code or Codex is found and logged in and nothing is protected | An agent can start a worker before the owner reaches step 5. Without protection the chooser may spend the plan the orchestrator needs. One yes protects it from minute one. |
| 3 | CTO title (new) | first machine, fresh gateway, terminal only | The owner asked to choose it at first setup. Cosmetic, so it comes after the two questions that change behaviour. |
| 4 | `Open agent-net now? [Y/n]` (new; `[y/N]` on a joining machine) | every machine with a terminal | It is the hand-off to the TUI, where the rest is learned. |

Waits for the TUI, and where it is learned:

| Item | Where | Why it waits |
|---|---|---|
| Charters and edges | step 4, the CTO, Graph | Needs the owner's judgement per project; the CTO drafts from the project's own agent. Edges are learned from traffic. |
| Adding machines | step 1b, CTO `invite` tool (new) | Needs the TUI open to see the result; works any time. |
| Worker profiles and difficulty | Quotas Workers panel, the CTO | The chooser works with no roster (it uses the harness default and says how to register one). |
| Request rules, peer trust (`t`), delegation policy (`p`) | the CTO, the keys | Only useful once agents talk; the owner sees why in Messages first. |
| Tool differences | step 6, Tools | Need two machines and a scan. |
| Machine names (`N`), project names (`D`) | keys, the CTO | Cosmetic; the owner sees the long name first and wants to change it. |
| Desktop notifications | help `n` | On by default. |
| Quota logins that need attention | Quotas tab | Setup prints one summary line; the fix happens per provider. |

Rejected:

- **Ask nothing new.** Leaves the protect gap of rank 2, and the owner asked for the title at setup.
- **Ask for a charter or a worker model in setup.** The owner has not seen the TUI or their sessions yet; any answer would be a guess.
- **Ask about ssh for Enter on remote machines.** ssh is the default and falls back to a printed command (decision 0002 W16); nothing to ask.

## Open questions for the owner

1. **Protect at setup (Part D rank 2):** ask the yes/no question, or only teach it in TUI step 5? The design recommends asking, because a worker can spend the plan before the owner reaches step 5.
2. **Step order:** is "Graph before Work" right, or should protection (step 5) come right after the CTO step, because it protects money?
3. **Reset of onboarding:** is a CTO tool enough, or do you want a help-screen key?
4. **Owner name in notes:** "your user" everywhere (recommended), or do you want your name kept for your own network?
5. **Bare `agent-net`:** fine to make it open the TUI, or keep `agent-net tui` as the only way?
6. **The `>` key:** acceptable for "skip", or prefer another key?

## Build plan

Each part is one worker run, with the failing test first. Parts 1 to 3 can run in parallel; 4 needs 3; 6 needs 5.

1. **Owner name out of the notes.** `CTONote`, `OverruleNote`, `WrittenApprovalNote` callers, the Pi and OpenCode plugin copies, `guidance.md`. Test: no `Nicolas` in any delivered note or guidance text; the existing Go test that compares the TS copies still passes. Update `docs/security.md` (notes list) and decision 0022's note examples by a dated addendum.
2. **Small TUI and doc fixes.** Help says seven integrations; the `n` notifications line moves under a heading; README "From a shell" block once. Golden updates for help.
3. **Setup output.** Quota step in one line; colours module for setup (16 colours, `NO_COLOR`, `TERM=dumb`, not a terminal, no terminal query); the result line; "What you have now" from operator calls with the 3-second wait; "Do this first"; the open question with `exec` of the TUI; joining-machine and failure variants. Tests: golden outputs for the four mockups with a fake daemon socket, with and without colour.
4. **Setup questions.** Protect question (replaces the hint in `protected.go`); CTO title question and `cto_title` in gateway `owner_settings` with its rules; `install.sh` stops printing `agent-net invite`. Tests: asked only on a fresh gateway with a terminal; never asked on join, rerun, upgrade or without a terminal; value rules.
5. **CTO title in the TUI and the CTO.** Title field in the `m` form; `title` in `cto_settings`; box title, placeholder, hint and Messages sender use it. Goldens at 80 and 120 columns.
6. **Onboarding steps.** `onboarding` table and `onboarding_get`/`onboarding_set` operator methods with the owner gate; the step engine in `internal/tui` (pure function: snapshot plus stored rows to one step line), the line in `cto_view.go`, the `>` key, done detection for every row of the steps table; empty-state lines. Tests: one table test per step for shown, passed over and done; a golden per step at 80 and 120 columns; a test that no step is ever shown twice after done or skipped.
7. **Add a machine from the CTO, and bare `agent-net`.** CTO tool `invite` (runs on the gateway machine's daemon, returns the install lines, shown in the chat); `agent-net` with no arguments on a terminal runs the TUI. Update README Quickstart and `docs/setup.md` to name only `agent-net setup` and `agent-net`.
