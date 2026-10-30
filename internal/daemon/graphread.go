package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Graph read (decision 0022). A session's charter and the edges that matter to
// it come from the gateway on every turn, through the same local method as the
// delegation policy. The graph changes rarely, so the daemon keeps the last
// answer for graphTTL, and uses it for graphStale when the gateway is gone.

const (
	graphTTL   = 2 * time.Second
	graphStale = 10 * time.Minute
)

type graphEntry struct {
	at    time.Time
	graph protocol.Graph
}

type graphState struct {
	mu    sync.Mutex
	cache map[string]graphEntry
	// now is replaced in tests.
	now func() time.Time
}

func (g *graphState) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// graphOf returns the graph of one node. ok is false when neither the gateway
// nor a recent answer has it.
func (d *Daemon) graphOf(ctx context.Context, project string) (protocol.Graph, bool) {
	if project == "" {
		return protocol.Graph{}, false
	}
	g := &d.graph
	now := g.clock()
	g.mu.Lock()
	e, have := g.cache[project]
	g.mu.Unlock()
	if have && now.Sub(e.at) < graphTTL {
		return e.graph, true
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	raw, err := d.call(cctx, "graph.read", protocol.GraphRead{Project: project})
	var got protocol.GraphGot
	if err == nil {
		err = json.Unmarshal(raw, &got)
	}
	if err != nil {
		if have && now.Sub(e.at) < graphStale {
			return e.graph, true
		}
		return protocol.Graph{}, false
	}
	g.mu.Lock()
	if g.cache == nil {
		g.cache = map[string]graphEntry{}
	}
	g.cache[project] = graphEntry{at: now, graph: got.Graph}
	g.mu.Unlock()
	return got.Graph, true
}

// charterContext is the per-turn text for a session's node. Empty when the
// node has no charter.
func (d *Daemon) charterContext(ctx context.Context, project string) string {
	g, ok := d.graphOf(ctx, project)
	if !ok {
		return ""
	}
	return protocol.NodeContext(g, project)
}

// charterBrief is the charter section of a worker's brief. Empty when the
// node has no charter.
func (d *Daemon) charterBrief(ctx context.Context, project string) string {
	g, ok := d.graphOf(ctx, project)
	if !ok {
		return ""
	}
	for _, n := range g.Nodes {
		if n.Name == project {
			return protocol.CharterBrief(n)
		}
	}
	return ""
}
