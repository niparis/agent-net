package daemon

import (
	"context"
	"encoding/json"
	"strings"
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

// catalogNames lists the server names a session's tool-list read returns.
func catalogNames(t *testing.T, lc *localClient) string {
	t.Helper()
	var r struct {
		Servers []protocol.MCPServer `json:"servers"`
	}
	if err := json.Unmarshal(lc.mustCall("mcp_tools", nil), &r); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range r.Servers {
		names = append(names, s.Name)
	}
	return strings.Join(names, ",")
}

// Every session shares one kept catalog answer, so any number of tool-list
// reads while the catalog is unchanged costs the gateway one mcp.catalog. A
// catalog change drops the kept answer, and the next read is fresh.
func TestMCPCatalogIsReadOnceUntilItChanges(t *testing.T) {
	useHelper(t)
	g := startGateway(t, t.TempDir(), "")
	host := startNode(t, g, nodeOpts{cmdline: "pi"})
	work := startNode(t, g, nodeOpts{cmdline: "pi"})
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	cliClient(t, host).mustCall("mcp_add", protocol.MCPAdd{Server: stdioSpec("probe")})
	first, _ := helloPi(t, work, "na", repo, false)
	second, _ := helloPi(t, work, "nb", repo, false)

	before := catalogReads(t, g, work.d.NodeID())
	for i := 0; i < 20; i++ {
		if got := catalogNames(t, first); got != "probe" {
			t.Fatalf("read %d: %q", i, got)
		}
		if got := catalogNames(t, second); got != "probe" {
			t.Fatalf("second session read %d: %q", i, got)
		}
	}
	if got := catalogReads(t, g, work.d.NodeID()) - before; got != 1 {
		t.Fatalf("40 unchanged tool-list reads made %d gateway mcp.catalog calls, want 1", got)
	}

	// Disable the server. The gateway pushes mcp.changed and the next read
	// must come from the gateway again, not from the kept answer.
	cliClient(t, host).mustCall("mcp_enable", protocol.MCPSetEnabled{Name: "probe", Enabled: false})
	waitPush(t, first, "mcp_changed")
	after := catalogReads(t, g, work.d.NodeID())
	if got := catalogNames(t, first); got != "" {
		t.Fatalf("catalog after the change: %q, want none", got)
	}
	if got := catalogNames(t, second); got != "" {
		t.Fatalf("second session after the change: %q, want none", got)
	}
	if got := catalogReads(t, g, work.d.NodeID()) - after; got != 1 {
		t.Fatalf("the reads after a change made %d gateway mcp.catalog calls, want 1", got)
	}
}
