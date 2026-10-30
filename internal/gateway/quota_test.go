package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestNodesInfoCarriesQuotas(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("mac", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeHello(node, "mac", "v1"); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s}
	q := &protocol.Quotas{Providers: []protocol.QuotaProvider{{
		Provider: "claude",
		Windows:  []protocol.QuotaWindow{{ID: "seven_day", Label: "week", Kind: "weekly", PercentUsed: 59}},
	}}}
	g.usage.set(node, c.now(), protocol.UsageReport{Quotas: q})
	info, err := g.NodesInfo()
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Nodes) != 1 || info.Nodes[0].Quotas == nil {
		t.Fatalf("nodes info: %+v", info)
	}
	if got := info.Nodes[0].Quotas.Providers[0].Windows[0].PercentUsed; got != 59 {
		t.Errorf("percent %v", got)
	}
	// A stale report is not offered to agents.
	c.t = c.t.Add(protocol.UsageStaleSeconds*time.Second + time.Second)
	info, _ = g.NodesInfo()
	if len(info.Nodes) != 1 || info.Nodes[0].Quotas != nil {
		t.Errorf("stale quotas: %+v", info.Nodes[0].Quotas)
	}
}

func TestCleanQuotaError(t *testing.T) {
	r := cleanUsage(protocol.UsageReport{QuotaError: strings.Repeat("x", 400)})
	if len(r.QuotaError) > 300 || r.QuotaError == "" {
		t.Fatalf("error length %d", len(r.QuotaError))
	}
}
