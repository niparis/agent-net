package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// This file holds the owner-data frames (decision 0007):
//
//   - node.usage: a daemon's CPU and memory summary. The gateway keeps the
//     newest one per machine in memory only. It is a live reading, not
//     history, so it needs no table and no migration, and it is gone when the
//     gateway restarts (the next report is at most 10 seconds away).
//   - query.usage: every machine's summary.
//   - query.counts: how many sessions are online and recently offline.
//   - query.usage_top, query.session_text and query.session_enter: a request from one machine's
//     owner for the top processes, the conversation or the way to enter a session on another
//     machine. The gateway only relays it to the machine that holds the
//     session and passes the answer back. It does not store, log or count the
//     content, and writes no event.

const (
	maxUsageTrees       = 500
	maxUsageSessions    = 500
	usageKeepFor        = 10 * time.Minute
	relayUsageTopWait   = 20 * time.Second
	relaySessionTextMax = 30 * time.Second
)

type usageEntry struct {
	at  time.Time
	rep protocol.UsageReport
}

// usageTable holds the newest report per node.
type usageTable struct {
	mu sync.Mutex
	m  map[string]usageEntry
}

func (u *usageTable) set(nodeID string, at time.Time, rep protocol.UsageReport) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.m == nil {
		u.m = map[string]usageEntry{}
	}
	u.m[nodeID] = usageEntry{at, rep}
}

func (u *usageTable) all() map[string]usageEntry {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make(map[string]usageEntry, len(u.m))
	for k, v := range u.m {
		out[k] = v
	}
	return out
}

// nodeQuotas returns the newest quota reading of a machine, or nil when it
// reported none, or the report is stale.
func (g *Gateway) nodeQuotas(nodeID string) *protocol.Quotas {
	e, ok := g.usage.all()[nodeID]
	if !ok || g.store.now().Sub(e.at) > protocol.UsageStaleSeconds*time.Second {
		return nil
	}
	return e.rep.Quotas
}

// cleanUsage bounds what a node may send: sizes and values.
func cleanUsage(r protocol.UsageReport) protocol.UsageReport {
	pct := func(v float64) float64 {
		if v != v || v < 0 {
			return 0
		}
		return v
	}
	m := &r.Machine
	m.CPU, m.CPUAvg = min(pct(m.CPU), 100), min(pct(m.CPUAvg), 100)
	for i := range m.Load {
		m.Load[i] = pct(m.Load[i])
	}
	if len(r.Trees) > maxUsageTrees {
		r.Trees = r.Trees[:maxUsageTrees]
	}
	for i := range r.Trees {
		t := &r.Trees[i]
		t.CPU, t.CPUAvg = pct(t.CPU), pct(t.CPUAvg)
		if len(t.Sessions) > maxUsageSessions {
			t.Sessions = t.Sessions[:maxUsageSessions]
		}
		if len(t.Harness) > 20 {
			t.Harness = t.Harness[:20]
		}
	}
	if r.Trees == nil {
		r.Trees = []protocol.TreeUsage{}
	}
	if w := r.Workers; w != nil {
		cp := *w
		cp.Running, cp.Queued = max(cp.Running, 0), max(cp.Queued, 0)
		switch cp.Room {
		case protocol.RoomOK, protocol.RoomCPU, protocol.RoomMem, protocol.RoomCap, protocol.RoomGap, protocol.RoomMeasuring:
		default:
			cp.Room = ""
		}
		if cp.Headroom != cp.Headroom || cp.Headroom > 1 {
			cp.Headroom = -1
		}
		if len(cp.Reason) > 300 {
			cp.Reason = cp.Reason[:300]
		}
		if len(cp.Queue) > 50 {
			cp.Queue = cp.Queue[:50]
		}
		cp.Queue = append([]protocol.QueuedWorker(nil), cp.Queue...)
		for i := range cp.Queue {
			q := &cp.Queue[i]
			q.QueueID, q.Harness, q.Model, q.Owner, q.Project, q.Hostname, q.Reason =
				clip(q.QueueID, 40), clip(q.Harness, 20), clip(q.Model, 100), clip(q.Owner, 60), clip(q.Project, 100), clip(q.Hostname, 100), clip(q.Reason, 300)
		}
		r.Workers = &cp
	}
	r.QuotaError = clip(r.QuotaError, 300)
	r.QuotaNote = clip(r.QuotaNote, 300)
	r.Quotas = cleanQuotas(r.Quotas)
	return r
}

// cleanQuotas bounds a quota reading from a node: sizes, values and text.
func cleanQuotas(q *protocol.Quotas) *protocol.Quotas {
	if q == nil {
		return nil
	}
	cp := *q
	if len(cp.Providers) > 100 {
		cp.Providers = cp.Providers[:100]
	}
	cp.Providers = append([]protocol.QuotaProvider(nil), cp.Providers...)
	for i := range cp.Providers {
		p := &cp.Providers[i]
		p.Provider, p.Plan, p.Error = clip(p.Provider, 40), clip(p.Plan, 40), clip(p.Error, 300)
		if len(p.Windows) > 50 {
			p.Windows = p.Windows[:50]
		}
		p.Windows = append([]protocol.QuotaWindow(nil), p.Windows...)
		for j := range p.Windows {
			w := &p.Windows[j]
			w.ID, w.Label, w.Kind, w.Pace = clip(w.ID, 80), clip(w.Label, 80), clip(w.Kind, 30), clip(w.Pace, 20)
			if w.PercentUsed != w.PercentUsed || w.PercentUsed < 0 {
				w.PercentUsed = 0
			}
			if w.PercentUsed > 100 {
				w.PercentUsed = 100
			}
		}
	}
	return &cp
}

// handleUsage serves node.usage, query.usage and query.counts.
// ok is false for any other frame.
func (g *Gateway) handleUsage(nc *nodeConn, typ string, raw []byte) (data any, ok bool, err error) {
	switch typ {
	case "node.usage":
		rep, err := decode[protocol.UsageReport](raw)
		if err != nil {
			return nil, true, reject("bad node.usage")
		}
		g.usage.set(nc.id, g.store.now(), cleanUsage(rep))
		return nil, true, nil

	case "query.usage":
		nodes, err := g.store.Nodes()
		if err != nil {
			return nil, true, err
		}
		have := g.usage.all()
		now := g.store.now()
		out := protocol.UsageAnswer{Nodes: []protocol.UsageNode{}}
		for _, n := range nodes {
			e, ok := have[n.NodeID]
			if !ok || now.Sub(e.at) > usageKeepFor {
				continue
			}
			out.Nodes = append(out.Nodes, protocol.UsageNode{NodeID: n.NodeID, Hostname: n.Hostname,
				AgeSeconds: int64(now.Sub(e.at) / time.Second), Usage: e.rep})
		}
		sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].Hostname < out.Nodes[j].Hostname })
		return out, true, nil

	case "query.counts":
		c, err := g.store.SessionCounts(24 * time.Hour)
		return c, true, err
	}
	return nil, false, nil
}

// SessionCounts counts online sessions and those that went offline within the
// last `recent`. It is one query on the index (online, last_seen).
func (s *Store) SessionCounts(recent time.Duration) (protocol.SessionCounts, error) {
	var c protocol.SessionCounts
	cutoff := s.now().Add(-recent).UnixNano()
	err := s.db.QueryRow(`SELECT
		COALESCE(SUM(CASE WHEN online = 1 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN online = 0 AND last_seen >= ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN online = 0 THEN 1 ELSE 0 END), 0)
		FROM sessions WHERE harness != 'owner'`, cutoff).Scan(&c.Online, &c.Offline24h, &c.OfflineTotal)
	return c, err
}

// isRelay tells whether a frame is a request that waits for another node.
func isRelay(typ string) bool {
	return typ == "query.owner_message" || typ == "query.usage_top" || typ == "query.session_text" || typ == "query.session_enter"
}

// serveRelay answers a relayed request. It runs in its own goroutine because
// it waits for another node while this node's read loop must keep going.
func (g *Gateway) serveRelay(ctx context.Context, nc *nodeConn, h protocol.Header, raw []byte) {
	var data json.RawMessage
	var err error
	if h.Type == "query.owner_message" {
		data, err = g.relayMessageBody(ctx, raw)
	} else {
		data, err = g.relay(ctx, h.Type, raw)
	}
	if err != nil {
		var rj Rejection
		if !errors.As(err, &rj) {
			// Only the kind of request is logged: never the answer, and not the
			// other node's error text either.
			g.logLine("error", "node", nc.id, "where", h.Type, "err", "relay failed")
			err = errors.New("the other machine could not answer")
		}
	}
	var reply []byte
	var eerr error
	if err != nil {
		reply, eerr = protocol.EncodeResult(h.ID, nil, err)
	} else {
		reply, eerr = protocol.EncodeResult(h.ID, data, nil)
	}
	if eerr != nil {
		return
	}
	nc.write(ctx, reply)
}

func (g *Gateway) relay(ctx context.Context, typ string, raw []byte) (json.RawMessage, error) {
	var req struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(raw, &req) != nil || req.SessionID == "" {
		return nil, reject("bad %s", typ)
	}
	a, ok, err := g.store.Agent(req.SessionID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, reject("unknown agent %q", req.SessionID)
	}
	target := g.conn(a.NodeID)
	if !a.Online || target == nil {
		return nil, reject("that session is offline")
	}
	// Forward every field the requester sent except the frame header.
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, reject("bad %s", typ)
	}
	delete(fields, "type")
	delete(fields, "id")
	wait, out := relayUsageTopWait, "node.usage_top"
	switch typ {
	case "query.session_text":
		wait, out = relaySessionTextMax, "node.session_text"
	case "query.session_enter":
		wait, out = relayUsageTopWait, "node.session_enter"
	}
	data, err := target.request(ctx, out, fields, wait)
	if err != nil {
		// The target's own refusals ("no such session", "not supported") are
		// meant for the owner; its text was made by agent-net code.
		return nil, reject("%s", err.Error())
	}
	return data, nil
}

// clip cuts s to at most n bytes.
func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// relayMessageBody asks a participant's daemon for the body. The gateway does
// not keep a copy of the relay response.
func (g *Gateway) relayMessageBody(ctx context.Context, raw []byte) (json.RawMessage, error) {
	var q protocol.QueryMessage
	if json.Unmarshal(raw, &q) != nil || q.MessageID == "" {
		return nil, reject("not found")
	}
	msg, ok, err := g.store.Message(q.MessageID)
	if err != nil {
		return nil, err
	}
	if !ok || (!msg.ExpiresAt.IsZero() && !msg.ExpiresAt.After(time.Now())) {
		return nil, reject("unknown message %q", q.MessageID)
	}
	var offline string
	connected := false
	seen := map[string]bool{}
	for _, sid := range []string{msg.From, msg.To} {
		a, found, e := g.store.Agent(sid)
		if e != nil {
			return nil, e
		}
		if !found || seen[a.NodeID] {
			continue
		}
		seen[a.NodeID] = true
		if offline == "" {
			offline = a.Hostname
			if offline == "" {
				offline = a.NodeID
			}
		}
		target := g.conn(a.NodeID)
		if target == nil {
			continue
		}
		connected = true
		data, e := target.request(ctx, "node.message_body", q, relayUsageTopWait)
		if e != nil {
			continue
		}
		var result protocol.MessageResult
		if json.Unmarshal(data, &result) != nil || result.Message.ID != q.MessageID || len(result.Message.Body) > protocol.MaxBodyBytes {
			continue
		}
		return data, nil
	}
	if offline != "" && !connected {
		return nil, reject("body held on %s, offline", offline)
	}
	return nil, reject("unknown message %q", q.MessageID)
}
