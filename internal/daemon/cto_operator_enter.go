package daemon

import (
	"context"
	"fmt"

	"github.com/niparis/agent-net/internal/protocol"
)

// requestOwnerEnter delegates opening to one visible owner TUI, including
// a TUI on another machine. Its herdr and terminal context determines the plan.
func (d *Daemon) requestOwnerEnter(ctx context.Context, method, id, node string) (any, error) {
	if method == "cto_enter" {
		id = d.ctoSessionID()
	}
	if id == "" {
		return nil, fmt.Errorf("%s needs session_id", method)
	}
	request := protocol.UIRequest{EnterSession: id, EnterCTO: method == "cto_enter"}
	if (node == "" || node == d.NodeID()) && d.showLocalOwnerEnter(request) {
		return map[string]any{"requested": true, "session_id": id, "node_id": d.NodeID()}, nil
	}
	return d.call(ctx, "owner.ui.request", protocol.OwnerUIRelay{Owner: d.ownerSessionID(), NodeID: node, Request: request})
}

func (d *Daemon) showLocalOwnerEnter(q protocol.UIRequest) bool {
	if q.EnterSession == "" || d.ln == nil {
		return false
	}
	// Gateway input cannot carry a command or a caller's herdr socket.
	request := protocol.UIRequest{EnterSession: q.EnterSession, EnterCTO: q.EnterCTO}
	d.ln.mu.Lock()
	var candidates []*localConn
	for c := range d.ln.conns {
		candidates = append(candidates, c)
	}
	d.ln.mu.Unlock()
	for _, c := range candidates {
		c.mu.Lock()
		owner := c.client == "cli" && c.tui
		c.mu.Unlock()
		if owner && c.writeJSON(map[string]any{"method": "cto_ui", "params": request}) == nil {
			return true
		}
	}
	return false
}
