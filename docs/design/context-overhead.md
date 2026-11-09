# Agent-net context overhead

Measurements taken 2026-11-09 from this checkout. Tokens are estimated as UTF-8 bytes / 4, rounded to the nearest token. This is a sizing proxy, not a tokenizer or a bill. Names, paths, graph size, harness versions, settings, caching and compaction change the result. The numbers exclude user task text, agent output, unrelated instruction files and other MCP servers. They count agent-net content even where it repeats text already present elsewhere.

## Sources

| Source | Bytes | Estimated tokens | When paid and harness |
| --- | ---: | ---: | --- |
| Guidance block, `block_claude.golden` | 9,366 | 2,342 | Loaded from instruction file at session start, normally retained on each model request. Claude. |
| Guidance block, `block_other.golden` | 9,323 | 2,331 | Same. Codex, OpenCode, Pi and agy where the instruction file is read. |
| MCP server fixed instructions | 428 | 107 | Session start and retained context. Claude, Codex and agy MCP clients; Pi and OpenCode use their plugin instead. Includes the announce rule. |
| Initial charter in MCP instructions, sample below | 899 | 225 | Session start, retained context. Claude, Codex and agy when server and charter are available. |
| Effective charter, sample below | 899 | 225 | Each prompt on Claude with policy hook; each model request on Pi and OpenCode when policy is available. Codex and agy have no per-turn hook; they get the initial charter. Claude delivered messages also trigger the hook. |
| Pi/OpenCode announce rule | 357 | 89 | Added to system context on each request by their plugins, unless already present. |
| Normal catalog, 32 tools | 29,513 | 7,378 | Available at startup; exposure to model depends on harness, below. Merger uses this role too. |
| Worker catalog, 19 tools | 16,285 | 4,071 | Available at startup for a worker with tools enabled. Codex worker briefs can instead say tools are off. |
| CTO catalog, 35 tools | 36,707 | 9,177 | Available at CTO server startup; actual CTO schemas can vary by role override. |
| Claude always-load subset, normal / worker / CTO | 3,886 / 3,145 / 3,145 | 972 / 786 / 786 | Catalog entries marked always-load, per request; remaining tools are candidates for ToolSearch. CTO v2 registrations can differ, so treat CTO figure as a catalog upper estimate. |
| Peer message wrapper, Claude sample | 411 | 103 | Per delivered request, in addition to message body. Other harnesses add a similar footer; authority, relationship and owner notes add variable text. |
| Worker notice, sample | 855 | 214 | Per completed worker, delivered to caller, excluding the worker's report. Includes 530-byte checklist and a four-byte sample report; actual stats and paths vary. |
| Step task reminder, sample | 743 | 186 | Per reminder, including a 254-byte default test contract, example evidence and diff stat. Repeated until claimed; other steps and evidence vary. |
| Worker brief fixture, Codex / Claude / OpenCode / Pi / agy | 1,405 / 1,557 / 1,323 / 1,323 / 1,318 | 351 / 389 / 331 / 331 / 330 | Once per worker task, then retained as history. Includes a short placeholder task, project rules, charter and worktree details; real task and charter add bytes. |
| Worker notice checklist alone | 530 | 133 | Included in the 855-byte notice, not an extra charge. |

The charter sample uses a growing project, a short purpose, refusal and rule, one consumer and its accepts/refuses. It is `protocol.NodeContext` output, including the default maturity rules and decision instruction. It is illustrative, not a measurement of a private charter. The agent's own charter, global-only rules, deploy edges, dependencies, delivery assignments and one-time draft request can add to it. No configured charter can mean zero. The daemon joins node and agent charter in `internal/daemon/policy.go` and `charter_text.go`; `internal/protocol/graph_context.go` builds the node portion. Delegation policy text is optional and has not been included in the sample.

The notice uses `FormatWorkerNotice` with a sample project and worktree; its report is not agent-net overhead. The reminder follows the single-task format in `internal/gateway/delivery_tasks.go`, with example identifiers, one-line diff, empty evidence and the default test contract in `internal/settings/standard.yaml`. Messages and reminder contents can be much larger. The worker fixtures are `internal/protocol/testdata/worker-settings-baseline.json`, sandboxed worktree variants. Guidance fixtures are the blocks alone, not surrounding project instructions.

Catalog size is the sum of compact JSON objects containing each role's name, description and input schema, selecting the CTO schema override where supplied. Output schemas are validated server-side and are not advertised for catalog reads. Wire framing, tool annotations, harness-specific representation, discovery results and legacy plugin tools are excluded. This is a lower-bound estimate of the full model-facing tool representation. Catalog definitions are in `internal/toolcatalog`, and role filtering is in `internal/mcpserver/roles.go`.

## Loading by harness

- Claude Code: the MCP server lists role-filtered tools. Claude can defer MCP tools through ToolSearch; `anthropic/alwaysLoad` marks a subset for eager loading. Exact selection depends on Claude configuration and tool usage. The optional `UserPromptSubmit` policy hook injects charter text for each prompt, including delivered messages. MCP instructions also carry the initial charter.
- Codex: agent-net exposes the role-filtered MCP catalog and start instructions. Its adapter does not implement a per-turn charter hook or decide model-side tool loading. Whether a particular Codex version defers MCP tools is not established by this repository; measure the model request before assuming the whole catalog is paid. The OpenAI API supports deferred MCP tools, but that does not establish Codex behavior: [tool search documentation](https://developers.openai.com/api/docs/guides/tools-tool-search).
- OpenCode: `plugins/opencode/agent-net.ts` registers the role-filtered catalog as native plugin tools and adds policy and charter through the context hook. Agent-net does not mark these definitions deferred. Provider or harness optimizations are not established here.
- Pi: `plugins/pi/agent-net.ts` registers role-filtered native tools and adds the charter in `before_agent_start`. Agent-net does not mark them deferred. Provider or harness optimizations are not established here.
- agy: agent-net supplies role-filtered MCP tools and initial instructions. There is no agent-net per-turn charter hook. The agy client's model-side loading strategy is not established in this repository.

## Catalog inventory

The normal and merger roles have: agent_announce, agent_ask_sort, agent_change_submit, agent_changes_find, agent_charter_draft, agent_file_send, agent_graph_read, agent_learn, agent_machine_get, agent_machine_log, agent_machines_find, agent_mcp_servers_find, agent_mcp_test, agent_message_get, agent_message_handoff, agent_message_reply, agent_message_send, agent_messages_find, agent_owner_ask, agent_owner_judgments_find, agent_project_add, agent_projects_find, agent_session_get, agent_sessions_find, agent_settings_read, agent_step_claim, agent_step_report, agent_worker_profiles_find, agent_worker_rate, agent_worker_start, agent_worker_stop, agent_workers_find.

The worker role has: agent_changes_find, agent_file_send, agent_graph_read, agent_handback, agent_learn, agent_machine_get, agent_machine_log, agent_machines_find, agent_mcp_servers_find, agent_message_get, agent_message_reply, agent_message_send, agent_messages_find, agent_owner_judgments_find, agent_projects_find, agent_session_get, agent_sessions_find, agent_settings_read, agent_workers_find.

The CTO role has: agent_graph_read, agent_learn, agent_machine_get, agent_machine_log, agent_machines_find, agent_mcp_servers_find, agent_message_get, agent_message_reply, agent_message_send, agent_messages_find, agent_owner_ask, agent_owner_judgments_find, agent_projects_find, agent_session_get, agent_sessions_find, agent_settings_read, agent_worker_profiles_find, agent_workers_find, answer, cto_new, cto_placement_memory, cto_proposals_find, cto_standing_orders, cto_ui_show, graph_change, mcp_call, mcp_change, project_change, projects_layout, proposal_ask, session_change, sessions_archive, settings_change, tools_change, worker_change.

## Example totals

These are input-context estimates over model requests, not a prediction of invoice cost. Assume 50 requests, no compaction, one copy of each retained prefix on every request, no extra agent-net messages, and the sample 899-byte charter. Exclude repeated historical hook outputs: if each becomes a persistent user message, its subsequent resends add substantially more. Tool result text and cache discounts are excluded.

| Session | Calculation in bytes per request | 50-request tokens |
| --- | --- | ---: |
| Claude, only always-load catalog | (9,366 + 428 + 899 initial + 3,886 + 899 hook) / 4 | 193,475 |
| Codex or agy, catalog eager assumption | (9,323 + 428 + 899 + 29,513) / 4 | 502,038 (assumption) |
| OpenCode or Pi, catalog eager assumption | (9,323 + 29,513 + 899 + 357) / 4 | 501,150 (assumption) |

For a typical 10-request Codex worker with tools disabled, use the 9,323-byte guidance, 428-byte MCP instructions if present, 899-byte initial charter and 1,405-byte brief: 10 x 12,055 / 4 = **30,138 tokens**. If the MCP server is actually off, subtract 3,318 tokens for its instructions and initial charter. One 855-byte worker notice adds about 214 tokens to the caller's next request. The illustrative OpenCode/Pi worker with eager worker catalog, guidance, brief, per-request charter and announce rule costs 10 x (9,323 + 1,323 + 16,285 + 899 + 357) / 4 = **70,468 tokens**. These scenarios deliberately specify assumptions rather than assert unverified harness billing.

## Trims ranked by potential savings

1. Defer or selectively expose the 29,513-byte normal catalog on eager harnesses. A session that needs none of it could save up to 7,378 tokens per request, or 368,913 over 50 requests, before discovery overhead. The agent loses immediate visibility and may fail to discover a needed tool. Confirm Codex and agy loading first; Pi/OpenCode need a search or smaller-tool mechanism.
2. Shorten tool descriptions and schemas by 25%. This saves about 1,845 tokens per eager normal request, or 92,228 over 50. This overlaps with item 1. The agent loses parameter guidance and may make more invalid calls.
3. Shorten the 9.3 KB guidance and remove overlap with tool and server instructions. Cutting it by half saves about 1,165 tokens per request, or 58,269 over 50 requests. The agent loses detailed examples and repeated procedural reminders; keep authority and safety rules discoverable.
4. Send the 899-byte charter once and resend only on change or compaction. Avoiding 49 additional copies saves about 11,013 newly inserted tokens over 50 prompts, plus any later resends of those copies. The agent loses immediate refresh if change detection or compaction handling fails; system-context injection that replaces rather than accumulates the prior copy needs separate accounting.
5. Shorten the 530-byte worker checklist by half, saving about 66 tokens per notice. The caller loses explicit review and test reminders. Compact step reminders and message footers too, but keep the provenance and authority distinction.
