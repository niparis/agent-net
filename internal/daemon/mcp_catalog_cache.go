package daemon

import (
	"context"
	"encoding/json"
	"sync"
)

// mcpCatalogCache keeps the gateway's answer to mcp.catalog for each catalog
// key, so repeated tool-list reads cost the gateway nothing while the catalog
// is unchanged.
//
// A key is the session's project: sessions of one project see the same
// catalog, so they share one answer. A session with no project has a key of
// its own, because the gateway may still give it a project from its profile.
// The gateway tells the daemon when the catalog changes with mcp.changed, and
// a new link is a fresh start, so the daemon drops every kept answer then.
type mcpCatalogCache struct {
	mu      sync.Mutex
	gen     uint64
	entries map[string]mcpCatalogEntry
}

type mcpCatalogEntry struct {
	gen  uint64
	data json.RawMessage
}

// invalidate drops every kept answer. A read already in flight under an older
// generation does not store its answer.
func (c *mcpCatalogCache) invalidate() {
	c.mu.Lock()
	c.gen++
	c.entries = nil
	c.mu.Unlock()
}

// getOrLoad returns the kept answer for key, or loads one and keeps it. A
// failed load keeps nothing.
func (c *mcpCatalogCache) getOrLoad(ctx context.Context, key string, load func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	c.mu.Lock()
	gen := c.gen
	if e, ok := c.entries[key]; ok && e.gen == gen {
		c.mu.Unlock()
		return e.data, nil
	}
	c.mu.Unlock()
	data, err := load(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.gen == gen {
		if c.entries == nil {
			c.entries = map[string]mcpCatalogEntry{}
		}
		c.entries[key] = mcpCatalogEntry{gen: gen, data: data}
	}
	c.mu.Unlock()
	return data, nil
}
