# 0101 - A Claude worktree copies the MCP choice of the main checkout

Date: 2026-11-08
Status: Requested by the caller (agent-net 2). Built.

## The problem

Two Claude workers for the project tenebrae-strats failed on 2026-11-08. Each sat
10 minutes and the daemon logged `worker.stopped reason=start_failed ... wrote
no session file in time`. The Claude job said `backgrounded ... (idle - send a
prompt to start)`. The project has a `.mcp.json` with the server
`tenebrae-library`. A worker gets a new git worktree, and a new worktree is a new
launch folder for Claude Code. The answer to "new MCP server needs approval"
([decision 0096](0096-claude-mcp-approval-and-start-failures.md)) is stored per
launch folder, so the prompt came back for every worker. A background job cannot
answer it. Load was not the cause: the second failure ran at load 4.6.

Decision 0096 made the block visible and gave the CTO a way to answer it. That
does not help a worker start, which needs no manual step.

## The decision

- Before a Claude launch in a worktree, the machine copies the owner's answers for
  the main checkout into the worktree's `.claude/settings.local.json`. It reads
  `enabledMcpjsonServers`, `disabledMcpjsonServers` and
  `enableAllProjectMcpServers` from `.claude/settings.local.json` of the main
  checkout folder and of each folder above it. The lists are joined. The
  nearest file that has the switch decides it. A refusal wins over an approval.
  The switch is written as the names of the servers the worktree's `.mcp.json`
  files define now, never as the switch, so a server added later still asks
  (the rule of 0096 stays).
- Only names of servers that the worktree defines are written. Names the
  worktree file already has, and every other key, are left alone. A settings file
  that git tracks is the project's own and is not edited.
- This applies to a Claude worker, and to a Claude session that starts in a new
  worktree (`folder: worktree`), the merger's included.
- A server with no answer is left without one. The start goes on and, if Claude
  Code blocks, 0096's check fails it within a few seconds with
  `mcp_approval_needed`. A worker's blocked job is stopped, because nobody
  attaches to it, and the text says to retry `worker_start` with `mcp_servers`.
- `worker_start` takes `mcp_servers: approve | disable`, the same field as
  `session_start`. It applies to the worker's folder after the copy. It is
  refused for a non-Claude harness and with `continue`. It travels through the
  gateway's launch frame and the machine's start queue like `base`.
- The copied file must not dirty the worktree. If `git check-ignore` does not
  already ignore it (the repository's `.gitignore`, or the user's global ignore
  list, which Claude Code fills), the daemon appends `/.claude/settings.local.json`
  to `info/exclude`. Git has one `info/exclude` for a repository and its worktrees,
  so the line also covers the main checkout. The repository's `.gitignore` is
  never changed.

## A note on parent folders

Decision 0096 found that Claude Code 2.1.319 does not read a parent folder's
`settings.local.json` for a session in a child folder. The copy reads the parent
folders anyway, because the owner's answer there (for tenebrae:
`enableAllProjectMcpServers: true` with `tenebrae-library` approved) says what
the owner wants for the whole tree, and the caller asked for it. The effect is
that a worktree of a project can be more approved than Claude Code would make
the project's own main checkout.

## What we gave up

A copied approval lets the worker's session run the `.mcp.json` command as the
owner. The owner already gave that approval for the main checkout (or a folder
above it), and a worker runs commands from the repository anyway. A worker that
edits `.mcp.json` in its worktree to add a server gets no approval for it,
because only the names found at launch are written. A Claude Code that changes
how it reads `settings.local.json` makes the copy useless without notice; the
fast `mcp_approval_needed` failure is the signal.
