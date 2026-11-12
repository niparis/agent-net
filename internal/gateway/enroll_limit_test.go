package gateway

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	cur time.Time
}

func newFakeClock(t0 time.Time) *fakeClock {
	return &fakeClock{cur: t0}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cur = c.cur.Add(d)
}

func TestEnrollRateLimitPerIP(t *testing.T) {
	clock := newFakeClock(time.Now())
	var logBuf bytes.Buffer
	logger := log.New(&logBuf, "", 0)
	gw, err := New(Options{DataDir: seededDataDir(t), Now: clock.now, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	handler := gw.Handler()

	sendEnroll := func(remoteIP string, code string) int {
		body, _ := json.Marshal(map[string]string{
			"code":     code,
			"hostname": "test-box",
			"os":       "linux",
		})
		req := httptest.NewRequest("POST", "/v1/enroll", bytes.NewReader(body))
		req.RemoteAddr = remoteIP + ":1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	ip1 := "192.0.2.1"
	// First 10 requests from ip1 should reach enroll logic (and fail with 403 Forbidden for bad code)
	for i := 1; i <= 10; i++ {
		code := sendEnroll(ip1, "badcode"+strconv.Itoa(i))
		if code != http.StatusForbidden {
			t.Fatalf("request %d: expected 403 Forbidden, got %d", i, code)
		}
	}

	logBefore := logBuf.String()
	if strings.Contains(logBefore, "enroll.rate_limited") {
		t.Fatal("expected no rate limit log before hitting limit")
	}

	// 11th request from ip1 should be rate limited (429)
	if code := sendEnroll(ip1, "badcode11"); code != http.StatusTooManyRequests {
		t.Fatalf("11th request: expected 429 Too Many Requests, got %d", code)
	}

	logAfterFirst429 := logBuf.String()
	firstCount := strings.Count(logAfterFirst429, "enroll.rate_limited")
	if firstCount != 1 {
		t.Fatalf("expected exactly 1 rate limit log, got %d", firstCount)
	}

	// 12th request from ip1 should still be 429, but NOT log again in the same window
	if code := sendEnroll(ip1, "badcode12"); code != http.StatusTooManyRequests {
		t.Fatalf("12th request: expected 429 Too Many Requests, got %d", code)
	}
	secondCount := strings.Count(logBuf.String(), "enroll.rate_limited")
	if secondCount != 1 {
		t.Fatalf("expected rate limit log to appear once per window, got %d occurrences", secondCount)
	}

	// A different IP should still be allowed
	ip2 := "192.0.2.2"
	if code := sendEnroll(ip2, "badcode1"); code != http.StatusForbidden {
		t.Fatalf("different IP request: expected 403 Forbidden, got %d", code)
	}

	// Advance clock past 10 minutes window
	clock.advance(11 * time.Minute)

	// ip1 should now be allowed again
	if code := sendEnroll(ip1, "badcode13"); code != http.StatusForbidden {
		t.Fatalf("request after window: expected 403 Forbidden, got %d", code)
	}
}

func TestEnrollRateLimitGlobal(t *testing.T) {
	clock := newFakeClock(time.Now())
	gw, err := New(Options{DataDir: seededDataDir(t), Now: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	handler := gw.Handler()

	sendEnroll := func(remoteIP string) int {
		body, _ := json.Marshal(map[string]string{
			"code":     "badcode",
			"hostname": "test-box",
			"os":       "linux",
		})
		req := httptest.NewRequest("POST", "/v1/enroll", bytes.NewReader(body))
		req.RemoteAddr = remoteIP + ":1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	// Send requests across different IPs (e.g., 6 requests per IP across 10 IPs = 60 requests)
	reqCount := 0
	for i := 1; i <= 10; i++ {
		ip := "192.0.2." + strconv.Itoa(i)
		for j := 1; j <= 6; j++ {
			reqCount++
			code := sendEnroll(ip)
			if code != http.StatusForbidden {
				t.Fatalf("request %d (ip %s): expected 403, got %d", reqCount, ip, code)
			}
		}
	}

	// 61st request from a new IP should hit global limit (429)
	newIP := "192.0.2.99"
	if code := sendEnroll(newIP); code != http.StatusTooManyRequests {
		t.Fatalf("61st request: expected global limit 429, got %d", code)
	}
}
