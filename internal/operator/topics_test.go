package operator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSnapshotRefetchesOnlyChangedTopics(t *testing.T) {
	d, path := startFake(t, func(method string, _ json.RawMessage) (any, string) {
		switch method {
		case "hello":
			return map[string]any{}, ""
		case protocol.OpInfo:
			return protocol.OperatorInfo{Connected: true}, ""
		case protocol.OpAgents:
			return protocol.AgentsResult{Agents: []protocol.AgentView{{SessionID: "new"}}}, ""
		default:
			return nil, "unexpected call " + method
		}
	})
	c := New(path)
	defer c.Close()
	previous := Snapshot{Connected: true, Nodes: []protocol.NodeView{{Hostname: "kept"}}}
	snap, err := c.SnapshotWith(context.Background(), SnapshotOptions{Topics: map[string]bool{protocol.TopicAgents: true}, Previous: previous})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Agents) != 1 || snap.Agents[0].SessionID != "new" || len(snap.Nodes) != 1 || snap.Nodes[0].Hostname != "kept" {
		t.Fatalf("snapshot %+v", snap)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, call := range d.calls {
		if call != "hello" && call != protocol.OpInfo && call != protocol.OpAgents {
			t.Fatalf("unexpected call %s", call)
		}
	}
}

func TestTopicSubscriptionRejectsOlderDaemon(t *testing.T) {
	_, path := startFake(t, func(method string, _ json.RawMessage) (any, string) {
		if method == "hello" {
			return map[string]any{}, ""
		}
		return nil, "unknown method"
	})
	c := New(path)
	defer c.Close()
	if _, err := c.SubscribeTopics(context.Background()); err == nil {
		t.Fatal("old daemon accepted subscription")
	}
}
