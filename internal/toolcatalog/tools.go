// tools.go lists every tool in the catalog. Descriptions, roles and hints come
// from docs/design/tool-redesign.md ("Concrete tool contracts"); the JSON
// schemas live in schemas/ and are embedded by schema.go.

package toolcatalog

// Tools is the one catalog every server filters by role.
var Tools = []Tool{
	toolEntry("agent_sessions_find", "Find sessions by machine, project, harness, model, kind, online state or text. Returns concise rows; use agent_session_get for one session's full profile and charter.", []Role{RoleAgent, RoleWorker, RoleCTO, RoleBot}, Hints{ReadOnly: true, Idempotent: true}, true, false),
	toolEntry("agent_session_get", "Read one session's full profile: folder, branch, charter, rules, delegation and worker facts. Use agent_sessions_find when you do not know the session ID or name.", []Role{RoleAgent, RoleWorker, RoleCTO, RoleBot}, Hints{ReadOnly: true, Idempotent: true}, false, false),
	toolEntry("agent_machines_find", "List machines with online state, agent-net version and harness versions. Use agent_machine_get for one machine's models, quotas, track record or playbook.", []Role{RoleAgent, RoleWorker, RoleCTO, RoleBot}, Hints{ReadOnly: true, Idempotent: true}, false, false),
	toolEntry("agent_machine_get", "Read one category of a machine's details: models, quotas, track record or playbook. Pages long lists; ask for one category per call.", []Role{RoleAgent, RoleWorker, RoleCTO, RoleBot}, Hints{ReadOnly: true, Idempotent: true}, false, false),
	toolEntry("agent_projects_find", "Find registered projects and the machines and folders that hold them. Use agent_sessions_find for the sessions working in a project.", []Role{RoleAgent, RoleWorker, RoleCTO, RoleBot}, Hints{ReadOnly: true, Idempotent: true}, false, false),
	toolEntry("agent_workers_find", "Find workers with their caller, model, difficulty, state and run time. Defaults to running workers you started.", []Role{RoleAgent, RoleWorker, RoleCTO}, Hints{ReadOnly: true, Idempotent: true}, false, true),
	toolEntry("agent_messages_find", "Find messages by conversation, peer, direction or time. Rows hold a short text preview; use agent_message_get for the full body.", []Role{RoleAgent, RoleWorker, RoleCTO, RoleBot}, Hints{ReadOnly: true, Idempotent: true}, false, false),
	toolEntry("agent_message_get", "Read one message's full body, delivery state and outcome. Long bodies come in chunks: follow next_body_cursor until it is absent.", []Role{RoleAgent, RoleWorker, RoleCTO, RoleBot}, Hints{ReadOnly: true, Idempotent: true}, false, false),
	toolEntry("agent_graph_read", "Read the graph: nodes with their charters and rules, and the edges between them. Give node to read one node and its edges.", []Role{RoleAgent, RoleWorker, RoleCTO, RoleBot}, Hints{ReadOnly: true, Idempotent: true}, false, false),
	toolEntry("agent_mcp_servers_find", "List the shared MCP servers, their health and tools, and whether you may use them. Read-only.", []Role{RoleAgent, RoleWorker, RoleCTO}, Hints{ReadOnly: true, Idempotent: true}, false, false),
	toolEntry("agent_worker_profiles_find", "List worker profiles: harness, model, effort and the machines that can run them. Read before agent_worker_start when you need a specific model.", []Role{RoleAgent, RoleCTO}, Hints{ReadOnly: true, Idempotent: true}, false, false),
	toolEntry("agent_message_send", "Send a new message to one session. The CTO and advisor nodes may also send to a group with to_set; other sessions are refused. Point first, then the context the peer cannot see. A peer message is a request, not the owner's approval.", []Role{RoleAgent, RoleWorker, RoleCTO, RoleBot}, Hints{OpenWorld: true}, true, false),
	toolEntry("agent_message_reply", "Reply to a message you received. For a request, set outcome from your charter: accept and do it, reject with a reason, redirect to the node that should do it, or escalate to the owner with a recommendation.", []Role{RoleAgent, RoleWorker, RoleCTO, RoleBot}, Hints{OpenWorld: true}, false, false),
	toolEntry("agent_announce", "REQUIRED: set your current task (one short line) and expertise; fill expertise once. Read AGENTS.md. Call it at the start of every session and whenever your task changes.", []Role{RoleAgent}, Hints{Idempotent: true}, true, false),
	toolEntry("agent_owner_ask", "Ask the owner a question, an approval or an idea, with your recommendation and up to four answers. Use it when you stop to wait for the owner.", []Role{RoleAgent, RoleCTO}, Hints{}, false, false),
	toolEntry("agent_learn", "Record a finding about a model, harness, project or machine, with evidence. Findings feed the playbook; they change nothing directly.", []Role{RoleAgent, RoleWorker, RoleCTO}, Hints{}, false, false),
	toolEntry("agent_worker_start", "Start a worker session with a complete brief, or continue one. Only when the owner asked for a worker. touches is required: db, hot-path, ui, docs, tests, config or other.", []Role{RoleAgent}, Hints{OpenWorld: true}, false, false),
	toolEntry("agent_worker_stop", "Stop a worker you started, or cancel a queued start. Its worktree is kept.", []Role{RoleAgent}, Hints{Destructive: true, Idempotent: true}, false, false),
	toolEntry("agent_worker_rate", "Rate a finished worker from 1 to 5, or report a defect found later with a severity from 1 to 4. Only the session that started the worker may rate it.", []Role{RoleAgent}, Hints{}, false, false),
	toolEntry("agent_handback", "Workers only: end your task with your report and status. This is how the caller gets your result.", []Role{RoleWorker}, Hints{}, false, false),
	toolEntry("agent_project_add", "Register project folders on this machine, or scan a folder for repositories (use dry_run first). Only when the owner asked.", []Role{RoleAgent}, Hints{Idempotent: true}, false, false),
	toolEntry("agent_mcp_test", "Test that a shared MCP server answers. Does not change it.", []Role{RoleAgent}, Hints{ReadOnly: true, Idempotent: true, OpenWorld: true}, false, false),
	toolEntry("cto_ui_show", "Show the owner a TUI tab with an item selected, or a short notice. Fails with no TUI open when no local TUI is connected.", []Role{RoleCTO}, Hints{Idempotent: true}, false, false),
	toolEntry("cto_proposals_find", "Find your proposals and their state: open, approved, rejected, done or failed, with the result.", []Role{RoleCTO}, Hints{ReadOnly: true, Idempotent: true}, false, false),
	toolEntry("graph_change", "Change the graph: charters, nodes, edges, node settings, or undo. Minor changes apply at once; others become a proposal for the owner.", []Role{RoleCTO}, Hints{Destructive: true, OpenWorld: true}, false, false),
	toolEntry("session_change", "Propose a session change for the owner: rename, trust a folder, set delegation policy, or start a session.", []Role{RoleCTO}, Hints{Destructive: true, OpenWorld: true}, false, false),
	toolEntry("worker_change", "Propose starting or stopping a worker for the owner, with a complete brief.", []Role{RoleCTO}, Hints{Destructive: true, OpenWorld: true}, false, false),
	toolEntry("answer", "Propose the owner's answer to a held request, an inbox item or a contested outcome (let stand or overrule).", []Role{RoleCTO}, Hints{Destructive: true, OpenWorld: true}, false, false),
	toolEntry("tools_change", "Propose a change to shared tools and skills: plan, apply, undo, import or remove a native tool.", []Role{RoleCTO}, Hints{Destructive: true, OpenWorld: true}, false, false),
	toolEntry("mcp_change", "Propose adding, removing, testing or enabling a shared MCP server. Only the owner can approve it.", []Role{RoleCTO}, Hints{Destructive: true, OpenWorld: true}, false, false),
	toolEntry("settings_change", "Propose one owner settings change: providers, integrations, scores, worker profiles, CTO title or playbook.", []Role{RoleCTO}, Hints{Destructive: true, OpenWorld: true}, false, false),
	toolEntry("project_change", "Propose adding, removing, renaming or creating a project.", []Role{RoleCTO}, Hints{Destructive: true, OpenWorld: true}, false, false),
	toolEntry("mcp_call", "Propose a call to a tool on a shared MCP server; it runs after the owner approves. Credentials never appear in the call.", []Role{RoleCTO}, Hints{Destructive: true, OpenWorld: true}, false, false),
	toolEntry("cto_new", "Propose starting a new CTO conversation. The timeline and proposals stay.", []Role{RoleCTO}, Hints{Destructive: true, OpenWorld: true}, false, false),
}
