package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The usage figures travel with the worker record, so a TUI on another machine
// reads them from the gateway like the task title.
func TestWorkerUsageTravelsThroughTheStore(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "n1", Name: "worker",
		Worker: &protocol.WorkerInfo{ID: "w_1", Owner: "s_o", Project: "shop", StartedAt: 10,
			Task: "deploy the routes", ContextTokens: 84000, ContextLimit: 1000000, Tokens: 345000}})
	if err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.Agent(id)
	if err != nil || !ok || a.Worker == nil {
		t.Fatalf("agent: %+v %v %v", a, ok, err)
	}
	w := a.Worker
	if w.Task != "deploy the routes" || w.ContextTokens != 84000 || w.ContextLimit != 1000000 || w.Tokens != 345000 {
		t.Errorf("worker facts: %+v", w)
	}
}
