package gateway

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func (g *Gateway) requestOwnerUI(ctx context.Context, nc *nodeConn, raw []byte) (any, error) {
	var p protocol.OwnerUIRelay
	if json.Unmarshal(raw, &p) != nil || !g.validToolOwner(nc, p.Owner) {
		return nil, reject("owner authority required")
	}
	if p.Request.EnterSession == "" {
		return nil, reject("session_id required")
	}
	// Only an identifier and action cross this relay, never a terminal command.
	request := protocol.UIRequest{EnterSession: p.Request.EnterSession, EnterCTO: p.Request.EnterCTO}
	type candidate struct {
		id   string
		node *nodeConn
	}
	var nodes []candidate
	g.mu.Lock()
	for id, node := range g.conns {
		if p.NodeID == "" || p.NodeID == id {
			nodes = append(nodes, candidate{id, node})
		}
	}
	g.mu.Unlock()
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].id < nodes[j].id })
	for _, node := range nodes {
		data, err := node.node.request(ctx, "node.owner_ui.request", request, 5*time.Second)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			continue
		}
		var result struct {
			Shown bool `json:"shown"`
		}
		if json.Unmarshal(data, &result) == nil && result.Shown {
			return map[string]any{"requested": true, "session_id": request.EnterSession, "node_id": node.id}, nil
		}
	}
	return nil, reject("no owner TUI is open on the requested machines")
}
