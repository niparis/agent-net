package gateway

import (
	"os"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// These benchmarks run against a copy of a real gateway database. Make the
// copy with sqlite3 ".backup" and set AGENT_NET_BENCH_DB to its path. They
// skip when the variable is unset, so CI never runs them. The copy is opened
// like the gateway opens its own file, so its migrations may write to it:
// never point this at the real database.
func benchRealGateway(b *testing.B) *Gateway {
	b.Helper()
	path := os.Getenv("AGENT_NET_BENCH_DB")
	if path == "" {
		b.Skip("AGENT_NET_BENCH_DB is not set")
	}
	if _, err := os.Stat(path); err != nil {
		b.Skip("no database copy at " + path)
	}
	s, err := OpenStore(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	nodes, err := s.Nodes()
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range nodes {
		g.conns[n.NodeID] = &nodeConn{id: n.NodeID}
	}
	return g
}

func BenchmarkRealListInbox(b *testing.B) {
	g := benchRealGateway(b)
	items, err := g.store.ListInbox()
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("inbox items: %d", len(items))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.store.ListInbox(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRealNodesInfo(b *testing.B) {
	g := benchRealGateway(b)
	info, err := g.NodesInfo()
	if err != nil {
		b.Fatal(err)
	}
	picks := 0
	for _, n := range info.Nodes {
		picks += len(n.QualityPicks)
	}
	b.Logf("nodes: %d, quality picks: %d", len(info.Nodes), picks)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.NodesInfo(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRealWorkerProfileList(b *testing.B) {
	g := benchRealGateway(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.workerProfileAction("n", "worker.profile.list", protocol.WorkerProfileFrame{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRealGraph(b *testing.B) {
	g := benchRealGateway(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.store.Graph(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRealAgents(b *testing.B) {
	g := benchRealGateway(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.store.Agents(protocol.QueryAgents{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRealAgent(b *testing.B) {
	g := benchRealGateway(b)
	all, err := g.store.Agents(protocol.QueryAgents{OnlineOnly: true})
	if err != nil || len(all) == 0 {
		b.Skip("no online agents")
	}
	id := all[0].SessionID
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := g.store.Agent(id); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRealSettingsSnapshot(b *testing.B) {
	g := benchRealGateway(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.store.SettingsSnapshot(); err != nil {
			b.Fatal(err)
		}
	}
}
