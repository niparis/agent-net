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
	sid := s("Session ID.")
	model := s("Empty uses default.")
	harness := en("Omit for default.", "opencode", "claude", "pi", "codex")
	add("tools_list", "List tool assignments.", map[string]ctoField{"kind": s("Kind."), "harness": s("Harness."), "project": s("Project."), "state": s("State."), "offset": n("Offset.")})
	add("mcp_import_candidates", "List MCP import candidates.", map[string]ctoField{"machine": s("Machine."), "harness": s("Harness."), "project": s("Project."), "class": en("Verdict.", "importable", "unsupported"), "offset": n("Offset.")})
	add("mcp_import", "Plan or import on explicit instruction.", map[string]ctoField{"candidate": s("Candidate."), "plan_id": s("Plan ID.")})
	add("mcp_native_remove", "Plan or undo removal; apply on explicit instruction.", map[string]ctoField{"candidate": s("Candidate."), "plan_id": s("Plan ID."), "undo_plan_id": s("Removal ID.")})
	add("tools_differences", "Read tool differences.", map[string]ctoField{"offset": n("Offset."), "limit": n("Default 20; max 60.")})
	add("tools_plan", "Plan assignments; repo_remove plans a local branch.", map[string]ctoField{
		"item": s("Item ID."), "target": obj("Assignment."),
		"operation": en("Operation.", "repo_remove"),
		"project":   s("Project."),
		"repo_targets": {typ: "array", desc: "Checkouts.", items: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
			"node_id":  map[string]any{"type": "string", "description": "Machine."},
			"checkout": map[string]any{"type": "string", "description": "Path."},
		}}},
	})
	add("tools_apply", "Apply on explicit owner instruction.", map[string]ctoField{"plan_id": s("Plan ID.")})
	add("tools_undo", "Undo tool assignment.", map[string]ctoField{"item": s("Item ID.")})
	add("tools_history", "Read tool history.", map[string]ctoField{"item": s("Item ID.")})
	add("ui_show", "Show a TUI view.", map[string]ctoField{"tab": en("Tab.", "sessions", "messages", "quotas", "map", "tools"), "select": s("Selection."), "filter": s("Filter."), "why": s("Reason.")})
	add("ui_notice", "Show notice.", map[string]ctoField{"text": s("Notice.")})
	add("info", "Read daemon info.", nil)
	add("nodes", "List machines or harness models.", map[string]ctoField{"models": obj("Selection.")})
	add("projects", "List projects.", map[string]ctoField{"name": s("Omit for all."), "limit": n("Default 20; max 50."), "before": s("Cursor.")})
	add("project_add", "Register or scan projects.", map[string]ctoField{"paths": arr("Paths."), "dir": s("Directory."), "scan": b("Directory."), "depth": n("Default 3; max 8."), "dry_run": b("Preview.")})
	add("project_remove", "Remove a project.", map[string]ctoField{"name": s("Project."), "path": s("Path.")})
	add("reply", "Reply as CTO.", map[string]ctoField{"message_id": s("Message ID."), "body": s("Reply.")})
	add("mcp_call", "Call an MCP tool.", map[string]ctoField{"server": s("Server name."), "tool": s("Tool."), "arguments": obj("Arguments.")})
	add("get", "Read session profile.", map[string]ctoField{"session_id": sid})
	add("messages", "Read messages.", map[string]ctoField{"session_id": sid, "conversation_id": s("Conversation."), "limit": n("Limit.")})
	add("message_status", "Read message status.", map[string]ctoField{"message_id": s("Message ID.")})
	add("session_text", "Read masked session text.", map[string]ctoField{"session_id": sid, "limit": n("Limit."), "before": s("Older."), "after": s("Newer."), "sidechain": b("Sub-agents.")})
	add("usage", "Read usage and quotas.", nil)
	add("usage_top", "Read session usage.", map[string]ctoField{"session_id": sid})
	add("counts", "Read session counts.", nil)
	add("graph_view", "Read the map.", map[string]ctoField{"project": s("Project; omit for all.")})
	add("session_enter", "Get session attachment plan.", map[string]ctoField{"session_id": sid, "tmux_socket": s("Tmux socket."), "herdr_socket": s("Herdr socket.")})
	add("session_trust", "Set session peer trust.", map[string]ctoField{"session_id": sid, "on": b("Enable.")})
	add("session_policy", "Set policy; read get first.", map[string]ctoField{"session_id": sid, "scope": en("Default session.", "session", "project"), "policy": obj("Policy."), "dry_run": b("Preview.")})
	add("project_display_set", "Set project name.", map[string]ctoField{"project": s("Project."), "name": s("Empty clears.")})
	add("project_display_get", "Read project name.", map[string]ctoField{"project": s("Project.")})
	add("node_rename", "Set machine display name.", map[string]ctoField{"node_id": s("Machine."), "name": s("Empty clears.")})
	add("integrations_get", "Read integrations.", nil)
	add("integrations_set", "Set integrations.", map[string]ctoField{"peer_messages": b("Messages."), "codex_tools": b("Codex."), "hint_hook": b("Hints."), "policy_hook": b("Policy."), "needs_hook": b("Needs you."), "workers_mod": b("Workers."), "quota_axi": b("Quotas.")})
	add("worker_stop", "Stop a worker or queued start.", map[string]ctoField{"session": s("Worker ID.")})
	add("held_list", "List held requests.", nil)
	add("held_answer", "Answer held request.", map[string]ctoField{"message_id": s("Message ID."), "action": en("a approves once, A approves and sets auto, r declines once, R declines and sets never.", "a", "A", "r", "R")})
	add("inbox_list", "List owner inbox.", nil)
	add("inbox_answer", "Answer inbox item.", map[string]ctoField{"id": s("Inbox ID."), "answer": s("Answer.")})
	add("outcome_inbox", "List outcomes to review.", nil)
	add("outcome_overrule", "Resolve an outcome.", map[string]ctoField{"reply_id": s("Reply ID."), "action": en("Action.", "let_stand", "overrule")})
	add("node_settings_get", "Read settings.", map[string]ctoField{"node": s("Node.")})
	add("node_settings_set", "Set an override.", map[string]ctoField{"node": s("Node."), "setting": en("Setting.", "tests_before_merge", "merge_authority", "worker_sandbox", "judge_consumers", "deploy_rule"), "value": s("Empty clears.")})
	add("playbook_get", "Read playbook.", map[string]ctoField{"model": model, "harness": harness})
	add("playbook_set", "Set playbook.", map[string]ctoField{"model": model, "harness": harness, "note": s("Empty clears."), "pin": b("Pin.")})
	add("learn", "Record a finding.", map[string]ctoField{"subject_kind": en("Kind.", "model", "harness", "model_harness", "project", "mcp", "machine"), "subject": s("Subject."), "finding": s("Finding."), "evidence": arr("Evidence.")})
	add("mcp_list", "List MCP servers.", nil)
	add("mcp_add", "Add MCP server; no secrets.", map[string]ctoField{"server": obj("Configuration."), "on": s("Host; local default.")})
	add("mcp_remove", "Remove MCP server.", map[string]ctoField{"name": s("Server name.")})
	add("mcp_test", "Test MCP server.", map[string]ctoField{"name": s("Server name.")})
	add("mcp_enable", "Enable or disable MCP server.", map[string]ctoField{"name": s("Server name."), "enabled": b("Enable.")})
	add("cto_enter", "Get CTO attachment plan.", nil)
	add("cto_send", "Queue CTO instruction.", map[string]ctoField{"text": s("Instruction.")})
	add("cto_new", "Start CTO conversation.", nil)
	add("cto_events", "Read CTO events.", map[string]ctoField{"index": n("Start index.")})
	add("cto_graph_history", "Read CTO graph history.", map[string]ctoField{"after": n("Cursor.")})
	add("cto_settings", "Read or set CTO settings.", map[string]ctoField{"harness": en("Harness.", "opencode", "claude"), "model": model, "compaction": obj("Compaction.")})
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
	setProperties("tools_plan", "target", map[string]any{"scope": property("string", "Scope.", "global", "projects", "uninstalled"), "projects": stringsArray("Projects."), "machines": stringsArray("Omit for all.")})
	setProperties("nodes", "models", map[string]any{"machine": property("string", "Machine."), "harness": property("string", "Harness.")})
	setProperties("session_policy", "policy", map[string]any{
		"delegate":    property("string", "Mode.", "", "off", "native", "workers"),
		"coder_model": property("string", "Coder."), "main_model": property("string", "Main."),
		"workers": property("string", "Sandbox.", "", "sandboxed", "no-network", "read-only"),
	})
	setProperties("mcp_add", "server", map[string]any{
		"name": property("string", "Server name."), "transport": property("string", "Transport.", "stdio", "http", "sse"),
		"command": property("string", "Command."), "args": stringsArray("No secrets."), "env": stringsArray("Names, not values."),
		"url": property("string", "Endpoint."), "timeout_sec": property("integer", "Seconds."),
		"headers": map[string]any{"type": "array", "description": "Headers.", "items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"name": property("string", "Name."), "env": property("string", "Env."), "file": property("string", "Path.")}}},
	})
	setProperties("cto_settings", "compaction", map[string]any{
		"threshold_percent":    property("integer", "Percent."),
		"cache_expiry_enabled": property("boolean", "Default true."),
		"cache_window_seconds": map[string]any{"type": "object", "description": "Seconds per provider.", "additionalProperties": map[string]any{"type": "integer"}},
	})

}
