package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// toolsWire is a fake gateway for the tools loop: it hands out targets and
// counts what the daemon asks and reports.
type toolsWire struct {
	mu      sync.Mutex
	targets []protocol.ToolTarget
	exports int
	reports []protocol.ToolResult
}

func (w *toolsWire) hook(_ context.Context, method string, params any) (json.RawMessage, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch method {
	case "tools.targets":
		raw, _ := json.Marshal(w.targets)
		return raw, true, nil
	case "tools.export":
		w.exports++
		raw, _ := json.Marshal(protocol.SkillBundle{})
		return raw, true, nil
	case "tools.report":
		w.reports = append(w.reports, params.(protocol.ToolResult))
		return nil, true, nil
	case "query.projects":
		return json.RawMessage(`{"projects":[]}`), true, nil
	}
	return nil, false, nil
}

func (w *toolsWire) counts() (exports, reports int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.exports, len(w.reports)
}

func toolsCopyTarget(version string) protocol.ToolTarget {
	return protocol.ToolTarget{
		Name:     "demo",
		Revision: protocol.ToolRevision{Item: `["skill","demo","abc"]`, Revision: 1, Version: version, Assignment: protocol.ToolAssignment{Scope: "global"}},
		Target:   protocol.ToolPlanTarget{Action: "copy", Harness: "claude"},
	}
}

func TestReconcileToolsSendsNothingForUnchangedTargets(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	wire := &toolsWire{targets: []protocol.ToolTarget{toolsCopyTarget("v1")}}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.testGatewayCall = wire.hook }})
	ctx := context.Background()
	n.d.reconcileTools(ctx)
	exports, reports := wire.counts()
	if exports != 1 || reports != 1 {
		t.Fatalf("first run: %d exports, %d reports, want 1 and 1", exports, reports)
	}
	// Wakes that are not full checks find the same version: no export, no report.
	for i := 0; i < 5; i++ {
		n.d.reconcileTools(ctx)
	}
	if e, r := wire.counts(); e != exports || r != reports {
		t.Fatalf("5 unchanged runs: %d exports and %d reports more, want none", e-exports, r-reports)
	}
	// A full check looks at the destination again (a skill removed by hand is
	// put back), but the same result is not reported twice.
	n.d.toolsFullNext.Store(true)
	n.d.reconcileTools(ctx)
	if e, r := wire.counts(); e != exports+1 || r != reports {
		t.Fatalf("full check: %d exports and %d reports more, want 1 and 0", e-exports, r-reports)
	}
}

func TestReconcileToolsReportsAChangedTarget(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	wire := &toolsWire{targets: []protocol.ToolTarget{toolsCopyTarget("v1")}}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.testGatewayCall = wire.hook }})
	ctx := context.Background()
	n.d.reconcileTools(ctx)
	exports, reports := wire.counts()
	wire.mu.Lock()
	wire.targets = []protocol.ToolTarget{toolsCopyTarget("v2")}
	wire.mu.Unlock()
	n.d.reconcileTools(ctx)
	if e, r := wire.counts(); e != exports+1 || r != reports+1 {
		t.Fatalf("a new version: %d exports and %d reports more, want 1 and 1", e-exports, r-reports)
	}
}

// A new link has no results yet: they are all sent again.
func TestReconcileToolsReportsAgainOnANewLink(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	wire := &toolsWire{targets: []protocol.ToolTarget{toolsCopyTarget("v1")}}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.testGatewayCall = wire.hook }})
	ctx := context.Background()
	n.d.reconcileTools(ctx)
	_, reports := wire.counts()
	n.d.mu.Lock()
	n.d.linkEpoch++
	n.d.mu.Unlock()
	n.d.reconcileTools(ctx)
	if _, r := wire.counts(); r != reports+1 {
		t.Fatalf("a new link: %d reports more, want 1", r-reports)
	}
}
