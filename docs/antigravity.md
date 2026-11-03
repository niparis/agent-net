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
  accepted, stored and listed. The Workers panel says `not startable yet`.
- **Updates.** The daily harness update runs `agy update` like the other
  harnesses.
- **Display.** `agent-net nodes --wide` shows the agy row with its version, its
  models and its sign-in state.

## What does not work yet

- Starting a worker on agy (build step 4).
- Antigravity as a main session, the agent-net plugin, MCP and hooks (steps 3
  and 5).
- Opening an agy session in herdr (step 6).

The vendor in an agy model id is used for display: `gemini` to Google,
`claude` to Anthropic and `gpt` to OpenAI. A Claude model through agy draws on
the Antigravity plan, not on your Anthropic subscription, so the
protected-provider rule does not apply to it.
