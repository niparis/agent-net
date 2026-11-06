package gateway

import (
	"context"
	"encoding/json"
	"path/filepath"
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
	if !strings.Contains(string(b), "machine fixture") || !strings.Contains(string(b), "has not reported typed Graph support") {
		t.Fatalf("missing blocker: %s", b)
	}
}

func TestGraphRefsStatusMatchesEnable(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	node, _, err := g.store.CreateNode("gateway-fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	nc := &nodeConn{id: node}
	owner, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Delivery: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"owner": owner})
		result, err := g.handle(context.Background(), nc, "graph.refs.status", raw)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(result)
		var status struct {
			Blockers []string `json:"blockers"`
			Backup   string   `json:"backup"`
		}
		if err = json.Unmarshal(b, &status); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(status.Backup, filepath.Dir(g.opts.DataDir)+string(filepath.Separator)+"gateway-before-typed-graph-") || !strings.HasSuffix(status.Backup, ".db") {
			t.Fatalf("backup suggestion %q", status.Backup)
		}
		if len(status.Blockers) != 1 || !strings.Contains(status.Blockers[0], want) {
			t.Fatalf("blockers: %s", b)
		}
		raw, _ = json.Marshal(map[string]string{"owner": owner, "backup": filepath.Join(t.TempDir(), "backup")})
		_, err = g.handle(context.Background(), nc, "graph.refs.enable", raw)
		if err == nil || err.Error() != status.Blockers[0] {
			t.Fatalf("enable %v, status %q", err, status.Blockers[0])
		}
	}
	check("machine gateway-fixture has not reported")
	if _, err = g.handle(context.Background(), nc, "hello", []byte(`{"version":"v0.4.145","hostname":"gateway-fixture","graph_refs":true}`)); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	g.conns[node] = &nodeConn{id: node}
	g.mu.Unlock()
	defer func() { g.mu.Lock(); delete(g.conns, node); g.mu.Unlock() }()
	check("machine gateway-fixture has not completed")
	if _, err = g.store.db.Exec(`UPDATE nodes SET hostname='' WHERE id=?`, node); err != nil {
		t.Fatal(err)
	}
	check("machine (" + node + ") has not completed")
}
