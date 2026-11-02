package gateway

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const (
	oauthPendingTTL   = 10 * time.Minute
	oauthCodeTTL      = 2 * time.Minute
	oauthClientLimit  = 200
	oauthPendingLimit = 20
)

var ErrOAuthTooManyTries = errors.New("too many tries")
var errOAuthLimit = errors.New("limit reached")
var errOAuthInvalidGrant = errors.New("invalid grant")

type oauthClient struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
}

// OAuthPending is the information the owner can inspect before approval.
type OAuthPending struct {
	ID, UserCode, ClientID, ClientName, RedirectURI, RedirectHost, Resource string
	CreatedAt, ExpiresAt                                                    time.Time
}

func oauthRandom(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func oauthUserCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 6)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}
func oauthContains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func (s *Store) registerOAuthClient(c oauthClient, now time.Time, source string) (oauthClient, error) {
	var err error
	c.ClientID, err = oauthRandom(32)
	if err != nil {
		return c, err
	}
	c.ClientIDIssuedAt = now.Unix()
	uris, err := json.Marshal(c.RedirectURIs)
	if err != nil {
		return c, err
	}
	grants, err := json.Marshal(c.GrantTypes)
	if err != nil {
		return c, err
	}
	responses, err := json.Marshal(c.ResponseTypes)
	if err != nil {
		return c, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return c, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM oauth_clients WHERE created_at <= ? AND NOT EXISTS (SELECT 1 FROM apps WHERE apps.client_id=oauth_clients.client_id)`, now.Add(-time.Hour).UnixNano()); err != nil {
		return c, err
	}
	var count int
	if err = tx.QueryRow(`SELECT count(*) FROM oauth_registration_attempts WHERE source=? AND created_at>?`, source, now.Add(-time.Hour).UnixNano()).Scan(&count); err != nil {
		return c, err
	}
	if count >= 5 {
		return c, errOAuthLimit
	}
	if err = tx.QueryRow(`SELECT count(*) FROM oauth_clients WHERE NOT EXISTS (SELECT 1 FROM apps WHERE apps.client_id=oauth_clients.client_id)`).Scan(&count); err != nil {
		return c, err
	}
	if count >= oauthClientLimit {
		if _, err = tx.Exec(`DELETE FROM oauth_pending WHERE client_id=(SELECT client_id FROM oauth_clients WHERE NOT EXISTS (SELECT 1 FROM apps WHERE apps.client_id=oauth_clients.client_id) ORDER BY created_at,rowid LIMIT 1)`); err != nil {
			return c, err
		}
		if _, err = tx.Exec(`DELETE FROM oauth_clients WHERE client_id=(SELECT client_id FROM oauth_clients WHERE NOT EXISTS (SELECT 1 FROM apps WHERE apps.client_id=oauth_clients.client_id) ORDER BY created_at,rowid LIMIT 1)`); err != nil {
			return c, err
		}
	}
	_, err = tx.Exec(`INSERT INTO oauth_clients (client_id,client_name,redirect_uris,grant_types,response_types,token_endpoint_auth_method,created_at,source) VALUES (?,?,?,?,?,?,?,?)`, c.ClientID, c.ClientName, string(uris), string(grants), string(responses), c.TokenEndpointAuthMethod, now.UnixNano(), source)
	if err != nil {
		return c, err
	}
	if _, err = tx.Exec(`DELETE FROM oauth_registration_attempts WHERE created_at<=?`, now.Add(-time.Hour).UnixNano()); err != nil {
		return c, err
	}
	if _, err = tx.Exec(`INSERT INTO oauth_registration_attempts (source,created_at) VALUES (?,?)`, source, now.UnixNano()); err != nil {
		return c, err
	}
	return c, tx.Commit()
}
func oauthReadClient(q interface{ QueryRow(string, ...any) *sql.Row }, id string) (oauthClient, error) {
	var c oauthClient
	var uris, grants, responses string
	var created int64
	err := q.QueryRow(`SELECT client_id,client_name,redirect_uris,grant_types,response_types,token_endpoint_auth_method,created_at FROM oauth_clients WHERE client_id=?`, id).Scan(&c.ClientID, &c.ClientName, &uris, &grants, &responses, &c.TokenEndpointAuthMethod, &created)
	if err != nil {
		return c, err
	}
	for _, pair := range []struct {
		raw string
		dst *[]string
	}{{uris, &c.RedirectURIs}, {grants, &c.GrantTypes}, {responses, &c.ResponseTypes}} {
		if err = json.Unmarshal([]byte(pair.raw), pair.dst); err != nil {
			return c, err
		}
	}
	c.ClientIDIssuedAt = time.Unix(0, created).Unix()
	return c, nil
}

type oauthPendingRequest struct {
	ID, UserCode, ClientID, ClientName, RedirectURI, Challenge, State, Resource, Status, AppID string
	CreatedAt, ExpiresAt                                                                       time.Time
}

func (s *Store) newOAuthPending(clientID, redirect, challenge, state, resource, source string, now time.Time) (oauthPendingRequest, error) {
	var p oauthPendingRequest
	tx, err := s.db.Begin()
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM oauth_clients WHERE created_at<=? AND NOT EXISTS (SELECT 1 FROM apps WHERE apps.client_id=oauth_clients.client_id)`, now.Add(-time.Hour).UnixNano()); err != nil {
		return p, err
	}
	client, err := oauthReadClient(tx, clientID)
	if err != nil {
		return p, err
	}
	if !oauthContains(client.RedirectURIs, redirect) || !oauthContains(client.GrantTypes, "authorization_code") || !oauthContains(client.ResponseTypes, "code") {
		return p, errOAuthInvalidGrant
	}
	if _, err = tx.Exec(`DELETE FROM oauth_pending WHERE expires_at<=?`, now.UnixNano()); err != nil {
		return p, err
	}
	p = oauthPendingRequest{ClientID: clientID, ClientName: client.ClientName, RedirectURI: redirect, Challenge: challenge, State: state, Resource: resource, Status: "pending", CreatedAt: now, ExpiresAt: now.Add(oauthPendingTTL)}
	var count int
	// Check each quota separately, so overlaps do not count twice.
	var clientCount, sourceCount int
	if err = tx.QueryRow(`SELECT count(*) FROM oauth_pending WHERE status='pending' AND client_id=?`, clientID).Scan(&clientCount); err != nil {
		return p, err
	}
	if err = tx.QueryRow(`SELECT count(*) FROM oauth_pending WHERE status='pending' AND source=?`, source).Scan(&sourceCount); err != nil {
		return p, err
	}
	if clientCount >= 3 || sourceCount >= 3 {
		return p, errOAuthLimit
	}
	if err = tx.QueryRow(`SELECT count(*) FROM oauth_pending WHERE status='pending'`).Scan(&count); err != nil {
		return p, err
	}
	if count >= oauthPendingLimit {
		if _, err = tx.Exec(`DELETE FROM oauth_pending WHERE id=(SELECT id FROM oauth_pending WHERE status='pending' ORDER BY created_at,rowid LIMIT 1)`); err != nil {
			return p, err
		}
	}
	p.ID, err = oauthRandom(16)
	if err != nil {
		return p, err
	}
	for {
		p.UserCode, err = oauthUserCode()
		if err != nil {
			return p, err
		}
		var exists int
		if err = tx.QueryRow("SELECT count(*) FROM oauth_pending WHERE user_code=? AND expires_at > ?", p.UserCode, now.UnixNano()).Scan(&exists); err != nil {
			return p, err
		}
		if exists == 0 {
			break
		}
	}
	_, err = tx.Exec(`INSERT INTO oauth_pending (id,user_code,client_id,redirect_uri,code_challenge,state,resource,created_at,expires_at,status,source) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, p.ID, p.UserCode, p.ClientID, p.RedirectURI, p.Challenge, p.State, p.Resource, p.CreatedAt.UnixNano(), p.ExpiresAt.UnixNano(), p.Status, source)
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}

// PendingByUserCode looks up a live request. Five misses per source in ten
// minutes block further lookups until the oldest miss expires.
func (s *Store) PendingByUserCode(code, source string, now time.Time) (OAuthPending, error) {
	var p OAuthPending
	tx, err := s.db.Begin()
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	cutoff := now.Add(-10 * time.Minute).UnixNano()
	if _, err = tx.Exec("DELETE FROM oauth_code_misses WHERE attempted_at <= ?", cutoff); err != nil {
		return p, err
	}
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM oauth_code_misses WHERE source=? AND attempted_at>?", source, cutoff).Scan(&count); err != nil {
		return p, err
	}
	if count >= 5 {
		return p, ErrOAuthTooManyTries
	}
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	var created, expires int64
	err = tx.QueryRow(`SELECT p.id,p.user_code,p.client_id,c.client_name,p.redirect_uri,p.resource,p.created_at,p.expires_at FROM oauth_pending p JOIN oauth_clients c ON c.client_id=p.client_id WHERE p.user_code=? AND p.status='pending' AND p.expires_at>?`, normalized, now.UnixNano()).Scan(&p.ID, &p.UserCode, &p.ClientID, &p.ClientName, &p.RedirectURI, &p.Resource, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err = tx.Exec("INSERT INTO oauth_code_misses (source,attempted_at) VALUES (?,?)", source, now.UnixNano()); err != nil {
			return p, err
		}
		if err = tx.Commit(); err != nil {
			return p, err
		}
		if count+1 >= 5 {
			return p, ErrOAuthTooManyTries
		}
		return p, sql.ErrNoRows
	}
	if err != nil {
		return p, err
	}
	u, err := url.Parse(p.RedirectURI)
	if err != nil {
		return p, err
	}
	p.RedirectHost = u.Hostname()
	p.CreatedAt = time.Unix(0, created)
	p.ExpiresAt = time.Unix(0, expires)
	return p, tx.Commit()
}

// ApprovePending atomically creates an app and approves a live request.
func (s *Store) ApprovePending(id, kind, name string, now time.Time, ownerSession ...string) error {
	if kind != "bot" && kind != "owner_app" {
		return reject("invalid app kind %q", kind)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var clientID, clientName, uris, redirect string
	err = tx.QueryRow(`SELECT c.client_id,c.client_name,c.redirect_uris,p.redirect_uri FROM oauth_pending p JOIN oauth_clients c ON c.client_id=p.client_id WHERE p.id=? AND p.status='pending' AND p.expires_at>?`, id, now.UnixNano()).Scan(&clientID, &clientName, &uris, &redirect)
	if err != nil {
		return err
	}
	// This is the same record as CreateApp, in the approval transaction so a
	// failed or racing approval cannot leave an app without an approval.
	appID := protocol.NewID("app")
	_, err = tx.Exec(`INSERT INTO apps (id,name,kind,client_id,client_name,redirect_uris,created_at,last_used_at,revoked_at,grants) VALUES (?,?,?,?,?,?,?,NULL,NULL,'[]')`, appID, name, kind, clientID, clientName, uris, now.UnixNano())
	if err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE oauth_pending SET status='approved', app_id=?, approved_at=? WHERE id=?", appID, now.UnixNano(), id); err != nil {
		return err
	}
	if len(ownerSession) > 0 && ownerSession[0] != "" {
		u, err := url.Parse(redirect)
		if err != nil {
			return err
		}
		text := fmt.Sprintf("New app approved: %s (%s, %s, %s)", safeAppLog(name), kind, safeAppLog(clientName), safeAppLog(u.Hostname()))
		if _, err = s.createInboxItem(tx, protocol.InboxItem{Kind: "check", SessionID: ownerSession[0], Node: "apps", Text: text, MessageID: "app-approval:" + id}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RefusePending refuses a live request without creating an app.
func (s *Store) RefusePending(id string) error {
	result, err := s.db.Exec("UPDATE oauth_pending SET status='refused' WHERE id=? AND status='pending' AND expires_at>?", id, s.now().UnixNano())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) oauthStatus(id string, now time.Time) (string, string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	var p oauthPendingRequest
	var expires int64
	err = tx.QueryRow(`SELECT client_id,redirect_uri,code_challenge,state,status,app_id,expires_at FROM oauth_pending WHERE id=?`, id).Scan(&p.ClientID, &p.RedirectURI, &p.Challenge, &p.State, &p.Status, &p.AppID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "expired", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if expires <= now.UnixNano() {
		return "expired", "", nil
	}
	if p.Status != "approved" {
		return p.Status, "", nil
	}
	var active int
	if err = tx.QueryRow("SELECT count(*) FROM apps WHERE id=? AND revoked_at IS NULL", p.AppID).Scan(&active); err != nil {
		return "", "", err
	}
	if active == 0 {
		return "refused", "", nil
	}
	code, err := oauthRandom(32)
	if err != nil {
		return "", "", err
	}
	// Keep only the newest unused code for this page. Used hashes stay so a
	// replay can revoke the tokens. Poll retries never persist a plaintext code.
	if _, err = tx.Exec("DELETE FROM oauth_codes WHERE pending_id=? AND used_at IS NULL", id); err != nil {
		return "", "", err
	}
	if _, err = tx.Exec(`INSERT INTO oauth_codes (hash,pending_id,app_id,client_id,redirect_uri,code_challenge,expires_at) VALUES (?,?,?,?,?,?,?)`, hashHex(code), id, p.AppID, p.ClientID, p.RedirectURI, p.Challenge, now.Add(oauthCodeTTL).UnixNano()); err != nil {
		return "", "", err
	}
	u, err := url.Parse(p.RedirectURI)
	if err != nil {
		return "", "", err
	}
	q := u.Query()
	q.Set("code", code)
	if p.State != "" {
		q.Set("state", p.State)
	} else {
		q.Del("state")
	}
	u.RawQuery = q.Encode()
	if err = tx.Commit(); err != nil {
		return "", "", err
	}
	return "approved", u.String(), nil
}

func (s *Store) exchangeOAuthCode(code, clientID, redirect, verifier string, now time.Time) (string, string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	var appID, storedClient, storedRedirect, challenge string
	var expires int64
	var used sql.NullInt64
	err = tx.QueryRow(`SELECT app_id,client_id,redirect_uri,code_challenge,expires_at,used_at FROM oauth_codes WHERE hash=?`, hashHex(code)).Scan(&appID, &storedClient, &storedRedirect, &challenge, &expires, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", errOAuthInvalidGrant
	}
	if err != nil {
		return "", "", err
	}
	if clientID != storedClient || redirect != storedRedirect {
		return "", "", errOAuthInvalidGrant
	}
	if used.Valid {
		// Revoke descendants of the replayed authorization code.
		if _, err = tx.Exec("DELETE FROM app_tokens WHERE family_id=? AND used_at IS NULL", hashHex(code)); err != nil {
			return "", "", err
		}
		if err = tx.Commit(); err != nil {
			return "", "", err
		}
		return "", "", errOAuthInvalidGrant
	}
	if expires <= now.UnixNano() || !validOAuthVerifier(verifier) || oauthPKCE(verifier) != challenge {
		return "", "", errOAuthInvalidGrant
	}
	client, err := oauthReadClient(tx, clientID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", errOAuthInvalidGrant
	}
	if err != nil {
		return "", "", err
	}
	family := hashHex(code)
	access, err := issueFamilyToken(tx, appID, family, "access", now, time.Hour)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", errOAuthInvalidGrant
	}
	if err != nil {
		return "", "", err
	}
	var refresh string
	if oauthContains(client.GrantTypes, "refresh_token") {
		refresh, err = issueFamilyToken(tx, appID, family, "refresh", now, 90*24*time.Hour)
		if err != nil {
			return "", "", err
		}
	}
	if _, err = tx.Exec("UPDATE oauth_codes SET used_at=? WHERE hash=?", now.UnixNano(), hashHex(code)); err != nil {
		return "", "", err
	}
	if err = tx.Commit(); err != nil {
		return "", "", err
	}
	return access, refresh, nil
}
func (s *Store) refreshOAuth(raw, clientID string, now time.Time) (string, string, error) {
	var stored string
	err := s.db.QueryRow(`SELECT a.client_id FROM app_tokens t JOIN apps a ON a.id=t.app_id WHERE t.hash=? AND t.type='refresh' AND (t.expires_at>? OR t.used_at IS NOT NULL) AND a.revoked_at IS NULL`, hashHex(raw), now.UnixNano()).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", errOAuthInvalidGrant
	}
	if err != nil {
		return "", "", err
	}
	if stored != clientID {
		return "", "", errOAuthInvalidGrant
	}
	client, err := oauthReadClient(s.db, clientID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", errOAuthInvalidGrant
	}
	if err != nil {
		return "", "", err
	}
	if !oauthContains(client.GrantTypes, "refresh_token") {
		return "", "", errOAuthInvalidGrant
	}
	access, refresh, err := s.RotateRefresh(raw, now)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", errOAuthInvalidGrant
	}
	return access, refresh, err
}

// CleanupOAuth removes expired public listener records.
func (s *Store) CleanupOAuth(now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM oauth_pending WHERE expires_at<=?`,
		`DELETE FROM oauth_registration_attempts WHERE created_at<=?`,
		`DELETE FROM oauth_codes WHERE (used_at IS NULL AND expires_at<=?) OR (used_at IS NOT NULL AND used_at<=?)`,
		`DELETE FROM oauth_clients WHERE created_at<=? AND NOT EXISTS (SELECT 1 FROM apps WHERE apps.client_id=oauth_clients.client_id)`,
	} {
		var args []any
		switch {
		case strings.Contains(q, "oauth_codes"):
			args = []any{now.UnixNano(), now.Add(-90 * 24 * time.Hour).UnixNano()}
		case strings.Contains(q, "oauth_clients"), strings.Contains(q, "oauth_registration_attempts"):
			args = []any{now.Add(-time.Hour).UnixNano()}
		default:
			args = []any{now.UnixNano()}
		}
		if _, err = tx.Exec(q, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}
