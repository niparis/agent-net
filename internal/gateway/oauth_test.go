package gateway

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/niparis/agent-net/internal/ratelimit"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

const oauthCallback = "https://client.example/callback?existing=yes"
const oauthVerifier = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"

func oauthFixture(t *testing.T) (*Store, *clock, http.Handler) {
	t.Helper()
	s, c := newTestStore(t)
	g := &Gateway{limiter: ratelimit.New(ratelimit.Config{OAuth: ratelimit.Rate{Requests: 10000, Per: time.Minute}, FailureLimit: 10000}, c.now), store: s, opts: Options{PublicURL: "https://gateway.example/"}, logger: log.New(io.Discard, "", 0)}
	mux := http.NewServeMux()
	g.registerOAuth(mux)
	return s, c, g.appsMiddleware(mux)
}
func oauthRequest(h http.Handler, method, path, body, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func oauthJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("JSON %d %s: %v", w.Code, w.Body.String(), err)
	}
	return v
}
func oauthRegister(t *testing.T, h http.Handler) string {
	t.Helper()
	w := oauthRequest(h, "POST", "/oauth/register", `{"client_name":"Cursor","redirect_uris":["https://client.example/callback?existing=yes","http://localhost:8787/callback"],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"token_endpoint_auth_method":"none"}`, "application/json")
	if w.Code != 201 {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	return oauthJSON(t, w)["client_id"].(string)
}
func oauthChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
func oauthAuthorize(h http.Handler, client string, extra url.Values) *httptest.ResponseRecorder {
	v := url.Values{"response_type": {"code"}, "client_id": {client}, "redirect_uri": {oauthCallback}, "state": {"state value"}, "code_challenge": {oauthChallenge(oauthVerifier)}, "code_challenge_method": {"S256"}}
	for k, values := range extra {
		v[k] = values
	}
	return oauthRequest(h, "GET", "/oauth/authorize?"+v.Encode(), "", "")
}

var oauthPageCode = regexp.MustCompile(`id="user-code">([^<]+)<`)
var oauthPageID = regexp.MustCompile(`/oauth/authorize/status\?id=([A-Za-z0-9_-]+)`)

func oauthPage(t *testing.T, w *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	code, id := oauthPageCode.FindStringSubmatch(w.Body.String()), oauthPageID.FindStringSubmatch(w.Body.String())
	if w.Code != 200 || len(code) != 2 || len(id) != 2 {
		t.Fatalf("authorize page: %d %s", w.Code, w.Body.String())
	}
	return code[1], id[1]
}
func oauthStatus(t *testing.T, h http.Handler, id string) map[string]any {
	t.Helper()
	w := oauthRequest(h, "GET", "/oauth/authorize/status?id="+url.QueryEscape(id), "", "")
	if w.Code != 200 {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
	return oauthJSON(t, w)
}
func oauthToken(h http.Handler, v url.Values) *httptest.ResponseRecorder {
	return oauthRequest(h, "POST", "/oauth/token", v.Encode(), "application/x-www-form-urlencoded")
}
func oauthExchange(code, client, verifier string) url.Values {
	return url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {client}, "redirect_uri": {oauthCallback}, "code_verifier": {verifier}}
}
func oauthRedirectCode(t *testing.T, status map[string]any) string {
	t.Helper()
	if status["status"] != "approved" {
		t.Fatalf("status = %v", status)
	}
	u, err := url.Parse(status["redirect"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("existing") != "yes" || u.Query().Get("state") != "state value" || u.Query().Get("code") == "" {
		t.Fatalf("redirect: %v", u)
	}
	return u.Query().Get("code")
}

func TestOAuthMetadata(t *testing.T) {
	_, _, h := oauthFixture(t)
	w := oauthRequest(h, "GET", "/.well-known/oauth-protected-resource", "", "")
	p := oauthJSON(t, w)
	if w.Code != 200 || p["resource"] != "https://gateway.example/mcp" || fmt.Sprint(p["authorization_servers"]) != "[https://gateway.example]" {
		t.Fatalf("protected resource %v", p)
	}
	w = oauthRequest(h, "GET", "/.well-known/oauth-authorization-server", "", "")
	p = oauthJSON(t, w)
	for k, v := range map[string]any{"issuer": "https://gateway.example", "authorization_endpoint": "https://gateway.example/oauth/authorize", "token_endpoint": "https://gateway.example/oauth/token", "registration_endpoint": "https://gateway.example/oauth/register", "response_types_supported": "[code]", "grant_types_supported": "[authorization_code refresh_token]", "code_challenge_methods_supported": "[S256]", "token_endpoint_auth_methods_supported": "[none]"} {
		if fmt.Sprint(p[k]) != fmt.Sprint(v) {
			t.Errorf("%s=%v, want %v", k, p[k], v)
		}
	}
}
func TestOAuthRegistration(t *testing.T) {
	s, c, h := oauthFixture(t)
	id := oauthRegister(t, h)
	var uris string
	if err := s.db.QueryRow("SELECT redirect_uris FROM oauth_clients WHERE client_id=?", id).Scan(&uris); err != nil || !strings.Contains(uris, "localhost") {
		t.Fatalf("stored uris %s %v", uris, err)
	}
	body := `{"client_name":"Test","redirect_uris":["https://example.test/cb","http://127.0.0.1/cb","http://[::1]:123/cb"],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"token_endpoint_auth_method":"none"}`
	w := oauthRequest(h, "POST", "/oauth/register", body, "application/json")
	p := oauthJSON(t, w)
	if w.Code != 201 || p["client_name"] != "Test" || len(p["redirect_uris"].([]any)) != 3 || len(p["grant_types"].([]any)) != 2 || fmt.Sprint(p["response_types"]) != "[code]" || p["token_endpoint_auth_method"] != "none" || p["client_id"] == "" || p["client_id_issued_at"] != float64(c.t.Unix()) {
		t.Fatalf("registration %d %v", w.Code, p)
	}
	for _, uri := range []string{"http://example.test/cb", "http://localhost.evil.test/cb", "http://127.0.0.2/cb", "https://user@example.test/cb", "https://example.test/cb#frag", "ftp://localhost/cb", "/relative", "http://[::ffff:127.0.0.1]/cb"} {
		raw, _ := json.Marshal(map[string]any{"redirect_uris": []string{uri}})
		w = oauthRequest(h, "POST", "/oauth/register", string(raw), "application/json")
		if w.Code != 400 {
			t.Errorf("accepted %q: %d %s", uri, w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{}`, `{"redirect_uris":[]}`, `{"redirect_uris":["https://a","https://b","https://c","https://d","https://e","https://f"]}`, `{"redirect_uris":["https://a"],"token_endpoint_auth_method":"client_secret_basic"}`, `{"redirect_uris":["https://a"],"grant_types":["password"]}`, `{"redirect_uris":["https://a"],"response_types":["token"]}`} {
		w = oauthRequest(h, "POST", "/oauth/register", body, "application/json")
		if w.Code != 400 {
			t.Errorf("accepted %s: %d %s", body, w.Code, w.Body.String())
		}
	}
}
func TestOAuthAuthorizeValidation(t *testing.T) {
	_, _, h := oauthFixture(t)
	id := oauthRegister(t, h)
	for _, v := range []url.Values{{"code_challenge": {""}}, {"code_challenge_method": {"plain"}}, {"code_challenge": {"bad"}}, {"redirect_uri": {"https://evil.example/"}}, {"response_type": {"token"}}, {"client_id": {"unknown"}}} {
		w := oauthAuthorize(h, id, v)
		if w.Code != 400 {
			t.Errorf("accepted %v: %d %s", v, w.Code, w.Body.String())
		}
	}
}
func TestOAuthFullFlow(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	code, id := oauthPage(t, oauthAuthorize(h, client, nil))
	if len(strings.ReplaceAll(code, "-", "")) != 6 || !strings.Contains(oauthAuthorize(h, client, nil).Body.String(), "Connected apps") {
		t.Fatal("missing code page instructions")
	}
	pending, err := s.PendingByUserCode(strings.ToLower(strings.ReplaceAll(code, "-", "")), "127.0.0.1", c.t)
	if err != nil || pending.ID != id || pending.ClientName != "Cursor" || pending.RedirectHost != "client.example" {
		t.Fatalf("pending %+v %v", pending, err)
	}
	if err = s.ApprovePending(id, "bot", "my bot", c.t); err != nil {
		t.Fatal(err)
	}
	authCode := oauthRedirectCode(t, oauthStatus(t, h, id))
	var stored string
	if err := s.db.QueryRow("SELECT hash FROM oauth_codes WHERE hash=?", hashHex(authCode)).Scan(&stored); err != nil || stored == authCode {
		t.Fatalf("code not hashed: %v", err)
	}
	bad := oauthToken(h, oauthExchange(authCode, client, strings.Repeat("a", 43)))
	if bad.Code != 400 || oauthJSON(t, bad)["error"] != "invalid_grant" {
		t.Fatalf("bad verifier: %d %s", bad.Code, bad.Body.String())
	}
	good := oauthToken(h, oauthExchange(authCode, client, oauthVerifier))
	if good.Code != 200 {
		t.Fatalf("exchange %d %s", good.Code, good.Body.String())
	}
	tokens := oauthJSON(t, good)
	if tokens["token_type"] != "Bearer" || tokens["expires_in"] != float64(3600) {
		t.Fatalf("tokens %v", tokens)
	}
	access, refresh := tokens["access_token"].(string), tokens["refresh_token"].(string)
	if _, err := s.AppForToken(access, "access", c.t); err != nil {
		t.Fatal(err)
	}
	rotated := oauthToken(h, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {client}})
	if rotated.Code != 200 {
		t.Fatalf("refresh %d %s", rotated.Code, rotated.Body.String())
	}
	newTokens := oauthJSON(t, rotated)
	if newTokens["refresh_token"] == refresh {
		t.Fatal("refresh not rotated")
	}
	if w := oauthToken(h, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {client}}); w.Code != 400 {
		t.Fatalf("old refresh accepted %d", w.Code)
	}
	replay := oauthToken(h, oauthExchange(authCode, client, oauthVerifier))
	if replay.Code != 400 || oauthJSON(t, replay)["error"] != "invalid_grant" {
		t.Fatalf("replay %d %s", replay.Code, replay.Body.String())
	}
	for _, raw := range []struct{ token, kind string }{{access, "access"}, {newTokens["access_token"].(string), "access"}, {newTokens["refresh_token"].(string), "refresh"}} {
		if _, err := s.AppForToken(raw.token, raw.kind, c.t); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("replay did not revoke %s: %v", raw.kind, err)
		}
	}
}
func TestOAuthPendingCapsExpiryReuseAndRefusal(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	code, id := oauthPage(t, oauthAuthorize(h, client, nil))
	p, err := s.PendingByUserCode(code, "source", c.t)
	if err != nil || p.ID != id {
		t.Fatal(err)
	}
	if err = s.RefusePending(id); err != nil {
		t.Fatal(err)
	}
	if got := oauthStatus(t, h, id); got["status"] != "refused" || got["redirect"] != nil {
		t.Fatalf("refused %v", got)
	}
	for range 20 {
		oauthPage(t, oauthAuthorize(h, client, nil))
	}
	if w := oauthAuthorize(h, client, nil); w.Code != 429 {
		t.Fatalf("cap: %d", w.Code)
	}
	c.t = c.t.Add(10 * time.Minute)
	if got := oauthStatus(t, h, id); got["status"] != "expired" {
		t.Fatalf("expired %v", got)
	}
	if _, err = s.PendingByUserCode(code, "source", c.t); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expired lookup %v", err)
	}
	page := oauthAuthorize(h, client, nil)
	code, id = oauthPage(t, page)
	cookie := oauthBrowserCookie(t, page, c.t)
	if err = s.ApprovePending(id, "bot", "retry bot", c.t); err != nil {
		t.Fatal(err)
	}
	// A retry is already approved, and cannot be looked up as a pending code.
	retry := oauthAuthorize(oauthWithCookie(h, cookie), client, nil)
	match := oauthPageID.FindStringSubmatch(retry.Body.String())
	if retry.Code != 200 || len(match) != 2 || strings.Contains(retry.Body.String(), `id="user-code"`) {
		t.Fatalf("retry page: %d %s", retry.Code, retry.Body.String())
	}
	retryID := match[1]
	if retryID == id {
		t.Fatal("reused pending ID")
	}
	oauthRedirectCode(t, oauthStatus(t, h, retryID))
	if _, err = s.PendingByUserCode(code, "source", c.t); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("approved lookup %v", err)
	}
	c.t = c.t.Add(10 * time.Minute)
	_, lateID := oauthPage(t, oauthAuthorize(oauthWithCookie(h, cookie), client, nil))
	if got := oauthStatus(t, h, lateID); got["status"] != "pending" {
		t.Fatalf("reuse outlived window %v", got)
	}
}
func TestOAuthPendingAttempts(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	code, _ := oauthPage(t, oauthAuthorize(h, client, nil))
	for range 4 {
		if _, err := s.PendingByUserCode("WRONGX", "one", c.t); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("miss: %v", err)
		}
	}
	if _, err := s.PendingByUserCode("WRONGX", "one", c.t); !errors.Is(err, ErrOAuthTooManyTries) {
		t.Fatalf("fifth miss: %v", err)
	}
	if _, err := s.PendingByUserCode(code, "one", c.t); !errors.Is(err, ErrOAuthTooManyTries) {
		t.Fatalf("limit: %v", err)
	}
	if _, err := s.PendingByUserCode(code, "two", c.t); err != nil {
		t.Fatalf("other source: %v", err)
	}
	c.t = c.t.Add(10 * time.Minute)
	if _, err := s.PendingByUserCode("WRONGX", "one", c.t); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("limit did not expire: %v", err)
	}
}

func TestOAuthClientLimitCleanup(t *testing.T) {
	s, c, h := oauthFixture(t)
	kept := oauthRegister(t, h)
	// An old client with an approved app must survive cleanup.
	_, id := oauthPage(t, oauthAuthorize(h, kept, nil))
	if err := s.ApprovePending(id, "bot", "kept", c.t); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < oauthClientLimit-1; i++ {
		oauthRegister(t, h)
	}
	w := oauthRequest(h, "POST", "/oauth/register", `{"redirect_uris":["https://example.test/cb"]}`, "application/json")
	if w.Code != 429 {
		t.Fatalf("client limit: %d %s", w.Code, w.Body.String())
	}
	c.t = c.t.Add(7 * 24 * time.Hour)
	replacement := oauthRegister(t, h)
	if replacement == kept {
		t.Fatal("client ID reused")
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM oauth_clients WHERE client_id=?", kept).Scan(&count); err != nil || count != 1 {
		t.Fatalf("approved client removed: %d %v", count, err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM oauth_clients").Scan(&count); err != nil || count != 2 {
		t.Fatalf("cleanup count: %d %v", count, err)
	}
}
func TestOAuthCodeBindingsAndExpiry(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	_, id := oauthPage(t, oauthAuthorize(h, client, nil))
	if err := s.ApprovePending(id, "bot", "bindings", c.t); err != nil {
		t.Fatal(err)
	}
	code := oauthRedirectCode(t, oauthStatus(t, h, id))
	for _, v := range []url.Values{oauthExchange(code, "other", oauthVerifier), oauthExchange(code, client, "bad"), {"grant_type": {"authorization_code"}, "code": {code}, "client_id": {client}, "redirect_uri": {"https://evil.example/"}, "code_verifier": {oauthVerifier}}} {
		if w := oauthToken(h, v); w.Code != 400 {
			t.Fatalf("accepted %v: %d %s", v, w.Code, w.Body.String())
		}
	}
	c.t = c.t.Add(2 * time.Minute)
	if w := oauthToken(h, oauthExchange(code, client, oauthVerifier)); w.Code != 400 || oauthJSON(t, w)["error"] != "invalid_grant" {
		t.Fatalf("expired code: %d %s", w.Code, w.Body.String())
	}
}
func TestOAuthApprovalAndTokenConcurrency(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	_, id := oauthPage(t, oauthAuthorize(h, client, nil))
	errs := make(chan error, 2)
	for i := range 2 {
		go func(i int) { errs <- s.ApprovePending(id, "bot", fmt.Sprintf("app %d", i), c.t) }(i)
	}
	success := 0
	for range 2 {
		if err := <-errs; err == nil {
			success++
		} else if !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("approval successes: %d", success)
	}
	apps, err := s.ListApps()
	if err != nil || len(apps) != 1 {
		t.Fatalf("orphan apps: %+v %v", apps, err)
	}
	code := oauthRedirectCode(t, oauthStatus(t, h, id))
	results := make(chan int, 2)
	for range 2 {
		go func() { results <- oauthToken(h, oauthExchange(code, client, oauthVerifier)).Code }()
	}
	success = 0
	for range 2 {
		status := <-results
		if status == 200 {
			success++
		} else if status != 400 {
			t.Fatalf("exchange status %d", status)
		}
	}
	if success != 1 {
		t.Fatalf("exchange successes: %d", success)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM app_tokens WHERE app_id=?", apps[0].ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("replay did not revoke issued tokens: %d %v", count, err)
	}
}
func TestOAuthRefreshClientBinding(t *testing.T) {
	s, c, h := oauthFixture(t)
	client, other := oauthRegister(t, h), oauthRegister(t, h)
	_, id := oauthPage(t, oauthAuthorize(h, client, nil))
	if err := s.ApprovePending(id, "bot", "bound", c.t); err != nil {
		t.Fatal(err)
	}
	code := oauthRedirectCode(t, oauthStatus(t, h, id))
	tokens := oauthJSON(t, oauthToken(h, oauthExchange(code, client, oauthVerifier)))
	refresh := tokens["refresh_token"].(string)
	w := oauthToken(h, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {other}})
	if w.Code != 400 || oauthJSON(t, w)["error"] != "invalid_grant" {
		t.Fatalf("wrong client: %d %s", w.Code, w.Body.String())
	}
	w = oauthToken(h, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {client}})
	if w.Code != 200 {
		t.Fatalf("correct client: %d %s", w.Code, w.Body.String())
	}
}

func TestOAuthCompletedCleanup(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	page := oauthAuthorize(h, client, nil)
	_, first := oauthPage(t, page)
	cookie := oauthBrowserCookie(t, page, c.t)
	c.t = c.t.Add(9 * time.Minute)
	if err := s.ApprovePending(first, "bot", "late approval", c.t); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(30 * time.Second)
	// A recent approval can be reused from the same browser.
	retry := oauthAuthorize(oauthWithCookie(h, cookie), client, nil)
	match := oauthPageID.FindStringSubmatch(retry.Body.String())
	if len(match) != 2 || strings.Contains(retry.Body.String(), `id="user-code"`) {
		t.Fatalf("late approval reuse: %s", retry.Body.String())
	}
	c.t = c.t.Add(11 * time.Minute)
	oauthPage(t, oauthAuthorize(h, client, nil))
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM oauth_pending WHERE id IN (?,?)", first, match[1]).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old completed requests not removed: %d %v", count, err)
	}
}
func TestOAuthEscapingAndDefaultGrants(t *testing.T) {
	s, c, h := oauthFixture(t)
	w := oauthRequest(h, "POST", "/oauth/register", `{"client_name":"<script>alert(1)</script>","redirect_uris":["https://client.example/callback?existing=yes"]}`, "application/json")
	client := oauthJSON(t, w)["client_id"].(string)
	page := oauthAuthorize(h, client, nil)
	if strings.Contains(page.Body.String(), "<script>alert(1)</script>") || !strings.Contains(page.Body.String(), "&lt;script&gt;") {
		t.Fatalf("unsafe client name: %s", page.Body.String())
	}
	_, id := oauthPage(t, page)
	if err := s.ApprovePending(id, "bot", "default", c.t); err != nil {
		t.Fatal(err)
	}
	code := oauthRedirectCode(t, oauthStatus(t, h, id))
	w = oauthToken(h, oauthExchange(code, client, oauthVerifier))
	if w.Code != 200 {
		t.Fatalf("exchange: %d %s", w.Code, w.Body.String())
	}
	if _, ok := oauthJSON(t, w)["refresh_token"]; ok {
		t.Fatal("refresh issued without a registered refresh grant")
	}
}

func TestOAuthClientNameControlCharacters(t *testing.T) {
	_, _, h := oauthFixture(t)
	for _, name := range []string{"bad\x1b[2Jname", "line\nbreak", "bad\x00name"} {
		raw, err := json.Marshal(map[string]any{"client_name": name, "redirect_uris": []string{"https://example.test/callback"}})
		if err != nil {
			t.Fatal(err)
		}
		w := oauthRequest(h, "POST", "/oauth/register", string(raw), "application/json")
		if w.Code != 400 {
			t.Errorf("accepted control characters in client name: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestOAuthReplayWithWrongVerifierRevokes(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	_, id := oauthPage(t, oauthAuthorize(h, client, nil))
	if err := s.ApprovePending(id, "bot", "replay", c.t); err != nil {
		t.Fatal(err)
	}
	code := oauthRedirectCode(t, oauthStatus(t, h, id))
	w := oauthToken(h, oauthExchange(code, client, oauthVerifier))
	if w.Code != 200 {
		t.Fatalf("exchange: %d %s", w.Code, w.Body.String())
	}
	tokens := oauthJSON(t, w)
	w = oauthToken(h, oauthExchange(code, client, strings.Repeat("z", 43)))
	if w.Code != 400 {
		t.Fatalf("replay: %d", w.Code)
	}
	if _, err := s.AppForToken(tokens["access_token"].(string), "access", c.t); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("replay with wrong verifier left tokens valid: %v", err)
	}
}

func TestOAuthAddressRateLimit(t *testing.T) {
	s, c := newTestStore(t)
	g := &Gateway{store: s, limiter: ratelimit.New(ratelimit.Config{}, c.now), opts: Options{PublicURL: "https://gateway.example"}}
	mux := http.NewServeMux()
	g.registerOAuth(mux)
	h := g.appsMiddleware(mux)
	for i := 0; i < 31; i++ {
		w := oauthRequest(h, "GET", "/.well-known/oauth-authorization-server", "", "")
		if i == 30 && (w.Code != 429 || w.Header().Get("Retry-After") == "") {
			t.Fatalf("request 31: %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
		}
	}
}

func TestOAuthFailuresBanAddress(t *testing.T) {
	for _, tc := range []struct{ name, method, path, body, contentType string }{
		{"registration", "POST", "/oauth/register", `{}`, "application/json"},
		{"code", "POST", "/oauth/token", "grant_type=authorization_code&client_id=missing&code=missing&redirect_uri=https%3A%2F%2Fexample.test&code_verifier=missing", "application/x-www-form-urlencoded"},
		{"status", "GET", "/oauth/authorize/status?id=missing", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c := newTestStore(t)
			g := &Gateway{store: s, limiter: ratelimit.New(ratelimit.Config{}, c.now), logger: log.New(io.Discard, "", 0)}
			mux := http.NewServeMux()
			g.registerOAuth(mux)
			h := g.appsMiddleware(mux)
			for i := 0; i < 10; i++ {
				w := oauthRequest(h, tc.method, tc.path, tc.body, tc.contentType)
				if w.Code == 429 {
					t.Fatalf("early ban at %d", i)
				}
			}
			if w := oauthRequest(h, tc.method, tc.path, tc.body, tc.contentType); w.Code != 429 {
				t.Fatalf("expected ban: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
