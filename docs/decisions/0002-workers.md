# 0002 - Workers: sessions started through agent-net

**Date:** 2026-10-29
**Status:** Accepted by the owner
**Context:** the owner wants to say "start a new opencode session in project <name> with <task> and <model>", from the CLI or from any agent.

## W1 - What a worker is

A worker is an agent session that agent-net starts, with no person watching it. It has an owner: the session (or the CLI on a host) that started it. It is listed by `agent_list` and `agent-net list` with its owner.

## W2 - Who can start one

The owner from the CLI (`agent-net start`) and any agent session (`agent_start`). A worker cannot start workers. This stops chains of agents starting agents.

## W3 - Where

- The caller names a project. The gateway resolves it from the sessions it has seen (`agent-net projects`) to a machine and a folder. Only known projects are allowed, never a free path.
- The harness must be installed on that machine, and the model must be in that harness's model list when the machine reports one.
- Machines accept workers by default. A machine opts out with `agent-net setup --no-launch`.

## W4 - How it starts

The daemon of the target machine starts the harness as its own child process, in a new process group, in the project folder. The gateway never runs processes. The command differs per harness, because nobody types into it:

| Harness | Command | Order |
|---|---|---|
| OpenCode | `opencode run` or a per-worker `opencode serve`, with `--model` | first |
| Claude Code | `claude -p` with stream-json input and output | second |
| Pi | `pi --mode rpc` | later |
| Codex | `codex app-server` | later |

## W5 - The task and the reply

The task is sent as a normal agent-net message from the caller to the worker. Replies route back to the caller as for any message. The daemon adds a fixed instruction before the task: the worker was started through agent-net, nobody watches it, and it must send its result, or say that it is blocked, with `agent_reply` to that message.

## W6 - Permissions

A worker uses the project's own permission settings. It never runs with permission checks turned off. When a step needs approval, the worker stops and reports to its caller. The caller decides with its user.

## W7 - Ending workers

The daemon that started a worker owns its process and kills the process group when one of these happens:

- no message to or from the worker for 60 minutes;
- 4 hours since it started;
- its owner session ended or stayed offline for 10 minutes;
- `agent_stop` from its owner, or `agent-net stop <session>`.

The daemon stores each worker's process ID and start time, so after a daemon restart it takes its workers back or ends them. `agent-net workers` lists them.

## W8 - Limits

At most 4 workers per machine at a time. The timeouts and the limit are settings, and these values are the start.
