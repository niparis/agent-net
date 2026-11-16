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

// skillArchiveChangePreviousMigration records the latest version before a
// change, so an undo can put it back. The column itself is added in the
// migration loop, only when absent.
const skillArchiveChangePreviousMigration = `SELECT 1 /* skill archive change previous column */;`

// skillArchiveLatestMigration adds the latest choice of each name (decision
// 0169). An intake copy of a name that already has a live version does not
// move it; an explicit add, a new version or a promote does. Before this table,
// the latest was the highest live version. The backfill keeps that, except for
// the project, plugin and agent-net copies that intake added after a name's
// first version: those were the copies that took over the latest, so they
// become candidates. A machine copy looks like an explicit machine add, so it
// keeps the highest-version rule.
const skillArchiveLatestMigration = `
CREATE TABLE IF NOT EXISTS skill_archive_latest (
 name TEXT PRIMARY KEY,
 version INTEGER NOT NULL);
INSERT OR IGNORE INTO skill_archive_latest(name, version)
 SELECT a.name, MAX(a.version) FROM skill_archive a
 WHERE a.removed_at=0 AND NOT (a.source_kind IN ('project','plugin','agent-net')
  AND a.version > (SELECT MIN(b.version) FROM skill_archive b WHERE b.name=a.name AND b.removed_at=0))
 GROUP BY a.name;
`

// skillArchiveGuardMigration keeps removals and splits the token figure
// (decision 0169). skill_archive_blocks holds what intake must not store
// again: one row per removed content (name and hash), and one row with an empty
// hash for a name removed as a whole. An explicit add clears them. The backfill
// blocks what was removed before this migration. context_tokens is the name
// plus the description in tokens; the column itself is added in the migration
// loop, only when absent, before this text runs. The old tokens column keeps
// the size of the whole bundle.
const skillArchiveGuardMigration = `
-- skill archive context tokens column
CREATE TABLE IF NOT EXISTS skill_archive_blocks (
 name TEXT NOT NULL,
 hash TEXT NOT NULL DEFAULT '',
 actor TEXT NOT NULL DEFAULT '',
 at INTEGER NOT NULL,
 PRIMARY KEY(name, hash));
UPDATE skill_archive SET context_tokens=(length(CAST(name AS BLOB))+length(CAST(description AS BLOB))+3)/4 WHERE context_tokens=0;
INSERT OR IGNORE INTO skill_archive_blocks(name, hash, actor, at)
 SELECT a.name, a.hash,
  COALESCE((SELECT c.actor FROM skill_archive_changes c WHERE c.name=a.name AND c.action='remove' AND c.undone_at=0 ORDER BY c.at DESC LIMIT 1), ''),
  a.removed_at
 FROM skill_archive a WHERE a.removed_at<>0;
INSERT OR IGNORE INTO skill_archive_blocks(name, hash, actor, at)
 SELECT a.name, '',
  COALESCE((SELECT c.actor FROM skill_archive_changes c WHERE c.name=a.name AND c.action='remove' AND c.undone_at=0 ORDER BY c.at DESC LIMIT 1), ''),
  MAX(a.removed_at)
 FROM skill_archive a GROUP BY a.name HAVING SUM(a.removed_at=0)=0;
`
