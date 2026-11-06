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

## Main sessions

An interactive `agy` started by the owner appears in Sessions and Graph after
its SessionStart hook reports its conversation. The daemon checks the hook's
process ancestry and working folder, and removes the session when that process
ends. It reads the local conversation summaries database for a title and to
exclude conversations with a parent. Worker conversations are not main
sessions. A session started before the plugin was enabled must be restarted to
be discovered. The daemon keeps start hooks in memory, so an existing session
needs a new SessionStart hook after a daemon restart. If the summaries database or its parent column is unavailable,
agy does not provide enough information to identify sub-conversations from it.

The agent-net MCP tools bind a conversation only when its ID is discovered and
the MCP process descends from the matching agy process. The session can then
send messages as itself. If the check does not match, the connection remains
unbound and cannot send as a session.

Messages to a busy session wait for its next Stop hook, which returns the
message to agy as a continuation. A PreInvocation hook can also inject a
waiting message as a user message. When idle in herdr, agent-net sends the
framed message as text to the matching pane and presses Enter. Messages with terminal control characters also wait for a hook. Otherwise the
message waits for the next hook. Its status says "waiting for the session's
next turn" until delivery. Waiting messages remain at the gateway while the
daemon reconnects; messages expire under the normal message expiry rules.
The hook path depends on agy running the installed plugin, and an idle session
outside herdr must have another turn before it receives a waiting message.

Enter opens `agy --conversation <id>` in the session folder or focuses its
existing herdr pane. In herdr it opens beside the TUI; from a plain terminal on
the same machine it uses the TUI terminal and ctrl+] returns. For a remote
session a local herdr client opens the remote herdr when available. The
new-session form does not offer agy; start an interactive session in its own
terminal first.

The vendor in an agy model id is used for display: `gemini` to Google,
`claude` to Anthropic and `gpt` to OpenAI. A Claude model through agy draws on
the Antigravity plan, not on your Anthropic subscription, so the
protected-provider rule does not apply to it.

## Workers

An agy worker runs in its worktree with this command (`-p=` is empty and last):

```sh
agy --input-format stream-json --output-format stream-json --model <id> --mode accept-edits --sandbox --settings '{"permissions":{"allow":["command(*)","mcp(agent-net_agent-net/*)"]}}' -p=
```

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

Read-only workers use `--mode plan --sandbox`, with only
`mcp(agent-net_agent-net/*)` in the allow list. They get no command allow rule.
The no-network project level is not enforced for agy.

A writable worker also receives `--add-dir` for its worktree's private git
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
