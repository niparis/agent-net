package gateway

// skillListsMigration adds the inherited skill lists on graph nodes
// (decision 0169, task 3). It is appended at the end of the migration list and
// never edited after it ships.
const skillListsMigration = `
-- Skill lists on global, team, repo and agent nodes. One row is one name on
-- one node: an add with an optional pin, or a remove that beats higher adds.
CREATE TABLE IF NOT EXISTS skill_node_lists (
 level TEXT NOT NULL,
 scope TEXT NOT NULL DEFAULT '',
 name TEXT NOT NULL,
 action TEXT NOT NULL,
 pin INTEGER NOT NULL DEFAULT 0,
 added_by TEXT NOT NULL DEFAULT '',
 added_at INTEGER NOT NULL,
 PRIMARY KEY(level, scope, name));
CREATE INDEX IF NOT EXISTS skill_node_lists_node ON skill_node_lists(level, scope);
-- Each set of one node's list, with the state before and after, so the owner
-- can undo it.
CREATE TABLE IF NOT EXISTS skill_list_changes (
 id TEXT PRIMARY KEY,
 level TEXT NOT NULL,
 scope TEXT NOT NULL DEFAULT '',
 before TEXT NOT NULL,
 after TEXT NOT NULL,
 actor TEXT NOT NULL,
 at INTEGER NOT NULL,
 undone_at INTEGER NOT NULL DEFAULT 0);
-- A suggestion the CTO can apply by id later.
CREATE TABLE IF NOT EXISTS skill_suggestions (
 id TEXT PRIMARY KEY,
 level TEXT NOT NULL,
 scope TEXT NOT NULL DEFAULT '',
 data TEXT NOT NULL,
 actor TEXT NOT NULL,
 at INTEGER NOT NULL);
`
