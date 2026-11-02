package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/niparis/agent-net/internal/ratelimit"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOAuthNoApprovalReuseSecurity(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	page := oauthAuthorize(h, client, nil)
	_, id := oauthPage(t, page)
	cookies := page.Result().Cookies()
	if err := s.ApprovePending(id, "bot", "test", c.t); err != nil {
		t.Fatal(err)
	}
	var cookie *http.Cookie
	if len(cookies) > 0 {
		cookie = cookies[0]
	}
	retry := oauthAuthorize(oauthWithCookie(h, cookie), client, nil)
	_, id = oauthPage(t, retry)
	if got := oauthStatus(t, h, id)["status"]; got != "pending" {
		t.Fatalf("reused approval: %v", got)
	}
	if len(cookies) != 0 {
		t.Fatal("approval cookie was written")
	}
}
func TestTokenRefreshReplayRevokesFamily(t *testing.T) {
	s, c := newTestStore(t)
	app, err := s.CreateApp("bot", "bot", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.IssueToken(app.ID, "refresh", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	access, next, err := s.RotateRefresh(old, c.t)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.RotateRefresh(old, c.t); err == nil {
		t.Fatal("replay accepted")
	}
	for _, x := range []struct{ v, k string }{{access, "access"}, {next, "refresh"}} {
		if _, err = s.AppForToken(x.v, x.k, c.t); err == nil {
			t.Fatalf("replay left %s valid", x.k)
		}
	}
}
func TestOAuthRegistrationAndPendingFairness(t *testing.T) {
	s, c, h := oauthFixture(t)
	for i := 0; i < 200; i++ {
		_, err := s.registerOAuthClient(oauthClient{ClientName: "flood"}, c.t, fmt.Sprintf("old-%d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE oauth_clients SET source='one-flood-source'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.registerOAuthClient(oauthClient{ClientName: "other"}, c.t, "new-source"); err != nil {
		t.Fatalf("full pool blocks new source: %v", err)
	}
	client := oauthRegister(t, h)
	for i := 0; i < 3; i++ {
		if w := oauthAuthorize(h, client, nil); w.Code != 200 {
			t.Fatalf("pending %d: %d", i, w.Code)
		}
	}
	if w := oauthAuthorize(h, client, nil); w.Code != 429 {
		t.Fatalf("client exceeded pending limit: %d", w.Code)
	}
}

type poisonBody struct{ read bool }

func (p *poisonBody) Read([]byte) (int, error) { p.read = true; return 0, io.EOF }
func (*poisonBody) Close() error               { return nil }
func TestAppsAuthenticateBeforeBody(t *testing.T) {
	g, _, _, _ := botFixture(t)
	p := &poisonBody{}
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Body = p
	r.Host = "apps.example.test"
	w := httptest.NewRecorder()
	g.AppsHandler().ServeHTTP(w, r)
	if p.read || w.Code != 401 {
		t.Fatalf("unauthorized body read=%v status=%d", p.read, w.Code)
	}
}
func TestAppsSessionLimit(t *testing.T) {
	g, _, token, _ := botFixture(t)
	h := g.AppsHandler()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
	for i := 0; i < 11; i++ {
		r := httptest.NewRequest("POST", "http://apps.example.test/mcp", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if i < 10 && w.Code != 200 {
			t.Fatalf("initialize %d: %d %s", i, w.Code, w.Body.String())
		}
		if i == 10 && w.Code == 200 {
			t.Fatal("eleventh session accepted")
		}
	}
}
func TestAppsPublicHostValidation(t *testing.T) {
	g, _, token, _ := botFixture(t)
	g.opts.PublicURL = "https://host.example:8443"
	server := httptest.NewServer(g.AppsHandler())
	defer server.Close()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
	for _, tc := range []struct {
		host, origin string
		valid        bool
	}{{"host.example", "https://host.example:8443", false}, {"host.example:8443", "", true}, {"wrong.example", "", false}, {"host.example", "https://wrong.example", false}} {
		r, err := http.NewRequest("POST", server.URL+"/mcp", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Host = tc.host
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if (response.StatusCode == 200) != tc.valid {
			t.Errorf("host %s origin %s: %d %s", tc.host, tc.origin, response.StatusCode, payload)
		}
	}
}

func TestOAuthLogUntrustedClientIDBounded(t *testing.T) {
	s, c := newTestStore(t)
	var logs bytes.Buffer
	g := &Gateway{store: s, limiter: ratelimit.New(ratelimit.Config{}, c.now), logger: log.New(&logs, "", 0)}
	v := url.Values{"grant_type": {"refresh_token"}, "client_id": {strings.Repeat("x", 16000)}, "refresh_token": {"bad"}}
	r := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	g.oauthToken(w, r)
	if strings.Contains(logs.String(), strings.Repeat("x", 65)) {
		t.Fatalf("unbounded log length %d", logs.Len())
	}
}
func TestTokenAccessCap(t *testing.T) {
	s, c := newTestStore(t)
	app, err := s.CreateApp("bot", "bot", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 21; i++ {
		if _, err = s.IssueToken(app.ID, "access", time.Hour); err != nil {
			t.Fatal(err)
		}
		c.t = c.t.Add(time.Second)
	}
	var n int
	if err = s.db.QueryRow("SELECT count(*) FROM app_tokens WHERE app_id=? AND type='access'", app.ID).Scan(&n); err != nil || n > 20 {
		t.Fatalf("access tokens %d: %v", n, err)
	}
}
func TestAppsApprovalNotice(t *testing.T) {
	s, _, h := oauthFixture(t)
	client := oauthRegister(t, h)
	_, id := oauthPage(t, oauthAuthorize(h, client, nil))
	var logs bytes.Buffer
	g := &Gateway{store: s, logger: log.New(&logs, "", 0)}
	node, _, err := s.CreateNode("example", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertSession(node, owner("example")); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"app_id": id, "kind": "bot", "name": "my bot"})
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "apps.approve", raw); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	want := "Connected: my bot (bot, Cursor, client.example). If this was not you, revoke it in Settings."
	if len(items) != 1 || items[0].Text != want || !strings.Contains(logs.String(), want) {
		t.Fatalf("notice %v logs %s", fmt.Sprint(items), logs.String())
	}
	if _, err := s.AnswerInbox(items[0].ID, "seen"); err != nil {
		t.Fatalf("notice cannot be dismissed: %v", err)
	}
}

func TestOAuthSourceAndPendingPools(t *testing.T) {
	s, c, h := oauthFixture(t)
	body := `{"redirect_uris":["https://client.example/callback?existing=yes"]}`
	register := func(source string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/oauth/register", strings.NewReader(body))
		r.RemoteAddr = source + ":1234"
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	var client string
	for i := 0; i < 5; i++ {
		w := register("192.0.2.1")
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body)
		}
		client = oauthJSON(t, w)["client_id"].(string)
	}
	if w := register("192.0.2.1"); w.Code != 429 {
		t.Fatalf("sixth registration: %d", w.Code)
	}
	if w := register("192.0.2.2"); w.Code != 201 {
		t.Fatalf("other source: %d", w.Code)
	}
	for i := 0; i < 3; i++ {
		_, err := s.newOAuthPending(client, oauthCallback, oauthChallenge(oauthVerifier), "", "", "192.0.2.1", c.t)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.newOAuthPending(client, oauthCallback, oauthChallenge(oauthVerifier), "", "", "192.0.2.2", c.t); err != errOAuthLimit {
		t.Fatalf("client pending limit: %v", err)
	}
	for i := 0; i < 20; i++ {
		cli, err := s.registerOAuthClient(oauthClient{RedirectURIs: []string{oauthCallback}, GrantTypes: []string{"authorization_code"}, ResponseTypes: []string{"code"}}, c.t, fmt.Sprintf("registration-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.newOAuthPending(cli.ClientID, oauthCallback, oauthChallenge(oauthVerifier), "", "", fmt.Sprintf("source-%d", i), c.t.Add(time.Duration(i)*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM oauth_pending WHERE status='pending'`).Scan(&count); err != nil || count != 20 {
		t.Fatalf("pending count %d: %v", count, err)
	}
}
func TestTokenCleanupUsedRetention(t *testing.T) {
	s, c := newTestStore(t)
	app, err := s.CreateApp("cleanup", "bot", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.IssueToken(app.ID, "refresh", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.RotateRefresh(old, c.t)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteExpiredTokens(c.t.Add(89 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = s.db.QueryRow(`SELECT count(*) FROM app_tokens WHERE hash=?`, hashHex(old)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("used hash removed early %d %v", n, err)
	}
	if err = s.DeleteExpiredTokens(c.t.Add(90 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT count(*) FROM app_tokens WHERE hash=?`, hashHex(old)).Scan(&n); err != nil || n != 0 {
		t.Fatalf("used hash retained %d %v", n, err)
	}
}
func TestAppsSessionRevocationAndOrigin(t *testing.T) {
	g, app, token, _ := botFixture(t)
	h := g.AppsHandler()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
	request := func(origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://apps.example.test/mcp", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("https://evil.example"); w.Code == 200 {
		t.Fatal("wrong origin accepted")
	}
	w := request("https://apps.example.test")
	id := w.Header().Get("Mcp-Session-Id")
	if w.Code != 200 || id == "" {
		t.Fatalf("session: %d %s", w.Code, w.Body.String())
	}
	appSessionPools.Lock()
	pool := appSessionPools.byStore[g.store]
	appSessionPools.Unlock()
	pool.mu.Lock()
	before := len(pool.live())
	pool.mu.Unlock()
	if before != 1 {
		t.Fatalf("sessions before revoke: %d", before)
	}
	if err := g.store.RevokeApp(app.ID); err != nil {
		t.Fatal(err)
	}
	pool.mu.Lock()
	after := len(pool.live())
	pool.mu.Unlock()
	if after != 0 {
		t.Fatalf("sessions after revoke: %d", after)
	}
}
func TestOAuthRefreshFailureBans(t *testing.T) {
	s, c := newTestStore(t)
	g := &Gateway{store: s, limiter: ratelimit.New(ratelimit.Config{FailureLimit: 1}, c.now), logger: log.New(io.Discard, "", 0)}
	r := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(url.Values{"grant_type": {"refresh_token"}, "client_id": {"unknown"}, "refresh_token": {"bad"}}.Encode()))
	r.RemoteAddr = "192.0.2.8:1234"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	g.oauthToken(httptest.NewRecorder(), r)
	if !g.limiter.Banned("192.0.2.8") {
		t.Fatal("refresh failure did not count")
	}
}

func TestAppTokenFamilyMigrationUpgrade(t *testing.T) {
	s, c := newTestStore(t)
	app, err := s.CreateApp("legacy", "bot", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.IssueToken(app.ID, "refresh", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`DROP INDEX app_tokens_family; DROP INDEX oauth_clients_source; DROP INDEX oauth_pending_source; DROP TABLE oauth_registration_attempts; ALTER TABLE app_tokens DROP COLUMN family_id; ALTER TABLE app_tokens DROP COLUMN used_at; ALTER TABLE oauth_clients DROP COLUMN source; ALTER TABLE oauth_pending DROP COLUMN source; DELETE FROM schema_version WHERE version >= (SELECT max(version)-2 FROM schema_version)`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(); err != nil {
		t.Fatal(err)
	}
	var family string
	if err = s.db.QueryRow(`SELECT family_id FROM app_tokens WHERE hash=?`, hashHex(raw)).Scan(&family); err != nil || family != app.ID {
		t.Fatalf("legacy family %q: %v", family, err)
	}
	access, next, err := s.RotateRefresh(raw, c.t)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.RotateRefresh(raw, c.t); err == nil {
		t.Fatal("legacy token replay accepted")
	}
	for _, token := range []struct{ v, k string }{{access, "access"}, {next, "refresh"}} {
		if _, err = s.AppForToken(token.v, token.k, c.t); err == nil {
			t.Fatalf("legacy replay left %s usable", token.k)
		}
	}
}
