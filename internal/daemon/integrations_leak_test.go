package daemon

import (
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// The integrations client must reuse the daemon's one pinned client. A new
// client per call left a connection to the gateway open per call, and on the
// owner's Mac this used up every local port (about 16,000 connections).
func TestIntegrationsReuseOneConnection(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"set":false}`))
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.StartTLS()
	defer srv.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	d := &Daemon{cfg: Config{GatewayURL: srv.URL, Token: "t", CAPEM: ca}}
	c, err := PinnedClient(ca)
	if err != nil {
		t.Fatal(err)
	}
	d.client = c
	for i := 0; i < 20; i++ {
		ic, ok := d.integrationsClient()
		if !ok {
			t.Fatal("no client")
		}
		if ic.HTTP != d.client {
			t.Fatal("integrations built its own HTTP client")
		}
		if _, err := ic.Get(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if n := conns.Load(); n > 2 {
		t.Fatalf("20 integrations calls opened %d connections, want at most 2", n)
	}
}
