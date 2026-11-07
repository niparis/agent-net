package gateway

import (
	"context"
	"encoding/json"

	"github.com/niparis/agent-net/internal/protocol"
)

// handleProjectChange uses the same owner identity and machine resolution as
// project creation. The target daemon checks its folders and writes its registry.
func (g *Gateway) handleProjectChange(ctx context.Context, nc *nodeConn, raw []byte) (any, error) {
	var p protocol.ProjectChangeRelay
	if json.Unmarshal(raw, &p) != nil {
		return nil, reject("bad project change")
	}
	owner, ok, err := g.store.Agent(p.Owner)
	if err != nil {
		return nil, err
	}
	if !ok || owner.NodeID != nc.id || !protocol.IsOwner(owner.Harness) || owner.Worker != nil {
		return nil, reject("only the owner session of this machine may change projects")
	}
	node, err := g.sessionRelayNode(p.NodeID)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	target := g.conns[node]
	g.mu.Unlock()
	if target == nil {
		return nil, reject("machine %s is offline", p.NodeID)
	}
	p.FromNode, p.NodeID = nc.id, node
	ctx, cancel := context.WithTimeout(ctx, sessionRelayWait)
	defer cancel()
	return target.call(ctx, protocol.FrameProjectOwnerChange, p)
}
