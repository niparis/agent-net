package gateway

// machineTablesMarker names the migration in the migrate loop.
const machineTablesMarker = "machine and transfer rework"

var machineTablesColumns = []struct{ table, name, spec string }{
	{"nodes", "state", `TEXT NOT NULL DEFAULT 'enrolled'`},
	{"nodes", "code_hash", `TEXT NOT NULL DEFAULT ''`},
	{"nodes", "expires_at", `INTEGER NOT NULL DEFAULT 0`},
	{"nodes", "used_at", `INTEGER NOT NULL DEFAULT 0`},
	{"transfers", "parts_received", `TEXT NOT NULL DEFAULT '[]'`},
}

// machineTablesMigration copies enroll_codes into nodes invite rows and
// transfer_parts into transfers.parts_received, then drops the two tables.
const machineTablesMigration = `
-- machine and transfer rework: enroll_codes into nodes, transfer_parts into transfers.
CREATE TABLE IF NOT EXISTS enroll_codes (code_hash TEXT PRIMARY KEY, expires_at INTEGER NOT NULL, used_at INTEGER);
CREATE TABLE IF NOT EXISTS transfer_parts (id TEXT NOT NULL, idx INTEGER NOT NULL, size INTEGER NOT NULL, sha256 TEXT NOT NULL, at INTEGER NOT NULL, PRIMARY KEY(id, idx));
INSERT INTO nodes (id, hostname, reported_hostname, os, token_hash, created_at, state, code_hash, expires_at, used_at)
SELECT 'n_inv_' || lower(hex(randomblob(6))), '', '', '', 'invited:' || lower(hex(randomblob(8))), expires_at, 'invited', code_hash, expires_at, COALESCE(used_at, 0)
FROM enroll_codes WHERE code_hash NOT IN (SELECT code_hash FROM nodes WHERE code_hash <> '');
UPDATE transfers SET parts_received = (
	SELECT COALESCE(json_group_array(json_object('idx', idx, 'size', size, 'sha256', sha256, 'at', at)), '[]')
	FROM (SELECT idx, size, sha256, at FROM transfer_parts WHERE transfer_parts.id = transfers.id ORDER BY idx)
) WHERE EXISTS (SELECT 1 FROM transfer_parts WHERE transfer_parts.id = transfers.id);
CREATE INDEX IF NOT EXISTS nodes_code_hash ON nodes (code_hash) WHERE code_hash <> '';
DROP TABLE IF EXISTS enroll_codes;
DROP TABLE IF EXISTS transfer_parts;
`
