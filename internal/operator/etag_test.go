package operator

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The snapshot keeps the ETag of the agents and the machines next to their
// rows, sends it on the next read, and keeps the rows when the gateway answers
// NotModified. The gateway then sends no rows, though its topic moved.
func TestSnapshotSendsETagsAndKeepsRowsWhenNotModified(t *testing.T) {
	var mu sync.Mutex
	var agentAsks []protocol.QueryAgents
	var nodeAsks []protocol.QueryNodes
	_, path := startFake(t, func(method string, params json.RawMessage) (any, string) {
		switch method {
		case protocol.OpInfo:
			return protocol.OperatorInfo{Connected: true}, ""
		case protocol.OpAgents:
			var q protocol.QueryAgents
			json.Unmarshal(params, &q)
			mu.Lock()
			agentAsks = append(agentAsks, q)
			mu.Unlock()
			if q.IfETag == "agents-1" {
				return protocol.AgentsResult{NotModified: true, ETag: "agents-1"}, ""
			}
			return protocol.AgentsResult{Agents: []protocol.AgentView{{SessionID: "s_1"}}, ETag: "agents-1"}, ""
		case protocol.OpNodes:
			var q protocol.QueryNodes
			json.Unmarshal(params, &q)
			mu.Lock()
			nodeAsks = append(nodeAsks, q)
			mu.Unlock()
			if q.IfETag == "nodes-1" {
				return protocol.NodesInfo{NotModified: true, ETag: "nodes-1"}, ""
			}
			return protocol.NodesInfo{Nodes: []protocol.NodeView{{NodeID: "n_1"}}, ETag: "nodes-1"}, ""
		case protocol.OpProjects:
			return protocol.ProjectsResult{}, ""
		}
		return map[string]any{}, ""
	})
	c := New(path)
	defer c.Close()
	first, err := c.SnapshotWith(context.Background(), SnapshotOptions{Lite: true})
	if err != nil || len(first.Agents) != 1 || len(first.Nodes) != 1 || first.AgentsETag != "agents-1" || first.NodesETag != "nodes-1" {
		t.Fatalf("first snapshot: %+v %v", first, err)
	}
	second, err := c.SnapshotWith(context.Background(), SnapshotOptions{Lite: true, Previous: first})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Agents) != 1 || len(second.Nodes) != 1 || second.AgentsETag != "agents-1" || second.NodesETag != "nodes-1" {
		t.Fatalf("second snapshot lost its rows: %+v", second)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(agentAsks) != 2 || !agentAsks[0].WantETag || agentAsks[0].IfETag != "" || agentAsks[1].IfETag != "agents-1" {
		t.Fatalf("agents asks: %+v", agentAsks)
	}
	if len(nodeAsks) != 2 || !nodeAsks[0].WantETag || nodeAsks[0].IfETag != "" || nodeAsks[1].IfETag != "nodes-1" {
		t.Fatalf("nodes asks: %+v", nodeAsks)
	}
	unchanged := map[string]bool{}
	for _, m := range second.Unchanged {
		unchanged[m] = true
	}
	if !unchanged[protocol.OpAgents] || !unchanged[protocol.OpNodes] {
		t.Fatalf("unchanged parts: %v", second.Unchanged)
	}
}

// Readers that do not set Lite (the phone, the command line) get no ETag.
func TestSnapshotWithoutLiteAsksForNoETag(t *testing.T) {
	var mu sync.Mutex
	var asked protocol.QueryAgents
	_, path := startFake(t, func(method string, params json.RawMessage) (any, string) {
		switch method {
		case protocol.OpInfo:
			return protocol.OperatorInfo{Connected: true}, ""
		case protocol.OpAgents:
			mu.Lock()
			json.Unmarshal(params, &asked)
			mu.Unlock()
			return protocol.AgentsResult{Agents: []protocol.AgentView{{SessionID: "s_1"}}}, ""
		case protocol.OpProjects:
			return protocol.ProjectsResult{}, ""
		}
		return map[string]any{}, ""
	})
	c := New(path)
	defer c.Close()
	if _, err := c.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if asked.WantETag || asked.IfETag != "" {
		t.Fatalf("a full read asked for an ETag: %+v", asked)
	}
}
