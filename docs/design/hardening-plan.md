# Hardening plan: delivery, files, security, docs

**Date:** 2026-11-03
**Status:** W1 to W6 approved by the owner on 2026-11-03 and started; W7 on the roadmap only, optional when built
**Source:**
- the external reverse-engineering report of commit 989e42bd (15 findings);
- the gap found the same day: no way to move a file between machines or agents.

## Goal

agent-net's main job is that a message or a file arrives exactly, and that only the right machines and agents can act. Fix every finding of the report that matters for one owner with a few machines, and add files as a first-class thing to send.

## Workstreams

### W1. Docs that match the code

One worker, half a day.

- **README, the worker sandbox:** say plainly that only Codex workers (and agy with its own flag) are sandboxed; Claude, Pi and OpenCode workers run with the owner's permissions until W7.
- **README, the harnesses table:** five harnesses, with agy.
- **One backup instruction:** `agent-net gateway backup`, in security.md and reference.md.
- **New section in reference.md,** "Dependencies on harness internals": Claude Code's `messagingSocketPath` inbox and its frame format (from open-cross-session), Codex's app-server control socket, OpenCode's server API, agy's hooks. What breaks if each changes, and how agent-net notices (W4.5).
- **security.md:** the gateway's listening address and what is open without a token.

### W2. Link health

One worker.

- **Keepalive:** a WebSocket ping every 20 s from both the daemon and the gateway; a link with no pong for 45 s is closed and redialled.
- **The offline mark:** the gateway marks a machine offline as soon as its link closes, not after a 30 s wait plus TCP timeouts.
- **Verification:** a real test with the Mac asleep. Its row goes offline within about a minute and comes back on wake, without a restart.

### W3. Housekeeping and logs

One worker. The owner's decision: messages are kept as long as possible; nothing deletes message bodies.

- **The daemon's `delivered` table** (duplicate check only): rows older than the message expiry (24 h) plus a margin are pruned.
- **A weekly `VACUUM`** of gateway.db, when its free pages exceed 20%.
- **macOS logs:** rotated by size (10 MB, 5 files kept), done by the daemon itself, since launchd does not rotate.

### W4. Delivery reliability

The most important workstream. Five steps, run in order 1, 2, 3, then 4 and 5 in parallel.

**Status (2026-11-04):** steps 1 to 3 are built (decision 0053, with its W4.2 and W4.3 updates). Steps 4 and 5 are next.

1. **No silent loss, no blind duplicate.**
   - The receiving daemon writes the delivery as `injecting` before it writes to the harness, and as `delivered` only after the harness confirms.
   - A write error is `failed`, not "delivered (unconfirmed)".
   - After a crash, an `injecting` row is reported as "may have arrived" to the sender, and not re-injected. A lost turn is worse than a duplicate, but a silent duplicate is worse than a notice.
   - Each harness reports a confirmation where it can: the plugin ack (Pi, OpenCode), the queue accept (Codex), the hook reply (agy), and for Claude the socket write plus the session file showing the message (to test).
2. **Retry.** Failed and unconfirmed deliveries are retried with backoff (30 s, 2 min, 10 min, 30 min) until the message expires. Today only a reconnect retries.
3. **The sender is told.** A message that expires or fails for good produces a short notice to the sender: "message to X was not delivered: reason". No silent expiry.
4. **Claude's own replies.** The map from "B answers A" to the request it answers is stored in daemon.db, not in memory. It is matched by conversation and by the most recent open request from that sender, so two open requests no longer cross. When the match is unsure, the reply is threaded to the conversation, not to a guessed message.
   - **Built (2026-11-04), not yet released:** the `claude_deliveries` table in daemon.db, one reply socket per sender and conversation, reopened at daemon start; several open requests with none named in the answer give a reply in the conversation with no parent (`conversation_id` on `msg.send`); rows pruned 24 h after the message expiry. See [Claude replies](../reference.md#claude-replies) and decision 0053.
5. **The Claude socket contract.**
   - At daemon start and after each Claude Code update, the daemon checks that a session file still has `messagingSocketPath` and that the socket accepts a frame.
   - If not: a warning in the TUI, on the Graph machine card and in `nodes -wide`, saying "Claude delivery broken by Claude Code <version>". Claude sessions are then marked not deliverable, so senders get "failed" at once instead of silence.

**Verification:** a real test on the live network for each step, with a fake failure injected (a socket removed, a daemon killed mid-write). The results are written down.

### W5. Files between machines and agents (Taildrop)

A spike first, then four build steps.

- **T0. Spike (the orchestrator, 1 to 2 hours):**
  - Linux: confirmed, `tailscale file get <dir>` works as the user.
  - Mac: does the app save incoming files straight into ~/Downloads, or hold them for `tailscale file get`?
  - File names with spaces, collisions, the largest size tried (500 MB), speed.
  - What happens when the receiver is offline: Taildrop waits, or fails?
- **T0 results (2026-11-03):**
  - **Mac**, Tailscale 1.92.5, Mac App Store build: an incoming file is saved straight into `~/Downloads` under its own name, with no `tailscale file get` step. A name with spaces arrived intact.
  - **Linux** (research, Tailscale 1.104.4): files wait until `tailscale file get <dir>` collects them. That works as the normal user, without root.
  - **Speed and size:** a 90 KB file arrived from the Mac at once, and a small file from research arrived in under 0.1 s.
  - **Consequence for T3:**
    - The sender gives every file a unique name that starts with `agent-net-<message id>-`.
    - The Mac daemon picks announced files up from `~/Downloads`, matched by name and hash.
    - The Linux daemon collects into a staging folder and moves any file it was not told about to `~/Downloads`, so the owner's own Taildrops still arrive where they expect.
  - **Not yet tested:** a receiver that is offline when the file is sent, and files above 100 MB.
- **T1. Decision record:**
  - A message carries a manifest: name, size, sha256, and an id per file.
  - The bytes go over Taildrop when both machines are on the same tailnet, under a unique name derived from the message id.
  - Otherwise they go through the gateway in parts. This uses the same transfer in parts that skills above 2 MB need (decision 0048, step 7).
  - **Limits:**
    - a maximum per file, 100 MB;
    - a maximum per message, 10 files;
    - allowed source folders: the session's project or worktree and /tmp, never ~/.ssh, keychains, agent-net state or other credential paths;
    - a secret scan of text files with the existing scanner;
    - received files are kept 7 days;
    - only the receiving session's folder.
- **T2. Gateway transfer in parts:**
  - upload and download in 1 MB parts, with a checksum on each;
  - resumable;
  - stored in the gateway data folder and deleted after delivery or expiry.
- **T3. Daemon send and receive:**
  - Send: check the path and the limits, hash, send the manifest, push the bytes (Taildrop or gateway).
  - Receive:
    - collect the files;
    - match each to its manifest by name and hash;
    - move it to `~/.local/state/agent-net/daemon/inbox/<session>/<message>/`;
    - mark the message delivered only when every file is in;
    - leave files nobody announced untouched, so the owner's own Taildrops are never taken.
  - The receiving agent's message says where the files are.
- **T4. Tools and harnesses:**
  - `agent_file_send(to, paths, note)` for sessions.
  - A `files` list on `agent_handback` for workers, which cannot send messages.
  - All five harness integrations, the MCP schemas, the guidance text.
- **T5. Owner flow:** "Send file" in the TUI, from any machine to any session. (A CTO-initiated fetch from the owner's machines was dropped.)

**Verification:** a real Mac to research transfer of the report.html case, through each path, in both directions.

### W6. Security

Four independent items; they can run in parallel.

1. **Revoke and rotate machine tokens.** The owner removes a machine or rotates its token from the TUI (Graph or Settings) or through a CTO proposal. A removed machine's link is closed at once and its sessions marked offline.
2. **The gateway's listening address:** nothing that works today changes.
   - A loud warning at gateway start (log, TUI, `nodes`) when it is reachable on a public address.
   - Rate limits on enrollment.
   - A new install on a machine with Tailscale offers the Tailscale address.
3. **Signed releases:**
   - checksums signed (ed25519) in the release workflow, with a key held only in GitHub Actions secrets: chosen over keyless signing because verification stays a few lines of standard Go, offline, with no extra library or trust data to keep current;
   - the public key embedded in the binary and in install.sh;
   - `upgrade` and the installer refuse an unsigned or mismatching release;
   - install.sh served from the tagged release, not from main;
   - a setup option to turn auto-upgrade off.
Scheduled backups were dropped: the owner keeps backups outside agent-net. The manual `gateway backup` command stays.

### W7. A sandbox for every worker (roadmap only)

Not started. On the README roadmap. When built it is optional, switched on per project.

- **D. Design note:**
  - Linux options: bubblewrap, Landlock;
  - macOS: sandbox-exec, which still works although deprecated;
  - what each harness needs to read and write (its config, its runtime folders, the worktree, /tmp, the network);
  - what to do on a machine where the tool is missing;
  - how the owner opts a project out.
- **B. Build:** a wrapper used by every worker launcher. The existing Codex and agy sandboxes stay, inside it. Tested with a worker that tries to write in ~ and must fail.

## Order and releases

| Phase | Work | Parallel? | Release |
|---|---|---|---|
| 1 | W1 docs, W2 keepalive, W3 retention, W6.1 token revoke, T0 Taildrop spike | yes, separate files | one release |
| 2 | W4.1, then W4.2, then W4.3 (delivery core) | in order; W6.2 and W6.4 in parallel | one release |
| 3 | W4.4 Claude replies, W4.5 socket check | yes | one release |
| 4 | T1 decision, then T2 + T3, then T4, then T5 | partly | a release after T4, one after T5 |
| 5 | W6.3 signed releases | alone (it changes the release itself) | the release that turns signing on |
| 6 | W7 design, then build | design alone | one release |

Before each release:
- the touched packages are tested after merging (the lesson of 2026-11-03);
- each item that changes behaviour is checked on the live network by the orchestrator.

## Decisions (2026-11-03)

- Message bodies are kept as long as possible; no retention deletion.
- The gateway's listening address does not change for existing setups; a public-exposure warning and enrollment limits are added.
- Releases are signed with an ed25519 key held in GitHub Actions secrets.
- Backups stay outside agent-net.
- No CTO-initiated file fetch; the owner sends files from the TUI.
