# agent-net options reference (generated from the code of this agent-net version)

This file is generated from the code lists. Do not edit it by hand. Run `go generate ./internal/daemon/ctoskill`, or `go test ./internal/daemon/ctoskill -run TestReferenceMatchesCode -update`.

## Node settings

Set with `graph_change` action `settings_set`, or `settings_change` area `values_write`, on a node.

| setting | values | default (growing) | differs by maturity |
|---|---|---|---|
| tests_before_merge | optional, suite, suite-new-tests, suite-new-tests-review | suite | experimental optional; early optional; mature suite-new-tests; critical suite-new-tests-review |
| merge_authority | full, delegated, ask | full | mature delegated; critical ask |
| worker_sandbox | sandboxed, no-network, read-only | sandboxed | critical no-network |
| judge_consumers | free, charter, strict, escalate | charter | experimental free; early free; mature strict; critical escalate |
| deploy_rule | brief, ask, owner | brief | mature ask; critical owner |
| autonomy | off, wake | off | same at every maturity |
| wake_on_message | on, off | on | same at every maturity |
| work_mode | on_request, workers_code | on_request | same at every maturity |
| loop_every | a duration above zero, such as 4h | empty (no loop) | same at every maturity |
| loop_prompt | text sent on each tick | empty | same at every maturity |
| loop_mode | always, on_change | empty (always) | same at every maturity |
| loop_plan | a path inside the session folder, such as PLAN.md | empty | same at every maturity |

Maturity levels: experimental, early, growing, mature, critical.

A stored value may carry an explanation after ` - ` when it is read. `charter_set` takes the bare level and stores the explanation in `maturity_note`. The maturity defaults above come from the settings recipe, so they change with it.

## Other repo settings

| setting | default | meaning |
|---|---|---|
| max_agents | 0 | Maximum live agent sessions in a repo; zero means no limit. |
| workers.isolation_minimum | git-default | Worker filesystem isolation policy. Values: git-default, worktree-required. |
| delivery.ci_mode | per_change | Whether the merger runs CI once per merge train or once per change. Repo visibility is not stored, so a public repo opts in with the repo setting train; the default stays per_change otherwise. Values: per_change, train. |
| merger.test_timeout | 90m | Wall-clock limit for a local test suite run through test-run. |
| workers.max_age | 4h | Maximum worker run age. |
| workers.idle_timeout | 60m | Idle time before a worker is stopped. |
| workers.queue_timeout | 2h | Maximum worker admission queue wait. |
| workers.auto_continue | true | Whether an early worker stop is continued automatically. |
| workers.auto_continue_max | 2 | Maximum automatic continues after early worker stops. |
| sessions.compaction_threshold_percent | 80 | Compact a persistent non-worker session when its context fill reaches this percent. Claude, OpenCode (including the CTO), Codex and pi support compaction. A harness with no compaction command is reported on its session row. |

## Team

A team groups repos and agents that share a lead, purpose or rules. Create it with `team_create`, add members with `team_member_add`, and set its lead with `lead_set` ref team. `team_charter` sets purpose, refuses, escalate, rules, asks_to, work_mode, lead_merges, mode and held_back; `team_settings_set` sets asks_to, lead_merges and the team-level node settings.

| setting | values | default | meaning |
|---|---|---|---|
| work_mode | on_request, workers_code | on_request | Who writes code. |
| asks_to | owner, lead | owner | Who answers a member's ask to the owner. |
| lead_merges | on, off | off | The owner's grant that the team lead merges the team's work. |
| mode | absolute, charter_first, negotiate | charter_first | How an order from the lead binds a member. `absolute`: an order is carried out like the owner's instruction; the member's charter does not override it; `charter_first`: an order is carried out unless the member's charter refuses it; then it goes up one level; `negotiate`: the member may push back with a reason and a counter-proposal; the lead accepts, changes or overrules. |
| held_back | free text | empty | What this team may not do. |

`held_back` narrows authority; rules text never grants it. These never delegate: deleting data, spending money, and using or sharing secrets that can spend money.

A team's charter fields are inherited by its members: a field of the same name on a repo or agent replaces the team's. Put a team-wide rule in `rules`, not in `purpose`, because a repo purpose replaces the team purpose.

## Leads and the tree

The repo lead is the repo's owner agent unless `lead_set` names another. The team lead is the team's owner agent. `lead_session` pins one session. A message from outside to a non-lead session is redirected to a live session of the lead agent; `repo:X` and `team:X` address the lead.

`reports_to` is the org tree: each Agent has at most one parent, the tree has no cycle, and the top is `owner`. Mergers are agents named `<project>-merger` under the repo's top agent. Lead level comes from a `lead_set` grant, from being a team lead, or from being chief of staff (`grant`, `team lead`, `chief of staff`). `started_by` says `agent-net` or `owner`.

## Delegation and workers

`session_change` action `policy`, with `scope` `session` or `project`:

- `delegate`: off, native or workers (empty means no mode)
- `coder_model` and `main_model`: loose model names, resolved on the machine of the session
- `workers`: sandboxed, no-network, read-only

A worker start leaves harness, model, provider, effort and machine empty to let the picker choose. Naming one needs the owner's words in `owner_words`, or the start waits for the owner. The difficulty is judged automatically. `isolation` is `worktree` or `none`; `none` is for read-only or non-code work. `fixes` names a shipped change this work fixes with `fixes_severity` from 1 to 4, `continue` names an ended worker to continue, and `base` names the base branch. Worker harnesses: opencode, claude, pi, codex, agy.

## Delivery

Steps: accept, review, test, approve, merge, release, deploy, verify.

Results: pass, fail, needs_owner, close. `waiting` is a question's reply, not a step result.

Step owners (`delivery_owner_set`):

| step | owners |
|---|---|
| accept | @caller, node <name>, @owner |
| review | @caller, node <name>, @owner |
| test | @merger, @caller, node <name>, @github, @owner |
| approve | @caller, node <name>, @owner, @delegate, @none |
| merge | @merger, @caller, node <name>, @github, @owner |
| release | @merger, @caller, node <name>, @owner |
| deploy | @caller, node <name>, @owner |
| verify | @caller, node <name>, @owner |

Review is required from difficulty `hard` or risk `4`; evidence independence is none, session, family.

`session_change` action `merger_start` starts the project's merger and assigns it the test and merge steps; `merger_stop` returns them to the caller. A release after merge can also be assigned to `@merger` with `delivery_owner_set` step `release`, `after_merge` true.

A deploy step runs only when the deploy rule is not `brief`; with `brief` the caller deploys as briefed. Step reminders wait `2h` and a stuck step is reported after `12h`.

## Edges

Stored kinds for `edge_set`: depends, deploys, accepts, reviews, tests, tests_after_merge, approves, approves_deploy, merges, releases, delivery_deploys, verifies. A `depends` edge goes from consumer to provider, and `*` is allowed as the consumer. A `deploys` edge goes from deployer to app. The delivery kinds go from the step owner node to the repo. `reports_to` is the org tree edge and `member_of` is team membership; neither is set with `edge_set`.

Rules: normal delivers with ordinary peer guidance; auto delivers with peer trust; ask holds for the owner; never refuses.

## Scopes

A scope is authority the owner delegates to a role node. `scope_set` takes an agent in `node`, a kind, an over target and an optional note (`value` through `graph_change`). Since decision 0166 a session whose role holds the scope may do that action without asking the owner. A scope never covers deleting data, spending money or using secrets.

Kinds: asks, admin, merge, release, deploy, review, permissions. Over: `all`, `team:X`, `repo:X`, `agent:X`.

### review - review and accept changes

- Shell: `git (status|log|diff|show|blame|ls-files|ls-tree|rev-parse|rev-list|describe|shortlog|grep|show-ref|merge-base|cat-file)( .*)?`, `git branch( (--list|--show-current|-a|-r|-v|-vv))*`, `(ls|cat|head|tail|wc|grep|rg|pwd|stat|file)( .*)?`, `gh pr (view|diff|checks|list|status)( .*)?`
- Tools: `agent_step_report`

### merge - merge

- Shell: `git (merge|rebase|commit)( .*)?`, `git push( (-u|--set-upstream))? [A-Za-z0-9._-]+ (main|master|HEAD:main|HEAD:master)`, `gh pr merge( .*)?`

### release - release

- Shell: `git tag( .*)?`, `git push( [A-Za-z0-9._-]+)? (--tags|refs/tags/\S+|v[0-9]+(\.[0-9]+)+\S*)`, `gh release (create|upload|view|list)( .*)?`, `(\./)?scripts/(release|dist)[A-Za-z0-9._-]*\.sh( .*)?`

### deploy - deploy

- Shell: `(\./)?scripts/deploy[A-Za-z0-9._-]*\.sh( .*)?`

### asks - sort and answer asks

- Tools: `agent_ask_sort`, `agent_ask_intake`

### permissions - apply approved permissions

- cto_op: `permission_apply`, `permission_undo`

### admin - run admin tools

- Tools: `agent_bar_edit`, `agent_charter_draft`, `agent_project_add`, `agent_worker_profile_add`, `agent_worker_profile_remove`
- cto_op: any method except the owner-only actions

The answer from the scope check is allow or no decision, never block: a call outside every scope falls back to the harness's own permission prompt. The Claude hook, Codex command approvals and OpenCode permission prompts answer through `tool_decide`. Pi has no permission prompt; it only reads the Delegated authority line.

## Charter

| field | levels | size and meaning |
|---|---|---|
| purpose | repo, team | one sentence, up to 160 characters. What the project is. |
| maturity | repo | one line. Repo enum: experimental, early, growing, mature or critical. |
| accepts | agent | at most 6 items, short phrases separated by semicolons or newlines. What work this project's agent takes on when other agents ask. |
| refuses | global, repo, team, agent | at most 6 items, short phrases separated by semicolons or newlines. What work the agent declines and where to redirect it. |
| feature_requests | agent | names and pointers. How an agent should raise an idea for new work. |
| escalate | team, agent | at most 6 items, short phrases separated by semicolons or newlines. What the agent must bring to the owner or its architect. |
| maturity_note | repo | one line. Optional one-line explanation of repo maturity. |
| rules | global, repo, team, agent | short free text. Taste and house rules, at any level. The code never interprets them. |
| work_mode | global, repo, team, agent | one value. Who writes code: workers_code (the session designs and reviews, agent-net workers write all code, no owner ask per task) or on_request (the default: hand a task to a worker only when the owner asks). |

A value holds at most 2048 bytes. Budget about 1200 characters for the whole charter because the project's agent reads it every turn. Neighbours see only the first 40 characters of `accepts` and `refuses`, so put the main item first. `purpose` is one sentence up to 160 characters. `accepts`, `refuses` and `escalate` hold at most 6 items. A value that looks like an IP address, a token or an absolute path gets a warning; never put secrets in a charter. Put detail in the project's AGENTS.md.

## Permissions, tools and the rest

- `cto_op` method `permission_apply` with the owner's words in `owner_words`, or `agent_permission_propose` for the Desk.
- `session_change` action `trust` for a folder, and `mcp_servers` to approve or disable a folder's `.mcp.json` servers. Shared MCP servers use `mcp_change`.
- `bar_edit` adds, edits or removes a project's bar rules; one rule is one line of at most 400 characters.
- `agent_owner_judgment_record` stores a standing rule: text, area (`network`, `repo:X` or `team:X`) and action. Both must match for the rule to apply.
- Role models are settings: `cto.models`, `chief.model` and `merger.models`. Each is an ordered list of candidates, one per line as harness, provider, model and effort; the first one that can run now is used.
- Owner sessions use harness `claude` by default; `opencode`, `codex` and `pi` are also supported. An empty model leaves the choice to the harness, and agent-net sets no default owner-session model.
- Placement: `projects_layout` scans the owner's home folder for git repos and worktrees, within a bounded depth and count, and caches the result. `cto_placement_memory` saves the owner's placement answers.
