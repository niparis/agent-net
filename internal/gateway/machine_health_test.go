package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMachineHealthHelloAndOldDaemon(t *testing.T) {
	s, _ := newTestStore(t)
	id, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	nc := &nodeConn{id: id}
	read := func() map[string]any {
		t.Helper()
		out, err := g.MachineGet(protocol.MachineGetRequest{Machine: "box"})
		if err != nil {
			t.Fatal(err)
		}
		checkOutput(t, "agent_machine_get", out)
		b, _ := json.Marshal(out)
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if _, err := g.handle(context.Background(), nc, "hello", []byte(`{"hostname":"box","version":"old"}`)); err != nil {
		t.Fatal(err)
	}
	if h, ok := read()["health"].(map[string]any); !ok || h["status"] != "not reported" {
		t.Fatalf("old health = %v", h)
	}
	hello := `{"hostname":"box","version":"new","health":{"status":"reported","restart_time":"2026-01-01T10:00:00Z","path":"/test/bin","helpers":{"git":"/test/bin/git","herdr":"not found"},"state_free_bytes":1234}}`
	if _, err := g.handle(context.Background(), nc, "hello", []byte(hello)); err != nil {
		t.Fatal(err)
	}
	h := read()["health"].(map[string]any)
	if h["path"] != "/test/bin" || h["state_free_bytes"] != float64(1234) || h["restart_time"] != "2026-01-01T10:00:00Z" || h["helpers"].(map[string]any)["git"] != "/test/bin/git" {
		t.Fatalf("health = %v", h)
	}
}
