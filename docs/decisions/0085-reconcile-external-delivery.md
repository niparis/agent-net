# Reconcile delivery with external merges

An owner's GitHub merge does not report agent-net steps. Worker changes then
stay open indefinitely, waiting for accept even though the work has shipped.

The change's daemon observes its local repos at most once every ten minutes.
It fetches only origin's default branch, resolved by repoinfo, and checks head
ancestry against that remote tip. If the clone has no origin/HEAD, it asks
origin for HEAD without setting a local symbolic ref. A positive observation records merge evidence
with `merge_kind: external` and that tip as `merged_commit`, then marks the
change done. It does not run or attest to tests, release, deploy or verify.

An unreported change older than seven days whose branch is absent locally and
on origin instead receives close evidence and becomes stopped. Fetch or remote
listing errors do not prove absence. External merge detection takes precedence.

A daemon-only gateway frame binds observations to the authenticated machine,
head and revision. The gateway checks abandonment age and the entire evidence
history in the same transaction that closes tasks and dismisses approvals.
Evidence uses the existing append-only table, so there is no schema migration.
