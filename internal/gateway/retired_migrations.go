package gateway

// Retired migrations. The code that used these tables is gone, but a shipped
// migration never changes or moves (testdata/migrations.lock), so its text
// stays here. retiredTablesDropMigration, at the end of the list, drops the
// tables they create.

// sessionDuesMigration created the table of agent_due items. Due items were
// removed; retiredTablesDropMigration drops the table.
const sessionDuesMigration = `
CREATE TABLE IF NOT EXISTS session_dues (
 id TEXT PRIMARY KEY,
 session_id TEXT NOT NULL,
 what TEXT NOT NULL,
 due_at INTEGER NOT NULL DEFAULT 0,
 after_kind TEXT NOT NULL DEFAULT '' CHECK (after_kind IN ('','reply','change_step','ask_closed')),
 after_ref TEXT NOT NULL DEFAULT '',
 blocker TEXT NOT NULL DEFAULT 'none',
 status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','woken','active','raised','done')),
 woken_at INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS session_dues_open ON session_dues(status, due_at);
CREATE INDEX IF NOT EXISTS session_dues_session ON session_dues(session_id, status);
`

// retiredTablesDropMigration drops the tables of four removed subsystems:
// session due items (agent_due), learned charter suggestions, findings
// (agent_learn) and permission proposals. Permissions are now granted only on
// the owner's words, with nothing stored. The older migrations that create or
// change these tables run before this one, so an old database replays through
// them first; IF EXISTS keeps a replayed drop safe. Automatic playbook notes
// came from findings: the unpinned ones go too, and the owner's notes stay.
const retiredTablesDropMigration = `
-- retired subsystems: session dues, learned suggestions, findings, permission proposals
DROP TABLE IF EXISTS session_dues;
DROP TABLE IF EXISTS learned_suggestions;
DROP TABLE IF EXISTS learned_suggestion_details;
DROP TABLE IF EXISTS learned_phrase_state;
DROP TABLE IF EXISTS learned_message_flow;
DROP TABLE IF EXISTS learned_phrase_runs;
DROP TABLE IF EXISTS learned_views;
DROP TABLE IF EXISTS findings;
DROP TABLE IF EXISTS permission_proposals;
DELETE FROM playbook WHERE source='auto' AND pinned=0;
`
