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

// stopTestNode connects a fake machine. It records the type of each frame the
// gateway sends, and answers worker.kill with kill(); other calls get no answer.
func stopTestNode(t *testing.T, g *Gateway, node string, kill func() error) (*nodeConn, chan string) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ws, err := websocket.Accept(w, r, nil); err == nil {
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
	setTestConn(t, g, node, nc)
	frames := make(chan string, 100)
	go func() {
		for {
			_, raw, e := client.Read(ctx)
			if e != nil {
				return
			}
			var h protocol.Header
			if json.Unmarshal(raw, &h) != nil || h.Type == "result" {
				continue
			}
			frames <- h.Type
			if h.Type == "worker.kill" {
				b, _ := protocol.EncodeResult(h.ID, nil, kill())
				var reply protocol.Header
				_ = json.Unmarshal(b, &reply)
				nc.answer(reply)
			}
		}
	}()
	return nc, frames
}

// stopSetup makes a machine, an owner session on it and a running agy worker
// that the owner started.
func stopSetup(t *testing.T) (g *Gateway, node, owner, worker string) {
	t.Helper()
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	node, _, err = g.store.CreateNode("host1", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	owner, err = g.store.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err = g.store.UpsertSession(node, workerReport(owner, "idle"))
	if err != nil {
		t.Fatal(err)
	}
	return g, node, owner, worker
}

func workerReport(owner, status string) protocol.SessionInfo {
	return protocol.SessionInfo{Harness: "agy", NativeID: "conv-1", Status: status, Cwd: "/tmp/wt",
		Worker: &protocol.WorkerInfo{ID: "w_1", Owner: owner, Project: "shop", Task: "Fix internal/shop/cart.go", RunGeneration: 1}}
}

func workerOnline(t *testing.T, g *Gateway, worker string) bool {
	t.Helper()
	a, ok, err := g.store.Agent(worker)
	if err != nil || !ok {
		t.Fatalf("agent %s: %v %v", worker, ok, err)
	}
	return a.Online
}

// The machine of the evidence answered "no worker ... runs on this machine"
// while its daemon kept reporting the session. The stop still ends the
// worker, and no later report of that session makes it a running worker
// again, in the worker list or in the overlap flags.
func TestStoppedWorkerStaysEndedWhenItsMachineStillReportsIt(t *testing.T) {
	g, node, owner, worker := stopSetup(t)
	nc, _ := stopTestNode(t, g, node, func() error {
		return reject("no worker with session %s runs on this machine", worker)
	})
	out, err := g.workerStop(t.Context(), nc, protocol.WorkerStopFrame{Session: worker, Requester: owner})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if out["stopped"] != true {
		t.Fatalf("stop answer = %v", out)
	}
	// The daemon's keepalive and a view of the conversation both report it.
	if _, err := g.store.UpsertSession(node, workerReport(owner, "busy")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: "agy", NativeID: "conv-1", Status: "idle", Cwd: "/tmp/wt"}); err != nil {
		t.Fatal(err)
	}
	if workerOnline(t, g, worker) {
		t.Fatal("the stopped worker is online again")
	}
	if a, _, _ := g.store.Agent(worker); a.Worker == nil {
		t.Fatal("a report without worker facts made the stopped worker a plain session")
	}
	overlaps, err := g.store.runningWorkerBriefs("shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(overlaps) != 0 {
		t.Fatalf("the stopped worker is still an overlap: %+v", overlaps)
	}
	// Its machine cannot start it again either.
	raw, _ := json.Marshal(protocol.WorkerRunState{Session: worker, RunGeneration: 2})
	if _, err := g.handleWorker(t.Context(), nc, "worker.run_state", raw); err == nil || !strings.Contains(err.Error(), "was stopped") {
		t.Fatalf("run_state of a stopped worker: %v", err)
	}
}

// A message to a stopped worker does not start it. The caller gets the
// message back in its own inbox, with a note that the worker was stopped.
func TestMessageToStoppedWorkerIsKeptForItsCaller(t *testing.T) {
	g, node, owner, worker := stopSetup(t)
	nc, frames := stopTestNode(t, g, node, func() error { return nil })
	if _, err := g.workerStop(t.Context(), nc, protocol.WorkerStopFrame{Session: worker, Requester: owner}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(protocol.MsgSend{Sender: owner, Recipient: worker, Body: "one more fix please", IdempotencyKey: "k1"})
	_, err := g.handle(t.Context(), nc, "msg.send", raw)
	if err == nil || !strings.Contains(err.Error(), "was stopped") {
		t.Fatalf("send to a stopped worker: %v", err)
	}
	if note := noteFor(t, g, node, owner, worker); !strings.Contains(note, "one more fix please") {
		t.Fatalf("no note with the message in the caller's inbox: %q", note)
	}
	for len(frames) > 0 {
		if typ := <-frames; typ == "worker.resume" {
			t.Fatal("the stopped worker was started again")
		}
	}
}

func noteFor(t *testing.T, g *Gateway, node, owner, worker string) string {
	t.Helper()
	msgs, err := g.store.Messages(node, protocol.QueryMessages{SessionID: owner})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.From == worker && strings.Contains(m.Body, "was stopped") {
			return m.Body
		}
	}
	return ""
}

// A stop while the machine is away is kept. The answer says it is pending, a
// message that waited for the worker goes back to its sender, and the stop is
// sent first when the machine syncs again.
func TestStopToAnOfflineMachineIsPendingAndSentOnReconnect(t *testing.T) {
	g, node, owner, worker := stopSetup(t)
	ownerNode, _, err := g.store.CreateNode("host2", "linux")
	if err != nil {
		t.Fatal(err)
	}
	caller, err := g.store.UpsertSession(ownerNode, protocol.SessionInfo{Harness: "claude", NativeID: "caller"})
	if err != nil {
		t.Fatal(err)
	}
	waiting, _, err := g.store.StoreMessage(protocol.MsgSend{Sender: caller, Recipient: worker, Body: "waiting text", IdempotencyKey: "k-wait"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = stopTestNode(t, g, ownerNode, func() error { return nil })
	requester := &nodeConn{id: node}
	out, err := g.workerStop(t.Context(), requester, protocol.WorkerStopFrame{Session: worker, Requester: owner})
	if err != nil {
		t.Fatalf("stop to an offline machine: %v", err)
	}
	if out["pending"] != true || !strings.Contains(out["note"].(string), "pending") {
		t.Fatalf("answer = %v", out)
	}
	if workerOnline(t, g, worker) {
		t.Fatal("a pending stop left the worker online")
	}
	if m, _, _ := g.store.Message(waiting.ID); m.State != protocol.StateWithdrawn {
		t.Fatalf("the waiting message is %s", m.State)
	}
	if noteFor(t, g, ownerNode, caller, worker) == "" {
		t.Fatal("the sender of the waiting message got no note")
	}

	nc, frames := stopTestNode(t, g, node, func() error { return nil })
	raw, _ := json.Marshal(protocol.SessionSync{Sessions: []protocol.SessionInfo{workerReport(owner, "busy")}})
	if err := g.answer(t.Context(), nc, protocol.Header{ID: "c1", Type: "session.sync"}, raw); err != nil {
		t.Fatal(err)
	}
	select {
	case typ := <-frames:
		if typ != "worker.kill" {
			t.Fatalf("first frame after the sync is %s, not the stop", typ)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the pending stop was not sent")
	}
	stops, err := g.store.pendingWorkerStops(node)
	if err != nil || len(stops) != 0 {
		t.Fatalf("pending stops after the reconnect: %v %v", stops, err)
	}
	if workerOnline(t, g, worker) {
		t.Fatal("the sync brought the stopped worker back")
	}
}
