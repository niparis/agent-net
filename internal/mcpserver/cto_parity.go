package mcpserver

import (
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
)

// ctoConcreteTools holds the internal argument schemas of the change calls that
// the merged action tools front. They are not exposed in the MCP tool list;
// they validate a stored call and keep proposals stored before part 10 working.
var ctoConcreteTools = func() map[string]ctoToolDef {
	out := map[string]ctoToolDef{}
	s := func(desc string) ctoField { return ctoField{typ: "string", desc: desc} }
	b := func(desc string) ctoField { return ctoField{typ: "boolean", desc: desc} }
	n := func(desc string) ctoField { return ctoField{typ: "integer", desc: desc} }
	obj := func(desc string) ctoField { return ctoField{typ: "object", desc: desc} }
	arr := func(desc string) ctoField { return ctoField{typ: "array", desc: desc} }
	en := func(desc string, values ...string) ctoField { return ctoField{typ: "string", desc: desc, enum: values} }
	add := func(name, desc string, fields map[string]ctoField) {
		out[name] = ctoToolDef{desc: desc, fields: fields, compact: true}
	}
	sid := s("Session ID.")
	model := s("Empty uses default.")
	harness := en("Omit for default.", "opencode", "claude", "pi", "codex")

	add("charter_set", "Set a charter attribute.", map[string]ctoField{"ref": {typ: "graph_ref"}, "level": en("Charter level.", "global", "team", "repo", "agent"), "node": s("Node or scope ID."), "attr": en("Attribute.", ctoCharterAttrs...), "value": s("2 KiB max; empty clears.")})
	for _, name := range []string{"repo_create", "agent_create"} {
		add(name, "Create a typed identity.", map[string]ctoField{"name": s("Display name."), "role": s("Agent role; optional.")})
	}
	for _, name := range []string{"repo_archive", "agent_tombstone"} {
		add(name, "Deactivate a typed identity; owner approval required.", map[string]ctoField{"ref": {typ: "graph_ref"}})
	}
	add("lead_set", "Set a Repo or Team lead.", map[string]ctoField{"ref": {typ: "graph_ref"}, "lead": {typ: "graph_ref"}})
	add("node_add", "Add an architect node.", map[string]ctoField{"name": s("Name.")})
	add("node_remove", "Remove an edgeless architect.", map[string]ctoField{"name": s("Name.")})
	add("edge_set", "Set a directed edge.", map[string]ctoField{
		"from": {typ: "graph_endpoint"}, "to": {typ: "graph_endpoint"},
		"kind": en("Consumer to provider, deployer to app.", ctoEdgeKinds...),
		"rule": en("Deliver, own, hold, refuse.", "normal", "auto", "ask", "never"),
	})
	add("edge_remove", "Remove a graph edge.", map[string]ctoField{"from": {typ: "graph_endpoint"}, "to": {typ: "graph_endpoint"}, "kind": en("Kind and direction.", ctoEdgeKinds...)})
	add("graph_undo", "Undo a graph change.", map[string]ctoField{"id": n("ID; omit for latest.")})
	add("node_settings_set", "Set an override.", map[string]ctoField{"node": s("Node."), "setting": en("Setting.", ctoNodeSettings...), "value": s("Empty clears.")})
	add("team_create", "Create a team.", map[string]ctoField{"name": s("Team name."), "purpose": s("Team purpose.")})
	add("team_rename", "Rename a team.", map[string]ctoField{"team": s("Team name or ID."), "name": s("New team name.")})
	add("team_charter", "Set an inherited Team charter field.", map[string]ctoField{"team": s("Team name or ID."), "attr": en("Attribute. asks_to sends questions for the owner to the team lead.", "purpose", "refuses", "escalate", "rules", "asks_to"), "value": s("Empty inherits. asks_to: owner or lead.")})
	add("team_purpose", "Change a team purpose.", map[string]ctoField{"team": s("Team name or ID."), "purpose": s("Team purpose.")})
	add("team_archive", "Archive a team.", map[string]ctoField{"team": s("Team name or ID.")})
	add("team_member_add", "Add a member to a team.", map[string]ctoField{"team": s("Team name or ID."), "member": {typ: "graph_ref", desc: "Member ref (agent or repo)."}, "ref": {typ: "graph_ref", desc: "Member ref (agent or repo)."}})
	add("team_member_remove", "Remove a member from a team.", map[string]ctoField{"team": s("Team name or ID."), "member": {typ: "graph_ref", desc: "Member ref (agent or repo)."}, "ref": {typ: "graph_ref", desc: "Member ref (agent or repo)."}})

	startFields := map[string]ctoField{"agent_id": s("Lasting Agent; omit for Repo owner."), "project": s("Project."), "path": s("Path."), "node_id": s("Machine."), "harness": harness, "model": model, "folder": en("Folder.", "project", "worktree"), "branch": s("Branch."), "first_message": s("First message."), "create": b("Create."), "trust": b("Trust the folder for Claude Code, then start."), "mcp_servers": en("Approve or disable the .mcp.json servers Claude Code would load in the session folder, then start.", "approve", "disable")}
	add("session_start", "Start an owner session.", startFields)
	add("merger_start", "Start the project merger.", map[string]ctoField{"project": s("Project."), "node_id": s("Machine."), "model": model})
	add("merger_stop", "Stop routing to the project merger.", map[string]ctoField{"project": s("Project."), "node_id": s("Machine.")})
	createFields := map[string]ctoField{}
	for k, v := range startFields {
		if k != "project" && k != "create" && k != "trust" {
			createFields[k] = v
		}
	}
	createFields["remote"] = s("Optional git@host:owner/repo.git or https://host/owner/repo.git.")
	add("project_create", "Create a project and start a session.", createFields)
	add("session_stop", "Stop an agent session; the conversation is kept.", map[string]ctoField{"session_id": sid})
	add("session_resume", "Resume an offline agent session in the background.", map[string]ctoField{"session_id": sid})
	add("session_restart", "Stop an agent session, then resume it.", map[string]ctoField{"session_id": sid})
	add("session_rename", "Rename a session for the owner.", map[string]ctoField{"session_id": sid, "name": s("Name; empty resets.")})
	add("session_trust", "Set session peer trust, or trust a project or path folder for Claude Code.", map[string]ctoField{"session_id": sid, "on": b("Enable."), "project": s("Registered project to trust for Claude Code."), "path": s("Existing folder to trust for Claude Code."), "node_id": s("Machine; omit for this machine.")})
	add("folder_mcp", "Approve or disable the .mcp.json servers Claude Code would load in a project or path folder, so a Claude session start does not stop on the approval prompt.", map[string]ctoField{"project": s("Registered project."), "path": s("Existing folder."), "node_id": s("Machine; omit for this machine."), "mcp_servers": en("Choice.", "approve", "disable")})
	add("session_policy", "Set policy; read get first.", map[string]ctoField{"session_id": sid, "scope": en("Default session.", "session", "project"), "policy": obj("Policy."), "dry_run": b("Preview.")})
	add("machine_remove", "Remove a machine and revoke its token. Owner approval required.", map[string]ctoField{"node_id": s("Machine ID.")})
	add("machine_rotate", "Rotate an online machine token. Offline machines can only be removed. Owner approval required.", map[string]ctoField{"node_id": s("Machine ID.")})
	add("process_stop", "Stop processes on a machine: SIGTERM, then SIGKILL. No undo.", map[string]ctoField{"node_id": s("Machine; required."), "pids": {typ: "array", desc: "Process IDs.", items: map[string]any{"type": "integer"}}})
	add("node_rename", "Set machine name.", map[string]ctoField{"node_id": s("Machine."), "name": s("Empty clears.")})

	add("worker_start", "Start a worker.", map[string]ctoField{
		"project": s("Default CTO project."), "harness": en("Harness; empty is auto.", "opencode", "claude", "pi", "codex"),
		"model": s("Model; empty is auto."), "difficulty": en("Deprecated and ignored: the level comes from the difficulty judge.", "trivial", "simple", "normal", "hard", "very_complex"),
		"touches": {typ: "array", desc: "Deprecated and ignored: the level comes from the difficulty judge.", items: map[string]any{"type": "string", "enum": []string{"db", "hot-path", "ui", "docs", "tests", "config", "other"}}},
		"base":    s("Branch."), "continue": s("Worker ID."), "task": s("Brief and limits."), "machine": s("Machine."),
		"isolation":     en("None reads only; default worktree.", "worktree", "none"),
		"mcp_servers":   en("Claude: approve or disable the .mcp.json servers of the worker's folder.", "approve", "disable"),
		"auto_continue": b("Default true: agent-net continues a worker that stops early."),
	})
	add("worker_stop", "Stop a worker or queued start.", map[string]ctoField{"session": s("Worker ID.")})

	add("held_answer", "Answer held request.", map[string]ctoField{"message_id": s("Message ID."), "action": en("a once, A and auto, r once, R and never.", "a", "A", "r", "R")})
	add("inbox_answer", "Answer inbox item.", map[string]ctoField{"id": s("Inbox ID."), "answer": s("Answer.")})
	add("outcome_overrule", "Resolve an outcome.", map[string]ctoField{"reply_id": s("Reply ID."), "action": en("Action.", "let_stand", "overrule")})

	add("tools_toggle", "Switch a skill on or off for one project.", map[string]ctoField{"item": s("Item ID."), "project": s("Project."), "on": b("On or off.")})
	add("tools_new_projects", "Set the choice for future projects.", map[string]ctoField{"item": s("Item ID."), "on": b("On or off.")})
	add("tools_plan", "Plan assignments; repo removal on a local branch.", map[string]ctoField{
		"item": s("Item ID."), "target": obj("Assignment."),
		"operation": en("Operation.", "repo_remove"),
		"project":   s("Project."),
		"repo_targets": {typ: "array", desc: "Checkouts.", items: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
			"node_id":  map[string]any{"type": "string", "description": "Machine."},
			"checkout": map[string]any{"type": "string", "description": "Path."},
		}}},
	})
	add("tools_apply", "Apply the plan.", map[string]ctoField{"plan_id": s("Plan ID.")})
	add("tools_undo", "Undo assignment.", map[string]ctoField{"item": s("Item ID.")})
	add("mcp_import", "Import a native MCP server.", map[string]ctoField{"candidate": s("Candidate."), "plan_id": s("Plan ID.")})
	add("mcp_native_remove", "Remove native MCP configuration.", map[string]ctoField{"candidate": s("Candidate."), "plan_id": s("Plan ID."), "undo_plan_id": s("Removal ID.")})

	add("mcp_add", "Add MCP server; no secrets.", map[string]ctoField{"server": obj("Configuration."), "on": s("Host; local default.")})
	add("mcp_remove", "Remove MCP server.", map[string]ctoField{"name": s("Server name.")})
	add("mcp_test", "Test MCP server.", map[string]ctoField{"name": s("Server name.")})
	add("mcp_enable", "Enable or disable server.", map[string]ctoField{"name": s("Server name."), "enabled": b("Enable.")})
	for _, name := range []string{"apps.list", "apps.public.status", "apps.public.on", "apps.public.off"} {
		add(name, "Read connected apps or control their internet access.", nil)
	}
	add("apps.ask", "Ask the owner to approve an app or phone pairing.", map[string]ctoField{"code": s("Code shown by the app."), "kind": en("App type.", "bot", "owner_app"), "name": s("App name.")})
	add("apps.grant", "Grant a shared MCP server to a bot.", map[string]ctoField{"id": s("App ID."), "grant": s("mcp server <name>.")})
	add("apps.ungrant", "Remove a bot grant.", map[string]ctoField{"id": s("App ID."), "grant": s("mcp server <name>.")})
	add("apps.rename", "Rename an active app.", map[string]ctoField{"id": s("App ID."), "name": s("New app name.")})
	add("apps.revoke", "Revoke an app and its credentials; cannot be undone.", map[string]ctoField{"id": s("App ID.")})
	add("mcp_call", "Call an MCP tool.", map[string]ctoField{"server": s("Server name."), "tool": s("Tool."), "arguments": obj("Arguments.")})

	add("owner_operation", "Run an owner operation.", map[string]ctoField{"method": en("Owner method.", strings.Fields(protocol.CTOOperatorReads+" "+protocol.CTOOperatorChanges)...), "arguments": obj("Owner method arguments.")})
	add("settings_values_write", "Write setting values.", map[string]ctoField{"changes": settingsChangesField(), "reason": s("Reason.")})
	add("settings_values_undo", "Undo a settings revision.", map[string]ctoField{"revision": n("Revision.")})
	add("integrations_set", "Set integrations.", map[string]ctoField{"peer_messages": b("Messages."), "codex_tools": b("Codex."), "hint_hook": b("Hints."), "policy_hook": b("Policy."), "needs_hook": b("Needs you."), "workers_mod": b("Workers."), "quota_axi": b("Quotas.")})
	add("notification_settings", "Set notifications.", map[string]ctoField{"disabled": b("Off if true.")})
	add("cto_settings", "Set CTO settings.", map[string]ctoField{"title": s("Name on screens."), "harness": en("Harness.", "opencode", "claude"), "model": model, "compaction": obj("Compaction.")})
	add("protected_providers_set", "Set protected providers.", map[string]ctoField{"protected_providers": arr("Provider IDs."), "reason": s("Reason.")})
	add("worker_providers_set", "Set worker providers.", map[string]ctoField{"providers": arr("IDs; omit for defaults.")})
	add("score_set", "Set a model score.", map[string]ctoField{"model": s("Model or pattern."), "score": {typ: "number", desc: "0-100; omit to clear."}})
	add("worker_profile_add", "Set a worker profile.", map[string]ctoField{"name": s("Replaces an existing name."), "harness": s("Harness."), "model": s("Model ID or name."), "effort": s("Effort.")})
	add("worker_profile_remove", "Remove a worker profile.", map[string]ctoField{"name": s("Name.")})
	add("playbook_set", "Set playbook.", map[string]ctoField{"model": model, "harness": harness, "note": s("Empty clears."), "pin": b("Pin.")})

	add("project_add", "Register or scan projects.", map[string]ctoField{"node_id": s("Machine name or node ID; omit for this machine."), "paths": arr("Paths."), "dir": s("Directory."), "scan": b("Directory."), "depth": n("3; max 8."), "dry_run": b("Preview.")})
	add("project_remove", "Remove a project.", map[string]ctoField{"name": s("Project."), "path": s("Path."), "node_id": s("Machine; omit for this machine.")})
	add("project_display_set", "Set project name.", map[string]ctoField{"project": s("Project."), "name": s("Empty clears.")})

	add("invite", "New machine install line.", nil)
	add("onboarding_restart", "Restart the getting-started steps.", nil)
	add("cto_send", "Queue CTO instruction.", map[string]ctoField{"text": s("Instruction.")})
	add("cto_new", "Start CTO conversation.", nil)

	// Nested schemas make the valid settings and credential sources visible to
	// the daemon validator, while the daemon remains the final validator.
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
		spec := out[tool]
		f := spec.fields[field]
		f.properties = props
		spec.fields[field] = f
		out[tool] = spec
	}
	setProperties("tools_plan", "target", map[string]any{"scope": property("string", "Scope.", "global", "projects", "uninstalled"), "projects": stringsArray("Projects."), "machines": stringsArray("All.")})
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
	return out
}()
