package gateway

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/ratelimit"
)

func TestReviewPublicOffSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	r := &publicRunner{}
	opts := Options{DataDir: dir, AppsListen: "127.0.0.1:0", FunnelRunner: r}
	g, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	publicCall(t, g, "apps.public.on", nil)
	publicCall(t, g, "apps.public.off", nil)
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	g, err = New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if s := publicCall(t, g, "apps.public.status", nil); s["on"] != false || g.publicServer != nil {
		t.Fatalf("restart: %v listener=%v", s, g.publicServer)
	}
}

func TestReviewPublicOffFailureStopsAndRetries(t *testing.T) {
	r := &publicRunner{}
	opts := Options{DataDir: t.TempDir(), AppsListen: "127.0.0.1:0", FunnelRunner: r}
	g, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Close() }()
	publicCall(t, g, "apps.public.on", nil)
	r.failOff = true
	_, err = g.publicOperation(context.Background(), "apps.public.off", nil)
	if err == nil || g.publicServer != nil || g.public.On {
		t.Fatalf("off error=%v status=%+v listener=%v", err, g.public, g.publicServer)
	}
	failed := publicCall(t, g, "apps.public.status", nil)
	if failed["on"] != false || failed["error"] == "" {
		t.Fatalf("cleanup failure status: %v", failed)
	}
	if err = g.Close(); err != nil {
		t.Fatal(err)
	}
	r.failOff = false
	g, err = New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if g.publicServer != nil {
		t.Fatal("listener restarted after failed cleanup")
	}
	s := publicCall(t, g, "apps.public.status", nil)
	if s["error"] != "" || r.target != "" {
		t.Fatalf("retry status=%v target=%q", s, r.target)
	}
}

func TestReviewOtherRoutes(t *testing.T) {
	r := &publicRunner{}
	g, err := New(Options{DataDir: t.TempDir(), AppsListen: "127.0.0.1:0", FunnelRunner: r})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	publicCall(t, g, "apps.public.on", nil)
	s := publicCall(t, g, "apps.public.status", nil)
	routes, ok := s["other_routes"].([]any)
	if !ok || len(routes) != 1 {
		t.Fatalf("other routes: %v", s)
	}
	route := routes[0].(map[string]any)
	if route["port"] != float64(443) || route["target"] != "http://127.0.0.1:7411" {
		t.Fatal(route)
	}
}

func TestReviewOAuthOneApprovalOneFamily(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	_, id := oauthPage(t, oauthAuthorize(h, client, nil))
	if err := s.ApprovePending(id, "bot", "test", c.t); err != nil {
		t.Fatal(err)
	}
	first := oauthRedirectCode(t, oauthStatus(t, h, id))
	second := oauthRedirectCode(t, oauthStatus(t, h, id))
	if first != second {
		t.Fatalf("poll changed code: %q %q", first, second)
	}
	if w := oauthToken(h, oauthExchange(first, client, oauthVerifier)); w.Code != 200 {
		t.Fatalf("exchange: %d %s", w.Code, w.Body.String())
	}
	if got := oauthStatus(t, h, id); got["status"] != "used" || got["redirect"] != nil {
		t.Fatalf("used status: %v", got)
	}
	var appID string
	if err := s.db.QueryRow(`SELECT app_id FROM oauth_pending WHERE id=?`, id).Scan(&appID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeApp(appID); err != nil {
		t.Fatal(err)
	}
	if got := oauthStatus(t, h, id); got["status"] != "used" || got["redirect"] != nil {
		t.Fatalf("used after revocation: %v", got)
	}
}

func TestReviewPhoneTokenPurpose(t *testing.T) {
	s, c, h, _ := phoneFixture(t)
	owner, err := s.CreateApp("oauth owner", "owner_app", "oauth", "owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	oauth, err := s.IssueToken(owner.ID, "access", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if w := phoneRequest(h, "POST", "/action", &http.Cookie{Name: phoneCookieName, Value: oauth}, "https://gateway.example"); w.Code != 401 {
		t.Fatalf("OAuth token accepted as phone: %d", w.Code)
	}
	cookie, _, _ := phonePair(t, s, c, h, "owner_app")
	if _, _, err := s.phoneAppForToken(cookie.Value, c.t); err != nil {
		t.Fatalf("phone token rejected: %v", err)
	}
	if _, err := s.AppForToken(cookie.Value, "access", c.t); err == nil {
		t.Fatal("phone token accepted as OAuth bearer")
	}
}

func TestReviewRefreshRotationBudgetAndRetention(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	_, id := oauthPage(t, oauthAuthorize(h, client, nil))
	if err := s.ApprovePending(id, "bot", "budget", c.t); err != nil {
		t.Fatal(err)
	}
	code := oauthRedirectCode(t, oauthStatus(t, h, id))
	tokens := oauthJSON(t, oauthToken(h, oauthExchange(code, client, oauthVerifier)))
	refresh := tokens["refresh_token"].(string)
	var logs bytes.Buffer
	s.logf = func(event string, args ...any) { fmt.Fprintln(&logs, event, args) }
	for i := 0; i < 200; i++ {
		w := oauthToken(h, url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {refresh}})
		if w.Code != 200 {
			t.Fatalf("rotation %d: %d %s", i, w.Code, w.Body.String())
		}
		refresh = oauthJSON(t, w)["refresh_token"].(string)
	}
	var appID string
	if err := s.db.QueryRow(`SELECT app_id FROM app_tokens WHERE hash=?`, hashHex(refresh)).Scan(&appID); err != nil {
		t.Fatal(err)
	}
	w := oauthToken(h, url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {refresh}})
	if w.Code != 400 || oauthJSON(t, w)["error"] != "invalid_grant" || !strings.Contains(logs.String(), "rotation") {
		t.Fatalf("budget: %d %s logs=%s", w.Code, w.Body.String(), logs.String())
	}
	for i := 0; i < 2001; i++ {
		_, err := s.db.Exec(`INSERT INTO app_tokens(hash,app_id,type,expires_at,created_at,family_id,used_at) VALUES(?,?,'refresh',?,?,?,?)`, fmt.Sprintf("used-%d", i), appID, c.t.Add(time.Hour).UnixNano(), c.t.Add(-time.Hour).UnixNano(), fmt.Sprintf("family-%d", i), c.t.Add(-time.Hour).UnixNano())
		if err != nil {
			t.Fatal(err)
		}
	}
	fresh, err := s.IssueToken(appID, "refresh", 90*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.RotateRefresh(fresh, c.t); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM app_tokens WHERE app_id=? AND type='refresh' AND used_at IS NOT NULL`, appID).Scan(&count); err != nil || count != 2000 {
		t.Fatalf("retained %d: %v", count, err)
	}
}

func TestReviewBadAuthorityCountsAndBans(t *testing.T) {
	s, c := newTestStore(t)
	g := &Gateway{store: s, opts: Options{PublicURL: "https://host.example:8443"}, limiter: ratelimit.New(ratelimit.Config{FailureLimit: 2, OAuth: ratelimit.Rate{Requests: 100, Per: time.Minute}}, c.now), logger: log.New(&bytes.Buffer{}, "", 0)}
	h := g.AppsHandler()
	request := func(host, origin string) int {
		r := httptest.NewRequest("GET", "https://host.example:8443/.well-known/oauth-authorization-server", nil)
		r.Host = host
		r.RemoteAddr = "192.0.2.9:1234"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if got := request("host.example:8443", ""); got != 200 {
		t.Fatalf("valid host: %d", got)
	}
	if got := request("host.example", ""); got != 421 {
		t.Fatalf("missing port: %d", got)
	}
	if got := request("host.example:8443", "https://evil.example"); got != 421 {
		t.Fatalf("bad origin: %d", got)
	}
	if got := request("host.example", ""); got != 429 {
		t.Fatalf("banned bad host: %d", got)
	}
}

func TestReviewOAuthLogsClientAddress(t *testing.T) {
	s, c := newTestStore(t)
	var logs bytes.Buffer
	g := &Gateway{store: s, opts: Options{PublicURL: "https://gateway.example"}, logger: log.New(&logs, "", 0), limiter: ratelimit.New(ratelimit.Config{OAuth: ratelimit.Rate{Requests: 100, Per: time.Minute}}, c.now)}
	h := g.AppsHandler()
	send := func(method, path, body, ct string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://gateway.example"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:7777"
		r.Header.Set("X-Forwarded-For", "198.51.100.42")
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := send("POST", "/oauth/register", `{"client_name":"test","redirect_uris":["https://client.example/callback?existing=yes"],"grant_types":["authorization_code","refresh_token"]}`, "application/json")
	client := oauthJSON(t, w)["client_id"].(string)
	q := url.Values{"response_type": {"code"}, "client_id": {client}, "redirect_uri": {oauthCallback}, "code_challenge": {oauthChallenge(oauthVerifier)}, "code_challenge_method": {"S256"}}
	_, id := oauthPage(t, send("GET", "/oauth/authorize?"+q.Encode(), "", ""))
	if err := s.ApprovePending(id, "bot", "test", c.t); err != nil {
		t.Fatal(err)
	}
	status := oauthJSON(t, send("GET", "/oauth/authorize/status?id="+id, "", ""))
	u, err := url.Parse(status["redirect"].(string))
	if err != nil {
		t.Fatal(err)
	}
	send("POST", "/oauth/token", oauthExchange(u.Query().Get("code"), client, oauthVerifier).Encode(), "application/x-www-form-urlencoded")
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if strings.Contains(line, "oauth_") && (!strings.Contains(line, "198.51.100.42") || strings.Contains(line, "127.0.0.1:7777")) {
			t.Fatalf("wrong source: %s", line)
		}
	}
}

func TestReviewExpiredOAuthCodeCannotBeReminted(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	_, id := oauthPage(t, oauthAuthorize(h, client, nil))
	if err := s.ApprovePending(id, "bot", "expiry", c.t); err != nil {
		t.Fatal(err)
	}
	oauthRedirectCode(t, oauthStatus(t, h, id))
	c.t = c.t.Add(oauthCodeTTL + time.Second)
	if err := s.CleanupOAuth(c.t); err != nil {
		t.Fatal(err)
	}
	got := oauthStatus(t, h, id)
	if got["status"] != "expired" || got["redirect"] != nil {
		t.Fatalf("expired code reminted: %v", got)
	}
}
func TestReviewTokenPurposeMigration(t *testing.T) {
	s, c := newTestStore(t)
	app, err := s.CreateApp("legacy phone", "owner_app", "phone", "phone", nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.IssueToken(app.ID, "access", phoneTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	// Re-run the purpose migration too, which is no longer the last one.
	if _, err = s.db.Exec(`ALTER TABLE app_tokens DROP COLUMN purpose; ALTER TABLE oauth_codes DROP COLUMN retry_code; DROP TABLE app_refresh_budget; DELETE FROM schema_version WHERE version >= ?`, migrationVersion(t, "App token purpose, OAuth retry code")); err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(); err != nil {
		t.Fatal(err)
	}
	var purpose string
	if err = s.db.QueryRow(`SELECT purpose FROM app_tokens WHERE hash=?`, hashHex(token)).Scan(&purpose); err != nil || purpose != "oauth" {
		t.Fatalf("purpose=%q err=%v", purpose, err)
	}
	if _, _, err = s.phoneAppForToken(token, c.t); err == nil {
		t.Fatal("legacy token accepted as phone")
	}
}

func TestReviewDefaultPortAuthority(t *testing.T) {
	for _, tc := range []struct {
		base, host, origin string
		want               bool
	}{
		{"https://host.example", "host.example:443", "https://host.example:443", true},
		{"https://host.example:443", "host.example", "https://host.example", true},
		{"http://host.example", "host.example:80", "http://host.example:80", true},
		{"https://host.example:8443", "host.example", "", false},
		{"https://host.example", "host.example:", "", false},
	} {
		r := httptest.NewRequest("GET", "https://unused/", nil)
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		if got := validPublicHost(r, tc.base); got != tc.want {
			t.Errorf("%+v got %v", tc, got)
		}
	}
}
func TestReviewBadHostUsesAddressRate(t *testing.T) {
	s, c := newTestStore(t)
	g := &Gateway{store: s, opts: Options{PublicURL: "https://host.example"}, limiter: ratelimit.New(ratelimit.Config{FailureLimit: 100, OAuth: ratelimit.Rate{Requests: 1, Per: time.Minute}}, c.now), logger: log.New(&bytes.Buffer{}, "", 0)}
	h := g.AppsHandler()
	for i, want := range []int{421, 429} {
		r := httptest.NewRequest("GET", "https://wrong.example/unknown", nil)
		r.RemoteAddr = "192.0.2.10:1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("request %d: %d want %d", i, w.Code, want)
		}
	}
}

func TestReviewPublicURLFlagNeedsSwitch(t *testing.T) {
	r := &publicRunner{missing: true}
	g, err := New(Options{DataDir: t.TempDir(), AppsListen: "127.0.0.1:0", PublicURL: "https://tunnel.example", FunnelRunner: r})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if g.publicServer != nil {
		t.Fatal("listener started before switch")
	}
	got := publicCall(t, g, "apps.public.on", nil)
	if got["url"] != "https://tunnel.example" || got["port"] != float64(0) || g.publicServer == nil {
		t.Fatalf("custom URL: %v", got)
	}
}

func TestReviewLegacyMultipleCodesOneFamily(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	_, id := oauthPage(t, oauthAuthorize(h, client, nil))
	if err := s.ApprovePending(id, "bot", "legacy", c.t); err != nil {
		t.Fatal(err)
	}
	first := oauthRedirectCode(t, oauthStatus(t, h, id))
	second, err := oauthRandom(32)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO oauth_codes(hash,pending_id,app_id,client_id,redirect_uri,code_challenge,expires_at,retry_code) SELECT ?,pending_id,app_id,client_id,redirect_uri,code_challenge,expires_at,? FROM oauth_codes WHERE hash=?`, hashHex(second), second, hashHex(first)); err != nil {
		t.Fatal(err)
	}
	if w := oauthToken(h, oauthExchange(first, client, oauthVerifier)); w.Code != 200 {
		t.Fatalf("first exchange: %d %s", w.Code, w.Body.String())
	}
	if got := oauthStatus(t, h, id); got["status"] != "used" || got["redirect"] != nil {
		t.Fatalf("status %v", got)
	}
	if w := oauthToken(h, oauthExchange(second, client, oauthVerifier)); w.Code != 400 || oauthJSON(t, w)["error"] != "invalid_grant" {
		t.Fatalf("second exchange: %d %s", w.Code, w.Body.String())
	}
}

type noRoutesRunner struct{}

func (noRoutesRunner) Run(_ context.Context, args ...string) (string, string, error) {
	if args[0] == "status" {
		return `{"BackendState":"Running","Self":{"DNSName":"test.example.ts.net.","Capabilities":["funnel"]}}`, "", nil
	}
	if args[0] == "serve" {
		return `{}`, "", nil
	}
	return "", "", nil
}
func TestReviewOtherRoutesEmpty(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir(), AppsListen: "127.0.0.1:0", FunnelRunner: noRoutesRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	s := publicCall(t, g, "apps.public.status", nil)
	routes, ok := s["other_routes"].([]any)
	if !ok || len(routes) != 0 {
		t.Fatalf("other routes: %v", s)
	}
}

func TestReviewRefreshRetentionCleanup(t *testing.T) {
	s, c := newTestStore(t)
	app, err := s.CreateApp("legacy used", "bot", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2001; i++ {
		_, err = tx.Exec(`INSERT INTO app_tokens(hash,app_id,type,expires_at,created_at,family_id,used_at) VALUES(?,?,'refresh',?,?,?,?)`, fmt.Sprintf("legacy-%d", i), app.ID, c.t.Add(time.Hour).UnixNano(), c.t.UnixNano(), "legacy", c.t.UnixNano()+int64(i))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Re-run the retention migration, which is no longer the last one.
	if _, err = s.db.Exec(`DELETE FROM schema_version WHERE version >= ?`, migrationVersion(t, "Bound consumed refresh history")); err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(); err != nil {
		t.Fatal(err)
	}
	var afterMigration int
	if err = s.db.QueryRow(`SELECT count(*) FROM app_tokens WHERE app_id=? AND type='refresh' AND used_at IS NOT NULL`, app.ID).Scan(&afterMigration); err != nil || afterMigration != 2000 {
		t.Fatalf("migration count=%d err=%v", afterMigration, err)
	}
	if _, err = s.db.Exec(`INSERT INTO app_tokens(hash,app_id,type,expires_at,created_at,family_id,used_at) VALUES('extra',?,'refresh',?,?,?,?)`, app.ID, c.t.Add(time.Hour).UnixNano(), c.t.UnixNano(), "legacy", c.t.UnixNano()-1); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteExpiredTokens(c.t); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM app_tokens WHERE app_id=? AND type='refresh' AND used_at IS NOT NULL`, app.ID).Scan(&count); err != nil || count != 2000 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	var old int
	if err = s.db.QueryRow(`SELECT count(*) FROM app_tokens WHERE hash='legacy-0'`).Scan(&old); err != nil || old != 0 {
		t.Fatalf("oldest remains=%d err=%v", old, err)
	}
}
