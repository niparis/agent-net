package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// liveMix runs a mix like the one the live gateway sees (profile of
// 2026-11-08): the TUI and the CTO read the agent list, the worker log, the
// nodes, the graph and the inbox, and every prompt checks who owns a session,
// while sessions report activity (session.upsert) and messages are
// acknowledged (delivery.update).
//
// One round is: a session.upsert, a message acknowledgement, then readsPerWrite
// times the six reads. The clock of the store moves by step for each read
// pass, so the reads that depend on the time (the agent list is kept for one
// second) see time pass as they do live.
type liveMix struct {
	g        *Gateway
	node     string
	nc       *nodeConn
	sessions []protocol.SessionInfo
	owner    string // a session id to check
	msgs     []string
	clock    time.Time
	spent    map[string]time.Duration // time in each kind of call, this run
}

const liveMixStep = 250 * time.Millisecond

// cpuTime is the CPU time this process has used. A busy host stretches the wall
// clock of a benchmark but not its CPU time, which is what the gateway spends
// all day.
func cpuTime() time.Duration {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func (m *liveMix) handle(b *testing.B, typ string, v any) {
	b.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		b.Fatal(err)
	}
	start := time.Now()
	if _, err := m.g.handle(context.Background(), m.nc, typ, raw); err != nil {
		b.Fatalf("%s: %v", typ, err)
	}
	m.g.standingWG.Wait()
	m.spent[typ] += time.Since(start)
}

func (m *liveMix) round(b *testing.B, i, readsPerWrite int) {
	b.Helper()
	// Writes: one session reports a new status, one message is acknowledged.
	si := m.sessions[i%len(m.sessions)]
	si.Status = []string{"idle", "busy"}[i%2]
	m.handle(b, "session.upsert", protocol.SessionUpsert{Session: si})
	if len(m.msgs) > 0 {
		state := []string{protocol.StateDispatched, protocol.StateDelivered}[i%2]
		if _, _, err := m.g.store.ApplyDeliveryReport(m.msgs[i%len(m.msgs)], state, "bench"); err != nil {
			b.Fatal(err)
		}
	}
	for r := 0; r < readsPerWrite; r++ {
		m.clock = m.clock.Add(liveMixStep)
		m.g.store.now = func() time.Time { return m.clock }
		m.handle(b, "query.agents", protocol.QueryAgents{})
		m.handle(b, "query.agents", protocol.QueryAgents{OnlineOnly: true})
		m.handle(b, "query.worker_log", protocol.WorkerLogQuery{Limit: 200})
		m.handle(b, "query.nodes", struct{}{})
		m.handle(b, "graph.get", protocol.QueryGraph{})
		m.handle(b, "inbox.list", struct{}{})
		start := time.Now()
		m.g.validToolOwner(m.nc, m.owner)
		m.spent["validToolOwner"] += time.Since(start)
	}
}

func runLiveMix(b *testing.B, m *liveMix) {
	for _, reads := range []int{1, 4} {
		b.Run(fmt.Sprintf("reads%d", reads), func(b *testing.B) {
			// Warm up outside the timer, as the live gateway is warm.
			m.round(b, 0, reads)
			m.spent = map[string]time.Duration{}
			b.ResetTimer()
			cpu := cpuTime()
			for i := 0; i < b.N; i++ {
				m.round(b, i, reads)
			}
			cpu = cpuTime() - cpu
			b.StopTimer()
			b.ReportMetric(float64(cpu.Nanoseconds())/float64(b.N), "cpu-ns/round")
			// Where the time of a round went, for each kind of call.
			for typ, d := range m.spent {
				b.ReportMetric(float64(d.Nanoseconds())/float64(b.N), typ+"-ns/round")
			}
		})
	}
}

// BenchmarkFixtureLiveMix needs no real database:
//
//	go test ./internal/gateway -run XXX -bench FixtureLiveMix -benchtime 30x
func BenchmarkFixtureLiveMix(b *testing.B) {
	f := newBenchFixture(b)
	node := f.nodes[0]
	for _, n := range f.nodes {
		if len(f.sessions[n]) > len(f.sessions[node]) {
			node = n
		}
	}
	m := &liveMix{g: f.g, node: node, nc: f.g.conns[node], sessions: f.sessions[node], owner: f.ids[0], clock: time.Now(), spent: map[string]time.Duration{}}
	// Messages between sessions, to acknowledge.
	for i := 0; i+1 < len(f.ids) && len(m.msgs) < 20; i++ {
		msg, _, err := f.g.store.StoreMessage(protocol.MsgSend{Sender: f.ids[i], Recipient: f.ids[i+1], Body: "bench"})
		if err != nil {
			b.Fatal(err)
		}
		m.msgs = append(m.msgs, msg.ID)
	}
	runLiveMix(b, m)
}

// BenchmarkRealLiveMix runs the same mix against a copy of the real database
// (AGENT_NET_BENCH_DB, see benchRealGateway).
func BenchmarkRealLiveMix(b *testing.B) {
	g := benchRealGateway(b)
	node, agents, infos := benchOnlineNode(b, g)
	nc := &nodeConn{id: node}
	// The live nodes report typed Graph support in hello.
	nc.graphRefs.Store(true)
	nc.sessionAgents.Store(true)
	m := &liveMix{g: g, node: node, nc: nc, sessions: infos, owner: agents[0].SessionID, clock: time.Now(), spent: map[string]time.Duration{}}
	rows, err := g.store.db.Query(`SELECT id FROM messages WHERE state IN ('stored','dispatched','delivered') ORDER BY rowid DESC LIMIT 50`)
	if err != nil {
		b.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			b.Fatal(err)
		}
		m.msgs = append(m.msgs, id)
	}
	rows.Close()
	b.Logf("sessions %d, messages %d", len(m.sessions), len(m.msgs))
	runLiveMix(b, m)
}
