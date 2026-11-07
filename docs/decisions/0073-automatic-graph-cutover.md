# 0073. Run the typed Graph cutover automatically

Date: 2026-11-07
Status: Built.

The typed Repo/Agent Graph cutover was a Settings action with a confirmation
and a backup path. It was never a real choice. The only blockers are machines
that have not completed a typed Graph hello or that run another version, and
the owner can only upgrade or remove those machines. The owner called it an
application upgrade, not a setting.

The gateway now runs the cutover itself. It checks at start and after a
machine completes a hello or reports a version. When the gate is off and no
machine blocks, it takes the same database and skills backup as the old action
and runs the same blocker checks and transaction recheck. There is no owner
call and no Settings entry.

A successful run logs one line with the backup path. A failed run keeps the
backup, logs the error and raises one Inbox item, then retries at the next
machine hello or gateway start. It never retries on the minute timer, so a
persistent failure cannot become a tight loop.

The gateway records the first start of each version in `graph_cutover_state`.
If blockers remain more than 24 hours after that start, the owner gets one
Inbox item naming the machines that must upgrade. The item is closed when the
blockers resolve. Teams and the Graph Team filter keep waiting for the
cutover, but their refusal now says the upgrade is waiting for all machines
instead of pointing at Settings.

The database backup is a new directory beside the gateway data directory,
named `gateway-before-typed-graph-<date>-<time>`. A later retry uses a new
directory when the earlier backup is still there. Turning the cutover off
still means restoring the backup taken before it.
