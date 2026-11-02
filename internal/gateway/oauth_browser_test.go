package gateway

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func oauthWithCookie(h http.Handler, cookie *http.Cookie) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie != nil {
			r.AddCookie(cookie)
		}
		h.ServeHTTP(w, r)
	})
}
func oauthBrowserCookie(t *testing.T, w *httptest.ResponseRecorder, now time.Time) *http.Cookie {
	t.Helper()
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected browser cookie, got %v", cookies)
	}
	cookie := cookies[0]
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(raw) != 32 || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/oauth" || cookie.Domain != "" || cookie.MaxAge != 600 || !cookie.Expires.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("unsafe browser cookie: %+v, decoded bytes %d, err %v", cookie, len(raw), err)
	}
	return cookie
}
func oauthApprovedBrowserFixture(t *testing.T) (*Store, *clock, http.Handler, string, *http.Cookie, string) {
	t.Helper()
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	page := oauthAuthorize(h, client, nil)
	_, id := oauthPage(t, page)
	cookie := oauthBrowserCookie(t, page, c.t)
	var saved string
	if err := s.db.QueryRow("SELECT browser_hash FROM oauth_pending WHERE id=?", id).Scan(&saved); err != nil || saved != hashHex(cookie.Value) || saved == cookie.Value {
		t.Fatalf("browser hash before approval: %q %v", saved, err)
	}
	if err := s.ApprovePending(id, "bot", "approved browser", c.t); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT browser_hash FROM oauth_pending WHERE id=?", id).Scan(&saved); err != nil || saved != hashHex(cookie.Value) {
		t.Fatalf("browser hash after approval: %q %v", saved, err)
	}
	var appID string
	if err := s.db.QueryRow("SELECT app_id FROM oauth_pending WHERE id=?", id).Scan(&appID); err != nil {
		t.Fatal(err)
	}
	return s, c, h, client, cookie, appID
}
func TestOAuthBrowserReuseAttackerWithoutCookie(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	originalCode, id := oauthPage(t, oauthAuthorize(h, client, nil))
	if err := s.ApprovePending(id, "bot", "victim", c.t); err != nil {
		t.Fatal(err)
	}
	attacker := oauthAuthorize(h, client, url.Values{"code_challenge": {oauthChallenge(strings.Repeat("z", 43))}})
	attackerCode, attackerID := oauthPage(t, attacker)
	if attackerCode == originalCode {
		t.Fatal("attacker received the approved user code")
	}
	status := oauthStatus(t, h, attackerID)
	if status["status"] != "pending" || status["redirect"] != nil {
		t.Fatalf("attacker got approval: %v", status)
	}
}
func TestOAuthBrowserReuseSameBrowser(t *testing.T) {
	_, _, h, client, cookie, _ := oauthApprovedBrowserFixture(t)
	retry := oauthAuthorize(oauthWithCookie(h, cookie), client, nil)
	match := oauthPageID.FindStringSubmatch(retry.Body.String())
	if retry.Code != 200 || len(match) != 2 || strings.Contains(retry.Body.String(), `id="user-code"`) {
		t.Fatalf("same browser not reused: %d %s", retry.Code, retry.Body.String())
	}
	oauthRedirectCode(t, oauthStatus(t, h, match[1]))
}
func TestOAuthBrowserReuseRevokedApp(t *testing.T) {
	s, _, h, client, cookie, appID := oauthApprovedBrowserFixture(t)
	if err := s.RevokeApp(appID); err != nil {
		t.Fatal(err)
	}
	retry := oauthAuthorize(oauthWithCookie(h, cookie), client, nil)
	_, id := oauthPage(t, retry)
	status := oauthStatus(t, h, id)
	if status["status"] != "pending" || status["redirect"] != nil {
		t.Fatalf("revoked approval reused: %v", status)
	}
}

func TestOAuthBrowserReuseRequiresAllBindings(t *testing.T) {
	_, _, h, client, cookie, _ := oauthApprovedBrowserFixture(t)
	other := oauthRegister(t, h)
	changed := *cookie
	changed.Value = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for _, tc := range []struct {
		name, client string
		cookie       *http.Cookie
		extra        url.Values
	}{
		{"wrong cookie", client, &changed, nil},
		{"wrong client", other, cookie, nil},
		{"wrong redirect", client, cookie, url.Values{"redirect_uri": {"http://localhost:8787/callback"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, id := oauthPage(t, oauthAuthorize(oauthWithCookie(h, tc.cookie), tc.client, tc.extra))
			status := oauthStatus(t, h, id)
			if status["status"] != "pending" || status["redirect"] != nil {
				t.Fatalf("unexpected reuse: %v", status)
			}
		})
	}
}

func TestOAuthBrowserCookieExpiry(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	page := oauthAuthorize(h, client, nil)
	_, id := oauthPage(t, page)
	cookie := oauthBrowserCookie(t, page, c.t)
	c.t = c.t.Add(9 * time.Minute)
	if err := s.ApprovePending(id, "bot", "recent approval", c.t); err != nil {
		t.Fatal(err)
	}
	// Reuse must not extend the lifetime of the original browser cookie.
	retry := oauthAuthorize(oauthWithCookie(h, cookie), client, nil)
	match := oauthPageID.FindStringSubmatch(retry.Body.String())
	if len(match) != 2 || strings.Contains(retry.Body.String(), `id="user-code"`) {
		t.Fatalf("valid cookie not reused: %s", retry.Body.String())
	}
	oauthRedirectCode(t, oauthStatus(t, h, match[1]))
	// Even a client that manually resends an expired cookie cannot reuse it.
	c.t = c.t.Add(time.Minute)
	_, retryID := oauthPage(t, oauthAuthorize(oauthWithCookie(h, cookie), client, nil))
	status := oauthStatus(t, h, retryID)
	if status["status"] != "pending" || status["redirect"] != nil {
		t.Fatalf("expired cookie reused: %v", status)
	}
}
