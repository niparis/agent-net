package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestGraphRefsStatusOwnerAndBlockers(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	node, _, err := g.store.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	nc := &nodeConn{id: node}
	owner, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Delivery: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.handle(context.Background(), nc, "graph.refs.status", []byte(`{}`)); err == nil {
		t.Fatal("non-owner read status")
	}
	raw, _ := json.Marshal(map[string]string{"owner": owner})
	result, err := g.handle(context.Background(), nc, "graph.refs.status", raw)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(result)
	if !strings.Contains(string(b), node) || !strings.Contains(string(b), "has not reported typed Graph support") {
		t.Fatalf("missing blocker: %s", b)
	}
}
