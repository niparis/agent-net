# 0108 - End settled Claude background processes

Date: 2026-11-09
Status: Built on the branch of worker w_c68aac27d237bb11. Not yet released.

## The problem

Claude Code keeps a pool of spare background processes. A `claude --bg` job
claims one, and the process keeps the command line `bg-spare` for the rest of
its life. When the job settles, the Claude daemon retires the spare after about
an hour of idle, and the process exits.

That retire is not reliable enough on the machines. On 2026-11-08 research had
23 `claude bg-spare` processes holding 2.9 GB PSS, the oldest 4.5 days old.
`daemon.status.json` listed no workers, so they were not doing work. They came
from finished jobs and from the spare pool, and they sat there until they were
reused or the pool was cleaned by hand. Memory pressure then killed a worker.

## What the daemon reads

- The live spare process has a session file, `~/.claude/sessions/<spare pid>.json`,
  with `jobId`. The idle spare has no session file.
- The job state is `~/.claude/jobs/<job>/state.json`, with `state` one of
  `working`, `stopped`, `done` or `failed`.
- A `claude bg-pty-host` process and the `bg-spare` process below it form one
  tree.

Reading the session file is cheaper and more precise than parsing
`~/.claude/daemon.log`: it names the job without a clock, and it says "no job
claimed this spare" by being absent.

## The decision

**One sweep every ten minutes.** It lists the processes, keeps only
`claude bg-pty-host` trees, and ends a tree when all of these hold:

- the tree is older than `claude.reap_after` (default `30m`);
- its job's state is `stopped`, `done` or `failed`;
- a job claimed it (the idle spare is kept);
- it belongs to the daemon's user (real and effective);
- it is not the agent-net daemon or gateway, and not a registered live session
  or worker;
- it is not the Claude daemon or its spare pool: those have no settled job.

Ending is SIGTERM to every process of the tree, then SIGKILL after two seconds
for what is left. The process start time is read again before SIGTERM and
before SIGKILL, so a reused process number is never signalled. This is the
pattern of `internal/daemon/mcpgroup.go`, and the refusals of the CTO process
stop (decision 0102).

**One check after a stop.** When agent-net stops a Claude worker or owner
session it records the tree before `claude stop`, because the stop removes the
session file. About ten seconds later it checks that the tree exited. If the job
is settled and the tree is still there, it ends it. This closes the gap between
a stop and the next ten-minute sweep.

**Settings.** `claude.reap_settled` (default `true`) turns the sweep off.
`claude.reap_after` (default `30m`) sets the age. Both are live advice settings
at network scope.

**One log line per ended tree.** `claude.reap job=<id> pid=<host pid>
age=<age> rss=<tree RSS> reason=<settled|stopped> ended=<term|kill|failed>`.
The line goes to the daemon log, the machine log the owner reads with
`agent_machine_log`.

## What it does not do

- It does not change Claude Code, its pool or its retire.
- It does not end a spare whose job is `working`, even when the process looks
  idle.
- It does not end the idle spare, the Claude daemon, the spare-pool host, the
  agent-net daemon, the gateway, another user's process, or a live session.
- It does not add a polling loop beside the ten-minute sweep. The only other
  check is the one-shot ten-second check that follows a stop agent-net made.
