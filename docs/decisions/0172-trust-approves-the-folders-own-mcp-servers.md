# 0172 - Folder trust approves the folder's own .mcp.json servers

Date: 2026-11-18

The owner asked that one session start need at most one approval: "yes shit has
to batched. else i'm just a meat proxy fuck that". A start of tenebrae-strats
needed three: a folder trust proposal, then an `mcp_approval_needed` failure
for the folder's own `.mcp.json` server `tenebrae-library`, then a
`session_change` with action `mcp_servers`.

## Decisions

1. **Trust is also yes to the folder's own servers.** When the owner grants
   folder trust, agent-net records `approve` for the servers that the folder's
   own `.mcp.json` declares, in the same write. A folder that has no `.mcp.json`
   is left as it is.
2. **Only the folder's own file.** Servers from a `.mcp.json` in a parent folder
   are not approved by this click. An explicit `mcp_servers: approve` on a start
   still covers every file Claude Code would load, as before.
3. **One click covers proposal, start trust and owner words.** The trust
   proposal apply, `trust_claude: true` on `session_start`, and the CTO's
   `owner_words` path all approve the folder's own servers. An explicit
   `mcp_servers: disable` given with the start still wins.
4. **The owner sees the names.** The trust ask lists the servers the click
   approves. A `needs_trust` start result carries them in `trust_mcp_servers`,
   and the ask the gateway raises from that result lists them.
5. **The undo takes back only what the click added.** The trust undo removes the
   server names that approval added to `enabledMcpjsonServers`, and puts back a
   refusal it took out of `disabledMcpjsonServers`. A server the owner had
   already approved is left alone.

## Consequences

The daemon approves the names in the folder's `.claude/settings.local.json`
before Claude Code starts, so the background job does not stop on the approval
prompt. An older gateway does not send the new server fields, so the daemon
falls back to the servers it finds in the folder's own file. An undo from that
older gateway removes the trust alone and leaves those approvals, because it
does not know which names the click added; upgrade the gateway with the daemons.
The CTO brief no longer sends the CTO to a separate `mcp_servers` step after a
trust.