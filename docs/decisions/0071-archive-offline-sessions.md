# 0071. Archive offline sessions

The owner can archive offline session records from Sessions. The gateway
stores archived_at on each record. Discovery reads and implicit project and
Graph reads exclude archived records. Historical joins keep using the stored
rows and profiles, so names in messages, worker runs and delivery history
survive. Session retention and worker cleanup skip archived rows and profiles.

Selection combines IDs, name and folder globs, project, harness, creation
cutoff and ended workers. No-project selection follows the explicit flag.
An ended worker has a completed run and worker_run_active is clear.

The owner reviews a dry run and confirms exact IDs. The gateway checks the
expected set and offline state in the write transaction, refusing a stale
batch as a whole. Restore uses the same path and confirmation. A new live
registration clears archived_at, so a resumed agent cannot remain invisible.

The CTO's sessions_archive proposal includes the selection and exact dry-run
names and IDs. Its existing owner approval runner applies only those IDs.
Restore stays owner-only. No proposal approval changes a pattern's membership.
