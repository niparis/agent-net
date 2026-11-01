# 0037 - Owner sessions are separate from workers

**Date:** 2026-11-01
**Status:** Accepted, from the owner's answers in the
[start-sessions design](../design/start-sessions.md).

An owner session is a normal harness session started by the person through
agent-net. It has no worker brief, hand-back, time limit, owner-agent link,
completion notice or automatic cleanup. It never appears in worker lists.
The launch mechanics are shared with workers; the worker records and rules
are not. Closing the TUI or restarting the daemon leaves the session running.

The first part adds owner-gated local `session_start` for Claude in a known
project folder, plus `session_start_get` to read the transition from starting
to a registered session. A separate in-memory record holds the launch time,
folder, branch and native and gateway IDs. Normal session discovery works
independently of this record. The record can be lost on daemon restart
without ending or hiding the session. A failed registration never makes a
started owner session subject to worker cleanup.

The remaining harnesses, TUI form, remote starts, folder creation, worktrees,
resume and stop actions follow the accepted design in later parts. No agent
or CTO tool may bypass the owner gate to start an owner session.
