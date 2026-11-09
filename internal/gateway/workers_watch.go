package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A Claude session's workers panel asks its daemon for the session's workers
// every few seconds. The daemon keeps the last gateway answer for each owner
// and asks again only when told the owner's rows changed. The agents topic
// cannot tell it: it moves for every session write on the network (about three
// a second), and the panel reads only one owner's rows.
//
// A node asks the gateway to watch a set of owners (workers.watch). The
// gateway reads the rows of those owners in one query each time the agents
// tables change, and pushes workers.changed for each owner whose rows are not
// what they were. The revision it numbers them with only goes up on one
// connection, so a daemon that keeps the highest one it saw never mistakes an
// older state for the current one.

// workersWatchRecheck is how often the rows are read even when no table the
// agents view reads has been written.
const workersWatchRecheck = 5 * time.Second

type watchedOwner struct {
	rev  uint64
	hash string
}

// workersWatch is the watch state of one node connection.
type workersWatch struct {
	mu      sync.Mutex
	owners  map[string]*watchedOwner
	seq     uint64
	started bool
}

// workerPanelHashes reads the rows a workers query for each owner reads, in
// one query, and returns a hash of what the daemon makes of them. The row
// order and the times the sessions were last seen are not in it.
func (s *Store) workerPanelHashes(owners []string) (map[string]string, error) {
	rows, err := s.agentsBaseOwned(agentListSelect, protocol.QueryAgents{IncludeOwners: true}, owners)
	if err != nil {
		return nil, err
	}
	want := make(map[string]bool, len(owners))
	for _, o := range owners {
		want[o] = true
	}
	type panelRow struct {
		ID, Node, Host, Harness, Model, Cwd, Status, Name string
		Online                                            bool
		Worker                                            *protocol.WorkerInfo
	}
	byOwner := make(map[string][]panelRow, len(owners))
	for _, a := range rows {
		r := panelRow{ID: a.SessionID, Node: a.NodeID, Host: a.Hostname, Harness: a.Harness, Model: a.Model, Cwd: a.Cwd,
			Status: a.Status, Name: a.Profile.Name, Online: a.Online, Worker: a.Worker}
		if want[a.SessionID] {
			byOwner[a.SessionID] = append(byOwner[a.SessionID], r)
		}
		if a.Worker != nil && a.Worker.Owner != a.SessionID && want[a.Worker.Owner] {
			byOwner[a.Worker.Owner] = append(byOwner[a.Worker.Owner], r)
		}
	}
	out := make(map[string]string, len(owners))
	for _, o := range owners {
		rs := byOwner[o]
		sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
		b, err := json.Marshal(rs)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		out[o] = hex.EncodeToString(sum[:])
	}
	return out, nil
}

// watchWorkers serves workers.watch.
func (g *Gateway) watchWorkers(ctx context.Context, nc *nodeConn, raw []byte) (any, error) {
	req, err := decode[protocol.WorkersWatch](raw)
	if err != nil {
		return nil, reject("bad workers.watch")
	}
	owners := dedupe(req.Owners)
	hashes := map[string]string{}
	if len(owners) > 0 {
		if hashes, err = g.store.workerPanelHashes(owners); err != nil {
			return nil, err
		}
	}
	w := &nc.workers
	w.mu.Lock()
	next := make(map[string]*watchedOwner, len(owners))
	revs := make(map[string]uint64, len(owners))
	for _, o := range owners {
		cur := w.owners[o]
		if cur == nil || cur.hash != hashes[o] {
			w.seq++
			cur = &watchedOwner{rev: w.seq, hash: hashes[o]}
		}
		next[o] = cur
		revs[o] = cur.rev
	}
	w.owners = next
	start := !w.started && len(next) > 0
	if start {
		w.started = true
	}
	w.mu.Unlock()
	if start {
		go g.watchWorkersLoop(ctx, nc)
	}
	return protocol.WorkersWatchResult{Revs: revs}, nil
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// watchWorkersLoop pushes the owners whose rows changed. It reads the rows when
// the agents tables moved, and now and then without that.
func (g *Gateway) watchWorkersLoop(ctx context.Context, nc *nodeConn) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	w := &nc.workers
	stamp := func() uint64 { return g.store.readStamp(agentsTables) + g.connVersion.Load() }
	last, lastRead := stamp(), time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-nc.dead:
			return
		case <-ticker.C:
		}
		now := stamp()
		if now == last && time.Since(lastRead) < workersWatchRecheck {
			continue
		}
		w.mu.Lock()
		owners := make([]string, 0, len(w.owners))
		for o := range w.owners {
			owners = append(owners, o)
		}
		w.mu.Unlock()
		if len(owners) == 0 {
			last, lastRead = now, time.Now()
			continue
		}
		sort.Strings(owners)
		hashes, err := g.store.workerPanelHashes(owners)
		if err != nil {
			continue // the stamp stays old, so the next tick reads again
		}
		last, lastRead = now, time.Now()
		var pushes []protocol.WorkersChanged
		w.mu.Lock()
		for _, o := range owners {
			cur := w.owners[o]
			if cur == nil || cur.hash == hashes[o] {
				continue
			}
			w.seq++
			cur.rev, cur.hash = w.seq, hashes[o]
			pushes = append(pushes, protocol.WorkersChanged{Owner: o, Rev: cur.rev})
		}
		w.mu.Unlock()
		for _, p := range pushes {
			frame, _ := json.Marshal(struct {
				Type string `json:"type"`
				protocol.WorkersChanged
			}{protocol.FrameWorkersChanged, p})
			if nc.write(ctx, frame) != nil {
				return
			}
		}
	}
}
