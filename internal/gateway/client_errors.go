package gateway

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/protocol"
)

// Client error counters. A daemon sends client.error when the TUI or the web
// console reports a request that failed or timed out on its side: the gateway
// never sees such a timeout, because the client gave up before an answer came.
// The counters sit next to the request counters (request_stats.go) and are
// read through agent_machine_get. They live in memory only, are counted per
// (method, origin) pair, and a pair past the same limit as the request
// counters is counted as method "other". A failure is rare next to a request,
// so one mutex is enough.

type clientErrorBucket struct {
	minute   int64
	errors   int64
	timeouts int64
	ns       int64
	maxNs    int64
}

type clientErrorCounter struct {
	errors, timeouts, ns, maxNs int64
	buckets                     [requestBuckets]clientErrorBucket
}

type clientErrorStats struct {
	mu   sync.Mutex
	keys map[requestKey]*clientErrorCounter
}

func newClientErrorStats() *clientErrorStats {
	return &clientErrorStats{keys: map[requestKey]*clientErrorCounter{}}
}

// clipText cuts s to at most n bytes on a character boundary.
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s
}

// record adds one client error. at is when the gateway heard of it.
func (c *clientErrorStats) record(origin string, e protocol.ClientError, at time.Time) {
	method := clipText(strings.TrimSpace(e.Method), protocol.ClientErrorMaxText)
	if method == "" {
		method = otherMethod
	}
	k := requestKey{method, protocol.CleanVia(origin)}
	ns := max(e.Ms, 0) * int64(time.Millisecond)
	minute := at.Unix() / 60

	c.mu.Lock()
	defer c.mu.Unlock()
	ctr := c.keys[k]
	if ctr == nil {
		if len(c.keys) >= maxRequestKeys {
			k.method = otherMethod
			ctr = c.keys[k]
		}
		if ctr == nil {
			ctr = &clientErrorCounter{}
			c.keys[k] = ctr
		}
	}
	ctr.errors++
	ctr.ns += ns
	ctr.maxNs = max(ctr.maxNs, ns)
	b := &ctr.buckets[minute%requestBuckets]
	if b.minute != minute {
		if b.minute > minute {
			return // a late report of a minute that is out of the window
		}
		*b = clientErrorBucket{minute: minute}
	}
	b.errors++
	b.ns += ns
	b.maxNs = max(b.maxNs, ns)
	if e.Timeout {
		ctr.timeouts++
		b.timeouts++
	}
}

// snapshot reads both windows, worst first. It is nil when nothing was ever
// reported, so agent_machine_get shows no empty block.
func (c *clientErrorStats) snapshot(now time.Time) *protocol.ClientErrorCounters {
	if c == nil {
		return nil
	}
	nowMinute := now.Unix() / 60
	var since, last []protocol.ClientErrorStat
	c.mu.Lock()
	for k, ctr := range c.keys {
		if ctr.errors > 0 {
			since = append(since, protocol.ClientErrorStat{Method: k.method, Origin: k.origin, Errors: ctr.errors,
				Timeouts: ctr.timeouts, TotalMs: ms(ctr.ns), MaxMs: ms(ctr.maxNs)})
		}
		var s protocol.ClientErrorStat
		var ns, maxNs int64
		for i := range ctr.buckets {
			b := &ctr.buckets[i]
			if b.minute > nowMinute-requestBuckets && b.minute <= nowMinute {
				s.Errors += b.errors
				s.Timeouts += b.timeouts
				ns += b.ns
				maxNs = max(maxNs, b.maxNs)
			}
		}
		if s.Errors > 0 {
			s.Method, s.Origin, s.TotalMs, s.MaxMs = k.method, k.origin, ms(ns), ms(maxNs)
			last = append(last, s)
		}
	}
	c.mu.Unlock()
	if len(since) == 0 {
		return nil
	}
	sortClientErrors(since)
	sortClientErrors(last)
	if last == nil {
		last = []protocol.ClientErrorStat{}
	}
	return &protocol.ClientErrorCounters{Since: since, Last10Min: last}
}

func sortClientErrors(rows []protocol.ClientErrorStat) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Errors != b.Errors {
			return a.Errors > b.Errors
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		return a.Origin < b.Origin
	})
}

// handleClientError counts one reported failure. The origin is the label the
// daemon put on the frame (Header.Via).
func (g *Gateway) handleClientError(raw []byte) (any, error) {
	var in struct {
		Via string `json:"via"`
		protocol.ClientError
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil, reject("bad client.error")
	}
	if g.clientErrs != nil {
		g.clientErrs.record(in.Via, in.ClientError, time.Now())
	}
	return map[string]bool{"ok": true}, nil
}
