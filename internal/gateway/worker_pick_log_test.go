package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerPickLogLineWritten(t *testing.T) {
	g, node, owner, _, c := setupFloorGateway(t)
	var logs bytes.Buffer
	g.logger = log.New(&logs, "", 0)

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
	ws := <-accepted
	nc := &nodeConn{id: node, ws: ws, dead: make(chan struct{}), cancel: cancel}
	g.mu.Lock()
	g.conns[node] = nc
	g.mu.Unlock()

	go func() {
		for {
			_, raw, e := client.Read(ctx)
			if e != nil {
				return
			}
			var h protocol.Header
			_ = json.Unmarshal(raw, &h)
			if h.Type == "worker.launch" {
				b, _ := protocol.EncodeResult(h.ID, protocol.WorkerLaunched{SessionID: "s_w1"}, nil)
				var reply protocol.Header
				_ = json.Unmarshal(b, &reply)
				nc.answer(reply)
			}
		}
	}()

	quotas := &protocol.Quotas{
		GeneratedAt: c.now().Unix(),
		Providers: []protocol.QuotaProvider{
			{
				Provider: "claude",
				Windows:  []protocol.QuotaWindow{{ID: "5h", PercentUsed: 20}},
			},
		},
	}
	g.usage.set(node, c.now(), protocol.UsageReport{Quotas: quotas})

	ans, err := g.workerStart(ctx, nc, protocol.WorkerStartFrame{
		Owner: owner,
		Level: "hard",
		WorkerStartRequest: protocol.WorkerStartRequest{
			Project: "alpha",
			Task:    "implement feature x",
			Harness: "claude",
			Model:   "auto",
		},
	})
	if err != nil {
		t.Fatalf("workerStart failed: %v", err)
	}
	if ans.NodeID != node {
		t.Fatalf("wrong node: %s", ans.NodeID)
	}

	output := logs.String()
	if !strings.Contains(output, "event=worker.pick") {
		t.Fatalf("logs missing event=worker.pick: %s", output)
	}
	if !strings.Contains(output, "session=s_w1") {
		t.Fatalf("logs missing session=s_w1: %s", output)
	}
	if !strings.Contains(output, "level=hard") {
		t.Fatalf("logs missing level=hard: %s", output)
	}
	if !strings.Contains(output, "chosen=\"claude claude-opus-5-5:high\"") && !strings.Contains(output, "chosen=claude") {
		t.Fatalf("logs missing chosen: %s", output)
	}
	if !strings.Contains(output, "reason=") {
		t.Fatalf("logs missing reason=: %s", output)
	}
	if !strings.Contains(output, "event=worker.pick_detail") {
		t.Fatalf("logs missing event=worker.pick_detail: %s", output)
	}
}
