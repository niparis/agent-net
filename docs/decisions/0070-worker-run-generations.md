# 0067 - Fence worker end reports by run generation

Date: 2026-11-06

A worker keeps its session ID across resumes. Its original start time also
stays fixed for the maximum age limit. Neither identifies the current run.
The worker_runs table records completed runs, and worker_starts records start
requests. Neither covers both gateway resumes and daemon delivery resumes.

The daemon now persists a generation per worker. It starts at 1 and increases
on every resume attempt. Adoption keeps the generation. Before registering a
resumed harness, either resume path sends worker.run_state to the gateway.
Registration and worker.stopped carry the same generation. The gateway stores
it separately from session metadata and fences end writes with it. A failed
resume rolls back only its own generation. Stale reports are logged.

Generation 0 means the sender predates this protocol. Those reports retain the
previous behavior. The gateway still activates a worker before asking an old
daemon to resume it. Finished workers stay ended after late session syncs.
