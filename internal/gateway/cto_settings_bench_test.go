package gateway

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// busyWriter starts four writers that each keep the writer connection for 2 ms
// per transaction, as a handler that stores a session or a message does. They
// stop with the benchmark.
func busyWriter(b *testing.B, s *Store) {
	b.Helper()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tx, err := s.db.Begin()
				if err != nil {
					return
				}
				_, _ = tx.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES ('bench', 'x', 1) ON CONFLICT (key) DO UPDATE SET value = excluded.value`)
				time.Sleep(2 * time.Millisecond)
				_ = tx.Commit()
			}
		}()
	}
	b.Cleanup(func() { close(stop); wg.Wait() })
	time.Sleep(20 * time.Millisecond) // let the writers queue up
}

// BenchmarkCTOSettingsGetWhileWritesRun is the HTTP read behind the TUI's
// cto_settings (and protected_providers_get, worker_floors_get and the other
// settings reads of a refresh), with the gateway busy writing: four writers
// each keep the writer connection for 2 ms per transaction, as a handler that
// stores a session or a message does. A read that only needs the read pool
// must not wait for them.
func BenchmarkCTOSettingsGetWhileWritesRun(b *testing.B) {
	s, _ := newTestStore(b)
	if s.db.r.Load() == nil {
		b.Skip("the read pool is off")
	}
	_, token, err := s.CreateNode("box", "linux")
	if err != nil {
		b.Fatal(err)
	}
	g := &Gateway{store: s}
	busyWriter(b, s)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/cto-settings", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		g.handleCTOSettingsGet(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
	}
}

// BenchmarkNodesInfoWhileWritesRun is query.nodes, which every TUI refresh and
// every proposals list asks, with the same busy writers.
func BenchmarkNodesInfoWhileWritesRun(b *testing.B) {
	s, _ := newTestStore(b)
	if s.db.r.Load() == nil {
		b.Skip("the read pool is off")
	}
	g := &Gateway{store: s, conns: map[string]*nodeConn{}, reqs: newRequestStats(time.Now())}
	id, _, err := s.CreateNode("box", "linux")
	if err != nil {
		b.Fatal(err)
	}
	if err = s.SetNodeHello(id, "box", "1"); err != nil {
		b.Fatal(err)
	}
	if _, err = g.NodesInfo(); err != nil {
		b.Fatal(err)
	}
	busyWriter(b, s)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.NodesInfo(); err != nil {
			b.Fatal(err)
		}
	}
}
