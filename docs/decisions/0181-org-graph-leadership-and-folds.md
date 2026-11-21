# 0181 - Org graph leadership and table folds

Date: 2026-11-21
Status: Approved by the owner on 2026-11-21.

## Context

Leadership was stored in five places: `repos.owner_agent_id`, `repos.lead_agent_id`, `agents.lead_session` and `agents.lead_level`, `teams.owner_agent_id`, and stored `reports_to` edges in `graph_edges`. Stored `reports_to` edges went stale when a repo or team lead changed. Four small tables (`team_members`, `outcome_overrules`, `charter_draft_asks`, `project_display_names`) also repeated keys of existing tables.

## Decision

1. **One source per leadership fact:**
   - **Repo lead:** `repos.lead_agent_id` is the single authoritative column (`NULL` means no lead). `repos.owner_agent_id` is dropped after migrating unconfigured repos (`lead_agent_id IS NULL`) to their `owner_agent_id` and cleared repos (`lead_agent_id=''`) to `NULL`.
   - **Agent lead session and level:** `agents.lead_session` and `agents.lead_level` are the single authoritative columns for an agent's pinned lead session and explicit lead-level grant.
   - **Team lead:** `teams.owner_agent_id` is the single authoritative column for a team's lead.
2. **Computed `reports_to`:** `reports_to` edges are computed on read (`agentParentsQ`) from `repos.lead_agent_id`, `teams.owner_agent_id`, `agents.team_id`, `repos.team_id`, `sessions`, `project_mergers`, `node_scopes`, and `owner_settings`, never stored in `graph_edges`. Stored `reports_to` rows are deleted.
3. **Delivery step-owner edges stay:** The `/delivery/*` step-owner edges (`accepts`, `tests`, `merges`, `releases`) remain in `graph_edges` because `merger.go` and `delivery_pipeline_settings.go` read and write them there.
4. **Four table folds:**
   - `team_members` folds into `agents.team_id` and `repos.team_id` (each member belongs to at most one team).
   - `outcome_overrules` folds into `graph_outcomes.overruled` and `graph_outcomes.overrule_message_id`.
   - `charter_draft_asks` folds into `charter_drafts` with `status='asked'`, `source_hash`, and `asked_at`.
   - `project_display_names` is dropped; `repos.display_name` holds the project display name.
