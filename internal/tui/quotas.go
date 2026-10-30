package tui

import (
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

type quotaRow struct {
	provider protocol.QuotaProvider
	at       int64
	machine  string
	readers  int
}

// quotaRows merges readings for the same provider and plan. A local report
// replaces the gateway copy of that machine.
func (m Model) quotaRows() []quotaRow {
	if !m.usage.have {
		return nil
	}
	nodes := map[string]protocol.UsageNode{}
	if g := m.usage.raw.Gateway; g != nil {
		for _, n := range g.Nodes {
			nodes[n.NodeID] = n
		}
	}
	if l := m.usage.raw.Local; l != nil {
		nodes[m.usage.raw.NodeID] = protocol.UsageNode{NodeID: m.usage.raw.NodeID, Hostname: m.usage.raw.Hostname, Usage: *l}
	}
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rows := map[string]quotaRow{}
	for _, id := range ids {
		n := nodes[id]
		q := n.Usage.Quotas
		if q == nil {
			continue
		}
		for _, p := range q.Providers {
			key := strings.ToLower(strings.TrimSpace(p.Provider)) + "\x00" + strings.ToLower(strings.TrimSpace(p.Plan))
			r, ok := rows[key]
			r.readers++
			if !ok || q.GeneratedAt > r.at {
				r.provider = p
				r.at = q.GeneratedAt
				r.machine = firstNonEmpty(n.Hostname, id)
			}
			rows[key] = r
		}
	}
	counts := map[string]int{}
	for _, r := range rows {
		counts[strings.ToLower(r.provider.Provider)]++
	}
	out := make([]quotaRow, 0, len(rows))
	for _, r := range rows {
		if counts[strings.ToLower(r.provider.Provider)] == 1 && r.readers > 1 {
			r.machine = ""
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].provider.Provider != out[j].provider.Provider {
			return out[i].provider.Provider < out[j].provider.Provider
		}
		return out[i].provider.Plan < out[j].provider.Plan
	})
	return out
}
