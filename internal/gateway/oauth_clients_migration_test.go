package gateway

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// The migration copies each client onto its app, keeps a client with no app as
// a registered row, and drops oauth_clients and app_refresh_budget.
func TestOAuthClientsMigrationMovesEachClient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s := migrateOnly(t, path, migrationVersion(t, oauthClientsMarker)-1)
	defer s.Close()
	for _, q := range []string{
		`INSERT INTO oauth_clients(client_id,client_name,redirect_uris,grant_types,response_types,token_endpoint_auth_method,created_at,source) VALUES
			('c_app','Cursor','["https://a.example/cb"]','["authorization_code","refresh_token"]','["code"]','none',100,'src-a'),
			('c_wait','Waiting','["https://b.example/cb"]','["authorization_code"]','["code"]','none',200,'src-b'),
			('c_wait2','Waiting too','["https://c.example/cb"]','["authorization_code"]','["code"]','none',300,'src-c'),
			('phone','Phone','[]','[]','[]','none',50,'')`,
		`INSERT INTO apps(id,name,kind,client_id,client_name,redirect_uris,created_at) VALUES
			('app_1','Trade bot','bot','c_app','Cursor','["https://a.example/cb"]',150),
			('app_2','Mac Safari','owner_app','phone','Mac','[]',60)`,
		`INSERT INTO app_refresh_budget(family_id,window_started_at,rotations) VALUES ('fam',1,7)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	if err := replayMigrations(t, s); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"oauth_clients", "app_refresh_budget"} {
		if tableExists(t, s, name) {
			t.Errorf("%s still exists", name)
		}
	}
	type row struct{ name, kind, state, grants, responses, method, source string }
	read := func(where string, args ...any) row {
		t.Helper()
		var r row
		err := s.db.QueryRow(`SELECT name,kind,state,grant_types,response_types,token_endpoint_auth_method,source FROM apps WHERE `+where, args...).Scan(&r.name, &r.kind, &r.state, &r.grants, &r.responses, &r.method, &r.source)
		if err != nil {
			t.Fatalf("read %s: %v", where, err)
		}
		return r
	}
	if got := read(`id='app_1'`); got != (row{"Trade bot", "bot", "approved", `["authorization_code","refresh_token"]`, `["code"]`, "none", "src-a"}) {
		t.Errorf("client merged into its app: %+v", got)
	}
	if got := read(`client_id='c_wait'`); got != (row{"", "owner_app", "registered", `["authorization_code"]`, `["code"]`, "none", "src-b"}) {
		t.Errorf("client with no app: %+v", got)
	}
	if got := read(`id='app_2'`); got.state != "approved" || got.grants != "[]" {
		t.Errorf("phone app: %+v", got)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM apps WHERE client_id='phone'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("phone rows: %d %v", n, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM apps WHERE state='registered'`).Scan(&n); err != nil || n != 2 {
		t.Errorf("registered rows: %d %v", n, err)
	}
	// Two registered rows share the empty name, and an approved app still needs a unique one.
	if _, err := s.db.Exec(`INSERT INTO apps(id,name,kind,client_id,client_name,created_at) VALUES ('app_3','Trade bot','bot','x','x',1)`); err == nil {
		t.Error("duplicate active app name accepted")
	}
	apps, err := s.ListApps()
	if err != nil || len(apps) != 2 {
		t.Errorf("apps after migration: %+v %v", apps, err)
	}
}

// A client that waits for approval is not an app: the owner's operations and
// the token issuer ignore it, whatever its ID.
func TestRegisteredClientIsNotAnApp(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	var id string
	if err := s.db.QueryRow(`SELECT id FROM apps WHERE client_id=?`, client).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueToken(id, "access", time.Hour); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("token for a registered client: %v", err)
	}
	if err := s.RenameApp(id, "named"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("rename: %v", err)
	}
	if err := s.SetAppGrant(id, "mcp server demo", false); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("ungrant: %v", err)
	}
	if err := s.RevokeApp(id); err != nil {
		t.Fatal(err)
	}
	var state string
	var revoked sql.NullInt64
	if err := s.db.QueryRow(`SELECT state,revoked_at FROM apps WHERE id=?`, id).Scan(&state, &revoked); err != nil || state != "registered" || revoked.Valid {
		t.Errorf("revoke touched a registered client: %s %v %v", state, revoked, err)
	}
	if apps, err := s.ListApps(); err != nil || len(apps) != 0 {
		t.Errorf("apps: %+v %v", apps, err)
	}

	// Approval replaces the registered row with the app, which keeps its client data.
	_, pending := oauthPage(t, oauthAuthorize(h, client, nil))
	if err := s.ApprovePending(pending, "bot", "now an app", c.t); err != nil {
		t.Fatal(err)
	}
	apps, err := s.ListApps()
	if err != nil || len(apps) != 1 || apps[0].Name != "now an app" || apps[0].ClientID != client {
		t.Errorf("apps after approval: %+v %v", apps, err)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT count(*) FROM apps WHERE client_id=?`, client).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("rows for the client after approval: %d %v", rows, err)
	}
}

// Each phone pairing is its own app with its own name, and no client row is
// needed for it.
func TestPhonePairingsKeepTheirOwnNames(t *testing.T) {
	s, c := newTestStore(t)
	for _, device := range []string{"iPhone", "iPad"} {
		p, err := s.newPhonePending(device, "src-"+device, c.t)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := s.PendingByUserCode(p.UserCode, "src-"+device, c.t); err != nil || got.ClientName != device {
			t.Fatalf("pending lookup: %+v %v", got, err)
		}
		if err := s.ApprovePending(p.ID, "owner_app", "my "+device, c.t); err != nil {
			t.Fatal(err)
		}
	}
	apps, err := s.ListApps()
	if err != nil || len(apps) != 2 {
		t.Fatalf("phone apps: %+v %v", apps, err)
	}
	for _, a := range apps {
		if a.ClientID != "phone" || a.Name != "my "+a.ClientName {
			t.Errorf("phone app: %+v", a)
		}
	}
}
