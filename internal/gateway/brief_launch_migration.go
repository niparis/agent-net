package gateway

// workerBriefLaunchMigration adds two columns to worker_starts. The column
// check in migrate() adds them to stores that already hold the table. The
// columns stay because a shipped migration is append-only; no code reads or
// writes them since the brief launch exclusion was removed.
const workerBriefLaunchMigration = `
-- Worker brief launch columns: a hash of the brief, and when a run of the start
-- ended without attempting the task, so a retry of the brief leaves the harness out.
SELECT 1 /* worker brief launch columns */;
`
