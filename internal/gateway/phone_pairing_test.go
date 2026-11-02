package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/ratelimit"
)

var phoneCodeRE = regexp.MustCompile(`id="user-code">([A-HJ-NP-Z2-9]{3}-[A-HJ-NP-Z2-9]{3})<`)
var phoneIDRE = regexp.MustCompile(`/phone/status\?id=([A-Za-z0-9_-]+)`)

func phoneFixture(t *testing.T) (*Store, *clock, http.Handler, *bytes.Buffer) {
	t.Helper()
	s, c := newTestStore(t)
	var logs bytes.Buffer
	g := &Gateway{store: s, limiter: ratelimit.New(ratelimit.Config{Page: ratelimit.Rate{Requests: 10000, Per: time.Minute}, OAuth: ratelimit.Rate{Requests: 10000, Per: time.Minute}, FailureLimit: 10000}, c.now), opts: Options{PublicURL: "https://gateway.example"}, logger: log.New(&logs, "", 0), conns: map[string]*nodeConn{}}
	return s, c, g.AppsHandler(), &logs
}
func phoneRequest(h http.Handler, method, path string, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://gateway.example"+path, nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("User-Agent", "Mozilla/5.0 (iPhone) AppleWebKit/605.1.15 Version/17.0 Mobile Safari/604.1")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func phonePair(t *testing.T, s *Store, c *clock, h http.Handler, kind string) (*http.Cookie, string, string) {
	t.Helper()
	page := phoneRequest(h, "GET", "/", nil, "")
	code, id := phoneCodeRE.FindStringSubmatch(page.Body.String()), phoneIDRE.FindStringSubmatch(page.Body.String())
	if page.Code != 200 || len(code) != 2 || len(id) != 2 || !strings.Contains(page.Body.String(), "Enter this code in your agent-net TUI: S, Connected apps, Add") {
		t.Fatalf("pairing page: %d %s", page.Code, page.Body.String())
	}
	decoded, err := base64.RawURLEncoding.DecodeString(id[1])
	if err != nil || len(decoded) != 16 {
		t.Fatalf("bad polling id %q", id[1])
	}
	p, err := s.PendingByUserCode(code[1], "tui", c.t)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(p)
	if p.ClientName != "iPhone Safari" || !strings.Contains(string(encoded), `"DefaultKind":"owner_app"`) || p.RedirectURI != "" {
		t.Fatalf("pending: %+v", p)
	}
	if err = s.ApprovePending(p.ID, kind, "My phone", c.t); err != nil {
		t.Fatal(err)
	}
	poll := phoneRequest(h, "GET", "/phone/status?id="+id[1], nil, "")
	var cookie *http.Cookie
	for _, v := range poll.Result().Cookies() {
		if v.Name == "__Host-agent_net_phone" {
			cookie = v
		}
	}
	if kind == "owner_app" {
		if poll.Code != 200 || cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge != int((90*24*time.Hour).Seconds()) {
			t.Fatalf("poll: %d %s cookie=%+v", poll.Code, poll.Body.String(), cookie)
		}
	} else if cookie != nil {
		t.Fatal("bot approval issued phone cookie")
	}
	return cookie, id[1], p.ID
}
func TestPhonePairingAndAuthorization(t *testing.T) {
	s, c, h, logs := phoneFixture(t)
	cookie, id, _ := phonePair(t, s, c, h, "owner_app")
	home := phoneRequest(h, "GET", "/", cookie, "")
	if home.Code != 200 || phoneCodeRE.Match(home.Body.Bytes()) || !strings.Contains(home.Body.String(), "<!doctype html>") {
		t.Fatalf("home: %d %s", home.Code, home.Body.String())
	}
	again := phoneRequest(h, "GET", "/phone/status?id="+id, nil, "")
	if len(again.Result().Cookies()) != 0 {
		t.Fatal("pairing replay issued cookie")
	}
	for _, tc := range []struct {
		method, path, origin string
		want                 int
	}{{"POST", "/action", "", 403}, {"POST", "/action", "https://evil.example", 403}, {"POST", "/action", "http://gateway.example", 403}, {"POST", "/action", "https://gateway.example", 503}, {"GET", "/events", "", 401}, {"POST", "/action", "", 401}} {
		ck := cookie
		if tc.want == 401 {
			ck = nil
		}
		w := phoneRequest(h, tc.method, tc.path, ck, tc.origin)
		if w.Code != tc.want {
			t.Errorf("%s %s origin %q: %d want %d", tc.method, tc.path, tc.origin, w.Code, tc.want)
		}
	}
	if !strings.Contains(logs.String(), "My phone") {
		t.Errorf("action log missing app name: %s", logs.String())
	}
	apps, err := s.ListApps()
	if err != nil || len(apps) != 1 {
		t.Fatalf("apps: %v %v", apps, err)
	}
	if err = s.RevokeApp(apps[0].ID); err != nil {
		t.Fatal(err)
	}
	if w := phoneRequest(h, "GET", "/", cookie, ""); !phoneCodeRE.Match(w.Body.Bytes()) {
		t.Fatalf("revoked cookie accepted: %d %s", w.Code, w.Body.String())
	}
}
func TestPhoneRejectsBotAndExpiredTokens(t *testing.T) {
	s, c, h, _ := phoneFixture(t)
	bot, err := s.CreateApp("bot", "bot", "client", "bot", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.IssueToken(bot.ID, "access", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, raw string }{{"bot", raw}, {"malformed", "abc"}} {
		t.Run(tt.name, func(t *testing.T) {
			w := phoneRequest(h, "GET", "/", &http.Cookie{Name: "__Host-agent_net_phone", Value: tt.raw}, "")
			if !phoneCodeRE.Match(w.Body.Bytes()) {
				t.Fatalf("accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
	owner, err := s.CreateApp("old phone", "owner_app", "phone", "phone", nil)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := s.IssueToken(owner.ID, "access", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Hour)
	if w := phoneRequest(h, "GET", "/", &http.Cookie{Name: "__Host-agent_net_phone", Value: expired}, ""); !phoneCodeRE.Match(w.Body.Bytes()) {
		t.Fatalf("expired accepted: %d", w.Code)
	}
	phonePair(t, s, c, h, "bot")
}
func TestPhoneSlidingRenewal(t *testing.T) {
	s, c, h, _ := phoneFixture(t)
	cookie, _, _ := phonePair(t, s, c, h, "owner_app")
	c.t = c.t.Add(61 * 24 * time.Hour)
	w := phoneRequest(h, "GET", "/", cookie, "")
	if w.Code != 200 {
		t.Fatalf("renewal: %d", w.Code)
	}
	var renewed *http.Cookie
	for _, v := range w.Result().Cookies() {
		if v.Name == cookie.Name {
			renewed = v
		}
	}
	if renewed == nil || renewed.MaxAge != int((90*24*time.Hour).Seconds()) {
		t.Fatalf("no renewed cookie: %v", w.Result().Cookies())
	}
	c.t = c.t.Add(31 * 24 * time.Hour)
	if w = phoneRequest(h, "GET", "/", renewed, ""); phoneCodeRE.Match(w.Body.Bytes()) {
		t.Fatal("renewed token expired at original expiry")
	}
}

func TestPhonePendingSharesOAuthLimitAndExpires(t *testing.T) {
	s, c, oauth := oauthFixture(t)
	client := oauthRegister(t, oauth)
	for i := 0; i < oauthPendingLimit; i++ {
		if _, err := s.newPhonePending("Phone", c.t); err != nil {
			t.Fatal(err)
		}
	}
	if w := oauthAuthorize(oauth, client, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("OAuth ignored phone limit: %d %s", w.Code, w.Body.String())
	}
	if _, err := s.newPhonePending("Phone", c.t); !errors.Is(err, errOAuthLimit) {
		t.Fatalf("phone limit: %v", err)
	}
	c.t = c.t.Add(oauthPendingTTL)
	p, err := s.newPhonePending("Phone", c.t)
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(oauthPendingTTL)
	status, token, err := s.redeemPhonePending(p.ID, c.t)
	if err != nil || status != "expired" || token != "" {
		t.Fatalf("expired: %q %q %v", status, token, err)
	}
}

func TestPhoneStatusRefusalAndOAuthIsolation(t *testing.T) {
	s, c, h, _ := phoneFixture(t)
	p, err := s.newPhonePending("Phone", c.t)
	if err != nil {
		t.Fatal(err)
	}
	w := phoneRequest(h, "GET", "/phone/status?id="+p.ID, nil, "")
	if !strings.Contains(w.Body.String(), `"status":"pending"`) {
		t.Fatalf("pending: %d %s", w.Code, w.Body.String())
	}
	w = phoneRequest(h, "GET", "/oauth/authorize/status?id="+p.ID, nil, "")
	if !strings.Contains(w.Body.String(), `"status":"expired"`) {
		t.Fatalf("OAuth consumed phone request: %d %s", w.Code, w.Body.String())
	}
	if err = s.RefusePending(p.ID); err != nil {
		t.Fatal(err)
	}
	w = phoneRequest(h, "GET", "/phone/status?id="+p.ID, nil, "")
	if !strings.Contains(w.Body.String(), `"status":"refused"`) || len(w.Result().Cookies()) != 0 {
		t.Fatalf("refusal: %d %s", w.Code, w.Body.String())
	}
	if w = phoneRequest(h, "GET", "/phone/status?id=!!!!!!!!!!!!!!!!!!!!!!", nil, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", w.Code)
	}
}

func TestPhoneRevocationStopsEventStream(t *testing.T) {
	s, c := newTestStore(t)
	app, err := s.CreateApp("phone", "owner_app", "phone", "phone", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.IssueToken(app.ID, "access", phoneTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, limiter: ratelimit.New(ratelimit.Config{}, c.now), logger: log.New(io.Discard, "", 0)}
	stopped := make(chan struct{})
	server := httptest.NewServer(g.phoneAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(stopped)
	})))
	defer server.Close()
	req, err := http.NewRequest("GET", server.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: phoneCookieName, Value: raw})
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if err = s.RevokeApp(app.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("event stream stayed open after revocation")
	}
}
