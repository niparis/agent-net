package gateway

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Request counters. The gateway counts every request frame it handles by
// (method, origin): how many, the total and longest handling time, and the
// bytes of the responses. The origin is the label the daemon puts on the frame
// (protocol.Via*), so the owner can see how much of the work comes from the
// TUI's polling, and how much from agents, workers and the daemons' own loops.
//
// The counters live in memory only: no database write, no log line, no limit
// on any request. A record is a few atomic adds and takes no lock. They are
// read through agent_machine_get (Requests), as totals since the gateway
// started and as the last ten minutes, kept in ten buckets of one minute.

const (
	// maxRequestKeys bounds the (method, origin) pairs. A node can send any
	// frame type, so a pair past the limit is counted as method "other".
	maxRequestKeys = 1000
	requestBuckets = 10
	requestTop     = 30
	otherMethod    = "other"
)

type requestKey struct{ method, origin string }

// requestBucket holds one minute of a pair. minute is the unix minute it
// holds; a bucket of an older minute is reset when its slot is used again.
type requestBucket struct {
	minute atomic.Int64
	calls  atomic.Int64
	ns     atomic.Int64
	maxNs  atomic.Int64
	bytes  atomic.Int64
}

type requestCounter struct {
	calls   atomic.Int64
	ns      atomic.Int64
	maxNs   atomic.Int64
	bytes   atomic.Int64
	roll    sync.Mutex // only when a bucket changes minute
	buckets [requestBuckets]requestBucket
}

type requestStats struct {
	started time.Time
	// keys is replaced as a whole when a pair is added, so a record finds its
	// counter without a lock. Pairs are few and added rarely.
	keys  atomic.Pointer[map[requestKey]*requestCounter]
	addMu sync.Mutex
}

func newRequestStats(started time.Time) *requestStats {
	r := &requestStats{started: started}
	m := map[requestKey]*requestCounter{}
	r.keys.Store(&m)
	return r
}

func atomicMax(a *atomic.Int64, v int64) {
	for {
		c := a.Load()
		if v <= c || a.CompareAndSwap(c, v) {
			return
		}
	}
}

// counter finds or adds the counter of a pair.
func (r *requestStats) counter(k requestKey) *requestCounter {
	if c := (*r.keys.Load())[k]; c != nil {
		return c
	}
	r.addMu.Lock()
	defer r.addMu.Unlock()
	cur := *r.keys.Load()
	if c := cur[k]; c != nil {
		return c
	}
	if len(cur) >= maxRequestKeys {
		k.method = otherMethod
		if c := cur[k]; c != nil {
			return c
		}
	}
	next := make(map[requestKey]*requestCounter, len(cur)+1)
	for key, c := range cur {
		next[key] = c
	}
	c := &requestCounter{}
	next[k] = c
	r.keys.Store(&next)
	return c
}

// record adds one handled request. at is when it ended.
func (r *requestStats) record(method, origin string, at time.Time, took time.Duration, respBytes int) {
	c := r.counter(requestKey{method, protocol.CleanVia(origin)})
	ns, n := int64(took), int64(respBytes)
	c.calls.Add(1)
	c.ns.Add(ns)
	c.bytes.Add(n)
	atomicMax(&c.maxNs, ns)

	minute := at.Unix() / 60
	b := &c.buckets[minute%requestBuckets]
	if b.minute.Load() != minute {
		c.roll.Lock()
		if cur := b.minute.Load(); cur != minute {
			if cur > minute {
				// A late record of a minute that is out of the window.
				c.roll.Unlock()
				return
			}
			b.calls.Store(0)
			b.ns.Store(0)
			b.maxNs.Store(0)
			b.bytes.Store(0)
			b.minute.Store(minute)
		}
		c.roll.Unlock()
	}
	b.calls.Add(1)
	b.ns.Add(ns)
	b.bytes.Add(n)
	atomicMax(&b.maxNs, ns)
}

func ms(ns int64) float64 { return float64(ns) / 1e6 }

// snapshot reads both windows.
func (r *requestStats) snapshot(now time.Time) protocol.GatewayRequests {
	nowMinute := now.Unix() / 60
	var since, last []protocol.RequestStat
	for k, c := range *r.keys.Load() {
		if n := c.calls.Load(); n > 0 {
			since = append(since, protocol.RequestStat{Method: k.method, Origin: k.origin, Calls: n,
				TotalMs: ms(c.ns.Load()), MaxMs: ms(c.maxNs.Load()), Bytes: c.bytes.Load()})
		}
		var s protocol.RequestStat
		var maxNs, ns int64
		for i := range c.buckets {
			b := &c.buckets[i]
			if m := b.minute.Load(); m > nowMinute-requestBuckets && m <= nowMinute {
				s.Calls += b.calls.Load()
				ns += b.ns.Load()
				s.Bytes += b.bytes.Load()
				maxNs = max(maxNs, b.maxNs.Load())
			}
		}
		if s.Calls > 0 {
			s.Method, s.Origin, s.TotalMs, s.MaxMs = k.method, k.origin, ms(ns), ms(maxNs)
			last = append(last, s)
		}
	}
	elapsed := max(int(now.Sub(r.started)/time.Second), 0)
	return protocol.GatewayRequests{
		StartedAt: r.started.UTC().Format(time.RFC3339),
		Since:     requestWindow(since, elapsed),
		Last10Min: requestWindow(last, min(elapsed, requestBuckets*60)),
	}
}

func requestWindow(rows []protocol.RequestStat, seconds int) protocol.RequestWindow {
	w := protocol.RequestWindow{Seconds: seconds, ByOrigin: []protocol.RequestOriginTotal{}, Top: []protocol.RequestStat{}}
	byOrigin := map[string]*protocol.RequestOriginTotal{}
	for _, s := range rows {
		w.Calls += s.Calls
		w.TotalMs += s.TotalMs
		w.Bytes += s.Bytes
		o := byOrigin[s.Origin]
		if o == nil {
			o = &protocol.RequestOriginTotal{Origin: s.Origin}
			byOrigin[s.Origin] = o
		}
		o.Calls += s.Calls
		o.TotalMs += s.TotalMs
		o.Bytes += s.Bytes
	}
	for _, o := range byOrigin {
		w.ByOrigin = append(w.ByOrigin, *o)
	}
	sort.Slice(w.ByOrigin, func(i, j int) bool {
		a, b := w.ByOrigin[i], w.ByOrigin[j]
		if a.TotalMs != b.TotalMs {
			return a.TotalMs > b.TotalMs
		}
		return a.Origin < b.Origin
	})
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.TotalMs != b.TotalMs {
			return a.TotalMs > b.TotalMs
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		return a.Origin < b.Origin
	})
	w.Top = append(w.Top, rows[:min(len(rows), requestTop)]...)
	return w
}
