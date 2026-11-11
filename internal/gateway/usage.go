package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
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
	version      atomic.Uint64
	quotaVersion atomic.Uint64
	mu           sync.Mutex
	m            map[string]usageEntry
}

func (u *usageTable) set(nodeID string, at time.Time, rep protocol.UsageReport) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.m == nil {
		u.m = map[string]usageEntry{}
	}
	if previous, ok := u.m[nodeID]; !ok || !reflect.DeepEqual(previous.rep.Quotas, rep.Quotas) {
		u.quotaVersion.Add(1)
	}
	u.m[nodeID] = usageEntry{at, rep}
	u.version.Add(1)
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

// lastQuotas returns the newest quota reading of a machine however old it is,
// or nil. A block reads only the window ids of it.
func (g *Gateway) lastQuotas(nodeID string) *protocol.Quotas {
	if e, ok := g.usage.all()[nodeID]; ok {
		return e.rep.Quotas
	}
	return nil
}

// nodeQuotas returns the newest quota reading of a machine, or nil when it
// reported none, or the report is stale. The quota blocks that worker runs
// reported on the machine are added as exhausted windows until their resets.
func (g *Gateway) nodeQuotas(nodeID string) *protocol.Quotas {
	now := g.store.now()
	var q *protocol.Quotas
	if e, ok := g.usage.all()[nodeID]; ok && now.Sub(e.at) <= protocol.UsageStaleSeconds*time.Second {
		q = e.rep.Quotas
	}
	blocks, err := g.store.QuotaBlocks(nodeID)
	if err != nil {
		if g.logger != nil {
			g.logLine("quota.blocks_error", "node", nodeID, "error", err)
		}
		return q
	}
	return protocol.WithQuotaBlocks(q, blocks, now)
}

// ownerQuotas returns the one quota reading that a worker start decides on.
// The subscriptions belong to the owner, not to the machine that runs the
// worker, and quota-axi cannot read every login on every machine: on
// 2026-11-11 the opencode-go monthly window was readable only on the Mac while
// workers ran on research, so the start saw no reading and workers went into
// the limit. A provider this machine cannot read takes a usable reading from
// another fresh report, and a machine with no report at all falls back to the
// others. The machine's own usable reading wins for a provider, so two accounts
// of one provider never exclude each other.
//
// Build it once per start and pass it to every quota decision of that start:
// the provider allowlist, model resolution, the floor, the chooser's room and
// the exhaustion refusal.
func (g *Gateway) ownerQuotas(nodeID string) *protocol.Quotas {
	own := g.nodeQuotas(nodeID)
	var out protocol.Quotas
	chosen := map[string]protocol.QuotaProvider{}
	if own != nil {
		out.GeneratedAt, out.Copy = own.GeneratedAt, own.Copy
		for _, p := range own.Providers {
			chosen[p.Provider] = p
		}
	}
	now := g.store.now()
	for id, e := range g.usage.all() {
		if id == nodeID || now.Sub(e.at) > protocol.UsageStaleSeconds*time.Second || e.rep.Quotas == nil {
			continue
		}
		for _, p := range e.rep.Quotas.Providers {
			if !quotaReadingUsable(p) {
				continue
			}
			if existing, ok := chosen[p.Provider]; ok && quotaReadingUsable(existing) {
				continue
			}
			chosen[p.Provider] = p
		}
		// A machine with no reading of its own takes the newest generated time
		// of the reports it borrows, so the chooser can still measure pace.
		if own == nil && e.rep.Quotas.GeneratedAt > out.GeneratedAt {
			out.GeneratedAt = e.rep.Quotas.GeneratedAt
		}
	}
	if own == nil && len(chosen) == 0 {
		return nil
	}
	out.Providers = make([]protocol.QuotaProvider, 0, len(chosen))
	for _, p := range chosen {
		out.Providers = append(out.Providers, p)
	}
	sort.Slice(out.Providers, func(i, j int) bool { return out.Providers[i].Provider < out.Providers[j].Provider })
	return &out
}

// quotaReadingUsable tells whether a provider reading can decide a worker
// start. Every decision needs the same thing: a fresh entry with windows. A
// stale or failed reading decides nothing.
func quotaReadingUsable(p protocol.QuotaProvider) bool {
	return !p.Stale && p.Error == "" && len(p.Windows) > 0
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
	if r.QuotaCopy != nil {
		c := *r.QuotaCopy
		c.Version, c.Dir = clip(c.Version, 40), clip(c.Dir, 300)
		r.QuotaCopy = &c
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
	if q.Copy != nil {
		c := *q.Copy
		c.Version, c.Dir = clip(c.Version, 40), clip(c.Dir, 300)
		cp.Copy = &c
	}
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
		g.mu.Lock()
		c.MachinesOnline = len(g.conns)
		g.mu.Unlock()
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
		FROM (SELECT last_seen,
			CASE WHEN harness='bot' THEN
				CASE WHEN last_seen > ? AND EXISTS(SELECT 1 FROM apps WHERE apps.id=sessions.node_id AND revoked_at IS NULL) THEN 1 ELSE 0 END
			ELSE online END AS online
			FROM sessions WHERE archived_at=0 AND harness != 'owner')`, cutoff, s.now().Add(-10*time.Minute).UnixNano()).Scan(&c.Online, &c.Offline24h, &c.OfflineTotal)
	if err == nil {
		err = s.db.QueryRow(`SELECT COUNT(*) FROM sessions s LEFT JOIN nodes n ON n.id=s.node_id
   WHERE s.archived_at=0 AND s.harness <> 'owner' AND (n.id IS NOT NULL OR s.harness='bot') AND NOT (` + piChildSQL + `)`).Scan(&c.ConsoleSessions)
	}
	if err == nil {
		err = s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE status='open'
   AND (next_who='' OR next_who='owner')
   AND kind IN ('question','approval','step','waiting','held','rejection','escalation','proposal','draft','learned','check','session_stall')`).Scan(&c.ConsoleAsks)
	}
	s.pendingMu.Lock()
	c.PendingStalls = s.pendingStalls
	s.pendingMu.Unlock()
	if err == nil {
		err = s.reminderCounts(&c)
	}
	if err == nil {
		err = s.checkCounts(&c)
	}
	return c, err
}

// isRelay tells whether a frame is a request that waits for another node.
func isRelay(typ string) bool {
	return typ == "query.machine_log" || typ == "query.owner_message" || typ == "query.usage_top" || typ == "query.session_text" || typ == "query.session_enter" || typ == "query.proposal_list" || typ == "query.proposal_answer"
}

// serveRelay answers a relayed request. It runs in its own goroutine because
// it waits for another node while this node's read loop must keep going.
func (g *Gateway) serveRelay(ctx context.Context, nc *nodeConn, h protocol.Header, raw []byte) {
	start, sent := time.Now(), 0
	defer g.finishRequest(h, nc.id, start, &sent)
	var data json.RawMessage
	var err error
	if h.Type == "query.machine_log" {
		data, err = g.relayMachineLog(ctx, raw)
	} else if h.Type == "query.owner_message" {
		data, err = g.relayMessageBody(ctx, raw)
	} else if h.Type == "query.proposal_list" || h.Type == "query.proposal_answer" {
		var q protocol.CTOProposalAnswer
		if json.Unmarshal(raw, &q) != nil || q.NodeID == "" {
			err = reject("bad proposal node")
		} else if target := g.conn(q.NodeID); target == nil {
			err = reject("that machine is offline")
		} else {
			kind := "node.proposal_list"
			if h.Type == "query.proposal_answer" {
				kind = "node.proposal_answer"
			}
			data, err = target.request(ctx, kind, q, relaySessionTextMax)
			if err != nil {
				err = reject("%s", err.Error())
			}
		}
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
	sent = len(reply)
	nc.write(ctx, reply)
}

func (g *Gateway) relay(ctx context.Context, typ string, raw []byte) (json.RawMessage, error) {
	var req struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(raw, &req) != nil || req.SessionID == "" {
		return nil, reject("bad %s", typ)
	}
	// Only the node and the online flag are used: no delegation or charter.
	a, ok, err := g.store.agentBase(req.SessionID)
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
	delete(fields, "via") // the origin label is for the counters only
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
	if !ok || (!msg.ExpiresAt.IsZero() && !msg.ExpiresAt.After(g.store.now())) {
		return nil, reject("unknown message %q", q.MessageID)
	}
	var offline string
	connected := false
	seen := map[string]bool{}
	for _, sid := range []string{msg.From, msg.To} {
		a, found, e := g.store.agentBase(sid)
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
