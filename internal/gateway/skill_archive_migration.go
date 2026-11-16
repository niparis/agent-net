package gateway

// skillArchiveMigration adds the skill archive (decision 0169). It holds plain
// rows: the owner asked for a database, not a Git repository. It is appended at
// the end of the migration list and never edited after it ships.
const skillArchiveMigration = `
-- Skill archive (decision 0169): one row per stored skill version.
CREATE TABLE IF NOT EXISTS skill_archive (
 name TEXT NOT NULL,
 version INTEGER NOT NULL,
 hash TEXT NOT NULL,
 content BLOB NOT NULL,
 description TEXT NOT NULL DEFAULT '',
 tokens INTEGER NOT NULL DEFAULT 0,
 source_kind TEXT NOT NULL DEFAULT 'machine',
 source_node TEXT NOT NULL DEFAULT '',
 source_path TEXT NOT NULL DEFAULT '',
 source_url TEXT NOT NULL DEFAULT '',
 source_commit TEXT NOT NULL DEFAULT '',
 source_session TEXT NOT NULL DEFAULT '',
 added_by TEXT NOT NULL DEFAULT '',
 added_at INTEGER NOT NULL,
 removed_at INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(name, version));
-- Same name and same content is one version, wherever it came from.
CREATE UNIQUE INDEX IF NOT EXISTS skill_archive_content ON skill_archive(name, hash);
CREATE INDEX IF NOT EXISTS skill_archive_live ON skill_archive(name, removed_at);
-- Every add and remove, so the owner can undo it.
CREATE TABLE IF NOT EXISTS skill_archive_changes (
 id TEXT PRIMARY KEY,
 action TEXT NOT NULL,
 name TEXT NOT NULL,
 version INTEGER NOT NULL DEFAULT 0,
 versions TEXT NOT NULL DEFAULT '[]',
 restored INTEGER NOT NULL DEFAULT 0,
 actor TEXT NOT NULL DEFAULT '',
 at INTEGER NOT NULL,
 undone_at INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS skill_archive_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '');
`

// skillArchiveSourceProjectMigration records the registered project a project
// skill folder came from, so its source line can name the project. The column
// itself is added in the migration loop, only when absent.
const skillArchiveSourceProjectMigration = `SELECT 1 /* skill archive source project column */;`
