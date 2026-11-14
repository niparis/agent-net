package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

// A machine that refuses the granted model does not lose the start: the route
// asks the gate for the next candidate in the same list and launches that one
// (decision 0162).
func TestLaunchFailureTriesTheNextCandidateInTheSameList(t *testing.T) {
	g, node, owner, _, _ := setupFloorGateway(t)
	g.opts.TestNoBriefChecks = true

	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err == nil {
			accepted <- ws
		}
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	nc := &nodeConn{id: node, ws: <-accepted, dead: make(chan struct{}), cancel: cancel}
	g.mu.Lock()
	g.conns[node] = nc
	g.mu.Unlock()

	var mu sync.Mutex
	var launched []protocol.WorkerLaunch
	go func() {
		for {
			_, raw, e := client.Read(ctx)
			if e != nil {
				return
			}
			var h protocol.Header
			if json.Unmarshal(raw, &h) != nil || h.Type != "worker.launch" {
				continue
			}
			var l protocol.WorkerLaunch
			if err := json.Unmarshal(raw, &l); err != nil {
				continue
			}
			mu.Lock()
			launched = append(launched, l)
			first := len(launched) == 1
			mu.Unlock()
			var res []byte
			if first {
				res, _ = protocol.EncodeResult(h.ID, nil, errors.New("this machine cannot run that model"))
			} else {
				res, _ = protocol.EncodeResult(h.ID, protocol.WorkerLaunched{SessionID: "s_next"}, nil)
			}
			var reply protocol.Header
			if json.Unmarshal(res, &reply) == nil {
				nc.answer(reply)
			}
		}
	}()

	ans, err := g.workerStart(ctx, nc, protocol.WorkerStartFrame{Owner: owner, Level: "hard",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Model: "auto"}})
	if err != nil {
		t.Fatalf("workerStart: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(launched) != 2 {
		t.Fatalf("launch attempts: %d, want 2", len(launched))
	}
	if launched[0].Model == launched[1].Model || ans.Model != launched[1].Model {
		t.Fatalf("launches %+v, answer %s: the start did not move to the next candidate", launched, ans.Model)
	}
	if !strings.Contains(ans.ModelNote, "launch failed on") {
		t.Fatalf("note does not say the first launch failed: %q", ans.ModelNote)
	}
}
