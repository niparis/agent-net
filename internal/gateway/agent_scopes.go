package gateway

import (
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// applyAgentScopes puts the scopes and the pinned lead session of each
// session's Agent on the rows of one agents read (decision 0166, decision
// 0160). It is one query for every Agent in the rows, not one per row: the
// session list is the hot read, and the scopes table is small.
func (s *Store) applyAgentScopes(rows []protocol.AgentView) error {
	var ids []string
	seen := map[string]bool{}
	for _, a := range rows {
		if a.AgentID != "" && !seen[a.AgentID] {
			seen[a.AgentID] = true
			ids = append(ids, a.AgentID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, len(ids))
	marks := make([]string, len(ids))
	for i, id := range ids {
		args[i] = id
		marks[i] = "?"
	}
	query := `SELECT a.id, COALESCE(a.lead_session,''), COALESCE(ns.kind,''), COALESCE(ns.over_ref,''),
		COALESCE(ns.note,''), COALESCE(ns.set_by,''), COALESCE(ns.set_at,0)
		FROM agents a LEFT JOIN node_scopes ns ON ns.node=a.id
		WHERE a.id IN (` + strings.Join(marks, ",") + `) ORDER BY a.id, ns.kind, ns.over_ref`
	res, err := s.db.Query(query, args...)
	if err != nil {
		return err
	}
	defer res.Close()
	type agentScopes struct {
		lead   string
		scopes []protocol.Scope
	}
	byAgent := map[string]*agentScopes{}
	for res.Next() {
		var id, lead string
		var sc protocol.Scope
		if err := res.Scan(&id, &lead, &sc.Kind, &sc.Over, &sc.Note, &sc.SetBy, &sc.SetAt); err != nil {
			return err
		}
		got := byAgent[id]
		if got == nil {
			got = &agentScopes{lead: lead}
			byAgent[id] = got
		}
		if sc.Kind != "" {
			sc.Node = id
			got.scopes = append(got.scopes, sc)
		}
	}
	if err := res.Err(); err != nil {
		return err
	}
	for i := range rows {
		got := byAgent[rows[i].AgentID]
		if got == nil {
			continue
		}
		rows[i].AgentLeadSession = got.lead
		rows[i].Scopes = got.scopes
	}
	return nil
}
