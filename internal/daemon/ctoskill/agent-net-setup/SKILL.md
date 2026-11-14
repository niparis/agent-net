---
name: agent-net-setup
description: Advise the owner precisely on setting up a new or existing repo, project, session or team in agent-net - leads, workers vs sessions vs subagents, merge/review/CI options, who releases and deploys, teams, edges, scopes, charters, permissions. Use when the owner asks how to set up, configure or restructure a repo, session, team or delivery flow, or asks "what are my options".
---

# agent-net setup advisor

Goal: give the owner a short, complete set of decisions for one repo/session/team, each with a recommended value and the exact call that sets it. Values and defaults are in reference.md (generated from the code of this agent-net version).

## How to advise

1. Ask only what you cannot read: purpose of the repo, how risky it is (maturity), who consumes it. Read the rest yourself: agent_projects_find, agent_sessions_find, agent_graph_read (typed), agent_owner_judgments_find, cto_standing_orders list.
2. Walk the decisions below in order. For each: recommended value, one-line why, the call. Skip decisions whose default fits.
3. Present as a short table (decision | recommend | why). Offer to apply; apply on his yes. Show graph changes as a diff with undo.
4. Owner standing orders override defaults (workers_code everywhere, lead naming "<repo> - lead", no special-case sessions, no added approval gates he did not ask for).

## What matters: guide on tier 1, default the rest

Never walk the owner through every option. Sort them:

- **Tier 1 - always decide with the owner** (these shape who does what; a wrong value causes real trouble): maturity (2), lead and its role (3), who writes code (4), merge path (6), who releases and deploys (8, 9), team placement (10). Ask in one table, with a recommendation each.
- **Tier 2 - raise only when it applies** (one line, recommended value): number of sessions (5), review independence for risky repos (7), edges to repos it calls or serves (11), scopes when a session must act without prompts (12), charter (13, ask the project agent to draft), permissions/trust when a start fails or he says so (14).
- **Tier 3 - set the default silently, mention only if asked**: autonomy and loops (15), compaction (16), worker limits, picker tuning, timers, step contracts, guidance texts, branch names, trailers, cache and inbox sizes. Apply defaults (maturity defaults, network defaults, his standing orders) and list them as "left at default" in one line.

Order of the answer: tier 1 table, then tier 2 lines that apply, then one line "everything else at defaults". Promote a tier 3 item to tier 2 only when the repo's facts make it matter (for example a long-running lead: compaction; a public repo: ci_mode train; an expensive model: picker).

## The decisions, in order

1. **Register / place** - project_change add (existing path) or create (new: name, path, node_id, harness, model, remote). Placement: projects_layout + cto_placement_memory. Default harness claude, model claude-opus-5-5 unless a learned default.
2. **Maturity** - charter_set level repo attr maturity: experimental | early | growing | mature. Maturity sets the defaults for 6 node settings; pick it first, then override only what differs.
3. **Lead** - who is first contact and decides. Options: the repo's own agent (default), a separate lead agent (agent_create + session_agent_set + lead_set ref repo and team), or a team lead above several repos. Name the lead session "<repo> - lead". A supervisory lead needs its own agent node with a rule "coordinate only"; deploy/merge scopes stay on the doing agent. Known trap: routing picks any live session of the lead AGENT; a lead session moved to another agent must also be set as repo AND team lead.
4. **Who writes code** - work_mode: workers_code (owner default; sessions design/review, workers implement) vs on_request. Per-session delegate policy: off | native (harness subagent) | workers (+ coder_model). Workers: picker chooses model unless owner names one (owner_words). Sandbox: sandboxed | no-network | read-only. Worker limits: workers.max_per_machine (12), workers.start_gap (6s between starts), workers.caller_gone_grace (10m before a worker whose caller left is stopped), workers.brief_approval (empty: no separate approval of the brief). Picker tuning: picker.* (weaker_bias, explore_share, bucket_width, k, allowed_fraction, uncertain_boost, uncertain_n).
5. **How many sessions** - max_agents (0 = no limit). Lead + N members. Members share the agent's charter; give a session its own agent node only when its rules or authority must differ. messages.inbox_limit (5) caps queued messages per agent; 0 means no limit.
6. **Merge path** - pick one:
   - caller merges (merge_authority full, default growing)
   - merge queue: merger_start (Sonnet is enough; it runs test + merge + release-after-merge) - best when several workers land in parallel
   - lead merges members' work: team lead_merges on
   - approval gate: merge_authority delegated (someone above holds merge) or ask (owner per merge)
   - standing exception: agent_owner_judgment_record area repo:X action merge (owner's exact conditions)
   Tests: tests_before_merge optional | suite | suite-new-tests | suite-new-tests-review. CI: delivery.ci_mode per_change | train. Evidence: delivery.pipeline.merge.requires (accept, review, test:suite by default). Approvers: delivery.pipeline.merge.approver and delivery.pipeline.deploy.approver (none, delegate or owner; maturity raises them). Before handback: delivery.caller_review_and_merge (the caller's instruction), delivery.handback_check (empty: no extra check), delivery.save_unfinished (true: keep an unfinished worker's changes). Worker branches: delivery.worker_branch_prefix (agent-net/). Step contracts: delivery.contract.<step> (accept, review, test, test_train, approve, merge, release, deploy, verify).
7. **Review** - required from difficulty hard by default. Independence: delivery.pipeline.review.independent none | session | family. Deep review = family (another model family). The caller can name a reviewer session.
8. **Release** - with a merger: merger releases after merge (delivery_owner_set step release, after_merge true, node @merger). Without: caller. Test-only trains do not release.
9. **Deploy** - deploys edge from deployer (e.g. homelab-agent) to app; deploy_rule brief (deploy as briefed) | ask | owner. Deploy scope follows the edge.
10. **Team** - team_create / team_member_add when repos share a lead, purpose or rules. Team settings: work_mode, asks_to owner|lead, lead_merges, mode absolute|charter_first|negotiate, held_back. Repo purpose overrides team purpose: a team-wide rule must go in rules, not purpose.
11. **Edges to other repos** - depends (consumer -> provider) with rule normal | auto | ask | never. auto = the provider acts on the consumer's requests without the owner.
12. **Scopes (delegated authority)** - scope_set on an agent: asks, admin, merge, release, deploy, review, permissions over all | team:X | repo:X | agent:X. Grants harness allows (the Claude hook, Codex command approvals and OpenCode permission prompts answer through tool_decide; Pi has no permission prompt and only reads the Delegated authority line). Never covers delete data, money, secrets.
13. **Charter** - purpose (<=160 chars), accepts/refuses (most important first, 160 visible to neighbours), escalate, rules, feature_requests. Whole charter ~1200 characters. Ask the project agent to draft; show as diff.
14. **Permissions / trust / tools** - folder trust and MCP approval: session_change trust / mcp_servers. Permission rules: cto_op permission_apply with owner_words when he said it in chat. Shared MCP servers: mcp_change. Project bar (diff rules): bar_edit. Guidance texts: guidance.* (the text written to agent instruction files); leave at defaults.
15. **Autonomy** - autonomy off | wake; wake_on_message on|off; loop_every/loop_prompt/loop_mode/loop_plan for a recurring task. chief.brief_time (09:00) sets the chief's daily brief.
16. **Auto compaction** - when a session's context is compacted. sessions.compaction_threshold_percent (default 80), set per network, team, repo or agent (settings_change values_write; most specific wins). Lower for long-running leads and mergers that must stay sharp; higher for short focused sessions. Network-wide also: settings_change area cto, compaction {threshold_percent, cache_expiry_enabled (default true: compact when the provider's prompt cache has expired), cache_window_seconds per provider}. Idle compaction: sessions.idle_compaction (true) and sessions.idle_compaction_min_tokens (200000). Cache windows: sessions.cache_window_seconds (anthropic 3600, default 300). Stall timeout: sessions.stall_timeout (20m).

## Common recipes

- **Solo app repo, growing**: own agent as lead, workers_code, caller merges with suite, review from hard, deploy via homelab deploys edge with brief.
- **Busy repo, many workers**: add merger (Sonnet), tests suite-new-tests, release after merge by merger.
- **Supervisory lead + 2 doers**: lead agent (rule coordinate only, repo+team lead), doers on the repo agent holding deploy/merge scopes.
- **Shared service used by several teams**: own repo charter (purpose, consumer-review rule), outside any single product team, depends edges from each consumer.