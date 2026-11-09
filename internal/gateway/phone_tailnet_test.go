package gateway

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/ratelimit"
)

type fakePhonePeer struct {
	uid uint32
	err error
}

func (f fakePhonePeer) UID(remote, local string) (uint32, error) { return f.uid, f.err }

type fakePhoneOwner struct {
	login string
	err   error
}

func (f fakePhoneOwner) Login(context.Context) (string, error) { return f.login, f.err }

func TestPhoneTailnetIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, login       string
		uid               uint32
		peerErr, ownerErr error
		cookie            bool
		paired            bool
	}{
		{name: "owner", login: "owner@example.test", paired: true},
		{name: "local forgery", login: "owner@example.test", uid: 1000},
		{name: "other user", login: "other@example.test"},
		{name: "Funnel"},
		{name: "missing socket", login: "owner@example.test", peerErr: os.ErrNotExist},
		{name: "proc denied", login: "owner@example.test", peerErr: os.ErrPermission},
		{name: "LocalAPI unavailable", login: "owner@example.test", ownerErr: errors.New("offline")},
		{name: "invalid cookie", login: "owner@example.test", cookie: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c := newTestStore(t)
			g := &Gateway{store: s, limiter: ratelimit.New(ratelimit.Config{}, c.now), logger: log.New(io.Discard, "", 0), opts: Options{PublicURL: "https://gateway.example"}, conns: map[string]*nodeConn{}, phoneTailnet: &phoneTailnet{peer: fakePhonePeer{tc.uid, tc.peerErr}, owner: fakePhoneOwner{"owner@example.test", tc.ownerErr}}}
			h := g.AppsHandler()
			request := func(cookie *http.Cookie) *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", "https://gateway.example/", nil)
				r.RemoteAddr = "127.0.0.1:45678"
				r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 7422}))
				if tc.login != "" {
					r.Header.Set("Tailscale-User-Login", tc.login)
				}
				r.Header.Set("User-Agent", "Mozilla/5.0 (iPhone) Safari/604.1")
				if cookie != nil {
					r.AddCookie(cookie)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			var ck *http.Cookie
			if tc.cookie {
				ck = &http.Cookie{Name: phoneCookieName, Value: "invalid"}
			}
			w := request(ck)
			cookies := w.Result().Cookies()
			if !tc.paired {
				if w.Code != 200 || !phoneCodeRE.Match(w.Body.Bytes()) || len(cookies) != 0 {
					t.Fatalf("want pairing without cookie: status=%d cookies=%d body=%s", w.Code, len(cookies), w.Body.String())
				}
				return
			}
			if w.Code != 200 || phoneCodeRE.Match(w.Body.Bytes()) || !strings.Contains(w.Body.String(), "<!doctype html>") || len(cookies) != 1 {
				t.Fatalf("want console and cookie: status=%d cookies=%d", w.Code, len(cookies))
			}
			ck = cookies[0]
			if ck.Name != phoneCookieName || !ck.Secure || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.MaxAge != int(phoneTokenTTL/time.Second) {
				t.Fatalf("cookie attributes: %+v", ck)
			}
			apps, err := s.ListApps()
			if err != nil || len(apps) != 1 || apps[0].Kind != "owner_app" || apps[0].Name != "Tailscale sign-in: owner@example.test, iPhone Safari" {
				t.Fatalf("apps: %+v %v", apps, err)
			}
			var count int
			if err = s.db.QueryRow(`SELECT count(*) FROM inbox_items WHERE text=? AND kind='check' AND status='open'`, "Paired "+apps[0].Name+" by Tailscale sign-in").Scan(&count); err != nil || count != 1 {
				t.Fatalf("notice count=%d: %v", count, err)
			}
			if w = request(ck); w.Code != 200 || len(w.Result().Cookies()) != 0 {
				t.Fatalf("paired repeat: %d", w.Code)
			}
			// Renewal uses the existing token and does not create another device or notice.
			c.t = c.t.Add(61 * 24 * time.Hour)
			if w = request(ck); len(w.Result().Cookies()) != 1 {
				t.Fatal("no sliding renewal")
			}
			if err = s.db.QueryRow(`SELECT count(*) FROM inbox_items WHERE source_ref LIKE 'tailscale-pair:%'`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("duplicate notice: %d %v", count, err)
			}
			if err = s.RevokeApp(apps[0].ID); err != nil {
				t.Fatal(err)
			}
			if w = request(ck); !phoneCodeRE.Match(w.Body.Bytes()) || len(w.Result().Cookies()) != 0 {
				t.Fatal("revoked device was automatically paired again")
			}
		})
	}
}

func TestPhonePeerUID(t *testing.T) {
	if runtime.GOOS != "linux" {
		if _, err := (procPhonePeer{dir: t.TempDir()}).UID("127.0.0.1:45678", "127.0.0.1:7422"); err == nil {
			t.Fatal("auto-pairing enabled outside Linux")
		}
		return
	}

	for _, tc := range []struct {
		name, remote, local, row string
		uid                      uint32
		bad                      bool
	}{
		{"root IPv4", "127.0.0.1:45678", "127.0.0.1:7422", "0100007F:B26E 0100007F:1CFE 01 0:0 00:0 0 0 0 123", 0, false},
		{"owner IPv4", "127.0.0.1:45678", "127.0.0.1:7422", "0100007F:B26E 0100007F:1CFE 01 0:0 00:0 0 1000 0 123", 1000, false},
		{"root IPv6", "[::1]:45678", "[::1]:7422", "00000000000000000000000001000000:B26E 00000000000000000000000001000000:1CFE 01 0:0 00:0 0 0 0 123", 0, false},
		{"wrong destination", "127.0.0.1:45678", "127.0.0.1:7422", "0100007F:B26E 0100007F:1CFF 01 0:0 00:0 0 0 0 123", 0, true},
		{"wrong address", "127.0.0.1:45678", "127.0.0.1:7422", "0200007F:B26E 0100007F:1CFE 01 0:0 00:0 0 0 0 123", 0, true},
		{"not established", "127.0.0.1:45678", "127.0.0.1:7422", "0100007F:B26E 0100007F:1CFE 06 0:0 00:0 0 0 0 0", 0, true},
		{"bad uid", "127.0.0.1:45678", "127.0.0.1:7422", "0100007F:B26E 0100007F:1CFE 01 0:0 00:0 0 nope 0 123", 0, true},
		{"nonloopback", "192.0.2.1:45678", "127.0.0.1:7422", "010200C0:B26E 0100007F:1CFE 01 0:0 00:0 0 0 0 123", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"tcp", "tcp6"} {
				data := "header\n"
				if (name == "tcp6") == strings.Contains(tc.remote, "[") {
					data += " 0: " + tc.row + "\n"
				}
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			uid, err := (procPhonePeer{dir: dir}).UID(tc.remote, tc.local)
			if (err != nil) != tc.bad || (!tc.bad && uid != tc.uid) {
				t.Fatalf("uid=%d err=%v", uid, err)
			}
		})
	}
	if _, err := (procPhonePeer{dir: t.TempDir()}).UID("127.0.0.1:45678", "127.0.0.1:7422"); err == nil {
		t.Fatal("missing proc files accepted")
	}
}

type phoneStatusRunner struct {
	calls  int
	status string
	err    error
}

func (f *phoneStatusRunner) Run(ctx context.Context, args ...string) (string, string, error) {
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 500*time.Millisecond {
		return "", "", errors.New("missing bounded deadline")
	}
	if strings.Join(args, " ") != "status --json" {
		return "", "", errors.New("unexpected command")
	}
	f.calls++
	return f.status, "", f.err
}
func TestPhoneOwnerLoginCache(t *testing.T) {
	r := &phoneStatusRunner{status: `{"Self":{"UserID":42},"User":{"42":{"LoginName":"owner@example.test"}}}`}
	now := time.Now()
	o := &tailscalePhoneOwner{runner: r, now: func() time.Time { return now }}
	for i := 0; i < 2; i++ {
		login, err := o.Login(context.Background())
		if err != nil || login != "owner@example.test" {
			t.Fatalf("%q %v", login, err)
		}
	}
	if r.calls != 1 {
		t.Fatalf("not cached: %d", r.calls)
	}
	now = now.Add(time.Minute)
	r.status = `{"Self":{"UserID":43},"User":{"42":{"LoginName":"owner@example.test"}}}`
	if login, err := o.Login(context.Background()); err == nil || login != "" {
		t.Fatalf("stale owner trusted: %q %v", login, err)
	}
	if r.calls != 2 {
		t.Fatalf("not refreshed: %d", r.calls)
	}
	// A failed refresh is cached briefly, then can recover to a different owner.
	r.status = `{"Self":{"UserID":43},"User":{"43":{"LoginName":"new@example.test"}}}`
	if _, err := o.Login(context.Background()); err == nil || r.calls != 2 {
		t.Fatal("failure not cached")
	}
	now = now.Add(6 * time.Second)
	if login, err := o.Login(context.Background()); err != nil || login != "new@example.test" || r.calls != 3 {
		t.Fatalf("recovery: %q %v calls=%d", login, err, r.calls)
	}
}
