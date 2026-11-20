package gateway

// planKindsMigration merges the three plan tables into plans, keyed by kind,
// and the two OAuth rate-limit tables into oauth_attempts, also keyed by kind
// (schema reduction, part 7). Every row is copied with its kind, then the old
// tables are dropped, all in the migration's transaction.
//
// The old tables are created again first, IF NOT EXISTS, so the copy can run
// on a replay: after a rewind the tables are gone, the copy reads an empty
// table and copies nothing. The old tables are not used after this migration.
const planKindsMigration = `
-- One table for the plans that change tools and MCP entries (kind tool,
-- mcp_import or mcp_native), and one for OAuth rate-limit records (kind
-- code_miss or registration).
CREATE TABLE IF NOT EXISTS tool_plans (id TEXT PRIMARY KEY, item TEXT NOT NULL, snapshot TEXT NOT NULL, plan TEXT NOT NULL, applied INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS mcp_import_plans (id TEXT PRIMARY KEY, snapshot TEXT NOT NULL, plan TEXT NOT NULL, applied INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS mcp_native_plans (id TEXT PRIMARY KEY, snapshot TEXT NOT NULL, plan TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'pending');
CREATE TABLE IF NOT EXISTS oauth_code_misses (source TEXT NOT NULL, attempted_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS oauth_registration_attempts (source TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS plans (
 kind TEXT NOT NULL CHECK (kind IN ('tool', 'mcp_import', 'mcp_native')),
 id TEXT NOT NULL,
 item TEXT NOT NULL DEFAULT '',
 snapshot TEXT NOT NULL,
 plan TEXT NOT NULL,
 applied INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT '',
 PRIMARY KEY (kind, id));
INSERT INTO plans(kind,id,item,snapshot,plan,applied,state) SELECT 'tool',id,item,snapshot,plan,applied,'' FROM tool_plans;
INSERT INTO plans(kind,id,item,snapshot,plan,applied,state) SELECT 'mcp_import',id,'',snapshot,plan,applied,'' FROM mcp_import_plans;
INSERT INTO plans(kind,id,item,snapshot,plan,applied,state) SELECT 'mcp_native',id,'',snapshot,plan,0,state FROM mcp_native_plans;
DROP TABLE IF EXISTS tool_plans;
DROP TABLE IF EXISTS mcp_import_plans;
DROP TABLE IF EXISTS mcp_native_plans;
CREATE TABLE IF NOT EXISTS oauth_attempts (
 kind TEXT NOT NULL CHECK (kind IN ('code_miss', 'registration')),
 source TEXT NOT NULL,
 at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS oauth_attempts_source ON oauth_attempts (kind, source, at);
INSERT INTO oauth_attempts(kind,source,at) SELECT 'code_miss',source,attempted_at FROM oauth_code_misses;
INSERT INTO oauth_attempts(kind,source,at) SELECT 'registration',source,created_at FROM oauth_registration_attempts;
DROP TABLE IF EXISTS oauth_code_misses;
DROP TABLE IF EXISTS oauth_registration_attempts;
`
