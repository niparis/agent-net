# 0094 - Claude MCP server approval and start failures reach the caller

Date: 2026-11-08
Status: Accepted by the owner, through the CTO. Built.

## The problem

On 2026-11-08 the CTO started Claude sessions for finget, slitherswap,
freqtradebots and tradedesk, with `trust: true`. All four starts failed with
"wrote no session file in time", "context deadline exceeded" or "signal:
killed". The Claude background jobs were alive. Their `state.json` said state
`blocked` and detail `1 new MCP server needs approval`. The `.mcp.json` sits in
the parent folder (`tenebrae/.mcp.json`) and defines one server,
`tenebrae-library`. Claude Code asks the user to approve it. A background job
cannot answer, so it waits forever.

Two more gaps showed up:

1. The CTO never learned of the failure. A start answers `starting` at once and
   fails later. Only the owner's TUI and the log heard about it.
2. The CTO had no way to answer the approval prompt. The owner said he does not
   do manual steps.

## What we found in Claude Code 2.1.319

The answer is stored in `<launch folder>/.claude/settings.local.json`:
`enabledMcpjsonServers` and `disabledMcpjsonServers`. We read the binary and a
live file. After the owner refused the server for finget, Claude Code wrote
`finget/.claude/settings.local.json` with `"disabledMcpjsonServers":
["tenebrae-library"]`. The parent folder has its own file with
`enabledMcpjsonServers` and `enableAllProjectMcpServers: true`; that does not
cover the child folders. The `projects[<path>].enabledMcpjsonServers` lists in
`~/.claude.json` are legacy: the version clears them. A refusal wins over an
approval. `.mcp.json` is read from the launch folder and every folder above it,
and a nearer file wins for the same name.

## The decision

- A Claude start that waits for the session file also reads the job state. If the
  job is `blocked` with an MCP approval detail, the start fails at once with the
  reason code `mcp_approval_needed`. The error names the folder, the servers and
  the command each runs, the `.mcp.json` files, the job ID and whether the job
  is still alive. We leave the job running, because it holds the prompt. We do
  not raise any timeout.
- A start the CTO asked for tells the CTO when it fails: a notification from the
  owner session with the reason code and the error. The start call answers
  `starting` first, so this is the only way the CTO can learn of it. The same
  holds for a start on another machine, from the owner's machine.
- `session_start` takes `mcp_servers: approve | disable`. Before launching,
  agent-net writes that choice into the session folder's
  `.claude/settings.local.json` for the servers in the `.mcp.json` files above
  it. `session_change` action `mcp_servers` does it without a start, for a
  project or path on any machine.
- agent-net never sets `enableAllProjectMcpServers`. It names the servers it
  found, so a server added to `.mcp.json` later still asks.
- Each use is audited with an Inbox notice that names the servers and the
  commands they run. The undo removes exactly the names the use added, and puts
  back a refusal an approval removed. The relay uses its own frames
  (`session.mcp_servers`), so an older gateway or machine refuses and never
  treats it as a folder trust.

The owner and the CTO lean to `disable` for the four repos, because the library
reaches the sessions through agent-net.

## What we gave up

An approved server runs a program from the repository as the owner. This is the
same trust a trusted folder already gives, and the owner asked for it. We added
no extra gate or limit. The blocked job stays alive until someone stops it or
attaches to it. A retry with `mcp_servers` starts a second job; the first one
is not stopped for the caller.
