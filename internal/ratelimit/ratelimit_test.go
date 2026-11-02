package ratelimit

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *testClock { return &testClock{t: time.Unix(1_700_000_000, 0)} }
func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestBucketRefillAndIndependentClasses(t *testing.T) {
	clock := newClock()
	l := New(Config{MCP: Rate{2, time.Minute}, OAuth: Rate{1, time.Minute}}, clock.now)
	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("a", "mcp"); !ok {
			t.Fatal("initial MCP token refused")
		}
	}
	if ok, retry := l.Allow("a", "mcp"); ok || retry != 30*time.Second {
		t.Fatalf("empty bucket: %v, %v", ok, retry)
	}
	if ok, _ := l.Allow("a", "oauth"); !ok {
		t.Fatal("OAuth bucket is not independent")
	}
	if ok, _ := l.Allow("b", "mcp"); !ok {
		t.Fatal("address bucket is not independent")
	}
	clock.advance(15 * time.Second)
	if ok, retry := l.Allow("a", "mcp"); ok || retry != 15*time.Second {
		t.Fatalf("half refill: %v, %v", ok, retry)
	}
	clock.advance(15 * time.Second)
	if ok, _ := l.Allow("a", "mcp"); !ok {
		t.Fatal("refilled token refused")
	}
	if ok, _ := l.Allow("a", "mcp"); ok {
		t.Fatal("refill gave too many tokens")
	}
	clock.advance(10 * time.Minute)
	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("a", "mcp"); !ok {
			t.Fatal("full bucket refused")
		}
	}
	if ok, _ := l.Allow("a", "mcp"); ok {
		t.Fatal("bucket exceeded capacity")
	}
}

func TestDefaultsAndUnknownKinds(t *testing.T) {
	clock := newClock()
	l := New(Config{}, clock.now)
	for _, tc := range []struct {
		class string
		limit int
	}{{"mcp", 120}, {"oauth", 30}, {"page", 60}} {
		for i := 0; i < tc.limit; i++ {
			if ok, _ := l.Allow(tc.class, tc.class); !ok {
				t.Fatalf("%s refused token %d", tc.class, i)
			}
		}
		if ok, _ := l.Allow(tc.class, tc.class); ok {
			t.Fatalf("%s exceeded default", tc.class)
		}
	}
	for _, tc := range []struct {
		kind  string
		limit int
	}{{"call", 60}, {"message", 30}} {
		for i := 0; i < tc.limit; i++ {
			if ok, _ := l.AllowApp(tc.kind, tc.kind); !ok {
				t.Fatalf("%s refused token %d", tc.kind, i)
			}
		}
		if ok, _ := l.AllowApp(tc.kind, tc.kind); ok {
			t.Fatalf("%s exceeded default", tc.kind)
		}
	}
	if ok, _ := l.Allow("unknown", "bad"); ok {
		t.Fatal("unknown route class allowed")
	}
	if ok, _ := l.AllowApp("unknown", "bad"); ok {
		t.Fatal("unknown app kind allowed")
	}
	if _, ok := l.addresses["unknown"]; ok {
		t.Fatal("unknown class created address state")
	}
	if _, ok := l.apps["unknown"]; ok {
		t.Fatal("unknown kind created app state")
	}
}

func TestBanWindowAndExpiry(t *testing.T) {
	clock := newClock()
	l := New(Config{FailureLimit: 3, FailureWindow: 10 * time.Minute, BanDuration: time.Hour}, clock.now)
	if l.Banned("missing") {
		t.Fatal("unknown address banned")
	}
	if len(l.addresses) != 0 {
		t.Fatal("ban lookup allocated address state")
	}
	if l.Fail("a") {
		t.Fatal("first failure banned")
	}
	clock.advance(6 * time.Minute)
	if l.Fail("a") {
		t.Fatal("second failure banned")
	}
	clock.advance(5 * time.Minute)
	if l.Fail("a") {
		t.Fatal("failure outside sliding window counted")
	}
	if !l.Fail("a") || !l.Banned("a") {
		t.Fatal("third failure in window did not ban")
	}
	if ok, retry := l.Allow("a", "mcp"); ok || retry != time.Hour {
		t.Fatalf("ban did not override full bucket: %v, %v", ok, retry)
	}
	clock.advance(time.Hour - time.Second)
	if !l.Banned("a") {
		t.Fatal("ban ended early")
	}
	if ok, retry := l.Allow("a", "page"); ok || retry != time.Second {
		t.Fatalf("ban retry: %v, %v", ok, retry)
	}
	clock.advance(time.Second)
	if l.Banned("a") {
		t.Fatal("ban did not expire")
	}
	if ok, _ := l.Allow("a", "mcp"); !ok {
		t.Fatal("expired ban still refuses requests")
	}
	if l.Fail("a") {
		t.Fatal("old failures persisted after ban")
	}
}

func TestDefaultBan(t *testing.T) {
	clock := newClock()
	l := New(Config{}, clock.now)
	for i := 0; i < 9; i++ {
		if l.Fail("a") {
			t.Fatalf("banned after %d failures", i+1)
		}
	}
	if !l.Fail("a") {
		t.Fatal("tenth failure did not ban")
	}
	if ok, retry := l.Allow("a", "page"); ok || retry != time.Hour {
		t.Fatalf("default ban: %v, %v", ok, retry)
	}
}

func TestAppLimits(t *testing.T) {
	clock := newClock()
	l := New(Config{AppCalls: Rate{2, time.Minute}, AppMessages: Rate{1, time.Hour}}, clock.now)
	for i := 0; i < 2; i++ {
		if ok, _ := l.AllowApp("app", "call"); !ok {
			t.Fatal("initial call refused")
		}
	}
	if ok, retry := l.AllowApp("app", "call"); ok || retry != time.Minute {
		t.Fatalf("call limit: %v, %v", ok, retry)
	}
	if ok, _ := l.AllowApp("app", "message"); !ok {
		t.Fatal("message bucket is not independent")
	}
	if ok, retry := l.AllowApp("app", "message"); ok || retry != time.Hour {
		t.Fatalf("message limit: %v, %v", ok, retry)
	}
	if ok, _ := l.AllowApp("other", "call"); !ok {
		t.Fatal("app buckets are not independent")
	}
	clock.advance(30 * time.Second)
	if ok, retry := l.AllowApp("app", "call"); ok || retry != 30*time.Second {
		t.Fatalf("call limit expired early: %v, %v", ok, retry)
	}
	if ok, _ := l.AllowApp("app", "message"); ok {
		t.Fatal("message refilled early")
	}
	clock.advance(30 * time.Second)
	if ok, _ := l.AllowApp("app", "call"); !ok {
		t.Fatal("app call did not expire")
	}
	clock.advance(time.Hour - time.Minute)
	if ok, _ := l.AllowApp("app", "message"); !ok {
		t.Fatal("message did not refill")
	}
}

func TestAppRollingWindow(t *testing.T) {
	clock := newClock()
	l := New(Config{AppCalls: Rate{2, time.Minute}}, clock.now)
	if ok, _ := l.AllowApp("app", "call"); !ok {
		t.Fatal("first call refused")
	}
	clock.advance(15 * time.Second)
	if ok, _ := l.AllowApp("app", "call"); !ok {
		t.Fatal("second call refused")
	}
	clock.advance(45 * time.Second)
	if ok, _ := l.AllowApp("app", "call"); !ok {
		t.Fatal("first call did not expire")
	}
	if ok, retry := l.AllowApp("app", "call"); ok || retry != 15*time.Second {
		t.Fatalf("rolling window allowed too many calls: %v, %v", ok, retry)
	}
	clock.advance(15 * time.Second)
	if ok, _ := l.AllowApp("app", "call"); !ok {
		t.Fatal("second call did not expire")
	}
}

func TestAddressEvictionAndIdleExpiry(t *testing.T) {
	clock := newClock()
	l := New(Config{MaxAddresses: 3, IdleTTL: time.Minute, FailureWindow: time.Second, BanDuration: time.Second}, clock.now)
	for _, addr := range []string{"a", "b", "c"} {
		l.Allow(addr, "mcp")
	}
	l.Allow("a", "page")
	l.Allow("d", "page")
	if len(l.addresses) != 3 {
		t.Fatalf("tracked %d addresses, want 3", len(l.addresses))
	}
	if _, ok := l.addresses["b"]; ok {
		t.Fatal("did not evict least recently seen address")
	}
	if _, ok := l.addresses["a"]; !ok {
		t.Fatal("evicted recently used address")
	}
	clock.advance(2 * time.Minute)
	l.Allow("new", "mcp")
	if len(l.addresses) != 1 {
		t.Fatalf("idle entries retained: %d", len(l.addresses))
	}
}

func TestAddressCapUnderScan(t *testing.T) {
	clock := newClock()
	l := New(Config{}, clock.now)
	for i := 0; i < 100_000; i++ {
		l.Allow(fmt.Sprintf("address-%d", i), "page")
		if len(l.addresses) > 10_000 {
			t.Fatalf("cap exceeded at %d: %d", i, len(l.addresses))
		}
	}
	if len(l.addresses) != 10_000 {
		t.Fatalf("tracked %d addresses, want 10000", len(l.addresses))
	}
}

func TestIdleExpiryDoesNotShortenBanOrAppLimit(t *testing.T) {
	clock := newClock()
	l := New(Config{IdleTTL: time.Second, FailureLimit: 1, BanDuration: time.Hour, AppMessages: Rate{1, time.Hour}}, clock.now)
	l.Fail("banned")
	l.AllowApp("app", "message")
	clock.advance(2 * time.Second)
	l.Allow("other", "mcp")
	l.AllowApp("other", "call")
	if !l.Banned("banned") {
		t.Fatal("idle cleanup shortened the ban")
	}
	if ok, _ := l.AllowApp("app", "message"); ok {
		t.Fatal("idle cleanup reset app limit")
	}
}

func TestIdleExpiryPreservesFailureWindow(t *testing.T) {
	clock := newClock()
	l := New(Config{IdleTTL: time.Second, FailureLimit: 2, FailureWindow: 10 * time.Minute, BanDuration: time.Second}, clock.now)
	if l.Fail("a") {
		t.Fatal("first failure banned")
	}
	clock.advance(2 * time.Minute)
	l.Allow("other", "mcp")
	if !l.Fail("a") {
		t.Fatal("idle cleanup forgot a failure still in the window")
	}
}

func TestAppEvictionAndIdleExpiry(t *testing.T) {
	clock := newClock()
	l := New(Config{MaxApps: 2, IdleTTL: time.Minute, AppCalls: Rate{1, time.Minute}, AppMessages: Rate{1, time.Minute}}, clock.now)
	l.AllowApp("a", "call")
	l.AllowApp("b", "call")
	l.AllowApp("a", "message")
	l.AllowApp("c", "call")
	if len(l.apps) != 2 {
		t.Fatalf("tracked %d apps, want 2", len(l.apps))
	}
	if _, ok := l.apps["b"]; ok {
		t.Fatal("did not evict least recently seen app")
	}
	clock.advance(2 * time.Minute)
	l.AllowApp("new", "call")
	if len(l.apps) != 1 {
		t.Fatalf("idle apps retained: %d", len(l.apps))
	}
}

func TestClientAddr(t *testing.T) {
	for _, tc := range []struct {
		remote    string
		forwarded []string
		want      string
	}{
		{"127.0.0.1:1234", []string{"1.2.3.4, 9.9.9.9"}, "9.9.9.9"},
		{"127.0.0.1:1234", []string{"1.2.3.4", "198.51.100.1, 9.9.9.9"}, "9.9.9.9"},
		{"[::1]:1234", []string{"192.0.2.4,  2001:db8::3 "}, "2001:db8::3"},
		{"127.0.0.1:1234", []string{"1.2.3.4, garbage"}, "127.0.0.1"},
		{"127.0.0.1:1234", []string{"1.2.3.4, 9.9.9.9:1234"}, "127.0.0.1"},
		{"127.0.0.1:1234", []string{"1.2.3.4", "garbage"}, "127.0.0.1"},
		{"127.0.0.1:1234", []string{"1.2.3.4", ""}, "127.0.0.1"},
		{"127.0.0.1:1234", []string{"1.2.3.4, "}, "127.0.0.1"},
		{"127.0.0.1:1234", []string{" , 192.0.2.4"}, "192.0.2.4"},
		{"198.51.100.7:1234", []string{"1.2.3.4, 9.9.9.9"}, "198.51.100.7"},
		{"[2001:db8::7]:1234", []string{"1.2.3.4", "9.9.9.9"}, "2001:db8::7"},
		{"127.0.0.1:1234", nil, "127.0.0.1"},
		{"192.0.2.1", []string{"198.51.100.1"}, "192.0.2.1"},
	} {
		r := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
		r.RemoteAddr = tc.remote
		for _, value := range tc.forwarded {
			r.Header.Add("X-Forwarded-For", value)
		}
		if got := ClientAddr(r); got != tc.want {
			t.Errorf("ClientAddr(%q, %v) = %q, want %q", tc.remote, tc.forwarded, got, tc.want)
		}
	}
}

func TestMiddlewareBansForwardedAddress(t *testing.T) {
	clock := newClock()
	l := New(Config{FailureLimit: 1}, clock.now)
	l.Fail("9.9.9.9")
	called := false
	h := Middleware(l, "mcp", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	for _, fake := range []string{"1.2.3.4", "192.0.2.1"} {
		r := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Add("X-Forwarded-For", fake)
		r.Header.Add("X-Forwarded-For", "9.9.9.9")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "3600" {
			t.Errorf("spoofed address %q: %d, Retry-After %q", fake, w.Code, w.Header().Get("Retry-After"))
		}
	}
	if called {
		t.Fatal("banned client reached handler through spoofed addresses")
	}
	if len(l.addresses) != 1 {
		t.Fatalf("spoofed addresses created state: %d", len(l.addresses))
	}
}

func TestMiddleware(t *testing.T) {
	clock := newClock()
	l := New(Config{MCP: Rate{1, time.Minute}, FailureLimit: 1, BanDuration: 90 * time.Second}, clock.now)
	called := 0
	h := Middleware(l, "mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called++; w.WriteHeader(http.StatusNoContent) }))
	request := func(addr string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
		r.RemoteAddr = addr + ":1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("192.0.2.1"); w.Code != http.StatusNoContent {
		t.Fatalf("first request: %d", w.Code)
	}
	if w := request("192.0.2.1"); w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "60" {
		t.Fatalf("bucket response: %d, %q", w.Code, w.Header().Get("Retry-After"))
	}
	l.Fail("192.0.2.2")
	clock.advance(500 * time.Millisecond)
	if w := request("192.0.2.2"); w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "90" {
		t.Fatalf("ban response: %d, %q", w.Code, w.Header().Get("Retry-After"))
	}
	if called != 1 {
		t.Fatalf("handler called %d times, want 1", called)
	}
	if len(l.addresses) != 2 {
		t.Fatalf("unexpected address state: %d", len(l.addresses))
	}
}

func TestConcurrentAccess(t *testing.T) {
	clock := newClock()
	l := New(Config{MCP: Rate{100, time.Minute}, AppCalls: Rate{100, time.Minute}, FailureLimit: 100000}, clock.now)
	var allowed, appAllowed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if ok, _ := l.Allow("a", "mcp"); ok {
					allowed.Add(1)
				}
				if ok, _ := l.AllowApp("app", "call"); ok {
					appAllowed.Add(1)
				}
				l.Fail("b")
				l.Banned("b")
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != 100 {
		t.Fatalf("allowed %d address requests, want 100", got)
	}
	if got := appAllowed.Load(); got != 100 {
		t.Fatalf("allowed %d app requests, want 100", got)
	}
}

func TestBanSurvivesAddressEviction(t *testing.T) {
	l := New(Config{MaxAddresses: 1, FailureLimit: 1}, nil)
	l.Fail("bad")
	l.Allow("other", "mcp")
	if !l.Banned("bad") {
		t.Fatal("eviction erased active ban")
	}
	if ok, _ := l.Allow("bad", "mcp"); ok {
		t.Fatal("evicted address allowed")
	}
}
