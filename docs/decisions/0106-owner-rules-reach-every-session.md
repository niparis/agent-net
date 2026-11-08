# 0106 - Owner rules reach every session

Date: 2026-11-08
Status: Requested by the owner on 2026-11-08 ("isn't there a way to auto inject
rules from the mcp?"). Built on a worker branch. Revised after
[0100](0100-team-questions-go-to-the-lead.md) joined `rules` across levels.

## Context

The owner wants a rule set once at the global level to reach every agent
session in every harness, without editing `CLAUDE.md` or `AGENTS.md`. The first
rule is the late-defect rule: report a defect found after a worker's hand-back
with `agent_worker_rate defect_found_later`.

What each harness got before this change:

| Harness | Charter rules |
|---|---|
| Claude Code | every prompt (`UserPromptSubmit` hook) |
| OpenCode | every model request (`context` hook) |
| Pi | every turn (`before_agent_start`) |
| Codex | never for an interactive thread. A worker had the charter in its brief. The app server has no text per turn (`turn/start` has no instruction field). |
| agy | never for an interactive session. A worker had the charter in its brief. |

Three gaps:

- A session in a folder with no node got no charter text, so no global level.
- Codex and agy sessions read no charter text at all.
- The daemon cached the policy answer and the node graph for 2 seconds, so a
  rule the owner had just set could miss a turn.

A fourth gap, a repo's own `rules` hiding the global `rules`, was closed by
[0100](0100-team-questions-go-to-the-lead.md): `rules` and `refuses` now join
across global, team, repo and agent by item. The first version of this change
added a separate "higher charter levels" block for that gap. It is removed:
the effective charter line already holds those rules.

## Decision

1. **The charter line is the carrier.** Every text a model reads is the
   effective charter of its node, which holds the global rules. No second block.
2. **A session that no node covers** gets the global `rules`, `refuses` and
   `escalate` alone (`context.get` when the node text is empty; the daemon
   asks `charter.global` for a worker brief or a new Codex thread of a project
   with no node). This is the one case that needs text apart from the node.
3. **MCP server instructions.** `agent-net mcp` asks the daemon once at start
   (`charter_text`) and adds the answer to its fixed instructions. The fixed
   text stays first and is the fallback for every failure. The total stays
   under 2000 characters, because Claude Code caps the length of MCP server
   text. A Codex or agy MCP process has no session when it starts, so it gets
   the global level only.
4. **Codex.** A thread that agent-net starts or resumes for the owner gets the
   project charter as `developerInstructions` (`thread/start`,
   `thread/resume`). This is the only per-thread text the Codex 0.167 app
   server takes. A worker brief carries the charter as before.
5. **A change shows on the next turn.** The gateway sends `charter.changed` to
   every connected daemon after it commits a charter value at any level, a team
   write or an undo. The daemon counts the frames and uses a cached policy or
   graph answer only from the epoch it was read in. A turn costs no extra
   call, no timer is shortened, and an older gateway keeps the 2-second cache.
6. No limit and no approval is added. The owner sets the rule where he already
   sets charters. The charter, the TUI and `agent_graph_read` do not change.

Not built: a Codex hook. Codex 0.167 has `UserPromptSubmit` hooks with
`additionalContext`, which would give Codex a per-turn path, but setup would
have to write Codex config on the owner's machine. That is a separate decision.

## Tests

- `internal/protocol/global_charter_test.go`: the global-only text and the
  length cap of the instructions.
- `internal/gateway/global_charter_test.go`: global, team and repo rules show in
  one `rules:` line once; a session with no node gets the global level; the
  global level alone; charter writes notify the daemons and a dry run or a
  refused write does not.
- `internal/daemon/global_rules_test.go`: the Claude hook and the Pi and
  OpenCode answers carry the rule and follow a change with the cache clocks
  stopped (so only `charter.changed` can bring the new rule); the MCP
  instructions of a new session; the Codex MCP process; the Codex
  `developerInstructions` at start and at resume; the worker brief.
- `internal/mcpserver/start_instructions_test.go`: the fallback and the cap.
