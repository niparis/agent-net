package gateway

// learnedTablesMarker names the migration in the migrate loop, which adds the
// columns below before the statement runs.
const learnedTablesMarker = "learned tables rework"

// learnedTablesColumns are the columns the rework adds. The loop adds each one
// only when it is missing, so a rewound database keeps the values it has.
var learnedTablesColumns = []struct{ table, name, spec string }{
	{"messages", "owner_prompt", `TEXT NOT NULL DEFAULT ''`},
	{"sessions", "owner_prompt", `TEXT NOT NULL DEFAULT ''`},
	{"sessions", "owner_prompt_at", `INTEGER NOT NULL DEFAULT 0`},
	{"learned_suggestions", "learned_seen", `INTEGER NOT NULL DEFAULT 0`},
	{"friction", "status", `TEXT NOT NULL DEFAULT 'new'`},
	{"friction", "status_by", `TEXT NOT NULL DEFAULT ''`},
	{"friction", "status_at", `INTEGER NOT NULL DEFAULT 0`},
	{"friction", "status_link", `TEXT NOT NULL DEFAULT ''`},
	{"friction", "status_note", `TEXT NOT NULL DEFAULT ''`},
}

// learnedTablesMigration moves the facts of six tables into columns and drops
// the tables. They held copies, caches or rate limits (owner review of
// 2026-11-21). Friction keeps the latest mark of each report, so older marks
// go with friction_status_history.
const learnedTablesMigration = `
-- learned tables rework: copy each fact into its column, then drop the six tables.
UPDATE messages SET owner_prompt = (SELECT cs.prompt FROM charter_signals cs WHERE cs.message_id = messages.id)
 WHERE EXISTS (SELECT 1 FROM charter_signals cs WHERE cs.message_id = messages.id AND cs.prompt <> '');
UPDATE sessions SET owner_prompt = (SELECT sp.prompt FROM signal_prompts sp WHERE sp.session_id = sessions.id),
 owner_prompt_at = (SELECT sp.at FROM signal_prompts sp WHERE sp.session_id = sessions.id)
 WHERE id IN (SELECT session_id FROM signal_prompts);
UPDATE learned_suggestions SET learned_seen = 1
 WHERE status = 'applied' AND history_id > 0
 AND EXISTS (SELECT 1 FROM learned_views v WHERE v.node = learned_suggestions.node AND learned_suggestions.history_id <= v.last_seen_id);
UPDATE friction SET status = h.status, status_by = h.changed_by, status_at = h.changed_at, status_link = h.link, status_note = h.note
 FROM friction_status_history h
 WHERE h.friction_id = friction.id AND h.id = (SELECT MAX(id) FROM friction_status_history WHERE friction_id = friction.id);
CREATE INDEX IF NOT EXISTS messages_owner_prompt ON messages (created_at) WHERE owner_prompt <> '';
DROP INDEX IF EXISTS friction_read;
DROP INDEX IF EXISTS friction_project_id;
DROP INDEX IF EXISTS friction_area_id;
CREATE INDEX friction_read ON friction(id DESC, project, area, created_at, session, change_id, harness, model, text, status, status_by, status_at, status_link, status_note);
CREATE INDEX friction_project_id ON friction(project, id DESC, area, created_at, session, change_id, harness, model, text, status, status_by, status_at, status_link, status_note);
CREATE INDEX friction_area_id ON friction(area, id DESC, project, created_at, session, change_id, harness, model, text, status, status_by, status_at, status_link, status_note);
DROP TABLE IF EXISTS charter_signals;
DROP TABLE IF EXISTS signal_prompts;
DROP TABLE IF EXISTS learned_message_flow;
DROP TABLE IF EXISTS learned_phrase_runs;
DROP TABLE IF EXISTS learned_views;
DROP TABLE IF EXISTS friction_status_history;
`
