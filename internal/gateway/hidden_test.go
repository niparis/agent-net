package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestHiddenItemsPersistAndForgetEndedSessionsAfter30Days(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("fake-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	old, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "old"})
	if err != nil {
		t.Fatal(err)
	}
	recent, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "recent"})
	if err != nil {
		t.Fatal(err)
	}
	live, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "live"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionOffline(node, old); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionOffline(node, recent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET last_seen=? WHERE id=?`, now.Add(-31*24*time.Hour).UnixNano(), old); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, logger: log.New(io.Discard, "", 0)}
	nc := &nodeConn{id: node}
	write := func(items map[string]any) {
		t.Helper()
		if _, err := g.handle(context.Background(), nc, "hidden.set", mustJSON(t, items)); err != nil {
			t.Fatal(err)
		}
	}
	read := func() struct {
		Sessions []string `json:"sessions"`
		Nodes    []string `json:"nodes"`
	} {
		t.Helper()
		data, err := g.handle(context.Background(), nc, "hidden.get", nil)
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Sessions []string `json:"sessions"`
			Nodes    []string `json:"nodes"`
		}
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	write(map[string]any{"sessions": []string{old, recent, live}, "nodes": []string{"cogforge"}})
	got := read()
	if len(got.Sessions) != 2 || got.Sessions[0] != live && got.Sessions[1] != live {
		t.Fatalf("hidden sessions after stale ended entry cleanup: %v", got.Sessions)
	}
	if got.Nodes == nil || len(got.Nodes) != 1 || got.Nodes[0] != "cogforge" {
		t.Fatalf("hidden nodes: %v", got.Nodes)
	}
	got = read()
	if len(got.Sessions) != 2 || len(got.Nodes) != 1 {
		t.Fatalf("preference did not persist: %+v", got)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
