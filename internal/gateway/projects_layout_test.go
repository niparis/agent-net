package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

func fakeConnectedNode(t *testing.T, g *Gateway, nodeID, hostname string, layout protocol.MachineLayout) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		accepted <- ws
	}))
	t.Cleanup(server.Close)

	client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(websocket.StatusNormalClosure, "") })

	ws := <-accepted
	nc := &nodeConn{id: nodeID, ws: ws, dead: make(chan struct{}), cancel: cancel}
	g.mu.Lock()
	g.conns[nodeID] = nc
	g.mu.Unlock()

	// Read loop on server end to route answers to nc
	go func() {
		for {
			_, raw, e := ws.Read(ctx)
			if e != nil {
				return
			}
			var h protocol.Header
			if json.Unmarshal(raw, &h) == nil && h.Type == "result" {
				nc.answer(h)
			}
		}
	}()

	// Client loop answering node.projects_layout
	go func() {
		for {
			_, raw, e := client.Read(ctx)
			if e != nil {
				return
			}
			var h protocol.Header
			_ = json.Unmarshal(raw, &h)
			if h.Type == "node.projects_layout" && h.ID != "" {
				b, _ := protocol.EncodeResult(h.ID, layout, nil)
				_ = client.Write(ctx, websocket.MessageText, b)
			}
		}
	}()
}

func TestGatewayProjectsLayout(t *testing.T) {
	s, _ := newTestStore(t)

	// Create two nodes in the store
	idA, _, err := s.CreateNode("box-alpha", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeHello(idA, "box-alpha", "v1"); err != nil {
		t.Fatal(err)
	}
	idB, _, err := s.CreateNode("box-beta", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeHello(idB, "box-beta", "v1"); err != nil {
		t.Fatal(err)
	}

	g := &Gateway{store: s, conns: map[string]*nodeConn{}}

	layoutA := protocol.MachineLayout{
		Machine:   "box-alpha",
		Home:      "/home/alice",
		Roots:     []protocol.LayoutRoot{{Path: "~/code", Kind: "grouped", Groups: []protocol.LayoutGroup{}, Projects: []protocol.LayoutProject{}}},
		ScannedAt: 1000,
	}
	layoutB := protocol.MachineLayout{
		Machine:   "box-beta",
		Home:      "/Users/bob",
		Roots:     []protocol.LayoutRoot{{Path: "~/src", Kind: "flat", Groups: []protocol.LayoutGroup{}, Projects: []protocol.LayoutProject{}}},
		ScannedAt: 2000,
	}

	fakeConnectedNode(t, g, idA, "box-alpha", layoutA)
	fakeConnectedNode(t, g, idB, "box-beta", layoutB)

	// Test querying all machines
	res, err := g.ProjectsLayout(context.Background(), protocol.ProjectsLayoutRequest{})
	if err != nil {
		t.Fatalf("ProjectsLayout: %v", err)
	}

	if len(res.Machines) != 2 {
		t.Fatalf("got %d machines, want 2: %+v", len(res.Machines), res.Machines)
	}
	if res.Machines[0].Machine != "box-alpha" || res.Machines[1].Machine != "box-beta" {
		t.Errorf("machines order mismatch: %+v", res.Machines)
	}

	// Test querying a single machine by name
	resB, err := g.ProjectsLayout(context.Background(), protocol.ProjectsLayoutRequest{Machine: "box-beta"})
	if err != nil {
		t.Fatalf("ProjectsLayout single: %v", err)
	}
	if len(resB.Machines) != 1 || resB.Machines[0].Machine != "box-beta" {
		t.Errorf("single machine query = %+v, want box-beta", resB.Machines)
	}

	// Test handleToolRead dispatch
	data, ok, err := g.handleToolRead(nil, "query.projects_layout", []byte(`{"machine":"box-alpha"}`))
	if !ok || err != nil {
		t.Fatalf("handleToolRead query.projects_layout: ok=%v err=%v", ok, err)
	}
	r, ok := data.(protocol.ProjectsLayoutResult)
	if !ok || len(r.Machines) != 1 || r.Machines[0].Machine != "box-alpha" {
		t.Errorf("handleToolRead result mismatch: %+v", data)
	}
}
