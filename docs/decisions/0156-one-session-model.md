# 0156 - One session model: the CTO is an ordinary session with a role

Date: 2026-11-12
Status: Accepted by the owner, through the CTO ("the CTO, the chief and every coding session start, show and are managed the same way ... one start path ... delete the old paths"). This is part A. Part B follows.

## Problem

The owner's words: "It's been a while we have been discussing having the same
functions to manage your sessions and the coding sessions. I thought they were
merged."

Decision 0149 gave every coding session one start path. The CTO still had two
private starters that bypassed it: a Claude background job with its own launch
call (`startClaudeCTO`), and a private OpenCode server with its own launch loop
(`startCTOLocked`). A change to the one start path did not reach the CTO.

## Decision

The CTO is an ordinary agent-net session with a role. A role is a set of
settings that the one start path applies: the name, the system prompt, the tool
list, the MCP servers and the model. The CTO is the only role today. The chief
of staff is an ordinary Claude agent session and needs no role.

### Claude

One builder, `claudeStartSpec`, makes the `claude --bg` spec for every Claude
start: an owner start, a resume, a wake, a merger and the CTO. The role
`cto` adds `--tools ""`, `--setting-sources ""`, `--system-prompt`,
`--mcp-config` with the `agent-net-cto` server, `--strict-mcp-config`, the
launch settings, and the job name `agent-net-cto`. It also names the variable
the launch must not inherit (`CLAUDE_CODE_DISABLE_MOUSE`, decision 0128).
`startClaudeRole` launches it with `startBackground`, the same launcher as every
other Claude session.

Two behaviours came from the CTO's private path into the shared launcher, so
every session has them: a job that ends before it writes a session file fails
at once with its detail, and a launch that prints an error carries that error
in the failure.

### OpenCode

A role needs its own OpenCode server, because its agent, system prompt,
permissions and MCP server are OpenCode configuration that the owner's shared
service must not carry. A worker needs a private server for the same reason.
Both now start it with one function, `serveOpenCodePrivate`: pick a port, retry
a lost port race, wait until the server answers. They differ only in who owns
the process, which each gives as functions.

### What stays the same

- The CTO's agent-net session ID is the network identity the gateway made
  (`cto_network`, kind `cto`). It does not change on a restart or an upgrade.
- The conversation lives in the same private folder (`<state>/cto/claude`), so
  the transcript is found again. The `cto` and `cto_actions` tables are not
  changed, so there is no migration.
- The CTO's restrictions are the same, for both harnesses. (Part B, [decision 0157](0157-cto-full-power-and-own-folders.md), gives it full built-in tools.)

### What was deleted

- `startClaudeCTO`, `startClaudeCTOOnce` and the CTO's own `runClaudeBackground`
  call;
- `startCTOLocked` and its copy of the private OpenCode start loop;
- `claudeCTOJobFailure` (now `claudeJobFailure`, shared).

Two tests fail if a private launcher comes back: `TestNoPrivateCTOLauncher` and
`TestClaudeStartSpecAppliesTheCTORole`.

### Gaps for part B

Part A moved the start. These stay, and part B moves them with Enter, the view
and the management in the TUI:

- The runtime record of the CTO job (`claudeCTO`, `cto.mu`): which job is the
  CTO, adoption after a daemon restart, crash monitoring (`monitorClaudeCTO`).
  It is separate from the ordinary session record, which carries no role yet.
- The CTO's own Enter plan, `cto_enter` and the `c` key. (Decision 0157 gives the CTO the shared Claude Enter plan.)
- Sending to the CTO (`DeliverCTO`) and reading its timeline, which use the
  CTO's own tables and transcript cache.
- A restart without the old conversation is in `startClaudeRole`: it retries
  with no resume when the failure says the old conversation is gone. The shared
  launcher reports that failure but does not retry for other sessions.
- The OpenCode CTO still keeps its server in `d.cto`, not in a session record.

## Not changed

The TUI, the Enter plan and herdr opening (decision 0046). The CTO's tables.
