# 0124 - Files to a Mac use the gateway

Date: 2026-11-09

## Context

A message with files to a Mac was stored on the gateway and the sender log
said `files sent ... path=taildrop`. Nothing arrived. Two messages did this on
2026-11-09 (m_cc49acf4d79e6544 at 09:06 UTC and m_e5c45b60300f02b3 at 14:30),
while same-machine hand-backs arrived in 5 to 7 seconds.

The Mac daemon's log held a goroutine dump from `stopWithin`:

```
goroutine 29567 [syscall, 15 minutes]:
os.Open
takeFile (internal/daemon/files_receive.go:415)
tryTake (internal/daemon/files_receive.go:400)
pass (internal/daemon/files_receive.go:351)
collectFiles (internal/daemon/files_receive.go:242)
collectThenDeliver (internal/daemon/files_receive.go:104)
created by (*Daemon).receiveFiles
```

The daemon reads Taildrop files from `~/Downloads`. On macOS an `open` there
can block in the kernel, most likely in a privacy (TCC) check or on an
iCloud-backed file. The path was an announced Taildrop name of 84 bytes. A
blocked syscall ignores the context, so the collector's deadline never fired,
the message was never delivered, and the stuck goroutine also held the
daemon's wait group, so `stopWithin` could not shut the daemon down.

## Decision

1. The sender never offers files by Taildrop to a receiver whose daemon runs
   on darwin, for every message and not only worker notices. It uses the
   gateway. It reads the receiver's OS from `query.nodes`, which the gateway
   fills from the OS a daemon reports when it enrolls. When the OS is empty
   (an older daemon or gateway), the sender uses the gateway too.
2. A daemon on darwin never stats or opens a file in `~/Downloads`. A sender
   that still uses Taildrop leaves its file there; that file is not collected.
3. On every other OS, the daemon takes the open of an arrived file in a helper
   goroutine with a timeout. The collector stops waiting at its own deadline
   or when the daemon stops. The helper is not counted by `stop`, so a syscall
   that never returns cannot block shutdown.
4. The worker notice rules stay: between machines a worker notice uses the
   gateway, and the receiver waits at most two minutes from when the gateway
   stored it before delivering the report with the names of missing files. A
   notice whose worktree was not pushed says `NOT PUSHED` in its status line.

Same-machine messages still use the local handoff, and a Taildrop that fails
still falls back to the gateway. Ordinary messages still wait for their files
until their existing expiry, and the receiver still checks each file's size
and hash before taking it. This changes the waiting rule in decision 0054 for
files to a Mac.

## Consequences

A file sent to a Mac over Taildrop by an older sender stays in the receiver's
Downloads, and the message fails or times out without it. Upgrading the
sender fixes the route. A blocked open can leave one helper goroutine behind
until the kernel returns; the daemon no longer waits for it.
