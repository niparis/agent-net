# 0149 - The owner's herdr is read-only; agent-net hosts in its own herdr

Date: 2026-11-12
Status: Accepted by the owner, through the CTO (two rules for the session redesign, below)

## Problem

The owner's words, relayed by the CTO:

1. "A herdr session the owner runs himself, with his own panes (outside or
   before agent-net), is read-only for agent-net: agent-net discovers and
   shows any harness running there and lets him enter it, but never creates
   new sessions, panes or tabs in it."
2. "Every new session agent-net creates goes into a herdr server/process owned
   by agent-net."

agent-net broke both. Enter from a TUI in herdr created a full tab in that
herdr (decision 0142: "In herdr, open a full tab"). The headless server for
Pi, remote viewers and sign-ins was the default `herdr server`, which is the
owner's own default session. One morning the owner's default session on one
machine held 47 viewer and leftover tabs. The daemon also focused panes,
closed Pi panes and typed into Antigravity panes there.

## Decision

**The owner's herdr is read-only.** On any herdr server that is not
agent-net's own, the daemon only runs `pane list` and `pane process-info`. It
finds and lists a harness that runs there, and Enter attaches to its pane from
the TUI's terminal. It never creates a tab or workspace, runs a command,
focuses, types into, splits or closes a pane there, and never starts the
default server.

- Enter from a TUI inside the owner's herdr hands over the TUI's own pane, the
  same way as in a plain terminal: the harness client runs in it under the
  agent-net bar, and ctrl+] returns. This replaces 0142's "In herdr, open a
  full tab". A start from such a TUI opens nothing; Enter opens it.
- Stop of a Pi or terminal session in the owner's herdr signals the session's
  registered process instead of closing its pane.
- An Antigravity session in the owner's herdr gets no typed text. Its messages
  wait for its next hook (Stop or PreInvocation).
- A session in the owner's herdr on another machine is opened with
  `herdr --remote <host>` (and `--session <name>` for a named session there).
  Its pane is not focused first; the note names the pane.

**agent-net hosts in its own herdr.** Everything the daemon itself hosts in
herdr goes into the named herdr session `agent-net`: a Pi session, a view that
another machine asked for, a sign-in that another machine asked for, and a CTO
view asked from another machine. The daemon starts it on demand with
`herdr --session agent-net server`, in its own process session so it outlives
a daemon restart, and finds its socket with `herdr session list --json`. A new
server has no workspace, so the first view creates one; later views are tabs.

Another machine opens a view there with
`herdr --remote <host> --session agent-net`, so decision 0046 still holds:
herdr's remote client whenever a local herdr exists, never ssh. Without a
local herdr, the ssh plan attaches with `HERDR_SESSION=agent-net`.

`EnterPlan.herdr_session` and `SignInPrepareResult.session` carry the session
name. An older daemon leaves them empty; then no `--session` is passed, which
is what that daemon expects.

## Not changed

The Claude, OpenCode and Codex start paths of decision 0148 stay as they are.
Discovery of herdr panes (`findHerdrPane`, `herdrPanes`,
`herdrPaneProcesses`) is unchanged. Idle compaction still reads pane focus
with `pane list`.

## Consequences

- A local sign-in from a TUI in herdr now runs in the TUI's own pane. The
  daemon no longer watches that pane, so the quota check after the login runs
  on its normal schedule, as it did already from a plain terminal.
- Pi sessions the owner started himself in his own herdr can still be entered
  and stopped. Pi sessions agent-net starts live in its own herdr; Enter
  attaches to them like any other.
