# 0184 - Org graph leadership and table folds

Date: 2026-11-21
Status: Approved by the owner on 2026-11-21.

## Context

Leadership was stored in five places: `repos.owner_agent_id`, `repos.lead_agent_id`, `agents.lead_session` and `agents.lead_level`, `teams.owner_agent_id`, and stored `reports_to` edges in `graph_edges`. Stored `reports_to` edges went stale when a repo or team lead changed. Four small tables (`team_members`, `outcome_overrules`, `charter_draft_asks`, `project_display_names`) also repeated keys of existing tables.

## Decision

1. **One source per leadership fact:**
   - **Repo lead:** `repos.lead_agent_id` is the single authoritative column (`NULL` means no lead). `repos.owner_agent_id` is dropped after migrating unconfigured repos (`lead_agent_id IS NULL`) to their `owner_agent_id` and cleared repos (`lead_agent_id=''`) to `NULL`.
   - **Agent lead session and level:** `agents.lead_session` and `agents.lead_level` are the single authoritative columns for an agent's pinned lead session and explicit lead-level grant.
   - **Team lead:** `teams.owner_agent_id` is the single authoritative column for a team's lead.
2. **Computed `reports_to`:** `reports_to` is computed on read (`agentParentsQ`) from `repos.lead_agent_id`, `teams.owner_agent_id`, `agents.team_id`, `repos.team_id`, `sessions`, `project_mergers`, `node_scopes`, and `owner_settings`. The stored rows that the old placement code wrote are deleted by the migration, so a lead change moves the tree at once. An explicit `edge_set` with kind `reports_to` (the CTO moves one Agent under another) is still allowed. It is stored as one row, replaces that Agent's computed parent, and can be undone. Explicit moves are applied first, so a computed parent that would close a cycle with one of them gives way. Only a cycle between explicit moves is refused.

   A Repo lead whose Repo is in no Team reports to `cto`, so a solo Repo lead sits under the CTO in the tree. Leading a Repo outranks working in another Repo: an Agent that leads a solo Repo and also has a session in another Repo reports to the CTO, not to that other Repo's lead. This is the display and reporting line only. Order rights and message routing still read the direct Team and Repo lead, so the CTO gains no order right from this line, and a request from another session of the CTO's Agent to a member of the solo Repo still goes to that Repo's lead. (Added 2026-11-25 on the owner's instruction, relayed by the CTO.)
3. **Delivery step-owner edges stay:** The `/delivery/*` step-owner edges (`accepts`, `tests`, `merges`, `releases`) remain in `graph_edges` because `merger.go` and `delivery_pipeline_settings.go` read and write them there.
4. **Four table folds:**
   - `team_members` folds into `agents.team_id` and `repos.team_id` (each member belongs to at most one team).
   - `outcome_overrules` folds into `graph_outcomes.overruled` and `graph_outcomes.overrule_message_id`.
   - `charter_draft_asks` folds into `charter_drafts` with `status='asked'`, `source_hash`, and `asked_at`.
   - `project_display_names` is dropped; `repos.display_name` holds the project display name.
