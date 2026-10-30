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
	add("info", "Read this daemon's connection, machine, owner session and local session information.", nil)
	add("nodes", "List network machines, installed harnesses, worker track records, playbook notes and subscription quotas.", nil)
	add("projects", "List registered and observed projects across machines, optionally filtered by exact name.", map[string]ctoField{"name": s("Exact project name, or omit for all.")})
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
	add("session_policy", "Read or set a session or project delegation policy and worker sandbox. Use dry_run to preview.", map[string]ctoField{"session_id": sid, "scope": en("Scope to save, or omit to read.", "session", "project"), "policy": obj("Policy object: delegate, coder_model, main_model and workers (sandboxed, no-network or read-only)."), "dry_run": b("Preview without saving.")})
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
	add("node_settings_set", "Set or clear one maturity override on the owner's instruction.", map[string]ctoField{"node": s("Node name."), "setting": en("Setting to override.", "tests_before_merge", "merge_authority", "worker_sandbox", "judge_consumers", "deploy_rule"), "value": s("Valid value for the setting, or empty to clear the override.")})
	add("playbook_get", "Read the owner's playbook note for a model and optional harness.", map[string]ctoField{"model": model, "harness": harness})
	add("playbook_set", "Set or clear the owner's playbook note for a model and optional harness.", map[string]ctoField{"model": model, "harness": harness, "note": s("Note text, or empty to clear."), "pin": b("Pin or unpin the note.")})
	add("learn", "Record a finding for possible promotion to the network playbook, subject to the normal daily limit.", map[string]ctoField{"subject_kind": en("What the finding concerns.", "model", "harness", "model_harness", "project", "mcp", "machine"), "subject": s("The model, harness, project, server or machine."), "finding": s("A concise actionable finding."), "evidence": arr("Session or worker IDs supporting the finding.")})
	add("mcp_list", "List shared MCP servers, their host, health, tools and credential sources.", nil)
	add("mcp_add", "Add a shared MCP server on the owner's instruction. Never include secrets: use environment names or file paths.", map[string]ctoField{"server": obj("MCP server object: name, transport (stdio, http or sse), command, args, env, url, headers and timeout_sec."), "on": s("Host machine for the server, or omit for this machine.")})
	for _, name := range []string{"mcp_remove", "mcp_test"} {
		add(name, "Remove or test a shared MCP server by name. Removing requires the owner's explicit words in this chat.", map[string]ctoField{"name": s("Registered server name.")})
	}
	add("mcp_enable", "Enable or disable a shared MCP server on the owner's instruction.", map[string]ctoField{"name": s("Registered server name."), "enabled": b("Whether the server is enabled.")})
	add("cto_settings", "Read or set the CTO harness, model and automatic compaction settings on the owner's instruction.", map[string]ctoField{"harness": en("CTO harness.", "opencode", "claude"), "model": model, "compaction": obj("Compaction object: threshold_percent, floor_tokens and cache_window_seconds by provider.")})
}
