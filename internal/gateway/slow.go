package gateway

import (
	"time"

	"github.com/niparis/agent-net/internal/diagnostics"
	"github.com/niparis/agent-net/internal/protocol"
)

func (g *Gateway) slowLoop(name string, start time.Time) {
	diagnostics.Slow(start, func(ms int64) { g.logLine("slow", "loop", name, "ms", ms) })
}

func (g *Gateway) logStepDispatchCount() {
	if count := g.stepDispatchRuns.Swap(0); count > 60 {
		g.logLine("step.dispatch_count", "level", "debug", "count", count)
	}
}

// finishRequest ends the timing of one request frame: the slow log line and
// the request counters (request_stats.go). sent is the size of the response.
func (g *Gateway) finishRequest(h protocol.Header, node string, start time.Time, sent *int) {
	g.slowRequest(h.Type, node, start)
	if g.reqs != nil {
		now := time.Now()
		g.reqs.record(h.Type, h.Via, now, now.Sub(start), *sent)
	}
}

func (g *Gateway) slowRequest(method, node string, start time.Time) {
	diagnostics.Slow(start, func(ms int64) { g.logLine("slow", "method", method, "ms", ms, "node", node) })
}
