# Agent-net context overhead

Measurements taken 2026-11-09 from this checkout. Tokens are estimated as UTF-8 bytes / 4, rounded to the nearest token. This is a sizing proxy, not a tokenizer or a bill. Names, paths, graph size, harness versions, settings, caching and compaction change the result. The numbers exclude user task text, agent output, unrelated instruction files and other MCP servers. They count agent-net content even where it repeats text already present elsewhere.

## Sources

| Source | Bytes | Estimated tokens | When paid and harness |
| --- | ---: | ---: | --- |
| Guidance block, `block_claude.golden` | 9,366 | 2,342 | Loaded from instruction file at session start, normally retained on each model request. Claude. |
| Guidance block, `block_other.golden` | 9,323 | 2,331 | Same. Codex, OpenCode, Pi and agy where the instruction file is read. |
| MCP server fixed instructions | 428 | 107 | Session start and retained context. Claude, Codex and agy MCP clients; Pi and OpenCode use their plugin instead. Includes the announce rule. |
| Initial charter in MCP instructions, measured hook-sized example | 3,555 | 889 | Session start, retained context. Claude, Codex and agy when server and charter are available. |
| Effective charter, real transcript common size | 3,555 | 889 | Each prompt on Claude with policy hook; each model request on Pi and OpenCode when policy is available. Codex and agy have no per-turn hook; they get the initial charter. Claude delivered messages also trigger the hook. |
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

The hook measurement reads `attachment.content` strings containing the effective-charter marker in Claude JSONL files, counts only `UserPromptSubmit` attachments, and sums UTF-8 content bytes. It copies no transcript text. These are snapshots of live transcripts, so counts can grow. They are different sessions, not repetitions of a synthetic sample.

| Session (short ID) | Injections | Total bytes | Most common size | Range |
| --- | ---: | ---: | ---: | ---: |
| 16a63c67 | 888 | 2,692,781 | 3,555 (715 times) | 758-4,138 |
| 6fb0a650 | 341 | 955,049 | 3,555 (247 times) | 758-3,555 |
| d50d5a55 | 5 | 3,790 | 758 (5 times) | 758 |

For the first session, the full size distribution is 715 x 3,555, 94 x 842, 42 x 758, 35 x 962, one x 2,164 and one x 4,138 bytes. The reported 868 injections were an earlier observation; the file contained 888 when measured here. Multiplying all 888 by 3,555 would give 3,156,840 bytes, but the actual sum is 2,692,781 because the content changed. These are inserted bytes, before resending history, not the accumulated input across requests.

The daemon joins node and agent charter in `internal/daemon/policy.go` and `charter_text.go`; `internal/protocol/graph_context.go` builds the node portion. Global rules, deployment edges, dependencies, delivery assignments, agent charter and the one-time draft request affect size. No configured charter can mean zero. Delegation policy text is optional and is not separately counted. The 3,555-byte example is the common real hook attachment, not necessarily the exact initial MCP charter.

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

These are input-context estimates over model requests, not an invoice. Assume 50 requests, no compaction, one 3,555-byte charter hook attachment per request, and one copy of each retained prefix on every request. A hook attachment inserted on request k is present on that request and all 50-k later requests: 50 + 49 + ... + 1 = 1,275 copies. Multiple delivered messages can each create another attachment in a turn, increasing the total. After compaction the sum restarts at the compacted history; actual compaction and caching depend on the harness. Tool results and cache discounts are excluded.

| Session | Calculation | 50-request tokens |
| --- | --- | ---: |
| Claude, only always-load catalog | [50 x (9,366 guidance + 428 instructions + 3,555 initial charter + 3,886 tools) + 1,275 x 3,555 hook] / 4 | 1,348,594 |
| Codex or agy, catalog eager assumption | 50 x (9,323 guidance + 428 instructions + 3,555 initial charter + 29,513 tools) / 4 | 535,238 |
| OpenCode or Pi, catalog eager assumption | 50 x (9,323 guidance + 29,513 tools + 3,555 charter + 357 announce) / 4 | 534,350 |

The Claude hook alone accounts for **1,133,156 input tokens** in the 50-request example. Replacing 50 injections with one identical retained copy would cost 44,438 tokens over 50 requests, saving **1,088,719** tokens. If the initial MCP charter already supplies the same content, avoiding all additional hook copies could save the full 1,133,156. The example does not count a separate model call per delivered message; those add more. The Pi and OpenCode hooks add system context on each request rather than demonstrated persistent attachments, so their table assumes one current copy per request, not the Claude triangular accumulation.

For a 10-request Codex worker with tools disabled, use the 9,323-byte guidance, 428-byte MCP instructions if present, a 3,555-byte initial charter and 1,405-byte brief: 10 x 14,711 / 4 = **36,778 tokens**. If the MCP server is actually off, subtract 9,958 tokens for its instructions and initial charter. One 855-byte worker notice adds about 214 tokens to the caller's next request. The illustrative OpenCode/Pi worker with eager worker catalog, guidance, brief, per-request charter and announce rule costs 10 x (9,323 + 1,323 + 16,285 + 3,555 + 357) / 4 = **77,108 tokens**. These scenarios specify assumptions rather than assert unverified harness billing.

## Trims ranked by potential savings

1. Send the charter once and resend it only when it changes or after compaction. In the 50-request Claude example, replacing the 50 hook attachments with one saves about 1,088,719 input tokens before cache effects. A short hash or delta line can signal unchanged or changed content, at the cost of its own repeated history. The agent risks stale or incomplete rules if change detection, delta application or compaction handling fails.
2. Defer or selectively expose the 29,513-byte normal catalog on eager harnesses. A session that needs none of it could save up to 7,378 tokens per request, or 368,913 over 50 requests, before discovery overhead. The agent loses immediate visibility and may fail to discover a needed tool. Confirm Codex and agy loading first; Pi/OpenCode need a search or smaller-tool mechanism.
3. Shorten tool descriptions and schemas by 25%. This saves about 1,845 tokens per eager normal request, or 92,228 over 50. This overlaps with item 2. The agent loses parameter guidance and may make more invalid calls.
4. Shorten the 9.3 KB guidance and remove overlap with tool and server instructions. Cutting it by half saves about 1,165 tokens per request, or 58,269 over 50 requests. The agent loses detailed examples and repeated procedural reminders; keep authority and safety rules discoverable.
5. Shorten the 530-byte worker checklist by half, saving about 66 tokens per notice. The caller loses explicit review and test reminders. Compact step reminders and message footers too, but keep the provenance and authority distinction.
