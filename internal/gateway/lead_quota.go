package gateway

import (
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Lead quota. A lead whose model has no quota left cannot run, so lead routing
// does not hand it a request. The gateway tick reads the quota the picker reads
// and stores one verdict per pinned lead session. A transaction never reads
// quota data, so routing cannot wait on it (the chief's verdict works the same
// way, chief_health.go).

// leadQuotaVerdicts maps a pinned lead session that is out of quota to the Unix
// time its window resets, or 0 when unknown. A session with quota is not listed.
type leadQuotaVerdicts map[string]int64

// setLeadQuota replaces every stored verdict with the ones from one tick.
func (s *Store) setLeadQuota(v leadQuotaVerdicts) {
	s.leadQuota.Store(&v)
}

// leadQuotaOut says whether a lead session has no quota left, and when its
// window resets.
func (s *Store) leadQuotaOut(session string) (resetsAt int64, out bool) {
	v := s.leadQuota.Load()
	if v == nil {
		return 0, false
	}
	resetsAt, out = (*v)[session]
	return resetsAt, out
}

// refreshLeadQuota reads the quota of each pinned lead session's model, from
// the reading a worker start decides on (ownerQuotas), and stores the verdicts.
func (g *Gateway) refreshLeadQuota() {
	rows, err := g.store.db.Query(`SELECT s.id,s.node_id,s.harness,s.model FROM agents a JOIN sessions s ON s.id=a.lead_session
		WHERE a.lead_session<>'' AND s.archived_at=0`)
	if err != nil {
		g.logLine("error", "where", "lead quota", "err", err)
		return
	}
	type pinned struct{ id, node, harness, model string }
	var leads []pinned
	for rows.Next() {
		var p pinned
		if err := rows.Scan(&p.id, &p.node, &p.harness, &p.model); err != nil {
			rows.Close()
			g.logLine("error", "where", "lead quota", "err", err)
			return
		}
		leads = append(leads, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		g.logLine("error", "where", "lead quota", "err", err)
		return
	}
	// The rows are closed before the quota is read: that read queries the store.
	verdicts := leadQuotaVerdicts{}
	quotas := map[string]*protocol.Quotas{}
	for _, p := range leads {
		q, ok := quotas[p.node]
		if !ok {
			q = g.ownerQuotas(p.node)
			quotas[p.node] = q
		}
		if resetsAt, out := protocol.QuotaOutUntil(q, p.harness, p.model); out {
			verdicts[p.id] = resetsAt
		}
	}
	g.store.setLeadQuota(verdicts)
}

// leadNoQuotaNote is the sender's receipt when the lead has no quota left and
// the request is delivered as addressed.
func leadNoQuotaNote(resetsAt int64) string {
	if resetsAt <= 0 {
		return "Lead has no quota; delivered as addressed."
	}
	return fmt.Sprintf("Lead has no quota until %s; delivered as addressed.", time.Unix(resetsAt, 0).UTC().Format("2006-01-02 15:04 UTC"))
}
