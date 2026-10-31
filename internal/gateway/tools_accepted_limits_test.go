package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The owner's accept call is bounded: no empty key, and no key over 512 bytes.
func TestAcceptDifferenceKeyIsBounded(t *testing.T) {
	g, node := scopeGateway(t)
	info := protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Name: "owner", Status: "idle"}
	owner, err := g.store.UpsertSession(node, info)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.store.db.Exec(`UPDATE sessions SET online=1 WHERE id=?`, owner); err != nil {
		t.Fatal(err)
	}
	accept := func(key string) error {
		raw, _ := json.Marshal(map[string]any{"key": key, "accepted": true, "owner": owner})
		_, err := g.handle(context.Background(), &nodeConn{id: node}, "tools.accept", raw)
		return err
	}
	if err := accept(strings.Repeat("k", 512)); err != nil {
		t.Fatalf("a key at the limit was refused: %v", err)
	}
	if err := accept(strings.Repeat("k", 513)); err == nil {
		t.Fatal("a key over the limit was accepted")
	}
	if err := accept(""); err == nil {
		t.Fatal("an empty key was accepted")
	}
	if keys, _ := g.store.ToolsAccepted(); len(keys) != 1 {
		t.Fatalf("stored keys: %d", len(keys))
	}
}
