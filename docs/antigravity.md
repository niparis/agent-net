# Antigravity (agy)

Antigravity is Google's CLI, called `agy` on the command line. Its integration is described in [decision 0049](decisions/0049-antigravity-harness.md).

## What works so far

- **Detection.** The daemon looks for `agy` with the same search as the other
  harnesses (PATH, the usual install folders, and the login shell PATH). It
  reports one `agy` entry with its path, its version from `agy --version`, and
  its other copies.
- **Models and sign-in.** It runs `agy models` with a 20 second limit. The
  output has a first line `Fetching available models...` and then one
  `<id><TAB><label>` per line. The ids are kept as they are, and the effort
  stays part of the id: `gemini-3.8-flash-low`, `gemini-4-argon-high`,
  `claude-sonnet-5-5-medium`, `gpt-oss-120b-medium`.
  The list only arrives when the machine is signed in, so it is also the
  sign-in check. A list means `signed in`, a message such as `not logged in`
  means `not signed in`, and a failure or timeout leaves the state unknown.
  Account details are never kept.
- **Worker profiles.** A profile may name `agy` and an agy model. It is
  accepted, stored and listed, and can start workers.
- **Plugin.** Setup and each daemon start install the agent-net plugin under `~/.gemini/config/plugins/agent-net/` when `~/.gemini` exists. Its rules supply guidance, its MCP server supplies tools, and its hooks report session starts and turns to the local daemon. Disable it with `agy plugin disable agent-net`. Uninstall removes its files.
- **Updates.** The daily harness update runs `agy update` like the other
  harnesses.
- **Display.** `agent-net nodes --wide` shows the agy row with its version, its
  models and its sign-in state.

## The workers skill

The plugin ships a skill named `workers` in `skills/workers/SKILL.md`. agy
loads plugin skills from that folder. In a session, `/workers` or a request
such as "show my workers" makes the model call `agent_workers_find` for the
workers that session started, running and ended. It prints one compact table
with name, state, model, run time, cost when the result has it, and title,
with running workers first. With no workers it prints one line. `agy plugin
validate <dir>` lists the skill as one component.

## Main sessions

An interactive `agy` started by the owner appears in Sessions and Graph after
its SessionStart hook reports its conversation. The daemon checks the hook's
process ancestry and working folder, and removes the session when that process
ends. It reads the local conversation summaries database for a title and to
exclude conversations with a parent. Worker conversations are not main
sessions. agy sends SessionStart only on the first prompt of a process, so a
session that began before the plugin was enabled is discovered at its next
turn: its PreInvocation or Stop hook goes through the same process and folder
checks. The daemon saves each discovered session in its state database. After
a daemon restart or upgrade it brings the session back while the same agy
process still runs, with the same session ID, and its MCP tools bind again.
The saved session is dropped when that process ends. If the summaries database
or its parent column is unavailable, agy does not provide enough information to
identify sub-conversations from it.

The agent-net MCP tools bind a conversation only when its ID is discovered and
the MCP process descends from the matching agy process. The session can then
send messages as itself. If the check does not match, the connection remains
unbound and cannot send as a session.

Messages to a busy session wait for its next Stop hook, which returns the
message to agy as a continuation. A PreInvocation hook can also inject a
waiting message as a user message. When idle in a pane of agent-net's own
herdr, agent-net sends the framed message as text to that pane and presses
Enter. A session in a herdr you run yourself is never typed into (decision
0154). Messages with terminal control characters also wait for a hook. Otherwise the
message waits for the next hook. Its status says "waiting for the session's
next turn" until delivery. Waiting messages remain at the gateway while the
daemon reconnects; messages expire under the normal message expiry rules.
The hook path depends on agy running the installed plugin, and an idle session
outside agent-net's herdr must have another turn before it receives a waiting
message.

Enter on a running agy opens the herdr pane that holds it. It attaches to that
pane and starts no agy, so the conversation never has two processes. ctrl+]
ends only the view; the running agy keeps running. For a remote session a
local herdr client opens the remote herdr when available. When the running
agy is in no herdr pane agent-net can reach, Enter starts nothing. The note
names the machine, the folder and the process ID, and says where to open it.
Only an agy that is not running gets `agy --conversation <id>` in the session
folder, in the TUI's terminal or herdr pane.

agent-net can also start and resume an agy session, from the TUI's
new-session form (`n`) or from the CTO with `session_change`. A start opens
`agy` in the project folder in a pane of agent-net's own herdr. agy creates the
conversation ID itself, and the SessionStart hook of that pane names it. The
session is then discovered like any other, and an Agent or lead role asked for
in the start is bound to that conversation.

A start with a first message passes it to agy as the `-i` prompt, so agy runs
it at once. This prompt is not an agent-net message, so it is not in the
message history. When the owner did not write the text, it begins with a line
that names the writer and says it is not from the owner. The owner's own words
go through unchanged. A writer agent-net does not know is refused. If the
SessionStart hook does not name the conversation within the start timeout, the
start fails. A start that another machine runs passes its first message the
same way.

A start without a first message is answered as started, and its note says so.
It waits in the pane. agy creates the conversation with its first prompt, so
the session shows up in Sessions after the owner sends one. The first time agy
runs in a new folder, it asks whether to trust the project, and nothing runs
until that is answered in the pane. If the pane closes before a first prompt,
the start fails and its Agent choice is released.

A resume runs `agy --conversation <id>` in the project folder the same way.
Both need herdr, as Pi does, because an interactive agy session has no attach
client and needs a terminal.

The vendor in an agy model id is used for display: `gemini` to Google,
`claude` to Anthropic and `gpt` to OpenAI. A Claude model through agy draws on
the Antigravity plan, not on your Anthropic subscription, so the
protected-provider rule does not apply to it.

## Workers

An agy worker runs in its worktree with this command (`-p=` is empty and last):

```sh
agy --input-format stream-json --output-format stream-json --model <id> --mode accept-edits --settings '{"permissions":{"allow":["command(*)","mcp(agent-net_agent-net/*)"]}}' -p=
```

The daemon runs this command inside agent-net's OS wrapper, as for Pi and
OpenCode workers: bubblewrap on Linux, `sandbox-exec` on macOS. The wrapper is
the write fence of [decision 0197](decisions/0197-worker-write-fence.md). agy
runs without its own `--sandbox` there, because agy's terminal sandbox cannot
run a command on the owner's machines: on Linux the exec of the shell fails
(`fork/exec /usr/bin/bash: no such file or directory`), and on macOS a shell
from a Nix profile is not permitted. The model then reran each command with
`BypassSandbox`, and nothing held the fence. `command(*)` lets shell commands
run without a prompt; the wrapper decides where they can write.

agy writes its own folders as it runs, so they are writable in the wrapper:
`~/.gemini/antigravity-cli` (conversations, the summaries database, logs,
helper binaries, its settings, where it adds each workspace it trusts, and the
login token) and the login folder `antigravity` in the user config folder
(`~/.config/antigravity` on Linux). agy has no option to move them, so these
are your own folders, not copies.

A project with the wrapper turned off (`"workers": {"os_sandbox_off": [...]}`
in `node.json`) runs the same command with agy's own `--sandbox` after
`--mode accept-edits`. The model can run a command outside that sandbox, so the
worker's permissions line says the fence is not enforced.

The agent-net agy plugin must be installed for `agent_handback`. Its MCP server
is exposed as `agent-net_agent-net`. The daemon checks the worker's process
group and the root conversation ID passed by agy on each MCP tool call.

The brief is the first stdin line. Follow-ups and automatic continues are later
lines in the same process. If the process has ended, the daemon adds
`--conversation <id>` before `-p=`. A handback waits for the result event so its
notice includes input, output, thinking and cache-read tokens. A turn without a
handback uses the normal idle notice and automatic continue limits. A failed
result, an early finish reason, denied tools or an error exit marks the worker
failed. Stop closes stdin, waits briefly, then stops the whole process group.

A worker also receives `--add-dir` for its worktree's private git
folder and the main repository git folder's `objects`, `refs` and `logs`. This
is the same narrow set a Codex worker gets. In a linked worktree the `.git`
file points at the main git folder that the sandbox otherwise hides, so without
these folders git cannot find the repository. With them `git status` and
`git diff` work, and a commit can write objects and move the branch. The main
`config` and `hooks` folders stay hidden.

Permission probes on agy 1.4.12 confirmed workspace edits and shell commands
with the normal worker flags. The sandbox made home read-only while allowing
writes in the working folder and `/tmp`. Auto mode was unavailable for that
account, and a write-file allow rule alone did not allow edits. Settings are
only for this invocation; the worker never uses a skip-permissions flag. These
are agy's restrictions, not an isolation boundary against arbitrary code running
as your operating-system user. See [security](security.md).
