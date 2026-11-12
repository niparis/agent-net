package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	data, ok, err := g.handleToolRead(context.Background(), nil, "query.projects_layout", []byte(`{"machine":"box-alpha"}`))
	if !ok || err != nil {
		t.Fatalf("handleToolRead query.projects_layout: ok=%v err=%v", ok, err)
	}
	r, ok := data.(protocol.ProjectsLayoutResult)
	if !ok || len(r.Machines) != 1 || r.Machines[0].Machine != "box-alpha" {
		t.Errorf("handleToolRead result mismatch: %+v", data)
	}
}

func fakeSlowNode(t *testing.T, g *Gateway, nodeID, hostname string) {
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

	// Server read loop
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

	// Client loop that deliberately ignores node.projects_layout (does not answer)
	go func() {
		for {
			_, _, e := client.Read(ctx)
			if e != nil {
				return
			}
		}
	}()
}

func TestGatewayProjectsLayoutSlowAndOldNodes(t *testing.T) {
	s, _ := newTestStore(t)

	// 1. Modern fast node
	idFast, _, err := s.CreateNode("box-fast", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeHello(idFast, "box-fast", "v0.4.133"); err != nil {
		t.Fatal(err)
	}

	// 2. Old node (below 0.4.132)
	idOld, _, err := s.CreateNode("box-old", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeHello(idOld, "box-old", "v0.4.132"); err != nil {
		t.Fatal(err)
	}

	// 3. Modern slow node (ignores request)
	idSlow, _, err := s.CreateNode("box-slow", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeHello(idSlow, "box-slow", "v0.4.133"); err != nil {
		t.Fatal(err)
	}

	// 4. Offline node (no conn in gateway)
	idOffline, _, err := s.CreateNode("box-offline", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeHello(idOffline, "box-offline", "v0.4.133"); err != nil {
		t.Fatal(err)
	}

	g := &Gateway{store: s, conns: map[string]*nodeConn{}}

	layoutFast := protocol.MachineLayout{
		Machine:   "box-fast",
		Home:      "/home/fast",
		Roots:     []protocol.LayoutRoot{},
		Loose:     []protocol.LayoutProject{},
		ScannedAt: 1000,
	}
	fakeConnectedNode(t, g, idFast, "box-fast", layoutFast)

	// Connect old node as well (it should be skipped by version without being called)
	fakeConnectedNode(t, g, idOld, "box-old", protocol.MachineLayout{Machine: "box-old"})

	// Connect slow node
	fakeSlowNode(t, g, idSlow, "box-slow")

	// Call with caller context timeout of 150ms to keep test fast (< 2s)
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()

	res, err := g.ProjectsLayout(ctx, protocol.ProjectsLayoutRequest{})
	if err != nil {
		t.Fatalf("ProjectsLayout: %v", err)
	}

	if len(res.Machines) != 1 || res.Machines[0].Machine != "box-fast" {
		t.Fatalf("Machines = %+v, want only box-fast", res.Machines)
	}

	if len(res.Skipped) != 3 {
		t.Fatalf("Skipped count = %d, want 3: %+v", len(res.Skipped), res.Skipped)
	}

	wantSkipped := map[string]string{
		"box-offline": "offline",
		"box-old":     "old version",
		"box-slow":    "timeout",
	}
	for _, sk := range res.Skipped {
		wantReason, ok := wantSkipped[sk.Machine]
		if !ok {
			t.Errorf("unexpected skipped machine: %+v", sk)
			continue
		}
		if sk.Reason != wantReason {
			t.Errorf("machine %s reason = %q, want %q", sk.Machine, sk.Reason, wantReason)
		}
	}
}

func TestGatewayProjectsLayoutUnknownMachineFilter(t *testing.T) {
	s, _ := newTestStore(t)
	idA, _, err := s.CreateNode("box-alpha", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeHello(idA, "box-alpha", "v0.4.133"); err != nil {
		t.Fatal(err)
	}
	idB, _, err := s.CreateNode("box-beta", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeHello(idB, "box-beta", "v0.4.133"); err != nil {
		t.Fatal(err)
	}

	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	_, err = g.ProjectsLayout(context.Background(), protocol.ProjectsLayoutRequest{Machine: "box-gamma"})
	if err == nil {
		t.Fatalf("expected error for unknown machine filter")
	}
	if !strings.Contains(err.Error(), "box-gamma") || !strings.Contains(err.Error(), "box-alpha") || !strings.Contains(err.Error(), "box-beta") {
		t.Errorf("error %q does not name unknown machine and known machines", err.Error())
	}
}
