package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// agentGraph is the public graph read behind the agent_graph_read tool and the
// CTO's graph view. The gateway frame graph.read is already public to enrolled
// machines; this daemon call projects it onto the public fields, which drops a
// node's owner-only settings before any agent can see them.
func (d *Daemon) agentGraph(ctx context.Context, q protocol.PublicGraphQuery) (protocol.PublicGraphPage, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := d.call(cctx, "graph.read", protocol.GraphRead{Project: q.Node, Ref: q.Ref, Typed: q.Typed, Also: true, Root: q.Root})
	if err != nil {
		return protocol.PublicGraphPage{}, err
	}
	var got protocol.GraphGot
	if err := json.Unmarshal(raw, &got); err != nil {
		return protocol.PublicGraphPage{}, err
	}
	return protocol.PublicGraph(got.Graph, q), nil
}
