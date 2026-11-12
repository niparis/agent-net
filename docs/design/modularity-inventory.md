# Modularity inventory

Status: working inventory, not a decision. Read-only survey of the code at the repo root of this checkout.
Purpose: input for a modularity design that sorts agent-net into four layers: Foundation, Module, Playbook, Surface.

Files I read to build this: `AGENTS.md`, `README.md`, `docs/reference.md`, `docs/security.md`, `docs/decisions/` (index, 0036, 0044, 0048), and the Go sources named in each row. Line numbers are from this checkout. A "not verified" mark means I did not trace it to the end.

Two naming traps to know before reading:

- **"Playbook" already exists as a feature.** Today it means the owner's and the automatic notes about models (`protocol/track.go:202-211`, `agent-net playbook set`, `docs/reference.md:493`). The design layer called "Playbook" is wider. Pick a different name for one of them.
- **"Owner" has two meanings.** For people it is the human. For workers, `worker.owner` and `Owner` in the worker frames mean the calling session that started the worker (`daemon/workers.go:127`, `protocol/workers.go:206-208`). Section 3 is about this second meaning.

## 0. How the code is laid out (short)

- One Go binary, three roles: gateway (`internal/gateway`), daemon (`internal/daemon`), client tools (`cmd/agent-net`, `internal/mcpserver`, `internal/tui`, `internal/operator`).
- Shared types and many policy constants live in `internal/protocol` (about 658 lines in `protocol.go`, plus 70 more files). It is the biggest coupling hub (see section 5).
- Gateway state is SQLite. Owner-level settings are rows in one table, `owner_settings(key, value, updated_at)`, one key per setting (`gateway/store.go`, keys listed in section 4).
- Daemon state is SQLite too (`daemon/state.go`), with its own `owner_preferences` table (`daemon/state.go:41`).
- Per-machine static config is `node.json` (`daemon/join.go:24-66`).
- Agent-facing tools come from one catalog, `internal/toolcatalog/tools.go`, with four roles: Agent, Worker, CTO, Bot.

## 1. Feature table

Columns: feature, layer, where it lives, depends on, can it be turned off today and how.

"Layer" is my proposal. Where a feature is split across layers, I say so.

### 1.1 Foundation

| Feature | Layer | Where | Depends on | Off today? |
|---|---|---|---|---|
| Gateway (TLS, enrollment, directory, message store, install endpoints) | Foundation | `internal/gateway/server.go`, `store.go`, `oauth*.go`; `cmd/agent-net/main.go` | protocol, SQLite | No. One machine runs it; `setup --gateway`. |
| Daemon (gateway link, local socket, outbox, state DB) | Foundation | `internal/daemon/daemon.go`, `link.go`, `local.go`, `state.go` | gateway, protocol | No. `uninstall` removes it. |
| Machine identity, node tokens, enrollment | Foundation | `gateway/server.go`, `daemon/join.go`, `setup/` | gateway | No. |
| Session discovery and identity (Claude, Codex, Pi, OpenCode, Antigravity) | Foundation | `daemon/claude/`, `daemon/codex_*.go`, `daemon/agy_peer.go`, `plugins/pi`, `plugins/opencode` | harnesses, daemon | Per session: env `AGENT_NET_IGNORE=1` (`docs/reference.md:612`). Not per harness. |
| Caller checks (owner gate, process-tree check, roles) | Foundation | `daemon/sessiontext.go:109-165` (`operatorGate`, `insideSession`), `daemon/local.go`, `mcpserver/roles.go:14-20`, `toolcatalog/` | daemon | No. Fixed list `protocol.OperatorMethods` (`protocol/operator.go:136-138`). |
| Messages (send, reply, hold, expire, idempotency) | Foundation | `gateway/store.go:1940-1975`, `gateway/held.go`, `daemon/messages_write.go`, `mcpserver/messages_write.go` | gateway, graph rules (see 1.2) | No. Limits fixed in `protocol/protocol.go:44-50`. |
| Security notes on messages (peer note, no-authority note, marker neutralizing) | Foundation | `protocol/trust.go:11-54`, `daemon/claude/claude.go:278-284`, TS copies in `plugins/*/agent-net.ts` | messages | No. |
| Secret redaction | Foundation | `internal/redact` | none | No. |
| Rate and loop limits | Foundation (limits are policy, see 2.12) | `gateway/store.go:1827-1831`, `protocol/protocol.go:44-50` | gateway | No. |
| Herdr entry rules | Foundation | `daemon/enter.go`, `daemon/herdr*.go`, `tui/enter.go`; rules in `AGENTS.md` and decision 0046 | daemon, TUI | `node.json` `enter_ssh` false turns off ssh entry only (`daemon/join.go:35-38`). Herdr rules: no. |
| Harness detection, model lists, inventory | Foundation | `internal/harnesses`, `internal/inventory`, `daemon/inventory.go` | daemon | No. |
| Harness updates | Foundation (could be Module) | `daemon/harness_update.go` | harnesses, integrations answer | Yes. Network integration item `harness_updates` ("Keep harnesses updated"), stored in the integrations answer (`protocol/integrations.go:16,51,71`; read at `daemon/harness_update.go:30`, `daemon/integrations.go:64`). |
| Agent upgrade (self-update, dist serving) | Foundation | `internal/upgrade`, `internal/upgradecli`, `daemon/autoupgrade.go`, `daemon/upgrade.go` | gateway | Env `AGENT_NET_NO_AUTO_UPGRADE=1`, flag `--no-auto-upgrade` (`cmd/agent-net/main.go:360`). |
| Setup, invite, uninstall | Foundation | `internal/setup`, `internal/agentfiles` | daemon | n/a |
| Project registry and repo detection | Foundation | `internal/repoinfo`, `daemon/tools_project.go`, `gateway/store.go` (projects), `temppath` | daemon, gateway | No. `AGENT_NET_IGNORE` hides one session. |
| Agent guidance block written to agent instruction files | Foundation mechanism, Playbook content | `internal/guidance/guidance.go`, `guidance.md`; writer `agentfiles/agentfiles.go:90-93,312` | agentfiles | No switch. Always written at daemon start (`daemon.Config.RefreshAgentFiles`, `daemon/daemon.go:124-127`). Uninstall removes it. |
| Event log, gateway log | Foundation | `gateway/store.go` events; retention flags `--keep-events` etc. (`docs/reference.md:631+`) | gateway | Retention flags only. |
| Usage sampling (CPU, memory) | Foundation | `internal/usage`, `daemon/usage.go` | daemon | Nil sampler in tests only (`daemon/daemon.go:218-219`). |

### 1.2 Modules

| Feature | Layer | Where | Depends on | Off today? |
|---|---|---|---|---|
| Component graph, charters, edges, node settings, maturity | Module (CTO, graph and charters group) | `gateway/store_graph.go`, `gateway/graphread.go`, `protocol/graph*.go`, `protocol/maturity.go`, `daemon/graphread.go`, `daemon/graph_public.go`, `tui/graph_tab.go`, `tui/graphview/` | gateway, messages | No. It is also read inside message delivery (`gateway/store.go:1940-1960`, `gateway/graphread.go:105`). Empty graph means rule "normal". |
| Request rules on edges (auto, ask, never) and peer trust | Module, but wired into Foundation messages | `gateway/store.go:1944-1962`, `gateway/graphread.go:105-130`, `store.go:1385-1400` (`setPeerTrust` writes a `*` edge) | graph | Per session: peer trust off. No global switch. |
| Idle check and autonomy wake | Module | `gateway/check.go`, `gateway/remind.go` | graph node setting `autonomy`, sessions | Idle check: no switch. Wake: node setting `autonomy` `off` (default) or `wake` (`protocol/maturity.go:156-165`). |
| CTO (private OpenCode or Claude conversation, proposals, owner window) | Module | `daemon/cto*.go`, `mcpserver/cto*.go`, `daemon/proposals*.go`, `gateway/cto_*.go`, `tui/cto.go`, `tui/proposals.go` | graph, owner gate, tool catalog | No explicit switch. It starts on demand (`daemon/cto.go:134`). CTO settings pick harness and model (`gateway/cto_settings.go:14-15`). |
| Learned charter suggestions and graph learning | Module | `gateway/learned*.go`, `daemon/charter_suggestions.go`, `internal/phrasing` | graph, messages | No. |
| Inbox (held requests, approvals, questions, escalations, outcomes) | Module (Surface is tab 7) | `gateway/store_inbox.go:28`, `gateway/held.go`, `gateway/store_outcome.go`, `daemon/proposals.go`, `tui/inbox.go` | messages, graph, owner gate | No switch. The "Needs you" hooks are an integration (see 1.4). |
| Workers (start, queue, admission, worktrees, hand-back, notices, stop, resume, cleanup) | Module | `daemon/workers.go`, `daemon/worker_*.go`, `daemon/queue.go`, `daemon/admission.go`, `daemon/cleanup.go`, `gateway/workers.go`, `protocol/workers.go`, `mcpserver/workers*.go` | sessions, messages, picker, harness adapters | Per machine: `setup --no-launch`, `node.json` `no_launch` (`daemon/join.go:32-33`, refuse at `daemon/workers.go:526`). Per CTO: no. |
| Worker model picker (rotation, scores, quota fit) | Module | `protocol/chooser.go`, `gateway/workers.go:456-520,1274-1360`, `gateway/protected_chooser.go`, `protocol/scores.go`, `scores.json` | workers, quotas, profiles | Explicit model skips it ("auto" or empty means picker, `gateway/workers.go:468`). No off switch. |
| Worker profiles and providers | Module | `gateway/worker_profiles.go`, `scores.go:134-178`, `protocol/worker_profiles.go`, `worker_providers.go` | picker | Settings rows `worker_profiles`, `worker_providers`. |
| Protected providers | Module/Playbook | `protocol/protected_providers.go`, `gateway/scores.go:185-238`, `gateway/protected_chooser.go` | picker | Default is empty (nothing protected). Set with `agent_protected_providers_set` or CTO settings change. |
| Difficulty judge | Module | `daemon/worker_assessment.go`, `daemon/difficulty*.go`, `internal/difficulty`, `protocol/worker_assessment.go` | workers, harness `opencode` | No product switch. Test flag `testNoDifficulty` (`worker_assessment.go:31`). Failure falls back to "normal". Judge model/harness fields exist only in `daemon.Config` (`daemon/daemon.go:176-187`) and the `dev` command (`cmd/agent-net/dev.go:74-83`). |
| Difficulty calibration | Module | `protocol/calibration.go`, `gateway/calibration.go` | workers, ratings | Read-only report; "Part 2 is read only" (`protocol/calibration.go:8-11`). |
| Quality ratings (worker rating, late defects, rework, quality weighting) | Module | `protocol/rate.go`, `protocol/quality.go`, `protocol/rework.go`, `gateway/ratings.go`, `gateway/quality*.go`, `tui/quality_cards.go` | workers | Weighting only: setting `quality_weighting` `{enabled}` (`gateway/quality_settings.go:12-45`, op `quality_get/set`). Rating ask and recording: no switch. |
| Track record and model playbook (notes, findings, auto learning) | Module | `protocol/track.go`, `protocol/learn.go`, `gateway/track.go`, `gateway/learned.go`, `agent_learn` tool | workers | Pin a note (`--pin`). Learning: no switch. |
| Worker cost memory and machine admission | Module | `daemon/cost.go`, `daemon/admission.go` | workers | `node.json` `workers.cpu_target`, `mem_reserve`, `start_gap`, `queue_timeout`, `max` (`daemon/join.go:57-65`). |
| Delegation policy (per session or project: off, native subagent, workers) | Module | `protocol/delegation.go`, `daemon/policy.go`, `gateway/store_policy.go`, `internal/claudeagents`, `setup/policyhook.go` | integrations (policy hook), workers | Integration `claude_policy_hook`; policy value `off` is itself a policy. |
| Worker sandbox / permissions | Module + Playbook | `protocol/delegation.go:20-31`, `protocol/maturity.go` (`worker_sandbox`), `gateway/store_policy.go:83-114`, `protocol/workers.go:543-566` | workers, graph | Level choice per project; enforced for claude, codex and agy only (`protocol/workers.go`, `WorkerEnforcer`). |
| Skills and tools management (inventory, plans, apply, undo, per-project on/off) | Module | `internal/inventory`, `internal/toolsdiff`, `gateway/tools_*.go`, `gateway/skillstore.go`, `daemon/tools_*.go`, `tui/tools*.go`, decision 0048 | inventory, proposals | Per skill and per project on/off is the feature itself ("new projects" flag, `gateway/store.go:549`). Whole module: no switch. |
| MCP federation (shared MCP servers hosted by daemons, import, coverage, scoped servers) | Module | `daemon/mcphost.go`, `daemon/mcp_native.go`, `daemon/mcp_coverage.go`, `gateway/mcp*.go`, `protocol/mcp*.go`, `inventory/mcp_*.go` | gateway, daemon | Per server `enabled` (`protocol/mcp.go:68`, `MCPSetEnabled` line 235). Whole module: no switch. |
| Quotas (provider quota reading) | Module | `internal/quotaaxi`, `daemon/quota.go`, `protocol/quota.go`, `tui/quotas.go`, `tui/view_quotas.go` | quota-axi binary | Yes. Integration `quota_axi`; setup flags `--quota-axi` / `--no-quota-axi` (`docs/reference.md:661-662`). |
| Usage figures and top lists | Module | `gateway/usage.go`, `tui/usage.go` | usage sampling | No. |
| Hints after tool errors | Module | `internal/hint`, `daemon/hint.go`, `setup/hinthook.go` | graph, sessions | Integration `claude_hint_hook`. |
| Peer-message acceptance for Claude | Module (harness setting) | `setup/peermsgs.go` | Claude settings | Integration `claude_peer_messages`; setup flags. |
| Codex tool approval | Module | `setup/integrations.go` | Codex config | Integration `codex_tools`. |
| Claude workers plugin (shows workers above prompt) | Module/Surface | `plugins/claude/agent-net-workers` | workers | Integration `claude_workers_mod`. |
| Claude "Needs you" hooks | Module | `setup/` needs hook, `agent-net needs-hook`, `protocol/waiting.go`, `daemon/waiting.go` | Inbox | Integration `claude_needs_hook`. |
| Notifications (desktop) | Module/Surface | `daemon/notify.go` | Inbox state | Yes. Daemon table `owner_preferences` key `notifications_disabled` (`daemon/notify.go:46-53`, TUI Settings row, `tui/settings.go:104`). |
| Owner onboarding tips | Surface | `gateway/onboarding.go`, `tui/` | TUI | Yes. `owner_settings` key `onboarding`, "Tips" row (`tui/settings.go:108`). |
| Hidden items (TUI visibility) | Surface | `gateway/hidden.go` | TUI | Is itself a view setting. |
| Antigravity harness | Module | `daemon/agy_peer.go`, `daemon/worker_agy.go`, decision 0049 | harness detection | Absent when `agy` is not installed. No switch. |

### 1.3 Surfaces

| Feature | Layer | Where | Depends on | Off today? |
|---|---|---|---|---|
| TUI (7 tabs: Sessions, Graph, Quotas, Messages, Tools, Settings, Inbox) | Surface | `internal/tui/` (model.go, view_main.go, tools_plan.go, inbox.go and more), `internal/operator/` client | operator methods | Tabs are fixed in code (`tui/model.go:1222` loop, help in `tui/help.go`). No per-tab switch. |
| CLI (`agent-net list`, `workers`, `send`, `start`, `charter`, `playbook`, and so on) | Surface | `cmd/agent-net/*.go` | operator methods | No. |
| CTO chat and CTO box in the TUI | Surface | `tui/cto.go`, `daemon/cto_tools.go` | CTO module | Not opened means not running. |
| Phone app and bot listener (connected apps) | Surface | `internal/phone`, `gateway/apps*.go`, `gateway/bot_*.go`, `gateway/phone_*.go`, `gateway/oauth*.go`, `internal/funnel`, `tui/apps.go` | gateway, owner identity | Yes. Setting `public` in `owner_settings` (`gateway/apps_public.go:45-50`), Settings row "Internet access", flags `--apps-listen`, `--apps-public-url` (`docs/reference.md:640-650`). A saved off stays off across restarts (`docs/reference.md:517`). |
| Grok bot (MCP endpoint at `/mcp`) | Surface | `gateway/bot_tools.go`, `gateway/apps.go` | connected apps | Revoke the app; or switch off public listener. |
| Herdr/session entry from TUI | Surface | `daemon/enter.go`, `tui/enter.go`, `tui/session_form.go` | herdr | See Foundation row. |
| Pi extension and OpenCode plugin | Surface/Foundation (harness adapters) | `plugins/pi`, `plugins/opencode` (TS, embedded) | tool catalog (`tools.gen.ts` is generated) | Replaced at daemon start (`agentfiles`); no switch. |

### 1.4 Reading the table

- Most Modules are **not** switchable today. Only these have a real switch: quotas, hint hook, policy hook, needs hook, workers plugin, Codex tool approval, Claude peer messages, harness updates, quality weighting, notifications, tips, connected apps, `no_launch`, `enter_ssh`.
- The switches that exist are about **installing things into a machine** (hooks, config files) or **view options**. None of them turns off a server-side module such as workers, the judge, the graph or the Inbox.
- Scope today: integrations are **per network** with a **per machine** override (`integrations-local.json`). `no_launch` is per machine. Node settings are per project node. Nothing is "per network, machine or project" in one uniform way.

## 2. Built-in opinions

Every built-in choice about how the owner works is one row below, verified against this checkout. Advice is text an agent reads and may ignore; a rule is enforced by agent-net. Lower levels may change advice freely, while a rule may only be tightened going down the hierarchy (`network -> team -> repo -> run` and `network -> team -> agent`). Rows are ordered by area: guidance (1-24), CTO (25-40), charter and maturity (41-50), workers and picker (51-83), judge (84-93), quotas and providers (94-101), approvals and Inbox (102-119), skills (120-126), delivery (127-137), notices (138-149).

| # | Opinion (plain words) | Where (file:line, verified on current code) | Today's value (short) | Kind: advice or rule | Home level: network, team, repo, agent or run | Lowest level that may override (or "none") | Proposed setting key | Notes |
|---|---|---|---|---|---|---|---|---|
| 1 | Write the agent-net guidance block into user-level agent instruction files on daemon startup | `internal/agentfiles/agentfiles.go:87-95,312`; `internal/agentfiles/antigravity.go:32`; `internal/daemon/daemon.go:136-140` | always on (CLAUDE.md, AGENTS.md, rules/AGENTS.md) | rule | network | none | `guidance.sync_agent_files` | [guidance] Daemon rewrites the managed block on start with no recipe toggle today. |
| 2 | Tell every session that other coding agents run on the owner's machines and can reach it | `internal/guidance/guidance.md:3` | enabled on all harnesses | advice | network | agent | `guidance.intro_text` | [guidance] Peer-mesh orientation; unnecessary for isolated tracker runs. |
| 3 | Tell sessions to answer peer questions and requests directly without asking the owner first | `internal/guidance/guidance.md:5`; `internal/guidance/guidance.go:26-30`; `internal/daemon/claude/claude.go:277` | reply directly; do not wait for owner | advice | network | agent | `guidance.peer_help_direct` | [guidance] Reply tool name varies per harness in guidance.go:26-30. |
| 4 | Tell sessions to decide by charter (accept, reject with reason, or redirect) and escalate only with a recommendation | `internal/guidance/guidance.md:6`; `internal/protocol/graph_context.go:15-16` | decide, do not defer; escalate with recommendation | advice | network | agent | `guidance.decide_not_defer` | [guidance] Duplicated in protocol.DecideRule; gateway separately enforces outcome fields (see inbox.outcome_escalation_rules). |
| 5 | Tell sessions when to check with the owner before acting on a peer request | `internal/guidance/guidance.md:7`; `internal/protocol/trust.go:27` | outside task, outside repo, or risky | advice | network | agent | `guidance.owner_check_triggers` | [guidance] Paired with PeerCheckNote on untrusted requests. |
| 6 | Tell trusted sessions that commits, branch pushes, and pull requests need no new ask | `internal/guidance/guidance.md:7`; `internal/protocol/trust.go:13` | commit, push branch, PR allowed without re-asking | advice | repo | agent | `guidance.trusted_free_actions` | [guidance] Applies only when agent-net attaches the peer-trust or owner-approved note. |
| 7 | List high-risk actions that always require asking the owner even when peer trust is on | `internal/guidance/guidance.md:7`; `internal/protocol/trust.go:13` | delete data, force-push, rewrite history, spend money, share secrets, change creds/perms, deploy beyond brief | advice | network | agent | `guidance.always_ask_actions` | [guidance] Written as advice today; candidate for enforced harness/git gates where detectable. |
| 8 | Tell sessions how to weigh the two CTO notes (owner-instructed vs CTO model output) | `internal/guidance/guidance.md:8`; `internal/protocol/trust.go:16,19` | owner-instructed = owner word in charter; session = peer | advice | network | team | `guidance.cto_note_trust` | [guidance] Note injection is enforced; how the receiver weighs each note is advice. |
| 9 | Tell sessions when to contact a peer proactively (breaking change, question, requested result) | `internal/guidance/guidance.md:9` | find session, read charter, send message | advice | network | agent | `guidance.peer_contact_triggers` | [guidance] Assumes live peer sessions rather than tracker handoffs. |
| 10 | Tell sessions to ask an owning peer early on remote service errors or after two failed tries | `internal/guidance/guidance.md:10` | ask peer on 5xx/refused/deploy fail or after 2 tries | advice | network | agent | `guidance.ask_peer_after_tries` | [guidance] Sets the 2-try debugging threshold before asking a peer. |
| 11 | Tell sessions to relay owner-approved risky requests to peers unedited and let the receiver decide | `internal/guidance/guidance.md:11` | sender relays with owner-approved note; peer decides | advice | network | agent | `guidance.relay_unedited` | [guidance] Prevents double-gating on the sender side. |
| 12 | Tell sessions to keep peer messages short: point first, repo/branch/files context, one need, no acks | `internal/guidance/guidance.md:12` | point first, one per need, no status/thanks/acks | advice | network | agent | `guidance.message_style` | [guidance] Cuts chatter and reply loops across sessions. |
| 13 | Require every session to announce its current task, set expertise once, and call agent_owner_ask when waiting | `internal/guidance/guidance.md:13`; `internal/toolcatalog/tools.go:22` | required at session start and task change | advice | network | agent | `guidance.announce_task` | [guidance] Marked REQUIRED in text and tool description, not enforced by a gate. |
| 14 | Tell sessions only the owner changes shared MCP servers and never to put raw secrets in tool calls | `internal/guidance/guidance.md:14` | owner-only MCP edits; pass env name or file path | advice | network | none | `guidance.mcp_and_secret_hygiene` | [guidance] Complements the owner gate on MCP add/remove and header env/file schema. |
| 15 | Tell sessions to register projects only when the owner asks and run dry_run first on folder scans | `internal/guidance/guidance.md:15`; `internal/toolcatalog/tools.go:29` | only when owner asks; dry_run first | advice | agent | none | `guidance.register_projects_when_asked` | [guidance] Bound non-worker agents have the tool; only text restricts when they call it. |
| 16 | Tell sessions to start a worker only when the owner asks | `internal/guidance/guidance.md:16`; `internal/toolcatalog/tools.go:25` | only when owner asks (unless delegation policy says workers) | advice | agent | none | `guidance.start_workers_when_asked` | [guidance] Key autonomy opinion (modularity.md:137 agent.may_start_workers); daemon allows any non-worker caller today. |
| 17 | Tell callers to write self-contained worker briefs and leave difficulty and touches unset for the judge | `internal/guidance/guidance.md:16` | peer-who-knows-nothing brief; do not set difficulty/touches | advice | network | agent | `guidance.worker_brief_style` | [guidance] Prevents dumping full chat history into worker tasks. |
| 18 | Tell callers to wait for the worker completion notice instead of polling session or message tools | `internal/guidance/guidance.md:17` | wait for notice; do not poll | advice | network | agent | `guidance.wait_for_worker_notice` | [guidance] Matches push delivery of worker notices. |
| 19 | Tell callers to report defects found after hand-back or merge with a 1-to-4 severity scale | `internal/guidance/guidance.md:19`; `internal/toolcatalog/tools.go:27` | required on late defect; severity 1..4 | advice | network | agent | `guidance.report_late_defects` | [guidance] Feeds rework points into model quality weighting. |
| 20 | Tell workers to follow [agent-net worker brief] over every general guidance rule, including announce_task | `internal/guidance/guidance.md:20` | worker brief overrides general guidance | advice | network | run | `guidance.worker_brief_precedence` | [guidance] Needed today because global CLAUDE.md/AGENTS.md applies to worker processes too. |
| 21 | Delegation policy mode telling an agent to code directly, use a native coder subagent, or use workers | `internal/protocol/delegation.go:17-20,53-55`; `internal/gateway/store_policy.go:38-64` | unset (none); modes off, native, workers | advice | agent | none | `delegation.mode` | [guidance] Delivered as [agent-net policy] prompt text; maps to modularity.md:137 agent.may_write_code. |
| 22 | Fall back from native subagent delegation to workers on harnesses without native subagent support | `internal/protocol/delegation.go:227-249` | native -> workers when unsupported (only claude supports native) | rule | agent | none | `delegation.native_unsupported_fallback` | [guidance] Enforced when computing EffectiveMode for a session. |
| 23 | Allow a delegating lead session to edit code itself after two subagent or worker failures | `internal/protocol/delegation.go:278-294` | self-edit allowed after 2 failures | advice | agent | none | `delegation.max_failures_before_self_edit` | [guidance] Hardcoded in PolicyText for both native and workers modes. |
| 24 | System prompt and description written into managed agent-net-coder-* native subagent files | `internal/protocol/delegation.go:264-269`; `internal/claudeagents/claudeagents.go:37-45` | implement cleanly, run relevant checks, report concise findings | advice | agent | none | `delegation.coder_subagent_prompt` | [guidance] Written to ~/.claude/agents/agent-net-coder-<model>.md. |
| 25 | CTO system brief defining role, live-agent charter drafting, diff/undo habit, and untrusted-input stance | `internal/daemon/cto.go:26,28,34,36,41,43,45` | fixed multi-paragraph ctoBrief | advice | network | none | `cto.brief_text` | [CTO] Single CTO per network (modularity.md:125). |
| 26 | CTO brief instruction to avoid protected providers for workers and ask the owner first | `internal/daemon/cto.go:30` | use other subscriptions first; ask owner before protected | advice | network | none | `cto.protected_provider_advice` | [CTO] Assumes an orchestrator session needs its subscription preserved. |
| 27 | CTO brief instruction to offer repointing an unavailable worker profile under the same name | `internal/daemon/cto.go:32` | propose worker_profile_add under same name | advice | network | none | `cto.repoint_unavailable_profile` | [CTO] Guides how the CTO maintains the worker roster. |
| 28 | CTO brief charter-size advice (1200 chars total, claims neighbors read 160 chars of accepts/refuses) | `internal/daemon/cto.go:38` | budget ~1200 chars; says 160 chars for neighbors | advice | network | none | `cto.charter_budget_advice` | [CTO] Drift: neighbor_context.go:12 actually cuts neighbor items at 40 chars, not 160. |
| 29 | Default CTO harness and allowed CTO harnesses | `internal/gateway/cto_settings.go:58,68,139` | default opencode; allowed opencode, claude | rule | network | none | `cto.allowed_harnesses` | [CTO] Validated by gateway when saving cto_settings. |
| 30 | Owner-facing CTO display title default and length cap | `internal/protocol/cto.go:14,30-56` | "CTO" (1..24 printable chars) | rule | network | none | `cto.title` | [CTO] Agents always see "CTO" in trust notes regardless of custom title. |
| 31 | CTO auto-compaction context threshold and idle cache-expiry window | `internal/protocol/cto.go:76-77,86-88` | compact at 50% context after 300s idle (enabled) | rule | network | none | `cto.compaction_policy` | [CTO] Bounded in gateway/cto_settings.go (10..100%, up to 24h window). |
| 32 | CTO MCP tool classes (read, talk, proposal) and as_owner proposal promotion | `internal/daemon/cto_classes.go:23-35,91-94,158-166`; `internal/mcpserver/cto_v2.go:15-28` | read/talk immediate; changes + message(as_owner) are proposals | rule | network | none | `cto.tool_classes` | [CTO] Unknown tools fail closed as proposals. |
| 33 | Allowlist of change actions and operator methods the CTO may propose or run | `internal/daemon/cto_classes.go:53-88`; `internal/daemon/cto_tools.go:570-585` | fixed action routes and ctoOwnerMethods/ctoLocalMethods | rule | network | none | `cto.allowed_actions` | [CTO] Gates what CTO proposals can execute on owner approval. |
| 34 | Graph change classification into minor (immediate) vs major (needs owner window or proposal) | `internal/gateway/cto_graph.go:46-157` | node_add/additive/depends minor; remove/settings/refuses/escalate/governs/deploys/critical major | rule | network | none | `cto.graph_minor_rules` | [CTO] Modularity plan extends this: CTO may adjust advice within limits; rules need owner (modularity.md:178). |
| 35 | English restriction words that promote a charter wording edit from minor to major | `internal/gateway/learned_apply.go:13`; `internal/gateway/cto_graph.go:121-125` | 16 words (only, never, must, ask, refuse, prohibited, ...) | rule | network | none | `cto.restriction_words` | [CTO] Prevents silent narrowing of a charter without owner review. |
| 36 | Duration and scope of the owner's TUI 'I'm driving' window for major CTO graph changes | `internal/daemon/cto_window.go:31,44-46,85-92`; `internal/daemon/cto.go:26` | 30m in-memory from TUI; graph changes only | rule | network | none | `cto.owner_window_minutes` | [CTO] Non-graph owner-only changes still require Inbox approval during the window. |
| 37 | Expiry time for unanswered CTO proposals | `internal/daemon/proposals.go:256,285` | 24h | rule | network | none | `cto.proposal_expire_hours` | [CTO] Expired proposals cannot be executed. |
| 38 | Maximum open CTO proposals, duplicate-call rejection, and attached CTO timeline context | `internal/daemon/proposals.go:296-301,310` | max 20 open; no exact duplicates; attach last 3 CTO turns | rule | network | none | `cto.proposal_max_open` | [CTO] Prevents proposal flooding in the Inbox. |
| 39 | Daily cap and field rules for auto-applying minor learned charter/graph changes | `internal/gateway/learned_apply.go:17-37,125-133` | owner-accepts, needs, consumers, depends minor; max 3/node/24h | rule | network | repo | `cto.learned_minor_daily_cap` | [CTO] Lower levels may only tighten (fewer auto-applied changes per day, down to 0). |
| 40 | Signal capture limits for graph learning (Claude prompt length, send window, quiet delay, signal horizon) | `internal/gateway/signal.go:36-38,81,107`; `internal/gateway/learned.go:83,175` | 300-char prompt, 10m send window, 2m node quiet, 90d horizon | rule | network | none | `cto.learned_signal_windows` | [CTO] Controls how owner prompts and reply outcomes feed learned charter suggestions. |
| 41 | One-node-per-project identity and special architect node kind instead of separate repo and agent objects | `internal/gateway/graphread.go:41-56`; `internal/gateway/store_graph.go:26-47` | session maps 1-1 to repo project; kinds component, architect, cto | rule | agent | none | `charter.agent_ownership_and_role` | [charter and maturity] modularity.md:57-65,137 splits repo from agent (owns + free-text role) and makes an architect an agent owning a team. |
| 42 | Repository charter scope and escalation fields (purpose, accepts, refuses, escalate) | `internal/protocol/graph.go:13-23`; `internal/protocol/graph_context.go:26-52` | stored per graph node; read every turn as charter advice | advice | repo | agent | `charter.scope_and_escalation` | [charter and maturity] Purpose/accepts/refuses belong to repo (or team for team purpose); an agent may add stricter escalation for itself (modularity.md:61,131). |
| 43 | Repository maturity tier and fallback tier when unset | `internal/protocol/maturity.go:15-19,102-117` | levels early, growing, mature, critical; unset uses growing defaults | rule | repo | none | `repo.maturity` | [charter and maturity] Property of the software (modularity.md:132); validated against the 4 tiers. |
| 44 | Preset matrix mapping each maturity tier to default test, merge, sandbox, consumer, and deploy settings | `internal/protocol/maturity.go:102-117`; `internal/mcpserver/cto.go:50` | early/growing/mature/critical -> 5 node settings | rule | network | none | `maturity.level_presets` | [charter and maturity] Network preset table; individual fields override at repo/agent/run via their own settings. |
| 45 | How strictly a repo judges incoming requests from consumer nodes (judge_consumers) | `internal/protocol/maturity.go:32-36,106-116,239-249` | charter (growing/unset); free, charter, strict, escalate | advice | repo | agent | `charter.judge_consumers` | [charter and maturity] Rendered as ConsumerText inside charter context. |
| 46 | Default agent autonomy mode (whether idle checks wake the session to continue or wait for the owner) | `internal/protocol/maturity.go:58-60,156-164`; `internal/gateway/check.go:93-108` | off (allowed: off, wake) | rule | agent | none | `charter.autonomy_mode` | [charter and maturity] Stored per graph node today; modularity.md:60,137 places agent autonomy at agent. |
| 47 | Charter attribute schema and size warning budgets (total, purpose, list item count, first item length) | `internal/protocol/charter_guide.go:22-32,99-118` | 9 attrs; warn >1200 chars, purpose >160, >6 items, first item >40 chars | advice | network | team | `charter.size_budgets` | [charter and maturity] Advisory warnings returned on charter edits, not hard rejections. |
| 48 | Charter and node-rules text block injected into sessions and worker briefs | `internal/protocol/graph_context.go:66-133`; `internal/protocol/maturity.go:254-263` | CharterBrief + RulesText + DecideRule layout | advice | network | repo | `charter.context_template` | [charter and maturity] Controls how repo purpose, accepts, refuses, escalation, and rules are phrased. |
| 49 | Neighbor summary truncation, priority order, count cap, and byte budget in session context | `internal/protocol/neighbor_context.go:12,37-48,74-79` | first item <=40 chars; max 8 neighbors, 1500B; priority rule!=normal > governs > deploys > depends | rule | network | team | `charter.neighbor_summary_limits` | [charter and maturity] Lower levels may only tighten (fewer neighbors or smaller byte cap). |
| 50 | Graph edge kinds, governance modes, and request relationship wording (order, suggestion, information) | `internal/mcpserver/cto.go:48`; `internal/protocol/graph_context.go:138-154`; `internal/gateway/graphread.go:93-102` | depends, governs (advisor/arbiter/owner), deploys -> RelationText | advice | network | team | `relationships.edge_semantics` | [charter and maturity] Maps directly to the Relationships module (modularity.md:86-95,127). |
| 51 | Default worker filesystem isolation in git repositories and Pi worktree requirement | `internal/daemon/workers.go:545-553`; `internal/daemon/worker_continue.go:60-64` | worktree in git repos (Pi always requires worktree; continue falls back to none if missing) | rule | network | run | `workers.isolation_minimum` | [workers and picker] Lower levels may only tighten (require worktree, disallow isolation: none; modularity.md:136). |
| 52 | Default worker sandbox permission level | `internal/protocol/delegation.go:25-31,44-49`; `internal/gateway/store_policy.go:67-115`; `internal/protocol/workers.go:495,546-560` | sandboxed (critical maturity -> no-network; levels: sandboxed, no-network, read-only) | rule | network | run | `workers.sandbox_level` | [workers and picker] Lower levels may only tighten (sandboxed -> no-network -> read-only). Enforced for Codex and Antigravity today; prompt advice on other harnesses. |
| 53 | Maximum wall-clock run age before the daemon stops a worker | `internal/protocol/workers.go:17`; `internal/daemon/join.go:63` | 4h | rule | network | run | `workers.max_age_minutes` | [workers and picker] Lower levels may only tighten (shorter max age). |
| 54 | Idle timeout before an inactive worker is stopped | `internal/protocol/workers.go:16`; `internal/daemon/join.go:61` | 60m | rule | network | run | `workers.idle_timeout_minutes` | [workers and picker] Lower levels may only tighten (shorter idle limit). |
| 55 | Grace period before stopping a worker whose calling session went offline | `internal/protocol/workers.go:18`; `internal/daemon/join.go:64` | 10m | rule | network | run | `workers.caller_gone_grace_minutes` | [workers and picker] Lower levels may only tighten when session-owned; tracker-owned Conductor runs have no calling session (Section 3). |
| 56 | Maximum time a worker start may wait in the machine admission queue | `internal/protocol/workers.go:46`; `internal/daemon/join.go:67-68` | 2h | rule | network | run | `workers.queue_timeout_minutes` | [workers and picker] Lower levels may only tighten (shorter queue wait). |
| 57 | Hard cap on concurrent workers per machine | `internal/protocol/workers.go:15`; `internal/daemon/join.go:59-60` | 12 | rule | network | none | `workers.max_per_machine` | [workers and picker] Machine is not an inheritance level (modularity.md:82-85); hosts may tune hardware caps in node.json. |
| 58 | Machine CPU busy thresholds for worker admission (5-minute target and 1-minute hot ceiling) | `internal/protocol/workers.go:26,29`; `internal/daemon/join.go:65-66` | 75% (5m target), 95% (1m hot cap) | rule | network | none | `workers.cpu_admission_limits` | [workers and picker] Shared network default; per-host node.json override sits outside recipe inheritance. |
| 59 | Minimum free RAM reserve kept when admitting new workers | `internal/protocol/workers.go:36-37`; `internal/daemon/join.go:66-67` | max(1536 MiB, 10% of RAM) | rule | network | none | `workers.memory_reserve` | [workers and picker] Shared network default; per-host node.json override sits outside recipe inheritance. |
| 60 | Assumed resource cost for an unmeasured model and run count kept for cost learning | `internal/protocol/workers.go:32-33,49` | 600 MiB RAM, 1.0 core; keep last 20 runs per harness+model | rule | network | none | `workers.cost_model_defaults` | [workers and picker] Calibration for machine admission. |
| 61 | Minimum spacing between consecutive worker starts on one machine | `internal/protocol/workers.go:42`; `internal/daemon/join.go:68-70` | 6s | rule | network | none | `workers.start_gap_seconds` | [workers and picker] Smooths burst starts while load sampler catches up. |
| 62 | Multi-machine placement rule when a project exists on several machines | `internal/gateway/workers.go:373-422,1065-1084` | caller's own machine first for CallerProject; else machine with most headroom | rule | network | none | `workers.machine_placement_policy` | [workers and picker] Refuses ambiguous multi-machine starts when load room is unknown. |
| 63 | Explicit model override for a single worker run | `internal/gateway/workers.go:466,528-545,564-586` | auto/empty uses picker; explicit model overrides picker; empty harness+model defaults to opencode | rule | run | none | `workers.run_model` | [workers and picker] Matches modularity.md:139 (Model for one task lives at run, inside what is allowed above). |
| 64 | Automatic continue when a worker stops early without handing back | `internal/protocol/workers.go:78`; `internal/daemon/worker_auto_continue.go:17-20` | enabled by default (absent = true) | rule | network | run | `workers.auto_continue` | [workers and picker] Lower levels may only tighten (turn off auto-continue for a repo, agent, or run). |
| 65 | Maximum automatic continues sent before reporting an early worker stop | `internal/protocol/workers.go:55` | 2 | rule | network | run | `workers.auto_continue_max` | [workers and picker] Lower levels may only tighten (fewer continues, down to 0). |
| 66 | Prompt wording sent on automatic and manual worker continuations | `internal/protocol/workers.go:60-61,150-159` | carry on until done, then hand back with complete: yes or say what blocks you | advice | network | repo | `workers.continue_prompt_text` | [workers and picker] Covers WorkerAutoContinueText and ContinueIntro. |
| 67 | Worker preamble brief (do whole task, no gold-plating, no sub-delegation, absolute paths, honest complete: false, no report files) | `internal/protocol/workers.go:499-528` | WorkerPreamble + WorkerPreambleCodex + WorkerClaudeNote | advice | network | repo | `workers.brief_preamble` | [workers and picker] Codex variant swaps agent_handback for plain final message with complete: yes/no. |
| 68 | Difficulty tier names and minimum capability scores required for each tier | `internal/protocol/chooser.go:278`; `internal/protocol/scores.go:185-187`; `internal/gateway/scores.go:81-128` | trivial 0, simple 20, normal 30, hard 40, very_complex 50 | rule | network | none | `picker.difficulty_minimums` | [workers and picker] Already stored in owner_settings key difficulty_minimums; calibration lives at network (modularity.md:123). |
| 69 | Baseline model capability table and default effort tier when effort is unnamed | `internal/protocol/scores.go:19-29,118-120`; `internal/protocol/scores.json:1` | embedded scores.json + owner overrides; unnamed effort uses medium | rule | network | none | `picker.model_scores` | [workers and picker] Shared calibration across the whole network. |
| 70 | Score-margin bands classifying model fit as excellent, good, poor, or denied | `internal/protocol/scores.go:202-211` | >=+10 excellent, >=0 good, >=-10 poor, <-10 denied | rule | network | none | `picker.fit_bands` | [workers and picker] Calibration bands used in picker notes and TUI. |
| 71 | Eligibility rule allowing automatic pick only at a profile's max difficulty or one level below | `internal/protocol/chooser.go:255-276`; `internal/gateway/workers.go:580-586` | max gap = 1 level below profile max | rule | network | none | `picker.max_level_gap` | [workers and picker] Keeps strong models from spending quota on trivial work; explicit model overrides with a note. |
| 72 | Exponential weight decay penalizing over-qualified models above the difficulty minimum | `internal/protocol/chooser.go:249,308` | scale = 8.0 score points | rule | network | none | `picker.overqualified_scale` | [workers and picker] Calibration weight in workerRotationWeight. |
| 73 | Recent-pick rotation memory window avoiding repeated selection of the same profile | `internal/protocol/chooser.go:52` | last 10 picks | rule | network | none | `picker.rotation_window` | [workers and picker] Spreads load across eligible profiles. |
| 74 | Quota usage ceiling above which the automatic picker excludes a provider window | `internal/protocol/chooser.go:389` | 90% used (or exhausted) | rule | network | none | `picker.quota_max_percent` | [workers and picker] Leaves a 10% buffer for interactive work or explicit starts. |
| 75 | Quota-room penalty subtracted per currently running worker on the same provider | `internal/protocol/chooser.go:151` | 5 percentage points per running worker | rule | network | none | `picker.running_worker_penalty` | [workers and picker] Prevents concurrent starts from piling onto one subscription. |
| 76 | Quota pace weighting curve favoring providers ahead of their reset clock | `internal/protocol/chooser.go:302` | exp(room / 20) clamped to [0.1, 4.0] | rule | network | none | `picker.quota_pace_curve` | [workers and picker] Calibration curve in workerRotationWeight. |
| 77 | Track-record completion bonus, last-outcome multipliers, and lookback window in the picker | `internal/protocol/chooser.go:310-317`; `internal/protocol/track.go:18` | 30d window; >=5 runs adds 0.20*complete_share; last fail *0.5, last ok *1.2 | rule | network | none | `picker.track_weights` | [workers and picker] Rewards models that finish cleanly on the same machine and harness. |
| 78 | Poor-track threshold demoting a model to last-resort tier | `internal/protocol/chooser.go:183-199,432-435` | >=5 finished runs and <50% complete | rule | network | none | `picker.poor_track_threshold` | [workers and picker] Used only when no adequate non-poor model is available. |
| 79 | Quality-rating picker weight toggle and multiplier clamp | `internal/protocol/quality.go:124-127`; `internal/protocol/chooser.go:318-320` | disabled by default; clamped to [0.5, 1.5] when enabled | rule | network | none | `picker.quality_weighting` | [workers and picker] Stored in owner_settings key quality_weighting. |
| 80 | Worker rating sampling cadence, 1-to-5 rating scale, and 200-byte note limit | `internal/protocol/rate.go:18-40` | every 5th run, or first 3 runs while model has <5 ratings; scale 1..5, defect 1..4 | rule | network | none | `picker.rating_sampling` | [workers and picker] Controls when completion notices ask the caller to rate a run. |
| 81 | Quality estimator priors: minimum ratings, ridge pull, weak-rater discount/cap, and rework/defect cost minutes | `internal/protocol/quality.go:86-101`; `internal/gateway/quality.go:14,209-214,329-331` | min 5 ratings (20 analysis), ridge 10, rater<45 weight 0.5 & no >avg boost, rework 2/10m, defect 10/30/60/180m | rule | network | none | `picker.quality_estimator_priors` | [workers and picker] Calibration constants for model quality cards and weights. |
| 82 | Agent finding rate limit, staleness horizons, and automatic playbook-note promotion thresholds | `internal/protocol/learn.go:35-42`; `internal/gateway/learn.go:149,170-173` | 3/session/day (500B); promote in 30d at >=3 findings from >=2 sessions or >=4 decided runs <50% ok; stale 90d/60d | rule | network | none | `picker.playbook_promotion` | [workers and picker] Pinned or owner-written playbook notes are never overwritten. |
| 83 | Difficulty calibration report window, rework weights, strong-caller cutoff, and offset bounds | `internal/protocol/calibration.go:15-30,240-248` | window 7, min 5 tasks, fix 1.0, continue 0.33, strong >=45, lower <0.5, raise >1.5, clamp [-2,+2] | rule | network | none | `picker.calibration_thresholds` | [workers and picker] Read-only report today (protocol/calibration.go:8-11). |
| 84 | Ignore any caller-supplied difficulty label and always use the difficulty judge's label on worker start | `internal/daemon/operator.go:320-326` | caller difficulty overwritten by judge label | rule | network | none | `judge.ignore_caller_difficulty` | [judge] Enforces guidance.md:16 in code so callers cannot game picker tiers. |
| 85 | Primary difficulty judge harness and model | `internal/daemon/difficulty_assessor.go:83-86`; `internal/daemon/daemon.go:194-195` | opencode + opencode-go/deepseek-v4.1-flash | rule | network | none | `judge.primary_model` | [judge] Calibration lives at network (modularity.md:110,123). |
| 86 | Fallback retry judge harness, model, reasoning effort, and reserved retry budget | `internal/daemon/difficulty_assessor.go:119-121,142-145,486` | codex + gpt-6.1-sol (effort low); reserve 21s when budget >=40s | rule | network | none | `judge.retry_model` | [judge] Used only when the primary judge fails or times out. |
| 87 | Difficulty judge timeout on worker start vs standalone assessment | `internal/daemon/worker_assessment.go:19`; `internal/daemon/difficulty.go:30,34` | 60s on worker start; 180s one-shot (10s roster lookup) | rule | network | none | `judge.timeout_seconds` | [judge] Start timeout raised from 30s to 60s after measured 15% timeout rate. |
| 88 | Read-only codebase analyzer pass before difficulty judging | `internal/daemon/worker_assessment.go:34`; `internal/daemon/daemon.go:191-193`; `internal/daemon/difficulty.go:38`; `internal/daemon/difficulty_assessor.go:278` | off on worker start; codex-only, lowest-max Codex profile when enabled | rule | network | none | `judge.analyzer_enabled` | [judge] Scores the task brief directly by default. |
| 89 | Fallback difficulty label when the judge is missing, errors, times out, or reports low confidence | `internal/protocol/worker_assessment.go:68-76`; `internal/daemon/worker_assessment.go:51-57` | normal (never blocks worker start) | rule | network | none | `judge.fallback_level` | [judge] Keeps judge outages from blocking runs. |
| 90 | Six-dimension 1-to-5 difficulty rubric and anchor examples | `internal/difficulty/difficulty.go:28-35` | reasoning, scope, coupling, uncertainty, verification, risk (examples from agent-net) | advice | network | none | `judge.rubric_text` | [judge] Prompt read by the judge model; anchor examples currently cite Go/SQLite/TUI files from this repo. |
| 91 | Codebase analyzer prompt rules for counting distinct changes and coupling | `internal/difficulty/difficulty.go:37-42` | count distinct behavior changes, check real coupling, cite files/symbols | advice | network | none | `judge.analyzer_prompt` | [judge] Prompt read by the analyzer model when enabled. |
| 92 | Dimension weights and numeric cut points mapping weighted scores to difficulty labels | `internal/difficulty/difficulty.go:18,290-291` | weights 0.20,0.10,0.20,0.15,0.15,0.20; cuts 1.5, 2.25, 3.0, 3.75 (v2026-11-02.1) | rule | network | none | `judge.weights_and_cut_points` | [judge] Enforced in Go after parsing the 6 integer dimension scores. |
| 93 | Single-dimension bottleneck floor when any rubric dimension scores 5 | `internal/difficulty/difficulty.go:308-318` | at least hard on any 5 | rule | network | none | `judge.bottleneck_floor` | [judge] Prevents low scope/coupling from masking 5-point reasoning or risk. |
| 94 | Protected providers list reserved as last-resort fallback in the automatic picker | `internal/protocol/protected_providers.go:11-16`; `internal/gateway/scores.go:185-200`; `internal/gateway/protected_chooser.go:15-55`; `internal/gateway/workers.go:555-557,594-599` | empty by default; unprotected first, refused for auto read-only, flagged when explicit | rule | network | none | `providers.protected_list` | [quotas and providers] Shared money/subscription setting at network (modularity.md:109,121). |
| 95 | Default protected-provider explanation wording ('saved for your orchestrator') | `internal/gateway/protected_chooser.go:36,58-61`; `internal/gateway/workers.go:598` | "saved for your orchestrator" / "your orchestrator's provider" | advice | network | none | `providers.protected_reason_text` | [quotas and providers] Hardcodes the assumption that a human-facing orchestrator session owns that subscription. |
| 96 | Provider pool mapping that exempts Claude models on Antigravity (agy-anthropic) from Anthropic protection | `internal/protocol/protected_providers.go:19-37` | claude harness -> anthropic; agy claude models -> agy-anthropic | rule | network | none | `providers.pool_mapping` | [quotas and providers] Reflects separate subscription pools (decision 0049). |
| 97 | Allowed worker providers and default rule permitting subscriptions while excluding pay-per-use APIs from loose picks | `internal/protocol/worker_providers.go:15-63`; `internal/gateway/scores.go:130-183`; `internal/gateway/workers.go:588-593` | 13 subscription providers + live quota windows allowed; pay-per-use excluded from loose match and flagged when explicit | rule | network | none | `providers.worker_allowlist` | [quotas and providers] Protects API spend across the network. |
| 98 | Provider preference order when a loose model name matches several providers | `internal/daemon/join.go:40-43` | unset in code; per-machine node.json model_providers list | rule | network | none | `providers.loose_match_order` | [quotas and providers] Today hand-edited per machine in node.json; belongs at network in recipes. |
| 99 | Refuse starting a worker when the target provider's quota window is exhausted | `internal/protocol/quota.go:291-301`; `internal/gateway/workers.go:600-605` | refuse at 100% / exhausted with reset timestamp | rule | network | none | `quotas.refuse_when_exhausted` | [quotas and providers] Enforced on both automatic and explicit starts. |
| 100 | High-quota warning threshold on explicit model starts and display summary order | `internal/protocol/quota.go:17,275-287,341` | warn at >=95% used; fixed 14-provider display rank | rule | network | none | `quotas.high_warning_percent` | [quotas and providers] Warns without blocking between 95% and 100% on explicit picks. |
| 101 | Subscription quota polling interval and quota-axi integration switch | `internal/daemon/quota.go:36`; `internal/daemon/daemon.go:177-178`; `internal/protocol/integrations.go:15,36` | poll every 5m when quota_axi integration is on | rule | network | none | `quotas.poll_interval_minutes` | [quotas and providers] Shared quota telemetry cadence. |
| 102 | Set of operator actions reserved to the human owner rather than agent sessions | `internal/daemon/sessiontext.go:35-57`; `internal/protocol/operator.go:118-142` | fixed ~85 operator methods (graph settings, trust, policy, Inbox, scores, tools, apps) | rule | network | agent | `approvals.owner_only_actions` | [approvals and Inbox] Lower levels may only tighten (add owner-only actions, never grant owner-only methods to agents; modularity.md:124). |
| 103 | Require every worker start to originate from a live agent or owner session (no tracker intake) | `internal/gateway/workers.go:323-332`; `internal/daemon/operator.go:311-319` | session-only intake (CTO start requires online owner session) | rule | team | repo | `intake.allowed_sources` | [approvals and Inbox] modularity.md:129 places intake (owner, Ideas, tracker) at team, overridable at repo; lower levels may only tighten allowed sources. |
| 104 | Forbid workers from starting nested workers | `internal/gateway/workers.go:333-335`; `internal/mcpserver/roles.go:15-20`; `internal/toolcatalog/tools.go:25` | max nesting depth = 0 (workers cannot start workers) | rule | network | agent | `approvals.worker_max_nesting_depth` | [approvals and Inbox] Lower levels may only tighten toward 0; today's network default 0 is already minimal. |
| 105 | Restrict worker stop, resume, rating, and late-defect reporting to the calling session or CLI owner | `internal/gateway/workers.go:1141-1143,1218-1220`; `internal/gateway/ratings.go:199,250-258` | stop/defect: caller or owner; resume/rate: caller only | rule | network | team | `approvals.worker_control_principals` | [approvals and Inbox] Lower levels may only tighten who may control runs; Conductor needs tracker/policy principals when no caller session exists (Section 3). |
| 106 | Cross-node request delivery rule (normal, auto, ask, never) and session peer-trust wildcard (* -> project auto) | `internal/gateway/graphread.go:105-127`; `internal/gateway/store.go:1495-1519,2058-2074`; `internal/gateway/held.go:33-109` | default normal; ask holds in Inbox; never rejects; CTO skips hold; peer trust sets * -> auto | rule | network | repo | `approvals.edge_request_rule` | [approvals and Inbox] Lower levels may only tighten request gates (auto -> normal -> ask -> never). |
| 107 | Require reason on reject/redirect/escalate outcomes, recommendation on escalate, and auto-create Inbox items | `internal/protocol/outcome.go:39-42,56-58`; `internal/gateway/store.go:2107-2122` | reason required; escalate requires recommendation; reject/escalate open Inbox row | rule | network | agent | `inbox.outcome_escalation_rules` | [approvals and Inbox] Lower levels may only tighten (escalate more cases to Inbox, never drop required fields; modularity.md:61,128). |
| 108 | Ideas backlog items sent to Inbox via agent_owner_ask kind idea | `internal/mcpserver/ops.go:70-71`; `internal/gateway/store_inbox.go:28` | enabled (stores feature_idea in owner Inbox) | rule | team | repo | `inbox.ideas_backlog_enabled` | [approvals and Inbox] Optional team backlog module in modularity.md:130; lower levels may only tighten (turn off idea submissions). |
| 109 | Inbox item kinds, agent_owner_ask option count limit, and 200-char waiting summary cap | `internal/gateway/store_inbox.go:27-31`; `internal/mcpserver/ops.go:65-73`; `internal/protocol/waiting.go:15-22,96` | 6 Inbox kinds; <=4 answer options; <=200-char one-line waiting text | rule | network | team | `inbox.allowed_kinds_and_limits` | [approvals and Inbox] Lower levels may only tighten (fewer allowed kinds, fewer options, or shorter summary). |
| 110 | Bind each written owner approval note to one exact action string and timestamp | `internal/protocol/maturity.go:324-326`; `internal/gateway/store_inbox.go:18-20` | "<owner> approved: <action>, <RFC3339>" (one action only) | rule | network | none | `approvals.single_action_scope` | [approvals and Inbox] Prevents reusing a prior approval on a new commit or target. |
| 111 | Idle check delay and daily per-session cap for sessions with open work | `internal/gateway/check.go:19-21,63-74` | after 20m idle, max 6 checks/session/day | rule | network | agent | `inbox.idle_check_schedule` | [approvals and Inbox] Lower levels may only tighten daily cap (fewer checks/day). Counts owned workers via worker.owner (Section 3). |
| 112 | Daily cap and progress guard on automatic wake messages when autonomy is wake | `internal/gateway/check.go:24,38-43,93-112` | max 6 wakes/session/day; stops on no-progress streak | rule | network | agent | `inbox.autonomy_wake_daily_cap` | [approvals and Inbox] Lower levels may only tighten (lower wake cap, down to 0). |
| 113 | Grace period and max request age for sending one unanswered-request reminder to an idle peer | `internal/gateway/remind.go:11,20-34` | after 2m recipient idle, request <24h old, once (suppressed on ask/never) | rule | network | team | `inbox.request_reminder_grace_minutes` | [approvals and Inbox] Lower levels may only tighten (longer grace or reminders off). |
| 114 | Conversation hop limit, message budget per conversation, per-sender rate limit, and message TTL | `internal/protocol/protocol.go:45-50`; `internal/gateway/store.go:1932-1951,2004-2012,2075-2083` | 8 hops, 50 msgs/conversation, 20 msgs/min/session, 24h TTL | rule | network | team | `approvals.message_loop_and_rate_limits` | [approvals and Inbox] Lower levels may only tighten (smaller hop/message/rate/TTL caps). |
| 115 | Gateway retention periods for messages, audit events, offline sessions, and worker runs | `internal/gateway/store_record.go:378-385,401-404`; `cmd/agent-net/main.go:245-248` | messages forever (0), events 90d, offline sessions 30d, worker runs forever (0) | rule | network | none | `network.retention_periods` | [approvals and Inbox] Configured by gateway CLI flags today. |
| 116 | Desktop notification toggle and per-session cooldown when an agent waits on the owner | `internal/daemon/notify.go:37,46-53,85` | enabled by default; at most 1 notification per 5m (300s) per session | rule | network | none | `inbox.notification_cooldown_seconds` | [approvals and Inbox] Stored per daemon in owner_preferences today. |
| 117 | Network harness integrations (Claude inbound peer messages, Codex tool auto-approval, Claude hooks) | `internal/protocol/integrations.go:9-14,28-49`; `internal/setup/peermsgs.go:19-26` | owner setup answers synced network-wide (machine flag can override locally) | rule | network | none | `approvals.harness_integrations` | [approvals and Inbox] Controls crossSessionInbound=accept and Codex MCP auto-approval. |
| 118 | Automatic daily harness self-update toggle, schedule, busy-skip retry, and command timeout | `internal/protocol/integrations.go:16,51`; `internal/daemon/harness_update.go:34-36,56-59,152` | on by default; 10m initial delay, 24h interval, 30m busy retry, 5m timeout | rule | network | none | `network.harness_auto_update` | [approvals and Inbox] Machine capability maintenance (modularity.md:82-85). |
| 119 | Connected phone app cookie lifetime, renewal window, and OAuth pending-approval expiry | `internal/gateway/phone_auth.go:17-18,45-50`; `internal/gateway/oauth_store.go:19` | phone token 90d (renew <30d left); OAuth pending 10m | rule | network | none | `approvals.connected_app_token_ttl` | [approvals and Inbox] Controls re-authentication frequency for remote owner surfaces. |
| 120 | Standing per-skill choice of whether newly discovered repos get the skill enabled | `internal/gateway/store.go:556`; `docs/decisions/0048-skills-on-off-per-project.md:38-50` | per-skill new_projects flag (0 or 1) | rule | network | repo | `skills.enabled_by_default` | [skills] modularity.md:135 puts skills on/off at network default, overridable at team, repo; uninstalled at network stays a hard ceiling (tighten only against banned/uninstalled items). |
| 121 | Global user-dir placement when a skill is on everywhere vs repo-dir projection when selective | `internal/gateway/tools_plan.go:119-125,205-208`; `internal/daemon/tools_plan.go:64-99` | global -> ~/.claude/skills, ~/.agents/skills, ~/.gemini/config/skills; selective -> repo dirs | rule | repo | none | `skills.placement_strategy` | [skills] Antigravity has no repo-level projection target in tools_plan, only ~/.gemini/config/skills. |
| 122 | Per-harness project skill folders and foreign-folder shadow conflict check | `internal/daemon/tools_project.go:146-175`; `internal/daemon/tools_plan.go:64-108`; `internal/daemon/tools_tracked.go:11`; `internal/inventory/project_roots.go:4-33` | project to .claude/skills + .agents/skills; block if shadowed in .codex/.opencode/.pi/.agent | rule | repo | none | `skills.project_roots` | [skills] Enforces non-conflicting skill paths across harnesses in a checkout. |
| 123 | Never overwrite or delete git-tracked project skills during tool plans | `internal/daemon/tools_tracked.go:16-56`; `internal/gateway/tools_plan.go:134` | git-tracked skills preserved | rule | repo | none | `skills.preserve_git_tracked` | [skills] Protects skills committed inside a repository. |
| 124 | Copy only agent-net-owned or git-ignored skills from .claude/skills and .agents/skills into worker worktrees | `internal/daemon/worktree_skills.go:13-60` | copy owned or git-ignored in .claude/skills and .agents/skills | rule | repo | run | `skills.worktree_copy_policy` | [skills] Lower levels may only tighten (copy fewer or no untracked skills into a run). |
| 125 | Retention count and max age for local skill and native-MCP config backups | `internal/daemon/tools_backups.go:16-18,36-45` | keep newest 20 per item, prune >30d (keep unfinished journal refs) | rule | network | none | `skills.backup_retention` | [skills] Bounds disk use under daemon state dir. |
| 126 | Shared MCP server attachment scope and default per-call timeout | `internal/protocol/mcp.go:13,68-72,85-90` | scope global (default), projects, or uninstalled; default call timeout 60s | rule | network | agent | `skills.mcp_server_attachment` | [skills] Registry and enabled ceiling live at network; lower levels (team, repo, agent; modularity.md:126) may only tighten which enabled servers/tools attach or shorten timeout. |
| 127 | Test rigor required before merging a change in a repository (tests_before_merge) | `internal/protocol/maturity.go:23-27,106-116,198-208` | suite (growing/unset); optional, suite, suite-new-tests, suite-new-tests-review | advice | repo | run | `delivery.tests_before_merge` | [delivery] Prompt text today (TestText); modularity.md:22,172 marks required CI/tests as a target rule. |
| 128 | Merge authority and gate policy for landing changes on the default branch | `internal/protocol/maturity.go:28-31,106-116,212-225`; `internal/gateway/graphread.go:130-145` | full (early/growing/unset), delegated (mature), ask (critical) | advice | repo | agent | `delivery.merge_gate` | [delivery] Prompt and trust-note text today; modularity.md:22,174,186 makes delivery.merge_gate (CI + review policy vs lead vs owner) an enforced rule. |
| 129 | Merge policy when a repository has no node in the component graph | `internal/protocol/maturity.go:285-287`; `internal/gateway/graphread.go:144` | MergeApprovalFor says ask owner; MergeAuthorityForSession falls back to full | advice | network | repo | `delivery.uncharted_merge_policy` | [delivery] Drift in current code: graph helper says ask owner while message delivery fallback resolves to growing -> full. |
| 130 | Which governance edge modes qualify an architect to approve merges under delegated authority | `internal/protocol/maturity.go:291-304` | governs edge in mode owner or arbiter (not advisor) | advice | team | repo | `delivery.delegated_approver_modes` | [delivery] Cross-repo governance choice; today shapes approval text rather than a git hook. |
| 131 | Caller review rule requiring a lead session to read the diff, run tests in the worktree, and merge only if asked | `internal/guidance/guidance.md:18`; `internal/protocol/workers.go:845-852` | read diff, run project tests, report check, merge only if user asked | advice | repo | agent | `delivery.caller_review_and_merge` | [delivery] Assumes a human-prompted lead session reviews every worker branch; modularity.md:173-174 splits review rule from merge gate. |
| 132 | Deploy approval policy (deploy_rule) and rule forbidding worker deploys unless brief and charter allow | `internal/protocol/maturity.go:37-40,106-116,228-236`; `internal/protocol/graph_context.go:158-174` | brief (early/growing/unset), ask (mature), owner (critical); workers blocked unless brief+rule allow | advice | repo | run | `delivery.deploy_rule` | [delivery] Injected into RulesText and WorkerDeployApprovalRules. |
| 133 | Git branch prefix created for worker worktrees | `internal/protocol/workers.go:724` | agent-net/<worktree-id> | rule | repo | none | `delivery.worker_branch_prefix` | [delivery] Enforced by daemon when creating a worker worktree. |
| 134 | Worker branch preparation instruction (commit if task asks; no automatic rebase on main before hand-back) | `internal/protocol/workers.go:584-587` | commit in worktree if task asks; no rebase rule | advice | repo | run | `delivery.worker_branch_prep` | [delivery] modularity.md:171 highlights worker rebasing on main before hand-back as an alternative repo recipe setting. |
| 135 | Auto-commit uncommitted worker changes on exit and append Agent-Net-* git commit trailers | `internal/daemon/worker_notice.go:259-301`; `internal/daemon/worker_commit.go:19-23,119-155,201-220`; `internal/protocol/workers.go:822-824` | save dirty tree on no-handback exit; trailers Agent-Net-Worker/Model/Harness/Difficulty/Caller | rule | repo | none | `delivery.worker_commit_and_trailers` | [delivery] Preserves partial work and provenance on worker branches. |
| 136 | Per-repository worker rules file path, 8 KiB cap, and boilerplate heading skip list | `internal/daemon/workers.go:498-508`; `internal/protocol/workers.go:588-590,636-645` | .agent-net/worker.md (<=8192B); skip Project rules / Read AGENTS.md in title | advice | repo | run | `delivery.repo_worker_rules` | [delivery] Already per-repo data on disk; byte cap is enforced by daemon. |
| 137 | Worker worktree cleanup interval and rule deleting only clean worktrees merged into the default branch | `internal/daemon/cleanup.go:19-20,82-215` | first check 1m, then every 30m; remove only when clean and ancestor of default branch | rule | repo | none | `delivery.worktree_cleanup_policy` | [delivery] Protects unmerged branches; squash-merged PR workflows need a different merge check. |
| 138 | Authority framing text appended to untrusted peer requests and worker messages | `internal/protocol/trust.go:23-29`; `internal/daemon/claude/claude.go:277-290` | PeerCheckNote + NoAuthorityNote | advice | network | team | `notices.untrusted_peer_framing` | [notices] Marker neutralization is a fixed security check; the policy text inside the note is recipe advice. |
| 139 | Authority framing text appended when peer trust or owner-approved relay applies | `internal/protocol/trust.go:13,43-54`; `internal/daemon/claude/claude.go:281-283,291-293` | OwnerApprovalNote + receiver merge rule | advice | network | repo | `notices.trusted_request_framing` | [notices] Combines trusted-peer free actions with the target repo's merge rule. |
| 140 | Attach a 200-char excerpt of the triggering Claude owner prompt to cross-project requests | `internal/protocol/trust.go:84-90`; `internal/gateway/store.go:1897`; `internal/daemon/claude/claude.go:299-301` | 200-char owner prompt + charter/merge framing | advice | network | team | `notices.owner_prompt_excerpt` | [notices] Shares owner intent across nodes when the prompt named the target. |
| 141 | Owner overrule notice text appended when the owner overrides a rejection or escalation | `internal/protocol/outcome.go:87`; `internal/daemon/claude/claude.go:285-287` | "the owner overrules: do it." | advice | network | none | `notices.owner_overrule_text` | [notices] Tells the session to execute the previously refused request. |
| 142 | No-reply-needed framing appended to delivered peer replies | `internal/daemon/claude/claude.go:274-275` | "No answer is needed. Send a new message only if you have a new question or the work needs it." | advice | network | agent | `notices.reply_no_ack_text` | [notices] Dampens polite thank-you ping-pong between agents. |
| 143 | Automatic reply messages sent when a cross-node request is refused by rule, declined, or expires while held | `internal/gateway/store.go:2072,2743`; `internal/gateway/held.go:106` | refused by your user's rule / declined / expired while awaiting your user | advice | network | none | `notices.held_request_replies` | [notices] Explains Inbox/edge outcomes to the waiting sender session. |
| 144 | Worker start result wording, queued-start 'do not redo' instruction, and owner_line prompt | `internal/protocol/workers.go:168-185`; `internal/protocol/worker_assessment.go:49-64,79-88` | do not redo queued task; "Tell your user: worker on <model>, difficulty <level>..." | advice | network | agent | `notices.worker_start_text` | [notices] Assumes a human-facing caller session relays owner_line. |
| 145 | Worker completion notice layout, untrusted-report banner, checklist, and agent_learn prompt | `internal/protocol/workers.go:782-856` | facts + ReportMarker + ChecklistMarker + agent_learn prompt | advice | network | repo | `notices.worker_completion_format` | [notices] Designed for delivery to a calling agent session rather than a tracker item. |
| 146 | Rating request line injected into sampled worker completion notices | `internal/protocol/rate.go:124-140`; `internal/protocol/workers.go:831-833` | "rate worker <id> ... with agent_worker_rate: 1 to 5 for the result, and one line on why" | advice | network | agent | `notices.worker_rate_prompt_text` | [notices] Prompts the calling session to supply quality data. |
| 147 | Mid-run idle worker notice delay and harness set | `internal/daemon/workers.go:96`; `internal/daemon/worker_idle_notice.go:57-95` | after 2m idle on opencode, codex, agy | rule | network | run | `notices.worker_idle_minutes` | [notices] Lower levels may only tighten (shorter idle threshold before alerting caller). |
| 148 | Idle-check question wording and autonomy wake continuation wording | `internal/gateway/check.go:22,25,110-112` | [agent-net] check: is your task done? ... / [agent-net] continue: you said you have work left ... | advice | network | agent | `notices.idle_check_and_wake_text` | [notices] Requires agent_announce protocol answers (done, more, blocked). |
| 149 | Post-error peer hint cap and per-session cooldowns | `internal/protocol/hint.go:18`; `internal/daemon/hint.go:20-23`; `internal/hint/hint.go:376-378` | max 2 peer hints; 30m same-peer repeat, 45s any-hint gap | rule | network | agent | `notices.error_hint_limits` | [notices] Lower levels may only tighten (fewer hints, down to 0, or longer cooldowns). |

### Security checks and protective limits (stay at network)

Mechanisms, security checks, and payload bounds that protect the system are not workflow opinions. They stay fixed rules at `network` level (`override: none`).

| Check or protective limit | Where (file:line, verified on current code) | Enforced rule at network | Why it is not an opinion |
|---|---|---|---|
| Owner CLI/TUI caller gate and parent-PID walk | `internal/daemon/sessiontext.go:110-120,137-180` | Require client == "cli" and caller PID outside every local session, worker, Codex server, and CTO process tree (64-parent walk). | Prevents agent sessions from invoking owner-only methods through shell tools. |
| Node enrollment, pinned-CA TLS 1.2+, and one-use invite codes | `docs/security.md:5-6,187-198`; `internal/gateway/server.go:88-90` | 10m one-use invite code hash, pinned server key on install, ECDSA P-256 CA with TLS >=1.2, per-start local admin token. | Authenticates machines and protects transport across the network. |
| Removed-machine session triggers in SQLite | `internal/gateway/store.go:562-565` | SQLite triggers abort session insert or online update for any machine with removed = 1. | Stops a revoked node from re-registering or reviving sessions. |
| Neutralize forged trust and cross-session markers in untrusted text | `internal/protocol/trust.go:62-64`; `internal/gateway/check.go:111` | NeutralizeMarkers rewrites [agent-net and <cross-session inside message bodies and wake strings. | Prevents prompt injection from spoofing agent-net authority framing. |
| Server-computed message sender, reply-parent, and authority flags | `internal/gateway/store.go:1966-1974,1993-2000,2022-2024`; `internal/protocol/protocol.go:77-90` | Reserve sender "agent-net", forbid self-send, verify registered CTO session for FromCTO/OwnerApproved, enforce parent recipient/sender on replies, overwrite RecipientTrust/Relation on delivery. | Guarantees message provenance and prevents senders from forging trust flags. |
| CTO built-in tool denial and process-tree channel binding | `internal/daemon/cto_tool_limits.go:16-22`; `docs/security.md:56-62,143-146` | Strip shell, file, search, web, and subagent tools from CTO runtimes; verify CTO process tree on every private MCP call. | Keeps the CTO confined to audited MCP reads, talk, and proposals. |
| CTO and agent read-scope parity and hidden graph/session fields | `docs/security.md:89-134` | Hide Inbox items, waiting-question text, unrelated message bodies, machine paths/locations, learned-evidence excerpts, and unknown node settings from agents and CTO. | Protects private owner data and local filesystem layout. |
| Charter secret/IP/path warning detectors and strict enum validation on node settings | `internal/protocol/charter_guide.go:75-84,123-160`; `internal/protocol/maturity.go:122-127,168-195` | Flag tokens, IPs, and paths in charters; drop unknown or invalid stored node setting values instead of widening authority. | Prevents public charters from leaking secrets and corrupt DB rows from loosening rules. |
| MCP header secret indirection and skill path-traversal / foreign-checkout guards | `internal/protocol/mcp.go:39-46`; `internal/daemon/tools_project.go:137-175`; `internal/daemon/tools_plan.go:56-62` | Store only env var names or file paths for MCP headers; reject skill writes to $HOME, symlinks, path traversal, or foreign checkouts. | Prevents secret storage in gateway DB and arbitrary filesystem writes from tool plans. |
| Secret redaction on relayed session transcripts and logs | `internal/daemon/sessiontext.go:15,29-32` | Mask secrets and cut transcripts on the holding machine before relaying through the gateway without storage. | Prevents leaking credentials from live session transcripts. |
| Herdr remote-clipboard and terminal handover invariants | `AGENTS.md:9-16`; `internal/daemon/enter.go:18-38` | Always use herdr --remote when local herdr exists; never open hidden panes outside herdr; workers never open views. | Foundation entry mechanism required for clipboard paste and terminal safety (decision 0046). |
| Protective payload, buffer, and protocol bounds | `internal/protocol/protocol.go:47`; `internal/protocol/graph.go:10`; `internal/protocol/outcome.go:17`; `internal/protocol/track.go:202-205`; `internal/protocol/mcp.go:14-23`; `internal/hint/hint.go:21`; `internal/protocol/workers.go:598,619`; `internal/protocol/delegation.go:66`; `internal/protocol/trust.go:67` | MaxBodyBytes 64 KiB, charter attr 2 KiB, outcome 2 KiB, playbook 1/2 KiB, MCP args 256 KiB / result 512 KiB / max timeout 10m, hint input 8 KiB, summary 120B, title 60B, model 100B, expertise 200B. | Protects memory, SQLite rows, and socket frames rather than expressing workflow style. |

### Summary

Total built-in opinions: **149** (plus **12** fixed network security checks and protective limits listed separately above).

**Counts by area:**

| Area | Rows | Count |
|---|---|---|
| guidance | 1-24 | 24 |
| CTO | 25-40 | 16 |
| charter and maturity | 41-50 | 10 |
| workers and picker | 51-83 | 33 |
| judge | 84-93 | 10 |
| quotas and providers | 94-101 | 8 |
| approvals and Inbox | 102-119 | 18 |
| skills | 120-126 | 7 |
| delivery | 127-137 | 11 |
| notices | 138-149 | 12 |
| **Total** | **1-149** | **149** |

**Counts by kind:**

| Kind | Count |
|---|---|
| advice | 54 |
| rule | 95 |
| **Total** | **149** |

**Counts by home level:**

| Home level | Count |
|---|---|
| network | 120 |
| team | 3 |
| repo | 17 |
| agent | 8 |
| run | 1 |
| **Total** | **149** |

**Top 10 opinions that matter most for Conductor (tracker-driven intake, no long-running orchestrator, merge by policy and evidence):**

1. **#103 `intake.allowed_sources`** - Requires every worker run to originate from a live agent or owner session. Conductor starts runs directly from tracker items without an open session.
2. **#16 `guidance.start_workers_when_asked`** - Tells sessions to start workers only when a human asks. Tracker-driven intake starts runs automatically from queued items.
3. **#55 `workers.caller_gone_grace_minutes`** - Stops a worker 10 minutes after its calling session goes offline. Conductor has no long-running orchestrator session holding the run open.
4. **#105 `approvals.worker_control_principals`** - Ties stop, resume, and ratings to a calling session (`worker.owner`) or CLI owner. Tracker-driven runs need item/policy ownership instead of a parent session.
5. **#128 `delivery.merge_gate`** - Treats merge authority (`full`, `delegated`, `ask`) as prompt text for a human or governing architect. Conductor merges by enforced policy when CI and review evidence pass.
6. **#131 `delivery.caller_review_and_merge`** - Tells a parent lead session to read the worktree diff, run tests, and merge only if the user asked. Conductor has no lead session in the loop and needs automated review/merge rules.
7. **#127 `delivery.tests_before_merge`** - States test expectations as advice (`run the suite`) rather than an enforced check. Policy-driven merge needs machine-verified test and CI status.
8. **#134 `delivery.worker_branch_prep`** - Commits on `agent-net/<id>` only if asked and leaves rebasing/integration to a caller. Headless tracker runs need a recipe rule for commit, rebase on main, push, and PR creation.
9. **#145 `notices.worker_completion_format`** - Formats hand-back reports, checklists, and `agent_learn` prompts as messages to a calling agent session. Conductor runs report status, evidence, and blockers back to the tracker item.
10. **#94 `providers.protected_list`** - Reserves protected subscriptions (and `providers.protected_reason_text` "saved for your orchestrator") for a live orchestrator session. Without an orchestrator, all allowed pools can serve runs directly.

## 3. Where code assumes "work starts from a session"

A work-item model would own runs. Today a run (worker) is owned by a calling session, called `owner`/`caller` in different files. Each item below would change.

### 3.1 Start

- `daemon/operator.go:199-233` - the `worker_start` operator call fills the caller from the calling session id `sid`.
- `daemon/operator.go:311-318` - `workerStartCall(ctx, caller, ...)` builds `WorkerStartFrame{Owner: caller, CallerProject, CallerPath, FromCTO}`.
- `mcpserver/workers_v2.go:249-261` - `filledWorker` fills `caller` from the calling session.
- `protocol/workers.go:258-269` - start frame fields `Owner`, `CallerProject`, `CallerPath`.
- `gateway/workers.go:323-335` - the caller must be a session of the sending node and not a worker; a CTO start needs an owner pseudo-session ("CTO worker start requires an owner session").
- `gateway/workers.go:456-520` - project is resolved from the caller's folder when `CallerProject` is set (not read in full; fields at `protocol/workers.go:261-269`).
- `protocol/workers.go:206-208` `WorkerInfo.Owner`; `:329-332` launch has `Owner` and `OwnerName`; `:385`, `:397`, `:416-420` kill/continue frames carry `Owner`/`Requester`/`AsOwner`.
- `gateway/workers.go:639` - launch built with `Owner: p.Owner, OwnerName: g.store.displayName(p.Owner)`.
- `daemon/worker_commit.go:141,209` - commit trailers name the caller session (and its display name).
- `gateway/workers.go:122` and `gateway/calibration.go:131-135` - `WorkerSessionOwner` becomes `WorkerRun.Caller` (`gateway/workers.go:~133`), the key for ratings and calibration.

### 3.2 Notices go to the caller

- `daemon/worker_notice.go:478-482` - after sending a completion notice, `noticeSinceRequest` is set only when `to == "" || to == w.owner`.
- `daemon/worker_notice.go:492-499` (`sendWorkerNoticeBody`) - recipient defaults to `w.owner`; parent message is the task message.
- `daemon/worker_notice.go:549-554` (`handbackTo`) - a hand-back can go "to another session than the owner" (a hook already exists; still session-addressed).
- `daemon/worker_notice.go:640` (`noteAsk`) - only messages from `w.owner` count as follow-ups; rework counters (`continueCount`, `fixCount`) are defined by the caller's messages (`daemon/workers.go:~150-156`).
- `daemon/worker_notice.go:712,821` - same `m.From == w.owner` test.
- `daemon/daemon.go:1068,1118` - Pi first task recognized by `m.From == targetWorker.owner`.
- `daemon/waiting.go:76` - a worker's wait ("Needs you") is routed to its owner.
- `protocol/workers.go:497-505` `WorkerPreamble` says "Another agent started you ... That agent will check your work and relay the result to its user."; `:518-526` same for Codex.
- `protocol/workers.go:841-850` - the checklist is written to the caller: "Before you tell your user ... Merge only if your user asked."
- `protocol/worker_assessment.go:42-57` - `WorkerOwnerLine` is "Tell your user: ..." for the caller to pass on.
- `protocol/rate.go:124-140` - the rate ask is a line in the caller's notice.

### 3.3 Hand-back goes to the caller

- `daemon/worker_notice.go:536-546` (`handback`) - the `agent_handback` call finds the worker by session and calls `handbackTo(..., "", "")`, which means "to the owner".
- `daemon/worker_codex.go:293-298` - Codex job `from: w.owner`, `msgID: w.taskMsg`; the final message of a turn is the hand-back to `w.owner`.
- `daemon/worker_codex.go:485` - same pair read again for the report.
- `daemon/worker_auto_continue.go:77-90` - for Codex the auto continue is a `codexJob{from: owner, msgID: task}` so the report goes to the caller "as a reply to the task".
- `daemon/worker_idle_notice.go` - idle notice goes to the caller (not verified in full).
- `toolcatalog/tools.go:28` - `agent_handback`: "This is how the caller gets your result."
- `guidance.md:16-20` - guidance text describes the caller flow.

### 3.4 Lifetime tied to the caller session

- `daemon/workers.go:1269-1325` - each tick checks `ownerOffline(owner)`; after `OwnerGrace` it ends the worker with `StopOwnerGone`.
- `daemon/workers.go:136,1317-1324` - fields `ownerGoneSince`, `ownerChecked`.
- `daemon/queue.go:117` - a queued start is dropped when its owner is gone for `OwnerGrace` (`qGone`).
- `protocol/workers.go:18,87`, `daemon/join.go:64,122`, `daemon/workers.go:53-55,101-103` - `OwnerGrace` and its name in `node.json` (`owner_grace`).
- `daemon/workers.go:1447` - end facts for `owner_gone`.
- `daemon/workers.go:1058` (`stopWorkerSession`) - the requester must be the owner (or the owner pseudo-session).

### 3.5 Continue, resume, stop, rate

- `gateway/workers.go:852,861` - continuing a worker requires the same owner.
- `gateway/workers.go:1032-1055` - the machine holding the worker asks the owner's machine to send the task; "the machine of the owner is offline" blocks it.
- `gateway/workers.go:1078-1080` - stop: "only the worker's owner or the person at the command line can stop it".
- `gateway/workers.go:1153-1156,1182` - resume by message needs `a.Worker.Owner == p.Sender`.
- `gateway/ratings.go:199,250` - rating and defect reports accepted only from the caller (or owner).
- `toolcatalog/tools.go:27` - "Only the session that started the worker may rate it."
- `protocol/calibration.go:~60`, `gateway/calibration.go:58-87` - calibration samples are keyed by `Caller`: it learns per calling session how its difficulty labels perform.

### 3.6 Other places that count workers by session

- `gateway/check.go:129-133` - `owningWorkerCount` uses `json_extract(worker,'$.owner')=?` to decide whether an idle session still "has work" (feeds the idle check and autonomy wake).
- `protocol/operator.go:222-224` - `AgentView.WorkerOwner`; `:442-444` `WorkerView.Owner`/`OwnerName`; `:526` fills it. The TUI Sessions tab and `agent-net workers --owner-claude` (`protocol/operator.go:421-423`) are built on it.
- `daemon/local.go:241` and `:733` - worker start is handled on the local connection of a session.
- `plugins/claude/agent-net-workers` - shows "the session's workers" (caller view).

### 3.7 What is already session-free

- A CTO start already uses the owner pseudo-session as caller (`daemon/cto_tools.go:391` `workerStartCall(ctx, sid, p, false, "", true)`, `gateway/workers.go:327`). So "caller is not a normal session" has one precedent.
- `taskID` already exists on a worker (`daemon/workers.go:~161`, `WorkerRun.TaskID`, `gateway/workers.go:~137`). It groups runs of one task for rework counting. It is the closest thing to a work item id today.
- Worker facts (`gateway/store.go` worker runs table, `--keep-worker-runs`) are not tied to a live session.
- Merge is never done by agent-net. "Merge authority" is text only (`protocol/maturity.go:212-214`) and `MergeApprovalFor` (`:276-300`) only produces a sentence. No code merges, so an evidence-gated auto-merge has no hook yet.

## 4. Existing switches

Where each one is stored and where it is read. "Scope" says network, machine, project, node (graph node), session.

| Switch | Scope | Stored | Read |
|---|---|---|---|
| `claude_peer_messages` | network, machine override | gateway `owner_settings` key `integrations` (`gateway/integrations.go:19`); machine file `integrations-local.json` (`integrations/integrations.go:~LocalPath`); applied record `integrations-applied.json` | `daemon/integrations.go:16,54-105` poll every minute; `setup.ApplyIntegrations` |
| `codex_tools` | same | same | same |
| `claude_hint_hook` | same | same | same; `daemon/hint.go` runs only if hook installed |
| `claude_policy_hook` | same | same; plus `claude-policy-hook.json` (`daemon/policy.go:90-96`) | `daemon/policy.go` |
| `claude_needs_hook` | same | same | `daemon/waiting.go` fed by hook |
| `claude_workers_mod` | same | same | plugin install in `setup` |
| `quota_axi` | same | same; `node.json` `quota_axi` path (`daemon/join.go:45-47`) | `daemon/quota.go`; `quotaaxi` |
| `harness_updates` | network (stored as `harness_updates_off`) | integrations answer (`protocol/integrations.go:51`) | `daemon/harness_update.go:30`; `daemon/integrations.go:64` |
| Setup flags per item (`--claude-hint-hook`, `--no-quota-axi`, ...) | machine | `integrations-local.json` | `integrations.Effective` |
| `no_launch` | machine | `node.json` (`daemon/join.go:33`) | `daemon/workers.go:526`, `daemon/operator.go:38`, gateway rejects in `gateway/workers.go:~457` |
| `enter_ssh`, `enter_ssh_hosts` | machine | `node.json` (`daemon/join.go:35-39`) | `daemon/enter.go` |
| `model_providers` | machine | `node.json` | `daemon.Config.ModelProviders` |
| `workers.*` limits | machine | `node.json` `workers` | `daemon/join.go:120-130`, `daemon/workers.go:84-125` |
| `hostname` | machine | `node.json` | `daemon.Hostname` |
| `quality_weighting` | network | `owner_settings` (`gateway/quality_settings.go:12`) | `gateway/quality.go:371` |
| `model_scores` | network | `owner_settings` key `model_scores` (`gateway/scores.go:14`) | picker |
| `difficulty_minimums` | network | `owner_settings` (`gateway/scores.go:81`) | picker, judge |
| `worker_providers` | network | `owner_settings` (`gateway/scores.go:130`) | picker |
| `protected_providers` | network | `owner_settings` (`gateway/scores.go:185`) | `gateway/workers.go:462`, `protected_chooser.go` |
| `worker_profiles` | network | `owner_settings` (`gateway/worker_profiles.go:14`) | picker |
| `worker_rotation` | network (state, not a switch) | `owner_settings` (`gateway/worker_rotation.go:12`) | picker |
| `cto_settings`, `cto_title`, `cto_session` | network | `owner_settings` (`gateway/cto_settings.go:14-15`, `cto_session.go`) | CTO runtime; also message routing (`gateway/store.go:1884,1918`, `gateway/remind.go:24`) |
| `hidden_items` | network (view) | `owner_settings` (`gateway/hidden.go:17`) | TUI |
| `onboarding` | network (view) | `owner_settings` | TUI |
| `public` (connected apps listener) | network | `owner_settings` (`gateway/apps_public.go:45-50`) | `gateway/apps_public.go` |
| `notifications_disabled` | machine | daemon DB `owner_preferences` (`daemon/state.go:41`) | `daemon/notify.go:46-53` |
| Delegation policy | session or project | gateway (policy store `gateway/store_policy.go`) | `daemon/policy.go`, adapters |
| Peer trust | session (written as `*` edge for the project) | `graph_edges` | `gateway/graphread.go:105-130` |
| Edge rule `auto/ask/never` | edge | `graph_edges.rule` | `gateway/store.go:1944-1962`, `gateway/remind.go:21-31` |
| Node settings (`tests_before_merge`, `merge_authority`, `worker_sandbox`, `judge_consumers`, `deploy_rule`, `autonomy`) | node | `graph_node_settings` | `protocol/maturity.go`, `gateway/store_policy.go:83-114`, `gateway/check.go` |
| Maturity | node | `graph_charter` attr `maturity` | `protocol/maturity.go:102` |
| Project worker permission level | project | policy store | `gateway/workers.go:623` `WorkerPermissions` |
| Tool item on/off per project, "new projects" | item x project | `tool_project_settings` and assignments (`gateway/store.go:549`) | `gateway/tools_assignment.go` |
| MCP server enabled | server | MCP registry (`protocol/mcp.go:68`) | `daemon/mcphost.go` |
| Learned suggestions rejected | node phrase | `learned_suggestions` (`gateway/store.go:319`) | `gateway/learned_apply.go` |
| `--keep-messages`, `--keep-events`, `--keep-sessions`, `--keep-worker-runs` | gateway | process flags / unit file | `cmd/agent-net/main.go`, gateway |
| `--apps-listen`, `--apps-public-url` | gateway | process flags | gateway |
| `AGENT_NET_IGNORE` | session | env of the harness process | daemon discovery |
| `AGENT_NET_NO_AUTO_UPGRADE` | process | env | `cmd/agent-net/main.go:137,143,360`, `mcpserver/cto.go:527` |
| `AGENT_NET_NO_BAR`, `AGENT_NET_GRAPH`, `AGENT_NET_GRAPH_MOTION` | TUI process | env | `ptyproxy/bar.go:49`, `tui/run.go:23` |
| `AGENT_NET_NO_SERVICE` | setup | env | `setup/setup.go:100`, `setup/install.go:84`, `daemon/upgrade.go:153` |
| `AGENT_NET_WORKER` | worker process | env set by daemon | `mcpserver/roles.go:16`, `mcpserver/workers_v2.go:33` choose the Worker tool role |

Observations:

- There is **no feature flag table**. Each switch has its own storage and its own read path. A general `modules.<name>.enabled` would be new.
- Network-wide owner settings live as JSON blobs in one table, with one HTTP handler each (`handleDifficulty`, `handleQualitySettings`, `handleProtectedProviders`, and so on in `gateway/`). Adding a new setting means a new key, handler, protocol op, daemon relay, TUI form and CTO action. See `docs/reference.md:493-503` for the playbook ops, and `daemon/cto_classes.go:69,86` for the CTO `settings_change` area list.
- Judge model, judge harness and analyzer have config fields but no user-facing setting (only `cmd/agent-net/dev.go:74-90`).

## 5. The five biggest coupling points

### 5.1 `internal/protocol` mixes types, policy constants and policy text

Evidence:
- `protocol/workers.go` holds machine limits (`:15-55`), the worker brief sentences (`:497-526`), the notice format (`:792-838`) and the checklist (`:841-850`) in the same file as wire types.
- `protocol/maturity.go` holds the maturity table (`:102-117`) and merge/deploy sentences (`:212-262`).
- `protocol/chooser.go`, `scores.go`, `delegation.go`, `rate.go`, `calibration.go`, `learn.go`, `track.go` each carry policy numbers next to types.
- The TS adapters copy constants (`protocol/trust.go:35` comment, `protocol/maturity.go:211` "Keep the TS copies equal"), and tests compare them.
Effect: a playbook cannot replace these without touching the wire package that every part imports. Playbook values need to become inputs that the gateway or daemon passes in, not package constants.

### 5.2 The picker, the judge and the worker start are one pipeline across three processes

Evidence:
- Daemon runs the judge before the start frame (`daemon/operator.go:311-323`, `daemon/worker_assessment.go:27-66`), the gateway runs the picker with scores, minimums, quotas, protected providers and rotation (`gateway/workers.go:456-520`, `:1274-1360`), and the daemon on the chosen machine admits or queues (`daemon/admission.go`, `daemon/queue.go`).
- The picker takes quotas (Quotas module), track record (track module), quality weight (quality module) and protected providers as direct arguments (`protocol/chooser.go:98`, `gateway/workers.go:1341-1351`, `gateway/quality.go:371`).
- `DifficultyFit` and the minimums are used by the judge result, the picker, the TUI and calibration (`protocol/scores.go:175`).
Effect: turning off the judge or quotas or quality means changing the signature path through all three processes. There is no interface boundary between "choose a model" and "start a worker".

### 5.3 A worker is owned by a calling session in every layer

Evidence: section 3. In short: the field `owner` appears in the daemon worker record (`daemon/workers.go:127`), the persisted row (`:480`), every wire frame (`protocol/workers.go:206-420`), the gateway authorization checks (`gateway/workers.go:852-1182`), ratings (`gateway/ratings.go:199,250`), calibration (`gateway/calibration.go:58-87`), the idle check (`gateway/check.go:129-133`), commit trailers (`daemon/worker_commit.go:141,209`), the brief and checklist text, and the guidance block. A work-item run owner needs a new "run owner" type that can be a session, a work item or the owner pseudo-session.

### 5.4 Graph rules, owner settings and CTO authority run through message delivery and one gate

Evidence:
- Message store reads graph edges in the send transaction (`gateway/store.go:1944-1962`) and the delivery path adds recipient trust, merge authority and relation fields (`protocol/protocol.go:62-80`, `gateway/graphread.go:95-130`). So the Foundation "messages" call into the graph Module.
- The CTO class table, owner methods, proposals, and `settings_change` area list are separate hand-kept tables (`daemon/cto_classes.go:23-86`, `daemon/cto_tools.go:540-553`, `protocol/operator.go:136-138`, `daemon/sessiontext.go:41-42`, `mcpserver/cto.go:48-50`, `mcpserver/cto_v2.go:22`, `toolcatalog/tools.go`). Completeness tests keep them in step (`docs/security.md` says "a completeness test checks the MCP registry").
- Owner settings are stored under keys that other modules read directly: `cto_session` is read by `gateway/store.go:1884,1918`, `gateway/remind.go:24`, `gateway/check.go:122`, `gateway/proposal_validate.go:200`.
Effect: a module cannot register its own settings, owner methods or CTO actions. Each new module edits five or six shared tables, so a module cannot be added or removed alone.

### 5.5 Guidance, CTO brief, tool descriptions and notices repeat the same opinions

Evidence:
- "Hand a task to a worker only when I ask" appears in `guidance.md:16`, `toolcatalog/tools.go:25,29`, the CTO brief, `docs/reference.md`, and `protocol/delegation.go:282-288` (delegation text).
- "Decide, do not defer" is in `guidance.md:6` and `protocol/graph_context.go:15-16`.
- Merge and approval rules are in `guidance.md:7`, `protocol/maturity.go:212-262`, `protocol/trust.go:37-45`, `protocol/workers.go:497-505,841-850` and TS adapter copies.
- Protected provider advice is in `daemon/cto.go:30`, `gateway/protected_chooser.go:36,61`, `protocol/chooser.go:93` and `daemon/cto_classes.go:229`.
- The guidance block is written to the user's global instruction files at every daemon start and has no off switch (`agentfiles/agentfiles.go:90-93,312`), and uninstall removes it.
Effect: a tracker-driven daemon that routes work items by workflow would inherit "start workers only when the owner asks" and "tell your user" from every one of these places. Playbook text needs one source with per-place rendering, plus a way to say "no guidance block" for a machine.

## 6. Notes for the design (not decisions)

- First cut that looks cheap: move switches into one `modules` map with scope (network, machine, project). Today's integrations answer (`protocol/integrations.go`) and `owner_settings` rows are the two storage patterns to unify.
- Existing data-like playbook seeds that are already per scope: `.agent-net/worker.md` (project, `daemon/workers.go:496`), node settings and maturity (node), `difficulty_minimums` and `protected_providers` (network), model playbook notes (network, per model/harness). A playbook format could start by absorbing these.
- Constants with measured origin that a playbook should keep as defaults with a note: judge timeout 60 s (`daemon/worker_assessment.go:14-19`, 40 starts measured), continue weight 0.33 ("owner set ... on 2026-11-01", `protocol/calibration.go:20-21`).
- Things I did not read in depth: `docs/agent-guidance.md`, `gateway/tools_plan.go`, `daemon/notify.go` rate limits, `internal/phone`, `gateway/bot_tools.go` internals, `tui` beyond Settings/tabs, `quotaaxi`. Rows that rest on them are marked "not verified" or kept short.
