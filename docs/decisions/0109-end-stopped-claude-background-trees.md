# 0109 - End a Claude background tree only after the roster says it is gone

Date: 2026-11-09
Status: Built on the branch of worker w_c68aac27d237bb11. Not yet released.

## The problem

`claude stop <id>` is a polite request to Claude Code's own supervisor. The
worker can still be alive after it returns. When agent-net stops a Claude
worker or owner session, it should confirm that the process tree really went
away, and end what is left.

An earlier draft of this work assumed that `claude bg-spare` processes with a
settled `~/.claude/jobs/<id>/state.json` were leftovers that could be swept
every ten minutes. That premise was wrong:

- The supervisor keeps its live truth in `~/.claude/daemon/roster.json`, written
  when its workers change and fresh within seconds. Each worker entry has `pid`
  (the bg-pty-host), `replPid` (the claimed process, whose command line still
  says `bg-spare`), `procStart` and `cwd`.
- Every pid in the roster is a live session or worker, including idle
  interactive sessions whose `state.json` says `done`.
- `daemon.status.json` can be hours stale and is not evidence.

## What the survey found

A read-only survey on research on 2026-11-09 found no leftover tree once the
roster was used. Every live tree was in the roster. The only other tree was the
idle spare, which has no session file and no job. The 23 processes measured on
2026-11-08 were gone by then; the pool had reused or retired them. So the safe
conclusion is that there is no standing backlog to sweep on this machine.

## The decision

**No periodic sweep.** A sweep would have to decide that a tree is dead from
state files that are not authoritative. The roster is only written on a change,
so a ten-minute sweep would usually find it stale and do nothing anyway.

**One check after an agent-net stop.** When agent-net stops a Claude worker or
owner session, it records the job's pty host and claimed process (pid, start
time) before `claude stop`, then checks again about ten seconds later. It ends
the tree only if all of these hold:

1. `~/.claude/daemon/roster.json` is readable and its `updatedAt` is within 60
   seconds; a missing or stale roster means do nothing.
2. The roster no longer lists the job, the pty host pid or the claimed pid.
3. `~/.claude/jobs/<job>/state.json` says `stopped`, `done` or `failed`.
4. The real process tree is still there with the recorded start times (a reused
   process number is not touched).
5. The tree belongs to the daemon's own user (real and effective), is not the
   agent-net daemon or gateway, and is not a registered live session or worker.

Ending is SIGTERM to each process of the tree, then SIGKILL after two seconds
for what is still the same run. The start time is checked again before SIGTERM
and before SIGKILL. This is the pattern of `internal/daemon/mcpgroup.go` and the
refusals of the CTO process stop (decision 0102).

**One log line per ended tree.**
`claude.tree job=<id> pid=<host pid> age=<age> rss=<tree RSS> ended=<term|kill|failed>`.
The line goes to the daemon log, the machine log the owner reads with
`agent_machine_log`.

## What it does not do

- It does not sweep or reap anything on its own.
- It does not treat a command name, an age, or a job state as proof that a
  process is dead. The roster comes first.
- It does not use `daemon.status.json`.
- It does not end a tree still in the roster, a working job, a live agent-net
  session or worker, another user's process, the agent-net daemon or the
  gateway.

## If a backlog appears later

A future sweep would have to wait for a fresh roster, not a state file. If a
tree is absent from a fresh roster and no agent-net session or worker owns it,
that is the only safe signal that it is a leftover. Re-check this decision and
the survey before adding one.
