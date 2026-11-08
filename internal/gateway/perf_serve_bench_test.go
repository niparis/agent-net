package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// These benchmarks measure the handlers the way the gateway runs them: one
// frame through g.handle. They run against a copy of a real database (see
// benchRealGateway) and skip when AGENT_NET_BENCH_DB is unset.

// benchOnlineNode returns the node that has the most online sessions, with the
// sessions it would report in a session.sync.
func benchOnlineNode(b *testing.B, g *Gateway) (string, []protocol.AgentView, []protocol.SessionInfo) {
	b.Helper()
	all, err := g.store.Agents(protocol.QueryAgents{OnlineOnly: true})
	if err != nil || len(all) == 0 {
		b.Skip("no online agents")
	}
	byNode := map[string][]protocol.AgentView{}
	for _, a := range all {
		if a.Harness == "cto" || a.Harness == protocol.OwnerHarness {
			continue
		}
		byNode[a.NodeID] = append(byNode[a.NodeID], a)
	}
	best := ""
	for n, list := range byNode {
		if best == "" || len(list) > len(byNode[best]) {
			best = n
		}
	}
	if best == "" {
		b.Skip("no online agent sessions")
	}
	var infos []protocol.SessionInfo
	for _, a := range byNode[best] {
		infos = append(infos, protocol.SessionInfo{SessionID: a.SessionID, Harness: a.Harness, NativeID: a.NativeID,
			PID: a.PID, Cwd: a.Cwd, Name: a.Profile.Name, Status: a.Status, Mode: a.Mode, Model: a.Model,
			Repo: a.Repo, Worker: a.Worker})
	}
	return best, byNode[best], infos
}

func BenchmarkRealHandlePolicyGet(b *testing.B) {
	g := benchRealGateway(b)
	node, agents, _ := benchOnlineNode(b, g)
	raw, err := json.Marshal(protocol.PolicyGet{SessionID: agents[0].SessionID, Turn: true})
	if err != nil {
		b.Fatal(err)
	}
	nc := g.conns[node]
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.handle(ctx, nc, "policy.get", raw); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRealHandleQueryAgents(b *testing.B) {
	g := benchRealGateway(b)
	node, _, _ := benchOnlineNode(b, g)
	raw, err := json.Marshal(protocol.QueryAgents{})
	if err != nil {
		b.Fatal(err)
	}
	nc := g.conns[node]
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.handle(ctx, nc, "query.agents", raw); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRealHandleSessionSync(b *testing.B) {
	g := benchRealGateway(b)
	node, _, infos := benchOnlineNode(b, g)
	raw, err := json.Marshal(protocol.SessionSync{Sessions: infos})
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("sessions in the sync: %d", len(infos))
	nc := g.conns[node]
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.handle(ctx, nc, "session.sync", raw); err != nil {
			b.Fatal(err)
		}
		g.standingWG.Wait()
	}
}

func BenchmarkRealHandleSessionUpsert(b *testing.B) {
	g := benchRealGateway(b)
	node, _, infos := benchOnlineNode(b, g)
	raw, err := json.Marshal(protocol.SessionUpsert{Session: infos[0]})
	if err != nil {
		b.Fatal(err)
	}
	nc := g.conns[node]
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.handle(ctx, nc, "session.upsert", raw); err != nil {
			b.Fatal(err)
		}
		g.standingWG.Wait()
	}
}

func BenchmarkRealHandleQueryProjects(b *testing.B) {
	g := benchRealGateway(b)
	node, _, _ := benchOnlineNode(b, g)
	raw, _ := json.Marshal(protocol.QueryProjects{})
	nc := g.conns[node]
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.handle(ctx, nc, "query.projects", raw); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRealDispatchStepTasks(b *testing.B) {
	g := benchRealGateway(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.dispatchStepTasks(ctx)
	}
}
