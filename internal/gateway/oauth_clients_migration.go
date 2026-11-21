package gateway

// oauthClientsMarker names the migration in the migrate loop, which adds the
// client columns before the statement runs.
const oauthClientsMarker = "oauth clients into apps"

// oauthClientsColumns are the client columns that apps gains. state is
// 'registered' for a client the owner has not approved yet, and 'approved' for
// every app.
var oauthClientsColumns = []migrationColumn{
	{"apps", "grant_types", `TEXT NOT NULL DEFAULT '[]'`},
	{"apps", "response_types", `TEXT NOT NULL DEFAULT '[]'`},
	{"apps", "token_endpoint_auth_method", `TEXT NOT NULL DEFAULT 'none'`},
	{"apps", "source", `TEXT NOT NULL DEFAULT ''`},
	{"apps", "state", `TEXT NOT NULL DEFAULT 'approved' CHECK (state IN ('registered','approved'))`},
}

// oauthClientsMigration merges oauth_clients into apps and drops the refresh
// budget (owner review of 2026-11-21). A client with no app is a 'registered'
// row with an empty name and a placeholder kind. The 'phone' client held
// nothing and is not copied. The rotation count is in the used refresh hashes
// of app_tokens.
const oauthClientsMigration = `
-- oauth clients into apps
DROP INDEX IF EXISTS apps_active_name;
CREATE UNIQUE INDEX apps_active_name ON apps (name) WHERE revoked_at IS NULL AND state = 'approved';
CREATE INDEX IF NOT EXISTS apps_client ON apps (client_id);
UPDATE apps SET grant_types = c.grant_types, response_types = c.response_types, token_endpoint_auth_method = c.token_endpoint_auth_method, source = c.source
 FROM oauth_clients c WHERE c.client_id = apps.client_id;
INSERT INTO apps (id, name, kind, client_id, client_name, redirect_uris, created_at, grants, grant_types, response_types, token_endpoint_auth_method, source, state)
 SELECT 'app_' || lower(hex(randomblob(8))), '', 'owner_app', c.client_id, c.client_name, c.redirect_uris, c.created_at, '[]', c.grant_types, c.response_types, c.token_endpoint_auth_method, c.source, 'registered'
 FROM oauth_clients c WHERE c.client_id <> 'phone' AND NOT EXISTS (SELECT 1 FROM apps a WHERE a.client_id = c.client_id);
DROP TABLE IF EXISTS oauth_clients;
DROP TABLE IF EXISTS app_refresh_budget;
`
