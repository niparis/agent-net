package mcpserver

// These tools use the same parameter names as the owner operator methods.
func init() {
	s := func(desc string) ctoField { return ctoField{typ: "string", desc: desc} }
	b := func(desc string) ctoField { return ctoField{typ: "boolean", desc: desc} }
	n := func(desc string) ctoField { return ctoField{typ: "integer", desc: desc} }
	obj := func(desc string) ctoField { return ctoField{typ: "object", desc: desc} }
	arr := func(desc string) ctoField { return ctoField{typ: "array", desc: desc} }
	en := func(desc string, values ...string) ctoField { return ctoField{typ: "string", desc: desc, enum: values} }
	add := func(name, desc string, fields map[string]ctoField) {
		ctoTools[name] = ctoToolDef{desc: desc, fields: fields}
	}
	sid := s("Session ID, as returned by agents.")
	model := s("Model name; an empty model selects the default where supported.")
	harness := en("Harness name, or omit to use the default.", "opencode", "claude", "pi", "codex")
	add("tools_list", "List observed skills and MCP items, their assignments and advisory charter needs.", map[string]ctoField{"kind": s("Filter by skill or mcp."), "harness": s("Filter by harness."), "project": s("Filter by registered project."), "state": s("Filter by observed state."), "offset": n("Page offset from next.")})
	add("mcp_import_candidates", "List native MCP servers available for import, including unsupported features. Use next as offset.", map[string]ctoField{"machine": s("Filter by machine name or ID."), "harness": s("Filter by harness."), "project": s("Filter by registered project."), "class": en("Filter by verdict.", "importable", "unsupported"), "offset": n("Page offset from next.")})
	add("mcp_import", "Plan or apply import of a native MCP server on explicit owner instruction. The native entry remains.", map[string]ctoField{"candidate": s("Candidate key from mcp_import_candidates, to make a plan."), "plan_id": s("Plan ID from a candidate plan, to apply it.")})
	add("mcp_native_remove", "Plan, apply or undo removal of an imported native MCP entry on explicit owner instruction. Running sessions retain native tools until restart.", map[string]ctoField{"candidate": s("Candidate key from mcp_import_candidates, to make a plan."), "plan_id": s("Plan ID from a candidate plan, to apply it."), "undo_plan_id": s("Plan ID of an applied removal, to undo it.")})
	add("tools_differences", "Read skill and MCP differences across machines and projects, with suggested fixes.", map[string]ctoField{"offset": n("Offset from next."), "limit": n("Page size, default 20, maximum 60.")})
	add("tools_plan", "Plan a tool assignment, including paths and conflicts. repo_remove plans a local branch and commit.", map[string]ctoField{
		"item": s("Exact item ID from tools_list."), "target": obj("Desired assignment with scope, optional project names and optional machines. Machines limits the change to those machines (names or IDs); leave it out for every machine."),
		"operation": en("Optional operation for a skill committed in a project.", "repo_remove"),
		"project":   s("Registered project name for repo_remove."),
		"repo_targets": {typ: "array", desc: "Registered project checkouts that hold this repo skill.", items: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
			"node_id":  map[string]any{"type": "string", "description": "Machine node ID from the project's skill location."},
			"checkout": map[string]any{"type": "string", "description": "Registered project checkout path."},
		}}},
	})
	add("tools_apply", "Apply a reviewed tool assignment plan on explicit owner instruction and return target results.", map[string]ctoField{"plan_id": s("Exact plan ID returned by tools_plan.")})
	add("tools_undo", "Undo an item's newest assignment revision where files are unchanged.", map[string]ctoField{"item": s("Exact item ID from tools_list.")})
	add("tools_history", "Read an item's recorded plans and assignment revisions, including actors, targets and results.", map[string]ctoField{"item": s("Exact item ID from tools_list.")})
	add("ui_show", "Show a tab, selection and filter in every open local TUI, with a short explanation.", map[string]ctoField{"tab": en("Tab to show.", "sessions", "messages", "quotas", "map", "tools"), "select": s("Session ID, exact session name or project node to select."), "filter": s("Text to filter the view."), "why": s("Short reason shown to the owner.")})
	add("ui_notice", "Show a short one-line notice in every open local TUI; returns no TUI open when none is connected.", map[string]ctoField{"text": s("Notice text.")})
	add("info", "Read this daemon's connection, machine, owner session and local session information.", nil)
	add("nodes", "List compact machine summaries, or one harness model list with models.", map[string]ctoField{"models": obj("Select one machine and harness model list.")})
	add("projects", "List registered and observed projects across machines, optionally filtered by exact name.", map[string]ctoField{"name": s("Exact project name, or omit for all."), "limit": n("Page size, default 20, maximum 50."), "before": s("Cursor from next_before.")})
	add("project_add", "Register repositories on this machine as projects. Scan with dry_run first; do this only on the owner's instruction.", map[string]ctoField{"paths": arr("Absolute repository paths on this machine."), "dir": s("Directory to scan on this machine."), "scan": b("Scan dir for repositories."), "depth": n("Maximum scan depth, default 3 and at most 8."), "dry_run": b("Preview without registering.")})
	add("project_remove", "Remove a registered project from this machine on the owner's instruction.", map[string]ctoField{"name": s("Registered project name."), "path": s("Registered project path.")})
	add("reply", "Reply as the owner's CTO to a message addressed to the owner, preserving CTO attribution.", map[string]ctoField{"message_id": s("Message ID addressed to the owner."), "body": s("Reply text.")})
	add("mcp_call", "Call a tool on a shared MCP server by server and tool name, using its documented arguments.", map[string]ctoField{"server": s("Registered MCP server name."), "tool": s("Tool name on the server."), "arguments": obj("Arguments according to that tool's schema.")})
	add("get", "Read a session's profile, project, charter and status by its session ID.", map[string]ctoField{"session_id": sid})
	add("messages", "Read recent network messages, optionally limited to a session or conversation.", map[string]ctoField{"session_id": sid, "conversation_id": s("Conversation ID to filter by."), "limit": n("Maximum messages to return.")})
	add("message_status", "Read one message and its delivery state using its message ID.", map[string]ctoField{"message_id": s("Message ID returned by send.")})
	add("session_text", "Read a masked page of a session's conversation, including tool entries when available.", map[string]ctoField{"session_id": sid, "limit": n("Maximum entries."), "before": s("Older cursor."), "after": s("Newer cursor."), "sidechain": b("Include Claude sub-agent entries.")})
	add("usage", "Read CPU, memory and subscription quota samples for the network's machines.", nil)
	add("usage_top", "Read CPU and memory usage for a session and its process tree.", map[string]ctoField{"session_id": sid})
	add("counts", "Read session counts from the network for the owner's interface.", nil)
	add("graph_view", "Read the graph as shown in the map, optionally filtered to a project.", map[string]ctoField{"project": s("Project name or omit for the whole graph.")})
	add("session_enter", "Get the owner's terminal attachment plan for a session; this does not attach the terminal.", map[string]ctoField{"session_id": sid, "tmux_socket": s("Owner terminal's tmux socket, if any."), "herdr_socket": s("Owner terminal's herdr socket, if any.")})
	add("session_trust", "Turn peer trust for a session on or off on the owner's instruction.", map[string]ctoField{"session_id": sid, "on": b("Whether peer trust is enabled.")})
	add("session_policy", "Set or clear a session or project delegation policy and worker sandbox. Read the effective policy with get first; use dry_run to preview.", map[string]ctoField{"session_id": sid, "scope": en("Scope to save; omitted defaults to session.", "session", "project"), "policy": obj("Policy object: delegate, coder_model, main_model and workers (sandboxed, no-network or read-only)."), "dry_run": b("Preview without saving.")})
	add("project_display_set", "Set or clear a project's display name on the owner's instruction.", map[string]ctoField{"project": s("Project identity."), "name": s("Display name, or empty to clear.")})
	add("project_display_get", "Read the display name for a project by its identity.", map[string]ctoField{"project": s("Project identity.")})
	add("node_rename", "Set a machine's network display name; an empty name clears it.", map[string]ctoField{"node_id": s("Machine node ID."), "name": s("Display name or empty to clear.")})
	add("integrations_get", "Read the owner's network-wide integration settings before changing any setting.", nil)
	add("integrations_set", "Set the complete integration settings. Change integrations only with the owner's explicit words in this chat.", map[string]ctoField{"peer_messages": b("Peer message delivery."), "codex_tools": b("Codex tools."), "hint_hook": b("Peer hints."), "policy_hook": b("Policy hook."), "needs_hook": b("Needs you hook."), "workers_mod": b("Worker integration."), "quota_axi": b("Quota integration.")})
	add("worker_stop", "Stop a worker or cancel a queued start. Require the owner's explicit words in this chat.", map[string]ctoField{"session": s("Worker session ID or queued start ID.")})
	add("held_list", "List requests held for the owner's decision by an ask rule.", nil)
	add("held_answer", "Answer a held request on the owner's instruction. Declining requires the owner's explicit words in this chat.", map[string]ctoField{"message_id": s("Held message ID."), "action": en("a approves once, A approves and sets auto, r declines once, R declines and sets never.", "a", "A", "r", "R")})
	add("inbox_list", "List the owner's lasting inbox of approvals, ideas, questions and escalations.", nil)
	add("inbox_answer", "Answer an inbox item on the owner's instruction; the answer is delivered to its agent.", map[string]ctoField{"id": s("Inbox item ID."), "answer": s("Owner's answer to deliver.")})
	add("outcome_inbox", "List request outcomes that need the owner's attention and possible overrule.", nil)
	add("outcome_overrule", "Resolve an outcome on the owner's instruction, including overruling a rejection.", map[string]ctoField{"reply_id": s("Outcome reply ID."), "action": en("Let the outcome stand or overrule it.", "let_stand", "overrule")})
	add("node_settings_get", "Read a node's maturity defaults, overrides and effective settings.", map[string]ctoField{"node": s("Component or architect node name.")})
	add("node_settings_set", "Set or clear one maturity override on the owner's instruction.", map[string]ctoField{"node": s("Node name."), "setting": en("Setting to override.", "tests_before_merge", "merge_authority", "worker_sandbox", "judge_consumers", "deploy_rule"), "value": s("Value, or empty to clear: tests_before_merge uses optional, suite, suite-new-tests or suite-new-tests-review; merge_authority uses full, delegated or ask; worker_sandbox uses sandboxed, no-network or read-only; judge_consumers uses free, charter, strict or escalate; deploy_rule uses brief, ask or owner.")})
	add("playbook_get", "Read the owner's playbook note for a model and optional harness.", map[string]ctoField{"model": model, "harness": harness})
	add("playbook_set", "Set or clear the owner's playbook note for a model and optional harness.", map[string]ctoField{"model": model, "harness": harness, "note": s("Note text, or empty to clear."), "pin": b("Pin or unpin the note.")})
	add("learn", "Record a finding for possible promotion to the network playbook, subject to the normal daily limit.", map[string]ctoField{"subject_kind": en("What the finding concerns.", "model", "harness", "model_harness", "project", "mcp", "machine"), "subject": s("The model, harness, project, server or machine."), "finding": s("A concise actionable finding."), "evidence": arr("Session or worker IDs supporting the finding.")})
	add("mcp_list", "List shared MCP servers, their host, health, tools and credential sources.", nil)
	add("mcp_add", "Add a shared MCP server on the owner's instruction. Never include secrets: use environment names or file paths.", map[string]ctoField{"server": obj("MCP server object: name, transport (stdio, http or sse), command, args, env, url, headers and timeout_sec."), "on": s("Host machine for the server, or omit for this machine.")})
	for _, name := range []string{"mcp_remove", "mcp_test"} {
		add(name, "Remove or test a shared MCP server by name. Removing requires the owner's explicit words in this chat.", map[string]ctoField{"name": s("Registered server name.")})
	}
	add("mcp_enable", "Enable or disable a shared MCP server on the owner's instruction.", map[string]ctoField{"name": s("Registered server name."), "enabled": b("Whether the server is enabled.")})
	add("cto_enter", "Get the owner's terminal attachment plan for the current CTO harness.", nil)
	add("cto_send", "Queue an owner instruction to the CTO conversation, only when the owner asks for that explicitly.", map[string]ctoField{"text": s("Owner instruction to queue.")})
	add("cto_new", "Start a new CTO conversation for the selected harness, only when the owner asks.", nil)
	add("cto_events", "Read messages and action lines from the current CTO conversation.", map[string]ctoField{"index": n("Message index to read from; zero begins at the start.")})
	add("cto_graph_history", "Read graph changes recorded for CTO conversations after an optional history ID.", map[string]ctoField{"after": n("Return changes after this history ID.")})
	add("cto_settings", "Read or set the CTO harness, model and automatic compaction settings on the owner's instruction.", map[string]ctoField{"harness": en("CTO harness.", "opencode", "claude"), "model": model, "compaction": obj("Compaction object: threshold_percent, cache_expiry_enabled and cache_window_seconds by provider.")})
	// Nested schemas make the valid settings and credential sources visible to
	// the model, while the daemon remains the final validator.
	property := func(typ, desc string, values ...string) map[string]any {
		p := map[string]any{"type": typ, "description": desc}
		if len(values) > 0 {
			p["enum"] = values
		}
		return p
	}
	stringsArray := func(desc string) map[string]any {
		return map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": "string"}}
	}
	setProperties := func(tool, field string, props map[string]any) {
		spec := ctoTools[tool]
		f := spec.fields[field]
		f.properties = props
		spec.fields[field] = f
		ctoTools[tool] = spec
	}
	setProperties("tools_plan", "target", map[string]any{"scope": property("string", "Desired scope.", "global", "projects", "uninstalled"), "projects": stringsArray("Registered project names when scope is projects."), "machines": stringsArray("Machine names or IDs the change is limited to. Leave out for every machine. Machines not named keep what they have.")})
	setProperties("nodes", "models", map[string]any{"machine": property("string", "Machine name or ID."), "harness": property("string", "Harness name.")})
	setProperties("session_policy", "policy", map[string]any{
		"delegate":    property("string", "Delegation mode.", "", "off", "native", "workers"),
		"coder_model": property("string", "Coder model name."), "main_model": property("string", "Main model name."),
		"workers": property("string", "Worker sandbox level; project scope only.", "", "sandboxed", "no-network", "read-only"),
	})
	setProperties("mcp_add", "server", map[string]any{
		"name": property("string", "Server name."), "transport": property("string", "Transport.", "stdio", "http", "sse"),
		"command": property("string", "Stdio command."), "args": stringsArray("Stdio arguments, without secrets."), "env": stringsArray("Environment variable names, never values."),
		"url": property("string", "Remote MCP endpoint."), "timeout_sec": property("integer", "Per-call timeout in seconds."),
		"headers": map[string]any{"type": "array", "description": "Header names and their credential sources.", "items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"name": property("string", "Header name."), "env": property("string", "Environment variable name."), "file": property("string", "Credential file path.")}}},
	})
	setProperties("cto_settings", "compaction", map[string]any{
		"threshold_percent":    property("integer", "Context percentage that triggers compaction."),
		"cache_expiry_enabled": property("boolean", "Compact before cache expiry where supported (default true)."),
		"cache_window_seconds": map[string]any{"type": "object", "description": "Prompt cache window in seconds per provider.", "additionalProperties": map[string]any{"type": "integer"}},
	})

}
