package operator

import (
	"context"
	"encoding/json"

	"github.com/niparis/agent-net/internal/protocol"
)

// GraphView reads the component graph. It is one owner-only read: a map or a
// CTO conversation can show the graph, but neither can change it here.
func (s *Client) GraphView(ctx context.Context, q protocol.QueryGraph) (protocol.GraphView, error) {
	var out protocol.GraphView
	raw, err := s.action(ctx, protocol.OpGraphView, q)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}
