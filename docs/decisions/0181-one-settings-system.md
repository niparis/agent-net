# 0181 - One settings system

Date: 2026-11-21

Status: Approved by the owner on 2026-11-21 ("execute on this plan").

## Context

The gateway had two settings systems. `setting_values` and `setting_history` have levels, scopes, history and undo. `owner_settings` was an older store of raw JSON keys with no history and no undo. The owner wants one application data model behind the MCP tools, so one settings system.

## Decision

- The owner's real settings are network settings in `setting_values`, under the key prefix `owner.`: integrations, CTO settings and title, protected providers, worker floors, model scores, the public listener, worker profiles, worker providers, difficulty minimums and the time zone. They are declared in `standard.yaml` with the new type `json` and the visibility `internal`. An internal setting is owner-only, is not listed on a settings screen, and is not sent to the machines. Each change goes through the settings write path, so it has a revision, a history row and undo.
- Runtime state is not a setting. The CTO and chief of staff session ids, the chief's clear and silence marks, the worker rotation, onboarding progress and hidden items move to `gateway_state`. No place in the org graph fits the role ids: `sessions` has no role column, `agents.role` names the role of an agent and not the session that holds it, and the graph edges relate nodes.
- `setting_revision` is gone. The settings revision is the newest revision in `setting_history`. A change that is not a setting (a delivery owner, a maturity) adds a marker row at seq 0, which no setting row uses.
- `settings_target_identity` is gone. The token sits in the column `settings_token` of `repos`, `agents` and `teams`, and the network token is the `gateway_state` key `settings_network_token`. A deleted repo takes its token with it, and a tombstoned agent loses it, as before.
- The one-time marks of `gateway_state` were checked one by one. `graph_ref_backup` is written and never read, so the migration deletes it. Every other mark has a reader. A running gateway reads `cto_legacy_route_start` (message routing), `rework_backfill_done`, `task_rework_backfill_done`, `asks_moved_on_cleanup_v1`, `owed_replies_live_since` and the graph cutover keys. The seeds `org_tree_built`, `node_scopes_seeded`, `front_door_flush_v1` and `cto_default_charter_v1` are read only when a migration runs again, after a rewind, and they keep that replay from doing its work twice (a replay without `front_door_flush_v1` closes the Desk items that wait on the owner again). They stay.

One migration copies the rows, then drops the three tables. The moved settings get one history revision with the actor `agent-net cutover`.

## Consequences

- No setting changes meaning or default. No new limit or approval gate.
- A read of an owner value is one point lookup, as before. A write is one settings revision, which pushes the snapshot to the machines, so rewriting a value is rarer than the old direct upsert. The worker rotation, which changes on every start, is state and stays a direct write.
- The Inbox screen no longer refreshes on a chief session change alone. Marker writes still refresh no screen.
