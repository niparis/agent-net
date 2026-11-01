# Start sessions from agent-net

## Fit with the primitives (decision 0036)

This design builds on agents, sessions, workers, workspaces and authority. Its owner-only session start conflicts with autonomy set to wake, which lets agent-net wake an agent. The owner must decide the boundary; see the open question below.

**Date:** 2026-10-31
**Status:** Design for the owner to review. Nothing here is built.
**Asked for:** "Today many of our agents are sessions that I start myself in a
terminal. Could we have agents started directly from agent-net? That would
clean up my screen clutter." And: "it has to be designed. It becomes really an
interface problem."

## The goal in one test

A full working day with one terminal window: the agent-net TUI. The owner
starts, enters, leaves, resumes and ends every agent session from it. No
terminal tab is opened to start an agent, on any machine.

agent-net already does two thirds of this. It sees every session on every
machine, and Enter steps into any of them (decision 0032), with ctrl+] to
leave while the session keeps running. What is missing is the first step:
starting one. This design adds that step and the last one, ending.

## How this was researched

- Read README, `docs/reference.md` (Workers, the TUI screen, Enter a session,
  Needs you, the CTO), decisions 0002, 0005, 0022, 0024, 0031 to 0035,
  `docs/design/keys-audit.md`, `docs/design/cto-as-agent.md` and
  `docs/design/onboarding.md` (branch `agent-net/w_7348270c5e5ea122`).
- Read the launch code: `internal/daemon/workers.go`, `worker_claude.go`,
  `worker_opencode.go`, `worker_codex.go`, `worker_pi.go`, `enter.go`,
  `herdr.go`, `codex_peers.go`, and the start form in `internal/tui/forms.go`.
- Rendered the golden scenes with `scripts/screens.sh` (Sessions, worker
  tree, start form, Graph agent card, Graph, offline filter).
- Checked the installed harness help on this machine, read-only: Claude Code
  2.1.316, Codex 0.163.0, OpenCode 2.2.0, Pi 1.4.0, herdr 0.8.0, tmux.

## Summary of the recommendations

1. **An owner session is not a worker.** It is yours, long-lived, in the
   project folder, with no brief, no hand-back and no automatic end. It reuses
   the worker launch code, not the worker rules.
2. **Resume is the most common case, and it needs no new key.** Enter on an
   offline row starts that conversation again in the background and enters
   it. This covers "I killed the last session of this project" when the owner
   wants the context back.
3. **One new key, `n`, opens one form: "New session".** It works wherever a
   project or a session is selected: Sessions and Graph. Agents start workers
   through `agent_start`; the CTO proposes worker starts for owner approval.
4. **The form has five lines**: Project, Harness, Model, Folder, First
   message. Everything is filled from the selection, so the usual start is
   `n` then Enter.
5. **First message decides what happens next.** Empty: start and enter at
   once (you came to talk). Filled: start in the background, send it, stay in
   the TUI (you came to delegate and keep watching the board).
6. **Each harness runs in its own background home**, not in a terminal
   agent-net holds: Claude's background service, the owner's OpenCode
   service, the shared Codex app server. Pi has none, so agent-net hosts it in
   a private tmux server. All of them outlive a daemon restart.
7. **A second session on a busy folder defaults to a new worktree**, with the
   reason on screen. Two sessions in one working tree can overwrite each other.
8. **"New project" is a path that agent-net does not know yet.** Type a path:
   an existing folder is registered, a missing one is created with
   `git init` after a confirm. Cloning goes through the CTO. No templates.
9. **The CTO proposes a start; the owner approves with one key** (decision
   0035). It is the best place for a new project, because the CTO can also
   draft the charter.
10. **`x` ends any session agent-net can end cleanly**, and says that the
    conversation is kept. No automatic idle end in the first version.

## What an owner session is

| | Worker (today) | Owner session (new) |
|---|---|---|
| Owner | the session or CLI that started it | you, the person |
| Who may start it | you, any agent, the CTO by proposal | you only, the CTO by proposal |
| Purpose | one task, then a report | open-ended work with you |
| Brief | fixed worker brief | none: a normal session |
| First message | the task, required | optional, sent as your own words |
| Folder | own worktree by default | the project folder by default |
| Permissions | sandbox level, never asks | the project's own settings; it asks you |
| Ends | hand-back, 60 min idle, 4 h, owner gone | when you end it |
| After the end | worktree kept or cleaned, record kept 4 h | conversation kept; Enter resumes |
| Model default | the difficulty chooser | the harness default, or what you pick |
| Name | `<project> worker <n>` | the project name, `<project> 2` (0024, 0033) |
| In Sessions | indented under its caller | a normal row in its machine group |
| Daemon restart | adopted again or ended as lost | nothing to adopt: the harness owns it |
| Machine load | admission queue | starts at once; a warning when room is not ok |

The reason for each difference is the same: an owner session is a session the
owner would otherwise have opened in a terminal. It must behave exactly like
one, minus the terminal.

## Situations

The owner named two situations. Both are real, but the first one hides two
different wishes, and the second one hides four different meanings.

| # | Situation | What the owner wants | Served by |
|---|---|---|---|
| 1 | The session of a project was killed or crashed, and the owner wants it back with its context | the same conversation | Enter on its offline row: resume |
| 2 | A known project has no live session, and the owner wants a clean start | a fresh conversation | `n` on the project |
| 3 | A project is busy and the owner wants a second session on it | a parallel session that does not clash | `n` on the busy row; Folder defaults to a new worktree |
| 4 | The owner wants a branch of work apart from the main tree | a session in a new worktree | `n`, Folder: new worktree |
| 5 | The project lives on another machine | the same start, run there | `n`; the machine comes from the project |
| 6 | A folder exists but agent-net has never seen a session there | a session in it | `n`, Project: the path; it is registered |
| 7 | A new idea, nothing on disk yet | a folder, a git repository, a session | `n`, Project: a new path; or ask the CTO |
| 8 | A repository to clone | a clone and a session | ask the CTO |
| 9 | Hand a long, open job to an agent the owner will check in on later | a session that starts working at once | `n` with a First message |
| 10 | After a reboot, the usual set of sessions is gone | the same sessions back | Enter on each offline row; see open question 6 |

Challenges to the owner's framing:

- **"Existing project / agent" is two things.** In agent-net the lasting
  identity of an agent is its chartered Graph node. A project is its home, and a conversation is session context.
  "The agent" usually means "the conversation I had", which is situation 1.
  "The project" with a clean slate is situation 2. They need different
  actions: one resumes, one starts fresh. Merging them into one form would
  force a "resume which conversation?" question on every start.
- **Situation 1 is the most frequent, and it should cost one key.** The
  offline row is already on screen (`o` shows offline sessions). Enter means
  "go into it" (decision 0032). Going into an offline session means starting
  it again. No form is needed.
- **Situation 3 matters more than it looks.** Screen clutter comes from many
  sessions, and many sessions on one project is common. The danger is not the
  start but two sessions editing the same files. The form must show it.
- **"New project" does not belong in this feature as a separate entry
  point.** It is a project that does not exist yet, and starting a session
  is the second step. The form accepts a path, so situations 6 and 7 are the
  same form with one confirm line. Cloning (8) needs a URL, a target folder
  and network access on the target machine: rare enough to leave to the CTO,
  which can also draft a charter and the first message at the same time.
- **Situation 9 is not a worker.** A worker ends after 60 idle minutes, works
  in a sandbox and reports to its caller. An owner who wants an agent to
  "work on the import pipeline today and I will look in from time to time"
  wants an owner session with a first message.

Situations left out:

- **Start a worker.** Agents use `agent_start`; the CTO proposes a start for
  owner approval. The owner TUI has no worker-start form.
- **Start an architect session (`role: architect:<name>`, decision 0022).**
  Later, through the CTO, once architects are built.
- **Fork a conversation** (Claude `--fork-session`, Pi `--fork`). Useful but
  rare; a later field, not a first-version need.

## Where the owner starts a session

The owner imagined three places: tab 1, tab 6 and the CTO. This design keeps
all three, adds tab 4, and makes them one form behind one key, so the count
of places costs nothing to learn.

| Place | How | Why it is there |
|---|---|---|
| Sessions (tab 1), any row | `n`: form filled from the row's project, machine, harness | The main board. The owner sees what runs and starts what is missing. |
| Sessions, offline or dormant row | Enter: resume and enter | Situation 1 with no form. |
| Graph (tab 4), agent card | `n`: form filled from the project | The card says `○ no live sessions`. That is exactly when the owner wants one. |
| Graph (tab 4), selected node | `n`: form filled from the node | The owner asked for it. It costs one key handler because the form is shared. |
| CTO | the owner asks in words; the CTO proposes; `a` approves in Needs you | New projects, clones, "start a session on X and tell it Y". |

Places rejected:

- **Messages, Quotas, Tools.** No project is selected there.
- **A CLI command (`agent-net new`).** The owner acts in the TUI only. A
  command would be a second way to learn. `agent-net start` stays for
  workers and scripts.
- **The phone.** Its plan mirrors TUI actions and is paused. When it resumes,
  the same `session_start` method serves it, as `session_enter` does now.

Graph: Enter on a Graph node opens its card, and the Graph card is
where a project's live sessions are listed. If the owner wants fewer places,
keep the card handler and avoid duplicating it on the network view. The Graph key line is already full,
so `n` appears there only in `?`.

### Why `n` starts an owner session

`n` opens this form on Sessions and Graph. Workers have a different owner and
lifetime, and are started by agents or through CTO proposals for owner
approval. The owner TUI has no worker-start form.

Inside the CTO box, `n` stays "new CTO conversation": the box has focus then,
so the two actions do not meet.

## The form

The form takes the place of the session list.
The CTO box and the machines box stay visible.

### Fields

| Field | What it takes | Default |
|---|---|---|
| Project | a known project, `name@machine`, or an absolute path (`~` allowed) | the selected row's or node's project |
| Harness | `claude`, `codex`, `opencode`, `pi`; only those the machine has | the selected session's harness; else the harness of the last session in that project; else the first of claude, codex, opencode, pi the machine has |
| Model | completes from the machine's model list for that harness | empty, shown as dim `default`: the harness's own default |
| Folder | left and right cycle: `project folder`, `new worktree` | `project folder`; `new worktree` when a live session already works in that folder |
| Branch | only when Folder is `new worktree`; free text | `agent-net/<project>-<n>`, the lowest free number |
| First message | free text, optional | empty |

The machine is not its own field. It comes from the project: a project on two
machines is offered as `name@machine`. A
path is a path on the selected row's machine, and `path@machine` names
another one.

The model is not the worker chooser. The difficulty chooser exists to keep
workers off the owner's own plan. An owner session is that plan. So the
default is whatever the harness would use if the owner typed `claude` in a
terminal, and the list completes loose names (`opus`, `deepseek 4.1`).

No Name field. The automatic name (decisions 0024 and 0033) is the project
name with a number, and the TASK column shows their announced tasks to tell two sessions apart once the
agent announces its task label. See open question 2.

Keys inside the form: up and down or tab move between fields, tab also
completes, left and right change Folder, Enter on the last field starts, Esc
cancels. Enter on any earlier field moves on.

### Mockup: `n` on an idle session, 80 columns

The owner selected the `tenebrae` row (claude, idle) on Mac and pressed `n`.
Focus starts on First message, because every other field is filled.

```
1 Sessions  2 Messages  3 Quotas  4 Graph  5 Tools │ 9 online · 2 busy
╭─Machines─────────────────────────────────────────────────────────────────────╮
│  ● Mac (here) v0.5.0 cpu ██░░░░░░ 21% of 10 mem ██░░ 9.1/16G                 │
│  ● build-vm   v0.5.0 cpu █░░░░░░░  8% of 4  mem █░░░ 2.0/8.0G                │
╰──────────────────────────────────────────────────────────────────────────────╯
New session ────────────────────────────────────────────────────────────────────
  Project        tenebrae                          on Mac, ~/code/tenebrae
  Harness        claude
  Model          default                           opus, sonnet, haiku, fable
  Folder       ◂ project folder ▸                  no other session here
> First message
                 empty: start it and enter it
                 text: start it in the background and send this

╭─CTO──────────────────────────────────────────────────────────────────────────╮
│ ...                                                                          │
│ enter: open the CTO · ctrl+]: back                                           │
╰──────────────────────────────────────────────────────────────────────────────╯
enter start  up/down field  left/right folder  esc cancel
```

The two dim lines under First message are the whole lesson of the form. They
change to one line once the owner types: `enter: start in the background and
send this`.

### Mockup: `n` on a busy session

A live session already works in `~/code/agent-net`, so Folder flips to a new
worktree and says why. Left or right sets it back.

```
New session ────────────────────────────────────────────────────────────────────
  Project        agent-net                         on Mac, ~/code/agent-net
  Harness        claude
  Model          default
  Folder       ◂ new worktree ▸                    agent-net is busy in the
                                                   project folder
  Branch         agent-net/agent-net-2             from main (HEAD)
> First message  Refactor the outbox retry into its own file. Keep tests gr…
                 enter: start in the background and send this
```

### Mockup: a path agent-net does not know

```
New session ────────────────────────────────────────────────────────────────────
  Project        ~/code/garden-planner             on Mac
                 ! new folder: it will be created with git init
  Harness        pi
  Model          openai/gpt-6.1                    pi default
  Folder         project folder                    a new folder has one tree
> First message  Set up a Go module and a README for a garden planner app.
                 enter: create the folder, then start
```

For an existing folder that is not a known project, the note line reads
`not a known project yet: it will be registered` (the registry of decision
0005). For an existing folder that is not a git repository, it reads
`not a git repository: no worktrees, no branch shown` and the session still
starts. A path outside the home folder of the machine's user is refused.

Creating a folder asks once more, on the result line: `Create
~/code/garden-planner on Mac and run git init? y/n`. That is the only extra
confirm in the form.

### Mockup: a project on another machine

```
New session ────────────────────────────────────────────────────────────────────
  Project        homelab@build-vm                  on build-vm, ~/src/homelab
  Harness        opencode                          build-vm has: opencode, pi
  Model          opencode-go/deepseek-v4.1
  Folder       ◂ project folder ▸                  no other session here
> First message
                 empty: start it and enter it (through ssh to build-vm)
```

The note says how Enter will reach it, so an ssh password prompt is not a
surprise. Enter on another machine uses the existing `session_enter` plan
(`ssh -t`, or `herdr --remote`).

### What happens on Enter

1. The TUI sends `session_start` to the local daemon, which runs it, or
   relays it through the gateway to the machine that holds the folder.
2. The form closes. A row appears at once in the machine group with STATUS
   `start` and the TASK `starting claude in tenebrae`.
3. With no first message, when the session is registered, the TUI runs its
   `session_enter` plan, the same terminal transfer Enter does today. ctrl+] returns
   to the TUI. With a first message, the TUI stays where it is and the result
   line says `started tenebrae 2 (claude) on Mac: first message sent. Enter
   opens it.`
4. If the start fails, the row goes and the result line says why, in the
   harness's own words where possible (`Claude Code does not trust
   ~/code/foo`).

## Resume: Enter on an offline row

Today Enter on an offline row shows a note, because there is nothing to
attach to. With this design, Enter asks on the result line:

```
  NAME         AGENT    PROJECT   MODEL      STATUS TRUST   CPU   RAM TASK
 Mac (here) 4 sessions ─────────────────────────────────────────────────────────
  agent-net    claude   agent-net opus-5-5   busy   -       12%  610M Outbox…
▸ tenebrae     claude   tenebrae  opus-5-5   off    -         -     - Charter…
  strategies   claude   strategi… sonnet-5-5 idle   -        1%  420M
  web          opencode shop      sonnet-5   idle   -         -     -
────────────────────────────────────────────────────────────────────────────────
Resume tenebrae (claude, last active 3h ago) and enter it?  enter: yes  esc: no
```

Why a confirm here and not elsewhere: an offline row can be weeks old, and
Enter is pressed often. The confirm costs one more Enter, and it says how old
the conversation is. See open question 7.

Resume keeps the conversation, the folder and the model. It does not keep the
old name number: the session gets the lowest free name when it registers
(decision 0024 rule 2). If the folder is gone, the result line says so and
offers nothing else.

## A started session in Sessions

An owner session started from agent-net is a normal row. It is not indented,
and it has no worker marks. The detail pane adds one line:

```
tenebrae 2  claude  idle  cpu 1%  ram 430M  procs 3
  id        s_4be0…  native 7c1e2a90…  job 7c1e2a90
  folder    ~/code/tenebrae
  branch    main
  model     opus (default)
  started   from agent-net on Mac at 14:02, in Claude's background service
  function  Document and evidence platform for the WBG archive.
```

The `started` line names where the session lives, so the owner knows that
closing the TUI or restarting the daemon does not end it. The header gains no
new count. The existing `a` filter (active only) hides idle sessions, which is
where forgotten background sessions would pile up.

### How it ends

| Way | What happens |
|---|---|
| Inside the session | `/exit`, ctrl+d, or the harness's own quit. The row goes offline. |
| `x` on the row | `Stop tenebrae 2? The conversation is kept: Enter on its offline row resumes it. y/n`. Then the harness's own stop (table below). |
| A daemon restart | Nothing. The session belongs to its harness's background home. |
| A reboot | Every session ends. Their rows are offline; Enter resumes each. |

Today `x` refuses every session that is not a worker. It changes to: stop a
worker as today; stop a session agent-net can end without killing a terminal
(Claude background sessions, OpenCode service sessions, sessions in the
agent-net tmux host); refuse an interactive session in the owner's own
terminal, with the note `tenebrae runs in your terminal on Mac: end it
there`. Killing a terminal the owner sees is never right.

No automatic end in the first version. A worker ends because nobody watches
it. An owner session is watched by its owner, and a surprise end loses the
owner's place. The cost of a forgotten session is memory, and the Machines
box already shows memory per machine. See open question 1.

## Harness by harness

"Start in the background and attach later" means four things:

1. a process that runs without a terminal and is not a child of the TUI;
2. a way to enter it with a screen and leave it running;
3. it survives a daemon restart;
4. its conversation can be opened again after it ends.

| | Claude Code | OpenCode | Codex | Pi |
|---|---|---|---|---|
| Background home | Claude's background service: `claude --bg` | the owner's OpenCode service | the shared Codex app server (`daemon_auto_start`) | none; agent-net's private tmux server |
| Start | `claude --bg --name <project> [--model M]` in the folder | `POST /api/session` on the service, with the folder in `x-opencode-directory` | start-and-enter: `codex [-m M] [prompt]` in the folder, as an enter plan | `tmux -L agent-net new-session -d -c <folder> -- pi --session-id <uuid> --name <project> [--model M]` |
| First message | sent to the session inbox as an owner message (worker path) | `POST /api/session/{id}/message` | the prompt argument; the TUI enters anyway (see below) | an owner message through the Pi extension (worker path) |
| Enter | `claude attach <job>` (exists) | `opencode --session <id>` (exists) | `codex resume <thread>` (exists) | tmux pane plan (exists, `findTmuxPane`) |
| Survives daemon restart | yes, not a daemon child | yes, the service owns it | yes, the app server owns it | yes: tmux forks away, and the unit has `KillMode=process` |
| Resume after end | `claude --bg --resume <session id>` (exists for workers) | the service keeps every session: Enter is enough | `codex resume <thread>` (exists) | `pi --session <id>` in a new tmux window |
| Stop | `claude stop <job>` (exists) | abort the turn; an idle service session costs nothing | close the window; the server unloads an idle thread | kill the tmux window |
| Folder trust | must be trusted; `claudetrust.Set` (exists) for folders agent-net created, a form note otherwise | none | Codex's own trust prompt, shown when entered | none |
| What exists today | almost all, in `startClaude` | session create and prompt, in the worker code against a private server | enter and discovery | the tmux enter plan |
| What is new | drop the worker settings, the worker env and the MCP override | find the owner service's address and password | the start plan; nothing headless | the host and its launcher |

Notes per harness:

- **Claude Code.** `startClaude` already does the start, the job ID, the
  session file and the binding. An owner session drops `AGENT_NET_WORKER`
  and the `--mcp-config` override, so it loads the owner's own MCP servers
  and settings exactly as a terminal session would. Open check: whether
  `claude --bg` accepts the first message as a prompt argument, which would
  make it the owner's own first turn instead of an inbox message. Either way
  works; the argument is better.
- **OpenCode.** The owner's sessions already live in the OpenCode service
  (decision 0011), and `opencode --session <id>` opens one through it. The
  daemon must reach the service API. `opencode api` is a CLI that talks to
  the running service, and the service writes its address to its
  `service.json`; the start uses one of them. If the service is not running,
  the form says `the OpenCode service is not running on Mac` and the start
  is refused. The daemon does not start the owner's service on its own,
  because that service hosts every OpenCode session (the same rule setup
  follows).
- **Codex.** The shared app server drops a thread with no window and no
  activity, and the adapter is read-only on purpose: it never subscribes, so
  it never receives approval requests (`codex_peers.go`). A headless Codex
  start with a first message would run a turn whose approvals nobody can
  answer. So, in the first version, a Codex start always enters, with the
  first message as the prompt argument. When the owner leaves with ctrl+],
  the window closes; a running turn goes on in the app server, and an idle
  thread shows `dorm` until Enter resumes it. A headless Codex start (the
  private app server of a worker, run with approvals sent to Needs you) is a
  spike, not a promise.
- **Pi.** Pi has no background mode with a screen: `--mode rpc` has none,
  and the plain mode needs a terminal. agent-net gives it one: a private
  tmux server per machine (`tmux -L agent-net`), one session per Pi session,
  invisible unless entered. Enter already plans a tmux attach for a process
  in a tmux pane. If tmux is missing, the form says `pi needs tmux on Mac to
  run in the background` and refuses.

Why not a tmux host for every harness: it would give up Claude's attach and
agent view, run OpenCode and Codex outside the shared servers that the
owner's other windows use, and make tmux a hard dependency. A native
background home is always better where one exists.

Why not herdr for Pi: the owner uses herdr, and herdr can create tabs and
start agents in panes (`herdr tab create --cwd`, `herdr agent start --kind
pi`). But a herdr tab is exactly the clutter this design removes, and herdr
is not on every machine. A named herdr session (`herdr --session agent-net`)
could replace tmux where herdr is present. See open question 3.

## What is kept from workers and what differs

Kept, as code:

- the launchers' start steps per harness, the session binding (session file,
  session ID, chosen UUID) and the wait until the gateway knows the session;
- the project lookup, `name@machine`, path matching, and the relay to the
  machine that holds the folder;
- model resolution against the machine's list, with loose names;
- worktree creation from `base`, and the branch naming helpers;
- the enter plans of every harness, ssh and herdr remote included;
- the start result shape (`status: starting`, then a notice) so the TUI shows
  a row before the session registers.

Not kept:

- the brief, `.agent-net/worker.md`, hand-back, the completion notice and its
  checklist;
- the sandbox level (decision 0020): the owner session asks the owner;
- idle end, the 4-hour limit, owner-gone end, the admission queue and the
  worker count;
- the cleanup of worktrees and the "unfinished work" commit: an owner
  worktree is the owner's, kept until the owner removes it;
- the difficulty chooser, protected providers and the track record: they are
  rules about delegated work;
- the worker name and the indent under a caller.

New:

- an owner-only operator method `session_start`, and a gateway frame that
  relays it to the machine that holds the folder; the target daemon accepts
  it only with the owner flag, as `worker.kill` does with `as_owner`;
- a small record per started session (machine, harness, native ID, folder,
  branch, started time) so the detail pane can say where it lives and `x`
  knows how to stop it. It is not a worker row and has no end rules;
- `session_resume` for offline rows, per harness;
- the Pi tmux host.

## The CTO

Under decision 0035 the CTO never runs an owner change; it proposes, the
owner approves with one key, and the daemon runs the stored call. Starting a
session is such a change. The CTO's change tool gets two actions:
`session_start` (the form's fields) and `project_create` (a path, and
optionally a git URL to clone).

The proposal shows in Needs you:

```
╭─Needs you────────────────────────────────────────────────────────────────────╮
│ ▸ CTO proposes  new session: claude in garden-planner on Mac          2m     │
│                 create ~/code/garden-planner (git init), then send:          │
│                 "Set up a Go module and a README from the notes below…"      │
│                 a approve · r decline · enter details                        │
╰──────────────────────────────────────────────────────────────────────────────╯
```

After `a`, the daemon creates the folder, registers it, and starts the
session with the first message, in the background. It never enters the
session for the owner: the owner approved from a list and may be busy. The
result line says `started garden-planner (claude) on Mac. Enter opens it.`
The CTO gets its one-line outcome note.

A CTO proposal always carries a first message. A start with no message from
the CTO has no purpose the owner could not get faster with `n`.

The CTO is the right place for situation 8 (clone) and the best place for
situation 7, because in the same conversation it can propose the charter
for the new node. It is not the right place for situations 1 to 4: one key
in the TUI beats a sentence plus an approval.

## Security

- `session_start` and `session_resume` are owner-only, behind the owner gate,
  like `session_enter`. Agents never get them: an owner session has no
  sandbox, no time limit and the owner's full rights, so an agent that could
  start one could escape every worker limit. The CTO only proposes.
- A path is checked by the daemon of its machine: it must be absolute after
  `~` is expanded, inside the home folder of that machine's user, and not a
  symbolic link out of it. Folder creation needs the owner's `y`.
- agent-net sets Claude folder trust only for folders it created on the
  owner's command. An existing untrusted folder shows a note, and the start
  asks `Trust ~/code/foo for Claude Code? y/n` on the result line: the
  owner's explicit choice.
- Starting on another machine gives the owner nothing that ssh to that
  machine does not already give. The gateway relays; it never runs anything.
- `docs/security.md` must list the new methods and the path rule.

## Risks

| Risk | Answer |
|---|---|
| Background sessions are forgotten and use memory and quota | The rows stay in Sessions with idle age; the Machines box shows memory. Open question 1 asks about an automatic stop. |
| A background session waits on a permission prompt nobody sees | Needs you shows waiting sessions of every harness (decision 0016). A first-message start that stops to ask shows there. |
| Two sessions edit the same working tree | Folder defaults to a new worktree when the folder is busy, and says so. |
| Enter on an old offline row starts a process by accident | The resume confirm, with the last active time. |
| Codex turns that need approval with no window | No headless Codex start in the first version. |
| The OpenCode service is down | The start is refused with the reason; the daemon never starts the owner's service. |
| tmux is missing for Pi | The form refuses with the reason. |
| The started process is not matched to its session row | Each launcher binds by an ID it knows before or at start (job ID, session ID, chosen UUID, thread ID), as workers do. |
| The daemon restarts during a start | The `start` row is lost; the session still appears through normal discovery. |
| Owner worktrees pile up | They are shown in the detail pane and never removed by agent-net. Open question 4. |

## Rejected alternatives

- **Owner sessions as long-lived workers (a flag on a worker):** every worker
  rule (brief, hand-back, sandbox, cleanup, owner-gone) would need an
  exception; share the launch code, not the record.
- **One tmux host for every harness:** loses Claude's attach and the shared
  OpenCode and Codex servers, and adds a dependency.
- **A herdr tab per started session:** brings the screen clutter back.
- **`s` with a Kind switch in one form:** one mistyped switch either
  sandboxes the owner or leaves an unsandboxed agent running for days.
- **A "New project" entry point of its own:** a new project is a path in the
  same form.
- **Project templates and scaffolding:** the first message asks the agent to
  do it, with the owner's words.
- **Clone in the form:** rare, needs a URL and a target; the CTO covers it.
- **Always enter after a start:** the first-message start exists so the owner
  can keep watching the board.
- **Never enter after a start:** an owner with nothing to say yet came to
  talk; making them press Enter again is a wasted key.
- **The difficulty chooser for owner sessions:** it avoids the owner's own
  plan on purpose, which is the wrong goal here.
- **An `agent-net new` command:** the owner acts in the TUI only.
- **Automatic idle end, now:** it loses the owner's place; decide after use.
- **Resume picker inside the form ("which conversation?"):** offline rows
  already are that picker.

## Open questions for the owner

**Autonomy and session starts:** This design permits only the owner to start or resume an owner session, with the CTO proposing a start for owner approval. Decision 0036 lets agent-net wake an agent when autonomy is `wake`. The owner decided on 2026-11-01: agent-net may wake an existing agent under `wake`; creating a new agent or a clone stays owner-only, with the CTO able to propose it for approval. Slice 4 wakes an online session with a message and never starts or resumes a session itself.

1. **Should idle background sessions stop by themselves?** For example after
   24 hours with no turn, with the conversation kept. Recommendation: no, not
   in the first version. Watch the memory column for a week, then decide.
2. **Do you want a Name field?** Recommendation: no. Names follow the project
   (`tenebrae 2`) and their announced tasks in TASK tell them apart. If you want one, it is a sixth
   line that calls `session_rename` after the session registers.
3. **Pi host: tmux, or a named herdr session where herdr is present?**
   Recommendation: tmux everywhere, because it is on every machine and has no
   visible tab. herdr only if you want to see these sessions in herdr too.
4. **Where do owner worktrees go?** Options: next to the project
   (`~/code/agent-net.worktrees/<branch>`), inside it (`.worktrees/`, ignored
   by git), or under agent-net's state folder. Recommendation: next to the
   project, so they are easy to find and never inside the repository.
5. **Where does a new folder go by default?** Recommendation: the folder
   that holds most registered projects on that machine (`~/code` here), shown
   as the start of the Project field when the owner types a bare name.
6. **After a reboot, should agent-net offer to resume the sessions that were
   running before?** Recommendation: not now; Enter on each offline row is
   enough. A "resume all from before the reboot" action can come later.
7. **Is the resume confirm right?** Recommendation: yes for rows offline more
   than an hour, and no confirm for rows that went offline in the last hour.
8. **Starting on another machine, may agent-net create a folder there?**
   Recommendation: yes, with the same confirm and the same home-folder rule.

## Build plan

Each part is one worker, with its own failing tests first, and leaves the
TUI working. Parts 1 to 3 are the useful core; the owner can try it after
part 3.

1. **Daemon: `session_start` for Claude, local machine, project folder.**
   Owner-gated method; reuse `startClaude` without the worker parts; the
   started-session record; `status: starting` then the registered session.
   Fake `claude` in tests. Add the method to `docs/security.md`.
2. **TUI: the `n` form on Sessions.** Project, Harness, Model, Folder (project
   folder only), First message; start and enter, or start in the background.
   Golden scenes at 80 and 120 columns; help and key line.
3. **Resume on Enter for offline rows.** `session_resume` for Claude
   (`--bg --resume`), the confirm line, OpenCode and Codex through their
   existing enter plans. Decision file for the change to 0032's Enter.
4. **OpenCode launcher.** Create and prompt a session on the owner's service;
   find its address; refuse clearly when it is down. Fake service in tests.
5. **Codex start plan.** Start-and-enter in the folder with model and prompt;
   the dormant thread resumes through the existing plan.
6. **Pi tmux host.** Launcher, enter through the tmux plan, resume with
   `--session`, stop. Refuse without tmux.
7. **`x` for owner sessions.** Per harness stop; refusal text for terminal
   sessions; the "conversation is kept" confirm.
8. **Other machines.** Gateway frame that relays `session_start` and
   `session_resume` with the owner flag; the start note about ssh.
9. **Worktree folder.** The Folder switch, the branch line, the busy-folder
   default and its reason; owner worktrees are never cleaned.
10. **Paths and new folders.** Path in Project; register an existing folder;
    create plus `git init` after `y`; the home-folder rule; the Claude trust
    note and its confirm.
11. **Graph entry points.** `n` on the Graph card and on a Graph node;
    `?` lines.
12. **CTO proposals.** `session_start` and `project_create` actions in the
    CTO change tool, the Needs you row and the runner. Depends on the
    proposal system of decision 0035.
13. **Docs.** `docs/reference.md` (Sessions keys, the form, Enter on offline
    rows, `x`), a new decision file for owner sessions, and the README line
    that says sessions can be started from the TUI.
