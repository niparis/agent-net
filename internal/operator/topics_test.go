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

func TestSnapshotSendsVersionsAndKeepsUnchangedParts(t *testing.T) {
	d, path := startFake(t, func(method string, _ json.RawMessage) (any, string) {
		switch method {
		case "hello":
			return map[string]any{}, ""
		case protocol.OpInfo:
			return protocol.OperatorInfo{Connected: true}, ""
		case protocol.OpAgents:
			return protocol.AgentsResult{NotModified: true}, ""
		case protocol.OpNodes:
			return protocol.NodesInfo{NotModified: true}, ""
		case "learned_list":
			return []protocol.LearnedSuggestion{}, ""
		}
		return map[string]any{}, ""
	})
	c := New(path)
	defer c.Close()
	previous := Snapshot{Connected: true,
		Agents:          []protocol.AgentView{{SessionID: "keep-agent"}},
		Nodes:           []protocol.NodeView{{Hostname: "keep-node"}},
		Learned:         []protocol.LearnedSuggestion{{ID: 3}},
		GatewayWarnings: []string{"keep-warning"},
	}
	versions := map[string]uint64{
		protocol.TopicAgents:  7,
		protocol.TopicNodes:   8,
		protocol.TopicLearned: 9,
		protocol.TopicChanges: 10,
	}
	snap, err := c.SnapshotWith(context.Background(), SnapshotOptions{Previous: previous, IfVersions: versions, Lite: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Agents) != 1 || snap.Agents[0].SessionID != "keep-agent" || len(snap.Nodes) != 1 || snap.Nodes[0].Hostname != "keep-node" {
		t.Fatalf("unchanged parts were cleared: %+v", snap)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var agents protocol.QueryAgents
	if err := json.Unmarshal(d.params[protocol.OpAgents], &agents); err != nil {
		t.Fatal(err)
	}
	if agents.IfVersion != 7 || !agents.Lite {
		t.Fatalf("agents asked with %+v, want if_version 7 and lite", agents)
	}
	var nodes protocol.QueryNodes
	if err := json.Unmarshal(d.params[protocol.OpNodes], &nodes); err != nil {
		t.Fatal(err)
	}
	if nodes.IfVersion != 8 {
		t.Fatalf("nodes asked with if_version %d, want 8", nodes.IfVersion)
	}
	var learned protocol.LearnedListQuery
	if err := json.Unmarshal(d.params["learned_list"], &learned); err != nil {
		t.Fatal(err)
	}
	if learned.IfVersion != 9 || !learned.Lite {
		t.Fatalf("learned asked with %+v, want if_version 9 and lite", learned)
	}
}
