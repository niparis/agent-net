package gateway

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func clientErrorFor(t *testing.T, rows []protocol.ClientErrorStat, method, origin string) protocol.ClientErrorStat {
	t.Helper()
	for _, r := range rows {
		if r.Method == method && r.Origin == origin {
			return r
		}
	}
	t.Fatalf("no client error row for %s/%s in %+v", method, origin, rows)
	return protocol.ClientErrorStat{}
}

// A request that failed on the client side is counted per method and origin,
// for the whole run and for the last ten minutes.
func TestClientErrorsCountPerMethodAndOrigin(t *testing.T) {
	t0 := time.Date(2026, 11, 9, 10, 0, 30, 0, time.UTC)
	c := newClientErrorStats()
	c.record("tui", protocol.ClientError{Method: "proposal_list", Ms: 5000, TimeoutMs: 5000, Timeout: true, Screen: "desk"}, t0)
	c.record("tui", protocol.ClientError{Method: "proposal_list", Ms: 20, Err: "boom"}, t0)
	c.record("phone", protocol.ClientError{Method: "proposal_list", Ms: 7, Err: "boom"}, t0)

	got := c.snapshot(t0)
	tui := clientErrorFor(t, got.Since, "proposal_list", "tui")
	if tui.Errors != 2 || tui.Timeouts != 1 || tui.MaxMs != 5000 || tui.TotalMs != 5020 {
		t.Fatalf("tui row = %+v", tui)
	}
	if phone := clientErrorFor(t, got.Since, "proposal_list", "phone"); phone.Errors != 1 || phone.Timeouts != 0 {
		t.Fatalf("phone row = %+v", phone)
	}
	if last := clientErrorFor(t, got.Last10Min, "proposal_list", "tui"); last.Errors != 2 {
		t.Fatalf("last 10 min = %+v", last)
	}
	// Eleven minutes later the window is empty but the total stays.
	later := c.snapshot(t0.Add(11 * time.Minute))
	if len(later.Last10Min) != 0 || clientErrorFor(t, later.Since, "proposal_list", "tui").Errors != 2 {
		t.Fatalf("eleven minutes later: %+v", later)
	}
	// The worst offender is listed first.
	if got.Since[0].Origin != "tui" {
		t.Fatalf("order = %+v", got.Since)
	}
}

// A node can send any method text, so the number of rows is bounded and the
// text is cut.
func TestClientErrorsAreBounded(t *testing.T) {
	t0 := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	c := newClientErrorStats()
	for i := 0; i < maxRequestKeys+50; i++ {
		c.record("tui", protocol.ClientError{Method: "m" + strconv.Itoa(i)}, t0)
	}
	c.record("tui", protocol.ClientError{Method: strings.Repeat("x", 500)}, t0)
	got := c.snapshot(t0)
	var total int64
	for _, r := range got.Since {
		total += r.Errors
		if len(r.Method) > protocol.ClientErrorMaxText {
			t.Fatalf("method not cut: %d bytes", len(r.Method))
		}
	}
	if total != int64(maxRequestKeys+51) {
		t.Fatalf("errors counted = %d, want %d", total, maxRequestKeys+51)
	}
	if len(got.Since) > maxRequestKeys+1 {
		t.Fatalf("rows = %d: the key count must stay bounded", len(got.Since))
	}
}

// The client.error frame bumps the counter under the origin the frame carries,
// and agent_machine_get shows it next to the request counters.
func TestClientErrorFrameShowsInMachineGet(t *testing.T) {
	s, _ := newTestStore(t)
	id, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetNodeHello(id, "box", "1"); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, conns: map[string]*nodeConn{}, reqs: newRequestStats(time.Now()), clientErrs: newClientErrorStats()}
	raw, _ := json.Marshal(map[string]any{"type": protocol.FrameClientError, "id": "1", "via": "tui",
		"method": "cto_settings", "ms": 5001, "timeout_ms": 5000, "timeout": true, "screen": "cto"})
	if _, err := g.handle(context.Background(), &nodeConn{id: id}, protocol.FrameClientError, raw); err != nil {
		t.Fatal(err)
	}
	if !parallelFrames[protocol.FrameClientError] {
		t.Fatal("client.error must not wait in the write lane")
	}
	out, err := g.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: "box"})
	if err != nil {
		t.Fatal(err)
	}
	checkOutput(t, "agent_machine_get", out)
	if out.Requests == nil || out.Requests.ClientErrors == nil {
		t.Fatalf("no client error block: %+v", out.Requests)
	}
	row := clientErrorFor(t, out.Requests.ClientErrors.Since, "cto_settings", "tui")
	if row.Errors != 1 || row.Timeouts != 1 || row.MaxMs != 5001 {
		t.Fatalf("row = %+v", row)
	}
}
