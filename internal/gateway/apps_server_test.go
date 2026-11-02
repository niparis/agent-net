package gateway

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// serveApps runs newAppsServer on a loopback port and returns its address.
func serveApps(t *testing.T, h http.Handler, readTimeout time.Duration) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := newAppsServer(h, readTimeout)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

func TestAppsServerReadDeadline(t *testing.T) {
	addr := serveApps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "body timed out", http.StatusRequestTimeout)
		}
	}), 100*time.Millisecond)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// Headers promise a body that never comes.
	if _, err = io.WriteString(conn, "POST /mcp HTTP/1.1\r\nHost: example.test\r\nContent-Length: 5\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("stalled body did not close: %v", err)
	}
	if !strings.Contains(string(response), "408") {
		t.Fatalf("response %s", response)
	}
}

func TestAppsServerConcurrentLimit(t *testing.T) {
	entered := make(chan struct{}, appsMaxInFlight)
	release := make(chan struct{})
	addr := serveApps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
	}), appsReadTimeout)
	done := make(chan struct{}, appsMaxInFlight)
	for i := 0; i < appsMaxInFlight; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			if res, err := http.Get("http://" + addr); err == nil {
				res.Body.Close()
			}
		}()
	}
	defer func() {
		close(release)
		for i := 0; i < appsMaxInFlight; i++ {
			<-done
		}
	}()
	deadline := time.After(2 * time.Second)
	for i := 0; i < appsMaxInFlight; i++ {
		select {
		case <-entered:
		case <-deadline:
			t.Fatal("requests did not reach the handler")
		}
	}
	client := &http.Client{Timeout: time.Second}
	res, err := client.Get("http://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("overflow status %d", res.StatusCode)
	}
}
