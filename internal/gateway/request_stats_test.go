package gateway

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func statFor(t *testing.T, w protocol.RequestWindow, method, origin string) protocol.RequestStat {
	t.Helper()
	for _, s := range w.Top {
		if s.Method == method && s.Origin == origin {
			return s
		}
	}
	t.Fatalf("no row for %s/%s in %+v", method, origin, w.Top)
	return protocol.RequestStat{}
}

func TestRequestStatsCountPerMethodAndOrigin(t *testing.T) {
	t0 := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	r := newRequestStats(t0)
	r.record("query.agents", "tui", t0, 2*time.Millisecond, 100)
	r.record("query.agents", "tui", t0, 6*time.Millisecond, 300)
	r.record("query.agents", "mcp", t0, time.Millisecond, 50)
	r.record("msg.send", "worker", t0, 10*time.Millisecond, 20)

	got := r.snapshot(t0)
	if got.Since.Calls != 4 || got.Last10Min.Calls != 4 {
		t.Fatalf("calls = %d / %d, want 4", got.Since.Calls, got.Last10Min.Calls)
	}
	tui := statFor(t, got.Since, "query.agents", "tui")
	if tui.Calls != 2 || tui.TotalMs != 8 || tui.MaxMs != 6 || tui.Bytes != 400 {
		t.Fatalf("tui row = %+v", tui)
	}
	if mcp := statFor(t, got.Since, "query.agents", "mcp"); mcp.Calls != 1 || mcp.Bytes != 50 {
		t.Fatalf("mcp row = %+v", mcp)
	}
	byOrigin := map[string]protocol.RequestOriginTotal{}
	for _, o := range got.Since.ByOrigin {
		byOrigin[o.Origin] = o
	}
	if byOrigin["tui"].Calls != 2 || byOrigin["worker"].Calls != 1 || byOrigin["mcp"].Calls != 1 {
		t.Fatalf("by origin = %+v", got.Since.ByOrigin)
	}
	// Biggest total time first: msg.send 10 ms, query.agents/tui 8 ms.
	if got.Since.Top[0].Method != "msg.send" || got.Since.Top[1].Origin != "tui" {
		t.Fatalf("order = %+v", got.Since.Top)
	}
}

func TestRequestStatsUntaggedIsUnknown(t *testing.T) {
	t0 := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	r := newRequestStats(t0)
	r.record("query.agents", "", t0, time.Millisecond, 1)
	r.record("query.agents", "not-a-real-origin", t0, time.Millisecond, 1)
	got := r.snapshot(t0)
	if s := statFor(t, got.Since, "query.agents", "unknown"); s.Calls != 2 {
		t.Fatalf("unknown row = %+v", s)
	}
}

func TestRequestStatsWindowDropsOldBuckets(t *testing.T) {
	t0 := time.Date(2026, 11, 9, 10, 0, 30, 0, time.UTC)
	r := newRequestStats(t0)
	r.record("query.agents", "tui", t0, time.Millisecond, 10)
	r.record("query.agents", "tui", t0.Add(5*time.Minute), time.Millisecond, 10)

	at := t0.Add(5 * time.Minute)
	if got := r.snapshot(at); got.Last10Min.Calls != 2 {
		t.Fatalf("at 5 min: window calls = %d, want 2", got.Last10Min.Calls)
	}
	// 11 minutes after the first call it is out of the window, the second is in.
	at = t0.Add(11 * time.Minute)
	got := r.snapshot(at)
	if got.Last10Min.Calls != 1 || got.Last10Min.Bytes != 10 {
		t.Fatalf("at 11 min: window = %+v, want 1 call", got.Last10Min)
	}
	if got.Since.Calls != 2 {
		t.Fatalf("since start = %d, want 2", got.Since.Calls)
	}
	// A new call reuses the slot of the old bucket and must not keep its numbers.
	r.record("query.agents", "tui", at, 4*time.Millisecond, 7)
	got = r.snapshot(at)
	if s := statFor(t, got.Last10Min, "query.agents", "tui"); s.Calls != 2 || s.MaxMs != 4 || s.Bytes != 17 {
		t.Fatalf("after reuse: %+v", s)
	}
	// Far later, the window is empty.
	if got := r.snapshot(t0.Add(time.Hour)); got.Last10Min.Calls != 0 || len(got.Last10Min.Top) != 0 {
		t.Fatalf("an hour later: %+v", got.Last10Min)
	}
}

func TestRequestStatsTop30AndBoundedKeys(t *testing.T) {
	t0 := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	r := newRequestStats(t0)
	for i := 0; i < maxRequestKeys+500; i++ {
		r.record("m"+strconv.Itoa(i), "tui", t0, time.Duration(i+1)*time.Microsecond, 1)
	}
	got := r.snapshot(t0)
	if len(got.Since.Top) != 30 {
		t.Fatalf("top = %d rows, want 30", len(got.Since.Top))
	}
	if got.Since.Calls != int64(maxRequestKeys+500) {
		t.Fatalf("calls = %d: calls past the key limit must still be counted", got.Since.Calls)
	}
}

func TestRequestStatsConcurrent(t *testing.T) {
	t0 := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	r := newRequestStats(t0)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				r.record("query.agents", "tui", t0, time.Microsecond, 1)
			}
		}()
	}
	wg.Wait()
	if got := r.snapshot(t0); got.Since.Calls != 8000 || got.Last10Min.Calls != 8000 {
		t.Fatalf("calls = %d / %d, want 8000", got.Since.Calls, got.Last10Min.Calls)
	}
}

func BenchmarkRequestStatsRecord(b *testing.B) {
	t0 := time.Now()
	r := newRequestStats(t0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.record("query.agents", "tui", t0, time.Millisecond, 512)
	}
}

func BenchmarkRequestStatsRecordParallel(b *testing.B) {
	t0 := time.Now()
	r := newRequestStats(t0)
	methods := []string{"query.agents", "query.nodes", "graph.get", "msg.send", "query.projects"}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			r.record(methods[i%len(methods)], "tui", t0, time.Millisecond, 512)
			i++
		}
	})
}

// agent_machine_get shows the counters next to the usage reading, and a
// request frame that ends is counted under the origin it carries.
func TestMachineGetShowsRequestCounters(t *testing.T) {
	s, _ := newTestStore(t)
	id, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetNodeHello(id, "box", "1"); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, conns: map[string]*nodeConn{}, reqs: newRequestStats(time.Now())}
	sent := 123
	g.finishRequest(protocol.Header{Type: "query.agents", Via: "tui"}, id, time.Now().Add(-3*time.Millisecond), &sent)
	g.finishRequest(protocol.Header{Type: "query.agents"}, id, time.Now(), &sent)

	out, err := g.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: "box"})
	if err != nil {
		t.Fatal(err)
	}
	checkOutput(t, "agent_machine_get", out)
	if out.Requests == nil {
		t.Fatal("no requests block")
	}
	tui := statFor(t, out.Requests.Since, "query.agents", "tui")
	if tui.Calls != 1 || tui.Bytes != 123 || tui.TotalMs < 3 {
		t.Fatalf("tui row = %+v", tui)
	}
	if u := statFor(t, out.Requests.Last10Min, "query.agents", "unknown"); u.Calls != 1 {
		t.Fatalf("unknown row = %+v", u)
	}
	cat, err := g.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: "box", Include: []string{"playbook"}})
	if err != nil || cat.Requests != nil {
		t.Fatalf("category call: err=%v requests=%+v", err, cat.Requests)
	}
}
