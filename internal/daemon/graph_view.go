package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// dispatchGraphView serves graph_view, the owner-only read behind the TUI's map.
// It changes nothing. The graph itself lives in the gateway, so this is one
// query frame and no local state.
func (lc *localConn) dispatchGraphView(ctx context.Context, raw json.RawMessage) (any, bool, error) {
	var q protocol.QueryGraph
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &q) != nil {
		return nil, true, errors.New("bad graph_view params")
	}
	view, err := lc.d.graphView(ctx, q)
	return view, true, err
}

// graphView reads the component graph from the gateway.
func (d *Daemon) graphView(ctx context.Context, q protocol.QueryGraph) (protocol.GraphView, error) {
	var out protocol.GraphView
	data, err := d.call(ctx, "query.graph", q)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(data, &out)
	return out, err
}
