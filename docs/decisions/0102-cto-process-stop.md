# 0102 - CTO process stop

Date: 2026-11-08
Status: Accepted by the owner, through the CTO.

## The problem

The owner does no manual steps. Orphan processes pile up on the machines: a
research daemon restart left MCP servers behind (pid 1491881 and its python
child), and other orphans sat on other machines. Nobody could stop them.

- The owner ran `kill` on the wrong machine by mistake. That is why the owner
  wants the CTO to do it.
- An agent's own permission guard refuses `kill`, so no agent session could do it
  either.

## The decision

The CTO stops processes on a machine, on the owner's instruction, with
`settings_change` area `process_stop`, `node_id` and `pids`. It acts at once. It
is not a proposal, and no other gate was added: the CTO decides when to use it.

I picked the `settings_change` machine areas (`machine_name`, `machine_remove`,
`machine_rotate`) because a process stop belongs to a machine, not to a session.
The concrete call is the owner operator method `process_stop`, served like
`folder_trust` (decision 0091): to the owner at the keyboard and to the CTO,
refused for agent clients.

**The machine is always named.** `node_id` is required and never defaults to the
local machine. This answers the wrong-machine mistake directly.

**What the machine refuses.** Pid 1 and any number below 1; the agent-net daemon
itself; any `agent-net daemon` or `agent-net gateway` process; a live session or
worker that the daemon knows by its registered process ID; any process that does
not belong to the daemon's own user. The reason goes in the result.

**What it does.** For each pid it reads the command line, the parent pid and the
start time first. Then it sends SIGTERM to every allowed pid, waits up to 5
seconds, and sends SIGKILL to any that is still the same process and still alive.
It checks the process start time again before SIGKILL, so a reused process number
is never killed. Each pid ends as `refused` (with the reason), `stopped_term`,
`stopped_kill` or `not_found`. I added `failed` for a signal that errors or a
process that survives SIGKILL, so the result never claims a stop that did not
happen.

**Relay.** A stop on another machine travels as the owner frames
`session.stop_process` (to the gateway) and `session.owner_stop_process` (to the
machine), the same way folder trust does. The gateway checks the owner session
and the target machine checks the owner flag again.

**Audit.** The result, the Inbox notice and the daemon log carry the command
line, parent pid and start time of each pid, and its outcome. The gateway keeps an
event with pids and outcomes. The notice says there is no undo, because a stopped
process cannot be brought back, and it offers none.

## What we gave up

The CTO can now stop any process of the owner's user, on any enrolled machine,
and nothing undoes it. The refusals keep it away from system processes and from
agent-net itself. They do not tell an orphan from a process the owner still
needs, and a parent or child of a live session is not protected, only the
registered process. We accept this because the owner asked for it, the owner
chose not to add an approval gate, and the Inbox notice records exactly what was
stopped.

Command lines can hold secrets such as an API key in an argument. They now reach
the Inbox and the daemon log, cut at 400 characters. They are what `ps` shows to
any user on the machine.
