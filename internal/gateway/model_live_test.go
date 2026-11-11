package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

// fakeModelNode answers node.models over a websocket, as a daemon does, and
// counts the live reads the gateway makes.
func fakeModelNode(t *testing.T, models []string) (*nodeConn, *atomic.Int32) {
	t.Helper()
	return fakeModelNodeFor(t, map[string][]string{"opencode": models})
}

func fakeModelNodeFor(t *testing.T, byHarness map[string][]string) (*nodeConn, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	conns := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err == nil {
			conns <- ws
		}
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.CloseNow() })
	server := <-conns
	t.Cleanup(func() { server.CloseNow() })
	nc := &nodeConn{id: "c", ws: client, cancel: func() {}}
	go func() {
		for {
			_, raw, err := client.Read(ctx)
			if err != nil {
				return
			}
			var h protocol.Header
			if json.Unmarshal(raw, &h) == nil {
				nc.answer(h)
			}
		}
	}()
	go func() {
		for {
			_, raw, err := server.Read(ctx)
			if err != nil {
				return
			}
			var h protocol.Header
			if json.Unmarshal(raw, &h) != nil {
				continue
			}
			switch h.Type {
			case protocol.FrameNodeModels:
				var req protocol.ModelListRequest
				json.Unmarshal(h.Data, &req)
				calls.Add(1)
				var list []protocol.HarnessModels
				for name, models := range byHarness {
					if req.Harness != "" && name != req.Harness {
						continue
					}
					list = append(list, protocol.HarnessModels{Name: name, Models: models})
				}
				sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
				out := protocol.ModelListResult{Harnesses: list}
				res, err := protocol.EncodeResult(h.ID, out, nil)
				if err == nil {
					server.Write(ctx, websocket.MessageText, res)
				}
			}
		}
	}()
	return nc, &calls
}

func TestMachineGetModelsIsLiveAndCached(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("research", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeHello(node, "research", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetNodeHarnesses(node, []protocol.HarnessInfo{{Name: "opencode", Version: "1"}}); err != nil {
		t.Fatal(err)
	}
	nc, calls := fakeModelNode(t, []string{"alibaba-token-plan/deepseek-v4.1-flash-1017", "openai/gpt-6"})
	nc.id = node
	g := &Gateway{store: s, logger: log.New(io.Discard, "", 0), conns: map[string]*nodeConn{node: nc}}
	first, err := g.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: "research", Include: []string{"models"}, Harness: "opencode", ModelFilter: "alibaba"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Models == nil || first.Models.Total != 1 || first.Models.Items[0].ID != "alibaba-token-plan/deepseek-v4.1-flash-1017" {
		t.Fatalf("live models: %+v", first.Models)
	}
	second, err := g.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: "research", Include: []string{"models"}, Harness: "opencode", ModelFilter: "alibaba"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Models == nil || second.Models.Total != 1 {
		t.Fatalf("second live read: %+v", second.Models)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("harness listed %d times, want 1 within the five minute cache", got)
	}
}

func TestProfileAddValidatesLiveModel(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("research", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeHello(node, "research", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetNodeHarnesses(node, []protocol.HarnessInfo{{Name: "opencode", Version: "1"}}); err != nil {
		t.Fatal(err)
	}
	sid, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	nc, _ := fakeModelNode(t, []string{"alibaba-token-plan/deepseek-v4.1-flash-1017"})
	nc.id = node
	g := &Gateway{store: s, logger: log.New(io.Discard, "", 0), conns: map[string]*nodeConn{node: nc}}
	if _, err := g.workerProfileAction(node, "worker.profile.add", protocol.WorkerProfileFrame{WorkerProfileRequest: protocol.WorkerProfileRequest{Name: "deepseek", Harness: "opencode", Model: "alibaba-token-plan/deepseek-v4.1-flash-1017"}, Requester: sid}); err != nil {
		t.Fatalf("a listed model was refused: %v", err)
	}
	_, err = g.workerProfileAction(node, "worker.profile.add", protocol.WorkerProfileFrame{WorkerProfileRequest: protocol.WorkerProfileRequest{Name: "missing", Harness: "opencode", Model: "alibaba-token-plan/nope"}, Requester: sid})
	if err == nil || !strings.Contains(err.Error(), "no model in the list matches") {
		t.Fatalf("a model the live list lacks was accepted: %v", err)
	}
}
