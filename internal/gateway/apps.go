package gateway

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
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
	raw, err := newAppToken()
	if err != nil {
		return "", err
	}
	result, err := tx.Exec("INSERT INTO app_tokens (hash, app_id, type, expires_at, created_at) SELECT ?, id, ?, ?, ? FROM apps WHERE id = ? AND revoked_at IS NULL", hashHex(raw), kind, now.Add(ttl).UnixNano(), now.UnixNano(), appID)
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
func appForToken(tx *sql.Tx, raw, kind string, now time.Time) (App, error) {
	app, err := scanApp(tx.QueryRow("SELECT "+appColumns+" FROM apps WHERE revoked_at IS NULL AND id = (SELECT app_id FROM app_tokens WHERE hash = ? AND type = ? AND expires_at > ?)", hashHex(raw), kind, now.UnixNano()))
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
	app, err := appForToken(tx, raw, kind, now)
	if err != nil {
		return App{}, err
	}
	if err := tx.Commit(); err != nil {
		return App{}, err
	}
	return app, nil
}

// RotateRefresh consumes a refresh token once and returns an access token
// (one hour) and a refresh token (90 days). Failure leaves the old token intact.
func (s *Store) RotateRefresh(raw string, now time.Time) (access, refresh string, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	app, err := appForToken(tx, raw, "refresh", now)
	if err != nil {
		return "", "", err
	}
	if _, err := tx.Exec("DELETE FROM app_tokens WHERE hash = ?", hashHex(raw)); err != nil {
		return "", "", err
	}
	access, err = issueAppToken(tx, app.ID, "access", now, time.Hour)
	if err != nil {
		return "", "", err
	}
	refresh, err = issueAppToken(tx, app.ID, "refresh", now, 90*24*time.Hour)
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
	return tx.Commit()
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

// DeleteExpiredTokens removes tokens that expire at or before now.
func (s *Store) DeleteExpiredTokens(now time.Time) error {
	_, err := s.db.Exec("DELETE FROM app_tokens WHERE expires_at <= ?", now.UnixNano())
	return err
}
