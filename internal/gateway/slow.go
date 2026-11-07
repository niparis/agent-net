package gateway

import (
	"time"

	"github.com/niparis/agent-net/internal/diagnostics"
)

func (g *Gateway) slowLoop(name string, start time.Time) {
	diagnostics.Slow(start, func(ms int64) { g.logLine("slow", "loop", name, "ms", ms) })
}

func (g *Gateway) logStepDispatchCount() {
	if count := g.stepDispatchRuns.Swap(0); count > 60 {
		g.logLine("step.dispatch_count", "level", "debug", "count", count)
	}
}

func (g *Gateway) slowRequest(method, node string, start time.Time) {
	diagnostics.Slow(start, func(ms int64) { g.logLine("slow", "method", method, "ms", ms, "node", node) })
}
