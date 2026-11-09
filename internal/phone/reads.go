package phone

import (
	"context"
	"encoding/json"
	"time"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

const readWindow = 3 * time.Second

type cachedRead struct {
	ready chan struct{}
	raw   json.RawMessage
	err   error
	until time.Time
}

// read shares a short-lived read across screens, streams and browser tabs.
// In-flight reads share the same result too. Failed reads are retried.
func (a *app) read(ctx context.Context, method string, params any) (json.RawMessage, error) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	key := method + "|" + string(encoded)
	a.cacheMu.Lock()
	if a.cache == nil {
		a.cache = map[string]*cachedRead{}
	}
	if entry := a.cache[key]; entry != nil {
		select {
		case <-entry.ready:
			if entry.err == nil && time.Now().Before(entry.until) {
				a.cacheMu.Unlock()
				return entry.raw, nil
			}
		default:
			a.cacheMu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-entry.ready:
				return entry.raw, entry.err
			}
		}
	}
	// Bound retained thread/detail pages when browsing many rows.
	for k, e := range a.cache {
		select {
		case <-e.ready:
			if time.Now().After(e.until) {
				delete(a.cache, k)
			}
		default:
		}
	}
	if len(a.cache) >= 128 {
		a.cache = map[string]*cachedRead{}
	}
	entry := &cachedRead{ready: make(chan struct{})}
	a.cache[key] = entry
	a.cacheMu.Unlock()
	entry.raw, entry.err = a.src.Call(ctx, method, params)
	a.cacheMu.Lock()
	entry.until = time.Now().Add(readWindow)
	close(entry.ready)
	a.cacheMu.Unlock()
	return entry.raw, entry.err
}
func (a *app) clearReads() { a.cacheMu.Lock(); a.cache = nil; a.cacheMu.Unlock() }

// screenSnapshot composes only fields used by this screen. Header counts are
// separate so a Messages or Desk refresh never needs the machine settings.
func (a *app) screenSnapshot(ctx context.Context, screen string) (operator.Snapshot, error) {
	var snap operator.Snapshot
	raw, err := a.read(ctx, protocol.OpInfo, nil)
	if err != nil {
		return snap, err
	}
	var info protocol.OperatorInfo
	if err = json.Unmarshal(raw, &info); err != nil {
		return snap, err
	}
	snap.Connected, snap.NodeID, snap.OwnerSession = info.Connected, info.NodeID, info.OwnerSession
	if !snap.Connected {
		return snap, nil
	}
	raw, err = a.read(ctx, protocol.OpAgents, protocol.QueryAgents{IncludeOwners: true})
	if err != nil {
		return snap, err
	}
	var agents protocol.AgentsResult
	if err = json.Unmarshal(raw, &agents); err != nil {
		return snap, err
	}
	snap.Agents = agents.Agents
	if screen == "sessions" || screen == "workers" {
		if raw, err := a.read(ctx, protocol.OpNodes, nil); err == nil {
			var nodes protocol.NodesInfo
			if json.Unmarshal(raw, &nodes) == nil {
				snap.Nodes = nodes.Nodes
			}
		}
	}
	if screen == "workers" {
		for _, r := range []struct {
			method string
			params any
			out    any
		}{
			{protocol.OpWorkerProfiles, nil, &snap.WorkerProfiles},
			{protocol.OpScoresGet, nil, &snap.Scores},
			{protocol.OpWorkerRotationGet, nil, &snap.WorkerRotation},
			{protocol.OpProtectedProvidersGet, nil, &snap.ProtectedProviders},
		} {
			if raw, err := a.read(ctx, r.method, r.params); err == nil {
				_ = json.Unmarshal(raw, r.out)
			}
		}
	}
	return snap, nil
}
