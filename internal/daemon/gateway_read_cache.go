package daemon

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
)

// gatewayReadCache keeps the answers of gateway reads that a poll repeats. An
// answer is valid while the gateway has pushed no change to the topics it
// depends on, so a poll while nothing changes costs the gateway nothing.
type gatewayReadCache struct {
	mu      sync.Mutex
	entries map[string]gatewayRead
}

type gatewayRead struct {
	link     *link
	versions []uint64
	data     json.RawMessage
}

// cachedGatewayRead answers a gateway read from the last answer while the
// topics have the versions they had when that answer was read. The daemon
// follows the topics of its gateway link by itself (the push the TUI uses), so
// the answer does not depend on a TUI being open.
//
// It asks the gateway, and keeps no answer, when the daemon has no topic state
// for the link: no link, a gateway that does not push topics, or the first call
// after a reconnect that failed to subscribe. The versions are read before the
// gateway is asked, so a change that lands during the read leaves the answer
// out of date at once and the next call asks again.
func (d *Daemon) cachedGatewayRead(ctx context.Context, key, typ string, params any, topics ...string) (json.RawMessage, error) {
	d.mu.Lock()
	l := d.lk
	d.mu.Unlock()
	if l == nil {
		return d.call(ctx, typ, params)
	}
	if err := d.topics.ensure(ctx, l, requestTopicVersions(l)); err != nil {
		return d.call(ctx, typ, params)
	}
	versions, ok := d.topics.versionsOf(l, topics...)
	if !ok {
		return d.call(ctx, typ, params)
	}
	c := &d.readCache
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && e.link == l && slices.Equal(e.versions, versions) {
		c.mu.Unlock()
		return e.data, nil
	}
	c.mu.Unlock()
	data, err := d.call(ctx, typ, params)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]gatewayRead)
	}
	for k, e := range c.entries {
		if e.link != l {
			delete(c.entries, k)
		}
	}
	c.entries[key] = gatewayRead{link: l, versions: versions, data: data}
	c.mu.Unlock()
	return data, nil
}
