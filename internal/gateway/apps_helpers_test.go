package gateway

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// These helpers make an app and its tokens for tests. Production code creates
// apps only by approval and tokens only by sign-in, so it has no such calls.

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

func issueAppToken(tx *sql.Tx, appID, kind string, now time.Time, ttl time.Duration) (string, error) {
	family, err := oauthRandom(32)
	if err != nil {
		return "", err
	}
	return issueFamilyToken(tx, appID, family, kind, "oauth", now, ttl)
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
