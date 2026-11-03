# 0055 - Antigravity workers survive daemon restarts

Date: 2026-11-03

## Context

Antigravity workers used daemon-owned stdin and stdout pipes. A release
restarts the daemon. The worker could finish its edits but lose its result
and hand-back when those pipes closed.

## Decision

Use real reattachment (option A), with the same process and conversation.
Like Pi, agy inherits a named pipe opened for both reading and writing.
Its own descriptor keeps stdin open while no daemon is running. Stdout goes
directly to a private file, without a daemon-owned copier pipe.

The daemon tails complete JSON lines. A private checkpoint stores the
consumed offset, cumulative usage, turn completion and acknowledged
hand-back together. Adoption registers the same session, restores its busy
turn, opens the FIFO and tails from that offset. Results written during
downtime are read even if the process has since exited.

The MCP shim reconnects to the daemon socket and repeats its conversation
hello on the next call. Hand-back is acknowledged only after its report is
saved. Publication waits for the result, so usage is included. A pending
report stays in the checkpoint until publication succeeds. Its notice uses
a stable idempotency key derived from the worker, stream generation and result offset.

No replacement model turn is needed, so recovery adds no caller rework or
automatic continuation. A normal stop kills the process group; closing the
daemon's FIFO descriptor alone no longer ends stdin.

## Limits

This changes newly started agy workers. Workers started by an older daemon
have no persistent streams and cannot be reattached with this method.

The fake harness exercises FIFO inheritance, daemon replacement, MCP
hand-back after replacement, a result and process exit during downtime,
usage retention and notice deduplication. No real agy or paid model is used.
The inherited FIFO pattern already used by Pi is shared by Linux and macOS;
these tests run on the host platform.
