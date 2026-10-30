# 0014 - Delegation policy: who does the coding in a session

**Date:** 2026-10-30
**Status:** Built on branch `worktree-agent-a8bf44cdefd9ed345`. Proposed until the owner has read it.
**Context:** The owner wants some sessions to hand coding to a cheaper model, and some not. Example: an OpenCode session on a strong model (Sol 6.1) that plans and reviews, and hands all code changes to DeepSeek 4.1 native agents. The owner wants to set this in agent-net, not by writing instructions into each session.

Base: [0002](0002-workers.md) (workers, loose model names), [0008](0008-operator-interface-and-worker-build.md) (the owner gate, one operator interface for the TUI and the phone), [0010](0010-tui-shows-usage-and-conversations.md) (the TUI), [0013](0013-peer-trust-and-hints.md) (the pattern for an owner-only per-session setting: gateway storage, TUI key, framing, hooks with consent).

## The setting

A policy has three values and a scope:

| Value | Meaning |
|---|---|
| `delegate` | `off`: do the code changes yourself, start no subagents or workers. `native`: hand code changes to a native subagent of the harness. `workers`: hand them to a worker started with `agent_start`. Empty: no mode. |
| `coder_model` | A loose model name ("deepseek 4.1"). Needed for `native` and `workers`. |
| `main_model` | Optional loose name of the model the session itself should run. |
| scope | `session` (this session) or `project` (the default for every session of the project on that machine). |

The session's own value wins over the project's. A new session of a project starts with the project's value: nothing is copied, the gateway looks it up when the session is read. Setting a project default drops the session's own value, so the session follows the project. Clearing (an empty policy) at a scope removes the value at that scope. A project is a machine plus a project name (lower case; the repository project the daemon found, else the announced one). A session with no project can only get a session policy.

## Who can set it

Only the owner. The operator method is `session_policy` (`{session_id, policy, scope, dry_run}`), behind the same gate as `session_trust` and `session_text`: it needs the owner's command line or TUI and is refused for an agent client and for a command line inside an agent session's process tree. No MCP, Pi or OpenCode tool sets it. The daemon sends the gateway frame `session.policy`; the gateway checks the values, resolves the models, stores the policy and writes a `session.policy` event and log line. The migration is number 10 (`sessions.delegation`, table `project_policies`).

The policy is owner text that goes into the model's context on every turn, so who may set it matters more than for trust. See [security.md](../security.md#delegation-policy).

## Models are resolved by the gateway

The gateway resolves the loose names with `ResolveModel` ([0002](0002-workers.md), W17) against the model list that the session's machine reports for the session's harness, with that machine's model usage. This is the same code for three things: the dry run that the TUI form shows before saving, the check at save time (a name that matches nothing is refused with the reason, so the owner learns at once), and every read (`list`, the TUI, the per-turn call). A harness with no model list (Claude Code, Pi, Codex today) passes the name as typed; the TUI says "no model list for this harness: used as typed". A name that stops matching later (a provider removed) shows in the TUI detail pane as an error, and the session uses the name as typed.

## The text, applied every turn

The text is one line. It starts with `[agent-net]`, the marker of agent-net's own framing, so a copy of it inside a peer's message body is broken by the marker rule of [0013](0013-peer-trust-and-hints.md#a3---a-peer-cannot-forge-the-note) and a peer cannot pass off its words as the owner's policy. The model names go through `DelegationPolicy.Clean` at the gateway (letters, digits and `. _ : / @ + -` and spaces, at most 100 characters, no control characters or brackets) and again in the daemon before they go into text.

`native`:

> [agent-net] Your user's standing policy for this session, set in agent-net: delegate all code changes to the `agent-net-coder-deepseek-deepseek-v4-1-flash` subagent (model deepseek/deepseek-v4.1-flash). You plan, write the brief, review its diff and run the tests. Do not edit code yourself unless the subagent fails twice.

`workers` (also what `native` becomes in Pi, which has no subagents):

> [agent-net] Your user's standing policy for this session, set in agent-net: delegate all code changes to a worker. Start one with agent_start in this project with model "deepseek/deepseek-v4.1-flash" (it gets its own git worktree by default), give it a brief, and wait for its report. You plan, review its diff and run the tests. Do not edit code yourself unless the worker fails twice.

`off`:

> [agent-net] Your user's standing policy for this session, set in agent-net: do the code changes yourself. Do not start subagents or workers for them.

With no mode (only a main model, or no policy) no text is added.

The adapter asks the daemon for it on every turn (local method `policy`, not an operator method; a session asks for its own). The daemon asks the gateway (`policy.get`, only for a session of its own machine) and keeps the answer for 2 seconds, so a change by the owner shows on the next turn within a few seconds, and a turn costs one local call. If the gateway cannot be reached, the last answer is used for up to 10 minutes. The text is never sent once and then forgotten: each model request carries it.

## Per harness

| | `off` / `workers` text | `native` | main model |
|---|---|---|---|
| OpenCode | `context` hook adds a system part to every model request of a root session | The plugin defines an agent at run time (see below) | `session.switchModel`, once per value, from the `prompt` hook |
| Claude Code | `UserPromptSubmit` hook `agent-net policy-hook`, `additionalContext` | A marked file `~/.claude/agents/agent-net-coder-<model>.md` with `model:` | Not possible from outside: the TUI says "switch the model yourself once" |
| Pi | `before_agent_start` adds it to the system prompt | Pi has no subagents: the `workers` text is sent, and the TUI says so | `pi.setModel` once per value, only for an exact `provider/id` the model registry knows |
| Codex | Not applied. Codex hooks were not checked. The TUI says "no hook for codex yet". | | |

### OpenCode

The OpenCode 2.2.0 plugin API has `ctx.agent.transform(editor => ...)`, and `editor.update(id, fn)` creates an agent that does not exist (checked in `packages/core/src/agent.ts` of the OpenCode repository: it uses `Info.default(id)` and sets it). So the subagent is registered at run time, with no file for the owner to write. The plugin registers `agent-net-coder-<slug of the resolved model>` with mode `subagent`, the resolved model (`provider/model` split at the first slash), a description and a prompt ("implement the brief, run the tests, report the diff"). One agent per coder model, because one OpenCode service hosts many sessions with different policies; the name in the text says which one to use. The plugin calls `ctx.agent.reload()` when a new agent appears, from the `prompt` hook, so the agent exists before the first model request.

The session's own subagent sessions get no policy (a root session is the one with a link; a child session's `rootOf` entry makes the plugin skip it). Without this the coder would be told to delegate.

### Claude Code

`agent-net policy-hook` is a second hidden subcommand next to `agent-net hint`. `setup` adds it to `~/.claude/settings.json` on event `UserPromptSubmit` only with the owner's yes (default No, `--claude-policy-hook`, `--no-claude-policy-hook`, no terminal means not added), keeps every other key and hook, and `uninstall` removes our entry. The hook reads the hook JSON on stdin, asks the daemon for the session's policy (budget 700 ms, about the cost of a local socket call) and prints `{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"..."}}` only when there is text. On any problem it prints nothing and exits 0.

For `native` the daemon, on the machine of the session, writes the subagent file when the hook asks (that is, the owner installed the hook and set a policy that names a subagent). The file has `name`, `description`, `model` and a marker comment. Only files with the prefix `agent-net-coder-` and the marker are ever changed or removed; a file with the same name that agent-net did not write is left alone and the session gets the `workers` text. `uninstall` removes every file with the prefix and the marker. Claude Code watches `~/.claude/agents/` and picks up new files within seconds, except the first file in a directory that did not exist when the session started (a restart is needed then).

The `model` value is the coder name as typed: Claude Code accepts `sonnet`, `opus`, `haiku`, `fable`, a full model ID, or `inherit`. The daemon has no Claude model list, so there is no check; a wrong name fails when Claude Code uses the subagent.

### Pi

Pi's README says it skips sub-agents. `native` falls back to `workers`, and the TUI detail pane says "Pi has no subagents: native uses workers". The main model is set with `pi.setModel`; the extension finds the model in the registry only by exact `provider/id`.

## What the TUI shows

Key `p` opens a form on the selected session: Delegate (left and right cycle none, off, native, workers), Coder, Main, Scope (session, project). Every move to another field asks the gateway for a dry run and shows what the loose names resolve to ("-> deepseek/deepseek-v4.1-flash") or why they do not. Enter on Scope saves; a refusal from the gateway stays on the form. The sessions table has a POLICY column (`own`, `sub`, `wrk`) when some session has a policy, the detail pane has `policy` and `main` lines with the scope and a note for each part the harness cannot do. `agent-net list` has a POLICY column. For Claude Code the node inventory says whether the hook is installed (`policy_ready`), and the TUI says "not applied yet: needs the Claude Code hook" when it is not.

## Alternatives not taken

- **One fixed agent name (`agent-net-coder`).** Two sessions with different coder models would fight over it. The model is in the name.
- **Writing OpenCode agent files into `~/.config/opencode/agent/`.** Runtime registration needs no file, nothing to clean up, and follows the policy at once.
- **Sending the instruction once, as a message.** It is forgotten after compaction and in a long session. A system prompt part or hook context on every turn is not.
- **Letting the session set its own policy.** The point is that the owner controls it.
- **A policy file in the repository.** The owner asked not to write instructions into each project.
- **Switching the main model on every turn.** It would fight the owner when they switch by hand. It is switched once per value the owner sets.

## Checked and not checked

Checked: the OpenCode plugin types in `@opencode/plugin` 2.2.0 (`AgentEditor.update`, `SessionHooks` `context` and `prompt`, `switchModel`) and `plugins/opencode/typecheck.sh` passes; the OpenCode agent editor in the OpenCode repository (`packages/core/src/agent.ts`, `update` creates the agent); the Claude Code subagent format, the `model` values, the live reload and the location priority (https://code.claude.com/docs/en/sub-agents); the `UserPromptSubmit` hook: no matcher, `additionalContext` in `hookSpecificOutput`, 10000 character cap per field, fires on every prompt (https://code.claude.com/docs/en/hooks); Pi's `before_agent_start`, `setModel` and the model registry types, and `plugins/pi/typecheck.sh` passes. The plugin behavior is tested with fake hosts.

Not checked: a live run in OpenCode, Claude Code or Pi (no paid model runs, and the real setups were not touched). Specifically: that OpenCode's `task` tool offers an agent registered this way and runs it on the given model; that the `context` hook's system parts reach the provider request on every continuation; that `switchModel` from the `prompt` hook applies to the request that follows; that Pi applies `setModel` in `before_agent_start` to the same turn; that Claude Code follows the added context (the hooks page warns that instruction-like context can trigger its prompt-injection defences; the text is written as the owner's standing policy, but a model may still ignore it); Codex has no support.
