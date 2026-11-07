package gateway

import (
	"strings"
	"testing"
	"time"
)

func TestSlowStepDispatchCount(t *testing.T) {
	g, logs := autoCutoverGateway(t, time.Now())
	g.stepDispatchRuns.Store(60)
	g.logStepDispatchCount()
	if strings.Contains(logs.String(), "step.dispatch_count") {
		t.Fatal(logs.String())
	}
	logs.Reset()
	g.stepDispatchRuns.Store(61)
	g.logStepDispatchCount()
	if !strings.Contains(logs.String(), "event=step.dispatch_count level=debug count=61") {
		t.Fatal(logs.String())
	}
	if g.stepDispatchRuns.Load() != 0 {
		t.Fatal("count not reset")
	}
}

func TestSlowRequestAndLoopLog(t *testing.T) {
	g, logs := autoCutoverGateway(t, time.Now())
	logs.Reset()
	g.slowRequest("graph.view", "test-node", time.Now().Add(-250*time.Millisecond))
	if strings.Count(logs.String(), "event=slow") != 1 || !strings.Contains(logs.String(), "method=graph.view ms=") || !strings.Contains(logs.String(), "node=test-node") {
		t.Fatal(logs.String())
	}
	logs.Reset()
	g.slowLoop("expiry", time.Now().Add(-250*time.Millisecond))
	if strings.Count(logs.String(), "event=slow") != 1 || !strings.Contains(logs.String(), "loop=expiry ms=") {
		t.Fatal(logs.String())
	}
}
