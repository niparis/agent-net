# 0171 - Switch the harness of a session without losing context

Date: 2026-11-17

The owner asked to move the CTO and the chief of staff between Claude Code,
Codex, OpenCode and Pi, and back, and to run agent-net with no Claude Code at
all. Today a switch keeps the owner's timeline but the new conversation starts
with no context, and the CTO's own skill exists only as a file in a Claude
folder.

## Decisions

1. **One session model.** Any session can switch harness. The CTO and the
   chief use the ordinary session machinery, with no special case for a role.
2. **Phase order 0, 1, 2, 3, 4.** Phase 0 is an audit. Phase 1 is the handover,
   the conversation table, one delivery confirm for every harness, and the same
   start and compaction context everywhere. Phase 2 is the role address and the
   chief started from its model list. Phase 3 is a Codex CTO with full power.
   Phase 4 is first use with no Claude Code. Pi comes later.
3. **The chief keeps gateway notes only**, like the CTO. Its memory is the
   notes the gateway holds, not a harness memory.
4. **Pi is refused for every role** until it can be woken and resumed.
5. **OpenCode and Pi workers are sandboxed by default.** Open until phase 4;
   setup must ask once and show the choice.

## Conversation per session

The daemon keeps one row per session and harness in
`session_conversations(session_id, harness, native_id, updated_at)`. It
replaces the CTO's `claude_session_id` and `session_id` columns, which stay in
the table for one release and are no longer read or written. The CTO's session
key is its network session id (phase 2).

Migration: the table is created once, and its rows are copied from the two
columns. The copied rows get `updated_at = 0`, so they count as stale. The
active conversation still works, because only a switch checks the age.

A switch to a harness resumes that harness's conversation only when its
`updated_at` is within 24 hours. An older conversation is dropped, and the
switch starts a new one. A stale resume brings back beliefs from days ago.

## Handover on every switch

A switch of harness, from the owner's settings or from the 0162 fallback,
writes one handover note before the old harness stops:

- The **mechanical layer** always comes first. It lists the open asks, the
  asks that wait on the CTO, the workers and changes the CTO started that are
  not done, and the owner's last 10 messages, verbatim. It also lists the
  harness-local work the daemon can see: a busy turn in OpenCode or a running
  Claude background job. It needs only the gateway and the daemon's own
  records, so it is written when the old harness is down.
- The **model layer** follows an owner switch from a harness that was running
  when the switch began. The old conversation has 60 seconds to answer. It holds
  the model's own words about open promises and why threads matter, and it is
  marked "model output, not the owner's words". A busy turn, a harness that was
  not running, a failed send or a timeout skips it. A fallback never has it,
  because the old model has just failed.

The note is named `handover-<UTC time>`, for example
`handover-20261117-100000.123`, because note names allow only lower-case
letters, digits, dots, underscores and hyphens. Each switch writes a new name,
so a handover is never overwritten. The new conversation reads the latest
handover name in its start text, and is told to read that note and its open
asks first.

An owner-started switch during a busy turn first sends the owner one Inbox
notice, then stops the old turn. A fallback never sends that notice.

The handover request and its reply stay in the old conversation, because it is
the same conversation when the harness comes back. The owner's timeline and the
timeline snapshot leave them out.

## The CTO's skill

`agent-net-setup` is no longer written into the harness folders
(`cto/claude/.claude/skills`, `cto/.claude/skills`). Before its first CTO harness
start, and again when the shipped content changes, the daemon sends its embedded
copy to the gateway archive. It adds the skill to the CTO's node list once. Each
harness then gets the skill from
the node's skill folder (decision 0169, task 4): `--plugin-dir` for Claude and
the skills root in `OPENCODE_CONFIG_CONTENT` for OpenCode. The "once" rule
means an owner who removes the skill from the list keeps it removed.

## Part b: the delivery confirm and the start block

One check per delivered message replaces the Claude-only confirm. The check
waits 20 seconds, then reads what the harness reports. A taken message costs one
read, the read that finds the turn. A busy session is checked again 20 seconds
later, and each busy check costs two reads (the busy state and the turn). An
idle, untaken message is sent once more, then the session restarts with the
message as its first prompt. A session has one check at a time: a newer message
replaces the older check, and the check ends when the message is taken, when the
session goes offline, ends or is archived, when agent-net stops it, or when the
daemon stops. On OpenCode, a restart reads the conversation first. When a copy of
the prompt is stored with an assistant reply after it, the restart does nothing.
When a copy is stored with no reply, the restart sends a short nudge that points
at it, not the full text again. When no copy is stored, the restart sends the
prompt once.

The sources are the Claude transcript (the message ID in the bytes added since
the send), the OpenCode conversation (a message the owner did not write, created
at or after the prompt, which includes a turn that fails), and Codex's notice
that its user message item carries the message ID (checked against a real app
server in codex_real_test.go). Pi and plugin sessions answer synchronously, so
they get no check.

After a compaction, the next turn carries the start block again. Claude keeps
its PostCompact hook. OpenCode and Codex take the daemon's compaction event
(noteCompacted, and the CTO's compaction).

## Phase 2: role address, the chief's own start, role wake

**Role address.** `chief` and `cto` are addresses. A message to one goes to the
session pinned for the role when the gateway stores it (`chief_session`,
`cto_session`). When the owner or the CTO pins another chief, the old chief
session gets `replaced_by` the new one, so a message sent to the old id reaches
the current chief. The sender's receipt says which session got the message. With
no chief set, a message to `chief` is refused and says so. The CTO's own rule
stays: it takes messages from the owner and replies only.

**The chief starts from `chief.model`.** The daemon that holds the CTO's
network identity checks the chief once a minute (`chief_start.go`). It starts
one when none is set, or when the set chief is offline on a harness that
cannot be woken. The start is the ordinary session start in the folder
`<state>/chief`. The model is the first usable entry of the list, through the
0162 gate with the CTO, chief and merger policy. The started session is pinned
with `chief_set`. Only one machine runs the loop, so two machines never start
two chiefs. A failed start waits 10 minutes. `chief_set session_id` still
adopts a hand-started session.

**A clear stands.** `chief_set` with `clear: true` stores the time and the `chief.model` line in `chief_cleared`; `chief.get` reports `cleared_at` while the line is the same. The loop starts no chief while a clear stands. Naming a chief, or changing `chief.model`, ends it.

**Pi is refused for every role.** The gate drops a Pi entry from a role list
with the reason `Pi cannot be woken by a message yet`. A list with only Pi
entries is refused with that reason.

**Role wake.** The chief and a project's merger are woken for a message and for
a ready step whatever the autonomy and `wake_on_message` say. The step lookup
took only Claude sessions before. It now takes every wakeable harness, and the
wake frame carries autonomy `wake` for a role, which the machine needs to wake a
Codex or OpenCode session.

**Chief memory.** The chief's memory is gateway notes only. A Claude chief
starts and resumes with `autoMemoryEnabled` false. Codex and OpenCode have no
such switch to set.

**The conversation key.** `session_conversations` is keyed by the CTO's network
session id, the id every session has. Before the CTO has one, the key is `cto`.
Registration, and opening the state, move the rows to the id with their age. A
row already under the id is kept.

**CTO fields that still duplicate the ordinary session record** (not folded,
because each is a behaviour change and not a rename):

- `cto.active_harness`, `active_model`, `models`, `opencode_model`,
  `claude_model`, `opencode_last_model`. The ordinary record would carry the
  real harness and model, but the CTO's gateway record has the harness `cto`,
  which the gateway tests in many places.
- `cto.graph_after` and `claude_graph_after`: a cursor for each conversation.
  They fit a column of `session_conversations`.
- `cto.session_id` and `claude_session_id`: no longer read or written.
- `cto_actions` and `cto_history` hold the harness's native conversation id.
- In memory, `ctoRuntime` and `claudeCTOState` keep the process, job, model and
  conversation of the CTO. An ordinary session keeps these in its session and
  worker records.

## Phase 3: the CTO on Codex

The CTO runs on `claude`, `opencode` or `codex`. Pi is refused with the reason
`Pi cannot be woken by a message yet`, in the settings, the command line and
the role list.

**Its own app server.** The daemon starts a private `codex app-server` for the
CTO in `<state>/cto/codex`, on a loopback WebSocket with a capability token, as
it does for a Codex worker. The owner's shared app server cannot host the CTO:
the `cto-mcp` channel admits a process only by its process tree, and the MCP
children of the shared server belong to every Codex session. The private
server's pid is a third root of that check, next to the Claude job and the
private OpenCode server. The server stops with the daemon and on a switch away;
the thread stays on disk and resumes.

**Tools and power.** The server gets one agent-net MCP server with `-c`:
`agent-net-cto` (`agent-net cto-mcp`), with its tools approved, and the owner's
own `agent-net` server off. The thread runs with approval `never` and the
`danger-full-access` sandbox (decision 0157), set on `thread/start` and
`thread/resume`. A request for approval that still arrives is accepted and
logged, because nobody watches the thread. The CTO prompt and start block are
the thread's developer instructions. `agent-net-setup` and the rest of the cto
node's skills reach the server with `skills/extraRoots/set`.

**Phase 1 behaviour.** A message goes in with `thread/queue/add` and its ID, and
the delivery confirm watches for the user message item that carries the ID.
Codex compacts on its own; its `contextCompaction` item marks the start block
due for the next turn. A switch to and from Codex writes the handover and keeps
one conversation per harness, as for the other two.

**Enter** runs `codex resume <thread> --remote ws://<addr>
--remote-auth-token-env AGENT_NET_CODEX_TOKEN`, with the token in the
environment. The plan opens through the same view path as the Claude and
OpenCode CTO, so decision 0046 holds as before.

**The settings form** lists the harnesses this machine has, of claude,
opencode and codex, and keeps the model typed for each.

The real-protocol check is `cto_codex_real_test.go`, run with
`AGENT_NET_TEST_REAL_CODEX`.

## Phase 4a: first use without Claude Code

**The setup role step.** After the harness scan, setup asks the daemon
(`role_setup`, owner only) to set the role lists that are not set yet. For each
installed harness, in the order Codex, OpenCode, Claude Code, the daemon runs
one hello probe on its default model. For OpenCode it also probes the first
model of each other provider. The probe is a one-shot run that must return a
final answer. A clean exit with no answer, a login whose limit is used up (429),
a timeout and a missing model all count as not usable. Pi is not probed, because
it cannot be a role yet. The usable entries, in that order, become `cto.models`,
`chief.model` and `merger.models`. A list the owner already set is kept. A CTO
that already has a local harness or conversation is kept too, even when its
network list is unset. Setup prints one line per role: `using <model>`, `kept
existing model list`, or what to install or sign in to. A failed probe adds a
line with its reason, so the owner sees why a harness was skipped.

**The first start.** When setup added the CTO list, the daemon registers the CTO
and sends it a first message. When it added the chief list, the daemon starts
the chief through the ordinary start (`ensureChief`) and waits for it to
register. Both run on their first usable entry. Later starts and the 0162 gate
follow the lists as before.

**Unattended roles.** A chief or merger started on the shared Codex server uses
approval `never` and the workspace-write sandbox with network access. A merger
also receives the shared Go build cache as `GOCACHE`, with permission to write
that folder and the Git folders its linked worktree needs. The same settings
apply when the session is resumed or woken. Ordinary owner sessions keep their
own settings.

OpenCode stores an allow rule on each chief or merger session. It follows the
session through a wake. For an adopted session, a wake or resume adds the rule
if needed and checks that the service stored it. The shared OpenCode service
runs with the owner's permissions and its own environment, and agent-net adds no
sandbox to a role there. So an OpenCode merger needs no cache override: it uses
the owner's Go cache.

**The TUI.** The New session form picks the harness for a machine from its
inventory. It keeps the harness of the selected session, or the most used one of
the project, only while the machine still has it and is not signed out of it.
Otherwise it takes the first harness the machine has and is signed in to, in the
form's order. So a machine with no Claude Code never starts on Claude.

**Checks.** `role_setup_e2e_test.go` sets up a machine with no `claude` on PATH
in a temp home, once with a fake Codex and once with a fake OpenCode. In each,
the CTO, the chief and one worker take a message and run a turn, and the CTO
has the `agent-net-setup` skill. The Codex CTO thread has approval `never` and
the `danger-full-access` sandbox, and the OpenCode chief has the allow rule. The
fakes cannot run a shell command. The real checks are opt-in tests:
`AGENT_NET_TEST_REAL_CODEX` for `TestCodexMergerRealServerRunsWithoutApproval`,
and `AGENT_NET_TEST_REAL_OPENCODE=1` for `TestRoleSetupProbeRealOpenCode` and
`TestOpenCodeRolePermissionsRealServer`.

## What this does not cover

- One real run on a machine with no Claude binary, before phase 4 is called done.
- The sandbox default for OpenCode and Pi workers (decision 5).
