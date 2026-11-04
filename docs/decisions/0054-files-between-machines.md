# 0054 - Files between machines

**Date:** 2026-11-03
**Status:** Accepted. The gateway transfer in parts (T2) and the daemon's
send and receive (T3) are built. The session tools (T4) and the owner flow
(T5) come in later steps.

## Context

agent-net can move a message between machines but not a file. The owner needs
to send a report, an image or a large skill from one machine to a session on
another. The hardening plan, workstream W5, splits this into a spike and four
build steps. This record covers T1 and T2.

The spike found:

- On macOS with Tailscale 1.92.5 (Mac App Store build) an incoming file lands
  in `~/Downloads` under its own name. No `tailscale file get` step is needed.
  A name with spaces arrived intact.
- On Linux with Tailscale 1.104.4 files wait for `tailscale file get <dir>`.
  That works as the normal user, without root.
- A 90 KB file arrived from the Mac at once. A small file from the research
  machine arrived in under 0.1 s. Files above 100 MB and a receiver that is
  offline at send time were not tested.

## Decision

### A message with files

A message carries a manifest. For each file it holds the name, the size, the
SHA-256 and an ID. The message text says where the files are.

### Two paths

- **Same tailnet: Taildrop.** The sender gives every file a unique name that
  starts with `agent-net-<message id>-`. The Mac daemon picks the announced
  files up from `~/Downloads`, matched by name and hash. The Linux daemon
  collects into a staging folder. It moves any file it was not told about to
  `~/Downloads`, so the owner's own Taildrops still arrive where they expect.
- **Otherwise: the gateway in parts.** The same transfer in parts that a skill
  above 2 MB needs (decision 0048). A skill is not a file message, so the
  gateway API knows only an upload ID, parts and a final hash.

The receiving agent's message names the folder. Only the receiving session's
folder gets the files.

### Limits

- At most 100 MB per file.
- At most 10 files per message.
- Allowed source folders: the session's project or worktree, and `/tmp`.
  Never `~/.ssh`, keychains, agent-net state or other credential paths.
- Every text file goes through the existing secret scanner before it is sent.
  A hit stops the file unless the owner sends it anyway.
- Received files are kept 7 days, then deleted.

These are daemon rules for T3. The gateway enforces its own limits on the
transfer in parts, below.

## Transfer in parts

The gateway stores bytes in parts so no frame reaches the 4 MB WebSocket
limit. It is generic: a file message and a skill copy use the same frames.

- **Parts of 1 MB.** Each part carries its own SHA-256. A part that fails the
  check is refused and sent again. One part as JSON is about 1.4 MB, well
  under the frame limit.
- **One upload ID.** `xfer.begin` names the receiving machine, the size and the
  whole SHA-256. The gateway answers with an upload ID, the part layout and
  its expiry.
- **Resume.** `xfer.status` lists the parts the gateway already holds. The
  sender sends only the missing ones. A new link can continue an upload that
  a dropped connection interrupted.
- **Whole check.** `xfer.finish` reads the stored parts, checks the whole
  SHA-256 and marks the upload ready. A mismatch drops the upload.
- **Download.** `xfer.get` returns one part, with its hash and the whole hash.
  Only the machine named as receiver may call it, and only after the whole
  check.
- **Delete.** `xfer.confirm` from the receiver deletes the parts at once. The
  gateway also deletes every upload 7 days after it began, downloaded or not.
- **Storage.** The parts live under `<gateway data>/transfers/<upload id>/`,
  folders 0700 and files 0600. They are never written elsewhere.
- **Limits.** The gateway refuses an upload above 100 MB. It also refuses one
  that would leave a sending machine with more than 1 GB staged. That cap is
  one maximum message (10 files of 100 MB), so the cap never refuses a message
  the per-file limits allow, and one machine cannot fill the gateway disk. A
  sender waits for a confirmation or an expiry before staging the next
  maximum message.
- **Who may act.** Only an enrolled machine with a valid node token can open a
  link. Only the sending machine may upload or resume an upload. Only the
  receiving machine may download or confirm it. The gateway does not look at
  the bytes; it checks lengths and hashes.

The gateway does not scan the bytes for secrets. The daemon does that before
it sends. The gateway data folder is the owner's private folder, like the
message store.

## What T3 needs from this API

- `xfer.begin`, `xfer.part`, `xfer.finish` and `xfer.status` to send, with the
  same upload ID kept to resume after a reconnect.
- `xfer.get` and `xfer.confirm` to receive and free the space.
- The part size from `xfer.begin`, not a constant, so a later change of the
  part size needs no daemon release.
- The whole SHA-256 from the manifest, which the gateway checks before the
  receiver may download.
- The receiver node ID from the session's machine, which the gateway checks
  against the link.

T3 adds the path checks, the manifest, Taildrop, the inbox folder and the
"delivered only when every file is in" rule. T4 adds the tools. T5 adds the
owner flow.

## T3: send and receive (2026-11-04)

- **The manifest is a field, not text.** `files` is a field of `msg.send` and
  of the message. The gateway stores it in its own table, `message_files`,
  checks it again with the same limits, and hands it over with every push. A
  sender reads the stored message back: a gateway that dropped the field is
  too old, and the send says so.
- **The message goes first, then the bytes.** The unique name holds the
  message ID, which only the gateway assigns. So the daemon stores the message
  and then pushes the files. A message with files is never queued in the
  outbox.
- **The receiver finds uploads by name.** Every file travels under its unique
  name on both paths; the gateway upload is named the same way. A new frame,
  `xfer.find`, lists the finished uploads addressed to the calling machine
  with given names. So a Taildrop that fails after the message was sent falls
  back to the gateway with no second message, and the receiver looks in every
  place on each pass. The manifest carries no upload ID.
- **Same tailnet means a peer in `tailscale status`.** Each daemon reports its
  Tailscale address in `hello` (`tailscale_ip`, read from the network
  interfaces). The sender uses Taildrop only when that address is an online
  peer of its own tailnet. The gateway keeps the address in its own table,
  `node_tailscale`.
- **Same machine.** Between two sessions of one machine the copy moves through
  a local folder; neither Taildrop (which cannot send to itself) nor the
  gateway carries it.
- **A held message keeps its files on the gateway.** If the owner refuses it,
  the gateway deletes the bytes after 7 days; a Taildrop would leave them in
  the receiver's Downloads.
- **Snapshots.** The sender copies each file while hashing it and sends the
  copy, so a file that changes after the check cannot fail the receiver's
  hash.
- **Half-written files.** A copy that fails its hash is refused only when the
  same copy (same size and time) fails twice in a row, so a Taildrop still
  being written is not taken for a bad file.
- **Waiting.** The receiver waits until the message expires (24 hours), and
  the sender sees `waiting for files: n of total in`. A message with files can
  reach its agent after a later message without files.
- **A worker's worktree.** It lives in agent-net's state folder, which is
  otherwise closed. A root inside that folder opens only itself, so a worker
  can send what it built.

## Left out

- Resuming a download part by part. A receiver starts the download again; the
  gateway still holds every part until the confirmation.
- More than one receiver per upload.
- Compression and encryption of the parts beyond the pinned TLS link.
