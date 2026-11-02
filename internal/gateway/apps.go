package gateway

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// App is an approved connected app. Kind is fixed when the app is created.
type App struct {
	ID           string
	Name         string
	Kind         string
	ClientID     string
	ClientName   string
	RedirectURIs []string
	CreatedAt    time.Time
	LastUsedAt   *time.Time
	RevokedAt    *time.Time
	Grants       []string
}

const appColumns = "id, name, kind, client_id, client_name, redirect_uris, created_at, last_used_at, revoked_at, grants"

func scanApp(row interface{ Scan(...any) error }) (App, error) {
	var app App
	var uris, grants string
	var created int64
	var used, revoked sql.NullInt64
	if err := row.Scan(&app.ID, &app.Name, &app.Kind, &app.ClientID, &app.ClientName, &uris, &created, &used, &revoked, &grants); err != nil {
		return App{}, err
	}
	if err := json.Unmarshal([]byte(uris), &app.RedirectURIs); err != nil {
		return App{}, err
	}
	if err := json.Unmarshal([]byte(grants), &app.Grants); err != nil {
		return App{}, err
	}
	app.CreatedAt = time.Unix(0, created)
	if used.Valid {
		at := time.Unix(0, used.Int64)
		app.LastUsedAt = &at
	}
	if revoked.Valid {
		at := time.Unix(0, revoked.Int64)
		app.RevokedAt = &at
	}
	return app, nil
}

// CreateApp records an approval. Names are unique among active apps.
func (s *Store) CreateApp(name, kind, clientID, clientName string, redirectURIs []string) (App, error) {
	if kind != "owner_app" && kind != "bot" {
		return App{}, reject("invalid app kind %q", kind)
	}
	if redirectURIs == nil {
		redirectURIs = []string{}
	}
	uris, err := json.Marshal(redirectURIs)
	if err != nil {
		return App{}, err
	}
	app := App{ID: protocol.NewID("app"), Name: name, Kind: kind, ClientID: clientID, ClientName: clientName, RedirectURIs: append([]string{}, redirectURIs...), CreatedAt: s.now(), Grants: []string{}}
	_, err = s.db.Exec("INSERT INTO apps ("+appColumns+") VALUES (?,?,?,?,?,?,?,NULL,NULL,'[]')", app.ID, name, kind, clientID, clientName, string(uris), app.CreatedAt.UnixNano())
	if err != nil {
		return App{}, err
	}
	return app, nil
}

func newAppToken() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}

func issueAppToken(tx *sql.Tx, appID, kind string, now time.Time, ttl time.Duration) (string, error) {
	family, err := oauthRandom(32)
	if err != nil {
		return "", err
	}
	return issueFamilyToken(tx, appID, family, kind, "oauth", now, ttl)
}

func issueFamilyToken(tx *sql.Tx, appID, family, kind, purpose string, now time.Time, ttl time.Duration) (string, error) {
	raw, err := newAppToken()
	if err != nil {
		return "", err
	}
	result, err := tx.Exec("INSERT INTO app_tokens (hash, app_id, type, expires_at, created_at, family_id, purpose) SELECT ?, id, ?, ?, ?, ?, ? FROM apps WHERE id = ? AND revoked_at IS NULL", hashHex(raw), kind, now.Add(ttl).UnixNano(), now.UnixNano(), family, purpose, appID)
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", sql.ErrNoRows
	}
	if kind == "access" {
		if _, err := tx.Exec(`DELETE FROM app_tokens WHERE hash IN (SELECT hash FROM app_tokens WHERE app_id=? AND type='access' ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET 20)`, appID); err != nil {
			return "", err
		}
	}
	return raw, nil
}

// IssueToken returns a 256-bit URL-safe token, storing only its SHA-256 hash.
// A missing or revoked app returns sql.ErrNoRows.
func (s *Store) IssueToken(appID, kind string, ttl time.Duration) (string, error) {
	if kind != "access" && kind != "refresh" {
		return "", reject("invalid app token type %q", kind)
	}
	if ttl <= 0 {
		return "", reject("app token lifetime must be positive")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	raw, err := issueAppToken(tx, appID, kind, s.now(), ttl)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return raw, nil
}

// appForToken also records the use, in the same transaction as validation.
func appForToken(tx *sql.Tx, raw, kind, purpose string, now time.Time) (App, error) {
	app, err := scanApp(tx.QueryRow("SELECT "+appColumns+" FROM apps WHERE revoked_at IS NULL AND id = (SELECT app_id FROM app_tokens WHERE hash = ? AND type = ? AND purpose = ? AND expires_at > ? AND used_at IS NULL)", hashHex(raw), kind, purpose, now.UnixNano()))
	if err != nil {
		return App{}, err
	}
	if _, err := tx.Exec("UPDATE apps SET last_used_at = ? WHERE id = ?", now.UnixNano(), app.ID); err != nil {
		return App{}, err
	}
	app.LastUsedAt = &now
	return app, nil
}

// AppForToken returns sql.ErrNoRows for unknown, expired or revoked tokens,
// including a token presented with the wrong type.
func (s *Store) AppForToken(raw, kind string, now time.Time) (App, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return App{}, err
	}
	defer tx.Rollback()
	app, err := appForToken(tx, raw, kind, "oauth", now)
	if err != nil {
		return App{}, err
	}
	if err := tx.Commit(); err != nil {
		return App{}, err
	}
	return app, nil
}

// RotateRefresh consumes a refresh token once and returns an access token
// (one hour) and a refresh token (90 days). Reuse revokes the token family.
func (s *Store) RotateRefresh(raw string, now time.Time) (access, refresh string, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	var appID, family string
	var used sql.NullInt64
	var expires int64
	err = tx.QueryRow(`SELECT t.app_id,t.family_id,t.used_at,t.expires_at FROM app_tokens t JOIN apps a ON a.id=t.app_id WHERE t.hash=? AND t.type='refresh' AND a.revoked_at IS NULL`, hashHex(raw)).Scan(&appID, &family, &used, &expires)
	if err != nil {
		return "", "", err
	}
	if used.Valid {
		if _, err = tx.Exec(`DELETE FROM app_tokens WHERE family_id=? AND used_at IS NULL`, family); err != nil {
			return "", "", err
		}
		if err = tx.Commit(); err != nil {
			return "", "", err
		}
		s.logf("oauth.refresh_replay", "app", appID)
		return "", "", sql.ErrNoRows
	}
	if expires <= now.UnixNano() {
		return "", "", sql.ErrNoRows
	}
	// A fixed 24-hour window starts at the first rotation for this family.
	var started int64
	var rotations int
	err = tx.QueryRow(`SELECT window_started_at,rotations FROM app_refresh_budget WHERE family_id=?`, family).Scan(&started, &rotations)
	if err != nil && err != sql.ErrNoRows {
		return "", "", err
	}
	if err == sql.ErrNoRows || now.UnixNano()-started >= int64(24*time.Hour) {
		started = now.UnixNano()
		rotations = 0
	}
	if rotations >= 200 {
		if _, err = tx.Exec(`DELETE FROM app_tokens WHERE family_id=? AND used_at IS NULL`, family); err != nil {
			return "", "", err
		}
		if err = tx.Commit(); err != nil {
			return "", "", err
		}
		s.logf("oauth.refresh_rotation_limit", "app", appID)
		return "", "", sql.ErrNoRows
	}
	if _, err = tx.Exec(`INSERT INTO app_refresh_budget(family_id,window_started_at,rotations) VALUES(?,?,?) ON CONFLICT(family_id) DO UPDATE SET window_started_at=excluded.window_started_at,rotations=excluded.rotations`, family, started, rotations+1); err != nil {
		return "", "", err
	}
	if _, err = tx.Exec(`UPDATE app_tokens SET used_at=? WHERE hash=?`, now.UnixNano(), hashHex(raw)); err != nil {
		return "", "", err
	}
	if _, err = tx.Exec(`DELETE FROM app_tokens WHERE hash IN (SELECT hash FROM app_tokens WHERE app_id=? AND type='refresh' AND used_at IS NOT NULL ORDER BY used_at DESC, rowid DESC LIMIT -1 OFFSET 2000)`, appID); err != nil {
		return "", "", err
	}
	access, err = issueFamilyToken(tx, appID, family, "access", "oauth", now, time.Hour)
	if err != nil {
		return "", "", err
	}
	refresh, err = issueFamilyToken(tx, appID, family, "refresh", "oauth", now, 90*24*time.Hour)
	if err != nil {
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

// RevokeApp marks an app revoked and removes all its tokens atomically.
// Repeated revocation preserves the original revocation time.
func (s *Store) RevokeApp(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE apps SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL", s.now().UnixNano(), id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM app_tokens WHERE app_id = ?", id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	closeAppSessions(s, id)
	return nil
}

// RenameApp changes an active app's display name and its synthetic bot session.
func (s *Store) RenameApp(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name cannot be empty")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE apps SET name=? WHERE id=? AND revoked_at IS NULL", name, id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return errors.New("name in use")
		}
		return err
	}
	if n, e := result.RowsAffected(); e != nil {
		return e
	} else if n == 0 {
		return sql.ErrNoRows
	}
	if _, err = tx.Exec("UPDATE sessions SET name=? WHERE harness='bot' AND native_id=?", name, id); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return errors.New("name in use")
		}
		return err
	}
	return nil
}

// ListApps includes revoked apps, ordered by creation time and ID.
func (s *Store) ListApps() ([]App, error) {
	rows, err := s.db.Query("SELECT " + appColumns + " FROM apps ORDER BY created_at, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	apps := []App{}
	for rows.Next() {
		app, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		apps = append(apps, app)
	}
	return apps, rows.Err()
}

// DeleteExpiredTokens removes expired tokens and refresh hashes used 90 days ago.
func (s *Store) DeleteExpiredTokens(now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM app_tokens WHERE (used_at IS NULL AND expires_at <= ?) OR (used_at IS NOT NULL AND used_at <= ?)", now.UnixNano(), now.Add(-90*24*time.Hour).UnixNano()); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM app_tokens WHERE rowid IN (SELECT rowid FROM (SELECT rowid, row_number() OVER (PARTITION BY app_id ORDER BY used_at DESC, rowid DESC) AS rank FROM app_tokens WHERE type='refresh' AND used_at IS NOT NULL) WHERE rank>2000)`); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM app_refresh_budget WHERE NOT EXISTS (SELECT 1 FROM app_tokens WHERE app_tokens.family_id=app_refresh_budget.family_id)`); err != nil {
		return err
	}
	return tx.Commit()
}
