# 0018 - Admit workers by machine load, with a queue

**Date:** 2026-10-30
**Status:** Built on a worktree branch. The owner approved the design; proposed until the owner has read this file.
**Context:** A machine accepted at most 4 workers, whatever it was doing. Four idle workers use little; four heavy ones can stall the machine. A refused start also made the caller retry by hand. The owner wants a start to depend on how loaded the machine is, and to wait instead of failing.

Base: [0002](0002-workers.md) (workers and their limits), [0007](0007-usage-and-session-text.md) (the CPU and memory sampler), [0008](0008-operator-interface-and-worker-build.md) (notices).

## Decision

1. **Averages.** The daemon already samples the machine every 5 seconds (decision 0007). It now also gives the average over the last 1 minute and the last 5 minutes of:
   - whole-machine CPU busy percent (from `/proc/stat` on Linux; on macOS from the sum of process CPU times). This is not the load average;
   - available memory: total RAM minus memory in use. On Linux "in use" is `MemTotal - MemAvailable`, so available is `MemAvailable`. On macOS the sampler counts app + wired + compressed memory (like Activity Monitor), so the file cache is counted as available. The macOS memory pressure level is not read.
2. **The cost of a worker is learned.** When a worker ends, the daemon stores the average and peak CPU and memory (RSS) of its process tree, per harness and model, in `daemon.db`. It keeps the last 20 per harness and model. The expected cost of a new worker is: memory = 90th percentile of the peak RSS; CPU = 90th percentile of the average CPU. With no history it is 600 MB and one core. A run shorter than 10 seconds, or one the sampler did not measure, is not stored.
3. **Admission.** A worker starts only when all of these hold. The first rule that fails gives the reason.
   - **Cap:** fewer than `max` workers run (default 12). This is a safety net, not the main rule. The old default was 4; `max` is still configurable.
   - **Memory:** 5-minute available memory, minus the expected RAM of the worker, minus the reserve, is above zero. The reserve is the larger of 1.5 GB and 10 percent of RAM (`mem_reserve` overrides it).
   - **CPU:** the 5-minute CPU average is below `cpu_target` (default 75) and the 1-minute average is below 95.
   - **Gap:** at least 60 seconds since the last worker was let through on this machine. The averages lag, so a new worker must show in them before the next one starts.
   - Running workers are never stopped because of load.
   - Without a sampler (an unsupported system) only the cap applies. With a sampler, a start waits ("still being measured") until it has two CPU samples.
4. **A queue, not a refusal.** When there is no room, `agent_start` answers `status: "queued"` with a `queue_id` (`q_...`), the position and the reason, for example `queued #2 on research: CPU 5-min average 82% (target 75%)`. No session exists yet. The queue is per machine, first come first served: while the queue is not empty, a new start joins it even if there is room, so nobody jumps ahead.
   - The daemon looks at the queue every 15 seconds and starts the head when admission says yes. One start per look, and the gap keeps them apart.
   - **Who sends the task.** Only the machine of the owner may send a message as the owner. So when a queued start becomes a worker, the machine that runs it tells the gateway (`worker.queued_start`); the gateway checks that the worker belongs to the sending machine and names that owner, and passes it to the owner's machine (`worker.task`), which sends the task as the owner would have.
   - **Notices.** The caller gets a "worker started" notice (`status: started`, the session ID, the queue wait) from the new worker, like other worker notices. A start still queued after 2 hours (`queue_timeout`), or one that fails when it gets its turn, ends with a `status: not started` notice. That notice comes from the machine's owner session. When the person at that machine's own command line is the owner, there is nobody to send it to, so only the daemon log has it.
   - **Cancel.** `agent_stop` and `agent-net stop` take the `queue_id`. Only the owner of the start, or the person at a command line, may cancel. The gateway does not know which machine holds a queue, so it asks the machines that accept workers; the one that has it answers.
   - **Owner gone.** The rules of running workers apply: if the owner session is offline for `owner_grace` (10 minutes), the queued start is dropped without a notice.
   - **Restart.** The queue is in `daemon.db`, so it survives a daemon restart.
5. **Machine choice.** When a project is on more than one machine, the gateway picks the one with the most room, if at least two can run the harness and every one of them reported its room in the last 45 seconds. A machine that has room beats one that has none; then the larger headroom wins. Headroom is the smaller of the free memory share (after the expected worker and the reserve) and the free CPU share (target minus the 5-minute average). The start result says why (`machine_note`). If a machine cannot say (an older agent-net), the old rule stays: name the machine. A project start from an agent keeps its old rule first (the caller's own machine wins).
6. **Owner view.** Each machine reports `workers N running, M queued - room: ok|cpu|mem|cap|gap|measuring` with its usage report (every 10 seconds). `agent-net workers` prints one such line per machine above the table. The TUI shows it on the machine line when the machine has workers, a queue or no room.
7. **Settings** in the `workers` part of `node.json`: `max` (12), `cpu_target` (75), `mem_reserve` (for example `"2GB"`; default the larger of 1.5 GB and 10 percent of RAM), `queue_timeout` (`"2h"`), and the old `idle`, `max_age`, `owner_grace`.

## What did not change, and what was left out

- **Resuming a worker** (the owner's message to a worker that handed back) still checks only the cap. It starts the same harness session again, which the owner already paid for in the queue.
- **The expected CPU cost** is stored and used to learn, but the CPU rule is the one the owner gave: the 5-minute and 1-minute machine averages. Expected CPU is not added to them.
- **The gap** is counted from when a start was let through, not from when the worker became a session.

## Also fixed in this change

At an upgrade, the gateway's `worker.cleanup` deleted the record of workers that were still running. See [the reference](../reference.md#workers) and `Store.CleanWorkers`: the daemon now sends the session IDs of the workers it runs, the gateway never deletes those, and an adopted worker that lost its record registers again under the same session ID.
