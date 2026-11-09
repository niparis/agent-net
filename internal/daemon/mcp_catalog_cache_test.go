package daemon

import (
	"context"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// catalogReads counts the mcp.catalog frames the gateway has handled since it
// started, from the gateway's own request counters.
func catalogReads(t *testing.T, g *testGateway, node string) int64 {
	t.Helper()
	out, err := g.gw.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: node})
	if err != nil {
		t.Fatalf("machine_get: %v", err)
	}
	if out.Requests == nil {
		return 0
	}
	var n int64
	for _, row := range out.Requests.Since.Top {
		if row.Method == "mcp.catalog" {
			n += row.Calls
		}
	}
	return n
}

// Sessions of one project share one kept catalog answer, so any number of
// tool-list reads while the catalog is unchanged costs the gateway one
// mcp.catalog. A catalog change drops the kept answer, and the next read is
// fresh.
func TestMCPCatalogReadsOnePerProject(t *testing.T) {
	w := newRefreshWorld(t, nodeOpts{cmdline: "pi"})
	alpha, _ := helloPi(t, w.work, "na", w.repos["alpha"], false)
	alpha2, _ := helloPi(t, w.work, "nb", w.repos["alpha"], false)

	before := catalogReads(t, w.g, w.work.d.NodeID())
	for i := 0; i < 20; i++ {
		if got := scopedCatalog(t, alpha); got != "probe" {
			t.Fatalf("read %d: %q", i, got)
		}
		if i%2 == 0 {
			if got := scopedCatalog(t, alpha2); got != "probe" {
				t.Fatalf("second session read %d: %q", i, got)
			}
		}
	}
	if got := catalogReads(t, w.g, w.work.d.NodeID()) - before; got != 1 {
		t.Fatalf("20 unchanged tool-list reads made %d gateway mcp.catalog calls, want 1", got)
	}

	// Scope probe to another project. The gateway pushes mcp.changed and the
	// next read must come from the gateway again, not from the kept answer.
	w.assignProbe(t, "beta")
	waitPush(t, alpha, "mcp_changed")
	after := catalogReads(t, w.g, w.work.d.NodeID())
	if got := scopedCatalog(t, alpha); got != "" {
		t.Fatalf("catalog after the change: %q, want none", got)
	}
	if got := catalogReads(t, w.g, w.work.d.NodeID()) - after; got != 1 {
		t.Fatalf("the first read after a change made %d gateway mcp.catalog calls, want 1", got)
	}
	// The fresh answer is kept again for the other session of the project.
	if got := scopedCatalog(t, alpha2); got != "" {
		t.Fatalf("second session after the change: %q, want none", got)
	}
	if got := catalogReads(t, w.g, w.work.d.NodeID()) - after; got != 1 {
		t.Fatalf("the next reads after the change made %d gateway mcp.catalog calls, want 1", got)
	}
}
