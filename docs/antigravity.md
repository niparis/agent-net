# Antigravity (agy)

Antigravity is Google's CLI, called `agy` on the command line. It is part of
the plan in [decision 0049](decisions/0049-antigravity-harness.md).

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
- **Updates.** The daily harness update runs `agy update` like the other
  harnesses.
- **Display.** `agent-net nodes --wide` shows the agy row with its version, its
  models and its sign-in state.

## What does not work yet

- Antigravity as a main session (step 5).
- Opening an agy session in herdr (step 6).

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

Permission probes on agy 1.4.12 confirmed workspace edits and shell commands
with the normal worker flags. The sandbox made home read-only while allowing
writes in the working folder and `/tmp`. Auto mode was unavailable for that
account, and a write-file allow rule alone did not allow edits. Settings are
only for this invocation; the worker never uses a skip-permissions flag. These
are agy's restrictions, not an isolation boundary against arbitrary code running
as your operating-system user. See [security](security.md).
