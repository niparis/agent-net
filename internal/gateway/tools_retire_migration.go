package gateway

// toolsRetireMarker names the migration in the migrate loop, which adds the
// column below before the statement runs.
const toolsRetireMarker = "tools retire"

// toolsRetireColumns are the columns this migration adds. The loop adds each
// one only when it is missing, so a rewound database keeps the values it has.
var toolsRetireColumns = []migrationColumn{
	{"skill_archive", "in_use", `INTEGER NOT NULL DEFAULT 0`},
}

// toolsRetireMigration retires the old Tools rollout (decision 0169, task 5)
// and folds two skill tables.
//
// The rollout tables hold nothing the archive lacks: the archive already holds
// every skill, and the owner reviewed the rest on 2026-11-21. They are dropped
// without a copy. inventory_reports fed only the Tools tab, so it goes too.
//
// skill_archive_latest becomes the column in_use of skill_archive. One version
// of a name has in_use 1, the version the old row named, even when it is not
// the newest. skill_archive_changes and skill_list_changes become one undo log,
// skill_changes, told apart by kind.
//
// The three source tables of the folds are created again first, IF NOT EXISTS,
// so the copy can run on a replay: after a rewind they are gone, and the copy
// then reads an empty table and copies nothing.
const toolsRetireMigration = `
-- tools retire: fold skill_archive_latest into skill_archive.in_use
CREATE TABLE IF NOT EXISTS skill_archive_latest (name TEXT PRIMARY KEY, version INTEGER NOT NULL);
UPDATE skill_archive SET in_use=0 WHERE in_use=1 AND name IN (SELECT name FROM skill_archive_latest);
UPDATE skill_archive SET in_use=1 WHERE EXISTS (SELECT 1 FROM skill_archive_latest l WHERE l.name=skill_archive.name AND l.version=skill_archive.version);
CREATE UNIQUE INDEX IF NOT EXISTS skill_archive_in_use ON skill_archive(name) WHERE in_use=1;
DROP TABLE IF EXISTS skill_archive_latest;
-- fold the two undo logs into skill_changes
CREATE TABLE IF NOT EXISTS skill_archive_changes (id TEXT PRIMARY KEY, action TEXT NOT NULL, name TEXT NOT NULL, version INTEGER NOT NULL DEFAULT 0, versions TEXT NOT NULL DEFAULT '[]', restored INTEGER NOT NULL DEFAULT 0, actor TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL, undone_at INTEGER NOT NULL DEFAULT 0, previous INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS skill_list_changes (id TEXT PRIMARY KEY, level TEXT NOT NULL, scope TEXT NOT NULL DEFAULT '', before TEXT NOT NULL, after TEXT NOT NULL, actor TEXT NOT NULL, at INTEGER NOT NULL, undone_at INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS skill_changes (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK (kind IN ('archive', 'list')),
 action TEXT NOT NULL DEFAULT '',
 name TEXT NOT NULL DEFAULT '',
 version INTEGER NOT NULL DEFAULT 0,
 versions TEXT NOT NULL DEFAULT '[]',
 restored INTEGER NOT NULL DEFAULT 0,
 previous INTEGER NOT NULL DEFAULT 0,
 level TEXT NOT NULL DEFAULT '',
 scope TEXT NOT NULL DEFAULT '',
 before TEXT NOT NULL DEFAULT '',
 after TEXT NOT NULL DEFAULT '',
 actor TEXT NOT NULL DEFAULT '',
 at INTEGER NOT NULL,
 undone_at INTEGER NOT NULL DEFAULT 0);
INSERT OR IGNORE INTO skill_changes(id,kind,action,name,version,versions,restored,previous,actor,at,undone_at)
 SELECT id,'archive',action,name,version,versions,restored,previous,actor,at,undone_at FROM skill_archive_changes;
INSERT OR IGNORE INTO skill_changes(id,kind,level,scope,before,after,actor,at,undone_at)
 SELECT id,'list',level,scope,before,after,actor,at,undone_at FROM skill_list_changes;
DROP TABLE IF EXISTS skill_archive_changes;
DROP TABLE IF EXISTS skill_list_changes;
-- the copy of skills.git into the archive is gone: drop its two markers
DELETE FROM gateway_state WHERE key IN ('skill_archive.git_intake_done','skill_archive.git_intake_at');
-- the old Tools rollout
DROP TABLE IF EXISTS tool_identities;
DROP TABLE IF EXISTS tool_locations;
DROP TABLE IF EXISTS tool_revisions;
DROP TABLE IF EXISTS tool_results;
DROP TABLE IF EXISTS tool_audit;
DROP TABLE IF EXISTS tool_project_settings;
DROP TABLE IF EXISTS tool_project_seen;
DROP TABLE IF EXISTS tool_accepted;
DROP TABLE IF EXISTS plans;
DROP TABLE IF EXISTS skill_versions;
DROP TABLE IF EXISTS mcp_imports;
DROP TABLE IF EXISTS inventory_reports;
`
