package gateway

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// BenchmarkRealPoolLatency measures how long reads wait while a writer runs,
// on a copy of the real database (AGENT_NET_BENCH_DB, see benchRealGateway).
// Four reader goroutines cycle through query.agents, learned.list and graph.get
// as the TUI and the CTO do; one writer reports session.upsert all the time.
// Each call is timed on the wall clock, because the live problem was waiting,
// not CPU. It reports the median and the 95th percentile of each call.
//
// Settings, all read from the environment so one test binary can run every
// variant one after the other:
//
//	AGENT_NET_BENCH_POOL=0      read pool off: one connection for everything;
//	                            a number N sets the most read connections
//	AGENT_NET_BENCH_NOCACHE=1   read cache off: every read goes to the database
//	AGENT_NET_BENCH_SECONDS=8   how long to run
//	AGENT_NET_BENCH_READERS=4   reader goroutines
//	AGENT_NET_BENCH_WRITE_MS=5  pause between two session.upsert calls
//	AGENT_NET_BENCH_READ_MS=1   pause between two calls of one reader
//
// Run it with -benchtime 1x: the run ends on time, not on b.N.
func BenchmarkRealPoolLatency(b *testing.B) {
	if v, err := strconv.Atoi(os.Getenv("AGENT_NET_BENCH_POOL")); err == nil && v >= 0 {
		old := readPoolMax
		readPoolMax = v
		defer func() { readPoolMax = old }()
	}
	g := benchRealGateway(b)
	if os.Getenv("AGENT_NET_BENCH_NOCACHE") != "" {
		g.store.connector = nil
	}
	node, agents, infos := benchOnlineNode(b, g)
	nc := &nodeConn{id: node}
	nc.graphRefs.Store(true)
	nc.sessionAgents.Store(true)

	// learned.list needs the owner's session.
	ownerNC, ownerID := nc, agents[0].SessionID
	if all, err := g.store.Agents(protocol.QueryAgents{OnlineOnly: true}); err == nil {
		for _, a := range all {
			if a.Harness == protocol.OwnerHarness {
				ownerNC, ownerID = &nodeConn{id: a.NodeID}, a.SessionID
				break
			}
		}
	}

	seconds := benchEnvInt("AGENT_NET_BENCH_SECONDS", 8)
	readers := benchEnvInt("AGENT_NET_BENCH_READERS", 4)
	pause := time.Duration(benchEnvInt("AGENT_NET_BENCH_WRITE_MS", 5)) * time.Millisecond
	readPause := time.Duration(benchEnvInt("AGENT_NET_BENCH_READ_MS", 1)) * time.Millisecond

	call := func(typ string, v any) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = g.handle(context.Background(), nc, typ, raw)
		return err
	}
	reads := []struct {
		name string
		run  func() error
	}{
		{"query.agents", func() error { return call("query.agents", protocol.QueryAgents{}) }},
		{"learned.list", func() error {
			g.validToolOwner(ownerNC, ownerID)
			_, err := g.store.OwnerLearned()
			return err
		}},
		{"graph.get", func() error { return call("graph.get", protocol.QueryGraph{}) }},
	}

	// Warm up like the live gateway is warm.
	for _, r := range reads {
		if err := r.run(); err != nil {
			b.Fatalf("%s: %v", r.name, err)
		}
	}

	type sample struct {
		name string
		d    time.Duration
	}
	var mu sync.Mutex
	var all []sample
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var mine []sample
			for n := i; ; n++ {
				select {
				case <-stop:
					mu.Lock()
					all = append(all, mine...)
					mu.Unlock()
					return
				default:
				}
				r := reads[n%len(reads)]
				start := time.Now()
				if err := r.run(); err != nil {
					b.Errorf("%s: %v", r.name, err)
					return
				}
				mine = append(mine, sample{r.name, time.Since(start)})
				time.Sleep(readPause)
			}
		}(i)
	}
	var writes []time.Duration
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; ; n++ {
			select {
			case <-stop:
				return
			default:
			}
			si := infos[n%len(infos)]
			si.Status = []string{"idle", "busy"}[n%2]
			start := time.Now()
			if err := call("session.upsert", protocol.SessionUpsert{Session: si}); err != nil {
				b.Errorf("session.upsert: %v", err)
				return
			}
			writes = append(writes, time.Since(start))
			time.Sleep(pause)
		}
	}()

	b.ResetTimer()
	time.Sleep(time.Duration(seconds) * time.Second)
	close(stop)
	wg.Wait()
	b.StopTimer()
	g.standingWG.Wait()

	report := func(name string, ds []time.Duration) {
		if len(ds) == 0 {
			return
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		p50, p95 := ds[len(ds)/2], ds[len(ds)*95/100]
		b.ReportMetric(float64(p50.Microseconds())/1000, name+"-p50-ms")
		b.ReportMetric(float64(p95.Microseconds())/1000, name+"-p95-ms")
		b.Logf("%-14s n=%-6d p50 %8.2f ms  p95 %8.2f ms  max %8.2f ms", name, len(ds),
			float64(p50.Microseconds())/1000, float64(p95.Microseconds())/1000, float64(ds[len(ds)-1].Microseconds())/1000)
	}
	by := map[string][]time.Duration{}
	for _, s := range all {
		by[s.name] = append(by[s.name], s.d)
	}
	for _, r := range reads {
		report(r.name, by[r.name])
	}
	report("session.upsert", writes)
}

func benchEnvInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v >= 0 {
		return v
	}
	return def
}
