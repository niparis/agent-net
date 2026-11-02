package gateway

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/ratelimit"
)

// Phone requests use oauth_pending and its shared cap and code namespace.
// The reserved client ID identifies them; state holds the device name.
func (s *Store) newPhonePending(name string, now time.Time) (oauthPendingRequest, error) {
	var p oauthPendingRequest
	tx, err := s.db.Begin()
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM oauth_pending WHERE expires_at <= ? AND (status != 'approved' OR approved_at <= ?)`, now.UnixNano(), now.Add(-oauthPendingTTL).UnixNano()); err != nil {
		return p, err
	}
	var count int
	if err = tx.QueryRow(`SELECT count(*) FROM oauth_pending WHERE status='pending' AND expires_at>?`, now.UnixNano()).Scan(&count); err != nil {
		return p, err
	}
	if count >= oauthPendingLimit {
		return p, errOAuthLimit
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO oauth_clients (client_id,client_name,redirect_uris,grant_types,response_types,token_endpoint_auth_method,created_at) VALUES ('phone','Phone','[]','[]','[]','none',?)`, now.UnixNano()); err != nil {
		return p, err
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
		if err = tx.QueryRow(`SELECT count(*) FROM oauth_pending WHERE user_code=? AND status='pending'`, p.UserCode).Scan(&count); err != nil {
			return p, err
		}
		if count == 0 {
			break
		}
	}
	p.ClientID = "phone"
	p.ClientName = name
	p.CreatedAt = now
	p.ExpiresAt = now.Add(oauthPendingTTL)
	p.Status = "pending"
	_, err = tx.Exec(`INSERT INTO oauth_pending (id,user_code,client_id,redirect_uri,code_challenge,state,resource,created_at,expires_at,status) VALUES (?,?,'phone','','',?,'',?,?,'pending')`, p.ID, p.UserCode, name, now.UnixNano(), p.ExpiresAt.UnixNano())
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}

// Redemption and token creation share a transaction, so status cannot be replayed.
func (s *Store) redeemPhonePending(id string, now time.Time) (string, string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	var status, appID string
	var expires int64
	err = tx.QueryRow(`SELECT status,app_id,expires_at FROM oauth_pending WHERE id=? AND client_id='phone'`, id).Scan(&status, &appID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "expired", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if expires <= now.UnixNano() {
		return "expired", "", nil
	}
	if status != "approved" {
		return status, "", nil
	}
	var kind string
	err = tx.QueryRow(`SELECT kind FROM apps WHERE id=? AND revoked_at IS NULL`, appID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && kind != "owner_app") {
		return "refused", "", nil
	}
	if err != nil {
		return "", "", err
	}
	token, err := issueAppToken(tx, appID, "access", now, phoneTokenTTL)
	if err != nil {
		return "", "", err
	}
	if _, err = tx.Exec(`UPDATE oauth_pending SET status='refused' WHERE id=? AND status='approved'`, id); err != nil {
		return "", "", err
	}
	if err = tx.Commit(); err != nil {
		return "", "", err
	}
	return "approved", token, nil
}

func phoneClientName(ua string) string {
	device := "Browser"
	switch {
	case strings.Contains(ua, "iPad"):
		device = "iPad"
	case strings.Contains(ua, "iPhone"):
		device = "iPhone"
	case strings.Contains(ua, "Android"):
		device = "Android"
	case strings.Contains(ua, "Macintosh"):
		device = "Mac"
	case strings.Contains(ua, "Windows"):
		device = "Windows"
	}
	browser := ""
	switch {
	case strings.Contains(ua, "Edg/") || strings.Contains(ua, "EdgiOS/"):
		browser = "Edge"
	case strings.Contains(ua, "FxiOS/") || strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "CriOS/") || strings.Contains(ua, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	if browser == "" {
		return device
	}
	if device == "Browser" {
		return browser
	}
	return device + " " + browser
}

var phonePairTemplate = template.Must(template.New("pair").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Connect phone</title><style>body{font:1.1rem system-ui;max-width:34rem;margin:4rem auto;padding:0 1.2rem;color:#102c29}strong{font-size:2.5rem;letter-spacing:.12em}</style></head><body><h1>Connect phone</h1><p>Enter this code in your agent-net TUI: S, Connected apps, Add</p><strong id="user-code">{{.Code}}</strong><p id="status">Waiting for approval...</p><script>async function poll(){try{const r=await fetch('/phone/status?id={{.ID}}',{cache:'no-store'});const v=await r.json();if(v.status==='approved'){location.reload();return}if(v.status==='refused'||v.status==='expired'){document.getElementById('status').textContent='Request '+v.status+'. Reload to try again.';return}}catch(e){}setTimeout(poll,2000)}setTimeout(poll,2000)</script></body></html>`))

func (g *Gateway) phonePairPage(w http.ResponseWriter, r *http.Request) {
	p, err := g.store.newPhonePending(phoneClientName(r.UserAgent()), g.store.now())
	if errors.Is(err, errOAuthLimit) {
		http.Error(w, "Too many pending requests. Try again later.", http.StatusTooManyRequests)
		return
	}
	if err != nil {
		http.Error(w, "Could not start pairing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	_ = phonePairTemplate.Execute(w, struct{ ID, Code string }{p.ID, p.UserCode[:3] + "-" + p.UserCode[3:]})
}
func (g *Gateway) phoneStatus(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id := q.Get("id")
	decoded, decodeErr := base64.RawURLEncoding.Strict().DecodeString(id)
	if !oauthSingle(q, "id") || decodeErr != nil || len(decoded) != 16 {
		oauthError(w, 400, "invalid_request")
		return
	}
	status, token, err := g.store.redeemPhonePending(id, g.store.now())
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	if status == "expired" {
		g.limiter.Fail(ratelimit.ClientAddr(r))
	}
	if token != "" {
		setPhoneCookie(w, token, g.store.now())
	}
	oauthJSONResponse(w, 200, map[string]string{"status": status})
}
