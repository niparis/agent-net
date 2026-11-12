# 0157 - The CTO has full power; the merger has its own folder

Date: 2026-11-12
Status: Accepted by the owner, typed to the agent-net lead on 2026-11-12. This is part B of decision 0156.

## Problem

The owner's words: "If the goal of CTO is to be ... my primary interface, then
of course it needs full power." Decision 0035 rule 5 gave the CTO no built-in
tools: Claude started with `--tools ""` and `--setting-sources ""`, its hooks
were off, and the OpenCode `cto` agent denied every built-in tool. The CTO could
not read a file or run a command, and a hook in its own folder could not run.

The merger ran in the lead's project folder. Claude Code keys a session's memory
by folder, so the merger and the lead shared one memory. The owner wants that
fixed "not as a hack".

The CTO had its own Enter plan. The owner wants to paste an image into the CTO
exactly as into a coding Claude session.

## Decision

### The CTO has full power

This replaces decision 0035 rule 5.

- **Built-in tools.** The Claude CTO starts without `--tools ""` and without
  `--setting-sources ""`, and its launch settings no longer set
  `disableAllHooks` or an empty `enabledPlugins`. It has Claude Code's built-in
  tools and reads the owner's Claude settings, hooks and plugins, as a coding
  session does. The OpenCode `cto` agent has no deny rules for built-in tools,
  and the `general` subagent is no longer disabled. The startup check that
  refused a CTO with built-in tools is removed. Claude's `--help` must still list
  `--strict-mcp-config`.
- **Its own MCP server stays the only agent-net server.** The CTO keeps
  `--strict-mcp-config` with only `agent-net cto-mcp`. This is its identity, not
  a tool limit: with the ordinary agent-net server it would register a second
  session.
- **The owner's rule for what runs without asking:** "anything that can easily
  be reverted, the CTO may do". The CTO's launch settings carry a `PreToolUse`
  hook on `mcp__agent-net-cto__cto_op`. It runs `agent-net cto-hook`, which
  reads `tool_input.method` and answers `permissionDecision: allow` for every
  method except `app_revoke`, `process_stop`, `machine_remove`,
  `machine_rotate`, `agent_tombstone`, `node_remove` and `repo_archive`. For
  those it gives no decision, so Claude Code's normal check asks the owner.
- **Owner changes that are easy to revert run at once, with no proposal.** This
  was already so for every change the CTO's tools classify as immediate. The
  irreversible ones still need the owner: `node_remove`, `agent_tombstone`,
  `repo_archive`, `machine_remove` and `machine_rotate` stay proposals, and the
  hook leaves `app_revoke` and `process_stop` to Claude Code's permission ask.
- **Messages from other agents stay peer requests.** Incoming messages are still
  labeled as peer input with the body quoted as data. Full power does not change
  who may instruct the CTO.

### The roles are data in the one start path

The CTO role is a set of settings in `session_role.go`: flags, launch settings
with the hook, MCP config and, for OpenCode, the agent's permission rules. The
coding sessions, the merger and the CTO differ only in these.

### The CTO opens like a coding Claude session

The CTO's Claude Enter plan is built by `claudeAttachPlan`, the builder of every
Claude session's plan, and its view by `sessionView`, keyed by its session ID.
It has the same attach command, host, folder and note. A TUI on this machine
hands over its terminal; a TUI on another machine opens it with
`herdr --remote` (decision 0046). Image paste therefore works the same. A test
asserts that the CTO's plan equals a coding Claude session's plan apart from the
job and the folder. The `c` key selects the CTO's Sessions row and opens it,
the same as Enter on that row.

### The merger has its own folder

`merger_start` starts the merger in its own detached git worktree of the
project, `<state>/mergers/<project>`, outside the project folder. A later start
reuses it. The owner's trust is still asked for the project folder, because the
merger's folder holds the same code. An existing merger keeps its folder until it
is replaced: start a new one, then `session_replace` old to new.

### The owner stops, resumes and restarts the CTO like any session

`session_stop`, `session_resume` and the restart path serve the CTO's session
too: `x` on its Sessions row stops it, Enter or resume starts it again on its
conversation, and a restart waits until the gateway reports it dormant. The
branch for the CTO sits at the entry of each ordinary function
(`sessionStopLocal`, `resumeAgent`, `waitSessionEnded`) and calls the CTO's own
stop and start. The CTO still cannot stop or restart its own session from its
tools.

## What this supersedes

- Decision 0035 rule 5 ("No built-in tools") and the consequence that removed
  "all built-in tools and owner plugins" from the CTO.
- In decision 0156: "The CTO's restrictions are the same, for both harnesses"
  and the gap "The CTO's own Enter plan".

## Not done: folding the CTO's runtime record

The CTO's runtime record (`claudeCTO`, `cto.mu`, `monitorClaudeCTO`, and `d.cto`
for OpenCode) stays separate from the ordinary session record, and
`cto_runtime.go` stays. Folding them is not safe as a plain move, for three
reasons:

- The record is the root of the check that lets a process use the CTO's MCP
  server (`isCTOPeer`): the CTO's process root and its start token. That server
  now has the owner's powers. The ordinary record is filled from harness
  discovery, which any owner process can feed.
- Every ordinary record is registered at the gateway as an ordinary session.
  The CTO's identity is the gateway's `cto` session. Folding it in would make a
  second identity unless the gateway's registration learns the role.
- Messages to the CTO go through `DeliverCTO`, which labels them as peer input.
  Ordinary delivery does not.

Doing it needs a role field on the ordinary record that only the role start
path can set, a gateway registration for a role session, and the CTO's
delivery moved with it. That is its own design step.
