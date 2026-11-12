# 0140 - Keep herdr; do not adopt herdr-core now

**Date:** 2026-11-11
**Status:** Accepted.

## Context

The approved session redesign (one way to start: a daemon-owned background
process; one way to view: full screen through each harness's attach client,
ctrl+] back) leaves herdr needed for: hosting Pi, which has no attach client;
viewing a session on another machine with clipboard and image paste; tabs when
the TUI runs in herdr. The owner asked whether herdr-core
(https://github.com/herdrdev/herdr-core) could replace herdr as the single host
for every session.

## What herdr-core is

Studied 2026-11-11, source and docs read. Rust (edition 2024), Apache-2.0,
version 0.1.20 from its schema (schema/hc.openrpc.json). The repo was created
2026-10-16, has no GitHub releases, is not on crates.io, and has 161 open
issues. schema/README.md: "Status: preview (protocol generation 1, version
0.x)", additive-only within a generation.

A separate process, not a library: a background core, holder processes that
own the PTYs, and the `hc` CLI. Consumers speak JSON-RPC 2.0 over a Unix socket,
with a binary framed mode for attach. No Go package; a pure-Go client would be
ours to write. macOS, Linux and Windows; static Linux builds.

Offers headless PTY hosting (terminal.run), persistence across viewer close,
core restart and update (not reboot), attach/detach with history, remote
machines over ssh (hc machine add, hc connect), terminal.put file transfer up to
8 MB, events.subscribe. README: "Core draws nothing": no tabs, no clipboard.

## How image paste works today

`herdr --remote` does not forward the clipboard. The herdr TUI client reads the
local clipboard image, sends it to the remote herdr server, which stages it as a
temp file (0700 dir, 24 h cleanup) and pastes the file's path as text; Claude
Code loads the image from the path.

herdr docs/next/CHANGELOG.md line 857: "Remote clients now bridge local
clipboard images into the remote pane by staging them as temporary image files
and pasting the remote path, so Claude Code image paste works over
`herdr --remote`. (#205)". Code in https://github.com/herdrdev/herdr: client
src/remote.rs and src/platform/*.rs, server src/server/clipboard_image.rs and
src/server/headless.rs, commit ed478be9 (2026-05-20). Triggered by Ctrl+V or an
empty bracketed paste.

herdr-core leaves this to viewers by design; herdr-app
(https://github.com/herdrdev/herdr-app) does the same through `hc put` and
pastes the path (src/terminal.rs, commit 7d77ac5). Open upstream: herdr-core #84
(remote copy lands on the host clipboard), herdr-app #746, herdr-app #31
(image-paste contract not settled), herdr-core #115 ("whether the existing TUI
will migrate to herdr-core remains undecided"), attach bugs herdr-core #232 to
#234.

## The API question

Owner's and CTO's conclusion: herdr-core is a terminal-level API, bytes in and
out. Claude Code (background jobs, hooks, transcripts), OpenCode (server API)
and Codex (app-server) already give agent-net richer, harness-level APIs, so
moving them onto a terminal API would be a step down. herdr-core's real value is
for Pi and agy, which have no proper API, as a uniform low-level fallback, and
for event-driven session state instead of polling herdr.

## Cost estimate

Adopting it as the single host would delete about 1,000 product lines
(internal/daemon/herdr.go, herdr_lookup.go, herdr_open.go, part of enter.go)
and 1,500 test lines, and add about 1,500 to 2,300 lines (a JSON-RPC and framing
client, attach, an image bridge, installing and updating hc on every machine)
plus tests. The code would not shrink, and it would depend on a young 0.x daemon
on every machine.

## Decision

Keep herdr. The session redesign parts 1 to 3 are unchanged. If agent-net drops
herdr later, it uses its own image bridge over agent-net's file transfer (a
possible part 4: the full-screen view catches Ctrl+V, sends the image with
agent-net's file transfer, pastes the path, over plain ssh), not herdr-core.

## Revisit when

- herdr-core leaves preview;
- its clipboard contract is settled (herdr-core #84, herdr-app #31);
- its attach bugs (#232 to #234) are closed;
- the herdr TUI moves onto core (#115); or
- agent-net's needs for Pi or agy grow.
