package gateway

import (
	"context"
	"encoding/json"
	"strings"
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
		out, err := g.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: "box"})
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

// A machine that is not connected gives an offline note, and the call works.
func TestMachineGetUsageOfflineNote(t *testing.T) {
	s, _ := newTestStore(t)
	id, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetNodeHello(id, "box", "1"); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	out, err := g.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: "box"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Usage == nil || out.Usage.Status != protocol.MachineUsageOffline || !strings.Contains(out.Usage.Note, "offline") || len(out.Usage.Top) != 0 {
		t.Fatalf("usage = %+v", out.Usage)
	}
	checkOutput(t, "agent_machine_get", out)
	cat, err := g.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: "box", Include: []string{"playbook"}})
	if err != nil || cat.Usage != nil {
		t.Fatalf("category call: err=%v usage=%+v", err, cat.Usage)
	}
}
