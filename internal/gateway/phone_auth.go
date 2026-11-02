package gateway

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/ratelimit"
)

const phoneCookieName = "__Host-agent_net_phone"
const phoneTokenTTL = 90 * 24 * time.Hour
const phoneRenewBefore = 30 * 24 * time.Hour

func validPhoneToken(raw string) bool {
	if len(raw) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	return err == nil && len(b) == 32
}

// phoneAppForToken validates and extends the existing token in one transaction.
// Keeping the token avoids invalidating simultaneous requests from the device.
func (s *Store) phoneAppForToken(raw string, now time.Time) (App, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return App{}, false, err
	}
	defer tx.Rollback()
	var expires int64
	err = tx.QueryRow(`SELECT t.expires_at FROM app_tokens t JOIN apps a ON a.id=t.app_id WHERE t.hash=? AND t.type='access' AND t.expires_at>? AND a.kind='owner_app' AND a.revoked_at IS NULL`, hashHex(raw), now.UnixNano()).Scan(&expires)
	if err != nil {
		return App{}, false, err
	}
	app, err := appForToken(tx, raw, "access", now)
	if err != nil {
		return App{}, false, err
	}
	renewed := time.Unix(0, expires).Sub(now) < phoneRenewBefore
	if renewed {
		if _, err = tx.Exec(`UPDATE app_tokens SET expires_at=? WHERE hash=?`, now.Add(phoneTokenTTL).UnixNano(), hashHex(raw)); err != nil {
			return App{}, false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return App{}, false, err
	}
	return app, renewed, nil
}
func setPhoneCookie(w http.ResponseWriter, token string, now time.Time) {
	http.SetCookie(w, &http.Cookie{Name: phoneCookieName, Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(phoneTokenTTL / time.Second), Expires: now.Add(phoneTokenTTL)})
}
func phoneRoute(path string) bool {
	return path == "/" || path == "/action" || path == "/events" || path == "/manifest.webmanifest" || strings.HasPrefix(path, "/session/") || strings.HasPrefix(path, "/static/")
}
func (g *Gateway) phoneAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !phoneRoute(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		oauthHeaders(w)
		cookie, err := r.Cookie(phoneCookieName)
		var app App
		var renewed bool
		if err == nil && validPhoneToken(cookie.Value) {
			app, renewed, err = g.store.phoneAppForToken(cookie.Value, g.store.now())
		} else {
			err = sql.ErrNoRows
		}
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "Authentication unavailable", http.StatusInternalServerError)
				return
			}
			if cookie != nil {
				g.limiter.Fail(ratelimit.ClientAddr(r))
			}
			if r.Method != http.MethodGet || r.URL.Path == "/events" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			g.phonePairPage(w, r)
			return
		}
		if renewed {
			setPhoneCookie(w, cookie.Value, g.store.now())
		}
		if r.Method == http.MethodPost && r.URL.Path == "/action" {
			g.logLine("phone.action", "app", app.Name, "app_id", app.ID, "source", ratelimit.ClientAddr(r))
			base, err := url.Parse(g.opts.PublicURL)
			origins := r.Header.Values("Origin")
			if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || len(origins) != 1 || origins[0] != base.Scheme+"://"+base.Host {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		if r.Method == http.MethodGet && r.URL.Path == "/events" {
			ctx, cancel := context.WithCancel(r.Context())
			done := make(chan struct{})
			go func() {
				defer close(done)
				ticker := time.NewTicker(time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						var valid int
						err := g.store.db.QueryRowContext(ctx, `SELECT 1 FROM app_tokens t JOIN apps a ON a.id=t.app_id WHERE t.hash=? AND t.type='access' AND t.expires_at>? AND a.id=? AND a.kind='owner_app' AND a.revoked_at IS NULL`, hashHex(cookie.Value), g.store.now().UnixNano(), app.ID).Scan(&valid)
						if err != nil {
							cancel()
							return
						}
					}
				}
			}()
			next.ServeHTTP(w, r.WithContext(ctx))
			cancel()
			<-done
			return
		}
		next.ServeHTTP(w, r)
	})
}
