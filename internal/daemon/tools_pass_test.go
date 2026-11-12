package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// toolsWire is a fake gateway for the tools loop: it hands out targets and
// counts what the daemon asks and reports.
type toolsWire struct {
	mu      sync.Mutex
	targets []protocol.ToolTarget
	exports int
	bundle  []byte // what tools.export returns; empty by default
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
		raw, _ := json.Marshal(protocol.SkillBundle{Data: w.bundle})
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
}

// A full check looks at every destination again, so a skill removed by hand is
// put back. The stored bundle is named by its hash and never changes, so the
// check must not fetch it again from the gateway: this was about 100
// tools.export calls a minute on a machine with a few hundred copy targets.
func TestReconcileToolsFullCheckDoesNotFetchAKnownBundle(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := inventory.SkillHash(source)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := inventory.PackSkill(source)
	if err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	wire := &toolsWire{bundle: bundle, targets: []protocol.ToolTarget{toolsCopyTarget(hash)}}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) {
		c.testGatewayCall = wire.hook
		c.InventoryHome = home
	}})
	ctx := context.Background()
	// InventoryHome starts the daemon's own tools loop, which makes the first
	// run. Wait for its report instead of racing it.
	dest := filepath.Join(home, ".claude", "skills", "demo", "SKILL.md")
	eventually(t, "first run reports", func() bool { _, r := wire.counts(); return r >= 1 })
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("first run did not install the skill: %v", err)
	}
	exports, _ := wire.counts()
	if exports < 1 {
		t.Fatal("first run did not fetch the bundle")
	}
	for i := 0; i < 5; i++ {
		n.d.toolsFullNext.Store(true)
		n.d.reconcileTools(ctx)
	}
	if e, _ := wire.counts(); e != exports {
		t.Fatalf("5 full checks: %d exports more, want none", e-exports)
	}
	// The check still repairs: a skill deleted by hand comes back.
	if err := os.RemoveAll(filepath.Dir(dest)); err != nil {
		t.Fatal(err)
	}
	n.d.toolsFullNext.Store(true)
	n.d.reconcileTools(ctx)
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("full check did not put the skill back: %v", err)
	}
	if e, _ := wire.counts(); e != exports {
		t.Fatalf("restoring from the known bundle fetched %d more", e-exports)
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

// The gateway keeps reported results in its database, not on the link. A
// reconnect alone reports nothing again; a report that failed is sent again on
// the next run, on whatever link that is.
func TestReconcileToolsKeepsReportsAcrossAReconnect(t *testing.T) {
	dir := t.TempDir()
	g := startGateway(t, dir, "")
	wire := &toolsWire{targets: []protocol.ToolTarget{toolsCopyTarget("v1")}}
	failed := false
	failFirstReport := func(ctx context.Context, method string, params any) (json.RawMessage, bool, error) {
		raw, handled, err := wire.hook(ctx, method, params)
		if method == "tools.report" && !failed {
			failed = true // reconcileTools runs on this goroutine only
			return nil, true, errNotConnected
		}
		return raw, handled, err
	}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.testGatewayCall = failFirstReport }})
	ctx := context.Background()
	n.d.reconcileTools(ctx)
	n.d.reconcileTools(ctx)
	if _, r := wire.counts(); r != 2 {
		t.Fatalf("a failed report and its retry: %d tools.report calls, want 2", r)
	}
	addr := g.addr
	g.stop()
	eventually(t, "the link drops", func() bool { return !n.d.Connected() })
	startGateway(t, dir, addr)
	eventually(t, "the link is back", n.d.Connected)
	n.d.mu.Lock()
	epoch := n.d.linkEpoch
	n.d.mu.Unlock()
	if epoch < 2 {
		t.Fatalf("link epoch %d after a reconnect, want at least 2", epoch)
	}
	n.d.reconcileTools(ctx)
	if _, r := wire.counts(); r != 2 {
		t.Fatalf("a reconnect with the same result: %d tools.report calls more, want 0", r-2)
	}
}
