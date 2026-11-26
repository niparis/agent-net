package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOpenCodeTimelineDoesNotBlockPresence(t *testing.T) {
	f := startCTOCompactionFixture(t)
	d := f.n.d
	d.cto.mu.Lock()
	originalBase, originalIdentity := d.cto.base, d.cto.identity
	d.cto.mu.Unlock()
	target, err := url.Parse(originalBase)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	entered := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/message") && r.Method == http.MethodGet {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer server.Close()
	d.cto.mu.Lock()
	d.cto.base, d.cto.identity = server.URL, "test-proxy"
	d.cto.mu.Unlock()
	defer func() {
		d.cto.mu.Lock()
		d.cto.base, d.cto.identity = originalBase, originalIdentity
		d.cto.mu.Unlock()
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = d.openCodeCTOEvents(ctx, 0) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timeline did not reach message read")
	}
	presence := make(chan struct{})
	go func() { d.ctoRunning(); close(presence) }()
	select {
	case <-presence:
	case <-time.After(1500 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("presence waited for the timeline HTTP read")
	}
	cancel()
	<-done
}
